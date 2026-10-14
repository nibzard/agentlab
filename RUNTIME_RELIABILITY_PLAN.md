# AgentLab Runtime Reliability Plan

Status: Proposed

Date: 2026-10-14

Baseline: `7f0f3fd`, after PR 1

AgentLab should provide a dependable execution environment for autonomous coding on operator-owned infrastructure. The priority is a consistent workflow: prepare a known environment and workspace checkpoint, grant bounded access, execute and observe, retain changes and evidence, and reclaim disposable compute.

Deliver this through five workstreams, each with a reviewable implementation boundary and acceptance tests. Readiness and bootstrap come first. Protected workspaces, recovery, and durable results complete the workflow before new behavior becomes the default.

## Scope and success criteria

The initial supported configuration is a Proxmox VM with an AgentLab-managed template and ZFS workspaces. Test both shell and API backends, with the daemon on-host and off-host where the selected transport supports that layout. An off-host shell deployment requires an explicit command transport; do not claim support for invoking local Proxmox commands remotely.

Existing lifecycle states, interactive sessions, and explicit workspace bindings remain compatible during the opt-in rollout. Changes to defaults apply to new autonomous jobs after the release gate passes.

Success means that:

- Readiness describes capabilities that were observed for the current guest incarnation.
- Supported deployment layouts provide the same guest identity, policy, and job protocol.
- A job on a workspace fork cannot alter its parent workspace.
- Retrying a request or restarting the daemon cannot silently duplicate execution or delete an unowned resource.
- Changes, logs, and verification evidence remain accessible after compute is destroyed.

Expansion of backends, dashboards, tenancy features, and pooling is deferred during this work. Arbitrary external side effects cannot be rolled back by workspace recovery; network permissions and credential scopes must bound that authority separately.

## Current implementation boundaries

| Area | Existing implementation | Gap addressed by this plan |
| --- | --- | --- |
| Provisioning | [Job orchestrator](internal/daemon/job_orchestrator.go) advances lifecycle states and probes SSH asynchronously. | A running VM and an execution-ready guest need distinct observations. |
| Bootstrap | [Bootstrap API](internal/daemon/bootstrap_api.go) looks up a job before validating and delivering bootstrap identity. Native SSH mode bypasses bootstrap and rejects jobs. | Guest identity must support standalone sandboxes, and bootstrap delivery must be independent of snippet availability. |
| Guest execution | [Guest runner](scripts/guest/agent-runner) bootstraps, prepares a repository, executes, reports, and uploads artifacts. | Readiness, execution identity, and result delivery need explicit protocol boundaries. |
| Persistence | [Workspace fork](internal/daemon/workspace_fork.go) and [snapshots](internal/daemon/workspace_snapshots.go) already enforce attachment and lease checks. | Autonomous jobs need a protected parent and explicit workspace modes. |
| Recovery | [Sandbox manager](internal/daemon/sandbox_manager.go) reconciles observed VM state and performs lifecycle cleanup. | Recovery needs durable operation intent, ownership evidence, and execution receipts. |
| Results | [Runner API](internal/daemon/runner_api.go) and [artifact storage](internal/db/artifacts.go) retain reports and uploaded artifacts. | Results need a versioned manifest, completeness status, and retention independent of compute. |
| Live validation | [Proxmox E2E test](tests/e2e_proxmox_test.go) is a placeholder that skips execution. | The release gate needs an executable, isolated live test harness. |

## Delivery order

| Workstream | Deliverable | Dependency |
| --- | --- | --- |
| 1 | Readiness contract and diagnostic waits | Existing guest authentication for supported job paths |
| 2 | Standalone guest identity and interchangeable bootstrap delivery | Readiness contract |
| 3 | Protected workspace modes and safe reset | Bootstrap and workspace readiness |
| 4 | Durable operations and recovery | Stable provisioning and workspace steps from workstreams 1 to 3 |
| 5 | Durable results and independent verification status | Execution identities from workstream 4 and workspace lineage from workstream 3 |

Treat these as five PR boundaries. Split a workstream when needed to keep migrations, protocol changes, and backend support reviewable. Each PR introduces its focused tests; the final PR assembles the complete release scenario.

## Workstream 1 Explicit readiness

### Contract

Retain current lifecycle state names initially. Add a readiness record with separate conditions for `vm_running`, `network_ready`, `guest_bootstrapped`, `workspace_ready`, and `execution_ready`. Each condition has an observation time, reason, and status of ready, pending, failed, or unsupported. Conditions that do not apply to a request are identified explicitly.

Bind observations to a sandbox generation and guest boot ID. A restart invalidates observations from the old boot; recreating a sandbox invalidates all observations from the old generation. A late report must not make a replacement guest ready.

A request specifies the capabilities it needs. A managed job requires authenticated guest identity, its required workspace, and an available runner. A standalone request requires its selected execution capability. A TCP connection to SSH port 22 alone does not establish authenticated command execution.

### Implementation

1. Add additive readiness models, persistence, and API fields. Include required capabilities and guest generation in the provisioning record.
2. Derive VM and address conditions from backend observations. Record authenticated guest readiness separately from execution status reports.
3. Extend the guest protocol to report bootstrap, workspace, and runner readiness before accepting work. Use the existing per-sandbox secret; validate generation and boot identity.
4. Add bounded waits to create and inspect workflows. Return the pending condition and last failure when a deadline expires. Unsupported capabilities fail preflight rather than waiting indefinitely.
5. Keep legacy clients working while new clients use readiness fields. Document that lifecycle `RUNNING` alone is not an execution-readiness guarantee.

Primary files: `internal/models/models.go`, `internal/db/migrations.go`, `internal/daemon/job_orchestrator.go`, `internal/daemon/runner_api.go`, `internal/daemon/api_types.go`, `cmd/agentlab/commands.go`, `cmd/agentlab/ssh.go`, and `scripts/guest/agent-runner`.

### Acceptance

- Broken guest networking, expired bootstrap, and failed workspace mounts cannot satisfy a managed job wait.
- Unauthorized, stale, or duplicate guest reports cannot advance the wrong generation.
- Lifecycle and readiness information remain distinguishable in JSON and human output.
- Native SSH sandboxes advertise their actual capabilities until workstream 2 supplies managed bootstrap.

## Workstream 2 Unified guest bootstrap

### Contract

Guest identity exists independently of a job. A bootstrap descriptor identifies the sandbox generation, controller endpoint, protocol version, and a short-lived single-use token. A successful bootstrap supplies identity and authorized capabilities; job data is optional.

Standalone bootstrap must not automatically release the entire default secrets bundle. Deliver only profile-authorized bindings and explicitly requested credentials. Reuse existing secret hashing, expiry, source restrictions, and scoped authorization.

### Implementation

1. Refactor bootstrap validation to authenticate the sandbox before loading optional job data. Make a no-job response a successful identity bootstrap rather than `job not found`.
2. Separate guest initialization from optional job launch. A standalone guest initializes its identity and services, reports readiness, and remains idle.
3. Introduce a narrow descriptor-delivery interface, conceptually `BootstrapGuest(ctx, vmid, descriptor)`. Preserve snippet delivery as one implementation.
4. Validate QEMU guest-agent command and file-delivery support on each supported Proxmox version. Implement an off-host transport only after the live capability probe passes. Transfer descriptors without exposing tokens in command logs or persistent guest files.
5. Ensure retries do not rotate a sandbox secret unexpectedly. Define recovery for a bootstrap response lost after token consumption: use an explicit controller-authorized replacement exchange and revoke the superseded credential. Do not broadly reopen consumed tokens.
6. Enable jobs in native provisioning only when delivery, authenticated readiness, and execution acknowledgement are verified. Until then, retain the explicit unsupported-mode error.
7. Test both transports against the same guest protocol suite and publish the supported deployment matrix.

Primary files: `internal/daemon/bootstrap_api.go`, `internal/db/bootstrap_tokens.go`, `internal/db/sandbox_secrets.go`, `internal/proxmox/proxmox.go`, `internal/proxmox/api_backend.go`, `internal/proxmox/shell_backend.go`, `internal/proxmox/cloudinit_snippets.go`, `scripts/guest/agent-runner`, and guest systemd units.

### Acceptance

- A standalone sandbox obtains its endpoint identity without a job or unnecessary credentials.
- On-host snippet delivery and supported off-host delivery produce equivalent readiness, metadata access, integration authorization, and job behavior.
- Concurrent token redemption permits one successful exchange. Expired and superseded credentials fail.
- A missing guest agent yields a clear preflight failure with no silent fallback to an unusable provisioning path.

## Workstream 3 Protected workspace execution

### Contract

Provide three explicit workspace modes: `ephemeral`, `fork`, and `exclusive`. Ephemeral work has no durable parent. Fork mode executes on a distinct volume derived from a recorded parent checkpoint. Exclusive mode permits direct writes to a durable workspace under its lease.

Use fork mode for new autonomous jobs that select an existing workspace during the opt-in rollout. Existing interactive sessions and explicitly requested exclusive jobs retain their behavior. Promote the safer default only after the recovery gate passes.

### Implementation

1. Add workspace mode and parent checkpoint fields to job requests, preflight responses, and job records.
2. Reuse snapshot and fork operations, including existing detached-volume and lease requirements. An attached parent requires an explicit stop or detach operation; never interrupt another session implicitly.
3. Record parent workspace, checkpoint, and fork lineage before attachment. Concurrent jobs receive separate forks from the same checkpoint.
4. Preserve result-bearing forks under an explicit retention policy. Reclaim unused temporary forks only after ownership and result status are known.
5. Make sandbox reset preserve durable workspace contents. Block VM rollback with an attached durable workspace until the backend can prove disk exclusion. The first supported reset path replaces the VM and reattaches the workspace.
6. Initially expose retention and discard operations plus a Git commit or patch for accepting useful changes. Automatic filesystem merging remains outside this workstream.

Primary files: `internal/daemon/workspace_fork.go`, `internal/daemon/workspace_snapshots.go`, `internal/daemon/workspace_lease.go`, `internal/daemon/workspace_rebind.go`, `internal/daemon/sandbox_manager.go`, `internal/daemon/api.go`, and job CLI commands.

### Acceptance

- Two jobs starting from one checkpoint have independent writable volumes.
- Deleting or corrupting files in one fork leaves the parent and sibling fork intact.
- Lease conflicts and unsupported storage fail before attaching or modifying the parent.
- Reset preserves a sentinel file and its checksum on the durable workspace.
- A VM snapshot cannot be used to silently roll back an attached durable workspace.

## Workstream 4 Durable operations and recovery

### Contract

Persist the desired operation before external side effects. An operation records its ID, caller scope, idempotency key, canonical request digest, desired outcome, resource claims, backend task references, completed steps, execution attempt ID, and last error.

The same scoped idempotency key and request return the same operation. Reusing the key with a different request returns a conflict. A retry may inspect or repeat a safely deduplicated step; it must not blindly rerun an agent whose execution status is uncertain.

### Implementation

1. Add append-only migrations for operation and attempt records. Thread operation IDs through allocation, provisioning, bootstrap, workspace attachment, and result reporting.
2. Persist each external action's intent and outcome. Capture backend task references and resource ownership markers where supported.
3. Extend reconciliation to inspect pending operations and their owned resources. Resume a known incomplete step, finish known cleanup, or classify the operation as requiring attention.
4. Treat a lost clone response or missing ownership marker conservatively. A reserved VMID alone is not sufficient evidence to delete a VM.
5. Add guest execution receipts keyed by attempt ID. After restart, query the existing attempt rather than dispatching a second agent. If the receipt cannot establish whether execution began, preserve an explicit unknown outcome.
6. Preserve leases and static address reservations for surviving owned resources. Release them only after the resource is absent, destruction is confirmed, or the action was rejected before resource creation.
7. Ensure HTTP cancellation stops waiting without silently cancelling an accepted durable operation. Expose an explicit cancellation operation with observable cleanup progress.

Primary files: `internal/db/migrations.go`, new operation persistence files, `internal/daemon/job_orchestrator.go`, `internal/daemon/sandbox_manager.go`, workspace managers, backend task handling, `internal/daemon/lifecycle.go`, and the guest runner.

### Acceptance

- Kill and restart the daemon after allocation, clone submission, clone completion, configuration, workspace attachment, bootstrap exchange, execution acknowledgement, and result receipt.
- Retry the original request after every interruption. Assert one logical operation and no duplicate agent dispatch.
- No test deletes an unowned VM or volume, including when a backend response is lost.
- Every remaining resource belongs to a visible operation, retained result, or explicit needs-attention record.
- Cancellation and recovery preserve the parent workspace and do not free addresses still owned by surviving VMs.

## Workstream 5 Durable results

### Contract

Every execution attempt produces a versioned result manifest tied to its operation, job, and workspace lineage. Compute destruction does not remove the manifest or retained artifacts.

Record execution status, result completeness, and verification status separately. An agent exit code of zero does not imply that checks passed or that the task is correct. Verification evidence identifies its producer; distinguish guest-reported output from independently executed checks.

### Implementation

1. Define the manifest with input commit, template identity and version, effective profile digest, attempt ID, workspace checkpoint and fork, output commit or patch, timestamps, exit status, and artifact checksums.
2. Record verification commands, their environment, exit statuses, and logs. Represent verification as not-run, passed, failed, or incomplete.
3. Collect staged, unstaged, and untracked changes under explicit size and redaction limits. Record collection failures and truncation instead of silently presenting a complete result.
4. Make result receipt and artifact upload idempotent by attempt ID. Preserve an incomplete manifest when upload fails; do not report a complete durable result prematurely.
5. Decouple result retention from sandbox teardown. Expose result inspection and export after VM deletion, and document retention and deletion policy.
6. Support a separate verification attempt in a fresh environment from the retained output revision. Its own manifest references the execution result and records the checks actually performed.

Primary files: `internal/daemon/job_orchestrator.go`, `internal/daemon/runner_api.go`, `internal/daemon/artifact_api.go`, `internal/daemon/artifact_gc.go`, `internal/db/artifacts.go`, `internal/models/models.go`, `scripts/guest/agent-runner`, and job result CLI commands.

### Acceptance

- After compute destruction, retrieve the input revision, output changes, execution logs, and verification records.
- Failed execution, failed verification, and failed artifact upload remain distinguishable.
- Duplicate or late reports cannot overwrite another attempt or turn an incomplete result into a complete one without its required artifacts.
- A fresh verification attempt can check the retained output without replaying the original agent task or receiving its unnecessary credentials.

## Live release gate

Replace the skipping Proxmox placeholder with a harness that uses explicitly configured test templates, storage, subnet, and a reserved VMID range. The harness tracks its own resources and refuses to operate outside those boundaries. Provide a dry-run inventory and retain diagnostics when cleanup is uncertain.

For each supported backend and deployment layout:

1. Create a detached parent workspace with a known Git commit and checksummed sentinel files; create a checkpoint.
2. Launch two deterministic jobs on separate forks from that checkpoint. Have one deliberately delete files on its fork and the other produce a known patch and test result.
3. Interrupt the daemon at each durable provisioning boundary in separate test cases, including after an external action succeeds but before its response is recorded.
4. Restart and retry with the same scoped idempotency keys. Verify resource ownership, execution receipts, independent forks, and diagnostic readiness.
5. Collect complete or explicitly incomplete result manifests, destroy disposable VMs, and retrieve retained results again.
6. Verify that the parent's revision and sentinel checksums are unchanged. Check for leaked VMs, volumes, leases, tokens, address claims, and accidental exposure of test credentials.
7. Repeat with missing guest-agent support, bootstrap expiry, workspace lease conflicts, upload failure, and an occupied VMID that the harness does not own.

Use deterministic commands for this gate; model output quality should not determine infrastructure correctness. Run unit, race, integration, documentation, build, and fuzz checks for each implementation stage. Publish live backend and transport results before changing defaults.

## Rollout and measures

Ship additive schemas and protocol capability negotiation first. Old templates advertise limited capabilities; managed readiness and bootstrap features require a compatible rebuilt template. Unknown fields must not grant authority or imply readiness. Introduce protected workspace modes explicitly, then change defaults for new autonomous jobs after the release gate passes.

Track time to requested readiness, bootstrap failure rate by transport, provisioning outcomes requiring operator intervention, recovery outcomes, duplicate dispatch incidents, parent-workspace checksum failures in the test gate, orphan resources, and result completeness after teardown. Establish a baseline before assigning latency or reliability targets. Any duplicate dispatch, unowned deletion, or parent-workspace corruption blocks promotion of the new defaults.

The first implementation PR should introduce the readiness schema, observations, diagnostic waits, and focused tests. It should establish the contract needed by unified bootstrap without claiming capabilities that the current guest transport cannot provide.
