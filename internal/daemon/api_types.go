package daemon

import "encoding/json"

type V1ErrorResponse struct {
	Error string `json:"error"`
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

type V1StatusResponse struct {
	Sandboxes      map[string]int    `json:"sandboxes"`
	Jobs           map[string]int    `json:"jobs"`
	NetworkModes   map[string]int    `json:"network_modes,omitempty"`
	Artifacts      V1StatusArtifacts `json:"artifacts"`
	Metrics        V1StatusMetrics   `json:"metrics"`
	RecentFailures []V1Event         `json:"recent_failures"`
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

type V1JobResponse struct {
	ID          string          `json:"id"`
	RepoURL     string          `json:"repo_url"`
	Ref         string          `json:"ref"`
	Profile     string          `json:"profile"`
	Task        string          `json:"task,omitempty"`
	Mode        string          `json:"mode,omitempty"`
	TTLMinutes  *int            `json:"ttl_minutes,omitempty"`
	Keepalive   bool            `json:"keepalive"`
	WorkspaceID *string         `json:"workspace_id,omitempty"`
	SessionID   *string         `json:"session_id,omitempty"`
	Status      string          `json:"status"`
	SandboxVMID *int            `json:"sandbox_vmid,omitempty"`
	Result      json.RawMessage `json:"result,omitempty"`
	Events      []V1Event       `json:"events,omitempty"`
	CreatedAt   string          `json:"created_at"`
	UpdatedAt   string          `json:"updated_at"`
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

type V1SandboxDestroyRequest struct {
	Force bool `json:"force"`
}

type V1SandboxRevertRequest struct {
	Force   bool  `json:"force"`
	Restart *bool `json:"restart,omitempty"`
}

type V1SandboxResponse struct {
	VMID          int               `json:"vmid"`
	Name          string            `json:"name"`
	Profile       string            `json:"profile"`
	State         string            `json:"state"`
	IP            string            `json:"ip,omitempty"`
	WorkspaceID   *string           `json:"workspace_id,omitempty"`
	Network       *V1SandboxNetwork `json:"network,omitempty"`
	Keepalive     bool              `json:"keepalive"`
	LeaseExpires  *string           `json:"lease_expires_at,omitempty"`
	LastUsedAt    *string           `json:"last_used_at,omitempty"`
	CreatedAt     string            `json:"created_at"`
	LastUpdatedAt string            `json:"updated_at"`
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
