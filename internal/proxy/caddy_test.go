package proxy

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
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

func TestAddRoute_RejectsDuplicateHost(t *testing.T) {
	var puts, deletes int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			fmt.Fprint(w, `[{"match":[{"host":["box.example.com"]}],"handle":[{"handler":"reverse_proxy","upstream":"10.77.0.5:8080"}],"terminal":true}]`)
		case http.MethodPut:
			puts++
			w.WriteHeader(http.StatusOK)
		case http.MethodDelete:
			deletes++
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	client := NewCaddyClient(srv.URL, nil)
	err := client.AddRoute(context.Background(), "box.example.com", "10.77.0.9:8080")
	if !errors.Is(err, ErrRouteExists) {
		t.Fatalf("AddRoute() error = %v, want ErrRouteExists", err)
	}
	if puts != 0 {
		t.Errorf("AddRoute() issued %d PUTs, want 0", puts)
	}
	if deletes != 0 {
		t.Errorf("AddRoute() issued %d DELETEs, want 0 (must not displace the live route)", deletes)
	}
}

func TestAddRoute_NewHostSucceeds(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			fmt.Fprint(w, `[{"match":[{"host":["other.example.com"]}]}]`)
		case http.MethodPut:
			if r.URL.Path != "/config/apps/http/servers/srv0/routes/other2-example-com" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	client := NewCaddyClient(srv.URL, nil)
	if err := client.AddRoute(context.Background(), "other2.example.com", "10.77.0.9:8080"); err != nil {
		t.Fatalf("AddRoute() error = %v, want nil", err)
	}
}
