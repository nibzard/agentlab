package daemon

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRunnerReportRejectsNonAgentSource(t *testing.T) {
	agentSubnet := mustParseCIDR(t, "10.77.0.0/16")
	api := NewRunnerAPI(nil, agentSubnet)

	payload := `{"job_id":"job-1","vmid":123,"status":"RUNNING"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/runner/report", strings.NewReader(payload))
	req.RemoteAddr = "192.168.1.10:4321"
	resp := httptest.NewRecorder()

	api.handleRunnerReport(resp, req)
	if resp.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", resp.Code)
	}
}
