package daemon

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/agentlab/agentlab/internal/db"
	"github.com/agentlab/agentlab/internal/models"
	"github.com/agentlab/agentlab/internal/proxmox"
)

const (
	defaultBootstrapTTL     = 10 * time.Minute
	bootstrapTokenBytes     = 16
	defaultProvisionTimeout = 10 * time.Minute
)

var (
	ErrJobNotFound        = errors.New("job not found")
	ErrJobSandboxMismatch = errors.New("job sandbox mismatch")
	ErrJobFinalized       = errors.New("job already finalized")
)

type JobReport struct {
	JobID     string
	VMID      int
	Status    models.JobStatus
	Message   string
	Artifacts []V1ArtifactMetadata
	Result    json.RawMessage
}

type JobOrchestrator struct {
	store            *db.Store
	profiles         map[string]models.Profile
	backend          proxmox.Backend
	sandboxManager   *SandboxManager
	workspaceMgr     *WorkspaceManager
	snippetStore     proxmox.SnippetStore
	sshPublicKey     string
	controllerURL    string
	logger           *log.Logger
	redactor         *Redactor
	metrics          *Metrics
	now              func() time.Time
	rand             io.Reader
	bootstrapTTL     time.Duration
	provisionTimeout time.Duration
	snippetsMu       sync.Mutex
	snippets         map[int]proxmox.CloudInitSnippet
}

func NewJobOrchestrator(store *db.Store, profiles map[string]models.Profile, backend proxmox.Backend, manager *SandboxManager, workspaceMgr *WorkspaceManager, snippetStore proxmox.SnippetStore, sshPublicKey, controllerURL string, logger *log.Logger, redactor *Redactor, metrics *Metrics) *JobOrchestrator {
	if logger == nil {
		logger = log.Default()
	}
	if redactor == nil {
		redactor = NewRedactor(nil)
	}
	return &JobOrchestrator{
		store:            store,
		profiles:         profiles,
		backend:          backend,
		sandboxManager:   manager,
		workspaceMgr:     workspaceMgr,
		snippetStore:     snippetStore,
		sshPublicKey:     strings.TrimSpace(sshPublicKey),
		controllerURL:    strings.TrimSpace(controllerURL),
		logger:           logger,
		redactor:         redactor,
		metrics:          metrics,
		now:              time.Now,
		rand:             rand.Reader,
		bootstrapTTL:     defaultBootstrapTTL,
		provisionTimeout: defaultProvisionTimeout,
		snippets:         make(map[int]proxmox.CloudInitSnippet),
	}
}

func (o *JobOrchestrator) Start(jobID string) {
	if o == nil {
		return
	}
	go func() {
		if err := o.Run(context.Background(), jobID); err != nil && o.logger != nil {
			msg := err.Error()
			if o.redactor != nil {
				msg = o.redactor.Redact(msg)
			}
			o.logger.Printf("job orchestration %s: %s", jobID, msg)
		}
	}()
}

func (o *JobOrchestrator) Run(ctx context.Context, jobID string) error {
	if o == nil || o.store == nil {
		return errors.New("job orchestrator unavailable")
	}
	ctx, cancel := o.withProvisionTimeout(ctx)
	defer cancel()
	if jobID == "" {
		return errors.New("job id is required")
	}
	job, err := o.store.GetJob(ctx, jobID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrJobNotFound
		}
		return err
	}
	if job.Status != models.JobQueued {
		return nil
	}
	profile, ok := o.profile(job.Profile)
	if !ok {
		return o.failJob(ctx, job, 0, fmt.Errorf("unknown profile %q", job.Profile))
	}
	if err := validateProfileForProvisioning(profile); err != nil {
		return o.failJob(ctx, job, 0, err)
	}
	if o.sandboxManager == nil {
		return o.failJob(ctx, job, 0, errors.New("sandbox manager unavailable"))
	}
	if o.backend == nil {
		return o.failJob(ctx, job, 0, errors.New("proxmox backend unavailable"))
	}
	if o.sshPublicKey == "" {
		return o.failJob(ctx, job, 0, errors.New("ssh public key unavailable"))
	}
	if o.controllerURL == "" {
		return o.failJob(ctx, job, 0, errors.New("controller URL unavailable"))
	}

	sandbox, created, err := o.ensureSandbox(ctx, job)
	if err != nil {
		return o.failJob(ctx, job, 0, err)
	}
	if created {
		if _, err := o.store.UpdateJobSandbox(ctx, job.ID, sandbox.VMID); err != nil {
			return o.failJob(ctx, job, sandbox.VMID, err)
		}
	}

	if err := o.sandboxManager.Transition(ctx, sandbox.VMID, models.SandboxProvisioning); err != nil {
		return o.failJob(ctx, job, sandbox.VMID, err)
	}

	if err := o.backend.Clone(ctx, proxmox.VMID(profile.TemplateVM), proxmox.VMID(sandbox.VMID), sandbox.Name); err != nil {
		return o.failJob(ctx, job, sandbox.VMID, err)
	}

	token, tokenHash, expiresAt, err := o.bootstrapToken()
	if err != nil {
		return o.failJob(ctx, job, sandbox.VMID, err)
	}
	if err := o.store.CreateBootstrapToken(ctx, tokenHash, sandbox.VMID, expiresAt); err != nil {
		return o.failJob(ctx, job, sandbox.VMID, err)
	}

	snippet, err := o.snippetStore.Create(proxmox.SnippetInput{
		VMID:           proxmox.VMID(sandbox.VMID),
		Hostname:       sandbox.Name,
		SSHPublicKey:   o.sshPublicKey,
		BootstrapToken: token,
		ControllerURL:  o.controllerURL,
	})
	if err != nil {
		return o.failJob(ctx, job, sandbox.VMID, err)
	}
	o.rememberSnippet(snippet)

	cfg := proxmox.VMConfig{
		Name:      sandbox.Name,
		CloudInit: snippet.StoragePath,
	}
	cfg, err = applyProfileVMConfig(profile, cfg)
	if err != nil {
		o.cleanupSnippet(sandbox.VMID)
		return o.failJob(ctx, job, sandbox.VMID, err)
	}
	if err := o.backend.Configure(ctx, proxmox.VMID(sandbox.VMID), cfg); err != nil {
		o.cleanupSnippet(sandbox.VMID)
		return o.failJob(ctx, job, sandbox.VMID, err)
	}

	if sandbox.WorkspaceID != nil && strings.TrimSpace(*sandbox.WorkspaceID) != "" {
		if o.workspaceMgr == nil {
			o.cleanupSnippet(sandbox.VMID)
			return o.failJob(ctx, job, sandbox.VMID, errors.New("workspace manager unavailable"))
		}
		if _, err := o.workspaceMgr.Attach(ctx, *sandbox.WorkspaceID, sandbox.VMID); err != nil {
			o.cleanupSnippet(sandbox.VMID)
			return o.failJob(ctx, job, sandbox.VMID, err)
		}
	}

	if err := o.sandboxManager.Transition(ctx, sandbox.VMID, models.SandboxBooting); err != nil {
		return o.failJob(ctx, job, sandbox.VMID, err)
	}
	if err := o.backend.Start(ctx, proxmox.VMID(sandbox.VMID)); err != nil {
		return o.failJob(ctx, job, sandbox.VMID, err)
	}

	ip, err := o.backend.GuestIP(ctx, proxmox.VMID(sandbox.VMID))
	if err != nil {
		return o.failJob(ctx, job, sandbox.VMID, err)
	}
	if ip != "" {
		if err := o.store.UpdateSandboxIP(ctx, sandbox.VMID, ip); err != nil {
			return o.failJob(ctx, job, sandbox.VMID, err)
		}
	}

	if err := o.sandboxManager.Transition(ctx, sandbox.VMID, models.SandboxReady); err != nil {
		return o.failJob(ctx, job, sandbox.VMID, err)
	}
	if err := o.sandboxManager.Transition(ctx, sandbox.VMID, models.SandboxRunning); err != nil {
		return o.failJob(ctx, job, sandbox.VMID, err)
	}
	if err := o.store.UpdateJobStatus(ctx, job.ID, models.JobRunning); err != nil {
		return o.failJob(ctx, job, sandbox.VMID, err)
	}
	if o.metrics != nil {
		o.metrics.IncJobStatus(models.JobRunning)
	}
	_ = o.store.RecordEvent(ctx, "job.running", &sandbox.VMID, &job.ID, "sandbox running", "")
	return nil
}

// ProvisionSandbox provisions a non-job sandbox end-to-end and returns the updated record.
func (o *JobOrchestrator) ProvisionSandbox(ctx context.Context, vmid int) (models.Sandbox, error) {
	if o == nil || o.store == nil {
		return models.Sandbox{}, errors.New("sandbox provisioner unavailable")
	}
	ctx, cancel := o.withProvisionTimeout(ctx)
	defer cancel()
	if vmid <= 0 {
		return models.Sandbox{}, errors.New("vmid must be positive")
	}
	sandbox, err := o.store.GetSandbox(ctx, vmid)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return models.Sandbox{}, ErrSandboxNotFound
		}
		return models.Sandbox{}, err
	}
	if sandbox.State == models.SandboxRunning {
		return sandbox, nil
	}
	if sandbox.State != models.SandboxRequested {
		return models.Sandbox{}, fmt.Errorf("sandbox %d in state %s cannot be provisioned", sandbox.VMID, sandbox.State)
	}

	profile, ok := o.profile(sandbox.Profile)
	if !ok {
		return models.Sandbox{}, fmt.Errorf("unknown profile %q", sandbox.Profile)
	}
	if err := validateProfileForProvisioning(profile); err != nil {
		return models.Sandbox{}, err
	}
	if o.sandboxManager == nil {
		return models.Sandbox{}, errors.New("sandbox manager unavailable")
	}
	if o.backend == nil {
		return models.Sandbox{}, errors.New("proxmox backend unavailable")
	}
	if o.sshPublicKey == "" {
		return models.Sandbox{}, errors.New("ssh public key unavailable")
	}
	if o.controllerURL == "" {
		return models.Sandbox{}, errors.New("controller URL unavailable")
	}

	var provisionErr error
	defer func() {
		if provisionErr == nil {
			return
		}
		_ = o.sandboxManager.Destroy(ctx, vmid)
	}()

	fail := func(err error) (models.Sandbox, error) {
		provisionErr = err
		return models.Sandbox{}, err
	}

	if err := o.sandboxManager.Transition(ctx, sandbox.VMID, models.SandboxProvisioning); err != nil {
		return fail(err)
	}
	if err := o.backend.Clone(ctx, proxmox.VMID(profile.TemplateVM), proxmox.VMID(sandbox.VMID), sandbox.Name); err != nil {
		return fail(err)
	}

	token, tokenHash, expiresAt, err := o.bootstrapToken()
	if err != nil {
		return fail(err)
	}
	if err := o.store.CreateBootstrapToken(ctx, tokenHash, sandbox.VMID, expiresAt); err != nil {
		return fail(err)
	}

	snippet, err := o.snippetStore.Create(proxmox.SnippetInput{
		VMID:           proxmox.VMID(sandbox.VMID),
		Hostname:       sandbox.Name,
		SSHPublicKey:   o.sshPublicKey,
		BootstrapToken: token,
		ControllerURL:  o.controllerURL,
	})
	if err != nil {
		return fail(err)
	}
	o.rememberSnippet(snippet)

	cfg := proxmox.VMConfig{
		Name:      sandbox.Name,
		CloudInit: snippet.StoragePath,
	}
	cfg, err = applyProfileVMConfig(profile, cfg)
	if err != nil {
		return fail(err)
	}
	if err := o.backend.Configure(ctx, proxmox.VMID(sandbox.VMID), cfg); err != nil {
		return fail(err)
	}

	if sandbox.WorkspaceID != nil && strings.TrimSpace(*sandbox.WorkspaceID) != "" {
		if o.workspaceMgr == nil {
			return fail(errors.New("workspace manager unavailable"))
		}
		if _, err := o.workspaceMgr.Attach(ctx, *sandbox.WorkspaceID, sandbox.VMID); err != nil {
			return fail(err)
		}
	}

	if err := o.sandboxManager.Transition(ctx, sandbox.VMID, models.SandboxBooting); err != nil {
		return fail(err)
	}
	if err := o.backend.Start(ctx, proxmox.VMID(sandbox.VMID)); err != nil {
		return fail(err)
	}

	ip, err := o.backend.GuestIP(ctx, proxmox.VMID(sandbox.VMID))
	if err != nil {
		return fail(err)
	}
	if ip != "" {
		if err := o.store.UpdateSandboxIP(ctx, sandbox.VMID, ip); err != nil {
			return fail(err)
		}
	}

	if err := o.sandboxManager.Transition(ctx, sandbox.VMID, models.SandboxReady); err != nil {
		return fail(err)
	}
	if err := o.sandboxManager.Transition(ctx, sandbox.VMID, models.SandboxRunning); err != nil {
		return fail(err)
	}

	updated, loadErr := o.store.GetSandbox(ctx, sandbox.VMID)
	if loadErr != nil {
		updated = sandbox
		updated.State = models.SandboxRunning
		if ip != "" {
			updated.IP = ip
		}
	}
	return updated, nil
}

func (o *JobOrchestrator) HandleReport(ctx context.Context, report JobReport) error {
	if o == nil || o.store == nil {
		return errors.New("job orchestrator unavailable")
	}
	if report.JobID == "" {
		return errors.New("job id is required")
	}
	if report.VMID <= 0 {
		return errors.New("vmid must be positive")
	}
	if report.Status == "" {
		return errors.New("status is required")
	}
	if o.redactor != nil {
		report.Message = o.redactor.Redact(strings.TrimSpace(report.Message))
	} else {
		report.Message = strings.TrimSpace(report.Message)
	}

	job, err := o.store.GetJob(ctx, report.JobID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrJobNotFound
		}
		return err
	}
	if job.Status == models.JobCompleted || job.Status == models.JobFailed || job.Status == models.JobTimeout {
		return ErrJobFinalized
	}
	if job.SandboxVMID == nil || *job.SandboxVMID != report.VMID {
		return ErrJobSandboxMismatch
	}

	if o.metrics != nil && job.Status != report.Status {
		o.metrics.IncJobStatus(report.Status)
		if report.Status == models.JobCompleted || report.Status == models.JobFailed || report.Status == models.JobTimeout {
			if !job.CreatedAt.IsZero() {
				o.metrics.ObserveJobDuration(report.Status, o.now().UTC().Sub(job.CreatedAt))
			}
		}
	}

	resultJSON, err := buildJobResult(report.Status, report.Message, report.Artifacts, report.Result, o.now().UTC())
	if err != nil {
		return err
	}
	if err := o.store.UpdateJobResult(ctx, job.ID, report.Status, resultJSON); err != nil {
		return err
	}
	_ = o.store.RecordEvent(ctx, "job.report", &report.VMID, &job.ID, report.Message, "")

	if report.Status == models.JobRunning {
		_ = o.ensureSandboxRunning(ctx, report.VMID)
		return nil
	}

	if report.Status == models.JobCompleted || report.Status == models.JobFailed || report.Status == models.JobTimeout {
		_ = o.ensureSandboxRunning(ctx, report.VMID)
		if o.sandboxManager != nil {
			target := sandboxStateForJobStatus(report.Status)
			if target != "" {
				_ = o.sandboxManager.Transition(ctx, report.VMID, target)
			}
		}
		if !job.Keepalive && o.sandboxManager != nil {
			_ = o.sandboxManager.Destroy(ctx, report.VMID)
			o.cleanupSnippet(report.VMID)
		}
		return nil
	}

	return nil
}

func (o *JobOrchestrator) ensureSandboxRunning(ctx context.Context, vmid int) error {
	if o.sandboxManager == nil {
		return errors.New("sandbox manager unavailable")
	}
	for {
		sandbox, err := o.store.GetSandbox(ctx, vmid)
		if err != nil {
			return err
		}
		if sandbox.State == models.SandboxRunning {
			return nil
		}
		next, ok := nextSandboxStateTowardRunning(sandbox.State)
		if !ok {
			return ErrInvalidTransition
		}
		if err := o.sandboxManager.Transition(ctx, vmid, next); err != nil {
			if errors.Is(err, ErrInvalidTransition) {
				continue
			}
			return err
		}
	}
}

func (o *JobOrchestrator) ensureSandbox(ctx context.Context, job models.Job) (models.Sandbox, bool, error) {
	if job.SandboxVMID != nil && *job.SandboxVMID > 0 {
		sandbox, err := o.store.GetSandbox(ctx, *job.SandboxVMID)
		if err == nil {
			return sandbox, false, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return models.Sandbox{}, false, err
		}
	}

	vmid, err := nextSandboxVMID(ctx, o.store)
	if err != nil {
		return models.Sandbox{}, false, err
	}
	now := o.now().UTC()
	var leaseExpires time.Time
	if job.TTLMinutes > 0 {
		leaseExpires = now.Add(time.Duration(job.TTLMinutes) * time.Minute)
	}
	sandbox := models.Sandbox{
		VMID:          vmid,
		Name:          fmt.Sprintf("sandbox-%d", vmid),
		Profile:       job.Profile,
		State:         models.SandboxRequested,
		Keepalive:     job.Keepalive,
		LeaseExpires:  leaseExpires,
		CreatedAt:     now,
		LastUpdatedAt: now,
	}
	created, err := createSandboxWithRetry(ctx, o.store, sandbox)
	if err != nil {
		return models.Sandbox{}, false, err
	}
	return created, true, nil
}

func (o *JobOrchestrator) failJob(ctx context.Context, job models.Job, vmid int, cause error) error {
	if cause == nil {
		return nil
	}
	message := cause.Error()
	if o.redactor != nil {
		message = o.redactor.Redact(message)
	}
	if o.metrics != nil {
		o.metrics.IncJobStatus(models.JobFailed)
		if !job.CreatedAt.IsZero() {
			o.metrics.ObserveJobDuration(models.JobFailed, o.now().UTC().Sub(job.CreatedAt))
		}
	}
	resultJSON, _ := buildJobResult(models.JobFailed, message, nil, nil, o.now().UTC())
	_ = o.store.UpdateJobResult(ctx, job.ID, models.JobFailed, resultJSON)
	_ = o.store.RecordEvent(ctx, "job.failed", nullableVMID(vmid), &job.ID, message, "")
	if vmid > 0 && !job.Keepalive && o.sandboxManager != nil {
		_ = o.sandboxManager.Destroy(ctx, vmid)
		o.cleanupSnippet(vmid)
	}
	return cause
}

func (o *JobOrchestrator) profile(name string) (models.Profile, bool) {
	if name == "" {
		return models.Profile{}, false
	}
	if o.profiles == nil {
		return models.Profile{}, false
	}
	profile, ok := o.profiles[name]
	return profile, ok
}

func (o *JobOrchestrator) bootstrapToken() (string, string, time.Time, error) {
	buf := make([]byte, bootstrapTokenBytes)
	if _, err := io.ReadFull(o.randReader(), buf); err != nil {
		return "", "", time.Time{}, err
	}
	token := hex.EncodeToString(buf)
	if o.redactor != nil {
		o.redactor.AddValues(token)
	}
	hash, err := db.HashBootstrapToken(token)
	if err != nil {
		return "", "", time.Time{}, err
	}
	expires := o.now().UTC().Add(o.bootstrapTTL)
	return token, hash, expires, nil
}

func (o *JobOrchestrator) rememberSnippet(snippet proxmox.CloudInitSnippet) {
	if snippet.VMID <= 0 {
		return
	}
	o.snippetsMu.Lock()
	defer o.snippetsMu.Unlock()
	o.snippets[int(snippet.VMID)] = snippet
}

func (o *JobOrchestrator) cleanupSnippet(vmid int) {
	if vmid <= 0 {
		return
	}
	o.snippetsMu.Lock()
	snippet, ok := o.snippets[vmid]
	if ok {
		delete(o.snippets, vmid)
	}
	o.snippetsMu.Unlock()
	if ok {
		_ = o.snippetStore.Delete(snippet)
	}
}

// CleanupSnippet removes a remembered cloud-init snippet for a VMID.
func (o *JobOrchestrator) CleanupSnippet(vmid int) {
	if o == nil {
		return
	}
	o.cleanupSnippet(vmid)
}

func (o *JobOrchestrator) randReader() io.Reader {
	if o.rand != nil {
		return o.rand
	}
	return rand.Reader
}

// WithProvisionTimeout overrides the default provisioning timeout.
func (o *JobOrchestrator) WithProvisionTimeout(timeout time.Duration) *JobOrchestrator {
	if o == nil {
		return o
	}
	o.provisionTimeout = timeout
	return o
}

func (o *JobOrchestrator) withProvisionTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if o == nil || o.provisionTimeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, o.provisionTimeout)
}

func buildJobResult(status models.JobStatus, message string, artifacts []V1ArtifactMetadata, result json.RawMessage, now time.Time) (string, error) {
	payload := struct {
		Status     string               `json:"status"`
		Message    string               `json:"message,omitempty"`
		Artifacts  []V1ArtifactMetadata `json:"artifacts,omitempty"`
		Result     json.RawMessage      `json:"result,omitempty"`
		ReportedAt string               `json:"reported_at"`
	}{
		Status:     string(status),
		Message:    strings.TrimSpace(message),
		Artifacts:  artifacts,
		Result:     result,
		ReportedAt: now.UTC().Format(time.RFC3339Nano),
	}
	out, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func nextSandboxStateTowardRunning(current models.SandboxState) (models.SandboxState, bool) {
	switch current {
	case models.SandboxRequested:
		return models.SandboxProvisioning, true
	case models.SandboxProvisioning:
		return models.SandboxBooting, true
	case models.SandboxBooting:
		return models.SandboxReady, true
	case models.SandboxReady:
		return models.SandboxRunning, true
	case models.SandboxRunning:
		return models.SandboxRunning, true
	default:
		return "", false
	}
}

func sandboxStateForJobStatus(status models.JobStatus) models.SandboxState {
	switch status {
	case models.JobCompleted:
		return models.SandboxCompleted
	case models.JobFailed:
		return models.SandboxFailed
	case models.JobTimeout:
		return models.SandboxTimeout
	default:
		return ""
	}
}

func nullableVMID(vmid int) *int {
	if vmid <= 0 {
		return nil
	}
	value := vmid
	return &value
}
