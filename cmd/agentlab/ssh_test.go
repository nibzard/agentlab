package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

func stubSSHDial(t *testing.T, fn func(ctx context.Context, network, address string) (net.Conn, error)) {
	t.Helper()
	orig := sshDialFn
	sshDialFn = fn
	t.Cleanup(func() { sshDialFn = orig })
}

func stubSSHCommand(t *testing.T, fn func(ctx context.Context, args []string) ([]byte, error)) {
	t.Helper()
	orig := sshCommandFn
	sshCommandFn = fn
	t.Cleanup(func() { sshCommandFn = orig })
}

func stubExecSSH(t *testing.T, fn func(args []string) error) {
	t.Helper()
	orig := execSSHFn
	execSSHFn = fn
	t.Cleanup(func() { execSSHFn = orig })
}

func stubInteractive(t *testing.T, value bool) {
	t.Helper()
	orig := isInteractiveFn
	isInteractiveFn = func() bool { return value }
	t.Cleanup(func() { isInteractiveFn = orig })
}

func TestSSHStartsStoppedSandbox(t *testing.T) {
	createdAt := "2026-02-08T12:00:00Z"
	updatedAt := "2026-02-08T12:02:00Z"
	var startCalled bool

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/sandboxes/9001", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("/v1/sandboxes/9001 method = %s", r.Method)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		resp := sandboxResponse{
			VMID:          9001,
			Name:          "sandbox-9001",
			Profile:       "yolo",
			State:         "STOPPED",
			IP:            "",
			CreatedAt:     createdAt,
			LastUpdatedAt: updatedAt,
		}
		writeJSON(t, w, http.StatusOK, resp)
	})
	mux.HandleFunc("/v1/sandboxes/9001/start", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("/v1/sandboxes/9001/start method = %s", r.Method)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		startCalled = true
		resp := sandboxResponse{
			VMID:          9001,
			Name:          "sandbox-9001",
			Profile:       "yolo",
			State:         "RUNNING",
			IP:            "203.0.113.5",
			CreatedAt:     createdAt,
			LastUpdatedAt: updatedAt,
		}
		writeJSON(t, w, http.StatusOK, resp)
	})

	socketPath := startUnixHTTPServer(t, mux)
	base := commonFlags{socketPath: socketPath, jsonOutput: false, timeout: time.Second}

	stubSSHDial(t, func(ctx context.Context, network, address string) (net.Conn, error) {
		conn, peer := net.Pipe()
		_ = peer.Close()
		return conn, nil
	})

	out := captureStdout(t, func() {
		err := runSSHCommand(context.Background(), []string{"9001"}, base)
		if err != nil {
			t.Fatalf("runSSHCommand() error = %v", err)
		}
	})

	if !startCalled {
		t.Fatalf("expected start endpoint to be called")
	}
	if !strings.Contains(out, "ssh") || !strings.Contains(out, "203.0.113.5") {
		t.Fatalf("expected ssh command output, got %q", out)
	}
}

func TestSSHNoStartStoppedSandbox(t *testing.T) {
	createdAt := "2026-02-08T12:00:00Z"
	updatedAt := "2026-02-08T12:02:00Z"
	var startCalled bool

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/sandboxes/9002", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("/v1/sandboxes/9002 method = %s", r.Method)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		resp := sandboxResponse{
			VMID:          9002,
			Name:          "sandbox-9002",
			Profile:       "yolo",
			State:         "STOPPED",
			IP:            "203.0.113.6",
			CreatedAt:     createdAt,
			LastUpdatedAt: updatedAt,
		}
		writeJSON(t, w, http.StatusOK, resp)
	})
	mux.HandleFunc("/v1/sandboxes/9002/start", func(w http.ResponseWriter, r *http.Request) {
		startCalled = true
		w.WriteHeader(http.StatusOK)
	})

	socketPath := startUnixHTTPServer(t, mux)
	base := commonFlags{socketPath: socketPath, jsonOutput: false, timeout: time.Second}

	err := runSSHCommand(context.Background(), []string{"--no-start", "9002"}, base)
	if err == nil {
		t.Fatalf("expected error for stopped sandbox with --no-start")
	}
	if !strings.Contains(err.Error(), "stopped") {
		t.Fatalf("expected stopped error, got %v", err)
	}
	if startCalled {
		t.Fatalf("did not expect start endpoint to be called")
	}
}

func TestSSHDirectPathPrefersDirect(t *testing.T) {
	createdAt := "2026-02-08T12:00:00Z"
	updatedAt := "2026-02-08T12:02:00Z"

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/sandboxes/9101", func(w http.ResponseWriter, r *http.Request) {
		resp := sandboxResponse{
			VMID:          9101,
			Name:          "sandbox-9101",
			Profile:       "yolo",
			State:         "RUNNING",
			IP:            "203.0.113.10",
			CreatedAt:     createdAt,
			LastUpdatedAt: updatedAt,
		}
		writeJSON(t, w, http.StatusOK, resp)
	})

	socketPath := startUnixHTTPServer(t, mux)
	base := commonFlags{socketPath: socketPath, jsonOutput: false, timeout: time.Second}

	stubSSHDial(t, func(ctx context.Context, network, address string) (net.Conn, error) {
		conn, peer := net.Pipe()
		_ = peer.Close()
		return conn, nil
	})

	out := captureStdout(t, func() {
		err := runSSHCommand(context.Background(), []string{"--jump-host", "jump.example", "--jump-user", "jumpuser", "9101"}, base)
		if err != nil {
			t.Fatalf("runSSHCommand() error = %v", err)
		}
	})

	if strings.Contains(out, "-J") {
		t.Fatalf("expected direct ssh without -J, got %q", out)
	}
	if !strings.Contains(out, "203.0.113.10") {
		t.Fatalf("expected sandbox IP in output, got %q", out)
	}
}

func TestSSHJumpPathFormatting(t *testing.T) {
	createdAt := "2026-02-08T12:00:00Z"
	updatedAt := "2026-02-08T12:02:00Z"

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/sandboxes/9102", func(w http.ResponseWriter, r *http.Request) {
		resp := sandboxResponse{
			VMID:          9102,
			Name:          "sandbox-9102",
			Profile:       "yolo",
			State:         "RUNNING",
			IP:            "203.0.113.11",
			CreatedAt:     createdAt,
			LastUpdatedAt: updatedAt,
		}
		writeJSON(t, w, http.StatusOK, resp)
	})

	socketPath := startUnixHTTPServer(t, mux)
	base := commonFlags{socketPath: socketPath, jsonOutput: false, timeout: time.Second}

	stubSSHDial(t, func(ctx context.Context, network, address string) (net.Conn, error) {
		return nil, errors.New("dial failed")
	})

	out := captureStdout(t, func() {
		err := runSSHCommand(context.Background(), []string{"--jump-host", "jump.example", "--jump-user", "jumpuser", "9102"}, base)
		if err != nil {
			t.Fatalf("runSSHCommand() error = %v", err)
		}
	})

	if !strings.Contains(out, "-J jumpuser@jump.example") {
		t.Fatalf("expected ProxyJump args, got %q", out)
	}
}

func TestSSHJumpConfigPrecedence(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	path, err := clientConfigPath()
	if err != nil {
		t.Fatalf("clientConfigPath() error = %v", err)
	}
	if err := writeClientConfig(path, clientConfig{JumpHost: "cfg.example", JumpUser: "cfguser"}); err != nil {
		t.Fatalf("writeClientConfig() error = %v", err)
	}

	createdAt := "2026-02-08T12:00:00Z"
	updatedAt := "2026-02-08T12:02:00Z"

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/sandboxes/9103", func(w http.ResponseWriter, r *http.Request) {
		resp := sandboxResponse{
			VMID:          9103,
			Name:          "sandbox-9103",
			Profile:       "yolo",
			State:         "RUNNING",
			IP:            "203.0.113.12",
			CreatedAt:     createdAt,
			LastUpdatedAt: updatedAt,
		}
		writeJSON(t, w, http.StatusOK, resp)
	})

	socketPath := startUnixHTTPServer(t, mux)
	base := commonFlags{socketPath: socketPath, jsonOutput: false, timeout: time.Second}

	stubSSHDial(t, func(ctx context.Context, network, address string) (net.Conn, error) {
		return nil, errors.New("dial failed")
	})

	out := captureStdout(t, func() {
		err := runSSHCommand(context.Background(), []string{"--jump-host", "flag.example", "--jump-user", "flaguser", "9103"}, base)
		if err != nil {
			t.Fatalf("runSSHCommand() error = %v", err)
		}
	})

	if !strings.Contains(out, "-J flaguser@flag.example") {
		t.Fatalf("expected flag jump config to win, got %q", out)
	}
	if strings.Contains(out, "cfg.example") {
		t.Fatalf("expected config jump host to be overridden, got %q", out)
	}
}

func TestSSHExecJSONConflict(t *testing.T) {
	base := commonFlags{socketPath: "/tmp/agentlab.sock", jsonOutput: false, timeout: time.Second}
	err := runSSHCommand(context.Background(), []string{"--json", "--exec", "9104"}, base)
	if err == nil {
		t.Fatalf("expected error for --json with --exec")
	}
	if !strings.Contains(err.Error(), "--json") {
		t.Fatalf("unexpected error message %q", err.Error())
	}
}

func TestSSHWaitProbesDirect(t *testing.T) {
	createdAt := "2026-02-08T12:00:00Z"
	updatedAt := "2026-02-08T12:02:00Z"

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/sandboxes/9201", func(w http.ResponseWriter, r *http.Request) {
		resp := sandboxResponse{
			VMID:          9201,
			Name:          "sandbox-9201",
			Profile:       "yolo",
			State:         "RUNNING",
			IP:            "203.0.113.20",
			CreatedAt:     createdAt,
			LastUpdatedAt: updatedAt,
		}
		writeJSON(t, w, http.StatusOK, resp)
	})

	socketPath := startUnixHTTPServer(t, mux)
	base := commonFlags{socketPath: socketPath, jsonOutput: false, timeout: time.Second}

	dialCalls := 0
	stubSSHDial(t, func(ctx context.Context, network, address string) (net.Conn, error) {
		dialCalls++
		conn, peer := net.Pipe()
		_ = peer.Close()
		return conn, nil
	})

	_ = captureStdout(t, func() {
		err := runSSHCommand(context.Background(), []string{"--wait", "9201"}, base)
		if err != nil {
			t.Fatalf("runSSHCommand() error = %v", err)
		}
	})

	if dialCalls < 2 {
		t.Fatalf("expected wait probe to dial at least twice, got %d", dialCalls)
	}
}

func TestSSHWaitProbesJump(t *testing.T) {
	createdAt := "2026-02-08T12:00:00Z"
	updatedAt := "2026-02-08T12:02:00Z"

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/sandboxes/9202", func(w http.ResponseWriter, r *http.Request) {
		resp := sandboxResponse{
			VMID:          9202,
			Name:          "sandbox-9202",
			Profile:       "yolo",
			State:         "RUNNING",
			IP:            "203.0.113.21",
			CreatedAt:     createdAt,
			LastUpdatedAt: updatedAt,
		}
		writeJSON(t, w, http.StatusOK, resp)
	})

	socketPath := startUnixHTTPServer(t, mux)
	base := commonFlags{socketPath: socketPath, jsonOutput: false, timeout: time.Second}

	stubSSHDial(t, func(ctx context.Context, network, address string) (net.Conn, error) {
		return nil, errors.New("dial failed")
	})

	commandCalls := 0
	stubSSHCommand(t, func(ctx context.Context, args []string) ([]byte, error) {
		commandCalls++
		return []byte("Permission denied"), errors.New("exit status 255")
	})

	out := captureStdout(t, func() {
		err := runSSHCommand(context.Background(), []string{"--wait", "--jump-host", "jump.example", "--jump-user", "jumpuser", "9202"}, base)
		if err != nil {
			t.Fatalf("runSSHCommand() error = %v", err)
		}
	})

	if commandCalls == 0 {
		t.Fatalf("expected ssh probe via jump to be executed")
	}
	if !strings.Contains(out, "-J jumpuser@jump.example") {
		t.Fatalf("expected ProxyJump args, got %q", out)
	}
}

func TestSSHExecPassesThroughArgs(t *testing.T) {
	createdAt := "2026-02-08T12:00:00Z"
	updatedAt := "2026-02-08T12:02:00Z"

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/sandboxes/9301", func(w http.ResponseWriter, r *http.Request) {
		resp := sandboxResponse{
			VMID:          9301,
			Name:          "sandbox-9301",
			Profile:       "yolo",
			State:         "RUNNING",
			IP:            "203.0.113.30",
			CreatedAt:     createdAt,
			LastUpdatedAt: updatedAt,
		}
		writeJSON(t, w, http.StatusOK, resp)
	})

	socketPath := startUnixHTTPServer(t, mux)
	base := commonFlags{socketPath: socketPath, jsonOutput: false, timeout: time.Second}

	stubInteractive(t, true)
	stubSSHDial(t, func(ctx context.Context, network, address string) (net.Conn, error) {
		conn, peer := net.Pipe()
		_ = peer.Close()
		return conn, nil
	})

	var gotArgs []string
	stubExecSSH(t, func(args []string) error {
		gotArgs = append([]string{}, args...)
		return nil
	})

	err := runSSHCommand(context.Background(), []string{"--exec", "9301", "uname", "-a"}, base)
	if err != nil {
		t.Fatalf("runSSHCommand() error = %v", err)
	}
	if len(gotArgs) < 2 {
		t.Fatalf("expected exec args, got %v", gotArgs)
	}
	if gotArgs[len(gotArgs)-2] != "uname" || gotArgs[len(gotArgs)-1] != "-a" {
		t.Fatalf("expected remote args to be passed through, got %v", gotArgs)
	}
}
