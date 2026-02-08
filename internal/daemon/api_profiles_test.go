package daemon

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/agentlab/agentlab/internal/models"
)

func TestProfilesListHandler(t *testing.T) {
	store := newTestStore(t)
	alphaUpdated := time.Date(2026, 2, 8, 12, 0, 0, 0, time.UTC)
	betaUpdated := time.Date(2026, 2, 7, 9, 30, 0, 0, time.UTC)
	profiles := map[string]models.Profile{
		"beta": {
			Name:       "beta",
			TemplateVM: 9100,
			UpdatedAt:  betaUpdated,
		},
		"alpha": {
			Name:       "alpha",
			TemplateVM: 9000,
			UpdatedAt:  alphaUpdated,
		},
	}
	api := NewControlAPI(store, profiles, nil, nil, nil, "", log.New(io.Discard, "", 0))

	req := httptest.NewRequest(http.MethodGet, "/v1/profiles", nil)
	rec := httptest.NewRecorder()
	api.handleProfiles(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var resp V1ProfilesResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Profiles) != 2 {
		t.Fatalf("expected 2 profiles, got %d", len(resp.Profiles))
	}
	if resp.Profiles[0].Name != "alpha" {
		t.Fatalf("expected first profile alpha, got %s", resp.Profiles[0].Name)
	}
	if resp.Profiles[0].TemplateVMID != 9000 {
		t.Fatalf("expected alpha template 9000, got %d", resp.Profiles[0].TemplateVMID)
	}
	if resp.Profiles[0].UpdatedAt != alphaUpdated.Format(time.RFC3339Nano) {
		t.Fatalf("expected alpha updated_at %s, got %s", alphaUpdated.Format(time.RFC3339Nano), resp.Profiles[0].UpdatedAt)
	}
	if resp.Profiles[1].Name != "beta" {
		t.Fatalf("expected second profile beta, got %s", resp.Profiles[1].Name)
	}
	if resp.Profiles[1].TemplateVMID != 9100 {
		t.Fatalf("expected beta template 9100, got %d", resp.Profiles[1].TemplateVMID)
	}
	if resp.Profiles[1].UpdatedAt != betaUpdated.Format(time.RFC3339Nano) {
		t.Fatalf("expected beta updated_at %s, got %s", betaUpdated.Format(time.RFC3339Nano), resp.Profiles[1].UpdatedAt)
	}
}
