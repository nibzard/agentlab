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
                              [--enable-remote-control] [--control-port 8845]
                              [--control-token <token>] [--rotate-control-token]
                              [--tailscale-serve|--no-tailscale-serve]

Environment overrides:
  AGENTLABD_SRC       Path to agentlabd binary
  AGENTLAB_SRC        Path to agentlab binary
  UNIT_SRC            Path to agentlabd.service unit file
  PREFIX              Install prefix (default /usr/local)
  BIN_DIR             Override bin dir (default $PREFIX/bin)
  SYSTEMD_DIR         Override systemd unit dir (default /etc/systemd/system)
  SKIP_SOCKET_CHECK   Set to 1 to skip socket permission verification
  INSTALL_SKILLS      Set to 0 to skip Claude Code skill installation
  CLAUDE_SKILLS_DIR   Override Claude Code skills directory
  ENABLE_REMOTE_CONTROL  Set to 1 to enable remote control config
  CONTROL_PORT           Control plane port (default 8845)
  CONTROL_TOKEN          Control plane bearer token (optional)
  ROTATE_CONTROL_TOKEN   Set to 1 to rotate control token
  TAILSCALE_SERVE        auto|on|off (default auto)
USAGE
}

if [[ ${1:-} == "-h" || ${1:-} == "--help" ]]; then
  usage
  exit 0
fi

PREFIX="${PREFIX:-/usr/local}"
SKIP_SOCKET_CHECK="${SKIP_SOCKET_CHECK:-0}"
INSTALL_SKILLS="${INSTALL_SKILLS:-1}"
CLAUDE_SKILLS_DIR="${CLAUDE_SKILLS_DIR:-}"
ENABLE_REMOTE_CONTROL="${ENABLE_REMOTE_CONTROL:-0}"
CONTROL_PORT="${CONTROL_PORT:-8845}"
CONTROL_TOKEN="${CONTROL_TOKEN:-}"
ROTATE_CONTROL_TOKEN="${ROTATE_CONTROL_TOKEN:-0}"
TAILSCALE_SERVE="${TAILSCALE_SERVE:-auto}"
REMOTE_CONTROL_REQUESTED=0

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
    --enable-remote-control)
      ENABLE_REMOTE_CONTROL=1
      shift
      ;;
    --control-port)
      [[ $# -lt 2 ]] && die "--control-port requires a value"
      CONTROL_PORT="$2"
      REMOTE_CONTROL_REQUESTED=1
      shift 2
      ;;
    --control-token)
      [[ $# -lt 2 ]] && die "--control-token requires a value"
      CONTROL_TOKEN="$2"
      REMOTE_CONTROL_REQUESTED=1
      shift 2
      ;;
    --rotate-control-token)
      ROTATE_CONTROL_TOKEN=1
      REMOTE_CONTROL_REQUESTED=1
      shift
      ;;
    --tailscale-serve)
      TAILSCALE_SERVE="on"
      REMOTE_CONTROL_REQUESTED=1
      shift
      ;;
    --no-tailscale-serve)
      TAILSCALE_SERVE="off"
      REMOTE_CONTROL_REQUESTED=1
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

if [[ "$REMOTE_CONTROL_REQUESTED" == "1" ]]; then
  ENABLE_REMOTE_CONTROL=1
fi

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
SKILL_SRC_DIR="${ROOT_DIR}/skills/agentlab"

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

install_claude_skill() {
  if [[ "$INSTALL_SKILLS" != "1" ]]; then
    log "Skipping Claude Code skill install (INSTALL_SKILLS=$INSTALL_SKILLS)"
    return
  fi

  local skill_src=""
  if [[ -f "${SKILL_SRC_DIR}/SKILL.md" ]]; then
    skill_src="${SKILL_SRC_DIR}/SKILL.md"
  elif [[ -f "${SKILL_SRC_DIR}/skill.md" ]]; then
    skill_src="${SKILL_SRC_DIR}/skill.md"
  else
    die "Claude Code skill not found in ${SKILL_SRC_DIR}"
  fi

  local target_dir="${CLAUDE_SKILLS_DIR}"
  local owner=""
  local group=""
  if [[ -z "$target_dir" ]]; then
    if [[ -n "${SUDO_USER:-}" && "${SUDO_USER}" != "root" ]]; then
      local user_home
      user_home="$(getent passwd "$SUDO_USER" | cut -d: -f6)"
      if [[ -z "$user_home" ]]; then
        user_home="/home/${SUDO_USER}"
      fi
      target_dir="${user_home}/.claude/skills"
      owner="$SUDO_USER"
      group="$(id -gn "$SUDO_USER")"
    else
      target_dir="/root/.claude/skills"
    fi
  fi

  log "Installing Claude Code skill to ${target_dir}/agentlab"
  install -d -m 0755 "${target_dir}/agentlab"
  install -m 0644 "$skill_src" "${target_dir}/agentlab/SKILL.md"
  if [[ -n "$owner" && -n "$group" ]]; then
    chown -R "$owner":"$group" "${target_dir}/agentlab"
  fi
}

CONFIG_PATH="/etc/agentlab/config.yaml"
CONFIG_UPDATED=0
REMOTE_CONTROL_LISTEN=""
REMOTE_CONTROL_TOKEN=""
REMOTE_CONTROL_DNS=""

normalize_tailscale_serve() {
  case "${TAILSCALE_SERVE}" in
    auto|on|off)
      return
      ;;
    1|true|yes)
      TAILSCALE_SERVE="on"
      ;;
    0|false|no)
      TAILSCALE_SERVE="off"
      ;;
    *)
      die "TAILSCALE_SERVE must be auto, on, or off (got: ${TAILSCALE_SERVE})"
      ;;
  esac
}

validate_control_port() {
  if ! [[ "${CONTROL_PORT}" =~ ^[0-9]+$ ]]; then
    die "control port must be numeric (got: ${CONTROL_PORT})"
  fi
  if (( CONTROL_PORT < 1 || CONTROL_PORT > 65535 )); then
    die "control port must be between 1 and 65535 (got: ${CONTROL_PORT})"
  fi
}

generate_control_token() {
  if command -v openssl >/dev/null 2>&1; then
    openssl rand -hex 32
    return
  fi
  if command -v od >/dev/null 2>&1; then
    head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n'
    return
  fi
  head -c 32 /dev/urandom | base64 | tr -d '\n'
}

yaml_quote() {
  local value="$1"
  value="${value//\\/\\\\}"
  value="${value//\"/\\\"}"
  printf '"%s"' "$value"
}

config_get() {
  local key="$1"
  [[ -f "$CONFIG_PATH" ]] || return 0
  awk -v key="$key" '
    $0 ~ "^[[:space:]]*" key ":" {
      sub("^[[:space:]]*" key ":[[:space:]]*", "", $0)
      sub(/[[:space:]]+#.*/, "", $0)
      gsub(/^[\"\047]/, "", $0)
      gsub(/[\"\047]$/, "", $0)
      print $0
      exit
    }' "$CONFIG_PATH"
}

config_upsert() {
  local key="$1"
  local value="$2"
  local line="${key}: $(yaml_quote "$value")"
  local tmp
  tmp="$(mktemp)"
  if [[ -f "$CONFIG_PATH" ]]; then
    awk -v key="$key" -v line="$line" '
      BEGIN {done=0}
      {
        if ($0 ~ "^[[:space:]]*" key ":") {
          print line
          done=1
          next
        }
        print
      }
      END {
        if (!done) print line
      }' "$CONFIG_PATH" > "$tmp"
  else
    printf "%s\n" "$line" > "$tmp"
  fi
  if [[ ! -f "$CONFIG_PATH" ]] || ! cmp -s "$tmp" "$CONFIG_PATH"; then
    mv "$tmp" "$CONFIG_PATH"
    CONFIG_UPDATED=1
  else
    rm -f "$tmp"
  fi
  chmod 0600 "$CONFIG_PATH"
  chown root:root "$CONFIG_PATH"
}

tailscale_running() {
  command -v tailscale >/dev/null 2>&1 || return 1
  tailscale status >/dev/null 2>&1
}

tailscale_dns_name() {
  local json dns host suffix
  json="$(tailscale status --json 2>/dev/null | tr -d '\n')"
  dns="$(printf '%s' "$json" | sed -n 's/.*"DNSName":"\\([^"]*\\)".*/\\1/p')"
  dns="${dns%.}"
  if [[ -z "$dns" ]]; then
    host="$(printf '%s' "$json" | sed -n 's/.*"HostName":"\\([^"]*\\)".*/\\1/p')"
    suffix="$(printf '%s' "$json" | sed -n 's/.*"MagicDNSSuffix":"\\([^"]*\\)".*/\\1/p')"
    suffix="${suffix%.}"
    if [[ -n "$host" && -n "$suffix" ]]; then
      dns="${host}.${suffix}"
    fi
  fi
  if [[ -n "$dns" ]]; then
    printf "%s" "$dns"
    return 0
  fi
  return 1
}

configure_remote_control() {
  if [[ "$ENABLE_REMOTE_CONTROL" != "1" ]]; then
    return
  fi

  normalize_tailscale_serve
  validate_control_port

  local listen_host="127.0.0.1"
  local listen="${listen_host}:${CONTROL_PORT}"
  local existing_token
  existing_token="$(config_get "control_auth_token" || true)"
  local token=""

  if [[ -n "$CONTROL_TOKEN" ]]; then
    token="$CONTROL_TOKEN"
  elif [[ "$ROTATE_CONTROL_TOKEN" == "1" ]]; then
    token="$(generate_control_token)"
  elif [[ -n "$existing_token" ]]; then
    token="$existing_token"
  else
    token="$(generate_control_token)"
  fi

  config_upsert "control_listen" "$listen"
  config_upsert "control_auth_token" "$token"

  REMOTE_CONTROL_LISTEN="$listen"
  REMOTE_CONTROL_TOKEN="$token"

  if [[ "$TAILSCALE_SERVE" == "off" ]]; then
    return
  fi

  if tailscale_running; then
    log "Publishing control plane via tailscale serve (tcp ${CONTROL_PORT})"
    tailscale serve --tcp="${CONTROL_PORT}" "tcp://${listen_host}:${CONTROL_PORT}"
    if REMOTE_CONTROL_DNS="$(tailscale_dns_name)"; then
      log "Tailscale DNS: ${REMOTE_CONTROL_DNS}"
    fi
  else
    if [[ "$TAILSCALE_SERVE" == "on" ]]; then
      die "tailscale serve requested but tailscale is not running"
    fi
    log "Tailscale not running; skipping tailscale serve"
  fi
}

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

configure_remote_control

log "Installing binaries to $BIN_DIR"
install -d -m 0755 "$BIN_DIR"
install -m 0755 "$AGENTLABD_SRC" "$BIN_DIR/agentlabd"
install -m 0755 "$AGENTLAB_SRC" "$BIN_DIR/agentlab"

install_claude_skill

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

if [[ "$CONFIG_UPDATED" == "1" ]]; then
  log "Restarting agentlabd to apply config changes"
  systemctl restart agentlabd.service
fi

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

if [[ "$ENABLE_REMOTE_CONTROL" == "1" && -n "$REMOTE_CONTROL_TOKEN" ]]; then
  local_endpoint="http://${REMOTE_CONTROL_LISTEN}"
  if [[ -n "$REMOTE_CONTROL_DNS" ]]; then
    local_endpoint="http://${REMOTE_CONTROL_DNS}:${CONTROL_PORT}"
  fi
  log "Remote control endpoint: ${local_endpoint}"
  log "Connect with: agentlab connect --endpoint ${local_endpoint} --token ${REMOTE_CONTROL_TOKEN}"
fi

log "Install complete"
log "Add users to the agentlab group for socket access: usermod -aG agentlab <user>"
