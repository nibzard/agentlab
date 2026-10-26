#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
OUT="$ROOT/docs/reference/cli.md"
LLMS_OUT="$ROOT/llms.txt"
if [[ "${1:-}" == "--check" ]]; then
  preview_dir="$(mktemp -d)"
  trap 'rm -rf "$preview_dir"' EXIT
  OUT="$preview_dir/cli.md"
  LLMS_OUT="$preview_dir/llms.txt"
fi
mkdir -p "$(dirname "$OUT")"

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
python3 "$ROOT/scripts/dev/generate_cli_inventory.py" --reference "$OUT" --llms-output "$LLMS_OUT"
if [[ "${1:-}" == "--check" ]]; then
  cmp -s "$OUT" "$ROOT/docs/reference/cli.md" || { echo "CLI reference is out of date; run make docs-gen" >&2; exit 1; }
  cmp -s "$LLMS_OUT" "$ROOT/llms.txt" || { echo "llms inventory is out of date; run make docs-gen" >&2; exit 1; }
fi
