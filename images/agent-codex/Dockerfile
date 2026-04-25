# AgentLab Codex CLI Agent Image
# Extends the base agent image with OpenAI Codex CLI pre-installed.
#
# Build:
#   docker build -t agentlab/agent-codex:latest .
#
# Use:
#   agentlab sandbox new --type=docker --image=agentlab/agent-codex:latest --prompt="build a web app"

FROM agentlab/agent-base:latest

# Install OpenAI Codex CLI globally.
RUN npm install -g @openai/codex

# Pre-configure Codex CLI to use the metadata endpoint for LLM access.
# This routes LLM calls through the daemon's credential proxy at
# http://169.254.169.254/proxy/llm/v1/ so no API keys are needed inside the sandbox.
ENV OPENAI_API_BASE="http://169.254.169.254/proxy/llm/v1"
ENV OPENAI_BASE_URL="http://169.254.169.254/proxy/llm/v1"

# Copy AGENTS.md template into place.
COPY --chown=root:root AGENTS.md /etc/agentlab/AGENTS.md

# Codex workspace.
WORKDIR /workspace
