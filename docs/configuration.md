# Configuration Reference

This document provides a comprehensive reference for AgentLab configuration options.

## Table of Contents

- [Main Configuration](#main-configuration)
- [Profile Configuration](#profile-configuration)
- [Environment Variables](#environment-variables)
- [Validation Rules](#validation-rules)
- [Example Configurations](#example-configurations)

## Main Configuration

The main configuration file is located at `/etc/agentlab/config.yaml` by default. This file controls the daemon's behavior, network settings, Proxmox backend, and system paths.

### Configuration Fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `profiles_dir` | string | `/etc/agentlab/profiles` | Directory containing profile YAML files |
| `data_dir` | string | `/var/lib/agentlab` | Base directory for runtime data |
| `log_dir` | string | `/var/log/agentlab` | Directory for log files |
| `run_dir` | string | `/run/agentlab` | Directory for runtime socket files |
| `socket_path` | string | `/run/agentlab/agentlabd.sock` | Unix socket path for CLI communication |
| `control_listen` | string | `""` (disabled) | Optional TCP listen address for remote control plane |
| `control_auth_token` | string | `""` | Bearer token required when `control_listen` is set |
| `control_allow_cidrs` | []string | `[]` | Optional CIDR allowlist for remote control (defense in depth) |
| `db_path` | string | `/var/lib/agentlab/agentlab.db` | SQLite database path |
| `bootstrap_listen` | string | `10.77.0.1:8844` | Bootstrap API listen address |
| `artifact_listen` | string | `10.77.0.1:8846` | Artifact upload API listen address |
| `metrics_listen` | string | `""` (disabled) | Prometheus metrics listen address (localhost only) |
| `agent_subnet` | string | `""` (auto) | CIDR for agent VM network (e.g., `10.77.0.0/16`) |
| `controller_url` | string | `""` (auto) | External URL for bootstrap API |
| `artifact_upload_url` | string | `""` (auto) | External URL for artifact upload |
| `artifact_dir` | string | `/var/lib/agentlab/artifacts` | Directory for stored artifacts |
| `artifact_max_bytes` | int64 | `268435456` (256MB) | Maximum artifact size in bytes |
| `artifact_token_ttl_minutes` | int | `1440` (24 hours) | Artifact token TTL in minutes |
| `bootstrap_rate_limit_qps` | float | `1` | Per-IP QPS limit for guest bootstrap fetch (0 disables) |
| `bootstrap_rate_limit_burst` | int | `3` | Per-IP burst limit for guest bootstrap fetch (0 disables) |
| `artifact_rate_limit_qps` | float | `5` | Per-IP QPS limit for artifact uploads (0 disables) |
| `artifact_rate_limit_burst` | int | `10` | Per-IP burst limit for artifact uploads (0 disables) |
| `secrets_dir` | string | `/etc/agentlab/secrets` | Directory for encrypted secrets bundles |
| `secrets_bundle` | string | `default` | Default secrets bundle name |
| `secrets_age_key_path` | string | `/etc/agentlab/keys/age.key` | Path to age encryption key |
| `secrets_sops_path` | string | `sops` | Path to sops binary |
| `snippets_dir` | string | `/var/lib/vz/snippets` | Proxmox snippets directory |
| `snippet_storage` | string | `local` | Proxmox storage name for snippets |
| `ssh_public_key` | string | `""` | SSH public key for VM access |
| `ssh_public_key_path` | string | `""` | Path to SSH public key file |
| `proxmox_command_timeout` | duration | `2m` | Timeout for Proxmox shell commands |
| `provisioning_timeout` | duration | `10m` | Timeout for VM provisioning |
| `proxmox_backend` | string | `api` | Proxmox backend: `api` or `shell` |
| `proxmox_clone_mode` | string | `linked` | Clone mode for templates: `linked` (fast, storage-efficient) or `full` (full copy) |
| `proxmox_api_url` | string | `https://localhost:8006` | Proxmox API URL |
| `proxmox_api_token` | string | `""` (required) | Proxmox API token |
| `proxmox_node` | string | `""` (auto) | Proxmox node name (auto-detected if empty) |
| `proxmox_tls_insecure` | bool | `false` | Disable TLS certificate verification for Proxmox API (insecure, explicit opt-in) |
| `proxmox_tls_ca_path` | string | `""` | Optional CA bundle path for Proxmox API verification |
| `proxmox_api_shell_fallback` | bool | `false` | Allow API backend to fall back to shell for volume snapshot/clone |

### Network Configuration

#### Wildcard Bindings

When using wildcard addresses (`0.0.0.0` or `[::]`) for `bootstrap_listen` or `artifact_listen`, additional configuration is required:

- `agent_subnet` must be set to specify the VM network CIDR
- `controller_url` must be set when `bootstrap_listen` uses wildcard
- `artifact_upload_url` must be set when `artifact_listen` uses wildcard

Example wildcard configuration:
```yaml
bootstrap_listen: "0.0.0.0:8844"
artifact_listen: "0.0.0.0:8846"
agent_subnet: "10.77.0.0/16"
controller_url: "https://agentlab.example.com:8844"
artifact_upload_url: "https://agentlab.example.com:8846/upload"
```

#### Metrics Endpoint

The `metrics_listen` endpoint exposes Prometheus metrics and **must** be localhost-only:

- Valid: `localhost:9090`, `127.0.0.1:9090`, `[::1]:9090`
- Invalid: `0.0.0.0:9090`, `10.77.0.1:9090`

#### Remote Control Plane

To enable remote control (tailnet-friendly), set `control_listen` and a bearer token.
The listener is disabled by default and should be bound to loopback or a Tailscale IP.

Quick setup: `scripts/install_host.sh --enable-remote-control` (or `agentlab init --apply`) will
populate `control_listen` and `control_auth_token` in `/etc/agentlab/config.yaml` and set the
file permissions to `0600`. Re-running the installer reuses the existing token unless you
explicitly rotate or override it.

Example (loopback + Tailscale Serve proxy):
```yaml
control_listen: "127.0.0.1:8845"
control_auth_token: "replace-with-generated-token"
control_allow_cidrs:
  - "127.0.0.1/32"
```

Example (bind directly to tailnet IP):
```yaml
control_listen: "100.64.12.34:8845"
control_auth_token: "replace-with-generated-token"
control_allow_cidrs:
  - "100.64.0.0/10"
```

Notes:
- `control_auth_token` is required when `control_listen` is set.
- Wildcard binds (`0.0.0.0` or `[::]`) are rejected unless `control_allow_cidrs` is set.
- `control_allow_cidrs` is optional but recommended for defense in depth.

### Proxmox Backend Configuration

#### Clone Mode

```yaml
proxmox_clone_mode: linked  # or "full" for full clones
```

`linked` clones are faster and more storage-efficient on ZFS/LVM-thin. Use `full` if your storage backend does not support linked clones or if you need independent disks.

#### API Backend (Recommended)

```yaml
proxmox_backend: api
proxmox_clone_mode: linked
proxmox_api_url: https://localhost:8006
proxmox_api_token: root@pam!token=uuid
proxmox_node: pve1  # optional, auto-detected if empty
proxmox_tls_insecure: false  # default; verify TLS certificates
proxmox_api_shell_fallback: false  # set true to allow shell fallback for volume ops
```

To trust a self-signed certificate, provide a CA bundle:

```yaml
proxmox_tls_ca_path: /etc/agentlab/certs/proxmox-ca.pem
```

To disable TLS verification (not recommended), set:

```yaml
proxmox_tls_insecure: true  # INSECURE: disables certificate verification
```

**Advantages:**
- More reliable and consistent
- Better error handling
- No shell escaping issues
- Recommended for production use

#### Shell Backend (Fallback)

```yaml
proxmox_backend: shell
proxmox_clone_mode: linked
proxmox_command_timeout: 5m  # increase for slower systems
```

**Limitations:**
- Requires root or Proxmox API access
- Shell command escaping complexity
- Less reliable parsing of command output
- Fallback option only

### Timeout Configuration

| Timeout | Default | Purpose |
|---------|---------|---------|
| `proxmox_command_timeout` | 2m | Maximum time for single Proxmox shell commands |
| `provisioning_timeout` | 10m | Maximum time for entire VM provisioning process |
| `artifact_token_ttl_minutes` | 1440 | How long artifact upload tokens remain valid |

Durations are specified as Go duration strings: `30s`, `5m`, `1h`, etc.

### Artifact Configuration

Artifacts are files uploaded from sandboxes after job completion:

```yaml
artifact_dir: /var/lib/agentlab/artifacts
artifact_max_bytes: 536870912  # 512MB
artifact_token_ttl_minutes: 2880  # 48 hours
```

Considerations:
- `artifact_max_bytes`: Total upload limit per token
- Storage: Ensure adequate disk space in `artifact_dir`
- Tokens: Single-use tokens with configurable TTL

### Rate Limiting

Guest-facing endpoints are rate limited per IP to reduce abuse from compromised or misbehaving sandboxes:

- `bootstrap_rate_limit_qps` / `bootstrap_rate_limit_burst` apply to `POST /v1/bootstrap/fetch`
- `artifact_rate_limit_qps` / `artifact_rate_limit_burst` apply to `POST /upload`

Set any QPS or burst value to `0` to disable rate limiting for trusted environments.

## Profile Configuration

Profiles are YAML files in `/etc/agentlab/profiles/` that define sandbox behavior, resources, and network settings. Each file can contain multiple profiles (multi-document YAML).

### Required Profile Fields

| Field | Type | Description |
|-------|------|-------------|
| `name` | string | Unique profile identifier |
| `template_vmid` | int | Proxmox template VM ID to clone |

### Optional Profile Fields

#### Network Configuration

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `network.bridge` | string | `vmbr1` | Network bridge for VM |
| `network.model` | string | `virtio` | Network card model |
| `network.mode` | string | `nat` | Networking policy mode: `off`, `nat`, or `allowlist`. AgentLab maps the mode to a firewall group. |
| `network.firewall` | bool | `unset` | Enable Proxmox firewall on the NIC. If omitted, AgentLab leaves the existing setting unchanged unless `network.firewall_group` is set (then it forces firewall on). |
| `network.firewall_group` | string | `""` | Optional Proxmox firewall group applied to the NIC (requires firewall enabled). |

Notes:
- `network.mode` values map to Proxmox firewall groups:
  - `off` -> `agent_nat_off` (no network)
  - `nat` -> `agent_nat_default` (NAT + RFC1918/ULA blocks)
  - `allowlist` -> `agent_nat_allowlist` (egress allowlist rules)
- When `network.mode` is set, AgentLab enables the firewall automatically and applies the mapped firewall group.
- `network.firewall_group` must be non-empty and uses Proxmox firewall group names (for example, `agent_nat_default`). If it is set while `network.firewall: false`, provisioning fails.
- If `network.firewall_group` is set without `network.firewall`, AgentLab enables the firewall for that NIC automatically.

#### Resource Configuration

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `resources.cores` | int | Template value | Number of CPU cores |
| `resources.memory_mb` | int | Template value | Memory in MB |
| `resources.cpulist` | string | `""` | CPU pinning list (e.g., `"2-5,8,10"`) |

#### Behavior Defaults

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `behavior.keepalive_default` | bool | `false` | Default keepalive for sandboxes |
| `behavior.ttl_minutes_default` | int | `0` (no TTL) | Default TTL in minutes |
| `behavior.inner_sandbox` | string | `""` (disabled) | Inner sandbox: `bubblewrap` or `none` |
| `behavior.inner_sandbox_args` | []string | `[]` | Additional inner sandbox arguments |

#### Security Configuration

| Field | Type | Description |
|-------|------|-------------|
| `behavior.inner_sandbox` | string | Inner sandbox type for additional isolation |

**Valid inner_sandbox values:**
- `""`, `"none"`, `"off"`, `"false"`, `"0"`, `"disabled"` - No inner sandbox
- `"true"`, `"yes"`, `"1"` - Enables bubblewrap
- `"bubblewrap"`, `"bwrap"` - Explicit bubblewrap

### Host Mount Restrictions

**Host bind mounts are not allowed** in AgentLab for security reasons. Profiles attempting to specify host mounts will be rejected during provisioning.

Detected host mount keys (rejected):
- `host_mount`, `host_mounts`, `host_path`, `host_paths`
- `bind_mount`, `bind_mounts`, `binds`, `mounts`
- `virtiofs`, `virtio_fs`
- Any key containing "host" + ("mount" or "path" or "bind")
- Any key containing "bind" + "mount"

**Use workspace disks instead** for persistent storage.

### Profile File Format

Single profile:
```yaml
name: yolo-ephemeral
template_vmid: 9000
resources:
  cores: 4
  memory_mb: 8192
network:
  bridge: vmbr1
  model: virtio
  mode: nat
```

Multiple profiles (multi-document YAML):
```yaml
---
name: small
template_vmid: 9000
resources:
  cores: 2
  memory_mb: 4096
---
name: large
template_vmid: 9000
resources:
  cores: 8
  memory_mb: 16384
```

## Environment Variables

Configuration can be overridden via environment variables. Environment variables take precedence over file settings.

| Environment Variable | Config Field | Example |
|---------------------|--------------|---------|
| `AGENTLAB_PROFILES_DIR` | `profiles_dir` | `/opt/agentlab/profiles` |
| `AGENTLAB_DATA_DIR` | `data_dir` | `/mnt/data/agentlab` |
| `AGENTLAB_LOG_DIR` | `log_dir` | `/var/log/agentlab` |
| `AGENTLAB_RUN_DIR` | `run_dir` | `/run/agentlab` |
| `AGENTLAB_SOCKET_PATH` | `socket_path` | `/run/agentlab/agentlabd.sock` |
| `AGENTLAB_DB_PATH` | `db_path` | `/mnt/data/agentlab.db` |
| `AGENTLAB_BOOTSTRAP_LISTEN` | `bootstrap_listen` | `10.77.0.1:8844` |
| `AGENTLAB_ARTIFACT_LISTEN` | `artifact_listen` | `10.77.0.1:8846` |
| `AGENTLAB_METRICS_LISTEN` | `metrics_listen` | `localhost:9090` |
| `AGENTLAB_AGENT_SUBNET` | `agent_subnet` | `10.77.0.0/16` |
| `AGENTLAB_CONTROLLER_URL` | `controller_url` | `https://agentlab.example.com` |
| `AGENTLAB_ARTIFACT_UPLOAD_URL` | `artifact_upload_url` | `https://agentlab.example.com/upload` |
| `AGENTLAB_ARTIFACT_DIR` | `artifact_dir` | `/var/lib/agentlab/artifacts` |
| `AGENTLAB_ARTIFACT_MAX_BYTES` | `artifact_max_bytes` | `536870912` |
| `AGENTLAB_ARTIFACT_TOKEN_TTL_MINUTES` | `artifact_token_ttl_minutes` | `1440` |
| `AGENTLAB_SECRETS_DIR` | `secrets_dir` | `/etc/agentlab/secrets` |
| `AGENTLAB_SECRETS_BUNDLE` | `secrets_bundle` | `default` |
| `AGENTLAB_SECRETS_AGE_KEY_PATH` | `secrets_age_key_path` | `/etc/agentlab/keys/age.key` |
| `AGENTLAB_SECRETS_SOPS_PATH` | `secrets_sops_path` | `/usr/bin/sops` |
| `AGENTLAB_SNIPPETS_DIR` | `snippets_dir` | `/var/lib/vz/snippets` |
| `AGENTLAB_SNIPPET_STORAGE` | `snippet_storage` | `local` |
| `AGENTLAB_SSH_PUBLIC_KEY` | `ssh_public_key` | `"ssh-rsa AAAAB3..."` |
| `AGENTLAB_SSH_PUBLIC_KEY_PATH` | `ssh_public_key_path` | `/etc/agentlab/ssh.pub` |
| `AGENTLAB_PROXMOX_COMMAND_TIMEOUT` | `proxmox_command_timeout` | `5m` |
| `AGENTLAB_PROVISIONING_TIMEOUT` | `provisioning_timeout` | `15m` |
| `AGENTLAB_PROXMOX_BACKEND` | `proxmox_backend` | `api` |
| `AGENTLAB_PROXMOX_CLONE_MODE` | `proxmox_clone_mode` | `linked` |
| `AGENTLAB_PROXMOX_API_URL` | `proxmox_api_url` | `https://localhost:8006` |
| `AGENTLAB_PROXMOX_API_TOKEN` | `proxmox_api_token` | `root@pam!token=uuid` |
| `AGENTLAB_PROXMOX_NODE` | `proxmox_node` | `pve1` |
| `AGENTLAB_PROXMOX_TLS_INSECURE` | `proxmox_tls_insecure` | `false` |
| `AGENTLAB_PROXMOX_TLS_CA_PATH` | `proxmox_tls_ca_path` | `/etc/agentlab/certs/proxmox-ca.pem` |
| `AGENTLAB_PROXMOX_API_SHELL_FALLBACK` | `proxmox_api_shell_fallback` | `true` |

## Validation Rules

### Required Fields

The following fields must be non-empty:
- `profiles_dir`
- `run_dir`
- `socket_path`
- `bootstrap_listen`
- `artifact_listen`
- `artifact_dir`
- `secrets_dir`

### Numeric Constraints

| Field | Constraint | Error Message |
|-------|-----------|---------------|
| `artifact_max_bytes` | Must be > 0 | `artifact_max_bytes must be positive` |
| `artifact_token_ttl_minutes` | Must be > 0 | `artifact_token_ttl_minutes must be positive` |
| `proxmox_command_timeout` | Must be >= 0 | `proxmox_command_timeout must be non-negative` |
| `provisioning_timeout` | Must be >= 0 | `provisioning_timeout must be non-negative` |

### URL Validation

URLs must:
- Include scheme (`http://` or `https://`)
- Include host
- Use only http or https schemes

Invalid URL examples:
- `controller_url: example.com` (missing scheme)
- `controller_url: ftp://example.com` (invalid scheme)
- `controller_url: http://` (missing host)

### CIDR Validation

The `agent_subnet` field must be a valid CIDR notation if specified:

Valid examples:
- `10.77.0.0/16`
- `192.168.1.0/24`
- `fd00::/64` (IPv6)
- `10.77.0.1/32` (single host)

Invalid examples:
- `10.77.0.0` (missing prefix length)
- `not-a-cidr` (invalid format)

### Listen Address Validation

Listen addresses must be in `host:port` format:

Valid examples:
- `10.77.0.1:8844`
- `[fd00::1]:8844` (IPv6)
- `localhost:8844`
- `0.0.0.0:8844` (wildcard)

Invalid examples:
- `8844` (missing host)
- `not-host-port` (invalid format)

### SSH Key Validation

If `ssh_public_key_path` is specified, the file must exist and contain a valid SSH public key. The key will be read and stored in `ssh_public_key` at load time.

### Proxmox Backend Validation

- `proxmox_backend` must be either `"shell"` or `"api"`
- When `proxmox_backend` is `"api"`, `proxmox_api_token` is required
- `proxmox_tls_insecure` cannot be true when `proxmox_tls_ca_path` is set

### Profile Validation

Profiles are validated at provisioning time:

1. **Required fields:** `name` and `template_vmid` must be present
2. **Unique names:** Profile names must be unique across all profile files
3. **Host mounts rejected:** Any profile specifying host mounts will be rejected
4. **Inner sandbox:** Only `"bubblewrap"` is supported for `behavior.inner_sandbox`

## Example Configurations

### Minimal Production Configuration

```yaml
# /etc/agentlab/config.yaml
proxmox_backend: api
proxmox_api_url: https://localhost:8006
proxmox_api_token: root@pam!agentlab-api=uuid-here
```

All other values use defaults.

### Development Configuration

```yaml
# /etc/agentlab/config.yaml
data_dir: /mnt/data/agentlab
log_dir: /var/log/agentlab
bootstrap_listen: localhost:8844
artifact_listen: localhost:8846
metrics_listen: localhost:9090
artifact_max_bytes: 1073741824  # 1GB for testing
proxmox_backend: shell
proxmox_command_timeout: 5m
provisioning_timeout: 15m
```

### Remote Access Configuration

```yaml
# /etc/agentlab/config.yaml
bootstrap_listen: 0.0.0.0:8844
artifact_listen: 0.0.0.0:8846
agent_subnet: 10.77.0.0/16
controller_url: https://agentlab.example.com:8844
artifact_upload_url: https://agentlab.example.com:8846/upload
```

### Profile Examples

#### Ephemeral Job Runner (yolo-ephemeral)

```yaml
# /etc/agentlab/profiles/yolo-ephemeral.yaml
name: yolo-ephemeral
template_vmid: 9000
resources:
  cores: 4
  memory_mb: 8192
network:
  bridge: vmbr1
  model: virtio
  mode: nat
behavior:
  keepalive_default: false
  ttl_minutes_default: 60
  inner_sandbox: bubblewrap
```

#### Development Workspace (yolo-workspace)

```yaml
# /etc/ikola/dev/agentlab/profiles/yolo-workspace.yaml
name: yolo-workspace
template_vmid: 9000
resources:
  cores: 8
  memory_mb: 16384
network:
  bridge: vmbr1
  model: virtio
  mode: nat
behavior:
  keepalive_default: true
  ttl_minutes_default: 0  # no auto-expire
  inner_sandbox: none
```

#### Interactive Development (interactive-dev)

```yaml
# /etc/agentlab/profiles/interactive-dev.yaml
name: interactive-dev
template_vmid: 9000
resources:
  cores: 16
  memory_mb: 32768
network:
  bridge: vmbr1
  model: virtio
  mode: nat
behavior:
  keepalive_default: true
  ttl_minutes_default: 0
```

#### Multi-Profile File

```yaml
# /etc/agentlab/profiles/sizes.yaml
---
name: tiny
template_vmid: 9000
resources:
  cores: 1
  memory_mb: 2048
---
name: small
template_vmid: 9000
resources:
  cores: 2
  memory_mb: 4096
---
name: medium
template_vmid: 9000
resources:
  cores: 4
  memory_mb: 8192
---
name: large
template_vmid: 9000
resources:
  cores: 8
  memory_mb: 16384
---
name: xlarge
template_vmid: 9000
resources:
  cores: 16
  memory_mb: 32768
```

#### CPU-Pinned Profile

```yaml
# /etc/agentlab/profiles/cpu-pinned.yaml
name: high-performance
template_vmid: 9000
resources:
  cores: 4
  memory_mb: 8192
  cpulist: "4-7"  # Pin to physical cores 4-7
network:
  bridge: vmbr1
```

## Validation Error Messages

### Common Errors and Solutions

| Error | Cause | Solution |
|-------|-------|----------|
| `profiles_dir is required` | Missing or empty profiles directory | Create `/etc/agentlab/profiles/` or set `profiles_dir` |
| `socket_path is required` | Missing socket path | Ensure `run_dir` is set or specify `socket_path` directly |
| `bootstrap_listen must be host:port` | Invalid listen address format | Use `host:port` format (e.g., `10.77.0.1:8844`) |
| `agent_subnet is required when bootstrap_listen binds to wildcard` | Wildcard binding without subnet | Set `agent_subnet` when using `0.0.0.0` or `[::]` |
| `controller_url is required when bootstrap_listen binds to wildcard` | Wildcard binding without external URL | Set `controller_url` to external access URL |
| `artifact_max_bytes must be positive` | Invalid artifact size limit | Set `artifact_max_bytes` to a positive integer |
| `proxmox_api_token is required when using api backend` | API backend configured without token | Set `proxmox_api_token` or switch to shell backend |
| `profile has unsupported behavior.inner_sandbox` | Invalid inner sandbox type | Use `bubblewrap` or leave empty for none |
| `profile requests host mounts` | Profile contains host mount keys | Remove host mount configuration, use workspace disks |
