package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/agentlab/agentlab/internal/integrations"
	"github.com/agentlab/agentlab/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDB_SandboxTagsRoundTrip proves tags persist through CreateSandbox and
// UpdateSandboxTags and read back identically (review M6).
func TestDB_SandboxTagsRoundTrip(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	normalized, err := integrations.NormalizeTags([]string{"Prod", "WEB", "prod"})
	require.NoError(t, err)

	sb := models.Sandbox{
		VMID:      2001,
		Name:      "tags-sb",
		Profile:   "default",
		State:     models.SandboxRunning,
		Tags:      integrations.JoinTags(normalized),
		CreatedAt: time.Now().UTC(),
	}
	require.NoError(t, store.CreateSandbox(ctx, sb))

	loaded, err := store.GetSandbox(ctx, 2001)
	require.NoError(t, err)
	assert.Equal(t, "prod,web", loaded.Tags)

	// Replace tags.
	replaced, err := integrations.NormalizeTags([]string{"staging", "gpu"})
	require.NoError(t, err)
	require.NoError(t, store.UpdateSandboxTags(ctx, 2001, integrations.JoinTags(replaced)))
	loaded, err = store.GetSandbox(ctx, 2001)
	require.NoError(t, err)
	assert.Equal(t, "staging,gpu", loaded.Tags)

	// Clear tags.
	require.NoError(t, store.UpdateSandboxTags(ctx, 2001, ""))
	loaded, err = store.GetSandbox(ctx, 2001)
	require.NoError(t, err)
	assert.Equal(t, "", loaded.Tags)
}

// TestSandboxToV1_ReturnsTags confirms the API response projects parsed tags.
func TestSandboxToV1_ReturnsTags(t *testing.T) {
	store := newTestStore(t)
	api := NewControlAPI(store, map[string]models.Profile{}, nil, nil, nil, "", log.New(io.Discard, "", 0))

	resp := api.sandboxToV1(models.Sandbox{
		VMID: 77, Name: "x", Profile: "p", State: models.SandboxRunning, Tags: "prod,web",
	})
	assert.Equal(t, []string{"prod", "web"}, resp.Tags)

	// Empty tags are omitted (omitempty).
	resp = api.sandboxToV1(models.Sandbox{VMID: 78, Name: "y", Profile: "p", State: models.SandboxRunning})
	assert.Nil(t, resp.Tags)
}

func newTagsUpdateAPI(t *testing.T) (*ControlAPI, *stubBackend) {
	t.Helper()
	store := newTestStore(t)
	backend := &stubBackend{
		vmConfig: map[string]string{"cores": "4", "memory": "8192"},
	}
	manager := NewSandboxManager(store, backend, log.New(io.Discard, "", 0))
	api := NewControlAPI(store, map[string]models.Profile{}, manager, nil, nil, "", log.New(io.Discard, "", 0)).WithBackend(backend)
	now := time.Now().UTC()
	require.NoError(t, store.CreateSandbox(context.Background(), models.Sandbox{
		VMID: 110, Name: "handler-sb", Profile: "default",
		State: models.SandboxRunning, Tags: "legacy",
		CreatedAt: now, LastUpdatedAt: now,
	}))
	return api, backend
}

// TestSandboxUpdate_TagsOnlyDoesNotReconfigure: a tags-only update must persist
// tags without touching VM compute (review M6).
func TestSandboxUpdate_TagsOnlyDoesNotReconfigure(t *testing.T) {
	api, backend := newTagsUpdateAPI(t)

	req := httptest.NewRequest(http.MethodPost, "/v1/sandboxes/110/update",
		bytes.NewBufferString(`{"tags":["Prod","WEB"]}`))
	rec := httptest.NewRecorder()
	api.handleSandboxByID(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, 0, backend.configureCalls, "tags-only update must not reconfigure the VM")

	var resp V1SandboxResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	assert.Equal(t, []string{"prod", "web"}, resp.Tags)
}

// TestSandboxUpdate_TagsClear: an explicit empty slice clears tags.
func TestSandboxUpdate_TagsClear(t *testing.T) {
	api, _ := newTagsUpdateAPI(t)

	req := httptest.NewRequest(http.MethodPost, "/v1/sandboxes/110/update",
		bytes.NewBufferString(`{"tags":[]}`))
	rec := httptest.NewRecorder()
	api.handleSandboxByID(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var resp V1SandboxResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	assert.Nil(t, resp.Tags)

	loaded, err := api.store.GetSandbox(context.Background(), 110)
	require.NoError(t, err)
	assert.Equal(t, "", loaded.Tags)
}

// TestSandboxUpdate_TagsOmittedLeavesUnchanged: omitting the tags field must
// not alter existing tags during a resource update.
func TestSandboxUpdate_TagsOmittedLeavesUnchanged(t *testing.T) {
	api, _ := newTagsUpdateAPI(t)

	req := httptest.NewRequest(http.MethodPost, "/v1/sandboxes/110/update",
		bytes.NewBufferString(`{"cores":2}`))
	rec := httptest.NewRecorder()
	api.handleSandboxByID(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	loaded, err := api.store.GetSandbox(context.Background(), 110)
	require.NoError(t, err)
	assert.Equal(t, "legacy", loaded.Tags, "omitting tags must preserve existing value")
}

// TestSandboxTags_DriveIntegrationMatching proves the end-to-end creation →
// persistence → integration-attachment path (review M6).
func TestSandboxTags_DriveIntegrationMatching(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	normalized, err := integrations.NormalizeTags([]string{"Prod", "region-us"})
	require.NoError(t, err)
	require.NoError(t, store.CreateSandbox(ctx, models.Sandbox{
		VMID: 3001, Name: "match-sb", Profile: "default",
		State: models.SandboxRunning, Tags: integrations.JoinTags(normalized),
		CreatedAt: time.Now().UTC(),
	}))

	loaded, err := store.GetSandbox(ctx, 3001)
	require.NoError(t, err)
	sandboxTags := parseTags(loaded.Tags)

	selector, err := integrations.NormalizeTagSelector("prod")
	require.NoError(t, err)
	integ := &integrations.Integration{AttachMode: integrations.AttachTag, AttachSelector: selector}
	assert.True(t, integ.MatchesSandbox(loaded.Name, sandboxTags), "tag-attached integration should match")

	nonMatching, err := integrations.NormalizeTagSelector("staging")
	require.NoError(t, err)
	other := &integrations.Integration{AttachMode: integrations.AttachTag, AttachSelector: nonMatching}
	assert.False(t, other.MatchesSandbox(loaded.Name, sandboxTags), "non-matching tag should not attach")
}
