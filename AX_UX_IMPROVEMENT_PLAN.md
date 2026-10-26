# AgentLab AX and UX fixing and improvement plan

Reviewed: 2026-10-15. Baseline: `4614749` (`v0.4.0-17-g4614749`).
AX means agent experience. UX covers human use of the CLI, dashboard,
onboarding, and recovery workflows.

Implementation and validation are complete in the
`feat/ax-ux-improvements` worktree. See the [final acceptance audit](#final-acceptance-audit). This file
is the current implementation plan; the older `IMPROVEMENT_PLAN.md` remains
historical background and contains assumptions that no longer describe the code.

## Current status and next action

- [x] Record the baseline and inspect existing plans and tests.
- [x] Reproduce CLI parsing, error output, defaults, and JSON compatibility issues.
- [x] Verify dashboard contracts against daemon routes and response shapes.
- [x] Review execution, provisioning, retries, and discovery contracts.
- [x] Check installation, documentation, and recovery journeys.
- [x] Finalize priorities, dependencies, acceptance criteria, and validation.
- [x] Implement and verify the issues below.

Delivery: review the uncommitted changes in `/Users/air/dev/agentlab-ax-ux` on
`feat/ax-ux-improvements`. All original items below are complete. Validation used
isolated fixtures, real headless Chrome and all three completion shells. Real
host provisioning is a separate controlled acceptance run, as scoped below.

The largest shared problem is drift between advertised behavior and actual
behavior. Preserve the useful foundation: plan validation, structured daemon
errors, inventory/reconciliation, diagnostic bundles, scoped authorization,
workspace recovery, and the existing release archive contract.

## Evidence, priorities, and scope

**Reproduced** means a command or isolated executable fixture demonstrated the
behavior. **Source-confirmed** means both the implementation and relevant caller
were inspected. **Proposed improvement** means a product decision rather than a
proven defect. Observation tests that pass while recording an incorrect result
are evidence, not passing acceptance tests.

P1: fix before relying on the affected core workflow. P2: correctness or
recurring workflow friction. P3: discoverability and polish. Size estimates are
relative: S = localized change, M = several interfaces/tests, L = API/storage
or lifecycle work; these are not calendar estimates.

Review covered CLI, API, dashboard source, installation/release configuration,
agent documentation, and lifecycle/recovery contracts. Temporary Go overlay
tests, a local HTTP fixture, a local Unix-socket fixture, and JavaScript execution
with DOM/fetch stubs were used. No live VM creation, destruction, host setup, or
production preference changes were performed during this pass. No browser was
available; visual layout, keyboard interaction, and real-browser authentication
remain acceptance work. This is not a complete security or backend audit.

## Prioritized register

| ID | Priority / size | Item | Evidence | Status |
| --- | --- | --- | --- | --- |
| AX-07 | P1 / S | Preserve all fields in filtered sandbox JSON | Reproduced | Done |
| UX-01 | P1 / M | Release workspace leases on forced destruction; align dashboard Destroy | Reproduced | Done |
| AX-01 | P1 / M | Parse flags consistently and reject unused arguments | Reproduced | Done |
| AX-02 | P1 / M | Make JSON errors and exit codes consistent | Reproduced | Done |
| AX-03 | P1 / L | Recover creation across timeouts and retries | Source-confirmed | Done |
| AX-09 | P1 / M | Replace false-positive exec dry-run validation | Reproduced | Done |
| AX-10 | P2 / S | Report exec timeouts as timeouts | Reproduced | Done |
| AX-08 | P2 / M | Report captured-output truncation explicitly | Reproduced | Done |
| UX-02 | P2 / S | Correct dashboard active counts | Reproduced | Done |
| UX-03 | P2 / S | Align prune wording, targets, and outcomes | Source-confirmed | Done |
| UX-05 | P2 / S | Preserve dashboard proxy query parameters | Reproduced | Done |
| UX-06 | P2 / M | Repair Events view and scope selection | Reproduced | Done |
| UX-07 | P2 / M | Coordinate dashboard authentication and retries | Reproduced | Done |
| AX-04 | P2 / L | Add complete job discovery and dashboard details | Source-confirmed | Done |
| AX-05 | P2 / M | Apply and validate CLI preferences | Reproduced | Done |
| AX-06 | P2 / L | Publish usable schemas and capabilities | Source-confirmed / first-pass CLI check | Done |
| DOC-01 | P2 / M | Make onboarding and agent recipes executable | Source-confirmed | Done |
| DOC-02 | P2 / S | Fix installer success/cleanup behavior | Reproduced | Done |
| QA-01 | P2 / M | Test public contracts and remove timing-dependent cap coverage | Reproduced coverage gap | Done |
| UX-04 | P3 / M | Generate help and completion from command definitions | Source-confirmed | Done |
| IMP-01 | P3 / M | Improve inventory density, filtering, and empty states | Proposed improvement | Done |
| IMP-02 | P3 / M | Add explicit wait/progress and actionable recovery summaries | Proposed improvement | Done |

## Delivery order and dependencies

| Batch | Scope | Dependencies | Exit condition |
| --- | --- | --- | --- |
| 1. Immediate regressions | AX-07, UX-01 | None | Unknown JSON fields preserved; destroy cleanup parity proven |
| 2. CLI contracts | AX-01, AX-02, AX-05 | Shared argument/output policy | Documented command forms work; errors are deterministic |
| 3. Dashboard correctness | UX-02, UX-03, UX-05, UX-06, UX-07 | UX-06 uses UX-05 | Counts, events, authentication, and actions match daemon behavior |
| 4. Exec contracts | AX-09, AX-10, AX-08 | AX-09 shares parser with AX-01 | Dry-run, timeout, and truncation results are truthful |
| 5. Durable workflows | AX-03, AX-04 | Extend schemas/auth tests alongside APIs | Lost responses and process restarts have a supported recovery path |
| 6. Discovery and documentation | AX-06, UX-04, DOC-01 | Parser/API definitions finalized | Schemas, help, examples, and completion agree |
| Independent install fix | DOC-02 | Preserve release naming contract | Fixture installation succeeds and cleans up on all paths |
| Continuous quality | QA-01 | Every batch | Meaningful contract checks accompany fixes |
| Product improvements | IMP-01, IMP-02 | Correct collection APIs and lifecycle behavior | Human and agent journeys require fewer lookups and guesses |

Documentation for a changed behavior ships in its own batch; batch 6 is the
broader consolidation. Avoid a single large refactor. Keep changes independently
reviewable and preserve the module path and release pipeline described in
[AGENTS.md](AGENTS.md).

## Fix specifications and acceptance criteria

### AX-07 — Preserve the daemon JSON contract

Evidence: a running-sandbox fixture with `health` and an unknown `future_field`
retains both under `ls --all --json` but loses both under `ls --json`.
The recent change decodes through the narrower CLI response struct.

Sources: [list handler](cmd/agentlab/commands.go),
[CLI types](cmd/agentlab/api.go), [daemon types](internal/daemon/api_types.go),
[documented pass-through contract](docs/reference/agent-json-output.md).

Change: filter records using raw JSON while preserving every retained record
and unknown top-level envelope field. Keep table rendering independent. Do not
solve compatibility by adding only the currently known `health` field.

Acceptance:

- Default text and JSON exclude only DESTROYED; stopped/failed records remain.
- `--all` and `-a` retain the complete collection.
- Known and unknown nested/top-level fields survive filtering unchanged.
- Empty/all-destroyed results contain `sandboxes: []`, not null.
- A newer daemon remains usable with this CLI without field loss.

### UX-01 — Make destruction cleanup consistent

Evidence: an isolated running-job fixture completed ForceDestroy but retained
its workspace lease owner, nonce, and expiry. Ordinary Destroy explicitly calls
`releaseWorkspaceLeases`; ForceDestroy does not. The dashboard always sends
`force:true`.

Sources: [Destroy/ForceDestroy](internal/daemon/sandbox_manager.go),
[dashboard destroySandbox](internal/dashboard/static/assets/app.js),
[existing normal-destroy lease test](internal/daemon/sandbox_manager_test.go).

Correction to the first review: normal Destroy already permits RUNNING ->
DESTROYED, and both variants stop the VM. The claim that force bypasses an
existing blanket restriction on running sandboxes was overstated. Comments and
some error text also disagree with the state machine. The verified priority is
cleanup parity, not adding an assumed missing stop step.

Change: share successful destruction cleanup, including workspace lease release,
address/exposure/secret cleanup and resource accounting. Use ordinary destruction
from the dashboard; expose exceptional force behavior only if it has a distinct,
documented meaning. Show sandbox name, state, and linked workload in confirmation.

Acceptance:

- Normal and forced destruction release leases belonging to the destroyed work.
- An unrelated/new owner's lease is never released by stale cleanup.
- Repeated requests are safe; partial backend failures remain diagnosable.
- State transitions, error messages, comments, and UI behavior agree.
- Regressions use an isolated DB and stub backend, not a live destructive test.

### AX-01 — Parse command arguments consistently

Evidence: `sandbox show 123 --json` prints text; `--json sandbox show 123
--bogus` succeeds; `logs 123 --follow` returns immediately. SSH already has a
separate parser supporting flags on either side of the VMID.

Sources: [parseFlags and command handlers](cmd/agentlab/commands.go),
[global parsing](cmd/agentlab/main.go), [SSH parsing](cmd/agentlab/ssh.go),
[modifiers](cmd/agentlab/modifiers.go).

Change: establish one parsing contract for flags before/after positional
arguments; validate arity and unknown flags before any request. Preserve the
literal remote-command tail after `--`, repeated flags, `--flag=value`, boolean
false values, quoted values, and modifier syntax. Do not implement this by
blindly reordering arbitrary strings.

Acceptance:

- Equivalent documented flag placements produce equivalent requests/output.
- Unknown flags and surplus arguments fail with exit 2 before network activity.
- Global flag values are never mistaken for command names or IDs.
- SSH remote argv, repeated tags, and +modifiers retain their semantics.
- Cover show, logs, artifacts, snapshots, workspace repair, and session commands
  at the process boundary, including the examples currently in documentation.

### AX-02 — Make errors machine-readable and useful

Evidence: `sandbox show --json 124` produces a human error on a fixture 404,
while global `--json sandbox show 124` produces JSON. `sandbox list --json
--bogus` prints usage and human text. Invalid VMIDs exit 1 instead of usage exit 2.

Sources: [main error handling](cmd/agentlab/main.go),
[error metadata](cmd/agentlab/errors.go), [argument validation](cmd/agentlab/commands.go).

Change: resolve output mode once and propagate it through error handling.
Introduce typed usage errors consistently; include stable local error codes and
structured next-step/hint fields alongside existing daemon codes. Keep useful
human explanations, but keep usage banners out of machine output.

Acceptance:

- Every supported --json placement produces exactly one JSON failure document.
- Invalid arguments exit 2; operational failures exit 1; help/success exit 0.
- Codes distinguish syntax, validation, authorization, not-found, and transport
  failures without requiring prose matching.
- Preserve the documented validation-result behavior: `ok:false` exits 1 with
  one result document, without appending a second error envelope.
- Explicit `--json=false` overrides a saved JSON default.

Dependencies: AX-01 and the preference precedence policy in AX-05.

### AX-03 — Make provisioning and retries recoverable

Evidence: sandbox creation allocates a record and then blocks until provisioning
finishes before returning its identifier. Provisioning outlives the HTTP request.
The dashboard proxy times out at 30 seconds and the create form remains enabled.
There is no request idempotency key in the create contract.

Sources: [handleSandboxCreate](internal/daemon/api.go),
[proxy timeout](internal/dashboard/server.go),
[create form](internal/dashboard/static/assets/app.js).

Change in stages:

1. Add an opt-in asynchronous create response with a stable resource/operation
   ID and status URL. Preserve the existing blocking API for current callers.
2. Make dashboard creation use it and show pending/running/failed progress;
   disable duplicate submit while an acknowledged request is in flight.
3. Add persisted idempotency keys scoped to caller and operation. Reusing a key
   with the same payload returns the existing operation; a different payload
   returns a conflict. Recheck authorization on lookup/replay.
4. Expose CLI wait/no-wait behavior without silently changing existing JSON
   response expectations. Errors after allocation retain the operation/VMID.

Acceptance: a dropped response, retry, slow clone over 30 seconds, and daemon
restart never cause an untraceable duplicate allocation for one idempotency key.
The operator can distinguish rejected, accepted/in-progress, failed, and unknown
outcome. Document timeout versus cancellation semantics explicitly.

### AX-09 — Make exec dry-run validate the actual command

Evidence: `/v1/exec/dry-run` accepts `sandbox imaginary --bogus` with `ok:true`
and rejects the implemented `pool status`. It only checks the first word against
a separately maintained allowlist; leading flags bypass even that check.

Sources: [handleDryRun/isValidCLICommand](internal/api/exec.go),
[CLI dispatch](cmd/agentlab/main.go).

Change: use the same command/argument definitions as the CLI. Return the level
of validation performed. Distinguish syntax validation from daemon preflight and
from permission to execute. Never run a mutating command to validate its syntax.

Acceptance: reject unknown nested commands, unknown flags, missing arguments,
and incompatible options; accept every real command and global flag placement;
preserve quoted argv. Syntax dry-run makes no infrastructure or preference
changes. Keep the existing full-access authorization boundary.

Dependencies: AX-01; reusable definitions also support UX-04.

### AX-10 and AX-08 — Make execution outcomes truthful

Evidence: a one-second timeout of a longer-running fixture reports exit 1 in
both capture and streaming mode, rather than documented 124. A 6 MiB successful
capture returns exit 0 and exactly 4 MiB of stdout without a truncation marker.
Streaming does emit an output-cap notice when a finite fixture exceeds the cap.

Source: [exec handlers and limitWriter](internal/api/exec.go).

Change: classify context deadline/cancellation before generic process-exit
errors. Add explicit timeout/cancellation and per-stream truncation metadata,
including captured/dropped byte counts where available. Return the effective
timeout when clamped. Preserve the existing stdout/stderr/exit_code fields.

Acceptance:

- Deadline expiry reports documented 124 in capture and terminal stream events.
- Ordinary process exit 1 remains distinguishable from a deadline.
- Successful but truncated capture is explicitly marked, even if stdout is JSON.
- Stream consumers receive a machine-readable truncation signal and final exit.
- Bounded memory/output remain enforced; Unicode boundaries and both streams
  are covered. Larger results have a documented streaming/artifact path.

### UX-02 — Correct dashboard state counts

Evidence: executing the shipped JS with two RUNNING and one READY sandbox plus
one RUNNING and two QUEUED jobs displays zero active counts. API keys are
uppercase; the UI reads lowercase. The sandbox expression also uses OR, not sum.

Source: [loadStatus](internal/dashboard/static/assets/app.js).

Change: use canonical state values and explicitly define active totals. Separate
current resources from historical destroyed records in the summary.

Acceptance: fixture counts match expected totals for mixed, empty, and unknown
states; unknown states remain visible without being misclassified as active.

### UX-03 — Make pruning targets and results explicit

Evidence: UI says Prune Stopped; `PruneOrphans` processes only TIMEOUT records
and can skip individual failures while returning a count.

Sources: [bulk actions](internal/dashboard/static/assets/app.js),
[PruneOrphans](internal/daemon/sandbox_manager.go).

Change: initially rename to Prune Timed-out Sandboxes to match current behavior.
Show a dry-run preview of targets and return per-resource outcomes, including
partial failures. Do not silently expand pruning to stopped machines.

Acceptance: stopped machines remain untouched; preview matches execution;
partial failures are visible and identify retryable targets; an empty target
set is explained. JSON and UI report the same result.

### UX-05 — Preserve proxy request semantics

Evidence: a Unix-socket fixture receives `/v1/sandboxes/123/events` when the
incoming dashboard request includes `?tail=2&after=7`. Forwarders use URL.Path
and discard RawQuery. This affects event cursors and artifact selectors too.

Source: [dashboard proxy handlers](internal/dashboard/server.go).

Change: preserve query parameters and correctly encoded path segments through
a shared URL builder. Keep incoming authentication separate from outbound daemon
authentication. Preserve daemon status and response metadata.

Acceptance: event limits/cursors, scoped message queries, and artifact selectors
arrive unchanged; percent-encoded values survive; every proxy handler is covered.

### UX-06 — Repair the Events journey

Evidence: `loadEvents` requests unscoped `/v1/messages`, but the daemon requires
scope_type and scope_id. The daemon fixture returns 400, the JS shows an error,
and the later failure-digest fetch is skipped.

Sources: [loadEvents](internal/dashboard/static/assets/app.js),
[handleMessageList/parseMessageScope](internal/daemon/api.go).

Change: load general status/failure events independently. Provide an explicit
job/workspace/session selector for scoped messages, or a separate authorized
aggregate event endpoint if that product scope is deliberately added. Label the
feed's coverage accurately; messages plus failures are not a full audit stream.

Acceptance: Events loads useful data without a selected message scope; selecting
a scope fetches that scope correctly; one source failing does not hide others;
empty, unauthorized, unavailable, and stale states are distinguishable.

Dependency: UX-05 for query forwarding. Preserve scope authorization.

### UX-07 — Coordinate authentication and refresh

Evidence: executing three concurrent initial requests produces three token
prompts, even after the first prompt supplies the token. Polling continues every
five seconds and each request independently handles 401.

Source: [api/init/refreshAll](internal/dashboard/static/assets/app.js).

Change: use one authentication state and one shared pending login operation.
Pause polling while authentication is required. After success, retry waiting
requests using the current token. Avoid overlapping refresh cycles. Provide a
clear retry/cancel flow and accessible form rather than repeated native prompts.

Acceptance: a full initial refresh asks once; cancel does not reopen prompts on
every timer tick; expired tokens recover once; failure preserves useful stale
data with a timestamp. Do not treat a daemon-side authorization error as an
endless request for a different browser token.

### AX-04 — Add complete job discovery

Evidence: CLI has no job list; GET `/v1/jobs` is unsupported. The dashboard uses
partial status projections/failure digests, fills metadata with dashes, and only
adds a View action for lowercase failed status.

Sources: [handleJobs](internal/daemon/api.go), [loadJobs](internal/dashboard/static/assets/app.js),
[job storage](internal/db/jobs.go).

Change: add an authorized, paginated jobs collection and `job list` with state,
workspace/profile filters and deterministic ordering. Render actual job metadata
in the dashboard and permit details for every status. Show job-to-sandbox and
workspace links. Add explicit job outcome fields where existing report data
supports them; do not invent exit codes for historical jobs missing that data.

Acceptance: find a running/successful/failed job after losing its ID; page
without duplicates; preserve scope/ownership isolation; inspect any result and
artifacts. Avoid suggesting that an empty projection means no jobs exist.

### AX-05 — Make stored preferences effective

Evidence: isolated saved JSON/3s/custom-socket preferences still resolve to
text/10m/default socket. Saved default-profile does not satisfy sandbox new.
The stored output-format helper has no callers.

Sources: [defaults commands](cmd/agentlab/defaults_commands.go),
[global options](cmd/agentlab/main.go), [sandbox creation](cmd/agentlab/commands.go).

Change: define and implement precedence: explicit CLI flag, applicable existing
environment override, saved preference, built-in default. Validate names and
values on write, and surface corrupt preference files. Show effective values and
their origins. Only support default-backend where a command has an actual backend
option; never silently override a daemon's configured backend from client prefs.

Acceptance: every advertised key has a tested consumer or is rejected with a
clear explanation; explicit values win; JSON false works; existing remote
endpoint/token precedence is preserved; tests never touch real user config.

### AX-06 — Publish schemas agents can use

Evidence: schema lists endpoint paths and Go type names without definitions of
request fields. It also lists a job-events path explicitly noted as unimplemented.
Some current route families are absent from the catalog.

Sources: [schema catalog](internal/daemon/schema.go),
[request/response types](internal/daemon/api_types.go), [routes](internal/daemon/api.go).

Change: generate machine-readable schemas with required/optional fields, enums,
defaults, constraints, examples, error shapes, and authorization requirements.
Add configured capabilities separately from the static API version, including
backend-dependent availability. Describe operation completion and retry semantics.

Acceptance: an agent can construct a valid create request without reading Go
source; all advertised routes exist; implemented public routes are represented;
examples validate; compatibility rules explicitly distinguish additive fields
from breaking changes. Schema tests check route/type parity, not just nonempty
arrays. Keep unknown-field tolerance in clients.

### DOC-01 — Verify onboarding and task recipes

Evidence: install tutorial chmods config.yaml without creating it; the agent
loop provisions a standalone sandbox and then starts a separate job that allocates
its own sandbox. Recovery recipes place --repair/--restart after positional IDs,
which current parsing ignores. Root llms files and historical plans duplicate
and contradict current capabilities and requirements.

Sources: [install tutorial](docs/tutorials/install-agentlab.md),
[agent loop](docs/how-to/drive-agentlab-as-a-coding-agent.md),
[recovery guide](docs/how-to/recover-with-revert-and-fsck.md), [llms.txt](llms.txt),
[snippet checker](scripts/dev/docs_snippets_check.sh).

Change:

- Give laptop CLI users and Proxmox operators separate setup paths. Create a
  valid config before startup, and use the actual repository/install locations
  without changing the intentionally different Go module path.
- Separate interactive sandbox use from unattended job execution. The default
  job recipe starts one job and discovers its sandbox from sandbox_vmid.
- Make teardown and persistent workspace handling explicit and consistent.
- Correct HTTP/tailnet opt-in examples, actual config locations on macOS/Linux,
  and SSH print-versus-execute behavior.
- Generate command references/llms inventories from current definitions; mark
  historical root reports clearly rather than treating them as current guides.

Acceptance: run documented command recipes against safe fixtures, including
failure/recovery paths. A fresh-user walkthrough reaches one working sandbox or
one job without hidden config steps or an extra unused VM. Prefix-only snippet
validation is insufficient. Real host provisioning remains a separate controlled
acceptance run.

### DOC-02 — Fix installer success and cleanup

Evidence: running the actual installer against a fake local archive installs the
binary and prints done, then exits 1 with `tmpdir: unbound variable`. The EXIT
trap references a function-local variable after main has returned. Separately,
latest-version resolution directly requires curl although download advertises a
wget fallback.

Source: [installer](scripts/install.sh).

Change: give cleanup state a valid lifetime, preserve the meaningful exit status,
and use the same supported download mechanism for release metadata and archives.
Make shell/PATH instructions appropriate for the user's shell. Preserve archive
names and the existing GitHub release pipeline.

Acceptance: fixture install exits 0 and removes temp files; download/extraction/
install failures exit nonzero and clean up; curl-only and wget-only environments
work as advertised. Verify published checksums before replacing an existing
binary as part of installer hardening; failed verification retains the old binary.
No release publication is needed to test this change.

### UX-04 — Unify help, completion, and command discovery

Evidence: help is a large duplicated usage string; group help gives little
explanation. Completion omits implemented commands/flags, including sandbox
snapshot, secret mutations, and the recent list --all flag.

Sources: [help/dispatch](cmd/agentlab/main.go),
[completions](cmd/agentlab/completion_commands.go), [exec dry-run](internal/api/exec.go).

Change: share command metadata with the parser. Offer concise root help,
examples and defaults in command help, and generated bash/zsh/fish completion.
Avoid a broad framework migration unless needed for AX-01's behavior.

Acceptance: every command/alias/flag is discoverable; examples parse; completion
understands value-taking global flags; regeneration has no unexplained diff.

## Product improvements after correctness

### IMP-01 — Make inventory easy to scan

Proposed: show current sandboxes by default in the dashboard, with an explicit
history toggle. Add search, state/profile filters, consistent pagination, and
optional wide output. Use short human timestamps with exact values available;
keep JSON timestamps lossless. Empty states should explain filters and offer the
relevant next action. Label database state versus live inventory/reconciliation
results and show when each was refreshed. Do not silently redefine API defaults
while fixing one presentation surface.

Acceptance: a fixture with 100+ destroyed records and one stopped/running machine
makes the current machine immediately discoverable in CLI and dashboard; a user
can still inspect history. Missing permission and unavailable data never appear
as a misleading empty collection.

### IMP-02 — Make waiting and recovery explicit

Proposed: add bounded `job wait`/sandbox wait behavior with documented terminal
states and exit outcomes, backed by AX-03's operation identifiers. Present current
stage, elapsed time, resource/job IDs, and an actionable next command. Explain
whether interrupting a client merely stops waiting or cancels work. Surface lease
expiry, persistent workspace ownership, and artifacts alongside job results.

Acceptance: an agent can create, wait, inspect the result, collect artifacts, and
clean up without scraping human text or guessing whether a timeout cancelled the
work. A human can resume observation after disconnecting using one identifier.

## QA-01 — Validation strategy and delivery checks

For each item, add regressions that fail on the observed public behavior and pass
on the desired contract. Avoid tests that merely assert a source string exists.

- CLI: process-level stdout/stderr/exit checks against local HTTP fixtures;
  flag-position matrix; error and success JSON; unknown-field preservation.
- Daemon: isolated DB/stub backend for cleanup, idempotency, scope filtering,
  partial failures, and operation recovery. No live destructive tests by default.
- Exec: finite output exceeding the cap; explicit deadlines; both stream modes;
  dry-run parity. The current cap test depends on a shell loop producing enough
  output in two seconds and should use deterministic data instead.
- Dashboard: execute rendered behavior with API fixtures, then browser acceptance
  for authentication, counts, create progress, confirmation, stale/empty/error
  states, keyboard focus, modal dismissal, and narrow viewports. Structural
  security checks remain useful but do not establish functional correctness.
- Installer: fake release metadata/archive fixtures in temp prefixes, failure
  injection, checksum verification, and cleanup/exit status checks.
- Docs/schema: validate actual example arguments and response schemas, route
  coverage, and generated help/completion. The current snippet checker mainly
  validates command prefixes and misses argument/output contract drift.

Use `TMPDIR=/tmp` for local Go tests on macOS because long default temp paths can
exceed Unix socket path limits. Run the packages affected by each change, then
repository-required checks for the final batch. Record actual results here; do
not describe unrun live/browser/backend checks as passing. Preserve module and
archive names; prepare/publish releases only within explicitly requested scope.

## Validation record

| Check | Result | Interpretation |
| --- | --- | --- |
| CLI local endpoint matrix | Reproduced text despite trailing --json, ignored trailing unknown flag, immediate return for trailing --follow, and invalid VMID exit 1 | AX-01/AX-02 confirmed |
| JSON fixture with health and future_field | Both fields disappear by default and survive --all | AX-07 confirmed |
| Isolated preferences overlay | Stored output/timeout/socket/profile have no effect on relevant commands | AX-05 confirmed |
| Exec dry-run overlay | Invalid nested command accepted; valid pool status rejected | AX-09 confirmed |
| Exec timeout overlay | Capture and streaming both return 1 after deadline | AX-10 confirmed |
| Finite 6 MiB exec fixture | Capture returns 0 and 4 MiB without marker; stream emits cap notice | AX-08 confirmed; streaming cap itself works in this fixture |
| Existing streaming-cap test | Failed during first review, passed on second-pass isolated rerun | Timing-dependent coverage; not proof of a missing streaming cap |
| Dashboard proxy socket fixture | Query removed before upstream request | UX-05 confirmed |
| Shipped JS status fixture | Mixed uppercase active states render zero active | UX-02 confirmed |
| Daemon + shipped JS Events fixtures | Missing scope returns 400; failure digest not fetched afterward | UX-06 confirmed |
| Shipped JS concurrent auth fixture | Three initial requests produce three prompts | UX-07 confirmed |
| ForceDestroy DB/backend fixture | Success with job workspace lease owner/nonce/expiry retained | UX-01 confirmed and first-pass explanation corrected |
| Actual installer with fake archive | Binary installed and done printed, then exit 1 from trap | DOC-02 confirmed |
| Baseline CLI/dashboard/API packages | `TMPDIR=/tmp go test -json ./cmd/agentlab ./internal/dashboard ./internal/api` passed; exit 0 | Does not replace contract/browser tests |

Temporary review probes were supplied with Go overlays and fixtures outside the
tracked source tree. Their PASS results mean the observations ran successfully;
production behavior is still unfixed. Reimplement them as acceptance regressions
with the opposite expectations when taking the corresponding work item.

## Decisions and corrections

- Keep this plan separate from the older comparison-based improvement plan.
- Promote AX-07 to P1: it is a regression in the just-installed CLI and violates
  the documented daemon-field pass-through contract.
- Correct UX-01's initial explanation: both destroy paths allow a running sandbox
  to be destroyed; the reproduced defect is forced cleanup leaving workspace
  leases behind. Update stale comments/error messages along with the fix.
- Treat the previously failed streaming-cap test as timing-dependent. A finite
  output fixture demonstrated the cap notice; capture truncation and timeout
  classification remain separate, reproduced product defects.
- Preserve existing blocking creation APIs while adding an explicit asynchronous
  path and idempotency. Record any later compatibility decision here.
- No application code, installed binary, host configuration, or release changed
  by this review. All implementation items remain open. Unrelated changes to
  `scripts/systemd/agentlabd.service` and new `docs/specs/` files appeared during
  the review and were left untouched; they are outside this baseline assessment.

## Keeping this plan current

1. Before starting an item, mark it In progress and list its concrete scope.
2. Update evidence or correct claims immediately when new facts change the plan.
3. Record implementation files/commit, checks run, results, and remaining limits.
4. Mark Done only when its acceptance criteria pass; split partial work explicitly.
5. Update dependencies and the next action at the top before handing off or
   committing a completed batch. Add discovered work with a stable ID.
6. Record installed/deployed versions separately from committed fixes. Do not
   equate a source fix with a successful deployment or release.

## Implementation log

Worktree: `/Users/air/dev/agentlab-ax-ux`; branch: `feat/ax-ux-improvements`.
Main checkout contains unrelated uncommitted template work and is untouched.

Historical checkpoints follow. Their remaining-scope statements are superseded
by the final acceptance audit.

Implemented in the worktree (uncommitted; no installation, deployment, or release):

| Scope | Implementation and evidence | Remaining acceptance |
| --- | --- | --- |
| AX-07 | Raw JSON envelope/record filtering; fixture preserves health, unknown nested/envelope fields and large integers; empty array coverage | None |
| UX-01 | ForceDestroy shares Destroy; successful cleanup releases owned leases; isolated normal/force, repeated-call and unrelated-owner fixtures; dashboard ordinary destroy with details | Cross-cutting real-browser acceptance remains with QA-01; session/rebind/nonce/backend-failure fixtures now pass |
| AX-01/02 | Registered-type interspersed parsing; global extraction respects flag values and --; arity limits; process-boundary JSON/status/no-request regressions; typed local usage/transport errors and hints | Complete all semantic-validation, token parser, SSH/modifier and documented flag-position matrices |
| AX-05 | Saved socket, timeout, JSON, profile and image applied; writes validated; unsupported backend rejected; explicit JSON false; effective values/origins | Profile/image consumer and corrupt/unsupported preference coverage passes; broaden effective-origin process coverage |
| AX-03 | Persisted caller/key operations, conflicting payload rejection; allocation and operation VMID commit atomically; async create, dashboard submit guard/progress, CLI no-wait and operation show/wait | Actual dropped-response/concurrent retry and reloaded browser-key fixtures pass; complete slow-provision/restart authorization journey checks |
| AX-04 | Authorized ID-cursor jobs API and CLI filters; dashboard records, pagination, details for all states and sandbox references | Broader ownership/filter/cursor tests; workspace navigation and richer outcome acceptance |
| AX-09 / UX-04 | Generated registered-flag catalog used for dry-run, arity, catalog help and bash/zsh/fish completion; syntax-only validation level | Complete semantic parity and actual shell completion acceptance; descriptions/defaults, public help, catalog parity and generated inventories now implemented |
| AX-08/10 | Context-first timeout/cancel classification; effective timeout and per-stream capture counts/truncation; streaming truncation events; finite cap fixture; timeout tests in both modes | None for AX-08/10: Unicode and both-stream cap/accounting/live-stream regressions now pass |
| UX-02/05 | Canonical counts sum active states and separate destroyed history; every forwarding handler preserves query/escaped path, idempotency key, status and metadata | None for reproduced contracts |
| UX-03 | Timed-out wording, preview, preview-selected IDs and per-resource outcomes; stopped sandbox preserved in fixture | None for UX-03: failure/retry, changed/missing targets, CLI result status and executed dashboard preview fixtures pass |
| UX-06/07 | Independent scoped messages/failure digest; shared accessible login dialog, current-token retries, pause/cancel, refresh guard and stale-data timestamp | Real browser accessibility/layout/auth expiry and cancellation acceptance |
| AX-06 | Reflected JSON request/response definitions, required create fields, constraints/example, configured capabilities; removed phantom job-events route; added job/operation catalog | Complete route/type/authorization parity and schema constraints/examples across every family |
| DOC-01/02 | Separate laptop/host setup; actual repo source build; config created before chmod/start; one-job recipe and waits, preferences and exec metadata corrected; installer lifetime/download/checksum/PATH fixes | Broader executable recipe matrix and generated llms inventory; installer fixtures all pass |
| IMP-01/02 | Current inventory default, history/search/state/profile filters and paging; bounded job/sandbox/operation waits with resume guidance and cancellation semantics | Browser density/filter acceptance; fuller recovery/lease summaries |

Validation executed:

- `TMPDIR=/tmp go test ./...` passed twice after the major changes (logs in
  `/tmp/agentlab-ax-all.log` and `/tmp/agentlab-ax-all-final.log`). A later complete
  rerun is recorded in `/tmp/agentlab-ax-verified.log`; final fixture additions
  are being checked again.
- All six affected Go packages passed uncached together in
  `/tmp/agentlab-ax-final-batch.log`.
- `go vet ./...` passed; `git diff --check` passed.
- Node dashboard behavior fixtures: five passing tests for mixed/unknown/empty
  counts, independent scoped Events, concurrent shared login, and daemon 401.
  Go dashboard tests invoke these fixtures when Node is available.
- Actual installer fixtures: five passing tests, curl-only/wget-only success and
  checksum/download/extraction failures with cleanup and old-binary preservation.
- Proxy Unix-socket fixture exercises every forwarding handler with queries,
  escaped path segments, idempotency keys, distinct daemon authentication and
  response metadata.
- Added safe queued-runner operation replay/conflict/restart/caller-isolation,
  scoped job pagination, and prune preview tests. No live VM actions.
- The existing `TestServe_GracefulShutdown` failed at its five-second bound in
  several focused runs, while full runs passed. This predates the new operation
  fixtures and remains a timing/environment verification caveat.
- Browser acceptance attempted with a local fake daemon/dashboard: computer-use
  reports `Browser is not available: iab`; browser inventory is empty. No visual,
  keyboard, narrow-viewport or real-browser authentication pass is claimed.
  Temporary preview servers were stopped.

The goal remains active because the acceptance gaps above are not complete.

### Continuation audit — CLI, cleanup, recovery, and quality

Previous goal turn classified as progress. This continuation inspected the current
worktree and corrected additional defects rather than assuming prior green runs
proved the whole plan:

- Fixed catalog operand extraction for `user show/rm`, `team rm/members`,
  `integration rm`, token inspection, and optional local secret bundle names.
  Added AST/runtime parser parity checks and expanded actual-process tests.
  Token and version commands now use the shared parser; each process JSON failure
  test verifies exactly one document and no network activity for usage errors.
- Removed obsolete hand-maintained completion trees and the first-word exec
  allowlist. Root help is concise; public leaf help and named help use generated
  command metadata, including aliases, flag descriptions/defaults, and examples.
  Generated CLI and llms inventories now ship through `make docs-gen`.
  `make docs-verify` checks regeneration without mutating tracked outputs or
  confusing legitimate uncommitted documentation with stale generation.
- Destruction captures lease nonces before backend work and atomically checks
  nonce plus current job/session binding on release. New isolated fixtures cover
  session leases, rebinds before/during destruction, same-owner nonce replacement,
  and backend failure. Sandbox details now expose linked job/session IDs, so
  dashboard confirmation no longer misleadingly reports every job as absent.
  Corrected stale API errors, CLI descriptions, README and state-machine docs.
- Prune reports retryability, skipped state changes and disappeared explicit
  targets. CLI supports repeated preview-selected `--vmid`, prints actionable
  retry commands, explains empty previews, and returns exit 1 with one result
  document for partial failures. Invalid `dry_run` values are rejected before
  destruction. Backend-failure/retry, stopped/missing-target and executed dashboard
  preview/action fixtures pass. UX-03 reproduced-contract acceptance is complete.
- Added Unicode capture and stream cap tests for both stdout/stderr, exact byte
  accounting, terminal exit events and a handshake proving small output arrives
  before process exit. Corrected the prior implementation's 4 KiB streaming delay.
  Timeout clamping now also handles maximum integer inputs without overflow.
  AX-08 acceptance is complete; no live guest execution was required.
- Closed an operation-liveness race between DB reservation and concurrent replay,
  prevented a second allocation under an already-linked operation, and bounded
  final completion persistence during shutdown. An actual HTTP dropped-response
  test with twelve concurrent retries schedules exactly one background operation
  and allocates exactly one sandbox. Dashboard recovery persists the exact key,
  payload and operation ID across page reloads and offers explicit resume.
- Saved profile/image preferences reach actual create requests; explicit overrides
  win. Unsupported saved preferences can be deleted using the CLI. Corrupt file
  errors identify the file, and help remains available without loading it.
- Diagnosed the recurring shutdown timeout using goroutine stacks: HTTP grace and
  SQLite close could reach the test's exact five-second deadline. Lifecycle probes
  now drain responses and disable keepalives; the test allows the production grace
  period plus a bounded cleanup margin and retains failure stack diagnostics.
  Five consecutive isolated shutdown runs pass.

Verification evidence for this continuation:

| Check | Result |
| --- | --- |
| `GOTOOLCHAIN=go1.25.0 TMPDIR=/tmp go test ./...` | Passed, `/tmp/agentlab-ax-go125-all.log` |
| Full tests on installed Go 1.27 after the shutdown fix | Passed before the last help-only cleanup, `/tmp/agentlab-ax-turn-all-verified.log` |
| Targeted daemon/API `-race` for retry, lease, prune, Unicode and live stream handshakes | Passed, `/tmp/agentlab-ax-continuation-race.log` |
| `GOTOOLCHAIN=go1.25.0 make quality` | Passed: vet, Staticcheck, govulncheck, docs lint, links, typos, snippets; `/tmp/agentlab-ax-quality-fixed.log` |
| Default `make quality` under installed Go 1.27 | Pinned Staticcheck cannot decode Go 1.27 export data; verified workaround is the repository-supported Go 1.25 toolchain, without changing tool pins |
| `make docs-gen` followed by `make docs-verify` | Passed; command inventory, examples and generated reference are current |
| Executed dashboard JS fixtures | Eight passed, including reloaded create recovery, actual workload confirmation and preview-selected prune failures |
| Actual installer fixtures | Five passed again, `/tmp/agentlab-ax-turn-installer.log` |
| `git diff --check` | Passed |

Remaining scope is still the full plan. In particular: complete API route/type,
authorization, constraints and example coverage; finish shared semantic validation
and shell completion acceptance; extend job filter/ownership/outcome and dashboard
navigation/recovery checks; broaden executable onboarding/recovery recipes and
full inventory/wait journey coverage. Real-browser layout, focus, dismissal,
authentication expiry/cancel and narrow viewport acceptance remain unverified.
No additional browser surface became available. No deployment/release occurred;
all implementation remains in the isolated, uncommitted worktree.


### Continuation audit — executable schema contracts and shell wrappers

Expanded the schema registry to all named daemon wire types plus exec types.
Registry/user/team handlers now use named wire structs so published schemas and
actual decoding/encoding share their source. Requests publish strict fields,
examples, required fields, defaults, enums, bounds and cross-field constraints;
responses tolerate additive fields and accurately describe nullable collections.
Every request example validates with a Draft 7 validator and decodes through the
actual strict JSON decoder. Invalid workspace choices, update bodies, ports,
create types, integration combinations and empty exec requests are rejected by
schema tests.

Added missing pool, secret, integration, user/team, exec and guest route families.
Every advertised control method is exercised against the real mux with a denied
token and checked against its actual permission. Registered control families are
checked for catalog coverage; all schema type references compile. Real collection
responses validate against their published definitions. Listener availability,
backend support, scope restrictions, parameters, async/stream variants, completion
and retry semantics are now explicit. Configured examples select a real profile.
Guest source/secret credentials and storage/profile readiness remain runtime
preflight requirements rather than claims of schema validity.

The old snapshot singular path was advertised but not implemented and was used
by the dashboard. It now aliases the canonical snapshots path before the common
authorization check; the dashboard uses the canonical path. Corrected stale API
documentation claiming integration handlers skipped authorization. Schema registry
freshness is checked by `make docs-verify`.

Executed the generated completion wrappers in actual Bash and Zsh, covering
nested commands, global flag values and flags following operands. Both pass.
Fish is absent, so its runtime wrapper acceptance remains open. Browser surfaces
remain unavailable; no visual/browser pass is claimed.

Validation:

- Full `GOTOOLCHAIN=go1.25.0 TMPDIR=/tmp go test ./...` passed,
  `/tmp/agentlab-ax-schema-all.log`.
- `GOTOOLCHAIN=go1.25.0 make quality` passed,
  `/tmp/agentlab-ax-schema-quality.log`.
- Expanded schema contracts passed, `/tmp/agentlab-ax-schema-final.log`.
- Actual Bash/Zsh wrappers passed; Fish skipped because unavailable,
  `/tmp/agentlab-ax-shell-completion.log`.
- `make docs-verify` and `git diff --check` passed.

The overall goal remains active. Shared CLI semantic parity, broader job
ownership/filter/outcome and navigation fixtures, executable recipe/recovery
matrices, Fish, and real-browser acceptance still require work. All changes remain
uncommitted in the isolated worktree; no installation or release occurred.

### Continuation audit — shared CLI option and value validation

Moved command option-combination validation into `internal/clicontract` and call
it from both parsed CLI flags and exec syntax dry-run. No-wait/SSH conflicts,
restart choices, artifact selectors, explicit empty-tag replacement and workspace
creation requirements now share rules. Boolean options use Go boolean parsing,
so `=0`, `=1` and `=TRUE` have the same meaning in validation and execution.
Scoped these rules to their actual commands rather than applying name-based
conflicts to unrelated commands.

Moved TTL, size, memory and workspace-wait value parsing into the shared package;
CLI wrappers preserve typed usage errors and existing units/rounding. Dry-run
now rejects those invalid values using the actual parsers. Added a memory
multiplication overflow guard. Explicit empty optional values preserve their
existing omission behavior. Added subprocess regressions proving incompatible
creation/update/workspace flags yield one JSON usage error with exit 2 and no
HTTP request, plus valid/invalid dry-run boolean/unit/combination matrices.

Both CLI and contract packages pass, `/tmp/agentlab-ax-shared-values-final.log`.
The full Go 1.25 suite passes, `/tmp/agentlab-ax-options-all.log`.
Generated-artifact verification and whitespace checks pass. Quality caught an
error-message punctuation issue, which was fixed; the final quality rerun is
recorded in `/tmp/agentlab-ax-options-quality-final.log`.

Next CLI parity work: SSH parsing/remote tails, numeric target operands, modifier
validation and saved-default-dependent required options. These remain explicit
acceptance gaps; the overall goal is active.

### Continuation audit — SSH, modifiers and target operands

Replaced SSH's manually maintained flag-value scanner with a shared parser built
from the generated command flags. Options may precede or follow the VMID;
explicit `--` and implicit remote command tails preserve quoted argv and remote
flags. Global extraction and help detection now respect implicit remote tails,
so `ssh 123 echo --json` or `--help` cannot change local output/help behavior.
SSH user/port errors are typed usage failures. Shared validation covers port
bounds, blank users and JSON/exec conflicts.

Modifier syntax uses one parser for normal commands and dry-run; empty `+` and
whitespace-only modifiers fail before requests. Profile lookup remains preflight.
The generator derives numeric VMID operand indexes from actual command parsers;
dry-run validates those targets using the CLI's shared VMID parser, including
workspace attach and sandbox wait. Removed obsolete SSH splitting helpers.

Dry-run now publishes explicit deferred checks for saved defaults, daemon
profile/storage/policy, resource authorization and terminal requirements. An
omitted sandbox profile is syntactically valid because a saved default may
supply it; syntax-only validation does not read preferences or claim the command
will execute successfully. Required non-defaulted arguments remain checked.
Added API, shell-argv and process tests for these distinctions, including remote
quoted/global/help flags, unknown SSH flags, invalid ports/VMIDs, empty modifiers
and no-request JSON usage errors.

Validation: full Go 1.25 tests passed in
`/tmp/agentlab-ax-ssh-complete-all.log`; all repository quality checks passed in
`/tmp/agentlab-ax-ssh-complete-quality.log`. Generated artifacts and whitespace
checks pass. The goal remains active with job ownership/filter/outcome/navigation,
recipe/recovery and browser/Fish acceptance still outstanding. Changes remain
uncommitted in the isolated worktree.

### Continuation audit — job outcomes, navigation and scoped pagination

Added optional structured `outcome` fields derived only from stored runner
reports. Recorded zero exit/duration values are retained, malformed individual
runner fields are omitted, historical absent values remain absent, and original
result JSON is unchanged. Shared job conversion exposes the projection in both
detail and collection responses, and the generated schema includes its shape.
The dashboard shows exit/duration/message/commit with explicit "Not recorded"
wording for missing historical values.

Job rows/details now navigate to workspace details and sandbox details.
Workspace inventory IDs open details, and attached sandboxes are clickable.
Workspace detail renders actual stored metadata and raw JSON, without inventing
lease information absent from that response. The executed JS fixture follows a
job-to-workspace action, checks encoded IDs, recorded zero outcomes and missing
historical wording. Nine JS fixtures pass in
`/tmp/agentlab-ax-job-dashboard.log`.

Added an API fixture with 430 interleaved in-scope/out-of-scope jobs plus an
unbound job. It pages through the real HTTP handler across internal storage
page boundaries, verifies combined state/profile/workspace filters, deterministic
cursor ordering, no duplicates/leaks, omitted unbound work, empty-array results
and recorded outcome projection. Existing authorization fixtures retain the
permission boundary. Focused contracts pass in
`/tmp/agentlab-ax-job-contracts.log`.

CLI next-page guidance retains state/profile/workspace filters and limit, using
shell-safe quoting. HTTP documentation now includes the jobs collection and the
recorded-versus-missing outcome contract. Full test and quality results are in
`/tmp/agentlab-ax-job-final-all.log` and
`/tmp/agentlab-ax-job-final-quality.log`; generated-file verification passes.

Remaining goal scope includes executable documentation/recovery matrices,
remaining wait/recovery summaries, broader command-definition semantic coverage
and actual browser/Fish acceptance. All changes remain uncommitted in the
isolated worktree; no live infrastructure actions or release occurred.

### Continuation audit — wait recovery and executable recovery guide

Wait commands now preserve raw resource fields while adding an `observation`
object with resource type/ID, terminal state, success, elapsed seconds, explicit
`cancelled_work:false` and next commands. Job results point to artifact listing
and download; failures add doctor guidance. Text mode reports state changes and
elapsed time to stderr and prints final next steps. JSON stays one document with
no progress text. Operation failure/unknown output retains operation identity,
actual allocation VMID when known, and a recorded daemon error when present.
Resume commands are shell-quoted; transport/timeouts retain the exact ID.

Subprocess tests verify success/failure exit status, a single JSON document,
unknown fields and a large integer surviving wait output. A bounded running-job
fixture proves timeout remains a deadline error, prints no partial JSON, uses
GET only and carries an exact resume command. No remote cancellation is claimed.
The new wait metadata/state/exit contract is documented in the JSON reference.

Added a subprocess test extracting the actual command lines from the recovery
guide and executing them against a local wire fixture. It checks detach/revert,
read-only check/fsck, repair, attach and inspection. Request bodies prove that
post-ID `--restart`/`--force` and `--repair` take effect. The fixture includes the
existing sandbox inspection lease-touch request. The guide now explicitly
detaches the persistent workspace before root revert and describes workspace
check as comparison rather than filesystem repair.

Full Go 1.25 tests pass in `/tmp/agentlab-ax-recovery-all.log`; repository quality
checks pass in `/tmp/agentlab-ax-recovery-quality.log`. Generated files and
whitespace verification pass. Remaining full-plan work includes onboarding/job
recipe matrices, lease/workspace/artifact summaries, a final command semantics
audit, and browser/Fish runtime acceptance. Goal remains active; worktree changes
are uncommitted and no live VM or release was touched.

### Continuation audit — real Chrome dashboard acceptance

The installed Chrome can run headlessly with a separate profile even though the
computer-use browser service is absent. Added an opt-in Playwright/Chrome test
using production static assets, security headers and inbound authentication with
a local API fixture. Optional driver setup is pinned and documented in
CONTRIBUTING.md; no application npm dependency or live VM is required.

The browser found that modals never received focus. Added shared open/close
behavior with dialog semantics, initial focus, Tab/Shift-Tab trapping, Escape and
backdrop dismissal, and focus restoration. Narrow layout now wraps navigation
and actions, bounds modal width and scrolls resource tables. Visual inspection
also caught the checkbox inheriting full text-input width; checkbox labels now
render together. Login has an accessible dialog name.

Actual Chrome acceptance passes for one shared initial login; expired token,
cancel/poll pause and explicit retry; daemon-denial versus browser-auth handling;
canonical counts; current inventory with 101 destroyed records; history/search
and explanatory filtered empty state; stale data after a 503; keyed creation and
operation completion; modal focus/trapping/Escape/return focus; and a 390px
viewport without document overflow. Confirmation cancellation checks identify
the VMID and perform no destroy request. Desktop modal and narrow screenshots
are retained in `/tmp/agentlab-ax-dashboard-modal.png` and
`/tmp/agentlab-ax-dashboard-mobile.png`.

Unavailable collection endpoints now report unavailable data instead of a false
empty collection, and workspace refresh errors retain stale rows. Corrected the
agent guide's obsolete exit-code wording and its extra-provisioning phase list.
Full Go tests pass in `/tmp/agentlab-ax-browser-all.log`; all dashboard tests with
Chrome pass in `/tmp/agentlab-ax-browser-verified.log`; real-browser confirmation
coverage is in `/tmp/agentlab-ax-browser-confirmation.log`. Repository quality
passes in `/tmp/agentlab-ax-browser-final-quality.log`; generated-file and
whitespace checks pass.

The browser blocker is resolved. Remaining acceptance includes executable
onboarding/job journeys, lease/workspace/artifact summaries, Fish runtime and a
final requirement-by-requirement audit. Goal remains active, and implementation
is still isolated and uncommitted without installation or release.

### Continuation audit — executable onboarding and one-job journey

Added an executable Bash block to the agent guide, using explicit repository,
profile and task inputs plus jq. It discovers capabilities/profiles, validates,
submits exactly one job with keepalive, waits, reads its result, downloads a
recorded artifact and destroys that job's sandbox. A failed terminal job still
collects/cleans up and returns 1. A wait/transport error retains its JSON resume
guidance and does not destroy potentially active work.

The subprocess test extracts and executes this exact documentation block against
a local HTTP fixture. Four cases pass: completion, failed terminal job,
preflight rejection (no creation), and unavailable observation (no cleanup).
It verifies one job submission and no standalone sandbox create, quoted task and
keepalive request fields, exact artifact selector and downloaded bytes, and one
cleanup only for terminal work. The fresh-user connect/status test likewise uses
the install guide's actual lines, temp user configuration and no endpoint/token
environment override; the second process reaches the saved endpoint with the
saved token. Connect's read-only host discovery is included in the fixture.

Workspace API records now expose recorded lease owner and expiry for recovery,
without exposing the nonce. CLI workspace/check summaries and dashboard details
show these values. Fixed future lease expiry previously displayed as "just now";
sandbox details show its exact timestamp plus a future-relative interval.
New daemon/JS regressions verify owner/expiry projection, nonce exclusion and
future-time wording. Ten dashboard behavior fixtures now pass.

Full Go 1.25 tests pass in `/tmp/agentlab-ax-onboarding-final-all.log`; repository
quality passes in `/tmp/agentlab-ax-onboarding-final-quality.log`. All dashboard
tests with real Chrome pass in `/tmp/agentlab-ax-onboarding-browser.log`. The final
CLI and documentation checks are recorded in
`/tmp/agentlab-ax-onboarding-cli-final.log` and
`/tmp/agentlab-ax-onboarding-docs-final.log`. Generated files and whitespace checks
pass. No real host provisioning, user config, deployment or release was touched.

Next: audit the complete original acceptance criteria against current evidence,
finish remaining command/shell gaps and reconcile the status table. The goal
remains active and all changes are isolated and uncommitted.

### Continuation audit — all shell runtimes and process coverage

Fetched a Fish bottle and extracted it under `/tmp`, without installing it into
the system. The binary runs there. The initial completion subprocess crashed
with startup configuration enabled; its clean-start probe succeeded. Updated
all shell harnesses to disable user startup files (`--no-config`, `-f`, or
`--noprofile --norc`) so acceptance cannot depend on personal shell settings.
Actual Bash, Zsh and Fish wrappers now all pass nested command, global-value and
post-operand flag completion, `/tmp/agentlab-ax-three-shells-clean.log`.

The acceptance audit found logs follow lacked a process regression. Added both
leading/trailing flag placements: a real CLI process reads the initial event,
remains alive to fetch a second event with the correct after cursor, and exits
cleanly on interrupt with no stderr. This passes in
`/tmp/agentlab-ax-logs-process.log`. Extended the process matrix with valid session
show and snapshot list/save placements. The HTTP fixture checks snapshot target,
POST method, checkpoint name and force value rather than trusting exit 0 alone;
`/tmp/agentlab-ax-audit-process-matrix.log` passes.

Full Go 1.25 tests with Fish on PATH pass in `/tmp/agentlab-ax-audit-all.log`;
repository quality passes in `/tmp/agentlab-ax-audit-quality.log`.
Generated-artifact and whitespace checks pass.

The completion runtime gap is resolved. The goal is still active: audit the
literal over-30-second provisioning case, remaining schema route/constraint
coverage and command semantics, then reconcile every original item's status
against the final evidence. No release, live VM action or system shell install
occurred; work remains isolated and uncommitted.


### Continuation audit — slow clone, complete listener catalog and required flags

Added and executed the opt-in timed creation contract. A stub Clone call stays
blocked for 31 real seconds after allocation. The HTTP client has a 2s timeout,
but async acknowledgement and replay remain available. Replay retains operation
and VMID with one clone, a second controller reports that allocated resource as
unknown, and release/completion/replay still yield one sandbox and one clone.
It passes in `/tmp/agentlab-ax-slow-clone.log` (31.03s). The optional gate and safe
invocation are documented in CONTRIBUTING.md.

The schema audit found public liveness/metrics routes absent. Cataloged health
on control/bootstrap/artifact/metrics listeners and the optional metrics route,
including plain-text variants, no bearer requirement and registered-surface
availability. Metrics registration now participates in discovery. Removed the
health exemption from registered-route parity; the actual health body validates
as its primitive response type. Schema contracts pass in
`/tmp/agentlab-ax-schema-liveness.log`.

The command audit found required flags for registry/token/secret commands were
not visible to dry-run. The catalog generator now infers unconditional empty
flag guards from handler definitions. Shared option validation uses those
requirements for normal parsing and dry-run; help marks them required. Secret
public-key value/file exclusivity is shared too. New process tests prove missing
integration/team/token/secret inputs produce exit 2 with one JSON document before
requests or preference/key mutation. Syntax tests accept explicit key-file paths
without reading them. Command/contract tests pass in
`/tmp/agentlab-ax-required-contracts.log`.

Full Go 1.25 tests pass in `/tmp/agentlab-ax-audit-schema-all.log`; repository
quality passes in `/tmp/agentlab-ax-audit-schema-quality.log`. Generated files and
whitespace checks pass. The audit now closes UX-01, AX-03, UX-07, DOC-01, UX-04 and
IMP-02 based on their retained lifecycle, timed retry/restart, actual Chrome,
executable recipe, all-shell and wait/lease/artifact evidence above.

The overall goal remains active. Remaining audit work includes typed semantic
usage errors (notably token TTL/scope validation before key I/O), preference-origin
and remaining public-command semantics, plus reconciling the other item statuses.
No live VM actions, installation or release occurred; work remains isolated and
uncommitted.


### Continuation audit — semantic errors, preference origins and stale Events

Token claim validation now runs before private-key discovery/read and is shared
with dry-run: duration, nonempty command list, supported sandbox scope shape and
positive VMID. Zero token TTL preserves the auth library's one-hour fallback;
negative TTL is rejected. Valid scope VMIDs are canonicalized. Malformed token
inspection is a usage error. Message post/tail scope choices now share exactly-one
validation. Explicit request/observation timeouts must be positive, matching
saved preference validation; non-positive flags previously produced inconsistent
HTTP fallback versus immediate wait expiry. Updated the old parser-only tests
and reference for this explicit semantic contract.

Added real-process preference-origin coverage: built-ins, all supported saved
keys, saved connection, environment endpoint, explicit endpoint/socket/timeout,
JSON false, and no token disclosure. Explicit empty profile now fails rather
than silently falling back to a saved profile. Required token/registry/secret
inputs and invalid claims produce exit 2 before network or key I/O. CLI/contract
checks pass in `/tmp/agentlab-ax-precedence-final.log`; claim checks pass in
`/tmp/agentlab-ax-token-claims.log`.

The documentation audit found stale "output-format not applied" and client
backend preference claims. Corrected both references. Testing shipped profile
YAML through the actual loader found a comment-only document and an unsupported
Docker profile; removed those invalid documents. All six documented VM/LXC
profiles now load and validate, `/tmp/agentlab-ax-shipped-profiles-fixed.log`.

The Events audit found refresh failures cleared useful cached sources. Added
separate in-memory message-scope and failure-digest caches, stale source timestamps
and explicit unavailable-empty wording. Changing message scope never reuses
another scope's cache. Eleven executed JS fixtures and real Chrome pass in
`/tmp/agentlab-ax-events-stale.log`.

Full Go 1.25 tests pass in `/tmp/agentlab-ax-semantic-final-all.log`; repository
quality passes in `/tmp/agentlab-ax-semantic-final-quality.log`. The final changed
CLI/contract and dashboard packages pass separately after the last targeted
fixes; vet, generated files and whitespace checks pass.

The evidence above closes AX-05, AX-06, AX-04, UX-06 and IMP-01. Remaining open
items are AX-01/02/09 and QA-01: audit init/bootstrap's semantic argument errors
and shared preflight before host actions, then run final cross-cutting checks and
reconcile the completion matrix. Goal remains active. All changes remain isolated
and uncommitted; no live infrastructure, installed product or release changed.


## Final acceptance audit

Completed against baseline `4614749` in the isolated
`/Users/air/dev/agentlab-ax-ux` worktree, branch `feat/ax-ux-improvements`.
The original scope is retained; no required implementation or safe acceptance
check remains open. Changes are deliberately uncommitted for review.

| Item | Acceptance evidence in the current implementation |
| --- | --- |
| AX-07 | Raw envelope/record filtering retains unknown nested/top-level fields and large integers; default/all/-a, stopped/failed and empty-array fixtures in `commands_sandbox_list_test.go`. |
| UX-01 | Force delegates to the common destroy path. Owned job/session lease cleanup, owner/nonce/rebind isolation, repeated requests and backend-failure fixtures; actual dashboard confirmation identifies workload and uses ordinary cleanup. |
| AX-01 | Registered-type parsing and arity checks; subprocess success/error matrices for global placement, snapshots, session, logs follow, repair and artifacts; shared SSH/modifier parsing preserves literal/quoted argv and repeated flags. |
| AX-02 | Process tests assert one JSON document, exit 2 before requests/host tools for syntax/semantic errors, exit 1 for operational/validation outcomes, printed validation results without a second envelope, help/success 0 and explicit JSON false. Daemon codes and structured hints are preserved. |
| AX-03 | Persisted caller-scoped keys, atomic allocation linkage, payload conflict, lookup/replay authorization, dropped response with 12 retries, restart unknown outcome and a real 31s blocked clone all retain one operation/allocation. CLI and dashboard recover by ID/key. |
| AX-09 | Dry-run uses generated registered flags/operands/required guards and shared semantic rules. Unknown/nested/flag/arity/conflict/type cases reject; all command families and globals are cataloged. Deferred defaults, daemon, terminal and authorization checks are explicit; syntax validation performs no mutations. |
| AX-10 | Capture/stream deadline and cancellation fixtures preserve 124/130 with explicit outcome metadata; ordinary exits remain distinct. |
| AX-08 | Finite both-stream cap fixtures, byte accounting, truncation signals, UTF-8 boundaries and immediate small-stream output; no timing-dependent infinite cap loop. |
| UX-02 | Mixed/unknown/empty JS count fixtures and real Chrome verify canonical summed active totals and separate destroyed history. |
| UX-03 | Preview and explicit VMIDs, stopped preservation, changed/missing/retryable failures and partial JSON exit status; executed UI preview/confirmation/results match API semantics. |
| UX-05 | Unix-socket fixtures exercise every proxy handler with queries, escaped segments, selectors/cursors, idempotency keys, distinct authentication and response metadata. |
| UX-06 | Independent failure digest and scoped messages, accurate coverage labels, empty/error/stale distinctions and timestamped per-source caches; changing scope never reuses another scope's cached messages. |
| UX-07 | Actual Chrome verifies one shared login, expiration, cancel/poll pause, explicit retry and no browser login for daemon denials. Refresh guards and timestamped stale data retain useful results. |
| AX-04 | Real jobs API/CLI collection with filters/cursors; 430-job scope-before-pagination fixture excludes unbound/foreign work and duplicates. Actual metadata, all-status details, related navigation, original result plus recorded-only outcomes; executable artifact journey. |
| AX-05 | Every advertised preference has a consumer; unsupported backend is rejected and removable. Real-process effective-origin matrix covers saved connection/environment/CLI precedence and JSON false without token disclosure; corrupt files and profile/image consumers are tested. |
| AX-06 | Draft 7 definitions, strict request fields, required/default/enum/bounds/cross-field rules and validated examples; compiled references, real response validation and route/permission parity including liveness. Configured surfaces/profiles/backend capabilities and completion/retry/compatibility policies are distinct from static version. |
| DOC-01 | Fresh-user connect/status and full one-job Bash block execute from the docs; success, terminal failure, rejected preflight and observation failure cases. Recovery lines execute with post-ID flags. All six shipped profiles load/validate. Laptop/host setup, source identity, config creation, teardown and generated inventories agree. |
| DOC-02 | Real installer fixtures cover curl-only, wget-only, checksum, download, extraction and install failures; cleanup/status and old-binary preservation pass. Published archive naming and release workflow remain unchanged. |
| UX-04 | Catalog derives from registered parser flags and required guards; AST/runtime parity and generated-file verification. Concise root/group/leaf/alias help and actual Bash/Zsh/Fish completion with clean startup settings pass. |
| IMP-01 | Real Chrome with 101 destroyed records finds the current sandbox immediately; history/search/filters/paging, bounded narrow layout and explanatory empty states. Missing/unavailable permissions do not masquerade as empty collections; JSON timestamps remain lossless. |
| IMP-02 | Bounded job/sandbox/operation waits retain IDs, stage/elapsed and exact resume guidance. Terminal JSON includes recovery commands without losing unknown fields. Lease owner/expiry, workspace links and recorded artifacts support the tested create/wait/inspect/collect/cleanup journey; client cancellation does not claim remote cancellation. |
| QA-01 | All acceptance uses actual CLI/HTTP/DB/JS/browser/shell/installer behavior. Final tests, race checks, required quality tools, schemas, documentation recipes and regeneration pass as recorded below. No live destructive infrastructure tests were used. |

Final semantic audit additionally aligned init/bootstrap port/conflict/backend and
SSH target/user validation before host actions. Sentinel host tools prove invalid
subprocess inputs execute none. Shared target parsing preserves existing
`user@host` and IPv6 syntax. Fixed dry-run help booleans/aliases, flag-looking
literal values and malformed three-dash spellings. Defaults/completion now use
registered parsing; completion enums and preference values share validation.

Final validation on Go 1.25.0 (the repository-supported toolchain):

| Gate | Result / retained log |
| --- | --- |
| Uncached `go test ./... -count=1` with Chrome, Fish and timed-clone gates enabled | Passed, `/tmp/agentlab-ax-completion-all.log` |
| `make quality` (gofmt/vet/Staticcheck/govulncheck/docs lint/links/typos/snippets) | Passed, `/tmp/agentlab-ax-completion-quality.log` |
| Race: API, daemon, DB and dashboard | Passed, `/tmp/agentlab-ax-final-race.log` |
| Race rerun for final API/CLI-contract parser refinements | Passed, `/tmp/agentlab-ax-completion-api-race.log` |
| Six real-installer fixtures | Passed, `/tmp/agentlab-ax-final-installer-six.log` |
| Generated API/CLI catalogs, CLI/llms docs and whitespace | `make docs-verify` and `git diff --check` passed |

Pinned Staticcheck does not understand installed Go 1.27 export data; using the
repository-supported Go 1.25 toolchain is the verified workaround. Chrome uses a
separate headless profile, and Fish was extracted to a temporary directory rather
than installed system-wide. Real Proxmox host provisioning remains the separate
controlled acceptance run explicitly excluded from default safe validation.
No release/tag/push, product installation, or user host configuration change was
performed. Module path, repository identity and release archive contract remain
intact.
