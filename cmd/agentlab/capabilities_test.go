package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/agentlab/agentlab/internal/protocol"
	"github.com/stretchr/testify/require"
)

func TestCapabilitiesCompatibilityAndNoAuthFallback(t *testing.T) {
	for _, status := range []int{200, 401, 403, 404} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				require.Equal(t, "/v1/capabilities", r.URL.Path)
				w.WriteHeader(status)
				json.NewEncoder(w).Encode(protocol.Capabilities{ProtocolVersion: 99, APISchemaVersion: 99})
			}))
			defer server.Close()
			_, err := fetchCapabilities(context.Background(), newAPIClient(clientOptions{Endpoint: server.URL}, time.Second))
			require.Error(t, err)
			require.Equal(t, 1, calls)
		})
	}
}
