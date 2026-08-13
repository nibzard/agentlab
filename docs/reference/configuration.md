# Configuration reference

Reference for every `agentlabd` configuration key: type, default, and validation rule. The daemon loads `/etc/agentlab/config.yaml` by default through `internal/config.Load(path)`, validates it, and refuses to start on a fatal error.

## Paths and directories

| Key | Type | Default | Description |
| --- | --- | --- | --- |
| `profiles_dir` | string | `/etc/agentlab/profiles` | Directory holding profile YAML files. Required non-empty. |
| `data_dir` | string | `/var/lib/agentlab` | Base directory for runtime data. Derives `db_path` and `artifact_dir` when unset. |
| `run_dir` | string | `/run/agentlab` | Runtime directory. Derives `socket_path` when unset. |
| `socket_path` | string | `/run/agentlab/agentlabd.sock` | Unix socket path for CLI to daemon traffic. Required non-empty. |
| `db_path` | string | `/var/lib/agentlab/agentlab.db` | SQLite database path. |
| `artifact_dir` | string | `/var/lib/agentlab/artifacts` | Directory for stored artifacts. Required non-empty. |
| `snippets_dir` | string | `/var/lib/vz/snippets` | Proxmox cloud-init snippet directory. |
| `snippet_storage` | string | `local` | Proxmox storage name for snippets. |
| `ssh_public_key_path` | string | `""` | Path to an SSH public key file. Contents are read into `ssh_public_key` at load time. |

## Local control socket

| Key | Type | Default | Description |
| --- | --- | --- | --- |
| `socket_path` | string | `/run/agentlab/agentlabd.sock` | Trusted Unix socket for the CLI. Bypasses network auth. |

## Remote control plane

Disabled by default. When enabled it is the network trust boundary.

| Key | Type | Default | Description |
| --- | --- | --- | --- |
| `control_listen` | string | `""` (disabled) | Optional TCP `host:port` for the remote control plane. Bind to loopback or a Tailscale IP. |
| `control_auth_token` | string | `""` | Bearer token required for `control_listen`. Required when `control_listen` is set. |
| `control_allow_cidrs` | []string | `[]` | CIDR allowlist for the control listener. Required when `control_listen` binds to a wildcard address. |
| `authorized_keys_path` | string | `""` | Path to SSH `authorized_keys` enabling SSH-signed control-plane tokens. |
| `cli_path` | string | `""` (auto) | Path to the `agentlab` CLI binary. Enables `POST /v1/exec` and `/v1/exec/dry-run` when resolvable. |

## Guest listeners

Bound to the agent subnet by default and reachable only by guests.

| Key | Type | Default | Description |
| --- | --- | --- | --- |
| `bootstrap_listen` | string | `10.77.0.1:8844` | Bootstrap and metadata API listen address. |
| `artifact_listen` | string | `10.77.0.1:8846` | Artifact upload API listen address. |
| `agent_subnet` | string | `""` (auto) | CIDR of the agent VM network. Required when a guest listener binds to a wildcard. |
| `controller_url` | string | `""` (auto) | External URL of the bootstrap API. Required when `bootstrap_listen` is a wildcard. Must include an `http(s)` scheme. |
| `artifact_upload_url` | string | `""` (auto) | External URL of the artifact upload API. Required when `artifact_listen` is a wildcard. Must include an `http(s)` scheme. |
| `metadata_routing_enabled` | bool | `false` | Install iptables DNAT so `169.254.169.254` traffic reaches the bootstrap listener. |

## Metrics

| Key | Type | Default | Description |
| --- | --- | --- | --- |
| `metrics_listen` | string | `""` (disabled) | Prometheus metrics listen address. Must bind to loopback only. The convention is `127.0.0.1:8847`. |

Validation rejects `0.0.0.0` and any non-loopback host.

## Proxmox backend

| Key | Type | Default | Description |
| --- | --- | --- | --- |
| `proxmox_backend` | string | `shell` | Backend selection: `shell` or `api`. |
| `proxmox_clone_mode` | string | `linked` | Template clone mode: `linked` (fast) or `full` (independent disks). |
| `proxmox_api_url` | string | `https://localhost:8006` | Proxmox REST API base URL. |
| `proxmox_api_token` | string | `""` | Proxmox API token. Required when `proxmox_backend` is `api`. |
| `proxmox_node` | string | `""` (auto) | Proxmox node name. Auto-detected when empty. |
| `proxmox_tls_insecure` | bool | `false` | Skip Proxmox TLS verification. Cannot be `true` when `proxmox_tls_ca_path` is set. |
| `proxmox_tls_ca_path` | string | `""` | Optional CA bundle path for Proxmox API verification. |
| `proxmox_api_shell_fallback` | bool | `false` | Let the API backend fall back to `pvesm` for volume snapshot and clone operations. |
| `proxmox_command_timeout` | duration | `2m` | Timeout for a single Proxmox command. Must be non-negative. |
| `provisioning_timeout` | duration | `10m` | Timeout for the whole VM provisioning process. Must be non-negative. |

!!! warning "proxmox_backend default is shell"
    The code default for `proxmox_backend` is `shell` (the `qm` / `pvesh` / `pvesm` CLI tools). An earlier revision of this documentation listed `api`. The `api` backend is recommended for production and requires `proxmox_api_token`. See [../explanation/shell-vs-api-backend.md](../explanation/shell-vs-api-backend.md).

## Secrets

| Key | Type | Default | Description |
| --- | --- | --- | --- |
| `secrets_dir` | string | `/etc/agentlab/secrets` | Directory for encrypted secrets bundles. Required non-empty. |
| `secrets_bundle` | string | `default` | Default secrets bundle name to load. |
| `secrets_age_key_path` | string | `/etc/agentlab/keys/age.key` | Path to the age private key used to decrypt bundles. |
| `secrets_sops_path` | string | `sops` | Path to the `sops` binary for reading `.sops.*` bundles. |

## Artifacts

| Key | Type | Default | Description |
| --- | --- | --- | --- |
| `artifact_max_bytes` | int64 | `268435456` (256 MB) | Per-request upload body size limit, wired to `http.MaxBytesReader`. The token remains reusable. Must be greater than zero. |
| `artifact_token_ttl_minutes` | int | `1440` (24h) | Per-job artifact upload token lifetime in minutes. Must be greater than zero. |

## Rate limiting

Per-source-IP limits on guest-facing endpoints. A QPS or burst of `0` disables the limiter.

| Key | Type | Default | Description |
| --- | --- | --- | --- |
| `bootstrap_rate_limit_qps` | float64 | `1` | Per-IP QPS limit for `POST /v1/bootstrap/fetch`. |
| `bootstrap_rate_limit_burst` | int | `3` | Per-IP burst for the bootstrap limiter. |
| `artifact_rate_limit_qps` | float64 | `5` | Per-IP QPS limit for `POST /upload`. |
| `artifact_rate_limit_burst` | int | `10` | Per-IP burst for the artifact limiter. |

## Idle stop and resource pool

| Key | Type | Default | Description |
| --- | --- | --- | --- |
| `idle_stop_enabled` | bool | `true` | Master switch for auto-stopping idle RUNNING sandboxes. |
| `idle_stop_interval` | duration | `1m` | Interval of the idle-stop loop. Must be positive when enabled. |
| `idle_stop_minutes_default` | int | `30` | Minutes of inactivity before an idle sandbox is stopped. Set to `0` to disable. |
| `idle_stop_cpu_threshold` | float64 | `0.05` | CPU usage threshold below which a sandbox is considered idle. Must be in `[0,1]`. |
| `pool_total_cores` | int | `0` (unlimited) | Total physical CPU cores available for sandbox over-commit tracking. |
| `pool_total_memory_mb` | int | `0` (unlimited) | Total RAM in MiB available for sandbox over-commit tracking. |

!!! note "Pool admission is not yet documented"
    `pool_total_cores` and `pool_total_memory_mb` are tracked and exposed at `GET /v1/pool/status`, but the admission policy and rejection behavior are not yet documented.

## Offline and proxy

| Key | Type | Default | Description |
| --- | --- | --- | --- |
| `offline` | bool | `false` | Block all outbound external network calls for air-gapped deployments. Overridable by `-offline`. |
| `proxy_tls_mode` | string | `""` | TLS mode for the reverse proxy. Cannot be `letsencrypt` when `offline` is true. |
| `proxy_domain` | string | `""` | Base domain used by the reverse proxy for sandbox subdomains. |
| `integrations_enabled` | bool | `false` | Enable the integrations system and its control-plane API routes. |

## Profiles

Profiles are YAML files in `profiles_dir` and may contain multiple documents. Required fields are `name` and `template_vmid`. Profile behavior fields override the global defaults at provisioning time.

| Profile field | Type | Default | Description |
| --- | --- | --- | --- |
| `network.mode` | string | `nat` | Network policy: `off`, `nat`, or `allowlist`. Maps to Proxmox firewall groups `agent_nat_off`, `agent_nat_default`, `agent_nat_allowlist`. |
| `behavior.inner_sandbox` | string | `""` | Inner sandbox isolation. Only `bubblewrap` is supported besides empty or none. |
| `behavior.inner_sandbox_args` | []string | none | Extra bubblewrap arguments appended token by token. |
| `behavior.idle_stop_minutes_default` | int | inherits global | Per-profile override of idle stop minutes. Set to `0` to disable for that profile. |

Profile host mounts (`host_mount`, `bind_mount`, `virtiofs`, and any key matching host plus mount, path, or bind) are rejected at provisioning. For the full profile and template field constraints, see [profile-and-template-schema.md](profile-and-template-schema.md).

## Validation rules

`Config.Validate()` enforces the following before the daemon starts:

- Required fields are non-empty (`profiles_dir`, `socket_path`, `secrets_dir`, `artifact_dir`).
- `host:port` and URL fields parse, and URLs include an `http(s)` scheme.
- CIDR fields parse.
- `metrics_listen` binds to loopback only.
- `control_listen` requires `control_auth_token`; wildcard binds additionally require `control_allow_cidrs`.
- Wildcard `bootstrap_listen` or `artifact_listen` require `agent_subnet` plus `controller_url` or `artifact_upload_url`.
- `proxmox_backend` is `shell` or `api`; `api` requires `proxmox_api_token`; `proxmox_tls_insecure` cannot be true when `proxmox_tls_ca_path` is set.
- `proxy_tls_mode` cannot be `letsencrypt` when `offline` is true.
- Timeouts and TTLs are non-negative or positive as noted above.

## File permissions

The config file `/etc/agentlab/config.yaml` must be owner-readable, not accessible by others, and not group-writable or group-executable. Group-readable mode only warns; prefer `chmod 0600`. `agentlabd` refuses to start if the file is world-readable or group-writable.

`agentlab init --apply` (run as root) and `scripts/install_host.sh --enable-remote-control` write `control_listen` and `control_auth_token` to the config file and set its permissions to `0600`.

```bash
sudo agentlab init --apply --control-port 8845 --rotate-control-token
```

## Environment variables do not override config

The `AGENTLAB_*` environment variables drive CLI and client connection behavior only. `Load()` does not read `AGENTLAB_*` variables for any daemon config field. To change daemon behavior, edit the config file and restart `agentlabd`. See [global-flags-env-and-exit-codes.md](global-flags-env-and-exit-codes.md) for the client-side variables.

## Related

- [agentlabd flags](agentlabd-flags.md) for the `-config` and `-offline` overrides.
- [Listeners and ports](listeners-and-ports.md) for what each listener address binds.
- [HTTP API reference](http-api.md) for the routes the config controls.
