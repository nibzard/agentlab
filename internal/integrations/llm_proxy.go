package integrations

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/agentlab/agentlab/internal/offline"
)

// ProxyHandlerOptions configures optional behavior for proxy handlers.
type ProxyHandlerOptions struct {
	// Offline, when true, blocks requests to external (non-private) addresses.
	Offline bool
	// ConnectTimeout bounds the TCP/TLS dial to the upstream. Zero or negative
	// means use the default (15s). Replaces a portion of the former flat 5m
	// client.Timeout so a hung dial cannot hold a handler for minutes (M5).
	ConnectTimeout time.Duration
	// ResponseHeaderTimeout bounds how long the proxy waits for the upstream to
	// return response headers (time to first response byte). Zero or negative
	// means use the default (180s, generous for slow first-token LLM compute).
	ResponseHeaderTimeout time.Duration
	// ResponseBodyIdleTimeout bounds how long the response body may stall between
	// bytes before the proxy aborts the upstream read. Zero or negative means use
	// the default (120s). This is an idle deadline, not a total deadline: a long
	// but active stream (large git clone, long LLM completion) is allowed to
	// exceed the former flat 5m cap, while a truly stalled connection is reclaimed.
	ResponseBodyIdleTimeout time.Duration
}

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
func LLMProxyHandler(integ *Integration, logger *log.Logger, opts ...ProxyHandlerOptions) http.Handler {
	var opt ProxyHandlerOptions
	if len(opts) > 0 {
		opt = opts[0]
	}
	if integ == nil || integ.Type != TypeLLMProxy {
		return http.NotFoundHandler()
	}
	if logger == nil {
		logger = log.Default()
	}
	transport := newProxyTransport(opt)
	var roundTripper http.RoundTripper = transport
	if opt.Offline {
		roundTripper = offline.NewOfflineTransport(transport)
	}
	// No flat client.Timeout: connect and response-header phases are bounded by
	// the transport, and the response-body phase is bounded by an idle deadline
	// applied while streaming (review M5).
	client := &http.Client{Transport: roundTripper}
	_, _, bodyIdle := resolveProxyTimeouts(opt)
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

		// Derive a cancellable context so the idle watchdog can abort a stalled
		// upstream body read (review M5). Cancellation also propagates the
		// client disconnecting from the proxy.
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()

		proxyReq, err := http.NewRequestWithContext(ctx, r.Method, targetURL, r.Body)
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
			if _, ok := err.(offline.ErrBlocked); ok {
				logger.Printf("llm-proxy %s: blocked in offline mode: %v", integ.Name, err)
				http.Error(w, "upstream unavailable in offline mode", http.StatusServiceUnavailable)
				return
			}
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

		// Stream the response body, bounded by the idle deadline so a stalled
		// upstream is reclaimed (review M5). For SSE responses the scanner
		// flushes after each event; both paths share the same idle watchdog.
		if isSSEResponse(resp) {
			streamSSE(w, resp.Body, bodyIdle, cancel, logger, integ.Name)
		} else {
			copyResponseBody(w, resp.Body, bodyIdle, cancel, logger, "llm-"+integ.Name)
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
// flushing after each line, and aborts (via cancel) if no bytes arrive within
// the idle window (review M5).
func streamSSE(w http.ResponseWriter, body io.Reader, idle time.Duration, cancel context.CancelFunc, logger *log.Logger, name string) {
	flusher, canFlush := w.(http.Flusher)
	var last atomic.Int64
	last.Store(time.Now().UnixNano())
	stop := idleWatchdog(idle, &last, cancel)
	defer stop()
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		last.Store(time.Now().UnixNano())
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
