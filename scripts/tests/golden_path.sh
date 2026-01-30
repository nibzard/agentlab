#!/usr/bin/env bash
set -euo pipefail

log() {
  printf "[golden-path] %s\n" "$*"
}

die() {
  printf "[golden-path] ERROR: %s\n" "$*" >&2
  exit 1
}

usage() {
  cat <<'USAGE'
Usage: scripts/tests/golden_path.sh [options]

Runs an end-to-end job that clones a tiny repo, writes a golden artifact,
uploads it, and validates sandbox teardown.

Options:
  --config PATH      Config file path (default /etc/agentlab/config.yaml)
  --socket PATH      agentlabd socket path (default from config)
  --profile NAME     Profile name (default yolo-ephemeral)
  --host-ip IP       Host IP on agent subnet (default from config or vmbr1)
  --port PORT        Repo server port (default 18080)
  --timeout SEC      Job completion timeout in seconds (default 900)
  --keep-temp        Keep temp repo + server logs

Environment overrides:
  AGENTLAB_CONFIG
  AGENTLAB_SOCKET
  AGENTLAB_PROFILE
  AGENTLAB_HOST_IP
  AGENTLAB_REPO_PORT
  AGENTLAB_TIMEOUT_SECONDS
  AGENTLAB_KEEP_TEMP
  AGENTLAB_TASK
USAGE
}

CONFIG_PATH="${AGENTLAB_CONFIG:-/etc/agentlab/config.yaml}"
SOCKET_PATH="${AGENTLAB_SOCKET:-}"
PROFILE="${AGENTLAB_PROFILE:-yolo-ephemeral}"
HOST_IP="${AGENTLAB_HOST_IP:-}"
REPO_PORT="${AGENTLAB_REPO_PORT:-18080}"
TIMEOUT_SECONDS="${AGENTLAB_TIMEOUT_SECONDS:-900}"
KEEP_TEMP="${AGENTLAB_KEEP_TEMP:-0}"
TASK_TEXT="${AGENTLAB_TASK:-golden-path: write artifact}"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --config)
      [[ $# -lt 2 ]] && die "--config requires a value"
      CONFIG_PATH="$2"
      shift 2
      ;;
    --socket)
      [[ $# -lt 2 ]] && die "--socket requires a value"
      SOCKET_PATH="$2"
      shift 2
      ;;
    --profile)
      [[ $# -lt 2 ]] && die "--profile requires a value"
      PROFILE="$2"
      shift 2
      ;;
    --host-ip)
      [[ $# -lt 2 ]] && die "--host-ip requires a value"
      HOST_IP="$2"
      shift 2
      ;;
    --port)
      [[ $# -lt 2 ]] && die "--port requires a value"
      REPO_PORT="$2"
      shift 2
      ;;
    --timeout)
      [[ $# -lt 2 ]] && die "--timeout requires a value"
      TIMEOUT_SECONDS="$2"
      shift 2
      ;;
    --keep-temp)
      KEEP_TEMP=1
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      die "Unknown argument: $1"
      ;;
  esac
done

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || die "missing dependency: $1"
}

require_cmd agentlab
require_cmd git
require_cmd jq
require_cmd curl

if [[ ! "$REPO_PORT" =~ ^[0-9]+$ ]]; then
  die "port must be a number"
fi
if [[ ! "$TIMEOUT_SECONDS" =~ ^[0-9]+$ ]]; then
  die "timeout must be a number"
fi
if [[ "$TIMEOUT_SECONDS" -le 0 ]]; then
  die "timeout must be positive"
fi

trim() {
  local value="$1"
  value="${value#"${value%%[![:space:]]*}"}"
  value="${value%"${value##*[![:space:]]}"}"
  printf "%s" "$value"
}

strip_quotes() {
  local value
  value="$(trim "$1")"
  value="${value%\"}"
  value="${value#\"}"
  value="${value%\'}"
  value="${value#\'}"
  printf "%s" "$value"
}

config_value() {
  local key="$1"
  [[ -f "$CONFIG_PATH" ]] || return 0
  local line
  line="$(awk -v k="$key" -F ':' '
    /^[[:space:]]*#/ { next }
    $1 ~ "^[[:space:]]*"k"[[:space:]]*$" {
      sub(/^[^:]*:[[:space:]]*/, "", $0)
      sub(/[[:space:]]+#.*/, "", $0)
      print $0
      exit
    }' "$CONFIG_PATH")"
  if [[ -n "$line" ]]; then
    strip_quotes "$line"
  fi
}

if [[ -z "$SOCKET_PATH" ]]; then
  SOCKET_PATH="$(config_value socket_path)"
  if [[ -z "$SOCKET_PATH" ]]; then
    run_dir="$(config_value run_dir)"
    if [[ -n "$run_dir" ]]; then
      SOCKET_PATH="${run_dir}/agentlabd.sock"
    fi
  fi
  if [[ -z "$SOCKET_PATH" ]]; then
    SOCKET_PATH="/run/agentlab/agentlabd.sock"
  fi
fi

ARTIFACT_DIR="$(config_value artifact_dir)"
if [[ -z "$ARTIFACT_DIR" ]]; then
  data_dir="$(config_value data_dir)"
  if [[ -n "$data_dir" ]]; then
    ARTIFACT_DIR="${data_dir}/artifacts"
  fi
fi
if [[ -z "$ARTIFACT_DIR" ]]; then
  ARTIFACT_DIR="/var/lib/agentlab/artifacts"
fi

if [[ -z "$HOST_IP" ]]; then
  bootstrap_listen="$(config_value bootstrap_listen)"
  if [[ -n "$bootstrap_listen" ]]; then
    host_part="${bootstrap_listen%:*}"
    if [[ "$host_part" != "0.0.0.0" && "$host_part" != "::" ]]; then
      HOST_IP="$host_part"
    fi
  fi
fi
if [[ -z "$HOST_IP" ]]; then
  if command -v ip >/dev/null 2>&1; then
    HOST_IP="$(ip -4 -o addr show vmbr1 2>/dev/null | awk '{print $4}' | cut -d/ -f1 | head -n1)"
  fi
fi
if [[ -z "$HOST_IP" ]]; then
  HOST_IP="10.77.0.1"
fi

if [[ ! -S "$SOCKET_PATH" ]]; then
  die "agentlabd socket not found at $SOCKET_PATH"
fi
if ! curl --unix-socket "$SOCKET_PATH" -fsS http://unix/healthz >/dev/null; then
  die "agentlabd health check failed via $SOCKET_PATH"
fi

TMP_DIR=""
SERVER_PID=""
SERVER_LOG=""
REPO_URL=""

cleanup() {
  if [[ -n "$SERVER_PID" ]]; then
    kill "$SERVER_PID" >/dev/null 2>&1 || true
    wait "$SERVER_PID" >/dev/null 2>&1 || true
  fi
  if [[ -n "$TMP_DIR" && "$KEEP_TEMP" != "1" ]]; then
    rm -rf "$TMP_DIR"
  fi
}
trap cleanup EXIT

TMP_DIR="$(mktemp -d /tmp/agentlab-golden.XXXXXX)"
SERVER_LOG="${TMP_DIR}/repo-server.log"

work_dir="${TMP_DIR}/work"
repo_dir="${TMP_DIR}/repo.git"
mkdir -p "$work_dir"

git -C "$work_dir" init >/dev/null
git -C "$work_dir" config user.email "agentlab@example.invalid"
git -C "$work_dir" config user.name "AgentLab Golden Path"

mkdir -p "$work_dir/.agentlab"
cat >"$work_dir/.agentlab/run.sh" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

ART_DIR="/run/agentlab/artifacts"
mkdir -p "$ART_DIR"

{
  echo "golden-path ok"
  echo "job_id=${AGENTLAB_JOB_ID:-}"
  echo "task=${AGENTLAB_TASK:-}"
  echo "timestamp=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
} >"${ART_DIR}/golden.txt"
EOF
chmod 755 "$work_dir/.agentlab/run.sh"

cat >"$work_dir/README.md" <<'EOF'
# AgentLab golden path repo
EOF

git -C "$work_dir" add . >/dev/null
git -C "$work_dir" commit -m "golden path" >/dev/null
git -C "$work_dir" branch -M main >/dev/null
git clone --bare "$work_dir" "$repo_dir" >/dev/null
git -C "$repo_dir" update-server-info >/dev/null

start_http_server() {
  local bind_ip="$1"
  local port="$2"
  if command -v python3 >/dev/null 2>&1; then
    (cd "$TMP_DIR" && python3 -m http.server --bind "$bind_ip" "$port" >"$SERVER_LOG" 2>&1) &
    SERVER_PID=$!
    return 0
  fi
  if command -v python >/dev/null 2>&1; then
    (cd "$TMP_DIR" && python -m http.server --bind "$bind_ip" "$port" >"$SERVER_LOG" 2>&1) &
    SERVER_PID=$!
    return 0
  fi
  return 1
}

start_git_daemon() {
  local bind_ip="$1"
  local port="$2"
  if ! git daemon --version >/dev/null 2>&1; then
    return 1
  fi
  git daemon --reuseaddr --export-all --base-path="$TMP_DIR" --listen="$bind_ip" --port="$port" "$TMP_DIR" >"$SERVER_LOG" 2>&1 &
  SERVER_PID=$!
  return 0
}

SERVER_KIND="http"
if start_http_server "$HOST_IP" "$REPO_PORT"; then
  REPO_URL="http://${HOST_IP}:${REPO_PORT}/repo.git"
else
  SERVER_KIND="git"
  if start_git_daemon "$HOST_IP" "$REPO_PORT"; then
    REPO_URL="git://${HOST_IP}:${REPO_PORT}/repo.git"
  else
    die "python http.server or git daemon required for repo hosting"
  fi
fi

sleep 1
if [[ -z "$SERVER_PID" ]] || ! kill -0 "$SERVER_PID" >/dev/null 2>&1; then
  tail -n 50 "$SERVER_LOG" >&2 || true
  die "repo server failed to start on ${HOST_IP}:${REPO_PORT}"
fi

log "Repo server running (${SERVER_KIND}) at ${REPO_URL}"

if ! git ls-remote "$REPO_URL" >/dev/null 2>&1; then
  tail -n 50 "$SERVER_LOG" >&2 || true
  die "failed to reach repo at ${REPO_URL}"
fi

job_json="$(agentlab --socket "$SOCKET_PATH" --json job run --repo "$REPO_URL" --task "$TASK_TEXT" --profile "$PROFILE")"
job_id="$(printf "%s" "$job_json" | jq -r '.id // empty')"
if [[ -z "$job_id" || "$job_id" == "null" ]]; then
  die "failed to parse job id from response"
fi

log "Job started: $job_id (profile=$PROFILE)"

deadline=$(( $(date +%s) + TIMEOUT_SECONDS ))
job_status=""
sandbox_vmid=""
while true; do
  now=$(date +%s)
  if (( now > deadline )); then
    die "timed out waiting for job completion"
  fi
  job_json="$(curl --unix-socket "$SOCKET_PATH" -fsS "http://unix/v1/jobs/${job_id}")"
  job_status="$(printf "%s" "$job_json" | jq -r '.status // empty')"
  sandbox_vmid="$(printf "%s" "$job_json" | jq -r '.sandbox_vmid // empty')"
  case "$job_status" in
    COMPLETED|FAILED|TIMEOUT)
      break
      ;;
    *)
      ;;
  esac
  sleep 5
done

log "Job status: ${job_status}"

if [[ "$job_status" != "COMPLETED" ]]; then
  if [[ -n "$sandbox_vmid" && "$sandbox_vmid" != "null" ]]; then
    log "Last events for sandbox ${sandbox_vmid}:"
    agentlab --socket "$SOCKET_PATH" logs "$sandbox_vmid" --tail 50 || true
  fi
  die "job did not complete successfully (status=$job_status)"
fi

artifact_bundle="${ARTIFACT_DIR}/${job_id}/agentlab-artifacts.tar.gz"
if [[ ! -f "$artifact_bundle" ]]; then
  die "artifact bundle not found at ${artifact_bundle}"
fi

if ! tar -tzf "$artifact_bundle" | grep -q "golden.txt"; then
  die "golden artifact missing from ${artifact_bundle}"
fi

log "Artifact bundle verified: ${artifact_bundle}"

if [[ -n "$sandbox_vmid" && "$sandbox_vmid" != "null" ]]; then
  destroy_deadline=$(( $(date +%s) + 180 ))
  while true; do
    now=$(date +%s)
    if (( now > destroy_deadline )); then
      die "sandbox ${sandbox_vmid} did not reach DESTROYED state"
    fi
    sandbox_json="$(curl --unix-socket "$SOCKET_PATH" -fsS "http://unix/v1/sandboxes/${sandbox_vmid}")"
    sandbox_state="$(printf "%s" "$sandbox_json" | jq -r '.state // empty')"
    if [[ "$sandbox_state" == "DESTROYED" ]]; then
      break
    fi
    sleep 5
  done
  log "Sandbox ${sandbox_vmid} destroyed"
else
  log "Sandbox vmid not reported; skipping teardown check"
fi

if [[ "$KEEP_TEMP" == "1" ]]; then
  log "Keeping temp directory: ${TMP_DIR}"
fi

log "Golden path integration test complete"
