# AgentLab Quickstart

Get from zero to running sandboxes in 5 minutes.

## Install

```bash
# One-liner installer
curl -fsSL https://agentlab.dev/install.sh | bash

# Or with specific version
curl -fsSL https://agentlab.dev/install.sh | bash -s -- --version v0.1.0

# Or from source
go install github.com/agentlab/agentlab/cmd/agentlab@latest
go install github.com/agentlab/agentlab/cmd/agentlabd@latest
```

## Initialize

Set up the host where sandboxes will run:

```bash
# On the host machine
agentlab init --apply

# Or bootstrap a remote host over SSH
agentlab bootstrap --host myserver.example.com
```

## Create Your First Sandbox

```bash
# Quick create with defaults
agentlab new

# With a name and profile
agentlab new --name mybox --profile yolo-ephemeral

# List sandboxes
agentlab ls

# SSH into it
agentlab ssh <vmid>

# When done
agentlab rm <vmid>
```

## Set Your Preferences

```bash
# Set default profile so you don't have to specify it every time
agentlab defaults write default-profile yolo-ephemeral

# Always get JSON output
agentlab defaults write output-format json

# Check your settings
agentlab defaults list
```

## Shell Completion

```bash
# Bash
echo 'eval "$(agentlab completion bash)"' >> ~/.bashrc
source ~/.bashrc

# Zsh
echo 'eval "$(agentlab completion zsh)"' >> ~/.zshrc
source ~/.zshrc

# Fish
agentlab completion fish > ~/.config/fish/completions/agentlab.fish
```

## Running a Job

```bash
# Run a task from a git repo
agentlab job run \
  --repo https://github.com/user/repo \
  --task "run tests" \
  --profile yolo-ephemeral

# Check status
agentlab job show <job-id>

# Download artifacts
agentlab job artifacts download <job-id>
```

## Using Agent-Ready Images

```bash
# Create a sandbox with Claude Code pre-installed
agentlab new --image agent-claude --prompt "build a REST API"

# The agent starts automatically and works on your prompt
agentlab logs <vmid> --follow
```

## What's Next?

- [CLI Reference](cli.md) — Full command documentation
- [Architecture](architecture.md) — How AgentLab works
- [Configuration](configuration.md) — Daemon and client config
- [Security](security.md) — Authentication and authorization
- [Integrations](../llms.txt) — Machine-readable capability index
