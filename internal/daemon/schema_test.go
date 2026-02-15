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
