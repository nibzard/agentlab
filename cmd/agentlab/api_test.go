package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
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
	client := newAPIClient(clientOptions{}, 0)
	if client.socketPath != defaultSocketPath {
		t.Fatalf("socketPath = %q, want %q", client.socketPath, defaultSocketPath)
	}
	if client.httpClient == nil {
		t.Fatalf("expected httpClient to be set")
	}
}

func TestNewAPIClientRemoteBaseURL(t *testing.T) {
	client := newAPIClient(clientOptions{Endpoint: "https://example.com/", Token: "token"}, time.Second)
	if client.endpoint != "https://example.com" {
		t.Fatalf("endpoint = %q, want %q", client.endpoint, "https://example.com")
	}
	if client.baseURL != "https://example.com" {
		t.Fatalf("baseURL = %q, want %q", client.baseURL, "https://example.com")
	}
	if client.token != "token" {
		t.Fatalf("token = %q, want %q", client.token, "token")
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

func TestAPIClientWithTimeoutRespectsExistingDeadline(t *testing.T) {
	client := &apiClient{timeout: 50 * time.Millisecond}

	outerCtx, outerCancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer outerCancel()

	gotCtx, cancel := client.withTimeout(outerCtx)
	defer cancel()
	if gotCtx != outerCtx {
		t.Fatalf("expected context to be unchanged when outer deadline is sooner")
	}
	outerDeadline, ok := outerCtx.Deadline()
	if !ok {
		t.Fatalf("expected outer deadline")
	}
	gotDeadline, ok := gotCtx.Deadline()
	if !ok {
		t.Fatalf("expected got deadline")
	}
	if !gotDeadline.Equal(outerDeadline) {
		t.Fatalf("deadline = %v, want %v", gotDeadline, outerDeadline)
	}

	longCtx, longCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer longCancel()

	gotShort, shortCancel := client.withTimeout(longCtx)
	defer shortCancel()
	if gotShort == longCtx {
		t.Fatalf("expected derived context when client timeout is sooner")
	}
	longDeadline, ok := longCtx.Deadline()
	if !ok {
		t.Fatalf("expected long deadline")
	}
	shortDeadline, ok := gotShort.Deadline()
	if !ok {
		t.Fatalf("expected short deadline")
	}
	if !shortDeadline.Before(longDeadline) {
		t.Fatalf("short deadline = %v, want before %v", shortDeadline, longDeadline)
	}
}

func TestAPIClientDoJSONSuccess(t *testing.T) {
	var gotReq *http.Request
	var gotBody []byte
	client := &apiClient{
		socketPath: "/tmp/agentlab.sock",
		baseURL:    "http://unix",
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

func TestAPIClientDoJSONAuthHeader(t *testing.T) {
	var gotAuth string
	client := &apiClient{
		endpoint: "https://example.com",
		token:    "secret",
		baseURL:  "https://example.com",
		httpClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			gotAuth = req.Header.Get("Authorization")
			return newTestResponse(http.StatusOK, `{}`), nil
		})},
	}

	_, err := client.doJSON(context.Background(), http.MethodGet, "/v1/test", nil)
	if err != nil {
		t.Fatalf("doJSON() error = %v", err)
	}
	if gotAuth != "Bearer secret" {
		t.Fatalf("Authorization = %q, want %q", gotAuth, "Bearer secret")
	}
}

func TestAPIClientDoJSONEncodeError(t *testing.T) {
	called := false
	client := &apiClient{
		baseURL: "http://unix",
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
		baseURL:    "http://unix",
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

func TestAPIClientDoRequestAuthHeader(t *testing.T) {
	var gotAuth string
	client := &apiClient{
		endpoint: "https://example.com",
		token:    "secret",
		baseURL:  "https://example.com",
		httpClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			gotAuth = req.Header.Get("Authorization")
			return newTestResponse(http.StatusOK, "ok"), nil
		})},
	}

	resp, err := client.doRequest(context.Background(), http.MethodGet, "/v1/test", nil, nil)
	if err != nil {
		t.Fatalf("doRequest() error = %v", err)
	}
	_ = resp.Body.Close()
	if gotAuth != "Bearer secret" {
		t.Fatalf("Authorization = %q, want %q", gotAuth, "Bearer secret")
	}
}

func TestAPIClientDoRequestHeadersAndErrorHandling(t *testing.T) {
	var gotReq *http.Request
	client := &apiClient{
		socketPath: "/tmp/agentlab.sock",
		baseURL:    "http://unix",
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

func TestAPIClientAuthHeaderRemote(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	endpoint, err := normalizeEndpoint(srv.URL)
	if err != nil {
		t.Fatalf("normalizeEndpoint() error = %v", err)
	}
	client := newAPIClient(clientOptions{Endpoint: endpoint, Token: "secret-token"}, time.Second)
	_, err = client.doJSON(context.Background(), http.MethodGet, "/v1/status", nil)
	if err != nil {
		t.Fatalf("doJSON() error = %v", err)
	}
	if gotAuth != "Bearer secret-token" {
		t.Fatalf("Authorization header = %q, want %q", gotAuth, "Bearer secret-token")
	}
}

func TestAPIClientAuthErrors(t *testing.T) {
	tests := []struct {
		name         string
		status       int
		body         string
		wantContains string
	}{
		{"unauthorized json", http.StatusUnauthorized, `{"error":"unauthorized"}`, "unauthorized"},
		{"forbidden fallback", http.StatusForbidden, "", "status 403"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotAuth string
			client := &apiClient{
				endpoint: "https://example.com",
				baseURL:  "https://example.com",
				token:    "super-secret",
				httpClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
					gotAuth = req.Header.Get("Authorization")
					return newTestResponse(tt.status, tt.body), nil
				})},
			}

			_, err := client.doJSON(context.Background(), http.MethodGet, "/v1/status", nil)
			if err == nil {
				t.Fatalf("expected error for status %d", tt.status)
			}
			if gotAuth != "Bearer super-secret" {
				t.Fatalf("Authorization header = %q, want %q", gotAuth, "Bearer super-secret")
			}
			if strings.Contains(err.Error(), "super-secret") {
				t.Fatalf("token leaked in error: %v", err)
			}
			if tt.wantContains != "" && !strings.Contains(err.Error(), tt.wantContains) {
				t.Fatalf("error = %q, want to contain %q", err.Error(), tt.wantContains)
			}
		})
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

func TestEndpointPath(t *testing.T) {
	got, err := endpointPath("/v1/jobs/", "job-123", "artifacts")
	if err != nil {
		t.Fatalf("endpointPath() error = %v", err)
	}
	if got != "/v1/jobs/job-123/artifacts" {
		t.Fatalf("endpointPath() = %q, want %q", got, "/v1/jobs/job-123/artifacts")
	}
}

func TestEndpointPathEscapesSegments(t *testing.T) {
	got, err := endpointPath("v1/jobs", "job id", "a?b#c")
	if err != nil {
		t.Fatalf("endpointPath() error = %v", err)
	}
	want := "/v1/jobs/job%20id/a%3Fb%23c"
	if got != want {
		t.Fatalf("endpointPath() = %q, want %q", got, want)
	}
}

func TestEndpointPathRejectsBadSegments(t *testing.T) {
	tests := []string{"", ".", "..", "a/b", "a\\b", "bad\x00value", "bad\nvalue"}
	for _, segment := range tests {
		if _, err := endpointPath("/v1/jobs", segment); err == nil {
			t.Fatalf("endpointPath() expected error for segment %q", segment)
		}
	}
}
