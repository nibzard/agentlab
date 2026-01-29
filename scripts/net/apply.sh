#!/usr/bin/env bash
set -euo pipefail

log() {
  printf "[agentlab-nft] %s\n" "$*"
}

die() {
  printf "[agentlab-nft] ERROR: %s\n" "$*" >&2
  exit 1
}

usage() {
  cat <<'USAGE'
Usage: scripts/net/apply.sh [--bridge vmbr1] [--wan vmbr0] [--subnet 10.77.0.0/16] [--apply] [--force]

Options:
  --bridge   Agent bridge/interface name (default: vmbr1)
  --wan      WAN/LAN interface for NAT egress (default: vmbr0)
  --subnet   Agent subnet CIDR (default: 10.77.0.0/16)
  --apply    Enable and start the agentlab-nftables.service
  --force    Overwrite managed files if they already exist with different content
USAGE
}

BRIDGE="vmbr1"
WAN="vmbr0"
SUBNET="10.77.0.0/16"
APPLY=0
FORCE=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --bridge)
      [[ $# -lt 2 ]] && die "--bridge requires a value"
      BRIDGE="$2"
      shift 2
      ;;
    --wan)
      [[ $# -lt 2 ]] && die "--wan requires a value"
      WAN="$2"
      shift 2
      ;;
    --subnet)
      [[ $# -lt 2 ]] && die "--subnet requires a value"
      SUBNET="$2"
      shift 2
      ;;
    --apply)
      APPLY=1
      shift
      ;;
    --force)
      FORCE=1
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

if [[ $EUID -ne 0 ]]; then
  die "This script must be run as root"
fi

NFT_BIN="$(command -v nft || true)"
[[ -n "$NFT_BIN" ]] || die "nft command not found"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TEMPLATE="${SCRIPT_DIR}/agent_nat.nft"
[[ -f "$TEMPLATE" ]] || die "Template not found: $TEMPLATE"

NFT_DIR="/etc/nftables.d"
DEST_FILE="${NFT_DIR}/agentlab.nft"
UNIT_FILE="/etc/systemd/system/agentlab-nftables.service"

render_rules() {
  sed \
    -e "s|^define agent_if = .*|define agent_if = \"${BRIDGE}\"|" \
    -e "s|^define wan_if = .*|define wan_if = \"${WAN}\"|" \
    -e "s|^define agent_subnet = .*|define agent_subnet = ${SUBNET}|" \
    "$TEMPLATE"
}

write_if_changed() {
  local content="$1"
  local target="$2"

  if [[ -f "$target" ]]; then
    if cmp -s "$content" "$target"; then
      log "$target already up to date"
      return
    fi

    if [[ "$FORCE" != "1" ]]; then
      die "$target exists with different content; use --force to overwrite"
    fi
  fi

  log "Writing $target"
  install -m 0644 "$content" "$target"
}

install -d -m 0755 "$NFT_DIR"
install -d -m 0755 "$(dirname "$UNIT_FILE")"

rules_tmp="$(mktemp)"
render_rules > "$rules_tmp"
write_if_changed "$rules_tmp" "$DEST_FILE"
rm -f "$rules_tmp"

unit_tmp="$(mktemp)"
cat <<EOF_UNIT > "$unit_tmp"
[Unit]
Description=AgentLab nftables rules (agent NAT + egress blocks)
After=network.target
Wants=network.target

[Service]
Type=oneshot
ExecStartPre=-${NFT_BIN} delete table inet agentlab
ExecStartPre=-${NFT_BIN} delete table ip agentlab_nat
ExecStart=${NFT_BIN} -f ${DEST_FILE}
RemainAfterExit=yes

[Install]
WantedBy=multi-user.target
EOF_UNIT

write_if_changed "$unit_tmp" "$UNIT_FILE"
rm -f "$unit_tmp"

if [[ "$APPLY" == "1" ]]; then
  if command -v systemctl >/dev/null 2>&1; then
    log "Enabling agentlab-nftables.service"
    systemctl daemon-reload
    systemctl enable --now agentlab-nftables.service

    if ! systemctl is-active --quiet agentlab-nftables.service; then
      systemctl --no-pager --full status agentlab-nftables.service || true
      die "agentlab-nftables.service failed to start"
    fi

    log "agentlab-nftables.service is active"
  else
    log "systemctl not found; applying rules once"
    "$NFT_BIN" delete table inet agentlab >/dev/null 2>&1 || true
    "$NFT_BIN" delete table ip agentlab_nat >/dev/null 2>&1 || true
    "$NFT_BIN" -f "$DEST_FILE"
  fi
else
  log "Rules installed. Apply with: systemctl enable --now agentlab-nftables.service"
fi

log "Done"
