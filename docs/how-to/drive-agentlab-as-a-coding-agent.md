# Drive AgentLab as a coding agent

As a coding agent, you drive AgentLab through the CLI with `--json`. The loop is
the same each time: discover what exists, validate a job, submit the job,
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
