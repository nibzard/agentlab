# Prometheus metrics

Reference for the Prometheus metrics exposed by `agentlabd`. All metric names are prefixed with `agentlab_` and are served on the metrics listener at `GET /metrics`.

## Enablement

Metrics are disabled by default. Set `metrics_listen` to a loopback address to enable scraping. The convention is `127.0.0.1:8847`. Validation rejects `0.0.0.0` and any non-loopback host.

```yaml
metrics_listen: "127.0.0.1:8847"
```

For scrape setup, see [../how-to/scrape-prometheus-metrics.md](../how-to/scrape-prometheus-metrics.md).

## Sandbox metrics

| Metric | Type | Labels | Description |
| --- | --- | --- | --- |
| `agentlab_sandbox_transitions_total` | counter | `from`, `to` | Total sandbox state transitions. |
| `agentlab_sandbox_provision_duration_seconds` | histogram | - | Time from sandbox creation to RUNNING. |
| `agentlab_sandbox_time_to_ready_seconds` | histogram | - | Time from sandbox creation to READY. |
| `agentlab_sandbox_time_to_ssh_seconds` | histogram | - | Time from sandbox creation to SSH port readiness. |
| `agentlab_sandbox_start_duration_seconds` | histogram | `result` | Time spent starting a sandbox VM. |
| `agentlab_sandbox_stop_duration_seconds` | histogram | `result` | Time spent stopping a sandbox VM. |
| `agentlab_sandbox_destroy_duration_seconds` | histogram | `result` | Time spent destroying a sandbox VM. |
| `agentlab_sandbox_revert_total` | counter | `result` | Total sandbox revert operations. |
| `agentlab_sandbox_revert_duration_seconds` | histogram | `result` | Time spent reverting a sandbox to the clean snapshot. |
| `agentlab_sandbox_idle_stop_total` | counter | `result` | Total idle sandbox stops. |

## Job metrics

| Metric | Type | Labels | Description |
| --- | --- | --- | --- |
| `agentlab_job_status_total` | counter | `status` | Total job status transitions. |
| `agentlab_job_duration_seconds` | histogram | `status` | Job runtime from creation to final status. |
| `agentlab_job_time_to_start_seconds` | histogram | - | Time from job creation to RUNNING. |

## Workspace metrics

| Metric | Type | Labels | Description |
| --- | --- | --- | --- |
| `agentlab_workspace_lease_contention_total` | counter | - | Total workspace lease acquisition conflicts. |
| `agentlab_workspace_lease_wait_duration_seconds` | histogram | - | Time spent waiting to acquire a workspace lease. |
| `agentlab_workspace_snapshot_total` | counter | `operation`, `result` | Total workspace snapshot operations. |
| `agentlab_workspace_snapshot_duration_seconds` | histogram | `operation`, `result` | Time spent creating or restoring workspace snapshots. |

## Histogram buckets

| Metric group | Buckets (seconds) |
| --- | --- |
| SLO (`provision`, `time_to_ready`, `time_to_ssh`, `job_time_to_start`) | 1, 2, 5, 10, 20, 30, 60, 120, 300, 600, 1200 |
| Operations (`start`, `stop`, `destroy`, `revert`, workspace `snapshot`) | 0.5, 1, 2, 5, 10, 20, 30, 60, 120, 300 |
| `job_duration_seconds` | 5, 10, 30, 60, 120, 300, 600, 1200, 1800, 3600, 7200 |
| `workspace_lease_wait_duration_seconds` | 0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10, 30, 60 |

## Label conventions

The `result` label records the outcome of an operation. An empty result is normalized to `unknown` before publication, so every observed sample carries a non-empty `result`. The `status` label uses the job status constants (`QUEUED`, `RUNNING`, `COMPLETED`, `FAILED`, `TIMEOUT`). The `from` and `to` labels on `transitions_total` use the sandbox state constants.

## Related

- [Listeners and ports](listeners-and-ports.md) for the metrics bind address.
- [Configuration reference](configuration.md) for `metrics_listen` validation.
- [Event contract](event-contract.md) for the `sandbox.state` events that mirror `transitions_total`.
