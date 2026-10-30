# Mint and use scoped tokens for an agent

Give an agent a narrow, short-lived credential instead of the full-control token.
A scoped token lets the agent run a fixed set of commands against a fixed
sandbox, and nothing more. The token is signed with an SSH key on your machine,
so the daemon stores no bearer credential for it. Revocation records contain
identifiers and audit metadata.

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
   namespace unless you accept every permission under it. Choose an unused
   protected output file for the credential.

    ```bash
    umask 077
    agentlab token create \
      --key ~/.ssh/id_ed25519 \
      --cmds schema.read,status.read,host.read,sandbox.read,sandbox.start,sandbox.stop,sandbox.lease \
      --scope sandbox:<vmid> \
      --ttl 8h \
      --subject worker-7 --json=false > "$token_file"
    ```

   `--cmds` is required and becomes the `cmds` claim. `--ttl` defaults to one
   hour. The token has the form
   `agentlab.<header>.<claims>.<signature>`, where the signature is the SSH
   signer over the `header.claims` string. Signing is client-side only.

3. **Inspect the token before you hand it out.**

   Decode the claims to confirm what you minted:

    ```bash
    agentlab token inspect --token-stdin --json < "$token_file"
    ```

    !!! warning "Inspect does not verify the signature"
        `token inspect` decodes the claims through an unverified parse. Use it
        for display and trusted credential identification. Never treat these
        claims as proof that an action is authorized.

4. **Hand the token to the agent.**

   Use protected stdin to save a named connection without putting the credential
   in arguments, shell history, or chat:

    ```bash
    agentlab connect --context "$task_context" --endpoint https://daemon.example:8844 --token-stdin < "$token_file"
    agentlab --context "$task_context" sandbox show "$vmid" --json
    ```

   The example grants the read permissions used by connection and capability
   discovery. Named contexts keep a credential reference in protected local state.
   The current fallback stores credentials in mode `0600` files. Pass an explicit
   context when several agents use different servers.

5. **Rotate and revoke.**

   Keep `--ttl` short. Read `token_revocation` in the selected server's capabilities
   before revoking a signed token. Use a credential management context with
   `schema.read`, `token.revoke`, and `token.read`, with no sandbox scope.
   Ordinary task credentials do not need those management permissions.

   Secure inspection returns `issuer` and `revocation_id`. The latter binds the
   issuer and signed `jti` through SHA-256. Neither identifier grants access.
   Using an authorized management context, save and observe the revocation:

    ```bash
    agentlab --context "$management_context" token revoke --issuer "$issuer" "$revocation_id" --json
    agentlab --context "$management_context" token status --issuer "$issuer" "$revocation_id" --json
    ```

   The server requires its expected identity before saving the revocation.
   Identical retries preserve the first actor and timestamp. Records survive daemon
   restarts and remain after token expiry. Reusing the same signed ID does not
   restore access. Revocation applies to the selected server; repeat it on other
   servers that accept that credential when they are in the authorized scope.

   Every new remote request checks signature, expiry, and durable revocation state.
   Revoked credentials receive HTTP 401. A failed status lookup receives HTTP 503.
   The dashboard preserves those responses. No alternate transport or legacy-token
   fallback overrides a revocation failure.

   Already authenticated requests can finish. Revocation does not cancel server
   jobs or independent guest credentials. Use authorized job cancellation when work
   must stop, then verify its resource outcome.

   A token without `jti` cannot be revoked individually. The legacy static control
   token also has no per-token identity; `legacy_token_revocation` remains false.
   Retire a signing key to invalidate all its tokens. Remove its public key from
   the daemon `authorized_keys` and restart the daemon to load the new key set.
   Rotate the configured control token to replace a legacy static credential.
   Per-token revocation does not retire the signing key or prevent fresh token IDs.

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
4. **Collection routes have specific rules.** Lists filter resources to the
   sandbox scope. Global mutations, including source uploads and credential
   management, reject sandbox-scoped credentials. Read the route schema instead
   of assuming that a route without one VMID ignores scope.

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
agentlab connect --context "$task_context" --endpoint http://daemon.tail-xxxx.ts.net:8844 --allow-insecure-http --token-stdin < "$token_file"
agentlab --context "$task_context" sandbox show "$vmid" --json
```

HTTPS and loopback HTTP are always allowed.

## Verify

- `token inspect --token-stdin --json` shows the `cmds`, `scope`, and `expires`
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

## Enroll a task context

Use an existing remote context to identify the server. Keep the signing key with
its operator. The public key must already be authorized by the daemon; enrollment
does not register another signing key or change the server's trust configuration.

```bash
agentlab --context "$management_context" token enroll --save-context "$task_context" --key "$signing_key" --cmds schema.read,status.read,host.read,sandbox.read,token.renew --scope "sandbox:$vmid" --ttl 1h --subject worker-7 --json
```

Enrollment signs locally, verifies the candidate through the remote API, and saves
its credential reference. It does not print the credential. Connection and storage
failures preserve an existing destination. A concurrent destination change blocks
publication. The source context remains active when it was already selected.

The example includes discovery permissions because enrollment verifies capabilities
with the candidate. It includes `token.renew` for the renewal protocol. Select
only the task permissions you authorize. A delegated agent needs the credential,
not the operator's private signing key.

## Renew without changing permissions

Start renewal before expiry. Include `schema.read` for CLI identity checks and
`token.renew` for renewal. Use a stable request ID for one replacement. Choose
a different ID for a later replacement. Renewal keeps the issuer, subject,
commands, and sandbox scope. The replacement lifetime is from one minute to
24 hours. The first attempt requires the matching operator key. A pending attempt
can resume with its protected staged credential and public signing metadata.

```bash
agentlab token renew --context "$task_context" --request-id "$renewal_key" --key "$signing_key" --ttl 1h --json
agentlab token renew --context "$task_context" --request-id "$renewal_key" --ttl 1h --json
```

The server prepares one candidate per parent. Commit revokes the parent and saves
an immutable receipt in one transaction. Identical commit retries return that
receipt. The client stages the candidate before preparation. If the commit reply
is lost, resume the same request. The original context can still contain the now
revoked parent until recovery publishes the staged replacement. Do not mint a
new candidate to bypass that state.

A concurrent context change blocks publication. Preserve the staged context shown
in the error. It remains a protected, usable signed credential unless expired or
revoked. Inspect its binding before choosing it or restoring the original target.
An expired staged credential requires explicit operator enrollment of a new
credential. Server records and local receipts remain available for inspection.
Completed request IDs retain their original results across later renewals.

The JSON result includes `state`, `cleanup_status`, public context references,
credential identifiers, expiry, and the server receipt. It contains no credential
or private key. `published` records completed publication; it does not prove that
an expired or subsequently revoked credential still works. Dashboards bound to
verified contexts adopt a compatible saved replacement without another enrollment.
Explicit credential overrides keep their startup credential and require restart.
See the [dashboard guide](run-the-dashboard.md#renew-a-saved-credential) for limits.

SSH signing keys remain a broad authority in the existing model. A key holder can
mint independent credentials. Per-token revocation does not revoke an issuer or
all descendants. Tokens can also authenticate on other servers that trust that
issuer. This renewal targets one verified server and preserves its current scope.
Revoke each credential that must stop, and retire the signing key when its signing
authority must end. Renewal never uses the trusted socket as an authorization fallback.
