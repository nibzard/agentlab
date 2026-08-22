#!/usr/bin/env bash
set -euo pipefail

# Verifies T11, T20, and T21 for the agentlab network scripts.
#
# Part A runs everywhere. It checks the template and the smoke test with
# static assertions, so the checks fail when a rule or probe is removed.
#
# Part B needs root. It builds a private network namespace with one bridge,
# three taps, and three guest namespaces, loads the real template, binds two
# taps with the real apply.sh, and drives real traffic through the rules.
# It also runs the remote probe block of smoke_test.sh inside a guest. The
# lab never touches the host network namespace. Part B is skipped when root
# or unshare is unavailable.

log() {
  printf "[net-filter-test] %s\n" "$*"
}

die() {
  printf "[net-filter-test] ERROR: %s\n" "$*" >&2
  exit 1
}

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
TEMPLATE="${ROOT_DIR}/scripts/net/agent_nat.nft"
APPLY_SH="${ROOT_DIR}/scripts/net/apply.sh"
SMOKE_SH="${ROOT_DIR}/scripts/net/smoke_test.sh"

for f in "$TEMPLATE" "$APPLY_SH" "$SMOKE_SH"; do
  [[ -f "$f" ]] || die "missing file: $f"
done

check() {
  local desc="$1"
  local file="$2"
  local pattern="$3"
  if grep -Eq "$pattern" "$file"; then
    log "PASS: ${desc}"
  else
    log "FAIL: ${desc}"
    FAILURES=$((FAILURES + 1))
  fi
}

FAILURES=0

log "Part A: static checks"

bash -n "$APPLY_SH" || die "apply.sh fails bash -n"
bash -n "$SMOKE_SH" || die "smoke_test.sh fails bash -n"
log "PASS: shell scripts parse"

# apply.sh must expose the tap-binding modes and render the guest ports
# into the template define.
for flag in --bind-tap --unbind-tap --sync-taps --guest-ports --bridge-addr; do
  if "$APPLY_SH" --help 2>/dev/null | grep -q -- "$flag"; then
    log "PASS: apply.sh documents ${flag}"
  else
    log "FAIL: apply.sh documents ${flag}"
    FAILURES=$((FAILURES + 1))
  fi
done
check "template defines guest ports for rendering" "$TEMPLATE" \
  '^define guest_tcp_ports = '
check "template defines bridge address for rendering" "$TEMPLATE" \
  '^define bridge_addr = '

# The template must keep the values in defines. No literal sandbox addresses
# inside the rules.
check "input chain hooks input" "$TEMPLATE" \
  '^  chain input \{$'
check "input chain polices only the agent bridge" "$TEMPLATE" \
  'iifname != \$agent_if return'
check "input chain accepts guest ports from the define" "$TEMPLATE" \
  'tcp dport \$guest_tcp_ports accept'
check "input chain accepts the metadata address from the define" "$TEMPLATE" \
  'ip daddr \$metadata_ip tcp dport 80 accept'
check "input chain drops every other new connection" "$TEMPLATE" \
  'ct state new drop'
check "input chain accepts DNS and DHCP" "$TEMPLATE" \
  'udp dport 53 accept'
check "bridge table exists" "$TEMPLATE" \
  'table bridge agentlab_l2'
check "bridge chain drops ARP from a bound tap on mismatch" "$TEMPLATE" \
  'ether type arp iifname @bound_ports drop'
check "bridge chain drops IPv4 from a bound tap on mismatch" "$TEMPLATE" \
  'ether type ip iifname @bound_ports drop'
check "strict checks key on the tap port, not the bridge" "$TEMPLATE" \
  'ether type ip iifname \. ether saddr \. ip saddr @tap_bindings return'

# Guest ports may appear only in the defines section, never inside a rule.
if awk '/^table /{in_table=1} in_table && /8844/{found=1} END{exit found}' "$TEMPLATE"; then
  log "FAIL: literal guest port inside a table block"
  FAILURES=$((FAILURES + 1))
else
  log "PASS: guest ports stay in the defines"
fi

# nft treats meta ibriport as a second meta ibrname, which matches the
# bridge for every tap. The per-tap rules must use iifname instead.
if grep -v '^[[:space:]]*#' "$TEMPLATE" | grep -q 'ibriport'; then
  log "FAIL: template uses meta ibriport, which never matches a tap"
  FAILURES=$((FAILURES + 1))
else
  log "PASS: template does not use meta ibriport in rules"
fi

# Smoke test assertions for T21 and the T11 criterion.
check "smoke test probes the Proxmox VE API block" "$SMOKE_SH" \
  'probe_block "host-pve-api-block".*8006'
check "smoke test probes the host ssh block" "$SMOKE_SH" \
  'probe_block "host-ssh-block".*22'
check "smoke test keeps bootstrap reachable" "$SMOKE_SH" \
  'probe_allow "host-bootstrap-reach".*8844'
check "smoke test keeps metadata reachable" "$SMOKE_SH" \
  'probe_allow "host-metadata-reach".*169\.254\.169\.254 80'
check "smoke test claims a neighbour address" "$SMOKE_SH" \
  'spoof_check|--spoof-ip'

# Syntax check of the template. nft needs CAP_NET_ADMIN even for -c, so run
# it in a throwaway user namespace when the current shell lacks it.
if command -v nft >/dev/null 2>&1; then
  if nft -c -f "$TEMPLATE" >/dev/null 2>&1; then
    log "PASS: nft -c accepts the template"
  elif unshare -Urn nft -c -f "$TEMPLATE" >/dev/null 2>&1; then
    log "PASS: nft -c accepts the template (user namespace)"
  else
    log "FAIL: nft -c rejects the template"
    nft -c -f "$TEMPLATE" 2>&1 | head -n 5 || true
    FAILURES=$((FAILURES + 1))
  fi
else
  log "SKIP: nft not installed; template not syntax checked"
fi

log "Part A result: ${FAILURES} failures"

# ---------------------------------------------------------------------------
# Part B: live ruleset test in a private network namespace.
# ---------------------------------------------------------------------------

CAN_RUN_LAB=1
if [[ $EUID -ne 0 ]] && ! sudo -n true >/dev/null 2>&1; then
  CAN_RUN_LAB=0
fi
if ! command -v unshare >/dev/null 2>&1; then
  CAN_RUN_LAB=0
fi
for cmd in nft ip curl python3; do
  command -v "$cmd" >/dev/null 2>&1 || CAN_RUN_LAB=0
done

if [[ $CAN_RUN_LAB -eq 0 ]]; then
  log "SKIP: Part B needs root, unshare, nft, ip, curl, and python3"
  if (( FAILURES > 0 )); then
    die "Part A reported ${FAILURES} failures"
  fi
  log "Summary: PASS (static checks only; live lab skipped)"
  exit 0
fi

LAB="$(mktemp /tmp/agentlab-net-filter-lab.XXXXXX.inner.sh)"
trap 'rm -f "$LAB"' EXIT
cat >"$LAB" <<'INNER'
#!/usr/bin/env bash
set -uo pipefail
# Runs inside: unshare -n -m bash <this file>. Everything here is isolated
# from the host: a fresh network namespace and a fresh mount namespace.

TEMPLATE="$1"
APPLY_SH="$2"
SMOKE_SH="$3"

fails=0
result() { # result PASS|SKIP|FAIL name detail
  printf "%s %s %s\n" "$1" "$2" "$3"
  [[ "$1" != "FAIL" ]] || fails=$((fails + 1))
}

probe_http() { # probe_http NETNS IP PORT CURL_ARGS...
  local netns="$1" ip="$2" port="$3"; shift 3
  ip netns exec "$netns" curl -sS -o /dev/null --connect-timeout 2 \
    --max-time 3 "$@" -w '%{http_code}' "http://${ip}:${port}/" 2>&1 || true
}

# Topology: vmbr1 is the agent bridge, vmbr0 stands in for the LAN.
# A crashed earlier run can leave plain files under /run/netns that break
# ip netns add, so remove them first and delete the namespaces on exit.
rm -f /run/netns/ga /run/netns/gb /run/netns/gc
PIDS=()
cleanup() {
  kill "${PIDS[@]}" >/dev/null 2>&1 || true
  local n
  for n in ga gb gc; do
    ip netns del "$n" >/dev/null 2>&1 || true
  done
  rm -f /run/netns/ga /run/netns/gb /run/netns/gc
}
trap cleanup EXIT

ip link set lo up
# setup_vmbr1.sh enables forwarding on the real host. The lab needs it too,
# or the kernel answers forwarded probes with ICMP errors instead of letting
# the forward chain drop them silently.
sysctl -qw net.ipv4.ip_forward=1 || true
ip link add vmbr0 type bridge
ip link set vmbr0 up
ip addr add 192.168.50.1/24 dev vmbr0
# The lab has no upstream. Route the tailnet range at the host so packets
# reach the forward chain and the drop rule decides, instead of the routing
# layer answering with an instant ICMP unreachable.
ip route add 100.64.0.0/10 dev vmbr0
ip link add vmbr1 type bridge
ip link set vmbr1 up
ip addr add 10.77.0.1/16 dev vmbr1
ip addr add 169.254.169.254/32 dev vmbr1

mk_guest() { # mk_guest NAME TAP IP
  ip netns add "$1" || { result FAIL "netns-$1" "ip netns add failed"; exit 1; }
  ip link add "v_$1" netns "$1" type veth peer name "$2"
  ip link set "$2" master vmbr1 up
  ip netns exec "$1" ip link set lo up
  ip netns exec "$1" ip link set "v_$1" up
  ip netns exec "$1" ip addr add "$3/16" dev "v_$1"
  ip netns exec "$1" ip route add default via 10.77.0.1
  ip netns exec "$1" true || { result FAIL "netns-$1" "netns unusable"; exit 1; }
}
mac_of() {
  ip netns exec "$1" ip -o link show "v_$1" \
    | sed -n 's/.*ether \([0-9a-f:]*\).*/\1/p'
}

mk_guest ga tap101i0 10.77.0.5
mk_guest gb tap102i0 10.77.0.9
mk_guest gc tap103i0 10.77.0.13
MAC_A="$(mac_of ga)"
MAC_B="$(mac_of gb)"

# Load the shipped template and bind two taps with the shipped script. The
# third tap stays unbound and exercises the subnet-wide floor.
nft -f "$TEMPLATE" || { result FAIL load-template "nft -f failed"; exit 1; }
"$APPLY_SH" --bind-tap tap101i0 "$MAC_A" 10.77.0.5 >/dev/null 2>&1 \
  || { result FAIL bind-tap-a "apply.sh --bind-tap failed"; }
"$APPLY_SH" --bind-tap tap102i0 "$MAC_B" 10.77.0.9 >/dev/null 2>&1 \
  || { result FAIL bind-tap-b "apply.sh --bind-tap failed"; }
bound="$(nft list set bridge agentlab_l2 bound_ports 2>/dev/null | grep -c 'tap')"
if (( bound >= 2 )); then
  result PASS bind-taps "bound_ports holds ${bound} taps"
else
  result FAIL bind-taps "bound_ports holds only ${bound} taps"
fi

# Listeners that stand in for the Proxmox VE API, sshd, and the daemon.
SRV_DIR="$(mktemp -d)"
python3 -m http.server 8844 --bind 10.77.0.1 --directory "$SRV_DIR" >/dev/null 2>&1 &
PIDS+=("$!")
python3 -m http.server 8006 --bind 0.0.0.0 --directory "$SRV_DIR" >/dev/null 2>&1 &
PIDS+=("$!")
python3 -m http.server 22 --bind 0.0.0.0 --directory "$SRV_DIR" >/dev/null 2>&1 &
PIDS+=("$!")
sleep 1

# The daemon installs this DNAT rule for the metadata endpoint
# (internal/daemon/metadata_routing.go).
if iptables -t nat -A PREROUTING -d 169.254.169.254 -p tcp --dport 80 \
  -j DNAT --to-destination 10.77.0.1:8844 2>/dev/null; then
  result PASS metadata-dnat "iptables DNAT installed"
else
  result SKIP metadata-dnat "iptables unavailable; direct rule covers the flow"
fi

g() { ip netns exec ga "$@"; }

# T20: guest-facing port reachable from a bound sandbox.
out="$(probe_http ga 10.77.0.1 8844)"
if [[ "$out" == "200" ]]; then
  result PASS bootstrap-reach "10.77.0.1:8844 answered"
else
  result FAIL bootstrap-reach "10.77.0.1:8844 unreachable (${out})"
fi

# T20: the Proxmox VE API port is blocked even though a listener answers.
out="$(probe_http ga 10.77.0.1 8006)"
if [[ "$out" == *timed* || "$out" == *Timeout* || -z "$out" ]]; then
  result PASS pve-api-block "10.77.0.1:8006 blocked"
else
  result FAIL pve-api-block "10.77.0.1:8006 answered (${out})"
fi

# T20: sshd on the bridge address is blocked.
out="$(probe_http ga 10.77.0.1 22)"
if [[ "$out" == *timed* || "$out" == *Timeout* || -z "$out" ]]; then
  result PASS ssh-block "10.77.0.1:22 blocked"
else
  result FAIL ssh-block "10.77.0.1:22 answered (${out})"
fi

# T20: a host address that is not on the agent bridge is blocked too.
out="$(probe_http ga 192.168.50.1 8006)"
if [[ "$out" == *timed* || "$out" == *Timeout* || -z "$out" ]]; then
  result PASS host-addr-block "192.168.50.1:8006 blocked"
else
  result FAIL host-addr-block "192.168.50.1:8006 answered (${out})"
fi

# T20: the metadata endpoint still answers through the DNAT path.
out="$(probe_http ga 169.254.169.254 80)"
if [[ "$out" == "200" ]]; then
  result PASS metadata-reach "169.254.169.254:80 answered"
else
  result FAIL metadata-reach "169.254.169.254:80 unreachable (${out})"
fi

# A raw gratuitous ARP frame is the classic ARP-poisoning tool. The lab
# sends one from a guest namespace; the observable is the neighbour entry
# on the receiving side. An entry in STALE state accepts an update, so the
# check cannot pass vacuously: with the rules removed the entry flips to
# the attacker MAC.
GARP="$(mktemp /tmp/agentlab-garp.XXXXXX.py)"
cat >"$GARP" <<'PY'
import socket, struct, sys
# argv: IFNAME MAC IP OP — send one gratuitous ARP frame (1 request, 2 reply).
ifname, mac, ip, op = sys.argv[1], sys.argv[2], sys.argv[3], int(sys.argv[4])
sha = bytes(int(b, 16) for b in mac.split(':'))
spa = socket.inet_aton(ip)
bcast = b'\xff' * 6
arp = struct.pack('!HHBBH6s4s6s4s', 1, 0x0800, 6, 4, op, sha, spa, bcast, spa)
frame = bcast + sha + struct.pack('!H', 0x0806) + arp
s = socket.socket(socket.AF_PACKET, socket.SOCK_RAW)
s.bind((ifname, 0))
s.send(frame)
PY

# T11: a bound sandbox claims its neighbour's address. The claim is the
# pivot of the F4 exploit: only with the neighbour's IP mapped to the
# attacker MAC can the attacker receive the replies of a spoofed
# connection. The host must keep mapping the neighbour to the neighbour's
# MAC. This is the done-when criterion for T11.
ping -c1 -W1 10.77.0.9 >/dev/null 2>&1 || true
ip neigh change 10.77.0.9 dev vmbr1 nud stale >/dev/null 2>&1 || true
neigh="$(ip neigh show 10.77.0.9 dev vmbr1 | head -n1)"
if [[ -z "$MAC_B" || "$neigh" != *"$MAC_B"* ]]; then
  result SKIP spoof-block "host did not resolve 10.77.0.9 (${neigh:-none})"
else
  ip netns exec ga python3 "$GARP" v_ga "$MAC_A" 10.77.0.9 1 >/dev/null 2>&1
  ip netns exec ga python3 "$GARP" v_ga "$MAC_A" 10.77.0.9 2 >/dev/null 2>&1
  sleep 1
  neigh="$(ip neigh show 10.77.0.9 dev vmbr1 | head -n1)"
  if [[ "$neigh" == *"$MAC_A"* ]]; then
    result FAIL spoof-block "ga hijacked the host entry for 10.77.0.9 (${neigh})"
  elif [[ "$neigh" == *"$MAC_B"* ]]; then
    result PASS spoof-block "the ARP claim of 10.77.0.9 was dropped"
  else
    result FAIL spoof-block "unexpected neighbour entry (${neigh:-none})"
  fi
  ip neigh flush 10.77.0.9 dev vmbr1 >/dev/null 2>&1 || true
fi

# T11: an unbound tap keeps service through the subnet-wide floor.
out="$(probe_http gc 10.77.0.1 8844)"
if [[ "$out" == "200" ]]; then
  result PASS floor-allows "unbound tap still reaches 10.77.0.1:8844"
else
  result FAIL floor-allows "unbound tap cannot reach 10.77.0.1:8844 (${out})"
fi

# T11: no tap may claim the bridge address by ARP, not even an unbound
# one. gc is unbound, so only the subnet-wide floor protects the gateway.
# gb must keep mapping the gateway to the bridge MAC after gc claims it.
GW_MAC="$(ip -o link show vmbr1 | sed -n 's/.*ether \([0-9a-f:]*\).*/\1/p')"
GC_MAC="$(ip netns exec gc ip -o link show v_gc \
  | sed -n 's/.*ether \([0-9a-f:]*\).*/\1/p')"
ip netns exec gb ping -c1 -W1 10.77.0.1 >/dev/null 2>&1 || true
ip netns exec gb ip neigh change 10.77.0.1 dev v_gb nud stale \
  >/dev/null 2>&1 || true
neigh="$(ip netns exec gb ip neigh show 10.77.0.1 dev v_gb | head -n1)"
if [[ -z "$GW_MAC" || "$neigh" != *"$GW_MAC"* ]]; then
  result SKIP gateway-claim-block "gb did not resolve the gateway (${neigh:-none})"
else
  ip netns exec gc python3 "$GARP" v_gc "$GC_MAC" 10.77.0.1 1 >/dev/null 2>&1
  ip netns exec gc python3 "$GARP" v_gc "$GC_MAC" 10.77.0.1 2 >/dev/null 2>&1
  sleep 1
  neigh="$(ip netns exec gb ip neigh show 10.77.0.1 dev v_gb | head -n1)"
  if [[ "$neigh" == *"$GC_MAC"* ]]; then
    result FAIL gateway-claim-block "gc hijacked the gateway entry (${neigh})"
  elif [[ "$neigh" == *"$GW_MAC"* ]]; then
    result PASS gateway-claim-block "ARP claim of 10.77.0.1 was dropped"
  else
    result FAIL gateway-claim-block "unexpected neighbour entry (${neigh:-none})"
  fi
fi
rm -f "$GARP"

# T21: the smoke test must fail when the input chain is gone. Remove the
# chain and confirm the Proxmox VE API becomes reachable, which flips the
# host-pve-api-block probe to FAIL.
nft delete chain inet agentlab input
out="$(probe_http ga 10.77.0.1 8006)"
nft -f "$TEMPLATE"
if [[ "$out" == "200" ]]; then
  result PASS chain-removal-flips "8006 reachable without the input chain"
else
  result FAIL chain-removal-flips "8006 stayed blocked without the chain (${out})"
fi

# Run the real probe block from smoke_test.sh inside a guest, with the same
# arguments the SSH path passes. DNS and egress have no servers here, so
# those two lines are ignored.
remote="$(sed -n "/<<'REMOTE'/,/^REMOTE$/p" "$SMOKE_SH" \
  | sed '1d;$d')"

smoke_out="$(printf '%s\n' "$remote" | ip netns exec ga bash -s -- \
  "example.com" "https://example.com" \
  "192.168.50.1" "22" "100.64.1.1" "22" \
  "10.77.0.1" "2" "3" "10.77.0.9" 2>&1 || true)"

for probe in lan-block tailnet-block host-pve-api-block host-ssh-block \
  host-bootstrap-reach host-metadata-reach spoof-block; do
  if printf '%s\n' "$smoke_out" | grep -q "PASS|${probe}|"; then
    result PASS "smoke:${probe}" "probe reports blocked/allowed as required"
  else
    detail="$(printf '%s\n' "$smoke_out" | grep "|${probe}|" | head -n1)"
    result FAIL "smoke:${probe}" "probe did not pass (${detail:-no output})"
  fi
done

# apply.sh tap-binding modes. The lab has no qm or pct, so a sync cannot
# rebuild the bindings; it must still succeed, leave the sets empty, and
# keep the guests working through the subnet-wide floor. First check that
# unbind removes exactly one binding.
if "$APPLY_SH" --unbind-tap tap102i0 "$MAC_B" 10.77.0.9 >/dev/null 2>&1; then
  result PASS unbind-tap "apply.sh --unbind-tap succeeded"
else
  result FAIL unbind-tap "apply.sh --unbind-tap failed"
fi
bound="$(nft list set bridge agentlab_l2 bound_ports 2>/dev/null | grep -c 'tap')"
if (( bound == 1 )); then
  result PASS unbind-tap-effect "one binding left after unbind"
else
  result FAIL unbind-tap-effect "expected one binding left, found ${bound}"
fi

if "$APPLY_SH" --sync-taps >/dev/null 2>&1; then
  result PASS sync-taps "apply.sh --sync-taps succeeded without qm and pct"
else
  result FAIL sync-taps "apply.sh --sync-taps failed"
fi
bound="$(nft list set bridge agentlab_l2 bound_ports 2>/dev/null | grep -c 'tap')"
if (( bound == 0 )); then
  result PASS sync-taps-effect "no binding without a Proxmox data source"
else
  result FAIL sync-taps-effect "unexpected ${bound} bindings after sync"
fi
out="$(probe_http ga 10.77.0.1 8844)"
if [[ "$out" == "200" ]]; then
  result PASS floor-after-sync "guest still reaches 8844 after sync"
else
  result FAIL floor-after-sync "guest cut off after sync (${out})"
fi

kill "${PIDS[@]}" >/dev/null 2>&1 || true
exit $(( fails > 0 ? 1 : 0 ))
INNER

LAB_OUT="$(mktemp)"
LAB_RC=0
if [[ $EUID -eq 0 ]]; then
  unshare -n -m bash "$LAB" "$TEMPLATE" "$APPLY_SH" "$SMOKE_SH" >"$LAB_OUT" 2>&1 || LAB_RC=$?
else
  sudo -n unshare -n -m bash "$LAB" "$TEMPLATE" "$APPLY_SH" "$SMOKE_SH" >"$LAB_OUT" 2>&1 || LAB_RC=$?
fi

while read -r status name detail; do
  if [[ "$status" == "PASS" || "$status" == "FAIL" || "$status" == "SKIP" ]]; then
    log "${status}: ${name} - ${detail}"
  elif [[ -n "$status" ]]; then
    log "lab: ${status} ${name} ${detail}"
  fi
done <"$LAB_OUT"
rm -f "$LAB_OUT"

if (( LAB_RC != 0 )); then
  die "live lab failed (exit ${LAB_RC}); see the FAIL lines above"
fi

if (( FAILURES > 0 )); then
  die "Part A reported ${FAILURES} failures"
fi

log "Summary: PASS"
