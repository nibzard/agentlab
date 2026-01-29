#!/usr/bin/env bash
set -euo pipefail

log() {
  printf "[agentlab-net] %s\n" "$*"
}

die() {
  printf "[agentlab-net] ERROR: %s\n" "$*" >&2
  exit 1
}

usage() {
  cat <<'USAGE'
Usage: scripts/net/setup_vmbr1.sh [--bridge vmbr1] [--address 10.77.0.1/16] [--apply] [--force]

Options:
  --bridge   Bridge name to configure (default: vmbr1)
  --address  Bridge address with CIDR (default: 10.77.0.1/16)
  --apply    Apply network changes immediately (ifreload -a or ifup)
  --force    Overwrite managed config file if it already exists

This script:
  - writes a vmbr1 bridge config for Proxmox/ifupdown
  - enables IPv4 forwarding persistently via /etc/sysctl.d/99-agentlab.conf
USAGE
}

BRIDGE="vmbr1"
ADDRESS="10.77.0.1/16"
APPLY=0
FORCE=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --bridge)
      [[ $# -lt 2 ]] && die "--bridge requires a value"
      BRIDGE="$2"
      shift 2
      ;;
    --address)
      [[ $# -lt 2 ]] && die "--address requires a value"
      ADDRESS="$2"
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

INTERFACES="/etc/network/interfaces"
SYSCTL_FILE="/etc/sysctl.d/99-agentlab.conf"
INCLUDE_REGEX='^[[:space:]]*(source|source-directory)[[:space:]]+/etc/network/interfaces\.d/\*'

[[ -f "$INTERFACES" ]] || die "$INTERFACES not found"

log "Ensuring IPv4 forwarding is enabled"
SYSCTL_CONTENT="# AgentLab forwarding\nnet.ipv4.ip_forward=1\n"

if [[ -f "$SYSCTL_FILE" ]]; then
  existing_sysctl="$(cat "$SYSCTL_FILE")"
  if [[ "$existing_sysctl" != "$SYSCTL_CONTENT" ]]; then
    log "Updating $SYSCTL_FILE"
    printf "%s" "$SYSCTL_CONTENT" > "$SYSCTL_FILE"
  else
    log "$SYSCTL_FILE already up to date"
  fi
else
  log "Writing $SYSCTL_FILE"
  printf "%s" "$SYSCTL_CONTENT" > "$SYSCTL_FILE"
fi

if ! sysctl -p "$SYSCTL_FILE" >/dev/null; then
  die "Failed to apply sysctl settings"
fi

BRIDGE_STANZA="auto $BRIDGE
iface $BRIDGE inet static
    address $ADDRESS
    bridge-ports none
    bridge-stp off
    bridge-fd 0
"

existing_configs="$(grep -Rsl "iface[[:space:]]\+$BRIDGE" "$INTERFACES" /etc/network/interfaces.d 2>/dev/null || true)"
TARGET_FILE=""

if grep -Eq "$INCLUDE_REGEX" "$INTERFACES"; then
  mkdir -p /etc/network/interfaces.d
  TARGET_FILE="/etc/network/interfaces.d/agentlab-${BRIDGE}.cfg"
  if [[ -n "$existing_configs" && "$existing_configs" != "$TARGET_FILE" ]]; then
    die "Bridge $BRIDGE already defined in: $existing_configs"
  fi

  if [[ -f "$TARGET_FILE" ]]; then
    if grep -q "address $ADDRESS" "$TARGET_FILE" && grep -q "iface $BRIDGE" "$TARGET_FILE"; then
      log "$TARGET_FILE already defines $BRIDGE"
    elif [[ "$FORCE" == "1" ]]; then
      log "Overwriting $TARGET_FILE"
      printf "%s" "$BRIDGE_STANZA" > "$TARGET_FILE"
    else
      die "$TARGET_FILE exists with different content; use --force to overwrite"
    fi
  else
    log "Writing $TARGET_FILE"
    printf "%s" "$BRIDGE_STANZA" > "$TARGET_FILE"
  fi
else
  TARGET_FILE="$INTERFACES"
  if [[ -n "$existing_configs" ]]; then
    log "$BRIDGE already configured in $existing_configs; skipping bridge config"
  else
    log "Appending $BRIDGE to $INTERFACES"
    printf "\n# AgentLab vmbr1 bridge\n%s" "$BRIDGE_STANZA" >> "$INTERFACES"
  fi
fi

if [[ "$APPLY" == "1" ]]; then
  if command -v ifreload >/dev/null 2>&1; then
    log "Applying network config with ifreload -a"
    ifreload -a
  elif command -v ifup >/dev/null 2>&1; then
    log "Bringing up $BRIDGE with ifup"
    ifup "$BRIDGE"
  else
    die "Neither ifreload nor ifup found to apply network changes"
  fi
else
  log "Bridge config written; apply with 'ifreload -a' or 'ifup $BRIDGE'"
fi

log "Done"
