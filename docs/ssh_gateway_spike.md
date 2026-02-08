# SSH Gateway Spike: Username-Routed SSH Access

## Summary
This spike explores an optional SSH gateway that maps SSH usernames to sandbox create/connect actions. The gateway runs on the Proxmox host, authenticates users via `authorized_keys`, calls the existing AgentLab `/v1` API over the Unix socket, and proxies an SSH session into the sandbox.

Primary UX targets:
- `ssh new@host` creates a sandbox and attaches immediately.
- `ssh sbx-<vmid>@host` connects to an existing sandbox.
- `ssh <vmid>@host` is shorthand for `sbx-<vmid>`.

## Goals
- Reduce friction for humans who want “just SSH” workflows.
- Avoid requiring tailnet subnet routing on every device (optional complement).
- Keep policy control in `agentlabd` (creation, TTLs, audit events).

## Non-goals
- Replace `agentlab ssh` or remove subnet routing immediately.
- Provide multi-tenant RBAC beyond key-based allowlists in this spike.
- Perfect SSH feature parity (agent forwarding, SFTP, etc.).

## Auth Model
- Gateway authenticates users via a dedicated `authorized_keys` file.
- Each accepted public key maps to a principal (fingerprint); this can be used for auditing.
- No password auth, no keyboard-interactive auth.
- Gateway uses a separate SSH private key to connect to sandboxes (host-owned, root-only).

## Username Grammar
- `new` -> create a sandbox using a default profile.
- `new+<profile>` / `new:<profile>` / `new-<profile>` -> create with explicit profile.
- `sbx-<vmid>` -> connect to existing sandbox VMID.
- `<vmid>` -> shorthand for `sbx-<vmid>`.

## Routing Logic
1. Accept SSH connection and authenticate public key.
2. Parse username into a route target (new vs existing, profile, vmid).
3. For `new`:
   - `POST /v1/sandboxes` with `profile` and `keepalive` (policy-defined).
4. For existing:
   - `GET /v1/sandboxes/{vmid}` and `POST /v1/sandboxes/{vmid}/start` if STOPPED.
5. Poll until sandbox has an IP, then open an SSH session to that IP.

## Attach Mechanism
- The gateway proxies an SSH session by opening a second SSH connection to the sandbox.
- It forwards PTY and window resize requests and bridges stdin/stdout/stderr.
- The gateway does not expose sandbox IPs directly to clients.

## Audit Logging (future)
- Add event types: `ssh.gateway.connect`, `ssh.gateway.create`, `ssh.gateway.error`.
- Include key fingerprint, requested username, resolved VMID, and profile.
- The spike only logs locally; production should push to the AgentLab events table.

## Networking Impact
- **Complement** to Tailscale subnet routing:
  - Clients connect to the host over Tailscale (or LAN) only.
  - No requirement to accept subnet routes for `10.77.0.0/16` on every device.
  - Host-only routing reduces inbound access from tailnet to the agent subnet.
- **Tradeoff**: gateway becomes a choke point and requires hardening.

Decision for now: treat the gateway as a complement, not a replacement. Subnet routing remains valuable for power users and debugging.

## Threat Model Notes
- Gateway holds sandbox SSH private key. Protect it (root-only, host hardening).
- Compromise of gateway host still means full control; gateway does not increase that risk, but it centralizes access.
- Strong key hygiene is required for users; no password auth.

## Prototype Summary
Implemented a minimal SSH gateway prototype at `cmd/agentlab-ssh-gateway/main.go`.

Behavior:
- Accepts SSH public-key auth via `authorized_keys`.
- Parses username routing rules above.
- Calls `agentlabd` over the Unix socket to create/lookup/start sandboxes.
- Proxies SSH sessions into sandbox IPs using the host’s sandbox SSH key.

Run example:
```
agentlab-ssh-gateway \
  --listen 0.0.0.0:2222 \
  --authorized-keys /etc/agentlab/keys/ssh_gateway_authorized_keys \
  --sandbox-key /etc/agentlab/keys/agentlab_id_ed25519 \
  --socket /run/agentlab/agentlabd.sock
```

Connect examples:
```
ssh new@host -p 2222
ssh sbx-1020@host -p 2222
ssh 1020@host -p 2222
```

Known limitations:
- Uses `InsecureIgnoreHostKey()` for the sandbox SSH host key.
- No per-user policy controls (profile allowlist, max concurrency).
- No session accounting in the AgentLab events table yet.

## Next Steps (if pursued)
- Add per-key policy mapping (allowed profiles, max sandboxes, TTL overrides).
- Integrate event logging into `agentlabd`.
- Replace insecure host key handling with a known_hosts store.
- Optional: support `ProxyJump` mode and SFTP.
