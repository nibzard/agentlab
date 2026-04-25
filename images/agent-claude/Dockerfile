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
RUN npm install -g @anthropic-ai/claude-code

# Pre-configure Claude Code to use the metadata endpoint for LLM access.
# This routes LLM calls through the daemon's credential proxy at
# http://169.254.169.254/proxy/llm/v1/ so no API keys are needed inside the sandbox.
ENV CLAUDE_API_BASE_URL="http://169.254.169.254/proxy/llm/v1"
ENV ANTHROPIC_BASE_URL="http://169.254.169.254/proxy/llm"

# Copy CLAUDE.md template into place.
COPY --chown=root:root CLAUDE.md /etc/agentlab/CLAUDE.md

# Claude Code workspace.
WORKDIR /workspace
