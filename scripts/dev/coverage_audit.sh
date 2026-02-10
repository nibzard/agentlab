#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
GO_BIN="${GO:-go}"
export LC_ALL=C

COVERAGE_DIR="${COVERAGE_DIR:-$ROOT_DIR/dist/coverage}"
COVERAGE_OUT="${COVERAGE_OUT:-$COVERAGE_DIR/coverage.out}"
TOP_N="${TOP_N:-15}"

mkdir -p "$COVERAGE_DIR"

cd "$ROOT_DIR"

echo "==> Running unit tests with coverage"
"$GO_BIN" test -coverprofile="$COVERAGE_OUT" -covermode=atomic ./...

FUNC_REPORT="$COVERAGE_DIR/coverage.func.txt"
"$GO_BIN" tool cover -func="$COVERAGE_OUT" > "$FUNC_REPORT"

OVERALL_COVERAGE="$(awk '/^total:/{print $3}' "$FUNC_REPORT")"

echo
printf "Overall coverage: %s\n" "$OVERALL_COVERAGE"

echo
printf "Per-package coverage (ascending):\n"
awk '
/^mode:/ {next}
{
  split($1, parts, ":")
  file=parts[1]
  pkg=file
  sub(/\/[^\/]+$/, "", pkg)
  stmts=$2
  count=$3
  total[pkg]+=stmts
  if (count > 0) covered[pkg]+=stmts
}
END {
  for (p in total) {
    if (total[p] == 0) {
      continue
    }
    pct=(covered[p]/total[p])*100
    printf "%.2f\t%s\n", pct, p
  }
}
' "$COVERAGE_OUT" | sort -n | awk '{printf "%6.2f%%  %s\n", $1, $2}'

echo
printf "Lowest coverage files (top %s):\n" "$TOP_N"
awk '
/^mode:/ {next}
{
  split($1, parts, ":")
  file=parts[1]
  stmts=$2
  count=$3
  total[file]+=stmts
  if (count > 0) covered[file]+=stmts
}
END {
  for (f in total) {
    if (total[f] == 0) {
      continue
    }
    pct=(covered[f]/total[f])*100
    printf "%.2f\t%s\n", pct, f
  }
}
' "$COVERAGE_OUT" | sort -n | head -n "$TOP_N" | awk '{printf "%6.2f%%  %s\n", $1, $2}'

echo
printf "Lowest coverage functions (top %s):\n" "$TOP_N"
awk '
/^total:/ {next}
{
  pct=$NF
  gsub(/%/, "", pct)
  if (pct >= 100) {
    next
  }
  name=$1
  printf "%.2f\t%s\n", pct, name
}
' "$FUNC_REPORT" | sort -n | head -n "$TOP_N" | awk '{printf "%6.2f%%  %s\n", $1, $2}'

if [ -s "$COVERAGE_OUT" ]; then
  echo
  printf "Coverage profile: %s\n" "$COVERAGE_OUT"
  printf "Function report:  %s\n" "$FUNC_REPORT"
fi
