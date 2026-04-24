# agentlab-ssh-gateway

SSH gateway that provides remote access to the agentlab daemon API. Supports two modes:

1. **CLI exec mode**: Run any `agentlab` command over SSH, e.g. `ssh host sandbox list --json`
2. **Proxy mode**: Interactive SSH sessions proxied to sandbox VMs, e.g. `ssh new@host`

## Build

Use the build tag or the Makefile target:

```bash
make build-ssh-gateway
# or
GOFLAGS="" go build -tags sshgateway -o bin/agentlab-ssh-gateway ./cmd/agentlab-ssh-gateway
```

## Run

```bash
bin/agentlab-ssh-gateway \
  --listen 0.0.0.0:2222 \
  --authorized-keys /etc/agentlab/keys/ssh_gateway_authorized_keys \
  --sandbox-key /etc/agentlab/keys/agentlab_id_ed25519 \
  --socket /run/agentlab/agentlabd.sock
```

The gateway auto-detects the `agentlab` CLI binary from `PATH`. Override with `--cli-path`.

## Usage

### CLI commands over SSH

Any `agentlab` CLI command works identically over SSH:

```bash
# List sandboxes
ssh -p 2222 user@agentlab.myserver.com sandbox list --json

# Create a sandbox
ssh -p 2222 user@agentlab.myserver.com sandbox new --profile yolo-ephemeral

# Check status
ssh -p 2222 user@agentlab.myserver.com status

# View logs
ssh -p 2222 user@agentlab.myserver.com logs 1001 --tail 50

# Job operations
ssh -p 2222 user@agentlab.myserver.com job show abc123
```

All CLI flags work over SSH exactly as they do locally. The gateway routes commands to the local `agentlab` binary with `--socket` set to the daemon's Unix socket.

### Interactive sandbox proxy

For interactive SSH access to sandboxes, use username-based routing:

```bash
# Create a new sandbox and get an interactive shell
ssh -p 2222 new@agentlab.myserver.com

# Create with a specific profile
ssh -p 2222 new+yolo-ephemeral@agentlab.myserver.com

# Connect to an existing sandbox by VM ID
ssh -p 2222 sbx-1001@agentlab.myserver.com
ssh -p 2222 1001@agentlab.myserver.com
```

## Authentication

Authentication uses SSH public keys via an `authorized_keys` file. Only keys listed in the file are accepted.

```bash
# Generate a key pair
ssh-keygen -t ed25519 -f ~/.ssh/agentlab_gateway -C "agentlab-gateway"

# Add to authorized_keys
echo "ssh-ed25519 AAAA... user@host" >> /etc/agentlab/keys/ssh_gateway_authorized_keys
```

## SSH client configuration

Add to `~/.ssh/config` for convenience:

```
Host agentlab
    HostName agentlab.myserver.com
    Port 2222
    IdentityFile ~/.ssh/agentlab_gateway
```

Then use:

```bash
ssh agentlab sandbox list --json
ssh new@agentlab
```

## Configuration flags

| Flag | Default | Description |
|------|---------|-------------|
| `--listen` | `0.0.0.0:2222` | Listen address |
| `--socket` | `/run/agentlab/agentlabd.sock` | Daemon Unix socket |
| `--authorized-keys` | `/etc/agentlab/keys/ssh_gateway_authorized_keys` | Authorized keys file |
| `--host-key` | `/etc/agentlab/keys/ssh_gateway_host_ed25519` | SSH host key (auto-generated if missing) |
| `--sandbox-key` | `/etc/agentlab/keys/agentlab_id_ed25519` | Key for connecting to sandboxes |
| `--cli-path` | auto-detect | Path to `agentlab` binary |
| `--profile` | `yolo-ephemeral` | Default profile for `new` proxy |
| `--keepalive` | `true` | Set keepalive on new sandboxes |
| `--wait-timeout` | `4m` | Timeout for sandbox provisioning |
| `--idle-timeout` | `5m` | Idle timeout for SSH connections |
| `--keepalive-interval` | `30s` | SSH keepalive interval |
