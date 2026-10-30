# Agent JSON output reference

The global `--json` flag is the primary machine-readable surface for the
`agentlab` CLI. Pass it on every call to get structured output that you can
parse. This page describes the contract: what the bytes mean, where errors land,
how to version your parser, and which fields each command returns.

For the workflow that consumes this output end to end, see
[Drive AgentLab as a coding agent](../how-to/drive-agentlab-as-a-coding-agent.md).
For the daemon route table behind these commands, see
[HTTP API reference](http-api.md).

## What `--json` gives you and why it is the primary surface

Every command accepts the global `--json` flag. In text mode the CLI prints
human-readable tables and hints. In JSON mode it prints a single JSON document
(or a stream of documents) that you can parse with confidence. The flag is
parsed once in `parseGlobal` and threaded into the shared `commonFlags.jsonOutput`
field that every command reads.

```bash
agentlab status --json
agentlab sandbox show <vmid> --json
agentlab job show <job_id> --json
```

Prefer the CLI `--json` surface over raw HTTP when you can. The CLI adds the
exit-code and error-envelope contract described below, which the raw HTTP routes
do not provide in the same form.

## The passthrough model: `--json` prints raw daemon bytes

For about 40 commands, the JSON branch is one line:

```text
return prettyPrintJSON(os.Stdout, payload)
```

`payload` is the raw response body from the daemon. The CLI does not unmarshal
the body into its own Go structs and re-marshal it. It only indents the bytes
with two spaces and adds a trailing newline. This means the JSON you parse is the
daemon shape, not the CLI shape.

The two exceptions are `status`, which normalizes schema versions before it
prints, and `schema`, which always prints JSON. See
[Schema versioning](#schema-versioning).

## Stability contract: daemon shapes are a superset

The daemon response types are a superset of the CLI response types. Two fields
exist only on the daemon side:

- `health` appears on every sandbox response.
- `timeline` appears on every job response.

Neither field has a counterpart in the CLI structs. Parse leniently and ignore
keys you do not recognize. New daemon fields can appear in a release without the
CLI bumping a version. A parser that fails on unknown keys will break.

The safest objects to parse strictly are `workspace list` items and `session show`.
For those two, the daemon and CLI structs match field for field, with no
daemon-only additions.

## Success and failure: the error envelope arrives on stdout

In `--json` mode, both success and failure print to **stdout**. The error
envelope is never sent to stderr in JSON mode. A common parser bug is to read
only stderr on a non-zero exit and miss the message.

The error envelope has four keys:

| Key | Presence | Meaning |
| --- | --- | --- |
| `error` | Always present | Short error string. |
| `message` | Always present | Human-readable detail. Falls back to `error`. |
| `code` | Optional (`omitempty`) | Machine code from the daemon. |
| `details` | Optional (`omitempty`) | Extra context from the daemon. |

A failed request looks like this on stdout:

```json
{"error":"sandbox not found","message":"sandbox not found","code":"not_found"}
```

In text mode the same error goes to stderr instead. The stream you read depends
on the mode, so set `--json` and read stdout for both outcomes.

### Exit codes

Capture the exit code on every call and branch on it.

| Code | Meaning |
| --- | --- |
| `0` | Success, or help displayed. |
| `1` | Command or request failed. |
| `2` | Invalid arguments or usage. |

A usage error in JSON mode still prints the error envelope to stdout and exits
`2`. Treat `2` as a bug in your command construction, not a daemon failure.

## The validate special case: `ok:false` with exit 1 and no envelope

`job validate --json` and `sandbox validate --json` behave differently from a
normal failure. When validation fails, the command prints the full result JSON
to stdout and exits `1` **without** an error envelope.

The result JSON looks like this:

```json
{
  "ok": false,
  "errors": [
    {"code": "unknown_profile", "field": "profile", "message": "profile 'x' not found"}
  ],
  "warnings": []
}
```

Distinguish a validate failure from a request failure by checking whether the
stdout JSON contains an `error` key:

- No `error` key, `ok` is `false`: validation failed. The `errors` array holds
  the reasons.
- `error` key present: the request itself failed. Read `message`.

This split exists because the validate commands print the result body first,
then return a marker error that suppresses the normal envelope.

## Pretty-printed output versus NDJSON

Most commands print one pretty-printed JSON document: two-space indent, multi-line,
trailing newline. Three streaming commands differ:

| Command | Format |
| --- | --- |
| `logs <vmid> --json` | NDJSON: one compact event object per line. |
| `msg tail --json` | NDJSON: one compact message object per line. |
| `connect --json`, `disconnect --json` | One compact object, HTML escaping disabled. |

For NDJSON, parse line by line. Each line is a self-contained object. Do not try
to parse the whole stream as one document.

Sample `logs` NDJSON line:

```json
{"id":42,"ts":"2026-08-12T10:00:00Z","kind":"state_change","sandbox_vmid":100,"msg":"READY -> RUNNING"}
```

Sample `msg tail` NDJSON line:

```json
{"id":7,"ts":"2026-08-12T10:01:00Z","scope_type":"workspace","scope_id":"ws-1","author":"alice","kind":"note","text":"build green"}
```

## Schema versioning

Two commands give you a version signal. Read them before you assume a field
exists.

`agentlab schema` prints the JSON schema document and ignores `--json`. It
always prints JSON:

```bash
agentlab schema
```

`agentlab status --json` reports `api_schema_version` and `event_schema_version`.
The status command normalizes a zero schema version to `1` before it prints. A
missing version field therefore reads as `1`, not `0`. Do not treat `0` as a real
version.

```bash
agentlab status --json
```

Use `status` to learn which schema versions the daemon advertises, then consult
`schema` for the shape. When a field is absent, fall back to lenient parsing
rather than failing.

## A lenient-parsing recipe

Wrap every call in a small driver that captures the exit code, branches on the
error envelope, and ignores unknown keys. The recipe below works for every
non-streaming command. For `logs` and `msg tail`, parse each stdout line
separately.

```bash
set -o pipefail
out=$(agentlab sandbox show <vmid> --json)
code=$?
if [ "$code" -ne 0 ] && printf '%s' "$out" | jq -e '.error' >/dev/null 2>&1; then
  printf 'request failed: %s\n' "$(printf '%s' "$out" | jq -r '.message')"
  exit "$code"
fi
printf '%s\n' "$out" | jq '{vmid, state, ip}'
```

Notes on this recipe:

- The command substitution captures stdout even when the command exits non-zero.
- The `.error` check separates a request failure from a validate failure.
- The final `jq` projects only the keys you need and ignores extras such as
  `health`.

### Strict-safe objects

You can parse these two objects with a strict schema because the daemon and CLI
agree field for field:

- `workspace list` items
- `session show`

Every other object may carry daemon-only fields. Keep those parsers lenient.

## Field dictionaries

The tables below list the keys an agent should expect. Daemon-only additions are
marked. Optional keys may be omitted when empty.

### `status`

| Key | Type | Notes |
| --- | --- | --- |
| `api_schema_version` | int | Zero is normalized to `1`. |
| `event_schema_version` | int | Zero is normalized to `1`. |
| `sandboxes` | object | Counts grouped by state. |
| `jobs` | object | Counts grouped by status. |
| `network_modes` | object | Counts grouped by network mode. Optional. |
| `artifacts` | object | Artifact storage summary. |
| `metrics` | object | Metrics listener status. |
| `skill_bundle` | object | Installed skill bundle name and version. |
| `recent_failures` | array | Recent failure events. |

### Sandbox (`sandbox new`, `sandbox show`, `sandbox list`)

`sandbox list` returns `{"sandboxes": [ ... ]}`, excluding destroyed sandboxes
by default. Use `sandbox list --all --json` to include destroyed records.
Each item and each
`sandbox new` or `sandbox show` response uses this shape.

| Key | Type | Notes |
| --- | --- | --- |
| `vmid` | int | Sandbox identifier. |
| `name` | string | Human-readable name. |
| `profile` | string | Profile used to provision. |
| `type` | string | `vm` or `lxc`. Optional. |
| `image` | string | Container image for LXC. Optional. |
| `prompt` | string | Initial agent prompt. Optional. |
| `tags` | array of string | Integration-attachment tags. Optional. |
| `state` | string | Current state. See [state machine](state-machine.md). |
| `ip` | string | Sandbox IP. Empty until `READY`. Optional. |
| `workspace_id` | string | Attached workspace. Optional. |
| `network` | object | Network mode and firewall group. Optional. |
| `keepalive` | bool | Whether auto-destroy is suppressed. |
| `lease_expires_at` | string | Lease expiry timestamp. Optional. |
| `last_used_at` | string | Last activity timestamp. Optional. |
| `resources` | object | `cores` and `memory_mb`. Optional. |
| `health` | object | **Daemon-only.** Lifecycle summary. Not in CLI struct. |
| `created_at` | string | Creation timestamp. |
| `updated_at` | string | Last update timestamp. |

### Job (`job run`, `job show`)

| Key | Type | Notes |
| --- | --- | --- |
| `id` | string | Job identifier. |
| `repo_url` | string | Repository URL. |
| `ref` | string | Git ref. |
| `profile` | string | Profile name. |
| `task` | string | Task prompt. Optional. |
| `mode` | string | Run mode. Optional. |
| `ttl_minutes` | int | Time-to-live in minutes. Optional. |
| `keepalive` | bool | Whether auto-destroy is suppressed. |
| `workspace_id` | string | Bound workspace. Optional. |
| `session_id` | string | Bound session. Optional. |
| `status` | string | Current status. Match uppercase strings exactly. |
| `sandbox_vmid` | int | Provisioned sandbox. Optional. |
| `result` | object (raw JSON) | Free-form result. Optional. |
| `events` | array | Recent events. Optional. Controlled by `--events-tail`. |
| `timeline` | object | **Daemon-only.** Job timeline summary. Not in CLI struct. |
| `created_at` | string | Creation timestamp. |
| `updated_at` | string | Last update timestamp. |

The `timeline` object carries `job_id`, `status`, `started_at`, `completed_at`,
`event_count`, `failure_count`, `last_event_id`, `last_event_kind`,
`last_event_at`, `last_failure_at`, `last_failure_kind`, and
`last_failure_message`.

### `job validate` and `sandbox validate`

| Key | Type | Notes |
| --- | --- | --- |
| `ok` | bool | `true` when the plan is valid. |
| `errors` | array | Blocking issues. Empty when `ok` is `true`. |
| `warnings` | array | Non-blocking issues. |
| `plan` | object | The normalized plan. Optional. |

Each item in `errors` and `warnings` has `code`, `field`, and `message`. When
`ok` is `false`, the command exits `1` without an error envelope. See
[The validate special case](#the-validate-special-case-okfalse-with-exit-1-and-no-envelope).

### `workspace list`

`workspace list` returns `{"workspaces": [ ... ]}`. Each item uses this shape.
This object is safe to parse strictly.

| Key | Type | Notes |
| --- | --- | --- |
| `id` | string | Workspace identifier. |
| `name` | string | Human-readable name. |
| `storage` | string | Storage backend. |
| `volid` | string | Volume identifier. |
| `size_gb` | int | Size in gibibytes. |
| `attached_vmid` | int | Attached sandbox. Optional. |
| `created_at` | string | Creation timestamp. |
| `updated_at` | string | Last update timestamp. |

### `session show`

This object is safe to parse strictly.

| Key | Type | Notes |
| --- | --- | --- |
| `id` | string | Session identifier. |
| `name` | string | Human-readable name. |
| `workspace_id` | string | Bound workspace. |
| `current_vmid` | int | Current sandbox. Optional. |
| `profile` | string | Profile used to rebuild. |
| `branch` | string | Git branch. Optional. |
| `created_at` | string | Creation timestamp. |
| `updated_at` | string | Last update timestamp. |

### `logs` NDJSON

Each stdout line is one event object.

| Key | Type | Notes |
| --- | --- | --- |
| `id` | int | Monotonic event id. |
| `ts` | string | Event timestamp. |
| `kind` | string | Event kind. |
| `sandbox_vmid` | int | Source sandbox. Optional. |
| `job_id` | string | Source job. Optional. |
| `msg` | string | Short message. Optional. |
| `json` | object (raw JSON) | Event payload. Optional. |

### `msg tail` NDJSON

Each stdout line is one message object.

| Key | Type | Notes |
| --- | --- | --- |
| `id` | int | Monotonic message id. Use as the follow cursor. |
| `ts` | string | Message timestamp. |
| `scope_type` | string | `job`, `workspace`, or `session`. |
| `scope_id` | string | Target identifier. Free-form, not validated. |
| `author` | string | Message author. Optional. |
| `kind` | string | Message kind. No enforced taxonomy. Optional. |
| `text` | string | Message text. Optional. |
| `json` | object (raw JSON) | Message payload. Optional. |

### Error envelope

| Key | Presence | Notes |
| --- | --- | --- |
| `error` | Always | Short error string. |
| `message` | Always | Human-readable detail. |
| `code` | Optional | Machine code from the daemon. |
| `details` | Optional | Extra context. |

## Pitfalls

- **Saved output preferences affect commands.** Set the default with
  `agentlab defaults write output-format json`. Pass `--json` explicitly in
  automation, or `--json=false` to request text. Explicit flags override the
  saved preference. See
  [Global flags, environment, and exit codes](global-flags-env-and-exit-codes.md).

- **`--and-ssh` is rejected with `--json`.** Create the sandbox with `--json`
  first, then run `agentlab ssh` separately with the returned `vmid`.

  ```bash
  agentlab ssh <vmid> --exec -- echo ready
  ```

- **Errors are not on stderr in JSON mode.** Both success and failure print to
  stdout. If you read only stderr on a non-zero exit, you miss the message.

- **Validate failures carry no `error` key.** For `job validate` and
  `sandbox validate`, a non-zero exit with `ok:false` is the expected result
  body, not a request failure.

## Where to go next

- For the full agent workflow: [Drive AgentLab as a coding agent](../how-to/drive-agentlab-as-a-coding-agent.md).
- For multi-agent coordination: [Coordinate multiple agents](../tutorials/coordinate-multiple-agents.md).
- For token delegation: [Mint and use scoped tokens for an agent](../how-to/mint-scoped-tokens-for-an-agent.md).
- For isolation and credentials: [How AgentLab isolates and credentials an agent](../explanation/how-agentlab-isolates-and-credentials-an-agent.md).
- For the route table behind these commands: [HTTP API reference](http-api.md).
- For state strings: [State machine](state-machine.md).
- For the event contract: [Event contract](event-contract.md).

## Missing connection credentials

A non-interactive `connect --json` command never requests hidden input. When no
credential is available, it preserves the existing connection and returns the
existing error envelope with recovery fields:

```json
{"error":"provide a daemon credential through --token-stdin or AGENTLAB_TOKEN, then retry connect","message":"provide a daemon credential through --token-stdin or AGENTLAB_TOKEN, then retry connect","state":"blocked","code":"credentials_required","requirement":"daemon_credential","action":"provide a daemon credential through --token-stdin or AGENTLAB_TOKEN, then retry connect"}
```

Provide the credential through secure input, then retry the same command.
`context list --json` returns the active context and public context metadata.
It never returns credential contents.

## Capability discovery

Run `agentlab capabilities --json` before using an optional server feature.
The authenticated `/v1/capabilities` route requires `schema.read`. It returns
protocol and schema versions, the server identity, supported features, and the
caller's command permissions and sandbox scope. It never returns a token.
A tunnel or dashboard proxy does not expand those permissions.

Treat a missing or false feature as unsupported. Feature support does not imply
available capacity or permission to perform the action. The command rejects an
unknown protocol or API schema version. An older daemon without discovery returns
`capabilities_unavailable`; inspect `agentlab schema` or upgrade the daemon.
Authentication and authorization errors remain errors. The client does not retry
through another transport.

## Keyed job submissions

`job run --request-id KEY` requires capability discovery and sends the key in the
`Idempotency-Key` header. Keys contain 1 to 128 letters, digits, dots, colons,
underscores, or hyphens. The first create returns the existing raw job payload
with HTTP status 201. A replay returns the current payload for the original job
with status 200 and `Idempotency-Replayed: true`. The job ID stays the same.
No success envelope or stream format changes.

The key is scoped to the authenticated caller and server identity. Stored request
metadata contains hashes, a job ID, an expiry, and a workspace binding when used.
The job table remains the only execution record. Authorization runs before every
request and checks the original job's current sandbox on replay.

New submission errors preserve `error`, `code`, `message`, and optional `details`.
They add `action` and a Boolean `retryable` field. For example:

```json
{"error":"request key already belongs to different inputs","code":"idempotency_conflict","message":"request key already belongs to different inputs","action":"reuse the original inputs or choose a new request key","retryable":false}
```

A conflict uses HTTP 409. An expired key or removed original job uses HTTP 410.
Keys expire after 30 days, and tombstones remain stored. Reusing those keys does
not create another job. Check `idempotent_workspace_create` before including a
workspace creation request. Keyed branch session creation remains unsupported.

A workspace allocation records a stable workspace ID before its backend call.
A complete volume receipt allows database publication to resume. An unknown
allocation outcome returns HTTP 409, `state:blocked`, and
`code:workspace_allocation_inspection_required`. The additive
`workspace_allocation` object contains `job_id`, `workspace_id`, `name`, `storage`,
`size_gb`, `state`, `created_at`, and a known `volume_id` when available.
Its state is `planned`, `allocating`, `allocated`, or `committed`.
New records also include `owner_vmid` and `allocation_name` after reservation.
The CLI preserves this object in JSON errors.

A read-only preflight failure returns `workspace_allocation_preflight_failed`.
Its state remains `planned`. Repair storage configuration or access, then reuse
the original request key. An allocation response error remains `allocating`;
it cannot use this reset path. Changed owner reservations also block publication.

`retryable:true` means a complete receipt permits another publication attempt.
`retryable:false` requires resource inspection. Never allocate another volume
merely because a backend response or database acknowledgement was lost.
Unknown outcomes and missing committed workspaces are not recreated or deleted.
Automatic reconciliation of unknown backend outcomes remains incomplete.

## Job cancellation

`POST /v1/jobs/{id}/cancel` persists a cancellation request and normally returns
HTTP 202. It requires `job.cancel` and the job's sandbox scope. Observation uses
`GET` on the same route with `job.read`. `job show` also includes an optional
`cancellation` object. Existing success payloads and job status names remain valid.

```json
{"job_id":"job-example","state":"requested","work_stopped":false,"cleanup_status":"pending","requested_at":"2026-10-29T00:00:00Z","updated_at":"2026-10-29T00:00:00Z","resources":[]}
```

Cancellation states are `requested`, `running`, `blocked`, `cancelled`, and
`not_needed`. A blocked result includes `error_code` and `action`. `resources`
identifies destroyed, retained, or inspection-required resources. The server sets
`work_stopped: true` after backend verification. A completed job can also report
`not_needed` because its task has already finished.

The CLI's `--wait` option observes that request. An observation timeout returns the
last record with `wait_timed_out: true`; it does not change the server state.
A blocked result uses exit code 1 and prints one structured result in JSON mode.
The job's final legacy status is `FAILED` after cancellation. Its result includes
`cancelled: true` and a `cancellation` object, which distinguishes cancellation
from a task failure. Guest reports cannot replace that result.

## Local repository snapshots

`repo snapshot --json` returns source and sanitized revisions, the bundle hash,
file and change counts, and protected artifact paths. It does not upload content.
The manifest lists files, hashes, modes, changes, and additional exclusions.

`repo upload --manifest PATH --json` returns `id`, `repo_url`, `manifest`,
`created_at`, and `expires_at`. Repeating the upload preserves its ID and expiry.
Use that `repo_url` and the manifest `revision` as the job `ref`. Job creation
checks the source owner, expiry, content, and supported execution path. Pass
`--request-id` for safe job submission retries. The raw job response stays unchanged.

`repo show ID --json` observes the source metadata. `repo remove ID --json`
returns the ID, `removed:true`, and `cleanup:"verified"`. Removal refuses queued
or running jobs. Pending cleanup and verified results with blocked cleanup also
protect the original source. Expiry blocks new jobs after 30 days; it does not remove bytes.

Check `repository_upload` and `repository_snapshot_jobs` before this workflow.
Updated VM templates are required. `repository_results` enables verified patch
collection. Full result accounting remains pending, so `repository_transfer` stays false. See
[Prepare a local repository snapshot](../how-to/prepare-local-repository-snapshot.md)
for the content policy, credentials, and resource cleanup.

## Verified repository results

`job result JOB_ID --json` returns the job status, private output directory,
patch path, execution archive path, receipt path, and remaining resources.
It requires `repository_results`, `job.read`, and `job.artifacts`.

The nested `result.result.repository_result` in `job show` records server evidence.
Its `execution` field records `state: "guest_reported"`, `agent_exit_code`,
`duration_seconds`, and `tests: "not_reported"`. Archive and terminal report
validation do not prove tests passed. Invalid or credential-bearing execution
archives block collection and retain the sandbox.
Its `state` is `verified` or `blocked`. A verified source result identifies the
manifest hash, uploaded source ID, original source revision, initial task revision,
returned revision, artifact paths, patch hash,
patch size, exclusion count, and verification time. An empty patch has zero bytes
and no uploaded patch path. The client creates the empty local file itself.

`cleanup_status` is `pending`, `verified`, `retained`, or `blocked`. It describes
the sandbox outcome. The resource list also identifies retained source uploads.
Upload expiry blocks new submissions; it does not remove the stored source bytes.
A blocked result includes an action and retained resources. The CLI error envelope
adds `job_id`, `repository_result`, and `resources` for this blocked state.

The guest cannot declare a result verified. The daemon verifies immutable artifact
receipts, bundle contents, original base file hashes, and the derived patch hash. The client checks the
files again during collection. Execution validation checks bounded archive contents
and binds the report to its job, source, and terminal status. Tests remain unreported
until structured test evidence is supported.

The `target` field pins the server identity, sandbox ID, name, creation time,
VM generation ID, and guest incarnation. Missing or changed identities block
sandbox deletion. Pending cleanup resumes after daemon restart and repeats
artifact verification. An absent VM needs only local record cleanup.

`job cleanup JOB_ID --json` requires `repository_result_cleanup`, `job.cleanup`,
and access to the job's sandbox. It returns `job_id`, `accepted`, `server_identity`,
`repository_result`, `resources`, and the next observation command. A saved retry
returns `accepted:true`; completed or retained cleanup returns `accepted:false`.
The saved target remains unchanged. Task execution is not restarted.
Collection recovery before a verified result is saved remains incomplete.

## Packaged agent skill

`skill show --json` returns `manifest`, the bundle `sha256`, and its `files`.
`skill export [--out DIRECTORY] --json` also returns `directory` and `entrypoint`.
The entrypoint is the exported `SKILL.md`; its references use the same directory.
The bundle version is distinct from the CLI version.

These commands are local. They do not require connection credentials or server
capabilities. Export defaults to a private cache directory identified by its digest.
It writes all guides before publishing the directory. Concurrent identical exports
return the same complete result. Existing content must match the packaged bundle.
A modified destination produces a blocked `skill_export_changed` result and remains
unchanged. No exported instructions run during this command.

## SSH token credential status

`token create --json` adds `revocation_id` to the existing credential and signing
metadata. The response still contains the secret `token`; keep it in protected
storage. `token inspect --token-stdin --json` reads at most 16 KiB from standard
input. It returns claim metadata, `revocation_id`, and `verified:false`.
Inspection does not verify a signature or authorize an action. The legacy
positional inspection argument remains accepted for script compatibility.

```bash
agentlab token inspect --token-stdin --json < "$token_file"
agentlab --context "$management_context" token revoke --issuer "$issuer" "$revocation_id" --json
agentlab --context "$management_context" token status --issuer "$issuer" "$revocation_id" --json
```

Both remote commands require `token_revocation` and a discovered server identity.
Credential management requires unscoped authority: `token.revoke` for mutation
and `token.read` for observation. Capability discovery also requires `schema.read`.
The CLI preserves a known context identity at its saved endpoint and sends the
expected server identity with the mutation. It rejects a mismatched receipt.

The response contains `issuer`, `revocation_id`, `revoked`, `server_identity`,
and, when revoked, `revoked_at` and `actor`. Identical retries retain the first
audit identity and timestamp. `revoked:false` reports only the absence of a record;
it is not a token validity result. No bearer credential is returned by revocation
or status commands.

Revoked requests receive `v1/auth/token_revoked` with HTTP 401 and
`retryable:false`. Expired requests receive `v1/auth/token_expired` with HTTP 401.
Unavailable status storage returns `v1/auth/credential_status_unavailable`,
HTTP 503, and `retryable:true`. Each retains the legacy `error` field and supplies
an `action`. These errors use `state:"blocked"`. Expired or revoked credentials
require `daemon_credential`; unavailable storage requires `credential_status_store`.
Do not retry a revoked credential or switch to a trusted socket.
Revocation does not cancel already admitted work.

## Local credential storage

`connect --json` retains the existing response fields and adds `context` and
`credential_storage`. Its `config_path` identifies `contexts.json` for new
connections. Credentials are read through protected stdin or the terminal and
are not returned. Storage is `file` or `secret-service`. An absent storage field
on older context metadata means `file`.

`context credentials NAME --credential-store auto|os|file --json` returns public
context metadata, including the new reference and storage type. It does not
change the active selection or return a token. `context credentials-cleanup --json`
returns `credential_cleanup_complete:true` after recovering saved write intents and
processing retirement records. `context credentials-pending --json` returns
`credential_transactions`, an array of public pending-write metadata. Each item
contains `id`, `context`, `credential_ref`, `credential_storage`, `endpoint`,
`created_at`, and an optional `server_identity`. It contains no token or digest.

Structured storage errors retain `error` and `message` and include `state:blocked`,
`requirement:credential_store`, a stable code, an action, `retryable`, and
`context_metadata_saved`. When metadata is saved, inspect `context list` before
repeating a connection or removal command. This flag describes a metadata commit;
it does not mean that a removed context still exists.

Common codes are `credential_store_unavailable`, `credential_store_locked`,
`credential_not_found`, `credential_store_verification_failed`, and
`credential_cleanup_required`. A contended context lock returns
`context_store_busy` with `retryable:true`. Other structured storage errors require
the stated corrective action. Neither an unavailable native reference nor a locked
collection selects a fallback credential or another transport.

An unresolved write adds `credential_transaction_pending:true` and a
`credential_transaction` object to the structured storage error. Its reference
identifies the retained record; it does not grant access. A changed unreferenced
record returns `credential_transaction_changed`. Invalid or changed ownership
metadata returns `credential_journal_invalid` or `credential_journal_changed`.
Restore the recorded store and inspect pending writes before retrying cleanup.
Pending discovery and cleanup bypass connection settings, credential environment
values, and saved output preferences. Pass `--json` explicitly in automation.

## Workspace copy recovery

Workspace fork failures preserve the existing error envelope. They add
`state:blocked`, `code:workspace_fork_inspection_required`, a corrective action,
`retryable:false`, and a `workspace_fork` object. This object includes the
operation ID, acting identity hash, server identity, source and target IDs,
copy snapshot, last saved state, and any storage receipt. `target_state:retained`
means the copy was verified but workspace publication still needs inspection.
An unverified target is never treated as disposable from its name.

Use `workspace fork-status SOURCE [OPERATION_ID] --json` to read the durable
record after reconnecting. Omitting the ID lists the source's saved attempts.
This requires `workspace.read` and the source's sandbox scope.
Storage snapshot failures can add `storage_operation` with the known resources.
No successful response shape is replaced by this recovery metadata.

A saved running state does not prove worker liveness after daemon loss.
Client cancellation stops the synchronous helper and its children. It does not
prove rollback. Unknown copy outcomes, resumption, and cleanup still require
further reconciliation work. Local fixture tests do not prove live Proxmox
storage compatibility.

## Explicit test commands

The `job_test_commands` capability supports an ordered `test_commands` array.
`job run` and `job validate` accept repeatable `--test-command` flags. The API
accepts at most 16 commands, 4096 bytes per command, and 16384 bytes in total.
Commands must be valid UTF-8, nonempty, and free of NUL characters. The submission
key binds the ordered commands. Changing a command requires a new logical task.

The runner executes commands after a successful agent exit. It uses `bash -c`
in the task repository and applies the configured inner sandbox. Commands share
a 300-second budget, bounded by the remaining job lease. The runner terminates
an expired command, then skips remaining commands. A missing timeout utility
skips commands and fails the job. Agent failure also skips tests. Test failure
sets the job to `FAILED`; `agent_exit_code` remains the agent's exit code.

Uploaded repository result receipts include `execution.test_runs`. Each run has
`command`, `state`, and either execution fields or a skip reason. Executed runs
contain `exit_code`, `duration_seconds`, `log_path`, and `log_sha256`. States are
`passed`, `failed`, `timed_out`, and `skipped`. Skip reasons are `agent_failed`,
`budget_exhausted`, and `runner_unavailable`. `execution.tests` summarizes these
command results as `passed`, `failed`, `skipped`, or `not_reported`.

The server and client bind archive evidence to the stored commands and log
digests. Missing evidence blocks repository result verification and cleanup.
URL-based jobs validate command reports before accepting terminal status, but
do not receive a verified repository receipt. These are guest-reported command
outcomes. They do not prove test coverage or independent task assertions.
Old templates must be rebuilt to emit requested test evidence.

## Server setup operations

Submitted setup operations use a server journal and a stable systemd service.
`operation show` reads the server through SSH and preserves its event cursor.
Local observations do not advance server progress timestamps. Worker observations
include `kind`, `unit`, `state`, `active`, `pid`, `observed_at`, and any available
exit status. An absent service is distinct from a failed service state query.
A historical `running` record with an inactive worker returns
`setup_worker_stopped` and requires inspection before resume.

`setup --no-wait` returns after submission. `operation resume` waits for server
completion and verifies the destination connection. `client_connection_state`
is `pending` or `verified`. `client_connection_verified_at` records successful
client verification. Server success does not establish client enrollment.
An identity mismatch leaves the working connection intact.

Observation errors can include `setup_observation_timeout` or
`setup_observation_unavailable`. They do not mark the server operation failed.
`setup_connection_pending` means server readiness completed but the client
connection still needs verification. `setup_payload_required` and
`setup_payload_changed` require the original pinned artifacts. A rejected worker
protocol returns `setup_worker_unsupported` before installation.

Cancellation records local intent before contacting the server. Its response
separates `worker_stopped` from `work_stopped`. The latter stays false when
external tasks or cleanup remain unverified. An unavailable cancellation response
returns `setup_cancellation_pending`. The error includes the operation ID and
inspection action. Retained root files and configuration backups remain visible
in the operation resources. Setup request keys expire after 30 days. Expiry
prevents new execution; observation and cancellation remain available.

The server journal is authoritative. The client reference contains host settings,
identities, artifact digests, and the request key. Credentials do not enter either
operation record. Initial setup still requires trusted SSH access. These records
are separate from daemon jobs because setup can begin before a daemon exists.

## Credential enrollment and renewal

`token enroll` verifies an operator-signed credential over the selected remote
endpoint and saves it with `--save-context`. It emits public identifiers and
verification metadata. It does not emit the credential. The signing public key
must already be authorized by the server.

`token renew --context NAME --request-id KEY` binds to a named remote context.
Endpoint, token, and socket overrides are rejected. New attempts require a matching
operator signing key. Pending attempts reuse protected staging credentials. Both
commands accept lifetimes from one minute to 24 hours.

The renewal result records the original context, staged context, issuer and
revocation identifiers, candidate digest, timestamps, state, cleanup, and server
receipt. States progress through `staged`, `committed`, and `published`. Cleanup
becomes `verified` after the staging context is removed. Candidate and parent
credentials never enter the journal. A public signing key supports recovery.

A lost commit response returns `credential_renewal_pending`. Retry the same
context and request ID. A changed destination returns `context_binding_changed`.
Missing signing authority returns `signing_authority_required`. An expired staged
credential returns `renewal_credential_expired` and requires explicit enrollment.
Receipts retain completed request IDs; a replay returns historical publication,
not proof of current authentication. Local receipts and server revocation records
have no automatic expiry. A later rotation does not erase an earlier receipt.

Preparation and commit use the existing remote token checks and `token.renew`.
Sandbox scope remains valid for this self operation and must match the candidate.
The protocol does not grant a token holder signing authority, register a public
key, or revoke an issuer. Server jobs continue when a client credential rotates.

## Connection identity verification

`connect --json` adds `server_identity`, `identity_verified`, and `compatibility`.
`compatibility: verified` means compatible discovery and request enforcement pass.
`compatibility: legacy_unverified` applies only when an unbound legacy daemon has
no capabilities endpoint. Its context has no verified server identity. Existing
connection fields and the protected credential reference remain available.

A verified remote client sends its bound identity with every request and checks
enforcement support before sending operation payloads. Changed identity returns
`server_identity_mismatch` or `v1/auth/server_identity_mismatch`. Missing identity
enforcement returns `unsupported_feature`. A missing capability endpoint returns
`capabilities_unavailable`. These failures do not send the requested operation.
An attempted streaming header override returns `server_identity_override`.
Redirect responses return `endpoint_redirect` without selecting another endpoint.

Publication uses a conditional destination write. A concurrent change returns
`context_binding_changed` and preserves the newer context. Connection verification
does not print its credential or raw status response. A remote dashboard that
cannot verify request identity returns a structured blocked compatibility error
before it opens a listener.

### Workspace fork recovery

`workspace fork-resume SOURCE OPERATION_ID --json` requires the advertised
`workspace_fork_recovery` feature. Success returns the original workspace.
Blocked recovery retains `workspace_fork` metadata and the original target.
The operation includes its pinned `size_gb`. A native receipt can include
`journal_id` and `recovered`. These fields supplement the source, snapshot,
and target GUIDs. They do not authorize deletion of retained resources.

### Execution log inspection

Servers with `execution_inspection` expose `GET /v1/jobs/{id}/execution`.
The index identifies the job, server, manifest, archive, evidence, and log entries.
Text pages contain byte offsets and an explicit end-of-file flag. Pin
`archive_sha256` when paging. A verified archive binds the reported evidence
to the admitted job. It does not establish test coverage. See the
[HTTP contract](http-api.md#inspect-verified-execution-logs) for limits and errors.
