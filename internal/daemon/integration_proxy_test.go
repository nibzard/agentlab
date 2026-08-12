package daemon

import (
	"bytes"
	"context"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/agentlab/agentlab/internal/db"
	"github.com/agentlab/agentlab/internal/integrations"
	"github.com/agentlab/agentlab/internal/models"
)

func seedProxySandbox(t *testing.T, store *db.Store, vmid int, name string, state models.SandboxState, ip, tags string) {
	t.Helper()
	now := time.Now().UTC()
	if err := store.CreateSandbox(context.Background(), models.Sandbox{
		VMID:          vmid,
		Name:          name,
		Profile:       "default",
		State:         state,
		IP:            ip,
		Tags:          tags,
		CreatedAt:     now,
		LastUpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed sandbox %d: %v", vmid, err)
	}
}

func proxyTestStore(t *testing.T) *db.Store {
	return newTestStore(t)
}

// TestResolveSandbox_Identification verifies that only a unique LIVE sandbox is
// identified; unknown, stale, destroyed, ambiguous, and expired-lease sources
// are not (review H4).
func TestResolveSandbox_Identification(t *testing.T) {
	store := proxyTestStore(t)
	seedProxySandbox(t, store, 2001, "alpha", models.SandboxRunning, "10.77.0.2", "team-a")
	seedProxySandbox(t, store, 2002, "stale", models.SandboxDestroyed, "10.77.0.3", "team-b")
	seedProxySandbox(t, store, 2003, "stopped", models.SandboxStopped, "10.77.0.4", "")
	seedProxySandbox(t, store, 2004, "expired", models.SandboxTimeout, "10.77.0.5", "")
	// Two live sandboxes sharing an address → ambiguous.
	seedProxySandbox(t, store, 2005, "dup1", models.SandboxRunning, "10.77.0.6", "")
	seedProxySandbox(t, store, 2006, "dup2", models.SandboxReady, "10.77.0.6", "")

	api := NewIntegrationProxyAPI(nil, store, nil, nil, log.New(io.Discard, "", 0), false, false)

	cases := []struct {
		addr      string
		wantIdent bool
		wantName  string
	}{
		{"10.77.0.2:1234", true, "alpha"}, // unique live
		{"10.77.0.3:1234", false, ""},     // destroyed
		{"10.77.0.4:1234", false, ""},     // stopped
		{"10.77.0.5:1234", false, ""},     // expired (timeout state)
		{"10.77.0.6:1234", false, ""},     // ambiguous (two live)
		{"10.77.0.99:1234", false, ""},    // unknown
		{"", false, ""},                   // unspecified
	}
	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodGet, "/proxy/x", nil)
		req.RemoteAddr = tc.addr
		name, tags, identified := api.resolveSandbox(req)
		if identified != tc.wantIdent {
			t.Errorf("addr %q: identified=%v, want %v", tc.addr, identified, tc.wantIdent)
		}
		if tc.wantIdent && name != tc.wantName {
			t.Errorf("addr %q: name=%q, want %q", tc.addr, name, tc.wantName)
		}
		if tc.wantIdent && len(tags) == 0 {
			t.Errorf("addr %q: expected tags, got none", tc.addr)
		}
	}
}

func newProxyReq(method, path, addr string) *http.Request {
	req := httptest.NewRequest(method, path, nil)
	req.RemoteAddr = addr
	return req
}

// TestIntegrationProxy_DeniesUnidentified verifies that an unidentified source
// is rejected for every attachment mode when trust_agent_subnet is off (review
// H4). Previously auto:all admitted unidentified callers.
func TestIntegrationProxy_DeniesUnidentified(t *testing.T) {
	store := proxyTestStore(t)
	// No live sandbox at 10.77.0.99.
	intStore := integrationsTestStore(t, store)
	proxyTestIntStoreWithName(t, store, intStore, "auto", integrations.AttachAutoAll, "")
	proxyTestIntStoreWithName(t, store, intStore, "named", integrations.AttachSandbox, "alpha")
	proxyTestIntStoreWithName(t, store, intStore, "tagged", integrations.AttachTag, "team-a")
	subnet := mustParseCIDR(t, "10.77.0.0/16")
	api := NewIntegrationProxyAPI(intStore, store, subnet, nil, log.New(io.Discard, "", 0), false, false)

	mux := http.NewServeMux()
	api.Register(mux)

	for _, name := range []string{"auto", "named", "tagged"} {
		req := newProxyReq(http.MethodGet, "/proxy/"+name+"/info/refs", "10.77.0.99:1234")
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("integration %q unidentified source: code=%d, want 403", name, rec.Code)
		}
	}
}

// TestIntegrationProxy_AdmitsIdentifiedAndBypass verifies (a) a positively
// identified live sandbox is admitted and (b) trust_agent_subnet admits an
// unidentified source for auto:all only (review H4).
func TestIntegrationProxy_AdmitsIdentifiedAndBypass(t *testing.T) {
	store := proxyTestStore(t)
	seedProxySandbox(t, store, 2010, "alpha", models.SandboxRunning, "10.77.0.10", "team-a")
	intStore := integrationsTestStore(t, store)
	proxyTestIntStoreWithName(t, store, intStore, "auto", integrations.AttachAutoAll, "")
	subnet := mustParseCIDR(t, "10.77.0.0/16")

	t.Run("identified live sandbox is admitted (passes the gate)", func(t *testing.T) {
		var buf bytes.Buffer
		api := NewIntegrationProxyAPI(intStore, store, subnet, nil, log.New(&buf, "", 0), false, false)
		mux := http.NewServeMux()
		api.Register(mux)
		req := newProxyReq(http.MethodGet, "/proxy/auto/info/refs", "10.77.0.10:1234")
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		// The identification/attachment gate passed: the audit line was written
		// and the response is not the gate's "not identified" 403.
		if rec.Code == http.StatusForbidden && rec.Body.String() != "" &&
			contains(rec.Body.String(), "not identified") {
			t.Errorf("identified source was gated: code=%d body=%s", rec.Code, rec.Body.String())
		}
		if !contains(buf.String(), "credential-proxy:") {
			t.Errorf("expected audit log for admitted request, got: %s", buf.String())
		}
	})

	t.Run("trust_agent_subnet bypasses identification for auto:all", func(t *testing.T) {
		var buf bytes.Buffer
		api := NewIntegrationProxyAPI(intStore, store, subnet, nil, log.New(&buf, "", 0), false, true)
		mux := http.NewServeMux()
		api.Register(mux)
		req := newProxyReq(http.MethodGet, "/proxy/auto/info/refs", "10.77.0.99:1234")
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code == http.StatusForbidden && contains(rec.Body.String(), "not identified") {
			t.Errorf("trust-subnet source was gated: code=%d body=%s", rec.Code, rec.Body.String())
		}
		if !contains(buf.String(), "credential-proxy:") {
			t.Errorf("expected audit log for bypassed request, got: %s", buf.String())
		}
	})

	t.Run("trust_agent_subnet does not bypass sandbox/tag modes", func(t *testing.T) {
		proxyTestIntStoreWithName(t, store, intStore, "named", integrations.AttachSandbox, "alpha")
		api := NewIntegrationProxyAPI(intStore, store, subnet, nil, log.New(io.Discard, "", 0), false, true)
		mux := http.NewServeMux()
		api.Register(mux)
		req := newProxyReq(http.MethodGet, "/proxy/named/info/refs", "10.77.0.99:1234")
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("sandbox-mode unidentified with trust on: code=%d, want 403", rec.Code)
		}
	})
}

// integrationsTestStore returns a fresh integration store over the given db
// store, without seeding any integration.
func integrationsTestStore(t *testing.T, store *db.Store) *integrations.Store {
	t.Helper()
	enc := make([]byte, 32)
	intStore, err := integrations.NewStore(store, enc)
	if err != nil {
		t.Fatalf("new integration store: %v", err)
	}
	return intStore
}

// proxyTestIntStoreWithName creates an integration through an existing store.
func proxyTestIntStoreWithName(t *testing.T, store *db.Store, intStore *integrations.Store, name string, mode integrations.AttachmentMode, selector string) {
	t.Helper()
	if err := intStore.Create(context.Background(), &integrations.Integration{
		Name:           name,
		Type:           integrations.TypeGitProxy,
		AttachMode:     mode,
		AttachSelector: selector,
		Secret:         "test-secret",
	}); err != nil {
		t.Fatalf("create integration %s: %v", name, err)
	}
}

func contains(s, sub string) bool {
	return bytes.Contains([]byte(s), []byte(sub))
}
