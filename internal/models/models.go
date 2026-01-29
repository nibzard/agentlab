package models

import "time"

type SandboxState string

const (
	SandboxRequested    SandboxState = "REQUESTED"
	SandboxProvisioning SandboxState = "PROVISIONING"
	SandboxBooting      SandboxState = "BOOTING"
	SandboxReady        SandboxState = "READY"
	SandboxRunning      SandboxState = "RUNNING"
	SandboxCompleted    SandboxState = "COMPLETED"
	SandboxFailed       SandboxState = "FAILED"
	SandboxTimeout      SandboxState = "TIMEOUT"
	SandboxStopped      SandboxState = "STOPPED"
	SandboxDestroyed    SandboxState = "DESTROYED"
)

type Sandbox struct {
	VMID          int
	Name          string
	Profile       string
	State         SandboxState
	IP            string
	WorkspaceID   *string
	Keepalive     bool
	LeaseExpires  time.Time
	CreatedAt     time.Time
	LastUpdatedAt time.Time
}

type JobStatus string

const (
	JobQueued    JobStatus = "QUEUED"
	JobRunning   JobStatus = "RUNNING"
	JobCompleted JobStatus = "COMPLETED"
	JobFailed    JobStatus = "FAILED"
	JobTimeout   JobStatus = "TIMEOUT"
)

type Job struct {
	ID          string
	RepoURL     string
	Ref         string
	Profile     string
	Task        string
	Mode        string
	TTLMinutes  int
	Keepalive   bool
	Status      JobStatus
	SandboxVMID *int
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type Profile struct {
	Name       string
	TemplateVM int
	UpdatedAt  time.Time
	RawYAML    string
}

type Workspace struct {
	ID          string
	Name        string
	Storage     string
	VolumeID    string
	SizeGB      int
	AttachedVM  *int
	CreatedAt   time.Time
	LastUpdated time.Time
}
