package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDashboardChecksConnectionBeforeListening(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/status" {
			t.Error(r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer invalid" {
			t.Error("missing selected credential")
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()
	err := runDashboardCommand(context.Background(), []string{"--listen", "invalid-address"}, commonFlags{endpoint: server.URL, token: "invalid", timeout: time.Second})
	if err == nil || !strings.Contains(err.Error(), "cannot open dashboard") {
		t.Fatalf("expected connection diagnostic, got %v", err)
	}
	if strings.Contains(err.Error(), "Bearer invalid") {
		t.Fatal("credential exposed")
	}
}

func TestDashboardCompatibilityFailureIsStructuredBeforeListen(t *testing.T) {
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/status" {
			writeJSON(t, w, 200, statusResponse{})
			return
		}
		require.Equal(t, "/v1/capabilities", r.URL.Path)
		writeJSON(t, w, 200, map[string]int{"protocol_version": 99, "api_schema_version": 99})
	}))
	defer remote.Close()
	err := runDashboardCommand(context.Background(), []string{"--listen", "invalid-address"}, commonFlags{endpoint: remote.URL, token: "private-fixture", jsonOutput: true, timeout: time.Second})
	require.Error(t, err)
	var output bytes.Buffer
	writeJSONError(&output, err)
	var metadata map[string]any
	require.NoError(t, json.Unmarshal(output.Bytes(), &metadata))
	require.Equal(t, "blocked", metadata["state"])
	require.Equal(t, "compatible_server", metadata["requirement"])
	require.Equal(t, "dashboard_credential_identity_unavailable", metadata["code"])
	require.NotContains(t, output.String(), "private-fixture")
}

func TestDashboardHelp(t *testing.T) {
	if err := runDashboardCommand(context.Background(), []string{"--help"}, commonFlags{}); !errors.Is(err, errHelp) {
		t.Fatalf("help: %v", err)
	}
}
