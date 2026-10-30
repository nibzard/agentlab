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

## Use the resumable setup command

`agentlab setup` composes SSH bootstrap, initialization, connection verification,
and a sandbox readiness test. The client includes installation assets and uses a verified local cache.
A released client downloads Linux binaries for the server architecture from the
same pinned release version. It verifies both archives against `checksums.txt`
before extraction. The verified cache also works offline.

Use `--agentlab-bin` and `--agentlabd-bin` to supply explicit offline binaries.
A development client requires those paths, or an explicit `--assets` source tree
with matching `dist/` binaries. It never selects `latest` as a fallback.

```bash
agentlab setup --host server.example --repo . --assets /path/to/agentlab --plan
agentlab setup --host server.example --repo . --assets /path/to/agentlab
```

The first command checks the host and saves a plan without installing software.
The second command applies that plan. Existing SSH credentials and trusted host
keys must work in batch mode. Use `--ssh-user`, `--ssh-port`, and `--identity`
when the default SSH settings do not apply. Non-root users need non-interactive
sudo access. Setup never requests a password through hidden input.

Setup checks the server platform, installation permissions, Proxmox tools, and
free template storage before mutation. It chooses artifacts for the server
architecture. It compares the SSH identity with the authenticated API identity
before saving the connection. An older daemon without identity support produces
an explicit verification failure.

The command prints an operation ID before submission. After submission, inspect,
resume, or cancel the operation through the same trusted SSH connection:

```bash
agentlab --json operation show OPERATION_ID
agentlab --json operation resume OPERATION_ID
agentlab --json operation cancel OPERATION_ID
```

Use `--no-wait` to return after starting the server worker. Otherwise, setup waits
for its result. A wait timeout stops observation. The worker can continue on the
server. `operation resume` observes active work and finishes the client connection.
If the worker stopped, it checks external state before repeating a setup step.
A planned operation still needs the original setup command to submit it.

The server worker runs as a transient systemd service. It uses one host lock and
a stable unit name. The server stores its journal under `/var/lib/agentlab/setup`.
The client saves a private reference and observations. Observations preserve the
server event cursor and progress timestamps. They do not create a second progress
history. SSH loss does not cancel an admitted server operation.

Keep the original settings and artifact contents. The default request key comes
from the normalized settings and deterministic payload digest. An explicit key
accepts 1 to 128 letters, digits, dots, colons, underscores, or hyphens. A changed
input with that key returns a conflict. Keys expire after 30 days. Inspect retained
resources before choosing a new key. Expired requests can still be cancelled.

Staging uses immutable files in a private directory owned by root. Every worker
invocation verifies its ownership and pinned digest. Old binaries without setup
worker protocol 1 block before installation. An interrupted initial upload needs
the original local binaries and assets. Later recovery can use the server payload.
After a host reboot, use `operation resume`; automatic reboot restart is not
implemented. A saved running state alone does not prove worker liveness.

Setup records a sandbox ID before creation. Recovery checks that sandbox before
creating another one. The readiness test checks guest health and executes
`/bin/true` inside the guest. It checks the recorded sandbox against backend inventory before deletion.
Cleanup requires backend absence; a database tombstone alone is insufficient. Live Proxmox ownership and crash
trials still require acceptance testing.

`client_connection_state` separates server completion from client enrollment.
It remains `pending` until the endpoint authenticates, its identity matches SSH,
and the destination context is saved. The context then becomes active. A failed
verification preserves the working context. The `verified` state and timestamp
record that completed check; `operation show` does not revalidate credentials.
Context switching does not redirect an operation that is already running.

Cancellation saves a local intent before contacting the server. The worker checks
cancellation during execution and attempts readiness cleanup. Installed software
and network changes remain. Inspect `worker_stopped`, `work_stopped`, cleanup,
and retained resources. A stopped worker does not prove that external Proxmox
tasks stopped. Staging files, the pinned worker, the request, and configuration
backups can remain for inspection. No automatic rollback is claimed.

The local repository remains unchanged. Setup records its revision; repository
transfer and task execution use the repository and job commands. Process fixture
trials cover client death, worker death, and cancellation. Native systemd service
startup, fresh Proxmox hosts, power loss, and mixed versions still need validation.

The default assets come from the client binary. The current working directory
does not select installation scripts. An explicit `--assets` path overrides the
shipped assets. The cache uses a content hash and rejects modified scripts,
symlinks, and unexpected files. Remove a damaged cache directory and retry;
the client recreates it from its embedded assets.

The networking service uses a helper under `/usr/local/lib/agentlab/net`.
This managed helper remains after temporary setup assets are removed. An old
generated unit can migrate from a temporary helper path without overwriting other
unit changes.

Automatic downloads use the official `nibzard/agentlab` GitHub repository.
HTTPS protects the release source. SHA-256 checksums detect damaged or changed
archives. These checks are not a release signature. Download redirects cannot
downgrade HTTPS. The client rejects unsafe archive entries and bounds download
and extraction sizes. It publishes the cache only after both binaries verify.

Cache reads verify the executable hashes. A changed cache returns an error with
a corrective action. A retry removes only incomplete downloads for the same
release request. Explicit binary paths and download sources never silently switch
to another source after a failure.
