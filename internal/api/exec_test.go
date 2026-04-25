package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBuildExecArgs(t *testing.T) {
	tests := []struct {
		name       string
		socketPath string
		cmd        string
		want       []string
	}{
		{
			name:       "simple command",
			socketPath: "/run/agentlab/agentlabd.sock",
			cmd:        "sandbox list --json",
			want:       []string{"--socket", "/run/agentlab/agentlabd.sock", "sandbox", "list", "--json"},
		},
		{
			name:       "single word command",
			socketPath: "/tmp/test.sock",
			cmd:        "status",
			want:       []string{"--socket", "/tmp/test.sock", "status"},
		},
		{
			name:       "command with equals flag",
			socketPath: "/run/agentlab/agentlabd.sock",
			cmd:        "sandbox new --profile=dev",
			want:       []string{"--socket", "/run/agentlab/agentlabd.sock", "sandbox", "new", "--profile=dev"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := buildExecArgs(tc.socketPath, tc.cmd)
			if len(got) != len(tc.want) {
				t.Fatalf("buildExecArgs(%q, %q) = %v, want %v", tc.socketPath, tc.cmd, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("buildExecArgs[%d] = %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestIsValidCLICommand(t *testing.T) {
	tests := []struct {
		cmd  string
		want bool
	}{
		{"sandbox", true},
		{"status", true},
		{"new", true},
		{"ls", true},
		{"ssh", true},
		{"rm", true},
		{"job", true},
		{"workspace", true},
		{"session", true},
		{"profile", true},
		{"version", true},
		{"help", true},
		{"--json", true},
		{"-v", true},
		{"bogus", false},
		{"unknowncmd", false},
	}

	for _, tc := range tests {
		t.Run(tc.cmd, func(t *testing.T) {
			got := isValidCLICommand(tc.cmd)
			if got != tc.want {
				t.Errorf("isValidCLICommand(%q) = %v, want %v", tc.cmd, got, tc.want)
			}
		})
	}
}

func TestExecAPI_HandleExec_MethodReject(t *testing.T) {
	api := NewExecAPI("echo", "/tmp/test.sock", nil)
	mux := http.NewServeMux()
	api.Register(mux)

	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		t.Run(method, func(t *testing.T) {
			req := httptest.NewRequest(method, "/v1/exec", nil)
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)

			if rec.Code != http.StatusMethodNotAllowed {
				t.Errorf("method %s: got status %d, want %d", method, rec.Code, http.StatusMethodNotAllowed)
			}
		})
	}
}

func TestExecAPI_HandleExec_MissingCommand(t *testing.T) {
	api := NewExecAPI("echo", "/tmp/test.sock", nil)
	mux := http.NewServeMux()
	api.Register(mux)

	body := `{"timeout": 30}`
	req := httptest.NewRequest(http.MethodPost, "/v1/exec", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("got status %d, want %d; body: %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}

	var resp map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["error"] != "command is required" {
		t.Errorf("error = %q, want %q", resp["error"], "command is required")
	}
}

func TestExecAPI_HandleExec_EmptyBody(t *testing.T) {
	api := NewExecAPI("echo", "/tmp/test.sock", nil)
	mux := http.NewServeMux()
	api.Register(mux)

	req := httptest.NewRequest(http.MethodPost, "/v1/exec", nil)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("got status %d, want %d; body: %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

func TestExecAPI_HandleExec_EchoCommand(t *testing.T) {
	// Use "echo" as a stand-in for the agentlab binary to verify the
	// exec pipeline works end-to-end. echo will succeed with exit 0.
	api := NewExecAPI("echo", "/tmp/test.sock", nil)
	mux := http.NewServeMux()
	api.Register(mux)

	body := `{"command": "hello world"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/exec", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var resp ExecResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.ExitCode != 0 {
		t.Errorf("exit_code = %d, want 0", resp.ExitCode)
	}
	// echo should output the remaining args
	if !strings.Contains(resp.Stdout, "hello") {
		t.Errorf("stdout = %q, should contain command output", resp.Stdout)
	}
}

func TestExecAPI_HandleDryRun(t *testing.T) {
	api := NewExecAPI("echo", "/tmp/test.sock", nil)
	mux := http.NewServeMux()
	api.Register(mux)

	tests := []struct {
		name    string
		body    string
		wantOK  bool
		wantErr bool
	}{
		{
			name:   "valid sandbox command",
			body:   `{"command": "sandbox list --json"}`,
			wantOK: true,
		},
		{
			name:   "valid status command",
			body:   `{"command": "status"}`,
			wantOK: true,
		},
		{
			name:   "valid global flag",
			body:   `{"command": "--json status"}`,
			wantOK: true,
		},
		{
			name:    "unknown command",
			body:    `{"command": "foobarbaz"}`,
			wantOK:  false,
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/v1/exec/dry-run", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("got status %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
			}

			var resp ExecDryRunResponse
			if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if resp.OK != tc.wantOK {
				t.Errorf("ok = %v, want %v", resp.OK, tc.wantOK)
			}
			if tc.wantErr && len(resp.Errors) == 0 {
				t.Error("expected errors, got none")
			}
			if !tc.wantErr && len(resp.Errors) > 0 {
				t.Errorf("expected no errors, got %v", resp.Errors)
			}
		})
	}
}

func TestExecAPI_HandleDryRun_MethodReject(t *testing.T) {
	api := NewExecAPI("echo", "/tmp/test.sock", nil)
	mux := http.NewServeMux()
	api.Register(mux)

	req := httptest.NewRequest(http.MethodGet, "/v1/exec/dry-run", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("got status %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestExecAPI_HandleDryRun_MissingCommand(t *testing.T) {
	api := NewExecAPI("echo", "/tmp/test.sock", nil)
	mux := http.NewServeMux()
	api.Register(mux)

	body := `{}`
	req := httptest.NewRequest(http.MethodPost, "/v1/exec/dry-run", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("got status %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestExecAPI_HandleExec_CustomTimeout(t *testing.T) {
	api := NewExecAPI("echo", "/tmp/test.sock", nil)
	mux := http.NewServeMux()
	api.Register(mux)

	// Verify that custom timeout is accepted and doesn't cause errors.
	body := `{"command": "status", "timeout": 10}`
	req := httptest.NewRequest(http.MethodPost, "/v1/exec", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
}

func TestExecAPI_HandleExec_FailingCommand(t *testing.T) {
	// Use "false" which always exits with code 1.
	api := NewExecAPI("false", "/tmp/test.sock", nil)
	mux := http.NewServeMux()
	api.Register(mux)

	body := `{"command": "sandbox list"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/exec", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d", rec.Code, http.StatusOK)
	}

	var resp ExecResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.ExitCode == 0 {
		t.Error("expected non-zero exit code for failing command")
	}
}

func TestExecAPI_HandleExec_Streaming(t *testing.T) {
	api := NewExecAPI("echo", "/tmp/test.sock", nil)
	mux := http.NewServeMux()
	api.Register(mux)

	body := `{"command": "hello streaming", "stream": true}`
	req := httptest.NewRequest(http.MethodPost, "/v1/exec", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	ct := rec.Header().Get("Content-Type")
	if !strings.Contains(ct, "text/event-stream") {
		t.Errorf("content-type = %q, want text/event-stream", ct)
	}

	// Verify we get at least an exit event in the SSE stream.
	respBody := rec.Body.String()
	if !strings.Contains(respBody, `"type":"exit"`) {
		t.Errorf("SSE body missing exit event: %s", respBody)
	}
}

func TestLimitWriter(t *testing.T) {
	var buf strings.Builder
	lw := newLimitWriter(&buf, 10)

	// Write within limit.
	n, err := lw.Write([]byte("hello"))
	if err != nil || n != 5 {
		t.Fatalf("write within limit: n=%d err=%v", n, err)
	}

	// Write exceeding limit — should silently truncate.
	n, err = lw.Write([]byte(" world this is too long"))
	if err != nil {
		t.Fatalf("write over limit: err=%v", err)
	}
	// All bytes should be "consumed" even if not all written.
	if n != len(" world this is too long") {
		t.Errorf("n = %d, want %d", n, len(" world this is too long"))
	}

	// Only first 10 bytes should have been written.
	if buf.String() != "hello worl" {
		t.Errorf("buf = %q, want %q", buf.String(), "hello worl")
	}
}

func TestResolveCLIPath_Explicit(t *testing.T) {
	// /bin/echo should exist on any Linux system.
	path, err := ResolveCLIPath("/bin/echo")
	if err != nil {
		t.Fatalf("ResolveCLIPath(/bin/echo): %v", err)
	}
	if path != "/bin/echo" {
		t.Errorf("path = %q, want /bin/echo", path)
	}
}

func TestResolveCLIPath_NotFound(t *testing.T) {
	_, err := ResolveCLIPath("/nonexistent/binary/path")
	if err == nil {
		t.Error("expected error for nonexistent path")
	}
}

func TestResolveCLIPath_Directory(t *testing.T) {
	_, err := ResolveCLIPath("/tmp")
	if err == nil {
		t.Error("expected error for directory path")
	}
}

func TestExecAPI_Register_NilMux(t *testing.T) {
	api := NewExecAPI("echo", "/tmp/test.sock", nil)
	api.Register(nil) // should not panic
}

func TestExecAPI_HandleExec_InvalidJSON(t *testing.T) {
	api := NewExecAPI("echo", "/tmp/test.sock", nil)
	mux := http.NewServeMux()
	api.Register(mux)

	req := httptest.NewRequest(http.MethodPost, "/v1/exec", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("got status %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestExecAPI_HandleExec_UnknownFields(t *testing.T) {
	api := NewExecAPI("echo", "/tmp/test.sock", nil)
	mux := http.NewServeMux()
	api.Register(mux)

	body := `{"command": "status", "bogus_field": true}`
	req := httptest.NewRequest(http.MethodPost, "/v1/exec", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	// Should reject unknown fields.
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("got status %d, want %d; body: %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}
