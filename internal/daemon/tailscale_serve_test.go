package daemon

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

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

func TestTailscaleServePublisherPublishTCP(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
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
		fmt.Sprintf("tailscale serve --tcp=%d tcp://127.0.0.1:%d", port, port): {stdout: ""},
		"tailscale status --json": {stdout: statusJSON},
	}}
	publisher := &TailscaleServePublisher{Runner: runner}

	result, err := publisher.Publish(context.Background(), "web-1", "127.0.0.1", port)
	require.NoError(t, err)
	require.Equal(t, fmt.Sprintf("tcp://host.tailnet.ts.net:%d", port), result.URL)
	require.Equal(t, exposureStateServing, result.State)
	require.Len(t, runner.calls, 2)
	require.Equal(t, fmt.Sprintf("tailscale serve --tcp=%d tcp://127.0.0.1:%d", port, port), runner.calls[0])
	require.Equal(t, "tailscale status --json", runner.calls[1])
}

func TestTailscaleServePublisherPublishHTTPHealthy(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	addr := strings.TrimPrefix(server.URL, "http://")
	host, portStr, err := net.SplitHostPort(addr)
	require.NoError(t, err)
	port, err := strconv.Atoi(portStr)
	require.NoError(t, err)

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

func TestTailscaleServePublisherUnpublishNotFound(t *testing.T) {
	runner := &tsStubRunner{responses: map[string]tsRunnerResponse{
		"tailscale serve --tcp=2222 off": {err: errors.New("no serve config for port")},
	}}
	publisher := &TailscaleServePublisher{Runner: runner}

	err := publisher.Unpublish(context.Background(), "web-3", 2222)
	require.ErrorIs(t, err, ErrServeRuleNotFound)
}
