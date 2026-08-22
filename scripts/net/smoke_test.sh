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
  --gateway-ip IP      Host address on the agent bridge, used for the host
                       service blocks (default: vmbr1 IP or 10.77.0.1)
  --spoof-ip IP        In-subnet address that belongs to another sandbox.
                       The test claims it inside the sandbox and asserts the
                       connection to the gateway is blocked (L2 anti-spoofing).
                       Requires passwordless sudo in the sandbox. Off by default.
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
  AGENTLAB_GATEWAY_IP
  AGENTLAB_SPOOF_IP
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
GATEWAY_IP="${AGENTLAB_GATEWAY_IP:-}"
SPOOF_IP="${AGENTLAB_SPOOF_IP:-}"
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
    --gateway-ip)
      [[ $# -lt 2 ]] && die "--gateway-ip requires a value"
      GATEWAY_IP="$2"
      shift 2
      ;;
    --spoof-ip)
      [[ $# -lt 2 ]] && die "--spoof-ip requires a value"
      SPOOF_IP="$2"
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
    LAN_TARGET="$(ip -4 -o addr show vmbr0 2>/dev/null | awk '{print $4}' | cut -d/ -f1 | head -n1 || true)"
  fi
fi
if [[ -z "$LAN_TARGET" ]]; then
  die "LAN target not set; use --lan-target HOST[:PORT]"
fi

if [[ -z "$GATEWAY_IP" ]]; then
  if command -v ip >/dev/null 2>&1; then
    GATEWAY_IP="$(ip -4 -o addr show vmbr1 2>/dev/null | awk '{print $4}' | cut -d/ -f1 | head -n1 || true)"
  fi
fi
if [[ -z "$GATEWAY_IP" ]]; then
  GATEWAY_IP="10.77.0.1"
fi

if [[ -z "$TAILNET_TARGET" ]]; then
  if command -v tailscale >/dev/null 2>&1; then
    TAILNET_TARGET="$(tailscale ip -4 2>/dev/null | head -n1 || true)"
  fi
fi
if [[ -z "$TAILNET_TARGET" ]]; then
  if command -v ip >/dev/null 2>&1; then
    TAILNET_TARGET="$(ip -4 -o addr show tailscale0 2>/dev/null | awk '{print $4}' | cut -d/ -f1 | head -n1 || true)"
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
    "$GATEWAY_IP" \
    "$TIMEOUT_SEC" \
    "$TIMEOUT_SEC" \
    "$SPOOF_IP" <<'REMOTE'
set -euo pipefail

dns_name="$1"
egress_url="$2"
lan_host="$3"
lan_port="$4"
tail_host="$5"
tail_port="$6"
gateway_host="$7"
connect_timeout="$8"
max_time="$9"
spoof_ip="${10:-}"

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
  local extra=("${@:4}")
  local url="http://${host}:${port}/"
  local output=""
  local status=0

  set +e
  output=$(curl -sS --fail --connect-timeout "$connect_timeout" --max-time "$max_time" ${extra[@]+"${extra[@]}"} "$url" -o /dev/null 2>&1)
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

# A service that must stay reachable. Any HTTP-level answer, including an
# error status, proves the connection completed. Only a timeout or a refused
# or unreachable socket counts as blocked.
probe_allow() {
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
    emit "PASS" "$name" "reachable ${host}:${port}"
    return 0
  fi

  if echo "$output" | grep -qi "Connection refused"; then
    emit "FAIL" "$name" "blocked (${host}:${port}, refused)"
    return 1
  fi
  if echo "$output" | grep -qi "timed out"; then
    emit "FAIL" "$name" "blocked (${host}:${port}, timeout)"
    return 1
  fi
  if [[ $status -eq 7 || $status -eq 28 ]]; then
    emit "FAIL" "$name" "blocked (${host}:${port}, curl exit ${status})"
    return 1
  fi

  emit "PASS" "$name" "reachable (${host}:${port}, curl exit ${status})"
  return 0
}

probe_block "lan-block" "$lan_host" "$lan_port" || true
probe_block "tailnet-block" "$tail_host" "$tail_port" || true

# Host services on the agent bridge must be unreachable from a sandbox. These
# checks fail when the agentlab input chain is missing, because the Proxmox VE
# API and sshd then answer on the bridge address.
probe_block "host-pve-api-block" "$gateway_host" 8006 || true
probe_block "host-ssh-block" "$gateway_host" 22 || true

# The guest-facing listeners must still answer, so the input chain does not
# over-block. The metadata check also covers the DNAT path in prerouting.
probe_allow "host-bootstrap-reach" "$gateway_host" 8844 || true
probe_allow "host-metadata-reach" 169.254.169.254 80 || true

# Claim a neighbour's in-subnet address and connect to the gateway with it.
# The bridge agentlab_l2 rules must drop those frames, so the connection
# times out. The address is removed again afterwards.
spoof_check() {
  local spoof="$1"
  local dev=""
  local rc=0

  if ! command -v sudo >/dev/null 2>&1 || ! sudo -n true >/dev/null 2>&1; then
    emit "FAIL" "spoof-block" "passwordless sudo required in the sandbox"
    return 1
  fi
  command -v ip >/dev/null 2>&1 || {
    emit "FAIL" "spoof-block" "ip command missing in the sandbox"
    return 1
  }

  dev="$(ip -o route get "$gateway_host" 2>/dev/null | sed -n 's/.* dev \([^ ]*\).*/\1/p' | head -n1)"
  if [[ -z "$dev" ]]; then
    emit "FAIL" "spoof-block" "no route to ${gateway_host}"
    return 1
  fi

  if ! sudo -n ip address add "${spoof}/32" dev "$dev" >/dev/null 2>&1; then
    emit "FAIL" "spoof-block" "could not add ${spoof} to ${dev}"
    return 1
  fi

  probe_block "spoof-block" "$gateway_host" 8844 --interface "$spoof"
  rc=$?
  sudo -n ip address del "${spoof}/32" dev "$dev" >/dev/null 2>&1 || true
  return "$rc"
}

if [[ -n "$spoof_ip" ]]; then
  spoof_check "$spoof_ip" || true
fi
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
