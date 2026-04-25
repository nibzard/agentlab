// Package api implements the HTTPS API that mirrors the CLI 1:1.
//
// This is the "SSH API shoved into a POST body" pattern: one API that works
// identically over SSH and HTTP. The exec handler spawns the agentlab CLI
// binary with the provided command string, capturing stdout/stderr and
// returning the result as JSON.
//
// Endpoints:
//   - POST /v1/exec        - Execute a CLI command
//   - POST /v1/exec/dry-run - Validate a command without executing it
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const (
	// maxOutputBytes limits the captured stdout/stderr per command execution.
	maxOutputBytes = 4 << 20 // 4MB

	// defaultExecTimeout is the default timeout for command execution.
	defaultExecTimeout = 5 * time.Minute
)

// ExecRequest is the JSON body for POST /v1/exec and POST /v1/exec/dry-run.
type ExecRequest struct {
	// Command is the agentlab CLI command string, e.g. "sandbox list --json".
	Command string `json:"command"`

	// Timeout is an optional execution timeout in seconds.
	// Defaults to 300 (5 minutes). 0 means no timeout.
	Timeout int `json:"timeout,omitempty"`

	// Stream enables SSE streaming output when true.
	// Only supported on POST /v1/exec, not /v1/exec/dry-run.
	Stream bool `json:"stream,omitempty"`
}

// ExecResponse is the JSON response for POST /v1/exec (non-streaming).
type ExecResponse struct {
	ExitCode int    `json:"exit_code"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
}

// ExecDryRunResponse is the JSON response for POST /v1/exec/dry-run.
type ExecDryRunResponse struct {
	OK      bool     `json:"ok"`
	Command string   `json:"command"`
	Args    []string `json:"args"`
	Errors  []string `json:"errors,omitempty"`
}

// SSEEvent is a single server-sent event for streaming exec output.
type SSEEvent struct {
	Type string `json:"type"` // "stdout", "stderr", "exit"
	Data string `json:"data"` // base64 or raw text
	Code int    `json:"code,omitempty"` // exit code (only for type="exit")
}

// ExecAPI handles POST /v1/exec and POST /v1/exec/dry-run.
//
// It spawns the agentlab CLI binary for each request, reusing the same
// command parsing as the SSH gateway. This ensures 100% CLI parity: whatever
// `agentlab <cmd>` does locally, `POST /v1/exec {"command":"<cmd>"}` does
// over HTTP.
type ExecAPI struct {
	cliPath    string
	socketPath string
	logger     *log.Logger
}

// NewExecAPI creates a new exec API handler.
//
// Parameters:
//   - cliPath: path to the agentlab CLI binary (use "" for auto-detect)
//   - socketPath: path to the daemon unix socket (passed via --socket flag)
//   - logger: optional logger (defaults to log.Default)
func NewExecAPI(cliPath, socketPath string, logger *log.Logger) *ExecAPI {
	if logger == nil {
		logger = log.Default()
	}
	return &ExecAPI{
		cliPath:    cliPath,
		socketPath: socketPath,
		logger:     logger,
	}
}

// Register registers the exec API routes on the given mux.
func (api *ExecAPI) Register(mux *http.ServeMux) {
	if mux == nil {
		return
	}
	mux.HandleFunc("/v1/exec/dry-run", api.handleDryRun)
	mux.HandleFunc("/v1/exec", api.handleExec)
}

// handleExec executes a CLI command and returns the output.
func (api *ExecAPI) handleExec(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeExecError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req ExecRequest
	if err := decodeExecJSON(r, &req); err != nil {
		writeExecError(w, http.StatusBadRequest, err.Error())
		return
	}

	cmd := strings.TrimSpace(req.Command)
	if cmd == "" {
		writeExecError(w, http.StatusBadRequest, "command is required")
		return
	}

	args := buildExecArgs(api.socketPath, cmd)

	if req.Stream {
		api.handleExecStream(w, r, args, req)
		return
	}

	api.handleExecCapture(w, r, args, req)
}

// handleExecCapture runs the command and captures all output before responding.
func (api *ExecAPI) handleExecCapture(w http.ResponseWriter, r *http.Request, args []string, req ExecRequest) {
	timeout := defaultExecTimeout
	if req.Timeout > 0 {
		timeout = time.Duration(req.Timeout) * time.Second
	}

	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	api.logger.Printf("exec: agentlab %s", strings.Join(args[2:], " "))

	proc := exec.CommandContext(ctx, api.cliPath, args...)
	var stdout, stderr bytes.Buffer
	proc.Stdout = newLimitWriter(&stdout, maxOutputBytes)
	proc.Stderr = newLimitWriter(&stderr, maxOutputBytes)

	exitCode := 0
	if err := proc.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
			if exitCode < 0 {
				exitCode = 1
			}
		} else if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			exitCode = 124
		} else {
			exitCode = 1
		}
	}

	writeExecJSON(w, http.StatusOK, ExecResponse{
		ExitCode: exitCode,
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
	})
}

// handleExecStream runs the command with SSE streaming output.
func (api *ExecAPI) handleExecStream(w http.ResponseWriter, r *http.Request, args []string, req ExecRequest) {
	timeout := defaultExecTimeout
	if req.Timeout > 0 {
		timeout = time.Duration(req.Timeout) * time.Second
	}

	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	flusher, canFlush := w.(http.Flusher)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	if canFlush {
		flusher.Flush()
	}

	api.logger.Printf("exec-stream: agentlab %s", strings.Join(args[2:], " "))

	proc := exec.CommandContext(ctx, api.cliPath, args...)

	stdoutPipe, err := proc.StdoutPipe()
	if err != nil {
		api.writeSSE(w, SSEEvent{Type: "stderr", Data: err.Error()}, canFlush, flusher)
		api.writeSSE(w, SSEEvent{Type: "exit", Code: 1}, canFlush, flusher)
		return
	}
	stderrPipe, err := proc.StderrPipe()
	if err != nil {
		_ = stdoutPipe.Close()
		api.writeSSE(w, SSEEvent{Type: "stderr", Data: err.Error()}, canFlush, flusher)
		api.writeSSE(w, SSEEvent{Type: "exit", Code: 1}, canFlush, flusher)
		return
	}

	if err := proc.Start(); err != nil {
		api.writeSSE(w, SSEEvent{Type: "stderr", Data: err.Error()}, canFlush, flusher)
		api.writeSSE(w, SSEEvent{Type: "exit", Code: 1}, canFlush, flusher)
		return
	}

	var wg sync.WaitGroup
	wg.Add(2)

	streamPipe := func(label string, pipe io.Reader) {
		defer wg.Done()
		buf := make([]byte, 4096)
		for {
			n, readErr := pipe.Read(buf)
			if n > 0 {
				api.writeSSE(w, SSEEvent{Type: label, Data: string(buf[:n])}, canFlush, flusher)
			}
			if readErr != nil {
				return
			}
		}
	}

	go streamPipe("stdout", stdoutPipe)
	go streamPipe("stderr", stderrPipe)

	wg.Wait()

	exitCode := 0
	if err := proc.Wait(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
			if exitCode < 0 {
				exitCode = 1
			}
		} else {
			exitCode = 1
		}
	}

	api.writeSSE(w, SSEEvent{Type: "exit", Code: exitCode}, canFlush, flusher)
}

// handleDryRun validates a command without executing it.
func (api *ExecAPI) handleDryRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeExecError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req ExecRequest
	if err := decodeExecJSON(r, &req); err != nil {
		writeExecError(w, http.StatusBadRequest, err.Error())
		return
	}

	cmd := strings.TrimSpace(req.Command)
	if cmd == "" {
		writeExecError(w, http.StatusBadRequest, "command is required")
		return
	}

	args := buildExecArgs(api.socketPath, cmd)
	// The CLI args start after --socket <path>, so the actual command args
	// are args[2:].
	cliArgs := args[2:]

	resp := ExecDryRunResponse{
		OK:      true,
		Command: cmd,
		Args:    cliArgs,
	}

	// Basic validation: check that the first arg is a known command.
	if len(cliArgs) > 0 && !isValidCLICommand(cliArgs[0]) {
		resp.OK = false
		resp.Errors = append(resp.Errors, fmt.Sprintf("unknown command: %q", cliArgs[0]))
	}

	writeExecJSON(w, http.StatusOK, resp)
}

// writeSSE writes a single SSE event to the response writer.
func (api *ExecAPI) writeSSE(w http.ResponseWriter, event SSEEvent, canFlush bool, flusher http.Flusher) {
	data, _ := json.Marshal(event)
	fmt.Fprintf(w, "data: %s\n\n", data)
	if canFlush {
		flusher.Flush()
	}
}

// buildExecArgs constructs CLI arguments with --socket prepended.
func buildExecArgs(socketPath, cmd string) []string {
	args := []string{"--socket", socketPath}
	args = append(args, strings.Fields(cmd)...)
	return args
}

// isValidCLICommand checks whether the first token is a known agentlab command.
func isValidCLICommand(first string) bool {
	known := map[string]bool{
		// Top-level aliases (from IMPL-6)
		"new": true, "ls": true, "ssh": true, "rm": true,
		// Core commands
		"status": true, "schema": true, "init": true, "bootstrap": true,
		"job": true, "sandbox": true, "workspace": true, "session": true,
		"profile": true, "secrets": true, "msg": true,
		"logs": true, "connect": true, "disconnect": true,
		"expose": true, "unexpose": true,
		"lease": true, "token": true, "defaults": true,
		"version": true, "help": true, "doctor": true,
		"events": true, "artifacts": true,
		"snapshot": true, "snapshots": true,
		"integration": true, "credential": true,
		"daemon": true,
	}
	if known[first] {
		return true
	}
	// Global flags (--json, --socket, etc.) are valid starts.
	if strings.HasPrefix(first, "-") {
		return true
	}
	return false
}

// limitWriter wraps a writer and stops writing after maxBytes.
type limitWriter struct {
	w       io.Writer
	written int64
	max     int64
}

func newLimitWriter(w io.Writer, max int64) *limitWriter {
	return &limitWriter{w: w, max: max}
}

func (lw *limitWriter) Write(p []byte) (int, error) {
	if lw.written >= lw.max {
		return len(p), nil
	}
	remaining := lw.max - lw.written
	if int64(len(p)) > remaining {
		n, err := lw.w.Write(p[:remaining])
		lw.written += int64(n)
		if err != nil {
			return n, err
		}
		lw.written = lw.max
		return len(p), nil
	}
	n, err := lw.w.Write(p)
	lw.written += int64(n)
	return n, err
}

// decodeExecJSON decodes a JSON request body.
func decodeExecJSON(r *http.Request, dest any) error {
	if r.Body == nil {
		return errors.New("request body is required")
	}
	defer r.Body.Close()
	r.Body = http.MaxBytesReader(nil, r.Body, 1<<20)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dest); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	return nil
}

// writeExecJSON writes a JSON response.
func writeExecJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

// writeExecError writes a JSON error response.
func writeExecError(w http.ResponseWriter, status int, msg string) {
	writeExecJSON(w, status, map[string]string{"error": msg})
}

// ResolveCLIPath finds the agentlab CLI binary.
func ResolveCLIPath(explicit string) (string, error) {
	if explicit != "" {
		info, err := os.Stat(explicit)
		if err != nil {
			return "", fmt.Errorf("cli-path %s: %w", explicit, err)
		}
		if info.IsDir() {
			return "", fmt.Errorf("cli-path %s is a directory", explicit)
		}
		return explicit, nil
	}
	path, err := exec.LookPath("agentlab")
	if err != nil {
		return "", fmt.Errorf("agentlab binary not found in PATH; set cli_path in config")
	}
	return path, nil
}
