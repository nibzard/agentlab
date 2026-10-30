# Discovery and setup

## Connect to an existing server

Use an existing named context when it identifies the intended server.
For a new connection, obtain the endpoint and a protected credential input.
The CLI verifies the connection before saving it. A failed connection preserves
the previous settings.

```bash
agentlab connect --context "$context" --endpoint "$endpoint" --token-stdin < "$credential_file"
agentlab --context "$context" capabilities --json
agentlab --context "$context" status --json
```

For a human at a terminal, omit `--token-stdin` to use the hidden credential prompt.
Do not put the credential value in `--token` or a chat message.
If unattended input is unavailable, report the structured blocked requirement.

Check the returned server identity and authorization. Preserve remote permission
denials. Do not retry a scoped token through the trusted Unix socket.
The dashboard uses the same context:

```bash
agentlab --context "$context" dashboard
```

Its browser enrollment code is short-lived and single-use. The daemon credential
stays in the local service. Stopping the dashboard does not stop server jobs.

## Set up a new server

Setup needs an existing SSH connection to a supported Proxmox host.
Discover the host architecture, backend, storage, network, capacity, and available
profiles through the setup inspection. Ask for missing host details before applying.
Use an explicit repository path, profile, context, and stable request key.

```bash
agentlab setup --host "$host" --repo "$repo" --profile "$profile" --context "$context" --request-id "$setup_key" --plan --json
agentlab operation show "$operation_id" --json
```

Review the persisted plan within the user's authorized setup scope. Apply with
the same inputs and request key after resolving its reported requirements:

```bash
agentlab setup --host "$host" --repo "$repo" --profile "$profile" --context "$context" --request-id "$setup_key" --json
```

Setup selects artifacts for the server architecture and verifies a real readiness
sandbox. Report success only when readiness and test resource cleanup are verified.
A saved connection or successful package installation alone does not prove readiness.

## Resume or cancel setup

An admitted setup runs on the server through a systemd service. Keep its operation
ID and private client reference. A wait timeout or client exit does not cancel
that worker. Use `--no-wait` when you need an immediate submission result.
Planned operations still need the original setup command to submit them.

```bash
agentlab operation show "$operation_id" --json
agentlab operation resume "$operation_id" --json
```

Inspect `worker.state`, `worker.observed_at`, the server progress cursor, and
retained resources. A saved running state does not prove liveness. Resume waits
for an active worker or starts a stopped worker with its original pinned request.
An interrupted initial upload needs the original local binaries and assets.
Do not replace those inputs or choose a new key to bypass an uncertain result.
After a host reboot, resume explicitly. Automatic reboot restart is unavailable.

Server success and client enrollment are separate observations. Check
`client_connection_state=verified` and its verification timestamp after resume.
A failed endpoint or identity check preserves the working context. Readiness and
temporary resource cleanup must also pass before you report setup completion.

Use `operation cancel ID --json` when cancellation is requested. Cancellation
records local intent before contacting the server. Inspect `worker_stopped`,
`work_stopped`, cleanup, and retained resources. A stopped worker does not prove
that external tasks stopped. Cancellation does not undo installed software or
network changes. Expired requests permit observation and cancellation; inspect
resources before submitting a new key. Preserve working connections and existing
user work when a prerequisite blocks progress.

## Enroll and renew task credentials

Keep operator signing keys separate from delegated task credentials. An already
authorized operator can use `token enroll --save-context NAME --key PATH --cmds`
to verify and save a narrow task credential. Include its required discovery
permissions. Enrollment does not register another trusted signing key.

CLI renewal requires `schema.read` and `token.renew` in the original credential. It preserves the
issuer, subject, command permissions, and sandbox scope. Use a stable request ID
for one renewal. The first attempt needs the operator signing key. Resume a
pending attempt with the same context, request ID, and lifetime; its protected
staging credential can recover a lost reply without rereading the private key.

```bash
agentlab token renew --context "$context" --request-id "$renewal_key" --key "$signing_key" --ttl 1h --json
```

Inspect the renewal state and cleanup. A lost commit reply can leave the original
context pointing at its revoked parent. Preserve the staged context and resume
the same request. A changed context must not be overwritten. Missing signing
authority or an expired staging credential requires explicit operator action.
Do not mint another credential or broaden permissions to bypass a revocation.
A completed receipt is historical. It does not prove current authentication.
A dashboard bound to the same verified context adopts a compatible published
replacement. Explicit credential overrides require restart. A changed server,
issuer, subject, permission set, or scope requires a new dashboard session.

## Hand off to the dashboard

Start `agentlab --context "$context" dashboard` for the same verified server.
The connection panel shows the target, identity, and permissions. The guided
sandbox form discovers profiles and checks a plan before creation. Do not retry
an uncertain creation until you inspect the sandbox list. Capacity preflight
checks configured limits; it does not reserve resources.

Use job details to inspect progress, command results, execution artifacts, cleanup,
and retained leases. Job completion alone does not prove that tests passed.
Cancellation requires its existing permission and a confirmation for the job and
server. Preserve reported resources when cleanup needs inspection.

## Check request identity support

Use `identity_verified` and `compatibility` from connect output. A verified context
requires `request_server_identity` on the daemon. CLI requests preserve that server
binding through preflight, uploads, artifact downloads, and streaming requests.
A changed server or missing enforcement requires restoration or an upgrade.
Do not strip the binding or select a trusted socket to bypass that result.

`legacy_unverified` keeps an older daemon usable without claiming verified
identity. Upgrade it before using a remote dashboard or identity-bound workflow.
Modern connection verification needs `schema.read` and `status.read`; it does not
need `host.read`. Use a new context with an explicitly supplied endpoint and
credential for an intended replacement server. A concurrent destination change
must remain intact.
