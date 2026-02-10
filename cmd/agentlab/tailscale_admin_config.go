// ABOUTME: Tailscale admin API configuration helpers for optional subnet route automation.
// ABOUTME: Normalizes credentials, merges overrides, and validates configuration inputs.

package main

import (
	"fmt"
	"strings"
)

type tailscaleAdminConfig struct {
	Tailnet           string   `json:"tailnet,omitempty"`
	APIKey            string   `json:"api_key,omitempty"`
	OAuthClientID     string   `json:"oauth_client_id,omitempty"`
	OAuthClientSecret string   `json:"oauth_client_secret,omitempty"`
	OAuthScopes       []string `json:"oauth_scopes,omitempty"`
}

func (cfg *tailscaleAdminConfig) hasCredentials() bool {
	if cfg == nil {
		return false
	}
	if strings.TrimSpace(cfg.APIKey) != "" {
		return true
	}
	return strings.TrimSpace(cfg.OAuthClientID) != "" && strings.TrimSpace(cfg.OAuthClientSecret) != ""
}

func (cfg *tailscaleAdminConfig) isZero() bool {
	if cfg == nil {
		return true
	}
	return strings.TrimSpace(cfg.Tailnet) == "" &&
		strings.TrimSpace(cfg.APIKey) == "" &&
		strings.TrimSpace(cfg.OAuthClientID) == "" &&
		strings.TrimSpace(cfg.OAuthClientSecret) == "" &&
		len(cfg.OAuthScopes) == 0
}

func normalizeTailscaleAdminConfig(cfg *tailscaleAdminConfig) *tailscaleAdminConfig {
	if cfg == nil {
		return nil
	}
	normalized := *cfg
	normalized.Tailnet = strings.TrimSpace(normalized.Tailnet)
	normalized.APIKey = strings.TrimSpace(normalized.APIKey)
	normalized.OAuthClientID = strings.TrimSpace(normalized.OAuthClientID)
	normalized.OAuthClientSecret = strings.TrimSpace(normalized.OAuthClientSecret)
	if len(normalized.OAuthScopes) > 0 {
		scopes := make([]string, 0, len(normalized.OAuthScopes))
		for _, scope := range normalized.OAuthScopes {
			scope = strings.TrimSpace(scope)
			if scope != "" {
				scopes = append(scopes, scope)
			}
		}
		normalized.OAuthScopes = scopes
	}
	if normalized.APIKey != "" {
		normalized.OAuthClientID = ""
		normalized.OAuthClientSecret = ""
		normalized.OAuthScopes = nil
	}
	if normalized.isZero() {
		return nil
	}
	return &normalized
}

func parseTailscaleOAuthScopes(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		switch r {
		case ',', ' ', '\n', '\t', '\r':
			return true
		default:
			return false
		}
	})
	scopes := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			scopes = append(scopes, part)
		}
	}
	if len(scopes) == 0 {
		return nil
	}
	return scopes
}

func mergeTailscaleAdminConfig(base, override *tailscaleAdminConfig) *tailscaleAdminConfig {
	if base == nil && override == nil {
		return nil
	}
	merged := tailscaleAdminConfig{}
	if base != nil {
		merged = *base
	}
	if override != nil {
		if strings.TrimSpace(override.Tailnet) != "" {
			merged.Tailnet = override.Tailnet
		}
		oauthTouched := strings.TrimSpace(override.OAuthClientID) != "" ||
			strings.TrimSpace(override.OAuthClientSecret) != "" ||
			len(override.OAuthScopes) > 0
		if strings.TrimSpace(override.APIKey) != "" {
			merged.APIKey = override.APIKey
			merged.OAuthClientID = ""
			merged.OAuthClientSecret = ""
			merged.OAuthScopes = nil
		} else if oauthTouched {
			merged.APIKey = ""
			if strings.TrimSpace(override.OAuthClientID) != "" {
				merged.OAuthClientID = override.OAuthClientID
			}
			if strings.TrimSpace(override.OAuthClientSecret) != "" {
				merged.OAuthClientSecret = override.OAuthClientSecret
			}
			if len(override.OAuthScopes) > 0 {
				merged.OAuthScopes = override.OAuthScopes
			}
		}
	}
	return normalizeTailscaleAdminConfig(&merged)
}

func validateTailscaleAdminConfig(cfg *tailscaleAdminConfig) error {
	if cfg == nil {
		return nil
	}
	if strings.TrimSpace(cfg.APIKey) != "" {
		return nil
	}
	if strings.TrimSpace(cfg.OAuthClientID) != "" || strings.TrimSpace(cfg.OAuthClientSecret) != "" || len(cfg.OAuthScopes) > 0 {
		if strings.TrimSpace(cfg.OAuthClientID) == "" {
			return fmt.Errorf("tailscale admin oauth client id is required")
		}
		if strings.TrimSpace(cfg.OAuthClientSecret) == "" {
			return fmt.Errorf("tailscale admin oauth client secret is required")
		}
		return nil
	}
	return fmt.Errorf("tailscale admin credentials are required")
}

func tailscaleAdminConfigFromFlags(tailnet, apiKey, oauthID, oauthSecret, oauthScopes string) (*tailscaleAdminConfig, bool) {
	tailnet = strings.TrimSpace(tailnet)
	apiKey = strings.TrimSpace(apiKey)
	oauthID = strings.TrimSpace(oauthID)
	oauthSecret = strings.TrimSpace(oauthSecret)
	scopes := parseTailscaleOAuthScopes(oauthScopes)
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
