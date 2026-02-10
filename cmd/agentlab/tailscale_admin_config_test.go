package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseTailscaleOAuthScopes(t *testing.T) {
	scopes := parseTailscaleOAuthScopes("device:read, routes:write  tailnet:read\n")
	assert.Equal(t, []string{"device:read", "routes:write", "tailnet:read"}, scopes)
}

func TestNormalizeTailscaleAdminConfigAPIKeyWins(t *testing.T) {
	cfg := &tailscaleAdminConfig{
		Tailnet:           "example",
		APIKey:            "tskey-api-123",
		OAuthClientID:     "id",
		OAuthClientSecret: "secret",
		OAuthScopes:       []string{"device:read"},
	}
	normalized := normalizeTailscaleAdminConfig(cfg)
	if assert.NotNil(t, normalized) {
		assert.Equal(t, "tskey-api-123", normalized.APIKey)
		assert.Equal(t, "", normalized.OAuthClientID)
		assert.Equal(t, "", normalized.OAuthClientSecret)
		assert.Empty(t, normalized.OAuthScopes)
	}
}

func TestMergeTailscaleAdminConfig(t *testing.T) {
	base := &tailscaleAdminConfig{Tailnet: "example", APIKey: "tskey-api-123"}
	override := &tailscaleAdminConfig{OAuthClientID: "id", OAuthClientSecret: "secret", OAuthScopes: []string{"device:read"}}
	merged := mergeTailscaleAdminConfig(base, override)
	if assert.NotNil(t, merged) {
		assert.Equal(t, "example", merged.Tailnet)
		assert.Equal(t, "", merged.APIKey)
		assert.Equal(t, "id", merged.OAuthClientID)
		assert.Equal(t, "secret", merged.OAuthClientSecret)
		assert.Equal(t, []string{"device:read"}, merged.OAuthScopes)
	}
}

func TestValidateTailscaleAdminConfigRequiresCredentials(t *testing.T) {
	cfg := &tailscaleAdminConfig{Tailnet: "example"}
	assert.Error(t, validateTailscaleAdminConfig(cfg))
}
