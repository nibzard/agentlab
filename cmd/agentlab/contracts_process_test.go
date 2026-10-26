package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestCLIProcessContracts(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "agentlab")
	build := exec.Command("go", "build", "-o", binary, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path == "/v1/jobs/wait-fail" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"id":"wait-fail","status":"FAILED","future":{"value":9007199254740993}}`))
			return
		}
		if r.URL.Path == "/v1/sandboxes/124" {
			w.WriteHeader(404)
			w.Write([]byte(`{"code":"not_found","message":"sandbox not found"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/sandboxes/prune" {
			w.Write([]byte(`{"count":0,"dry_run":false,"outcomes":[{"vmid":888,"outcome":"failed","retryable":true,"error":"backend unavailable"}]}`))
			return
		}
		if strings.Contains(r.URL.Path, "/snapshots") {
			var request map[string]any
			if r.Method == http.MethodPost {
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"request_path": r.URL.Path, "request_method": r.Method, "request_body": request})
			return
		}
		if strings.HasPrefix(r.URL.Path, "/v1/integrations/") {
			w.Write([]byte(`{"status":"deleted","name":"fixture"}`))
			return
		}
		w.Write([]byte(`{"vmid":123,"state":"RUNNING","future":{"value":9007199254740993}}`))
	}))
	defer server.Close()
	config := t.TempDir()
	hostMarker := filepath.Join(config, "unexpected-host-action")
	hostTools := filepath.Join(config, "host-tools")
	if err := os.Mkdir(hostTools, 0700); err != nil {
		t.Fatal(err)
	}
	for _, tool := range []string{"ssh", "scp", "sudo", "qm", "pvesh", "systemctl"} {
		if err := os.WriteFile(filepath.Join(hostTools, tool), []byte("#!/bin/sh\n: > "+shellQuote(hostMarker)+"\nexit 97\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}

	for _, test := range []struct {
		args []string
		exit int
		code string
	}{
		{[]string{"job", "wait", "wait-fail", "--json"}, 1, ""},
		{[]string{"sandbox", "wait", "123", "--json"}, 0, ""},
		{[]string{"session", "show", "session-fixture", "--json"}, 0, ""},
		{[]string{"--json", "session", "show", "session-fixture"}, 0, ""},
		{[]string{"sandbox", "snapshot", "save", "123", "checkpoint", "--force", "--json"}, 0, ""},
		{[]string{"--json", "sandbox", "snapshot", "save", "--force", "123", "checkpoint"}, 0, ""},
		{[]string{"sandbox", "snapshot", "list", "123", "--json"}, 0, ""},
		{[]string{"--json", "sandbox", "snapshot", "list", "123"}, 0, ""},
		{[]string{"sandbox", "show", "123", "--json"}, 0, ""},
		{[]string{"--json", "sandbox", "show", "123"}, 0, ""},
		{[]string{"sandbox", "show", "124", "--json"}, 1, "not_found"},
		{[]string{"sandbox", "new", "--profile", "p", "--no-wait", "--and-ssh", "--json"}, 2, "invalid_arguments"},
		{[]string{"sandbox", "update", "123", "--tag=", "--clear-tags", "--json"}, 2, "invalid_arguments"},
		{[]string{"job", "run", "--repo", "r", "--profile", "p", "--task", "t", "--workspace-create", "w", "--json"}, 2, "invalid_arguments"},
		{[]string{"ssh", "123", "--port", "65536", "--json"}, 2, "invalid_arguments"},
		{[]string{"ssh", "123", "--user=", "--json"}, 2, "invalid_arguments"},
		{[]string{"sandbox", "new", "+", "--json"}, 2, "invalid_arguments"},
		{[]string{"integration", "add", "--json"}, 2, "invalid_arguments"},
		{[]string{"team", "member", "add", "--team", "dev", "--json"}, 2, "invalid_arguments"},
		{[]string{"token", "create", "--cmds", "*", "--ttl", "bad", "--key", "/not-read", "--json"}, 2, "invalid_arguments"},
		{[]string{"token", "create", "--cmds", "*", "--scope", "sandbox:0", "--key", "/not-read", "--json"}, 2, "invalid_arguments"},
		{[]string{"token", "create", "--cmds", ", ,", "--key", "/not-read", "--json"}, 2, "invalid_arguments"},
		{[]string{"msg", "tail", "--job", "one", "--workspace", "two", "--json"}, 2, "invalid_arguments"},
		{[]string{"token", "create", "--json"}, 2, "invalid_arguments"},
		{[]string{"secrets", "add-ssh-key", "--name", "fixture", "--json"}, 2, "invalid_arguments"},
		{[]string{"sandbox", "show", "nope", "--json"}, 2, "invalid_arguments"},
		{[]string{"sandbox", "show", "123", "--bogus", "--json"}, 2, "invalid_arguments"},
		{[]string{"sandbox", "show", "123", "extra", "--json"}, 2, "invalid_arguments"},
		{[]string{"sandbox", "list", "extra", "--json"}, 2, "invalid_arguments"},
		{[]string{"--json", "sandbox", "snapshot", "list", "123", "extra"}, 2, "invalid_arguments"},
		{[]string{"workspace", "fsck", "ws", "--repair", "--bogus", "--json"}, 2, "invalid_arguments"},
		{[]string{"session", "show", "id", "extra", "--json"}, 2, "invalid_arguments"},
		{[]string{"user", "show", "alice", "--json"}, 0, ""},
		{[]string{"user", "rm", "alice", "--json"}, 0, ""},
		{[]string{"team", "rm", "dev", "--json"}, 0, ""},
		{[]string{"team", "members", "dev", "--json"}, 0, ""},
		{[]string{"integration", "rm", "fixture", "--json"}, 0, ""},
		{[]string{"user", "show", "alice", "extra", "--json"}, 2, "invalid_arguments"},
		{[]string{"token", "inspect", "invalid-token", "extra", "--json"}, 2, "invalid_arguments"},
		{[]string{"token", "list", "extra", "--key", "/nonexistent", "--json"}, 2, "invalid_arguments"},
		{[]string{"status", "--timeout=0", "--json"}, 2, "invalid_arguments"},
		{[]string{"status", "--timeout=-1s", "--json"}, 2, "invalid_arguments"},
		{[]string{"token", "inspect", "malformed", "--json"}, 2, "invalid_arguments"},
		{[]string{"bootstrap", "--host", "fixture.example", "--ssh-port", "0", "--json"}, 2, "invalid_arguments"},
		{[]string{"bootstrap", "--host", "-invalid", "--json"}, 2, "invalid_arguments"},
		{[]string{"bootstrap", "--host", "fixture.example", "--ssh-user", "bad user", "--json"}, 2, "invalid_arguments"},
		{[]string{"bootstrap", "--json"}, 2, "invalid_arguments"},
		{[]string{"init", "--apply", "--control-port", "70000", "--json"}, 2, "invalid_arguments"},
		{[]string{"init", "--apply", "--backend", "imaginary", "--json"}, 2, "invalid_arguments"},
		{[]string{"init", "--apply", "--tailscale-serve", "--no-tailscale-serve", "--json"}, 2, "invalid_arguments"},
		{[]string{"status", "---json", "--json"}, 2, "invalid_arguments"},
		{[]string{"job", "run", "--repo", "--help", "--json"}, 2, "invalid_arguments"},
		{[]string{"version", "--bogus", "--json"}, 2, "invalid_arguments"},
		{[]string{"version", "extra", "--json"}, 2, "invalid_arguments"},
		{[]string{"sandbox", "prune", "--vmid", "888", "--json"}, 1, ""},
		{[]string{"sandbox", "prune", "--vmid", "invalid", "--json"}, 2, "invalid_arguments"},
	} {
		t.Run(strings.Join(test.args, " "), func(t *testing.T) {
			before := requests.Load()
			args := append([]string{"--endpoint", server.URL}, test.args...)
			cmd := exec.Command(binary, args...)
			cmd.Env = append(os.Environ(), "AGENTLAB_ENDPOINT=", "AGENTLAB_TOKEN=", "XDG_CONFIG_HOME="+config, "HOME="+config, "PATH="+hostTools+string(os.PathListSeparator)+os.Getenv("PATH"))
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			err := cmd.Run()
			exit := 0
			if err != nil {
				if e, ok := err.(*exec.ExitError); ok {
					exit = e.ExitCode()
				} else {
					t.Fatal(err)
				}
			}
			if exit != test.exit {
				t.Fatalf("exit %d want %d: %s %s", exit, test.exit, stdout.String(), stderr.String())
			}
			var payload map[string]any
			rawOutput := append([]byte{}, stdout.Bytes()...)
			decoder := json.NewDecoder(&stdout)
			if err := decoder.Decode(&payload); err != nil {
				t.Fatalf("JSON: %v %s", err, stdout.String())
			}
			if err := decoder.Decode(new(any)); err != io.EOF {
				t.Fatalf("expected exactly one JSON document: %v", err)
			}
			if test.code != "" && payload["code"] != test.code {
				t.Fatalf("code: %+v", payload)
			}
			if len(test.args) > 1 && test.args[1] == "wait" {
				observation, ok := payload["observation"].(map[string]any)
				if !ok || observation["cancelled_work"] != false || len(observation["next_commands"].([]any)) == 0 {
					t.Fatalf("missing observation/recovery: %s", stdout.String())
				}
				if !bytes.Contains(rawOutput, []byte("9007199254740993")) {
					t.Fatal("wait lost unknown large integer")
				}
			}
			if exit == 0 && strings.Contains(strings.Join(test.args, " "), "snapshot") {
				if payload["request_path"] != "/v1/sandboxes/123/snapshots" {
					t.Fatalf("snapshot target changed: %s", rawOutput)
				}
				if strings.Contains(strings.Join(test.args, " "), "save") {
					body, ok := payload["request_body"].(map[string]any)
					if !ok || body["name"] != "checkpoint" || body["force"] != true || payload["request_method"] != "POST" {
						t.Fatalf("snapshot flags lost: %s", rawOutput)
					}
				}
			}
			if stderr.Len() != 0 {
				t.Fatalf("unexpected stderr: %s", stderr.String())
			}
			if _, err := os.Stat(hostMarker); err == nil {
				t.Fatal("argument validation executed a host tool")
			}
			if exit == 2 && requests.Load() != before {
				t.Fatal("usage error performed a request")
			}
		})
	}
}

func TestSavedPreferencesApply(t *testing.T) {
	useTempClientConfig(t)
	if err := saveDefaults(map[string]string{"output-format": "json", "default-timeout": "3s", "default-socket": "/tmp/custom.sock", "default-profile": "custom"}); err != nil {
		t.Fatal(err)
	}
	opts, _, err := parseGlobal([]string{"status"})
	if err != nil {
		t.Fatal(err)
	}
	if !opts.jsonOutput || opts.timeout.Seconds() != 3 || opts.socketPath != "/tmp/custom.sock" {
		t.Fatalf("%+v", opts)
	}
	opts, _, err = parseGlobal([]string{"status", "--json=false", "--timeout=7s", "--socket=/tmp/explicit.sock"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.jsonOutput || opts.timeout.Seconds() != 7 || opts.socketPath != "/tmp/explicit.sock" {
		t.Fatalf("%+v", opts)
	}
}

func TestSavedProfileAndImageReachCreateRequest(t *testing.T) {
	useTempClientConfig(t)
	if err := saveDefaults(map[string]string{"default-profile": "saved", "default-image": "ubuntu:24.04"}); err != nil {
		t.Fatal(err)
	}
	var received sandboxCreateRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/profiles":
			w.Write([]byte(`{"profiles":[{"name":"saved"},{"name":"explicit"}]}`))
		case "/v1/sandboxes":
			if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
				t.Error(err)
			}
			w.Write([]byte(`{"vmid":123,"state":"RUNNING"}`))
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	base := commonFlags{endpoint: server.URL, jsonOutput: true, timeout: time.Second}
	for _, test := range []struct {
		args           []string
		profile, image string
	}{
		{nil, "saved", "ubuntu:24.04"},
		{[]string{"--profile", "explicit", "--image", "alpine:3"}, "explicit", "alpine:3"},
	} {
		output := captureStdout(t, func() {
			if err := runSandboxNew(context.Background(), test.args, base); err != nil {
				t.Fatal(err)
			}
		})
		if received.Profile != test.profile || received.Image != test.image {
			t.Fatalf("wrong effective create preferences: %+v", received)
		}
		if !strings.Contains(output, `"vmid": 123`) {
			t.Fatal(output)
		}
	}
	if err := runSandboxNew(context.Background(), []string{"--profile="}, base); !errors.Is(err, errUsage) {
		t.Fatalf("explicit empty profile silently used saved default: %v", err)
	}

}

func TestUnsupportedSavedPreferenceCanBeDeleted(t *testing.T) {
	useTempClientConfig(t)
	if err := saveDefaults(map[string]string{"default-backend": "docker"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := parseGlobal([]string{"status"}); err == nil {
		t.Fatal("unsupported preference was silently applied")
	}
	opts, args, err := parseGlobal([]string{"defaults", "delete", "default-backend"})
	if err != nil {
		t.Fatal(err)
	}
	captureStdout(t, func() {
		if err := dispatch(context.Background(), args, commonFlags{jsonOutput: opts.jsonOutput}); err != nil {
			t.Fatal(err)
		}
	})
	if _, _, err := parseGlobal([]string{"status"}); err != nil {
		t.Fatal(err)
	}
	path, err := defaultsFilePath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	_, _, err = parseGlobal([]string{"status"})
	if err == nil || !strings.Contains(err.Error(), path) {
		t.Fatalf("corrupt preferences did not identify their file: %v", err)
	}
}

func TestHelpWorksWithCorruptPreferences(t *testing.T) {
	useTempClientConfig(t)
	path, err := defaultsFilePath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := parseGlobal([]string{"--help"}); !errors.Is(err, errHelp) {
		t.Fatalf("root help blocked by preferences: %v", err)
	}
	for _, args := range [][]string{{"sandbox", "show", "--help"}, {"help", "sandbox", "show"}} {
		if _, _, err := parseGlobal(args); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}
}
