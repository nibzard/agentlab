# Coordinate multiple agents

Goal: act as an orchestrator agent that provisions a worker sandbox, gives the
worker durable state without sharing one volume, coordinates over the shared
messagebox, delegates a least-privilege token, and hands the work off to a human
reviewer. You will use workspace forks, scoped tokens, integration proxies, and
the message log together.

## Prerequisites

- The `agentlab` CLI can reach the daemon. Run
  [Drive AgentLab as a coding agent](../how-to/drive-agentlab-as-a-coding-agent.md)
  first for the single-agent loop this tutorial extends.
- A profile that attaches a workspace on ZFS storage, so the volume supports
  snapshots and forks. The bundled `yolo-workspace` profile works.
- An SSH signing key whose public half is registered in the daemon
  `authorized_keys`, so you can mint a scoped token. See
  [Mint and use scoped tokens for an agent](../how-to/mint-scoped-tokens-for-an-agent.md).
- Two strings agreed out-of-band with the worker and the human reviewer: a
  shared `scope_id` and a small set of message `kind` values (used in step 5).

## Steps

1. Discover profiles and confirm the schema version before you provision.

    ```bash
    agentlab schema
    agentlab profile list
    ```

   `agentlab schema` always prints JSON and ignores `--json`. Read
   `api_schema_version` from `agentlab status` before you assume a field exists.
   For the full machine-readable contract, see
   [Agent JSON output reference](../reference/agent-json-output.md).

2. Provision an agent-ready sandbox for the sibling worker. Pass the task with
   `--prompt`, keep it alive, and tag it for ownership.

    ```bash
    agentlab sandbox new --profile <profile> --keepalive --ttl 120 \
      --name build-runner-7 --tag owner:alice \
      --prompt "Run the test suite, then post the result."
    ```

   The top-level `vmid` in the JSON response is the worker VMID. Track it. The
   daemon stores the prompt verbatim and surfaces it on the metadata service at
   `169.254.169.254` as `metadata.prompt`. The booted guest must query that
   service itself. Nothing in AgentLab executes the prompt for you. Poll until
   the sandbox is ready:

    ```bash
    agentlab sandbox show <vmid> --json
    ```

   Use the sandbox when `state` is `RUNNING` and `ip` is non-empty. See
   [State machine](../reference/state-machine.md) for every transition. To find
   the sandbox again later, list all sandboxes and filter the JSON client-side;
   the `tags` you set travel in the response. There is no server-side tag filter.

    ```bash
    agentlab sandbox list --json
    ```

   !!! note "--prompt is data, not an instruction to the daemon"
       The prompt is delivered through the link-local metadata service, which is
       restricted to the agent subnet. Only the guest can read it. If your image
       does not query `169.254.169.254`, the prompt is inert.

3. Delegate a least-privilege token and a credential to the worker. Mint a token
   that lets the worker read, start, stop, and renew the lease on its sandbox,
   and nothing else. Enumerate the permissions. The bare namespace `sandbox`
   would also grant `sandbox.destroy` and `sandbox.bulk`.

    ```bash
    agentlab token create --key ~/.ssh/id_ed25519 \
      --cmds sandbox.read,sandbox.start,sandbox.stop,sandbox.lease \
      --scope sandbox:<vmid> --ttl 8h
    ```

   Hand the printed token to the worker as the `AGENTLAB_TOKEN` environment
   variable. Do not pass it on the command line or write it to shell history.
   For the full token model, see
   [Mint and use scoped tokens for an agent](../how-to/mint-scoped-tokens-for-an-agent.md).

   !!! warning "The signing key is the trust root"
       Anyone who holds the SSH private key can mint a full-access token with
       `--cmds '*'`. Keep the private key off the worker host. Give the worker
       only the minted token string.

   Give the worker a credential without copying the secret into the VM. Register
   an integration proxy. Inside the VM the worker targets the proxy URL, and the
   daemon injects the secret at the proxy.

    ```bash
    agentlab integration add --name upstream-api --type http-proxy \
      --target https://api.example.com --secret <new-token> \
      --secret-type bearer --attach sandbox:build-runner-7
    ```

   Inside the sandbox the worker calls
   `http://169.254.169.254/proxy/upstream-api/...`. The secret never enters the
   VM. For how the proxy identifies the caller, and why `auto:all` is not "serve
   to anyone", see
   [How AgentLab isolates and credentials an agent](../explanation/how-agentlab-isolates-and-credentials-an-agent.md).

4. Share durable state safely. Fork the workspace, never attach one volume
   twice. A workspace attaches to exactly one sandbox at a time. Attach is gated
   by a query that requires `attached_vmid` to be empty, plus a workspace lease.
   You cannot attach the same volume to two agents at once.

   Detach the source workspace, clear its lease, snapshot it, then fork. Snapshot
   and fork require the workspace detached and unleased:

    ```bash
    agentlab workspace detach <source-workspace>
    agentlab workspace lease clear <source-workspace>
    agentlab workspace snapshot create <source-workspace> handoff-snap
    agentlab workspace fork <source-workspace> --name ws-worker --from-snapshot handoff-snap
    ```

   The fork is an independent volume. The worker now creates a session on it and
   resumes, which provisions a sandbox and attaches the volume at `/work`:

    ```bash
    agentlab session create --name worker-session --profile <profile> --workspace ws-worker
    agentlab session resume worker-session
    ```

   `session resume` rebuilds the VM from the profile and reattaches the same
   workspace volume, so `/work` survives sandbox replacement. `session stop`
   destroys the VM but keeps the session row and the workspace binding. For the
   persistence model, see
   [Workspaces, sessions, and sandboxes](../explanation/workspaces-sessions-sandboxes.md).

   !!! warning "Do not attach one volume twice"
       Two writers on one workspace volume corrupt the filesystem. Always fork or
       snapshot first. The daemon refuses the second attach, but plan around the
       rule instead of relying on the error.

5. Coordinate over the shared messagebox. The messagebox is an append-only log
   keyed by scope. Post a task scoped to the worker session:

    ```bash
    agentlab msg post --session worker-session \
      --author orchestrator --kind task \
      --text "Run tests on ws-worker and post a result."
    ```

   The worker tails the same scope for new messages:

    ```bash
    agentlab msg tail --session worker-session --follow
    ```

   `--follow` is HTTP polling. The CLI calls
   `GET /v1/messages?after_id=<cursor>` every two seconds. It is not a push
   stream. For the routes behind these commands, see
   [HTTP API reference](../reference/http-api.md).

   !!! warning "scope_id is free-form text"
       `scope_id` is never checked against a real job, workspace, or session. A
       typo opens a dead channel that no one reads. Agree on the exact `scope_id`
       (here the session name `worker-session`) with the other agents
       out-of-band before you post.

   The messagebox is broadcast, not point-to-point. There is no `recipient`
   field. Every reader on the same scope sees every message. Filter client-side
   by `author` and `kind`. The `kind` field has no enforced taxonomy. The values
   `task`, `result`, `note`, and `handoff` are conventions this tutorial uses.

   To resume a crashed tail, use the monotonic message id as the cursor, not the
   timestamp. Each response carries `last_id`. Replay from there, then re-enter
   `--follow`:

    ```text
    GET /v1/messages?scope_type=session&scope_id=worker-session&after_id=<last_id>
    ```

6. Migrate or hand back state. When the worker finishes and you want its `/work`
   on a different profile, rebind the workspace. Rebind builds a fresh VM from
   the new profile and reattaches the same volume. Keep the old sandbox with
   `--keep-old` if you are not sure the rebind will succeed:

    ```bash
    agentlab workspace rebind ws-worker --profile <review-profile> --ttl 120 --keep-old
    ```

   To bring a stopped worker session back instead, resume it. Resume provisions a
   new VM and reattaches the same volume, so the worker files return:

    ```bash
    agentlab session resume worker-session
    ```

7. Hand off to the human and tear down. Detach the workspace from the worker
   sandbox and attach it to the human sandbox, one operation at a time. Then post
   a `handoff` message so the human knows the state is ready:

    ```bash
    agentlab workspace detach ws-worker
    agentlab workspace attach ws-worker <human-vmid>
    agentlab msg post --session worker-session --author orchestrator \
      --kind handoff --text "State ready in ws-worker; please review."
    ```

   Destroy the worker sandbox when the human confirms the handoff. Add `--force`
   only if the sandbox is stuck in an invalid state:

    ```bash
    agentlab sandbox destroy <vmid>
    ```

## Expected result

`agentlab sandbox list --json` shows the worker sandbox destroyed and the
workspace attached to the human sandbox. `agentlab msg tail --session
worker-session` shows the `task`, `result`, and `handoff` messages in order. The
worker `/work` contents are intact on the new VM, because they lived on the
forked workspace volume rather than the ephemeral root disk.

## Next

- For the JSON fields each command returns, see
  [Agent JSON output reference](../reference/agent-json-output.md).
- For the single-agent loop this tutorial builds on, see
  [Drive AgentLab as a coding agent](../how-to/drive-agentlab-as-a-coding-agent.md).
- For the token limits and revocation model, see
  [Mint and use scoped tokens for an agent](../how-to/mint-scoped-tokens-for-an-agent.md).
- For the credential proxy and isolation boundaries, see
  [How AgentLab isolates and credentials an agent](../explanation/how-agentlab-isolates-and-credentials-an-agent.md).
