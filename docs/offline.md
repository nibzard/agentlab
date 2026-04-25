# Offline / Air-Gapped Setup Guide

AgentLab supports fully offline (air-gapped) deployments where the host has no internet access. This is a key self-hosted advantage over cloud-hosted alternatives.

## How Offline Mode Works

When offline mode is enabled:

- All outbound HTTP requests to external (public) addresses are blocked via an offline transport layer
- Only private/local network destinations are allowed: loopback (`127.0.0.0/8`, `::1`), link-local (`169.254.0.0/16`, `fe80::/10`), RFC-1918 private (`10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`), and unique local IPv6 (`fc00::/7`)
- The Tailscale exposure publisher is disabled (requires internet coordination)
- LLM, Git, and HTTP proxy integrations only forward to private addresses
- Docker backend requires pre-pulled images

## Enabling Offline Mode

### Option 1: CLI Flag

```bash
agentlabd --offline
```

### Option 2: Configuration File

```yaml
# /etc/agentlab/config.yaml
offline: true
```

### Option 3: Combined with Self-Signed Proxy

For air-gapped deployments that still need TLS and subdomain routing:

```yaml
# /etc/agentlab/config.yaml
offline: true
proxy_enabled: true
proxy_domain: agentlab.local
proxy_tls_mode: self-signed
proxy_ca_dir: /etc/agentlab/ca
```

## Preparing Images for Offline Use

### Docker Backend

All container images must be pre-pulled before starting the daemon:

```bash
# Pull images while internet is available
docker pull ubuntu:22.04
docker pull debian:12
docker pull ghcr.io/agentlab/agent-base:latest

# Verify images are available locally
docker images
```

When offline mode is active and an image is not found locally, the daemon returns an error:

```
image ubuntu:22.04 not found locally and offline mode prevents pulling from registry;
run 'docker pull ubuntu:22.04' before starting the daemon
```

### Proxmox / LXC Backend

VM templates must exist on the Proxmox node before provisioning:

```bash
# Import template while internet is available
# Templates are stored in Proxmox and do not require internet at runtime
```

### Libvirt Backend

Disk images must be available in the libvirt pool:

```bash
# Pre-download base images
virsh vol-create-as default base-ubuntu.qcow2 10G
```

## Networking in Offline Mode

### Metadata Endpoint

The metadata endpoint at `169.254.169.254` works fully offline since it is a link-local address. Sandboxes can access:

- `GET /identity` - Sandbox metadata
- `GET /metadata` - Key-value pairs
- `GET /proxy/...` - Credential proxy (private upstreams only)

### Local LLM Proxy

To provide LLM access to sandboxes in an air-gapped environment, run a local LLM server (e.g., Ollama) and configure an LLM integration pointing to it:

```bash
# Install and start Ollama on the host or a local server
ollama serve
ollama pull codellama

# Add LLM integration pointing to local Ollama
agentlab integration add llm-proxy \
  --name=local-llm \
  --target=http://10.0.0.1:11434 \
  --provider=ollama
```

Since Ollama runs on a private address, the offline transport allows requests through.

### Reverse Proxy with Self-Signed CA

For subdomain-based sandbox access without internet:

```yaml
proxy_enabled: true
proxy_domain: agentlab.local
proxy_tls_mode: self-signed
```

The self-signed CA generates certificates locally with no OCSP or CRL checks that would require internet.

## Validation

Offline mode has these validation rules:

1. `proxy_tls_mode` cannot be `letsencrypt` (ACME requires internet)
2. All integration proxy targets must resolve to private addresses
3. Docker images must be pre-pulled (verified at daemon start)

## Limitations

| Feature | Offline Support |
|---------|----------------|
| Sandbox create/start/stop/destroy | Full |
| Metadata endpoint (169.254.169.254) | Full |
| Self-signed TLS / Caddy proxy | Full |
| LLM proxy (local Ollama) | Full |
| Git proxy (local Gitea) | Full |
| HTTP proxy (private upstream) | Full |
| Let's Encrypt TLS | Not supported |
| Tailscale exposure | Not supported |
| Docker image pull | Pre-pull required |
| Cloud LLM providers (OpenAI, Anthropic) | Not supported |
