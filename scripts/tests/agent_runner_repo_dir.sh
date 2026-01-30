#!/usr/bin/env bash
set -euo pipefail

log() {
  printf "[agent-runner-repo-dir] %s\n" "$*"
}

die() {
  printf "[agent-runner-repo-dir] ERROR: %s\n" "$*" >&2
  exit 1
}

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
TARGET="$ROOT_DIR/scripts/guest/agent-runner"

[[ -f "$TARGET" ]] || die "agent-runner not found at $TARGET"

inside=0
depth=0
cd_count=0
while IFS= read -r line; do
  if (( inside == 0 )); then
    if [[ "$line" == *'if [[ "$AGENT_COMMAND" == "agentlab-agent" ]]'* ]]; then
      inside=1
      depth=1
      continue
    fi
  else
    if [[ "$line" == *'if [['* ]]; then
      depth=$((depth + 1))
    fi
    if (( depth == 1 )) && [[ "$line" =~ ^[[:space:]]*else[[:space:]]*$ ]]; then
      break
    fi
    if [[ "$line" =~ ^[[:space:]]*fi[[:space:]]*$ ]]; then
      depth=$((depth - 1))
      if (( depth == 0 )); then
        break
      fi
    fi
    if [[ "$line" == *'cd "$REPO_DIR"'* ]]; then
      cd_count=$((cd_count + 1))
    fi
  fi
done <"$TARGET"

if (( cd_count < 2 )); then
  die "agentlab-agent branch does not cd into repo for all paths"
fi

log "ok"
