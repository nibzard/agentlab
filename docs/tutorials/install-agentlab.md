# Install AgentLab

Install the CLI on your laptop. Run the Linux-only daemon on the Proxmox host.
Laptop users can stop after connecting to an existing host; host operators continue
with the host setup guide.

## Prerequisites

- A Linux or macOS host with `curl` or `wget`.
- Write access to `/usr/local/bin`, or `sudo` for that directory.
- GoReleaser release artifacts are published for `linux/amd64`, `linux/arm64`, `darwin/amd64`, and `darwin/arm64`. The daemon is Linux-only.
- For source builds, Go 1.25 or later, as required by `go.mod`.

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

3. Laptop users: connect to an existing control plane using credentials supplied
   by its operator. Use HTTPS, or explicitly acknowledge HTTP inside a trusted
   tunnel with `--allow-insecure-http`.

    ```bash
    agentlab connect --endpoint https://host.example:8845 --token <token>
    agentlab status --json
    ```

   Client settings use the OS user config directory: `~/Library/Application Support/agentlab`
   on macOS; `$XDG_CONFIG_HOME/agentlab` or `~/.config/agentlab` on Linux. The daemon stays on the Linux host.

   (Optional) Install shell completion for your shell.

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
    git clone https://github.com/nibzard/agentlab.git
    cd agentlab
    go build -o agentlab ./cmd/agentlab
    go build -o agentlabd ./cmd/agentlabd
    ```

5. Create the daemon directories and a minimal config. The daemon reads `/etc/agentlab/config.yaml` by default and listens on the Unix socket at `/run/agentlab/agentlabd.sock`.

    ```bash
    sudo mkdir -p /etc/agentlab /etc/agentlab/profiles /var/lib/agentlab /run/agentlab
    sudo install -m 0600 /dev/null /etc/agentlab/config.yaml
    sudo tee /etc/agentlab/config.yaml >/dev/null <<'YAML'
    ssh_public_key_path: /etc/agentlab/keys/agentlab_id_ed25519.pub
    secrets_bundle: default
    bootstrap_listen: 10.77.0.1:8844
    artifact_listen: 10.77.0.1:8846
    controller_url: http://10.77.0.1:8844
    YAML
    ```

6. Complete [Proxmox host setup](set-up-proxmox-host.md), including the bridge,
   SSH key, profiles, and template. Then start the daemon and check the socket.

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
