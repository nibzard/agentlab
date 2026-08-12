package integrations

import (
	"context"
	"io"
	"log"
	"net"
	"net/http"
	"sync/atomic"
	"time"
)

// Per-phase proxy timeout defaults (review M5). These replace the former flat
// 5m client.Timeout on the LLM and git proxies.
const (
	defaultProxyConnectTimeout        = 15 * time.Second
	defaultProxyResponseHeaderTimeout = 180 * time.Second
	defaultProxyBodyIdleTimeout       = 120 * time.Second
)

// resolveProxyTimeouts returns the effective per-phase timeouts, applying
// defaults for any non-positive configured value.
func resolveProxyTimeouts(o ProxyHandlerOptions) (connect, header, idle time.Duration) {
	connect = o.ConnectTimeout
	if connect <= 0 {
		connect = defaultProxyConnectTimeout
	}
	header = o.ResponseHeaderTimeout
	if header <= 0 {
		header = defaultProxyResponseHeaderTimeout
	}
	idle = o.ResponseBodyIdleTimeout
	if idle <= 0 {
		idle = defaultProxyBodyIdleTimeout
	}
	return connect, header, idle
}

// newProxyTransport builds an http.Transport whose connect and response-header
// phases are bounded, replacing the previous flat client.Timeout (review M5).
// The returned transport reuses connections but never lets a single request
// hang indefinitely while dialing or while awaiting response headers.
func newProxyTransport(o ProxyHandlerOptions) *http.Transport {
	connect, header, _ := resolveProxyTimeouts(o)
	dialer := &net.Dialer{
		Timeout:   connect,
		KeepAlive: 30 * time.Second,
	}
	t := &http.Transport{
		DialContext:           dialer.DialContext,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   10,
		IdleConnTimeout:       90 * time.Second,
		ResponseHeaderTimeout: header,
		// ForceAttemptHTTP2 lets the transport negotiate HTTP/2 with upstreams
		// that support it (e.g. the OpenAI API), which is more efficient for
		// long streams. HTTP/1.1 remains the fallback.
		ForceAttemptHTTP2: true,
	}
	if o.Offline {
		// Ignore proxy environment variables in offline mode so the request
		// dials directly and the offline allow-list is actually consulted.
		t.Proxy = nil
	} else {
		t.Proxy = http.ProxyFromEnvironment
	}
	return t
}

// idleWatchdog starts a goroutine that calls cancel if no activity is recorded
// on mark within the idle window. It returns a stop function that must be
// called when streaming ends (always, on both success and error paths) so the
// goroutine exits. The watchdog samples at idle/2 granularity; time.Since is
// computed against mark, which the caller updates atomically on each read.
//
// If idle <= 0 the watchdog is a no-op and the returned stop function is a no-op.
func idleWatchdog(idle time.Duration, mark *atomic.Int64, cancel context.CancelFunc) (stop func()) {
	if idle <= 0 {
		return func() {}
	}
	done := make(chan struct{})
	interval := idle / 2
	if interval <= 0 {
		interval = idle
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				if time.Since(time.Unix(0, mark.Load())) > idle {
					cancel()
					return
				}
			}
		}
	}()
	return func() { close(done) }
}

// copyResponseBody streams src to w, flushing when possible, and aborts the
// upstream read via cancel if no bytes arrive within the idle window. This
// bounds the response-body phase without imposing a flat total deadline, so a
// long but active stream is allowed to continue while a stalled connection is
// reclaimed (review M5). The caller owns cancel (typically from the request
// context); cancel may be nil when idle <= 0.
func copyResponseBody(w http.ResponseWriter, src io.Reader, idle time.Duration, cancel context.CancelFunc, logger *log.Logger, name string) {
	flusher, _ := w.(http.Flusher)
	var last atomic.Int64
	last.Store(time.Now().UnixNano())
	stop := idleWatchdog(idle, &last, cancel)
	defer stop()
	buf := make([]byte, 32*1024)
	for {
		n, readErr := src.Read(buf)
		if n > 0 {
			last.Store(time.Now().UnixNano())
			if _, werr := w.Write(buf[:n]); werr != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if readErr != nil {
			if readErr != io.EOF && logger != nil {
				logger.Printf("proxy %s: response body read error: %v", name, readErr)
			}
			return
		}
	}
}
