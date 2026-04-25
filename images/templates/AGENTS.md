# AGENTS.md

> This file provides context for AI coding agents running inside an AgentLab sandbox.

## Sandbox Environment

You are running inside an **AgentLab sandbox** — an isolated development environment
with full system access. You have root privileges and can install packages, modify
system configuration, and run any commands needed.

## Available Services

The following services are available at `http://169.254.169.254` (the metadata endpoint):

| Endpoint | Description |
|---|---|
| `GET /identity` | Your sandbox identity (name, vmid, profile) |
| `GET /metadata` | Key-value metadata set at sandbox creation |
| `GET /secrets/{name}` | On-demand access to named secrets |
| `GET /proxy/llm/...` | LLM credential proxy (OpenAI-compatible API) |
| `GET /proxy/git/...` | Git credential proxy for clone/push operations |

### Quick Access with `agentlab-guest`

```bash
agentlab-guest identity     # Who am I?
agentlab-guest metadata     # What metadata was I given?
agentlab-guest secret db_pw # Get a secret
agentlab-guest prompt       # Get my initial prompt
agentlab-guest proxy llm/v1/chat/completions  # Use LLM proxy
```

## Workspace

- Your working directory is `/workspace`
- Write all project files here
- The workspace may persist across sandbox restarts (if attached to a workspace volume)

## Tools Available

- **Languages**: Python 3, Node.js 20, Go
- **Build tools**: build-essential, make, cmake
- **Search**: ripgrep (`rg`), fd, grep
- **Version control**: git (credential proxy available)
- **Editors**: vim, nano
- **Networking**: curl, wget, jq

## Guidelines

1. Write clean, well-tested code
2. Use git for version control when appropriate
3. Install additional dependencies as needed
4. Document your changes clearly
5. Run tests before declaring completion
