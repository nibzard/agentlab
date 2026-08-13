# Mint and use scoped tokens for an agent

Give an agent a narrow, short-lived credential instead of the full-control token.
A scoped token lets the agent run a fixed set of commands against a fixed
sandbox, and nothing more. The token is signed with an SSH key on your machine,
so the daemon stores no secret for it.

This page is the focused authorization guide for agent delegation. For the
broader trust model, read
[Control plane and trust boundaries](../explanation/control-plane-and-trust-boundaries.md).
For how an agent uses the token to drive a sandbox, read
[Drive AgentLab as a coding agent](drive-agentlab-as-a-coding-agent.md).

## Prerequisites

- An SSH key pair for token signing. The private key stays with you. Only its
  public key is registered with the daemon.
- A daemon that is reachable over a remote HTTP `--endpoint`. Scope limits apply
  only on the TCP/HTTP path.
- A sandbox VMID to scope the token to, when you want to bind the agent to one
  VM.
- The daemon `authorized_keys` file, writable by an operator.

!!! warning "Scope is enforced only on the remote path"
    Command and scope checks run only for remote, SSH-signed tokens. The local
    Unix socket carries no identity and is a full-access trusted path. Delegate
    a token only to an agent that calls a remote `--endpoint`.

## Steps

1. **Register the signing public key.**

   The daemon trusts tokens signed by keys in its `authorized_keys` file. Print
   the public half of your signing key:

    ```bash
    cat ~/.ssh/id_ed25519.pub
    ```

   Add that line to the daemon `authorized_keys` file as an operator. Confirm
   the fingerprint the daemon uses as the token issuer, the `iss` claim:

    ```bash
    agentlab token list --key ~/.ssh/id_ed25519
    ```

   The `Fingerprint` field is the SHA-256 fingerprint of the public key. The
   daemon looks up signing keys by this value when it verifies a token.

2. **Mint a least-privilege token.**

   List the exact command permissions the agent needs. Do not grant a bare
   namespace unless you accept every permission under it.

    ```bash
    agentlab token create \
      --key ~/.ssh/id_ed25519 \
      --cmds sandbox.read,sandbox.start,sandbox.stop,sandbox.lease \
      --scope sandbox:<vmid> \
      --ttl 8h \
      --subject worker-7
    ```

   `--cmds` is required and becomes the `cmds` claim. `--ttl` defaults to one
   hour. The token has the form
   `agentlab.<header>.<claims>.<signature>`, where the signature is the SSH
   signer over the `header.claims` string. Signing is client-side only.

3. **Inspect the token before you hand it out.**

   Decode the claims to confirm what you minted:

    ```bash
    agentlab token inspect <new-token>
    ```

    !!! warning "Inspect does not verify the signature"
        `token inspect` decodes the claims through an unverified parse. Use it
        for display only. Never use its output to make an access decision.

4. **Hand the token to the agent.**

   Pass the token as an environment variable, not as a shell flag. This keeps
   it out of process listings and shell history.

    ```bash
    export AGENTLAB_TOKEN=<new-token>
    export AGENTLAB_ENDPOINT=https://daemon.example:8844
    agentlab sandbox show <vmid>
    ```

   The bearer is sent only when an endpoint is set. Token precedence is flag,
   then environment variable, then `client.json`. The CLI forces `client.json`
   to mode `0600`.

5. **Rotate and revoke.**

   Keep `--ttl` short as the primary control. There is no per-token revocation
   list: the `jti` claim is generated but never checked. To revoke at once,
   remove the public key from the daemon `authorized_keys` file and reload the
   daemon:

    ```bash
    sudo systemctl reload agentlabd
    ```

   Removing one key invalidates every token signed by that key on the next
   request.

## What the token grants

Command matching is exact, `*` for all, or a dot-boundary namespace. The claim
`sandbox` matches `sandbox` and `sandbox.read`, but not `sandboxsecret`. The
claim `sandbox.read` matches `sandbox.read` but not `sandbox.readonly`.

| `cmds` claim | Grants |
|---|---|
| `sandbox.read` | only `sandbox.read` |
| `sandbox` | every `sandbox.*` permission, including `sandbox.destroy` and `sandbox.bulk` |
| `*` with empty scope | full access, unconstrained by per-route checks |

For least privilege, enumerate the specific permissions. Use entries such as
`sandbox.read`, `sandbox.start`, `sandbox.stop`, and `sandbox.lease`.

Sandbox scope is a list of `sandbox:<vmid>` entries. An empty scope means all
sandboxes. When the token carries a scope, the daemon checks it only on routes
that target one concrete VMID.

## What the token denies

Four limits hold even when the `cmds` and `scope` claims look permissive.

1. **Socket bypass.** The local Unix socket is full access. It carries no
   identity, so command and scope checks do not run. Limits apply only to
   remote, SSH-signed tokens.
2. **Bulk deny.** Any token that declares a scope is denied bulk and
   cross-sandbox operations. This holds even when `cmds` includes
   `sandbox.bulk` and the scope covers the targets. It covers
   `sandbox stop --all`, `sandbox prune`, and `sandbox reconcile`.
3. **Exec is full-access only.** `/v1/exec` and `/v1/exec/dry-run` reject a
   scoped SSH token with `403 exec requires a full-access token`. Only the Unix
   socket, the legacy static token, or a token with `cmds *` and an empty scope
   may call them.
4. **Create and list are not scope-bound.** Create, list, and collection routes
   have no single target VMID, so scope is not checked there. `list`
   additionally filters its results to in-scope VMIDs.

Do not hand the legacy control token, or an `*` SSH token with an empty scope,
to an agent. Both are unconstrained full-access credentials.

## Transport policy

An endpoint must include an explicit `http://` or `https://` scheme. A bare
host and port is rejected, so the bearer never travels as plaintext by
accident.

Plaintext HTTP to a non-loopback host needs an explicit opt-in, because the
bearer would cross the network in cleartext. Use it only inside a trusted
tunnel such as Tailscale:

```bash
export AGENTLAB_ALLOW_INSECURE_HTTP=1
agentlab --endpoint http://daemon.tail-xxxx.ts.net:8844 --token <token> sandbox show <vmid>
```

HTTPS and loopback HTTP are always allowed.

## Verify

- `agentlab token inspect <new-token>` shows the `cmds`, `scope`, and `expires`
  values you expect.
- With the token set, a permitted call such as `agentlab sandbox show <vmid>`
  returns the sandbox.
- A call outside the scope or commands returns
  `403 token is not authorized for <permission>`.

For the fields each command returns under `--json`, see the
[Agent JSON output reference](../reference/agent-json-output.md). For the
credential proxy that keeps secrets out of the VM, see
[How AgentLab isolates and credentials an agent](../explanation/how-agentlab-isolates-and-credentials-an-agent.md).
For token delegation across cooperating agents, see
[Coordinate multiple agents](../tutorials/coordinate-multiple-agents.md).
