package tailcatexperiment

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAPITargetRejectsAlternateTargets(t *testing.T) {
	for _, target := range []string{"unix:///run/agentlab/agentlabd.sock", "http://localhost:8080", "http://192.0.2.1", "https://operator:secret@127.0.0.1", "http://127.0.0.1/v1", "http://127.0.0.1?target=other"} {
		if _, err := APITarget(target); err == nil {
			t.Fatal("accepted a target outside the fixed loopback API policy")
		}
	}
}
func TestAPIProxyPreservesBearerAndPinsServer(t *testing.T) {
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer fixture-token" || r.Header.Get(IdentityHeader) != "fixture-server" {
			t.Error("proxy changed request authority")
		}
		if r.Header.Get("X-Forwarded-Host") != "" {
			t.Error("proxy retained a caller's forwarded host")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()
	target, err := APITarget(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := APIProxy(target, "fixture-server", &http.Transport{Proxy: nil})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("POST", "http://other.invalid/v1/jobs", strings.NewReader("{}"))
	request.Header.Set("Authorization", "Bearer fixture-token")
	request.Header.Set("X-Forwarded-Host", "untrusted.invalid")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || calls != 1 {
		t.Fatal("fixed API forwarding failed")
	}
	request.Header.Set(IdentityHeader, "another-server")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusConflict || calls != 1 {
		t.Fatal("changed identity reached the API")
	}
	request = httptest.NewRequest("CONNECT", "http://other.invalid/private-shell", nil)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound || calls != 1 {
		t.Fatal("non-API target reached the upstream")
	}
}
