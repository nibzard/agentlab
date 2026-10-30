//go:build integration

package tailcatexperiment

import (
	"context"
	"crypto/ed25519"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agentlab/agentlab/internal/auth"
	"github.com/agentlab/agentlab/internal/hostidentity"
	"github.com/tailscale/tailcat"
	"golang.org/x/crypto/ssh"
	"tailscale.com/envknob"
	"tailscale.com/net/netcheck"
	"tailscale.com/syncs"
	"tailscale.com/tailcfg"
	"tailscale.com/tstest/integration"
	"tailscale.com/types/key"
)

// The relay and STUN service belong to this test. Upstream's test controls
// disable public network and captive-portal probes.
func TestMain(m *testing.M) {
	envknob.Setenv("IN_TS_TEST", "true")
	netcheck.HookStartCaptivePortalDetection.SetForTest(func(context.Context, *netcheck.Client, *tailcfg.DERPMap, tailcfg.DERPRegionID, func(bool)) (<-chan struct{}, func()) {
		return syncs.ClosedChan(), func() {}
	})
	os.Exit(m.Run())
}
func TestLocalRelayPreservesAPIIdentityAndAccess(t *testing.T) {
	quiet := func(string, ...any) {}
	relay := integration.RunDERPAndSTUN(t, quiet, "127.0.0.1")
	clientIdentity, err := OpenIdentity(filepath.Join(t.TempDir(), "state", "identity.json"), "client")
	if err != nil {
		t.Fatalf("could not prepare the client identity: %v", err)
	}
	serverIdentityState, err := OpenIdentity(filepath.Join(t.TempDir(), "state", "identity.json"), "server")
	if err != nil {
		t.Fatalf("could not prepare the server identity: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	server, listener, err := serverIdentityState.Listen(ctx, []key.NodePublic{clientIdentity.PublicKey()}, relay.Regions[1])
	if err != nil {
		t.Fatal("private Tailcat relay did not start")
	}
	defer server.Close()

	_, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal("could not create the fixture signer")
	}
	signer, err := ssh.NewSignerFromKey(privateKey)
	if err != nil {
		t.Fatal("could not initialize the fixture signer")
	}
	keys, err := auth.ParseAuthorizedKeys(ssh.MarshalAuthorizedKey(signer.PublicKey()))
	if err != nil {
		t.Fatal("could not load the fixture public key")
	}
	middleware, err := auth.NewMiddlewareWithStore(keys, "", nil)
	if err != nil {
		t.Fatal("could not initialize shared authentication")
	}
	revocations := &prototypeRevocations{}
	middleware.WithRevocationChecker(revocations)
	serverIdentity, err := hostidentity.Local()
	if err != nil {
		t.Fatal("the fixture server identity is unavailable")
	}
	upstream := httptest.NewServer(middleware.WrapNetwork(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		caller := auth.FromContext(r.Context())
		if caller == nil || caller.Token == nil || !caller.Token.IsCommandAllowed("job.artifacts") || !caller.Token.IsSandboxAllowed(1001) {
			http.Error(w, "denied", 403)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"state":"ready"}`)
	})))
	defer upstream.Close()
	target, err := APITarget(upstream.URL)
	if err != nil {
		t.Fatal("invalid private API target")
	}
	handler, err := APIProxy(target, serverIdentity, nil)
	if err != nil {
		t.Fatal("proxy policy did not initialize")
	}
	api := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	done := make(chan error, 1)
	go func() { done <- api.Serve(listener) }()
	defer api.Close()
	client, err := clientIdentity.Client(server.TailcatAddr())
	if err != nil {
		t.Fatal("could not prepare the client connection")
	}
	defer client.Close()
	transport := &http.Transport{Proxy: nil, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) { return client.DialTCPPort(ctx, 8843) }}
	defer transport.CloseIdleConnections()
	httpClient := &http.Client{Transport: transport, Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	check := func(token string, expected int) {
		request, err := http.NewRequestWithContext(ctx, "GET", "http://tailcat.invalid/v1/jobs/job-fixture/artifacts", nil)
		if err != nil {
			t.Fatal("invalid fixture request")
		}
		request.Header.Set("Authorization", "Bearer "+token)
		response, err := httpClient.Do(request)
		if err != nil {
			t.Fatal("approved peer could not reach the authenticated API")
		}
		io.Copy(io.Discard, response.Body)
		response.Body.Close()
		if response.StatusCode != expected {
			t.Fatal("transport changed the shared authentication or scope decision")
		}
	}
	create := func(commands, scope []string, ttl time.Duration) string {
		raw, err := auth.CreateToken(signer, auth.TokenCreateRequest{Commands: commands, Scope: scope, TTL: ttl})
		if err != nil {
			t.Fatal("could not create fixture credential")
		}
		return raw
	}
	allowed := create([]string{"job.artifacts"}, []string{"sandbox:1001"}, time.Hour)
	check(allowed, http.StatusOK)
	check(create([]string{"job.read"}, []string{"sandbox:1001"}, time.Hour), http.StatusForbidden)
	check(create([]string{"job.artifacts"}, []string{"sandbox:1002"}, time.Hour), http.StatusForbidden)
	check(create([]string{"job.artifacts"}, []string{"sandbox:1001"}, -time.Hour), http.StatusUnauthorized)
	revocations.revoked.Store(true)
	check(allowed, http.StatusUnauthorized)
	intruder := &tailcat.Client{Server: server.TailcatAddr(), Key: key.NewNode(), Logf: quiet}
	defer intruder.Close()
	deniedCtx, stop := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer stop()
	conn, err := intruder.DialTCPPort(deniedCtx, 8843)
	if err == nil {
		conn.Close()
		t.Fatal("an unapproved transport peer reached the API")
	}
	if err := api.Close(); err != nil {
		t.Fatal("API listener did not close")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("API worker did not stop")
	}
}

type prototypeRevocations struct{ revoked atomic.Bool }

func (r *prototypeRevocations) TokenRevoked(context.Context, string, string) (bool, error) {
	return r.revoked.Load(), nil
}

func TestPersistedIdentityRestartsLocalListener(t *testing.T) {
	quiet := func(string, ...any) {}
	relay := integration.RunDERPAndSTUN(t, quiet, "127.0.0.1")
	root := filepath.Join(t.TempDir(), "state")
	serverPath := filepath.Join(root, "server.json")
	clientPath := filepath.Join(root, "client.json")
	var peer tailcat.Addr
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	for attempt := 0; attempt < 2; attempt++ {
		serverIdentity, err := OpenIdentity(serverPath, "server")
		if err != nil {
			t.Fatalf("server identity did not reopen: %v", err)
		}
		clientIdentity, err := OpenIdentity(clientPath, "client")
		if err != nil {
			t.Fatalf("client identity did not reopen: %v", err)
		}
		server, listener, err := serverIdentity.Listen(ctx, []key.NodePublic{clientIdentity.PublicKey()}, relay.Regions[1])
		if err != nil {
			t.Fatal("listener did not start with the saved identity")
		}
		current := server.TailcatAddr()
		if attempt == 0 {
			peer = current
		} else if current != peer {
			server.Close()
			t.Fatal("the private transport address changed across restart")
		}
		endpoint := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "ready") }), ReadHeaderTimeout: time.Second}
		stopped := make(chan error, 1)
		go func() { stopped <- endpoint.Serve(listener) }()
		client, err := clientIdentity.Client(peer)
		if err != nil {
			endpoint.Close()
			server.Close()
			t.Fatal("client identity did not initialize")
		}
		transport := &http.Transport{Proxy: nil, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) { return client.DialTCPPort(ctx, 8843) }}
		requester := &http.Client{Transport: transport, Timeout: 5 * time.Second}
		response, requestError := requester.Get("http://tailcat.invalid/v1/fixture")
		var body []byte
		if requestError == nil {
			body, requestError = io.ReadAll(io.LimitReader(response.Body, 100))
			response.Body.Close()
		}
		transport.CloseIdleConnections()
		client.Close()
		endpoint.Close()
		server.Close()
		select {
		case <-stopped:
		case <-time.After(time.Second):
			t.Fatal("listener worker did not stop")
		}
		if requestError != nil || string(body) != "ready" {
			t.Fatal("the approved peer did not reconnect with the saved identity")
		}
	}
}
