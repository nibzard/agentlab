package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func newTestResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}

func TestNewAPIClientDefaults(t *testing.T) {
	client := newAPIClient("", 0)
	if client.socketPath != defaultSocketPath {
		t.Fatalf("socketPath = %q, want %q", client.socketPath, defaultSocketPath)
	}
	if client.httpClient == nil {
		t.Fatalf("expected httpClient to be set")
	}
}

func TestAPIClientWithTimeout(t *testing.T) {
	ctx := context.Background()
	var nilClient *apiClient
	ctxNoTimeout, cancel := nilClient.withTimeout(ctx)
	defer cancel()
	if ctxNoTimeout != ctx {
		t.Fatalf("expected context to be unchanged")
	}
	if _, ok := ctxNoTimeout.Deadline(); ok {
		t.Fatalf("expected no deadline for nil client")
	}

	client := &apiClient{timeout: 25 * time.Millisecond}
	ctxWithTimeout, cancelWithTimeout := client.withTimeout(ctx)
	defer cancelWithTimeout()
	if ctxWithTimeout == ctx {
		t.Fatalf("expected derived context")
	}
	if _, ok := ctxWithTimeout.Deadline(); !ok {
		t.Fatalf("expected deadline for timeout context")
	}
}

func TestAPIClientDoJSONSuccess(t *testing.T) {
	var gotReq *http.Request
	var gotBody []byte
	client := &apiClient{
		socketPath: "/tmp/agentlab.sock",
		httpClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			gotReq = req
			body, _ := io.ReadAll(req.Body)
			gotBody = body
			return newTestResponse(http.StatusOK, `{"ok":true}`), nil
		})},
	}

	payload := map[string]string{"hello": "world"}
	data, err := client.doJSON(context.Background(), http.MethodPost, "/v1/test", payload)
	if err != nil {
		t.Fatalf("doJSON() error = %v", err)
	}
	if string(data) != `{"ok":true}` {
		t.Fatalf("unexpected response body: %s", string(data))
	}
	if gotReq == nil {
		t.Fatalf("expected request to be captured")
	}
	if gotReq.Method != http.MethodPost {
		t.Fatalf("method = %s, want %s", gotReq.Method, http.MethodPost)
	}
	if gotReq.URL.Path != "/v1/test" {
		t.Fatalf("path = %s, want /v1/test", gotReq.URL.Path)
	}
	if gotReq.Header.Get("Accept") != "application/json" {
		t.Fatalf("Accept header missing")
	}
	if gotReq.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("Content-Type header missing")
	}
	var decoded map[string]string
	if err := json.Unmarshal(gotBody, &decoded); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if decoded["hello"] != "world" {
		t.Fatalf("payload mismatch: %v", decoded)
	}
}

func TestAPIClientDoJSONEncodeError(t *testing.T) {
	called := false
	client := &apiClient{
		httpClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			called = true
			return newTestResponse(http.StatusOK, `{}`), nil
		})},
	}

	_, err := client.doJSON(context.Background(), http.MethodPost, "/v1/test", make(chan int))
	if err == nil {
		t.Fatalf("expected error from JSON encode")
	}
	if called {
		t.Fatalf("expected no request when JSON encoding fails")
	}
}

func TestAPIClientDoJSONErrorResponse(t *testing.T) {
	client := &apiClient{
		socketPath: "/tmp/agentlab.sock",
		httpClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return newTestResponse(http.StatusBadRequest, `{"error":"bad request"}`), nil
		})},
	}

	_, err := client.doJSON(context.Background(), http.MethodGet, "/v1/test", nil)
	if err == nil {
		t.Fatalf("expected error for status code")
	}
	if err.Error() != "bad request" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAPIClientDoRequestHeadersAndErrorHandling(t *testing.T) {
	var gotReq *http.Request
	client := &apiClient{
		socketPath: "/tmp/agentlab.sock",
		httpClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			gotReq = req
			return newTestResponse(http.StatusOK, "ok"), nil
		})},
	}

	resp, err := client.doRequest(context.Background(), http.MethodPost, "/v1/test", strings.NewReader("payload"), map[string]string{
		"X-Test": "ok",
		" ":      "ignored",
	})
	if err != nil {
		t.Fatalf("doRequest() error = %v", err)
	}
	if gotReq == nil {
		t.Fatalf("expected request to be captured")
	}
	if gotReq.URL.Path != "/v1/test" {
		t.Fatalf("path = %s, want /v1/test", gotReq.URL.Path)
	}
	if gotReq.Header.Get("X-Test") != "ok" {
		t.Fatalf("expected header to be set")
	}
	if gotReq.Header.Get(" ") != "" {
		t.Fatalf("expected blank header to be ignored")
	}
	_ = resp.Body.Close()

	client.httpClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return newTestResponse(http.StatusNotFound, `{"error":"not found"}`), nil
	})}
	_, err = client.doRequest(context.Background(), http.MethodGet, "/v1/missing", nil, nil)
	if err == nil {
		t.Fatalf("expected error for status code")
	}
	if err.Error() != "not found" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseAPIErrorFallback(t *testing.T) {
	err := parseAPIError(http.StatusInternalServerError, []byte("not-json"))
	if err == nil || !strings.Contains(err.Error(), "status 500") {
		t.Fatalf("expected fallback error, got %v", err)
	}

	err = parseAPIError(http.StatusBadRequest, []byte(`{"error":"boom"}`))
	if err == nil || err.Error() != "boom" {
		t.Fatalf("expected parsed error, got %v", err)
	}
}

func TestPrettyPrintJSON(t *testing.T) {
	buf := &bytes.Buffer{}
	if err := prettyPrintJSON(buf, []byte(`{"b":1}`)); err != nil {
		t.Fatalf("prettyPrintJSON() error = %v", err)
	}
	want := "{\n  \"b\": 1\n}\n"
	if buf.String() != want {
		t.Fatalf("prettyPrintJSON() = %q, want %q", buf.String(), want)
	}

	buf.Reset()
	bad := []byte("not-json")
	if err := prettyPrintJSON(buf, bad); err != nil {
		t.Fatalf("prettyPrintJSON() error = %v", err)
	}
	if buf.String() != string(bad) {
		t.Fatalf("prettyPrintJSON() fallback = %q, want %q", buf.String(), string(bad))
	}
}
