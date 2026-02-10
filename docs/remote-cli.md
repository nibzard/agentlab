# Remote CLI Quickstart

This page gets you from zero to fully remote control: install AgentLab on a Proxmox host, enable tailnet access, connect from your Mac, and use `agentlab ssh` (direct or via ProxyJump).

## Prereqs

- Proxmox host reachable over SSH (key-based or Tailscale SSH).
- Tailscale installed on the host and your Mac, in the same tailnet.
- `agentlab` CLI on your Mac (build from source or install a release).

## Control-plane topology (choose one)

### Topology A: loopback + Tailscale Serve (recommended)

This is what `scripts/install_host.sh --enable-remote-control`, `agentlab init --apply`, and `agentlab bootstrap` configure. The daemon listens on `127.0.0.1:8845` and Tailscale Serve publishes it to the tailnet.

```yaml
control_listen: "127.0.0.1:8845"
control_auth_token: "<token>"
```

Optional allowlist when you set `control_allow_cidrs`:

```yaml
control_allow_cidrs:
  - "127.0.0.1/32"
```

### Topology B: bind directly to the tailnet IP

This skips Tailscale Serve and listens on the host's tailnet IP.

```yaml
control_listen: "100.64.12.34:8845"
control_auth_token: "<token>"
control_allow_cidrs:
  - "100.64.0.0/10"
```

Restart `agentlabd` after changing config:

```bash
sudo systemctl restart agentlabd.service
```

## Quickstart path 1: bootstrap from your Mac (recommended)

1. Build the CLI and Linux binaries from the repo (or use a release URL).

```bash
make build
```

2. Run bootstrap (uploads scripts/binaries, configures networking, enables remote control).

```bash
agentlab bootstrap --host root@proxmox.example \
  --release-url https://example.com/agentlab/releases/v0.1.0 \
  --tailscale-authkey tskey-XXXXXXXXXXXX \
  --tailscale-hostname agentlab-proxmox
```

3. After bootstrap completes, your client config is written and you can verify:

```bash
agentlab status
```

Notes:
- Bootstrap uses Topology A and publishes the control plane via `tailscale serve` when Tailscale is running.
- To skip serve, add `--no-tailscale-serve`. To force serve, add `--tailscale-serve`.
- You can pass `--control-port`, `--control-token`, or `--rotate-control-token` to customize control-plane auth.
- If you built `dist/agentlab_linux_amd64` and `dist/agentlabd_linux_amd64`, you can omit `--release-url` and bootstrap will upload them.

## Quickstart path 2: manual host install

Run these on the Proxmox host:

```bash
make build
sudo scripts/install_host.sh --enable-remote-control
sudo scripts/net/setup_vmbr1.sh --apply
sudo scripts/net/apply.sh --apply
sudo scripts/net/setup_tailscale_router.sh --apply
```

Optional remote-control flags for the installer:

```bash
sudo scripts/install_host.sh --enable-remote-control \
  --control-port 8845 \
  --control-token <token> \
  --rotate-control-token \
  --tailscale-serve
```

The installer prints a ready-to-run `agentlab connect ...` command. Run it on your Mac.

Ensure SSH access works by setting a public key in `/etc/agentlab/config.yaml`:

```yaml
ssh_public_key_path: /etc/agentlab/keys/agentlab_id_ed25519.pub
```

Copy the matching private key to your Mac and use `--identity` if it is not your default SSH key.

## Connect from your Mac

```bash
agentlab connect --endpoint http://host.tailnet.ts.net:8845 --token <token>
```

Config is stored in `~/.config/agentlab/client.json` (or `$XDG_CONFIG_HOME/agentlab/client.json`) with `0600` permissions.

Precedence (highest to lowest):
1. CLI flags `--endpoint` / `--token`
2. Environment `AGENTLAB_ENDPOINT` / `AGENTLAB_TOKEN`
3. Saved client config

Remove saved config:

```bash
agentlab disconnect
```

## SSH from your Mac

### Direct mode (preferred)

If the tailnet subnet route is approved and your Mac accepts routes:

```bash
agentlab ssh <vmid>
```

### ProxyJump fallback

If you cannot accept routes, set a jump host:

```bash
agentlab connect --endpoint http://host.tailnet.ts.net:8845 --token <token> \
  --jump-host host.tailnet.ts.net \
  --jump-user <user>
```

Or ad-hoc:

```bash
agentlab ssh <vmid> --jump-host host.tailnet.ts.net --jump-user <user>
```

`agentlab ssh` probes TCP/22 on the sandbox IP. If the direct route is not reachable and a jump host is configured, it switches to ProxyJump automatically.

## Non-interactive SSH guidance

- Use key-based auth or Tailscale SSH so `ssh` never prompts.
- `agentlab ssh --exec` requires a TTY. For scripts, use the printed command or `--json` output.
- Example non-interactive command using your key:

```bash
ssh -o BatchMode=yes -i ~/.ssh/agentlab_id_ed25519 agent@10.77.0.130 -- uname -a
```

## Troubleshooting

### No route to 10.77.0.0/16

- Check route approval in the Tailscale admin console.
- Verify the host is advertising the subnet:

```bash
tailscale status
```

- Re-run the router setup if needed:

```bash
sudo scripts/net/setup_tailscale_router.sh --apply
```

### Route not approved

- In the Tailscale admin console, approve the `10.77.0.0/16` subnet route for the Proxmox host.
- Wait a minute, then retry `agentlab ssh <vmid>`.

### macOS not accepting routes

- Run:

```bash
sudo tailscale up --accept-routes
```

- Or enable subnet routes in the Tailscale macOS app.

### ProxyJump auth failures

- Verify the jump host works directly:

```bash
ssh <user>@host.tailnet.ts.net
```

- If needed, specify a key:

```bash
agentlab ssh <vmid> --jump-host host.tailnet.ts.net --jump-user <user> \
  --identity ~/.ssh/agentlab_id_ed25519
```

- If you use Tailscale SSH, confirm it is enabled for the jump host and your user.
