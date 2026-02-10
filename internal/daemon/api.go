package daemon

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/agentlab/agentlab/internal/buildinfo"
	"github.com/agentlab/agentlab/internal/db"
	"github.com/agentlab/agentlab/internal/models"
	"github.com/agentlab/agentlab/internal/proxmox"
)

const (
	maxJSONBytes              = 1 << 20 // Maximum size for JSON request bodies (1MB)
	defaultSandboxVMIDStart   = 1000    // Default starting VM ID for sandboxes
	defaultJobRef             = "main"  // Default git reference for jobs
	defaultJobMode            = "dangerous"
	maxCreateJobIDIterations  = 5    // Max retries for unique job ID generation
	defaultEventsLimit        = 200  // Default events returned per query
	defaultJobEventsTail      = 50   // Default events tailed for job details
	maxEventsLimit            = 1000 // Maximum events allowed per query
	defaultMessagesLimit      = 200  // Default messages returned per query
	maxMessagesLimit          = 1000 // Maximum messages allowed per query
	defaultStatusFailureLimit = 10   // Default recent failures returned in status
	defaultExposureState      = "requested"
	workspaceWaitPollInterval = 250 * time.Millisecond
)

var (
	statusSandboxStates = []models.SandboxState{
		models.SandboxRequested,
		models.SandboxProvisioning,
		models.SandboxBooting,
		models.SandboxReady,
		models.SandboxRunning,
		models.SandboxCompleted,
		models.SandboxFailed,
		models.SandboxTimeout,
		models.SandboxStopped,
		models.SandboxDestroyed,
	}
	statusJobStatuses = []models.JobStatus{
		models.JobQueued,
		models.JobRunning,
		models.JobCompleted,
		models.JobFailed,
		models.JobTimeout,
	}
	errWorkspaceWaitTimeout = errors.New("workspace wait timeout")
	messageScopeTypes       = map[string]struct{}{
		"job":       {},
		"workspace": {},
		"session":   {},
	}
)

// ControlAPI handles local control plane HTTP requests over the Unix socket.
//
// It provides the v1 API for managing jobs, sandboxes, and workspaces. The API
// is served over a Unix socket and is used by the agentlab CLI for local control.
//
// Endpoints:
//   - POST   /v1/jobs                 - Create a new job
//   - GET    /v1/jobs/{id}            - Get job details
//   - GET    /v1/jobs/{id}/artifacts  - List job artifacts
//   - GET    /v1/jobs/{id}/artifacts/download - Download job artifacts
//   - POST   /v1/jobs/{id}/doctor     - Create job doctor bundle
//   - GET    /v1/profiles             - List available profiles
//   - GET    /v1/status               - Control plane status summary
//   - POST   /v1/sandboxes            - Create a new sandbox
//   - GET    /v1/sandboxes            - List all sandboxes
//   - GET    /v1/sandboxes/{vmid}     - Get sandbox details
//   - POST   /v1/sandboxes/{vmid}/start     - Start a stopped sandbox
//   - POST   /v1/sandboxes/{vmid}/stop      - Stop a running sandbox
//   - POST   /v1/sandboxes/stop_all         - Stop all running sandboxes
//   - POST   /v1/sandboxes/{vmid}/touch     - Update sandbox last_used_at
//   - POST   /v1/sandboxes/{vmid}/revert    - Revert a sandbox to snapshot "clean"
//   - POST   /v1/sandboxes/{vmid}/destroy   - Destroy a sandbox
//   - POST   /v1/sandboxes/{vmid}/lease/renew - Renew sandbox lease
//   - GET    /v1/sandboxes/{vmid}/events - Get sandbox events
//   - POST   /v1/sandboxes/{vmid}/doctor - Create sandbox doctor bundle
//   - POST   /v1/messages             - Post a message to the messagebox
//   - GET    /v1/messages             - List messagebox entries by scope
//   - POST   /v1/sandboxes/prune      - Prune orphaned sandboxes
//   - POST   /v1/workspaces           - Create a workspace
//   - GET    /v1/workspaces           - List workspaces
//   - GET    /v1/workspaces/{id}      - Get workspace details
//   - GET    /v1/workspaces/{id}/check  - Check workspace consistency
//   - POST   /v1/workspaces/{id}/attach  - Attach workspace to VM
//   - POST   /v1/workspaces/{id}/detach  - Detach workspace from VM
//   - POST   /v1/workspaces/{id}/rebind   - Rebind workspace to new VM
//   - POST   /v1/workspaces/{id}/fork     - Fork workspace to a new volume
//   - GET    /v1/workspaces/{id}/snapshots - List workspace snapshots
//   - POST   /v1/workspaces/{id}/snapshots - Create workspace snapshot
//   - POST   /v1/workspaces/{id}/snapshots/{name}/restore - Restore workspace snapshot
//   - POST   /v1/sessions             - Create a session
//   - GET    /v1/sessions             - List sessions
//   - GET    /v1/sessions/{id}        - Get session details
//   - POST   /v1/sessions/{id}/resume - Resume a session (rebind workspace)
//   - POST   /v1/sessions/{id}/stop   - Stop a session (destroy sandbox)
//   - POST   /v1/sessions/{id}/fork   - Fork a session (new workspace required)
//   - POST   /v1/sessions/{id}/doctor - Create session doctor bundle
//   - POST   /v1/exposures            - Create a new exposure
//   - GET    /v1/exposures            - List exposures
//   - DELETE /v1/exposures/{name}     - Delete exposure
type ControlAPI struct {
	store             *db.Store
	profiles          map[string]models.Profile
	backend           proxmox.Backend
	sandboxManager    *SandboxManager
	workspaceMgr      *WorkspaceManager
	jobOrchestrator   *JobOrchestrator
	exposurePublisher ExposurePublisher
	artifactRoot      string
	metrics           *Metrics
	metricsEnabled    bool
	agentSubnet       string
	tailscaleStatus   func(context.Context) (string, error)
	logger            *log.Logger
	redactor          *Redactor
	now               func() time.Time
}

// NewControlAPI creates a new control API instance.
//
// Parameters:
//   - store: Database store for persistence
//   - profiles: Map of available profiles by name
//   - manager: Sandbox manager for lifecycle operations
//   - workspaceMgr: Workspace manager for volume operations (optional)
//   - orchestrator: Job orchestrator for job execution (optional)
//   - artifactRoot: Root directory for artifact storage
//   - logger: Logger for operational output (uses log.Default if nil)
//
// Returns a configured ControlAPI ready for registration.
func NewControlAPI(store *db.Store, profiles map[string]models.Profile, manager *SandboxManager, workspaceMgr *WorkspaceManager, orchestrator *JobOrchestrator, artifactRoot string, logger *log.Logger) *ControlAPI {
	if logger == nil {
		logger = log.Default()
	}
	return &ControlAPI{
		store:           store,
		profiles:        profiles,
		sandboxManager:  manager,
		workspaceMgr:    workspaceMgr,
		jobOrchestrator: orchestrator,
		artifactRoot:    strings.TrimSpace(artifactRoot),
		logger:          logger,
		now:             time.Now,
	}
}

// WithMetricsEnabled annotates the status response with metrics listener state.
func (api *ControlAPI) WithMetricsEnabled(enabled bool) *ControlAPI {
	if api == nil {
		return api
	}
	api.metricsEnabled = enabled
	return api
}

// WithBackend sets the Proxmox backend used for doctor bundles.
func (api *ControlAPI) WithBackend(backend proxmox.Backend) *ControlAPI {
	if api == nil {
		return api
	}
	api.backend = backend
	return api
}

// WithMetrics registers the metrics collector for recording workspace lease stats.
func (api *ControlAPI) WithMetrics(metrics *Metrics) *ControlAPI {
	if api == nil {
		return api
	}
	api.metrics = metrics
	return api
}

// WithAgentSubnet annotates host info responses with the configured agent subnet.
func (api *ControlAPI) WithAgentSubnet(subnet string) *ControlAPI {
	if api == nil {
		return api
	}
	api.agentSubnet = strings.TrimSpace(subnet)
	return api
}

// WithTailscaleStatus provides a lookup hook for MagicDNS discovery.
func (api *ControlAPI) WithTailscaleStatus(fn func(context.Context) (string, error)) *ControlAPI {
	if api == nil {
		return api
	}
	api.tailscaleStatus = fn
	return api
}

// WithRedactor sets the redactor used for doctor bundles.
func (api *ControlAPI) WithRedactor(redactor *Redactor) *ControlAPI {
	if api == nil {
		return api
	}
	api.redactor = redactor
	return api
}

// WithExposurePublisher wires the exposure publisher used for tailscale serve integration.
func (api *ControlAPI) WithExposurePublisher(publisher ExposurePublisher) *ControlAPI {
	if api == nil {
		return api
	}
	api.exposurePublisher = publisher
	return api
}

// Register registers all control API handlers with the provided mux.
//
// The mux will handle all v1 API endpoints. If mux is nil, this is a no-op.
//
// After registration, the following routes are available:
//   - /v1/jobs - Job management
//   - /v1/sandboxes - Sandbox management
//   - /v1/workspaces - Workspace management
func (api *ControlAPI) Register(mux *http.ServeMux) {
	if mux == nil {
		return
	}
	mux.HandleFunc("/v1/jobs", api.handleJobs)
	mux.HandleFunc("/v1/jobs/", api.handleJobByID)
	mux.HandleFunc("/v1/profiles", api.handleProfiles)
	mux.HandleFunc("/v1/status", api.handleStatus)
	mux.HandleFunc("/v1/host", api.handleHost)
	mux.HandleFunc("/v1/messages", api.handleMessages)
	mux.HandleFunc("/v1/sandboxes", api.handleSandboxes)
	mux.HandleFunc("/v1/sandboxes/", api.handleSandboxByID)
	mux.HandleFunc("/v1/sandboxes/stop_all", api.handleSandboxStopAll)
	mux.HandleFunc("/v1/sandboxes/prune", api.handleSandboxPrune)
	mux.HandleFunc("/v1/workspaces", api.handleWorkspaces)
	mux.HandleFunc("/v1/workspaces/", api.handleWorkspaceByID)
	mux.HandleFunc("/v1/sessions", api.handleSessions)
	mux.HandleFunc("/v1/sessions/", api.handleSessionByID)
	mux.HandleFunc("/v1/exposures", api.handleExposures)
	mux.HandleFunc("/v1/exposures/", api.handleExposureByName)
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
	tail := strings.TrimPrefix(r.URL.Path, "/v1/jobs/")
	parts := strings.Split(strings.Trim(tail, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		writeError(w, http.StatusNotFound, "job not found")
		return
	}
	jobID := parts[0]

	switch len(parts) {
	case 1:
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w, []string{http.MethodGet})
			return
		}
		api.handleJobGet(w, r, jobID)
		return
	case 2:
		if parts[1] == "artifacts" {
			if r.Method != http.MethodGet {
				writeMethodNotAllowed(w, []string{http.MethodGet})
				return
			}
			api.handleJobArtifactsList(w, r, jobID)
			return
		}
		if parts[1] == "doctor" {
			if r.Method != http.MethodPost {
				writeMethodNotAllowed(w, []string{http.MethodPost})
				return
			}
			api.handleJobDoctor(w, r, jobID)
			return
		}
	case 3:
		if parts[1] == "artifacts" && parts[2] == "download" {
			if r.Method != http.MethodGet {
				writeMethodNotAllowed(w, []string{http.MethodGet})
				return
			}
			api.handleJobArtifactDownload(w, r, jobID)
			return
		}
	}

	writeError(w, http.StatusNotFound, "job not found")
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
	if req.WorkspaceID != nil {
		value := strings.TrimSpace(*req.WorkspaceID)
		if value == "" {
			req.WorkspaceID = nil
		} else {
			req.WorkspaceID = &value
		}
	}
	if req.WorkspaceCreate != nil {
		req.WorkspaceCreate.Name = strings.TrimSpace(req.WorkspaceCreate.Name)
		req.WorkspaceCreate.Storage = strings.TrimSpace(req.WorkspaceCreate.Storage)
	}
	if req.SessionID != nil {
		value := strings.TrimSpace(*req.SessionID)
		if value == "" {
			req.SessionID = nil
		} else {
			req.SessionID = &value
		}
	}

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
	if req.WorkspaceID != nil && req.WorkspaceCreate != nil {
		writeError(w, http.StatusBadRequest, "workspace_id and workspace_create are mutually exclusive")
		return
	}
	if req.SessionID != nil && req.WorkspaceCreate != nil {
		writeError(w, http.StatusBadRequest, "session_id cannot be combined with workspace_create")
		return
	}
	ctx := r.Context()
	var resolvedSessionID *string
	if req.SessionID != nil {
		session, err := api.resolveSession(ctx, *req.SessionID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				writeError(w, http.StatusNotFound, "session not found")
				return
			}
			writeError(w, http.StatusInternalServerError, "failed to load session")
			return
		}
		workspaceID := strings.TrimSpace(session.WorkspaceID)
		if workspaceID == "" {
			writeError(w, http.StatusBadRequest, "session workspace_id is required")
			return
		}
		if req.WorkspaceID != nil && strings.TrimSpace(*req.WorkspaceID) != workspaceID {
			writeError(w, http.StatusBadRequest, "session workspace_id mismatch")
			return
		}
		if req.WorkspaceID == nil {
			req.WorkspaceID = &workspaceID
		}
		value := session.ID
		resolvedSessionID = &value
	}
	if req.WorkspaceWaitSeconds != nil && *req.WorkspaceWaitSeconds < 0 {
		writeError(w, http.StatusBadRequest, "workspace_wait_seconds must be non-negative")
		return
	}
	if req.WorkspaceWaitSeconds != nil && *req.WorkspaceWaitSeconds > 0 && req.WorkspaceID == nil && req.WorkspaceCreate == nil {
		writeError(w, http.StatusBadRequest, "workspace_wait_seconds requires workspace_id or workspace_create")
		return
	}
	if !api.profileExists(req.Profile) {
		writeError(w, http.StatusBadRequest, "unknown profile")
		return
	}
	var (
		ttlMinutes int
		keepalive  bool
	)
	if req.Keepalive != nil {
		keepalive = *req.Keepalive
	}
	if profile, ok := api.profile(req.Profile); ok {
		if err := validateProfileForProvisioning(profile); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		appliedTTL, appliedKeepalive, err := applyProfileBehaviorDefaults(profile, req.TTLMinutes, req.Keepalive)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid profile behavior defaults")
			return
		}
		ttlMinutes = appliedTTL
		keepalive = appliedKeepalive
	} else {
		ttlMinutes = derefInt(req.TTLMinutes)
	}

	var (
		workspaceID       *string
		workspace         models.Workspace
		workspaceWaitSec  int
		workspaceLeaseTTL time.Duration
	)
	if req.WorkspaceID != nil || req.WorkspaceCreate != nil {
		if api.workspaceMgr == nil {
			writeError(w, http.StatusInternalServerError, "workspace manager unavailable")
			return
		}
		if req.WorkspaceCreate != nil {
			if req.WorkspaceCreate.Name == "" {
				writeError(w, http.StatusBadRequest, "workspace_create.name is required")
				return
			}
			if req.WorkspaceCreate.SizeGB <= 0 {
				writeError(w, http.StatusBadRequest, "workspace_create.size_gb must be positive")
				return
			}
			created, err := api.workspaceMgr.Create(ctx, req.WorkspaceCreate.Name, req.WorkspaceCreate.Storage, req.WorkspaceCreate.SizeGB)
			if err != nil {
				if errors.Is(err, ErrWorkspaceExists) {
					writeError(w, http.StatusConflict, "workspace already exists")
					return
				}
				writeError(w, http.StatusInternalServerError, "failed to create workspace")
				return
			}
			workspace = created
		} else if req.WorkspaceID != nil {
			resolved, err := api.workspaceMgr.Resolve(ctx, *req.WorkspaceID)
			if err != nil {
				if errors.Is(err, ErrWorkspaceNotFound) {
					writeError(w, http.StatusNotFound, "workspace not found")
					return
				}
				writeError(w, http.StatusInternalServerError, "failed to load workspace")
				return
			}
			workspace = resolved
		}
		workspaceID = &workspace.ID
		workspaceWaitSec = derefInt(req.WorkspaceWaitSeconds)
		workspaceLeaseTTL = workspaceLeaseDuration(ttlMinutes)
	}
	var job models.Job
	var createErr error
	for i := 0; i < maxCreateJobIDIterations; i++ {
		jobID, err := newJobID()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to create job id")
			return
		}
		var leaseOwner string
		var leaseNonce string
		if workspaceID != nil {
			leaseOwner = workspaceLeaseOwnerForJobOrSession(jobID, resolvedSessionID)
			nonce, _, err := api.acquireWorkspaceLease(ctx, workspace, leaseOwner, workspaceLeaseTTL, workspaceWaitSec)
			if err != nil {
				switch {
				case errors.Is(err, ErrWorkspaceLeaseHeld), errors.Is(err, errWorkspaceWaitTimeout):
					latest, loadErr := api.workspaceMgr.Resolve(ctx, workspace.ID)
					if loadErr == nil {
						workspace = latest
					}
					writeError(w, http.StatusConflict, "workspace lease held", workspaceConflictDetails(workspace, workspaceWaitSec))
				case errors.Is(err, ErrWorkspaceNotFound):
					writeError(w, http.StatusNotFound, "workspace not found")
				default:
					writeError(w, http.StatusInternalServerError, "failed to acquire workspace lease")
				}
				return
			}
			leaseNonce = nonce
			latest, err := api.workspaceMgr.Resolve(ctx, workspace.ID)
			if err != nil {
				_, _ = api.store.ReleaseWorkspaceLease(ctx, workspace.ID, leaseOwner, leaseNonce)
				if errors.Is(err, ErrWorkspaceNotFound) {
					writeError(w, http.StatusNotFound, "workspace not found")
					return
				}
				writeError(w, http.StatusInternalServerError, "failed to load workspace")
				return
			}
			workspace = latest
			if workspace.AttachedVM != nil && *workspace.AttachedVM > 0 {
				_, _ = api.store.ReleaseWorkspaceLease(ctx, workspace.ID, leaseOwner, leaseNonce)
				recordWorkspaceLeaseEvent(ctx, api.store, "workspace.lease.released", nil, nil, workspace.ID, leaseOwner, time.Time{})
				writeError(w, http.StatusConflict, "workspace already attached", workspaceConflictDetails(workspace, workspaceWaitSec))
				return
			}
		}
		now := api.now().UTC()
		job = models.Job{
			ID:          jobID,
			RepoURL:     req.RepoURL,
			Ref:         req.Ref,
			Profile:     req.Profile,
			Task:        req.Task,
			Mode:        req.Mode,
			TTLMinutes:  ttlMinutes,
			Keepalive:   keepalive,
			WorkspaceID: workspaceID,
			SessionID:   resolvedSessionID,
			Status:      models.JobQueued,
			CreatedAt:   now,
			UpdatedAt:   now,
		}
		createErr = api.store.CreateJob(ctx, job)
		if createErr == nil {
			if workspaceID != nil && leaseNonce != "" {
				recordWorkspaceLeaseEvent(ctx, api.store, "workspace.lease.acquired", nil, &job.ID, workspace.ID, leaseOwner, workspace.LeaseExpires)
			}
			break
		}
		if workspaceID != nil && leaseNonce != "" {
			_, _ = api.store.ReleaseWorkspaceLease(ctx, *workspaceID, leaseOwner, leaseNonce)
			recordWorkspaceLeaseEvent(ctx, api.store, "workspace.lease.released", nil, nil, *workspaceID, leaseOwner, time.Time{})
		}
		if !isUniqueConstraint(createErr) {
			break
		}
	}
	if createErr != nil {
		writeError(w, http.StatusInternalServerError, "failed to create job")
		return
	}
	if api.jobOrchestrator == nil {
		_ = api.store.UpdateJobStatus(ctx, job.ID, models.JobFailed)
		writeError(w, http.StatusInternalServerError, "job orchestration unavailable")
		return
	}
	api.jobOrchestrator.Start(job.ID)
	writeJSON(w, http.StatusCreated, jobToV1(job))
}

func (api *ControlAPI) handleJobGet(w http.ResponseWriter, r *http.Request, jobID string) {
	jobID = strings.TrimSpace(jobID)
	if jobID == "" {
		writeError(w, http.StatusNotFound, "job not found")
		return
	}
	job, err := api.store.GetJob(r.Context(), jobID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "job not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load job")
		return
	}
	query := r.URL.Query()
	eventsTail := defaultJobEventsTail
	if raw := query.Get("events_tail"); strings.TrimSpace(raw) != "" {
		parsed, err := parseQueryInt(raw)
		if err != nil || parsed < 0 {
			writeError(w, http.StatusBadRequest, "invalid events_tail")
			return
		}
		eventsTail = parsed
	}
	if eventsTail > maxEventsLimit {
		eventsTail = maxEventsLimit
	}
	resp := jobToV1(job)
	if eventsTail > 0 {
		events, err := api.store.ListEventsByJobTail(r.Context(), job.ID, eventsTail)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load job events")
			return
		}
		resp.Events = make([]V1Event, 0, len(events))
		for _, ev := range events {
			resp.Events = append(resp.Events, eventToV1(ev))
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

func (api *ControlAPI) handleJobArtifactsList(w http.ResponseWriter, r *http.Request, jobID string) {
	jobID = strings.TrimSpace(jobID)
	if jobID == "" {
		writeError(w, http.StatusNotFound, "job not found")
		return
	}
	if _, err := api.store.GetJob(r.Context(), jobID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "job not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load job")
		return
	}
	artifacts, err := api.store.ListArtifactsByJob(r.Context(), jobID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list artifacts")
		return
	}
	resp := V1ArtifactsResponse{
		JobID:     jobID,
		Artifacts: make([]V1Artifact, 0, len(artifacts)),
	}
	for _, artifact := range artifacts {
		resp.Artifacts = append(resp.Artifacts, artifactToV1(artifact))
	}
	writeJSON(w, http.StatusOK, resp)
}

func (api *ControlAPI) handleJobArtifactDownload(w http.ResponseWriter, r *http.Request, jobID string) {
	jobID = strings.TrimSpace(jobID)
	if jobID == "" {
		writeError(w, http.StatusNotFound, "job not found")
		return
	}
	if _, err := api.store.GetJob(r.Context(), jobID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "job not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load job")
		return
	}
	root := strings.TrimSpace(api.artifactRoot)
	if root == "" {
		writeError(w, http.StatusInternalServerError, "artifact root is not configured")
		return
	}
	artifacts, err := api.store.ListArtifactsByJob(r.Context(), jobID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list artifacts")
		return
	}
	if len(artifacts) == 0 {
		writeError(w, http.StatusNotFound, "no artifacts found")
		return
	}

	query := r.URL.Query()
	pathParam := strings.TrimSpace(query.Get("path"))
	nameParam := strings.TrimSpace(query.Get("name"))
	if pathParam != "" && nameParam != "" {
		writeError(w, http.StatusBadRequest, "path and name are mutually exclusive")
		return
	}
	var selected *db.Artifact
	if pathParam != "" {
		cleaned, err := sanitizeArtifactPath(pathParam)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		for i := range artifacts {
			if artifacts[i].Path == cleaned {
				selected = &artifacts[i]
				break
			}
		}
	} else if nameParam != "" {
		if err := validateArtifactName(nameParam); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		for i := range artifacts {
			if artifacts[i].Name == nameParam {
				selected = &artifacts[i]
			}
		}
	} else {
		selected = &artifacts[len(artifacts)-1]
	}

	if selected == nil {
		writeError(w, http.StatusNotFound, "artifact not found")
		return
	}
	jobDir := filepath.Join(root, jobID)
	targetPath, err := safeJoin(jobDir, selected.Path)
	if err != nil {
		writeError(w, http.StatusBadRequest, "artifact path is invalid")
		return
	}
	file, err := os.Open(targetPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeError(w, http.StatusNotFound, "artifact file missing")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to open artifact")
		return
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to stat artifact")
		return
	}
	contentType := strings.TrimSpace(selected.MIME)
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	filename := filepath.Base(selected.Name)
	if filename == "." || filename == string(filepath.Separator) || filename == "" {
		filename = filepath.Base(selected.Path)
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", strconv.FormatInt(stat.Size(), 10))
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, file)
}

func (api *ControlAPI) handleProfiles(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, []string{http.MethodGet})
		return
	}
	resp := V1ProfilesResponse{Profiles: []V1Profile{}}
	if api.profiles == nil {
		writeJSON(w, http.StatusOK, resp)
		return
	}
	names := make([]string, 0, len(api.profiles))
	for name := range api.profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	resp.Profiles = make([]V1Profile, 0, len(names))
	for _, name := range names {
		profile, ok := api.profiles[name]
		if !ok {
			continue
		}
		resp.Profiles = append(resp.Profiles, profileToV1(profile))
	}
	writeJSON(w, http.StatusOK, resp)
}

func (api *ControlAPI) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, []string{http.MethodGet})
		return
	}
	ctx := r.Context()
	sandboxCounts, err := api.store.CountSandboxesByState(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to count sandboxes")
		return
	}
	jobCounts, err := api.store.CountJobsByStatus(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to count jobs")
		return
	}
	networkModes := api.buildNetworkModeCounts(ctx)
	failureEvents, err := api.store.ListRecentFailureEvents(ctx, defaultStatusFailureLimit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load recent failures")
		return
	}

	resp := V1StatusResponse{
		Sandboxes:      formatSandboxCounts(sandboxCounts),
		Jobs:           formatJobCounts(jobCounts),
		NetworkModes:   formatNetworkModeCounts(networkModes),
		Artifacts:      buildArtifactStatus(api.artifactRoot),
		Metrics:        V1StatusMetrics{Enabled: api.metricsEnabled},
		RecentFailures: make([]V1Event, 0, len(failureEvents)),
	}
	for _, ev := range failureEvents {
		resp.RecentFailures = append(resp.RecentFailures, eventToV1(ev))
	}
	writeJSON(w, http.StatusOK, resp)
}

func (api *ControlAPI) handleHost(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, []string{http.MethodGet})
		return
	}
	resp := V1HostResponse{
		Version:     buildinfo.Version,
		AgentSubnet: strings.TrimSpace(api.agentSubnet),
	}
	if api.tailscaleStatus != nil {
		if dns, err := api.tailscaleStatus(r.Context()); err == nil {
			dns = strings.TrimSpace(dns)
			dns = strings.TrimSuffix(dns, ".")
			if dns != "" {
				resp.TailscaleDNS = dns
			}
		} else if api.logger != nil {
			api.logger.Printf("host info: tailscale status unavailable: %v", err)
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

func (api *ControlAPI) handleMessages(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		api.handleMessageCreate(w, r)
	case http.MethodGet:
		api.handleMessageList(w, r)
	default:
		writeMethodNotAllowed(w, []string{http.MethodGet, http.MethodPost})
	}
}

func (api *ControlAPI) handleMessageCreate(w http.ResponseWriter, r *http.Request) {
	if api.store == nil {
		writeError(w, http.StatusInternalServerError, "message store unavailable")
		return
	}
	var req V1MessageCreateRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}
	scopeType, scopeID, err := parseMessageScope(req.ScopeType, req.ScopeID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	text := strings.TrimSpace(req.Text)
	payload := bytes.TrimSpace(req.Payload)
	if text == "" && len(payload) == 0 {
		writeError(w, http.StatusBadRequest, "message text or json is required")
		return
	}
	msg := db.Message{
		Timestamp: api.now().UTC(),
		ScopeType: scopeType,
		ScopeID:   scopeID,
		Author:    strings.TrimSpace(req.Author),
		Kind:      strings.TrimSpace(req.Kind),
		Text:      text,
		JSON:      string(payload),
	}
	created, err := api.store.CreateMessage(r.Context(), msg)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to record message", err)
		return
	}
	writeJSON(w, http.StatusCreated, messageToV1(created))
}

func (api *ControlAPI) handleMessageList(w http.ResponseWriter, r *http.Request) {
	if api.store == nil {
		writeError(w, http.StatusInternalServerError, "message store unavailable")
		return
	}
	query := r.URL.Query()
	scopeType, scopeID, err := parseMessageScope(query.Get("scope_type"), query.Get("scope_id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	afterParam := strings.TrimSpace(query.Get("after_id"))
	if afterParam == "" {
		afterParam = strings.TrimSpace(query.Get("after"))
	}
	after, err := parseQueryInt64(afterParam)
	if err != nil || after < 0 {
		writeError(w, http.StatusBadRequest, "invalid after_id")
		return
	}
	limit, err := parseQueryInt(query.Get("limit"))
	if err != nil || limit < 0 {
		writeError(w, http.StatusBadRequest, "invalid limit")
		return
	}
	if limit <= 0 {
		limit = defaultMessagesLimit
	}
	if limit > maxMessagesLimit {
		limit = maxMessagesLimit
	}
	var messages []db.Message
	if after > 0 {
		messages, err = api.store.ListMessagesByScope(r.Context(), scopeType, scopeID, after, limit)
	} else {
		messages, err = api.store.ListMessagesByScopeTail(r.Context(), scopeType, scopeID, limit)
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load messages", err)
		return
	}
	resp := V1MessagesResponse{Messages: make([]V1Message, 0, len(messages))}
	var lastID int64
	for _, msg := range messages {
		if msg.ID > lastID {
			lastID = msg.ID
		}
		resp.Messages = append(resp.Messages, messageToV1(msg))
	}
	if lastID > 0 {
		resp.LastID = lastID
	}
	writeJSON(w, http.StatusOK, resp)
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
		if parts[1] == "start" {
			if r.Method != http.MethodPost {
				writeMethodNotAllowed(w, []string{http.MethodPost})
				return
			}
			api.handleSandboxStart(w, r, vmid)
			return
		}
		if parts[1] == "stop" {
			if r.Method != http.MethodPost {
				writeMethodNotAllowed(w, []string{http.MethodPost})
				return
			}
			api.handleSandboxStop(w, r, vmid)
			return
		}
		if parts[1] == "touch" {
			if r.Method != http.MethodPost {
				writeMethodNotAllowed(w, []string{http.MethodPost})
				return
			}
			api.handleSandboxTouch(w, r, vmid)
			return
		}
		if parts[1] == "revert" {
			if r.Method != http.MethodPost {
				writeMethodNotAllowed(w, []string{http.MethodPost})
				return
			}
			api.handleSandboxRevert(w, r, vmid)
			return
		}
		if parts[1] == "destroy" {
			if r.Method != http.MethodPost {
				writeMethodNotAllowed(w, []string{http.MethodPost})
				return
			}
			api.handleSandboxDestroy(w, r, vmid)
			return
		}
		if parts[1] == "events" {
			if r.Method != http.MethodGet {
				writeMethodNotAllowed(w, []string{http.MethodGet})
				return
			}
			api.handleSandboxEvents(w, r, vmid)
			return
		}
		if parts[1] == "doctor" {
			if r.Method != http.MethodPost {
				writeMethodNotAllowed(w, []string{http.MethodPost})
				return
			}
			api.handleSandboxDoctor(w, r, vmid)
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

func (api *ControlAPI) handleWorkspaces(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		api.handleWorkspaceCreate(w, r)
	case http.MethodGet:
		api.handleWorkspaceList(w, r)
	default:
		writeMethodNotAllowed(w, []string{http.MethodGet, http.MethodPost})
	}
}

func (api *ControlAPI) handleWorkspaceByID(w http.ResponseWriter, r *http.Request) {
	tail := strings.TrimPrefix(r.URL.Path, "/v1/workspaces/")
	parts := strings.Split(strings.Trim(tail, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	id := parts[0]
	switch len(parts) {
	case 1:
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w, []string{http.MethodGet})
			return
		}
		api.handleWorkspaceGet(w, r, id)
		return
	case 2:
		switch parts[1] {
		case "check":
			if r.Method != http.MethodGet {
				writeMethodNotAllowed(w, []string{http.MethodGet})
				return
			}
			api.handleWorkspaceCheck(w, r, id)
			return
		case "snapshots":
			switch r.Method {
			case http.MethodGet:
				api.handleWorkspaceSnapshotsList(w, r, id)
				return
			case http.MethodPost:
				api.handleWorkspaceSnapshotCreate(w, r, id)
				return
			default:
				writeMethodNotAllowed(w, []string{http.MethodGet, http.MethodPost})
				return
			}
		case "attach":
			if r.Method != http.MethodPost {
				writeMethodNotAllowed(w, []string{http.MethodPost})
				return
			}
			api.handleWorkspaceAttach(w, r, id)
			return
		case "detach":
			if r.Method != http.MethodPost {
				writeMethodNotAllowed(w, []string{http.MethodPost})
				return
			}
			api.handleWorkspaceDetach(w, r, id)
			return
		case "rebind":
			if r.Method != http.MethodPost {
				writeMethodNotAllowed(w, []string{http.MethodPost})
				return
			}
			api.handleWorkspaceRebind(w, r, id)
			return
		case "fork":
			if r.Method != http.MethodPost {
				writeMethodNotAllowed(w, []string{http.MethodPost})
				return
			}
			api.handleWorkspaceFork(w, r, id)
			return
		}
	case 4:
		if parts[1] == "snapshots" && parts[3] == "restore" {
			if r.Method != http.MethodPost {
				writeMethodNotAllowed(w, []string{http.MethodPost})
				return
			}
			api.handleWorkspaceSnapshotRestore(w, r, id, parts[2])
			return
		}
	}
	writeError(w, http.StatusNotFound, "workspace not found")
}

func (api *ControlAPI) handleSessions(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		api.handleSessionCreate(w, r)
	case http.MethodGet:
		api.handleSessionList(w, r)
	default:
		writeMethodNotAllowed(w, []string{http.MethodGet, http.MethodPost})
	}
}

func (api *ControlAPI) handleSessionByID(w http.ResponseWriter, r *http.Request) {
	tail := strings.TrimPrefix(r.URL.Path, "/v1/sessions/")
	parts := strings.Split(strings.Trim(tail, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}
	id := parts[0]
	switch len(parts) {
	case 1:
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w, []string{http.MethodGet})
			return
		}
		api.handleSessionGet(w, r, id)
		return
	case 2:
		switch parts[1] {
		case "resume":
			if r.Method != http.MethodPost {
				writeMethodNotAllowed(w, []string{http.MethodPost})
				return
			}
			api.handleSessionResume(w, r, id)
			return
		case "stop":
			if r.Method != http.MethodPost {
				writeMethodNotAllowed(w, []string{http.MethodPost})
				return
			}
			api.handleSessionStop(w, r, id)
			return
		case "fork":
			if r.Method != http.MethodPost {
				writeMethodNotAllowed(w, []string{http.MethodPost})
				return
			}
			api.handleSessionFork(w, r, id)
			return
		case "doctor":
			if r.Method != http.MethodPost {
				writeMethodNotAllowed(w, []string{http.MethodPost})
				return
			}
			api.handleSessionDoctor(w, r, id)
			return
		}
	}
	writeError(w, http.StatusNotFound, "session not found")
}

func (api *ControlAPI) handleExposures(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		api.handleExposureCreate(w, r)
	case http.MethodGet:
		api.handleExposureList(w, r)
	default:
		writeMethodNotAllowed(w, []string{http.MethodGet, http.MethodPost})
	}
}

func (api *ControlAPI) handleExposureByName(w http.ResponseWriter, r *http.Request) {
	tail := strings.TrimPrefix(r.URL.Path, "/v1/exposures/")
	name := strings.TrimSpace(strings.Trim(tail, "/"))
	if name == "" {
		writeError(w, http.StatusNotFound, "exposure not found")
		return
	}
	if r.Method != http.MethodDelete {
		writeMethodNotAllowed(w, []string{http.MethodDelete})
		return
	}
	api.handleExposureDelete(w, r, name)
}

func (api *ControlAPI) handleSandboxList(w http.ResponseWriter, r *http.Request) {
	sandboxes, err := api.store.ListSandboxes(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list sandboxes")
		return
	}
	resp := V1SandboxesResponse{Sandboxes: make([]V1SandboxResponse, 0, len(sandboxes))}
	for _, sb := range sandboxes {
		resp.Sandboxes = append(resp.Sandboxes, api.sandboxToV1(sb))
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
	writeJSON(w, http.StatusOK, api.sandboxToV1(sandbox))
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
	provisionSandbox := req.JobID == ""
	if req.Profile == "" {
		writeError(w, http.StatusBadRequest, "profile is required")
		return
	}
	if !api.profileExists(req.Profile) {
		writeError(w, http.StatusBadRequest, "unknown profile")
		return
	}
	var (
		ttlMinutes int
		keepalive  bool
	)
	if req.Keepalive != nil {
		keepalive = *req.Keepalive
	}
	if profile, ok := api.profile(req.Profile); ok {
		if err := validateProfileForProvisioning(profile); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		appliedTTL, appliedKeepalive, err := applyProfileBehaviorDefaults(profile, req.TTLMinutes, req.Keepalive)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid profile behavior defaults")
			return
		}
		ttlMinutes = appliedTTL
		keepalive = appliedKeepalive
	} else {
		ttlMinutes = derefInt(req.TTLMinutes)
	}
	if req.TTLMinutes != nil && *req.TTLMinutes <= 0 {
		writeError(w, http.StatusBadRequest, "ttl_minutes must be positive")
		return
	}
	if req.VMID != nil && *req.VMID <= 0 {
		writeError(w, http.StatusBadRequest, "vmid must be positive")
		return
	}

	if provisionSandbox && api.jobOrchestrator == nil {
		ctx := r.Context()
		vmid := 0
		if req.VMID != nil {
			vmid = *req.VMID
		}
		_ = api.store.RecordEvent(ctx, "sandbox.provision_failed", &vmid, nil, "job orchestrator not initialized", "")
		writeError(w, http.StatusServiceUnavailable, "sandbox provisioning unavailable: job orchestrator not initialized (ssh_public_key required)")
		return
	}

	ctx := r.Context()
	if req.Workspace != nil && strings.TrimSpace(*req.Workspace) != "" {
		if api.workspaceMgr == nil {
			writeError(w, http.StatusInternalServerError, "workspace manager unavailable")
			return
		}
		workspace, err := api.workspaceMgr.Resolve(ctx, *req.Workspace)
		if err != nil {
			if errors.Is(err, ErrWorkspaceNotFound) {
				writeError(w, http.StatusNotFound, "workspace not found")
				return
			}
			writeError(w, http.StatusInternalServerError, "failed to load workspace")
			return
		}
		if workspace.AttachedVM != nil {
			if req.VMID == nil || *req.VMID != *workspace.AttachedVM {
				writeError(w, http.StatusConflict, "workspace already attached")
				return
			}
		}
		workspaceID := workspace.ID
		req.Workspace = &workspaceID
	}
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
		next, err := nextSandboxVMID(ctx, api.store)
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
	if ttlMinutes > 0 {
		leaseExpires = now.Add(time.Duration(ttlMinutes) * time.Minute)
	}

	sandbox := models.Sandbox{
		VMID:          vmid,
		Name:          req.Name,
		Profile:       req.Profile,
		State:         models.SandboxRequested,
		Keepalive:     keepalive,
		LeaseExpires:  leaseExpires,
		CreatedAt:     now,
		LastUpdatedAt: now,
	}
	if req.Workspace != nil && *req.Workspace != "" {
		workspace := *req.Workspace
		sandbox.WorkspaceID = &workspace
	}

	createdSandbox, err := createSandboxWithRetry(ctx, api.store, sandbox)
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

	if provisionSandbox {
		// Provisioning should not be coupled to the lifetime of the HTTP request context.
		// AI agents may use short client-side timeouts or disconnect while provisioning continues.
		updated, err := api.jobOrchestrator.ProvisionSandbox(context.Background(), createdSandbox.VMID)
		if err != nil {
			// Log the actual error before writing generic response
			if api.logger != nil {
				api.logger.Printf("provision sandbox %d failed: %v", createdSandbox.VMID, err)
			}
			writeError(w, http.StatusInternalServerError, "failed to provision sandbox", err)
			return
		}
		writeJSON(w, http.StatusCreated, api.sandboxToV1(updated))
		return
	}

	writeJSON(w, http.StatusCreated, api.sandboxToV1(createdSandbox))
}

func (api *ControlAPI) handleWorkspaceCreate(w http.ResponseWriter, r *http.Request) {
	if api.workspaceMgr == nil {
		writeError(w, http.StatusInternalServerError, "workspace manager unavailable")
		return
	}
	var req V1WorkspaceCreateRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	req.Storage = strings.TrimSpace(req.Storage)
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if req.SizeGB <= 0 {
		writeError(w, http.StatusBadRequest, "size_gb must be positive")
		return
	}
	workspace, err := api.workspaceMgr.Create(r.Context(), req.Name, req.Storage, req.SizeGB)
	if err != nil {
		switch {
		case errors.Is(err, ErrWorkspaceExists):
			writeError(w, http.StatusConflict, "workspace already exists")
		default:
			writeError(w, http.StatusInternalServerError, "failed to create workspace")
		}
		return
	}
	writeJSON(w, http.StatusCreated, workspaceToV1(workspace))
}

func (api *ControlAPI) handleWorkspaceList(w http.ResponseWriter, r *http.Request) {
	if api.workspaceMgr == nil {
		writeError(w, http.StatusInternalServerError, "workspace manager unavailable")
		return
	}
	workspaces, err := api.workspaceMgr.List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list workspaces")
		return
	}
	resp := V1WorkspacesResponse{Workspaces: make([]V1WorkspaceResponse, 0, len(workspaces))}
	for _, ws := range workspaces {
		resp.Workspaces = append(resp.Workspaces, workspaceToV1(ws))
	}
	writeJSON(w, http.StatusOK, resp)
}

func (api *ControlAPI) handleWorkspaceGet(w http.ResponseWriter, r *http.Request, id string) {
	if api.workspaceMgr == nil {
		writeError(w, http.StatusInternalServerError, "workspace manager unavailable")
		return
	}
	workspace, err := api.workspaceMgr.Resolve(r.Context(), id)
	if err != nil {
		if errors.Is(err, ErrWorkspaceNotFound) {
			writeError(w, http.StatusNotFound, "workspace not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load workspace")
		return
	}
	writeJSON(w, http.StatusOK, workspaceToV1(workspace))
}

func (api *ControlAPI) handleWorkspaceCheck(w http.ResponseWriter, r *http.Request, id string) {
	if api.workspaceMgr == nil {
		writeError(w, http.StatusInternalServerError, "workspace manager unavailable")
		return
	}
	result, err := api.workspaceMgr.Check(r.Context(), id)
	if err != nil {
		if errors.Is(err, ErrWorkspaceNotFound) {
			writeError(w, http.StatusNotFound, "workspace not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to check workspace")
		return
	}
	writeJSON(w, http.StatusOK, workspaceCheckToV1(result))
}

func (api *ControlAPI) handleWorkspaceAttach(w http.ResponseWriter, r *http.Request, id string) {
	if api.workspaceMgr == nil {
		writeError(w, http.StatusInternalServerError, "workspace manager unavailable")
		return
	}
	var req V1WorkspaceAttachRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.VMID <= 0 {
		writeError(w, http.StatusBadRequest, "vmid must be positive")
		return
	}
	workspace, err := api.workspaceMgr.Attach(r.Context(), id, req.VMID)
	if err != nil {
		switch {
		case errors.Is(err, ErrWorkspaceNotFound):
			writeError(w, http.StatusNotFound, "workspace not found")
		case errors.Is(err, ErrWorkspaceAttached), errors.Is(err, ErrWorkspaceVMInUse):
			writeError(w, http.StatusConflict, err.Error())
		case errors.Is(err, ErrSandboxNotFound):
			writeError(w, http.StatusNotFound, "sandbox not found")
		default:
			writeError(w, http.StatusInternalServerError, "failed to attach workspace")
		}
		return
	}
	writeJSON(w, http.StatusOK, workspaceToV1(workspace))
}

func (api *ControlAPI) handleWorkspaceDetach(w http.ResponseWriter, r *http.Request, id string) {
	if api.workspaceMgr == nil {
		writeError(w, http.StatusInternalServerError, "workspace manager unavailable")
		return
	}
	workspace, err := api.workspaceMgr.Detach(r.Context(), id)
	if err != nil {
		switch {
		case errors.Is(err, ErrWorkspaceNotFound):
			writeError(w, http.StatusNotFound, "workspace not found")
		case errors.Is(err, ErrWorkspaceNotAttached):
			writeError(w, http.StatusConflict, err.Error())
		default:
			writeError(w, http.StatusInternalServerError, "failed to detach workspace")
		}
		return
	}
	writeJSON(w, http.StatusOK, workspaceToV1(workspace))
}

func (api *ControlAPI) handleWorkspaceRebind(w http.ResponseWriter, r *http.Request, id string) {
	if api.jobOrchestrator == nil {
		writeError(w, http.StatusServiceUnavailable, "workspace rebind unavailable")
		return
	}
	var req V1WorkspaceRebindRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.Profile = strings.TrimSpace(req.Profile)
	if req.Profile == "" {
		writeError(w, http.StatusBadRequest, "profile is required")
		return
	}
	if req.TTLMinutes != nil && *req.TTLMinutes <= 0 {
		writeError(w, http.StatusBadRequest, "ttl_minutes must be positive")
		return
	}
	if !api.profileExists(req.Profile) {
		writeError(w, http.StatusBadRequest, "unknown profile")
		return
	}
	if profile, ok := api.profile(req.Profile); ok {
		if err := validateProfileForProvisioning(profile); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	result, err := api.jobOrchestrator.RebindWorkspace(r.Context(), id, req.Profile, req.TTLMinutes, req.KeepOld)
	if err != nil {
		switch {
		case errors.Is(err, ErrWorkspaceNotFound):
			writeError(w, http.StatusNotFound, "workspace not found")
		case errors.Is(err, ErrWorkspaceAttached), errors.Is(err, ErrWorkspaceVMInUse), errors.Is(err, ErrWorkspaceLeaseHeld):
			writeError(w, http.StatusConflict, err.Error())
		case errors.Is(err, ErrSandboxNotFound):
			writeError(w, http.StatusNotFound, "sandbox not found")
		case errors.Is(err, ErrInvalidTransition):
			writeError(w, http.StatusConflict, "invalid sandbox state")
		default:
			writeError(w, http.StatusInternalServerError, "failed to rebind workspace")
		}
		return
	}
	resp := V1WorkspaceRebindResponse{
		Workspace: workspaceToV1(result.Workspace),
		Sandbox:   api.sandboxToV1(result.Sandbox),
		OldVMID:   result.OldVMID,
	}
	writeJSON(w, http.StatusOK, resp)
}

func (api *ControlAPI) handleWorkspaceFork(w http.ResponseWriter, r *http.Request, id string) {
	if api.workspaceMgr == nil {
		writeError(w, http.StatusInternalServerError, "workspace manager unavailable")
		return
	}
	var req V1WorkspaceForkRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	req.FromSnapshot = strings.TrimSpace(req.FromSnapshot)
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	workspace, err := api.workspaceMgr.Fork(r.Context(), id, req.Name, req.FromSnapshot)
	if err != nil {
		switch {
		case errors.Is(err, ErrWorkspaceNotFound):
			writeError(w, http.StatusNotFound, "workspace not found")
		case errors.Is(err, ErrWorkspaceExists):
			writeError(w, http.StatusConflict, "workspace already exists")
		case errors.Is(err, ErrWorkspaceForkAttached):
			writeError(w, http.StatusConflict, "workspace must be detached for fork")
		case errors.Is(err, ErrWorkspaceSnapshotNotFound):
			writeError(w, http.StatusNotFound, "workspace snapshot not found")
		case errors.Is(err, ErrWorkspaceLeaseHeld):
			writeError(w, http.StatusConflict, "workspace lease held")
		case errors.Is(err, proxmox.ErrStorageUnsupported):
			writeError(w, http.StatusBadRequest, "workspace storage does not support cloning")
		default:
			writeError(w, http.StatusInternalServerError, "failed to fork workspace")
		}
		return
	}
	writeJSON(w, http.StatusCreated, workspaceToV1(workspace))
}

func (api *ControlAPI) handleWorkspaceSnapshotsList(w http.ResponseWriter, r *http.Request, id string) {
	if api.workspaceMgr == nil {
		writeError(w, http.StatusInternalServerError, "workspace manager unavailable")
		return
	}
	snapshots, err := api.workspaceMgr.SnapshotList(r.Context(), id)
	if err != nil {
		if errors.Is(err, ErrWorkspaceNotFound) {
			writeError(w, http.StatusNotFound, "workspace not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to list workspace snapshots")
		return
	}
	resp := V1WorkspaceSnapshotsResponse{Snapshots: make([]V1WorkspaceSnapshotResponse, 0, len(snapshots))}
	for _, snapshot := range snapshots {
		resp.Snapshots = append(resp.Snapshots, workspaceSnapshotToV1(snapshot))
	}
	writeJSON(w, http.StatusOK, resp)
}

func (api *ControlAPI) handleWorkspaceSnapshotCreate(w http.ResponseWriter, r *http.Request, id string) {
	if api.workspaceMgr == nil {
		writeError(w, http.StatusInternalServerError, "workspace manager unavailable")
		return
	}
	var req V1WorkspaceSnapshotCreateRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	start := api.now().UTC()
	snapshot, err := api.workspaceMgr.SnapshotCreate(r.Context(), id, req.Name)
	result := "success"
	if err != nil {
		result = "error"
	}
	if api.metrics != nil {
		api.metrics.IncWorkspaceSnapshot("create", result)
		api.metrics.ObserveWorkspaceSnapshotDuration("create", result, api.now().UTC().Sub(start))
	}
	if err != nil {
		switch {
		case errors.Is(err, ErrWorkspaceNotFound):
			writeError(w, http.StatusNotFound, "workspace not found")
		case errors.Is(err, ErrWorkspaceSnapshotExists):
			writeError(w, http.StatusConflict, "workspace snapshot already exists")
		case errors.Is(err, ErrWorkspaceSnapshotAttached):
			writeError(w, http.StatusConflict, "workspace must be detached for snapshots")
		case errors.Is(err, ErrWorkspaceLeaseHeld):
			writeError(w, http.StatusConflict, "workspace lease held")
		case errors.Is(err, proxmox.ErrStorageUnsupported):
			writeError(w, http.StatusBadRequest, "workspace storage does not support snapshots")
		default:
			writeError(w, http.StatusInternalServerError, "failed to create workspace snapshot")
		}
		return
	}
	writeJSON(w, http.StatusCreated, workspaceSnapshotToV1(snapshot))
}

func (api *ControlAPI) handleWorkspaceSnapshotRestore(w http.ResponseWriter, r *http.Request, id, name string) {
	if api.workspaceMgr == nil {
		writeError(w, http.StatusInternalServerError, "workspace manager unavailable")
		return
	}
	name = strings.TrimSpace(name)
	if name == "" {
		writeError(w, http.StatusBadRequest, "snapshot name is required")
		return
	}
	start := api.now().UTC()
	snapshot, err := api.workspaceMgr.SnapshotRestore(r.Context(), id, name)
	result := "success"
	if err != nil {
		result = "error"
	}
	if api.metrics != nil {
		api.metrics.IncWorkspaceSnapshot("restore", result)
		api.metrics.ObserveWorkspaceSnapshotDuration("restore", result, api.now().UTC().Sub(start))
	}
	if err != nil {
		switch {
		case errors.Is(err, ErrWorkspaceNotFound):
			writeError(w, http.StatusNotFound, "workspace not found")
		case errors.Is(err, ErrWorkspaceSnapshotNotFound):
			writeError(w, http.StatusNotFound, "workspace snapshot not found")
		case errors.Is(err, ErrWorkspaceSnapshotAttached):
			writeError(w, http.StatusConflict, "workspace must be detached for snapshots")
		case errors.Is(err, ErrWorkspaceLeaseHeld):
			writeError(w, http.StatusConflict, "workspace lease held")
		case errors.Is(err, proxmox.ErrStorageUnsupported):
			writeError(w, http.StatusBadRequest, "workspace storage does not support snapshots")
		default:
			writeError(w, http.StatusInternalServerError, "failed to restore workspace snapshot")
		}
		return
	}
	writeJSON(w, http.StatusOK, workspaceSnapshotToV1(snapshot))
}

func (api *ControlAPI) handleSessionCreate(w http.ResponseWriter, r *http.Request) {
	if api.store == nil {
		writeError(w, http.StatusServiceUnavailable, "session store unavailable")
		return
	}
	if api.workspaceMgr == nil {
		writeError(w, http.StatusInternalServerError, "workspace manager unavailable")
		return
	}
	var req V1SessionCreateRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	req.Profile = strings.TrimSpace(req.Profile)
	req.Branch = strings.TrimSpace(req.Branch)
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if req.Profile == "" {
		writeError(w, http.StatusBadRequest, "profile is required")
		return
	}
	if !api.profileExists(req.Profile) {
		writeError(w, http.StatusBadRequest, "unknown profile")
		return
	}
	if req.WorkspaceID != nil && req.WorkspaceCreate != nil {
		writeError(w, http.StatusBadRequest, "workspace_id and workspace_create are mutually exclusive")
		return
	}
	if req.WorkspaceID == nil && req.WorkspaceCreate == nil {
		writeError(w, http.StatusBadRequest, "workspace_id or workspace_create is required")
		return
	}

	ctx := r.Context()
	if _, err := api.store.GetSessionByName(ctx, req.Name); err == nil {
		writeError(w, http.StatusConflict, "session already exists")
		return
	} else if err != nil && !errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusInternalServerError, "failed to check session")
		return
	}

	var workspace models.Workspace
	if req.WorkspaceCreate != nil {
		req.WorkspaceCreate.Name = strings.TrimSpace(req.WorkspaceCreate.Name)
		if req.WorkspaceCreate.Name == "" {
			writeError(w, http.StatusBadRequest, "workspace_create.name is required")
			return
		}
		if req.WorkspaceCreate.SizeGB <= 0 {
			writeError(w, http.StatusBadRequest, "workspace_create.size_gb must be positive")
			return
		}
		created, err := api.workspaceMgr.Create(ctx, req.WorkspaceCreate.Name, req.WorkspaceCreate.Storage, req.WorkspaceCreate.SizeGB)
		if err != nil {
			if errors.Is(err, ErrWorkspaceExists) {
				writeError(w, http.StatusConflict, "workspace already exists")
				return
			}
			writeError(w, http.StatusInternalServerError, "failed to create workspace")
			return
		}
		workspace = created
	} else if req.WorkspaceID != nil {
		resolved, err := api.workspaceMgr.Resolve(ctx, *req.WorkspaceID)
		if err != nil {
			if errors.Is(err, ErrWorkspaceNotFound) {
				writeError(w, http.StatusNotFound, "workspace not found")
				return
			}
			writeError(w, http.StatusInternalServerError, "failed to load workspace")
			return
		}
		workspace = resolved
	}

	sessionID, err := newSessionID()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create session id")
		return
	}

	var (
		leaseOwner string
		leaseNonce string
	)
	if workspace.AttachedVM == nil || *workspace.AttachedVM <= 0 {
		leaseOwner = workspaceLeaseOwnerForSession(sessionID)
		nonce, _, err := api.acquireWorkspaceLease(ctx, workspace, leaseOwner, workspaceLeaseDefaultTTL, 0)
		if err != nil {
			switch {
			case errors.Is(err, ErrWorkspaceLeaseHeld), errors.Is(err, errWorkspaceWaitTimeout):
				latest, loadErr := api.workspaceMgr.Resolve(ctx, workspace.ID)
				if loadErr == nil {
					workspace = latest
				}
				writeError(w, http.StatusConflict, "workspace lease held", workspaceConflictDetails(workspace, 0))
			case errors.Is(err, ErrWorkspaceNotFound):
				writeError(w, http.StatusNotFound, "workspace not found")
			default:
				writeError(w, http.StatusInternalServerError, "failed to acquire workspace lease")
			}
			return
		}
		leaseNonce = nonce
		recordWorkspaceLeaseEvent(ctx, api.store, "workspace.lease.acquired", nil, nil, workspace.ID, leaseOwner, api.now().UTC().Add(workspaceLeaseDefaultTTL))
	}

	var currentVMID *int
	if workspace.AttachedVM != nil && *workspace.AttachedVM > 0 {
		value := *workspace.AttachedVM
		currentVMID = &value
	}

	now := api.now().UTC()
	session := models.Session{
		ID:          sessionID,
		Name:        req.Name,
		WorkspaceID: workspace.ID,
		CurrentVMID: currentVMID,
		Profile:     req.Profile,
		Branch:      req.Branch,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := api.store.CreateSession(ctx, session); err != nil {
		if leaseOwner != "" && leaseNonce != "" {
			_, _ = api.store.ReleaseWorkspaceLease(ctx, workspace.ID, leaseOwner, leaseNonce)
			recordWorkspaceLeaseEvent(ctx, api.store, "workspace.lease.released", nil, nil, workspace.ID, leaseOwner, time.Time{})
		}
		if isUniqueConstraint(err) {
			writeError(w, http.StatusConflict, "session already exists")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to create session")
		return
	}
	writeJSON(w, http.StatusCreated, api.sessionToV1(session))
}

func (api *ControlAPI) handleSessionList(w http.ResponseWriter, r *http.Request) {
	if api.store == nil {
		writeError(w, http.StatusServiceUnavailable, "session store unavailable")
		return
	}
	sessions, err := api.store.ListSessions(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list sessions")
		return
	}
	resp := V1SessionsResponse{Sessions: make([]V1SessionResponse, 0, len(sessions))}
	for _, session := range sessions {
		resp.Sessions = append(resp.Sessions, api.sessionToV1(session))
	}
	writeJSON(w, http.StatusOK, resp)
}

func (api *ControlAPI) handleSessionGet(w http.ResponseWriter, r *http.Request, id string) {
	session, err := api.resolveSession(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "session not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load session")
		return
	}
	writeJSON(w, http.StatusOK, api.sessionToV1(session))
}

func (api *ControlAPI) handleSessionResume(w http.ResponseWriter, r *http.Request, id string) {
	if api.jobOrchestrator == nil {
		writeError(w, http.StatusServiceUnavailable, "session resume unavailable")
		return
	}
	if api.workspaceMgr == nil {
		writeError(w, http.StatusInternalServerError, "workspace manager unavailable")
		return
	}
	var payload struct{}
	if err := decodeOptionalJSON(w, r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx := r.Context()
	session, err := api.resolveSession(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "session not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load session")
		return
	}
	if strings.TrimSpace(session.WorkspaceID) == "" {
		writeError(w, http.StatusBadRequest, "session workspace_id is required")
		return
	}
	workspace, err := api.workspaceMgr.Resolve(ctx, session.WorkspaceID)
	if err != nil {
		if errors.Is(err, ErrWorkspaceNotFound) {
			writeError(w, http.StatusNotFound, "workspace not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load workspace")
		return
	}
	if owner := workspaceLeaseOwnerForSession(session.ID); owner != "" && workspace.LeaseOwner == owner && strings.TrimSpace(workspace.LeaseNonce) != "" {
		if released, err := api.store.ReleaseWorkspaceLease(ctx, workspace.ID, owner, workspace.LeaseNonce); err == nil && released {
			recordWorkspaceLeaseEvent(ctx, api.store, "workspace.lease.released", nil, nil, workspace.ID, owner, time.Time{})
		}
	}
	if session.CurrentVMID != nil && *session.CurrentVMID > 0 {
		owner := workspaceLeaseOwnerForSandbox(*session.CurrentVMID)
		if owner != "" && workspace.LeaseOwner == owner && strings.TrimSpace(workspace.LeaseNonce) != "" {
			vmid := *session.CurrentVMID
			if released, err := api.store.ReleaseWorkspaceLease(ctx, workspace.ID, owner, workspace.LeaseNonce); err == nil && released {
				recordWorkspaceLeaseEvent(ctx, api.store, "workspace.lease.released", &vmid, nil, workspace.ID, owner, time.Time{})
			}
		}
	}

	result, err := api.jobOrchestrator.RebindWorkspace(ctx, session.WorkspaceID, session.Profile, nil, false)
	if err != nil {
		switch {
		case errors.Is(err, ErrWorkspaceNotFound):
			writeError(w, http.StatusNotFound, "workspace not found")
		case errors.Is(err, ErrWorkspaceAttached), errors.Is(err, ErrWorkspaceVMInUse), errors.Is(err, ErrWorkspaceLeaseHeld):
			writeError(w, http.StatusConflict, err.Error())
		case errors.Is(err, ErrSandboxNotFound):
			writeError(w, http.StatusNotFound, "sandbox not found")
		case errors.Is(err, ErrInvalidTransition):
			writeError(w, http.StatusConflict, "invalid sandbox state")
		default:
			writeError(w, http.StatusInternalServerError, "failed to resume session")
		}
		return
	}
	if err := api.store.UpdateSessionCurrentVMID(ctx, session.ID, &result.Sandbox.VMID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update session")
		return
	}
	updated, loadErr := api.store.GetSession(ctx, session.ID)
	if loadErr != nil {
		updated = session
		value := result.Sandbox.VMID
		updated.CurrentVMID = &value
		updated.UpdatedAt = api.now().UTC()
	}
	resp := V1SessionResumeResponse{
		Session:   api.sessionToV1(updated),
		Workspace: workspaceToV1(result.Workspace),
		Sandbox:   api.sandboxToV1(result.Sandbox),
		OldVMID:   result.OldVMID,
	}
	writeJSON(w, http.StatusOK, resp)
}

func (api *ControlAPI) handleSessionStop(w http.ResponseWriter, r *http.Request, id string) {
	if api.sandboxManager == nil {
		writeError(w, http.StatusInternalServerError, "sandbox manager unavailable")
		return
	}
	if api.workspaceMgr == nil {
		writeError(w, http.StatusInternalServerError, "workspace manager unavailable")
		return
	}
	ctx := r.Context()
	session, err := api.resolveSession(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "session not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load session")
		return
	}
	if session.CurrentVMID != nil && *session.CurrentVMID > 0 {
		if err := api.sandboxManager.Destroy(ctx, *session.CurrentVMID); err != nil && !errors.Is(err, ErrSandboxNotFound) {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to destroy sandbox: %v", err))
			return
		}
	}
	if err := api.store.UpdateSessionCurrentVMID(ctx, session.ID, nil); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update session")
		return
	}
	workspace, err := api.workspaceMgr.Resolve(ctx, session.WorkspaceID)
	if err != nil {
		if errors.Is(err, ErrWorkspaceNotFound) {
			writeError(w, http.StatusNotFound, "workspace not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load workspace")
		return
	}
	if workspace.AttachedVM != nil && *workspace.AttachedVM > 0 {
		writeError(w, http.StatusConflict, "workspace already attached")
		return
	}
	owner := workspaceLeaseOwnerForSession(session.ID)
	switch {
	case owner == "":
		// nothing to do
	case workspace.LeaseOwner == owner && strings.TrimSpace(workspace.LeaseNonce) != "":
		expiresAt := api.now().UTC().Add(workspaceLeaseDefaultTTL)
		if renewed, err := api.store.RenewWorkspaceLease(ctx, workspace.ID, owner, workspace.LeaseNonce, expiresAt); err == nil && renewed {
			recordWorkspaceLeaseEvent(ctx, api.store, "workspace.lease.renewed", nil, nil, workspace.ID, owner, expiresAt)
		}
	case workspace.LeaseOwner != "" && workspace.LeaseOwner != owner:
		writeError(w, http.StatusConflict, "workspace lease held", workspaceConflictDetails(workspace, 0))
		return
	default:
		nonce, _, err := api.acquireWorkspaceLease(ctx, workspace, owner, workspaceLeaseDefaultTTL, 0)
		if err != nil {
			switch {
			case errors.Is(err, ErrWorkspaceLeaseHeld), errors.Is(err, errWorkspaceWaitTimeout):
				latest, loadErr := api.workspaceMgr.Resolve(ctx, workspace.ID)
				if loadErr == nil {
					workspace = latest
				}
				writeError(w, http.StatusConflict, "workspace lease held", workspaceConflictDetails(workspace, 0))
			case errors.Is(err, ErrWorkspaceNotFound):
				writeError(w, http.StatusNotFound, "workspace not found")
			default:
				writeError(w, http.StatusInternalServerError, "failed to acquire workspace lease")
			}
			return
		}
		recordWorkspaceLeaseEvent(ctx, api.store, "workspace.lease.acquired", nil, nil, workspace.ID, owner, api.now().UTC().Add(workspaceLeaseDefaultTTL))
		_ = nonce
	}

	updated, loadErr := api.store.GetSession(ctx, session.ID)
	if loadErr != nil {
		updated = session
		updated.CurrentVMID = nil
		updated.UpdatedAt = api.now().UTC()
	}
	writeJSON(w, http.StatusOK, api.sessionToV1(updated))
}

func (api *ControlAPI) handleSessionFork(w http.ResponseWriter, r *http.Request, id string) {
	if api.workspaceMgr == nil {
		writeError(w, http.StatusInternalServerError, "workspace manager unavailable")
		return
	}
	var req V1SessionForkRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	req.Profile = strings.TrimSpace(req.Profile)
	req.Branch = strings.TrimSpace(req.Branch)
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if req.WorkspaceID != nil && req.WorkspaceCreate != nil {
		writeError(w, http.StatusBadRequest, "workspace_id and workspace_create are mutually exclusive")
		return
	}
	if req.WorkspaceID == nil && req.WorkspaceCreate == nil {
		writeError(w, http.StatusBadRequest, "workspace_id or workspace_create is required")
		return
	}

	ctx := r.Context()
	origin, err := api.resolveSession(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "session not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load session")
		return
	}
	if _, err := api.store.GetSessionByName(ctx, req.Name); err == nil {
		writeError(w, http.StatusConflict, "session already exists")
		return
	} else if err != nil && !errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusInternalServerError, "failed to check session")
		return
	}

	profile := req.Profile
	if profile == "" {
		profile = origin.Profile
	}
	if profile == "" {
		writeError(w, http.StatusBadRequest, "profile is required")
		return
	}
	if !api.profileExists(profile) {
		writeError(w, http.StatusBadRequest, "unknown profile")
		return
	}

	var workspace models.Workspace
	if req.WorkspaceCreate != nil {
		req.WorkspaceCreate.Name = strings.TrimSpace(req.WorkspaceCreate.Name)
		if req.WorkspaceCreate.Name == "" {
			writeError(w, http.StatusBadRequest, "workspace_create.name is required")
			return
		}
		if req.WorkspaceCreate.SizeGB <= 0 {
			writeError(w, http.StatusBadRequest, "workspace_create.size_gb must be positive")
			return
		}
		created, err := api.workspaceMgr.Create(ctx, req.WorkspaceCreate.Name, req.WorkspaceCreate.Storage, req.WorkspaceCreate.SizeGB)
		if err != nil {
			if errors.Is(err, ErrWorkspaceExists) {
				writeError(w, http.StatusConflict, "workspace already exists")
				return
			}
			writeError(w, http.StatusInternalServerError, "failed to create workspace")
			return
		}
		workspace = created
	} else if req.WorkspaceID != nil {
		resolved, err := api.workspaceMgr.Resolve(ctx, *req.WorkspaceID)
		if err != nil {
			if errors.Is(err, ErrWorkspaceNotFound) {
				writeError(w, http.StatusNotFound, "workspace not found")
				return
			}
			writeError(w, http.StatusInternalServerError, "failed to load workspace")
			return
		}
		workspace = resolved
	}

	sessionID, err := newSessionID()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create session id")
		return
	}
	var (
		leaseOwner string
		leaseNonce string
	)
	if workspace.AttachedVM == nil || *workspace.AttachedVM <= 0 {
		leaseOwner = workspaceLeaseOwnerForSession(sessionID)
		nonce, _, err := api.acquireWorkspaceLease(ctx, workspace, leaseOwner, workspaceLeaseDefaultTTL, 0)
		if err != nil {
			switch {
			case errors.Is(err, ErrWorkspaceLeaseHeld), errors.Is(err, errWorkspaceWaitTimeout):
				latest, loadErr := api.workspaceMgr.Resolve(ctx, workspace.ID)
				if loadErr == nil {
					workspace = latest
				}
				writeError(w, http.StatusConflict, "workspace lease held", workspaceConflictDetails(workspace, 0))
			case errors.Is(err, ErrWorkspaceNotFound):
				writeError(w, http.StatusNotFound, "workspace not found")
			default:
				writeError(w, http.StatusInternalServerError, "failed to acquire workspace lease")
			}
			return
		}
		leaseNonce = nonce
		recordWorkspaceLeaseEvent(ctx, api.store, "workspace.lease.acquired", nil, nil, workspace.ID, leaseOwner, api.now().UTC().Add(workspaceLeaseDefaultTTL))
	}

	branch := req.Branch
	if branch == "" {
		branch = origin.Branch
	}
	var currentVMID *int
	if workspace.AttachedVM != nil && *workspace.AttachedVM > 0 {
		value := *workspace.AttachedVM
		currentVMID = &value
	}
	now := api.now().UTC()
	session := models.Session{
		ID:          sessionID,
		Name:        req.Name,
		WorkspaceID: workspace.ID,
		CurrentVMID: currentVMID,
		Profile:     profile,
		Branch:      branch,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := api.store.CreateSession(ctx, session); err != nil {
		if leaseOwner != "" && leaseNonce != "" {
			_, _ = api.store.ReleaseWorkspaceLease(ctx, workspace.ID, leaseOwner, leaseNonce)
			recordWorkspaceLeaseEvent(ctx, api.store, "workspace.lease.released", nil, nil, workspace.ID, leaseOwner, time.Time{})
		}
		if isUniqueConstraint(err) {
			writeError(w, http.StatusConflict, "session already exists")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to create session")
		return
	}
	writeJSON(w, http.StatusCreated, api.sessionToV1(session))
}

func (api *ControlAPI) handleExposureCreate(w http.ResponseWriter, r *http.Request) {
	if api.store == nil {
		writeError(w, http.StatusServiceUnavailable, "exposure registry unavailable")
		return
	}
	if api.exposurePublisher == nil {
		writeError(w, http.StatusServiceUnavailable, "exposure publisher unavailable")
		return
	}
	var req V1ExposureCreateRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	req.TargetIP = strings.TrimSpace(req.TargetIP)
	req.URL = strings.TrimSpace(req.URL)
	req.State = strings.TrimSpace(req.State)

	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if req.VMID <= 0 {
		writeError(w, http.StatusBadRequest, "vmid must be positive")
		return
	}
	if req.Port <= 0 || req.Port > 65535 {
		writeError(w, http.StatusBadRequest, "port must be between 1 and 65535")
		return
	}

	ctx := r.Context()
	if _, err := api.store.GetExposure(ctx, req.Name); err == nil {
		writeError(w, http.StatusConflict, "exposure already exists")
		return
	} else if err != nil && !errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusInternalServerError, "failed to check exposure")
		return
	}
	sandbox, err := api.store.GetSandbox(ctx, req.VMID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "sandbox not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load sandbox")
		return
	}
	if req.TargetIP == "" {
		req.TargetIP = strings.TrimSpace(sandbox.IP)
	}
	if req.TargetIP == "" {
		writeError(w, http.StatusConflict, "sandbox has no ip assigned")
		return
	}
	if net.ParseIP(req.TargetIP) == nil {
		writeError(w, http.StatusBadRequest, "target_ip must be a valid IP")
		return
	}

	publishResult, err := api.exposurePublisher.Publish(ctx, req.Name, req.TargetIP, req.Port)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to configure exposure")
		return
	}
	if publishResult.State == "" {
		publishResult.State = defaultExposureState
	}

	now := api.now().UTC()
	exposure := db.Exposure{
		Name:      req.Name,
		VMID:      req.VMID,
		Port:      req.Port,
		TargetIP:  req.TargetIP,
		URL:       publishResult.URL,
		State:     publishResult.State,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := api.store.CreateExposure(ctx, exposure); err != nil {
		_ = api.exposurePublisher.Unpublish(ctx, exposure.Name, exposure.Port)
		if isUniqueConstraint(err) {
			writeError(w, http.StatusConflict, "exposure already exists")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to create exposure")
		return
	}

	payload, _ := json.Marshal(map[string]any{
		"name":      exposure.Name,
		"vmid":      exposure.VMID,
		"port":      exposure.Port,
		"target_ip": exposure.TargetIP,
		"url":       exposure.URL,
		"state":     exposure.State,
		"force":     req.Force,
	})
	vmid := exposure.VMID
	_ = api.store.RecordEvent(ctx, "exposure.create", &vmid, nil, fmt.Sprintf("exposure %s created", exposure.Name), string(payload))

	writeJSON(w, http.StatusCreated, exposureToV1(exposure))
}

func (api *ControlAPI) handleExposureList(w http.ResponseWriter, r *http.Request) {
	if api.store == nil {
		writeError(w, http.StatusServiceUnavailable, "exposure registry unavailable")
		return
	}
	exposures, err := api.store.ListExposures(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list exposures")
		return
	}
	resp := V1ExposuresResponse{Exposures: make([]V1Exposure, 0, len(exposures))}
	for _, exposure := range exposures {
		resp.Exposures = append(resp.Exposures, exposureToV1(exposure))
	}
	writeJSON(w, http.StatusOK, resp)
}

func (api *ControlAPI) handleExposureDelete(w http.ResponseWriter, r *http.Request, name string) {
	if api.store == nil {
		writeError(w, http.StatusServiceUnavailable, "exposure registry unavailable")
		return
	}
	if api.exposurePublisher == nil {
		writeError(w, http.StatusServiceUnavailable, "exposure publisher unavailable")
		return
	}
	exposure, err := api.store.GetExposure(r.Context(), name)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "exposure not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load exposure")
		return
	}
	if err := api.exposurePublisher.Unpublish(r.Context(), exposure.Name, exposure.Port); err != nil && !errors.Is(err, ErrServeRuleNotFound) {
		writeError(w, http.StatusInternalServerError, "failed to remove exposure")
		return
	}
	if err := api.store.DeleteExposure(r.Context(), name); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "exposure not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to delete exposure")
		return
	}
	payload, _ := json.Marshal(map[string]any{
		"name":      exposure.Name,
		"vmid":      exposure.VMID,
		"port":      exposure.Port,
		"target_ip": exposure.TargetIP,
		"url":       exposure.URL,
		"state":     exposure.State,
	})
	vmid := exposure.VMID
	_ = api.store.RecordEvent(r.Context(), "exposure.delete", &vmid, nil, fmt.Sprintf("exposure %s deleted", exposure.Name), string(payload))

	writeJSON(w, http.StatusOK, exposureToV1(exposure))
}

func (api *ControlAPI) handleSandboxDestroy(w http.ResponseWriter, r *http.Request, vmid int) {
	if api.sandboxManager == nil {
		writeError(w, http.StatusInternalServerError, "sandbox manager unavailable")
		return
	}
	var req V1SandboxDestroyRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var err error
	if req.Force {
		err = api.sandboxManager.ForceDestroy(r.Context(), vmid)
	} else {
		err = api.sandboxManager.Destroy(r.Context(), vmid)
	}
	if err != nil {
		switch {
		case errors.Is(err, ErrSandboxNotFound):
			writeError(w, http.StatusNotFound, "sandbox not found")
		case errors.Is(err, ErrInvalidTransition):
			if api.store != nil {
				if sb, getErr := api.store.GetSandbox(r.Context(), vmid); getErr == nil {
					writeError(w, http.StatusConflict, fmt.Sprintf("cannot destroy sandbox in %s state. Valid states: STOPPED, DESTROYED. Use --force to bypass", sb.State))
				} else {
					writeError(w, http.StatusConflict, "invalid sandbox state for destroy operation. Use --force to bypass")
				}
			} else {
				writeError(w, http.StatusConflict, "invalid sandbox state. Use --force to bypass")
			}
		default:
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to destroy sandbox: %v", err))
		}
		return
	}
	sandbox, err := api.store.GetSandbox(r.Context(), vmid)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load sandbox")
		return
	}
	writeJSON(w, http.StatusOK, api.sandboxToV1(sandbox))
}

func (api *ControlAPI) handleSandboxStart(w http.ResponseWriter, r *http.Request, vmid int) {
	if api.sandboxManager == nil {
		writeError(w, http.StatusInternalServerError, "sandbox manager unavailable")
		return
	}
	if err := api.sandboxManager.Start(r.Context(), vmid); err != nil {
		switch {
		case errors.Is(err, ErrSandboxNotFound):
			writeError(w, http.StatusNotFound, "sandbox not found")
		case errors.Is(err, ErrInvalidTransition):
			if api.store != nil {
				if sb, getErr := api.store.GetSandbox(r.Context(), vmid); getErr == nil {
					writeError(w, http.StatusConflict, fmt.Sprintf("cannot start sandbox in %s state. Valid states: STOPPED", sb.State))
				} else {
					writeError(w, http.StatusConflict, "invalid sandbox state for start operation")
				}
			} else {
				writeError(w, http.StatusConflict, "invalid sandbox state for start operation")
			}
		default:
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to start sandbox: %v", err))
		}
		return
	}
	sandbox, err := api.store.GetSandbox(r.Context(), vmid)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load sandbox")
		return
	}
	writeJSON(w, http.StatusOK, api.sandboxToV1(sandbox))
}

func (api *ControlAPI) handleSandboxStop(w http.ResponseWriter, r *http.Request, vmid int) {
	if api.sandboxManager == nil {
		writeError(w, http.StatusInternalServerError, "sandbox manager unavailable")
		return
	}
	if err := api.sandboxManager.Stop(r.Context(), vmid); err != nil {
		switch {
		case errors.Is(err, ErrSandboxNotFound):
			writeError(w, http.StatusNotFound, "sandbox not found")
		case errors.Is(err, ErrInvalidTransition):
			if api.store != nil {
				if sb, getErr := api.store.GetSandbox(r.Context(), vmid); getErr == nil {
					writeError(w, http.StatusConflict, fmt.Sprintf("cannot stop sandbox in %s state. Valid states: READY, RUNNING", sb.State))
				} else {
					writeError(w, http.StatusConflict, "invalid sandbox state for stop operation")
				}
			} else {
				writeError(w, http.StatusConflict, "invalid sandbox state for stop operation")
			}
		default:
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to stop sandbox: %v", err))
		}
		return
	}
	sandbox, err := api.store.GetSandbox(r.Context(), vmid)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load sandbox")
		return
	}
	writeJSON(w, http.StatusOK, api.sandboxToV1(sandbox))
}

func (api *ControlAPI) handleSandboxStopAll(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, []string{http.MethodPost})
		return
	}
	if api.sandboxManager == nil {
		writeError(w, http.StatusInternalServerError, "sandbox manager unavailable")
		return
	}
	if api.store == nil {
		writeError(w, http.StatusInternalServerError, "sandbox store unavailable")
		return
	}
	var req V1SandboxStopAllRequest
	if err := decodeOptionalJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	forceUsed := req.Force
	ctx := r.Context()
	sandboxes, err := api.store.ListSandboxes(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list sandboxes")
		return
	}

	resp := V1SandboxStopAllResponse{
		Results: make([]V1SandboxStopAllResult, 0, len(sandboxes)),
	}
	for _, sb := range sandboxes {
		result := V1SandboxStopAllResult{
			VMID:    sb.VMID,
			Name:    sb.Name,
			Profile: sb.Profile,
			State:   string(sb.State),
			Result:  "skipped",
		}
		var stopErr error
		if sb.State == models.SandboxReady || sb.State == models.SandboxRunning {
			stopErr = api.sandboxManager.Stop(ctx, sb.VMID)
			if stopErr == nil {
				result.Result = "stopped"
				result.State = string(models.SandboxStopped)
			} else if !errors.Is(stopErr, ErrSandboxNotFound) && !errors.Is(stopErr, ErrInvalidTransition) {
				result.Result = "failed"
				result.Error = stopErr.Error()
			} else {
				result.Error = stopErr.Error()
			}
		}

		switch result.Result {
		case "stopped":
			resp.Stopped++
		case "failed":
			resp.Failed++
		default:
			resp.Skipped++
		}
		resp.Total++
		resp.Results = append(resp.Results, result)

		payload, _ := json.Marshal(map[string]any{
			"result":         result.Result,
			"state":          result.State,
			"previous_state": string(sb.State),
			"error":          result.Error,
		})
		msg := fmt.Sprintf("stop_all %s", result.Result)
		if result.Error != "" {
			msg = fmt.Sprintf("%s: %s", msg, result.Error)
		}
		_ = api.store.RecordEvent(ctx, "sandbox.stop_all.result", &sb.VMID, nil, msg, string(payload))
	}

	summaryPayload, _ := json.Marshal(map[string]any{
		"total":   resp.Total,
		"stopped": resp.Stopped,
		"skipped": resp.Skipped,
		"failed":  resp.Failed,
		"force":   forceUsed,
	})
	_ = api.store.RecordEvent(ctx, "sandbox.stop_all", nil, nil,
		fmt.Sprintf("stop_all completed: stopped=%d skipped=%d failed=%d", resp.Stopped, resp.Skipped, resp.Failed),
		string(summaryPayload),
	)

	writeJSON(w, http.StatusOK, resp)
}

func (api *ControlAPI) handleSandboxTouch(w http.ResponseWriter, r *http.Request, vmid int) {
	now := api.now().UTC()
	if err := api.store.UpdateSandboxLastUsed(r.Context(), vmid, now); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "sandbox not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to update sandbox usage")
		return
	}
	sandbox, err := api.store.GetSandbox(r.Context(), vmid)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "sandbox not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load sandbox")
		return
	}
	writeJSON(w, http.StatusOK, api.sandboxToV1(sandbox))
}

func (api *ControlAPI) handleSandboxRevert(w http.ResponseWriter, r *http.Request, vmid int) {
	if api.sandboxManager == nil {
		writeError(w, http.StatusInternalServerError, "sandbox manager unavailable")
		return
	}
	var req V1SandboxRevertRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	result, err := api.sandboxManager.Revert(r.Context(), vmid, RevertOptions{
		Force:   req.Force,
		Restart: req.Restart,
	})
	if err != nil {
		var inUse SandboxInUseError
		var missing SnapshotMissingError
		switch {
		case errors.Is(err, ErrSandboxNotFound):
			writeError(w, http.StatusNotFound, "sandbox not found")
		case errors.As(err, &inUse):
			msg := "sandbox has a running job; use --force to revert"
			if inUse.JobID != "" {
				msg = fmt.Sprintf("sandbox has running job %s; use --force to revert", inUse.JobID)
			}
			writeError(w, http.StatusConflict, msg)
		case errors.As(err, &missing), errors.Is(err, ErrSnapshotMissing):
			name := cleanSnapshotName
			if missing.Name != "" {
				name = missing.Name
			}
			writeError(w, http.StatusConflict, fmt.Sprintf("snapshot %q not found; reprovision the sandbox or create it via Proxmox (qm snapshot %d %s)", name, vmid, name))
		case errors.Is(err, ErrInvalidTransition):
			if api.store != nil {
				if sb, getErr := api.store.GetSandbox(r.Context(), vmid); getErr == nil {
					writeError(w, http.StatusConflict, fmt.Sprintf("cannot revert sandbox in %s state. Valid states: READY, RUNNING, STOPPED, COMPLETED, FAILED, TIMEOUT", sb.State))
				} else {
					writeError(w, http.StatusConflict, "invalid sandbox state for revert operation")
				}
			} else {
				writeError(w, http.StatusConflict, "invalid sandbox state for revert operation")
			}
		default:
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to revert sandbox: %v", err))
		}
		return
	}
	resp := V1SandboxRevertResponse{
		Sandbox:    api.sandboxToV1(result.Sandbox),
		Restarted:  result.Restarted,
		WasRunning: result.WasRunning,
		Snapshot:   result.Snapshot,
	}
	writeJSON(w, http.StatusOK, resp)
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
			if api.store != nil {
				if sb, getErr := api.store.GetSandbox(r.Context(), vmid); getErr == nil {
					writeError(w, http.StatusConflict, fmt.Sprintf("cannot renew lease in %s state. Valid states: RUNNING", sb.State))
				} else {
					writeError(w, http.StatusConflict, "sandbox lease not renewable")
				}
			} else {
				writeError(w, http.StatusConflict, "sandbox lease not renewable")
			}
		default:
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to renew lease: %v", err))
		}
		return
	}
	writeJSON(w, http.StatusOK, V1LeaseRenewResponse{
		VMID:         vmid,
		LeaseExpires: lease.UTC().Format(time.RFC3339Nano),
	})
}

func (api *ControlAPI) handleSandboxPrune(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, []string{http.MethodPost})
		return
	}
	if api.sandboxManager == nil {
		writeError(w, http.StatusInternalServerError, "sandbox manager unavailable")
		return
	}
	count, err := api.sandboxManager.PruneOrphans(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to prune sandboxes")
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"count": count})
}

func (api *ControlAPI) handleSandboxEvents(w http.ResponseWriter, r *http.Request, vmid int) {
	if api.store == nil {
		writeError(w, http.StatusInternalServerError, "event store unavailable")
		return
	}
	query := r.URL.Query()
	after, err := parseQueryInt64(query.Get("after"))
	if err != nil || after < 0 {
		writeError(w, http.StatusBadRequest, "invalid after")
		return
	}
	tail, err := parseQueryInt(query.Get("tail"))
	if err != nil || tail < 0 {
		writeError(w, http.StatusBadRequest, "invalid tail")
		return
	}
	limit, err := parseQueryInt(query.Get("limit"))
	if err != nil || limit < 0 {
		writeError(w, http.StatusBadRequest, "invalid limit")
		return
	}
	if tail > 0 && after > 0 {
		writeError(w, http.StatusBadRequest, "tail and after are mutually exclusive")
		return
	}
	if limit <= 0 {
		limit = defaultEventsLimit
	}
	if limit > maxEventsLimit {
		limit = maxEventsLimit
	}
	var events []db.Event
	if tail > 0 {
		if tail > maxEventsLimit {
			tail = maxEventsLimit
		}
		events, err = api.store.ListEventsBySandboxTail(r.Context(), vmid, tail)
	} else {
		events, err = api.store.ListEventsBySandbox(r.Context(), vmid, after, limit)
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load events")
		return
	}
	resp := V1EventsResponse{Events: make([]V1Event, 0, len(events))}
	var lastID int64
	for _, ev := range events {
		if ev.ID > lastID {
			lastID = ev.ID
		}
		resp.Events = append(resp.Events, eventToV1(ev))
	}
	if lastID > 0 {
		resp.LastID = lastID
	}
	writeJSON(w, http.StatusOK, resp)
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

func (api *ControlAPI) profile(name string) (models.Profile, bool) {
	if name == "" {
		return models.Profile{}, false
	}
	if api.profiles == nil {
		return models.Profile{}, false
	}
	profile, ok := api.profiles[name]
	return profile, ok
}

func jobToV1(job models.Job) V1JobResponse {
	resp := V1JobResponse{
		ID:          job.ID,
		RepoURL:     job.RepoURL,
		Ref:         job.Ref,
		Profile:     job.Profile,
		Task:        job.Task,
		Mode:        job.Mode,
		Keepalive:   job.Keepalive,
		WorkspaceID: job.WorkspaceID,
		SessionID:   job.SessionID,
		Status:      string(job.Status),
		CreatedAt:   job.CreatedAt.UTC().Format(time.RFC3339Nano),
		UpdatedAt:   job.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}
	if job.TTLMinutes > 0 {
		value := job.TTLMinutes
		resp.TTLMinutes = &value
	}
	if job.SandboxVMID != nil && *job.SandboxVMID > 0 {
		resp.SandboxVMID = job.SandboxVMID
	}
	if job.ResultJSON != "" {
		resp.Result = json.RawMessage(job.ResultJSON)
	}
	return resp
}

func (api *ControlAPI) sessionToV1(session models.Session) V1SessionResponse {
	resp := V1SessionResponse{
		ID:          session.ID,
		Name:        session.Name,
		WorkspaceID: session.WorkspaceID,
		CurrentVMID: session.CurrentVMID,
		Profile:     session.Profile,
		Branch:      session.Branch,
	}
	if !session.CreatedAt.IsZero() {
		resp.CreatedAt = session.CreatedAt.UTC().Format(time.RFC3339Nano)
	}
	if !session.UpdatedAt.IsZero() {
		resp.UpdatedAt = session.UpdatedAt.UTC().Format(time.RFC3339Nano)
	}
	return resp
}

func workspaceConflictDetails(workspace models.Workspace, waitSeconds int) error {
	attached := 0
	if workspace.AttachedVM != nil {
		attached = *workspace.AttachedVM
	}
	leaseOwner := strings.TrimSpace(workspace.LeaseOwner)
	leaseExpires := ""
	if !workspace.LeaseExpires.IsZero() {
		leaseExpires = workspace.LeaseExpires.UTC().Format(time.RFC3339Nano)
	}
	if waitSeconds > 0 {
		return fmt.Errorf("workspace_id=%s workspace_name=%s attached_vmid=%d lease_owner=%s lease_expires_at=%s workspace_wait_seconds=%d", workspace.ID, workspace.Name, attached, leaseOwner, leaseExpires, waitSeconds)
	}
	return fmt.Errorf("workspace_id=%s workspace_name=%s attached_vmid=%d lease_owner=%s lease_expires_at=%s", workspace.ID, workspace.Name, attached, leaseOwner, leaseExpires)
}

func (api *ControlAPI) resolveSession(ctx context.Context, idOrName string) (models.Session, error) {
	if api == nil || api.store == nil {
		return models.Session{}, errors.New("session store unavailable")
	}
	idOrName = strings.TrimSpace(idOrName)
	if idOrName == "" {
		return models.Session{}, errors.New("session id is required")
	}
	session, err := api.store.GetSession(ctx, idOrName)
	if err == nil {
		return session, nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return models.Session{}, err
	}
	session, err = api.store.GetSessionByName(ctx, idOrName)
	if err != nil {
		return models.Session{}, err
	}
	return session, nil
}

func (api *ControlAPI) acquireWorkspaceLease(ctx context.Context, workspace models.Workspace, owner string, ttl time.Duration, waitSeconds int) (string, time.Duration, error) {
	if api == nil || api.store == nil {
		return "", 0, errors.New("workspace lease store unavailable")
	}
	owner = strings.TrimSpace(owner)
	if owner == "" {
		return "", 0, errors.New("workspace lease owner required")
	}
	if ttl <= 0 {
		ttl = workspaceLeaseDefaultTTL
	}
	start := api.now().UTC()
	var deadline time.Time
	if waitSeconds > 0 {
		deadline = start.Add(time.Duration(waitSeconds) * time.Second)
	}
	backoff := workspaceWaitPollInterval
	for {
		nonce, err := newWorkspaceLeaseNonce(nil)
		if err != nil {
			return "", 0, err
		}
		expiresAt := api.now().UTC().Add(ttl)
		acquired, err := api.store.TryAcquireWorkspaceLease(ctx, workspace.ID, owner, nonce, expiresAt)
		if err != nil {
			return "", 0, err
		}
		if acquired {
			waited := api.now().UTC().Sub(start)
			if waited > 0 && waitSeconds > 0 && api.metrics != nil {
				api.metrics.ObserveWorkspaceLeaseWait(waited)
			}
			return nonce, waited, nil
		}
		if api.metrics != nil {
			api.metrics.IncWorkspaceLeaseContention()
		}
		if waitSeconds <= 0 {
			return "", api.now().UTC().Sub(start), ErrWorkspaceLeaseHeld
		}
		if !deadline.IsZero() && api.now().UTC().After(deadline) {
			return "", api.now().UTC().Sub(start), errWorkspaceWaitTimeout
		}
		select {
		case <-ctx.Done():
			return "", api.now().UTC().Sub(start), ctx.Err()
		case <-time.After(backoff):
		}
		backoff *= 2
		if backoff > 2*time.Second {
			backoff = 2 * time.Second
		}
	}
}

func (api *ControlAPI) sandboxToV1(sb models.Sandbox) V1SandboxResponse {
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
	if !sb.LastUsedAt.IsZero() {
		value := sb.LastUsedAt.UTC().Format(time.RFC3339Nano)
		resp.LastUsedAt = &value
	}
	if profile, ok := api.profile(sb.Profile); ok {
		resp.Network = profileNetworkToV1(profile)
	}
	return resp
}

func profileNetworkToV1(profile models.Profile) *V1SandboxNetwork {
	spec, err := parseProfileProvisionSpec(profile.RawYAML)
	if err != nil {
		return nil
	}
	mode, err := resolveNetworkMode(spec.Network)
	if err != nil {
		return nil
	}
	group, err := resolveFirewallGroup(spec.Network)
	if err != nil {
		return nil
	}
	network := V1SandboxNetwork{
		Mode: mode,
	}
	if spec.Network.Firewall != nil {
		network.Firewall = spec.Network.Firewall
	}
	if group != "" {
		network.FirewallGroup = group
	}
	if network.Firewall == nil && group != "" {
		value := true
		network.Firewall = &value
	}
	return &network
}

func profileToV1(profile models.Profile) V1Profile {
	resp := V1Profile{
		Name:         profile.Name,
		TemplateVMID: profile.TemplateVM,
	}
	if !profile.UpdatedAt.IsZero() {
		resp.UpdatedAt = profile.UpdatedAt.UTC().Format(time.RFC3339Nano)
	}
	return resp
}

func workspaceToV1(ws models.Workspace) V1WorkspaceResponse {
	resp := V1WorkspaceResponse{
		ID:        ws.ID,
		Name:      ws.Name,
		Storage:   ws.Storage,
		VolumeID:  ws.VolumeID,
		SizeGB:    ws.SizeGB,
		CreatedAt: ws.CreatedAt.UTC().Format(time.RFC3339Nano),
		UpdatedAt: ws.LastUpdated.UTC().Format(time.RFC3339Nano),
	}
	if ws.AttachedVM != nil && *ws.AttachedVM > 0 {
		value := *ws.AttachedVM
		resp.AttachedVMID = &value
	}
	return resp
}

func workspaceSnapshotToV1(snapshot models.WorkspaceSnapshot) V1WorkspaceSnapshotResponse {
	return V1WorkspaceSnapshotResponse{
		WorkspaceID: snapshot.WorkspaceID,
		Name:        snapshot.Name,
		BackendRef:  snapshot.BackendRef,
		CreatedAt:   snapshot.CreatedAt.UTC().Format(time.RFC3339Nano),
	}
}

func workspaceCheckToV1(result WorkspaceCheckResult) V1WorkspaceCheckResponse {
	resp := V1WorkspaceCheckResponse{
		Workspace: workspaceToV1(result.Workspace),
		Volume: V1WorkspaceCheckVolume{
			VolumeID: result.Volume.VolumeID,
			Storage:  result.Volume.Storage,
			Path:     result.Volume.Path,
			Exists:   result.Volume.Exists,
		},
		Findings:  make([]V1WorkspaceCheckFinding, 0, len(result.Findings)),
		CheckedAt: result.CheckedAt.UTC().Format(time.RFC3339Nano),
	}
	for _, finding := range result.Findings {
		resp.Findings = append(resp.Findings, V1WorkspaceCheckFinding{
			Code:        finding.Code,
			Severity:    finding.Severity,
			Message:     finding.Message,
			Details:     finding.Details,
			Remediation: workspaceRemediationToV1(finding.Remediation),
		})
	}
	return resp
}

func workspaceRemediationToV1(remediation []WorkspaceCheckRemediation) []V1WorkspaceCheckRemediation {
	if len(remediation) == 0 {
		return nil
	}
	out := make([]V1WorkspaceCheckRemediation, 0, len(remediation))
	for _, item := range remediation {
		out = append(out, V1WorkspaceCheckRemediation{
			Action:  item.Action,
			Command: item.Command,
			Note:    item.Note,
		})
	}
	return out
}

func exposureToV1(exposure db.Exposure) V1Exposure {
	resp := V1Exposure{
		Name:     exposure.Name,
		VMID:     exposure.VMID,
		Port:     exposure.Port,
		TargetIP: exposure.TargetIP,
		URL:      exposure.URL,
		State:    exposure.State,
	}
	if !exposure.CreatedAt.IsZero() {
		resp.CreatedAt = exposure.CreatedAt.UTC().Format(time.RFC3339Nano)
	}
	if !exposure.UpdatedAt.IsZero() {
		resp.UpdatedAt = exposure.UpdatedAt.UTC().Format(time.RFC3339Nano)
	}
	return resp
}

func formatSandboxCounts(counts map[models.SandboxState]int) map[string]int {
	out := make(map[string]int, len(statusSandboxStates))
	for _, state := range statusSandboxStates {
		out[string(state)] = 0
	}
	for state, count := range counts {
		out[string(state)] = count
	}
	return out
}

func formatJobCounts(counts map[models.JobStatus]int) map[string]int {
	out := make(map[string]int, len(statusJobStatuses))
	for _, status := range statusJobStatuses {
		out[string(status)] = 0
	}
	for status, count := range counts {
		out[string(status)] = count
	}
	return out
}

func formatNetworkModeCounts(counts map[string]int) map[string]int {
	out := make(map[string]int, len(networkModeOrder))
	for _, mode := range networkModeOrder {
		out[mode] = 0
	}
	for mode, count := range counts {
		out[mode] = count
	}
	return out
}

func (api *ControlAPI) buildNetworkModeCounts(ctx context.Context) map[string]int {
	out := make(map[string]int, len(networkModeOrder))
	for _, mode := range networkModeOrder {
		out[mode] = 0
	}
	if api == nil || api.store == nil {
		return out
	}
	sandboxes, err := api.store.ListSandboxes(ctx)
	if err != nil {
		api.logger.Printf("status: list sandboxes for network modes: %v", err)
		return out
	}
	profileModes := buildProfileNetworkModes(api.profiles)
	for _, sb := range sandboxes {
		mode := profileModes[sb.Profile]
		if mode == "" {
			mode = defaultNetworkMode
		}
		out[mode]++
	}
	return out
}

func buildProfileNetworkModes(profiles map[string]models.Profile) map[string]string {
	out := make(map[string]string, len(profiles))
	for name, profile := range profiles {
		mode := defaultNetworkMode
		spec, err := parseProfileProvisionSpec(profile.RawYAML)
		if err == nil {
			if resolved, err := resolveNetworkMode(spec.Network); err == nil && resolved != "" {
				mode = resolved
			}
		}
		out[name] = mode
	}
	return out
}

func buildArtifactStatus(root string) V1StatusArtifacts {
	root = strings.TrimSpace(root)
	status := V1StatusArtifacts{
		Root: root,
	}
	if root == "" {
		status.Error = "artifact root not configured"
		return status
	}
	total, free, err := statFSBytes(root)
	if err != nil {
		status.Error = err.Error()
		return status
	}
	status.TotalBytes = total
	status.FreeBytes = free
	if total >= free {
		status.UsedBytes = total - free
	}
	return status
}

func statFSBytes(path string) (uint64, uint64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, 0, err
	}
	blockSize := uint64(stat.Bsize)
	total := uint64(stat.Blocks) * blockSize
	free := uint64(stat.Bavail) * blockSize
	return total, free, nil
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

func decodeOptionalJSON(w http.ResponseWriter, r *http.Request, dest any) error {
	if r.Body == nil || r.Body == http.NoBody {
		return nil
	}
	defer r.Body.Close()
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBytes)
	data, err := io.ReadAll(r.Body)
	if err != nil {
		return err
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(data))
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

func writeError(w http.ResponseWriter, status int, msg string, err ...error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	payload := map[string]string{"error": msg}
	// Always include error details for better debugging
	if len(err) > 0 {
		details := err[0].Error()
		payload["details"] = details
	}
	data, _ := json.Marshal(payload)
	w.Write(data)
}

func writeMethodNotAllowed(w http.ResponseWriter, methods []string) {
	w.Header().Set("Allow", strings.Join(methods, ", "))
	writeError(w, http.StatusMethodNotAllowed, "method not allowed")
}

func newSessionID() (string, error) {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return "session_" + hex.EncodeToString(buf), nil
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

func parseQueryInt(value string) (int, error) {
	if strings.TrimSpace(value) == "" {
		return 0, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, err
	}
	return parsed, nil
}

func parseQueryInt64(value string) (int64, error) {
	if strings.TrimSpace(value) == "" {
		return 0, nil
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, err
	}
	return parsed, nil
}

func parseMessageScope(scopeType, scopeID string) (string, string, error) {
	scopeType = strings.ToLower(strings.TrimSpace(scopeType))
	if scopeType == "" {
		return "", "", errors.New("scope_type is required")
	}
	if _, ok := messageScopeTypes[scopeType]; !ok {
		return "", "", errors.New("invalid scope_type")
	}
	scopeID = strings.TrimSpace(scopeID)
	if scopeID == "" {
		return "", "", errors.New("scope_id is required")
	}
	return scopeType, scopeID, nil
}

func eventToV1(ev db.Event) V1Event {
	resp := V1Event{
		ID:        ev.ID,
		Kind:      ev.Kind,
		Timestamp: ev.Timestamp.UTC().Format(time.RFC3339Nano),
		Message:   strings.TrimSpace(ev.Message),
	}
	if ev.SandboxVMID != nil {
		resp.SandboxVMID = ev.SandboxVMID
	}
	if ev.JobID != nil {
		resp.JobID = *ev.JobID
	}
	if strings.TrimSpace(ev.JSON) != "" {
		payload := []byte(ev.JSON)
		if !json.Valid(payload) {
			payload, _ = json.Marshal(ev.JSON)
		}
		resp.Payload = json.RawMessage(payload)
	}
	if resp.Message == "" {
		resp.Message = ""
	}
	return resp
}

func messageToV1(msg db.Message) V1Message {
	resp := V1Message{
		ID:        msg.ID,
		ScopeType: msg.ScopeType,
		ScopeID:   msg.ScopeID,
		Author:    msg.Author,
		Kind:      msg.Kind,
		Text:      msg.Text,
	}
	if !msg.Timestamp.IsZero() {
		resp.Timestamp = msg.Timestamp.UTC().Format(time.RFC3339Nano)
	}
	if strings.TrimSpace(msg.JSON) != "" {
		payload := []byte(msg.JSON)
		if !json.Valid(payload) {
			payload, _ = json.Marshal(msg.JSON)
		}
		resp.Payload = json.RawMessage(payload)
	}
	return resp
}

func artifactToV1(artifact db.Artifact) V1Artifact {
	resp := V1Artifact{
		Name:      artifact.Name,
		Path:      artifact.Path,
		SizeBytes: artifact.SizeBytes,
		Sha256:    artifact.Sha256,
		MIME:      artifact.MIME,
	}
	if !artifact.CreatedAt.IsZero() {
		resp.CreatedAt = artifact.CreatedAt.UTC().Format(time.RFC3339Nano)
	}
	return resp
}

func validateArtifactName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("artifact name is required")
	}
	if strings.ContainsAny(name, "/\\") {
		return errors.New("artifact name must not contain path separators")
	}
	return nil
}
