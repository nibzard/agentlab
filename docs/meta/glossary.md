# Glossary

The terms defined here are used across the AgentLab documentation. Each entry is grounded in the source the docs are generated from.

## A

agent subnet
:   The CIDR network that carries sandbox traffic on bridge `vmbr1`. The default is `10.77.0.0/16` with host address `10.77.0.1`. Guests reach the bootstrap and artifact listeners on this subnet.

agentlab
:   The user-facing command-line client for `agentlabd`. It talks to the daemon over a Unix socket (local) or an HTTP endpoint (remote), and is the single supported surface for users and automation.

agentlabd
:   The long-running host daemon that owns all Proxmox and policy access. It exposes the local Unix-socket control plane plus mandatory bootstrap and artifact listeners, and optional control and metrics listeners, and drives the sandbox, job, workspace, session, exposure, and secrets managers.

agentlab-dashboard
:   An optional binary that serves a browser UI and proxies requests to `agentlabd` over its Unix socket.

agentlab-ssh-gateway
:   A binary built behind the `sshgateway` build tag. It is excluded from releases and is not installed by `install.sh`.

age
:   The encryption tool AgentLab uses to encrypt secrets bundles at rest. The private key lives at `secrets_age_key_path`, default `/etc/agentlab/keys/age.key`.

air-gapped mode
:   See **offline mode**.

artifact
:   A file or tar bundle a job produces and uploads to the daemon through the artifact listener. The default bundle name is `agentlab-artifacts.tar.gz`.

artifact listener
:   The guest-only HTTP listener that receives artifact uploads at `POST /upload`. The default address is `10.77.0.1:8846`.

artifact token
:   A scoped, time-limited token a guest uses to upload artifacts. The default lifetime is `artifact_token_ttl_minutes` (1440 minutes, or 24 hours) and the cap is `artifact_max_bytes` (256 MiB).

## B

backend
:   The integration mode the daemon uses to manage VMs. Proxmox backends are `api` (HTTP API token, recommended) or `shell` (`qm` and `pvesh`, the code default). `docker` and `libvirt` are selectable as sandbox backends. See [Shell versus API backend](../explanation/shell-vs-api-backend.md).

bootstrap listener
:   The guest-only HTTP listener that serves bootstrap fetch, metadata, runner reports, and the integration proxy. The default address is `10.77.0.1:8844`.

bootstrap token
:   A one-time, short-lived (10-minute, `defaultBootstrapTTL`) token bound to a VMID. A guest presents it once to fetch its secrets bundle, job config, and artifact upload instructions.

bundle
:   See **secrets bundle**.

## C

cloud-init snippet
:   The per-sandbox cloud-init configuration the daemon writes to drive guest bootstrap. The Tailscale Admin API key is never written to it.

control plane
:   The versioned JSON API `agentlabd` serves. The base path is `/v1`, content type is `application/json`, and timestamps use RFC3339Nano. It runs on the local Unix socket by default and optionally on a TCP listener for remote clients. See [HTTP API reference](../reference/http-api.md).

control token
:   The bearer token (`control_auth_token`) the remote TCP control listener requires. The CLI sends it as `--token`. Mandatory whenever `control_listen` is set.

## D

doctor
:   A diagnostic bundle the CLI gathers for a sandbox, session, or job (`sandbox doctor`, `session doctor`, `job doctor`). The bundle is redacted for support.

## E

event contract
:   The shared event envelope (`kind`, `schema_version`, `stage`, `payload`) the daemon emits for sandbox, workspace, session, job, and exposure changes. See [Event contract](../reference/event-contract.md).

exposure
:   A host-owned Tailscale Serve endpoint that publishes a sandbox port on the tailnet. Names default to `sbx-<vmid>-<port>`, and every expose and unexpose emits an audit event.

## F

firewall group
:   A Proxmox security group that a profile **network mode** maps to. `off` maps to `agent_nat_off`, `nat` maps to `agent_nat_default`, and `allowlist` maps to `agent_nat_allowlist`.

## I

idle stop
:   A daemon feature that stops sandboxes with no SSH activity after the idle threshold. Enabled by default (`idle_stop_enabled: true`), polling every `idle_stop_interval` (1m), with a default idle window of `idle_stop_minutes_default` (30). See [Idle stop and lease model](../explanation/idle-stop-and-lease-model.md).

integration
:   An LLM, Git, or HTTP provider registered at `/v1/integrations`, with an optional credential proxy at `/proxy/`. The integrations system and its credential shapes are not yet documented.

## J

job
:   A record that clones a repository into a sandbox, runs a task, collects artifacts, and tears the sandbox down unless `--keepalive` is set. See [Run your first job](../tutorials/run-first-job.md).

## K

keepalive
:   A sandbox or job flag (`--keepalive`) that opts into a renewable lease instead of automatic stop or destroy on TTL expiration. Leases can be renewed only while the sandbox is `RUNNING`.

## L

lease
:   See **TTL and lease**.

## M

message
:   An entry in the shared messagebox scoped to a job, workspace, or session. Posted and tailed with `agentlab msg post` and `agentlab msg tail`.

metrics listener
:   An optional, loopback-only listener that serves Prometheus exposition at `GET /metrics`. Disabled by default (`metrics_listen` empty).

network mode
:   A profile's `network.mode` value: `off`, `nat` (default), or `allowlist`. Each maps to a firewall group.

## O

offline mode
:   A daemon mode set by the `-offline` flag or `offline: true` in config. It blocks outbound HTTP to public addresses (allowing only loopback, link-local, RFC1918, and `fc00::/7`), disables the Tailscale exposure publisher, and rejects `letsencrypt` TLS. See [Run AgentLab air-gapped](../how-to/run-air-gapped-offline.md).

## P

profile
:   A YAML file in `profiles_dir` (default `/etc/agentlab/profiles`) that selects a template, resources, a network mode, and behavior overrides for a sandbox. Required fields are `name` and `template_vmid`. See [Author a profile](../how-to/author-a-profile.md) and the [Profile and template schema](../reference/profile-and-template-schema.md).

reconciler
:   A loop that runs every 30 seconds (`defaultLeaseGCInterval`) to reconcile existing database rows against Proxmox status, mark stale entries destroyed, and advance stuck states toward `RUNNING`. It does not import orphaned VMs; orphan adoption is a separate inventory path. It survives daemon restarts. See [Reconciliation and shutdown](../explanation/reconciliation-and-shutdown.md).

resource pool
:   The daemon's view of total CPU cores and memory (`pool_total_cores`, `pool_total_memory_mb`, both `0` for unlimited) used for over-commit tracking through `/v1/pool/status`. The admission and rejection policy is not yet documented.

## S

sandbox
:   An isolated Proxmox VE VM that AgentLab provisions to run an AI coding agent. Its lifecycle runs `REQUESTED` to `PROVISIONING` to `BOOTING` to `READY` to `RUNNING`, then to `COMPLETED`, `FAILED`, `TIMEOUT`, or `STOPPED`, and finally `DESTROYED`. See the [State machine reference](../reference/state-machine.md).

schema_version
:   A version field in the API and event contract that clients discover through `GET /v1/schema` (also `agentlab schema`). Compatibility guarantees across releases are not yet documented.

secrets bundle
:   An age-encrypted file under `secrets_dir` (default `/etc/agentlab/secrets`) that carries env, git, SSH key, and Tailscale enrollment secrets. Bundle format is version 1. See [Secrets reference](../reference/secrets.md).

session
:   A binding of a workspace to a profile that gives a resumable, stateful development loop across sandbox VMs. See [Run a stateful dev session](../tutorials/stateful-dev-session.md).

sops
:   An alternate encryption backend. Existing `.sops.*` bundles can be read and validated, but in-place writes are not supported.

tailnet
:   A Tailscale network that carries remote control-plane and SSH traffic and publishes host-owned sandbox exposures through Tailscale Serve. See [Enable remote tailnet access](../tutorials/enable-remote-tailnet-access.md).

template
:   A cloneable Proxmox VM with `qemu-guest-agent` enabled. Profiles clone a template to make sandboxes. See [Build a VM template](../tutorials/build-a-vm-template.md).

tmpfs
:   The in-memory filesystem (`/run/agentlab/secrets`) where the guest receives secrets. It is wiped on stop.

TTL and lease
:   A sandbox time-to-live. The default TTL comes from the profile. On a keepalive sandbox the lease can be renewed while `RUNNING`; otherwise TTL expiration stops and destroys the sandbox.

## U

Unix socket
:   The trusted local control path at `socket_path`, default `/run/agentlab/agentlabd.sock`. The local socket bypasses the remote auth wrapper.

user and team
:   Resources exposed at `/v1/users` and `/v1/teams` for multi-user SSH key support. The multi-user and team model, including single-user versus multi-user mode and RBAC scope, is not yet documented.

## V

VMID
:   The Proxmox numeric VM identifier (for example `1020`) that AgentLab uses as the sandbox primary key.

vmbr1
:   The Proxmox Linux bridge that carries the agent subnet and isolates sandboxes from the host LAN. See [Network isolation model](../explanation/network-isolation-model.md).

## W

workspace
:   A persistent storage volume you can attach to and detach from ephemeral sandbox VMs so state survives VM teardown. Created on a Proxmox storage pool, default `local-zfs`. See [Use workspaces and rebind](../how-to/use-workspaces-and-rebind.md).
