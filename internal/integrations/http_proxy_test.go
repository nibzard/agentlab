package integrations

import (
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTTPProxyHandlerBearer(t *testing.T) {
	// Create an upstream server that echoes back the Authorization header.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("auth=" + auth + " path=" + r.URL.Path))
	}))
	defer upstream.Close()

	integ := &Integration{
		Name:       "myapi",
		Type:       TypeHTTPProxy,
		Target:     upstream.URL,
		Secret:     "sk-test-secret",
		SecretType: "bearer",
		AttachMode: AttachAutoAll,
	}

	handler := HTTPProxyHandler(integ, log.Default())
	proxy := httptest.NewServer(handler)
	defer proxy.Close()

	resp, err := http.Get(proxy.URL + "/proxy/myapi/v1/data?foo=bar")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	got := string(body)

	if !strings.Contains(got, "auth=Bearer sk-test-secret") {
		t.Errorf("response does not contain Bearer token: %s", got)
	}
	if !strings.Contains(got, "path=/v1/data") {
		t.Errorf("response does not contain correct path: %s", got)
	}
}

func TestHTTPProxyHandlerCustomHeader(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("X-Custom-Key")
		w.Write([]byte("key=" + key))
	}))
	defer upstream.Close()

	integ := &Integration{
		Name:         "custom",
		Type:         TypeHTTPProxy,
		Target:       upstream.URL,
		Secret:       "my-secret-key",
		SecretType:   "header",
		SecretHeader: "X-Custom-Key",
		AttachMode:   AttachAutoAll,
	}

	handler := HTTPProxyHandler(integ, log.Default())
	proxy := httptest.NewServer(handler)
	defer proxy.Close()

	resp, err := http.Get(proxy.URL + "/proxy/custom/test")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if string(body) != "key=my-secret-key" {
		t.Errorf("response = %s, want key=my-secret-key", body)
	}
}

func TestHTTPProxyHandlerBasicAuth(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		w.Write([]byte("ok=" + strings.TrimSpace(string(rune('0'+boolToInt(ok)))) + " user=" + user + " pass=" + pass))
	}))
	defer upstream.Close()

	integ := &Integration{
		Name:       "basicauth",
		Type:       TypeHTTPProxy,
		Target:     upstream.URL,
		Secret:     "my-password",
		SecretType: "basic-auth",
		Username:   "admin",
		AttachMode: AttachAutoAll,
	}

	handler := HTTPProxyHandler(integ, log.Default())
	proxy := httptest.NewServer(handler)
	defer proxy.Close()

	resp, err := http.Get(proxy.URL + "/proxy/basicauth/endpoint")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	got := string(body)

	if !strings.Contains(got, "user=admin") {
		t.Errorf("response missing user=admin: %s", got)
	}
	if !strings.Contains(got, "pass=my-password") {
		t.Errorf("response missing pass=my-password: %s", got)
	}
}

func TestHTTPProxyHandlerNilIntegration(t *testing.T) {
	handler := HTTPProxyHandler(nil, nil)
	proxy := httptest.NewServer(handler)
	defer proxy.Close()

	resp, err := http.Get(proxy.URL + "/proxy/test")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestHTTPProxyHandlerWrongType(t *testing.T) {
	integ := &Integration{
		Name:       "git",
		Type:       TypeGitProxy,
		Target:     "https://github.com",
		Secret:     "ghp-test",
		AttachMode: AttachAutoAll,
	}
	handler := HTTPProxyHandler(integ, nil)
	proxy := httptest.NewServer(handler)
	defer proxy.Close()

	resp, err := http.Get(proxy.URL + "/proxy/git/test")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
