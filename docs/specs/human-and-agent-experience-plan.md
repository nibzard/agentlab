# Human and agent experience plan

Status: Proposed. Date: 2026-10-29.

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
- Per-token revocation storage and propagation guarantees.
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
