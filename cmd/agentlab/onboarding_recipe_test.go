package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
)

func TestDocumentedLaptopConnectWithFreshUserConfig(t *testing.T) {
	document, err := os.ReadFile("../../docs/tutorials/install-agentlab.md")
	if err != nil {
		t.Fatal(err)
	}
	match := regexp.MustCompile(`(?m)^\s*(agentlab connect --endpoint [^\n]+)\n\s*(agentlab status --json)`).FindStringSubmatch(string(document))
	if len(match) != 3 {
		t.Fatal("connect/status recipe missing")
	}
	dir := t.TempDir()
	binary := filepath.Join(dir, "agentlab")
	if out, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get("Authorization") != "Bearer fixture-token" {
			t.Errorf("saved token missing: %q", r.Header.Get("Authorization"))
		}
		if r.URL.Path != "/v1/status" && r.URL.Path != "/v1/host" {
			t.Errorf("fresh laptop performed unexpected action: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"api_schema_version":1,"event_schema_version":1,"sandboxes":{},"jobs":{}}`))
	}))
	defer server.Close()
	for i, line := range match[1:] {
		line = strings.ReplaceAll(strings.ReplaceAll(line, "https://host.example:8845", server.URL), "<token>", "fixture-token")
		args := strings.Fields(line)[1:]
		command := exec.Command(binary, args...)
		command.Env = append(os.Environ(), "AGENTLAB_ENDPOINT=", "AGENTLAB_TOKEN=", "XDG_CONFIG_HOME="+dir, "HOME="+dir)
		out, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("fresh recipe %s: %v %s", line, err, out)
		}
		if i == 1 && !strings.Contains(string(out), `"api_schema_version": 1`) {
			t.Fatalf("saved connection did not return status JSON: %s", out)
		}
	}
	if requests.Load() < 1 {
		t.Fatal("recipe never reached daemon")
	}
}
