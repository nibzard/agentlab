package daemon

import "encoding/json"

type V1ErrorResponse struct {
	Error string `json:"error"`
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
	Mode string `json:"mode,omitempty"`
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
	RepoURL    string `json:"repo_url"`
	Ref        string `json:"ref"`
	Profile    string `json:"profile"`
	Task       string `json:"task"`
	Mode       string `json:"mode"`
	TTLMinutes *int   `json:"ttl_minutes,omitempty"`
	Keepalive  bool   `json:"keepalive,omitempty"`
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
	Status      string          `json:"status"`
	SandboxVMID *int            `json:"sandbox_vmid,omitempty"`
	Result      json.RawMessage `json:"result,omitempty"`
	CreatedAt   string          `json:"created_at"`
	UpdatedAt   string          `json:"updated_at"`
}

type V1SandboxCreateRequest struct {
	Name       string  `json:"name"`
	Profile    string  `json:"profile"`
	Keepalive  bool    `json:"keepalive,omitempty"`
	TTLMinutes *int    `json:"ttl_minutes,omitempty"`
	Workspace  *string `json:"workspace_id,omitempty"`
	VMID       *int    `json:"vmid,omitempty"`
	JobID      string  `json:"job_id,omitempty"`
}

type V1SandboxResponse struct {
	VMID          int     `json:"vmid"`
	Name          string  `json:"name"`
	Profile       string  `json:"profile"`
	State         string  `json:"state"`
	IP            string  `json:"ip,omitempty"`
	WorkspaceID   *string `json:"workspace_id,omitempty"`
	Keepalive     bool    `json:"keepalive"`
	LeaseExpires  *string `json:"lease_expires_at,omitempty"`
	CreatedAt     string  `json:"created_at"`
	LastUpdatedAt string  `json:"updated_at"`
}

type V1SandboxesResponse struct {
	Sandboxes []V1SandboxResponse `json:"sandboxes"`
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

type V1WorkspaceRebindResponse struct {
	Workspace V1WorkspaceResponse `json:"workspace"`
	Sandbox   V1SandboxResponse   `json:"sandbox"`
	OldVMID   *int                `json:"old_vmid,omitempty"`
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
