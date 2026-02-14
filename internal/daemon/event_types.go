package daemon

type EventKind string

type EventDomain string

type EventStage string

const (
	eventDomainSandbox   EventDomain = "sandbox"
	eventDomainJob       EventDomain = "job"
	eventDomainWorkspace EventDomain = "workspace"
	eventDomainArtifact  EventDomain = "artifact"
	eventDomainExposure  EventDomain = "exposure"
	eventDomainRecovery  EventDomain = "recovery"
)

const (
	EventStageLifecycle EventStage = "lifecycle"
	EventStageLease     EventStage = "lease"
	EventStageSLO       EventStage = "slo"
	EventStageRecovery  EventStage = "recovery"
	EventStageSnapshot  EventStage = "snapshot"
	EventStageReport    EventStage = "report"
	EventStageNetwork   EventStage = "network"
	EventStageArtifact  EventStage = "artifact"
	EventStageExposure  EventStage = "exposure"
)

const (
	eventContractSchemaVersion = 1
)

const (
	// Sandbox lifecycle.
	EventKindSandboxState          EventKind = "sandbox.state"
	EventKindSandboxLease          EventKind = "sandbox.lease"
	EventKindSandboxIPPending      EventKind = "sandbox.ip_pending"
	EventKindSandboxIPConflict     EventKind = "sandbox.ip_conflict"
	EventKindSandboxSLOReady       EventKind = "sandbox.slo.ready"
	EventKindSandboxSLOSSHReady    EventKind = "sandbox.slo.ssh_ready"
	EventKindSandboxSLOSSHFailed   EventKind = "sandbox.slo.ssh_failed"
	EventKindSandboxSnapshotCreated EventKind = "sandbox.snapshot.created"
	EventKindSandboxSnapshotFailed  EventKind = "sandbox.snapshot.failed"
	EventKindSandboxRevertStarted  EventKind = "sandbox.revert.started"
	EventKindSandboxRevertFailed   EventKind = "sandbox.revert.failed"
	EventKindSandboxRevertFinished EventKind = "sandbox.revert.completed"
	EventKindSandboxStartCompleted  EventKind = "sandbox.start.completed"
	EventKindSandboxStartFailed     EventKind = "sandbox.start.failed"
	EventKindSandboxStopCompleted   EventKind = "sandbox.stop.completed"
	EventKindSandboxStopFailed      EventKind = "sandbox.stop.failed"
	EventKindSandboxPauseCompleted  EventKind = "sandbox.pause.completed"
	EventKindSandboxPauseFailed     EventKind = "sandbox.pause.failed"
	EventKindSandboxResumeCompleted EventKind = "sandbox.resume.completed"
	EventKindSandboxResumeFailed    EventKind = "sandbox.resume.failed"
	EventKindSandboxDestroyCompleted EventKind = "sandbox.destroy.completed"
	EventKindSandboxDestroyFailed    EventKind = "sandbox.destroy.failed"
	EventKindSandboxProvisionFailed  EventKind = "sandbox.provision_failed"
	EventKindSandboxStopAll          EventKind = "sandbox.stop_all"
	EventKindSandboxStopAllResult    EventKind = "sandbox.stop_all.result"
	EventKindSandboxIdleStop         EventKind = "sandbox.idle_stop"

	// Job lifecycle.
	EventKindJobCreated   EventKind = "job.created"
	EventKindJobRunning   EventKind = "job.running"
	EventKindJobFailed    EventKind = "job.failed"
	EventKindJobReport    EventKind = "job.report"
	EventKindJobSLOStart  EventKind = "job.slo.start"

	// Workspace lifecycle and lease flow.
	EventKindWorkspaceLeaseAcquired EventKind = "workspace.lease.acquired"
	EventKindWorkspaceLeaseReleased EventKind = "workspace.lease.released"
	EventKindWorkspaceLeaseRenewed  EventKind = "workspace.lease.renewed"
	EventKindWorkspaceSnapshotCreated EventKind = "workspace.snapshot.created"
	EventKindWorkspaceSnapshotFailed  EventKind = "workspace.snapshot.create_failed"
	EventKindWorkspaceSnapshotRestored EventKind = "workspace.snapshot.restored"
	EventKindWorkspaceSnapshotRestoreFailed EventKind = "workspace.snapshot.restore_failed"
	EventKindWorkspaceFSCKStarted EventKind = "workspace.fsck.started"
	EventKindWorkspaceFSCKFailed  EventKind = "workspace.fsck.failed"
	EventKindWorkspaceFSCKDone    EventKind = "workspace.fsck.completed"

	// Artifact and exposure lifecycle for operations that affect observability.
	EventKindArtifactUpload EventKind = "artifact.upload"
	EventKindArtifactGC     EventKind = "artifact.gc"
	EventKindExposureCreate EventKind = "exposure.create"
	EventKindExposureDelete EventKind = "exposure.delete"
	EventKindExposureCleanupFailed EventKind = "exposure.cleanup.failed"
)

type EventPayloadSchema struct {
	Kind         EventKind   `json:"kind"`
	Domain       EventDomain `json:"domain"`
	Stage        EventStage  `json:"stage"`
	Schema       int         `json:"schema"`
	Required     []string    `json:"required"`
	Optional     []string    `json:"optional"`
	Description  string      `json:"description"`
}

var EventCatalog = map[EventKind]EventPayloadSchema{
	EventKindSandboxState: {
		Kind: EventKindSandboxState, Domain: eventDomainSandbox, Stage: EventStageLifecycle, Schema: eventContractSchemaVersion,
		Required: []string{"from_state", "to_state"}, Optional: []string{"duration_ms"},
		Description: "Sandbox finite state transition.",
	},
	EventKindSandboxLease: {
		Kind: EventKindSandboxLease, Domain: eventDomainSandbox, Stage: EventStageLease, Schema: eventContractSchemaVersion,
		Required: []string{"expires_at"}, Description: "Sandbox lease lifecycle updates.",
	},
	EventKindSandboxIPPending: {
		Kind: EventKindSandboxIPPending, Domain: eventDomainSandbox, Stage: EventStageNetwork, Schema: eventContractSchemaVersion,
		Required: nil, Description: "No IP observed yet during provisioning.",
	},
	EventKindSandboxIPConflict: {
		Kind: EventKindSandboxIPConflict, Domain: eventDomainSandbox, Stage: EventStageNetwork, Schema: eventContractSchemaVersion,
		Required: []string{"conflicting_vmid", "ip"}, Description: "IP conflict detection while assigning sandbox IP.",
	},
	EventKindSandboxSLOReady: {
		Kind: EventKindSandboxSLOReady, Domain: eventDomainSandbox, Stage: EventStageSLO, Schema: eventContractSchemaVersion,
		Required: []string{"duration_ms"}, Optional: []string{"checkpoint"}, Description: "Time from sandbox create to READY.",
	},
	EventKindSandboxSLOSSHReady: {
		Kind: EventKindSandboxSLOSSHReady, Domain: eventDomainSandbox, Stage: EventStageSLO, Schema: eventContractSchemaVersion,
		Required: []string{"duration_ms"}, Optional: []string{"ip"}, Description: "SSH readiness SLO for sandbox.",
	},
	EventKindSandboxSLOSSHFailed: {
		Kind: EventKindSandboxSLOSSHFailed, Domain: eventDomainSandbox, Stage: EventStageSLO, Schema: eventContractSchemaVersion,
		Required: []string{"duration_ms", "error"}, Description: "SSH readiness SLO failure details.",
	},
	EventKindSandboxSnapshotCreated: {
		Kind: EventKindSandboxSnapshotCreated, Domain: eventDomainSandbox, Stage: EventStageSnapshot, Schema: eventContractSchemaVersion,
		Required: []string{"name"}, Optional: []string{"backend_ref", "error"}, Description: "Snapshot created for sandbox recovery.",
	},
	EventKindSandboxSnapshotFailed: {
		Kind: EventKindSandboxSnapshotFailed, Domain: eventDomainSandbox, Stage: EventStageSnapshot, Schema: eventContractSchemaVersion,
		Required: []string{"name", "error"}, Description: "Snapshot operation failed for sandbox recovery.",
	},
	EventKindSandboxRevertStarted: {
		Kind: EventKindSandboxRevertStarted, Domain: eventDomainRecovery, Stage: EventStageRecovery, Schema: eventContractSchemaVersion,
		Required: []string{"snapshot", "was_running", "force"}, Optional: []string{"restart"}, Description: "Sandbox revert operation started.",
	},
	EventKindSandboxRevertFailed: {
		Kind: EventKindSandboxRevertFailed, Domain: eventDomainRecovery, Stage: EventStageRecovery, Schema: eventContractSchemaVersion,
		Required: []string{"snapshot", "was_running", "force", "error"}, Description: "Sandbox revert operation failed.",
	},
	EventKindSandboxRevertFinished: {
		Kind: EventKindSandboxRevertFinished, Domain: eventDomainRecovery, Stage: EventStageRecovery, Schema: eventContractSchemaVersion,
		Required: []string{"snapshot", "was_running", "force"}, Optional: []string{"restart", "duration_ms"}, Description: "Sandbox revert operation completed.",
	},
	EventKindSandboxStartCompleted: {
		Kind: EventKindSandboxStartCompleted, Domain: eventDomainSandbox, Stage: EventStageLifecycle, Schema: eventContractSchemaVersion,
		Required: []string{"duration_ms"}, Description: "Sandbox start completed.",
	},
	EventKindSandboxStartFailed: {
		Kind: EventKindSandboxStartFailed, Domain: eventDomainSandbox, Stage: EventStageLifecycle, Schema: eventContractSchemaVersion,
		Required: []string{"duration_ms", "error"}, Description: "Sandbox start failed.",
	},
	EventKindSandboxStopCompleted: {
		Kind: EventKindSandboxStopCompleted, Domain: eventDomainSandbox, Stage: EventStageLifecycle, Schema: eventContractSchemaVersion,
		Required: []string{"duration_ms"}, Description: "Sandbox stop completed.",
	},
	EventKindSandboxStopFailed: {
		Kind: EventKindSandboxStopFailed, Domain: eventDomainSandbox, Stage: EventStageLifecycle, Schema: eventContractSchemaVersion,
		Required: []string{"duration_ms", "error"}, Description: "Sandbox stop failed.",
	},
	EventKindSandboxPauseCompleted: {
		Kind: EventKindSandboxPauseCompleted, Domain: eventDomainSandbox, Stage: EventStageLifecycle, Schema: eventContractSchemaVersion,
		Required: []string{"duration_ms"}, Description: "Sandbox pause completed.",
	},
	EventKindSandboxPauseFailed: {
		Kind: EventKindSandboxPauseFailed, Domain: eventDomainSandbox, Stage: EventStageLifecycle, Schema: eventContractSchemaVersion,
		Required: []string{"duration_ms", "error"}, Description: "Sandbox pause failed.",
	},
	EventKindSandboxResumeCompleted: {
		Kind: EventKindSandboxResumeCompleted, Domain: eventDomainSandbox, Stage: EventStageLifecycle, Schema: eventContractSchemaVersion,
		Required: []string{"duration_ms"}, Description: "Sandbox resume completed.",
	},
	EventKindSandboxResumeFailed: {
		Kind: EventKindSandboxResumeFailed, Domain: eventDomainSandbox, Stage: EventStageLifecycle, Schema: eventContractSchemaVersion,
		Required: []string{"duration_ms", "error"}, Description: "Sandbox resume failed.",
	},
	EventKindSandboxDestroyCompleted: {
		Kind: EventKindSandboxDestroyCompleted, Domain: eventDomainSandbox, Stage: EventStageLifecycle, Schema: eventContractSchemaVersion,
		Required: []string{"duration_ms"}, Description: "Sandbox destroy completed.",
	},
	EventKindSandboxDestroyFailed: {
		Kind: EventKindSandboxDestroyFailed, Domain: eventDomainSandbox, Stage: EventStageLifecycle, Schema: eventContractSchemaVersion,
		Required: []string{"duration_ms", "error"}, Description: "Sandbox destroy failed.",
	},
	EventKindSandboxProvisionFailed: {
		Kind: EventKindSandboxProvisionFailed, Domain: eventDomainRecovery, Stage: EventStageRecovery, Schema: eventContractSchemaVersion,
		Optional: []string{"error"}, Description: "Provision attempt was rejected before start.",
	},
	EventKindSandboxStopAll: {
		Kind: EventKindSandboxStopAll, Domain: eventDomainRecovery, Stage: EventStageRecovery, Schema: eventContractSchemaVersion,
		Required: []string{"force"}, Description: "Batch stop request completed (summary recorded in payload).",
	},
	EventKindSandboxStopAllResult: {
		Kind: EventKindSandboxStopAllResult, Domain: eventDomainRecovery, Stage: EventStageRecovery, Schema: eventContractSchemaVersion,
		Required: []string{"result"}, Optional: []string{"state", "error", "previous_state"}, Description: "Per-sandbox stop_all result.",
	},
	EventKindSandboxIdleStop: {
		Kind: EventKindSandboxIdleStop, Domain: eventDomainRecovery, Stage: EventStageRecovery, Schema: eventContractSchemaVersion,
		Required: []string{"idle_for_minutes"}, Optional: []string{"error"}, Description: "Background idle-stop action completed.",
	},

	EventKindJobCreated: {
		Kind: EventKindJobCreated, Domain: eventDomainJob, Stage: EventStageLifecycle, Schema: eventContractSchemaVersion,
		Optional: []string{"status"}, Description: "Canonical job creation event (reserved for API-facing events).",
	},
	EventKindJobRunning: {
		Kind: EventKindJobRunning, Domain: eventDomainJob, Stage: EventStageLifecycle, Schema: eventContractSchemaVersion,
		Optional: []string{"status"}, Description: "Job reached RUNNING state in sandbox.",
	},
	EventKindJobFailed: {
		Kind: EventKindJobFailed, Domain: eventDomainJob, Stage: EventStageLifecycle, Schema: eventContractSchemaVersion,
		Required: []string{"status"}, Description: "Job transitioned to FAILED.",
	},
	EventKindJobReport: {
		Kind: EventKindJobReport, Domain: eventDomainJob, Stage: EventStageReport, Schema: eventContractSchemaVersion,
		Required: []string{"status"}, Optional: []string{"reported_at", "artifacts", "result", "message"}, Description: "Periodic or final job report from runner.",
	},
	EventKindJobSLOStart: {
		Kind: EventKindJobSLOStart, Domain: eventDomainJob, Stage: EventStageSLO, Schema: eventContractSchemaVersion,
		Required: []string{"duration_ms"}, Description: "Job start duration SLO event.",
	},

	EventKindWorkspaceLeaseAcquired: {
		Kind: EventKindWorkspaceLeaseAcquired, Domain: eventDomainWorkspace, Stage: EventStageLease, Schema: eventContractSchemaVersion,
		Required: []string{"workspace_id", "owner"}, Description: "Workspace lease acquired for job/session.",
	},
	EventKindWorkspaceLeaseReleased: {
		Kind: EventKindWorkspaceLeaseReleased, Domain: eventDomainWorkspace, Stage: EventStageLease, Schema: eventContractSchemaVersion,
		Required: []string{"workspace_id", "owner"}, Description: "Workspace lease released.",
	},
	EventKindWorkspaceLeaseRenewed: {
		Kind: EventKindWorkspaceLeaseRenewed, Domain: eventDomainWorkspace, Stage: EventStageLease, Schema: eventContractSchemaVersion,
		Required: []string{"workspace_id", "owner", "expires_at"}, Description: "Workspace lease renewed.",
	},
	EventKindWorkspaceSnapshotCreated: {
		Kind: EventKindWorkspaceSnapshotCreated, Domain: eventDomainWorkspace, Stage: EventStageSnapshot, Schema: eventContractSchemaVersion,
		Required: []string{"workspace_id", "name"}, Description: "Workspace snapshot successfully created.",
	},
	EventKindWorkspaceSnapshotFailed: {
		Kind: EventKindWorkspaceSnapshotFailed, Domain: eventDomainWorkspace, Stage: EventStageSnapshot, Schema: eventContractSchemaVersion,
		Required: []string{"workspace_id", "name", "error"}, Description: "Workspace snapshot creation failed.",
	},
	EventKindWorkspaceSnapshotRestored: {
		Kind: EventKindWorkspaceSnapshotRestored, Domain: eventDomainWorkspace, Stage: EventStageSnapshot, Schema: eventContractSchemaVersion,
		Required: []string{"workspace_id", "name"}, Description: "Workspace snapshot restored.",
	},
	EventKindWorkspaceSnapshotRestoreFailed: {
		Kind: EventKindWorkspaceSnapshotRestoreFailed, Domain: eventDomainWorkspace, Stage: EventStageSnapshot, Schema: eventContractSchemaVersion,
		Required: []string{"workspace_id", "name", "error"}, Description: "Workspace snapshot restore failed.",
	},
	EventKindWorkspaceFSCKStarted: {
		Kind: EventKindWorkspaceFSCKStarted, Domain: eventDomainWorkspace, Stage: EventStageRecovery, Schema: eventContractSchemaVersion,
		Required: []string{"workspace_id"}, Description: "Workspace fsck started.",
	},
	EventKindWorkspaceFSCKFailed: {
		Kind: EventKindWorkspaceFSCKFailed, Domain: eventDomainWorkspace, Stage: EventStageRecovery, Schema: eventContractSchemaVersion,
		Required: []string{"workspace_id", "error"}, Description: "Workspace fsck failed.",
	},
	EventKindWorkspaceFSCKDone: {
		Kind: EventKindWorkspaceFSCKDone, Domain: eventDomainWorkspace, Stage: EventStageRecovery, Schema: eventContractSchemaVersion,
		Required: []string{"workspace_id", "status"}, Description: "Workspace fsck completed.",
	},

	EventKindArtifactUpload: {
		Kind: EventKindArtifactUpload, Domain: eventDomainArtifact, Stage: EventStageArtifact, Schema: eventContractSchemaVersion,
		Required: []string{"name"}, Optional: []string{"path", "vmid", "size_bytes", "sha256", "mime"}, Description: "Artifact upload completed.",
	},
	EventKindArtifactGC: {
		Kind: EventKindArtifactGC, Domain: eventDomainArtifact, Stage: EventStageArtifact, Schema: eventContractSchemaVersion,
		Required: []string{"name"}, Optional: []string{"vmid", "path"}, Description: "Artifact removed by retention policy.",
	},
	EventKindExposureCreate: {
		Kind: EventKindExposureCreate, Domain: eventDomainExposure, Stage: EventStageExposure, Schema: eventContractSchemaVersion,
		Required: []string{"name", "vmid", "port", "target_ip"}, Description: "Exposure created.",
	},
	EventKindExposureDelete: {
		Kind: EventKindExposureDelete, Domain: eventDomainExposure, Stage: EventStageExposure, Schema: eventContractSchemaVersion,
		Required: []string{"name", "vmid", "port"}, Description: "Exposure deleted.",
	},
	EventKindExposureCleanupFailed: {
		Kind: EventKindExposureCleanupFailed, Domain: eventDomainExposure, Stage: EventStageExposure, Schema: eventContractSchemaVersion,
		Required: []string{"name", "vmid", "port", "error"}, Description: "Exposure cleanup encountered an error.",
	},
}

