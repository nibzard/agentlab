package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestEffectivePreferenceOriginsAtProcessBoundary(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "agentlab")
	if out, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	run := func(endpoint string, args ...string) []byte {
		t.Helper()
		command := exec.Command(binary, args...)
		command.Env = append(os.Environ(), "HOME="+dir, "XDG_CONFIG_HOME="+dir, "AGENTLAB_ENDPOINT="+endpoint, "AGENTLAB_TOKEN=")
		out, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("%v: %v %s", args, err, out)
		}
		return out
	}
	type effective struct {
		Value  string `json:"value"`
		Origin string `json:"origin"`
	}
	decode := func(out []byte) map[string]effective {
		t.Helper()
		var values map[string]effective
		if err := json.Unmarshal(out, &values); err != nil {
			t.Fatalf("effective JSON: %v %s", err, out)
		}
		return values
	}
	built := decode(run("", "defaults", "effective", "--json"))
	if built["default-socket"].Origin != "built-in" || built["default-timeout"].Origin != "built-in" {
		t.Fatalf("built-in origins: %+v", built)
	}
	for _, pair := range [][2]string{{"output-format", "json"}, {"default-timeout", "3s"}, {"default-socket", "/tmp/saved.sock"}, {"default-profile", "fixture"}, {"default-image", "ubuntu:24.04"}} {
		run("", "defaults", "write", pair[0], pair[1])
	}
	saved := decode(run("", "defaults", "effective"))
	for key, want := range map[string]string{"output-format": "json", "default-timeout": "3s", "default-socket": "/tmp/saved.sock", "default-profile": "fixture", "default-image": "ubuntu:24.04"} {
		if saved[key].Value != want || saved[key].Origin != "saved preference" {
			t.Errorf("saved %s: %+v", key, saved[key])
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"version":"fixture","api_schema_version":1}`))
	}))
	defer server.Close()
	run("", "connect", "--endpoint", server.URL, "--token", "fixture-secret")
	connection := decode(run("", "defaults", "effective"))
	if connection["endpoint"].Value != server.URL || connection["endpoint"].Origin != "saved connection" {
		t.Fatalf("saved connection: %+v", connection)
	}
	environment := decode(run("http://127.0.0.1:1", "defaults", "effective"))
	if environment["endpoint"].Value != "http://127.0.0.1:1" || environment["endpoint"].Origin != "environment" {
		t.Fatalf("environment: %+v", environment)
	}
	explicit := decode(run("http://127.0.0.1:1", "defaults", "effective", "--endpoint", "http://127.0.0.1:2", "--socket", "/tmp/explicit.sock", "--timeout", "7s", "--json"))
	for key, want := range map[string]string{"endpoint": "http://127.0.0.1:2", "default-socket": "/tmp/explicit.sock", "default-timeout": "7s", "output-format": "json"} {
		if explicit[key].Value != want || explicit[key].Origin != "CLI flag" {
			t.Errorf("explicit %s: %+v", key, explicit[key])
		}
	}
	text := string(run("", "defaults", "effective", "--json=false"))
	if !strings.Contains(text, "output-format=text (CLI flag)") || strings.Contains(text, "fixture-secret") {
		t.Fatalf("false override or secret leak: %s", text)
	}
}
