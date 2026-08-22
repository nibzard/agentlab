package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/agentlab/agentlab/internal/auth"
	"github.com/agentlab/agentlab/internal/db"
	"github.com/agentlab/agentlab/internal/models"
	"github.com/agentlab/agentlab/internal/proxy"
	testutil "github.com/agentlab/agentlab/internal/testing"
	"github.com/stretchr/testify/require"
)

// newExposureGuardAPI builds a ControlAPI wired for the exposure create
// guard tests. The sandbox rows carry the given IPs.
func newExposureGuardAPI(t *testing.T, publisher ExposurePublisher, sandboxes ...testutil.SandboxOpts) *ControlAPI {
	t.Helper()
	store := newTestStore(t)
	api := NewControlAPI(store, map[string]models.Profile{}, nil, nil, nil, "", log.New(io.Discard, "", 0)).
		WithExposurePublisher(publisher).
		WithAgentSubnet("10.77.0.0/16")
	ctx := context.Background()
	for _, opts := range sandboxes {
		opts.CreatedAt = time.Now().UTC()
		opts.LastUpdatedAt = opts.CreatedAt
		require.NoError(t, store.CreateSandbox(ctx, testutil.NewTestSandbox(opts)))
	}
	return api
}

// postExposure serves a create request through the registered mux. A non-nil
// identity is injected the way the network auth middleware does.
func postExposure(t *testing.T, api *ControlAPI, id *auth.RequestIdentity, body string) (*httptest.ResponseRecorder, *fakeExposurePublisher) {
	t.Helper()
	publisher := &fakeExposurePublisher{publishResult: ExposurePublishResult{URL: "tcp://tailnet.example:8080", State: exposureStateServing}}
	api.exposurePublisher = publisher
	mux := http.NewServeMux()
	api.Register(mux)
	req := httptest.NewRequest(http.MethodPost, "/v1/exposures", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	if id != nil {
		mux.ServeHTTP(rec, req.WithContext(auth.WithIdentity(req.Context(), id)))
	} else {
		mux.ServeHTTP(rec, req)
	}
	return rec, publisher
}

func exposureScopedToken(commands ...string) *auth.RequestIdentity {
	return &auth.RequestIdentity{Method: "ssh-token", Token: &auth.Token{Claims: auth.TokenClaims{
		Commands: commands,
		Scope:    []string{"sandbox:101"},
	}}}
}

// TestExposureCreateRejectsClientTargetIP verifies the client can no longer
// choose the exposure target (review F2, task T04). The strict decoder
// refuses the removed field, and the published target always comes from the
// sandbox row.
func TestExposureCreateRejectsClientTargetIP(t *testing.T) {
	api := newExposureGuardAPI(t, nil, testutil.SandboxOpts{
		VMID: 101, Name: "sb-101", State: models.SandboxRunning, IP: "10.77.0.10",
	})

	rec, publisher := postExposure(t, api, nil, `{"name":"sbx-101-8080","vmid":101,"port":8080,"target_ip":"127.0.0.1"}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "target_ip")
	require.Zero(t, publisher.publishCalls)

	rec, publisher = postExposure(t, api, nil, `{"name":"sbx-101-8080","vmid":101,"port":8080}`)
	require.Equal(t, http.StatusCreated, rec.Code)
	require.Equal(t, 1, publisher.publishCalls)
	var created V1Exposure
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&created))
	require.Equal(t, "10.77.0.10", created.TargetIP)
}

// TestExposureCreateScopeEnforced verifies the create route resolves the
// requested vmid for the sandbox scope check (review F2, task T05).
func TestExposureCreateScopeEnforced(t *testing.T) {
	api := newExposureGuardAPI(t, nil,
		testutil.SandboxOpts{VMID: 101, Name: "sb-101", State: models.SandboxRunning, IP: "10.77.0.10"},
		testutil.SandboxOpts{VMID: 202, Name: "sb-202", State: models.SandboxRunning, IP: "10.77.0.20"},
	)
	scoped := exposureScopedToken("exposure.create")

	// Out-of-scope target: the token covers sandbox 101 only.
	rec, publisher := postExposure(t, api, scoped, `{"name":"sbx-202-8080","vmid":202,"port":8080}`)
	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Zero(t, publisher.publishCalls)

	// In-scope target passes authorization and still decodes the body after
	// the scope resolver buffered it.
	rec, publisher = postExposure(t, api, scoped, `{"name":"sbx-101-8080","vmid":101,"port":8080}`)
	require.Equal(t, http.StatusCreated, rec.Code)
	require.Equal(t, 1, publisher.publishCalls)

	// The permission itself is still enforced.
	rec, publisher = postExposure(t, api, exposureScopedToken("sandbox.read"), `{"name":"sbx-101-8080","vmid":101,"port":8080}`)
	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Zero(t, publisher.publishCalls)
}

// TestExposureCreateRejectsBadDerivedTargets verifies the target derived from
// the sandbox row is still checked against the address classes and the agent
// subnet (review F2, task T06).
func TestExposureCreateRejectsBadDerivedTargets(t *testing.T) {
	cases := []struct {
		name string
		ip   string
	}{
		{"loopback", "127.0.0.1"},
		{"ipv6 loopback", "::1"},
		{"link-local", "169.254.169.254"},
		{"ipv6 link-local", "fe80::1"},
		{"outside agent subnet", "8.8.8.8"},
		{"other private subnet", "192.168.1.50"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			api := newExposureGuardAPI(t, nil, testutil.SandboxOpts{
				VMID: 101, Name: "sb-101", State: models.SandboxRunning, IP: tc.ip,
			})
			rec, publisher := postExposure(t, api, nil, `{"name":"sbx-101-8080","vmid":101,"port":8080}`)
			require.Equal(t, http.StatusBadRequest, rec.Code)
			require.NotEmpty(t, rec.Body.String())
			require.Zero(t, publisher.publishCalls)
		})
	}
}

// TestExposureCreateRejectsInvalidNames verifies the subdomain-safe charset
// gate (review F3, task T09).
func TestExposureCreateRejectsInvalidNames(t *testing.T) {
	cases := []struct {
		name  string
		label string
	}{
		{"", "empty"},
		{"sbx'101", "single quote"},
		{`sbx"101`, "double quote"},
		{"SBX-202-443", "uppercase"},
		{"sbx_202", "underscore"},
		{"sbx 202", "space"},
		{"-sbx", "leading hyphen"},
		{strings.Repeat("a", 64), "too long"},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			api := newExposureGuardAPI(t, nil, testutil.SandboxOpts{
				VMID: 101, Name: "sb-101", State: models.SandboxRunning, IP: "10.77.0.10",
			})
			body := fmt.Sprintf(`{"name":%q,"vmid":101,"port":8080}`, tc.name)
			rec, publisher := postExposure(t, api, nil, body)
			require.Equal(t, http.StatusBadRequest, rec.Code)
			require.Zero(t, publisher.publishCalls)
		})
	}
}

// TestExposureCreateRejectsSubdomainCollision verifies uniqueness holds on
// the published subdomain, not on the raw name (review F8, task T22).
func TestExposureCreateRejectsSubdomainCollision(t *testing.T) {
	api := newExposureGuardAPI(t, nil,
		testutil.SandboxOpts{VMID: 202, Name: "sb-202", State: models.SandboxRunning, IP: "10.77.0.20"},
		testutil.SandboxOpts{VMID: 203, Name: "sb-203", State: models.SandboxRunning, IP: "10.77.0.30"},
	)
	ctx := context.Background()
	require.NoError(t, api.store.CreateExposure(ctx, db.Exposure{
		Name:      "sbx-202-443",
		VMID:      202,
		Port:      443,
		TargetIP:  "10.77.0.20",
		State:     "ready",
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}))

	// "sbx-202-443-" is a distinct valid name that publishes the same
	// hostname as the existing "sbx-202-443".
	rec, publisher := postExposure(t, api, nil, `{"name":"sbx-202-443-","vmid":203,"port":443}`)
	require.Equal(t, http.StatusConflict, rec.Code)
	require.Contains(t, rec.Body.String(), "subdomain")
	require.Zero(t, publisher.publishCalls)

	// The exact duplicate still conflicts as before.
	rec, publisher = postExposure(t, api, nil, `{"name":"sbx-202-443","vmid":203,"port":443}`)
	require.Equal(t, http.StatusConflict, rec.Code)
	require.Zero(t, publisher.publishCalls)

	// A free subdomain still works.
	rec, publisher = postExposure(t, api, nil, `{"name":"sbx-203-443","vmid":203,"port":443}`)
	require.Equal(t, http.StatusCreated, rec.Code)
	require.Equal(t, 1, publisher.publishCalls)
}

// TestExposureCreateRouteConflictMappedToConflict verifies a publisher that
// refuses to displace a live Caddy route surfaces as 409, not 500
// (review F8, task T23).
func TestExposureCreateRouteConflictMappedToConflict(t *testing.T) {
	api := newExposureGuardAPI(t, nil, testutil.SandboxOpts{
		VMID: 101, Name: "sb-101", State: models.SandboxRunning, IP: "10.77.0.10",
	})
	api.exposurePublisher = &fakeExposurePublisher{
		publishErr: fmt.Errorf("add caddy route: %w", proxy.ErrRouteExists),
	}
	mux := http.NewServeMux()
	api.Register(mux)
	req := httptest.NewRequest(http.MethodPost, "/v1/exposures", bytes.NewBufferString(`{"name":"sbx-101-8080","vmid":101,"port":8080}`))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	require.Equal(t, http.StatusConflict, rec.Code)
	require.Contains(t, rec.Body.String(), "route")
}
