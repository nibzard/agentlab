# Operator runbook

This runbook covers day-2 operations for the AgentLab control plane on a Proxmox host.
It assumes the host has Proxmox VE installed and that you are operating as root or
using sudo.

## Quick reference

Services:
- agentlabd: `systemctl status agentlabd.service`
- AgentLab nftables rules: `systemctl status agentlab-nftables.service`

Key paths:
- Config: `/etc/agentlab/config.yaml`
- Profiles: `/etc/agentlab/profiles/*.yaml`
- Secrets bundles: `/etc/agentlab/secrets`
- Age key: `/etc/agentlab/keys/age.key`
- Database: `/var/lib/agentlab/agentlab.db`
- Artifacts: `/var/lib/agentlab/artifacts`
- Logs: `/var/log/agentlab/agentlabd.log`
- Socket: `/run/agentlab/agentlabd.sock`
- Cloud-init snippets (default): `/var/lib/vz/snippets`

Cloud-init snippets are visible in the Proxmox UI and API to anyone who can view
VM config or snippet storage. They include the one-time bootstrap token, controller
URL, and VMID. The token is short-lived and single-use, but treat snippet contents
as sensitive and restrict Proxmox access accordingly. Snippets are deleted when
sandboxes are destroyed; remove stale snippets manually if a VM is kept or snapshotted.

CLI reference:
- `docs/cli.md` (auto-generated from `agentlab --help`)

Network defaults:
- Agent bridge: `vmbr1` on `10.77.0.0/16` (host `10.77.0.1`)
- NAT egress: `vmbr0`
- Tailscale interface: `tailscale0`

## Idle auto-stop

AgentLab can auto-stop RUNNING sandboxes when there are no active SSH sessions
and CPU usage stays under a low threshold for N minutes. SSH activity is
detected via host conntrack entries (established flows to `sandbox_ip:22`).

Defaults (in `/etc/agentlab/config.yaml`):

```yaml
idle_stop_enabled: true
idle_stop_interval: 1m
idle_stop_minutes_default: 30
idle_stop_cpu_threshold: 0.05
```

Per-profile override (set to `0` to disable idle stop for that profile):

```yaml
behavior:
  idle_stop_minutes_default: 15
```

Notes:
- `conntrack` (from `conntrack-tools`) must be available on the host.
- If SSH detection fails, the idle stop loop will skip stopping the sandbox.

## First-time setup checklist

1) Build binaries on the host:

```bash
make build
```

2) Install binaries + systemd unit:

```bash
sudo scripts/install_host.sh
```

3) Create the agent bridge + enable IP forwarding:

```bash
sudo scripts/net/setup_vmbr1.sh --apply
```

4) Install NAT + egress/tailnet block rules:

```bash
sudo scripts/net/apply.sh --apply
```

5) Configure Tailscale subnet routing (approve the route in the admin console):

```bash
sudo scripts/net/setup_tailscale_router.sh --apply
```

6) Create a secrets bundle and config file.

- Follow `docs/secrets.md` to create `/etc/agentlab/secrets/default.age` (or sops).
- Create `/etc/agentlab/config.yaml` with at least an SSH key so cloud-init can
  install access for operators:

```yaml
ssh_public_key_path: /etc/agentlab/keys/agentlab_id_ed25519.pub
secrets_bundle: default
# Optional: Prometheus metrics on localhost only.
metrics_listen: "127.0.0.1:8847"
```

Permissions: ensure `/etc/agentlab/config.yaml` is `0600`. `agentlabd` fails
startup if the file is world-readable or group-writable, and warns on `0640`.

7) Create at least one profile in `/etc/agentlab/profiles/`. Only `name` and
`template_vmid` are required today; the raw YAML is stored for future use.

```yaml
name: yolo-ephemeral
template_vmid: 9000
```

8) Build the VM template (defaults to VMID 9000):

```bash
sudo scripts/create_template.sh
```

9) Restart the daemon after config or profile changes:

```bash
sudo systemctl restart agentlabd.service
```

## One-command onboarding (`agentlab init`)

`agentlab init` runs a read-only checklist for the most common host prerequisites:
`vmbr1` bridge, IP forwarding, nftables rules, snippets directory, profiles, templates,
the AgentLab skill bundle, and the optional remote control plane.

```bash
agentlab init
```

To apply the recommended host setup steps (bridge + nftables + template creation +
remote control config), run:

```bash
sudo agentlab init --apply
```

Notes:
- `agentlab init --apply` reuses `scripts/net/setup_vmbr1.sh`,
  `scripts/net/apply.sh`, `scripts/create_template.sh`, and
  `scripts/install_host.sh --install-skills-only` for skill-bundle updates.
  Run it from the repo root, or pass `--assets /path/to/agentlab` if the
  scripts are elsewhere.
- Use `--force` to overwrite managed network config files if you previously edited them.
- The default template VMID is taken from your profiles (or 9000 if none are found).
- `agentlab init` check status includes `skill_bundle` with `missing`/`upgrade`/`ok`
  states to make bundle drift visible.

To validate end-to-end provisioning (bootstrap + artifacts), run the smoke test:

```bash
agentlab init --smoke-test
```

The smoke test uses `scripts/tests/golden_path.sh`, which starts a temporary
local git repo server and runs a job that uploads a golden artifact.
It requires `python3` (or `git daemon`) on the host.

### AgentLab skill bundle upgrades and rollback

Skill bundles are manifest-driven. A running version is tracked in
`/etc/agentlab/config.yaml` as:

```yaml
claude_skill_bundle_name: agentlab
claude_skill_bundle_version: "1.0.0"
```

Upgrade behavior:

- `scripts/install_host.sh --install-skills-only` upgrades only when the source bundle
  name/version differs and files differ, then writes updated metadata.
- `agentlab init --apply` runs that same install step automatically and reports a skipped
  step when the bundle is already current.

Rollback options:

- Pin a version during host script execution with `CLAUDE_SKILL_VERSION=<version>` to
  prevent accidental cross-release installs.
- Switch back to the previous checkout (or bundle source directory), then rerun:

```bash
sudo scripts/install_host.sh --install-skills-only
```

If needed, remove `/home/<user>/.claude/skills/agentlab` (or the value of
`CLAUDE_SKILLS_DIR`) and reinstall from the desired commit.

## Remote CLI (tailnet)

Enable the TCP control plane on the host, then connect from another machine (Mac, CI runner, etc.).
For a copy/paste quickstart that covers bootstrap, connect, and non-interactive SSH, see `docs/remote-cli.md`.

Quick enable (host):

```bash
sudo scripts/install_host.sh --enable-remote-control
# or:
sudo agentlab init --apply
```

This configures `control_listen` on `127.0.0.1:8845`, generates a token if missing, and publishes the port
with `tailscale serve` when Tailscale is running. The script prints a copy-pastable `agentlab connect` command.

Connect from another machine:

```bash
agentlab connect --endpoint http://host.tailnet.ts.net:8845 --token <token>
```

This writes a local client config file at `$XDG_CONFIG_HOME/agentlab/client.json` (or `~/.config/agentlab/client.json`) with permissions set to `0600`. Commands will use the saved endpoint and token automatically.

Precedence (highest to lowest): CLI flags → environment variables (`AGENTLAB_ENDPOINT`, `AGENTLAB_TOKEN`) → config file → defaults.

To remove the saved config:

```bash
agentlab disconnect
```

## Bootstrap from a laptop (Mac/Linux)

`agentlab bootstrap` provisions a Proxmox host end-to-end over SSH. It uploads the host scripts, configures `vmbr1` and nftables, optionally enables Tailscale subnet routing, installs `agentlabd`, enables the remote control plane, and writes your local client config so future `agentlab ...` commands work without flags.

Requirements:
- SSH access to the Proxmox host (key-based or Tailscale SSH).
- Passwordless sudo if you are not connecting as `root`.
- Linux binaries available via `dist/agentlab_linux_amd64` + `dist/agentlabd_linux_amd64`, or a release URL for the bootstrap to download.

Run from the repo root (or pass `--assets` to point at the repo):

```bash
agentlab bootstrap --host root@proxmox.example \
  --release-url https://example.com/agentlab/releases/v0.1.0
```

Optional flags:
- `--ssh-user`, `--ssh-port`, `--identity` to control SSH connection settings.
- `--control-token` or `--rotate-control-token` to manage the control auth token.
- `--tailscale-authkey` (and optional `--tailscale-hostname`) to bring up Tailscale and advertise the agent subnet automatically.
- `--tailscale-serve` or `--no-tailscale-serve` to force serve publishing.
- `--force` to overwrite managed network files (`vmbr1` or nftables) when they already exist.

After a successful bootstrap, your client config is written to `~/.config/agentlab/client.json`, and you can run:

```bash
agentlab status
```

## Branch sessions

Use branch sessions to tie a persistent workspace to a git branch name.
The branch name is slugified (lowercase, non-alphanumerics become `-`) and
prefixed with `branch-` to form the deterministic session name.

Create or switch to a branch session:

```bash
agentlab session branch feature/login --profile yolo-workspace
```

If the session does not exist, AgentLab creates a workspace named
`branch-<slug>` with the default size/storage (80GB on `local-zfs`). Override
with `--workspace`, `--workspace-create`, `--workspace-size`, or
`--workspace-storage`.

Run a job against the branch session workspace:

```bash
agentlab job run \
  --repo https://github.com/org/repo \
  --task "run tests" \
  --profile yolo-workspace \
  --branch feature/login
```

When `--branch` is used, the job runs against the session workspace and
updates the session `current_vmid` to the new sandbox. If two branch names
slugify to the same session name, the command will error if the existing
session is labeled with a different branch.

## Template build and updates

Build the template with defaults:

```bash
sudo scripts/create_template.sh
```

Common overrides:

```bash
sudo scripts/create_template.sh \
  --vmid 9100 \
  --name agentlab-ubuntu-2404 \
  --storage local-zfs \
  --bridge vmbr1
```

Checksum verification (recommended when downloading images):

```bash
sudo scripts/create_template.sh \
  --image-sha256-url https://cloud-images.ubuntu.com/noble/current/SHA256SUMS
```

Notes:
- `scripts/create_template.sh` requires `qm` (Proxmox) and `virt-customize`
  (`libguestfs-tools`) unless you pass `--skip-customize`.
- Use `--image-sha256 <sha>` or `--image-sha256-url <url>` to verify the image
  download. The script uses `sha256sum` (or `shasum -a 256`) and exits on
  mismatch.
- The script exits if the VMID already exists. For updates, create a new VMID
  and update your profile `template_vmid` to the new value.
- If you change guest tooling (agent-runner, workspace units, CLI versions),
  rebuild the template and update profiles to the new VMID.

## Inner sandboxing (bubblewrap)

AgentLab can optionally run the agent CLI inside a bubblewrap mount namespace
within the guest. This is enabled per profile.

Enable in a profile:

```yaml
behavior:
  inner_sandbox: bubblewrap
  inner_sandbox_args:
    - --bind
    - /scratch
    - /scratch
```

Notes:
- The default sandbox uses a read-only root and rebinds `/tmp`, `/var/tmp`,
  `/run`, the repo path, and `$HOME` as writable; `/run/agentlab/secrets` is
  re-bound read-only.
- `inner_sandbox_args` entries are appended as individual bubblewrap arguments.
  List each token separately.
- Ensure `bubblewrap` is installed in the guest template (rebuild the template
  or install the package inside the image).
- If bubblewrap fails with "permission denied", enable unprivileged user
  namespaces in the guest (`kernel.unprivileged_userns_clone=1`).
- Emergency disable: set `AGENTLAB_INNER_SANDBOX=0` in
  `/etc/agentlab/agent-runner.env`.

Tradeoffs:
- Pros: reduces accidental writes to system paths, limits persistence, adds
  mount/pid namespace isolation within the guest.
- Cons: requires unprivileged user namespaces; can break tools that expect
  writable `/run` or `/var`; not a full security boundary; no network isolation.

## Secrets rotation

Follow the rotation steps in `docs/secrets.md`:

1) Create a new bundle (for example, `default-2026-01-30.age`).
2) Update `secrets_bundle` in `/etc/agentlab/config.yaml`.
3) Restart `agentlabd`.
4) Keep the old bundle until all running sandboxes complete, then revoke old
   tokens and remove the old bundle file.

Age key rotation:
- Generate a new age key, re-encrypt the bundle, update
  `secrets_age_key_path`, then restart `agentlabd`.

## Tailscale routing

Advertising the subnet route:

```bash
sudo scripts/net/setup_tailscale_router.sh --apply --subnet 10.77.0.0/16
```

Verify:
- `tailscale status` shows the host as a subnet router.
- Approve the route in the Tailscale admin console if required.
- From a tailnet device, `ssh agent@10.77.x.y` should work once the route is
  accepted and the sandbox is running.

Optional: auto-approve the subnet route via the Tailscale Admin API:
- This is client-side only and opt-in because the credentials are sensitive.
- Provide one of the following before running `agentlab bootstrap`:
- `AGENTLAB_TAILSCALE_API_KEY` and `AGENTLAB_TAILSCALE_TAILNET` (or `-` for the default tailnet).
- `AGENTLAB_TAILSCALE_OAUTH_CLIENT_ID`, `AGENTLAB_TAILSCALE_OAUTH_CLIENT_SECRET`, and optional `AGENTLAB_TAILSCALE_OAUTH_SCOPES`.
- Or store the same values under `tailscale_admin` in `~/.config/agentlab/client.json` (file is forced to `0600`).
- `agentlab bootstrap` will attempt to approve the `agent_subnet` route and report the result.

If tailnet access fails:
- Confirm `tailscale0` exists and has an IP.
- Verify nftables rules are active: `systemctl status agentlab-nftables.service`.
- Re-run `scripts/net/apply.sh --apply` if rules were not installed.

## Remote SSH (direct + ProxyJump fallback)

`agentlab ssh` prefers a direct connection to the sandbox over the tailnet subnet
route (`10.77.0.0/16`). It probes TCP/22; if reachable, it prints a direct SSH
command. If not reachable, it can fall back to a jump host via SSH ProxyJump.

Direct mode prerequisites:
- The subnet route is approved in the Tailscale admin console.
- The client device is accepting routes (for macOS, run `tailscale up --accept-routes`
  or enable subnet routes in the Tailscale app).

ProxyJump setup (recommended for remote clients that cannot accept routes):
1) Save jump defaults when you connect:
```bash
agentlab connect --endpoint http://host.tailnet.ts.net:8845 --token <token> --jump-user <user>
```
2) SSH normally:
```bash
agentlab ssh <vmid>
```

Ad-hoc ProxyJump (no saved config):
```bash
agentlab ssh <vmid> --jump-host host.tailnet.ts.net --jump-user <user>
```

Non-interactive tips:
- Use SSH keys (preferred) or enable Tailscale SSH for the jump host and sandbox.

## Remote control plane (tailnet-friendly)

AgentLabd can expose the control API over TCP so you can run `agentlab` from
another tailnet device. This listener is optional and must be protected with a
Bearer token.

Quick enable (host):

```bash
sudo scripts/install_host.sh --enable-remote-control
# or:
sudo agentlab init --apply
```

The installer writes `control_listen`/`control_auth_token` to `/etc/agentlab/config.yaml`
(permissions set to `0600`). Re-running the installer reuses the existing token unless you
pass `--rotate-control-token` or `--control-token`.

Recommended pattern A: bind to loopback and publish with Tailscale Serve:

```yaml
control_listen: "127.0.0.1:8845"
control_auth_token: "replace-with-generated-token"
control_allow_cidrs:
  - "127.0.0.1/32"
```

```bash
sudo tailscale serve --tcp=8845 tcp://127.0.0.1:8845
```

Recommended pattern B: bind directly to the host's tailnet IP:

```yaml
control_listen: "100.64.12.34:8845"
control_auth_token: "replace-with-generated-token"
control_allow_cidrs:
  - "100.64.0.0/10"
```

Notes:
- `control_auth_token` is required whenever `control_listen` is set.
- Wildcard binds (`0.0.0.0` or `[::]`) are rejected unless `control_allow_cidrs`
  is explicitly configured.
- When using Tailscale Serve as a proxy, `RemoteAddr` will typically be
  `127.0.0.1`, so include `127.0.0.1/32` in the allowlist if you enable it.
- `GET /v1/host` returns the daemon version, agent subnet, and MagicDNS name
  (when available), which helps remote clients auto-configure endpoints.

Threat model:
- The control plane can create/destroy VMs and access artifact metadata.
  Treat the token as a high-privilege secret and rotate it if exposed.
- Prefer tailnet-only access; do not expose the control listener to LAN/WAN
  directly.

## Tailscale Serve exposures

AgentLab can expose sandbox ports over the tailnet using host-level Tailscale Serve.
Each exposure maps `host.tailnet.ts.net:<port>` to the sandbox IP and performs a
TCP health check (plus an optional HTTP probe on common HTTP ports).

Requirements:
- `tailscale` CLI installed and logged in on the host.
- MagicDNS enabled in the tailnet for stable DNS names.

Troubleshooting:
- Inspect active rules: `tailscale serve status`
- Remove a stale rule: `tailscale serve --tcp=<port> off`
- If exposure creation fails, confirm `tailscale status --json` works and the
  daemon can execute `tailscale` from its environment.

Notes:
- Exposures are removed automatically when the owning sandbox is destroyed
  (best-effort).

## Debugging stuck sandboxes

1) Identify the sandbox and state:

```bash
agentlab sandbox list
agentlab sandbox show <vmid>
```

2) Check events and job state:

```bash
agentlab logs <vmid> --tail 200
agentlab job show <job_id> --events-tail 200
```

3) Check daemon health and logs:

```bash
systemctl status agentlabd.service
journalctl -u agentlabd.service -n 200 --no-pager
tail -n 200 /var/log/agentlab/agentlabd.log
```

4) Check Proxmox VM state and guest agent:

```bash
qm status <vmid>
qm config <vmid>
qm agent <vmid> ping
```

5) Verify cloud-init snippet exists (default path) and is referenced:

```bash
ls -l /var/lib/vz/snippets
qm config <vmid> | grep -E 'cicustom|cloudinit'
```

6) Validate network policy from a tailnet device:

```bash
scripts/net/smoke_test.sh --ip <sandbox_ip> --ssh-key <path>
```

Common causes:
- QEMU guest agent missing or stopped in the template.
- Invalid profile `template_vmid` after a template rebuild.
- Missing secrets bundle or invalid `secrets_bundle` name in config.
- Tailnet route not approved in the admin console.

## Daemon recovery

Restart the daemon:

```bash
sudo systemctl restart agentlabd.service
```

Verify:
- `systemctl status agentlabd.service` is active.
- The socket exists and is group-writable: `/run/agentlab/agentlabd.sock`.
- Users who run `agentlab` are in the `agentlab` group:

```bash
sudo usermod -aG agentlab <user>
```

If the daemon will not start:
- Check `/etc/agentlab/config.yaml` for YAML errors.
- Confirm `/etc/agentlab/profiles` contains valid YAML with `name` and
  `template_vmid`.
- Inspect logs with `journalctl -u agentlabd.service`.
