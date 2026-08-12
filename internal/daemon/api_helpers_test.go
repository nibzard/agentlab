package daemon

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type decodePayload struct {
	Name string `json:"name"`
}

func TestDecodeJSONBodySuccess(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name":"ok"}`))
	var payload decodePayload
	if err := decodeJSON(w, r, &payload); err != nil {
		t.Fatalf("decodeJSON() error = %v", err)
	}
	if payload.Name != "ok" {
		t.Fatalf("payload.Name = %q, want %q", payload.Name, "ok")
	}
}

func TestDecodeJSONBodyEmpty(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(""))
	var payload decodePayload
	err := decodeJSON(w, r, &payload)
	if !errors.Is(err, io.EOF) {
		t.Fatalf("decodeJSON() error = %v, want EOF", err)
	}
}

func TestDecodeJSONBodyNil(t *testing.T) {
	w := httptest.NewRecorder()
	r := &http.Request{Body: nil}
	var payload decodePayload
	err := decodeJSON(w, r, &payload)
	if err == nil {
		t.Fatalf("expected error")
	}
	if err.Error() != "request body is required" {
		t.Fatalf("error = %q, want %q", err.Error(), "request body is required")
	}
}

func TestDecodeOptionalJSONEmpty(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(" \n\t"))
	var payload decodePayload
	if err := decodeOptionalJSON(w, r, &payload); err != nil {
		t.Fatalf("decodeOptionalJSON() error = %v", err)
	}
}

func TestDecodeJSONTrailingData(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name":"ok"} trailing`))
	var payload decodePayload
	err := decodeJSON(w, r, &payload)
	if err == nil {
		t.Fatalf("expected error")
	}
	if err.Error() != "unexpected trailing data" {
		t.Fatalf("error = %q, want %q", err.Error(), "unexpected trailing data")
	}
}

// TestDecodeJSONBodyOversizeReturns413 verifies the bounded decoder rejects an
// oversized body and writeJSONDecodeError maps it to 413 (review M4).
func TestDecodeJSONBodyOversizeReturns413(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(strings.Repeat("x", maxJSONBytes+1)))
	var payload decodePayload
	err := decodeJSON(w, r, &payload)
	if err == nil {
		t.Fatal("expected oversize error")
	}

	w2 := httptest.NewRecorder()
	writeJSONDecodeError(w2, err)
	if w2.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", w2.Code)
	}
}

// TestWriteJSONDecodeError_BadRequestForPlainError verifies a non-overflow
// decode error maps to 400 (review M4).
func TestWriteJSONDecodeError_BadRequestForPlainError(t *testing.T) {
	w := httptest.NewRecorder()
	writeJSONDecodeError(w, errors.New("invalid JSON: unexpected token"))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}
