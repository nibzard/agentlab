# Drive AgentLab as a coding agent

As a coding agent, you drive AgentLab through the CLI with `--json`. The loop is
the same each time: discover what exists, submit one job,
read the result, then tear the sandbox down. This page walks one full loop and
gives you the parsing rules that make it reliable.

This page is the machine-facing companion to the
[Agent JSON output reference](../reference/agent-json-output.md). For the
concepts behind isolation and credentials, read
[How AgentLab isolates and credentials an agent](../explanation/how-agentlab-isolates-and-credentials-an-agent.md).

## Prerequisites

- The `agentlab` CLI can reach the daemon. Use the Unix socket or a remote
  `--endpoint`.
- You have a profile name to use. If you do not, Step 1 finds one.
- You pass `--json` on every call.

Pass `--json` explicitly in scripts. A saved `output-format=json` preference is
also supported; `--json=false` overrides it. Flags may precede or follow operands.

## The agent loop

Each task has five core phases, plus two optional ones:

1. **Discover** the schema and the available profiles.
2. **Choose** an unattended job or an interactive sandbox journey.
3. **Submit** one job after validation; it provisions its own sandbox.
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

2. **Choose one execution journey.**

   For an unattended task, continue to `job run` below. It allocates its own
   sandbox; read `sandbox_vmid` from `job show` to discover that sandbox. Do not
   create a separate sandbox for the same job.

   For interactive work instead, create a standalone sandbox and observe its
   operation. A retry with the same key and payload returns the same operation.

    ```bash
    agentlab sandbox new --profile <profile> --no-wait --idempotency-key <unique-task-key> --json
    agentlab operation wait <operation_id> --timeout 10m --json
    agentlab ssh <vmid> --exec
    ```

   A client timeout or interrupt stops waiting; it does not cancel provisioning.
   Resume with `operation show` or `operation wait` using the same ID. An
   `unknown` outcome after daemon restart requires inspecting its resource VMID
   before creating another resource. Existing blocking creation remains available
   when `--no-wait` and `--idempotency-key` are omitted.

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
    agentlab job wait <job_id> --timeout 10m --json
    agentlab job show <job_id> --json --events-tail 20
    agentlab job list --json
    ```

   Job status transitions are `QUEUED -> RUNNING -> (COMPLETED | FAILED | TIMEOUT)`.
   Match the uppercase strings exactly. A guest exit code of `124` maps to
   `TIMEOUT`.

   Read the full outcome from one `job show`:

   - The terminal status is the verdict.
   - `result.message` is the human-readable exit or failure text.
   - `result.result` is free-form JSON.

   The optional `outcome.exit_code` and `outcome.duration_seconds` project recorded
   runner fields. Missing historical values remain absent; a completed state
   does not imply a recorded exit code. The full guest report also remains in
   `report.json` inside the artifact tarball (Step 5). For how the
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
   `POST /v1/exec`. Accepted execution returns HTTP 200; malformed requests and
   authorization failures return HTTP errors:

    ```json
    { "exit_code": 0, "stdout": "...", "stderr": "..." }
    ```

   Branch on `exit_code`, not on the HTTP status. `0` means success. `124` means
   the call timed out. The default timeout is five minutes and the maximum is
   one hour. `effective_timeout_seconds` reports the applied limit. Capture output
   is capped at 4 MiB per stream; `stdout_truncated`/`stderr_truncated` and captured/
   dropped byte counts report loss. Streaming has a combined 4 MiB cap and emits
   `truncation` events before the terminal event. Use job artifacts for larger
   results. `timed_out` and `cancelled` distinguish deadline expiry (124) from
   cancellation (130) and ordinary process failures.

    !!! warning "/v1/exec is full-access only"
        `POST /v1/exec` runs an agentlab verb on the daemon host and is
        full-access only. A scoped SSH token is rejected with `403`. See
        [How AgentLab isolates and credentials an agent](../explanation/how-agentlab-isolates-and-credentials-an-agent.md).

## Keep a long task alive

A job normally destroys its sandbox when it finishes. Pass `--keepalive` to
`job run` to prevent that auto-destroy. Keepalive also keeps the workspace
lease-renewal loop running for the job.

For a sandbox you provisioned yourself, renew its lease before it expires:

```bash
agentlab sandbox lease renew --ttl <minutes> <vmid>
```

Lease renewal is `RUNNING`-only. In any other state the daemon returns `409`.
`--ttl` is accepted before or after the VMID.

## Tear down

Destroy the sandbox when you are done:

```bash
agentlab sandbox destroy <vmid>
```

Both normal and forced destruction stop the VM, detach persistent storage,
and release leases still owned by that work. `--force` is a compatibility alias
for the same cleanup path. Persistent workspace volumes survive destruction;
inspect `workspace list` and lease metadata before reuse. Jobs normally clean up
their own sandbox unless keepalive was requested.

## Executable one-job journey

This Bash recipe needs `jq`, configured daemon access, and `REPO` and `PROFILE`
environment variables. Set `TASK` and `OUT_DIR` as desired. It creates one job
with keepalive, collects a recorded artifact, and then destroys that job's
sandbox. Validation failure creates nothing. A wait timeout leaves work running
and prints its resume guidance; it does not trigger cleanup of active work.
A terminal failed job still collects its artifacts and cleans up, then exits 1.

<!-- agentlab:one-job-recipe -->
```bash
set -euo pipefail
: "${REPO:?Set REPO to a cloneable repository URL}"
: "${PROFILE:?Set PROFILE to an installed profile name}"
TASK=${TASK:-run tests}
OUT_DIR=${OUT_DIR:-./out}
mkdir -p "$OUT_DIR"
agentlab schema > "$OUT_DIR/schema.json"
agentlab profile list --json > "$OUT_DIR/profiles.json"
agentlab job validate --repo "$REPO" --profile "$PROFILE" --task "$TASK" --json > "$OUT_DIR/plan.json"
agentlab job run --repo "$REPO" --profile "$PROFILE" --task "$TASK" --keepalive --json > "$OUT_DIR/job.json"
job_id=$(jq -er '.id' "$OUT_DIR/job.json")
wait_code=0
agentlab job wait "$job_id" --timeout 10m --json > "$OUT_DIR/wait.json" || wait_code=$?
if ! jq -e '.observation' "$OUT_DIR/wait.json" >/dev/null; then
  cat "$OUT_DIR/wait.json"
  exit "$wait_code"
fi
agentlab job show "$job_id" --events-tail 20 --json > "$OUT_DIR/result.json"
agentlab job artifacts "$job_id" --json > "$OUT_DIR/artifacts.json"
artifact_path=$(jq -r '.artifacts[0].path // empty' "$OUT_DIR/artifacts.json")
if [ -n "$artifact_path" ]; then
  agentlab job artifacts download "$job_id" --path "$artifact_path" --out "$OUT_DIR/" --json > "$OUT_DIR/download.json"
fi
vmid=$(jq -r '.sandbox_vmid // empty' "$OUT_DIR/result.json")
if [ -n "$vmid" ]; then
  agentlab sandbox destroy "$vmid" --json > "$OUT_DIR/cleanup.json"
fi
exit "$wait_code"
```
<!-- agentlab:one-job-recipe:end -->

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
