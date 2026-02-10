// ABOUTME: Client-side configuration for remote control plane access.
// ABOUTME: Handles XDG config storage, environment overrides, and endpoint normalization.

package main

import (
	"encoding/json"
	"errors"
	"fmt"
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
	envTailscaleTailnet           = "AGENTLAB_TAILSCALE_TAILNET"
	envTailscaleAPIKey            = "AGENTLAB_TAILSCALE_API_KEY"
	envTailscaleOAuthClientID     = "AGENTLAB_TAILSCALE_OAUTH_CLIENT_ID"
	envTailscaleOAuthClientSecret = "AGENTLAB_TAILSCALE_OAUTH_CLIENT_SECRET"
	envTailscaleOAuthScopes       = "AGENTLAB_TAILSCALE_OAUTH_SCOPES"
)

type clientConfig struct {
	Endpoint       string                `json:"endpoint,omitempty"`
	Token          string                `json:"token,omitempty"`
	JumpHost       string                `json:"jump_host,omitempty"`
	JumpUser       string                `json:"jump_user,omitempty"`
	TailscaleAdmin *tailscaleAdminConfig `json:"tailscale_admin,omitempty"`
}

func clientConfigPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, clientConfigDir, clientConfigFile), nil
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
		Endpoint:       strings.TrimSpace(os.Getenv(envEndpoint)),
		Token:          strings.TrimSpace(os.Getenv(envToken)),
		TailscaleAdmin: tailscaleAdmin,
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
		trimmed = "http://" + trimmed
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
