package integrations

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

// LLMProxyHandler returns an http.Handler that proxies OpenAI-compatible LLM
// API requests to the configured upstream provider, injecting the daemon-held
// API key so that sandboxes never see credentials.
//
// Supported providers:
//   - openai: Forwards to api.openai.com with Authorization: Bearer <key>
//   - anthropic: Forwards to api.anthropic.com with x-api-key: <key>
//   - ollama: Forwards to local endpoint (no auth key required)
//
// The handler accepts any HTTP method and path under /proxy/{name}/... and
// forwards to the upstream API, rewriting the URL and injecting credentials.
// SSE streaming is supported: when the upstream responds with
// Content-Type: text/event-stream, the response is flushed incrementally.
func LLMProxyHandler(integ *Integration, logger *log.Logger) http.Handler {
	if integ == nil || integ.Type != TypeLLMProxy {
		return http.NotFoundHandler()
	}
	if logger == nil {
		logger = log.Default()
	}
	transport := &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     90 * time.Second,
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   300 * time.Second, // LLM requests can be long
	}
	provider := integ.DetectProvider()

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Strip the /proxy/{name}/ prefix to get the API sub-path.
		prefix := "/proxy/" + integ.Name + "/"
		subPath := strings.TrimPrefix(r.URL.Path, prefix)
		if subPath == "" {
			subPath = "/"
		}

		target := strings.TrimRight(integ.Target, "/")
		targetURL := target + "/" + subPath
		if r.URL.RawQuery != "" {
			targetURL += "?" + r.URL.RawQuery
		}

		proxyReq, err := http.NewRequestWithContext(r.Context(), r.Method, targetURL, r.Body)
		if err != nil {
			logger.Printf("llm-proxy %s: create request error: %v", integ.Name, err)
			http.Error(w, "proxy error", http.StatusBadGateway)
			return
		}

		// Copy headers from original request, excluding hop-by-hop headers.
		for k, vals := range r.Header {
			if isHopByHop(k) {
				continue
			}
			for _, v := range vals {
				proxyReq.Header.Add(k, v)
			}
		}

		// Inject provider-specific credentials.
		injectLLMCredentials(proxyReq, integ, provider)

		logger.Printf("llm-proxy %s: %s %s provider=%s", integ.Name, r.Method, subPath, provider)

		resp, err := client.Do(proxyReq)
		if err != nil {
			logger.Printf("llm-proxy %s: upstream error: %v", integ.Name, err)
			http.Error(w, "upstream error", http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()

		// Copy response headers.
		for k, vals := range resp.Header {
			if isHopByHop(k) {
				continue
			}
			for _, v := range vals {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(resp.StatusCode)

		// Stream the response body.
		// For SSE responses (text/event-stream), flush after each event.
		if isSSEResponse(resp) {
			streamSSE(w, resp.Body, logger, integ.Name)
		} else {
			flusher, canFlush := w.(http.Flusher)
			buf := make([]byte, 32*1024)
			for {
				n, readErr := resp.Body.Read(buf)
				if n > 0 {
					if _, writeErr := w.Write(buf[:n]); writeErr != nil {
						return
					}
					if canFlush {
						flusher.Flush()
					}
				}
				if readErr != nil {
					break
				}
			}
		}
	})
}

// injectLLMCredentials adds provider-specific authentication headers.
func injectLLMCredentials(req *http.Request, integ *Integration, provider string) {
	if integ == nil || integ.Secret == "" {
		return
	}
	switch provider {
	case "openai":
		req.Header.Set("Authorization", "Bearer "+integ.Secret)
	case "anthropic":
		req.Header.Set("x-api-key", integ.Secret)
		// Anthropic requires anthropic-version header.
		if req.Header.Get("anthropic-version") == "" {
			req.Header.Set("anthropic-version", "2023-06-01")
		}
	case "ollama":
		// Ollama typically doesn't require auth; skip injection.
	default:
		// Fallback to Bearer token.
		req.Header.Set("Authorization", "Bearer "+integ.Secret)
	}
}

// isSSEResponse checks if the response is a Server-Sent Events stream.
func isSSEResponse(resp *http.Response) bool {
	ct := resp.Header.Get("Content-Type")
	return strings.Contains(ct, "text/event-stream")
}

// streamSSE copies an SSE stream from the response body to the writer,
// flushing after each complete event (line starting with "data:", "event:", etc.).
func streamSSE(w http.ResponseWriter, body io.Reader, logger *log.Logger, name string) {
	flusher, canFlush := w.(http.Flusher)
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		fmt.Fprintln(w, line)
		if canFlush {
			flusher.Flush()
		}
	}
	if err := scanner.Err(); err != nil {
		logger.Printf("llm-proxy %s: sse stream error: %v", name, err)
	}
}
