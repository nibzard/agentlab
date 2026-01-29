package daemon

import "encoding/json"

type V1ErrorResponse struct {
	Error string `json:"error"`
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

type V1ArtifactMetadata struct {
	Name      string `json:"name"`
	Path      string `json:"path,omitempty"`
	SizeBytes int64  `json:"size_bytes,omitempty"`
	Sha256    string `json:"sha256,omitempty"`
	MIME      string `json:"mime,omitempty"`
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
