# Guest bootstrap recovery after restart and reboot

Status: implemented and verified on `pve2`, 2026-10-16. See
[acceptance evidence](guest-bootstrap-recovery-acceptance.md) and the
[upgrade procedure](../how-to/upgrade-guest-recovery.md).

Baseline: `3d39d9e` on `main`, 2026-10-16.

## Problem and evidence

The runner caches its bootstrap response and endpoint credential under
`/run/agentlab/secrets`. `agent-runner.service` invokes
`agent-secrets-cleanup` through `ExecStopPost`, which deletes that cache.
A reboot also clears `/run`. The next runner invocation attempts bootstrap
again with the descriptor's original token, which the daemon already consumed.
`SandboxManager.Start` starts the VM without replacing that descriptor.

Before implementation, this failure path was source-confirmed. Earlier live
tests covered fresh enrollment and daemon restart, but did not establish guest
restart/reboot recovery. The new integration fixtures and live acceptance now
cover those paths explicitly.

The outcome is that an existing sandbox can recover authenticated reporting and
metadata access after a runner restart, guest reboot, or daemon restart, while
keeping its Tailscale identity and never repeating an agent job blindly.

## Required invariants

- Initial enrollment still requires a single-use, expiring bootstrap token.
  A consumed token must not become a reusable recovery credential.
- Neither source IP nor VMID alone authorizes recovery. Bind authentication to
  a sandbox incarnation so VMID reuse cannot revive an old identity.
- Do not disable secret cleanup or persist decrypted bootstrap bundles, git
  credentials, LLM keys, artifact tokens, or enrollment keys to make recovery
  work. Keep execution credentials in protected tmpfs.
- Persistent guest authentication material is generated independently for each
  VM, scoped only to recovery, and revocable by the daemon. It is absent from
  templates and smoke clones.
- Recovery does not call `tailscale up` when the existing device is connected,
  rename the device, create a second node, or mint another enrollment key.
  Re-enrollment after logout/revocation is a separate explicit action.
- Completed jobs never run again. An interrupted running job is reported as
  interrupted and requires an explicit retry unless a future execution engine
  provides a verified resume protocol.

## Preferred design

### Persistent recovery identity

Generate an Ed25519 key pair in the guest before the initial bootstrap request.
Store only the recovery identity and non-secret incarnation metadata under
`/var/lib/agentlab/identity`, owned by root, directory mode `0700`, private-key
mode `0600`. A fixed root-owned bootstrap helper performs signing; the agent
process receives only its short-lived runtime credential.

Use existing, explicitly verified guest cryptographic tooling or a packaged
helper; do not download a signing implementation at runtime. Verify key
generation, canonical message encoding and signature verification in isolated
tests before selecting the helper implementation.

Include the public key and an idempotency request ID in initial bootstrap.
Atomically bind the public key to a daemon-generated sandbox incarnation when
claiming the initial token. Reject duplicate active public keys across
incarnations. Detect an identity copied from another descriptor/incarnation;
do not silently adopt it.

Append a database migration after the existing template-build migration.
Store public keys, incarnation IDs, revocation state, challenge hashes/expiry,
and recovery request state. Never store private keys or plaintext credentials
in the database. Do not rewrite a migration already applied on `pve2`.

### Authenticated recovery protocol

Add guest-subnet, rate-limited challenge and recovery endpoints. A challenge
has a cryptographically random nonce, a short expiry, and a single-use claim.
Sign a canonical message containing a protocol/version domain, VMID,
incarnation, challenge ID and nonce, requested operation, and request ID.
Verify all fields, the signature, current sandbox lifecycle, and revocation.
Use the existing subnet and request-size protections as additional checks.
Authenticate the controller and protect response confidentiality with TLS and
a CA or server pin delivered through the trusted bootstrap descriptor. A signed
challenge alone does not protect a returned bearer credential on plain HTTP.
Define the existing HTTP installation's migration explicitly; do not introduce
an automatic downgrade when certificate verification fails.

Return a fresh endpoint credential and the minimum bootstrap information
needed for the current lifecycle. Atomically rotate credentials and consume
the challenge. Never rotate a winning request's credential because a concurrent
request lost its claim. Define and test idempotent retry after a lost response;
any temporarily retained response containing credentials must be encrypted,
short-lived, and accessible only through a new proof by the same identity.

An initial bootstrap response can also be lost after token consumption. Since
the guest generates its identity before fetching, a committed registration must
allow proof-based recovery in that case. If registration never committed, the
original unconsumed token remains the initial path. Do not infer registration
success from a client-side network error.

### Guest lifecycle and job execution

Move credential acquisition into a fixed root-owned helper that runs before
the runner, after cloud-init. Serialize helper invocations locally and enforce
the corresponding server-side claim. The helper chooses initial registration
or signed recovery and atomically installs runtime data with correct ownership.

Keep `ExecStopPost` cleanup. On restart, the helper reconstructs authorized
runtime state instead of relying on a cache that cleanup removed. The runner
never needs access to the persistent private key.

Return an explicit job disposition: no job, terminal, interrupted, or authorized
new execution. Couple any new execution to a durable attempt claim. Recovering
authentication is not permission to rerun a job in `RUNNING` or a terminal
state. Do not reissue artifact or job execution credentials to standalone or
terminal sandboxes unnecessarily.

Persist/report initialization and recovery outcomes separately from VM
readiness and Tailscale connectivity. Use fixed diagnostic codes for expired
challenges, revoked identity, unavailable controller, and interrupted jobs.
Never record signatures, private keys, credentials, or complete responses in
events or logs. Redact runtime credentials before use.

### Revocation, templates, and compatibility

Revoke the recovery identity, pending challenges, and cached recovery responses
on destruction and VMID reuse, through every normal/forced/lease cleanup path.
Recheck lifecycle in the same transaction that accepts recovery so a request
cannot race destruction and restore access afterward.

Template creation must remove identity material and verification must assert
it is absent. Smoke clones must not register a persistent identity. Two real
clones must generate distinct public keys and retain distinct Tailscale nodes.

An older guest with a consumed token has no proof suitable for recovery.
Provide an explicit, authenticated operator recovery action or require
reprovisioning from an updated template. If an in-place action is implemented,
scope it to the target VM and expected incarnation, deliver fresh registration
material through a trusted backend channel, and audit it. Never silently relax
authentication for legacy guests. Preserve native-SSH-only backend behavior
and return an actionable unsupported-bootstrap diagnostic where necessary.

## Implementation sequence

1. **Reproduce and specify.** Add failing runner-restart/reboot fixtures and
   define recovery request/response shapes, incarnation semantics, job
   dispositions, credential expiry, and retry behavior.
2. **Daemon identity and recovery.** Add the migration, transactional claims,
   cryptographic verification, idempotent retry handling, revocation, fixed
   diagnostics, and authorization tests.
3. **Guest helper and execution gates.** Package the helper, update service
   ordering and permissions, retain cleanup, and prevent duplicate job
   execution. Add template sanitization and smoke-verification checks.
4. **Upgrade and diagnostics.** Implement the chosen legacy recovery path,
   extend discovery and CLI diagnostics, and document rollout/rollback.
5. **Live acceptance and cleanup.** Build a new managed template and complete
   the scenarios below on dedicated `pve2` resources. Existing production
   profiles and VMs remain unchanged.

## Acceptance tests

| Scenario | Required result |
| --- | --- |
| Fresh standalone sandbox | Single initial bootstrap, distinct identity, connected Tailscale, authenticated reports. |
| `systemctl restart agent-runner` | Runtime secrets are cleaned then reacquired; reporting/metadata recover; no extra enrollment. |
| Guest reboot and stop/start | Identity persists, tmpfs credentials are recreated, same Tailscale device returns. |
| Daemon restart before/during recovery | Durable registration/challenge state reconciles; recovery does not duplicate credentials or jobs. |
| Initial or recovery response lost | Authenticated retry succeeds without resetting the initial token or rotating a winner's credentials. |
| Concurrent recovery | Claims serialize; no request invalidates an acknowledged credential through a losing claim. |
| Expired/replayed/wrong-scope proof | Rejected without credential issuance, state mutation, or secret disclosure. |
| Destroy or reuse VMID during recovery | Old identity and in-flight proofs cannot authenticate the new sandbox. |
| Job runner restart | Completed job stays completed; interrupted job is visible; no duplicate agent side effects. |
| Two clones and template inspection | Different public keys/device identities; no guest identity, runtime secret, or enrollment state in the base. |
| Invalid enrollment key or controller outage | Bounded retries and useful independent diagnostics; guest readiness remains truthful. |

Use durable counters/attempt IDs to detect duplicate work, not log-text presence
alone. Run Go unit/integration/race tests, executable guest-shell fixtures,
service ordering/permission checks, and documentation checks. Live acceptance
must verify authenticated metadata/report requests, identity stability, and
cleanup of test resources and tailnet devices. Record the resulting build ID,
template VMID, and evidence before declaring this fix complete.

## Implemented decisions

Migration 23 adds incarnation-bound public identities, nonce generations,
encrypted deliveries and durable execution attempts. An opt-in TLS 1.3 listener
keeps legacy HTTP installations compatible while verified v2 descriptors pin
the controller certificate. Runtime endpoint credentials last one hour; a root
timer refreshes them every 20 minutes. Challenges expire after one minute and
encrypted idempotent deliveries after five minutes. Same-generation concurrent
proofs cannot both rotate credentials.

The root OpenSSL helper generates and uses the Ed25519 key, performs initial
Tailscale enrollment, and hands only tmpfs runtime data to the agent. The agent
has no unrestricted sudo. Runtime cleanup remains enabled; key material is
removed from templates and absent from smoke clones. `recover` interrupts an
unfinished job; `refresh` preserves a running job. Execution requires a separate
one-time durable attempt claim and fails closed on an ambiguous acknowledgement.

Legacy consumed-token guests require reprovisioning from an updated template.
There is no token reset, HTTP downgrade, or unauthenticated in-place recovery.
Host capability discovery and separate recovery diagnostics expose this limit.
Acceptance also fixed an Ubuntu boot ordering cycle and stale-IP metadata
resolution; details and validation are recorded in the acceptance evidence.
