#!/usr/bin/env bash
set -euo pipefail

log() {
  printf "[install_host] %s\n" "$*"
}

die() {
  printf "[install_host] ERROR: %s\n" "$*" >&2
  exit 1
}

usage() {
  cat <<'USAGE'
Usage: scripts/install_host.sh [--prefix /usr/local] [--skip-socket-check]

Environment overrides:
  AGENTLABD_SRC       Path to agentlabd binary
  AGENTLAB_SRC        Path to agentlab binary
  UNIT_SRC            Path to agentlabd.service unit file
  PREFIX              Install prefix (default /usr/local)
  BIN_DIR             Override bin dir (default $PREFIX/bin)
  SYSTEMD_DIR         Override systemd unit dir (default /etc/systemd/system)
  SKIP_SOCKET_CHECK   Set to 1 to skip socket permission verification
USAGE
}

if [[ ${1:-} == "-h" || ${1:-} == "--help" ]]; then
  usage
  exit 0
fi

PREFIX="${PREFIX:-/usr/local}"
SKIP_SOCKET_CHECK="${SKIP_SOCKET_CHECK:-0}"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --prefix)
      [[ $# -lt 2 ]] && die "--prefix requires a value"
      PREFIX="$2"
      shift 2
      ;;
    --skip-socket-check)
      SKIP_SOCKET_CHECK=1
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

if ! command -v systemctl >/dev/null 2>&1; then
  die "systemctl not found; systemd is required"
fi

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"

BIN_DIR="${BIN_DIR:-${PREFIX}/bin}"
SYSTEMD_DIR="${SYSTEMD_DIR:-/etc/systemd/system}"
UNIT_SRC="${UNIT_SRC:-${ROOT_DIR}/scripts/systemd/agentlabd.service}"

AGENTLABD_SRC="${AGENTLABD_SRC:-}"
AGENTLAB_SRC="${AGENTLAB_SRC:-}"

if [[ -z "$AGENTLABD_SRC" ]]; then
  for candidate in "${ROOT_DIR}/dist/agentlabd_linux_amd64" "${ROOT_DIR}/bin/agentlabd"; do
    if [[ -x "$candidate" ]]; then
      AGENTLABD_SRC="$candidate"
      break
    fi
  done
fi

if [[ -z "$AGENTLAB_SRC" ]]; then
  for candidate in "${ROOT_DIR}/dist/agentlab_linux_amd64" "${ROOT_DIR}/bin/agentlab"; do
    if [[ -x "$candidate" ]]; then
      AGENTLAB_SRC="$candidate"
      break
    fi
  done
fi

[[ -n "$AGENTLABD_SRC" ]] || die "agentlabd binary not found; run 'make build' or set AGENTLABD_SRC"
[[ -n "$AGENTLAB_SRC" ]] || die "agentlab binary not found; run 'make build' or set AGENTLAB_SRC"
[[ -f "$UNIT_SRC" ]] || die "agentlabd.service not found at $UNIT_SRC"

if ! getent group agentlab >/dev/null 2>&1; then
  log "Creating system group agentlab"
  groupadd --system agentlab
fi

log "Creating directories"
install -d -o root -g agentlab -m 0750 \
  /etc/agentlab \
  /etc/agentlab/profiles \
  /var/lib/agentlab \
  /var/log/agentlab \
  /run/agentlab
install -d -o root -g root -m 0700 \
  /etc/agentlab/secrets \
  /etc/agentlab/keys

log "Installing binaries to $BIN_DIR"
install -d -m 0755 "$BIN_DIR"
install -m 0755 "$AGENTLABD_SRC" "$BIN_DIR/agentlabd"
install -m 0755 "$AGENTLAB_SRC" "$BIN_DIR/agentlab"

log "Installing systemd unit"
install -d -m 0755 "$SYSTEMD_DIR"
install -m 0644 "$UNIT_SRC" "$SYSTEMD_DIR/agentlabd.service"

if [[ ! -f /var/log/agentlab/agentlabd.log ]]; then
  install -m 0640 -o root -g agentlab /dev/null /var/log/agentlab/agentlabd.log
else
  chown root:agentlab /var/log/agentlab/agentlabd.log
  chmod 0640 /var/log/agentlab/agentlabd.log
fi

log "Reloading systemd and enabling agentlabd"
systemctl daemon-reload
systemctl enable --now agentlabd.service

if ! systemctl is-active --quiet agentlabd.service; then
  systemctl --no-pager --full status agentlabd.service || true
  die "agentlabd.service failed to start"
fi

if [[ "$SKIP_SOCKET_CHECK" != "1" ]]; then
  SOCKET_PATH="/run/agentlab/agentlabd.sock"
  if [[ ! -S "$SOCKET_PATH" ]]; then
    die "Socket $SOCKET_PATH not found; set SKIP_SOCKET_CHECK=1 to bypass"
  fi

  socket_owner="$(stat -c '%U' "$SOCKET_PATH")"
  socket_group="$(stat -c '%G' "$SOCKET_PATH")"
  socket_mode="$(stat -c '%a' "$SOCKET_PATH")"
  socket_mode_oct=$((8#$socket_mode))

  if [[ "$socket_group" != "agentlab" ]]; then
    die "Socket group is $socket_group (expected agentlab)"
  fi

  if (( (socket_mode_oct & 0o020) == 0 )); then
    die "Socket $SOCKET_PATH is not group-writable (mode $socket_mode)"
  fi

  log "Socket permissions OK (owner=$socket_owner group=$socket_group mode=$socket_mode)"
else
  log "Skipping socket permission check"
fi

log "Install complete"
log "Add users to the agentlab group for socket access: usermod -aG agentlab <user>"
