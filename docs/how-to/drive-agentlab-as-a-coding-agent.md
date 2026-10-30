# Drive AgentLab as a coding agent

As a coding agent, you drive AgentLab through the CLI with `--json`. The loop is
the same each time: discover what exists, validate a job, submit the job,
read the result, then tear the sandbox down. This page walks one full loop and
gives you the parsing rules that make it reliable.

This page is the machine-facing companion to the
[Agent JSON output reference](../reference/agent-json-output.md). For the
concepts behind isolation and credentials, read
[How AgentLab isolates and credentials an agent](../explanation/how-agentlab-isolates-and-credentials-an-agent.md).

## Load the packaged skill

The client contains the maintained AgentLab skill and its workflow guides.
Inspect its bundle version and file list without a server connection:

```bash
agentlab skill show --json
agentlab skill export --json
```

The export response contains `directory`, `entrypoint`, `manifest`, `sha256`,
and `files`. Read the exported `SKILL.md` through your agent's skill loader.
The entrypoint links guides for setup, job execution, standalone work, recovery,
networking, and result inspection. The bundle version is separate from the CLI version.

By default, export uses a private cache directory identified by the bundle digest.
It also accepts an unused destination in your configured agent skill directory:

```bash
agentlab skill export --out "$skill_directory/agentlab" --json
```

These local commands do not load a server credential or change agent settings.
An identical export is a successful retry. Modified or unexpected files produce
`skill_export_changed` and remain unchanged. To update an existing skill, export
the new bundle to an unused directory, review it, and use your agent's installation
workflow. Do not overwrite local customizations without review.

The host installer also preserves an existing skill when its files differ.
It stages the new bundle outside the configured skill directory and prints
the pending path. For example, with `CLAUDE_SKILLS_DIR=/home/operator/.claude/skills`,
updates go under `/home/operator/.claude/.agentlab-skill-updates/<digest>/agentlab`.
The existing skill stays selected. Review both bundles, preserve your local
instructions, and select the reviewed entrypoint through your agent's skill loader.
Identical retries reuse the pending bundle. Changed pending files stop the skill
installation with a corrective action. `CLAUDE_SKILL_FORCE` no longer overwrites files.
Daemon metadata changes only when the active directory matches the new bundle.

The guides require capability discovery before optional features. They distinguish
an agent process exit from test evidence and account for retained resources.
They do not implement a second provisioning or authorization path.

## Prerequisites

- The `agentlab` CLI can reach the daemon. Use the Unix socket or a remote
  `--endpoint`.
- You have a profile name to use. If you do not, Step 1 finds one.
- You pass `--json` on every call.

!!! warning "Pass --json every time"
    Pass `--json` explicitly in automation. You can also save `output-format=json`
    with `agentlab defaults write output-format json`. Explicit flags override
    the saved preference.

## The agent loop

Each task has five core phases, plus two optional ones:

1. **Discover** the schema and the available profiles.
2. **Choose job-managed provisioning**. The job creates its own sandbox.
3. **Submit** a job, after you validate the plan.
4. **Poll** the job to a terminal state. Read the result.
5. **Collect** artifacts. **Tear down** the sandbox.

The optional phases are running commands (Step 6) and keeping a long task alive.
The steps below cover one full loop.

## Steps

1. **Discover the schema and profiles.**

   Learn what the daemon accepts before you provision anything.

    ```bash
    agentlab schema
    agentlab profile list --json
    ```

   `agentlab schema` prints JSON unconditionally and ignores `--json`. Parse it
   for the field and event versions before you assume any field exists.
   `agentlab profile list --json` returns the profile names you may pass to
   `--profile`. Read one and store it.

   Validate the sandbox plan before you create it. This checks the profile,
   workspace, and modifiers without allocating a VMID.

    ```bash
    agentlab sandbox validate --profile <profile> --json
    ```

2. **Use the sandbox created by the job.**

   `job run` allocates a sandbox for the job. Do not call `sandbox new` first
   for this workflow. A standalone sandbox is a separate VM and does not become
   the job's sandbox. Creating both requires separate cleanup for each VM.

   Submit the job in Step 3. Read `sandbox_vmid` from `job show` when you need
   to inspect its VM. The job destroys its sandbox after completion unless you
   pass `--keepalive`.

3. **Validate, then submit a job.**

   For `job run`, the flags `--repo`, `--profile`, and `--task` are all required.
   Validate the plan first.

    ```bash
    agentlab job validate --repo <repo> --profile <profile> --task "<task>" --json
    agentlab job run --repo <repo> --profile <profile> --task "<task>" --json
    ```

   The validate call returns `ok`, `errors`, `warnings`, and `plan`. Read
   `errors` before you submit. When you submit, store the returned job ID.

    !!! note "Validate failure is not a request failure"
        On failure, `job validate --json` prints the result JSON with `ok:false`
        and `errors[]` to stdout, then exits 1. There is no error envelope. Tell
        a validate failure from a real error by checking whether the stdout JSON
        contains an `error` key. See the
        [Agent JSON output reference](../reference/agent-json-output.md).

4. **Poll the job. Read the result.**

    ```bash
    agentlab job show <job_id> --json --events-tail 20
    ```

   Job status transitions are `QUEUED -> RUNNING -> (COMPLETED | FAILED | TIMEOUT)`.
   Match the uppercase strings exactly. A guest exit code of `124` maps to
   `TIMEOUT`.

   Read the full outcome from one `job show`:

   - The terminal status is the verdict.
   - `result.message` is the human-readable exit or failure text.
   - `result.result` is free-form JSON.

   There is no dedicated `exit_code` field on the job. For the raw guest exit
   code, read `report.json` inside the artifact tarball (Step 5). For how the
   guest builds that report, see
   [Guest runner flow](../explanation/guest-runner-flow.md).

5. **List, then download artifacts.**

   List the artifacts first. Then download by exact path.

    ```bash
    agentlab job artifacts <job_id> --json
    agentlab job artifacts download <job_id> --path <path> --out ./out/
    ```

   - `job artifacts <job_id> --json` returns the exact `path` and `sha256` for
     each artifact. Use this list for deterministic retrieval.
   - `job artifacts download` with no selector silently defaults to `--bundle`
     and downloads `agentlab-artifacts.tar.gz`. Make the choice explicit with
     `--path` or `--bundle`.
   - The raw guest exit code lives in `report.json` inside the bundle. Extract
     the tarball to read it.

   For retention and storage caveats, see
   [How to collect and download job artifacts](collect-job-artifacts.md).

6. **Run commands inside the VM or on the host.**

   You have two execution paths. Pick by where the command must run.

   To run a command inside the VM:

    ```bash
    agentlab ssh <vmid> --exec -- <command> <args>
    ```

   With `--exec`, agentlab replaces itself with `ssh` through `syscall.Exec`.
   The `ssh` exit code becomes the remote command's exit code. The agentlab
   process exits with that code. Without `--exec`, the command is only printed.
   It is not run.

   To run an agentlab verb programmatically on the daemon host, call
   `POST /v1/exec`. The response is always HTTP 200:

    ```json
    { "exit_code": 0, "stdout": "...", "stderr": "..." }
    ```

   Branch on `exit_code`, not on the HTTP status. `0` means success. `124` means
   the call timed out. The default timeout is five minutes and the maximum is
   one hour. Values above the maximum are clamped silently. Output is capped at
   4 MiB per stream and is truncated in capture mode.

    !!! warning "/v1/exec is full-access only"
        `POST /v1/exec` runs an agentlab verb on the daemon host and is
        full-access only. A scoped SSH token is rejected with `403`. See
        [How AgentLab isolates and credentials an agent](../explanation/how-agentlab-isolates-and-credentials-an-agent.md).

## Prepare local changes

For a local worktree, use `agentlab repo snapshot --repo . --json` to create
protected transfer artifacts. The snapshot preserves local changes and excludes
ignored files, known credentials, and prior source history. It leaves the source
index and branch unchanged.

Upload the result with `agentlab repo upload --manifest PATH --json`. Save its
source ID, `repo_url`, manifest `revision`, and expiry. Use that URL and revision
with `job run --request-id KEY`. Snapshot jobs use a stateless VM with an updated
guest runner. They preserve existing guest worktrees. Check `repository_upload`
and `repository_snapshot_jobs` in capabilities first.

Collect its patch and execution archive with `agentlab job result ID --json`.
Check `repository_results` first. The client verifies the downloaded artifacts
before publishing a private result directory. Review the patch before applying
it. Read the job status and execution archive before reporting a test result.
A failed collection retains the sandbox and reports its lease and required action.

Remove the source upload with `agentlab repo remove ID` after its jobs finish.
Source expiry blocks new jobs; it does not remove stored bytes. Structured test
report validation and complete result accounting remain pending. See [Prepare a local repository snapshot](prepare-local-repository-snapshot.md)
for permissions, limits, and recovery steps.

## Keep a long task alive

A job normally destroys its sandbox when it finishes. Pass `--keepalive` to
`job run` to prevent that auto-destroy. Keepalive also keeps the workspace
lease-renewal loop running for the job.

For a sandbox you provisioned yourself, renew its lease before it expires:

```bash
agentlab sandbox lease renew --ttl <minutes> <vmid>
```

Lease renewal is `RUNNING`-only. In any other state the daemon returns `409`.
Flags can appear before or after the VMID.

## Tear down

For a standalone sandbox or a job kept with `--keepalive`, destroy the sandbox when you are done:

```bash
agentlab sandbox destroy <vmid>
```

Use `--force` for a sandbox that is stuck in an invalid or transient state.
`--force` calls `ForceDestroy`, which clears the record even when a normal
transition is not allowed.

```bash
agentlab sandbox destroy <vmid> --force
```

## A reusable --json driver recipe

Wrap every call in the same routine. The rules come from the
[Agent JSON output reference](../reference/agent-json-output.md).

1. Capture stdout, stderr, and the exit code together.
2. If the exit code is `2`, treat it as a usage error and stop.
3. Parse stdout as JSON. Do not parse stderr. The error envelope is on stdout.
4. If the JSON has an `error` key, read `{error, message, code, details}` and
   branch on it.
5. Otherwise read the command's own response shape. Ignore keys you do not use.
   The daemon response is a superset.

A minimal driver for one call:

```bash
out=$(agentlab sandbox show "$vmid" --json 2>/dev/null)
code=$?
if [ "$code" -eq 2 ]; then exit 2; fi
echo "$out" | jq 'if has("error") then .error else . end'
```

To delegate work to a sibling agent, hand it a scoped token instead of the
control token. See
[Mint and use scoped tokens for an agent](mint-scoped-tokens-for-an-agent.md)
and [Coordinate multiple agents](../tutorials/coordinate-multiple-agents.md).

## Expected result

After the loop you have:

- A job that reached `COMPLETED`, `FAILED`, or `TIMEOUT`.
- The artifact bundle with `report.json` extracted.
- The sandbox destroyed and its VMID released.

## Next

- [Agent JSON output reference](../reference/agent-json-output.md) for every
  field shape.
- [How AgentLab isolates and credentials an agent](../explanation/how-agentlab-isolates-and-credentials-an-agent.md)
  for the two execution paths.
- [Coordinate multiple agents](../tutorials/coordinate-multiple-agents.md) to
  share state and hand off work.

## Retry a job submission without creating another job

Discover support before using a request key:

```bash
agentlab capabilities --json
```

Require `features.idempotent_job_submission` and a token with `schema.read` and
`job.create`. Select a profile from the server's profile list. Then submit one
job with a key that you save with your task:

```bash
agentlab --context development job run --request-id task-2026-10-29-a \
  --repo https://example.com/team/project.git --profile yolo-ephemeral \
  --task 'Run the tests and return the results' --json
```

If the response is lost, retry with the same context, caller identity, key, and
inputs. The server returns the original job. Do not allocate another sandbox;
the job owns sandbox creation. Observe the returned job ID with `job show`.
Changing the active context does not change a client that has already resolved
its connection. Concurrent agents should use an explicit context.

A changed input with the same key returns `idempotency_conflict`. Keys have a
30-day retention period. An expired key returns `idempotency_key_expired` and
cannot silently create new work. A removed original job returns
`submission_job_removed`. Inspect remaining resources before choosing a new key.
The server keeps request tombstones so removal does not permit duplicate work.

Keyed submission supports stateless jobs, existing workspaces, and dependent
workspace creation when `idempotent_workspace_create` is true. Workspace creation
records its identity before allocating storage. A saved successful volume receipt
can resume database publication without another allocation. The workspace and
job binding are committed together. Retries keep the original workspace ID.

An interrupted allocation with no complete receipt returns a blocked state and
`workspace_allocation_inspection_required`. Its `workspace_allocation` object
identifies the reserved workspace, storage, allocation state, and any known volume.
Preserve these resources. Inspect the backend before choosing another request key.
Automatic reconciliation of unknown backend outcomes remains unavailable.
Do not delete a retained volume to make the retry pass.

Check `idempotent_branch_sessions` before combining a key with branch creation.
That feature remains false. Unsupported combinations stop before resource creation.
Legacy submissions without a request key keep their existing behavior.

Submission deduplication preserves the original job record across daemon restart.
It does not yet provide complete recovery of interrupted server provisioning.
Repository transfer and a complete resumable task workflow remain in progress.

## Cancel a job and account for its resources

Check `features.job_cancellation`, then request cancellation:

```bash
agentlab --context development job cancel --wait 2m JOB_ID --json
```

The token needs `schema.read` for discovery, `job.cancel` for the request, and
`job.read` for observation. The request checks the job's sandbox scope. A scoped
token must have a concrete sandbox target. The worker also checks the saved scope
before it changes a resource.

The initial response can have `state: requested` and `work_stopped: false`.
Provisioning can still be active. The server serializes cancellation with job
provisioning and observes backend state before returning a verified stopped result.
The request survives client termination. Daemon startup resumes unfinished requests.

A wait timeout adds `wait_timed_out: true`. The server request can continue.
Observe the same job instead of submitting another job:

```bash
agentlab --context development job show JOB_ID --json
```

Its optional `cancellation` field reports progress, resource IDs, cleanup, and
corrective actions. A verified result uses `state: cancelled` and
`work_stopped: true`. Temporary VM sandboxes are removed. Keepalive sandboxes remain
stopped with their recorded lease expiry. Workspace volumes remain available.
A legacy job status of `FAILED` preserves the existing status contract; the result
also contains `cancelled: true` and the cancellation details.

A blocked result requires inspection. It preserves resources when the target or
ownership changes, the saved scope is insufficient, or cleanup cannot be verified.
Shared session jobs and container jobs currently require their resource controls;
job cancellation stops before it changes those resources. A new request for an
already failed job also requires inspection. Its retained sandbox can contain
later user work. Completed jobs report that cancellation is not needed.

After you resolve a blocked requirement, repeat `job cancel` for the same job.
The worker inspects actual state before repeating cleanup. It preserves the
original sandbox and workspace binding. Late guest reports cannot overwrite a
completed cancellation result.
