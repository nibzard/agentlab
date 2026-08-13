# How to build and run the SSH gateway

Build the optional `agentlab-ssh-gateway` binary and use it to expose `agentlabd`
over SSH. Clients authenticate by public key, then run `agentlab` CLI commands in
exec mode or connect straight into a sandbox in proxy mode.

## Prerequisites

- A Go toolchain that satisfies the project's `go.mod`.
- `agentlabd` running with its Unix socket at
  `/run/agentlab/agentlabd.sock`.
- A client public key per user, and a host-owned SSH private key that can reach
  sandboxes.

!!! warning "Not released"
    The gateway is built behind the `sshgateway` build tag and is excluded from
    GoReleaser releases. Build and run it yourself.

## Steps

1. Build the binary with the build tag:

    ```bash
    make build-ssh-gateway
    ```

   Or directly: `go build -tags sshgateway -o bin/agentlab-ssh-gateway ./cmd/agentlab-ssh-gateway`.

2. Create an `authorized_keys` file with one client public key per line. The
   gateway matches the SHA256 fingerprint of the presented key. An empty file is
   a fatal startup error.

3. Run the gateway. The defaults are `0.0.0.0:2222`, profile `yolo-ephemeral`,
   and sandbox user `agent` on port 22:

    ```bash
    agentlab-ssh-gateway \
       --listen 0.0.0.0:2222 \
       --authorized-keys /etc/agentlab/keys/ssh_gateway_authorized_keys \
       --sandbox-key /etc/agentlab/keys/agentlab_id_ed25519 \
       --socket /run/agentlab/agentlabd.sock
    ```

4. Connect with an SSH username that selects the action:

   | Username          | Action                                         |
   |-------------------|------------------------------------------------|
   | `new`             | Create a sandbox from the default profile.      |
   | `new+<profile>`   | Create a sandbox from the named profile.        |
   | `sbx-<vmid>`      | Connect to an existing sandbox.                 |
   | `<vmid>`          | Shorthand for `sbx-<vmid>`.                     |

    ```bash
    ssh new@gateway-host
    ssh new+yolo-workspace@gateway-host
    ssh sbx-1001@gateway-host
    ```

5. In exec mode, pass a known `agentlab` command and the gateway forwards it to
   the daemon over the socket:

    ```bash
    ssh gateway-host sandbox list
    ```

## Verify

- A `new` connection provisions a sandbox and drops you into a shell as `agent`.
- An `sbx-<vmid>` connection starts the sandbox if `STOPPED`, waits for an IP,
  then bridges the session.

## Hardening notes

- Protect the sandbox SSH private key as root-only on the host.
- Sandbox host keys are pinned per VMID by trust-on-first-use. A recreated VMID
  rotates its pin before the new key is trusted; any later mismatch is rejected
  as impersonation.
- An idle watchdog closes idle sessions after the timeout (default 5m), and
  outbound keepalive requests default to every 30s.

!!! note "Gateway events are not implemented"
    The SSH gateway spike lists planned event types `ssh.gateway.connect`,
    `ssh.gateway.create`, and `ssh.gateway.error`, but these are not implemented
    in the current source. Do not rely on them for auditing yet.
