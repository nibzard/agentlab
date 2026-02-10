#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
OUT="$ROOT/docs/cli.md"

if ! command -v go >/dev/null 2>&1; then
  echo "go is required to generate CLI docs" >&2
  exit 1
fi

cat <<'DOC' > "$OUT"
# AgentLab CLI Reference

This file is auto-generated from `agentlab --help`.
Do not edit by hand. Run `make docs-gen` to refresh.

## Usage

```text
DOC

(
  cd "$ROOT"
  go run -mod=readonly ./cmd/agentlab --help
) >> "$OUT"

echo '```' >> "$OUT"
