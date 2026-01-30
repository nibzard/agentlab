# AGENTLAB_DEV_SPECIFICATION.md

> **Scope:** Development task backlog for building **AgentLab**: unattended, ephemeral (and optionally long-running) “YOLO” agent sandboxes on **Proxmox VE**, derived from the product spec in `PROXMOX_SPECS.md`.  
> **Audience:** engineering (infra + backend + guest image + security).  
> **Key outcome:** `agentlab job run …` provisions a sandbox VM, runs an agent loop unattended with full Internet egress, uploads artifacts, reports status, and tears down the VM by default.

---

## 1. Project decisions (locked for MVP)

These are the defaults we will implement first; make them configurable later if needed.

- **Guest OS:** Ubuntu (use current LTS cloud image; recommend **Ubuntu 24.04 LTS**).
- **Proxmox storage:** `local-zfs` (fast linked clones; workspace volumes as ZVOLs).
- **Remote access:** **Tailscale** (tailnet devices must be able to SSH into sandboxes).
- **Agent tooling to package in template:**  
  1) **Claude Code CLI** (primary “DevOps agent”, used with Skills),  
  2) **OpenAI Codex** CLI,  
  3) **OpenCode** CLI.
- **No MCP server:** we will ship **Skills (`SKILL.md`) only** (Skills call the unprivileged `agentlab` CLI; the CLI talks to `agentlabd` over a Unix socket).
- **Artifact upload:** implement an **embedded artifact upload service in `agentlabd`** backed by a dedicated ZFS dataset + strict token scoping (S3/MinIO backend can be added later).

---

## 2. Priority definitions

- **P0 — Required for end-to-end unattended runs** (and safe-by-default isolation).
- **P1 — Strongly recommended for day-2 usability** (workspaces, better logs, tailscale UX).
- **P2 — Nice-to-have hardening/polish** (TUI, advanced allowlists, metrics).

---

## 3. System-level acceptance criteria (MVP)

1. A developer can run: `agentlab job run --repo <url> --task "<text>" --profile yolo-ephemeral`.
2. `agentlabd` clones a VM from a Proxmox template (linked clone preferred), boots it on an **agent-only NAT network**, and enforces a **lease TTL**.
3. The guest `agent-runner` fetches secrets via a **one-time bootstrap token**, stores them **only in tmpfs**, executes the agent loop, and reports status.
4. The sandbox VM has **full outbound Internet** but **cannot initiate connections to LAN/private networks or the tailnet** (aside from responses to inbound sessions).
5. Artifacts (logs + outputs) are uploaded to the controller and available for retrieval via the CLI.
6. On completion (success/failure/timeout), the VM is destroyed unless `--keepalive` is requested.

---

## 4. Tailscale access model (MVP)

We will **not** install Tailscale inside each sandbox (reduces lateral-movement risk).  
Instead:

- Proxmox host runs Tailscale and advertises the agent subnet (e.g., `10.77.0.0/16`) as a **subnet router**.
- Tailnet devices can connect directly to sandbox IPs (SSH) once they accept the route.
- Host firewall rules MUST:
  - allow **inbound** from `tailscale0 → vmbr1` (ports needed, primarily SSH)
  - block **new outbound** connections from `vmbr1 → tailscale0` (prevent sandbox → tailnet pivot)
  - allow **established/related** traffic so SSH works both ways

This preserves “remote access for humans” without giving sandboxes a free path into the tailnet.

---

## 5. Workstreams & task backlog

### Workstream A — Repository, build, packaging

#### A1. Project skeleton & build pipeline
- **Priority:** P0  
- **Owner:** Backend/Platform  
- **Dependencies:** none  
- **Description:**  
  Create the repository structure for:
  - `agentlabd` daemon (systemd service)
  - `agentlab` CLI
  - shared libraries: config, db, proxmox backend, models
  - `scripts/` for host + template automation  
  The spec favors “single binary preferred” and a minimal dependency footprint.
- **Implementation notes:**
  - Use a language/tooling stack that reliably produces static-ish binaries (Go is a strong default).
  - Provide `make build`, `make lint`, `make test`.
- **Definition of done:**
  - `agentlabd --version` and `agentlab --version` work.
  - CI runs lint + unit tests.
  - Release artifacts produced for Proxmox host (amd64).

#### A2. Host install script & systemd unit
- **Priority:** P0  
- **Owner:** Infra/Platform  
- **Dependencies:** A1  
- **Description:**  
  Implement `scripts/install_host.sh` + a hardened `agentlabd.service` that:
  - creates `/etc/agentlab`, `/var/lib/agentlab`, `/var/log/agentlab`, `/run/agentlab`  
  - installs binaries
  - enables + starts the daemon  
  (Directory layout matches the spec.)
- **Definition of done:**
  - `systemctl status agentlabd` healthy
  - unix socket exists at `/run/agentlab/agentlabd.sock`
  - log path + permissions correct

---

### Workstream B — Host networking, NAT, and Tailscale

#### B1. Create agent-only bridge (`vmbr1`) and routing prerequisites
- **Priority:** P0  
- **Owner:** Infra  
- **Dependencies:** none  
- **Description:**  
  Create an internal bridge `vmbr1` (no physical NIC) for agent sandboxes:
  - subnet: `10.77.0.0/16`
  - host IP: `10.77.0.1`  
  Enable IP forwarding on the host.
- **Definition of done:**
  - `ip a` shows `vmbr1` with `10.77.0.1/16`
  - forwarding enabled persistently

#### B2. nftables policy: NAT + RFC1918/ULA egress blocks
- **Priority:** P0  
- **Owner:** Infra/Security  
- **Dependencies:** B1  
- **Description:**  
  Implement nftables rules (preferred over iptables) for:
  - NAT `10.77.0.0/16 → vmbr0` (WAN/LAN uplink)
  - allow established/related forwarding
  - drop sandbox-initiated traffic to:
    - RFC1918 + link-local (IPv4): `10/8`, `172.16/12`, `192.168/16`, `169.254/16`
    - IPv6 ULA + link-local: `fc00::/7`, `fe80::/10`  
  This enforces “full Internet, no LAN.”
- **Definition of done:**
  - From a sandbox: `curl https://example.com` works
  - From a sandbox: `curl http://192.168.1.1` fails (blocked)
  - Rules are persistent across reboot

#### B3. Tailscale subnet routing + firewall asymmetric policy
- **Priority:** P0  
- **Owner:** Infra/Security  
- **Dependencies:** B1, B2  
- **Description:**  
  Configure Tailscale on the Proxmox host as a subnet router for `10.77.0.0/16`.
  Enforce:
  - allow tailnet → sandbox connections
  - block sandbox → tailnet *new* connections  
  Add explicit drop rules for tailnet ranges (IPv4 `100.64.0.0/10` and the Tailscale IPv6 range if used) when source is `10.77.0.0/16` and destination is tailnet, while permitting established/related flows.
- **Definition of done:**
  - From a tailnet laptop: `ssh agent@10.77.x.y` works (route accepted)
  - From a sandbox: initiating `ssh <tailnet-device>` fails
  - Existing inbound SSH sessions remain stable

#### B4. “Connectivity smoke test” script
- **Priority:** P1  
- **Owner:** Infra  
- **Dependencies:** B2, B3  
- **Description:**  
  Create `scripts/net/smoke_test.sh` that validates:
  - Internet egress from sandbox
  - LAN blocks
  - tailnet inbound allowed, tailnet outbound blocked
  - DNS resolution in sandbox
- **Definition of done:**
  - Single command produces a clear pass/fail report for operators

---

### Workstream C — Proxmox backend abstraction

#### C1. Proxmox backend interface (shell-first)
- **Priority:** P0  
- **Owner:** Backend  
- **Dependencies:** A1  
- **Description:**  
  Implement a backend interface with a **shell backend** that uses `qm`/`pvesh`:
  - clone (linked clone preferred)
  - configure CPU/mem/net/cloud-init
  - start/stop/destroy
  - query status + obtain IP (via qemu-guest-agent preferred)
- **Definition of done:**
  - Unit tests with command mocking
  - Works end-to-end on a real Proxmox node in dev

#### C2. Cloud-init snippet lifecycle management
- **Priority:** P0  
- **Owner:** Backend/Security  
- **Dependencies:** C1  
- **Description:**  
  Generate per-sandbox cloud-init user-data snippets containing **only**:
  - hostname
  - SSH public key
  - bootstrap JSON (token + controller URL + vmid)  
  Ensure snippet files are:
  - created in the configured snippets storage
  - uniquely named (include vmid + random suffix)
  - deleted on VM destroy
  - never contain long-lived secrets
- **Definition of done:**
  - Grep audit confirms snippets never include API keys
  - Snippets removed after VM teardown (including failures)

#### C3. IP discovery via qemu-guest-agent
- **Priority:** P0  
- **Owner:** Backend  
- **Dependencies:** C1, Template tasks (E1)  
- **Description:**  
  Standardize on qemu-guest-agent for VM IP discovery. Implement:
  - polling with backoff until guest reports IP on vmbr1
  - fallback to DHCP lease parsing if qga unavailable
- **Definition of done:**
  - `agentlab sandbox show <vmid>` prints IP reliably

---

### Workstream D — agentlabd daemon (control plane)

#### D1. agentlabd core service + config loader
- **Priority:** P0  
- **Owner:** Backend  
- **Dependencies:** A1  
- **Description:**  
  Implement `agentlabd` as a systemd service that:
  - loads config from `/etc/agentlab/config.yaml`
  - loads profiles from `/etc/agentlab/profiles/*.yaml`
  - binds Unix socket `/run/agentlab/agentlabd.sock`
  - binds guest bootstrap listener on `10.77.0.1:8844`  
  (Localhost debug listener optional/off by default.)
- **Definition of done:**
  - Service starts cleanly
  - Config validation errors are clear and safe (no secret dumps)

#### D2. SQLite schema + migrations
- **Priority:** P0  
- **Owner:** Backend  
- **Dependencies:** D1  
- **Description:**  
  Implement SQLite storage at `/var/lib/agentlab/agentlab.db` with the core tables:
  `sandboxes`, `jobs`, `profiles`, `workspaces`, `bootstrap_tokens`, `events`.
- **Definition of done:**
  - Migrations run automatically on startup
  - Backward-compatible schema evolution plan documented

#### D3. Sandbox state machine + lease/TTL engine
- **Priority:** P0  
- **Owner:** Backend  
- **Dependencies:** D2, C1  
- **Description:**  
  Implement the sandbox lifecycle state machine:  
  `REQUESTED → PROVISIONING → BOOTING → READY → RUNNING → (COMPLETED|FAILED|TIMEOUT) → DESTROYED`  
  Add a lease/TTL engine:
  - default TTL comes from profile
  - `--keepalive` enables renewable leases
  - TTL expiration triggers stop/destroy (unless keepalive)
- **Definition of done:**
  - Deterministic transitions with persisted state
  - GC thread cleans up expired sandboxes even after daemon restart

#### D4. Local control API (Unix socket)
- **Priority:** P0  
- **Owner:** Backend  
- **Dependencies:** D1, D2, D3  
- **Description:**  
  Implement the local API endpoints (subset for MVP):
  - `POST /v1/jobs`
  - `GET /v1/jobs/{id}`
  - `POST /v1/sandboxes`
  - `GET /v1/sandboxes`, `GET /v1/sandboxes/{vmid}`
  - `POST /v1/sandboxes/{vmid}/destroy`
  - `POST /v1/sandboxes/{vmid}/lease/renew`
- **Definition of done:**
  - API documented in `docs/api.md`
  - Request/response types stable and versioned
  - Socket permissions restrict access to intended Unix group

#### D5. Job orchestration: “create → run → report → destroy”
- **Priority:** P0  
- **Owner:** Backend  
- **Dependencies:** D4, E3, F1  
- **Description:**  
  Implement job orchestration:
  - create sandbox with bootstrap token
  - wait for guest runner to report RUNNING/COMPLETED
  - on completion, persist results + artifacts metadata
  - destroy sandbox if ephemeral
- **Definition of done:**
  - `agentlab job run` completes end-to-end for a trivial repo task
  - Failures are captured with actionable diagnostics

---

### Workstream E — Secrets & bootstrap service

#### E1. Secrets at rest: bundle format + encryption
- **Priority:** P0  
- **Owner:** Security/Backend  
- **Dependencies:** D1  
- **Description:**  
  Implement secret bundles stored at rest on the host, encrypted (age or sops).  
  Bundles should support:
  - git token / deploy key
  - LLM API keys (Anthropic/OpenAI)
  - claude code settings fragments
  - artifact upload token settings
- **Definition of done:**
  - Secrets never committed to repo
  - Rotation workflow documented
  - `agentlabd` can decrypt into memory when needed

#### E2. One-time bootstrap token issuance + validation
- **Priority:** P0  
- **Owner:** Backend/Security  
- **Dependencies:** D2  
- **Description:**  
  On sandbox creation:
  - mint a random `bootstrap_token` (TTL 5–15 minutes)
  - store hash + expiry in DB
  - inject only the token into cloud-init
  - mark token consumed on first successful fetch
- **Definition of done:**
  - token cannot be reused
  - expired tokens are rejected
  - token material never appears in logs

#### E3. Guest bootstrap API: `/v1/bootstrap/fetch`
- **Priority:** P0  
- **Owner:** Backend/Security  
- **Dependencies:** E2, D1  
- **Description:**  
  Implement the guest-only endpoint bound to `10.77.0.1:8844` that returns:
  - secrets bundle (env vars + git creds)
  - job config (repo URL, ref, task, agent selection)
  - artifact upload instructions (scoped token/URL)
  - policy metadata (dangerous mode, etc.)
- **Definition of done:**
  - fetch works only from vmbr1 (enforced by bind + firewall)
  - response is minimal and does not include unrelated secrets
  - request is authenticated solely by bootstrap token + vmid binding

---

### Workstream F — Guest template & runner

#### F1. Ubuntu template creation script
- **Priority:** P0  
- **Owner:** Infra/Guest  
- **Dependencies:** none  
- **Description:**  
  Implement `scripts/create_template.sh` that:
  - downloads Ubuntu cloud image
  - creates VM (q35/UEFI optional), enables cloud-init
  - enables qemu-guest-agent
  - installs baseline packages: git/curl/ca-certificates/jq
  - creates `agent` user; disables password auth
- **Definition of done:**
  - Template VM boots and cloud-init applies user-data
  - qemu-guest-agent reports ready

#### F2. Package agent CLIs into template (Claude Code, Codex, OpenCode)
- **Priority:** P0  
- **Owner:** Guest/DevEx  
- **Dependencies:** F1  
- **Description:**  
  Install and pin versions of:
  - Claude Code CLI (primary)
  - OpenAI Codex CLI
  - OpenCode CLI  
  Provide a single wrapper in the template (e.g., `/usr/local/bin/agentlab-agent`) that can dispatch to the selected tool so the runner does not care about differences.
- **Definition of done:**
  - `agentlab-agent --help` works in a fresh sandbox
  - versions are pinned + recorded (build stamp)

#### F3. Implement `agent-runner` + systemd unit
- **Priority:** P0  
- **Owner:** Guest/Backend  
- **Dependencies:** E3, F2  
- **Description:**  
  Implement `agent-runner` behavior (systemd service):
  1) read `/etc/agentlab/bootstrap.json` (token/controller/vmid)
  2) fetch bootstrap payload
  3) write secrets to **tmpfs** (`/run/secrets`) only
  4) clone/pull repo into `/tmp/repo` (or `/work/repo` if workspace attached)
  5) execute agent loop with the selected CLI
  6) stream logs + status events to controller
  7) upload artifacts + final report
- **Definition of done:**
  - runner survives transient network failures (retry with backoff)
  - secrets never written outside `/run/secrets`
  - structured status updates observable from host

#### F4. Secrets cleanup on stop
- **Priority:** P1  
- **Owner:** Guest/Security  
- **Dependencies:** F3  
- **Description:**  
  Add `ExecStopPost` or a dedicated cleanup unit to:
  - wipe `/run/secrets`
  - wipe temp dirs used for repo checkout if ephemeral
- **Definition of done:**
  - after completion, secrets are not present in filesystem artifacts
  - postmortem logs do not contain secret values

---

### Workstream G — agentlab CLI

#### G1. CLI: job run + sandbox management
- **Priority:** P0  
- **Owner:** Backend/DevEx  
- **Dependencies:** D4  
- **Description:**  
  Implement:
  - `agentlab job run …`
  - `agentlab sandbox new/list/show/destroy`
  - `agentlab sandbox lease renew`
  - `agentlab logs <vmid> --follow`
- **Definition of done:**
  - UX: clear outputs for vmid, IP, lease expiry, job status
  - `--json` output for automation

#### G2. CLI: SSH helper with Tailscale-friendly output
- **Priority:** P1  
- **Owner:** DevEx  
- **Dependencies:** C3, B3  
- **Description:**  
  Implement `agentlab ssh <vmid>` that:
  - resolves the sandbox IP
  - prints the exact `ssh` command to use
  - optionally execs ssh when run interactively  
  Include a note if the Tailscale subnet route is not reachable.
- **Definition of done:**
  - works from: (a) host, (b) tailnet device with route enabled

---

### Workstream H — Workspaces (persistent /work)

#### H1. Workspace volume management on `local-zfs`
- **Priority:** P1  
- **Owner:** Backend/Infra  
- **Dependencies:** D2, C1  
- **Description:**  
  Implement workspace disks as ZFS volumes managed via Proxmox storage:
  - create volume (size, name)
  - attach as `scsi1` (or `virtio1`)
  - detach and track attachment in DB
- **Definition of done:**
  - `agentlab workspace create/list/attach/detach` works reliably
  - attaching the same workspace to two VMs is prevented

#### H2. Guest auto-mount `/work`
- **Priority:** P1  
- **Owner:** Guest  
- **Dependencies:** H1, F1  
- **Description:**  
  Implement robust `/work` mounting in guest:
  - detect disk by label/UUID
  - format if first attach (ext4 recommended)
  - mount at boot with systemd mount unit
- **Definition of done:**
  - `/work` is present and writable
  - rebind to a new sandbox preserves files

#### H3. Workspace rebind workflow
- **Priority:** P1  
- **Owner:** Backend  
- **Dependencies:** H1, H2, D3  
- **Description:**  
  Implement `agentlab workspace rebind <name> --profile yolo-workspace`:
  - create new sandbox
  - attach workspace
  - (optional) destroy old sandbox
- **Definition of done:**
  - rebind is idempotent and safe under failures
  - user gets new vmid + IP

---

### Workstream I — Artifact upload & retrieval

#### I1. Embedded artifact upload service (agentlabd)
- **Priority:** P0  
- **Owner:** Backend/Security  
- **Dependencies:** D1, E3  
- **Description:**  
  Add an HTTP listener on `10.77.0.1:8846` for sandbox uploads:
  - scoped, per-job upload token(s)
  - streaming uploads with strict size limits
  - path sanitization (no traversal)
  - store under `/var/lib/agentlab/artifacts/<job_id>/…` on ZFS
  - record metadata in DB (sha256, size, mime, created_at)
- **Definition of done:**
  - sandbox can upload a tarball/log bundle
  - uploads fail cleanly on invalid token or oversize

#### I2. CLI: list/download artifacts
- **Priority:** P1  
- **Owner:** DevEx  
- **Dependencies:** I1, G1  
- **Description:**  
  Implement:
  - `agentlab job artifacts <job_id>`
  - `agentlab job artifacts download <job_id> --out …`  
  Consider supporting “download latest bundle” as the default.
- **Definition of done:**
  - artifacts retrievable from host and tailnet devices

#### I3. Retention & garbage collection
- **Priority:** P1  
- **Owner:** Backend/Infra  
- **Dependencies:** I1, D3  
- **Description:**  
  Implement retention policy:
  - per-profile artifact TTL
  - delete artifacts for destroyed sandboxes after TTL
  - optionally keep “job bundle” longer
- **Definition of done:**
  - GC runs on a schedule and is restart-safe
  - reports what it deleted (without leaking secrets)

---

### Workstream J — Skills for Claude Code CLI

#### J1. Ship `skills/agentlab/SKILL.md`
- **Priority:** P0  
- **Owner:** DevEx  
- **Dependencies:** G1  
- **Description:**  
  Create a Skills file that exposes safe, high-level operations by calling `agentlab`:
  - `/job-run` (repo + task + profile)
  - `/sandbox-new`, `/sandbox-list`, `/sandbox-show`, `/sandbox-destroy`
  - `/lease-renew`
  - `/logs-follow`
  - `/workspace-rebind`  
  **Rule:** skills must never call `qm` directly; only the CLI.
- **Definition of done:**
  - Claude Code can execute Skills on the host to manage sandboxes
  - commands are validated and include guardrails (e.g., require explicit `--dangerous`)

#### J2. Package Skills with the Claude Code configuration
- **Priority:** P1  
- **Owner:** DevEx  
- **Dependencies:** J1, F2  
- **Description:**  
  Ensure Skills are discoverable by Claude Code in the environment we run it (host or sandbox):
  - document required directory placement
  - provide install step in `scripts/install_host.sh` and/or template build
- **Definition of done:**
  - “skills available” test passes in a clean install

---

### Workstream K — Security hardening & policy enforcement

#### K1. “No host mounts” enforcement
- **Priority:** P0  
- **Owner:** Security/Infra  
- **Dependencies:** C1  
- **Description:**  
  Ensure no VM gets host bind mounts (Proxmox config audit).  
  Add a guard in `agentlabd` that refuses to provision profiles that request host mounts.
- **Definition of done:**
  - provisioning fails if a profile tries to mount host paths
  - operator docs explain why

#### K2. Secret redaction & log safety
- **Priority:** P0  
- **Owner:** Security/Backend  
- **Dependencies:** E1, E3, F3  
- **Description:**  
  Implement a redaction layer:
  - never log bootstrap tokens
  - never log secret values
  - scrub known env var keys from structured logs
- **Definition of done:**
  - automated test proves redaction for common keys

#### K3. agentlabd systemd hardening
- **Priority:** P1  
- **Owner:** Security/Infra  
- **Dependencies:** A2  
- **Description:**  
  Harden `agentlabd.service` with systemd sandboxing where compatible with Proxmox tooling:
  - `ProtectSystem=strict` (with required write paths)
  - `PrivateTmp=true`
  - `NoNewPrivileges=true`
  - restrict capabilities to what’s required
- **Definition of done:**
  - service still provisions VMs
  - hardening settings documented + reviewed

#### K4. Optional inner sandbox for agent process (bubblewrap)
- **Priority:** P2  
- **Owner:** Security/Guest  
- **Dependencies:** F3  
- **Description:**  
  Evaluate running the agent CLI inside bubblewrap or similar to reduce damage within the guest.
- **Definition of done:**
  - documented pros/cons and a working prototype behind a profile flag

---

### Workstream L — Observability & operability

#### L1. Event stream & per-job logs
- **Priority:** P1  
- **Owner:** Backend  
- **Dependencies:** D2, F3  
- **Description:**  
  Implement:
  - structured `events` table entries
  - `GET /v1/jobs/{id}` includes last N events + pointers to artifacts
- **Definition of done:**
  - `agentlab job show <id>` gives enough info to debug failures

#### L2. Operator “runbook” docs
- **Priority:** P1  
- **Owner:** Infra/DevEx  
- **Dependencies:** B1–B3, F1, A2  
- **Description:**  
  Document:
  - how to create/update the template
  - how to rotate secrets
  - how to debug stuck sandboxes
  - how to recover from daemon restart
  - how to use Tailscale subnet routing for access
- **Definition of done:**
  - new operator can set up from scratch with docs only

#### L3. Metrics (Prometheus) — optional
- **Priority:** P2  
- **Owner:** Backend  
- **Dependencies:** D1  
- **Description:**  
  Add counters/gauges:
  - sandboxes by state
  - job success/failure/timeout
  - provision time distributions
- **Definition of done:**
  - `/metrics` behind localhost-only flag

---

### Workstream M — QA & safety testing

#### M1. End-to-end “golden path” integration test
- **Priority:** P0  
- **Owner:** QA/Backend  
- **Dependencies:** A2, B2, D5, F3  
- **Description:**  
  Provide a single scripted test that:
  - creates a sandbox
  - runs a tiny repo task (e.g., write a file, run unit tests)
  - uploads an artifact
  - validates teardown
- **Definition of done:**
  - test passes on a clean Proxmox node with the template installed

#### M2. Network isolation regression tests
- **Priority:** P1  
- **Owner:** Security/QA  
- **Dependencies:** B2, B3  
- **Description:**  
  Automated checks (inside sandbox) that:
  - verify private LAN ranges are blocked
  - verify tailnet ranges are blocked for new connections
  - verify Internet works
- **Definition of done:**
  - tests run in CI where possible, or as a required manual pre-release step

---

## 6. Deliverables checklist (what “done” looks like)

- [ ] `agentlabd` runs on Proxmox host and provisions sandboxes from an Ubuntu template.
- [ ] `agentlab` CLI can create/list/show/destroy sandboxes and run jobs end-to-end.
- [ ] VMs have full Internet egress but cannot reach LAN or tailnet (except established replies).
- [ ] One-time bootstrap secret delivery works; secrets live only in tmpfs.
- [ ] Artifact upload works and artifacts can be downloaded via CLI.
- [ ] Tailscale subnet routing allows developers to SSH into sandboxes remotely.
- [ ] `skills/agentlab/SKILL.md` exists and can drive `agentlab` operations from Claude Code.

---

## 7. Notes on future scope (explicitly not MVP)

- MCP server (explicitly excluded).
- Full Proxmox REST backend (design interface to add later).
- Domain allowlists/deny-lists beyond RFC1918 + tailnet blocks.
- TUI (optional).
