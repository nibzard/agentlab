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
