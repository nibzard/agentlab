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
)

const defaultSocketPath = "/run/agentlab/agentlabd.sock"

const (
	maxJSONOutputBytes = 4 << 20
)

type apiClient struct {
	socketPath string
	httpClient *http.Client
}

type apiError struct {
	Error string `json:"error"`
}

type jobCreateRequest struct {
	RepoURL    string `json:"repo_url"`
	Ref        string `json:"ref,omitempty"`
	Profile    string `json:"profile"`
	Task       string `json:"task"`
	Mode       string `json:"mode,omitempty"`
	TTLMinutes *int   `json:"ttl_minutes,omitempty"`
	Keepalive  bool   `json:"keepalive,omitempty"`
}

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
	CreatedAt   string          `json:"created_at"`
	UpdatedAt   string          `json:"updated_at"`
}

type sandboxCreateRequest struct {
	Name       string  `json:"name,omitempty"`
	Profile    string  `json:"profile"`
	Keepalive  bool    `json:"keepalive,omitempty"`
	TTLMinutes *int    `json:"ttl_minutes,omitempty"`
	Workspace  *string `json:"workspace_id,omitempty"`
	VMID       *int    `json:"vmid,omitempty"`
	JobID      string  `json:"job_id,omitempty"`
}

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

type sandboxesResponse struct {
	Sandboxes []sandboxResponse `json:"sandboxes"`
}

type leaseRenewRequest struct {
	TTLMinutes int `json:"ttl_minutes"`
}

type leaseRenewResponse struct {
	VMID         int    `json:"vmid"`
	LeaseExpires string `json:"lease_expires_at"`
}

type eventResponse struct {
	ID          int64           `json:"id"`
	Timestamp   string          `json:"ts"`
	Kind        string          `json:"kind"`
	SandboxVMID *int            `json:"sandbox_vmid,omitempty"`
	JobID       string          `json:"job_id,omitempty"`
	Message     string          `json:"msg,omitempty"`
	Payload     json.RawMessage `json:"json,omitempty"`
}

type eventsResponse struct {
	Events []eventResponse `json:"events"`
	LastID int64           `json:"last_id,omitempty"`
}

func newAPIClient(socketPath string) *apiClient {
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
	}
}

func (c *apiClient) doJSON(ctx context.Context, method, path string, payload any) ([]byte, error) {
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

func parseAPIError(status int, data []byte) error {
	if len(data) > 0 {
		var apiErr apiError
		if err := json.Unmarshal(data, &apiErr); err == nil && apiErr.Error != "" {
			return errors.New(apiErr.Error)
		}
	}
	return fmt.Errorf("request failed with status %d", status)
}

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
