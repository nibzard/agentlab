#!/usr/bin/env bash
set -euo pipefail

log() {
  printf "[smoke-test] %s\n" "$*"
}

die() {
  printf "[smoke-test] ERROR: %s\n" "$*" >&2
  exit 1
}

usage() {
  cat <<'USAGE'
Usage: scripts/net/smoke_test.sh --ip SANDBOX_IP [options]

Validates sandbox networking by running checks inside the sandbox via SSH.

Required:
  --ip IP              Sandbox IP address (e.g., 10.77.0.23)

Options:
  --user USER          SSH username (default: agent)
  --ssh-key PATH       SSH private key to use
  --ssh-port PORT      SSH port (default: 22)
  --timeout SEC        Timeout seconds for each network probe (default: 5)
  --lan-target HOST[:PORT]     LAN target to confirm blocks (default: vmbr0 IP or fail)
  --tailnet-target HOST[:PORT] Tailnet target to confirm blocks (default: tailscale0 IP or fail)
  --dns-name NAME      DNS name to resolve in sandbox (default: example.com)
  --egress-url URL     URL to fetch for Internet egress (default: https://example.com)

Environment overrides:
  AGENTLAB_SANDBOX_IP
  AGENTLAB_SANDBOX_USER
  AGENTLAB_SSH_KEY
  AGENTLAB_SSH_PORT
  AGENTLAB_SMOKE_TIMEOUT
  AGENTLAB_LAN_TARGET
  AGENTLAB_TAILNET_TARGET
  AGENTLAB_DNS_NAME
  AGENTLAB_EGRESS_URL

Notes:
  - Run this from a tailnet device to validate tailnet inbound access.
  - If auto-detection fails, pass --lan-target and --tailnet-target explicitly.
USAGE
}

SANDBOX_IP="${AGENTLAB_SANDBOX_IP:-}"
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
    --ip)
      [[ $# -lt 2 ]] && die "--ip requires a value"
      SANDBOX_IP="$2"
      shift 2
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

require_cmd ssh

if [[ -z "$SANDBOX_IP" ]]; then
  usage
  die "--ip is required"
fi

if [[ ! "$SSH_PORT" =~ ^[0-9]+$ ]]; then
  die "--ssh-port must be a number"
fi
if [[ ! "$TIMEOUT_SEC" =~ ^[0-9]+$ ]]; then
  die "--timeout must be a number"
fi

if [[ -z "$LAN_TARGET" ]]; then
  if command -v ip >/dev/null 2>&1; then
    LAN_TARGET="$(ip -4 -o addr show vmbr0 2>/dev/null | awk '{print $4}' | cut -d/ -f1 | head -n1)"
  fi
fi
if [[ -z "$LAN_TARGET" ]]; then
  die "LAN target not set; use --lan-target HOST[:PORT]"
fi

if [[ -z "$TAILNET_TARGET" ]]; then
  if command -v tailscale >/dev/null 2>&1; then
    TAILNET_TARGET="$(tailscale ip -4 2>/dev/null | head -n1)"
  fi
fi
if [[ -z "$TAILNET_TARGET" ]]; then
  if command -v ip >/dev/null 2>&1; then
    TAILNET_TARGET="$(ip -4 -o addr show tailscale0 2>/dev/null | awk '{print $4}' | cut -d/ -f1 | head -n1)"
  fi
fi
if [[ -z "$TAILNET_TARGET" ]]; then
  die "Tailnet target not set; use --tailnet-target HOST[:PORT]"
fi

parse_target() {
  local name="$1"
  local target="$2"
  local default_port="$3"
  local host=""
  local port=""

  if [[ "$target" == *":"* ]]; then
    host="${target%:*}"
    port="${target##*:}"
  else
    host="$target"
    port="$default_port"
  fi

  if [[ -z "$host" ]]; then
    die "$name target host missing"
  fi
  if [[ ! "$port" =~ ^[0-9]+$ ]]; then
    die "$name target port must be a number"
  fi

  printf "%s %s" "$host" "$port"
}

read -r LAN_HOST LAN_PORT <<<"$(parse_target LAN "$LAN_TARGET" 22)"
read -r TAILNET_HOST TAILNET_PORT <<<"$(parse_target tailnet "$TAILNET_TARGET" 22)"

SSH_ARGS=(
  -o BatchMode=yes
  -o ConnectTimeout="${TIMEOUT_SEC}"
  -o StrictHostKeyChecking=no
  -o UserKnownHostsFile=/dev/null
  -p "$SSH_PORT"
)

if [[ -n "$SSH_KEY" ]]; then
  SSH_ARGS+=( -i "$SSH_KEY" )
fi

SSH_DEST="${SSH_USER}@${SANDBOX_IP}"

set +e
REMOTE_OUTPUT=$(
  ssh "${SSH_ARGS[@]}" "$SSH_DEST" bash -s -- \
    "$DNS_NAME" \
    "$EGRESS_URL" \
    "$LAN_HOST" \
    "$LAN_PORT" \
    "$TAILNET_HOST" \
    "$TAILNET_PORT" \
    "$TIMEOUT_SEC" \
    "$TIMEOUT_SEC" <<'REMOTE'
set -euo pipefail

dns_name="$1"
egress_url="$2"
lan_host="$3"
lan_port="$4"
tail_host="$5"
tail_port="$6"
connect_timeout="$7"
max_time="$8"

emit() {
  printf "%s|%s|%s\n" "$1" "$2" "$3"
}

missing=()
for cmd in curl getent; do
  if ! command -v "$cmd" >/dev/null 2>&1; then
    missing+=("$cmd")
  fi
done

if (( ${#missing[@]} > 0 )); then
  emit "FAIL" "deps" "missing: ${missing[*]}"
  exit 2
fi

set +e
resolved_ip="$(getent ahosts "$dns_name" 2>/dev/null | awk 'NR==1{print $1}')"
set -e
if [[ -n "$resolved_ip" ]]; then
  emit "PASS" "dns" "$dns_name -> $resolved_ip"
else
  emit "FAIL" "dns" "failed to resolve $dns_name"
fi

egress_status=0
set +e
curl -fsS --max-time "$max_time" "$egress_url" >/dev/null 2>&1
egress_status=$?
set -e

if [[ $egress_status -eq 0 ]]; then
  emit "PASS" "egress" "$egress_url"
else
  emit "FAIL" "egress" "curl exit $egress_status for $egress_url"
fi

probe_block() {
  local name="$1"
  local host="$2"
  local port="$3"
  local url="http://${host}:${port}/"
  local output=""
  local status=0

  set +e
  output=$(curl -sS --fail --connect-timeout "$connect_timeout" --max-time "$max_time" "$url" -o /dev/null 2>&1)
  status=$?
  set -e
  if [[ $status -eq 0 ]]; then
    emit "FAIL" "$name" "reachable ${host}:${port}"
    return 1
  fi

  if [[ $status -eq 28 ]]; then
    emit "PASS" "$name" "blocked (${host}:${port}, timeout)"
    return 0
  fi

  if [[ $status -eq 7 ]]; then
    if echo "$output" | grep -qi "Connection refused"; then
      emit "FAIL" "$name" "reachable (${host}:${port}, refused)"
      return 1
    fi
    if echo "$output" | grep -qi "No route to host"; then
      emit "PASS" "$name" "blocked (${host}:${port}, no route)"
      return 0
    fi
    if echo "$output" | grep -qi "Network is unreachable"; then
      emit "PASS" "$name" "blocked (${host}:${port}, network unreachable)"
      return 0
    fi
    if echo "$output" | grep -qi "timed out"; then
      emit "PASS" "$name" "blocked (${host}:${port}, timeout)"
      return 0
    fi
    emit "FAIL" "$name" "reachable (${host}:${port}, connect error)"
    return 1
  fi

  emit "FAIL" "$name" "reachable (${host}:${port}, curl exit ${status})"
  return 1
}

probe_block "lan-block" "$lan_host" "$lan_port" || true
probe_block "tailnet-block" "$tail_host" "$tail_port" || true
REMOTE
)
SSH_STATUS=$?
set -e

pass_count=0
fail_count=0

report() {
  local status="$1"
  local name="$2"
  local detail="$3"

  if [[ "$status" == "PASS" ]]; then
    pass_count=$((pass_count + 1))
    log "PASS: $name - $detail"
  else
    fail_count=$((fail_count + 1))
    log "FAIL: $name - $detail"
  fi
}

if [[ $SSH_STATUS -eq 255 ]]; then
  report "FAIL" "ssh" "failed to connect to ${SSH_DEST}"
  exit 1
fi

report "PASS" "ssh" "connected to ${SSH_DEST}"

while IFS='|' read -r status name detail; do
  if [[ "$status" != "PASS" && "$status" != "FAIL" ]]; then
    continue
  fi
  report "$status" "$name" "$detail"
done <<<"$REMOTE_OUTPUT"

if [[ $fail_count -gt 0 ]]; then
  log "Summary: FAIL (${fail_count} failed, ${pass_count} passed)"
  exit 1
fi

log "Summary: PASS (${pass_count} passed)"
