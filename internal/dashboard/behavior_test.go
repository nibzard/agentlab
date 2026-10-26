package dashboard

import (
	"os/exec"
	"testing"
)

func TestDashboardBehavior(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node.js unavailable; run node --test internal/dashboard/app_behavior_test.cjs")
	}
	cmd := exec.Command(node, "--test", "app_behavior_test.cjs")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("dashboard behavior tests: %v\n%s", err, out)
	}
}
