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
	"unicode/utf8"

	"github.com/agentlab/agentlab/internal/argv"
	"github.com/agentlab/agentlab/internal/auth"
	"github.com/agentlab/agentlab/internal/clicontract"
)

const (
	// maxOutputBytes limits the captured stdout/stderr per command execution,
	// in both capture and streaming modes (review H1).
	maxOutputBytes = 4 << 20 // 4MB

	// defaultExecTimeout is applied when a caller omits or sends a non-positive
	// timeout (review H1: zero is a server default, not "unlimited").
	defaultExecTimeout = 5 * time.Minute

	// maxExecTimeout is the server-side ceiling on a caller-requested timeout.
	// Positive client values above this are clamped down (review H1).
	maxExecTimeout = time.Hour
)

// ExecRequest is the JSON body for POST /v1/exec and POST /v1/exec/dry-run.
type ExecRequest struct {
	// Command is the agentlab CLI command string, e.g. "sandbox list --json".
	// It is split on whitespace; use Args for commands whose values contain
	// spaces.
	Command string `json:"command"`

	// Args is the canonical, pre-tokenized CLI argv (review M9). When non-empty
	// it takes precedence over Command and is passed to the CLI verbatim, so a
	// value like "fix the flaky test" survives as one argument instead of being
	// shattered by whitespace splitting.
	Args []string `json:"args,omitempty"`

	// Timeout is an optional execution timeout in seconds. Zero or a negative
	// value selects the server default (5 minutes); it does NOT disable the
	// timeout. Values above the server maximum are clamped to it (review H1).
	Timeout int `json:"timeout,omitempty"`

	// Stream enables SSE streaming output when true.
	// Only supported on POST /v1/exec, not /v1/exec/dry-run.
	Stream bool `json:"stream,omitempty"`
}

// ExecResponse is the JSON response for POST /v1/exec (non-streaming).
type ExecResponse struct {
	ExitCode                int    `json:"exit_code"`
	Stdout                  string `json:"stdout"`
	Stderr                  string `json:"stderr"`
	TimedOut                bool   `json:"timed_out"`
	Cancelled               bool   `json:"cancelled"`
	EffectiveTimeoutSeconds int    `json:"effective_timeout_seconds"`
	StdoutTruncated         bool   `json:"stdout_truncated"`
	StderrTruncated         bool   `json:"stderr_truncated"`
	StdoutCapturedBytes     int64  `json:"stdout_captured_bytes"`
	StderrCapturedBytes     int64  `json:"stderr_captured_bytes"`
	StdoutDroppedBytes      int64  `json:"stdout_dropped_bytes"`
	StderrDroppedBytes      int64  `json:"stderr_dropped_bytes"`
}

type ExecErrorResponse struct {
	Error string `json:"error"`
}

// ExecDryRunResponse is the JSON response for POST /v1/exec/dry-run.
type ExecDryRunResponse struct {
	Deferred   []string `json:"deferred"`
	OK         bool     `json:"ok"`
	Validation string   `json:"validation"`
	Command    string   `json:"command"`
	Args       []string `json:"args"`
	Errors     []string `json:"errors,omitempty"`
}

// SSEEvent is a single server-sent event for streaming exec output.
type SSEEvent struct {
	TimedOut                bool   `json:"timed_out,omitempty"`
	Cancelled               bool   `json:"cancelled,omitempty"`
	EffectiveTimeoutSeconds int    `json:"effective_timeout_seconds,omitempty"`
	Stream                  string `json:"stream,omitempty"`
	CapturedBytes           int64  `json:"captured_bytes,omitempty"`
	DroppedBytes            int64  `json:"dropped_bytes,omitempty"`
	Type                    string `json:"type"`           // "stdout", "stderr", "exit"
	Data                    string `json:"data"`           // base64 or raw text
	Code                    int    `json:"code,omitempty"` // exit code (only for type="exit")
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

	// /v1/exec is the generic CLI escape hatch: it can invoke any command,
	// including ones that touch resources outside any coarse scope claim. It is
	// therefore restricted to full-access identities. Trusted callers (the local
	// Unix socket, which has no identity; and the legacy bearer token, whose
	// identity has Token==nil) are admitted. SSH-signed tokens must declare
	// Commands ["*"] with no Scope (review C1/M9).
	if !execAllowed(r.Context()) {
		writeExecError(w, http.StatusForbidden, "exec requires a full-access token")
		return
	}

	var req ExecRequest
	if err := decodeExecJSON(r, &req); err != nil {
		writeExecError(w, http.StatusBadRequest, err.Error())
		return
	}

	cliArgs, err := resolveExecArgs(req)
	if err != nil {
		writeExecError(w, http.StatusBadRequest, err.Error())
		return
	}

	args := buildExecArgsFromCLI(api.socketPath, cliArgs)

	if req.Stream {
		api.handleExecStream(w, r, args, req)
		return
	}

	api.handleExecCapture(w, r, args, req)
}

// handleExecCapture runs the command and captures all output before responding.
func (api *ExecAPI) handleExecCapture(w http.ResponseWriter, r *http.Request, args []string, req ExecRequest) {
	ctx, cancel := context.WithTimeout(r.Context(), applyExecTimeout(req.Timeout))
	defer cancel()

	api.logger.Printf("exec: agentlab %s", CommandSummary(args[2:]))

	proc := exec.CommandContext(ctx, api.cliPath, args...)
	var stdout, stderr bytes.Buffer
	outWriter, errWriter := newLimitWriter(&stdout, maxOutputBytes), newLimitWriter(&stderr, maxOutputBytes)
	proc.Stdout, proc.Stderr = outWriter, errWriter

	exitCode := execExitCode(ctx, proc.Run())
	trimCapturedUTF8(&stdout, outWriter)
	trimCapturedUTF8(&stderr, errWriter)

	writeExecJSON(w, http.StatusOK, ExecResponse{
		ExitCode:                exitCode,
		Stdout:                  stdout.String(),
		Stderr:                  stderr.String(),
		TimedOut:                errors.Is(ctx.Err(), context.DeadlineExceeded),
		Cancelled:               errors.Is(ctx.Err(), context.Canceled),
		EffectiveTimeoutSeconds: int(applyExecTimeout(req.Timeout) / time.Second),
		StdoutTruncated:         outWriter.dropped > 0, StderrTruncated: errWriter.dropped > 0,
		StdoutCapturedBytes: outWriter.written, StderrCapturedBytes: errWriter.written,
		StdoutDroppedBytes: outWriter.dropped, StderrDroppedBytes: errWriter.dropped,
	})
}

// handleExecStream runs the command with SSE streaming output.
//
// stdout and stderr are read by two goroutines but funneled through a single
// channel into one writer goroutine, because http.ResponseWriter is not safe
// for concurrent use (review H1). The writer also enforces a total output cap;
// once exceeded, further output is dropped (the process is allowed to finish so
// its exit code is still reported).
func (api *ExecAPI) handleExecStream(w http.ResponseWriter, r *http.Request, args []string, req ExecRequest) {
	ctx, cancel := context.WithTimeout(r.Context(), applyExecTimeout(req.Timeout))
	defer cancel()

	flusher, canFlush := w.(http.Flusher)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	if canFlush {
		flusher.Flush()
	}

	api.logger.Printf("exec-stream: agentlab %s", CommandSummary(args[2:]))

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

	// Fan both pipes into one channel so only the writer goroutine below ever
	// touches the ResponseWriter.
	type streamChunk struct {
		label string
		data  string
	}
	chunks := make(chan streamChunk, 64)

	var readers sync.WaitGroup
	readers.Add(2)
	readPipe := func(label string, pipe io.Reader) {
		defer readers.Done()
		buf := make([]byte, 4096)
		pending := make([]byte, 0, utf8.UTFMax)
		for {
			n, readErr := pipe.Read(buf)
			data := append(pending, buf[:n]...)
			length := len(data)
			if readErr == nil {
				length = completeUTF8Prefix(data)
			}
			if length > 0 {
				select {
				case chunks <- streamChunk{label, string(data[:length])}:
				case <-ctx.Done():
					return
				}
			}
			pending = append(pending[:0], data[length:]...)
			if readErr != nil {
				return
			}
		}
	}
	go readPipe("stdout", stdoutPipe)
	go readPipe("stderr", stderrPipe)

	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		var written int64
		captured, dropped := map[string]int64{}, map[string]int64{}
		capped := false
		for ch := range chunks {
			remaining := int64(maxOutputBytes) - written
			data := ch.data
			if int64(len(data)) > remaining {
				data = data[:remaining]
				data = data[:completeUTF8Prefix([]byte(data))]
				if !capped {
					api.writeSSE(w, SSEEvent{Type: "stderr", Data: "output limit exceeded"}, canFlush, flusher)
					capped = true
				}
			}
			captured[ch.label] += int64(len(data))
			dropped[ch.label] += int64(len(ch.data) - len(data))
			written += int64(len(data))
			if data != "" {
				api.writeSSE(w, SSEEvent{Type: ch.label, Data: data}, canFlush, flusher)
			}
		}
		for _, label := range []string{"stdout", "stderr"} {
			if dropped[label] > 0 {
				api.writeSSE(w, SSEEvent{Type: "truncation", Stream: label, CapturedBytes: captured[label], DroppedBytes: dropped[label]}, canFlush, flusher)
			}
		}
	}()

	readers.Wait()
	close(chunks)
	<-writerDone

	exitCode := execExitCode(ctx, proc.Wait())

	// Sole remaining writer (the writer goroutine has exited): safe to emit the
	// terminal event.
	api.writeSSE(w, SSEEvent{Type: "exit", Code: exitCode, TimedOut: errors.Is(ctx.Err(), context.DeadlineExceeded), Cancelled: errors.Is(ctx.Err(), context.Canceled), EffectiveTimeoutSeconds: int(applyExecTimeout(req.Timeout) / time.Second)}, canFlush, flusher)
}

// handleDryRun validates a command without executing it.
func (api *ExecAPI) handleDryRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeExecError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !execAllowed(r.Context()) {
		writeExecError(w, http.StatusForbidden, "exec requires a full-access token")
		return
	}

	var req ExecRequest
	if err := decodeExecJSON(r, &req); err != nil {
		writeExecError(w, http.StatusBadRequest, err.Error())
		return
	}

	cliArgs, err := resolveExecArgs(req)
	if err != nil {
		writeExecError(w, http.StatusBadRequest, err.Error())
		return
	}

	args := buildExecArgsFromCLI(api.socketPath, cliArgs)
	// The CLI args start after --socket <path>, so the actual command args
	// are args[2:].
	resolved := args[2:]

	resp := ExecDryRunResponse{
		OK:         true,
		Validation: "syntax",
		Deferred:   []string{"saved preferences and default-dependent required options", "daemon profile/storage/policy preflight", "resource existence and execution authorization", "interactive terminal requirements"},
		Command:    req.Command,
		Args:       resolved,
	}

	// Validate shared command syntax without resolving defaults or performing operations.
	if err := clicontract.Validate(resolved); err != nil {
		resp.OK = false
		resp.Errors = append(resp.Errors, err.Error())
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

// execAllowed reports whether the request's identity may use /v1/exec. The
// generic exec escape hatch can invoke any CLI command, so it is restricted to
// full-access identities: trusted callers (no identity, e.g. the Unix socket)
// and the legacy bearer token (Token==nil) pass, as do SSH tokens that declare
// Commands ["*"] with no Scope. Scoped SSH tokens are rejected.
func execAllowed(ctx context.Context) bool {
	id := auth.FromContext(ctx)
	if id == nil || id.Token == nil {
		return true
	}
	return id.Token.IsFullAccess()
}

// resolveExecArgs derives the canonical CLI argv from the request. Structured
// Args (M9) take precedence over the free-form Command string and are passed
// through verbatim so argument boundaries — including spaces inside a value —
// are preserved. When Args is empty, Command is split with the argv tokenizer,
// which honors single/double quotes and backslash escapes instead of breaking
// on every whitespace run.
func resolveExecArgs(req ExecRequest) ([]string, error) {
	if len(req.Args) > 0 {
		out := make([]string, len(req.Args))
		copy(out, req.Args)
		return out, nil
	}
	cmd := strings.TrimSpace(req.Command)
	if cmd == "" {
		return nil, errors.New("command or args is required")
	}
	tokens, err := argv.Tokenize(cmd)
	if err != nil {
		return nil, fmt.Errorf("parse command: %w", err)
	}
	if len(tokens) == 0 {
		return nil, errors.New("command or args is required")
	}
	return tokens, nil
}

// buildExecArgs constructs CLI arguments with --socket prepended from a
// command string. Retained for the legacy Command path.
func buildExecArgs(socketPath, cmd string) []string {
	args := []string{"--socket", socketPath}
	tokens, err := argv.Tokenize(cmd)
	if err == nil {
		args = append(args, tokens...)
	}
	return args
}

// globalValueFlags are agentlab global flags that consume the following token as
// their value (e.g. "--token <secret>"). CommandSummary skips that value so it
// is never mistaken for the command verb or logged.
var globalValueFlags = map[string]bool{
	"--endpoint": true,
	"--token":    true,
	"--socket":   true,
	"--timeout":  true,
}

// CommandSummary returns a log-safe summary of a CLI argv: the command verb and
// at most its subcommand. Flags and their values are never included, so a
// sensitive value passed via --value or --token (e.g. "secrets set-env --value
// <secret>", or "--token <secret> sandbox list") cannot reach the logs
// (review H1). Leading global flags — including their values — are skipped.
func CommandSummary(cliArgs []string) string {
	var cmd []string
	skipNext := false
	for _, a := range cliArgs {
		if skipNext {
			skipNext = false
			continue
		}
		if strings.HasPrefix(a, "-") {
			// A value-taking global flag written as "--flag value" consumes the
			// next token; the "--flag=value" form carries its value inline and
			// needs no special handling.
			if !strings.Contains(a, "=") && globalValueFlags[a] {
				skipNext = true
			}
			continue
		}
		cmd = append(cmd, a)
		if len(cmd) == 2 {
			break
		}
	}
	if len(cmd) == 0 {
		return "<unknown>"
	}
	return strings.Join(cmd, " ")
}

// applyExecTimeout resolves a caller-requested timeout (seconds) into a bounded
// duration. Zero or negative selects the server default; values above the server
// maximum are clamped to it (review H1).
func applyExecTimeout(requested int) time.Duration {
	if requested <= 0 {
		return defaultExecTimeout
	}
	if requested > int(maxExecTimeout/time.Second) {
		return maxExecTimeout
	}
	return time.Duration(requested) * time.Second
}

// buildExecArgsFromCLI prepends --socket to an already-tokenized argv.
func buildExecArgsFromCLI(socketPath string, cliArgs []string) []string {
	args := []string{"--socket", socketPath}
	return append(args, cliArgs...)
}

// limitWriter wraps a writer and stops writing after maxBytes.
type limitWriter struct {
	w       io.Writer
	written int64
	dropped int64
	max     int64
}

func newLimitWriter(w io.Writer, max int64) *limitWriter {
	return &limitWriter{w: w, max: max}
}

func (lw *limitWriter) Write(p []byte) (int, error) {
	if lw.written >= lw.max {
		lw.dropped += int64(len(p))
		return len(p), nil
	}
	remaining := lw.max - lw.written
	if int64(len(p)) > remaining {
		lw.dropped += int64(len(p)) - remaining
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
	writeExecJSON(w, status, ExecErrorResponse{Error: msg})
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

// execExitCode classifies the context before the killed process exit status.
func execExitCode(ctx context.Context, err error) int {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return 124
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		return 130
	}
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() >= 0 {
		return exitErr.ExitCode()
	}
	return 1
}

// completeUTF8Prefix withholds only an incomplete final rune. Invalid complete
// bytes retain the JSON encoder's normal replacement behavior.
func completeUTF8Prefix(data []byte) int {
	if len(data) == 0 {
		return 0
	}
	start := len(data) - 1
	for start >= 0 && !utf8.RuneStart(data[start]) {
		start--
	}
	if start >= 0 && !utf8.FullRune(data[start:]) {
		return start
	}
	return len(data)
}
func trimCapturedUTF8(buf *bytes.Buffer, writer *limitWriter) {
	if writer.dropped == 0 {
		return
	}
	length := completeUTF8Prefix(buf.Bytes())
	removed := buf.Len() - length
	buf.Truncate(length)
	writer.written -= int64(removed)
	writer.dropped += int64(removed)
}
