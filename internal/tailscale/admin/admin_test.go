package admin

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewClientRequiresAPIKey(t *testing.T) {
	t.Parallel()
	if _, err := NewClient("", ""); err == nil {
		t.Fatal("expected error for empty api key")
	}
}

func TestNewClientDefaultsTailnet(t *testing.T) {
	t.Parallel()
	c, err := NewClient("admin-api-key-fixture", "")
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	if c.tailnet != "-" {
		t.Fatalf("tailnet = %q want -", c.tailnet)
	}
	// An explicit tailnet is preserved verbatim.
	c2, err := NewClient("admin-api-key-fixture", "example.com")
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	if c2.tailnet != "example.com" {
		t.Fatalf("tailnet = %q want example.com", c2.tailnet)
	}
}

func TestCreateKeySuccess(t *testing.T) {
	t.Parallel()
	var (
		gotMethod string
		gotPath   string
		gotAuth   string
		gotUA     string
		gotBody   map[string]any
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotUA = r.Header.Get("User-Agent")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"k123","key":"fresh-auth-key-fixture","description":"agentlab vmid=42","expires":"2026-01-01T00:00:00Z"}`))
	}))
	defer srv.Close()

	c, err := NewClient("admin-api-key-fixture", "-", WithBaseURL(srv.URL))
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	resp, err := c.CreateKey(context.Background(), CreateKeyRequest{
		Capabilities: KeyCapabilities{Devices: KeyDeviceCapabilities{Create: KeyCreateCapabilities{
			Reusable: false, Ephemeral: true, Preauthorized: true, Tags: []string{"tag:agent"},
		}}},
		ExpirySeconds: 3600,
		Description:   "agentlab vmid=42",
	})
	if err != nil {
		t.Fatalf("create key: %v", err)
	}
	if resp.Key != "fresh-auth-key-fixture" {
		t.Fatalf("key = %q", resp.Key)
	}
	if resp.ID != "k123" {
		t.Fatalf("id = %q", resp.ID)
	}
	if gotMethod != http.MethodPost {
		t.Fatalf("method = %s", gotMethod)
	}
	if gotPath != "/tailnet/-/keys" {
		t.Fatalf("path = %s", gotPath)
	}
	if gotAuth != "Bearer admin-api-key-fixture" {
		t.Fatalf("auth header = %q", gotAuth)
	}
	if gotUA != "agentlab" {
		t.Fatalf("user-agent = %q", gotUA)
	}
	// The capabilities nesting must mirror the Admin API contract, and the
	// admin api key must never appear inside the request body.
	caps, _ := gotBody["capabilities"].(map[string]any)
	if caps == nil {
		t.Fatalf("missing capabilities in body: %#v", gotBody)
	}
	devices, _ := caps["devices"].(map[string]any)
	if devices == nil {
		t.Fatalf("missing devices: %#v", caps)
	}
	create, _ := devices["create"].(map[string]any)
	if create == nil {
		t.Fatalf("missing create: %#v", devices)
	}
	if create["ephemeral"] != true || create["preauthorized"] != true {
		t.Fatalf("unexpected create caps: %#v", create)
	}
	// reusable is single-use (false). It carries omitempty, so false is dropped
	// from the wire — assert it is never set true rather than literally false.
	if create["reusable"] == true {
		t.Fatalf("expected reusable to be unset/false (single-use), got %#v", create["reusable"])
	}
	if gotBody["expirySeconds"] != float64(3600) {
		t.Fatalf("expirySeconds = %#v", gotBody["expirySeconds"])
	}
	if gotBody["description"] != "agentlab vmid=42" {
		t.Fatalf("description = %#v", gotBody["description"])
	}
	if serialized, _ := json.Marshal(gotBody); strings.Contains(string(serialized), "admin-api-key-fixture") {
		t.Fatalf("admin api key leaked into request body: %s", serialized)
	}
}

func TestCreateKeyErrorOnNon2xx(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"unauthorized"}`))
	}))
	defer srv.Close()

	c, err := NewClient("admin-api-key-fixture", "-", WithBaseURL(srv.URL))
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	_, err = c.CreateKey(context.Background(), CreateKeyRequest{})
	if err == nil || !strings.Contains(err.Error(), "failed") {
		t.Fatalf("expected api failure error, got %v", err)
	}
	// The error must surface the upstream body, never echo the api key.
	if strings.Contains(err.Error(), "admin-api-key-fixture") {
		t.Fatalf("admin api key leaked into error: %v", err)
	}
}

func TestCreateKeyMissingKeyValue(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"id":"k123","key":""}`))
	}))
	defer srv.Close()

	c, err := NewClient("admin-api-key-fixture", "-", WithBaseURL(srv.URL))
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	_, err = c.CreateKey(context.Background(), CreateKeyRequest{})
	if err == nil || !strings.Contains(err.Error(), "missing key value") {
		t.Fatalf("expected missing key error, got %v", err)
	}
}

func TestCreateKeyContextCancel(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		// Never respond; the canceled context must abort the call.
		select {}
	}))
	defer srv.Close()

	c, err := NewClient("admin-api-key-fixture", "-", WithBaseURL(srv.URL))
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = c.CreateKey(ctx, CreateKeyRequest{})
	if err == nil {
		t.Fatal("expected error from canceled context")
	}
}

func TestNormalizeTags(t *testing.T) {
	t.Parallel()
	got := NormalizeTags([]string{"agent", "tag:prod", "", "  web  "})
	want := []string{"tag:agent", "tag:prod", "tag:web"}
	if len(got) != len(want) {
		t.Fatalf("tags = %#v want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("tags[%d] = %q want %q", i, got[i], want[i])
		}
	}
	if got := NormalizeTags(nil); len(got) != 0 {
		t.Fatalf("expected empty for nil input, got %#v", got)
	}
}
