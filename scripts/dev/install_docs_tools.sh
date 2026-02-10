#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
TOOLS_DIR="${ROOT_DIR}/bin/tools"

LYCHEE_VERSION="0.22.0"
TYPOS_VERSION="1.43.4"

mkdir -p "${TOOLS_DIR}"

if ! command -v curl >/dev/null 2>&1; then
  echo "curl is required to download docs tools." >&2
  exit 1
fi

os="$(uname -s)"
arch="$(uname -m)"

case "$os" in
  Linux)
    os="linux"
    ;;
  Darwin)
    os="darwin"
    ;;
  *)
    echo "Unsupported OS: ${os}" >&2
    exit 1
    ;;
esac

case "$arch" in
  x86_64|amd64)
    arch="x86_64"
    ;;
  arm64|aarch64)
    arch="aarch64"
    ;;
  *)
    echo "Unsupported architecture: ${arch}" >&2
    exit 1
    ;;
esac

fetch_tar() {
  local url="$1"
  local bin_name="$2"
  local tmpdir

  tmpdir="$(mktemp -d)"
  curl -sSfL "$url" -o "${tmpdir}/archive.tar.gz"
  tar -xzf "${tmpdir}/archive.tar.gz" -C "${tmpdir}"
  install -m 0755 "${tmpdir}/${bin_name}" "${TOOLS_DIR}/${bin_name}"
  rm -rf "${tmpdir}"
}

if [ ! -x "${TOOLS_DIR}/lychee" ]; then
  lychee_asset=""
  if [ "${os}" = "linux" ]; then
    lychee_asset="lychee-${arch}-unknown-linux-gnu.tar.gz"
  elif [ "${os}" = "darwin" ]; then
    if [ "${arch}" = "aarch64" ]; then
      lychee_asset="lychee-arm64-macos.tar.gz"
    else
      echo "lychee does not ship a darwin x86_64 tarball; install via cargo or brew." >&2
      exit 1
    fi
  fi

  lychee_url="https://github.com/lycheeverse/lychee/releases/download/lychee-v${LYCHEE_VERSION}/${lychee_asset}"
  echo "Installing lychee ${LYCHEE_VERSION}..."
  fetch_tar "${lychee_url}" "lychee"
fi

if [ ! -x "${TOOLS_DIR}/typos" ]; then
  typos_asset=""
  if [ "${os}" = "linux" ]; then
    typos_asset="typos-v${TYPOS_VERSION}-${arch}-unknown-linux-musl.tar.gz"
  elif [ "${os}" = "darwin" ]; then
    typos_asset="typos-v${TYPOS_VERSION}-${arch}-apple-darwin.tar.gz"
  fi

  typos_url="https://github.com/crate-ci/typos/releases/download/v${TYPOS_VERSION}/${typos_asset}"
  echo "Installing typos ${TYPOS_VERSION}..."
  fetch_tar "${typos_url}" "typos"
fi

echo "Docs tools installed in ${TOOLS_DIR}"
