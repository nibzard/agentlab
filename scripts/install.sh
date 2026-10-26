#!/usr/bin/env bash
# agentlab one-liner installer
#
# Usage:
#   curl -fsSL https://agentlab.dev/install.sh | bash
#   curl -fsSL https://agentlab.dev/install.sh | bash -s -- --prefix /usr/local
#   wget -qO- https://agentlab.dev/install.sh | bash
#
# Or from GitHub releases:
#   curl -fsSL https://raw.githubusercontent.com/nibzard/agentlab/main/scripts/install.sh | bash
set -euo pipefail

VERSION="${AGENTLAB_VERSION:-latest}"
PREFIX="${AGENTLAB_PREFIX:-/usr/local}"
BIN_DIR="${AGENTLAB_BIN_DIR:-${PREFIX}/bin}"
REPO="nibzard/agentlab"
GITHUB_BASE="https://github.com/${REPO}"
install_tmpdir=""
trap 'rm -rf "${install_tmpdir:-}"' EXIT

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
NC='\033[0m'

log()  { printf "${GREEN}[agentlab]${NC} %s\n" "$*"; }
warn() { printf "${YELLOW}[agentlab]${NC} %s\n" "$*" >&2; }
die()  { printf "${RED}[agentlab]${NC} %s\n" "$*" >&2; exit 1; }

usage() {
  cat <<'USAGE'
Usage: install.sh [options]

Options:
  --prefix DIR      Install prefix (default /usr/local)
  --version TAG     Version to install (default latest)
  -h, --help        Show this help

Environment variables:
  AGENTLAB_VERSION   Version tag to install (default latest)
  AGENTLAB_PREFIX    Install prefix (default /usr/local)
  AGENTLAB_BIN_DIR   Override bin directory (default $PREFIX/bin)
USAGE
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --prefix)
      [[ $# -lt 2 ]] && die "--prefix requires a value"
      PREFIX="$2"
      BIN_DIR="${AGENTLAB_BIN_DIR:-${PREFIX}/bin}"
      shift 2
      ;;
    --version)
      [[ $# -lt 2 ]] && die "--version requires a value"
      VERSION="$2"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      die "unknown option: $1"
      ;;
  esac
done

# Detect OS and architecture
detect_platform() {
  local os arch

  os="$(uname -s | tr '[:upper:]' '[:lower:]')"
  case "$os" in
    linux*)  os="linux" ;;
    darwin*) os="darwin" ;;
    *)       die "unsupported OS: $(uname -s)" ;;
  esac

  arch="$(uname -m)"
  case "$arch" in
    x86_64|amd64) arch="amd64" ;;
    aarch64|arm64) arch="arm64" ;;
    *) die "unsupported architecture: $arch" ;;
  esac

  echo "${os}-${arch}"
}

# Resolve the latest version tag from GitHub
resolve_version() {
  local tag="$1"
  if [[ "$tag" == "latest" ]]; then
    local metadata
    metadata="$(mktemp)"
    download "https://api.github.com/repos/${REPO}/releases/latest" "$metadata" || { rm -f "$metadata"; die "could not download release metadata"; }
    tag=$(cat "$metadata" | grep '"tag_name"' | head -1 | sed -E 's/.*"tag_name":[[:space:]]*"([^"]+)".*/\1/') || true
    rm -f "$metadata"
    if [[ -z "$tag" ]]; then
      die "could not determine latest version. Set AGENTLAB_VERSION explicitly."
    fi
  fi
  echo "$tag"
}

# Download a file, trying curl then wget
download() {
  local url="$1"
  local dest="$2"

  if command -v curl &>/dev/null; then
    curl -fsSL "$url" -o "$dest"
  elif command -v wget &>/dev/null; then
    wget -q "$url" -O "$dest"
  else
    die "neither curl nor wget found. Install one to proceed."
  fi
}

main() {
  local platform version url tmpdir archive ext

  platform="$(detect_platform)"
  version="$(resolve_version "$VERSION")"

  log "installing agentlab ${version} for ${platform}"

  # Determine archive extension
  ext="tar.gz"

  url="${GITHUB_BASE}/releases/download/${version}/agentlab_${version}_${platform}.${ext}"

  tmpdir="$(mktemp -d)"
  install_tmpdir="$tmpdir"

  archive="${tmpdir}/agentlab.${ext}"

  log "downloading ${url}"
  download "$url" "$archive"

  download "${GITHUB_BASE}/releases/download/${version}/checksums.txt" "${tmpdir}/checksums.txt"
  local archive_name expected actual
  archive_name="agentlab_${version}_${platform}.${ext}"
  expected="$(awk -v name="$archive_name" '$2 == name {print $1}' "${tmpdir}/checksums.txt")"
  [[ "${#expected}" -eq 64 ]] || die "missing or invalid archive checksum"
  if command -v sha256sum &>/dev/null; then
    actual="$(sha256sum "$archive" | awk '{print $1}')"
  elif command -v shasum &>/dev/null; then
    actual="$(shasum -a 256 "$archive" | awk '{print $1}')"
  else
    die "sha256sum or shasum is required to verify the download"
  fi
  [[ "$actual" == "$expected" ]] || die "archive checksum mismatch"

  # Extract
  log "extracting..."
  tar -xzf "$archive" -C "$tmpdir"

  # Find the binary — it may be at the root or in a subdirectory
  local bin
  bin="$(find "$tmpdir" -name agentlab -type f 2>/dev/null | head -1)"
  [[ -z "$bin" ]] && die "agentlab binary not found in archive"

  # Ensure target directory exists
  if [[ ! -d "$BIN_DIR" ]]; then
    log "creating ${BIN_DIR}"
    mkdir -p "$BIN_DIR"
  fi

  # Install
  local target="${BIN_DIR}/agentlab"
  install -m 0755 "$bin" "$target"
  log "installed agentlab to ${target}"

  # Install shell completions
  if "$target" completion bash &>/dev/null; then
    local completion_dir="${PREFIX}/share/bash-completion/completions"
    mkdir -p "$completion_dir" 2>/dev/null || true
    "$target" completion bash > "${completion_dir}/agentlab" 2>/dev/null || true
  fi

  # Verify
  local installed_version
  installed_version="$("$target" --version 2>&1 | head -1)" || true
  log "installed: ${installed_version}"

  # Add to PATH if not already there
  if [[ ":${PATH}:" != *":${BIN_DIR}:"* ]]; then
    warn "add ${BIN_DIR} to your PATH:"
    case "${SHELL##*/}" in
      zsh) warn "  Add export PATH=\"$BIN_DIR:\$PATH\" to ~/.zshrc" ;;
      fish) warn "  Run: fish_add_path '$BIN_DIR'" ;;
      *) warn "  Add export PATH=\"$BIN_DIR:\$PATH\" to your shell startup file" ;;
    esac
  fi

  log "done! run 'agentlab --help' to get started."
}

main
