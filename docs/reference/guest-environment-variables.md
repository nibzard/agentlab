# Guest environment variables

The in-guest runner reads `AGENTLAB_*` environment variables to control
bootstrap, agent selection, timeouts, directories, cleanup, and the inner
sandbox. This page lists the variables consumed by `agent-runner`,
`agent-secrets-cleanup`, and `agentlab-agent`. For the runner flow, see
../explanation/guest-runner-flow.md.

Most defaults come from the `${VAR:-default}` expansions in
`scripts/guest/agent-runner`. The file `scripts/guest/agent-runner.env` is an
optional, fully commented template. The `agent-runner.service` unit loads it
from `/etc/agentlab/agent-runner.env` only when present (the `EnvironmentFile=-`
prefix marks it optional), and `agent-runner` sources it only when the file
exists.

## Bootstrap and connection

| Variable | Default | Description |
| --- | --- | --- |
| `AGENTLAB_BOOTSTRAP` | `/etc/agentlab/bootstrap.json` | Path to the bootstrap payload (token, controller URL, vmid). |
| `AGENTLAB_RUNNER_ENV` | `/etc/agentlab/agent-runner.env` | Path to the runner environment file. |
| `AGENTLAB_BOOTSTRAP_CACHE` | `1` | Cache the bootstrap payload to disk for retries. |
| `AGENTLAB_BOOTSTRAP_RETRY_MAX` | `10` | Max attempts for the bootstrap fetch loop. |
| `AGENTLAB_RETRY_MAX` | `6` | Max attempts for status report POSTs. |

A bootstrap fetch that returns HTTP 404 with `job not found` makes the runner
exit `0` cleanly. Other failures retry with exponential backoff capped at 30s.

## Agent selection

| Variable | Default | Description |
| --- | --- | --- |
| `AGENTLAB_AGENT` | `claude` | Coding agent for `agentlab-agent` and `agent-runner`. One of `claude`, `codex`, `opencode`. |
| `AGENTLAB_AGENT_COMMAND` | (unset) | Override the entire agent invocation. When unset, the runner uses `.agentlab/run.sh` if present, else `agentlab-agent`. |
| `AGENTLAB_AGENT_ARGS` | (unset) | Extra arguments appended to a custom `AGENTLAB_AGENT_COMMAND`. |
| `AGENTLAB_TOOLS_ENV` | `/etc/agentlab/agent-tools.env` | Hardcoded path (not overridable) inside `agentlab-agent`. Sourced only for `--version` and `--list`, not before exec. |

## Runner behavior

| Variable | Default | Description |
| --- | --- | --- |
| `AGENTLAB_RUNNER_STREAM_LOGS` | `0` | When `1`, stream agent output to the daemon as `RUNNING` status messages, throttled to every 2s. |
| `AGENTLAB_RUNNER_LOG_MAX_CHARS` | `800` | Maximum characters per streamed log status message. |
| `AGENTLAB_RUNNER_TIMEOUT_SECONDS` | `0` | Hard wall-clock timeout for the agent. Overrides the job TTL. Uses `timeout --kill-after=30`. `0` means use `ttl_minutes`. |

## Timeouts (curl)

| Variable | Default | Description |
| --- | --- | --- |
| `AGENTLAB_CURL_CONNECT_TIMEOUT` | `10` | curl `--connect-timeout` for bootstrap, report, and upload. |
| `AGENTLAB_CURL_BOOTSTRAP_MAX_TIME` | `60` | curl `--max-time` for the bootstrap fetch. |
| `AGENTLAB_CURL_REPORT_MAX_TIME` | `20` | curl `--max-time` for the runner report POST. |
| `AGENTLAB_CURL_UPLOAD_MAX_TIME` | `300` | curl `--max-time` for artifact upload. |

## Directories and cleanup

| Variable | Default | Description |
| --- | --- | --- |
| `AGENTLAB_RUN_DIR` | `/run/agentlab` | Runtime directory for state and logs. |
| `AGENTLAB_REPO_DIR` | (unset) | Where the job repo is cloned. When unset, `/work/repo` if `/work` is writable, else `$AGENTLAB_WORK_DIR_BASE/repo`. |
| `AGENTLAB_WORK_DIR_BASE` | `/tmp` | Base directory for the repo when `/work` is absent. |
| `AGENTLAB_SECRETS_DIR` | `/run/agentlab/secrets` | Directory for per-job secrets. Must be under `/run` or `/dev/shm` for cleanup. |
| `AGENTLAB_ARTIFACTS_DIR` | `$AGENTLAB_RUN_DIR/artifacts` | Directory where the artifact tarball is staged before upload. |
| `AGENTLAB_CLEANUP_SECRETS` | `1` | When `1`, `agent-secrets-cleanup` wipes the secrets dir on service stop. |
| `AGENTLAB_CLEANUP_REPO` | `1` | When `1`, `agent-secrets-cleanup` removes the repo dir on service stop. Never removes `/work/*`. |

`agent-secrets-cleanup` only wipes secrets under `/run` or `/dev/shm` and only
removes the repo under `AGENTLAB_WORK_DIR_BASE`.

## Inner sandbox

| Variable | Default | Description |
| --- | --- | --- |
| `AGENTLAB_INNER_SANDBOX` | (unset) | Enable a bubblewrap inner sandbox around the agent. Values: `bubblewrap`, `bwrap`, `true`, `yes`, `1`, or `none`/`off` to disable. |
| `AGENTLAB_INNER_SANDBOX_ARGS` | (unset) | Extra `bwrap` arguments appended to the inner-sandbox prefix. |

The inner sandbox mounts the secrets directory read-only and the root
filesystem read-only with `--unshare-all`. It is not a full security boundary
and provides no network isolation. See
../how-to/use-the-inner-bubblewrap-sandbox.md.

## Job context exported to the agent

The runner exports these seven variables into the agent process environment:

| Variable | Description |
| --- | --- |
| `AGENTLAB_JOB_ID` | Current job ID. |
| `AGENTLAB_JOB_MODE` | Job mode. |
| `AGENTLAB_JOB_PROFILE` | Profile used for the sandbox. |
| `AGENTLAB_TASK` | Job task text. |
| `AGENTLAB_TASK_FILE` | Path to the task file. |
| `AGENTLAB_REPO_DIR` | Where the job repo is cloned. |
| `AGENTLAB_INNER_SANDBOX` | Resolved inner-sandbox mode (for example `bubblewrap`), or empty when disabled. |

## Guest helper

`agentlab-guest` reads sandbox metadata from `http://169.254.169.254`. It is a
helper script, not a configuration surface. See
reference/profile-and-template-schema.md for the profile fields that seed the
guest environment.
