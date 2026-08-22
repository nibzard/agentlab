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
                          [--tailscale-if tailscale0] [--tailnet-v4 100.64.0.0/10] [--tailnet-v6 fd7a:115c:a1e0::/48]
                          [--bridge-addr 10.77.0.1] [--guest-ports 8844,8846]
                          [--bind-tap PORT MAC IP | --unbind-tap PORT MAC IP | --sync-taps]

Options:
  --bridge   Agent bridge/interface name (default: vmbr1)
  --wan      WAN/LAN interface for NAT egress (default: vmbr0)
  --subnet   Agent subnet CIDR (default: 10.77.0.0/16)
  --bridge-addr  Host address on the agent bridge (default: 10.77.0.1)
  --guest-ports  Comma-separated guest-facing TCP ports the host keeps
                 reachable from the agent bridge (default: 8844,8846)
  --tailscale-if  Tailscale interface name (default: tailscale0)
  --tailnet-v4    Tailnet IPv4 CIDR to block from sandbox (default: 100.64.0.0/10)
  --tailnet-v6    Tailnet IPv6 CIDR to block from sandbox (default: fd7a:115c:a1e0::/48)
  --apply    Enable and start the agentlab-nftables.service
  --force    Overwrite managed files if they already exist with different content

Tap binding modes (update the bridge agentlab_l2 anti-spoofing sets only, no
files are written):
  --bind-tap PORT MAC IP    Bind one sandbox tap to its MAC and IPv4 address
  --unbind-tap PORT MAC IP  Remove one binding; the tap falls back to the
                            subnet-wide checks until it is bound again
  --sync-taps               Rebuild every binding from the Proxmox NIC
                            configuration and the dnsmasq leases
USAGE
}

BRIDGE="vmbr1"
WAN="vmbr0"
SUBNET="10.77.0.0/16"
BRIDGE_ADDR="10.77.0.1"
GUEST_PORTS="8844,8846"
TAILSCALE_IF="tailscale0"
TAILNET_V4="100.64.0.0/10"
TAILNET_V6="fd7a:115c:a1e0::/48"
APPLY=0
FORCE=0
BIND_TAP=()
UNBIND_TAP=()
SYNC_TAPS=0

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
    --bridge-addr)
      [[ $# -lt 2 ]] && die "--bridge-addr requires a value"
      BRIDGE_ADDR="$2"
      shift 2
      ;;
    --guest-ports)
      [[ $# -lt 2 ]] && die "--guest-ports requires a value"
      GUEST_PORTS="$2"
      shift 2
      ;;
    --tailscale-if)
      [[ $# -lt 2 ]] && die "--tailscale-if requires a value"
      TAILSCALE_IF="$2"
      shift 2
      ;;
    --tailnet-v4)
      [[ $# -lt 2 ]] && die "--tailnet-v4 requires a value"
      TAILNET_V4="$2"
      shift 2
      ;;
    --tailnet-v6)
      [[ $# -lt 2 ]] && die "--tailnet-v6 requires a value"
      TAILNET_V6="$2"
      shift 2
      ;;
    --apply)
      APPLY=1
      shift
      ;;
    --bind-tap)
      [[ $# -lt 4 ]] && die "--bind-tap requires PORT MAC IP"
      BIND_TAP=("$2" "$3" "$4")
      shift 4
      ;;
    --unbind-tap)
      [[ $# -lt 4 ]] && die "--unbind-tap requires PORT MAC IP"
      UNBIND_TAP=("$2" "$3" "$4")
      shift 4
      ;;
    --sync-taps)
      SYNC_TAPS=1
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

L2_TABLE="bridge agentlab_l2"
LEASE_GLOBS=(
  "/var/lib/misc/dnsmasq*.leases"
  "/var/lib/dnsmasq/*.leases"
)

# Print the IPv4 address that dnsmasq leased to one MAC, or fail.
lease_ip_for_mac() {
  local want="$(printf '%s' "$1" | tr 'A-F' 'a-f')"
  local now file ip
  now="$(date +%s)"
  local files=()
  local glob
  for glob in "${LEASE_GLOBS[@]}"; do
    while IFS= read -r file; do
      files+=("$file")
    done < <(compgen -G "$glob" || true)
  done
  for file in "${files[@]}"; do
    [[ -r "$file" ]] || continue
    ip="$(awk -v mac="$want" -v now="$now" \
      '($1 == "0" || $1 + 0 > now) && $2 == mac { found = $3 } END { if (found != "") print found }' \
      "$file")"
    if [[ -n "$ip" ]]; then
      printf '%s\n' "$ip"
      return 0
    fi
  done
  return 1
}

# Print "port vmid nic-index" for every sandbox tap on the agent bridge.
# Proxmox names a VM tap tap<vmid>i<idx>, the firewall link side fwln<vmid>i<idx>,
# and a container veth<vmid>i<idx>.
list_bridge_ports() {
  local link port master
  for link in /sys/class/net/*; do
    [[ -e "${link}/master" ]] || continue
    master="$(basename "$(readlink -f "${link}/master")")"
    [[ "$master" == "$BRIDGE" ]] || continue
    port="$(basename "$link")"
    [[ "$port" =~ ^(tap|fwln|veth)([0-9]+)i([0-9]+)$ ]] || continue
    printf '%s %s %s\n' "$port" "${BASH_REMATCH[2]}" "${BASH_REMATCH[3]}"
  done
}

# Print the MAC of one VM or container NIC from its Proxmox configuration.
vm_nic_mac() {
  local vmid="$1" idx="$2" tool line mac csv
  for tool in qm pct; do
    command -v "$tool" >/dev/null 2>&1 || continue
    line="$("$tool" config "$vmid" 2>/dev/null | awk -v key="net${idx}:" '$1 == key { print; exit }')"
    [[ -n "$line" ]] || continue
    csv=",${line#*:},"
    [[ "$csv" == *",bridge=${BRIDGE},"* ]] || return 1
    mac="$(printf '%s' "$line" | grep -oE '([0-9A-Fa-f]{2}:){5}[0-9A-Fa-f]{2}' | head -n1 || true)"
    [[ -n "$mac" ]] || return 1
    printf '%s\n' "$mac"
    return 0
  done
  return 1
}

require_l2_table() {
  "$NFT_BIN" list table $L2_TABLE >/dev/null 2>&1 || \
    die "table ${L2_TABLE} is not loaded; run scripts/net/apply.sh --apply first"
}

validate_binding() {
  local port="$1" mac="$2" ip="$3"
  [[ "$port" =~ ^[A-Za-z0-9._-]{1,15}$ ]] || die "invalid tap name: ${port}"
  [[ "$mac" =~ ^([0-9A-Fa-f]{2}:){5}[0-9A-Fa-f]{2}$ ]] || die "invalid MAC address: ${mac}"
  [[ "$ip" =~ ^([0-9]{1,3}\.){3}[0-9]{1,3}$ ]] || die "invalid IPv4 address: ${ip}"
}

bind_tap() {
  local port="$1" mac="$2" ip="$3"
  validate_binding "$port" "$mac" "$ip"
  require_l2_table

  # Delete first, so a repeated call updates the binding instead of growing
  # the set. The port joins bound_ports last: an interrupted bind then only
  # weakens the checks to the subnet-wide floor, never cuts the tap off.
  "$NFT_BIN" delete element $L2_TABLE tap_bindings "{ \"${port}\" . ${mac} . ${ip} }" >/dev/null 2>&1 || true
  "$NFT_BIN" delete element $L2_TABLE port_macs "{ \"${port}\" . ${mac} }" >/dev/null 2>&1 || true
  "$NFT_BIN" add element $L2_TABLE port_macs "{ \"${port}\" . ${mac} }" >/dev/null
  "$NFT_BIN" add element $L2_TABLE tap_bindings "{ \"${port}\" . ${mac} . ${ip} }" >/dev/null
  "$NFT_BIN" add element $L2_TABLE bound_ports "{ \"${port}\" }" >/dev/null
  log "bound ${port}: ${mac} -> ${ip}"
}

unbind_tap() {
  local port="$1" mac="$2" ip="$3"
  validate_binding "$port" "$mac" "$ip"
  require_l2_table

  "$NFT_BIN" delete element $L2_TABLE tap_bindings "{ \"${port}\" . ${mac} . ${ip} }" >/dev/null
  "$NFT_BIN" delete element $L2_TABLE port_macs "{ \"${port}\" . ${mac} }" >/dev/null 2>&1 || true
  "$NFT_BIN" delete element $L2_TABLE bound_ports "{ \"${port}\" }" >/dev/null 2>&1 || true
  log "unbound ${port}: ${mac} no longer requires ${ip}"
}

sync_tap_bindings() {
  require_l2_table

  local port vmid idx mac ip bound=0 pending=0
  "$NFT_BIN" flush set $L2_TABLE bound_ports >/dev/null
  "$NFT_BIN" flush set $L2_TABLE port_macs >/dev/null
  "$NFT_BIN" flush set $L2_TABLE tap_bindings >/dev/null

  while read -r port vmid idx; do
    [[ -n "$port" ]] || continue
    if ! mac="$(vm_nic_mac "$vmid" "$idx")" || [[ -z "$mac" ]]; then
      pending=$((pending + 1))
      continue
    fi
    if ! ip="$(lease_ip_for_mac "$mac")" || [[ -z "$ip" ]]; then
      pending=$((pending + 1))
      continue
    fi
    "$NFT_BIN" add element $L2_TABLE port_macs "{ \"${port}\" . ${mac} }" >/dev/null
    "$NFT_BIN" add element $L2_TABLE tap_bindings "{ \"${port}\" . ${mac} . ${ip} }" >/dev/null
    "$NFT_BIN" add element $L2_TABLE bound_ports "{ \"${port}\" }" >/dev/null
    bound=$((bound + 1))
    log "bound ${port}: ${mac} -> ${ip}"
  done < <(list_bridge_ports)

  log "tap bindings: ${bound} bound, ${pending} without a binding (subnet-wide floor applies)"
}

if (( ${#BIND_TAP[@]} > 0 || ${#UNBIND_TAP[@]} > 0 || SYNC_TAPS )); then
  if (( ${#BIND_TAP[@]} > 0 )); then
    bind_tap "${BIND_TAP[@]}"
  fi
  if (( ${#UNBIND_TAP[@]} > 0 )); then
    unbind_tap "${UNBIND_TAP[@]}"
  fi
  if (( SYNC_TAPS )); then
    sync_tap_bindings
  fi
  log "Done"
  exit 0
fi

NFT_DIR="/etc/nftables.d"
DEST_FILE="${NFT_DIR}/agentlab.nft"
UNIT_FILE="/etc/systemd/system/agentlab-nftables.service"

format_guest_ports() {
  local out=""
  local port=""

  IFS=',' read -r -a raw_ports <<<"$GUEST_PORTS"
  for port in "${raw_ports[@]}"; do
    port="${port//[[:space:]]/}"
    [[ "$port" =~ ^[0-9]+$ ]] || die "--guest-ports entries must be numbers: $port"
    (( port >= 1 && port <= 65535 )) || die "--guest-ports entry out of range: $port"
    out="${out:+$out, }$port"
  done

  [[ -n "$out" ]] || die "--guest-ports requires at least one port"
  printf "{ %s }" "$out"
}

render_rules() {
  sed \
    -e "s|^define agent_if = .*|define agent_if = \"${BRIDGE}\"|" \
    -e "s|^define wan_if = .*|define wan_if = \"${WAN}\"|" \
    -e "s|^define agent_subnet = .*|define agent_subnet = ${SUBNET}|" \
    -e "s|^define bridge_addr = .*|define bridge_addr = ${BRIDGE_ADDR}|" \
    -e "s|^define guest_tcp_ports = .*|define guest_tcp_ports = ${GUEST_PORT_SET}|" \
    -e "s|^define tailscale_if = .*|define tailscale_if = \"${TAILSCALE_IF}\"|" \
    -e "s|^define tailnet_v4 = .*|define tailnet_v4 = ${TAILNET_V4}|" \
    -e "s|^define tailnet_v6 = .*|define tailnet_v6 = ${TAILNET_V6}|" \
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

GUEST_PORT_SET="$(format_guest_ports)"

rules_tmp="$(mktemp)"
render_rules > "$rules_tmp"
write_if_changed "$rules_tmp" "$DEST_FILE"
rm -f "$rules_tmp"

unit_tmp="$(mktemp)"
cat <<EOF_UNIT > "$unit_tmp"
[Unit]
Description=AgentLab nftables rules (agent NAT, egress/tailnet blocks, host and L2 filtering)
After=network.target
Wants=network.target

[Service]
Type=oneshot
ExecStartPre=-${NFT_BIN} delete table inet agentlab
ExecStartPre=-${NFT_BIN} delete table bridge agentlab_l2
ExecStartPre=-${NFT_BIN} delete table ip agentlab_nat
ExecStart=${NFT_BIN} -f ${DEST_FILE}
# Refresh the per-tap anti-spoofing bindings from the Proxmox NIC
# configuration and the DHCP leases. A missing script or data source only
# weakens the checks to the subnet-wide floor, so the minus prefix is safe.
ExecStartPost=-${SCRIPT_DIR}/apply.sh --sync-taps
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
    "$NFT_BIN" delete table bridge agentlab_l2 >/dev/null 2>&1 || true
    "$NFT_BIN" delete table ip agentlab_nat >/dev/null 2>&1 || true
    "$NFT_BIN" -f "$DEST_FILE"
    sync_tap_bindings
  fi
else
  log "Rules installed. Apply with: systemctl enable --now agentlab-nftables.service"
fi

log "Done"
