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
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/agentlab/agentlab/internal/db"
	"github.com/agentlab/agentlab/internal/models"
)

const (
	maxJSONBytes             = 1 << 20
	defaultSandboxVMIDStart  = 1000
	defaultJobRef            = "main"
	defaultJobMode           = "dangerous"
	maxCreateJobIDIterations = 5
)

// ControlAPI handles local control plane HTTP requests over the Unix socket.
type ControlAPI struct {
	store          *db.Store
	profiles       map[string]models.Profile
	sandboxManager *SandboxManager
	now            func() time.Time
}

func NewControlAPI(store *db.Store, profiles map[string]models.Profile, manager *SandboxManager) *ControlAPI {
	return &ControlAPI{
		store:          store,
		profiles:       profiles,
		sandboxManager: manager,
		now:            time.Now,
	}
}

func (api *ControlAPI) Register(mux *http.ServeMux) {
	if mux == nil {
		return
	}
	mux.HandleFunc("/v1/jobs", api.handleJobs)
	mux.HandleFunc("/v1/jobs/", api.handleJobByID)
	mux.HandleFunc("/v1/sandboxes", api.handleSandboxes)
	mux.HandleFunc("/v1/sandboxes/", api.handleSandboxByID)
}

func (api *ControlAPI) handleJobs(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		api.handleJobCreate(w, r)
	default:
		writeMethodNotAllowed(w, []string{http.MethodPost})
	}
}

func (api *ControlAPI) handleJobByID(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, []string{http.MethodGet})
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/v1/jobs/")
	id = strings.Trim(id, "/")
	if id == "" || strings.Contains(id, "/") {
		writeError(w, http.StatusNotFound, "job not found")
		return
	}
	job, err := api.store.GetJob(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "job not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load job")
		return
	}
	writeJSON(w, http.StatusOK, jobToV1(job))
}

func (api *ControlAPI) handleJobCreate(w http.ResponseWriter, r *http.Request) {
	var req V1JobCreateRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.RepoURL = strings.TrimSpace(req.RepoURL)
	req.Ref = strings.TrimSpace(req.Ref)
	req.Profile = strings.TrimSpace(req.Profile)
	req.Task = strings.TrimSpace(req.Task)
	req.Mode = strings.TrimSpace(req.Mode)

	if req.RepoURL == "" {
		writeError(w, http.StatusBadRequest, "repo_url is required")
		return
	}
	if req.Profile == "" {
		writeError(w, http.StatusBadRequest, "profile is required")
		return
	}
	if req.Task == "" {
		writeError(w, http.StatusBadRequest, "task is required")
		return
	}
	if req.Ref == "" {
		req.Ref = defaultJobRef
	}
	if req.Mode == "" {
		req.Mode = defaultJobMode
	}
	if req.TTLMinutes != nil && *req.TTLMinutes <= 0 {
		writeError(w, http.StatusBadRequest, "ttl_minutes must be positive")
		return
	}
	if !api.profileExists(req.Profile) {
		writeError(w, http.StatusBadRequest, "unknown profile")
		return
	}

	ctx := r.Context()
	var job models.Job
	var createErr error
	for i := 0; i < maxCreateJobIDIterations; i++ {
		jobID, err := newJobID()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to create job id")
			return
		}
		now := api.now().UTC()
		job = models.Job{
			ID:         jobID,
			RepoURL:    req.RepoURL,
			Ref:        req.Ref,
			Profile:    req.Profile,
			Task:       req.Task,
			Mode:       req.Mode,
			TTLMinutes: derefInt(req.TTLMinutes),
			Keepalive:  req.Keepalive,
			Status:     models.JobQueued,
			CreatedAt:  now,
			UpdatedAt:  now,
		}
		createErr = api.store.CreateJob(ctx, job)
		if createErr == nil {
			break
		}
		if !isUniqueConstraint(createErr) {
			break
		}
	}
	if createErr != nil {
		writeError(w, http.StatusInternalServerError, "failed to create job")
		return
	}
	writeJSON(w, http.StatusCreated, jobToV1(job))
}

func (api *ControlAPI) handleSandboxes(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		api.handleSandboxCreate(w, r)
	case http.MethodGet:
		api.handleSandboxList(w, r)
	default:
		writeMethodNotAllowed(w, []string{http.MethodGet, http.MethodPost})
	}
}

func (api *ControlAPI) handleSandboxByID(w http.ResponseWriter, r *http.Request) {
	tail := strings.TrimPrefix(r.URL.Path, "/v1/sandboxes/")
	parts := strings.Split(strings.Trim(tail, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		writeError(w, http.StatusNotFound, "sandbox not found")
		return
	}
	vmid, err := strconv.Atoi(parts[0])
	if err != nil || vmid <= 0 {
		writeError(w, http.StatusBadRequest, "invalid vmid")
		return
	}

	switch len(parts) {
	case 1:
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w, []string{http.MethodGet})
			return
		}
		api.handleSandboxGet(w, r, vmid)
		return
	case 2:
		if parts[1] == "destroy" {
			if r.Method != http.MethodPost {
				writeMethodNotAllowed(w, []string{http.MethodPost})
				return
			}
			api.handleSandboxDestroy(w, r, vmid)
			return
		}
	case 3:
		if parts[1] == "lease" && parts[2] == "renew" {
			if r.Method != http.MethodPost {
				writeMethodNotAllowed(w, []string{http.MethodPost})
				return
			}
			api.handleSandboxLeaseRenew(w, r, vmid)
			return
		}
	}

	writeError(w, http.StatusNotFound, "sandbox not found")
}

func (api *ControlAPI) handleSandboxList(w http.ResponseWriter, r *http.Request) {
	sandboxes, err := api.store.ListSandboxes(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list sandboxes")
		return
	}
	resp := V1SandboxesResponse{Sandboxes: make([]V1SandboxResponse, 0, len(sandboxes))}
	for _, sb := range sandboxes {
		resp.Sandboxes = append(resp.Sandboxes, sandboxToV1(sb))
	}
	writeJSON(w, http.StatusOK, resp)
}

func (api *ControlAPI) handleSandboxGet(w http.ResponseWriter, r *http.Request, vmid int) {
	sandbox, err := api.store.GetSandbox(r.Context(), vmid)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "sandbox not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load sandbox")
		return
	}
	writeJSON(w, http.StatusOK, sandboxToV1(sandbox))
}

func (api *ControlAPI) handleSandboxCreate(w http.ResponseWriter, r *http.Request) {
	var req V1SandboxCreateRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	req.Profile = strings.TrimSpace(req.Profile)
	req.JobID = strings.TrimSpace(req.JobID)
	if req.Profile == "" {
		writeError(w, http.StatusBadRequest, "profile is required")
		return
	}
	if !api.profileExists(req.Profile) {
		writeError(w, http.StatusBadRequest, "unknown profile")
		return
	}
	if req.TTLMinutes != nil && *req.TTLMinutes <= 0 {
		writeError(w, http.StatusBadRequest, "ttl_minutes must be positive")
		return
	}
	if req.VMID != nil && *req.VMID <= 0 {
		writeError(w, http.StatusBadRequest, "vmid must be positive")
		return
	}

	ctx := r.Context()
	if req.JobID != "" {
		if _, err := api.store.GetJob(ctx, req.JobID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				writeError(w, http.StatusNotFound, "job not found")
				return
			}
			writeError(w, http.StatusInternalServerError, "failed to load job")
			return
		}
	}

	vmid := 0
	if req.VMID != nil {
		vmid = *req.VMID
	}
	if vmid == 0 {
		next, err := api.nextSandboxVMID(ctx)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to allocate vmid")
			return
		}
		vmid = next
	}
	if req.Name == "" {
		req.Name = fmt.Sprintf("sandbox-%d", vmid)
	}

	now := api.now().UTC()
	var leaseExpires time.Time
	if req.TTLMinutes != nil && *req.TTLMinutes > 0 {
		leaseExpires = now.Add(time.Duration(*req.TTLMinutes) * time.Minute)
	}

	sandbox := models.Sandbox{
		VMID:          vmid,
		Name:          req.Name,
		Profile:       req.Profile,
		State:         models.SandboxRequested,
		Keepalive:     req.Keepalive,
		LeaseExpires:  leaseExpires,
		CreatedAt:     now,
		LastUpdatedAt: now,
	}
	if req.Workspace != nil && *req.Workspace != "" {
		workspace := *req.Workspace
		sandbox.WorkspaceID = &workspace
	}

	createdSandbox, err := api.createSandboxWithRetry(ctx, sandbox)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create sandbox")
		return
	}

	if req.JobID != "" {
		updated, err := api.store.UpdateJobSandbox(ctx, req.JobID, createdSandbox.VMID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to attach job")
			return
		}
		if !updated {
			writeError(w, http.StatusNotFound, "job not found")
			return
		}
	}

	writeJSON(w, http.StatusCreated, sandboxToV1(createdSandbox))
}

func (api *ControlAPI) handleSandboxDestroy(w http.ResponseWriter, r *http.Request, vmid int) {
	if api.sandboxManager == nil {
		writeError(w, http.StatusInternalServerError, "sandbox manager unavailable")
		return
	}
	if err := api.sandboxManager.Destroy(r.Context(), vmid); err != nil {
		switch {
		case errors.Is(err, ErrSandboxNotFound):
			writeError(w, http.StatusNotFound, "sandbox not found")
		case errors.Is(err, ErrInvalidTransition):
			writeError(w, http.StatusConflict, "invalid sandbox state")
		default:
			writeError(w, http.StatusInternalServerError, "failed to destroy sandbox")
		}
		return
	}
	sandbox, err := api.store.GetSandbox(r.Context(), vmid)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load sandbox")
		return
	}
	writeJSON(w, http.StatusOK, sandboxToV1(sandbox))
}

func (api *ControlAPI) handleSandboxLeaseRenew(w http.ResponseWriter, r *http.Request, vmid int) {
	if api.sandboxManager == nil {
		writeError(w, http.StatusInternalServerError, "sandbox manager unavailable")
		return
	}
	var req V1LeaseRenewRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.TTLMinutes <= 0 {
		writeError(w, http.StatusBadRequest, "ttl_minutes must be positive")
		return
	}
	lease, err := api.sandboxManager.RenewLease(r.Context(), vmid, time.Duration(req.TTLMinutes)*time.Minute)
	if err != nil {
		switch {
		case errors.Is(err, ErrSandboxNotFound):
			writeError(w, http.StatusNotFound, "sandbox not found")
		case errors.Is(err, ErrLeaseNotRenewable):
			writeError(w, http.StatusConflict, "sandbox lease not renewable")
		default:
			writeError(w, http.StatusInternalServerError, "failed to renew lease")
		}
		return
	}
	writeJSON(w, http.StatusOK, V1LeaseRenewResponse{
		VMID:         vmid,
		LeaseExpires: lease.UTC().Format(time.RFC3339Nano),
	})
}

func (api *ControlAPI) profileExists(name string) bool {
	if name == "" {
		return false
	}
	if api.profiles == nil {
		return true
	}
	_, ok := api.profiles[name]
	return ok
}

func (api *ControlAPI) nextSandboxVMID(ctx context.Context) (int, error) {
	maxVMID, err := api.store.MaxSandboxVMID(ctx)
	if err != nil {
		return 0, err
	}
	if maxVMID < defaultSandboxVMIDStart {
		return defaultSandboxVMIDStart, nil
	}
	return maxVMID + 1, nil
}

func (api *ControlAPI) createSandboxWithRetry(ctx context.Context, sandbox models.Sandbox) (models.Sandbox, error) {
	attempt := sandbox
	for i := 0; i < 5; i++ {
		err := api.store.CreateSandbox(ctx, attempt)
		if err == nil {
			return attempt, nil
		}
		if !isUniqueConstraint(err) {
			return models.Sandbox{}, err
		}
		attempt.VMID++
		if attempt.Name == sandbox.Name {
			attempt.Name = fmt.Sprintf("sandbox-%d", attempt.VMID)
		}
	}
	return models.Sandbox{}, errors.New("vmid allocation failed")
}

func jobToV1(job models.Job) V1JobResponse {
	resp := V1JobResponse{
		ID:        job.ID,
		RepoURL:   job.RepoURL,
		Ref:       job.Ref,
		Profile:   job.Profile,
		Task:      job.Task,
		Mode:      job.Mode,
		Keepalive: job.Keepalive,
		Status:    string(job.Status),
		CreatedAt: job.CreatedAt.UTC().Format(time.RFC3339Nano),
		UpdatedAt: job.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}
	if job.TTLMinutes > 0 {
		value := job.TTLMinutes
		resp.TTLMinutes = &value
	}
	if job.SandboxVMID != nil && *job.SandboxVMID > 0 {
		resp.SandboxVMID = job.SandboxVMID
	}
	return resp
}

func sandboxToV1(sb models.Sandbox) V1SandboxResponse {
	resp := V1SandboxResponse{
		VMID:          sb.VMID,
		Name:          sb.Name,
		Profile:       sb.Profile,
		State:         string(sb.State),
		IP:            sb.IP,
		WorkspaceID:   sb.WorkspaceID,
		Keepalive:     sb.Keepalive,
		CreatedAt:     sb.CreatedAt.UTC().Format(time.RFC3339Nano),
		LastUpdatedAt: sb.LastUpdatedAt.UTC().Format(time.RFC3339Nano),
	}
	if !sb.LeaseExpires.IsZero() {
		value := sb.LeaseExpires.UTC().Format(time.RFC3339Nano)
		resp.LeaseExpires = &value
	}
	return resp
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dest any) error {
	if r.Body == nil {
		return errors.New("request body is required")
	}
	defer r.Body.Close()
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dest); err != nil {
		return err
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return errors.New("unexpected trailing data")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, V1ErrorResponse{Error: msg})
}

func writeMethodNotAllowed(w http.ResponseWriter, methods []string) {
	w.Header().Set("Allow", strings.Join(methods, ", "))
	writeError(w, http.StatusMethodNotAllowed, "method not allowed")
}

func newJobID() (string, error) {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return "job_" + hex.EncodeToString(buf), nil
}

func derefInt(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}

func isUniqueConstraint(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "UNIQUE constraint failed")
}
