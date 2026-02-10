//go:build e2e
// +build e2e

package tests

import (
	"os"
	"testing"
)

func TestE2EProxmoxPlaceholder(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}
	if os.Getenv("AGENTLAB_E2E") == "" {
		t.Skip("set AGENTLAB_E2E=1 and Proxmox credentials to run e2e tests")
	}
	t.Skip("e2e tests require a real Proxmox environment")
}
