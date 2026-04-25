# CLAUDE.md

> AgentLab-specific instructions for Claude Code.

## Environment

You are running inside an **AgentLab sandbox** managed by `agentlabd`. The sandbox
provides a full Linux environment with development tools pre-installed.

## LLM Access

Your LLM calls are automatically routed through the AgentLab credential proxy.
No API keys are needed — the daemon at `169.254.169.254` handles authentication.
The following environment variables are pre-configured:

- `CLAUDE_API_BASE_URL` → `http://169.254.169.254/proxy/llm/v1`
- `ANTHROPIC_BASE_URL` → `http://169.254.169.254/proxy/llm`

## Git Access

Git operations against GitHub/GitLab are automatically proxied through the daemon.
Credentials are injected at the network layer — no tokens are stored on disk.

```bash
git clone https://github.com/user/repo.git  # credentials auto-injected
```

## Useful Commands

```bash
# Discover your sandbox identity
agentlab-guest identity

# Read sandbox metadata
agentlab-guest metadata

# Access a named secret
agentlab-guest secret <name>

# Check the initial prompt you were given
agentlab-guest prompt
```

## Project Conventions

- Work in `/workspace` — this is your project root
- Write tests for your code
- Use `git` for version control
- Keep changes focused and well-documented
- Run linting and tests before completing a task

## Networking

- Outbound internet access is available (may be NAT'd)
- The metadata endpoint is at `169.254.169.254`
- Your sandbox IP is in the `10.77.0.0/16` subnet
- The daemon/gateway is at `10.77.0.1`
