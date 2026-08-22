package daemon

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type tsRunnerResponse struct {
	stdout string
	err    error
}

type tsStubRunner struct {
	responses map[string]tsRunnerResponse
	calls     []string
}

func (r *tsStubRunner) Run(_ context.Context, name string, args ...string) (string, error) {
	key := strings.Join(append([]string{name}, args...), " ")
	r.calls = append(r.calls, key)
	resp, ok := r.responses[key]
	if !ok {
		return "", fmt.Errorf("unexpected command: %s", key)
	}
	return resp.stdout, resp.err
}

// nonLoopbackIPv4 returns a bindable non-loopback IPv4 address of the host,
// or an empty string when every interface is loopback. Exposure targets may
// not be loopback addresses, so tests that need a reachable target dial a
// real interface address instead.
func nonLoopbackIPv4() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	for _, addr := range addrs {
		ipnet, ok := addr.(*net.IPNet)
		if !ok || ipnet.IP.IsLoopback() || ipnet.IP.IsLinkLocalUnicast() || ipnet.IP.To4() == nil {
			continue
		}
		return ipnet.IP.To4().String()
	}
	return ""
}

func TestTailscaleServePublisherPublishTCP(t *testing.T) {
	host := nonLoopbackIPv4()
	if host == "" {
		t.Skip("no bindable non-loopback address on this host")
	}
	listener, err := net.Listen("tcp", net.JoinHostPort(host, "0"))
	require.NoError(t, err)
	defer listener.Close()
	go func() {
		conn, _ := listener.Accept()
		if conn != nil {
			_ = conn.Close()
		}
	}()
	port := listener.Addr().(*net.TCPAddr).Port

	statusJSON := `{"Self":{"DNSName":"host.tailnet.ts.net."}}`
	runner := &tsStubRunner{responses: map[string]tsRunnerResponse{
		fmt.Sprintf("tailscale serve --tcp=%d tcp://%s:%d", port, host, port): {stdout: ""},
		"tailscale status --json": {stdout: statusJSON},
	}}
	publisher := &TailscaleServePublisher{Runner: runner}

	result, err := publisher.Publish(context.Background(), "web-1", host, port)
	require.NoError(t, err)
	require.Equal(t, fmt.Sprintf("tcp://host.tailnet.ts.net:%d", port), result.URL)
	require.Equal(t, exposureStateServing, result.State)
	require.Len(t, runner.calls, 2)
	require.Equal(t, fmt.Sprintf("tailscale serve --tcp=%d tcp://%s:%d", port, host, port), runner.calls[0])
	require.Equal(t, "tailscale status --json", runner.calls[1])
}

func TestTailscaleServePublisherPublishHTTPHealthy(t *testing.T) {
	host := nonLoopbackIPv4()
	if host == "" {
		t.Skip("no bindable non-loopback address on this host")
	}
	listener, err := net.Listen("tcp", net.JoinHostPort(host, "0"))
	require.NoError(t, err)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	server.Listener = listener
	server.Start()
	defer server.Close()
	port := listener.Addr().(*net.TCPAddr).Port

	statusJSON := `{"Self":{"DNSName":"host.tailnet.ts.net."}}`
	runner := &tsStubRunner{responses: map[string]tsRunnerResponse{
		fmt.Sprintf("tailscale serve --tcp=%d tcp://%s:%d", port, host, port): {stdout: ""},
		"tailscale status --json": {stdout: statusJSON},
	}}
	publisher := &TailscaleServePublisher{Runner: runner, HTTPPorts: map[int]struct{}{port: {}}}

	result, err := publisher.Publish(context.Background(), "web-2", host, port)
	require.NoError(t, err)
	require.Equal(t, exposureStateHealthy, result.State)
}

// TestTailscaleServePublisherPublishUnreachable covers the serve command
// shape and URL assembly on hosts where no live target is available. The
// TEST-NET-1 address passes the target policy (plain unicast) and never
// answers, so the exposure lands in the unhealthy state.
func TestTailscaleServePublisherPublishUnreachable(t *testing.T) {
	statusJSON := `{"Self":{"DNSName":"host.tailnet.ts.net."}}`
	runner := &tsStubRunner{responses: map[string]tsRunnerResponse{
		"tailscale serve --tcp=8123 tcp://192.0.2.10:8123": {stdout: ""},
		"tailscale status --json":                          {stdout: statusJSON},
	}}
	publisher := &TailscaleServePublisher{Runner: runner, TCPTimeout: 200 * time.Millisecond}

	result, err := publisher.Publish(context.Background(), "web-4", "192.0.2.10", 8123)
	require.NoError(t, err)
	require.Equal(t, "tcp://host.tailnet.ts.net:8123", result.URL)
	require.Equal(t, exposureStateUnhealthy, result.State)
	require.Len(t, runner.calls, 2)
	require.Equal(t, "tailscale serve --tcp=8123 tcp://192.0.2.10:8123", runner.calls[0])
}

// TestTailscaleServePublisherRejectsBadTargets covers the address classes the
// publisher refuses before any serve rule is installed (review F2, task T06).
func TestTailscaleServePublisherRejectsBadTargets(t *testing.T) {
	cases := []struct {
		label string
		ip    string
		want  string
	}{
		{"loopback", "127.0.0.1", "loopback"},
		{"ipv6 loopback", "::1", "loopback"},
		{"link-local", "169.254.169.254", "link-local"},
		{"unspecified", "0.0.0.0", "unspecified"},
		{"multicast", "239.1.1.1", "multicast"},
		{"invalid", "host.example", "not a valid ip"},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			runner := &tsStubRunner{}
			publisher := &TailscaleServePublisher{Runner: runner}

			_, err := publisher.Publish(context.Background(), "web-5", tc.ip, 8123)
			require.ErrorContains(t, err, tc.want)
			require.Empty(t, runner.calls, "no serve rule may be installed for a rejected target")
		})
	}
}

// TestTailscaleServePublisherEnforcesAgentSubnet covers the subnet half of
// the target policy: with AgentSubnet configured, a routable address outside
// the sandbox network is still refused.
func TestTailscaleServePublisherEnforcesAgentSubnet(t *testing.T) {
	_, subnet, err := net.ParseCIDR("10.77.0.0/16")
	require.NoError(t, err)
	runner := &tsStubRunner{}
	publisher := &TailscaleServePublisher{Runner: runner, AgentSubnet: subnet}

	_, err = publisher.Publish(context.Background(), "web-6", "192.0.2.10", 8123)
	require.ErrorContains(t, err, "outside the agent subnet")
	require.Empty(t, runner.calls)
}

func TestTailscaleServePublisherUnpublishNotFound(t *testing.T) {
	runner := &tsStubRunner{responses: map[string]tsRunnerResponse{
		"tailscale serve --tcp=2222 off": {err: errors.New("no serve config for port")},
	}}
	publisher := &TailscaleServePublisher{Runner: runner}

	err := publisher.Unpublish(context.Background(), "web-3", 2222)
	require.ErrorIs(t, err, ErrServeRuleNotFound)
}
