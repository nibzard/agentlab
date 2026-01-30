#!/usr/bin/env bash
set -euo pipefail

log() {
  printf "[agent-runner-no-job] %s\n" "$*"
}

die() {
  printf "[agent-runner-no-job] ERROR: %s\n" "$*" >&2
  exit 1
}

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
TARGET="$ROOT_DIR/scripts/guest/agent-runner"

[[ -f "$TARGET" ]] || die "agent-runner not found at $TARGET"

rg -n "job not found" "$TARGET" >/dev/null || die "missing job-not-found handling"
rg -n "no job assigned" "$TARGET" >/dev/null || die "missing no-job exit log"
rg -n "return 2" "$TARGET" >/dev/null || die "missing no-job return code"
rg -n "404" "$TARGET" >/dev/null || die "missing 404 handling"

log "ok"
