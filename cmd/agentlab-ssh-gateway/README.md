# agentlab-ssh-gateway (experimental)

This directory contains an experimental SSH gateway spike. It is intentionally excluded from default builds/tests because it depends on an unstable prototype API.

## Build

Use the build tag or the Makefile target:

```bash
make build-ssh-gateway
# or
GOFLAGS="" go build -tags sshgateway -o bin/agentlab-ssh-gateway ./cmd/agentlab-ssh-gateway
```

## Run (example)

```bash
bin/agentlab-ssh-gateway \
  --listen 0.0.0.0:2222 \
  --authorized-keys /etc/agentlab/keys/ssh_gateway_authorized_keys \
  --sandbox-key /etc/agentlab/keys/agentlab_id_ed25519 \
  --socket /run/agentlab/agentlabd.sock
```

See `docs/ssh_gateway_spike.md` for the spike design notes and limitations.
