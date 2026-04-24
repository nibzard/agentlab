package proxy

import (
	"testing"
)

func TestNewCaddyClient_Defaults(t *testing.T) {
	client := NewCaddyClient("", nil)
	if client.Endpoint != defaultCaddyAdminAPI {
		t.Errorf("endpoint = %q, want %q", client.Endpoint, defaultCaddyAdminAPI)
	}
	if client.HTTPClient == nil {
		t.Error("HTTPClient is nil")
	}
	if client.Logger == nil {
		t.Error("Logger is nil")
	}
}

func TestNewCaddyClient_CustomEndpoint(t *testing.T) {
	client := NewCaddyClient("http://caddy:2019", nil)
	if client.Endpoint != "http://caddy:2019" {
		t.Errorf("endpoint = %q, want http://caddy:2019", client.Endpoint)
	}
}

func TestNewCaddyClient_TrailingSlash(t *testing.T) {
	client := NewCaddyClient("http://localhost:2019/", nil)
	if client.Endpoint != "http://localhost:2019" {
		t.Errorf("endpoint = %q, want http://localhost:2019 (no trailing slash)", client.Endpoint)
	}
}
