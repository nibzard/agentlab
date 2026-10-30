package protocol

import "time"

// WorkspaceAllocation identifies storage retained by a durable job request.
type WorkspaceAllocation struct {
	OwnerVMID      int       `json:"owner_vmid,omitempty"`
	AllocationName string    `json:"allocation_name,omitempty"`
	JobID          string    `json:"job_id"`
	WorkspaceID    string    `json:"workspace_id"`
	Name           string    `json:"name"`
	Storage        string    `json:"storage"`
	SizeGB         int       `json:"size_gb"`
	State          string    `json:"state"`
	VolumeID       string    `json:"volume_id,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
}
