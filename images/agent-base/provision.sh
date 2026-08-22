#!/usr/bin/env bash
# AgentLab LXC Provisioning Script
# Run inside an LXC container to prepare it as an agent-ready sandbox.
#
# Usage:
#   ./provision.sh [--agent claude|codex|none]
#
set -euo pipefail

AGENT="${1:-none}"

export DEBIAN_FRONTEND=noninteractive

echo "=== AgentLab LXC Agent Provisioning ==="

# Update and install base packages.
apt-get update && apt-get install -y --no-install-recommends \
    build-essential \
    ca-certificates \
    curl \
    git \
    jq \
    less \
    locales \
    man-db \
    openssh-server \
    python3 \
    python3-pip \
    ripgrep \
    sudo \
    tmux \
    unzip \
    vim \
    wget \
    && rm -rf /var/lib/apt/lists/*

# Configure locale.
locale-gen en_US.UTF-8 2>/dev/null || true

# Install Node.js 20 LTS.
if ! command -v node &>/dev/null; then
    curl -fsSL https://deb.nodesource.com/setup_20.x | bash -
    apt-get install -y nodejs
    rm -rf /var/lib/apt/lists/*
fi

# Install Go.
if ! command -v go &>/dev/null; then
    GO_VERSION="1.22.5"
    curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz" | tar -C /usr/local -xzf -
    echo 'export PATH=$PATH:/usr/local/go/bin' > /etc/profile.d/go.sh
fi

# Configure SSH.
mkdir -p /run/sshd
sed -i 's/#PermitRootLogin prohibit-password/PermitRootLogin yes/' /etc/ssh/sshd_config
sed -i 's/#PasswordAuthentication yes/PasswordAuthentication no/' /etc/ssh/sshd_config

# Create workspace directory.
mkdir -p /workspace
chmod 777 /workspace

# Install agentlab-guest helper.
mkdir -p /usr/local/bin
cat > /usr/local/bin/agentlab-guest <<'GUEST_EOF'
#!/usr/bin/env bash
# AgentLab guest helper - fetches sandbox metadata and secrets.
set -euo pipefail
META="http://169.254.169.254"
SECRET_FILE="${AGENTLAB_SANDBOX_SECRET_FILE:-/run/agentlab/secrets/sandbox-secret}"
SECRET=""
if [[ -r "$SECRET_FILE" ]]; then
    SECRET="$(cat "$SECRET_FILE")"
fi
# Every metadata request must carry the per-sandbox secret. Requests
# without it are rejected.
meta_curl() {
    if [[ -n "$SECRET" ]]; then
        curl -sf -H "X-AgentLab-Sandbox-Secret: $SECRET" "$@"
    else
        echo "agentlab-guest: missing sandbox secret at $SECRET_FILE" >&2
        return 1
    fi
}
case "${1:-help}" in
    identity)  meta_curl "$META/identity" | jq . ;;
    metadata)  meta_curl "$META/metadata" | jq . ;;
    secret)    meta_curl "$META/secrets/${2:?secret name required}" | jq -r '.value' ;;
    prompt)    meta_curl "$META/metadata" | jq -r '.prompt // empty' ;;
    help|*)    echo "Usage: agentlab-guest {identity|metadata|secret <name>|prompt}" ;;
esac
GUEST_EOF
chmod +x /usr/local/bin/agentlab-guest

# Install agent entrypoint script.
cat > /usr/local/bin/agent-entrypoint.sh <<'ENTRYPOINT_EOF'
#!/usr/bin/env bash
set -euo pipefail
META="http://169.254.169.254"
SECRET_FILE="${AGENTLAB_SANDBOX_SECRET_FILE:-/run/agentlab/secrets/sandbox-secret}"
SECRET=""
if [[ -r "$SECRET_FILE" ]]; then
    SECRET="$(cat "$SECRET_FILE")"
fi
PROMPT=""
if [[ -n "$SECRET" ]]; then
    PROMPT=$(curl -sf --max-time 5 -H "X-AgentLab-Sandbox-Secret: $SECRET" "$META/metadata" 2>/dev/null | jq -r '.prompt // empty' || true)
fi
if [ -n "$PROMPT" ] && command -v claude &>/dev/null; then
    echo "Launching Claude Code with prompt: ${PROMPT:0:80}..."
    cd /workspace
    claude --print "$PROMPT" 2>&1 | tee /workspace/agent-output.md
elif [ -n "$PROMPT" ] && command -v codex &>/dev/null; then
    echo "Launching Codex CLI with prompt: ${PROMPT:0:80}..."
    cd /workspace
    codex "$PROMPT" 2>&1 | tee /workspace/agent-output.md
fi
ENTRYPOINT_EOF
chmod +x /usr/local/bin/agent-entrypoint.sh

# Install requested agent.
case "$AGENT" in
    --agent)
        shift
        case "${1:-}" in
            claude)
                echo "Installing Claude Code..."
                npm install -g @anthropic-ai/claude-code 2>/dev/null || echo "Warning: Claude Code install failed"
                ;;
            codex)
                echo "Installing OpenAI Codex CLI..."
                npm install -g @openai/codex 2>/dev/null || echo "Warning: Codex CLI install failed"
                ;;
            none)
                echo "No agent selected, skipping agent installation."
                ;;
            *)
                echo "Unknown agent: $1"
                echo "Usage: provision.sh [--agent claude|codex|none]"
                exit 1
                ;;
        esac
        ;;
esac

echo "=== AgentLab provisioning complete ==="
