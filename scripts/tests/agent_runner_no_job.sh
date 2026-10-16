#!/usr/bin/env bash
set -euo pipefail
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
fixture_dir="$(mktemp -d)"
runner_pid=""
trap 'if [[ -n "$runner_pid" ]]; then kill "$runner_pid" 2>/dev/null || true; fi; rm -rf "$fixture_dir"' EXIT
mkdir "$fixture_dir/bin"
export FIXTURE_ROOT="$fixture_dir"
export REAL_SLEEP
REAL_SLEEP="$(command -v sleep)"
cat > "$fixture_dir/bin/curl" <<'CURL'
#!/usr/bin/env bash
set -eu
out=""; payload=""
while [[ $# -gt 0 ]]; do
 case "$1" in -o) out="$2";shift 2;; -d) payload="$2";shift 2;; *)shift;; esac
done
if [[ -n "$out" ]]; then cat "$FIXTURE_ROOT/payload.json" > "$out"; printf 200
else printf '%s\n' "$payload" >> "$FIXTURE_ROOT/reports.ndjson"; fi
CURL
cat > "$fixture_dir/bin/tailscale" <<'TS'
#!/usr/bin/env bash
if [[ "$1" == up ]]; then
 if [[ "$FIXTURE_MODE" == failed ]]; then echo "invalid key tskey-auth-secret-fixture"; exit 1; fi
 exit 0
fi
if [[ "$FIXTURE_MODE" == connected ]]; then printf '{"BackendState":"Running","Self":{"Online":true}}'
else printf '{"BackendState":"NeedsLogin","Self":{"Online":false}}'; fi
TS
cat > "$fixture_dir/bin/sudo" <<'SUDO'
#!/usr/bin/env bash
exec "$@"
SUDO
cat > "$fixture_dir/bin/systemctl" <<'SYSTEMCTL'
#!/usr/bin/env bash
exit 0
SYSTEMCTL
cat > "$fixture_dir/bin/git" <<'GIT'
#!/usr/bin/env bash
echo unexpected-job-execution >> "$FIXTURE_ROOT/git-called"
exit 1
GIT
cat > "$fixture_dir/bin/sleep" <<'SLEEP'
#!/usr/bin/env bash
exec "$REAL_SLEEP" 0.1
SLEEP
chmod +x "$fixture_dir/bin/"*
export PATH="$fixture_dir/bin:$PATH"
export AGENTLAB_BOOTSTRAP="$fixture_dir/bootstrap.json" AGENTLAB_RUNNER_ENV="$fixture_dir/no-env"
export AGENTLAB_SECRETS_DIR="$fixture_dir/secrets" AGENTLAB_RUN_DIR="$fixture_dir/run" AGENTLAB_WORK_DIR_BASE="$fixture_dir/work"
printf '{"token":"bootstrap-fixture","controller":"http://fixture","vmid":1234}' > "$AGENTLAB_BOOTSTRAP"
for mode in connected failed unconfigured; do
 export FIXTURE_MODE="$mode"
 rm -rf "$AGENTLAB_SECRETS_DIR" "$fixture_dir/reports.ndjson"
 if [[ "$mode" == unconfigured ]]; then
  printf '{"sandbox":{"vmid":1234},"sandbox_secret":"endpoint-secret-fixture"}' > "$fixture_dir/payload.json"
  expected=UNCONFIGURED
 else
  printf '{"sandbox":{"vmid":1234},"sandbox_secret":"endpoint-secret-fixture","tailscale":{"authkey":"tskey-auth-secret-fixture","hostname":"sandbox-1234"}}' > "$fixture_dir/payload.json"
  expected="${mode^^}"
 fi
 bash "$ROOT_DIR/scripts/guest/agent-runner" > "$fixture_dir/output" 2>&1 & runner_pid=$!
 for ((i=0;i<100;i++)); do
  if [[ -s "$fixture_dir/reports.ndjson" ]] && jq -e -s --arg expected "$expected" 'any(.[]; .tailscale_state==$expected)' "$fixture_dir/reports.ndjson" >/dev/null; then break; fi
  "$REAL_SLEEP" 0.05
 done
 [[ -s "$fixture_dir/reports.ndjson" ]] || { cat "$fixture_dir/output"; echo 'standalone bootstrap did not report' >&2;exit 1; }
 jq -e -s --arg expected "$expected" 'last | .vmid==1234 and .runner_ready and .tailscale_state==$expected' "$fixture_dir/reports.ndjson" >/dev/null
 kill -0 "$runner_pid" # A standalone runner stays alive to retain its tmpfs endpoint secret.
 kill -TERM "$runner_pid";wait "$runner_pid" || true;runner_pid=""
 [[ ! -e "$fixture_dir/git-called" ]]
 if rg -q 'tskey-auth-secret-fixture' "$fixture_dir/output"; then echo 'enrollment key leaked into runner log' >&2;exit 1;fi
 jq -e '.tailscale.authkey == null' "$AGENTLAB_SECRETS_DIR/bootstrap.json" >/dev/null
 printf '[agent-runner-no-job] %s: ok\n' "$mode"
done
