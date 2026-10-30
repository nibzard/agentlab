package daemon

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/agentlab/agentlab/internal/models"
	"github.com/agentlab/agentlab/internal/pool"
	"github.com/stretchr/testify/require"
)

func TestSandboxPlanChecksCapacityWithoutReserving(t *testing.T) {
	store := newTestStore(t)
	profiles := map[string]models.Profile{"small": {Name: "small", TemplateVM: 9000, RawYAML: "resources:\n  cores: 2\n  memory_mb: 2048\n"}}
	capacity := pool.New(pool.Config{TotalCores: 4, TotalMemoryMB: 4096})
	orchestrator := &JobOrchestrator{backend: &orchestratorBackend{}, sandboxManager: &SandboxManager{}, sshPublicKey: "fixture", nativeSSHKeys: true}
	api := NewControlAPI(store, profiles, nil, nil, orchestrator, "", nil).WithResourcePool(capacity)
	validate := func() V1SandboxValidatePlanResponse {
		response := httptest.NewRecorder()
		api.handleSandboxValidatePlan(response, httptest.NewRequest(http.MethodPost, "/v1/sandboxes/validate-plan", bytes.NewBufferString(`{"profile":"small"}`)))
		require.Equal(t, 200, response.Code)
		require.NotContains(t, response.Body.String(), "other-tenant")
		var plan V1SandboxValidatePlanResponse
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &plan))
		return plan
	}
	for range 2 {
		plan := validate()
		require.True(t, plan.OK, "%+v", plan.Errors)
		require.True(t, plan.Plan.Capacity.Checked)
		require.Equal(t, 2, plan.Plan.Capacity.Cores)
		require.Equal(t, 2048, plan.Plan.Capacity.MemoryMB)
		require.Zero(t, capacity.Status().ActiveCount)
	}
	require.NoError(t, capacity.Allocate(1001, "other-tenant", "other-profile", 4, 4096, false))
	blocked := validate()
	require.False(t, blocked.OK)
	require.Nil(t, blocked.Plan)
	require.Equal(t, "resource_capacity_unavailable", blocked.Errors[0].Code)
	require.Equal(t, 1, capacity.Status().ActiveCount)
	require.NoError(t, capacity.Release(1001))
	require.True(t, validate().OK)
	api.resourcePool = nil
	unchecked := validate()
	require.True(t, unchecked.OK)
	require.False(t, unchecked.Plan.Capacity.Checked)
	require.Contains(t, unchecked.Warnings, V1PreflightIssue{Code: "capacity_not_checked", Field: "profile", Message: "pool capacity is not configured for this profile; verify host capacity before creation"})
}
