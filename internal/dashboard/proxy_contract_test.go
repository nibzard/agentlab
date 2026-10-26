package dashboard

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func TestProxyPreservesRequestTarget(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	seen := make(chan *http.Request, 1)
	daemon := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r
		w.Header().Set("X-Fixture", "preserved")
		w.WriteHeader(207)
		io.WriteString(w, `{"ok":true}`)
	})}
	go daemon.Serve(listener)
	defer daemon.Close()
	server := testServer(path)
	server.token = "daemon-token"
	for _, test := range []struct {
		name, method, path string
		handler            http.HandlerFunc
	}{
		{"get", "GET", "/api/v1/sandboxes/123/events", server.proxyGet},
		{"post", "POST", "/api/v1/sandboxes/prune", server.proxyPost},
		{"sandboxes", "POST", "/api/v1/sandboxes", server.proxySandboxes},
		{"jobs", "GET", "/api/v1/jobs", server.proxyJobs},
		{"sandbox action", "POST", "/api/v1/sandboxes/123/stop", server.proxySandboxAction},
		{"workspace action", "GET", "/api/v1/workspaces/name%2Fencoded/check", server.proxyWorkspaceAction},
		{"session action", "GET", "/api/v1/sessions/id", server.proxySessionAction},
		{"exposures", "GET", "/api/v1/exposures", server.proxyExposures},
		{"delete", "DELETE", "/api/v1/exposures/name", server.proxyDelete},
		{"messages", "GET", "/api/v1/messages", server.proxyMessages},
	} {
		t.Run(test.name, func(t *testing.T) {
			query := "?tail=2&after=7&scope_id=a%2Fb%20c&name=x%26y"
			req := httptest.NewRequest(test.method, test.path+query, nil)
			req.Header.Set("Authorization", "Bearer browser-token")
			req.Header.Set("Idempotency-Key", "fixture-key")
			rec := httptest.NewRecorder()
			test.handler(rec, req)
			if rec.Code != 207 {
				t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
			}
			upstream := <-seen
			if upstream.RequestURI != test.path[len("/api"):]+query {
				t.Fatalf("changed target %s", upstream.RequestURI)
			}
			if upstream.Header.Get("Authorization") != "Bearer daemon-token" || upstream.Header.Get("Idempotency-Key") != "fixture-key" {
				t.Fatal("incorrect outbound headers")
			}
			if rec.Header().Get("X-Fixture") != "preserved" {
				t.Fatal("response metadata lost")
			}
		})
	}
}
