# Human and agent experience plan

Status: In progress. Date: 2026-10-29.

This plan defines the target experience and the work required to deliver it.
New commands below are proposals, not instructions for the current release.
Use short sentences, consistent terms, and ASCII diagrams in implementation docs.
This writing follows the requested simplified style; formal ASD-STE100 compliance
has not been certified.

## Outcome

A user gives one instruction:

> Set up AgentLab on my server. Connect this computer. Prepare this repository,
> run its tests in a sandbox, and show me the results.

An authorized agent completes the task with the supported AgentLab interface.
It asks only for missing information, credentials, or authority.
The user can inspect the same environment in the dashboard at any time.

Setup is complete only after a real sandbox passes a readiness test.
A task is complete only when its results and remaining resources are accounted for.

## Current foundation and gaps

| Area | Existing foundation | Required work |
| --- | --- | --- |
| Installation | CLI and daemon builds and release archives | Include the dashboard experience in normal installation |
| Server setup | SSH bootstrap and host initialization | Add a repeatable workflow with durable progress |
| Connection | Saved client endpoint and token | Share resolution across CLI, dashboard, and agent tools |
| Dashboard | Separate binary with a Unix socket proxy | Support remote connections and a CLI entry point |
| Agent output | JSON, errors, schema versions, and event streams | Extend contracts without breaking existing consumers |
| Work execution | Jobs, sandboxes, workspaces, sessions, and artifacts | Provide a complete resumable task workflow |
| Delegation | Scoped SSH-signed tokens on the remote API | Improve enrollment, renewal, and revocation |
| Networking | HTTPS and existing Tailscale workflows | Evaluate Tailcat as an optional transport |

The current Unix socket is a trusted full-access path. Remote token scope checks
must remain effective when the dashboard or a new transport sends requests.
Do not route delegated agents through the trusted socket to bypass these checks.

## Product rules

- Keep one source of connection settings for all interfaces.
- Show the selected server before a state-changing action.
- Discover valid settings before asking the user to supply them.
- Reuse existing access and working network connections.
- Verify each setup change before proceeding.
- Make retries safe and progress durable.
- Keep credentials out of chat, command arguments, logs, and artifacts.
- Use the same authorization rules in the CLI, dashboard, and agent tools.
- Return explicit states and corrective actions when work cannot continue.
- Preserve existing workspaces and user changes during repair or cleanup.

## Human workflow

### Install and connect

A normal client installation provides both the CLI and dashboard assets.
Prefer an embedded dashboard launched by the CLI. Retain the separate dashboard
binary as a compatibility entry point if required.

For an existing server, use the proposed flow:

```text
agentlab connect --endpoint https://server.example
agentlab dashboard
```

The connect command requests a credential through a secure input method.
It checks server identity, authentication, and compatibility before saving.
A failed attempt leaves the working connection intact.

For a new server, use the proposed flow:

```text
agentlab setup --host server.example --repo .
```

Setup uses existing SSH access. It checks the host architecture, supported
backend, storage, network, permissions, and available capacity.
It builds on bootstrap and initialization instead of duplicating their logic.
It uses artifacts for the server platform, even when the client uses macOS.

```text
Install client
      |
      v
Inspect server ---- Missing access ---> Request secure authentication
      |
      v
Calculate changes
      |
      v
Apply changes within the user's authority
      |
      v
Save and verify connection
      |
      v
Prepare template and test sandbox
      |
      v
Open dashboard and begin work
```

Installing software requires initial server access. Tailcat cannot provide that
access before a process is installed and started on the server.

### Use the dashboard

The dashboard reads the same connection as the CLI. It checks the connection
before opening the browser. It shows the server, identity, and permissions.

The local listener binds to loopback by default. Browser enrollment uses a
short-lived, single-use exchange and a session cookie. Do not place the daemon
credential in browser storage or URLs. Validate request origin and protect
state-changing requests against cross-site request forgery.

The interface must provide:

- A guided first sandbox flow with valid profiles and capacity checks.
- Clear empty, loading, disconnected, failed, and ready states.
- One connection error with a corrective action, rather than repeated raw errors.
- Progress and cancellation for long operations.
- Resource-specific confirmation for destructive actions.
- Keyboard access, visible focus, clear labels, and usable narrow layouts.
- A result view with logs, test results, artifacts, and remaining resources.

Stopping the dashboard stops only the local service. Server jobs continue.
Restarting the dashboard restores access to the same server state.

### Switch servers

Add named contexts. A context stores the server identity, transport, credential
reference, and defaults. Keep secrets in the credential store.

```text
agentlab context use development
agentlab dashboard
agentlab --context production status
```

An operation binds to a context and server identity when created. Changing the
active context must not redirect an operation that is already running.
Concurrent agents should pass an explicit context instead of changing global state.

## Agent workflow

Ship a maintained agent skill with the client. Keep operational logic in AgentLab.
The skill teaches discovery, setup, execution, recovery, and cleanup.
An optional Model Context Protocol (MCP) server can expose the same operations.
It must not implement a separate authorization or execution model.

```text
User instruction
      |
      v
Discover capabilities and selected server
      |
      v
Inspect state and validate requested work
      |
      v
Run or resume setup
      |
      v
Create job OR standalone sandbox
      |
      v
Execute task and observe progress
      |
      v
Collect changes, tests, and artifacts
      |
      v
Clean up temporary resources or record retained leases
      |
      v
Report verified results to the user
```

Choose one execution path. A job already creates its own sandbox. Do not create
an additional sandbox for that job. Standalone interactive work is a separate path.

For a local repository, define how content reaches the server. Preserve local
uncommitted changes. Identify the source revision and transferred changes.
Exclude secrets and ignored files by default. Use explicit credential references
for private repository access. Return changes as a reviewable patch or branch.
Do not push repository changes unless the user authorized that action.

Non-interactive commands must never wait for hidden input. Return a structured
blocked state with the missing requirement. Separate missing credentials from
missing authorization and invalid configuration. Resume after the requirement is met.

## Shared architecture

```text
CLI             Local dashboard           Agent skill / MCP
 |                    |                         |
 +--------------------+-------------------------+
                      |
          Shared connection and API client
                      |
          +-----------+------------+
          |           |            |
       HTTPS      Unix socket    Tailcat adapter
          |        local trust    optional
          |           |            |
          +-----------+------------+
                      |
             Daemon API and policy
                      |
         Operations, jobs, and resource state
```

Extract connection resolution from CLI-specific code into a reusable package.
Keep the current precedence: explicit options, environment, then saved settings.
Define endpoint-versus-socket conflicts explicitly and test them.
Add contexts without silently selecting another server.

Reuse the authenticated remote API for Tailcat. The Unix socket remains a separate
local trust boundary. A tunnel does not grant API permissions.

## Durable operations and machine contracts

Extend the existing JSON contract. Do not replace raw success payloads or the
current error envelope without a versioned migration.
Preserve documented exit codes, validation failures, and stream formats.

Add capability discovery for client/server compatibility and optional features.
Expose stable error codes, retry guidance, resource IDs, and corrective actions.
Keep diagnostic text out of machine output streams.

Define durable operations for setup and other long workflows. Reuse job and event
storage where practical. Avoid two competing records for the same job state.

Each operation records:

- Operation ID, owner, context, and verified server identity.
- Request identity and normalized input hash.
- Current step, completed checks, and resource IDs.
- Progress timestamps and resumable event cursor.
- Error code, retry policy, and required user action.
- Final result, cleanup status, and retained resource expiry.

Use explicit states such as planned, running, blocked, succeeded, failed, and
cancelled. A wait timeout does not mean the server operation failed.

```text
Submit request ---> Save operation ---> Perform work ---> Save result
      |                                      |
      +-- Client disconnects                 +-- Work continues
                 |
                 v
         Reconnect with operation ID
                 |
                 v
         Observe or resume safely
```

Deduplicate create requests with an idempotency key scoped to the caller and server.
The same key and inputs return the same operation. Different inputs with that key
return a conflict. Specify retention and behavior after key expiry.

After a crash, inspect external resource state before repeating a side effect.
Use locking for concurrent setup attempts. Cancellation must report whether work
stopped, remains active, or requires cleanup. Never claim rollback without verification.

## Credentials and authorization

Use the operating system credential store where available. Provide a documented
protected-file fallback for unattended systems. Store credential references in contexts.

Separate installation authority from routine task authority. Mint narrow task
credentials with an expiry. Check authorization on every remote API route.
Add an explicit revocation design; current token expiry alone is not immediate
per-token revocation. Bind audit records to the acting identity and operation.

A request to set up a server authorizes necessary setup within that scope.
Ask for additional authority before deleting existing data, purchasing resources,
or exposing services beyond the requested audience. Reuse prior authorization.

## Tailcat decision and experiment

Recommendation: keep HTTPS as the default for working installations. Offer Tailcat
when it removes a network setup barrier. Do not install it for all users by default.

Tailcat provides encrypted connections, port forwarding, and a Go library. It can
use direct connections or relay fallback. Its default addresses are temporary;
saved keys support reuse. Treat connection material as sensitive.

Source: [Tailcat repository and documentation](https://github.com/tailscale/tailcat).
These observations were reviewed on 2026-10-29. Recheck the selected version before implementation.

### Initial integration

- Install a pinned helper only when the user selects this transport.
- Verify artifact integrity, platform support, license notices, and upgrade behavior.
- Have AgentLab own helper startup, shutdown, health checks, and recovery.
- Pair peers through an authenticated channel with a limited enrollment period.
- Restrict the tunnel to the authenticated AgentLab API.
- Persist required identity material securely and define rotation and revocation.
- Return connection state in JSON, including direct or relayed transport when available.
- Keep server and client setup reproducible without parsing human terminal messages.

Do not expose an unrestricted shell or the trusted daemon socket as a shortcut.
Do not change transports silently after an authentication or identity failure.

### Decision gate

Compare the helper against existing HTTPS/Tailscale and SSH forwarding for:

- First connection time and required user actions.
- Direct and relay reliability, throughput, and reconnect behavior.
- Operation during process restart and network changes.
- Credential expiry, revocation, and peer identity checks.
- macOS and Linux support on both supported architectures.
- Binary size, dependency maintenance, and release reproducibility.
- Relay rate limits, availability, and the cost of operating private relays.

Adopt only if it measurably improves setup without weakening authorization.
Evaluate embedding the Go library after this gate. Keep a helper prototype until
library compatibility and lifecycle behavior are understood.

## Delivery phases

### Phase 1: Make the existing connection work everywhere

- Extract the shared connection resolver and API client.
- Add remote endpoint support to the dashboard.
- Add the proposed `agentlab dashboard` entry point and browser enrollment.
- Include dashboard assets in normal builds and installations.
- Add connection diagnostics with one actionable error.

Acceptance: after a successful connect, CLI and dashboard reach the same server.
No manual tunnel or second daemon token is required. Scoped access stays scoped.
A build from source includes the dashboard experience.

### Phase 2: Make setup repeatable

- Compose bootstrap, initialization, connection, and template readiness checks.
- Add persisted setup operations, safe retries, and concurrent-run protection.
- Support secure interactive input and explicit non-interactive blocked results.
- Add named contexts and migration from the existing client configuration.
- Test a sandbox and clean up the test resources before reporting setup success.

Acceptance: interruption after each setup step can be recovered without duplicate
services, unintended token rotation, lost settings, or leaked test resources.
Setup on an unsupported host stops before mutation with a clear reason.

### Phase 3: Complete the agent task contract

- Add capabilities and compatible structured recovery metadata.
- Add idempotent submission, operation observation, and cancellation semantics.
- Complete repository transfer, results, artifacts, and resource accounting.
- Ship the agent skill; add MCP only where it improves supported client integration.
- Add credential enrollment, renewal, and revocation coverage.

Acceptance: an agent completes a repository task from one user instruction.
It resumes after client termination, returns verified results, and accounts for
all created resources. Missing access produces one clear request to the user.

### Phase 4: Test optional Tailcat transport

- Build the helper integration behind an explicit feature option.
- Run the decision-gate comparison and publish the measured results.
- Decide whether to retain the helper, embed the library, or defer the feature.

Acceptance: approved peers can reconnect after restarts. Unauthorized peers cannot
use the API. Relay failure produces a clear result. Existing HTTPS users are unaffected.

### Phase 5: Validate the complete experience

- Run fresh-install trials with humans and supported coding agents.
- Review accessibility, error messages, first-run guidance, and task results.
- Test upgrades and mixed client/server versions.
- Update tutorials, generated command references, and troubleshooting pages.

Acceptance: a new user can follow one installation path through their first task.
The supported agent integration can complete the same path using documented interfaces.

## Verification and success measures

Use disposable hosts and repositories for end-to-end tests. Never use live user
sandboxes for destructive acceptance tests.

| Scenario | Required result |
| --- | --- |
| Existing remote connection | CLI and dashboard identify the same server |
| Clean supported host | Setup reaches a tested sandbox |
| Missing SSH access | Secure authentication request with no secret output |
| Invalid daemon token | Clear authentication error; no transport downgrade |
| Lost response after create | Retry returns the original operation |
| Restart during provisioning | Reconcile actual state and resume safely |
| Two agents use different contexts | Each remains bound to its intended server |
| Concurrent setup | One mutation owner; no duplicate installation |
| Scoped credential | Forbidden actions fail through every remote transport |
| Expired or revoked credential | Access stops according to the documented policy |
| Local repository has changes | Changes survive transfer and task completion |
| Job-managed sandbox | No extra standalone sandbox is allocated |
| Cancel or lease expiry | Resource outcome is explicit and verifiable |
| Old client or daemon | Supported behavior or actionable compatibility failure |
| Tailcat relay unavailable | Bounded failure with useful diagnostics |
| Offline installation | Existing offline path works without helper downloads |

Establish a baseline before setting final performance targets. Track:

- Time to first working connection and first tested sandbox, separately.
- Manual steps and credential interactions.
- Setup completion and successful recovery rates.
- Task completion after an agent process restart.
- Duplicate resources and temporary resources left after cleanup.
- Time to identify and correct a connection error.

Initial release gates: zero duplicate creates in retry tests, zero leaked temporary
resources in cleanup tests, and zero credential exposure in captured outputs.
Run at least ten fresh-install trials for each supported primary workflow. Target
at least nine successful completions without maintainer intervention. Record template
download and build time separately so it does not hide connection regressions.
Collect product telemetry only through an explicit, documented user choice.

## Compatibility and release work

Migrate the existing client configuration into a default context without losing
its endpoint or credential. Provide recovery if migration fails.
Keep existing scripts working while new structured contracts are introduced.
Advertise optional features through capabilities before clients use them.

Preserve the release archive name contract and Go module path. Update GoReleaser,
installer checks, and documentation together if package contents change.
Use the normal automated release pipeline. Validate all supported targets with
release checks and snapshot builds before tagging a release.

## Open implementation decisions

Resolve these through prototypes before committing to public contracts:

- Whether the standalone dashboard binary remains a supported release artifact.
- The unattended credential store and credential renewal mechanism.
- Revocation record handling during database restore and server migration.
- Operation storage ownership before the daemon is installed.
- Repository transfer limits and patch delivery behavior.
- Minimum compatible client/server versions and capability negotiation.
- Tailcat version, helper supervision, relay policy, and library stability.

## Related documentation

- [Bootstrap over SSH](../how-to/bootstrap-proxmox-host-over-ssh.md)
- [Run the dashboard](../how-to/run-the-dashboard.md)
- [Drive AgentLab as a coding agent](../how-to/drive-agentlab-as-a-coding-agent.md)
- [Agent JSON output](../reference/agent-json-output.md)
- [Scoped agent tokens](../how-to/mint-scoped-tokens-for-an-agent.md)
- [Control plane trust boundaries](../explanation/control-plane-and-trust-boundaries.md)

## Implementation record

The first implementation adds a shared transport and endpoint policy package.
The CLI embeds the dashboard and exposes `agentlab dashboard`. The compatibility
binary accepts remote endpoints and reads existing connection settings.
Remote dashboard calls preserve scoped daemon authorization. Redirects cannot
change the selected server. Browser enrollment uses a one-use code with a
five-minute limit and an HttpOnly session cookie. Tests cover enrollment,
expiry, cross-origin rejection, remote scope responses, and redirect handling.

Remaining phase 1 work includes automatic browser launch, unified configuration
resolution, connection identity and permissions in the interface, and a complete
connection error view. Phases 2 through 5 are not complete. No Tailcat transport
or fresh-host acceptance result is claimed by this implementation.

The next increment adds named contexts, explicit context selection, and a
protected-file credential store. Context writes use a process lock and atomic
replacement. Existing settings migrate to a default context when context
management starts. The original file remains unchanged for recovery. Clients
retain resolved settings when the active context changes. Tests cover concurrent
writes, selection, credential permissions, and migration.

Connect now supports bounded `--token-stdin` input and terminal input with echo
disabled. Non-interactive missing credentials return a structured blocked state.
Changing an endpoint does not reuse a credential inherited from another server.
The compatibility dashboard reads the active context. Context identity checks,
operating system credential storage, and durable setup remain pending.

The setup foundation now inspects the SSH host before it uploads assets or
changes the server. It checks Linux, architecture, machine identity, installation
permissions, Proxmox tools, and free template storage. Artifact selection uses the
server architecture. Uploads reject executables with the wrong ELF architecture.
The host installer selects the same architecture and stops printing daemon tokens.
Readiness, memory, network, and backend capacity checks still need integration
with the complete setup workflow.

An internal `operations` package prototypes durable state before installation.
The client owns these records until daemon-backed workflows take over. Records
contain an owner, context, verified server identity, input hash, state, events,
resource IDs, cleanup status, and corrective action. They do not store raw inputs
or arbitrary diagnostic output. Writes use atomic replacement and filesystem sync.
An execution lock permits one runner per operation. Observation does not wait
for that lock. Each retry checks external state before repeating an action.
Cancellation is cooperative and does not claim rollback.

Request keys are scoped to the owner and verified server identity. Equal inputs
return the original record. Different inputs return a conflict. Keys expire after
30 days. Expired records remain inspectable, and their keys cannot silently create
new resources. Automatic record removal is not implemented. Tests cover a lost
response after resource creation, restart reconciliation, concurrent execution,
observation, cancellation, blocked access, and expired keys. The public setup command now connects to this prototype, as described below.

The setup command now composes bootstrap, initialization, API identity verification,
a real sandbox health check, a guest command, and owned-resource cleanup. It stores
the planned VM ID before creation and reconciles it after a lost response. An SSH
channel holds a server lock while a connected runner applies changes. Loss of
that channel cancels the local runner. Durable detached server execution remains
pending. `operation show` observes atomic records. `operation cancel` reports
cooperative cancellation without claiming that the current step has stopped.

The authenticated host response now includes an AgentLab server identity derived
from the machine identity. It does not expose the raw machine ID. Setup checks
that the SSH server and API server match before saving connection settings or
mutating test sandboxes. A private context binds the operation connection. The
record accounts for that retained context, test sandboxes, and temporary assets.
Tests cover the server lock, a lost create response, owned-sandbox cleanup,
protection of unrelated sandboxes, and API identity mismatch. Fresh-host trials,
network and memory acceptance checks, repository transfer, and detached execution
remain incomplete. Setup uses packaged installation assets. Development clients require matching
Linux binaries; released clients retrieve their pinned server artifacts.

The client now embeds the host installation scripts and the existing agent skill.
Setup and initialization use these assets through a content-addressed cache by
default. An explicit assets path supports source development. The cache rejects
changed content, symlinks, and unexpected files. Tests cover use outside a checkout,
concurrent extraction, and tampering. Released clients retrieve verified server binaries for their pinned version.
Development clients use explicit Linux binaries.

The networking service now uses a persistent managed helper instead of a path
inside the temporary setup bundle. Tests install the files in an isolated
filesystem, remove the bundle, and verify that the helper remains usable. A unit
with only the old generated temporary path can migrate without a force option.
Other file differences remain protected.

The daemon and CLI now expose capability discovery. It reports protocol and
schema versions, implemented feature flags, server identity, and caller command
permissions and scope. The route requires the existing schema permission and
preserves scoped authorization. Tests cover zero-permission denial, the route
authorization matrix, scoped discovery, unknown versions, and errors without
transport fallback. The dashboard proxy exposes the same authenticated route.

Released clients now retrieve the CLI and daemon archives for the inspected Linux
server architecture. They use the client release version instead of `latest`.
The downloader checks archive names and SHA-256 checksums, rejects unsafe paths
and non-regular entries, and limits download and extraction sizes. An atomic cache
contains both binaries and their hashes. Verified cached binaries work offline.
Concurrent callers share a request lock. Retry removes incomplete downloads for
that request. Explicit sources retain their original failure behavior.

Tests cover pinned archive names, both binaries, checksum failures, unsafe paths,
symlinks, duplicate checksums, cache changes, offline use, concurrent downloads,
and cancellation while waiting for a download lock. The full suite also exposed
a timing assumption in an existing cleanup test. Its fixture now triggers the
timeout at guest configuration after clone and cancels the parent context.
Repeated tests verify cleanup without depending on a 25 ms database setup budget.
The production cleanup logic is unchanged.

Job creation now supports an optional `Idempotency-Key` header and the CLI
`--request-id` option. A durable request record reserves a job ID before lease
mutation. File locks serialize the same caller key across daemon processes.
The request record contains hashes, the reserved job ID, an expiry, and an immutable
workspace binding. It does not duplicate job execution state. A database trigger
records that the original job was inserted, so a removed job cannot be recreated
by retrying its request key. Request tombstones remain after the 30-day expiry.

Replay returns the current original job with HTTP 200. Initial creation keeps
HTTP 201 and the existing raw payload. The route authorizes both the submitted
target and the original job's current sandbox. Caller identities have separate key
spaces. A pending request can recover its existing job-owned workspace lease.
Errors add an action and a Boolean retry flag without replacing the existing
envelope. Tests cover concurrent submissions, handler reconstruction, conflicts,
expiry, removed jobs, caller isolation, scope checks, workspace binding, and a
lost response after lease acquisition.

The CLI checks capabilities before using request keys or creating dependent
resources. Stateless jobs and existing workspaces are supported. Keyed workspace
creation and branch session creation stop before mutation and remain advertised
as unsupported. Complete server provisioning recovery, full repository transfer,
and dependent resource creation remain pending. Job cancellation is implemented
as described below.

Job cancellation now has a persisted request and a CLI entry point. It records
actor identities, sandbox scope, pinned resource bindings, progress, corrective
actions, and resource outcomes. The worker shares an execution lock with job
provisioning. Provisioning observes the saved cancellation request and stops.
Cancellation waits for provisioning to exit before verifying backend state.
Daemon startup resumes unfinished requests. The dashboard proxy uses the same
remote API route and authorization rules.

The worker removes owned temporary VM sandboxes and verifies their absence.
Keepalive VM sandboxes remain stopped with explicit retention details. Workspace
volumes remain intact. Changed targets, changed backend identity, insufficient
scope, shared session jobs, container jobs, and uncertain cleanup produce blocked
results. A new request for an old failed job requires inspection instead of acting
on a sandbox that can contain later work. An observation timeout does not imply
server failure. Late guest reports cannot replace a verified cancellation result.
The legacy job status remains FAILED, with explicit cancellation metadata in its
result and an optional cancellation object on observation.

Tests cover workspace preservation, temporary cleanup, retained sandboxes,
authorization, saved scope, repeated requests, recovery dispatch, late reports,
cancellation during provisioning, duplicate-sandbox prevention, and observation
timeouts. Two remaining cleanup fixtures now use explicit backend checkpoints
instead of short deadlines that include database setup. Fresh-host cancellation
and process-kill trials remain unverified. Shared session and container job
cancellation remain unsupported and explicit in recovery messages.

Local repository preparation now provides `repo snapshot`. It reads the effective
working tree without changing source files, the index, or the current branch.
It builds a sanitized base and snapshot in a private repository. The bundle omits
original history. The manifest identifies the source revision, transferred local
changes, file hashes, executable modes, bundle checksum, and additional exclusions.
Git ignore rules also apply to tracked files. Known credential paths and content
patterns receive additional filtering. These patterns cannot identify every secret.

Snapshots reject unsupported submodules, source changes during capture, unsafe
symlinks, oversized content, and modified cache entries. Source hooks, file
monitoring, content filters, and lazy network fetches are disabled. Tests cover
staged and unstaged changes, deletion, untracked files, source preservation,
ignored tracked files, credentials, history removal, unusual paths, symlinks,
executable modes, deterministic bundles, exclusion metadata, and offline context
recovery. Server upload, authenticated guest delivery, and returned patches now
extend these local snapshots. Complete task resource accounting remains pending.

Repository upload now has authenticated create, observation, and removal routes,
plus `repo upload`, `repo show`, and `repo remove`. Immutable source records bind
the manifest and bundle to the acting identity and server. Concurrent retries
return the same record and expiry. Records expire for new submissions after
30 days. Expiry does not remove bytes or interrupt admitted work. Removal requires
no queued or running job references and verifies file cleanup before metadata
removal. Retrying an interrupted upload removes its abandoned temporary files.
Automatic removal of abandoned or expired uploads remains pending.

The upload validator checks hashes, manifest contents, object limits, synthetic
commit metadata, and the two-commit history. It rejects extra history, extra refs,
unsafe symlinks, recognized credentials, delta packs, tags, and unbounded
inflation. It bounds decompression before Git imports the pack. The host never
checks out the uploaded repository or runs its code.

Snapshot jobs now pin uploaded source before creating a job. The source lock
prevents concurrent removal during admission. Identity, expiry, revision, backend,
and content checks run before provisioning. Idempotent replay still observes the
original job after source removal. Snapshot work is limited to stateless VM jobs.
Workspace and branch session requests stop before mutation. Guest bootstrap
contains the pinned content identity. Download requires the sandbox secret,
agent subnet, and the current active job binding. Cancelled and terminal jobs
cannot obtain source bytes through this route.

The guest runner checks the bundle size, hash, and revision. It uses a fresh
private repository without inherited Git configuration, hooks, or network fetches.
Existing guest worktrees remain unchanged. Job results identify the source and
repository path. Tests run the real guest runner against authenticated delivery,
execute a task on local source changes, and verify existing-worktree preservation
and secret-free captured output. Other tests cover owner isolation, scope denial,
expiry, concurrent upload, retry after handler reconstruction, corrupt content,
abandoned upload recovery, source removal, active-job protection, and pinned
bootstrap metadata. Updated VM templates are required. Fresh-host delivery and
mixed-template trials remain unverified. Full task accounting and structured test
evidence remain pending, so `repository_transfer` stays false. The narrower
`repository_upload` and `repository_snapshot_jobs` capabilities expose this increment.

Task result export now shares the snapshot implementation. It reads against the
initial task revision, including committed and uncommitted task changes. It
creates a binary-capable patch, sanitized bundle, and matching manifest. Exclusions
apply to both patch trees, so excluded content does not become a source deletion.
Tests apply returned text and binary changes to the initial worktree and verify
that source commits and local files remain unchanged.

The trusted template recipe installs the local exporter from a pinned copy of
the running daemon binary. Its local command starts no daemon and reads no
connection credential. The template capability manifest advertises result export.
The guest uploads results through immutable artifact paths. Identical retries
reuse the original artifact record; changed bytes receive a conflict.

The daemon checks recorded artifact hashes, source revision, bundle policy, and
the derived patch hash before sandbox cleanup. Missing or corrupt results fail
the job and retain the sandbox with explicit lease and recovery details. Cleanup
checks backend identity, other active jobs, and verified VM absence. Keepalive
jobs record retained resources. A durable pending result remains explicit if
resource finalization does not finish. Saved pending cleanup now resumes after
server restart with repeated evidence and identity checks.

The new `job result` command downloads verified files into a private directory.
It checks hashes, manifest identity, bundle contents, and cached results. It uses
atomic publication, per-job locks, and recovery of abandoned staging files.
It returns the patch, execution archive, receipt, job status, and resource list.
It does not apply or push repository changes. The HTTP client now keeps response
contexts active until streaming bodies close. A regression test verifies delayed
body delivery. The existing request timeout remains active during the read.

Tests run the actual guest runner and exporter against authenticated delivery,
artifact upload, result verification, and simulated VM cleanup. Other tests cover
immutable uploads, changed artifacts, retained work, changed backend identity,
keepalive resources, empty patches, client collection, and cache tampering.
Execution archives receive content and report checks, as described below.
Structured test evidence and full task accounting remain incomplete. The narrower
`repository_results` capability advertises collection. `repository_transfer`
remains false. No fresh-host result or complete acceptance gate is claimed.

Result verification now compares the returned base file hashes and modes with
the original uploaded source manifest. A correct revision label alone does not
prove that relationship. Explicit exclusions can omit a file from both trees;
they cannot substitute different original content. Tests reject a structurally
valid forged bundle with the correct source label. The result receipt also
retains the upload ID and original source revision after source removal.

A local benchmark covers result verification at 10, 1,000, and 10,000 files.
The initial 10,000-file run takes 22.502 seconds on the test machine, exceeding
the guest report timeout. Batched object reads reduce that measured case to
0.333 seconds. Tests check binary record boundaries, exact object sizes, full
object IDs, and replacement references. These local measurements use generated
small files. They do not establish complete workflow targets or fresh-host results.

Snapshot result cleanup now uses the daemon lifecycle context. Closing the guest
report connection during VM shutdown does not cancel cleanup or its saved outcome.
A test cancels that reporting context during destruction and verifies the final
receipt. A separate test verifies cancellation cannot replace a result while its
cleanup is being finalized. The Linux child cancellation fixture now accepts the
kernel's no-such-process result when a child exits during its `/proc` read.
Repeated race tests cover that observation without changing production process
termination behavior.

Execution archive validation now checks bounded gzip and tar contents without
extraction. It rejects unsafe paths, links, special files, executable files,
duplicate paths, conflicting parents, unsupported metadata, extra gzip streams,
and invalid checksums. Expanded content is limited to 64 MiB. Each file is
limited to 8 MiB. A report and runner log are required.

The report identifies the admitted job, uploaded source, initial revision, and
result manifest. Its terminal status, exit code, and duration must agree with
the terminal report. Missing fields, duplicate JSON keys, ambiguous case variants,
and conflicting status fields fail collection and retain the sandbox.
The receipt records guest-reported execution and explicitly unreported tests.
The client repeats archive validation before publishing its local result directory.
These checks do not prove task assertions. Structured test evidence remains
pending. The shared credential checks below also apply to artifact content.

The guest now validates execution evidence before uploading any result artifact.
It shares the recognized credential policy with repository snapshots. It also
checks exact bootstrap credentials and supplied environment values through a
private file. Credentials do not enter validator command arguments or errors.
The daemon and client repeat the recognized credential checks. A real guest
runner test puts an opaque runtime credential in a binary artifact. It verifies
that no result artifact uploads and the sandbox remains available.
Unknown or encoded secrets still require review. Structured test evidence remains
pending. The packaged helper and template preflight now verify archive support.

Result export also checks known runtime credential values in source content.
It excludes matching text and binary files from both patch trees. This preserves
the original worktree and avoids turning exclusions into deletions. Credentials
in file names block export, including names that would appear as exclusions.
Tests cover multiline values, binary files, unchanged source files, and credential
names that must not appear in published metadata. The normal connect help now
shows secure input, and the command reference includes context selection.

Verification on 2026-10-29 passes: the full Go test suite, `go vet ./...`,
documentation lint, documentation snippets, and whitespace checks. GoReleaser
configuration validation and snapshot builds pass for all six release targets.
The package smoke check verifies native result export, execution validation,
and dashboard enrollment against a local authenticated fixture. No release is
published. Real hosts, fresh installations, structured test evidence, restart
reconciliation, and the remaining delivery phases are still incomplete.

Pending result cleanup now survives a daemon restart. The first terminal result
pins the server identity, sandbox creation time, VM generation, and guest
incarnation. Recovery shares report and execution locks with normal finalization.
It repeats source, artifact, bundle, patch, and execution checks. Changed or missing
identities block deletion. Existing sandbox keepalive settings remain effective.
An already absent VM requires local record cleanup without another backend destroy.
Source removal protects pending cleanup and verified results with blocked cleanup.

The additive `job cleanup JOB_ID` command saves a cleanup retry and returns the
observation command. Its API requires `job.cleanup` and the sandbox scope.
Retries preserve the original target and do not restart task execution. Completed
and retained outcomes are idempotent. Old results without pinned identity require
inspection. Recovery before a verified terminal result is saved remains pending.
Tests cover restart, keepalive, concurrent recovery, generation changes with the
same VM ID and name, changed server and guest identity, changed artifacts,
interruption after backend destruction, source protection, permission denial,
scope denial, and capability negotiation.

Cleanup recovery verification passes on 2026-10-29: the full Go suite,
`go vet ./...`, focused race tests, generated command documentation, Markdown
lint, documentation snippets, and whitespace checks. The six-target snapshot
build and native package smoke check also pass. Tests use simulated backends
and local services. No live Proxmox acceptance result or release publication
is claimed.

The maintained agent skill now covers connection discovery, setup, job submission,
local repository transfer, standalone work, cancellation, result evidence, cleanup,
and retained resources. It uses capabilities and explicit contexts. It preserves
existing authorization and request keys. The guides distinguish guest execution
from test proof and identify unsupported recovery paths. Unsafe credential examples
and unconditional approval requirements were removed.

The client now exposes `skill show` and `skill export`. The local commands do not
load connection credentials or require a checkout. Export publishes the complete
bundle atomically, verifies retries, and preserves modified instructions. The
normal client installer points to the export command. Command snippets and Markdown
checks now include the skill guides. Tests cover metadata, local references,
complete export, concurrent publication, cancelled export, symlinks, extra files,
modified instructions, offline configuration, and syntax validation without writes.
Independent agent behavior and fresh-user trials remain unverified.

CLI and compatibility dashboard configuration now share `AGENTLAB_CONFIG_DIR`.
The explicit directory must be absolute. This supports isolated client state
without relying on platform-specific home configuration. Package smoke checks
use it to avoid reading user connection settings on either supported operating
system. Local skill inspection and export remain independent of that directory.

The packaged skill examples also run through the real CLI grammar validator.
The check tokenizes examples without a shell, omits protected stdin redirection,
and verifies that no daemon request occurs. This catches unsupported flags in
addition to the documentation command-prefix checks. It does not establish
independent agent behavior or successful live execution.

Skill delivery verification passes on 2026-10-29: the full Go suite, focused race
tests, `go vet ./...`, skill frontmatter validation, Markdown checks, command
snippets, and real CLI grammar checks. The six-target release snapshot and native
package smoke check pass. The smoke check verifies exported skill bytes and
preservation of modified instructions. No publication, independent agent trial,
or live-host acceptance result is claimed.

SSH token revocation now has durable storage and a global management API.
Migration 27 stores the issuer, revocation digest, server identity, first actor,
and timestamp. The digest binds the signed issuer and token ID. The store keeps
neither the bearer credential nor its raw signed ID. Identical retries preserve
the audit record. Tombstones remain after expiry and across restarts.

The network middleware checks current revocation state for each SSH-token request.
A revoked token is rejected without a legacy-token or transport fallback. Storage
failure rejects the request. Expiry now rejects the exact Unix expiry second.
Structured errors preserve the old error field and add stable codes and actions.
Already admitted requests may finish; revocation does not claim job cancellation.
The dashboard preserves remote authentication failures.

The CLI adds `token revoke`, `token status`, and protected stdin for inspection.
Inspection returns a revocation identifier without printing the credential and
explicitly labels its signature as unverified. Mutation checks the known context
identity at its endpoint and sends the expected server identity. The API requires
global `token.revoke` authority; observation requires global `token.read`.
Sandbox-scoped credentials cannot manage global credential state.

Tests cover issuer and nonce separation, independent tokens under one signing key,
restart persistence, immutable audit metadata, concurrent requests, exact expiry,
permissions, scope, wrong-server preconditions, missing capabilities, protected
input, failed status storage, proxy behavior, and already admitted requests.
Legacy static credentials and SSH tokens without a signed ID still need key or
credential retirement. Renewal, secure enrollment, operating system credential
storage, and live revocation trials remain incomplete.

The native package smoke check now creates an ephemeral SSH signing key outside
the checkout. It compares creation and protected-stdin inspection metadata.
Inspection returns matching revocation identifiers and no bearer credential.
The key is not enrolled on a server. This checks packaged CLI behavior only.

SSH token revocation verification passes on 2026-10-30: the full Go suite,
focused race checks, `go vet ./...`, generated documentation, Markdown checks,
and command snippets. Release configuration, six-target snapshot builds, and
native package smoke checks also pass. The authorization coverage matrix includes
both management routes. Tests use local services and simulated backends.
Live-host acceptance, enrollment, renewal, and the other open phases remain.

The legacy host installer now preserves existing agent instructions. It compares
complete bundle trees, including extra files and entry types. An identical retry
reuses the active bundle. A different active bundle remains selected while the
new bundle is staged outside the skill loader directory. The pending path uses
a content digest, so retries reuse one update. Changed pending files stop the
installation and remain unchanged. The deprecated force option cannot overwrite
instructions. Active bundle metadata changes only when the active files match.

Skill publication uses a bounded installation lock and a complete staging tree.
It refuses source symlinks and unsupported entries, unsafe lock files, and
symlinked pending directories. Publication does not replace an entry created
concurrently. Failed file copies remove their staging tree. Existing loader
permissions and files outside installed symlinks remain unchanged. Tests run
the actual installer functions in temporary directories without host mutation.
Power-loss durability and live-host upgrades remain unverified.

Host skill preservation verification passes on 2026-10-30: the full Go suite,
focused race tests, `go vet ./...`, Bash syntax, generated documentation,
Markdown lint, command snippets, release configuration, six-target snapshot
builds, and native package smoke checks. Installer tests use isolated temporary
directories. No live-host upgrade or independent agent loader trial is claimed.

Linux clients now support the operating system Secret Service for new connection
credentials. Auto selection uses an existing unlocked service on a local Unix
session bus. Missing session or service uses the documented protected-file
fallback. Locked storage and service errors block without a downgrade. Explicit
os mode requires native storage; file mode supports unattended clients. Neither
adapter places credentials in process arguments. Native access does not start
services, create collections, unlock records, or open prompts.

Connect and bootstrap now save credential references. Connect reports the context,
storage type, and metadata path. It repairs the active context without requiring
its old credential to load. Existing legacy files remain recovery copies. Native
references use context format version 2; old file references remain readable.
Existing references always use their recorded store, irrespective of new defaults.
The credential record is verified before metadata publication. Context locks and
native requests respect a five-second deadline. File reads reject unsafe entry
types, public permissions, and oversized records. Damaged context metadata cannot
silently select local trust.

The context credentials command moves an available credential between stores
without changing its server or active selection. Replacement and removal record
credential retirement before deletion. Failed cleanup reports that metadata is
saved and remains retryable through context credentials-cleanup. Cleanup refuses
active references. Tests cover failed writes, failed verification, missing native
records, preservation of the working connection, cleanup after restart, prompt
refusal, service ownership, configuration scope, and blocked authentication.

A disposable GNOME Keyring trial passes on a private bus with private data and
runtime directories. A record larger than the external helper's input limit
survives a keyring restart. Locked collection access is rejected, and cleanup
succeeds after restoration. This is local credential-service evidence only.
macOS Keychain, pre-commit orphan reconciliation, enrollment, renewal, and live
human or agent acceptance trials remain open.

The native package smoke check also covers the default connection context,
protected-file storage, authenticated status, credential replacement, cleanup,
and disconnect. An optional disposable keyring trial runs the packaged client
against the real isolated service. Protected stdin selects native storage;
status resolves the same credential after a keyring restart; disconnect removes
that client's native reference. No desktop keyring or live server is used by
these isolated trials.

Linux credential storage verification passes on 2026-10-30: the full Go suite,
focused race tests, `go vet ./...`, generated documentation, Markdown checks,
command snippets, and skill validation. Release configuration and all six snapshot
targets build. Native package smoke checks pass, including file credential
lifecycle checks. The packaged client also passes the real disposable keyring
trial under the race detector. The trials use private local services and temporary
configuration. No release publication or live Proxmox acceptance is claimed.

Credential writes now have a private durable intent before the store is changed.
The intent binds the configuration directory, context, store, generated reference,
transaction ID, and expected record digest. It contains no token. New credential
records include ownership IDs. Protected-file publication stages a complete
record and links it without replacing another entry. Recovery can discard an
owned partial stage without promoting it to a working credential.

The local credentials-pending command exposes public transaction metadata.
It omits credentials and digests. Discovery and cleanup bypass saved connection
settings, output preferences, and credential environment values. Cleanup works
before the first context file exists and leaves legacy selection intact.
Recovery first checks actual committed references. It preserves active values,
clears their completed intents, and removes only matching unreferenced records.
Changed records, unsafe stages, invalid journals, and unavailable stores remain
blocked with a transaction ID and corrective action. Same-context retries recover
an earlier write before allocating another reference. Other contexts can proceed.

Retirement cleanup is limited to the context changed by a normal mutation.
Explicit cleanup processes all saved intents and retirement records. Directory
syncs preserve publication and cleanup ordering. The journal is bounded to 128
pending records. Old orphan records without an intent are not discovered or
adopted. Context schema versions and legacy credential readers remain supported.

Subprocess tests terminate clients after intent publication, a partial file stage,
a stored credential, a linked stage, context publication, and credential deletion.
Recovery preserves committed credentials and reclaims owned orphan files without
creating an empty context index. Tests also cover lost acknowledgements, changed
values, journal tampering, capacity limits, active hard links, and independent
contexts. The disposable native keyring trial includes an uncommitted record
across a provider restart. System power-loss behavior and live desktop trials
remain unverified.

Credential transaction recovery verification passes on 2026-10-30: the full Go
suite, focused race tests, `go vet ./...`, generated documentation, Markdown
checks, command snippets, and skill validation. All six release snapshot targets
build. Native smoke checks confirm that ordinary credential operations leave no
pending intent or stage. The packaged client recovers all six terminated-client
checkpoints under the race detector. The private GNOME trial recovers an orphan
across provider restart and preserves committed credentials. No live-host trial,
system power-loss result, or release publication is claimed.

The CLI and compatibility dashboard now share deferred connection resolution.
Context selection captures public metadata first. Credential access follows all
explicit connection flags. A replacement token, another endpoint, or an explicit
socket can bypass an unavailable saved credential. Help, version, packaged skill
commands, local token inspection, local repository commands, and local recovery
commands bypass saved connections and output preferences.

An explicit socket selects local trust and clears remote credential data.
Explicit remote and socket targets conflict. An empty endpoint does not select
local trust. Changing the endpoint clears the saved credential, server identity,
SSH jump defaults, and HTTP acknowledgement. Environment tokens remain explicit
input for the final target. Removing the active context requires another explicit
selection. Remote failure never selects a socket automatically.

The resolver keeps its captured context when another process switches the active
context. It rechecks that context's binding before loading a saved credential.
A replacement reference or changed endpoint, identity, transport, or jump defaults
blocks the read. Credential records must match their reference, endpoint, and
server identity. Legacy records remain readable when those bindings match.
New context metadata also records public SSH jump defaults. SSH uses the same
resolved target and does not open another credential store for those defaults.

Tests cover missing saved credentials, explicit and environment replacements,
child command flags, transport conflicts, endpoint normalization, HTTP policy,
context replacement, record tampering, local help, and broken local settings.
Package smoke checks also cover unavailable saved references and local discovery.
This completes another connection-resolution increment. The full plan remains
in progress. Native macOS credentials, enrollment and renewal, detached setup,
dependent resource idempotency, interrupted result collection, complete test
evidence, guided dashboard flows, optional transport evaluation, mixed-version
upgrades, and live human and agent acceptance trials remain open.

Deferred resolution verification passes on 2026-10-30: the full Go suite,
focused race tests, `go vet ./...`, generated documentation, Markdown checks,
command snippets, and skill validation. All six snapshot targets build.
Native package smoke checks pass. The final packaged client also passes the
terminated-write and disposable GNOME Keyring recovery trials under the race
detector. These checks use local fixtures. They do not establish live Proxmox,
macOS, power-loss, or human and agent acceptance results. No release is published.

Dependent workspace creation now has a durable allocation journal for keyed jobs.
Migration 28 reserves the workspace identity and name before a backend call.
It stores complete volume receipts before publication. The workspace row and
submission binding commit together. Retrying a known receipt does not allocate
again. Ordinary creates respect pending name reservations. Completed submissions
retain the original job and workspace identity across handler reconstruction.

Unknown backend outcomes return a structured blocked state with job, workspace,
storage, state, and any known volume. The CLI preserves this recovery object.
Failed publication retains the volume. A retry never recreates a removed
committed workspace or deletes an uncertain volume. Capabilities advertise this
keyed path when a workspace manager and server identity are available.
Branch session creation remains unavailable for keyed requests.

Tests terminate a subprocess after an external allocation but before its receipt.
Retry after reopening SQLite reports the reserved identity without another
allocation or deletion. Tests also cover concurrent requests and names, a saved
receipt, failed workspace publication, failed binding, cancellation during the
backend call, missing committed resources, and expired request keys.
Automatic external reconciliation remains incomplete. Live Proxmox allocation
compatibility and ownership checks still require validation. The full plan
remains in progress; this record does not claim end-to-end workspace acceptance.

Workspace allocation verification passes on 2026-10-30: the full Go suite,
focused race tests, vet, generated command documentation, Markdown checks,
command snippets, and skill validation. Release configuration and all six
snapshot targets build. Native package smoke checks pass. Database tests also
reject publication under another allocation's reserved name and adoption of a
volume already recorded by another workspace. These are local fixture results.
No live storage acceptance, automatic unknown-outcome reconciliation, or release
publication is claimed.

Workspace allocation now uses valid storage owner IDs and owner-prefixed disk
names. Migration 29 reserves a separate ID through the existing sandbox and
template reservation table. New volumes use the range 900000000 to 999999999.
No extra guest is allocated. Cluster inventory includes containers and virtual
machines on other nodes. Inventory failures, changed reservations, and occupied
known owners block work without choosing another ID. Existing volumes retain
their current IDs. New workspace responses expose owner and allocation metadata.

The Proxmox allocation contract now reads the actual JSON string response.
The shell backend uses pvesh JSON for storage status and allocation. It does not
parse human allocation messages. Storage type selects the raw-image filename
and expected volume ID. Invalid or mismatched receipts remain uncertain.
A typed read-only preflight failure can safely return the journal to planned.
A failed allocation response cannot use that reset path. Known volume receipts
recheck their owner before database publication. The fake backend now rejects
duplicate allocation instead of silently renaming a volume.

Tests cover owner reservations, shared database guards, external collisions,
containers on other nodes, changed ownership, storage layout, protocol shapes,
invalid names, failed receipts, and repair after a preflight failure. Existing
terminated-worker tests also preserve the new owner metadata. This does not
prove live storage acceptance. Unknown allocations still need immutable ownership
proof. Manual cluster changes, cross-daemon coordination, and the existing
snapshot and clone backend paths require further compatibility work. The full
experience plan remains in progress.

Storage owner verification passes on 2026-10-30: the full Go suite, focused race
tests, vet, generated documentation, Markdown checks, and command snippets.
Integration tests pass with the fake backend, including job workspace attachment
and lease behavior. Release configuration and all six snapshot builds pass.
Native package smoke checks pass. These checks verify code and fixture contracts;
they do not establish live Proxmox allocation, storage snapshots, automatic
unknown-outcome reconciliation, or full human and agent acceptance. No release
is published.

Native workspace storage now uses an embedded local Proxmox helper. Snapshot
operations call the installed storage library. Forks use a full ZFS stream to
create an independent volume. They do not use unsupported HTTP routes or shell
subcommands. The API backend requires an explicit local helper and a loopback
endpoint. Failed authenticated reads stop before helper execution. Encrypted
sources block before copy until a key workflow exists.

Migration 30 records fork intent before copy. It binds the actor, server, source,
copy snapshot, and reserved destination. Forks no longer allocate and delete a
placeholder volume. Complete receipts include volume and snapshot identifiers.
Failures retain uncertain resources. Failed workspace publication retains a
verified copy. Pending names remain reserved. Same-actor retries return the
original pending operation. The workspace fork-status command lists or reads
these records through permission-checked endpoints. Snapshot publication failures
also retain the snapshot and return inspection metadata.

The embedded helper tests use the actual Perl interpreter with isolated mock
modules. They cover independent copies, snapshots, existing destinations, partial
copies, failed cleanup, wrong nodes, and encrypted sources. Process tests verify
cancellation of child processes. Database and API tests cover retained copies,
immutable intent bindings, name reservations, observation permissions, historical
sandbox scope, and preserved JSON recovery fields.

Fork execution remains synchronous. Historical state does not prove worker
liveness. Unknown outcomes still require immutable ownership proof and external
reconciliation. Automatic resumption, automatic cleanup, complete snapshot
interruption recovery, live storage trials, and mixed-version trials remain
open. These changes do not complete the experience plan.

Native storage verification passes on 2026-10-30: the full Go suite, focused race
tests, vet, integration tests, skill validation, generated documentation,
Markdown checks, and command snippets. All six release snapshot targets build.
Package smoke checks verify the exact embedded recovery guide and native client
flows. The shared CLI test server now uses the existing short socket directory
helper, which avoids path limits under longer temporary roots.

The first disk-backed full run exceeded the ten-minute test deadline during
SQLite synchronization. The first integration run exceeded its provisioning
deadline. The successful full and integration reruns use a private memory-backed
temporary directory for both TMPDIR and GOTMPDIR. They verify behavior, not
power-loss durability. Focused race checks also pass with disk-backed fixtures.
No live Proxmox, encrypted-copy, mixed-version, human acceptance, or release
publication result is claimed. The goal remains active.

Explicit job tests now use admitted, ordered test commands. Migration 31 persists
the plan with the job. The API, CLI, validation plan, job views, guest bootstrap,
and submission key preserve the commands. Input limits apply before allocation.
Capability checks prevent the client from sending test plans to older servers.

The guest runner executes commands after agent success, in the repository and
configured inner sandbox. Commands share five minutes, bounded by the remaining
job lease. Logs, exit codes, duration, and skip reasons remain separate from the
agent exit code. Failed commands fail the job. Uploaded repository receipts and
local collection verify ordered command identity and archived log digests.
Missing or changed evidence blocks verified cleanup. URL-based jobs validate
terminal command reports but retain their existing artifact contract.

Tests run the shipped guest script with real commands, upload results through
fixture APIs, and verify both passing and failing test receipts. Other tests
cover timeouts, skipped commands, command order, invalid plans, idempotency
conflicts, changed logs, missing evidence, and older server capability denial.
Guest-reported command success does not establish test coverage or independent
task assertions. Live Proxmox execution, mixed-version trials, and human and
agent acceptance remain open. The full experience goal remains active.

Explicit test evidence verification passes on 2026-10-30: the full Go suite,
focused race checks, integration checks, vet, generated documentation, Markdown
checks, command snippets, skill validation, and Bash syntax. All six snapshot
archives build. Native package smoke checks accept bound test evidence and reject
a changed test log. The final client validation check runs before connection or
session creation. Empty test arrays remain compatible with serialized receipts.

Full and integration tests use private memory-backed temporary directories.
These fixtures verify behavior; they do not prove power-loss durability or live
Proxmox execution. URL-based jobs preserve the sandbox when terminal test
evidence is missing. Their archive verification and interrupted report recovery
remain outside the uploaded repository result contract. Detached setup workers,
credential enrollment and renewal, automatic resource reconciliation, dashboard
workflows, optional transport evaluation, upgrades, and fresh human and agent
trials remain part of the original goal. No release is published.

Setup now submits a pinned server worker through SSH. A transient systemd service
owns execution after admission. It holds the host lock, records progress on the
server, initializes the host, verifies a readiness sandbox, and checks cleanup.
The client saves a private reference and observes the server journal. Client
observations preserve server event cursors and progress timestamps.

The payload is deterministic and immutable. Worker invocation verifies root
ownership, private directories, and the pinned binary digest. The request binds
owner, server identity, context, architecture, wait budget, and artifact digests.
Old artifacts without worker protocol 1 block before installation. Setup accepts
`--no-wait`. Operation resume waits for active work or restarts a stopped worker
with the same request after external checks. Initial staging recovery requires
the original local artifacts. Server-side recovery uses its retained payload.

Client enrollment remains pending until the endpoint authenticates, its identity
matches SSH, and the destination context is saved. Its verification has a separate
timestamp. Failed verification preserves the working context. Cancellation saves
local intent before contacting the server. It prevents later admission and checks
active execution. Worker termination and external task completion remain separate
claims. Expired keys block new execution but permit cancellation and observation.

Real process fixtures kill the client and verify continued server execution.
Other trials kill the worker during readiness, resume the same sandbox, and
cancel active work with readiness cleanup. Contract tests cover immutable uploads,
changed payloads, protected invocation, host locking, key expiry, credential-free
records, preserved event cursors, and connection identity mismatch. These trials
substitute host commands. They do not prove native Proxmox or systemd acceptance.
Automatic reboot restart, fresh hosts, power loss, mixed versions, complete native
resource ownership proof, and the remaining experience requirements remain open.

Readiness and cleanup now check backend inventory as well as the sandbox row.
A changed backend name or an unmanaged VM blocks cleanup. A database tombstone
with a remaining backend VM cannot produce verified cleanup. Recovery after that
condition clears preserves the existing sandbox identity and does not create
another VM. Initial staging errors also return the operation ID and planned
remote staging path. Failed submissions retain their original machine error code.

Server worker verification passes on 2026-10-30: the full Go suite, focused race
checks, integration checks, vet, generated command documentation, Markdown
checks, command snippets, and skill validation. All six snapshot targets build.
Native package smoke checks verify worker protocol 1 and the embedded agent guide.
No release is published. The full and integration suites use private memory-backed
temporary directories; these checks do not prove power-loss durability.

An unrelated test fixture initially gave three quick commands a one-second
budget. A clock boundary could skip the third command. Ordinary command checks
now use ten seconds; the timeout case retains its one-second limit. Production
test budgets remain unchanged. The full suite and focused race checks pass after
that correction.

This stage does not establish native Proxmox installation, native systemd service
startup, fresh human or agent acceptance, automatic reboot recovery, or complete
immutable VM ownership proof. Credential enrollment and renewal, native macOS
storage, automatic external reconciliation, interrupted result recovery,
dashboard workflows, optional transport evaluation, upgrade trials, and the
remaining original acceptance gates still require work. The goal remains active.

Credential enrollment now verifies an operator-signed task credential over the
remote API and saves its reference without printing it. It uses an already
authorized signing key. It does not register a new trust root. Conditional context
publication preserves a concurrently changed destination.

Credential renewal preserves issuer, subject, command permissions, and sandbox
scope. A new candidate requires operator signing authority. The client stores a
protected staging credential before preparation. Migration 32 records one candidate
per parent. Commit saves the parent revocation and receipt atomically. Retries
use that same candidate. Server revocation checks remain effective in the commit
transaction. Scoped credentials need explicit token.renew authority.

The client keeps separate receipts for request IDs. Recovery of a lost commit
reply does not need the private key again. It publishes the staged candidate only
if the destination binding still matches. A lost reply can temporarily leave the
original context pointing at its revoked parent. The staged context and receipt
support recovery. Expired staging requires explicit enrollment. Completed receipts
remain historical after later rotations or revocation.

Tests cover exact scope, denied widening, revocation races at the commit boundary,
restart and replay, lost replies, keyless recovery, conditional publication,
retained request receipts, and credential-free output. Native macOS storage,
hardware signing keys, key registration, dashboard credential refresh, live-user
and fresh-agent trials, and other original acceptance gates remain open. The
existing SSH key holder still has authority to mint independent tokens. This
protocol does not claim issuer or descendant revocation.

Credential lifecycle verification passes on 2026-10-30. The full Go suite, vet,
focused race checks, integration checks, generated documentation, Markdown checks,
command snippets, and skill validation pass. All six snapshot targets build.
Package smoke checks pass. The snapshot client also passes three real process
termination trials: before preparation, after preparation, and after server commit.
Each trial resumes without the signing key, reuses the same candidate, publishes
the saved credential, removes staging, and rejects the revoked parent.

These trials use a local HTTP server, real authentication, SQLite, and private
file storage. They do not establish power-loss durability, native macOS storage,
or live-host acceptance. Process termination during local publication and cleanup
remains a separate acceptance case. No release is published. The full experience
goal remains active.

The dashboard now provides a guided sandbox flow. It discovers profiles, checks
plans, reviews server identity and lease settings, and rechecks before creation.
Missing profiles remain blocked. Changes invalidate the review. Capacity preflight
uses the same configured pool as admission without reserving resources. Missing
capacity configuration is explicit. Native standalone validation follows the
existing native SSH requirement for controller access.

The connection panel shows the fixed endpoint, server identity, permissions,
scope, and credential expiry. It distinguishes browser enrollment from upstream
credential failure. A changed identity blocks creation. The browser keeps the
existing content policy. Dialogs support focus containment, Escape, and focus
return. The layout supports a narrow viewport. Bulk confirmations enumerate
sandbox targets and operate only on those targets.

Job details now support running and terminal jobs. They poll for progress and
preserve focused controls. They show reported test commands, execution evidence,
artifact downloads, cancellation, cleanup, retained resources, and reported lease
expiry. Test coverage is not inferred from command success. Missing evidence or
resource accounting stays explicit. The execution archive supplies downloadable
logs and reports. Inline archive inspection remains separate work.

Dashboard verification passes on 2026-10-30. The full Go suite, vet, focused race
checks, integration checks, documentation checks, and skill validation pass.
Nine JavaScript behavior checks cover enrollment, authorization, discovery,
changed identity, older validation endpoints, and capacity denial. All six
snapshot targets build and pass package smoke checks.

The native snapshot client passes a real browser fixture for guided creation,
missing profiles, capacity denial, changed settings, focus containment, narrow
layout with a full-length identity, polled job progress, command results,
cancellation, credential revocation, and changed identity. The fixture substitutes
the remote API. It does not establish live Proxmox execution, complete accessibility
acceptance, or fresh human and agent trials. Inline log inspection, automatic
credential refresh, complete resource recovery, macOS storage, mixed-version
trials, and remaining original gates still require work. The goal remains active.
No release is published.

Saved dashboard credentials now refresh within the original verified context.
The running process pins its named source and authorization baseline. Compatible
replacement publication keeps the browser session. Active context changes do not
redirect it. Explicit credentials, legacy files, and contexts without saved server
identity retain their startup credential.

The replacement must authenticate with the original issuer, subject, commands,
and scope. SSH replacements have a finite expiry within 24 hours. Endpoint,
identity, transport policy, and jump bindings remain fixed. Missing storage or a
changed binding blocks forwarding without a cached credential or socket fallback.
The dashboard does not mint a replacement or finish a pending renewal itself.

Capabilities now report the authenticated issuer and request identity support.
Bound dashboard requests carry the expected identity. Network authentication checks
that identity before handler dispatch, including when an endpoint changes after
client preflight. State-changing dashboard requests also recheck current authority.
Older servers without identity enforcement cannot enable saved credential refresh.

Saved refresh verification passes on 2026-10-30. Signed-token tests cover actual
renewal commit, parent revocation, compatible publication, concurrent readers,
changed issuer or grants, lifetime bounds, missing storage, removed contexts,
changed targets, and revocation without fallback to a usable older credential.
Network tests reject changed, empty, and duplicate identity headers before handler
execution for signed and legacy credentials. Eleven JavaScript checks pass.

The full Go suite, vet, focused race checks, integration checks, documentation,
and skill validation pass. All six snapshot targets build. Package smoke passes.
The native snapshot browser fixture passes both fixed and saved credential modes.
Saved mode keeps its browser session through publication, blocks changed authority,
and resumes after a compatible replacement. The API substitute does not establish
live renewal, native macOS storage, complete accessibility acceptance, or fresh
human and agent acceptance. The original goal remains active. No release is published.

CLI clients now retain the resolved server identity. Bound JSON, streaming,
artifact, repository, and enrollment requests check capability compatibility and
identity enforcement before sending operation payloads. They carry the original
identity through the actual request. Custom streaming headers cannot replace it.
Redirect responses fail without choosing another endpoint.

Connect now verifies capability identity and enforcement independently of optional
host metadata. A narrow credential needs discovery and status permissions; host
read permission is not needed for identity. Publication conditionally matches the
destination captured before verification. Concurrent changes remain intact.
An existing verified destination cannot silently accept another identity. Explicit
credentials and an endpoint can enroll a new server under a new context name.

The legacy connect path applies only to an unbound server with no capabilities
endpoint. It reports `legacy_unverified` and `identity_verified: false`, and stores
no verified identity from optional host claims. Other discovery or authentication
failures block publication. Existing connection fields remain available.

Remote dashboards with fixed credentials now verify and pin an identity before
opening a listener. They check enforcement before forwarding and retain their
startup credential. Verified saved credentials keep their existing refresh guard.
An incompatible remote dashboard returns structured blocked metadata before bind.
The local trusted socket remains a separate explicitly selected path.

CLI identity verification passes on 2026-10-30. Tests cover preflight before
payload reads, changed identity after preflight, old capability support, redirect
rejection, streaming header overrides, scoped connect without host read, concurrent
publication, and legacy labeling. A multipart redirect cannot publish a matching
receipt. Fixed dashboard credentials keep the target binding without following
saved credential changes. Incompatible startup returns structured blocked output.

The full Go suite, vet, focused race checks, integration checks, documentation,
and skill validation pass. All six snapshot targets build. Package smoke verifies
connection identity output. The native snapshot browser passes saved and fixed
credential modes. The snapshot client also passes renewal process termination
trials with the new request checks. These fixtures do not establish live host,
macOS storage, full mixed-version acceptance, or fresh human and agent gates.
The original goal remains active. No release is published.

Native workspace forks now have explicit recovery. The helper keeps a private,
locked journal with a random ownership value before native mutation. Snapshot
creation and full receive assign that value during the native operation. Recovery
checks successful inventory, local property ownership, source and snapshot GUIDs,
and the independent target GUID. It does not overwrite a target or use force,
recursive, incremental, resumable, or clone receive options.

A complete interrupted receive can finish verified snapshot cleanup. A completed
copy can finish failed workspace publication. The daemon retains the original
workspace ID, target volume, size, actor, server identity, and allocation owner.
Known receipt identities cannot change or disappear. Repeated successful requests
return the original workspace. A changed source binding blocks before native work.
A cancelled call releases its source lease through an independent cleanup context.

`workspace fork-resume SOURCE OPERATION_ID` uses the advertised
`workspace_fork_recovery` feature. The HTTP route requires `workspace.fork`, the
source scope, and the original actor. Same-name retries also use the saved intent
when the backend supports recovery. Fork observation remains a separate read path.
The CLI help, completion, API schema, recovery guide, and packaged skill include
this workflow.

Partial targets, changed GUIDs, inherited ownership properties, unsafe state
storage, missing proof, active native locks, and active source leases block
recovery. No blocked target is overwritten or deleted. Named source snapshots
remain. Retained volumes have no automatic expiration. After daemon termination,
a source lease can block recovery until its 30-minute expiry. Older unfinished
operations without a pinned size remain inspection cases. Native snapshot creation
and restoration still use their existing contracts; this change does not add
recovery to every storage operation.

Native fork recovery verification passes on 2026-10-30. Isolated Perl fixtures kill
the helper after snapshot creation, receive, each cleanup, and durable completion
before its reply. Recovery performs one receive and returns the same target.
Additional checks preserve partial or replaced targets, reject inherited tags,
retain named snapshots, and reject changed recorded identities and unsafe state
roots. Daemon tests cover restart, unknown replies, failed publication, actor and
source changes, and repeated requests. Authorization checks cover command denial
and source sandbox scope. The packaged client uses its bound connection and checks
the feature before mutation.

The full Go suite, vet, focused race checks, broader race checks for the affected
packages, integration checks, documentation checks, and skill validation pass.
All six snapshot targets build and package smoke checks pass. The native fixtures
substitute Proxmox and OpenZFS calls. They do not establish live storage acceptance
or power-loss durability. Cross-daemon allocation, external VM replacement proof,
complete unknown-resource reconciliation, macOS credential storage, Tailcat,
mixed-version trials, and the original human and agent acceptance gates remain
open. The goal remains active. No release is published.

The macOS credential adapter is implemented. It uses the default file-based
Keychain through pinned purego bindings. This preserves builds with Cgo disabled.
A child of the current executable handles each native request. Fixed arguments
and a filtered environment contain no credential. Requests and responses use
bounded private pipes. The parent enforces a five-second deadline and waits for
the child. Worker diagnostics are discarded and core dumps are disabled.

The worker disables legacy Keychain interaction before access. Item queries also
disallow authentication UI. It does not create or unlock a Keychain, enable iCloud
synchronization, or grant access to all applications. Configuration scope and a
random reference select each record. Add refuses an existing record. Read checks
Core Foundation result types, count, and size before copying native bytes.
Read and delete search only the selected default Keychain.

Contexts record `macos-keychain`. Existing credential journals and retirement
checks accept that store and keep their ownership checks. Existing native
references fail without a file fallback. Locked stores, denied access, worker
failure, and malformed records return structured blocked errors. New automatic
connections can use file storage when no native store exists. Explicit file
selection remains available for unattended clients.

Portable verification passes on 2026-10-30. Fixtures inspect the native query
policy and check configuration isolation, duplicate refusal, native result bounds,
record replacement, journal cleanup, missing records, deadlines, and diagnostic
privacy. Both macOS architectures compile without Cgo. Native integration tests
cover persistence and cleanup after a terminated client. Their binaries compile,
but this Linux workspace cannot execute them.

The package check for macOS covers actual CLI connection, native replacement,
migration to files and back, disconnect, and an optional upgrade from another
client. It keeps fixture records if verification or cleanup fails. It requires
an accessible, unlocked default Keychain and never changes the selected Keychain.
The release archives include the binding's license and notice. Package smoke
checks verify those files.

Native macOS runtime, code-signing access, release upgrades, process termination
at every native boundary, and power-loss behavior remain unverified. A replaced
binary can require access approval through macOS. Portable fixtures do not prove
that approval behavior. Live host acceptance, full resource reconciliation,
Tailcat, mixed-version trials, and the original human and agent gates also remain
open. The full experience goal remains active. No release is published.

The dashboard now provides inline inspection of verified execution logs. A user
can select the runner log, a reported test log, or the execution report. The view
loads bounded text pages and shows byte ranges and end of file. Previous, next,
and reload controls support keyboard use. The text area wraps in narrow layouts.
Guest markup remains text and never becomes HTML or an executable command.

The shared `GET /v1/jobs/{id}/execution` route requires `job.artifacts` and the
job's sandbox scope. It advertises `execution_inspection`. The route supports
verified repository snapshot results in terminal jobs. The index identifies the
job, server, manifest, archive, evidence, and available entries. Each page can pin
the archive digest. Older servers and other jobs retain artifact downloads.

The server reads one descriptor beneath the artifact root. The archive file
cannot be a symlink or a special file. A named pipe does not wait for a writer.
The server checks the full
archive, digest, report identity, requested test commands, test log digests, and
saved evidence before returning text. It does not extract files. Text pages keep
UTF-8 character boundaries and have a maximum of 65536 bytes. Invalid ranges,
missing entries, non-UTF-8 data, changed archives, and unverified evidence return
explicit errors. A rejected response contains no previously collected log prefix.
The dashboard also checks response identity and cursor progression. Late responses
cannot replace a newly selected log.

Verification passes on 2026-10-30. Archive tests cover UTF-8 paging, empty logs,
test log binding, invalid entries, credential content, changed digests, and trailing
gzip members. API tests cover command and sandbox scope denial, digest pinning,
invalid queries, missing evidence, changed bytes, and confinement beneath the
artifact root. Fifteen JavaScript checks include plain-text rendering, old servers,
changed identities, paging, and selection races.

The full Go suite, vet, focused race checks, integration checks, documentation,
and skill validation pass. All six snapshot targets build and package smoke passes.
The native snapshot browser fixture verifies inline paging, report selection,
markup inertness, keyboard focus, and narrow layout through a saved connection.
Its API is substituted. Backend tests inspect actual archives with SQLite and local
repository fixtures. These checks do not establish live guest execution or complete
accessibility acceptance. Native host and Mac acceptance, full resource recovery,
optional Tailcat, mixed-version trials, and fresh human and agent trials remain
open. The full goal remains active. No release is published.

An isolated Tailcat experiment now exists under `experiments/tailcat`. It pins
v0.7.0 and Go 1.27.1. Normal builds, installation, and root dependencies remain
unchanged. `make tailcat-prototype-check` selects the experiment explicitly.
The normal daemon still advertises `tailcat: false`.

The prototype restricts forwarding to a fixed literal loopback TCP API. It pins
server identity, preserves bearer credentials, and rejects non-API paths. It does
not enable a trusted socket, shell, or subnet forwarding. Its listener rejects an
empty peer allowlist. Error responses are structured and do not include upstream
diagnostics or connection secrets.

A private local relay fixture passes real WireGuard traffic. Approved peers retain
AgentLab's signature, command, sandbox, expiry, and revocation checks. An unapproved
peer cannot open the virtual API port. This is limited fixture coverage, not a
complete authorization or network acceptance result. The pinned upstream release
has a static peer allowlist; peer removal requires a tested restart or rotation
workflow. The newer main-branch admission interface is not assumed to exist.

The supervised helper, secure pairing, persisted identities, process recovery,
rotation, direct and relay comparison, native platform tests, and measured adoption
decision remain required. The current experiment does not satisfy the phase 4 gate.
No Tailcat transport is enabled on a user's server. The full goal remains active.

The Tailcat prototype also persists node and preshared identities in protected
files. It rejects damaged records, wrong roles, unsafe permissions, and unexpected
file entries. Public status formatting omits secrets. A real local-relay test
reopens the stored identities and reconnects after a listener restart with the
same private address. Integration and race checks pass. This does not establish
process-crash recovery, secure pairing, rotation, or comparative performance.
Those gates remain open before the helper can be adopted.
