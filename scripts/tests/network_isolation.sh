#!/usr/bin/env bash
set -euo pipefail

log() {
  printf "[net-regression] %s\n" "$*"
}

die() {
  printf "[net-regression] ERROR: %s\n" "$*" >&2
  exit 1
}

usage() {
  cat <<'USAGE'
Usage: scripts/tests/network_isolation.sh [options]

Creates (or reuses) a sandbox, waits for SSH, and runs the network isolation
smoke test to verify Internet egress plus LAN/tailnet blocks.

Options:
  --config PATH        Config file path (default /etc/agentlab/config.yaml)
  --socket PATH        agentlabd socket path (default from config)
  --profile NAME       Profile name for new sandbox (default yolo-ephemeral)
  --ttl DURATION       TTL for new sandbox (e.g., 30m)
  --vmid VMID          Use an existing sandbox VMID (skip create)
  --ip IP              Sandbox IP (skip IP discovery)
  --keep-sandbox       Do not destroy sandbox created by this test
  --user USER          SSH username (default: agent)
  --ssh-key PATH       SSH private key to use
  --ssh-port PORT      SSH port (default: 22)
  --timeout SEC        Timeout seconds for each network probe (default: 5)
  --wait-timeout SEC   Timeout seconds to wait for sandbox readiness (default: 300)
  --lan-target HOST[:PORT]     LAN target to confirm blocks (default: vmbr0 IP)
  --tailnet-target HOST[:PORT] Tailnet target to confirm blocks (default: tailscale0 IP)
  --dns-name NAME      DNS name to resolve in sandbox (default: example.com)
  --egress-url URL     URL to fetch for Internet egress (default: https://example.com)

Environment overrides:
  AGENTLAB_CONFIG
  AGENTLAB_SOCKET
  AGENTLAB_PROFILE
  AGENTLAB_SANDBOX_VMID
  AGENTLAB_SANDBOX_IP
  AGENTLAB_SANDBOX_TTL
  AGENTLAB_KEEP_SANDBOX
  AGENTLAB_WAIT_TIMEOUT
  AGENTLAB_SANDBOX_USER
  AGENTLAB_SSH_KEY
  AGENTLAB_SSH_PORT
  AGENTLAB_SMOKE_TIMEOUT
  AGENTLAB_LAN_TARGET
  AGENTLAB_TAILNET_TARGET
  AGENTLAB_DNS_NAME
  AGENTLAB_EGRESS_URL
USAGE
}

CONFIG_PATH="${AGENTLAB_CONFIG:-/etc/agentlab/config.yaml}"
SOCKET_PATH="${AGENTLAB_SOCKET:-}"
PROFILE="${AGENTLAB_PROFILE:-yolo-ephemeral}"
SANDBOX_TTL="${AGENTLAB_SANDBOX_TTL:-}"
VMID="${AGENTLAB_SANDBOX_VMID:-}"
SANDBOX_IP="${AGENTLAB_SANDBOX_IP:-}"
KEEP_SANDBOX="${AGENTLAB_KEEP_SANDBOX:-0}"
WAIT_TIMEOUT="${AGENTLAB_WAIT_TIMEOUT:-300}"
SSH_USER="${AGENTLAB_SANDBOX_USER:-agent}"
SSH_KEY="${AGENTLAB_SSH_KEY:-}"
SSH_PORT="${AGENTLAB_SSH_PORT:-22}"
TIMEOUT_SEC="${AGENTLAB_SMOKE_TIMEOUT:-5}"
LAN_TARGET="${AGENTLAB_LAN_TARGET:-}"
TAILNET_TARGET="${AGENTLAB_TAILNET_TARGET:-}"
DNS_NAME="${AGENTLAB_DNS_NAME:-example.com}"
EGRESS_URL="${AGENTLAB_EGRESS_URL:-https://example.com}"

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
    --ttl)
      [[ $# -lt 2 ]] && die "--ttl requires a value"
      SANDBOX_TTL="$2"
      shift 2
      ;;
    --vmid)
      [[ $# -lt 2 ]] && die "--vmid requires a value"
      VMID="$2"
      shift 2
      ;;
    --ip)
      [[ $# -lt 2 ]] && die "--ip requires a value"
      SANDBOX_IP="$2"
      shift 2
      ;;
    --keep-sandbox)
      KEEP_SANDBOX=1
      shift
      ;;
    --user)
      [[ $# -lt 2 ]] && die "--user requires a value"
      SSH_USER="$2"
      shift 2
      ;;
    --ssh-key)
      [[ $# -lt 2 ]] && die "--ssh-key requires a value"
      SSH_KEY="$2"
      shift 2
      ;;
    --ssh-port)
      [[ $# -lt 2 ]] && die "--ssh-port requires a value"
      SSH_PORT="$2"
      shift 2
      ;;
    --timeout)
      [[ $# -lt 2 ]] && die "--timeout requires a value"
      TIMEOUT_SEC="$2"
      shift 2
      ;;
    --wait-timeout)
      [[ $# -lt 2 ]] && die "--wait-timeout requires a value"
      WAIT_TIMEOUT="$2"
      shift 2
      ;;
    --lan-target)
      [[ $# -lt 2 ]] && die "--lan-target requires a value"
      LAN_TARGET="$2"
      shift 2
      ;;
    --tailnet-target)
      [[ $# -lt 2 ]] && die "--tailnet-target requires a value"
      TAILNET_TARGET="$2"
      shift 2
      ;;
    --dns-name)
      [[ $# -lt 2 ]] && die "--dns-name requires a value"
      DNS_NAME="$2"
      shift 2
      ;;
    --egress-url)
      [[ $# -lt 2 ]] && die "--egress-url requires a value"
      EGRESS_URL="$2"
      shift 2
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

if [[ -n "$VMID" && ! "$VMID" =~ ^[0-9]+$ ]]; then
  die "--vmid must be a number"
fi
if [[ -n "$SSH_PORT" && ! "$SSH_PORT" =~ ^[0-9]+$ ]]; then
  die "--ssh-port must be a number"
fi
if [[ -n "$TIMEOUT_SEC" && ! "$TIMEOUT_SEC" =~ ^[0-9]+$ ]]; then
  die "--timeout must be a number"
fi
if [[ -n "$WAIT_TIMEOUT" && ! "$WAIT_TIMEOUT" =~ ^[0-9]+$ ]]; then
  die "--wait-timeout must be a number"
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

need_agentlab=0
if [[ -z "$SANDBOX_IP" ]]; then
  if [[ -n "$VMID" ]]; then
    need_agentlab=1
  else
    need_agentlab=1
  fi
fi

if [[ -n "$VMID" && -z "$SANDBOX_IP" ]]; then
  need_agentlab=1
fi

if [[ $need_agentlab -eq 1 ]]; then
  require_cmd agentlab
  require_cmd jq
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
  if [[ ! -S "$SOCKET_PATH" ]]; then
    die "agentlabd socket not found at $SOCKET_PATH"
  fi
fi

require_cmd bash
require_cmd ssh

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/../.." && pwd)"
SMOKE_TEST="${ROOT_DIR}/scripts/net/smoke_test.sh"
if [[ ! -f "$SMOKE_TEST" ]]; then
  die "smoke test not found at ${SMOKE_TEST}"
fi

CREATED_SANDBOX=0

cleanup() {
  if [[ "$CREATED_SANDBOX" -eq 1 && "$KEEP_SANDBOX" != "1" && -n "$VMID" ]]; then
    log "Destroying sandbox ${VMID}"
    agentlab --socket "$SOCKET_PATH" sandbox destroy "$VMID" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

if [[ -z "$VMID" && -z "$SANDBOX_IP" ]]; then
  log "Creating sandbox (profile=${PROFILE})"
  name="net-regression-$(date -u +%Y%m%d%H%M%S)"
  create_args=(--socket "$SOCKET_PATH" --json sandbox new --profile "$PROFILE" --name "$name")
  if [[ -n "$SANDBOX_TTL" ]]; then
    create_args+=(--ttl "$SANDBOX_TTL")
  fi
  payload="$(agentlab "${create_args[@]}")"
  VMID="$(printf "%s" "$payload" | jq -r '.vmid // empty')"
  if [[ -z "$VMID" || "$VMID" == "null" ]]; then
    die "failed to parse vmid from sandbox create response"
  fi
  CREATED_SANDBOX=1
  log "Sandbox created: ${VMID}"
fi

wait_for_ip() {
  local deadline
  deadline=$(( $(date +%s) + WAIT_TIMEOUT ))
  while true; do
    if (( $(date +%s) > deadline )); then
      die "timed out waiting for sandbox ${VMID} IP"
    fi
    payload="$(agentlab --socket "$SOCKET_PATH" --json sandbox show "$VMID")"
    state="$(printf "%s" "$payload" | jq -r '.state // empty')"
    ip="$(printf "%s" "$payload" | jq -r '.ip // empty')"
    if [[ "$state" == "DESTROYED" || "$state" == "FAILED" || "$state" == "TIMEOUT" ]]; then
      die "sandbox ${VMID} entered state ${state} before IP was ready"
    fi
    if [[ -n "$ip" && "$ip" != "null" ]]; then
      SANDBOX_IP="$ip"
      break
    fi
    sleep 5
  done
}

if [[ -z "$SANDBOX_IP" && -n "$VMID" ]]; then
  log "Waiting for sandbox ${VMID} IP"
  wait_for_ip
fi

if [[ -z "$SANDBOX_IP" ]]; then
  die "sandbox IP is required (use --ip or --vmid)"
fi

wait_for_ssh() {
  local deadline
  local ssh_args
  deadline=$(( $(date +%s) + WAIT_TIMEOUT ))
  ssh_args=(
    -o BatchMode=yes
    -o ConnectTimeout="${TIMEOUT_SEC}"
    -o StrictHostKeyChecking=no
    -o UserKnownHostsFile=/dev/null
    -p "$SSH_PORT"
  )
  if [[ -n "$SSH_KEY" ]]; then
    ssh_args+=( -i "$SSH_KEY" )
  fi
  while true; do
    if ssh "${ssh_args[@]}" "${SSH_USER}@${SANDBOX_IP}" true >/dev/null 2>&1; then
      return 0
    fi
    if (( $(date +%s) > deadline )); then
      return 1
    fi
    sleep 5
  done
}

log "Waiting for SSH on ${SSH_USER}@${SANDBOX_IP}:${SSH_PORT}"
if ! wait_for_ssh; then
  die "timed out waiting for SSH on ${SANDBOX_IP}"
fi

log "Running smoke test"
SMOKE_ARGS=(
  --ip "$SANDBOX_IP"
  --user "$SSH_USER"
  --ssh-port "$SSH_PORT"
  --timeout "$TIMEOUT_SEC"
  --dns-name "$DNS_NAME"
  --egress-url "$EGRESS_URL"
)
if [[ -n "$SSH_KEY" ]]; then
  SMOKE_ARGS+=(--ssh-key "$SSH_KEY")
fi
if [[ -n "$LAN_TARGET" ]]; then
  SMOKE_ARGS+=(--lan-target "$LAN_TARGET")
fi
if [[ -n "$TAILNET_TARGET" ]]; then
  SMOKE_ARGS+=(--tailnet-target "$TAILNET_TARGET")
fi

bash "$SMOKE_TEST" "${SMOKE_ARGS[@]}"

log "Network isolation regression test complete"
