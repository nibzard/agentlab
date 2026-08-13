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

`sandbox list` returns `{"sandboxes": [ ... ]}`. Each item and each
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

- **The `output-format` default is never applied.** You can write
  `defaults write output-format json`, but the CLI never reads it back. The
  global `--json` flag always defaults to `false`. Pass `--json` on every
  invocation.

  ```bash
  agentlab defaults write output-format json
  ```

  This stores the value, but the next command without `--json` still prints
  text. See
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
