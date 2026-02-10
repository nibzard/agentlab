package daemon

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/agentlab/agentlab/internal/buildinfo"
	"github.com/agentlab/agentlab/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHostHandler(t *testing.T) {
	store := newTestStore(t)
	api := NewControlAPI(store, map[string]models.Profile{}, nil, nil, nil, "", log.New(io.Discard, "", 0)).
		WithAgentSubnet("10.77.0.0/16").
		WithTailscaleStatus(func(context.Context) (string, error) {
			return "host.tailnet.ts.net", nil
		})

	req := httptest.NewRequest(http.MethodGet, "/v1/host", nil)
	rec := httptest.NewRecorder()
	api.handleHost(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.Bytes()
	var resp V1HostResponse
	require.NoError(t, json.Unmarshal(body, &resp))
	assert.Equal(t, buildinfo.Version, resp.Version)
	assert.Equal(t, "10.77.0.0/16", resp.AgentSubnet)
	assert.Equal(t, "host.tailnet.ts.net", resp.TailscaleDNS)

	var raw map[string]any
	require.NoError(t, json.Unmarshal(body, &raw))
	if _, ok := raw["version"].(string); !ok {
		t.Fatalf("expected version string in payload")
	}
	if _, ok := raw["agent_subnet"].(string); !ok {
		t.Fatalf("expected agent_subnet string in payload")
	}
	if _, ok := raw["tailscale_dns"].(string); !ok {
		t.Fatalf("expected tailscale_dns string in payload")
	}
}
