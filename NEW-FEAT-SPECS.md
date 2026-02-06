# AgentLab PRD: Stateful Sessions, Workspace Safety, Messagebox, and One-Command Init

Status: Draft

Last updated: 2026-02-06

## Summary

AgentLab is already strong at provisioning isolated VMs and running jobs. The next product boundary is "persistent state done safely" plus "collaboration and resumability without external glue".

This PRD proposes five related feature sets:

- First-class "stateful mode" (workspace-backed jobs and sessions)
- Workspace safety primitives (locks, snapshots, restore, fork, health checks)
- A built-in Messagebox primitive for multi-agent coordination
- Session semantics and diagnosis workflows (resume, doctor, branch ergonomics)
- Appliance-level onboarding via `agentlab init` plus "golden profiles"

The goal is to make AgentLab feel like an opinionated, safe stateful agent appliance, not "a VM runner you assemble into workflows yourself".

## Context (Today's Baseline)

As of 2026-02-06, AgentLab already includes:

- Persistent workspaces: `agentlab workspace create/list/attach/detach/rebind`
- Workspaces are durable volumes and are mounted in guest at `/work` via `scripts/guest/agentlab-workspace-setup`
- `sandbox new` supports `--workspace <id|name>` (workspace attach at provision time)
- Jobs have durable audit events in SQLite (`events` table) exposed via `/v1/.../events` and `job show --events-tail`
- Sandbox lifecycle and lease primitives exist (TTL, keepalive, GC, reconcile)

The gaps are mostly product-surface and safety/operability around persistent state and coordination.

## Problem Statement

1. Persistent state is the real hard mode. Users care less about the VM lifecycle and more about: "What persists, where, how do I avoid corruption, and how do I recover?"
2. Multi-agent coordination is happening anyway (Slack/Discord as shared inbox/context window). Users need a simple shared mailbox/shared memory primitive more than they need a full message bus.
3. "Resume a session and diagnose it" is a killer workflow. Users want to pause/resume, snapshot/restore, and inspect sessions like they inspect git branches.
4. Onboarding needs to feel like an appliance: zero-to-working without manually touching infra config.

## Goals

- Provide an obvious mental model: ephemeral sandbox + persistent workspace + (optional) long-lived session.
- Make "stateful" an ergonomic one-flag workflow for both jobs and interactive sessions.
- Prevent accidental concurrent mutation of the same workspace, and make safe recovery paths first-class.
- Provide built-in coordination primitives for multi-agent collaboration without external Slack/Discord hacks.
- Make diagnosis/resume workflows fast and deterministic (doctor bundles, snapshots, branch workflows).
- Offer an `agentlab init` that can get a Proxmox host to "working" with minimal operator effort, plus curated profiles that match real use cases.

## Non-Goals

- Multi-host, distributed locking, or cross-host workspace replication (single Proxmox node first).
- A full pub/sub system (Messagebox is intentionally simple).
- Replacing git for code history (workspaces complement git; they do not replace it).
- Host bind mounts into guests (explicitly prohibited by existing security model).

## Concepts and Definitions

- Sandbox: An isolated Proxmox VM managed by AgentLab (`models.Sandbox`). Ephemeral by default.
- Workspace: A persistent disk volume (`models.Workspace`) attached to a sandbox at `scsi1` and mounted in guest at `/work`.
- Session: A higher-level concept that binds together a workspace, a (current) sandbox, a message stream, and history. Sessions are resumable and inspectable.
- Messagebox: A per-job/per-workspace/per-session message feed (append-only) with "tail/follow" semantics.

## Proposed Features

### 1) Stateful Mode (Workspaces as a Headline Feature)

#### User stories

- As a user, I can run a job with a persistent workspace in one command.
- As a user, I can create a long-lived interactive sandbox that always mounts the same workspace at `/work`.
- As a user, I can avoid re-cloning and re-downloading dependencies between runs (repo lives in `/work/repo` when a workspace is mounted).

#### UX requirements (CLI)

Add first-class stateful flags to job workflows:

- `agentlab job run ... --workspace <id|name>`
- `agentlab job run ... --workspace new:<name> --workspace-size 80G --workspace-storage local-zfs`
- `agentlab job run ... --workspace <id|name> --workspace-wait 10m` (wait until available instead of failing fast)

Add lightweight "stateful mode" shorthand:

- `agentlab job run ... --stateful` as an alias for "use a workspace" with sensible defaults.
- `agentlab sandbox new ... --stateful` optionally creates and attaches a workspace automatically.

#### Behavioral requirements

- If a workspace is attached, `agent-runner` should keep using `/work/repo` (already true today) to make state durable.
- Workspaces remain durable across sandbox destroy. On destroy, AgentLab must detach the workspace (already true via `SandboxManager` detach-on-destroy).
- If a user requests a workspace by name and it does not exist, `new:<name>` should create it (or `--workspace-create`).
- If the workspace is currently in use, `--workspace-wait` should block up to the timeout and then fail with a clear error.

#### Control API requirements

Avoid race conditions between job creation and sandbox selection. This implies one of:

- Extend `POST /v1/jobs` to include workspace selection and server-side sandbox pre-allocation for stateful jobs.
- Or introduce a new atomic endpoint that creates: workspace (optional) + sandbox record + job, then starts orchestration.

Proposed API shape (incremental, minimal new surface):

```json
// V1JobCreateRequest (new fields)
{
  "repo_url": "https://github.com/org/repo.git",
  "ref": "main",
  "profile": "yolo-workspace",
  "task": "run the test suite",
  "mode": "dangerous",
  "ttl_minutes": 120,
  "keepalive": false,

  "workspace": "workspace-alpha",
  "workspace_create": {
    "name": "workspace-alpha",
    "size_gb": 80,
    "storage": "local-zfs"
  },
  "workspace_wait_seconds": 600
}
```

Notes:

- `workspace` accepts ID or name.
- If `workspace_create` is provided, server creates the workspace if missing and errors if it already exists (or `create_if_missing` can be added later).
- If `workspace` is provided, server should pre-create a sandbox record with `workspace_id` and attach it to the job record before starting the orchestrator, so the orchestrator never races to allocate a different sandbox.

#### Acceptance criteria

- One command can create and run a workspace-backed job with a persistent `/work` mount.
- No race exists where a job can end up bound to the "wrong" sandbox when stateful flags are used.
- Clear errors exist for workspace not found, already attached, or timeout while waiting.

---

### 2) Workspace Safety Primitives

Persistent state needs safety rails. This section proposes four primitives.

#### 2.1 Workspace lock/lease (single-writer semantics)

Even if "attached to a VM" is already single-writer, users need a stronger coordination story:

- Waiting semantics (`--wait`) instead of "conflict, try again"
- A lock that survives transient attach/detach churn
- A way to detect "stale owner" and recover safely

Proposed behavior:

- Acquiring a workspace for stateful job/session obtains a lease: `(owner, expires_at)`.
- Lease is renewed periodically while in use.
- If lease is held by someone else, callers can wait (`--wait`) or fail immediately.
- Lease is cleared when the owning session/job ends, or when it expires.

Proposed data model (SQLite, new fields on `workspaces`):

- `lease_owner` (string, nullable)
- `lease_expires_at` (timestamp, nullable)
- `lease_nonce` (string, nullable, random token to prevent accidental release by non-owner)

Acceptance criteria:

- Concurrent attempts to use the same workspace do not cause thrashing or ambiguous state.
- "Wait for workspace" works across processes and survives daemon restart.

#### 2.2 Workspace snapshots and restore

Workspaces need a safe "checkpoint and revert" workflow independent of sandboxes.

Proposed commands:

- `agentlab workspace snapshot create <workspace> --name <snap>`
- `agentlab workspace snapshot list <workspace>`
- `agentlab workspace snapshot restore <workspace> --name <snap>`

Safety requirements:

- Default: snapshots and restore require the workspace to be detached.
- Optional: allow snapshot while attached only with an explicit flag and a documented consistency model.
- Restore should be explicit and should record an event.

Implementation constraints:

- Snapshot support is storage-specific. First supported target should be ZFS-backed volumes (for example `local-zfs`).
- Unsupported storages must return a clear "unsupported" error, not silent no-ops.

Proposed data model:

- New table `workspace_snapshots` with: `id`, `workspace_id`, `name`, `backend_ref`, `created_at`, `meta_json`.

Acceptance criteria:

- Users can create a named snapshot, list snapshots, and restore deterministically.
- Snapshot/restore is auditable via events.

#### 2.3 Workspace fork (clean-room copy)

Fork enables experimentation and "one sprite per branch" without trampling an existing workspace.

Proposed commands:

- `agentlab workspace fork <workspace> --name <new-name> [--from-snapshot <snap>]`

Behavior:

- By default, fork is from the latest state, requiring the workspace to be detached.
- If `--from-snapshot` is used, fork is from that snapshot.

Acceptance criteria:

- Fork produces an independent workspace volume and DB record.
- Fork does not mutate the source workspace.

#### 2.4 Workspace health check and fsck helper

Users need recovery workflows when a workspace becomes inconsistent (dirty FS, missing volume, DB drift).

Proposed commands:

- `agentlab workspace check <workspace>` (DB <-> Proxmox reconciliation and invariants)
- `agentlab workspace fsck <workspace> [--repair]` (safe by default, repair behind explicit flag)

Behavior:

- `check` validates invariants and prints actionable remediation steps.
- `fsck` defaults to read-only checks when possible; repair requires explicit opt-in.
- If a safe host-level fsck is not portable across storages, `fsck` can provision a dedicated "maintenance sandbox" to run checks in a controlled environment.

Acceptance criteria:

- Common failure modes produce clear next actions (detach first, missing volume, stale attached_vmid, VM missing, etc.).

---

### 3) Messagebox (Built-in Shared Mailbox/Memory)

#### Goal

Provide a simple append-only message feed per job/workspace/session so multi-agent coordination does not require external Slack/Discord hacks.

#### API requirements

New endpoints (local control API over Unix socket):

- `POST /v1/messages`
- `GET /v1/messages?scope_type=job&scope_id=<id>&after_id=<n>&limit=<n>`

Optional convenience endpoints:

- `GET /v1/jobs/{id}/messages?...`
- `GET /v1/workspaces/{id}/messages?...`
- `GET /v1/sessions/{id}/messages?...`

Proposed message shape:

```json
{
  "id": 123,
  "ts": "2026-02-06T12:00:00Z",
  "scope_type": "workspace",
  "scope_id": "workspace-0123abcd",
  "author": "agent:reviewer",
  "kind": "note",
  "text": "I updated the dependency graph; please rerun tests.",
  "json": { "links": ["..."] }
}
```

#### CLI requirements

- `agentlab msg post --workspace <id|name> --kind note --text "..."`
- `agentlab msg tail --workspace <id|name>`
- `agentlab msg tail --job <id>`
- `agentlab msg tail --session <id>`

#### Storage requirements (SQLite)

New table `messages`:

- `id` integer primary key
- `ts` timestamp
- `scope_type` (enum: job, workspace, session)
- `scope_id` string
- `author` string (optional)
- `kind` string (optional)
- `text` string (optional)
- `json` string (optional)

#### Acceptance criteria

- Two separate processes can coordinate via Messagebox without any external system.
- `tail` can follow messages incrementally using `after_id`.
- Messages are durable and auditable (basic retention policy is configurable later).

---

### 4) Session Semantics (Resume, Diagnose, Branch Ergonomics)

#### Goal

Make "resume a session and diagnose it" a first-class workflow. Users should be able to treat sessions like lightweight branches: pause/resume, fork, snapshot, inspect.

#### Proposed minimal session model

A session is a durable record that points at:

- `workspace_id` (required)
- `current_vmid` (optional)
- `profile` (default profile for new sandboxes)
- `branch` (optional string, used for naming and ergonomics)
- `created_at`, `updated_at`, `meta_json`

This allows:

- A session can exist even if no VM is running.
- `resume` can rebind the workspace into a new sandbox and set `current_vmid`.

#### CLI requirements

Session lifecycle:

- `agentlab session create --name <name> --profile <profile> --workspace new:<name> --size 80G`
- `agentlab session list`
- `agentlab session show <session>`
- `agentlab session resume <session> [--ttl 4h] [--keepalive]`
- `agentlab session stop <session>` (stop/destroy current sandbox, keep workspace)
- `agentlab session fork <session> --name <new-name> [--from-snapshot <snap>]`

Branch ergonomics:

- `agentlab session branch <branch>` creates or switches to a session named for the branch.
- `agentlab job run --branch <branch> ...` is an alias for session-backed runs (exact behavior defined below).

#### Behavior options (choose one; implement incrementally)

Option A (minimal, robust): "Resume always provisions a fresh sandbox"

- `session resume` uses existing `workspace rebind` behavior under the hood.
- Session stores only workspace and latest sandbox reference.
- Jobs remain separate and can target the session workspace by `--workspace`.

Option B (advanced): "Resume the same sandbox"

- Requires sandbox start/stop and tighter coupling between jobs and a specific running VM.
- Enables truly long-running agent processes inside a single VM.

This PRD recommends Option A first for correctness and simplicity, and Option B as a later enhancement.

#### Sandbox pause/resume and snapshots (future-compatible)

Proposed new sandbox operations:

- `agentlab sandbox pause <vmid>` (Proxmox suspend)
- `agentlab sandbox resume <vmid>` (Proxmox resume)
- `agentlab sandbox snapshot save <vmid> --name <snap>`
- `agentlab sandbox snapshot restore <vmid> --name <snap>`

Notes:

- VM snapshots may include the workspace disk depending on Proxmox behavior. Default safety rule should be conservative (require no workspace attached, or require explicit confirmation).

#### Diagnosis ("doctor") requirements

Add a doctor workflow that bundles the information needed to debug a sandbox/session quickly.

Proposed commands:

- `agentlab sandbox doctor <vmid> --out <path>`
- `agentlab job doctor <job_id> --out <path>`
- `agentlab session doctor <session> --out <path>`

Bundle contents (minimum):

- Current DB records (job, sandbox, workspace, session)
- Recent events (job and sandbox)
- Proxmox runtime status and config (via backend)
- Artifact inventory and optionally the latest artifact bundle

Acceptance criteria:

- Doctor produces a single deterministic bundle file that can be attached to an issue.
- Doctor never includes secrets (redaction rules apply).

---

### 5) One-Command Onboarding and Golden Profiles

#### Goal

Reduce "operator yak shave" by providing a guided `agentlab init` that gets a Proxmox host into a known-good state without manually editing a pile of infra config.

#### CLI requirements: `agentlab init`

Proposed behavior:

- `agentlab init` runs a checklist and prints a summary of what is missing.
- `agentlab init --apply` executes the safe, reversible steps automatically (network bridges, nftables rules, snippet dir, etc.).
- `agentlab init --smoke-test` provisions a small sandbox/job to validate end-to-end.

Implementation should reuse existing scripts where possible:

- `scripts/net/setup_vmbr1.sh`
- `scripts/net/apply.sh`
- `scripts/net/setup_tailscale_router.sh` (optional)
- `scripts/create_template.sh`
- `scripts/net/smoke_test.sh`

Acceptance criteria:

- A new Proxmox host can reach "first successful job" with minimal manual steps.
- `--apply` is explicit; default mode is read-only diagnostics.

#### Golden profiles

Ship an opinionated set of profile YAMLs aligned with real workflows. The goal is to stop forcing users to invent profiles before they have learned the system.

Proposed baseline profiles:

- `fast-dev` (quick provisioning, smaller resources, moderate TTL)
- `safe-untrusted` (tighter defaults, inner sandbox enabled if available)
- `stateful-dev` (workspace-friendly defaults, longer TTL optional)
- `long-running-session` (keepalive defaults, explicit TTL, session-friendly)

Acceptance criteria:

- Profiles are installed by default (or easily copied) and documented as "start here" choices.

## Cross-Cutting Requirements

### Security and data safety

- No secrets in doctor bundles, message streams, or snapshot metadata.
- Workspace operations must be explicit and auditable.
- Destructive operations must require explicit confirmation or flags: restore, fsck repair, delete, rollback.

### Observability and metrics

Add (or extend) metrics for:

- Workspace lock contention and wait time
- Snapshot operations (count, duration, failures)
- Doctor bundle creation (count, duration, size)
- Session create/resume/stop counts
- Messagebox throughput (messages posted, tail usage)

### Documentation

Add docs pages for:

- "Persistent state model": sandbox vs workspace vs session
- "Stateful mode" tutorials and best practices
- Safety guides: snapshots, forks, fsck, recovery playbooks
- Messagebox usage patterns for multi-agent teams
- `agentlab init` runbook and troubleshooting

## Milestones and Task List

This is organized as P0/P1/P2. Exact ordering can shift based on feedback and implementation complexity.

### P0: Make Stateful Safe and Ergonomic

- [ ] Add server-side support for stateful jobs (atomic job + workspace selection, no races)
- [ ] Extend CLI `agentlab job run` with `--workspace`, `--stateful`, and `--workspace-wait`
- [ ] Implement workspace lease/lock semantics in SQLite and `WorkspaceManager`
- [ ] Implement `workspace check` (DB <-> Proxmox invariants)
- [ ] Implement `sandbox doctor` (bundle: db records, events, proxmox status/config, artifacts)
- [ ] Add docs: "Stateful mode" and mental model overview
- [ ] Add tests for workspace lock contention and job creation semantics

### P1: Snapshots, Forks, Messagebox, Sessions (Option A)

- [ ] Extend proxmox backend interface to support workspace snapshots and clones (ZFS first)
- [ ] Add `workspace snapshot create/list/restore`
- [ ] Add `workspace fork` (clone from current or snapshot)
- [ ] Add `workspace fsck` helper (read-only by default; repair behind explicit flag)
- [ ] Add SQLite `messages` table and Control API endpoints for Messagebox
- [ ] Add CLI `agentlab msg post/tail`
- [ ] Add SQLite `sessions` table and minimal session commands: create/list/show/resume/stop/doctor
- [ ] Add docs: snapshots/forks/recovery, Messagebox, sessions
- [ ] Add migration tests for new tables/columns

### P2: Pause/Resume, Sandbox Snapshots, Onboarding Appliance

- [ ] Extend proxmox backend interface with VM suspend/resume
- [ ] Add CLI `sandbox pause/resume` and record events
- [ ] Add CLI `sandbox snapshot save/restore` (with conservative safety rules for attached workspaces)
- [ ] Implement `agentlab init` (read-only checks + `--apply` + smoke test)
- [ ] Ship golden profile pack and update install scripts to place them in `/etc/agentlab/profiles`
- [ ] Add docs: init flow, golden profiles, "first run" tutorial

### Future (Nice-to-have)

- [ ] Messagebox connectors (Slack/Discord/Linear) as optional adapters
- [ ] Session Option B (resume the same sandbox reliably, long-running agent processes)
- [ ] Guest-assisted consistent snapshots (qemu guest agent fsfreeze, if feasible)
- [ ] Retention policies and export/import for messages and session metadata

## Open Questions

- Should sessions be a pure CLI abstraction initially, or persisted in SQLite from day one?
- What storage backends should workspace snapshot/fork support beyond ZFS, and how do we detect capability safely?
- What is the exact safety model for snapshotting a workspace while attached to a running VM?
- Should Messagebox be local-only (Unix socket) or also exposed to guests (bootstrap token auth), and under what constraints?
- For `agentlab init`, what steps are safe to auto-apply vs only print as commands (operator trust and reversibility)?
