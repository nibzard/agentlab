// ABOUTME: HTTP client for communicating with agentlabd daemon over Unix socket.
// ABOUTME: Provides type-safe request/response structures and JSON serialization.

// Package main provides the HTTP client for communicating with agentlabd.
//
// The apiClient communicates with the agentlabd daemon over a Unix socket
// using HTTP. All responses are JSON-encoded.
//
// # API Client
//
// The client supports both JSON and raw HTTP operations:
//
//   - doJSON: For typical JSON request/response operations
//   - doRequest: For operations requiring custom headers or streaming responses
//
// # Error Handling
//
// API errors are returned as both HTTP status codes (>= 400) and JSON
// responses with an "error" field.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

const defaultSocketPath = "/run/agentlab/agentlabd.sock"

const (
	maxJSONOutputBytes = 4 << 20 // 4MB maximum JSON response size
)

// apiClient is an HTTP client for communicating with agentlabd over a Unix socket.
type apiClient struct {
	socketPath string
	httpClient *http.Client
	timeout    time.Duration
}

// apiError represents an error response from the agentlabd API.
type apiError struct {
	Error string `json:"error"`
}

// jobCreateRequest contains parameters for creating a new job.
type jobCreateRequest struct {
	RepoURL    string `json:"repo_url"`
	Ref        string `json:"ref,omitempty"`
	Profile    string `json:"profile"`
	Task       string `json:"task"`
	Mode       string `json:"mode,omitempty"`
	TTLMinutes *int   `json:"ttl_minutes,omitempty"`
	Keepalive  *bool  `json:"keepalive,omitempty"`
}

// jobResponse represents a job returned from the API.
type jobResponse struct {
	ID          string          `json:"id"`
	RepoURL     string          `json:"repo_url"`
	Ref         string          `json:"ref"`
	Profile     string          `json:"profile"`
	Task        string          `json:"task,omitempty"`
	Mode        string          `json:"mode,omitempty"`
	TTLMinutes  *int            `json:"ttl_minutes,omitempty"`
	Keepalive   bool            `json:"keepalive"`
	Status      string          `json:"status"`
	SandboxVMID *int            `json:"sandbox_vmid,omitempty"`
	Result      json.RawMessage `json:"result,omitempty"`
	Events      []eventResponse `json:"events,omitempty"`
	CreatedAt   string          `json:"created_at"`
	UpdatedAt   string          `json:"updated_at"`
}

// artifactInfo represents a single artifact uploaded from a sandbox.
type artifactInfo struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	SizeBytes int64  `json:"size_bytes"`
	Sha256    string `json:"sha256"`
	MIME      string `json:"mime,omitempty"`
	CreatedAt string `json:"created_at,omitempty"`
}

// artifactsResponse contains a list of artifacts for a job.
type artifactsResponse struct {
	JobID     string         `json:"job_id"`
	Artifacts []artifactInfo `json:"artifacts"`
}

// sandboxCreateRequest contains parameters for creating a new sandbox.
type sandboxCreateRequest struct {
	Name       string  `json:"name,omitempty"`
	Profile    string  `json:"profile"`
	Keepalive  *bool   `json:"keepalive,omitempty"`
	TTLMinutes *int    `json:"ttl_minutes,omitempty"`
	Workspace  *string `json:"workspace_id,omitempty"`
	VMID       *int    `json:"vmid,omitempty"`
	JobID      string  `json:"job_id,omitempty"`
}

// sandboxDestroyRequest contains parameters for destroying a sandbox.
type sandboxDestroyRequest struct {
	Force bool `json:"force"`
}

// sandboxRevertRequest contains parameters for reverting a sandbox to clean.
type sandboxRevertRequest struct {
	Force   bool  `json:"force"`
	Restart *bool `json:"restart,omitempty"`
}

// sandboxResponse represents a sandbox returned from the API.
type sandboxResponse struct {
	VMID          int     `json:"vmid"`
	Name          string  `json:"name"`
	Profile       string  `json:"profile"`
	State         string  `json:"state"`
	IP            string  `json:"ip,omitempty"`
	WorkspaceID   *string `json:"workspace_id,omitempty"`
	Keepalive     bool    `json:"keepalive"`
	LeaseExpires  *string `json:"lease_expires_at,omitempty"`
	CreatedAt     string  `json:"created_at"`
	LastUpdatedAt string  `json:"updated_at"`
}

// sandboxesResponse contains a list of sandboxes.
type sandboxesResponse struct {
	Sandboxes []sandboxResponse `json:"sandboxes"`
}

// profileResponse represents a profile returned from the API.
type profileResponse struct {
	Name         string `json:"name"`
	TemplateVMID int    `json:"template_vmid"`
	UpdatedAt    string `json:"updated_at"`
}

// profilesResponse contains a list of profiles.
type profilesResponse struct {
	Profiles []profileResponse `json:"profiles"`
}

// sandboxRevertResponse contains the result of a revert operation.
type sandboxRevertResponse struct {
	Sandbox    sandboxResponse `json:"sandbox"`
	Restarted  bool            `json:"restarted"`
	WasRunning bool            `json:"was_running"`
	Snapshot   string          `json:"snapshot"`
}

// leaseRenewRequest contains parameters for renewing a sandbox lease.
type leaseRenewRequest struct {
	TTLMinutes int `json:"ttl_minutes"`
}

// leaseRenewResponse contains the result of a lease renewal.
type leaseRenewResponse struct {
	VMID         int    `json:"vmid"`
	LeaseExpires string `json:"lease_expires_at"`
}

// pruneResponse contains the result of a prune operation.
type pruneResponse struct {
	Count int `json:"count"`
}

// workspaceCreateRequest contains parameters for creating a workspace.
type workspaceCreateRequest struct {
	Name    string `json:"name"`
	SizeGB  int    `json:"size_gb"`
	Storage string `json:"storage,omitempty"`
}

// workspaceAttachRequest contains parameters for attaching a workspace.
type workspaceAttachRequest struct {
	VMID int `json:"vmid"`
}

// workspaceRebindRequest contains parameters for rebinding a workspace to a new sandbox.
type workspaceRebindRequest struct {
	Profile    string `json:"profile"`
	TTLMinutes *int   `json:"ttl_minutes,omitempty"`
	KeepOld    bool   `json:"keep_old,omitempty"`
}

// workspaceResponse represents a workspace returned from the API.
type workspaceResponse struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Storage      string `json:"storage"`
	VolumeID     string `json:"volid"`
	SizeGB       int    `json:"size_gb"`
	AttachedVMID *int   `json:"attached_vmid,omitempty"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
}

// workspacesResponse contains a list of workspaces.
type workspacesResponse struct {
	Workspaces []workspaceResponse `json:"workspaces"`
}

// workspaceRebindResponse contains the result of a workspace rebind operation.
type workspaceRebindResponse struct {
	Workspace workspaceResponse `json:"workspace"`
	Sandbox   sandboxResponse   `json:"sandbox"`
	OldVMID   *int              `json:"old_vmid,omitempty"`
}

// eventResponse represents a single event from a sandbox.
type eventResponse struct {
	ID          int64           `json:"id"`
	Timestamp   string          `json:"ts"`
	Kind        string          `json:"kind"`
	SandboxVMID *int            `json:"sandbox_vmid,omitempty"`
	JobID       string          `json:"job_id,omitempty"`
	Message     string          `json:"msg,omitempty"`
	Payload     json.RawMessage `json:"json,omitempty"`
}

// eventsResponse contains a list of sandbox events.
type eventsResponse struct {
	Events []eventResponse `json:"events"`
	LastID int64           `json:"last_id,omitempty"`
}

// newAPIClient creates a new API client for communicating with agentlabd.
// The client uses HTTP over a Unix socket to communicate with the daemon.
func newAPIClient(socketPath string, timeout time.Duration) *apiClient {
	path := socketPath
	if path == "" {
		path = defaultSocketPath
	}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", path)
		},
	}
	return &apiClient{
		socketPath: path,
		httpClient: &http.Client{Transport: transport},
		timeout:    timeout,
	}
}

// doJSON sends an HTTP request with a JSON payload and returns the JSON response.
// It handles timeout, request serialization, and error parsing.
func (c *apiClient) doJSON(ctx context.Context, method, path string, payload any) ([]byte, error) {
	ctx, cancel := c.withTimeout(ctx)
	defer cancel()
	var body io.Reader
	if payload != nil {
		buf := &bytes.Buffer{}
		enc := json.NewEncoder(buf)
		if err := enc.Encode(payload); err != nil {
			return nil, err
		}
		body = buf
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://unix"+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request %s %s via %s: %w", method, path, c.socketPath, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxJSONOutputBytes))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, parseAPIError(resp.StatusCode, data)
	}
	return data, nil
}

// doRequest sends an HTTP request with a raw body and custom headers.
// Returns the raw HTTP response for streaming operations.
func (c *apiClient) doRequest(ctx context.Context, method, path string, body io.Reader, headers map[string]string) (*http.Response, error) {
	ctx, cancel := c.withTimeout(ctx)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, "http://unix"+path+"/"+strings.TrimPrefix(method, "/"), body)
	if err != nil {
		return nil, err
	}
	for key, value := range headers {
		if strings.TrimSpace(key) == "" {
			continue
		}
		req.Header.Set(key, value)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request %s %s via %s: %w", method, path, c.socketPath, err)
	}
	if resp.StatusCode >= 400 {
		data, readErr := io.ReadAll(io.LimitReader(resp.Body, maxJSONOutputBytes))
		_ = resp.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("request failed with status %d", resp.StatusCode)
		}
		return nil, parseAPIError(resp.StatusCode, data)
	}
	return resp, nil
}

// parseAPIError converts an HTTP error response into an error.
// It attempts to parse the response as JSON and extract the error message.
func parseAPIError(status int, data []byte) error {
	if len(data) > 0 {
		var apiErr apiError
		if err := json.Unmarshal(data, &apiErr); err == nil && apiErr.Error != "" {
			return errors.New(apiErr.Error)
		}
	}
	return fmt.Errorf("request failed with status %d", status)
}

// withTimeout adds the client's timeout to the context if configured.
func (c *apiClient) withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if c == nil || c.timeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, c.timeout)
}

// prettyPrintJSON formats JSON data with indentation and writes it to the writer.
func prettyPrintJSON(w io.Writer, data []byte) error {
	var out bytes.Buffer
	if err := json.Indent(&out, data, "", "  "); err != nil {
		_, err = w.Write(data)
		return err
	}
	out.WriteByte('\n')
	_, err := w.Write(out.Bytes())
	return err
}
