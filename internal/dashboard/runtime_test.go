package dashboard

import (
	"os/exec"
	"testing"
)

func TestDashboardJavaScriptBehavior(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node.js is required for browser behavior fixtures")
	}
	if output, err := exec.Command(node, "--test", "testdata/app.test.cjs").CombinedOutput(); err != nil {
		t.Fatalf("dashboard behavior: %v\n%s", err, output)
	}
}
