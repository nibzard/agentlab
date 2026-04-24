# AgentLab Improvement Plan

> Informed by analysis of exe.dev (exe.ssh) architecture and design patterns.
> AgentLab is the self-hosted alternative; exe.ssh is the cloud-based service.
> Date: 2026-04-24

---

## 1. SSH-First API Design

**exe.ssh**: The entire API is accessible via SSH (`ssh exe.dev new`, `ssh exe.dev ls --json`). The HTTPS API is literally "the SSH API shoved into a POST body" -- one API to learn, works identically over SSH and HTTP.

**agentlab today**: CLI communicates over a Unix socket with a local daemon. No remote access. No HTTPS API.

**Improvements**:
- Add an SSH gateway that maps to the same daemon commands (already have `cmd/agentlab-ssh-gateway` scaffolded)
- Add an HTTPS API that mirrors the CLI 1:1 (like exe's `POST /exec` pattern)
- Unify local + remote access: `agentlab sandbox new` locally should behave identically to `ssh agentlab.myserver.com sandbox new` remotely

---

## 2. Container-Image-Based Sandboxes for Speed

**exe.ssh**: Uses container images (Docker images) as VM base images, not traditional disk clones. VM creation takes ~2 seconds. VMs share CPU/RAM from a pooled allocation.

**agentlab today**: Clones Proxmox VMs from template images via profiles. Heavier, slower provisioning.

**Improvements**:
- Support LXC container-based sandboxes alongside full VMs for faster spin-up (Proxmox supports LXC natively)
- Add a "lightweight" sandbox type using container images (`agentlab sandbox new --image=ubuntu:22.04`)
- Resource pooling model -- allow over-committing CPU/RAM across sandboxes rather than hard per-VM allocation

---

## 3. Zero-Config Networking with Reverse Proxy

**exe.ssh**: Every VM gets `vmname.exe.xyz` with automatic TLS termination. No public IPs. Built-in HTTPS reverse proxy. `share set-public` makes it internet-accessible.

**agentlab today**: Dedicated subnet (10.77.0.0/16), manual `expose`/`unexpose` for ports, no TLS, no automatic domain names.

**Improvements**:
- Integrate Caddy or Traefik as a built-in reverse proxy
- Automatic subdomain assignment: `sandbox-name.agentlab.local` or `sandbox-name.yourdomain.com`
- Automatic TLS via Let's Encrypt / self-signed CA for self-hosted
- One-command sharing: `agentlab sandbox expose mybox --public` handles TLS + DNS + proxy

---

## 4. Authentication: SSH Keys as Identity

**exe.ssh**: Authentication is SSH keys. API tokens are signed with SSH keys locally (no server-side secret storage). Tokens have granular permissions (`cmds`, `exp`, `nbf`). Tokens are scoped to VMs.

**agentlab today**: Bearer tokens for guest APIs. No multi-user auth. No scoped permissions.

**Improvements**:
- SSH key-based authentication for daemon access (fits self-hosted perfectly -- just `~/.ssh/authorized_keys`)
- API tokens with scoped permissions (per-sandbox, per-command, time-limited)
- No external auth provider needed -- keys ARE the identity

---

## 5. Network-Level Secret Injection (Integrations)

**exe.ssh**: Integrations inject secrets at the network level (HTTP headers, auth tokens). Secrets never exist inside the VM. Supports HTTP Proxy Integration, GitHub Integration. Attach by VM, tag, or `auto:all`.

**agentlab today**: Secrets delivered via bootstrap API at VM boot, stored encrypted with age/sops. Secrets exist inside the sandbox.

**Improvements**:
- Add a proxy layer that injects secrets into requests (no secrets on disk)
- GitHub integration equivalent: proxy git clone through a gateway that injects credentials
- Tag-based attachment: apply integrations to all sandboxes with a given label
- `agentlab integration add http-proxy --name=myapi --target=https://api.example.com --bearer=sk-...`

---

## 6. Simplify the Mental Model

**exe.ssh**: ~15 commands. Core concept: "it's just a computer." VMs, persistent disks, sharing, integrations. Everything else is Linux.

**agentlab today**: 40+ commands across sandboxes, jobs, workspaces, sessions, snapshots, profiles, secrets, leases, artifacts, messagebox. Rich but complex.

**Improvements**:
- Collapse concepts: `sandbox` = computer with disk, `job` = task on a sandbox, `workspace` = persistent disk
- Consider merging sessions into sandboxes (a sandbox with a workspace IS a session)
- Reduce top-level command surface: `agentlab new`, `agentlab ls`, `agentlab ssh`, `agentlab rm` as the 80/20
- Keep advanced commands as subcommands for power users

---

## 7. Built-in Guest Services (Metadata Endpoint)

**exe.ssh**: Every VM has `http://169.254.169.254/` providing LLM Gateway, email sending, identity headers. No API keys needed inside the VM.

**agentlab today**: Bootstrap API (10.77.0.1:4242) for secrets, Artifact API (10.77.0.1:4243) for uploads. No built-in services.

**Improvements**:
- Add a metadata endpoint at `169.254.169.254` (cloud-standard metadata IP) providing:
  - Sandbox identity and metadata
  - LLM Gateway proxy (bring-your-own-key, configured at daemon level)
  - Email/notification gateway
  - Secret injection (replacing the bootstrap API)
- This makes sandboxes self-aware and reduces per-sandbox configuration

---

## 8. Built-in Coding Agent Support

**exe.ssh**: Shelley is a web-based coding agent built into the default image. Pre-installed `claude` and `codex`. `new --prompt=` sends an initial task to the agent.

**agentlab today**: Sandboxes are generic. No agent integration.

**Improvements**:
- Pre-bake agent-ready sandbox images (Claude Code, Codex, etc. pre-installed)
- `agentlab sandbox new --prompt="build a web app"` that starts an agent immediately
- Agent-friendly image with tools, git, and AGENTS.md/CLAUDE.md support
- Shelley-like web UI for agent interaction (optional component)

---

## 9. Multi-Tenancy for Self-Hosted Teams

**exe.ssh**: Teams with roles (billing_owner, admin, user), shared quotas, SSO (Google, OIDC), admin SSH access to any member's VM.

**agentlab today**: Single-user. No team concepts.

**Improvements**:
- Multi-user support via SSH keys (natural for self-hosted)
- Role-based access: admin (all sandboxes), user (own sandboxes)
- Optional OIDC integration for enterprise deployments
- Shared resource quotas across users
- `agentlab team add`, `agentlab team members`

---

## 10. Self-Hosted Differentiators

These are areas where agentlab should NOT copy exe.ssh but instead leverage its self-hosted nature:

| exe.ssh (cloud) | agentlab (self-hosted) |
|---|---|
| Locked to their infrastructure | Pluggable backends: Proxmox, libvirt, Docker, cloud APIs |
| Monthly subscription | Free, own your hardware |
| Fixed regions | Run anywhere -- laptop, homelab, colo, multi-site |
| Their domain (exe.xyz) | Your own domain, or `.local` / Tailscale |
| Their auth system | SSH keys + optional OIDC -- your choice |
| No offline mode | Full offline capability |
| Limited customization | Full control over networking, storage, images |

---

## 11. Developer Experience Patterns to Adopt

From exe.ssh's documentation and CLI design:

- **`--json` on every command** -- agentlab already does this, good
- **`defaults` system** -- `agentlab defaults write` for persistent preferences
- **Tab completion** -- generate shell completion scripts from command tree
- **Progressive discovery docs** -- provide an `llms.txt` for agents to discover capabilities
- **One-liner setup** -- `curl ... | bash` installer for the self-hosted case

---

## Priority Recommendations

### High impact, moderate effort
1. SSH gateway for remote daemon access
2. Caddy/Traefik integration with automatic TLS
3. Simplified command surface (aliases for common operations)
4. Metadata endpoint for guest services

### High impact, larger effort
5. LXC/lightweight sandbox backend for fast provisioning
6. Multi-user support via SSH keys
7. Network-level secret injection / integrations system

### Nice to have
8. Built-in agent runner / agent-ready images
9. OIDC/SSO for team deployments
10. Web dashboard for sandbox management
