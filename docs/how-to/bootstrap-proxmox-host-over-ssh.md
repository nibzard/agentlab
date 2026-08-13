# How to bootstrap a Proxmox host over SSH

Provision a remote Proxmox VE host end to end from your workstation with the
`agentlab bootstrap` command. Bootstrap uploads binaries, configures the
`vmbr1` bridge and nftables rules, installs and starts `agentlabd`, always
enables the remote control plane, and optionally joins Tailscale, then writes
a local client config so the CLI can drive the new host.

## Prerequisites

- Root SSH access to a Proxmox VE host (8.x or 9.x). Backend choice (`shell` or
  `api`) is config-driven (`proxmox_backend`), not version-driven; either
  backend works with either version.
- Either a release archive URL, or local `agentlab` and `agentlabd` binaries.
- Optionally a Tailscale auth key if you want the host on your tailnet.

## Steps

1. Bootstrap from a release archive. Bootstrap is a local-first command. It
   binds the common connection flags (`--endpoint`/`--token`/`--socket`) but
   ignores them; only `--json` is meaningful:

    ```bash
    agentlab bootstrap --host root@proxmox.example \
       --release-url https://example.com/agentlab/releases/vX.Y.Z
    ```

2. Or bootstrap from binaries you already built with `make build`:

    ```bash
    agentlab bootstrap --host root@proxmox.example \
       --agentlab-bin ./bin/agentlab --agentlabd-bin ./bin/agentlabd
    ```

3. Enable the remote control plane during bootstrap so a workstation can reach
   the daemon later. `--control-port` defaults to 8845:

    ```bash
    agentlab bootstrap --host root@proxmox.example \
       --release-url https://example.com/agentlab/releases/vX.Y.Z \
       --control-port 8845 --control-token <token>
    ```

4. Join the host to a tailnet by passing a Tailscale auth key and hostname. This
   is opt-in; without it the host stays off the tailnet:

    ```bash
    agentlab bootstrap --host root@proxmox.example \
       --release-url <url> \
       --tailscale-authkey <tailscale-authkey> \
       --tailscale-hostname agentlab-proxmox
    ```

On success, bootstrap writes a `client.json` for the new endpoint.

## Verify

- `agentlab status` reaches the new daemon. Status has no version field; for the
  version run `agentlab --version` (CLI) or query `GET /v1/host`
  (the `Version` field).
- `agentlab ls` lists sandboxes (empty on a fresh host).

!!! tip "Troubleshoot a failed run"
    Re-run bootstrap with `--verbose` for step-by-step output, or `--keep-temp`
    to retain the temporary asset bundle for inspection. `--force` overwrites an
    existing install. See ../how-to/connect-remote-daemon-over-tailnet.md to
    configure the workstation client after bootstrap.
