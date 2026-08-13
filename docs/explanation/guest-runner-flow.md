# Guest runner flow

Most of AgentLab runs on the host, but the actual coding work happens inside the
sandbox VM. This page explains what happens between a sandbox reaching the
`READY` state and its job reaching `COMPLETED`. The flow has five phases:
workspace setup, bootstrap fetch, repository preparation, agent execution, and
teardown with artifact upload.

## Workspace setup

When the VM boots, two systemd units prepare the filesystem before any agent
work begins. `agentlab-workspace-setup.service` looks for a secondary disk
labeled `AGENTLAB_WORK`, formats it as ext4 if needed, and starts `work.mount`
at `/work`. The mount only activates when
`/dev/disk/by-label/AGENTLAB_WORK` exists, so an ephemeral sandbox with no
workspace disk simply skips it. The workspace volume is the only durable
filesystem state inside the sandbox; everything else is ephemeral. See
[Workspaces, sessions, and sandboxes](workspaces-sessions-sandboxes.md).

## Bootstrap fetch

The `agent-runner.service` then starts as user `agent` with
`AGENTLAB_BOOTSTRAP` pointing at `/etc/agentlab/bootstrap.json`. The descriptor
holds the one-time bootstrap token, the controller URL, and the VMID. The runner
POSTs the token and VMID to `POST /v1/bootstrap/fetch` on the bootstrap
listener.

The fetch is retried up to `AGENTLAB_BOOTSTRAP_RETRY_MAX`, which defaults to 10,
with exponential backoff capped at 30 seconds. A special case applies: an HTTP
404 with the message `job not found` makes the runner exit `0` cleanly, because
the sandbox was created without a job to run. On success the daemon validates
the single-use token, returns the decrypted payload, and consumes the token.
What that payload contains, and why it is delivered this way, is covered in
[Secrets delivery model](secrets-delivery-model.md).

## Repository preparation

With the payload in hand, the runner writes `env.sh`, git credentials, and the
Claude settings into `/run/agentlab/secrets`, enrolls Tailscale if the payload
asks for it, and clones or updates the job repository into `REPO_DIR`. The
default `REPO_DIR` is `/work/repo` when `/work` is writable, otherwise
`/tmp/repo`, and the git ref defaults to `main` when unset. Git auth uses either
an SSH key with `IdentitiesOnly` or a token through a `GIT_ASKPASS` helper with
`GIT_TERMINAL_PROMPT=0`.

The runner then reports `RUNNING` to `POST /v1/runner/report`. Status values the
runner emits, and the daemon accepts, are `RUNNING`, `COMPLETED`, `FAILED`, and
`TIMEOUT`. An exit code of 124 maps to `TIMEOUT`.

## Agent execution

The agent itself is launched through `agentlab-agent`, which supports the
`claude`, `codex`, and `opencode` tools. The tool is selected with `--agent` or
the `AGENTLAB_AGENT` environment variable, and `claude` is the default. Claude
in particular runs with `DISABLE_AUTOUPDATER=1`. A repository-local
`.agentlab/run.sh` overrides the whole invocation when present and
`AGENTLAB_AGENT_COMMAND` is unset, which lets a project pin its own command.

Two optional containment layers wrap the agent. An inner bubblewrap sandbox can
mount the secrets directory read-only and the root filesystem read-only with
`--unshare-all`. The agent also runs under a hard wall-clock timeout driven by
`AGENTLAB_RUNNER_TIMEOUT_SECONDS` or the job TTL, enforced with
`timeout --kill-after=30`. The inner sandbox is explained in
[Use the inner bubblewrap sandbox](../how-to/use-the-inner-bubblewrap-sandbox.md).

Throughout execution the runner redacts secret material from logs. It builds a
filter from the bootstrap token, the artifact and git tokens, the Tailscale
tokens, and every environment value of at least six characters, replacing each
occurrence with `[REDACTED]`. When `AGENTLAB_RUNNER_STREAM_LOGS` is set, output
is streamed to the daemon as `RUNNING` status messages throttled to every two
seconds.

## Teardown and artifact upload

When the agent exits, the runner computes a final status, records the commit SHA
and duration into `report.json`, tars the logs and report into
`agentlab-artifacts.tar.gz`, and uploads the bundle through a Bearer token to
`POST /upload` on the artifact listener. The upload only fires when both the
artifact endpoint and token were present in the bootstrap payload. The runner
then POSTs the final status to `/v1/runner/report`, and `ExecStopPost` runs
`agent-secrets-cleanup`, which wipes the secrets directory under `/run` or
`/dev/shm` and removes the repo directory, refusing to touch anything under
`/work/*`.

## Related reading

- For an end-to-end run from the operator side:
  [Run your first job](../tutorials/run-first-job.md).
- For pulling the bundle the runner uploads:
  [Collect and download job artifacts](../how-to/collect-job-artifacts.md).
- For the variables the runner consumes:
  [Guest environment variables](../reference/guest-environment-variables.md).
- For the states this flow moves through:
  [State machine reference](../reference/state-machine.md).
