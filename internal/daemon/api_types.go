package daemon

import "encoding/json"

type V1ErrorResponse struct {
	Error   string `json:"error"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
	Details string `json:"details,omitempty"`
}

type V1StatusArtifacts struct {
	Root       string `json:"root,omitempty"`
	TotalBytes uint64 `json:"total_bytes,omitempty"`
	FreeBytes  uint64 `json:"free_bytes,omitempty"`
	UsedBytes  uint64 `json:"used_bytes,omitempty"`
	Error      string `json:"error,omitempty"`
}

type V1StatusMetrics struct {
	Enabled bool `json:"enabled"`
}

type V1SkillBundle struct {
	Name    string `json:"name,omitempty"`
	Version string `json:"version,omitempty"`
}

type V1StatusResponse struct {
	APISchemaVersion    int                               `json:"api_schema_version"`
	EventSchemaVersion  int                               `json:"event_schema_version"`
	Sandboxes           map[string]int                    `json:"sandboxes"`
	Jobs                map[string]int                    `json:"jobs"`
	NetworkModes        map[string]int                    `json:"network_modes,omitempty"`
	Artifacts           V1StatusArtifacts                 `json:"artifacts"`
	Metrics             V1StatusMetrics                   `json:"metrics"`
	SkillBundle         V1SkillBundle                     `json:"skill_bundle"`
	RecentFailures      []V1Event                         `json:"recent_failures"`
	SandboxHealth       map[int]V1SandboxLifecycleSummary `json:"sandbox_health"`
	JobTimelines        map[string]V1JobTimelineSummary   `json:"job_timelines"`
	RecentFailureDigest []V1FailureDigest                 `json:"recent_failure_digest"`
}

type V1FailureDigest struct {
	EventID     int64  `json:"event_id"`
	Timestamp   string `json:"ts"`
	Kind        string `json:"kind"`
	Schema      int    `json:"schema,omitempty"`
	Stage       string `json:"stage,omitempty"`
	SandboxVMID *int   `json:"sandbox_vmid,omitempty"`
	JobID       string `json:"job_id,omitempty"`
	Error       string `json:"error,omitempty"`
	Message     string `json:"message,omitempty"`
}

type V1SandboxLifecycleSummary struct {
	VMID               int    `json:"vmid"`
	State              string `json:"state"`
	Healthy            bool   `json:"healthy"`
	LastEventID        int64  `json:"last_event_id"`
	LastEventKind      string `json:"last_event_kind,omitempty"`
	LastEventAt        string `json:"last_event_at,omitempty"`
	FailureCount       int    `json:"failure_count"`
	LastFailureAt      string `json:"last_failure_at,omitempty"`
	LastFailureKind    string `json:"last_failure_kind,omitempty"`
	LastFailureMessage string `json:"last_failure_message,omitempty"`
}

type V1JobTimelineSummary struct {
	JobID              string `json:"job_id"`
	Status             string `json:"status"`
	StartedAt          string `json:"started_at,omitempty"`
	CompletedAt        string `json:"completed_at,omitempty"`
	EventCount         int    `json:"event_count"`
	FailureCount       int    `json:"failure_count"`
	LastEventID        int64  `json:"last_event_id"`
	LastEventKind      string `json:"last_event_kind,omitempty"`
	LastEventAt        string `json:"last_event_at,omitempty"`
	LastFailureAt      string `json:"last_failure_at,omitempty"`
	LastFailureKind    string `json:"last_failure_kind,omitempty"`
	LastFailureMessage string `json:"last_failure_message,omitempty"`
}

type V1HostResponse struct {
	Version      string `json:"version"`
	AgentSubnet  string `json:"agent_subnet,omitempty"`
	TailscaleDNS string `json:"tailscale_dns,omitempty"`
}

type V1BootstrapFetchRequest struct {
	Token string `json:"token"`
	VMID  int    `json:"vmid"`
}

type V1BootstrapGit struct {
	Token         string `json:"token,omitempty"`
	Username      string `json:"username,omitempty"`
	SSHPrivateKey string `json:"ssh_private_key,omitempty"`
	SSHPublicKey  string `json:"ssh_public_key,omitempty"`
	KnownHosts    string `json:"known_hosts,omitempty"`
}

type V1BootstrapArtifact struct {
	Endpoint string `json:"endpoint,omitempty"`
	Token    string `json:"token,omitempty"`
}

type V1BootstrapJob struct {
	ID         string `json:"id"`
	RepoURL    string `json:"repo_url"`
	Ref        string `json:"ref"`
	Task       string `json:"task,omitempty"`
	Mode       string `json:"mode,omitempty"`
	Profile    string `json:"profile,omitempty"`
	Keepalive  bool   `json:"keepalive,omitempty"`
	TTLMinutes *int   `json:"ttl_minutes,omitempty"`
}

type V1BootstrapPolicy struct {
	Mode             string   `json:"mode,omitempty"`
	InnerSandbox     string   `json:"inner_sandbox,omitempty"`
	InnerSandboxArgs []string `json:"inner_sandbox_args,omitempty"`
}

type V1BootstrapFetchResponse struct {
	Job                V1BootstrapJob       `json:"job"`
	Git                *V1BootstrapGit      `json:"git,omitempty"`
	Env                map[string]string    `json:"env,omitempty"`
	ClaudeSettingsJSON string               `json:"claude_settings_json,omitempty"`
	Artifact           *V1BootstrapArtifact `json:"artifact,omitempty"`
	Policy             *V1BootstrapPolicy   `json:"policy,omitempty"`
}

type V1PreflightIssue struct {
	Code    string `json:"code"`
	Field   string `json:"field"`
	Message string `json:"message"`
}

type V1JobCreateRequest struct {
	RepoURL              string                    `json:"repo_url"`
	Ref                  string                    `json:"ref"`
	Profile              string                    `json:"profile"`
	Task                 string                    `json:"task"`
	Mode                 string                    `json:"mode"`
	TTLMinutes           *int                      `json:"ttl_minutes,omitempty"`
	Keepalive            *bool                     `json:"keepalive,omitempty"`
	WorkspaceID          *string                   `json:"workspace_id,omitempty"`
	WorkspaceCreate      *V1WorkspaceCreateRequest `json:"workspace_create,omitempty"`
	WorkspaceWaitSeconds *int                      `json:"workspace_wait_seconds,omitempty"`
	SessionID            *string                   `json:"session_id,omitempty"`
}

type V1JobValidatePlanRequest struct {
	RepoURL              string                    `json:"repo_url"`
	Ref                  string                    `json:"ref"`
	Profile              string                    `json:"profile"`
	Task                 string                    `json:"task"`
	Mode                 string                    `json:"mode"`
	TTLMinutes           *int                      `json:"ttl_minutes,omitempty"`
	Keepalive            *bool                     `json:"keepalive,omitempty"`
	WorkspaceID          *string                   `json:"workspace_id,omitempty"`
	WorkspaceCreate      *V1WorkspaceCreateRequest `json:"workspace_create,omitempty"`
	WorkspaceWaitSeconds *int                      `json:"workspace_wait_seconds,omitempty"`
	SessionID            *string                   `json:"session_id,omitempty"`
}

type V1JobValidatePlan struct {
	RepoURL              string                    `json:"repo_url"`
	Ref                  string                    `json:"ref"`
	Profile              string                    `json:"profile"`
	Task                 string                    `json:"task"`
	Mode                 string                    `json:"mode"`
	TTLMinutes           *int                      `json:"ttl_minutes,omitempty"`
	Keepalive            bool                      `json:"keepalive"`
	WorkspaceID          *string                   `json:"workspace_id,omitempty"`
	WorkspaceCreate      *V1WorkspaceCreateRequest `json:"workspace_create,omitempty"`
	WorkspaceWaitSeconds *int                      `json:"workspace_wait_seconds,omitempty"`
	SessionID            *string                   `json:"session_id,omitempty"`
}

type V1JobValidatePlanResponse struct {
	OK       bool               `json:"ok"`
	Errors   []V1PreflightIssue `json:"errors"`
	Warnings []V1PreflightIssue `json:"warnings"`
	Plan     *V1JobValidatePlan `json:"plan,omitempty"`
}

type V1JobResponse struct {
	ID          string                `json:"id"`
	RepoURL     string                `json:"repo_url"`
	Ref         string                `json:"ref"`
	Profile     string                `json:"profile"`
	Task        string                `json:"task,omitempty"`
	Mode        string                `json:"mode,omitempty"`
	TTLMinutes  *int                  `json:"ttl_minutes,omitempty"`
	Keepalive   bool                  `json:"keepalive"`
	WorkspaceID *string               `json:"workspace_id,omitempty"`
	SessionID   *string               `json:"session_id,omitempty"`
	Status      string                `json:"status"`
	SandboxVMID *int                  `json:"sandbox_vmid,omitempty"`
	Result      json.RawMessage       `json:"result,omitempty"`
	Events      []V1Event             `json:"events,omitempty"`
	Timeline    *V1JobTimelineSummary `json:"timeline,omitempty"`
	CreatedAt   string                `json:"created_at"`
	UpdatedAt   string                `json:"updated_at"`
}

type V1SandboxCreateRequest struct {
	Name       string  `json:"name"`
	Profile    string  `json:"profile"`
	Keepalive  *bool   `json:"keepalive,omitempty"`
	TTLMinutes *int    `json:"ttl_minutes,omitempty"`
	Workspace  *string `json:"workspace_id,omitempty"`
	VMID       *int    `json:"vmid,omitempty"`
	JobID      string  `json:"job_id,omitempty"`
}

type V1SandboxValidatePlanRequest struct {
	Name       string  `json:"name"`
	Profile    string  `json:"profile"`
	Keepalive  *bool   `json:"keepalive,omitempty"`
	TTLMinutes *int    `json:"ttl_minutes,omitempty"`
	Workspace  *string `json:"workspace_id,omitempty"`
	VMID       *int    `json:"vmid,omitempty"`
	JobID      string  `json:"job_id,omitempty"`
}

type V1SandboxValidatePlan struct {
	Name       string  `json:"name"`
	Profile    string  `json:"profile"`
	Keepalive  bool    `json:"keepalive"`
	TTLMinutes *int    `json:"ttl_minutes,omitempty"`
	Workspace  *string `json:"workspace_id,omitempty"`
	VMID       *int    `json:"vmid,omitempty"`
	JobID      string  `json:"job_id,omitempty"`
}

type V1SandboxValidatePlanResponse struct {
	OK       bool                   `json:"ok"`
	Errors   []V1PreflightIssue     `json:"errors"`
	Warnings []V1PreflightIssue     `json:"warnings"`
	Plan     *V1SandboxValidatePlan `json:"plan,omitempty"`
}

type V1SandboxDestroyRequest struct {
	Force bool `json:"force"`
}

type V1SandboxRevertRequest struct {
	Force   bool  `json:"force"`
	Restart *bool `json:"restart,omitempty"`
}

type V1SandboxSnapshotCreateRequest struct {
	Name  string `json:"name"`
	Force bool   `json:"force,omitempty"`
}

type V1SandboxSnapshotRestoreRequest struct {
	Force bool `json:"force,omitempty"`
}

type V1SandboxResponse struct {
	VMID          int                        `json:"vmid"`
	Name          string                     `json:"name"`
	Profile       string                     `json:"profile"`
	State         string                     `json:"state"`
	IP            string                     `json:"ip,omitempty"`
	WorkspaceID   *string                    `json:"workspace_id,omitempty"`
	Network       *V1SandboxNetwork          `json:"network,omitempty"`
	Keepalive     bool                       `json:"keepalive"`
	LeaseExpires  *string                    `json:"lease_expires_at,omitempty"`
	LastUsedAt    *string                    `json:"last_used_at,omitempty"`
	Health        *V1SandboxLifecycleSummary `json:"health,omitempty"`
	CreatedAt     string                     `json:"created_at"`
	LastUpdatedAt string                     `json:"updated_at"`
}

type V1SandboxNetwork struct {
	Mode          string `json:"mode,omitempty"`
	Firewall      *bool  `json:"firewall,omitempty"`
	FirewallGroup string `json:"firewall_group,omitempty"`
}

type V1SandboxesResponse struct {
	Sandboxes []V1SandboxResponse `json:"sandboxes"`
}

type V1Profile struct {
	Name         string `json:"name"`
	TemplateVMID int    `json:"template_vmid"`
	UpdatedAt    string `json:"updated_at"`
}

type V1ProfilesResponse struct {
	Profiles []V1Profile `json:"profiles"`
}

type V1SandboxRevertResponse struct {
	Sandbox    V1SandboxResponse `json:"sandbox"`
	Restarted  bool              `json:"restarted"`
	WasRunning bool              `json:"was_running"`
	Snapshot   string            `json:"snapshot"`
}

type V1SandboxSnapshotResponse struct {
	VMID      int    `json:"vmid"`
	Name      string `json:"name"`
	CreatedAt string `json:"created_at,omitempty"`
}

type V1SandboxSnapshotsResponse struct {
	Snapshots []V1SandboxSnapshotResponse `json:"snapshots"`
}

type V1SandboxStopAllResult struct {
	VMID    int    `json:"vmid"`
	Name    string `json:"name,omitempty"`
	Profile string `json:"profile,omitempty"`
	State   string `json:"state"`
	Result  string `json:"result"`
	Error   string `json:"error,omitempty"`
}

type V1SandboxStopAllResponse struct {
	Total   int                      `json:"total"`
	Stopped int                      `json:"stopped"`
	Skipped int                      `json:"skipped"`
	Failed  int                      `json:"failed"`
	Results []V1SandboxStopAllResult `json:"results"`
}

type V1SandboxStopAllRequest struct {
	Force bool `json:"force,omitempty"`
}

type V1LeaseRenewRequest struct {
	TTLMinutes int `json:"ttl_minutes"`
}

type V1LeaseRenewResponse struct {
	VMID         int    `json:"vmid"`
	LeaseExpires string `json:"lease_expires_at"`
}

type V1WorkspaceCreateRequest struct {
	Name    string `json:"name"`
	SizeGB  int    `json:"size_gb"`
	Storage string `json:"storage,omitempty"`
}

type V1WorkspaceForkRequest struct {
	Name         string `json:"name"`
	FromSnapshot string `json:"from_snapshot,omitempty"`
}

type V1WorkspaceAttachRequest struct {
	VMID int `json:"vmid"`
}

type V1WorkspaceRebindRequest struct {
	Profile    string `json:"profile"`
	TTLMinutes *int   `json:"ttl_minutes,omitempty"`
	KeepOld    bool   `json:"keep_old,omitempty"`
}

type V1WorkspaceResponse struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Storage      string `json:"storage"`
	VolumeID     string `json:"volid"`
	SizeGB       int    `json:"size_gb"`
	AttachedVMID *int   `json:"attached_vmid,omitempty"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
}

type V1WorkspacesResponse struct {
	Workspaces []V1WorkspaceResponse `json:"workspaces"`
}

type V1WorkspaceSnapshotCreateRequest struct {
	Name string `json:"name"`
}

type V1WorkspaceSnapshotResponse struct {
	WorkspaceID string `json:"workspace_id"`
	Name        string `json:"name"`
	BackendRef  string `json:"backend_ref"`
	CreatedAt   string `json:"created_at"`
}

type V1WorkspaceSnapshotsResponse struct {
	Snapshots []V1WorkspaceSnapshotResponse `json:"snapshots"`
}

type V1WorkspaceCheckVolume struct {
	VolumeID string `json:"volid"`
	Storage  string `json:"storage,omitempty"`
	Path     string `json:"path,omitempty"`
	Exists   bool   `json:"exists"`
}

type V1WorkspaceCheckRemediation struct {
	Action  string `json:"action"`
	Command string `json:"command,omitempty"`
	Note    string `json:"note,omitempty"`
}

type V1WorkspaceCheckFinding struct {
	Code        string                        `json:"code"`
	Severity    string                        `json:"severity"`
	Message     string                        `json:"message"`
	Details     map[string]string             `json:"details,omitempty"`
	Remediation []V1WorkspaceCheckRemediation `json:"remediation,omitempty"`
}

type V1WorkspaceCheckResponse struct {
	Workspace V1WorkspaceResponse       `json:"workspace"`
	Volume    V1WorkspaceCheckVolume    `json:"volume"`
	Findings  []V1WorkspaceCheckFinding `json:"findings"`
	CheckedAt string                    `json:"checked_at"`
}

type V1WorkspaceFSCKRequest struct {
	Repair bool `json:"repair,omitempty"`
}

type V1WorkspaceFSCKVolume struct {
	VolumeID string `json:"volid"`
	Storage  string `json:"storage,omitempty"`
	Path     string `json:"path,omitempty"`
}

type V1WorkspaceFSCKResponse struct {
	Workspace      V1WorkspaceResponse   `json:"workspace"`
	Volume         V1WorkspaceFSCKVolume `json:"volume"`
	Method         string                `json:"method"`
	Mode           string                `json:"mode"`
	Status         string                `json:"status"`
	ExitCode       int                   `json:"exit_code"`
	ExitSummary    string                `json:"exit_summary,omitempty"`
	NeedsRepair    bool                  `json:"needs_repair,omitempty"`
	RebootRequired bool                  `json:"reboot_required,omitempty"`
	Command        string                `json:"command,omitempty"`
	Output         string                `json:"output,omitempty"`
	StartedAt      string                `json:"started_at"`
	CompletedAt    string                `json:"completed_at"`
}

type V1WorkspaceRebindResponse struct {
	Workspace V1WorkspaceResponse `json:"workspace"`
	Sandbox   V1SandboxResponse   `json:"sandbox"`
	OldVMID   *int                `json:"old_vmid,omitempty"`
}

type V1SessionCreateRequest struct {
	Name            string                    `json:"name"`
	Profile         string                    `json:"profile"`
	WorkspaceID     *string                   `json:"workspace_id,omitempty"`
	WorkspaceCreate *V1WorkspaceCreateRequest `json:"workspace_create,omitempty"`
	Branch          string                    `json:"branch,omitempty"`
}

type V1SessionResponse struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	WorkspaceID string `json:"workspace_id"`
	CurrentVMID *int   `json:"current_vmid,omitempty"`
	Profile     string `json:"profile"`
	Branch      string `json:"branch,omitempty"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

type V1SessionsResponse struct {
	Sessions []V1SessionResponse `json:"sessions"`
}

type V1SessionResumeResponse struct {
	Session   V1SessionResponse   `json:"session"`
	Workspace V1WorkspaceResponse `json:"workspace"`
	Sandbox   V1SandboxResponse   `json:"sandbox"`
	OldVMID   *int                `json:"old_vmid,omitempty"`
}

type V1SessionForkRequest struct {
	Name            string                    `json:"name"`
	Profile         string                    `json:"profile,omitempty"`
	WorkspaceID     *string                   `json:"workspace_id,omitempty"`
	WorkspaceCreate *V1WorkspaceCreateRequest `json:"workspace_create,omitempty"`
	Branch          string                    `json:"branch,omitempty"`
}

type V1ExposureCreateRequest struct {
	Name     string `json:"name"`
	VMID     int    `json:"vmid"`
	Port     int    `json:"port"`
	Force    bool   `json:"force,omitempty"`
	TargetIP string `json:"target_ip,omitempty"`
	URL      string `json:"url,omitempty"`
	State    string `json:"state,omitempty"`
}

type V1Exposure struct {
	Name      string `json:"name"`
	VMID      int    `json:"vmid"`
	Port      int    `json:"port"`
	TargetIP  string `json:"target_ip"`
	URL       string `json:"url,omitempty"`
	State     string `json:"state"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

type V1ExposuresResponse struct {
	Exposures []V1Exposure `json:"exposures"`
}

type V1Event struct {
	ID          int64           `json:"id"`
	Timestamp   string          `json:"ts"`
	Kind        string          `json:"kind"`
	Schema      int             `json:"schema_version,omitempty"`
	Stage       string          `json:"stage,omitempty"`
	SandboxVMID *int            `json:"sandbox_vmid,omitempty"`
	JobID       string          `json:"job_id,omitempty"`
	Message     string          `json:"msg,omitempty"`
	Payload     json.RawMessage `json:"json,omitempty"`
}

type V1EventsResponse struct {
	Events []V1Event `json:"events"`
	LastID int64     `json:"last_id,omitempty"`
}

type V1MessageCreateRequest struct {
	ScopeType string          `json:"scope_type"`
	ScopeID   string          `json:"scope_id"`
	Author    string          `json:"author,omitempty"`
	Kind      string          `json:"kind,omitempty"`
	Text      string          `json:"text,omitempty"`
	Payload   json.RawMessage `json:"json,omitempty"`
}

type V1Message struct {
	ID        int64           `json:"id"`
	Timestamp string          `json:"ts"`
	ScopeType string          `json:"scope_type"`
	ScopeID   string          `json:"scope_id"`
	Author    string          `json:"author,omitempty"`
	Kind      string          `json:"kind,omitempty"`
	Text      string          `json:"text,omitempty"`
	Payload   json.RawMessage `json:"json,omitempty"`
}

type V1MessagesResponse struct {
	Messages []V1Message `json:"messages"`
	LastID   int64       `json:"last_id,omitempty"`
}

type V1ArtifactMetadata struct {
	Name      string `json:"name"`
	Path      string `json:"path,omitempty"`
	SizeBytes int64  `json:"size_bytes,omitempty"`
	Sha256    string `json:"sha256,omitempty"`
	MIME      string `json:"mime,omitempty"`
}

type V1Artifact struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	SizeBytes int64  `json:"size_bytes"`
	Sha256    string `json:"sha256"`
	MIME      string `json:"mime,omitempty"`
	CreatedAt string `json:"created_at"`
}

type V1ArtifactsResponse struct {
	JobID     string       `json:"job_id"`
	Artifacts []V1Artifact `json:"artifacts"`
}

type V1ArtifactUploadResponse struct {
	JobID    string             `json:"job_id"`
	Artifact V1ArtifactMetadata `json:"artifact"`
}

type V1RunnerReportRequest struct {
	JobID     string               `json:"job_id"`
	VMID      int                  `json:"vmid"`
	Status    string               `json:"status"`
	Message   string               `json:"message,omitempty"`
	Artifacts []V1ArtifactMetadata `json:"artifacts,omitempty"`
	Result    json.RawMessage      `json:"result,omitempty"`
}

type V1RunnerReportResponse struct {
	JobStatus     string `json:"job_status"`
	SandboxStatus string `json:"sandbox_status,omitempty"`
}
