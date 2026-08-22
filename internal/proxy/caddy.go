// Package proxy provides a Caddy-based reverse proxy integration for AgentLab.
//
// It manages automatic subdomain routing, TLS certificate provisioning, and
// DNS resolution for sandbox exposures. The proxy integrates with the daemon's
// existing ExposurePublisher interface, allowing sandboxes to be exposed at
// subdomain-based URLs (e.g., mybox.agentlab.local) with automatic TLS.
package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

// ErrRouteExists is returned by AddRoute when a route for the requested
// host is already installed. Call RemoveRoute first to replace a live
// route; AddRoute never displaces an existing route on its own.
var ErrRouteExists = errors.New("route already exists for host")

// Default Caddy admin API address.
const defaultCaddyAdminAPI = "http://localhost:2019"

// CaddyClient manages Caddy reverse proxy configuration via the admin API.
type CaddyClient struct {
	Endpoint   string
	HTTPClient *http.Client
	Logger     *log.Logger
}

// NewCaddyClient creates a client for the Caddy admin API.
func NewCaddyClient(endpoint string, logger *log.Logger) *CaddyClient {
	if strings.TrimSpace(endpoint) == "" {
		endpoint = defaultCaddyAdminAPI
	}
	if logger == nil {
		logger = log.Default()
	}
	return &CaddyClient{
		Endpoint: strings.TrimRight(endpoint, "/"),
		HTTPClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		Logger: logger,
	}
}

// caddyRoute represents a Caddy route for JSON configuration.
type caddyRoute struct {
	Match    []caddyMatch  `json:"match,omitempty"`
	Handle   []caddyHandle `json:"handle,omitempty"`
	Terminal bool          `json:"terminal,omitempty"`
}

type caddyMatch struct {
	Host []string `json:"host,omitempty"`
}

type caddyHandle struct {
	Handler  string        `json:"handler"`
	Upstream string        `json:"upstream,omitempty"`
	Headers  *caddyHeaders `json:"headers,omitempty"`
}

type caddyHeaders struct {
	Request  map[string][]string `json:"request,omitempty"`
	Response map[string][]string `json:"response,omitempty"`
}

// caddyConfigRequest is the body sent to Caddy's config endpoint.
type caddyConfigRequest struct {
	Routes []caddyRoute `json:"routes,omitempty"`
}

// AddRoute creates a reverse proxy route in Caddy.
// The route maps subdomain to the target address (host:port).
// It returns an error wrapping ErrRouteExists when a route for the
// subdomain is already installed. Removing the old route is left to
// the caller so a second publisher cannot silently displace a live
// route and hijack its hostname.
func (c *CaddyClient) AddRoute(ctx context.Context, subdomain, targetAddr string) error {
	routeID := routeID(subdomain)

	// First try to get existing routes to see if this one exists
	existing, err := c.getRoutes(ctx)
	if err != nil {
		// Caddy may not be running; that's OK for config-only mode
		c.Logger.Printf("proxy: caddy get routes: %v", err)
	}

	// Reject a duplicate host instead of replacing the live route.
	for _, r := range existing {
		for _, m := range r.Match {
			for _, h := range m.Host {
				if h == subdomain {
					return fmt.Errorf("%w: %s", ErrRouteExists, subdomain)
				}
			}
		}
	}

	route := caddyRoute{
		Match: []caddyMatch{
			{Host: []string{subdomain}},
		},
		Handle: []caddyHandle{
			{
				Handler:  "reverse_proxy",
				Upstream: targetAddr,
			},
		},
		Terminal: true,
	}

	body, err := json.Marshal(route)
	if err != nil {
		return fmt.Errorf("marshal caddy route: %w", err)
	}

	url := fmt.Sprintf("%s/config/apps/http/servers/srv0/routes/%s", c.Endpoint, routeID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create caddy request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("caddy api request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("caddy api returned %d: %s", resp.StatusCode, string(respBody))
	}

	c.Logger.Printf("proxy: added route %s -> %s", subdomain, targetAddr)
	return nil
}

// RemoveRoute removes a reverse proxy route from Caddy.
func (c *CaddyClient) RemoveRoute(ctx context.Context, subdomain string) error {
	routeID := routeID(subdomain)
	return c.removeRoute(ctx, routeID)
}

func (c *CaddyClient) removeRoute(ctx context.Context, id string) error {
	url := fmt.Sprintf("%s/config/apps/http/servers/srv0/routes/%s", c.Endpoint, id)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return fmt.Errorf("create caddy delete request: %w", err)
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("caddy api delete: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil
	}
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("caddy api delete returned %d: %s", resp.StatusCode, string(respBody))
	}

	c.Logger.Printf("proxy: removed route %s", id)
	return nil
}

// IsRunning checks if the Caddy admin API is reachable.
func (c *CaddyClient) IsRunning(ctx context.Context) bool {
	url := fmt.Sprintf("%s/config/", c.Endpoint)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func (c *CaddyClient) getRoutes(ctx context.Context) ([]caddyRoute, error) {
	url := fmt.Sprintf("%s/config/apps/http/servers/srv0/routes", c.Endpoint)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("caddy returned %d", resp.StatusCode)
	}

	var routes []caddyRoute
	if err := json.NewDecoder(resp.Body).Decode(&routes); err != nil {
		return nil, err
	}
	return routes, nil
}

// routeID generates a stable config path ID from a subdomain.
func routeID(subdomain string) string {
	// Caddy config paths use keys as identifiers; use a safe version.
	safe := strings.ReplaceAll(subdomain, ".", "-")
	return safe
}
