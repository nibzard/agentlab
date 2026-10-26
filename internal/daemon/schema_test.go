package daemon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildSchemaResponse(t *testing.T) {
	resp := buildSchemaResponse()

	assert.Equal(t, controlAPISchemaVersion, resp.APISchemaVersion)
	assert.Equal(t, eventContractSchemaVersion, resp.EventSchemaVersion)
	assert.NotEmpty(t, resp.Resources)
	assert.NotEmpty(t, resp.EventKinds)
	assert.NotNil(t, resp.Compatibility)

	hasSchemaEndpoint := false
	for _, resource := range resp.Resources {
		if resource.Path == "/v1/schema" {
			hasSchemaEndpoint = true
			break
		}
	}
	require.True(t, hasSchemaEndpoint)
}

func TestHandleSchema(t *testing.T) {
	api := NewControlAPI(nil, nil, nil, nil, nil, "", nil)

	req := httptest.NewRequest(http.MethodGet, "/v1/schema", nil)
	rec := httptest.NewRecorder()
	api.handleSchema(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var resp schemaResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))

	assert.Equal(t, controlAPISchemaVersion, resp.APISchemaVersion)
	assert.Equal(t, eventContractSchemaVersion, resp.EventSchemaVersion)
	assert.NotEmpty(t, resp.Resources)
	assert.NotNil(t, resp.Compatibility)
	assert.GreaterOrEqual(t, len(resp.Resources), 1)
	assert.GreaterOrEqual(t, len(resp.EventKinds), 1)
}

func TestSchemaRequestDefinitions(t *testing.T) {
	response := buildSchemaResponse()
	for _, resource := range response.Resources {
		if resource.RequestType != "" {
			if _, ok := response.Definitions[resource.RequestType]; !ok {
				t.Errorf("missing request definition %s for %s", resource.RequestType, resource.Path)
			}
		}
		if resource.Path == "/v1/jobs/{id}/events" {
			t.Error("advertised unimplemented events endpoint")
		}
	}
	create, ok := response.Definitions["V1SandboxCreateRequest"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, []string{"profile"}, create["required"])
	properties := create["properties"].(map[string]any)
	assert.Contains(t, properties, "workspace_id")
	assert.Contains(t, properties, "keepalive")
	assert.Contains(t, properties, "ttl_minutes")
	require.NotEmpty(t, create["examples"])
}
