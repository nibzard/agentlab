// ABOUTME: Minimal Tailscale Admin API client for subnet route approval.

package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

const (
	tailscaleAdminBaseURL  = "https://api.tailscale.com/api/v2"
	tailscaleOAuthTokenURL = "https://api.tailscale.com/api/v2/oauth/token"
	defaultAdminTimeout    = 10 * time.Second
)

type tailscaleAdminClient struct {
	baseURL    string
	tailnet    string
	authHeader string
	httpClient *http.Client
}

type tailscaleDevice struct {
	ID               string   `json:"id"`
	NodeID           string   `json:"nodeId"`
	Name             string   `json:"name"`
	Hostname         string   `json:"hostname"`
	Addresses        []string `json:"addresses,omitempty"`
	AdvertisedRoutes []string `json:"advertisedRoutes,omitempty"`
	EnabledRoutes    []string `json:"enabledRoutes,omitempty"`
}

type tailscaleRoutesRequest struct {
	Routes []string `json:"routes"`
}

type tailscaleRoutesResponse struct {
	AdvertisedRoutes []string `json:"advertisedRoutes,omitempty"`
	EnabledRoutes    []string `json:"enabledRoutes,omitempty"`
}

type tailscaleOAuthTokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
}

type tailscaleRouteApproval struct {
	DeviceID   string
	DeviceName string
	Route      string
	Status     string
}

func newTailscaleAdminClient(ctx context.Context, cfg *tailscaleAdminConfig, timeout time.Duration) (*tailscaleAdminClient, error) {
	cfg = normalizeTailscaleAdminConfig(cfg)
	if cfg == nil || !cfg.hasCredentials() {
		return nil, errors.New("tailscale admin credentials not configured")
	}
	if timeout <= 0 {
		timeout = defaultAdminTimeout
	}
	tailnet := strings.TrimSpace(cfg.Tailnet)
	if tailnet == "" {
		tailnet = "-"
	}
	client := &tailscaleAdminClient{
		baseURL: tailscaleAdminBaseURL,
		tailnet: tailnet,
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}
	if cfg.APIKey != "" {
		client.authHeader = basicAuthHeader(cfg.APIKey)
		return client, nil
	}
	token, err := fetchTailscaleOAuthToken(ctx, cfg, timeout)
	if err != nil {
		return nil, err
	}
	client.authHeader = "Bearer " + token
	return client, nil
}

func fetchTailscaleOAuthToken(ctx context.Context, cfg *tailscaleAdminConfig, timeout time.Duration) (string, error) {
	if cfg == nil {
		return "", errors.New("oauth config missing")
	}
	form := url.Values{}
	form.Set("grant_type", "client_credentials")
	form.Set("client_id", strings.TrimSpace(cfg.OAuthClientID))
	form.Set("client_secret", strings.TrimSpace(cfg.OAuthClientSecret))
	if len(cfg.OAuthScopes) > 0 {
		form.Set("scope", strings.Join(cfg.OAuthScopes, " "))
	}
	if timeout <= 0 {
		timeout = defaultAdminTimeout
	}
	client := &http.Client{Timeout: timeout}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tailscaleOAuthTokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("oauth token request failed: %s", strings.TrimSpace(string(body)))
	}
	var tokenResp tailscaleOAuthTokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return "", err
	}
	if strings.TrimSpace(tokenResp.AccessToken) == "" {
		return "", errors.New("oauth token response missing access_token")
	}
	return strings.TrimSpace(tokenResp.AccessToken), nil
}

func (c *tailscaleAdminClient) listDevices(ctx context.Context) ([]tailscaleDevice, error) {
	query := url.Values{}
	query.Set("fields", "all")
	data, err := c.doJSON(ctx, http.MethodGet, fmt.Sprintf("/tailnet/%s/devices", url.PathEscape(c.tailnet)), query, nil)
	if err != nil {
		return nil, err
	}
	return decodeTailscaleDevices(data)
}

func (c *tailscaleAdminClient) getDeviceRoutes(ctx context.Context, deviceID string) (tailscaleRoutesResponse, error) {
	var resp tailscaleRoutesResponse
	data, err := c.doJSON(ctx, http.MethodGet, fmt.Sprintf("/device/%s/routes", url.PathEscape(deviceID)), nil, nil)
	if err != nil {
		return resp, err
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return resp, err
	}
	return resp, nil
}

func (c *tailscaleAdminClient) setDeviceRoutes(ctx context.Context, deviceID string, routes []string) (tailscaleRoutesResponse, error) {
	var resp tailscaleRoutesResponse
	payload := tailscaleRoutesRequest{Routes: routes}
	data, err := c.doJSON(ctx, http.MethodPost, fmt.Sprintf("/device/%s/routes", url.PathEscape(deviceID)), nil, payload)
	if err != nil {
		return resp, err
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return resp, err
	}
	return resp, nil
}

func (c *tailscaleAdminClient) doJSON(ctx context.Context, method, path string, query url.Values, body any) ([]byte, error) {
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

func decodeTailscaleDevices(data []byte) ([]tailscaleDevice, error) {
	var wrapper struct {
		Devices []tailscaleDevice `json:"devices"`
	}
	if err := json.Unmarshal(data, &wrapper); err == nil && wrapper.Devices != nil {
		return wrapper.Devices, nil
	}
	var devices []tailscaleDevice
	if err := json.Unmarshal(data, &devices); err != nil {
		return nil, err
	}
	return devices, nil
}

func basicAuthHeader(apiKey string) string {
	encoded := base64.StdEncoding.EncodeToString([]byte(apiKey + ":"))
	return "Basic " + encoded
}

func normalizeRouteCIDR(route string) string {
	route = strings.TrimSpace(route)
	if route == "" {
		return ""
	}
	if _, ipnet, err := net.ParseCIDR(route); err == nil {
		return ipnet.String()
	}
	return route
}

func uniqueRoutes(routes []string) []string {
	seen := make(map[string]struct{}, len(routes))
	out := make([]string, 0, len(routes))
	for _, route := range routes {
		normalized := normalizeRouteCIDR(route)
		if normalized == "" {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		out = append(out, normalized)
	}
	sort.Strings(out)
	return out
}

func containsRoute(routes []string, route string) bool {
	needle := normalizeRouteCIDR(route)
	if needle == "" {
		return false
	}
	for _, entry := range routes {
		if normalizeRouteCIDR(entry) == needle {
			return true
		}
	}
	return false
}

func pickDeviceID(device tailscaleDevice) string {
	if strings.TrimSpace(device.NodeID) != "" {
		return strings.TrimSpace(device.NodeID)
	}
	return strings.TrimSpace(device.ID)
}

func findDeviceByHints(devices []tailscaleDevice, hints []string) ([]tailscaleDevice, []tailscaleDevice) {
	matched := []tailscaleDevice{}
	if len(hints) == 0 {
		return matched, devices
	}
	for _, device := range devices {
		if matchDeviceHints(device, hints) {
			matched = append(matched, device)
			continue
		}
	}
	return matched, devices
}

func matchDeviceHints(device tailscaleDevice, hints []string) bool {
	name := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(device.Name), "."))
	hostname := strings.ToLower(strings.TrimSpace(device.Hostname))
	for _, hint := range hints {
		hint = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(hint), "."))
		if hint == "" {
			continue
		}
		if name != "" && hint == name {
			return true
		}
		if hostname != "" && hint == hostname {
			return true
		}
		if name != "" && strings.HasPrefix(name, hint+".") {
			return true
		}
	}
	return false
}

func findDeviceByRoute(devices []tailscaleDevice, route string) []tailscaleDevice {
	matches := []tailscaleDevice{}
	if route == "" {
		return matches
	}
	for _, device := range devices {
		if containsRoute(device.AdvertisedRoutes, route) {
			matches = append(matches, device)
		}
	}
	return matches
}

func approveTailscaleSubnetRoute(ctx context.Context, cfg *tailscaleAdminConfig, hints []string, route string) (tailscaleRouteApproval, error) {
	client, err := newTailscaleAdminClient(ctx, cfg, defaultAdminTimeout)
	if err != nil {
		return tailscaleRouteApproval{}, err
	}
	devices, err := client.listDevices(ctx)
	if err != nil {
		return tailscaleRouteApproval{}, err
	}
	matches, all := findDeviceByHints(devices, hints)
	if len(matches) == 0 {
		matches = findDeviceByRoute(all, route)
	}
	if len(matches) == 0 {
		return tailscaleRouteApproval{}, errors.New("no matching tailnet device found")
	}
	if len(matches) > 1 {
		return tailscaleRouteApproval{}, errors.New("multiple tailnet devices matched; provide a unique tailnet hostname")
	}
	device := matches[0]
	deviceID := pickDeviceID(device)
	if deviceID == "" {
		return tailscaleRouteApproval{}, errors.New("matched device missing id")
	}
	route = normalizeRouteCIDR(route)
	if route == "" {
		return tailscaleRouteApproval{}, errors.New("route is required")
	}
	enabled := uniqueRoutes(device.EnabledRoutes)
	advertised := device.AdvertisedRoutes
	if len(enabled) == 0 && len(advertised) == 0 {
		routesResp, err := client.getDeviceRoutes(ctx, deviceID)
		if err == nil {
			enabled = uniqueRoutes(routesResp.EnabledRoutes)
			advertised = routesResp.AdvertisedRoutes
		}
	}
	if containsRoute(enabled, route) {
		return tailscaleRouteApproval{DeviceID: deviceID, DeviceName: device.Name, Route: route, Status: "already-approved"}, nil
	}
	updated := uniqueRoutes(append(enabled, route))
	resp, err := client.setDeviceRoutes(ctx, deviceID, updated)
	if err != nil {
		return tailscaleRouteApproval{}, err
	}
	if !containsRoute(resp.EnabledRoutes, route) {
		return tailscaleRouteApproval{DeviceID: deviceID, DeviceName: device.Name, Route: route, Status: "pending"}, nil
	}
	return tailscaleRouteApproval{DeviceID: deviceID, DeviceName: device.Name, Route: route, Status: "approved"}, nil
}
