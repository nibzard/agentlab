package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
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
	if resp["error"] != "command or args is required" {
		t.Errorf("error = %q, want %q", resp["error"], "command or args is required")
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

		{name: "invalid nested command", body: `{"command":"sandbox imaginary --bogus"}`, wantErr: true},
		{name: "invalid flag after global flag", body: `{"command":"--json sandbox show 123 --bogus"}`, wantErr: true},
		{name: "missing operand", body: `{"command":"sandbox show"}`, wantErr: true},
		{name: "missing required flags", body: `{"command":"job run"}`, wantErr: true},
		{name: "default-dependent sandbox profile", body: `{"args":["sandbox","new"]}`, wantOK: true},
		{name: "remote quoted argv", body: `{"args":["ssh","123","echo","quoted value","--json"]}`, wantOK: true},
		{name: "empty modifier", body: `{"args":["sandbox","new","+"]}`, wantErr: true},
		{name: "invalid numeric target", body: `{"args":["sandbox","show","bad"]}`, wantErr: true},
		{name: "implemented pool command", body: `{"command":"pool status"}`, wantOK: true},
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
			if len(resp.Deferred) == 0 {
				t.Error("missing deferred context checks")
			}
			if resp.Validation != "syntax" {
				t.Errorf("validation level %q", resp.Validation)
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

// TestCommandSummary verifies that a log-safe summary of a CLI argv never
// includes flags or their values — so a secret passed via --value cannot reach
// the logs (review H1).
func TestCommandSummary(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"secret value flag", []string{"secrets", "set-env", "--value", "TOPSECRET"}, "secrets set-env"},
		{"secret equals form", []string{"secrets", "set-env", "--value=TOPSECRET"}, "secrets set-env"},
		{"command and subcommand", []string{"sandbox", "list", "--json"}, "sandbox list"},
		{"leading global flag", []string{"--json", "status"}, "status"},
		{"single word", []string{"status"}, "status"},
		{"third positional dropped", []string{"job", "run", "repo-url", "--branch", "x"}, "job run"},
		{"only flags", []string{"--json", "--socket", "/x"}, "<unknown>"},
		{"global token value skipped", []string{"--token", "tskey-secret-123", "sandbox", "list"}, "sandbox list"},
		{"global token equals form", []string{"--token=tskey-secret-123", "status"}, "status"},
		{"empty", []string{}, "<unknown>"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := CommandSummary(tc.args); got != tc.want {
				t.Errorf("CommandSummary(%v) = %q, want %q", tc.args, got, tc.want)
			}
			if tc.name == "secret value flag" || tc.name == "secret equals form" {
				joined := strings.Join(tc.args, " ")
				if out := CommandSummary(tc.args); strings.Contains(out, "TOPSECRET") {
					t.Errorf("summary leaked secret: %q (from %q)", out, joined)
				}
			}
		})
	}
}

func TestApplyExecTimeout(t *testing.T) {
	cases := []struct {
		requested int
		want      time.Duration
	}{
		{0, defaultExecTimeout},  // zero → server default
		{-5, defaultExecTimeout}, // negative → server default
		{30, 30 * time.Second},   // normal
		{3600, maxExecTimeout},   // exactly the ceiling
		{math.MaxInt, maxExecTimeout},
		{999999, maxExecTimeout}, // above ceiling → clamped
	}
	for _, tc := range cases {
		if got := applyExecTimeout(tc.requested); got != tc.want {
			t.Errorf("applyExecTimeout(%d) = %v, want %v", tc.requested, got, tc.want)
		}
	}
}

// TestExecAPI_LogSummaryOmitsSecretArg proves the exec log line (capture path)
// contains the command summary but never the secret value carried in an
// argument (review H1 regression).
func TestExecAPI_LogSummaryOmitsSecretArg(t *testing.T) {
	var buf bytes.Buffer
	api := NewExecAPI("echo", "/tmp/test.sock", log.New(&buf, "", 0))
	mux := http.NewServeMux()
	api.Register(mux)

	const secret = "SUPER-SECRET-VALUE-999"
	body := `{"command": "secrets set-env --name TOKEN --value ` + secret + `"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/exec", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	logged := buf.String()
	if strings.Contains(logged, secret) {
		t.Errorf("daemon log leaked secret value: %s", logged)
	}
	if !strings.Contains(logged, "secrets set-env") {
		t.Errorf("daemon log missing command summary: %s", logged)
	}
}

// writeFloodScript writes an executable shell script that ignores its arguments
// and floods stdout and stderr concurrently, to exercise the streaming fan-out.
func writeFloodScript(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "flood.sh")
	content := "#!/bin/sh\n" + body + "\n"
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}
	return path
}

// TestExecAPI_HandleExec_StreamingBothStreams exercises the concurrent
// stdout/stderr fan-out through the single writer goroutine (review H1). Run
// with -race to detect any concurrent ResponseWriter use.
func TestExecAPI_HandleExec_StreamingBothStreams(t *testing.T) {
	script := writeFloodScript(t, "for i in 1 2 3 4 5 6 7 8 9 10; do echo out$i; echo err$i >&2; done")
	api := NewExecAPI(script, "/tmp/test.sock", log.New(io.Discard, "", 0))
	mux := http.NewServeMux()
	api.Register(mux)

	body := `{"command": "run", "stream": true}`
	req := httptest.NewRequest(http.MethodPost, "/v1/exec", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	out := rec.Body.String()
	if !strings.Contains(out, `"type":"exit"`) {
		t.Errorf("missing exit event: %s", out)
	}
	if !strings.Contains(out, "out1") || !strings.Contains(out, "err1") {
		t.Errorf("missing concurrent stdout/stderr output: %s", out)
	}
}

// TestExecAPI_HandleExec_StreamingOutputCap verifies that streaming enforces a
// total output limit rather than streaming unbounded output (review H1).
func TestExecAPI_HandleExec_StreamingOutputCap(t *testing.T) {
	script := writeFloodScript(t, "head -c 6291456 /dev/zero | tr '\\000' x")
	api := NewExecAPI(script, "/tmp/test.sock", log.New(io.Discard, "", 0))
	mux := http.NewServeMux()
	api.Register(mux)

	// Finite deterministic output exceeds the cap without relying on shell-loop speed.
	body := `{"command": "run", "stream": true, "timeout": 30}`
	req := httptest.NewRequest(http.MethodPost, "/v1/exec", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	out := rec.Body.String()
	if !strings.Contains(out, "output limit exceeded") {
		t.Errorf("expected output cap message: %s", out)
	}
	// The cap (4MB) plus SSE framing must keep the response well below, say,
	// 16MB — proving output was truncated, not streamed forever.
	if len(out) > 16<<20 {
		t.Errorf("streamed response far exceeded cap: %d bytes", len(out))
	}
}

func TestExecTimeoutContract(t *testing.T) {
	script := writeFloodScript(t, "exec sleep 5")
	api := NewExecAPI(script, "/tmp/test.sock", log.New(io.Discard, "", 0))
	for _, stream := range []bool{false, true} {
		body := fmt.Sprintf(`{"args":["status"],"timeout":1,"stream":%t}`, stream)
		req := httptest.NewRequest(http.MethodPost, "/v1/exec", strings.NewReader(body))
		rec := httptest.NewRecorder()
		api.handleExec(rec, req)
		if stream {
			if !strings.Contains(rec.Body.String(), `"code":124`) || !strings.Contains(rec.Body.String(), `"timed_out":true`) {
				t.Fatal(rec.Body.String())
			}
		} else {
			var result ExecResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if result.ExitCode != 124 || !result.TimedOut {
				t.Fatalf("%+v", result)
			}
		}
	}
}

func TestExecCaptureTruncationContract(t *testing.T) {
	script := writeFloodScript(t, "head -c 6291456 /dev/zero | tr '\\000' x")
	api := NewExecAPI(script, "/tmp/test.sock", log.New(io.Discard, "", 0))
	rec := httptest.NewRecorder()
	api.handleExec(rec, httptest.NewRequest(http.MethodPost, "/v1/exec", strings.NewReader(`{"args":["status"]}`)))
	var result ExecResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 0 || !result.StdoutTruncated || result.StdoutCapturedBytes != maxOutputBytes || result.StdoutDroppedBytes != 2<<20 {
		t.Fatalf("exit=%d truncated=%t captured=%d dropped=%d", result.ExitCode, result.StdoutTruncated, result.StdoutCapturedBytes, result.StdoutDroppedBytes)
	}
}

func TestExecUnicodeTruncationBothStreams(t *testing.T) {
	data := strings.Repeat("界", maxOutputBytes/3+10)
	path := filepath.Join(t.TempDir(), "unicode.txt")
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	quoted := "'" + strings.ReplaceAll(path, "'", "'\\''") + "'"
	script := writeFloodScript(t, "cat "+quoted+"\ncat "+quoted+" >&2")
	api := NewExecAPI(script, "/tmp/test.sock", log.New(io.Discard, "", 0))
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", stream), func(t *testing.T) {
			rec := httptest.NewRecorder()
			body := fmt.Sprintf(`{"args":["status"],"stream":%t}`, stream)
			api.handleExec(rec, httptest.NewRequest(http.MethodPost, "/v1/exec", strings.NewReader(body)))
			if !stream {
				var result ExecResponse
				if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
					t.Fatal(err)
				}
				for _, output := range []string{result.Stdout, result.Stderr} {
					if strings.ContainsRune(output, '\uFFFD') || !utf8.ValidString(output) || len(output) != maxOutputBytes-1 {
						t.Fatalf("invalid captured UTF-8 or size: %d", len(output))
					}
				}
				if !result.StdoutTruncated || !result.StderrTruncated || result.StdoutCapturedBytes+result.StdoutDroppedBytes != int64(len(data)) || result.StderrCapturedBytes+result.StderrDroppedBytes != int64(len(data)) {
					t.Fatal("capture metadata does not account for both streams")
				}
				if result.ExitCode != 0 {
					t.Fatalf("truncation changed exit: %d", result.ExitCode)
				}
			} else {
				total := 0
				truncated := map[string]SSEEvent{}
				exit := false
				for _, line := range strings.Split(rec.Body.String(), "\n") {
					if !strings.HasPrefix(line, "data: ") {
						continue
					}
					var event SSEEvent
					if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event); err != nil {
						t.Fatal(err)
					}
					switch event.Type {
					case "stdout", "stderr":
						if event.Data == "output limit exceeded" {
							continue
						}
						if strings.ContainsRune(event.Data, '\uFFFD') || !utf8.ValidString(event.Data) {
							t.Fatal("stream split a Unicode rune")
						}
						total += len(event.Data)
					case "truncation":
						truncated[event.Stream] = event
					case "exit":
						exit = true
						if event.Code != 0 {
							t.Fatalf("exit %d", event.Code)
						}
					}
				}
				if total > maxOutputBytes || !exit {
					t.Fatal("stream exceeded cap or lost exit")
				}
				for _, label := range []string{"stdout", "stderr"} {
					event, ok := truncated[label]
					if !ok || event.CapturedBytes+event.DroppedBytes != int64(len(data)) {
						t.Fatalf("%s truncation accounting: %+v", label, event)
					}
				}
			}
		})
	}
}

func TestExecStreamingSmallOutputArrivesBeforeExit(t *testing.T) {
	// The child waits for an acknowledgement written by the HTTP output sink.
	// Buffering until 4 KiB/EOF would deadlock until the timeout and fail this test.
	ack := filepath.Join(t.TempDir(), "ack")
	quoted := "'" + strings.ReplaceAll(ack, "'", "'\\''") + "'"
	script := writeFloodScript(t, "printf '界'\nwhile [ ! -f "+quoted+" ]; do sleep 0.01; done\nprintf done")
	api := NewExecAPI(script, "/tmp/test.sock", log.New(io.Discard, "", 0))
	sink := &acknowledgingResponse{ResponseRecorder: httptest.NewRecorder(), path: ack}
	api.handleExec(sink, httptest.NewRequest(http.MethodPost, "/v1/exec", strings.NewReader(`{"args":["status"],"stream":true,"timeout":3}`)))
	var terminal SSEEvent
	for _, line := range strings.Split(sink.Body.String(), "\n") {
		if strings.HasPrefix(line, "data: ") {
			var event SSEEvent
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event); err != nil {
				t.Fatal(err)
			}
			if event.Type == "exit" {
				terminal = event
			}
		}
	}
	if terminal.Type != "exit" || terminal.Code != 0 || terminal.TimedOut {
		t.Fatalf("small output did not stream before exit: %+v", terminal)
	}
	if _, err := os.Stat(ack); err != nil {
		t.Fatal("no live output acknowledgement")
	}
	if !strings.Contains(sink.Body.String(), "done") {
		t.Fatal("child did not complete after streamed acknowledgement")
	}
}

type acknowledgingResponse struct {
	*httptest.ResponseRecorder
	path string
}

func (r *acknowledgingResponse) Write(data []byte) (int, error) {
	if bytes.Contains(data, []byte("界")) {
		if err := os.WriteFile(r.path, []byte("ack"), 0600); err != nil {
			return 0, err
		}
	}
	return r.ResponseRecorder.Write(data)
}
