#!/usr/bin/env bash
# Claude Code entrypoint wrapper for AgentLab.
# Reads the sandbox secret and injects it into Claude Code requests via ANTHROPIC_CUSTOM_HEADERS.

set -euo pipefail

log() {
    printf "[claude-entrypoint] %s\n" "$*" >&2
}

die() {
    log "ERROR: $*"
    exit 1
}

# The sandbox secret is written by agent-runner at bootstrap.
SANDBOX_SECRET_FILE="${AGENTLAB_SANDBOX_SECRET_FILE:-/run/agentlab/secrets/sandbox-secret}"

# Fail closed: if the secret file is missing or empty, Claude Code cannot communicate
# through the credential proxy (which now requires the secret per review F4/T12).
if [[ ! -r "$SANDBOX_SECRET_FILE" ]]; then
    die "sandbox secret file not found at $SANDBOX_SECRET_FILE"
fi

SANDBOX_SECRET="$(cat "$SANDBOX_SECRET_FILE")"
if [[ -z "$SANDBOX_SECRET" ]]; then
    die "sandbox secret is empty"
fi

# Claude Code 2.1.227+ supports ANTHROPIC_CUSTOM_HEADERS for injecting arbitrary headers
# into every request. We use this to attach the per-sandbox secret that the daemon's
# /proxy/llm endpoint requires.
export ANTHROPIC_CUSTOM_HEADERS="X-AgentLab-Sandbox-Secret: ${SANDBOX_SECRET}"

# Execute Claude Code with all passed-through arguments.
exec claude "$@"
