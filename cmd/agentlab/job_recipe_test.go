package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestDocumentedOneJobJourney(t *testing.T) {
	for _, tool := range []string{"bash", "jq"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(tool + " is required for the executable recipe")
		}
	}
	source, err := os.ReadFile("../../docs/how-to/drive-agentlab-as-a-coding-agent.md")
	if err != nil {
		t.Fatal(err)
	}
	_, recipe, ok := strings.Cut(string(source), "<!-- agentlab:one-job-recipe -->\n```bash\n")
	if !ok {
		t.Fatal("missing executable recipe")
	}
	recipe, _, ok = strings.Cut(recipe, "\n```\n<!-- agentlab:one-job-recipe:end -->")
	if !ok {
		t.Fatal("unterminated recipe")
	}
	binDir := t.TempDir()
	binary := filepath.Join(binDir, "agentlab")
	if out, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	for _, mode := range []string{"completed", "failed", "validation-failure", "wait-unavailable"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			outDir := filepath.Join(dir, "output")
			var mu sync.Mutex
			created, destroyed, downloads := 0, 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				switch r.Method + " " + r.URL.Path {
				case "GET /v1/schema":
					w.Write([]byte(`{"api_schema_version":1}`))
				case "GET /v1/profiles":
					w.Write([]byte(`{"profiles":[{"name":"fixture"}]}`))
				case "POST /v1/jobs/validate-plan":
					if mode == "validation-failure" {
						w.Write([]byte(`{"ok":false,"errors":[{"message":"fixture preflight rejection"}]}`))
					} else {
						w.Write([]byte(`{"ok":true}`))
					}
				case "POST /v1/jobs":
					var request jobCreateRequest
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
						t.Error(err)
					}
					if request.Keepalive == nil || !*request.Keepalive || request.Profile != "fixture" || request.Task != "run quoted task" {
						t.Errorf("recipe inputs lost: %+v", request)
					}
					created++
					w.WriteHeader(201)
					w.Write([]byte(`{"id":"job-fixture","status":"QUEUED","sandbox_vmid":1001}`))
				case "GET /v1/jobs/job-fixture":
					if mode == "wait-unavailable" {
						w.WriteHeader(503)
						w.Write([]byte(`{"error":"fixture unavailable"}`))
						return
					}
					state := "COMPLETED"
					if mode == "failed" {
						state = "FAILED"
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"id": "job-fixture", "status": state, "sandbox_vmid": 1001})
				case "GET /v1/jobs/job-fixture/artifacts":
					w.Write([]byte(`{"job_id":"job-fixture","artifacts":[{"name":"report.json","path":"job-fixture/report.json"}]}`))
				case "GET /v1/jobs/job-fixture/artifacts/download":
					if r.URL.Query().Get("path") != "job-fixture/report.json" {
						t.Error("artifact selector lost")
					}
					downloads++
					w.Write([]byte(`{"exit_code":0}`))
				case "POST /v1/sandboxes/1001/destroy":
					destroyed++
					w.Write([]byte(`{"vmid":1001,"state":"DESTROYED"}`))
				default:
					t.Errorf("extra/unknown resource request: %s %s", r.Method, r.URL.Path)
					http.Error(w, "unexpected route", 400)
				}
			}))
			defer server.Close()
			command := exec.Command("bash", "-c", recipe)
			command.Env = append(os.Environ(), "PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"), "AGENTLAB_ENDPOINT="+server.URL, "AGENTLAB_TOKEN=", "XDG_CONFIG_HOME="+dir, "HOME="+dir, "REPO=https://example.com/repo.git", "PROFILE=fixture", "TASK=run quoted task", "OUT_DIR="+outDir)
			out, err := command.CombinedOutput()
			exit := 0
			if err != nil {
				if status, ok := err.(*exec.ExitError); ok {
					exit = status.ExitCode()
				} else {
					t.Fatal(err)
				}
			}
			expected := 0
			if mode != "completed" {
				expected = 1
			}
			if exit != expected {
				t.Fatalf("exit %d want %d: %s", exit, expected, out)
			}
			mu.Lock()
			defer mu.Unlock()
			if mode == "validation-failure" {
				if created != 0 || destroyed != 0 || downloads != 0 {
					t.Fatal("rejected plan changed resources")
				}
				return
			}
			if created != 1 {
				t.Fatalf("expected one job-owned allocation, got %d", created)
			}
			if mode == "wait-unavailable" {
				if destroyed != 0 || downloads != 0 {
					t.Fatal("observation error cleaned up potentially active work")
				}
				if !bytes.Contains(out, []byte("job-fixture")) {
					t.Fatal("resume ID lost")
				}
				return
			}
			if destroyed != 1 || downloads != 1 {
				t.Fatalf("incomplete journey: destroy=%d downloads=%d", destroyed, downloads)
			}
			artifact, err := os.ReadFile(filepath.Join(outDir, "report.json"))
			if err != nil || string(artifact) != `{"exit_code":0}` {
				t.Fatalf("artifact: %s %v", artifact, err)
			}
		})
	}
}
