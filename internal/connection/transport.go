// Package connection provides the transport shared by AgentLab interfaces.
package connection

import (
	"context"
	"net"
	"net/http"
	"strings"
	"time"
)

// Transport selects the remote endpoint when set. Otherwise it uses the local
// trusted socket. Redirects are disabled to keep credentials at the selected server.
func Transport(endpoint, socket string, timeout time.Duration) (string, *http.Client) {
	client := &http.Client{Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if strings.TrimSpace(endpoint) != "" {
		return strings.TrimRight(endpoint, "/"), client
	}
	if socket == "" {
		socket = "/run/agentlab/agentlabd.sock"
	}
	client.Transport = &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", socket)
	}}
	return "http://unix", client
}
