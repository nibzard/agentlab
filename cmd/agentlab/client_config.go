// ABOUTME: Client-side configuration for remote control plane access.
// ABOUTME: Handles XDG config storage, environment overrides, and endpoint normalization.

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

const (
	clientConfigDir  = "agentlab"
	clientConfigFile = "client.json"

	envEndpoint                   = "AGENTLAB_ENDPOINT"
	envToken                      = "AGENTLAB_TOKEN"
	envAllowInsecureHTTP          = "AGENTLAB_ALLOW_INSECURE_HTTP"
	envTailscaleTailnet           = "AGENTLAB_TAILSCALE_TAILNET"
	envTailscaleAPIKey            = "AGENTLAB_TAILSCALE_API_KEY"
	envTailscaleOAuthClientID     = "AGENTLAB_TAILSCALE_OAUTH_CLIENT_ID"
	envTailscaleOAuthClientSecret = "AGENTLAB_TAILSCALE_OAUTH_CLIENT_SECRET"
	envTailscaleOAuthScopes       = "AGENTLAB_TAILSCALE_OAUTH_SCOPES"
)

type clientConfig struct {
	Endpoint          string                `json:"endpoint,omitempty"`
	Token             string                `json:"token,omitempty"`
	JumpHost          string                `json:"jump_host,omitempty"`
	JumpUser          string                `json:"jump_user,omitempty"`
	AllowInsecureHTTP bool                  `json:"allow_insecure_http,omitempty"`
	TailscaleAdmin    *tailscaleAdminConfig `json:"tailscale_admin,omitempty"`
}

// clientConfigRootOverride, when non-empty, forces clientConfigBaseDir (and
// therefore clientConfigPath / defaultsFilePath) to resolve beneath the given
// directory. It exists for test isolation: os.UserConfigDir ignores
// XDG_CONFIG_HOME on Darwin, so tests that only set XDG_CONFIG_HOME would
// otherwise read or mutate the developer's real configuration on macOS.
// Production code must never set it.
var clientConfigRootOverride string

// clientConfigBaseDir returns the directory holding agentlab's client-side
// configuration (client.json, defaults.json). It honors a test override first,
// then falls back to the platform default from os.UserConfigDir.
func clientConfigBaseDir() (string, error) {
	if clientConfigRootOverride != "" {
		return filepath.Join(clientConfigRootOverride, clientConfigDir), nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, clientConfigDir), nil
}

func clientConfigPath() (string, error) {
	dir, err := clientConfigBaseDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, clientConfigFile), nil
}

// requireConfigPathSafe rejects a path that escapes the active test override
// root. It is a no-op in production (no override set). This prevents a test
// that hard-codes a path, or a future code path that resolves config outside
// clientConfigBaseDir, from touching the real user configuration.
func requireConfigPathSafe(path string) error {
	if clientConfigRootOverride == "" {
		return nil
	}
	rel, err := filepath.Rel(clientConfigRootOverride, path)
	if err != nil {
		return fmt.Errorf("refusing to touch config path %q outside test root: %w", path, err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("refusing to touch config path %q outside test root", path)
	}
	return nil
}

func loadClientConfig() (clientConfig, bool, error) {
	path, err := clientConfigPath()
	if err != nil {
		return clientConfig{}, false, err
	}
	return loadClientConfigFrom(path)
}

func loadClientConfigFrom(path string) (clientConfig, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return clientConfig{}, false, nil
		}
		return clientConfig{}, false, err
	}
	var cfg clientConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return clientConfig{}, false, fmt.Errorf("invalid client config: %w", err)
	}
	cfg = normalizeClientConfig(cfg)
	if err := enforceClientConfigPermissions(path); err != nil {
		return clientConfig{}, false, err
	}
	return cfg, true, nil
}

func writeClientConfig(path string, cfg clientConfig) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("config path is required")
	}
	if err := requireConfigPathSafe(path); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	cfg = normalizeClientConfig(cfg)
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	return enforceClientConfigPermissions(path)
}

func removeClientConfig(path string) (bool, error) {
	if strings.TrimSpace(path) == "" {
		return false, fmt.Errorf("config path is required")
	}
	if err := requireConfigPathSafe(path); err != nil {
		return false, err
	}
	if err := os.Remove(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func enforceClientConfigPermissions(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	mode := info.Mode().Perm()
	if mode == 0600 {
		return nil
	}
	if err := os.Chmod(path, 0600); err != nil {
		return fmt.Errorf("client config must be 0600: %w", err)
	}
	return nil
}

func readEnvClientConfig() clientConfig {
	tailscaleAdmin, _ := readEnvTailscaleAdminConfig()
	return clientConfig{
		Endpoint:          strings.TrimSpace(os.Getenv(envEndpoint)),
		Token:             strings.TrimSpace(os.Getenv(envToken)),
		AllowInsecureHTTP: envBool(os.Getenv(envAllowInsecureHTTP)),
		TailscaleAdmin:    tailscaleAdmin,
	}
}

// envBool interprets a truthy environment variable: "1", "true", "yes" (case
// insensitive) and a non-empty value that is not an explicit falsy sentinel
// ("0", "false", "no", "") count as true.
func envBool(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "0", "false", "no", "off":
		return false
	default:
		return true
	}
}

func readEnvTailscaleAdminConfig() (*tailscaleAdminConfig, bool) {
	tailnet := strings.TrimSpace(os.Getenv(envTailscaleTailnet))
	apiKey := strings.TrimSpace(os.Getenv(envTailscaleAPIKey))
	oauthID := strings.TrimSpace(os.Getenv(envTailscaleOAuthClientID))
	oauthSecret := strings.TrimSpace(os.Getenv(envTailscaleOAuthClientSecret))
	scopes := parseTailscaleOAuthScopes(os.Getenv(envTailscaleOAuthScopes))
	if tailnet == "" && apiKey == "" && oauthID == "" && oauthSecret == "" && len(scopes) == 0 {
		return nil, false
	}
	cfg := &tailscaleAdminConfig{
		Tailnet:           tailnet,
		APIKey:            apiKey,
		OAuthClientID:     oauthID,
		OAuthClientSecret: oauthSecret,
		OAuthScopes:       scopes,
	}
	return normalizeTailscaleAdminConfig(cfg), true
}

func normalizeClientConfig(cfg clientConfig) clientConfig {
	cfg.Endpoint = strings.TrimSpace(cfg.Endpoint)
	cfg.Token = strings.TrimSpace(cfg.Token)
	cfg.JumpHost = strings.TrimSpace(cfg.JumpHost)
	cfg.JumpUser = strings.TrimSpace(cfg.JumpUser)
	cfg.TailscaleAdmin = normalizeTailscaleAdminConfig(cfg.TailscaleAdmin)
	return cfg
}

func normalizeEndpoint(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", nil
	}
	if !strings.Contains(trimmed, "://") {
		// Require an explicit scheme so a bare host:port never silently selects
		// plaintext HTTP, which would attach bearer credentials in cleartext
		// (review M8).
		return "", fmt.Errorf("endpoint %q must include an explicit scheme (http:// or https://); "+
			"bare host:port is rejected to avoid silently using plaintext HTTP", raw)
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("invalid endpoint %q", raw)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("endpoint scheme must be http or https")
	}
	if parsed.Host == "" {
		return "", fmt.Errorf("endpoint must include host")
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return "", fmt.Errorf("endpoint must not include a path")
	}
	parsed.Path = ""
	parsed.RawQuery = ""
	parsed.Fragment = ""
	endpoint := strings.TrimRight(parsed.String(), "/")
	return endpoint, nil
}

// validateEndpointPolicy enforces the transport-security policy for a resolved
// endpoint (review M8): plaintext HTTP to a non-loopback host is allowed only
// with an explicit acknowledgement, since bearer credentials would otherwise
// travel in cleartext outside a trusted tunnel. HTTPS and loopback HTTP are
// always allowed.
func validateEndpointPolicy(endpoint string, allowInsecureHTTP bool) error {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return nil
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return fmt.Errorf("invalid endpoint %q", endpoint)
	}
	if strings.EqualFold(parsed.Scheme, "https") {
		return nil
	}
	if isLoopbackHost(parsed.Hostname()) {
		return nil
	}
	if !allowInsecureHTTP {
		return fmt.Errorf("endpoint %q uses plaintext HTTP to a non-loopback host; "+
			"pass --allow-insecure-http (or set %s=1) only inside a trusted tunnel such as Tailscale",
			endpoint, envAllowInsecureHTTP)
	}
	return nil
}

// isLoopbackHost reports whether host is a loopback address or name.
func isLoopbackHost(host string) bool {
	host = strings.TrimSpace(host)
	if host == "localhost" {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	// Strip a bracketed IPv6 form just in case.
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		if ip := net.ParseIP(host[1 : len(host)-1]); ip != nil {
			return ip.IsLoopback()
		}
	}
	return false
}
