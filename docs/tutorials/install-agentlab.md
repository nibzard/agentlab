# Install AgentLab

Goal: install the `agentlab` CLI and the `agentlabd` daemon, then confirm both run.

## Prerequisites

- A Linux or macOS host with `curl` or `wget`.
- Write access to `/usr/local/bin`, or `sudo` for that directory.
- GoReleaser release artifacts are published for `linux/amd64`, `linux/arm64`, `darwin/amd64`, and `darwin/arm64`. The daemon is Linux-only.
- For source builds, Go 1.24 or later. The `go.mod` toolchain line pins this.

## Steps

1. Install the CLI with the one-liner installer. The script downloads the archive for your platform and installs the `agentlab` binary into `/usr/local/bin`.

    ```bash
    curl -fsSL https://agentlab.dev/install.sh | bash
    ```

   To pin a release, pass `--version`:

    ```bash
    curl -fsSL https://agentlab.dev/install.sh | bash -s -- --version v0.4.0
    ```

2. Verify the CLI.

    ```bash
    agentlab --version
    ```

3. (Optional) Install shell completion for your shell.

    ```bash
    echo 'eval "$(agentlab completion bash)"' >> ~/.bashrc
    ```

   `agentlab completion` also supports `zsh` and `fish`.

4. On the host that will run sandboxes, install the daemon. The daemon is a Linux-only release archive named `agentlabd_<tag>_<os>-<arch>.tar.gz`. Download the archive from the GitHub release page, extract it, and install the binary:

    ```bash
    install -m 0755 agentlabd /usr/local/bin/agentlabd
    agentlabd -version
    ```

   If you have Go installed, build both binaries from source instead:

    ```bash
    go install github.com/agentlab/agentlab/cmd/agentlab@latest
    go install github.com/agentlab/agentlab/cmd/agentlabd@latest
    ```

5. Create the daemon directories and a minimal config. The daemon reads `/etc/agentlab/config.yaml` by default and listens on the Unix socket at `/run/agentlab/agentlabd.sock`.

    ```bash
    sudo mkdir -p /etc/agentlab /etc/agentlab/profiles /var/lib/agentlab /run/agentlab
    sudo chmod 0600 /etc/agentlab/config.yaml
    ```

6. Start the daemon and check that it answers on the socket.

    ```bash
    sudo agentlabd -config /etc/agentlab/config.yaml
    ```

   In a second terminal, with the CLI on the same host:

    ```bash
    agentlab status
    ```

## Expected result

`agentlab --version` prints the CLI version. `agentlabd -version` prints the daemon build info. `agentlab status` reaches the daemon over the local socket and returns a control-plane status snapshot. The socket file appears at `/run/agentlab/agentlabd.sock`.

!!! note
    The `agentlabd` daemon must run as root so it can drive Proxmox `qm`/`pvesh` and bind the guest listeners. For a production setup, wrap the daemon in a systemd unit as described in [Set up the Proxmox host](set-up-proxmox-host.md).

## Next

With the binaries installed, continue to [Set up the Proxmox host](set-up-proxmox-host.md). For the full set of daemon flags, see [agentlabd flags](../reference/agentlabd-flags.md).
