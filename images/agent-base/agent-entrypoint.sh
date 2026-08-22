#!/usr/bin/env bash
# AgentLab Agent Entrypoint
# Starts SSH and optionally launches a coding agent with the initial prompt.
set -euo pipefail

# Start SSH daemon
/usr/sbin/sshd -e

# Fetch sandbox metadata from the metadata endpoint.
# The metadata endpoint is available at 169.254.169.254 inside sandboxes.
METADATA_BASE="http://169.254.169.254"
SANDBOX_SECRET_FILE="${AGENTLAB_SANDBOX_SECRET_FILE:-/run/agentlab/secrets/sandbox-secret}"

# The endpoint requires the per-sandbox secret stored by the agent runner.
# Requests without it are rejected.
SANDBOX_SECRET=""
if [[ -r "$SANDBOX_SECRET_FILE" ]]; then
    SANDBOX_SECRET="$(cat "$SANDBOX_SECRET_FILE")"
fi

fetch_metadata() {
    local path="$1"
    if [[ -n "$SANDBOX_SECRET" ]]; then
        curl -sf --max-time 5 -H "X-AgentLab-Sandbox-Secret: $SANDBOX_SECRET" "${METADATA_BASE}${path}" 2>/dev/null || echo "{}"
    else
        echo "{}"
    fi
}

# Get sandbox identity.
IDENTITY=$(fetch_metadata "/identity")
SANDBOX_NAME=$(echo "$IDENTITY" | jq -r '.name // "unknown"')

# Check for an initial prompt in sandbox metadata.
PROMPT=$(fetch_metadata "/metadata" | jq -r '.prompt // empty')

# Copy AGENTS.md template if present and no existing AGENTS.md.
if [ -f /etc/agentlab/AGENTS.md ] && [ ! -f /workspace/AGENTS.md ]; then
    cp /etc/agentlab/AGENTS.md /workspace/AGENTS.md
fi

# Copy CLAUDE.md template if present and no existing CLAUDE.md.
if [ -f /etc/agentlab/CLAUDE.md ] && [ ! -f /workspace/CLAUDE.md ]; then
    cp /etc/agentlab/CLAUDE.md /workspace/CLAUDE.md
fi

# If a prompt was provided, check for available agents.
if [ -n "$PROMPT" ]; then
    echo "Agent prompt received: ${PROMPT:0:80}..."

    # Prefer Claude Code if installed.
    if command -v claude &>/dev/null; then
        echo "Launching Claude Code with prompt..."
        cd /workspace
        claude --print "$PROMPT" 2>&1 | tee /workspace/agent-output.md
    # Fallback to OpenAI Codex CLI.
    elif command -v codex &>/dev/null; then
        echo "Launching Codex CLI with prompt..."
        cd /workspace
        codex "$PROMPT" 2>&1 | tee /workspace/agent-output.md
    else
        echo "No coding agent found. Prompt saved to /workspace/prompt.txt"
        echo "$PROMPT" > /workspace/prompt.txt
    fi
fi

# Keep container running (SSH daemon is in background).
exec "$@"
