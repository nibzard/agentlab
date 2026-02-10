package daemon

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/agentlab/agentlab/internal/models"
)

func TestControlAPIErrorResponses(t *testing.T) {
	store := newTestStore(t)
	api := NewControlAPI(store, map[string]models.Profile{}, nil, nil, nil, "", log.New(io.Discard, "", 0))
	mux := http.NewServeMux()
	api.Register(mux)

	t.Run("method not allowed returns 405 with allow header", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/v1/jobs", nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
		}
		if allow := rec.Header().Get("Allow"); allow != http.MethodPost {
			t.Fatalf("allow header = %q, want %q", allow, http.MethodPost)
		}
	})

	t.Run("missing id returns 404", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/v1/jobs/", nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
		}
		var payload map[string]string
		if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
			t.Fatalf("decode error response: %v", err)
		}
		if payload["error"] == "" {
			t.Fatalf("expected error message in response")
		}
	})

	t.Run("invalid JSON does not echo secrets", func(t *testing.T) {
		secret := "super-secret-token"
		body := bytes.NewBufferString(`{"repo_url":"https://example.com/repo.git","profile":"default","task":"do thing","token":"` + secret + `"}`)
		req := httptest.NewRequest(http.MethodPost, "/v1/jobs", body)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
		}
		if strings.Contains(rec.Body.String(), secret) {
			t.Fatalf("response leaked secret")
		}
	})

	t.Run("details redacted for client errors", func(t *testing.T) {
		rec := httptest.NewRecorder()
		writeError(rec, http.StatusBadRequest, "bad request", errors.New("token=super-secret"))

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
		}
		var payload V1ErrorResponse
		if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
			t.Fatalf("decode error response: %v", err)
		}
		if strings.Contains(payload.Details, "super-secret") {
			t.Fatalf("details leaked secret: %q", payload.Details)
		}
		if payload.Details == "" || !strings.Contains(payload.Details, redactedValue) {
			t.Fatalf("expected redacted details, got %q", payload.Details)
		}
	})

	t.Run("details omitted for server errors", func(t *testing.T) {
		rec := httptest.NewRecorder()
		writeError(rec, http.StatusInternalServerError, "internal error", errors.New("token=super-secret"))

		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
		}
		var payload V1ErrorResponse
		if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
			t.Fatalf("decode error response: %v", err)
		}
		if payload.Details != "" {
			t.Fatalf("expected empty details for server errors, got %q", payload.Details)
		}
	})
}
