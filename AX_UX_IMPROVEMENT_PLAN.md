# AgentLab AX and UX fixing and improvement plan

Reviewed: 2026-10-15. Baseline: `4614749` (`v0.4.0-17-g4614749`).
AX means agent experience. UX covers human use of the CLI, dashboard,
onboarding, and recovery workflows.

The second review pass is complete. Implementation has not started. This file
is the current implementation plan; the older `IMPROVEMENT_PLAN.md` remains
historical background and contains assumptions that no longer describe the code.

## Current status and next action

- [x] Record the baseline and inspect existing plans and tests.
- [x] Reproduce CLI parsing, error output, defaults, and JSON compatibility issues.
- [x] Verify dashboard contracts against daemon routes and response shapes.
- [x] Review execution, provisioning, retries, and discovery contracts.
- [x] Check installation, documentation, and recovery journeys.
- [x] Finalize priorities, dependencies, acceptance criteria, and validation.
- [ ] Implement and verify the issues below.

Next implementation item: **AX-07**, restore lossless sandbox JSON filtering.
Then address **UX-01**, forced-destroy cleanup, and **AX-01/AX-02**, command and
error parsing. These are bounded changes with reproduced failures.

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
| AX-07 | P1 / S | Preserve all fields in filtered sandbox JSON | Reproduced | Planned |
| UX-01 | P1 / M | Release workspace leases on forced destruction; align dashboard Destroy | Reproduced | Planned |
| AX-01 | P1 / M | Parse flags consistently and reject unused arguments | Reproduced | Planned |
| AX-02 | P1 / M | Make JSON errors and exit codes consistent | Reproduced | Planned |
| AX-03 | P1 / L | Recover creation across timeouts and retries | Source-confirmed | Planned |
| AX-09 | P1 / M | Replace false-positive exec dry-run validation | Reproduced | Planned |
| AX-10 | P2 / S | Report exec timeouts as timeouts | Reproduced | Planned |
| AX-08 | P2 / M | Report captured-output truncation explicitly | Reproduced | Planned |
| UX-02 | P2 / S | Correct dashboard active counts | Reproduced | Planned |
| UX-03 | P2 / S | Align prune wording, targets, and outcomes | Source-confirmed | Planned |
| UX-05 | P2 / S | Preserve dashboard proxy query parameters | Reproduced | Planned |
| UX-06 | P2 / M | Repair Events view and scope selection | Reproduced | Planned |
| UX-07 | P2 / M | Coordinate dashboard authentication and retries | Reproduced | Planned |
| AX-04 | P2 / L | Add complete job discovery and dashboard details | Source-confirmed | Planned |
| AX-05 | P2 / M | Apply and validate CLI preferences | Reproduced | Planned |
| AX-06 | P2 / L | Publish usable schemas and capabilities | Source-confirmed / first-pass CLI check | Planned |
| DOC-01 | P2 / M | Make onboarding and agent recipes executable | Source-confirmed | Planned |
| DOC-02 | P2 / S | Fix installer success/cleanup behavior | Reproduced | Planned |
| QA-01 | P2 / M | Test public contracts and remove timing-dependent cap coverage | Reproduced coverage gap | Planned |
| UX-04 | P3 / M | Generate help and completion from command definitions | Source-confirmed | Planned |
| IMP-01 | P3 / M | Improve inventory density, filtering, and empty states | Proposed improvement | Proposed |
| IMP-02 | P3 / M | Add explicit wait/progress and actionable recovery summaries | Proposed improvement | Proposed |

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
