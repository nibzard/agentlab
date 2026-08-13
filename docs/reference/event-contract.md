# Event contract

Reference for the AgentLab event envelope, domains, stages, and the catalog of event kinds. The daemon records events to the database through an `EventRecorder` and exposes them through the control plane. The contract is machine-readable at `GET /v1/schema`.

## Envelope

Every recorded event is stored and emitted as a JSON envelope:

```json
{
  "kind": "sandbox.state",
  "schema_version": 1,
  "stage": "lifecycle",
  "payload": { "from_state": "BOOTING", "to_state": "READY", "duration_ms": 4200 }
}
```

| Field | Type | Description |
| --- | --- | --- |
| `kind` | string | Event kind from the catalog below. |
| `schema_version` | int | Payload schema version. Currently `1`. |
| `stage` | string | Event stage. One of the stages listed below. |
| `payload` | object | Kind-specific payload. Required fields are validated before the event is recorded. |

The event schema version is `1` (`eventContractSchemaVersion`), and the control API schema version is `1` (`controlAPISchemaVersion`). Both are reported by `GET /v1/status` and `GET /v1/schema`.

## Domains and stages

| Domain | Values |
| --- | --- |
| Domain set | `sandbox`, `job`, `workspace`, `artifact`, `exposure`, `recovery` |

| Stage | Meaning |
| --- | --- |
| `lifecycle` | Create, start, stop, destroy, and similar state changes. |
| `lease` | Lease acquire, renew, release, and expiry. |
| `slo` | Duration and readiness SLO measurements. |
| `recovery` | Reconcile, revert, idle-stop, and fsck outcomes. |
| `snapshot` | Snapshot create, restore, and failure. |
| `report` | Runner status reports. |
| `network` | IP assignment and conflict detection. |
| `artifact` | Artifact upload and retention. |
| `exposure` | Tailnet exposure create, delete, and cleanup. |

## Sandbox events

| Kind | Stage | Required | Optional | Description |
| --- | --- | --- | --- | --- |
| `sandbox.state` | lifecycle | `from_state`, `to_state` | `duration_ms` | Sandbox finite-state transition. |
| `sandbox.lease` | lease | `expires_at` | - | Sandbox lease lifecycle update. |
| `sandbox.ip_pending` | network | - | - | No IP observed yet during provisioning. |
| `sandbox.ip_conflict` | network | `conflicting_vmid`, `ip` | - | IP conflict while assigning a sandbox IP. |
| `sandbox.slo.ready` | slo | `duration_ms` | `checkpoint` | Time from create to READY. |
| `sandbox.slo.ssh_ready` | slo | `duration_ms` | `ip` | SSH readiness SLO. |
| `sandbox.slo.ssh_failed` | slo | `duration_ms`, `error` | - | SSH readiness SLO failure. |
| `sandbox.configured` | lifecycle | - | `cores`, `memory_mb` | Sandbox resource configuration updated. |
| `sandbox.start.completed` | lifecycle | `duration_ms` | - | Sandbox start completed. |
| `sandbox.start.failed` | lifecycle | `duration_ms`, `error` | - | Sandbox start failed. |
| `sandbox.stop.completed` | lifecycle | `duration_ms` | - | Sandbox stop completed. |
| `sandbox.stop.failed` | lifecycle | `duration_ms`, `error` | - | Sandbox stop failed. |
| `sandbox.pause.completed` | lifecycle | `duration_ms` | - | Sandbox pause completed. |
| `sandbox.pause.failed` | lifecycle | `duration_ms`, `error` | - | Sandbox pause failed. |
| `sandbox.resume.completed` | lifecycle | `duration_ms` | - | Sandbox resume completed. |
| `sandbox.resume.failed` | lifecycle | `duration_ms`, `error` | - | Sandbox resume failed. |
| `sandbox.destroy.completed` | lifecycle | `duration_ms` | - | Sandbox destroy completed. |
| `sandbox.destroy.failed` | lifecycle | `duration_ms`, `error` | - | Sandbox destroy failed. |
| `sandbox.snapshot.created` | snapshot | `name` | `backend_ref`, `error` | Snapshot created for recovery. |
| `sandbox.snapshot.failed` | snapshot | `name`, `error` | - | Snapshot operation failed. |
| `sandbox.revert.started` | recovery | `snapshot`, `was_running`, `force` | `restart` | Revert operation started. |
| `sandbox.revert.completed` | recovery | `snapshot`, `was_running`, `force` | `restart`, `duration_ms` | Revert operation completed. |
| `sandbox.revert.failed` | recovery | `snapshot`, `was_running`, `force`, `error` | - | Revert operation failed. |
| `sandbox.provision_failed` | recovery | - | `error` | Provision attempt rejected before start. |
| `sandbox.stop_all` | recovery | `force` | - | Batch stop request recorded. |
| `sandbox.stop_all.result` | recovery | `result` | `state`, `error`, `previous_state` | Per-sandbox stop_all result. |
| `sandbox.idle_stop` | recovery | `idle_for_minutes` | `error` | Background idle-stop action completed. |

## Job events

| Kind | Stage | Required | Optional | Description |
| --- | --- | --- | --- | --- |
| `job.created` | lifecycle | - | `status` | Canonical job creation event. |
| `job.running` | lifecycle | - | `status` | Job reached RUNNING in the sandbox. |
| `job.failed` | lifecycle | `status` | - | Job transitioned to FAILED. |
| `job.report` | report | `status` | `reported_at`, `artifacts`, `result`, `message` | Periodic or final runner report. |
| `job.slo.start` | slo | `duration_ms` | - | Job start duration SLO. |

## Workspace events

| Kind | Stage | Required | Optional | Description |
| --- | --- | --- | --- | --- |
| `workspace.lease.acquired` | lease | `workspace_id`, `owner` | - | Lease acquired for a job or session. |
| `workspace.lease.released` | lease | `workspace_id`, `owner` | - | Lease released. |
| `workspace.lease.renewed` | lease | `workspace_id`, `owner`, `expires_at` | - | Lease renewed. |
| `workspace.snapshot.created` | snapshot | `workspace_id`, `name` | - | Workspace snapshot created. |
| `workspace.snapshot.create_failed` | snapshot | `workspace_id`, `name`, `error` | - | Workspace snapshot creation failed. |
| `workspace.snapshot.restored` | snapshot | `workspace_id`, `name` | - | Workspace snapshot restored. |
| `workspace.snapshot.restore_failed` | snapshot | `workspace_id`, `name`, `error` | - | Workspace snapshot restore failed. |
| `workspace.fsck.started` | recovery | `workspace_id` | - | Workspace fsck started. |
| `workspace.fsck.failed` | recovery | `workspace_id`, `error` | - | Workspace fsck failed. |
| `workspace.fsck.completed` | recovery | `workspace_id`, `status` | - | Workspace fsck completed. |

## Artifact and exposure events

| Kind | Stage | Required | Optional | Description |
| --- | --- | --- | --- | --- |
| `artifact.upload` | artifact | `name` | `path`, `vmid`, `size_bytes`, `sha256`, `mime` | Artifact upload completed. |
| `artifact.gc` | artifact | `name` | `vmid`, `path` | Artifact removed by retention policy. |
| `exposure.create` | exposure | `name`, `vmid`, `port`, `target_ip` | - | Exposure created. |
| `exposure.delete` | exposure | `name`, `vmid`, `port` | - | Exposure deleted. |
| `exposure.cleanup.failed` | exposure | `name`, `vmid`, `port`, `error` | - | Exposure cleanup error. |

## Validation

`NewEventPayloadForKind` looks up the kind in `EventCatalog`, validates that every required field is present and non-empty, marshals the payload, and wraps it in the envelope. An unknown kind or a missing required field is an error and the event is not recorded.

## Where events surface

| Surface | How to read |
| --- | --- |
| `GET /v1/sandboxes/{vmid}/events` | List events for a sandbox. Supports `tail`, `after`, and `limit`. |
| `GET /v1/jobs/{id}` with `events_tail` | Recent job events inline in the job response. |
| `agentlab logs <vmid>` | Sandbox log stream. |
| `agentlab job show <job_id> --events-tail <n>` | Recent job events. |
| `GET /v1/schema` | Full machine-readable catalog (`event_kinds`). |

## Compatibility policy

`GET /v1/schema` returns this policy in the `compatibility` field:

- `api`: additive endpoint, path, and optional field changes are preferred. Breaking changes bump the API schema version.
- `events`: event kinds, required payload fields, and required version values are managed as an additive contract.
- `errors`: unknown event kinds or fields should be ignored by clients.

!!! note "Cross-release guarantees are not yet documented"
    The contract is additive in intent, but specific `schema_version` compatibility guarantees across releases are not yet documented. Treat a schema bump as a breaking change and re-read `/v1/schema` after an upgrade.

## Related

- [HTTP API reference](http-api.md) for the routes that read events.
- [State machine reference](state-machine.md) for the transitions that `sandbox.state` records.
- [Prometheus metrics](metrics.md) for the `agentlab_sandbox_transitions_total` counter that mirrors state transitions.
