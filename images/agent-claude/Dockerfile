# AgentLab Claude Code Agent Image
# Extends the base agent image with Claude Code pre-installed.
#
# Build:
#   docker build -t agentlab/agent-claude:latest .
#
# Use:
#   agentlab sandbox new --type=docker --image=agentlab/agent-claude:latest --prompt="build a web app"

FROM agentlab/agent-base:latest

# Install Claude Code CLI globally.
# Pin to 2.1.227 or later for ANTHROPIC_CUSTOM_HEADERS support (T37).
RUN npm install -g '@anthropic-ai/claude-code@>=2.1.227'

# Pre-configure Claude Code to use the metadata endpoint for LLM access.
# This routes LLM calls through the daemon's credential proxy at
# http://169.254.169.254/proxy/llm/v1/ so no API keys are needed inside the sandbox.
ENV CLAUDE_API_BASE_URL="http://169.254.169.254/proxy/llm/v1"
ENV ANTHROPIC_BASE_URL="http://169.254.169.254/proxy/llm"

# Copy CLAUDE.md template into place.
COPY --chown=root:root CLAUDE.md /etc/agentlab/CLAUDE.md

# Copy Claude Code entrypoint wrapper that injects the sandbox secret header.
COPY --chown=root:root claude-entrypoint.sh /usr/local/bin/claude-entrypoint.sh
RUN chmod +x /usr/local/bin/claude-entrypoint.sh

# The wrapper reads /run/agentlab/secrets/sandbox-secret (written by agent-runner)
# and injects it via ANTHROPIC_CUSTOM_HEADERS so Claude Code requests to the
# /proxy/llm endpoint authenticate with the per-sandbox secret (T37).
ENTRYPOINT ["/usr/local/bin/claude-entrypoint.sh"]

# Claude Code workspace.
WORKDIR /workspace
