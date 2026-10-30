# How to connect to a remote daemon over the tailnet

Point the `agentlab` CLI at a daemon that runs on another host, then drive it
and SSH into sandboxes from your workstation.

## Prerequisites

- The remote control plane is enabled on the host. Either topology is valid:
  - **Topology A (recommended):** `control_listen: 127.0.0.1:8845` published
    with `tailscale serve`.
  - **Topology B:** `control_listen` bound to the host tailnet IP, with
    `control_allow_cidrs` set to your tailnet range.
- `control_auth_token` is set. It is mandatory whenever `control_listen` is set,
  and wildcard binds are rejected without `control_allow_cidrs`.
- The `agentlab` CLI installed on your workstation, on the same tailnet.

## Steps

1. Save the endpoint, token, and (optional) jump host. The endpoint must include
   an explicit `http://` or `https://` scheme:

    ```bash
    agentlab connect --endpoint http://host.tailnet.ts.net:8845 --allow-insecure-http --token-stdin < /protected/daemon-token
    ```

   `connect` checks capabilities, server identity enforcement, and status before
   saving a verified context. Include `schema.read` and `status.read` in a scoped
   credential. Host metadata is optional; `host.read` is not needed for identity.
   The output identifies the credential store and identity verification result.
   Keep the input file at mode `0600`.

2. Confirm the connection:

    ```bash
    agentlab status
    ```

3. Remove the saved connection when you no longer need it:

    ```bash
    agentlab disconnect
    ```

## SSH into a sandbox remotely

`agentlab ssh` probes TCP/22 on the sandbox IP over the tailnet subnet route. If
the route is unreachable and a jump host is configured, it falls back to SSH
ProxyJump automatically.

```bash
agentlab ssh <vmid>
agentlab ssh <vmid> --jump-host host.tailnet.ts.net --jump-user <user>
```

Save jump defaults once with `connect` so later `agentlab ssh` calls use them:

```bash
agentlab connect --endpoint http://host.tailnet.ts.net:8845 --allow-insecure-http \
  --jump-host host.tailnet.ts.net --jump-user <user> --token-stdin < /protected/daemon-token
```

## Verify

- `agentlab status` returns the status snapshot from the remote daemon.
- `agentlab ssh <vmid>` resolves to a direct or ProxyJump SSH command.

!!! warning "Plaintext HTTP needs an explicit opt-in"
    Plaintext HTTP to a non-loopback host is rejected unless you pass
    `--allow-insecure-http` or set `AGENTLAB_ALLOW_INSECURE_HTTP`, and only
    inside a trusted tunnel such as Tailscale. Precedence, highest to lowest, is
    CLI flags, then `AGENTLAB_ENDPOINT` / `AGENTLAB_TOKEN`, then the selected context
    or legacy `client.json`.

For a copy-paste quickstart that includes `agentlab bootstrap`, see
[Enable remote tailnet access](../tutorials/enable-remote-tailnet-access.md).
For the trust model behind the control listener, see
[Control plane and trust boundaries](../explanation/control-plane-and-trust-boundaries.md).

## Secure credential input and named contexts

Use `--token-stdin` to keep credentials out of command arguments:

```bash
agentlab connect --endpoint https://server.example --token-stdin < /protected/daemon-token
```

Keep the source file readable only by its owner. When you omit a token in an
interactive terminal, `connect` requests it with echo disabled. A non-interactive
command does not prompt. It returns a structured `credentials_required` error
with `state: blocked` when you use `--json`.

Save a verified connection under a name:

```bash
agentlab connect --context development --endpoint https://dev.example --token-stdin < /protected/dev-token
agentlab context list
agentlab context use development
agentlab dashboard
agentlab --context development status
```

The first named connection migrates existing settings to a `default` context.
The original `client.json` remains available for recovery. Run `context list`
to migrate an existing connection before you add another context.

Contexts store credential references in `contexts.json`. The protected-file
fallback stores credential data in the `credentials` directory with mode `0600`.
The directory and lock file live beside `client.json`. Back up that directory
as sensitive data. New connections use an unlocked Linux Secret Service or the default macOS
Keychain when available. See [credential storage](#choose-credential-storage) for selection
and recovery. The original legacy file can still contain an older credential.

Use `--context NAME` before the command for concurrent agents. This selects one
context without changing the active context. `AGENTLAB_CONTEXT` selects a context
when you omit the option. Unknown contexts fail without selecting another server.
Connection options override environment settings, which override saved settings.
A running client keeps its resolved connection when another client switches
contexts. The compatibility dashboard also uses the active context.

`agentlab disconnect` forgets the selected context and retires its local credential.
It does not revoke a daemon token. If you remove the active context, select
another context explicitly before you run more work. The CLI does not restore
legacy settings or select another server automatically.

## Choose credential storage

`auto` is the default for new credentials. On Linux, it uses an existing,
unlocked Secret Service on the local session bus. Without a session bus or
running service, it uses the protected-file fallback. A locked collection or
service failure returns a blocked error. It does not silently switch stores.
A configured but unreachable session bus also blocks without a fallback.
AgentLab does not start a credential service, create a collection, or open an
unlock prompt. Unlock the collection through your operating system before retrying.
On macOS, automatic selection uses the current default file-based Keychain.
The client uses the native Security framework through a bounded child process.
It disables Keychain interaction before access and does not create or unlock a
Keychain. Credentials pass through private pipes. No password helper or shell is
used. Records are scoped to the absolute configuration directory and a random
reference. They do not synchronize through iCloud.

A missing native store permits file storage for a new automatic connection.
A locked store, denied application access, or failed worker blocks the operation.
Existing native references never fall back. A replaced binary can need Keychain
access approval through macOS. Restore that access through the operating system,
or reconnect with a supplied credential and explicitly selected storage.
Native macOS lifecycle and release-upgrade acceptance still require a real Mac.

Select the fallback explicitly for an unattended client:

```bash
agentlab connect --endpoint https://server.example --credential-store file --token-stdin < /protected/daemon-token
```

Use `--credential-store os` to require operating system storage. Missing storage
returns `credential_store_unavailable`. `AGENTLAB_CREDENTIAL_STORE` selects the
default for new credentials; the explicit flag takes precedence. The connect
response includes `context`, `credential_storage`, and the metadata path.
An absent `credential_storage` field on a legacy context means file storage.
Existing references always use their recorded store. Changing the environment
cannot move a credential or make a failed read select a different store.

Move an existing context after its current credential is available:

```bash
agentlab context credentials development --credential-store os --json
agentlab context list --json
```

The client verifies the new record before saving its reference. It preserves
the selected context, endpoint, server identity, and jump settings. Failed storage
preserves the working reference. Replacement and removal record old references
for cleanup before deleting them. If cleanup fails, the error has
`context_metadata_saved: true`. Restore the recorded store, then retry cleanup:

```bash
agentlab context credentials-cleanup --json
```

Cleanup processes saved write intents and retirement records. It preserves
active references and does not revoke a server token. Context format version 2 identifies native
references; older clients must upgrade before using that context file.
Keep the same absolute configuration directory when using native references.
A copied context file alone does not include credentials from the operating system.

## Recover an interrupted credential write

The client saves a private intent before creating a credential. If the client
stops before publishing the context, the intent identifies the store and record.
Observe pending writes without loading a credential or contacting the server:

```bash
agentlab context credentials-pending --json
agentlab context credentials-cleanup --json
```

The pending response contains transaction IDs, context names, references, stores,
endpoints, and timestamps. It omits tokens and ownership digests. Recovery compares
the saved intent with actual context references and the stored record. It keeps
committed credentials and removes only matching unreferenced records. Retrying a
connection first recovers pending writes for that context. Valid pending intents
for other contexts do not block their normal writes. Cleanup does not create an
empty context file that could
hide a working legacy connection.

A blocked error can include `credential_transaction_pending: true` and a
`credential_transaction` object. Restore its recorded store before retrying.
`credential_transaction_changed` means ownership could not be verified. Preserve
any needed value through your operating system or protected file workflow.
Inspect the unreferenced record before removing it manually. After its removal,
cleanup can clear the intent. Do not paste credential values into a support request.

Keep transaction records in their original absolute configuration directory.
Changed, oversized, public, or unexpected journal entries require inspection.
Recovery is bounded to 128 pending records and does not scan arbitrary keyring
entries. It cannot identify legacy orphan records created without an intent.

## Verify the saved server identity

A verified context stores the identity from compatible capability discovery.
Subsequent CLI requests check that identity and request enforcement support before
sending operation payloads. JSON, streaming, artifact, and repository requests
carry `X-AgentLab-Server-Identity`. The daemon checks it before handler dispatch.
Changing the active context does not redirect an existing client.

A changed server or unsupported enforcement blocks a verified context. Restore
the original server or upgrade it. To enroll a different server at a reused URL,
provide an explicit endpoint and credential under a new context name. An existing
verified destination cannot silently accept another identity. Concurrent changes
to the destination also block publication and preserve the other change.

A legacy daemon without a capabilities endpoint remains usable through an
unverified connection. `connect --json` reports `identity_verified: false` and
`compatibility: legacy_unverified`. It does not copy a host metadata claim into
a verified binding. Upgrade that daemon before relying on context identity checks
or the remote dashboard. Other discovery, authentication, or compatibility errors
do not select this legacy path.
