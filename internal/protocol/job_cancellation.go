package protocol

// JobCancellation reports verified work and resource outcomes. The legacy job
// status remains FAILED for cancelled jobs; result.cancellation records the cause.
type JobCancellation struct {
	JobID         string        `json:"job_id"`
	State         string        `json:"state"`
	WorkStopped   bool          `json:"work_stopped"`
	CleanupStatus string        `json:"cleanup_status"`
	WorkspaceID   *string       `json:"workspace_id,omitempty"`
	Actors        []string      `json:"actors,omitempty"`
	SandboxVMID   *int          `json:"sandbox_vmid,omitempty"`
	Actor         string        `json:"actor,omitempty"`
	Scope         []string      `json:"scope,omitempty"`
	RequestedAt   string        `json:"requested_at"`
	UpdatedAt     string        `json:"updated_at"`
	ErrorCode     string        `json:"error_code,omitempty"`
	Action        string        `json:"action,omitempty"`
	Resources     []JobResource `json:"resources"`
}

type JobResource struct {
	Kind      string `json:"kind"`
	ID        string `json:"id"`
	State     string `json:"state"`
	ExpiresAt string `json:"expires_at,omitempty"`
}
