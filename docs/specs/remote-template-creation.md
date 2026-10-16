# Remote template creation through AgentLab

Status: implemented; [live acceptance verified on pve2](remote-template-creation-acceptance.md)

Date: 2026-10-15

## Problem and outcome

An operator connected from macOS or Linux must be able to build a Proxmox VM
template through the existing AgentLab control-plane connection. The client
submits a build, follows its progress, and later selects the successful template
for a profile. Image download, customization, and Proxmox operations run on the
daemon host. No client-side Proxmox tools, repository checkout, or SSH session
are required.

Today, `scripts/create_template.sh` performs the build on the Proxmox host.
`agentlab init --apply` creates missing templates but skips existing ones.
`agentlab bootstrap` installs and configures a remote host; it is not a template
build interface. The standalone `cmd/template` helper prints instructions and
is not connected to the main CLI or daemon.

In the motivating live test, `ubuntu-24-04-ai` referenced template 9999. A new
sandbox booted but contained no Tailscale executable. A supported build workflow
must make installed guest capabilities visible before a profile uses a template.

## Scope and decisions

The first release supports Ubuntu 24.04 amd64 cloud-image templates on the
Proxmox host running `agentlabd`, using local host tools. It works over the Unix
socket and authenticated remote endpoint. A configured Proxmox API backend does
not itself provide image customization; the feature still requires local build
tools and local-node storage access. Unsupported hosts return an explicit
capability error before allocating resources.

Builds are asynchronous, durable operations distinct from agent jobs. A client
disconnect does not cancel a build. New builds always use unused VMIDs; there is
no overwrite or in-place rebuild option. Existing templates, profiles, and
sandboxes remain intact until the operator explicitly activates a result.

Excluded from this release: LXC/Docker/libvirt templates, arbitrary shell hooks,
uploaded build scripts or disk images, distributed scheduling, cross-architecture
builds, template deletion, and general-purpose remote execution. Automatic
Tailscale enrollment for standalone sandboxes is a related dependency described
below, not an implied result of installing Tailscale in an image.

## Operator workflow

Proposed commands, executed on a laptop using the saved endpoint and token:

```bash
# Inspect daemon build support and host prerequisites.
agentlab template capabilities

# Validate the plan without downloading, allocating a VMID, or writing files.
agentlab template validate --name ubuntu-2404-agentlab --storage local-zfs

# Submit a build and wait for completion while displaying progress.
agentlab template create --name ubuntu-2404-agentlab \
  --storage local-zfs --bridge vmbr1 --wait

# Reconnect to an existing build; IDs below are examples.
agentlab template build list
agentlab template build show tb_example
agentlab template build logs tb_example --follow
agentlab template build wait tb_example --timeout 45m
agentlab template build cancel tb_example

# Inspect successful managed templates and activate one for future sandboxes.
agentlab template list
agentlab template show 9010
agentlab template activate 9010 --profile ubuntu-24-04-ai \
  --expected-template-vmid 9999
```

All commands honor existing `--endpoint`, `--token`, `--socket`, and `--json`
behavior. `create` returns immediately unless `--wait` is supplied. Its first
output includes the durable build ID and target VMID. The global HTTP timeout
limits individual requests; build execution has its own daemon-enforced timeout.
`--wait-timeout` limits client waiting and never cancels the server operation.
Ctrl-C stops waiting or log following, and prints a reconnect command.

JSON create/show/list responses are single JSON documents. Followed logs use
documented NDJSON records with sequence, UTC timestamp, stage, and message.
Wait returns exit 0 for success, 1 for failure/cancellation/wait timeout, and 2
for invalid arguments, consistent with the existing CLI exit-code categories.

## Build inputs and validation

The initial request supports these fields:

| Field | Behavior |
| --- | --- |
| `name` | Required; lowercase letters, digits, and hyphens; 1–63 characters. |
| `vmid` | Optional positive integer in the configured template range; otherwise allocated. |
| `recipe` | Default `ubuntu-24.04-agentlab-v1`; selected from daemon-owned recipes. |
| `storage` | Required enabled local-node disk storage supporting the selected image import. |
| `cloudinit_storage` | Defaults to `storage`; checked for cloud-init support. |
| `bridge` | Defaults to `vmbr1`; must exist on the local node. |
| `memory_mb`, `cores`, `disk_size_gb` | Defaults 4096, 2, 40; positive and within configured host limits. |
| `image_url`, `image_sha256` | Optional override pair; both required together. |
| `agent_tools` | Default true; installs recipe-pinned agent CLIs when enabled. |
| `timeout_seconds` | Default 3600, maximum 7200; host configuration may lower the maximum. |
| `request_id` | Client-generated UUID used for idempotent submission. |

The recipe owns package lists, upstream repositories, guest scripts, and tool
versions. Callers cannot provide command fragments, local file paths, package
manager options, or arbitrary script flags. Future recipe customization needs a
separate reviewed contract. Disabling agent tools still installs the runner,
SSH server, QEMU guest agent, jq, curl, and Tailscale.

For the default image, resolve Ubuntu's published checksum manifest for the
exact downloaded filename; record both source and resolved SHA-256. Do not
accept a download without successful checksum verification. Overrides require
HTTPS, an explicit digest, and a hostname allowed by host policy. Reject
embedded credentials, unsafe redirects, and destinations resolving to loopback,
link-local, private, or metadata addresses, including after redirects. The
capabilities response advertises permitted image hosts and input limits.

Validation checks installed `qm`, `virt-customize`, download/checksum tools,
KVM/libguestfs access, immutable recipe assets, storage state, bridge, VMID
availability, and estimated disk headroom. Report errors and warnings with
stable codes and fields. Never silently install host prerequisites or modify
host networking. Build requests repeat checks under reservation; validation
does not promise later availability.

## HTTP contract

Routes use existing control-plane authentication, JSON limits, redaction, and
error envelopes. Creation accepts `Idempotency-Key` matching `request_id`.

| Method and route | Result |
| --- | --- |
| `GET /v1/templates/capabilities` | Host support, recipes, defaults, limits, prerequisite failures. |
| `POST /v1/templates/validate-plan` | Normalized plan, warnings, validation errors; no mutation. |
| `POST /v1/template-builds` | `202 Accepted`, build record, and `Location` header. |
| `GET /v1/template-builds` | Paginated builds, optionally filtered by state. |
| `GET /v1/template-builds/{id}` | Durable state, stage, plan, timestamps, result, error. |
| `GET /v1/template-builds/{id}/logs?after=N&limit=M` | Ordered log records and next cursor; CLI polls for follow. |
| `POST /v1/template-builds/{id}/cancel` | Idempotent cancellation request and current state. |
| `GET /v1/templates` | Paginated successful AgentLab-managed templates. |
| `GET /v1/templates/{vmid}` | Manifest plus current Proxmox existence/drift check. |
| `POST /v1/templates/{vmid}/activate` | Atomically update one existing profile with an expected old VMID. |

Stable errors include `template_build_unsupported`, `template_prerequisite_missing`,
`template_vmid_conflict`, `template_storage_unavailable`,
`template_checksum_mismatch`, `template_build_failed`, `template_build_timeout`,
`template_build_interrupted`, `template_cleanup_required`,
`template_profile_conflict`, and `template_capability_missing`.
Use 400 for malformed input, 403 for authorization, 404 for absent resources,
409 for conflicts, 422 for invalid plans or unavailable prerequisites, and 503
for unavailable workers/backends. Errors name the failed stage and a useful
next action without exposing credentials or complete environment dumps.

The same idempotency key and normalized request return the original build;
reusing the key with different input returns 409. Generate the key before the
first submission so a lost response can be retried safely.

## Execution and persistence

Add a `TemplateBuildManager`, persistent build/manifest records, and ordered
build logs. Default to one active build per host; accepted work waits in a
bounded queue (default 10 pending requests). Queue saturation returns 503 with
a retry hint. Persist the request before returning 202.

States: `QUEUED -> RUNNING -> SUCCEEDED | FAILED | CANCELLED`.
`cancel_requested` is separate durable metadata. Running stages are
`preflight`, `download`, `customize`, `import`, `verify`, `boot_verify`, and `finalize`.
Only `SUCCEEDED` creates a selectable managed template.

Persist normalized inputs, caller identity, recipe/asset version and digest,
resolved image digest, reserved VMID, worker unit name, timestamps, exit status,
error code, cleanup status, and manifest reference. Log records have monotonic
per-build sequence numbers. Emit audit events for submission, stage changes,
cancellation, completion, cleanup failure, and activation using existing event
contracts with a build ID payload.

Reserve VMIDs across builds and sandbox allocation, and consult actual Proxmox
inventory. Coordinate reservations through a shared allocator rather than two
independent checks. Recheck before `qm create`; external races fail without
replacing or deleting another resource. Tag created resources with build
ownership and persist each allocation before subsequent steps.

On daemon startup, keep queued work and reconcile running records with worker
units. Reattach to running workers and collect completed unit results. If a
worker disappeared or its result is uncertain, mark the build failed with
`template_build_interrupted`; never repeat customization/import blindly.
Successful recovery requires verification of all recorded resources. A new
retry is a new build with a new target VMID.

## Worker, assets, and host service

Run a fixed host-installed worker in a dedicated systemd unit per build. Use
structured arguments/configuration, restrictive build-directory permissions,
a fixed environment, time/resource limits, and process-group cancellation.
Do not execute client-supplied shell text. The API process launches only this
trusted worker; disconnects do not control its lifetime.

Refactor the reusable stages of `scripts/create_template.sh` behind the worker,
preserving the manual script's interface. Keep one implementation for guest
contents. Add structured progress output and explicit ownership-aware cleanup
instead of parsing human log text or assuming a script trap handled failure.

Embed versioned recipe and guest assets in the daemon distribution and extract
them to immutable digest-addressed directories under
`/var/lib/agentlab/template-assets`. Pin each build to its asset digest across
daemon upgrades. The feature must work from the normal GoReleaser distribution
without a source checkout. Preserve the existing archive naming contract.

Store working files and manifests under `/var/lib/agentlab/template-builds`;
use the existing image cache under `/var/lib/agentlab/images`. Cache by content
digest, verify before reuse, and lock shared downloads. Failed downloads remain
temporary and are never treated as valid cache entries. Do not enroll the base
image into Tailscale or copy host credentials, private keys, bootstrap tokens,
or machine-specific Tailscale state into it.

Define the worker unit's minimum required filesystem/device/privilege access
and test it on Proxmox. Account for libguestfs/KVM, Proxmox IPC, storage imports,
and `/var/log/pve/tasks`; do not disable daemon filesystem protection globally.
Install prerequisites explicitly through host setup documentation. Service
failures must identify missing paths or capabilities in operator diagnostics.

## Verification and cleanup

Before finalization, inspect the customized image for required executables and
enabled services, record installed versions, and clear image identity/enrollment
state. Verify imported disk, cloud-init drive, boot configuration, QEMU guest
agent setting, and final Proxmox template flag. A success manifest includes
`tailscale`, `agent_runner`, `ssh`, `qemu_guest_agent`, and optional agent CLI
versions. Installation checks do not claim successful network enrollment.

Cancellation terminates the worker and children, waits for active Proxmox tasks
to settle, then removes only build-owned incomplete resources and working files.
Failures perform the same cleanup. Keep diagnostic logs and valid shared image
cache entries. Never remove an existing VM/template or an unowned disk based
only on a matching name/VMID. Cleanup failures preserve resource identifiers and
set `cleanup_required=true` with manual recovery guidance; do not report success.

Default to 30 days of terminal-build logs, capped at 50 MiB per build with an
explicit truncation marker. Keep successful manifests while templates exist.
Retention never deletes Proxmox templates or active-build data. Protect file
paths against traversal/symlink attacks and redact worker output before storage
and API delivery.

## Profile activation

Creation never changes a profile automatically. Activation requires a verified,
successful managed template on the supported node and a VM-type profile.
Check disk/resource compatibility and template drift again. Require
`expected_template_vmid`; a changed profile returns 409.

Serialize activation with profile loading and use atomic file replacement in
`profiles_dir`, preserving every other field and other YAML documents. Parse
and validate the full resulting profile set before publishing it. Swap the
daemon's in-memory profile snapshot without restart. Track file content hashes
to reject conflicting external edits. A failed write/reload leaves the previous
profile active. Emit an audit record containing old and new VMIDs.

Allow writes only to the configured profiles directory through the service's
explicit write policy. Implement activation as a narrow profile-store operation,
not a generic remote file editor. New sandbox requests capture one coherent
profile revision. Existing sandboxes and linked clones retain their original
template. Rollback activates a previously managed template with the same
compare-and-swap check. Unmanaged-template adoption is deferred.

## Authorization and compatibility

Add `template.read`, `template.validate`, `template.create`, `template.cancel`,
and `template.activate` permissions. The `template` command namespace grants
them using existing permission matching. Reject sandbox-scoped tokens for all
template routes, since templates are host-wide resources. Preserve current
trusted local/legacy bearer behavior and require normal remote authentication.
Enforce permissions in API handlers, not only the CLI.

Extend API discovery/schema and CLI help. An older daemon yields an actionable
unsupported-feature message. Existing scripts, `init`, sandbox provisioning,
module path, and release archive contract remain compatible. Mark
`cmd/template` as an instruction generator so its apparent command syntax is
not confused with the implemented workflow.

## Tailscale dependency and completion boundaries

The follow-up [standalone sandbox bootstrap](standalone-sandbox-tailscale-bootstrap.md)
is implemented and live-verified independently of template installation. It
initializes secrets and redaction, attempts enrollment, and reports failures
before optional job execution. The base image remains unenrolled; each fresh
clone initializes its own identity. A configured key alone is not evidence of
successful enrollment: verify the guest and daemon report a connected node.

## Implementation sequence

1. Add request/manifest types, config limits, migrations, shared VMID reservation,
   and state-transition tests.
2. Package immutable recipe assets and extract reusable build stages into the
   trusted worker. Add ownership tracking, capability checks, and image checksums.
3. Implement durable queueing, systemd worker lifecycle, logs, cancellation,
   timeout, cleanup, and restart reconciliation.
4. Add authorized HTTP routes, idempotency, schema discovery, CLI submission,
   polling/waiting, and log following.
5. Add profile activation with atomic persistence, conflict detection, coherent
   reload, and capability validation.
6. Update installer/service configuration, CLI/API/config/event references,
   template tutorial, and troubleshooting. Run the live acceptance workflow.

## Acceptance and test plan

Unit and integration tests must exercise meaningful failure boundaries:

- Invalid plans and denied tokens cause no downloads, allocations, or workers.
- Duplicate submission produces one build, including a lost-response retry.
- Concurrent builds and sandbox creation cannot reserve the same VMID.
- Missing tools/storage, insufficient disk, checksum failures, command failures,
  and unsafe image redirects yield stable errors and owned-resource cleanup.
- Disconnecting the client preserves execution; reconnecting resumes logs without
  omissions or duplicates, using cursors.
- Cancellation and timeout terminate children and do not destroy unrelated VMs;
  cleanup errors remain visible.
- Daemon restarts during download/import reconcile safely without duplicate work.
- Recipe assets remain pinned across daemon upgrades; release installation works
  without a checkout.
- An existing template cannot be overwritten. Failed builds never become
  selectable. Manifests distinguish installed capabilities from enrollment.
- Profile activation preserves unrelated YAML, rejects stale edits, reloads
  without restart, and leaves existing sandboxes unchanged.
- Requests, logs, manifests, images, and audit events contain no enrollment keys,
  host secrets, or inherited Tailscale identity.

Live acceptance on a designated Proxmox test host:

1. Install a normal daemon distribution and connect from macOS without SSH-based
   build execution or local Proxmox tools.
2. Validate and build into an unused VMID; disconnect and reconnect during build.
3. Confirm manifest versions, final template configuration, and an unregistered
   Tailscale installation.
4. Activate for a dedicated test profile; create a sandbox and verify DHCP, SSH,
   guest-agent access, runner availability, and `tailscale version`.
5. Verify `tailscale status`
   reports the new sandbox connected under its unique configured hostname with
   no assigned job. Test an invalid key and verify useful diagnostics.
6. Exercise one cancellation and one failed build; verify cleanup ownership.
   Remove only test-owned resources and retain existing production sandboxes.

The feature is complete when this workflow requires only AgentLab commands on
the client, survives disconnection, and produces a verified template that can
be selected for future sandboxes without replacing a template in use.

## Repository integration points

- `cmd/agentlab/main.go` and command files: command registration/help and HTTP client.
- `internal/daemon/api.go`, `api_types.go`, `authz.go`: routes, contracts, permissions.
- `internal/daemon/profiles.go`: profile parsing and coherent activation.
- `internal/db/`: build records, logs, manifests, migrations, reservations.
- `internal/proxmox/`: host inventory, import/template operations, task tracking.
- `scripts/create_template.sh` and `scripts/guest/`: shared recipes and guest assets.
- `scripts/install_host.sh`, `scripts/systemd/`, `.goreleaser.yaml`: installed worker,
  assets, and service access; preserve release naming.
- `docs/reference/` and `docs/tutorials/build-a-vm-template.md`: public contracts
  and the remote build workflow.
