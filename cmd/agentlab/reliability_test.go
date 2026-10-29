package main

import (
	"context"
	"encoding/json"
	"flag"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFlagsAfterOperand(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	force := fs.Bool("force", false, "")
	name := fs.String("name", "", "")
	if err := parseFlags(fs, []string{"1001", "--force", "--name", "my sandbox"}, func() {}, nil, true); err != nil {
		t.Fatal(err)
	}
	if !*force || *name != "my sandbox" || fs.NArg() != 1 || fs.Arg(0) != "1001" {
		t.Fatalf("flags or operand lost: %v", fs.Args())
	}
}

func TestJSONErrorAfterSubcommand(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	opts, _, err := parseGlobal([]string{"sandbox", "show", "invalid", "--json"})
	if err != nil {
		t.Fatal(err)
	}
	if !opts.jsonOutput {
		t.Fatal("JSON error mode not detected")
	}
}

func TestSavedPreferencesApply(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := saveDefaults(map[string]string{"output-format": "json", "default-timeout": "45s", "default-socket": "/tmp/preferred.sock"}); err != nil {
		t.Fatal(err)
	}
	opts, _, err := parseGlobal([]string{"status"})
	if err != nil {
		t.Fatal(err)
	}
	if !opts.jsonOutput || opts.timeout.String() != "45s" || opts.socketPath != "/tmp/preferred.sock" {
		t.Fatalf("preferences ignored: %+v", opts)
	}
	opts, _, err = parseGlobal([]string{"--json=false", "--timeout", "2s", "--socket", "/tmp/explicit.sock", "status"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.jsonOutput || opts.timeout.String() != "2s" || opts.socketPath != "/tmp/explicit.sock" {
		t.Fatalf("explicit flags lost: %+v", opts)
	}
}

func TestSavedProfileAppliesToCreationAndValidation(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := saveDefaults(map[string]string{"default-profile": "saved-profile"}); err != nil {
		t.Fatal(err)
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/profiles" {
			w.Write([]byte(`{"profiles":[{"name":"saved-profile"}]}`))
			return
		}
		requests++
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body["profile"] != "saved-profile" {
			t.Errorf("profile ignored: %v", body)
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/sandboxes/validate-plan" {
			w.Write([]byte(`{"ok":true,"plan":{}}`))
		} else {
			w.Write([]byte(`{"vmid":1001,"state":"RUNNING"}`))
		}
	}))
	defer server.Close()
	base := commonFlags{endpoint: server.URL, jsonOutput: true}
	for _, run := range []func(context.Context, []string, commonFlags) error{runSandboxNew, runSandboxValidate} {
		captureStdout(t, func() {
			if err := run(context.Background(), nil, base); err != nil {
				t.Fatal(err)
			}
		})
	}
	if requests != 2 {
		t.Fatalf("requests: %d", requests)
	}
}
