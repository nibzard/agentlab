package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/agentlab/agentlab/internal/connection"
	"github.com/agentlab/agentlab/internal/hostidentity"
	"github.com/agentlab/agentlab/internal/protocol"
)

type refreshFailure struct {
	Status       int
	Code, Action string
}

func (e *refreshFailure) Error() string { return e.Action }

func (e *refreshFailure) ErrorMetadata() map[string]any {
	requirement := "verified_server_identity"
	switch e.Code {
	case "dashboard_credential_identity_unavailable":
		requirement = "compatible_server"
	case "dashboard_credential_rejected", "dashboard_credential_authority_changed", "dashboard_credential_lifetime_invalid":
		requirement = "daemon_credential"
	case "dashboard_credential_unavailable":
		requirement = "credential_store"
	case "dashboard_credential_verification_unavailable":
		requirement = "server_connection"
	}
	return map[string]any{"state": "blocked", "code": e.Code, "requirement": requirement, "action": e.Action, "retryable": e.Status == 503}
}

func credentialRefreshStatus(err error) *refreshFailure {
	var failure *refreshFailure
	if errors.As(err, &failure) {
		return failure
	}
	return &refreshFailure{503, "dashboard_credential_unavailable", "Restore access to the saved credential store, then select Refresh. No cached credential or local socket is used."}
}

func fixedCredentialError(err error) error {
	status := credentialRefreshStatus(err)
	if status.Code == "dashboard_credential_rejected" {
		return &refreshFailure{409, status.Code, "The startup credential cannot authenticate discovery. Restore its discovery permission or restart the dashboard with the intended credential."}
	}
	return status
}

// contextCredentials follows only the named source selected at startup. Its
// authorization baseline never changes, even after several credential rotations.
type contextCredentials struct {
	binding                              connection.CredentialBinding
	initialToken, currentToken, endpoint string
	client                               *http.Client
	gate                                 chan struct{}
	baseline                             *protocol.Capabilities
}

func newContextCredentials(binding connection.CredentialBinding, token, endpoint string, client *http.Client) *contextCredentials {
	gate := make(chan struct{}, 1)
	gate <- struct{}{}
	return &contextCredentials{binding: binding, initialToken: token, endpoint: endpoint, client: client, gate: gate}
}

func refreshBindingFailure() error {
	return &refreshFailure{409, "dashboard_context_changed", "The dashboard context or server binding changed. Restore the original binding, or close this dashboard and start a new browser session for the intended context."}
}

func verifyRemoteCapabilities(ctx context.Context, client *http.Client, endpoint, token, expected string) (protocol.Capabilities, error) {
	var caps protocol.Capabilities
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"/v1/capabilities", nil)
	if err != nil {
		return caps, credentialRefreshStatus(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if expected != "" {
		req.Header.Set(hostidentity.RequestHeader, expected)
	}
	response, err := client.Do(req)
	if err != nil {
		return caps, credentialRefreshStatus(err)
	}
	defer response.Body.Close()
	if response.StatusCode == 401 || response.StatusCode == 403 {
		return caps, &refreshFailure{409, "dashboard_credential_rejected", "The selected credential cannot authenticate discovery. Finish its pending renewal or restore its discovery permission, then select Refresh. No older credential is used."}
	}
	if response.StatusCode == http.StatusConflict {
		return caps, refreshBindingFailure()
	}
	if response.StatusCode != 200 {
		return caps, &refreshFailure{503, "dashboard_credential_verification_unavailable", "Server credential verification is unavailable. Restore the original server, then select Refresh."}
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 65537))
	if err != nil || len(data) > 65536 || json.Unmarshal(data, &caps) != nil || !caps.Compatible() {
		return caps, &refreshFailure{409, "dashboard_credential_identity_unavailable", "This server cannot verify dashboard request identity. Upgrade the daemon before using a remote dashboard."}
	}
	if caps.ServerIdentity == "" || (expected != "" && caps.ServerIdentity != expected) {
		return caps, refreshBindingFailure()
	}
	if !caps.Features["request_server_identity"] {
		return caps, &refreshFailure{409, "dashboard_credential_identity_unavailable", "The server cannot enforce request identity. Update it before using a remote dashboard."}
	}
	return caps, nil
}

func (c *contextCredentials) capabilities(ctx context.Context, token string) (protocol.Capabilities, error) {
	caps, err := verifyRemoteCapabilities(ctx, c.client, c.endpoint, token, c.binding.Context.ServerIdentity)
	if err != nil {
		return caps, err
	}
	if caps.Authorization.Method != "ssh-token" && caps.Authorization.Method != "legacy-token" {
		return caps, &refreshFailure{409, "dashboard_credential_identity_unavailable", "The remote authentication method cannot verify credential refresh. Restore authenticated remote access before continuing."}
	}
	if caps.Authorization.Method == "ssh-token" && caps.Authorization.Issuer == "" {
		return caps, &refreshFailure{409, "dashboard_credential_identity_unavailable", "The server does not report the signing issuer. Update it before using saved credential refresh."}
	}
	return caps, nil
}

func sameRefreshAuthority(a, b protocol.Authorization) bool {
	return a.Method == b.Method && a.Issuer == b.Issuer && a.Subject == b.Subject && slices.Equal(a.Commands, b.Commands) && slices.Equal(a.Scope, b.Scope)
}

func (c *contextCredentials) token(ctx context.Context, verifyServer bool) (string, error) {
	select {
	case <-ctx.Done():
		return "", credentialRefreshStatus(ctx.Err())
	case <-c.gate:
		defer func() { c.gate <- struct{}{} }()
	}
	expected := c.binding.Context
	if !filepath.IsAbs(c.binding.Root) || expected.Name == "" || expected.ServerIdentity == "" || expected.Endpoint != strings.TrimRight(c.endpoint, "/") || (expected.Transport != "https" && expected.Transport != "http") {
		return "", refreshBindingFailure()
	}
	if connection.ValidateEndpointPolicy(expected.Endpoint, expected.AllowInsecureHTTP) != nil {
		return "", refreshBindingFailure()
	}
	store := connection.ContextStore{Root: c.binding.Root}
	actual, _, found, err := store.LookupContext(ctx, expected.Name, true)
	if err != nil {
		return "", credentialRefreshStatus(err)
	}
	if !found || actual.Name != expected.Name || actual.Endpoint != expected.Endpoint || actual.Transport != expected.Transport || actual.ServerIdentity != expected.ServerIdentity || actual.AllowInsecureHTTP != expected.AllowInsecureHTTP || actual.JumpHost != expected.JumpHost || actual.JumpUser != expected.JumpUser {
		return "", refreshBindingFailure()
	}
	if c.baseline == nil {
		caps, err := c.capabilities(ctx, c.initialToken)
		if err != nil {
			return "", err
		}
		c.baseline = &caps
		c.currentToken = c.initialToken
	}
	cfg, err := store.LoadBoundContext(ctx, actual)
	if err != nil {
		return "", credentialRefreshStatus(err)
	}
	if cfg.Token == "" {
		return "", credentialRefreshStatus(nil)
	}
	if cfg.Token != c.currentToken || verifyServer {
		caps, err := c.capabilities(ctx, cfg.Token)
		if err != nil {
			return "", err
		}
		if !sameRefreshAuthority(c.baseline.Authorization, caps.Authorization) {
			return "", &refreshFailure{409, "dashboard_credential_authority_changed", "The credential issuer, subject, permissions, or scope changed. Close this dashboard and start a new browser session for the intended authority."}
		}
		if cfg.Token != c.currentToken && caps.Authorization.Method == "ssh-token" && (caps.Authorization.ExpiresAt <= time.Now().Unix() || caps.Authorization.ExpiresAt > time.Now().Add(24*time.Hour).Unix()) {
			return "", &refreshFailure{409, "dashboard_credential_lifetime_invalid", "The replacement credential must have a valid expiry within 24 hours. Enroll a finite task credential, then select Refresh."}
		}
		c.currentToken = cfg.Token
	}
	return cfg.Token, nil
}
