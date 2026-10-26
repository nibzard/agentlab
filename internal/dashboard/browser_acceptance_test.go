package dashboard

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"testing"
)

// Opt in with AGENTLAB_PLAYWRIGHT_MODULE pointing to playwright-core. Uses a
// separate headless browser profile and a local API fixture, never live VMs.
func TestBrowserAcceptance(t *testing.T) {
	module := os.Getenv("AGENTLAB_PLAYWRIGHT_MODULE")
	if module == "" {
		t.Skip("set AGENTLAB_PLAYWRIGHT_MODULE for real-browser acceptance")
	}
	s := testServer("")
	s.browserToken = "browser-fixture"
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleStatic)
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/status":
			w.Write([]byte(`{"sandboxes":{"RUNNING":1,"DESTROYED":101},"jobs":{"COMPLETED":1}}`))
		case "/api/v1/profiles":
			w.Write([]byte(`{"profiles":[{"name":"default"}]}`))
		case "/api/v1/sandboxes":
			if r.Method == http.MethodPost {
				if r.Header.Get("Idempotency-Key") == "" {
					t.Error("browser creation omitted retry key")
				}
				w.WriteHeader(202)
				w.Write([]byte(`{"id":"operation-fixture","state":"accepted","vmid":1002}`))
				return
			}
			records := []map[string]any{{"vmid": 1001, "name": "current-fixture", "profile": "default", "state": "RUNNING"}}
			for i := 0; i < 101; i++ {
				records = append(records, map[string]any{"vmid": 2000 + i, "name": fmt.Sprint("destroyed-", i), "profile": "default", "state": "DESTROYED"})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"sandboxes": records})
		case "/api/v1/operations/operation-fixture":
			w.Write([]byte(`{"id":"operation-fixture","state":"succeeded","vmid":1002,"result":{"vmid":1002}}`))
		case "/api/v1/jobs":
			w.Write([]byte(`{"jobs":[{"id":"job-fixture","status":"COMPLETED","repo_url":"repo","profile":"default"}]}`))
		case "/api/v1/workspaces":
			w.Write([]byte(`{"workspaces":[]}`))
		case "/api/v1/exposures":
			w.Write([]byte(`{"exposures":[]}`))
		default:
			w.Write([]byte(`{}`))
		}
	})
	server := httptest.NewServer(s.securityHeaders(s.inboundMiddleware(mux)))
	defer server.Close()
	command := exec.Command("node", "browser_acceptance.cjs", server.URL)
	command.Env = append(os.Environ(), "AGENTLAB_PLAYWRIGHT_MODULE="+module)
	out, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("browser acceptance: %v\n%s", err, out)
	}
	t.Log(string(out))
}
