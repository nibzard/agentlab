package daemon

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestRunnerReportRejectsNonAgentSource verifies that requests from outside
// the agent subnet are rejected.
func TestRunnerReportRejectsNonAgentSource(t *testing.T) {
	agentSubnet := mustParseCIDR(t, "10.77.0.0/16")
	store := newTestStore(t)
	api := NewRunnerAPI(nil, store, agentSubnet)

	payload := `{"job_id":"job-1","vmid":123,"status":"RUNNING"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/runner/report", strings.NewReader(payload))
	req.RemoteAddr = "192.168.1.10:4321"
	resp := httptest.NewRecorder()

	api.handleRunnerReport(resp, req)
	if resp.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", resp.Code)
	}
}

// TestRunnerReportRequiresSandboxSecret verifies that the runner API requires
// the X-AgentLab-Sandbox-Secret header (T39).
func TestRunnerReportRequiresSandboxSecret(t *testing.T) {
	store := newTestStore(t)
	agentSubnet := mustParseCIDR(t, "10.77.0.0/16")
	seedSecretTestSandbox(t, store, 7001, "secret-holder", "10.77.9.10")
	secret := seedSandboxSecret(t, store, 7001)
	api := NewRunnerAPI(nil, store, agentSubnet)

	cases := []struct {
		name   string
		secret string
		want   int
	}{
		// A correct secret clears the gate. The report then reaches the
		// nil orchestrator, which answers 503. Getting past the secret
		// check is what this test asserts.
		{"correct secret", secret, http.StatusServiceUnavailable},
		{"wrong secret", "wrong-secret-value", http.StatusForbidden},
		{"missing secret", "", http.StatusForbidden},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			payload := `{"job_id":"job-1","vmid":7001,"status":"RUNNING"}`
			req := httptest.NewRequest(http.MethodPost, "/v1/runner/report", strings.NewReader(payload))
			req.RemoteAddr = "10.77.9.10:4321"
			if tc.secret != "" {
				req.Header.Set(sandboxSecretHeader, tc.secret)
			}
			resp := httptest.NewRecorder()

			api.handleRunnerReport(resp, req)
			if resp.Code != tc.want {
				t.Fatalf("expected %d, got %d", tc.want, resp.Code)
			}
		})
	}
}
