#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

log() {
  printf "[create_template] %s\n" "$*"
}

die() {
  printf "[create_template] ERROR: %s\n" "$*" >&2
  exit 1
}

usage() {
  cat <<'USAGE'
Usage: scripts/create_template.sh [options]

Options:
  --vmid <id>              Template VMID (default: 9000)
  --name <name>            Template name (default: agentlab-ubuntu-2404)
  --storage <storage>      Proxmox storage for root disk (default: local-zfs)
  --cloudinit-storage <s>  Storage for cloud-init drive (default: same as --storage)
  --bridge <bridge>        Network bridge (default: vmbr1)
  --memory <mb>            Memory in MB (default: 4096)
  --cores <n>              CPU cores (default: 2)
  --disk-size <size>       Resize root disk to size (default: 40G, set to 0 to skip)
  --image-url <url>        Cloud image URL
  --image-file <path>      Use a local image file instead of downloading
  --cache-dir <dir>        Cache directory for images (default: /var/lib/agentlab/images)
  --packages <list>        Comma/space separated package list
  --skip-customize         Skip virt-customize package/user prep
  --skip-agent-tools       Skip installing agent CLIs + wrapper
  --claude-version <v>     Claude Code CLI version (default: 1.0.100)
  --codex-version <v>      OpenAI Codex CLI version (default: 0.28.0)
  --opencode-version <v>   OpenCode CLI version (default: 0.6.4)
  --refresh                Re-download the cloud image
  -h, --help               Show this help

Notes:
  - Requires Proxmox qm on the host.
  - For package install + user setup, install libguestfs-tools (virt-customize).
USAGE
}

VMID=9000
NAME="agentlab-ubuntu-2404"
STORAGE="local-zfs"
CLOUDINIT_STORAGE=""
BRIDGE="vmbr1"
MEMORY=4096
CORES=2
DISK_SIZE="40G"
IMAGE_URL="https://cloud-images.ubuntu.com/noble/current/noble-server-cloudimg-amd64.img"
IMAGE_FILE=""
CACHE_DIR="/var/lib/agentlab/images"
PACKAGES="qemu-guest-agent,git,curl,ca-certificates,jq"
CLAUDE_CODE_VERSION="1.0.100"
CODEX_VERSION="0.28.0"
OPENCODE_VERSION="0.6.4"
SKIP_CUSTOMIZE=0
SKIP_AGENT_TOOLS=0
REFRESH=0

AGENTLAB_AGENT_WRAPPER="${SCRIPT_DIR}/guest/agentlab-agent"
AGENT_TOOLS_ENV=""

ensure_package() {
  local pkg="$1"
  local normalized
  normalized=",${PACKAGES// /,},"
  if [[ "$normalized" != *",$pkg,"* ]]; then
    PACKAGES="${PACKAGES},${pkg}"
  fi
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --vmid)
      [[ $# -lt 2 ]] && die "--vmid requires a value"
      VMID="$2"
      shift 2
      ;;
    --name)
      [[ $# -lt 2 ]] && die "--name requires a value"
      NAME="$2"
      shift 2
      ;;
    --storage)
      [[ $# -lt 2 ]] && die "--storage requires a value"
      STORAGE="$2"
      shift 2
      ;;
    --cloudinit-storage)
      [[ $# -lt 2 ]] && die "--cloudinit-storage requires a value"
      CLOUDINIT_STORAGE="$2"
      shift 2
      ;;
    --bridge)
      [[ $# -lt 2 ]] && die "--bridge requires a value"
      BRIDGE="$2"
      shift 2
      ;;
    --memory)
      [[ $# -lt 2 ]] && die "--memory requires a value"
      MEMORY="$2"
      shift 2
      ;;
    --cores)
      [[ $# -lt 2 ]] && die "--cores requires a value"
      CORES="$2"
      shift 2
      ;;
    --disk-size)
      [[ $# -lt 2 ]] && die "--disk-size requires a value"
      DISK_SIZE="$2"
      shift 2
      ;;
    --image-url)
      [[ $# -lt 2 ]] && die "--image-url requires a value"
      IMAGE_URL="$2"
      shift 2
      ;;
    --image-file)
      [[ $# -lt 2 ]] && die "--image-file requires a value"
      IMAGE_FILE="$2"
      shift 2
      ;;
    --cache-dir)
      [[ $# -lt 2 ]] && die "--cache-dir requires a value"
      CACHE_DIR="$2"
      shift 2
      ;;
    --packages)
      [[ $# -lt 2 ]] && die "--packages requires a value"
      PACKAGES="$2"
      shift 2
      ;;
    --skip-customize)
      SKIP_CUSTOMIZE=1
      shift
      ;;
    --skip-agent-tools)
      SKIP_AGENT_TOOLS=1
      shift
      ;;
    --claude-version)
      [[ $# -lt 2 ]] && die "--claude-version requires a value"
      CLAUDE_CODE_VERSION="$2"
      shift 2
      ;;
    --codex-version)
      [[ $# -lt 2 ]] && die "--codex-version requires a value"
      CODEX_VERSION="$2"
      shift 2
      ;;
    --opencode-version)
      [[ $# -lt 2 ]] && die "--opencode-version requires a value"
      OPENCODE_VERSION="$2"
      shift 2
      ;;
    --refresh)
      REFRESH=1
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

if ! command -v qm >/dev/null 2>&1; then
  die "qm not found; run this on a Proxmox host"
fi

if ! [[ "$VMID" =~ ^[0-9]+$ ]]; then
  die "--vmid must be numeric"
fi

if [[ -z "$CLOUDINIT_STORAGE" ]]; then
  CLOUDINIT_STORAGE="$STORAGE"
fi

if qm status "$VMID" >/dev/null 2>&1; then
  die "VMID $VMID already exists"
fi

if [[ "$SKIP_CUSTOMIZE" == "1" && "$SKIP_AGENT_TOOLS" == "0" ]]; then
  log "Skipping agent tool install because --skip-customize was set"
  SKIP_AGENT_TOOLS=1
fi

if [[ "$SKIP_AGENT_TOOLS" == "0" ]]; then
  [[ -f "$AGENTLAB_AGENT_WRAPPER" ]] || die "agentlab-agent wrapper not found at $AGENTLAB_AGENT_WRAPPER"
  ensure_package "nodejs"
  ensure_package "npm"
  ensure_package "ripgrep"
  AGENT_TOOLS_ENV="$(mktemp)"
  cat > "$AGENT_TOOLS_ENV" <<EOF
CLAUDE_CODE_VERSION="${CLAUDE_CODE_VERSION}"
CODEX_VERSION="${CODEX_VERSION}"
OPENCODE_VERSION="${OPENCODE_VERSION}"
BUILT_AT="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
EOF
  trap 'rm -f "$AGENT_TOOLS_ENV"' EXIT
fi

download_image() {
  local url="$1"
  local dest="$2"
  if command -v curl >/dev/null 2>&1; then
    curl -fL -o "$dest" "$url"
  elif command -v wget >/dev/null 2>&1; then
    wget -O "$dest" "$url"
  else
    die "Neither curl nor wget is available for download"
  fi
}

if [[ -z "$IMAGE_FILE" ]]; then
  install -d -m 0755 "$CACHE_DIR"
  IMAGE_FILE="${CACHE_DIR}/$(basename "$IMAGE_URL")"
  if [[ "$REFRESH" == "1" || ! -f "$IMAGE_FILE" ]]; then
    log "Downloading cloud image to $IMAGE_FILE"
    download_image "$IMAGE_URL" "$IMAGE_FILE"
  else
    log "Using cached image at $IMAGE_FILE"
  fi
fi

[[ -f "$IMAGE_FILE" ]] || die "Image file not found: $IMAGE_FILE"

WORK_IMAGE="${CACHE_DIR}/agentlab-${VMID}-$(basename "$IMAGE_FILE")"
rm -f "$WORK_IMAGE"
if cp --reflink=auto "$IMAGE_FILE" "$WORK_IMAGE" 2>/dev/null; then
  log "Copied image with reflink to $WORK_IMAGE"
else
  cp "$IMAGE_FILE" "$WORK_IMAGE"
  log "Copied image to $WORK_IMAGE"
fi

if [[ "$SKIP_CUSTOMIZE" == "0" ]]; then
  if ! command -v virt-customize >/dev/null 2>&1; then
    die "virt-customize not found; install libguestfs-tools or use --skip-customize"
  fi
  pkg_list="${PACKAGES// /,}"
  log "Customizing image with packages: $pkg_list"
  virt_args=(
    -a "$WORK_IMAGE"
    --install "$pkg_list"
    --run-command "id -u agent >/dev/null 2>&1 || useradd -m -s /bin/bash agent"
    --run-command "id -u agent >/dev/null 2>&1 && usermod -aG sudo agent"
    --run-command "id -u agent >/dev/null 2>&1 && passwd -l agent"
    --run-command "install -d -m 0755 /etc/sudoers.d"
    --run-command "printf 'agent ALL=(ALL) NOPASSWD:ALL\\n' > /etc/sudoers.d/90-agentlab"
    --run-command "chmod 0440 /etc/sudoers.d/90-agentlab"
    --run-command "install -d -m 0755 /etc/ssh/sshd_config.d"
    --run-command "printf 'PasswordAuthentication no\\nPermitRootLogin prohibit-password\\n' > /etc/ssh/sshd_config.d/99-agentlab.conf"
    --run-command "systemctl enable qemu-guest-agent"
  )

  if [[ "$SKIP_AGENT_TOOLS" == "0" ]]; then
    log "Installing agent CLIs (claude ${CLAUDE_CODE_VERSION}, codex ${CODEX_VERSION}, opencode ${OPENCODE_VERSION})"
    virt_args+=(
      --run-command "npm config set fund false"
      --run-command "npm config set update-notifier false"
      --run-command "npm config set unsafe-perm true"
      --run-command "npm install -g @anthropic-ai/claude-code@${CLAUDE_CODE_VERSION}"
      --run-command "npm install -g @openai/codex@${CODEX_VERSION}"
      --run-command "npm install -g opencode-ai@${OPENCODE_VERSION}"
      --run-command "install -d -m 0755 /etc/agentlab"
      --upload "${AGENTLAB_AGENT_WRAPPER}:/usr/local/bin/agentlab-agent"
      --upload "${AGENT_TOOLS_ENV}:/etc/agentlab/agent-tools.env"
      --run-command "chmod 0755 /usr/local/bin/agentlab-agent"
      --run-command "chmod 0644 /etc/agentlab/agent-tools.env"
      --run-command "command -v claude codex opencode >/dev/null"
    )
  else
    log "Skipping agent CLI install"
  fi

  virt-customize "${virt_args[@]}"
else
  log "Skipping image customization; ensure qemu-guest-agent and base packages are installed"
fi

log "Creating VM $VMID ($NAME)"
qm create "$VMID" \
  --name "$NAME" \
  --memory "$MEMORY" \
  --cores "$CORES" \
  --net0 "virtio,bridge=${BRIDGE}" \
  --ostype l26

log "Importing disk to storage $STORAGE"
import_output=""
if ! import_output=$(qm importdisk "$VMID" "$WORK_IMAGE" "$STORAGE" 2>&1); then
  printf "%s\n" "$import_output" >&2
  die "qm importdisk failed"
fi
printf "%s\n" "$import_output"

disk_ref="$(printf "%s\n" "$import_output" | sed -n "s/.*imported disk as '\\([^']*\\)'.*/\\1/p")"
if [[ -z "$disk_ref" ]]; then
  disk_ref="${STORAGE}:vm-${VMID}-disk-0"
fi

qm set "$VMID" --scsihw virtio-scsi-pci --scsi0 "$disk_ref"
qm set "$VMID" --ide2 "${CLOUDINIT_STORAGE}:cloudinit"
qm set "$VMID" --boot order=scsi0
qm set "$VMID" --serial0 socket --vga serial0
qm set "$VMID" --agent enabled=1
qm set "$VMID" --ipconfig0 ip=dhcp

if [[ -n "$DISK_SIZE" && "$DISK_SIZE" != "0" ]]; then
  log "Resizing disk to $DISK_SIZE"
  qm resize "$VMID" scsi0 "$DISK_SIZE"
fi

log "Converting VM $VMID to template"
qm template "$VMID"

log "Template created: VMID $VMID ($NAME)"
log "Cleanup: remove $WORK_IMAGE if you no longer need the working copy"
