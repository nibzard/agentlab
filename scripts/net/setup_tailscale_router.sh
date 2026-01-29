#!/usr/bin/env bash
set -euo pipefail

log() {
  printf "[agentlab-tailscale] %s\n" "$*"
}

die() {
  printf "[agentlab-tailscale] ERROR: %s\n" "$*" >&2
  exit 1
}

usage() {
  cat <<'USAGE'
Usage: scripts/net/setup_tailscale_router.sh [--subnet 10.77.0.0/16] [--hostname name] [--authkey key] [--apply]

Options:
  --subnet    Subnet to advertise (default: 10.77.0.0/16)
  --hostname  Optional hostname for tailscale up (only used with --authkey)
  --authkey   Auth key for unattended tailscale up if not logged in
  --apply     Run tailscale commands instead of printing them
USAGE
}

SUBNET="10.77.0.0/16"
HOSTNAME=""
AUTHKEY=""
APPLY=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --subnet)
      [[ $# -lt 2 ]] && die "--subnet requires a value"
      SUBNET="$2"
      shift 2
      ;;
    --hostname)
      [[ $# -lt 2 ]] && die "--hostname requires a value"
      HOSTNAME="$2"
      shift 2
      ;;
    --authkey)
      [[ $# -lt 2 ]] && die "--authkey requires a value"
      AUTHKEY="$2"
      shift 2
      ;;
    --apply)
      APPLY=1
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

if ! command -v tailscale >/dev/null 2>&1; then
  die "tailscale command not found"
fi

if tailscale status >/dev/null 2>&1; then
  if tailscale set --help >/dev/null 2>&1; then
    if [[ "$APPLY" == "1" ]]; then
      log "Advertising subnet route $SUBNET via tailscale set"
      tailscale set --advertise-routes="$SUBNET"
    else
      log "Run: tailscale set --advertise-routes=\"$SUBNET\""
    fi
  else
    if [[ "$APPLY" == "1" ]]; then
      die "tailscale set not available; run tailscale up with --advertise-routes"
    else
      log "Run: tailscale up --advertise-routes=\"$SUBNET\" (include existing flags)"
    fi
  fi
else
  if [[ -z "$AUTHKEY" ]]; then
    die "Tailscale not running; log in with 'tailscale up' or provide --authkey"
  fi

  if [[ "$APPLY" == "1" ]]; then
    cmd=(tailscale up --authkey "$AUTHKEY" --advertise-routes="$SUBNET")
    if [[ -n "$HOSTNAME" ]]; then
      cmd+=(--hostname "$HOSTNAME")
    fi
    log "Bringing up tailscale with advertised route $SUBNET"
    "${cmd[@]}"
  else
    if [[ -n "$HOSTNAME" ]]; then
      log "Run: tailscale up --authkey <redacted> --advertise-routes=\"$SUBNET\" --hostname \"$HOSTNAME\""
    else
      log "Run: tailscale up --authkey <redacted> --advertise-routes=\"$SUBNET\""
    fi
  fi
fi

log "Approve the subnet route in the Tailscale admin console if required"
log "Done"
