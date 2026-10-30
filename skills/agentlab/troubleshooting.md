# Recover interrupted work

## Classify the failure

Read the JSON error code, corrective action, retry flag, and resource metadata.
Missing authentication, missing authority, invalid configuration, unsupported
features, and active work need different responses.
Request only the requirement that is missing. Keep credential values out of chat.
Do not repeat a blocked request while its prerequisite remains unchanged.

If a connection attempt fails, preserve the working context. If a state-changing
request times out, inspect durable state before creating replacement work.
Reuse the original job request key or setup request key with the same inputs.
Do not treat a timeout as proof that execution stopped.

## Restore credential storage

Read `credential_storage` in `agentlab context list --json`. Existing references
keep their recorded store when environment settings change. A locked Linux
credential collection returns a blocked error without an unlock prompt or a file
fallback. Restore that collection or obtain a replacement credential securely.
Do not change an explicit `os` storage policy to bypass a failed read.

New unattended connections can explicitly select `--credential-store file`.
Auto selection uses a running, unlocked Linux Secret Service when available.
Without a session bus or service, it selects protected files. On macOS, it uses
the default file-based Keychain. It does not create or unlock a Keychain. A locked
store or denied application access blocks without prompting. Restore access
through macOS or reconnect with an explicit replacement credential and storage.
Native Mac lifecycle and upgrade acceptance remain open. New context credentials
stay out of `client.json`. Existing legacy files remain sensitive recovery copies.

When the current credential is available, move it without changing the server:

```bash
agentlab context credentials "$context" --credential-store os --json
```

If an error has `context_metadata_saved: true`, inspect the saved context first.
Restore the recorded store, then inspect and recover pending credential writes:

```bash
agentlab context credentials-pending --json
agentlab context credentials-cleanup --json
```

Recovery preserves committed references and checks ownership before removing an
unreferenced record. A changed record stays blocked. Preserve its value through
a secure local workflow and inspect it before manual removal. Pending discovery
and cleanup do not load legacy credentials or contact the server. These commands
also remove retired local records; they do not revoke server access.

## Cancel a job

Check `job_cancellation` before cancellation. A job cancellation request is durable
and requires `job.cancel` plus the sandbox scope. Shared sessions and container
jobs do not currently support this cancellation path.

```bash
agentlab --context "$context" job cancel "$job_id" --wait 30s --json
agentlab --context "$context" job show "$job_id" --json
```

A cancellation wait timeout does not mean cancellation failed or work stopped.
Read the cancellation state, `work_stopped`, cleanup status, and resources.
Keepalive sandboxes can remain stopped. Workspace volumes remain retained.
Report blocked cleanup and its action instead of claiming complete removal.

## Resume verified result cleanup

A terminal job status can still have `repository_result.cleanup_status=pending`.
The daemon resumes saved pending cleanup after restart and rechecks evidence.
For a verified result with a resolved cleanup error, check
`repository_result_cleanup` and save a retry:

```bash
agentlab --context "$context" job cleanup "$job_id" --json
agentlab --context "$context" job show "$job_id" --json
```

The request requires `job.cleanup` and the job's sandbox scope. HTTP 202 means the
request is saved, not that cleanup finished. Repeating a complete or retained
outcome is harmless. The retry keeps the saved server and sandbox identity.
It cannot adopt a replacement VM with the same ID or name.

If result verification failed, preserve the retained sandbox and collect evidence
before its lease expires. `job cleanup` does not bypass missing result evidence.
Collection recovery before a verified terminal receipt is saved remains incomplete.
Old results without a saved cleanup target require inspection.

Do not use global prune, stop-all, raw backend deletion, or a trusted-socket retry
to bypass an unclear task outcome. Preserve user data and report the specific
remaining resources, leases, and corrective actions.

## Retire a task credential

Use this path when credential management is within the user's authorized task.
Check `token_revocation` with the credential management context. It needs
`schema.read`, `token.revoke`, and `token.read`, with no sandbox scope.
Ordinary task credentials do not need global credential management permissions.

```bash
agentlab token inspect --token-stdin --json < "$token_file"
agentlab --context "$management_context" token revoke --issuer "$issuer" "$revocation_id" --json
agentlab --context "$management_context" token status --issuer "$issuer" "$revocation_id" --json
```

Use the issuer and revocation ID from trusted credential metadata. Inspection
returns `verified:false`; its output is not proof of authority or validity.
The server records identifiers and the acting identity without storing the token.
A retry preserves the first record. Revocation applies to that server and persists
across restarts. Reusing the signed ID does not restore access.

New requests fail after revocation. Already admitted work and independent guest
credentials remain separate. Use authorized cancellation when work must stop.
Tokens without a signed ID and static control credentials need their documented
key or credential retirement workflow. Do not bypass a revocation error through
a different transport or retry the same revoked credential.
