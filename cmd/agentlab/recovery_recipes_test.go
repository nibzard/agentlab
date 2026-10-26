package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
)

// Run the actual documentation commands, not a separately transcribed argv list.
// All operations target a local wire fixture; no daemon, VM or volume is touched.
func TestDocumentedRecoveryRecipes(t *testing.T) {
	document, err := os.ReadFile("../../docs/how-to/recover-with-revert-and-fsck.md")
	if err != nil {
		t.Fatal(err)
	}
	commands := regexp.MustCompile(`(?m)^\s*(agentlab [^\n]+)$`).FindAllStringSubmatch(string(document), -1)
	if len(commands) < 8 {
		t.Fatal("recovery recipe extraction missed commands")
	}
	dir := t.TempDir()
	binary := filepath.Join(dir, "agentlab")
	if out, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	var mu sync.Mutex
	repaired, restarted, attached := false, false, false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.Method + " " + r.URL.Path {
		case "GET /v1/workspaces":
			w.Write([]byte(`{"workspaces":[{"id":"ws-fixture","name":"dev-workspace","size_gb":10}]}`))
		case "POST /v1/workspaces/dev-workspace/detach":
			w.Write([]byte(`{"id":"ws-fixture","name":"dev-workspace"}`))
		case "GET /v1/workspaces/dev-workspace/check":
			w.Write([]byte(`{"workspace":{"id":"ws-fixture"},"ok":true}`))
		case "POST /v1/workspaces/dev-workspace/fsck":
			var request struct {
				Repair bool `json:"repair"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
			}
			repaired = repaired || request.Repair
			w.Write([]byte(`{"workspace":{"id":"ws-fixture"},"exit_code":0,"status":"clean"}`))
		case "POST /v1/workspaces/dev-workspace/attach":
			var request struct {
				VMID int `json:"vmid"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
			}
			attached = request.VMID == 1001
			w.Write([]byte(`{"id":"ws-fixture","attached_vmid":1001}`))
		case "POST /v1/sandboxes/1001/revert":
			var request struct {
				Restart *bool `json:"restart"`
				Force   bool  `json:"force"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
			}
			restarted = request.Restart != nil && *request.Restart && request.Force
			w.Write([]byte(`{"vmid":1001,"state":"RUNNING","restarted":true}`))
		case "GET /v1/sandboxes/1001", "POST /v1/sandboxes/1001/touch":
			w.Write([]byte(`{"vmid":1001,"state":"RUNNING"}`))
		default:
			t.Errorf("undocumented request: %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected route", 400)
		}
	}))
	defer server.Close()
	for _, match := range commands {
		args := strings.Fields(strings.TrimSpace(match[1]))[1:]
		args = append([]string{"--endpoint", server.URL}, args...)
		command := exec.Command(binary, args...)
		command.Env = append(os.Environ(), "AGENTLAB_ENDPOINT=", "AGENTLAB_TOKEN=", "XDG_CONFIG_HOME="+dir, "HOME="+dir)
		if out, err := command.CombinedOutput(); err != nil {
			t.Fatalf("documented command %s: %v %s", match[1], err, out)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if !repaired || !restarted || !attached {
		t.Fatalf("post-ID flags or targets were lost: repair=%t restart=%t attach=%t", repaired, restarted, attached)
	}
}
