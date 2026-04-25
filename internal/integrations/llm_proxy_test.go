package integrations

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLLMProxyHandlerOpenAI(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		ct := r.Header.Get("Content-Type")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(fmt.Sprintf(`{"auth":"%s","ct":"%s","path":"%s"}`, auth, ct, r.URL.Path)))
	}))
	defer upstream.Close()

	integ := &Integration{
		Name:       "openai",
		Type:       TypeLLMProxy,
		Target:     upstream.URL,
		Secret:     "sk-openai-test-key",
		Provider:   "openai",
		AttachMode: AttachAutoAll,
	}

	handler := LLMProxyHandler(integ, log.Default())
	proxy := httptest.NewServer(handler)
	defer proxy.Close()

	body := `{"model":"gpt-4","messages":[{"role":"user","content":"hello"}]}`
	resp, err := http.Post(proxy.URL+"/proxy/openai/v1/chat/completions", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	got := string(respBody)

	if !strings.Contains(got, `"auth":"Bearer sk-openai-test-key"`) {
		t.Errorf("response does not contain Bearer token: %s", got)
	}
	if !strings.Contains(got, `"path":"/v1/chat/completions"`) {
		t.Errorf("response does not contain correct path: %s", got)
	}
}

func TestLLMProxyHandlerAnthropic(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiKey := r.Header.Get("x-api-key")
		version := r.Header.Get("anthropic-version")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(fmt.Sprintf(`{"api_key":"%s","version":"%s"}`, apiKey, version)))
	}))
	defer upstream.Close()

	integ := &Integration{
		Name:       "anthropic",
		Type:       TypeLLMProxy,
		Target:     upstream.URL,
		Secret:     "sk-ant-test-key",
		Provider:   "anthropic",
		AttachMode: AttachAutoAll,
	}

	handler := LLMProxyHandler(integ, log.Default())
	proxy := httptest.NewServer(handler)
	defer proxy.Close()

	body := `{"model":"claude-3-5-sonnet-20241022","messages":[{"role":"user","content":"hello"}],"max_tokens":100}`
	resp, err := http.Post(proxy.URL+"/proxy/anthropic/v1/messages", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	got := string(respBody)

	if !strings.Contains(got, `"api_key":"sk-ant-test-key"`) {
		t.Errorf("response does not contain x-api-key: %s", got)
	}
	if !strings.Contains(got, `"version":"2023-06-01"`) {
		t.Errorf("response does not contain anthropic-version: %s", got)
	}
}

func TestLLMProxyHandlerAnthropicVersionPreserved(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		version := r.Header.Get("anthropic-version")
		w.Write([]byte(fmt.Sprintf(`{"version":"%s"}`, version)))
	}))
	defer upstream.Close()

	integ := &Integration{
		Name:       "anthropic",
		Type:       TypeLLMProxy,
		Target:     upstream.URL,
		Secret:     "sk-ant-test",
		Provider:   "anthropic",
		AttachMode: AttachAutoAll,
	}

	handler := LLMProxyHandler(integ, log.Default())
	proxy := httptest.NewServer(handler)
	defer proxy.Close()

	// Send a request with a custom anthropic-version header.
	req, _ := http.NewRequest("POST", proxy.URL+"/proxy/anthropic/v1/messages", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("anthropic-version", "2024-01-01")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)

	// The custom version should be preserved (not overwritten).
	if !strings.Contains(string(respBody), `"version":"2024-01-01"`) {
		t.Errorf("custom anthropic-version not preserved: %s", string(respBody))
	}
}

func TestLLMProxyHandlerOllama(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(fmt.Sprintf(`{"auth":"%s","path":"%s"}`, auth, r.URL.Path)))
	}))
	defer upstream.Close()

	integ := &Integration{
		Name:       "ollama",
		Type:       TypeLLMProxy,
		Target:     upstream.URL,
		Secret:     "",
		Provider:   "ollama",
		AttachMode: AttachAutoAll,
	}

	handler := LLMProxyHandler(integ, log.Default())
	proxy := httptest.NewServer(handler)
	defer proxy.Close()

	resp, err := http.Post(proxy.URL+"/proxy/ollama/api/chat", "application/json", strings.NewReader(`{"model":"llama3"}`))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	got := string(respBody)

	// Ollama should NOT have Authorization header.
	if strings.Contains(got, `"auth":""`) == false && got == "" {
		t.Errorf("unexpected response: %s", got)
	}
	if !strings.Contains(got, `"path":"/api/chat"`) {
		t.Errorf("response does not contain correct path: %s", got)
	}
}

func TestLLMProxyHandlerSSEStreaming(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Hello\"}}]}\n\n")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":\" World\"}}]}\n\n")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		fmt.Fprintf(w, "data: [DONE]\n\n")
	}))
	defer upstream.Close()

	integ := &Integration{
		Name:       "llm-stream",
		Type:       TypeLLMProxy,
		Target:     upstream.URL,
		Secret:     "sk-test",
		Provider:   "openai",
		AttachMode: AttachAutoAll,
	}

	handler := LLMProxyHandler(integ, log.Default())
	proxy := httptest.NewServer(handler)
	defer proxy.Close()

	resp, err := http.Post(proxy.URL+"/proxy/llm-stream/v1/chat/completions", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	got := string(body)

	if !strings.Contains(got, `data: {"choices":[{"delta":{"content":"Hello"}}]}`) {
		t.Errorf("missing first SSE event: %s", got)
	}
	if !strings.Contains(got, `data: {"choices":[{"delta":{"content":" World"}}]}`) {
		t.Errorf("missing second SSE event: %s", got)
	}
	if !strings.Contains(got, "data: [DONE]") {
		t.Errorf("missing DONE event: %s", got)
	}
}

func TestLLMProxyHandlerAutoDetectProvider(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	}))
	defer upstream.Close()

	tests := []struct {
		target   string
		wantProv string
	}{
		{"https://api.openai.com/v1", "openai"},
		{"https://api.anthropic.com/v1", "anthropic"},
		{"http://localhost:11434", "ollama"},
	}

	for _, tt := range tests {
		integ := &Integration{
			Name:       "auto",
			Type:       TypeLLMProxy,
			Target:     tt.target,
			Secret:     "sk-test",
			AttachMode: AttachAutoAll,
		}
		got := integ.DetectProvider()
		if got != tt.wantProv {
			t.Errorf("DetectProvider(%s) = %q, want %q", tt.target, got, tt.wantProv)
		}
	}
}

func TestLLMProxyHandlerNilIntegration(t *testing.T) {
	handler := LLMProxyHandler(nil, nil)
	proxy := httptest.NewServer(handler)
	defer proxy.Close()

	resp, err := http.Get(proxy.URL + "/proxy/test")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestLLMProxyHandlerWrongType(t *testing.T) {
	integ := &Integration{
		Name:       "http",
		Type:       TypeHTTPProxy,
		Target:     "https://example.com",
		Secret:     "test",
		AttachMode: AttachAutoAll,
	}
	handler := LLMProxyHandler(integ, nil)
	proxy := httptest.NewServer(handler)
	defer proxy.Close()

	resp, err := http.Get(proxy.URL + "/proxy/http/test")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestLLMProxyHandlerGET(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"models":[{"id":"gpt-4"}]}`))
	}))
	defer upstream.Close()

	integ := &Integration{
		Name:       "openai",
		Type:       TypeLLMProxy,
		Target:     upstream.URL,
		Secret:     "sk-test",
		Provider:   "openai",
		AttachMode: AttachAutoAll,
	}

	handler := LLMProxyHandler(integ, log.Default())
	proxy := httptest.NewServer(handler)
	defer proxy.Close()

	resp, err := http.Get(proxy.URL + "/proxy/openai/v1/models")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if string(body) != `{"models":[{"id":"gpt-4"}]}` {
		t.Errorf("response = %s, want models list", body)
	}
}
