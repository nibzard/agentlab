#!/usr/bin/env bash
# Exercise the real guest helper on Linux, including from an unprivileged CI user.
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT"
fixture="$(mktemp -d)"
trap 'rm -rf "$fixture"' EXIT
chmod 755 "$fixture"
case "$(docker info --format '{{.Architecture}}')" in
  aarch64|arm64) arch=arm64 ;;
  x86_64|amd64) arch=amd64 ;;
  *) echo 'Unsupported Docker architecture' >&2; exit 1 ;;
esac
image="${AGENTLAB_GUEST_TEST_IMAGE:-agentlab-guest-recovery-tests}"
docker build --tag "$image" --file scripts/tests/guest_bootstrap_recovery.Dockerfile "$fixture"
GOOS=linux GOARCH="$arch" CGO_ENABLED=0 go test -c -o "$fixture/daemon.test" ./internal/daemon
chmod 755 "$fixture/daemon.test"
docker run --rm --network none --user 65534:65534 \
  -v "$fixture:/fixture:ro" -v "$ROOT:/repo:ro" -e AGENTLAB_TEST_REPO=/repo \
  "$image" /fixture/daemon.test \
  -test.run '^(TestGuestRecovery|TestGuestTLS|TestMetadataIdentityReusedAddressAndAmbiguity)' -test.v
