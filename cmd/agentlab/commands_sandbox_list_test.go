package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRunSandboxListVisibility(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		all  bool
		json bool
	}{
		{name: "default table"},
		{name: "all table", args: []string{"--all"}, all: true},
		{name: "short all table", args: []string{"-a"}, all: true},
		{name: "default JSON", json: true},
		{name: "all JSON", args: []string{"--all"}, all: true, json: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/v1/sandboxes" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
				_, _ = w.Write([]byte(`{"sandboxes":[{"vmid":1,"name":"running-vm","state":"RUNNING"},{"vmid":2,"name":"destroyed-vm","state":"DESTROYED"},{"vmid":3,"name":"stopped-vm","state":"STOPPED"},{"vmid":4,"name":"failed-vm","state":"FAILED"}]}`))
			}))
			defer srv.Close()
			out := captureStdout(t, func() {
				if err := runSandboxList(context.Background(), tc.args, commonFlags{
					endpoint: srv.URL, timeout: time.Second, jsonOutput: tc.json,
				}); err != nil {
					t.Fatal(err)
				}
			})
			for _, name := range []string{"running-vm", "stopped-vm", "failed-vm"} {
				if !strings.Contains(out, name) {
					t.Errorf("missing %s in output: %s", name, out)
				}
			}
			if strings.Contains(out, "destroyed-vm") != tc.all {
				t.Errorf("destroyed visibility should be %t: %s", tc.all, out)
			}
			if tc.json {
				var resp sandboxesResponse
				if err := json.Unmarshal([]byte(out), &resp); err != nil {
					t.Fatalf("invalid JSON: %v", err)
				}
				want := 3
				if tc.all {
					want = 4
				}
				if len(resp.Sandboxes) != want {
					t.Errorf("got %d records, want %d", len(resp.Sandboxes), want)
				}
			}
		})
	}
}

func TestRunSandboxListOnlyDestroyedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"sandboxes":[{"vmid":1,"state":"DESTROYED"}]}`))
	}))
	defer srv.Close()
	out := captureStdout(t, func() {
		if err := runSandboxList(context.Background(), nil, commonFlags{
			endpoint: srv.URL, timeout: time.Second, jsonOutput: true,
		}); err != nil {
			t.Fatal(err)
		}
	})
	var resp sandboxesResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Sandboxes == nil || len(resp.Sandboxes) != 0 {
		t.Fatalf("expected empty JSON array, got %s", out)
	}
}

func TestSandboxListPreservesUnknownJSON(t *testing.T) {
	for _, args := range [][]string{nil, {"--all"}, {"-a"}} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`{"future_envelope":{"cursor":9007199254740993},"sandboxes":[{"vmid":1,"state":"RUNNING","health":{"ready":true},"future_field":{"large":9007199254740993}},{"vmid":2,"state":"DESTROYED"}]}`))
		}))
		out := captureStdout(t, func() {
			if err := runSandboxList(context.Background(), args, commonFlags{endpoint: srv.URL, timeout: time.Second, jsonOutput: true}); err != nil {
				t.Fatal(err)
			}
		})
		srv.Close()
		for _, field := range []string{`"future_envelope"`, `"health"`, `"future_field"`, `9007199254740993`} {
			if !strings.Contains(out, field) {
				t.Errorf("lost %s: %s", field, out)
			}
		}
	}
}
