# How AgentLab isolates and credentials an agent

This page explains isolation and credential delivery from the agent's point of
view. It covers the two places a command can run, how an agent gets a secret
without ever holding it, how the agent's own tools get confined, and the hard
limits an agent must respect. It complements the operator-facing models rather
than repeating them. For the broader trust model, see
[Control plane and trust boundaries](control-plane-and-trust-boundaries.md);
for the network around the VM, see
[Network isolation model](network-isolation-model.md).

## The agent's two boundaries

An agent always works across two boundaries at once.

- **Inside the sandbox VM** is where the agent's code, shell calls, and cloned
  repo live. The VM is network-isolated on the agent subnet and is treated as
  untrusted. Egress is governed by the outer VM and LXC firewall, not by the
  agent.
- **On the daemon host** is where the control plane runs. Every privileged
  operation funnels through the `agentlabd` daemon. An agent never calls
  `qm`, `pvesh`, or `pvesm` directly. See
  [Architecture](architecture.md) for how the daemon is wired.

This split is why the same verb (`sandbox list`, `job show`) can mean two
different things depending on which boundary runs it. The next section makes the
distinction concrete.

## Two ways to run a command

AgentLab gives an agent two execution paths. They look similar but run on
opposite sides of the boundary, and the difference changes what an agent can do.

| Path | Runs on | Identity required | Returns |
| --- | --- | --- | --- |
| `POST /v1/exec` | The daemon host | Full access only | `{exit_code, stdout, stderr}`, always HTTP 200 |
| `agentlab ssh <vmid> --exec` | Inside the sandbox VM | The sandbox lease | The remote command's exit code |

`POST /v1/exec` spawns the `agentlab` CLI on the **daemon host**. It is the
generic escape hatch: it can invoke any verb, including ones that touch
resources outside any scoped claim. For that reason it is restricted to
full-access identities. A scoped SSH token is rejected with `403 exec requires
a full-access token`; only the Unix socket, the legacy control token, or a
token with commands `["*"]` and empty scope pass. See
[Mint and use scoped tokens for an agent](../how-to/mint-scoped-tokens-for-an-agent.md)
for why scoped delegation deliberately cannot reach `/v1/exec`.

`agentlab ssh <vmid> --exec -- <cmd>` runs an arbitrary command **inside the
sandbox VM**. The CLI replaces its own process with `ssh` through `syscall.Exec`,
so the exit code you get back is the remote command's exit code. Without
`--exec`, the command is only printed, not run.

```bash
# Run a command inside the VM; its exit code becomes the shell exit code
agentlab ssh <vmid> --exec -- make test

# Print the connection details instead (mutually exclusive with --exec)
agentlab ssh <vmid> --json
```

```text
HTTP exec path (not an agentlab verb; call it as an HTTP request):

POST /v1/exec  { "command": "sandbox list --json" }
```

!!! note "`--json` and `--exec` are mutually exclusive"
    `agentlab ssh` rejects `--json` together with `--exec`. To drive the VM
    programmatically, create the sandbox with `--json` first, then call
    `agentlab ssh <vmid> --exec` separately. See the
    [Agent JSON output reference](../reference/agent-json-output.md).

## Credentials without holding secrets

An agent usually needs a token or API key. AgentLab can deliver one without ever
putting the secret inside the VM. The mechanism is the **credential proxy**, a
set of routes the daemon serves on the bootstrap listener at
`http://169.254.169.254/proxy/<name>/...`.

You register an `http-proxy` or `git-proxy` integration on the daemon. The
daemon stores the secret (encrypted at rest) and injects it at the proxy as the
request leaves. The secret is never returned by the API, and never enters the
VM disk. Inside the VM, the agent simply targets the proxy URL.

```bash
# Register a reverse proxy that injects a bearer token at the daemon
agentlab integration add --name myapi --type http-proxy \
  --target https://api.example.com --secret sk-live-example \
  --secret-type bearer --attach sandbox:<name>
```

Inside the VM, the agent calls the proxy and never sees the secret:

```bash
curl -s http://169.254.169.254/proxy/myapi/v1/widgets
```

The injection mode controls how the daemon adds the secret:

- **bearer** adds `Authorization: Bearer <secret>`.
- **header** sets a custom header to the secret value. The header defaults to
  `X-Api-Key` when you do not set `--secret-header`.
- **basic-auth** adds `Authorization: Basic <base64(user:secret)>`. The git
  proxy always uses basic auth with the username from `--username`.

!!! note "There is also an llm-proxy type"
    The `llm-proxy` integration forwards OpenAI-compatible requests to a
    provider with daemon-held credentials. It uses the same proxy path and the
    same identification rules. See [Secrets delivery model](secrets-delivery-model.md)
    for how this compares with the age or sops host-secrets model.

## Attach modes and per-request identification

Every credential-proxy request is scoped to a sandbox. The scoping is enforced
per request by **source IP**, not by a token the agent presents.

The proxy resolves the calling sandbox with `GetLiveSandboxByIP`, then checks
the integration's attach mode against that sandbox. The three attach modes map
to `MatchesSandbox` checks:

| Attach mode (`--attach`) | Matches when |
| --- | --- |
| `auto:all` | Any identified live sandbox |
| `sandbox:<name>` | The sandbox name equals `<name>` |
| `tag:<value>` | The sandbox carries tag `<value>` |

An unidentified, stale, or ambiguous source is rejected with `403 sandbox not
identified for integration access`. The only exception is `auto:all` under an
explicit, warned `trust_agent_subnet` opt-in, which serves any host in the agent
subnet without resolving it to a sandbox.

!!! warning "`auto:all` is not 'serve to anyone'"
    Identification is by source IP only, matched against a single **live**
    sandbox. If two live sandboxes share an IP, or the database still holds a
    stale IP, the request is rejected with 403. `auto:all` widens which
    identified sandbox matches; it does not skip identification.

## The inner bubblewrap layer

The outer VM is the first boundary. A profile can add a second boundary inside
the VM that confines the agent's own tool execution. Set
`behavior.inner_sandbox: bubblewrap` (plus optional `inner_sandbox_args`) in the
profile. The guest `agent-runner` then prepends a `bwrap` argv to the agent
command, so every shell call and tool invocation runs inside the nested sandbox.

The `bwrap` prefix is:

```text
bwrap --die-with-parent --unshare-all --share-net \
  --ro-bind / / --proc /proc --dev /dev \
  --bind /tmp /tmp --bind /var/tmp /var/tmp --bind /run /run \
  --bind $HOME $HOME --bind $REPO_DIR $REPO_DIR \
  --ro-bind $SECRETS_DIR $SECRETS_DIR --
```

What the inner layer confines and what it does not:

- It creates new **pid, user, mount, ipc, and uts** namespaces (`--unshare-all`).
- It **keeps the network namespace** (`--share-net` after `--unshare-all`). It
  does not isolate network egress.
- The repo and home tree are writable; the secrets directory is read-only.
- Egress is still governed by the outer VM and LXC `firewall_group`, not by
  `bwrap`.
- `bwrap` must be installed in the guest image. If it is missing, the job fails
  with `inner sandbox requested but bubblewrap (bwrap) not installed`.

See [Use the inner bubblewrap sandbox](../how-to/use-the-inner-bubblewrap-sandbox.md)
for the operator setup, and [Guest runner flow](guest-runner-flow.md) for where
the prefix is applied in the boot sequence.

## What is and is not auto-configured

A common assumption is that registering a proxy also rewrites the agent's
environment. It does not.

- Nothing injects `HTTP_PROXY` or `HTTPS_PROXY` into the sandbox. The agent must
  target the proxy URL explicitly.
- Nothing writes a git `insteadOf` rule. The helper
  `GitProxyConfigURL(integ, metadataBaseURL)` exists and returns an
  `insteadOf` string, but it has no in-repo caller. To clone through the git
  proxy, the agent must request the proxy URL directly, for example
  `http://169.254.169.254/proxy/github/user/repo.git`.

If you want git to use the proxy automatically, write the `insteadOf` rule
yourself in the VM from the agent's bootstrap step.

## Limits an agent must respect

An agent that drives these paths programmatically hits several hard limits. Plan
for them up front.

- `/v1/exec` output is capped at 4 MiB per stream (stdout and stderr each).
  In capture mode the output is silently truncated when the cap is reached. Do
  not rely on the last bytes of a large build log arriving intact.
- `/v1/exec` timeout defaults to 5 minutes. A caller can request up to 1
  hour; values above the ceiling are clamped silently, and zero or negative
  means the 5-minute default, not unlimited. A timeout produces `exit_code 124`,
  not an HTTP error. The response is always HTTP 200, so branch on `exit_code`.
- `ssh` is non-interactive. The CLI launches `ssh` with `BatchMode=yes`,
  `IdentitiesOnly`, `StrictHostKeyChecking=no`, and
  `UserKnownHostsFile=/dev/null`. These options exist so agents run reliably
  without a human at a terminal.
- The default ssh identity is `/etc/agentlab/keys/agentlab_id_ed25519`, or
  the path in `$AGENTLAB_SSH_IDENTITY`. Override it with `--identity`.
- Runner reports use a separate listener. The guest runner posts status to
  `POST /v1/runner/report` on the bootstrap listener (the agent subnet). That
  route takes no bearer token and is gated by agent-subnet source IP; it is not
  on the control listener. See [Guest runner flow](guest-runner-flow.md).

## How it fits together

- To drive a single sandbox end to end, see
  [Drive AgentLab as a coding agent](../how-to/drive-agentlab-as-a-coding-agent.md).
- To coordinate sibling agents, share state, and hand off over the messagebox,
  see [Coordinate multiple agents](../tutorials/coordinate-multiple-agents.md).
- To delegate a least-privilege credential, see
  [Mint and use scoped tokens for an agent](../how-to/mint-scoped-tokens-for-an-agent.md).
- For the machine-readable contract behind every command, see the
  [Agent JSON output reference](../reference/agent-json-output.md).
