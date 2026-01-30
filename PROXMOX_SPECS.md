# SPECS.md — AgentLab: Unattended Ephemeral “YOLO” Agent Sandboxes on Proxmox

> **Purpose:** Define a product-like system that reliably spins up, configures, runs, and tears down **ephemeral** (and optionally **long-running**) VM sandboxes for AI coding agents running unattended in “dangerous/YOLO” mode, while maintaining **full outbound Internet access** and minimizing blast radius on the host and LAN.

---

## 0) TL;DR for developers

- **Hypervisor:** Proxmox VE is the foundation (templates, linked clones, UI, backups).
- **Control plane:** `agentlabd` runs on the Proxmox host, owns the Proxmox API token, enforces policy, manages secrets, and provides a local HTTP API.
- **UX:** `agentlab` CLI/TUI talks to `agentlabd` over a Unix socket (preferred) or localhost HTTP.
- **Guest bootstrap:** VM template with cloud-init + a tiny `agent-runner` systemd service.
- **Security model:** Agent VMs are untrusted. They get:
  - **Full outbound Internet**
  - **No LAN access** (block RFC1918/ULA/link-local egress)
  - **No host mounts**
  - **Secrets injected into tmpfs only** (one-time fetch)
- **Persistence:** Optional “workspace disk” attached as a secondary virtual disk, allowing long-running environments without sacrificing “nuke root and rebind workspace” resetability.

---

## 1) Goals & non-goals

### Goals
1. **Unattended operation:** run agents in loops with minimal supervision.
2. **Ephemeral by default:** sandboxes are disposable; “reset” is destroy/recreate.
3. **Full outbound Internet:** apt/pip/npm/cargo + web/API calls must work.
4. **No LAN blast radius:** sandbox cannot reach local networks by default.
5. **Fast provisioning:** VM templates + linked clones; typical spin-up in seconds.
6. **Safe-ish secrets handling:** avoid persisting secrets in VM disks and in Proxmox snippet history.
7. **Developer-friendly:** clear API, deterministic state machine, reproducible installs.
8. **Long-running option:** allow sandboxes to persist, with lease/TTL controls.

### Non-goals
- Perfect isolation against a fully malicious actor with 0-days (impossible).
- Replacing Proxmox UI/backup system.
- Managing every possible agent tool; we provide a framework and profiles.
- LAN “research” targets (internal docs, private repos) by default—can be added as explicit allowlists later.

---

## 2) High-level concepts

### Entities
- **Template VM (`TEMPLATE`)**: a cloud-init enabled “golden image” with tooling + `agent-runner`.
- **Sandbox VM (`SANDBOX`)**: a clone/linked clone of TEMPLATE created for a job or a session.
- **Workspace Volume (`WORKSPACE`)**: optional persistent disk mounted at `/work`.
- **Profile (`PROFILE`)**: resource + network + secrets bundle + behavior policy.
- **Job (`JOB`)**: repo + task description + desired agent loop + output settings.
- **Lease (`LEASE`)**: TTL + keepalive for a sandbox; prevents accidental forever VMs.

### Modes
- **Ephemeral job-runner:** create → run → report → destroy.
- **Keep-alive session:** create → keep running → user can attach/ssh → destroy later.
- **Workspace-backed:** create ephemeral root + attach persistent workspace disk.

---

## 3) Architecture

### System diagram
```text
                 (trusted plane)                         (untrusted plane)
┌──────────────────────────────────┐            ┌───────────────────────────────────┐
│ Proxmox Host (PVE)               │            │ Agent Sandbox VMs (dangerous)     │
│                                  │            │                                   │
│  ┌───────────────┐               │            │  ┌─────────────────────────────┐  │
│  │ agentlabd     │<──Unix sock──►│ agentlab   │  │ agent-runner (systemd)      │  │
│  │ (daemon)      │               │ CLI/TUI    │  │  - fetch secrets (1-time)   │  │
│  │               │               │            │  │  - git clone/pull           │  │
│  │ - Proxmox API │               │            │  │  - run agent loop           │  │
│  │ - leases/TTL  │               │            │  │  - push PR/artifacts        │  │
│  │ - secrets svc │<──HTTP (10.77)┼────────────┼─►│  - report status            │  │
│  │ - NAT policy  │               │            │  └─────────────────────────────┘  │
│  └───────────────┘               │            │                                   │
│                                  │            └───────────────────────────────────┘
│  vmbr0: management/LAN           │
│  vmbr1: agent_nat (NAT to WAN)   │
└──────────────────────────────────┘
```

### Network policy diagram
```text
          vmbr0 (LAN / mgmt)                 vmbr1 (AGENT NAT subnet)
     ┌───────────────────────┐          ┌─────────────────────────────────┐
     │ PVE UI / SSH / LAN     │          │ 10.77.0.0/16                     │
     │ (restricted access)    │          │ - sandboxes live here            │
     └───────────┬───────────┘          │ - NAT to WAN                      │
                 │                       │ - egress blocks: RFC1918/ULA     │
                 │                       └───────────┬─────────────────────┘
                 │                                   │
                 │                              Outbound Internet
                 │                                   │
            (No direct VM -> LAN)                    ▼
```

---

## 4) Threat model & safety posture (practical)

**Assumption:** agent sandboxes may run arbitrary commands unattended. They can be buggy or behave adversarially.
We aim to reduce:
- host compromise risk
- LAN lateral movement
- secret exfiltration
- persistent tampering

### Core guardrails (MUST)
1. **No host bind mounts to agent sandboxes** (no `/home`, no ZFS datasets mounted inside).
2. **Separate agent network with NAT** (vmbr1). Full outbound, blocked RFC1918/ULA/link-local.
3. **Secrets not persisted**:
   - not written into VM disk
   - stored in tmpfs only
   - delivered via one-time fetch (short TTL)

### Optional hardening (SHOULD)
- Run agent as non-root inside guest.
- Restrict guest SSH access (ephemeral keys).
- Add “inner sandbox” for the agent process (bubblewrap) if feasible (profile `behavior.inner_sandbox`).
- Add egress allowlists for high-risk profiles (later).

---

## 5) Proxmox host requirements

- Proxmox VE 8.x (modern kernel/cgroup2; template/clones; firewall tooling).
- Storage: ZFS or LVM-thin recommended for fast clones.
- NICs:
  - `vmbr0` bound to physical NIC (management/LAN)
  - `vmbr1` internal bridge (no physical port) for agent NAT subnet

### Host directory layout
- `/etc/agentlab/` — config + profiles + policy
- `/var/lib/agentlab/` — sqlite db, metadata, cached templates, workspace registry
- `/var/log/agentlab/` — logs (daemon + job logs)
- `/run/agentlab/` — runtime sockets, pidfiles

---

## 6) Networking: agent_nat bridge + NAT + egress blocks

### Subnet
- `vmbr1`: 10.77.0.0/16 (example)
- host IP on vmbr1: 10.77.0.1
- DHCP: optional; recommended to use cloud-init static IPs or DHCP server VM

### NAT
Implement NAT on host using nftables (preferred) or iptables.
**Script:** `scripts/net/agent_nat.nft` and `scripts/net/apply.sh`

**Policy:**
- Allow outbound TCP/UDP to Internet
- Deny outbound to:
  - RFC1918: 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16
  - link-local: 169.254.0.0/16
  - multicast/reserved: 224.0.0.0/4 (optional)
  - IPv6 ULA: fc00::/7
  - IPv6 link-local: fe80::/10

**Note:** This keeps “full Internet” but blocks LAN.

---

## 7) Storage model

### 7.1 Template disks
- TEMPLATE VM lives on fast storage (`local-zfs` or `local-lvm`)
- Sandboxes are created as linked clones when possible

### 7.2 Workspace volumes (persistent /work)
- A workspace is a dedicated virtual disk (e.g., ZVOL or LVM-thin volume)
- Attached to sandbox as `scsi1` (or `virtio1`) and mounted at `/work`
- Workspace can be detached from one VM and attached to another (“rebind”)

### 7.3 No host mounts
Do **not** mount host directories into agent VMs.
This prevents sandboxes from reading or tampering with host data (including other sandboxes) and avoids unwanted persistence on the host; use workspace disks or artifact uploads instead.
All persistence is via:
- git remote
- artifact upload
- optional workspace disk

---

## 8) VM template specification

### 8.1 Base OS
- Debian 12 or Ubuntu LTS (choose one, keep stable)
- cloud-init enabled
- qemu-guest-agent enabled
- `agent` user created, password login disabled

### 8.2 Preinstalled components (template)
- git, curl, ca-certificates, jq
- your toolchains (optional: install at runtime instead)
- agent binaries (Claude Code CLI, Codex CLI, etc.) (optional to bake-in)
- `agent-runner` script + systemd unit

### 8.3 Cloud-init contract (inputs)
Cloud-init user-data must support:
- setting hostname
- adding ssh public key
- placing `bootstrap_token` (short-lived, one-time)
- selecting profile behaviors (job-runner vs keepalive)
- optionally attaching workspace disk (host-managed)

### 8.4 agent-runner contract
The guest’s `agent-runner` MUST:
1. Fetch secrets from controller using `bootstrap_token`
2. Store secrets in tmpfs (`/run/agentlab/secrets/…`) only
3. Clone/pull repo
4. Run agent loop / task command
5. Emit structured status + logs
6. Push results (git) + upload artifacts
7. Notify controller completion

---

## 9) Secrets & credentials design

### 9.1 Secret types
- Git tokens (scoped to repo/org)
- LLM API keys
- Claude Code `settings.json` fragments
- SSH deploy keys (optional)
- Artifact store tokens (optional)

### 9.2 Storage at rest (controller)
- Store secrets encrypted using `age` (recommended) or `sops`
- The daemon decrypts in memory on demand
- Rotate keys; support multiple secret bundles

### 9.3 Delivery to guest (one-time fetch)
- Controller generates a random `bootstrap_token` per sandbox (TTL 5–15 minutes)
- Token is injected into cloud-init or via Proxmox config
- Guest calls:
  - `POST http://10.77.0.1:8844/v1/bootstrap/fetch`
  - body: `{ "token": "...", "vmid": 123 }`
- Controller returns secrets once and invalidates token immediately.

### 9.4 No secrets in:
- VM disk
- cloud-init “write_files” beyond the bootstrap token
- Proxmox notes field
- controller logs

---

## 10) agentlabd (daemon) specification

### 10.1 Responsibilities
- Own Proxmox API token and perform VM operations
- Maintain state (sandboxes, jobs, profiles, leases, workspaces)
- Enforce policy (max sandboxes, TTL, network profile, no-LAN)
- Serve one-time secret fetch to guests (on agent_nat IP only)
- Provide local API for CLI/TUI/MCP
- Collect logs/status and present them to users
- Garbage-collect expired sandboxes and orphaned resources

### 10.2 Process model
- Runs as a systemd service on Proxmox host
- Minimal dependencies; single binary preferred
- Exposes:
  - Unix socket API for CLI: `/run/agentlab/agentlabd.sock`
  - HTTP listener on agent subnet: `10.77.0.1:8844` for guest bootstrap only
  - Optional localhost HTTP: `127.0.0.1:8845` for debugging (off by default)

### 10.3 State storage
SQLite DB at `/var/lib/agentlab/agentlab.db`

Tables (minimum):
- `sandboxes(vmid, name, profile, state, created_at, lease_expires_at, keepalive, workspace_id, ip, meta_json)`
- `jobs(job_id, repo_url, ref, profile, status, sandbox_vmid, created_at, updated_at, result_json)`
- `profiles(name, yaml, updated_at)`
- `workspaces(id, name, storage, volid, size_gb, attached_vmid, created_at, meta_json)`
- `bootstrap_tokens(token, vmid, expires_at, consumed_at)`
- `events(id, ts, kind, sandbox_vmid, job_id, msg, json)`

### 10.4 Sandbox state machine
```text
REQUESTED -> PROVISIONING -> BOOTING -> READY -> RUNNING -> (COMPLETED|FAILED|TIMEOUT)
                                              \-> STOPPED -> DESTROYED
```
Rules:
- TTL expiration triggers TIMEOUT → STOP → DESTROY (unless keepalive=true)
- A keepalive sandbox still has a renewable lease

### 10.5 Proxmox integration
Two supported backends:
1) **Shell backend** (simple): call `qm`, `pvesh` binaries
2) **REST backend** (robust): call Proxmox API with token

Start with shell backend; design interface so REST can be added later.

Essential operations:
- create sandbox: clone template (linked clone preferred)
- set cloud-init snippet reference
- set CPU/mem limits and cpuset/cpulist
- set network bridge (vmbr1)
- attach/detach workspace disk
- start/stop/destroy
- query status + IP (guest agent or DHCP leases)

---

## 11) Public APIs

### 11.1 Local control API (Unix socket or localhost)
Base path: `/v1`

#### Sandbox
- `POST /v1/sandboxes`
  - body: `{ "name": "...", "profile": "yolo-ephemeral", "job": { ... }, "keepalive": false, "workspace": null }`
- `GET /v1/sandboxes`
- `GET /v1/sandboxes/{vmid}`
- `POST /v1/sandboxes/{vmid}/start`
- `POST /v1/sandboxes/{vmid}/stop`
- `POST /v1/sandboxes/{vmid}/destroy`
- `POST /v1/sandboxes/{vmid}/lease/renew` body: `{ "ttl_minutes": 240 }`

#### Workspaces
- `POST /v1/workspaces` body: `{ "name":"foo", "size_gb": 80, "storage":"local-zfs" }`
- `POST /v1/workspaces/{id}/attach` body: `{ "vmid": 123 }`
- `POST /v1/workspaces/{id}/detach`
- `POST /v1/workspaces/{id}/rebind` body: `{ "profile":"yolo-workspace" }` (creates new sandbox, attaches workspace)

#### Profiles
- `GET /v1/profiles`
- `PUT /v1/profiles/{name}` (admin only)

#### Jobs
- `POST /v1/jobs` body: `{ "repo_url": "...", "ref":"main", "profile":"yolo-ephemeral", "task":"...", "mode":"dangerous", "ttl_minutes": 120 }`
- `GET /v1/jobs/{job_id}`

### 11.2 Guest bootstrap API (agent subnet only)
Bind: `10.77.0.1:8844`

- `POST /v1/bootstrap/fetch`
  - body: `{ "token":"...", "vmid":123 }`
  - response:
    ```json
    {
      "git": {"url":"...", "token":"..."},
      "env": {"ANTHROPIC_API_KEY":"...", "OPENAI_API_KEY":"..."},
      "claude_settings_json": "{...}",
      "artifact": {"endpoint":"...", "token":"..."},
      "policy": {"allowed_domains": ["*"], "mode": "dangerous"}
    }
    ```
  - semantics:
    - one-time: token invalidated after success
    - token expiry: 403

---

## 12) CLI/TUI (`agentlab`) specification

### 12.1 CLI commands
- `agentlab status`
- `agentlab sandbox new --profile <p> --repo <url> [--ref main] [--keepalive] [--workspace <name>]`
- `agentlab sandbox list`
- `agentlab sandbox show <vmid>`
- `agentlab sandbox lease renew <vmid> --ttl 4h`
- `agentlab sandbox destroy <vmid>`
- `agentlab workspace create --name foo --size 80G`
- `agentlab workspace list`
- `agentlab workspace attach foo <vmid>`
- `agentlab workspace rebind foo --profile yolo-workspace`
- `agentlab job run --repo <url> --task <text> --profile yolo-ephemeral`
- `agentlab logs <vmid> --follow`
- `agentlab ssh <vmid>` (helper; uses stored ephemeral key)

### 12.2 TUI (optional)
- list sandboxes, filter by profile/state
- show sandbox details + lease countdown
- tail logs
- manage workspaces
- quick actions (start/stop/destroy/renew)

---

## 13) Claude Code Skills + MCP integration

### 13.1 Claude Code Skills (client-side)
Provide `skills/agentlab/SKILL.md` that maps:
- `/sandbox-new` → runs `agentlab sandbox new …`
- `/sandbox-list` → runs `agentlab sandbox list`
- `/sandbox-destroy` → runs `agentlab sandbox destroy …`
- `/workspace-rebind` → runs `agentlab workspace rebind …`

**Rule:** Skills only call the **unprivileged CLI**. The CLI talks to the daemon via unix socket. The skill never runs `qm` directly.

### 13.2 MCP server (optional)
Expose the same operations as MCP tools for other agents.
The MCP server is just a thin adapter that calls local API endpoints.

---

## 14) Installation & bootstrap scripts

### 14.1 scripts/install_host.sh
Responsibilities:
- create directories
- install `agentlabd` and `agentlab`
- install systemd unit for `agentlabd`
- set up vmbr1 bridge (optional interactive)
- apply NAT + egress block rules
- create Proxmox API token with least privilege (manual step documented)
- validate connectivity

### 14.2 scripts/create_template.sh
- create base VM (cloud image)
- enable qemu-guest-agent
- install prerequisites
- install agent-runner + systemd unit
- convert to template

### 14.3 scripts/profiles/defaults.yaml
Ship initial profiles:
- `yolo-ephemeral`
- `yolo-workspace`
- `interactive-dev`

### 14.4 scripts/guest/agent-runner.sh
- fetch bootstrap secrets
- stage config in tmpfs
- run job loop
- push results & artifacts

### 14.5 scripts/guest/systemd units
- `agent-runner.service`
- `agent-secrets-cleanup.service` (ExecStopPost or separate unit)

---

## 15) Profiles (resource + policy bundles)

### Profile schema (YAML)
```yaml
name: yolo-ephemeral
template_vmid: 9000
network:
  bridge: vmbr1
  model: virtio
  firewall_group: agent_nat_default
resources:
  cores: 4
  memory_mb: 6144
  balloon: false
  cpulist: "0-7"           # optional: P-core threads
storage:
  root_size_gb: 40
  workspace: none          # or "attach"
behavior:
  mode: dangerous
  keepalive_default: false
  ttl_minutes_default: 180
  # inner_sandbox: bubblewrap
secrets_bundle: default
repo:
  clone_path: /tmp/repo    # workspace profile overrides to /work/repo
artifacts:
  upload: true
  endpoint: http://10.77.0.1:8846/upload
```

---

## 16) Workflows (end-to-end)

### 16.1 Unattended ephemeral job
1. User: `agentlab job run --repo … --task … --profile yolo-ephemeral`
2. Daemon:
   - allocates vmid
   - clones template (linked clone)
   - creates bootstrap token (TTL 10m)
   - writes cloud-init snippet (no secrets except token)
   - starts VM
   - tracks lease TTL
3. Guest:
   - boots, calls bootstrap fetch
   - clones repo, runs agent loop
   - pushes PR + uploads artifacts
   - reports completion
4. Daemon:
   - marks job complete
   - destroys VM

### 16.2 Long-running workspace sandbox
1. `agentlab workspace create --name foo --size 80G`
2. `agentlab sandbox new --profile yolo-workspace --workspace foo --keepalive`
3. VM runs; `/work` persists.
4. If compromised/messy:
   - `agentlab workspace rebind foo --profile yolo-workspace`
   - destroys old VM, creates new VM, attaches same workspace.

---

## 17) Observability & logs

### Host-side
- `agentlabd` logs: journald + `/var/log/agentlab/agentlabd.log`
- events table in sqlite for auditability
- per-job log bundle saved to `/var/lib/agentlab/artifacts/{job_id}.tar.gz`

### Guest-side
- journal for agent-runner
- optional forward logs to controller endpoint

---

## 18) Implementation plan (developer guide)

### 18.1 Milestone 1 — MVP (1–2 weeks)
- `agentlabd` daemon with shell backend (`qm`, `pvesh`)
- sqlite state
- create sandbox from template with cloud-init snippet
- one-time bootstrap token + guest fetch endpoint
- CLI: create/list/destroy, lease TTL, logs (basic)

### 18.2 Milestone 2 — Workspaces + rebind
- persistent disk creation + attach/detach
- `/work` mount logic in guest
- rebind workflow

### 18.3 Milestone 3 — TUI + MCP + Skills
- Bubble Tea/Textual UI
- MCP adapter
- packaged Claude Code skill file

### 18.4 Milestone 4 — Hardening + polish
- stricter egress controls (domain allowlists optional)
- signed artifacts
- structured job results + metrics

---

## 19) Appendices

### A) Example cloud-init user-data snippet (NO secrets)
```yaml
#cloud-config
hostname: sandbox-{{vmid}}
users:
  - name: agent
    groups: [sudo]
    shell: /bin/bash
    sudo: ["ALL=(ALL) NOPASSWD:ALL"]
    ssh_authorized_keys:
      - {{ssh_pubkey}}
write_files:
  - path: /etc/agentlab/bootstrap.json
    permissions: "0600"
    content: |
      {"token":"{{bootstrap_token}}","controller":"http://10.77.0.1:8844","vmid":{{vmid}}}
runcmd:
  - systemctl enable --now agent-runner.service
```

### B) Example `agent-runner.service`
```ini
[Unit]
Description=Agent Runner
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=agent
Environment=AGENTLAB_BOOTSTRAP=/etc/agentlab/bootstrap.json
ExecStart=/usr/local/bin/agent-runner.sh
Restart=on-failure
RestartSec=10

[Install]
WantedBy=multi-user.target
```

### C) Example guest runner outline
```bash
#!/usr/bin/env bash
set -euo pipefail

BOOTSTRAP="${AGENTLAB_BOOTSTRAP:-/etc/agentlab/bootstrap.json}"
TOKEN="$(jq -r .token "$BOOTSTRAP")"
CTRL="$(jq -r .controller "$BOOTSTRAP")"
VMID="$(jq -r .vmid "$BOOTSTRAP")"

mkdir -p /run/agentlab/secrets
chmod 700 /run/agentlab/secrets

# Fetch secrets one-time
SECRETS_JSON="$(curl -fsS -X POST "$CTRL/v1/bootstrap/fetch" \
  -H "Content-Type: application/json" \
  -d "{\"token\":\"$TOKEN\",\"vmid\":$VMID}")"

# Write only to tmpfs
echo "$SECRETS_JSON" > /run/agentlab/secrets/all.json
chmod 600 /run/agentlab/secrets/all.json

# Configure Claude settings (example)
jq -r '.claude_settings_json' /run/agentlab/secrets/all.json > /run/agentlab/secrets/claude-settings.json
chmod 600 /run/agentlab/secrets/claude-settings.json

# Run job loop (placeholder)
# - clone repo, run agent, push PR, upload artifacts
exec /usr/local/bin/run-agent-loop.sh /run/agentlab/secrets/all.json
```

### D) Example nftables (sketch)
```nft
table inet agentlab {
  chain forward {
    type filter hook forward priority 0;
    ct state established,related accept

    # block LAN ranges from agent subnet
    ip saddr 10.77.0.0/16 ip daddr {10.0.0.0/8,172.16.0.0/12,192.168.0.0/16,169.254.0.0/16} drop
    ip6 saddr fd00::/8 ip6 daddr fc00::/7 drop
    ip6 daddr fe80::/10 drop

    accept
  }

  chain postrouting {
    type nat hook postrouting priority 100;
    ip saddr 10.77.0.0/16 oifname "vmbr0" masquerade
  }
}
```

---

## 20) Safety notes

- This system is designed to **limit blast radius** of unattended agents, not to enable wrongdoing.
- Keep sandboxes **off your LAN** by default.
- Prefer **scoped, revocable tokens** and rotate often.
- If you later allow LAN, do it explicitly via a separate profile and firewall allowlists.

---

## 21) Open questions (choose defaults now; configurable later)
- Which base OS for template (Debian vs Ubuntu)
- Which storage pool name (local-zfs vs local-lvm)
- Which agent CLIs are baked into template vs installed per job
- Artifact upload service (simple HTTP on host vs MinIO)
