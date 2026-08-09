// ABOUTME: Minimal Tailscale Admin API client used by the agentlab daemon.
// ABOUTME: Supports minting per-VM auth keys with a tailnet API access token.

// Package admin is a minimal Tailscale Admin API client used by the agentlab
// daemon to mint per-VM auth keys. It intentionally supports only the auth-key
// creation endpoint and the tailnet-API-access-token (tskey-api-...) credential
// form; the broader CLI client in cmd/agentlab handles subnet-route approval
// with its own (OAuth-capable) implementation.
package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	// DefaultBaseURL is the Tailscale Admin API root.
	DefaultBaseURL = "https://api.tailscale.com/api/v2"
	// DefaultTimeout caps each Admin API call.
	DefaultTimeout = 10 * time.Second
)

// Client calls the Tailscale Admin API for a single tailnet.
type Client struct {
	baseURL    string
	tailnet    string
	authHeader string
	httpClient *http.Client
}

// Option configures a Client.
type Option func(*Client)

// WithHTTPClient overrides the underlying HTTP client (e.g. for tests).
func WithHTTPClient(h *http.Client) Option {
	return func(c *Client) {
		if h != nil {
			c.httpClient = h
		}
	}
}

// WithBaseURL overrides the API root (e.g. to point at a test server).
func WithBaseURL(u string) Option {
	return func(c *Client) {
		if strings.TrimSpace(u) != "" {
			c.baseURL = strings.TrimRight(u, "/")
		}
	}
}

// WithTimeout overrides the per-call timeout.
func WithTimeout(d time.Duration) Option {
	return func(c *Client) {
		if d > 0 {
			c.httpClient.Timeout = d
		}
	}
}

// NewClient returns a client authenticated with a tailnet API access token
// (tskey-api-...). tailnet is the tailnet organization name; pass "" or "-" to
// address the caller's own tailnet (the "-" wildcard path segment).
func NewClient(apiKey, tailnet string, opts ...Option) (*Client, error) {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return nil, errors.New("tailscale admin api key is required")
	}
	tailnet = strings.TrimSpace(tailnet)
	if tailnet == "" {
		tailnet = "-"
	}
	c := &Client{
		baseURL:    DefaultBaseURL,
		tailnet:    tailnet,
		authHeader: "Bearer " + apiKey,
		httpClient: &http.Client{Timeout: DefaultTimeout},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c, nil
}

// CreateKeyRequest describes a Tailscale auth key to create. The capabilities
// nesting mirrors the Tailscale Admin API and its Go/Terraform clients.
type CreateKeyRequest struct {
	Capabilities  KeyCapabilities `json:"capabilities"`
	ExpirySeconds int64           `json:"expirySeconds,omitempty"`
	Description   string          `json:"description,omitempty"`
}

// KeyCapabilities scopes a key's abilities to device creation.
type KeyCapabilities struct {
	Devices KeyDeviceCapabilities `json:"devices"`
}

// KeyDeviceCapabilities holds the device-creation capability set.
type KeyDeviceCapabilities struct {
	Create KeyCreateCapabilities `json:"create"`
}

// KeyCreateCapabilities are the properties of auth keys this credential can
// mint: single-use (reusable=false), auto-removing (ephemeral=true),
// pre-approved (preauthorized=true), and tagged.
type KeyCreateCapabilities struct {
	Reusable      bool     `json:"reusable,omitempty"`
	Ephemeral     bool     `json:"ephemeral,omitempty"`
	Preauthorized bool     `json:"preauthorized,omitempty"`
	Tags          []string `json:"tags,omitempty"`
}

// CreateKeyResponse is the Tailscale Admin API key-creation response. Key is the
// tskey-auth-... value and is returned ONLY on creation (never on later GETs).
type CreateKeyResponse struct {
	ID          string `json:"id"`
	Key         string `json:"key"`
	Description string `json:"description,omitempty"`
	Created     string `json:"created,omitempty"`
	Expires     string `json:"expires,omitempty"`
	Invalid     bool   `json:"invalid,omitempty"`
}

// CreateKey mints a new auth key via POST /tailnet/{tailnet}/keys. The returned
// Key is a secret and must be delivered only over a protected channel.
func (c *Client) CreateKey(ctx context.Context, req CreateKeyRequest) (CreateKeyResponse, error) {
	var resp CreateKeyResponse
	data, err := c.doJSON(ctx, http.MethodPost, fmt.Sprintf("/tailnet/%s/keys", url.PathEscape(c.tailnet)), nil, req)
	if err != nil {
		return resp, err
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return resp, fmt.Errorf("decode tailscale key response: %w", err)
	}
	if strings.TrimSpace(resp.Key) == "" {
		return resp, errors.New("tailscale key response missing key value")
	}
	return resp, nil
}

func (c *Client) doJSON(ctx context.Context, method, path string, query url.Values, body any) ([]byte, error) {
	urlStr := strings.TrimRight(c.baseURL, "/") + path
	if len(query) > 0 {
		urlStr = urlStr + "?" + query.Encode()
	}
	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, urlStr, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "agentlab")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.authHeader != "" {
		req.Header.Set("Authorization", c.authHeader)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("tailscale api %s %s failed: %s", method, path, strings.TrimSpace(string(data)))
	}
	return data, nil
}

// NormalizeTags ensures every tag carries the literal "tag:" prefix Tailscale
// requires, trimming whitespace and dropping empties. The minted key's tag set
// must be a superset of what the guest advertises via `tailscale up
// --advertise-tags`, so tags are passed through verbatim once prefixed.
func NormalizeTags(tags []string) []string {
	out := make([]string, 0, len(tags))
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			continue
		}
		if !strings.HasPrefix(tag, "tag:") {
			tag = "tag:" + tag
		}
		out = append(out, tag)
	}
	return out
}
