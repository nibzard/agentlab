# Security reference

AgentLab separates a trusted local control path from network-exposed guest and
remote-control paths. This page lists the authentication, authorization,
rate-limit, file-permission, and network-isolation controls the daemon
enforces. For the design rationale, see
../explanation/control-plane-and-trust-boundaries.md and
../explanation/network-isolation-model.md.

## Control-plane authentication

The `/v1/*` control API is served on two paths with different trust levels.

| Path | Default address | Authentication | Trust level |
| --- | --- | --- | --- |
| Local Unix socket | `/run/agentlab/agentlabd.sock` | None (trusted) | Full access |
| Remote TCP listener | `control_listen` (disabled) | Bearer token plus optional CIDR | Per-route scoped |

The auth middleware (`internal/auth/middleware.go`):

- Accepts an SSH-signed token (when `authorized_keys_path` is set) or the legacy
  pre-shared bearer token (`control_auth_token`).
- Enforces `control_allow_cidrs` when set. Requests from outside the allowlist
  get HTTP 403 `remote address not allowed`.
- Exempts `/healthz` and non-`/v1` paths from authentication.

The local Unix socket bypasses the auth wrapper; `ControlAPI.authorize` treats
it as full-access. Remote TCP traffic is wrapped with `WrapNetwork` and scoped
per-route by the handlers. Token comparison is constant-time.

## Remote control requirements

`control_listen` rules, validated at startup:

- `control_auth_token` is required whenever `control_listen` is set.
- Wildcard binds (`0.0.0.0` or `[::]`) are rejected unless `control_allow_cidrs`
  is explicitly configured.
- The remote endpoint URL must include an explicit `http://` or `https://`
  scheme; a bare host:port is rejected.
- Plaintext HTTP to a non-loopback host is rejected unless the caller passes
  `--allow-insecure-http` or sets `AGENTLAB_ALLOW_INSECURE_HTTP` (intended only
  inside a trusted tunnel such as Tailscale).

## Guest-facing endpoints

The bootstrap and artifact listeners are guest-only and restricted to the agent
subnet.

| Endpoint | Default address | Authentication | Rate limit |
| --- | --- | --- | --- |
| `POST /v1/bootstrap/fetch` | `10.77.0.1:8844` | One-time bootstrap token plus vmid | `bootstrap_rate_limit_qps` / `burst` |
| `POST /upload` | `10.77.0.1:8846` | Per-job artifact bearer token | `artifact_rate_limit_qps` / `burst` |

- Requests from outside `agent_subnet` get HTTP 403.
- Rate limits are per source IP. Setting qps or burst to `0` disables limiting.
- Default rate limits: bootstrap 1 QPS / burst 3; artifact 5 QPS / burst 10.
- Wildcard binds for these listeners require `agent_subnet` plus
  `controller_url` (bootstrap) or `artifact_upload_url` (artifact).

See reference/listeners-and-ports.md for the full listener catalog.

## Token lifecycles

| Token | Scope | Default TTL | Storage |
| --- | --- | --- | --- |
| Bootstrap token | Single sandbox bootstrap fetch | 10 minutes | Hashed at rest, single-use, consumed on success |
| Artifact upload token | Single job upload | `artifact_token_ttl_minutes` (default 1440 = 24h) | Not stored in the secrets bundle |
| Tailscale per-VM auth key | Single VM enrollment | 1 hour | Single-use, preauthorized after the bootstrap token is consumed |

## Secrets at rest and in delivery

- Secrets bundles are encrypted at rest with age (or sops) under `secrets_dir`.
  The daemon decrypts in memory on demand. Plaintext reads and writes require
  `--allow-plaintext`.
- Secrets are delivered to guests through the one-time bootstrap fetch into
  tmpfs at `/run/agentlab/secrets` and wiped on stop. They are never written to
  sandbox disk.
- Secret values are redacted in API responses and CLI output by default; the
  daemon scrubs staged secrets from logs. Use `agentlab secrets show --reveal`
  to display raw values.
- The Tailscale Admin API key is scrubbed from logs and responses and is never
  written to cloud-init snippets.

See reference/secrets.md for the bundle format.

## Host config permissions

`config.CheckConfigPermissions` validates `/etc/agentlab/config.yaml` at
startup.

| Rule | Result |
| --- | --- |
| Owner-readable | Required (error if not) |
| Accessible by others (mode bits `0o007`) | Error |
| Group-writable or group-executable | Error |
| Group-readable | Warning (prefer `chmod 0600`) |

`agentlab init --apply` and `scripts/install_host.sh --enable-remote-control`
set the config file to mode `0600`.

## Network isolation

Sandbox VMs get full outbound Internet while the daemon blocks the following
egress from `vmbr1`:

- RFC1918 ranges: `10/8`, `172.16/12`, `192.168/16`.
- IPv6 ULA and link-local (`fc00::/7`, `fe80::/10`).
- New sandbox-to-tailnet connections. Established replies are allowed.

Profiles select a firewall group through `network.mode`:

| Mode | Firewall group |
| --- | --- |
| `off` | `agent_nat_off` |
| `nat` | `agent_nat_default` |
| `allowlist` | `agent_nat_allowlist` |

See ../explanation/network-isolation-model.md.

## Host bind-mount guard

Profiles that request host mounts are rejected at provisioning time. Detected
keys include `host_mount`, `bind_mount`, `virtiofs`, and any key matching
host+(mount/path/bind) or bind+mount. Workspace disks are the supported
persistence mechanism.

## Proxmox credentials

The recommended backend is the API token. Rules:

- `proxmox_backend: api` requires `proxmox_api_token` in the form
  `user@realm!tokenid=uuid`. Token presence is checked at config load; the
  format is parsed when the API backend is constructed
  (`proxmox.NewAPIBackend`).
- `proxmox_tls_insecure` cannot be `true` when `proxmox_tls_ca_path` is set.
- The shell backend runs `qm`, `pvesh`, and `pvesm` directly, so the daemon
  process needs execute rights. The systemd unit must not set
  `NoNewPrivileges` or `PrivateTmp`; they break `qm` IPC with
  `ipcc_send_rec` errors.

See ../explanation/shell-vs-api-backend.md and
../how-to/configure-proxmox-api-backend.md.

## Error responses

Server errors return a stable envelope with `error`, `code`, and `message`
fields. Redacted `details` are returned by default for 4xx responses. The
`X-AgentLab-Debug: true` header only surfaces additional 5xx detail.

## Cloud-init snippet exposure

Cloud-init snippets under `/var/lib/vz/snippets` contain the one-time bootstrap
token, controller URL, and VMID. They are visible in the Proxmox UI and API.
Restrict Proxmox access and delete snippets for VMs you keep or snapshot.
Destroyed sandboxes clean up their own snippets.

## Offline mode

`offline: true` (or `agentlabd -offline`) blocks all outbound HTTP to public
addresses, allowing only loopback, link-local, RFC1918, and `fc00::/7`
destinations. It disables the Tailscale exposure publisher and rejects
`proxy_tls_mode: letsencrypt`. See ../how-to/run-air-gapped-offline.md.
