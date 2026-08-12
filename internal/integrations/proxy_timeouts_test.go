package integrations

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveProxyTimeouts_Defaults(t *testing.T) {
	connect, header, idle := resolveProxyTimeouts(ProxyHandlerOptions{})
	assert.Equal(t, defaultProxyConnectTimeout, connect)
	assert.Equal(t, defaultProxyResponseHeaderTimeout, header)
	assert.Equal(t, defaultProxyBodyIdleTimeout, idle)
}

func TestResolveProxyTimeouts_Overrides(t *testing.T) {
	connect, header, idle := resolveProxyTimeouts(ProxyHandlerOptions{
		ConnectTimeout:          5 * time.Second,
		ResponseHeaderTimeout:   20 * time.Second,
		ResponseBodyIdleTimeout: 7 * time.Second,
	})
	assert.Equal(t, 5*time.Second, connect)
	assert.Equal(t, 20*time.Second, header)
	assert.Equal(t, 7*time.Second, idle)
}

func TestResolveProxyTimeouts_NonPositiveFallsBack(t *testing.T) {
	connect, header, idle := resolveProxyTimeouts(ProxyHandlerOptions{
		ConnectTimeout:          -1,
		ResponseHeaderTimeout:   0,
		ResponseBodyIdleTimeout: -2,
	})
	assert.Equal(t, defaultProxyConnectTimeout, connect)
	assert.Equal(t, defaultProxyResponseHeaderTimeout, header)
	assert.Equal(t, defaultProxyBodyIdleTimeout, idle)
}

func TestNewProxyTransport_BoundsConnectAndHeader(t *testing.T) {
	tr := newProxyTransport(ProxyHandlerOptions{
		ConnectTimeout:        7 * time.Second,
		ResponseHeaderTimeout: 42 * time.Second,
	})
	assert.Equal(t, 42*time.Second, tr.ResponseHeaderTimeout)
	// DialContext is set (non-nil) so the dial phase is bounded.
	assert.NotNil(t, tr.DialContext)
	// HTTP/2 negotiation is enabled for efficient long streams.
	assert.True(t, tr.ForceAttemptHTTP2)
}

func TestNewProxyTransport_OfflineDisablesEnvProxy(t *testing.T) {
	tr := newProxyTransport(ProxyHandlerOptions{Offline: true})
	assert.Nil(t, tr.Proxy)

	tr2 := newProxyTransport(ProxyHandlerOptions{Offline: false})
	assert.NotNil(t, tr2.Proxy)
}

// TestIdleWatchdog_AbortsOnStall proves a reader that never records activity
// is cancelled once the idle window elapses.
func TestIdleWatchdog_AbortsOnStall(t *testing.T) {
	mark := &atomic.Int64{}
	mark.Store(time.Now().UnixNano())
	canceled := make(chan struct{})
	var once sync.Once
	cancel := func() { once.Do(func() { close(canceled) }) }

	stop := idleWatchdog(50*time.Millisecond, mark, cancel)
	defer stop()

	select {
	case <-canceled:
		// expected: stall exceeded the idle window
	case <-time.After(2 * time.Second):
		t.Fatal("watchdog did not cancel after idle window")
	}
}

// TestIdleWatchdog_KeepsActiveStreamAlive proves that as long as bytes keep
// flowing within the idle window, the watchdog never cancels — the property
// that lets a long stream exceed the former flat 5m cap (review M5).
func TestIdleWatchdog_KeepsActiveStreamAlive(t *testing.T) {
	mark := &atomic.Int64{}
	mark.Store(time.Now().UnixNano())
	canceled := make(chan struct{})
	var once sync.Once
	cancel := func() { once.Do(func() { close(canceled) }) }

	stop := idleWatchdog(40*time.Millisecond, mark, cancel)
	defer stop()

	deadline := time.After(250 * time.Millisecond)
	for {
		select {
		case <-deadline:
			return // survived without cancellation
		case <-canceled:
			t.Fatal("watchdog canceled an active stream")
		case <-time.After(10 * time.Millisecond):
			mark.Store(time.Now().UnixNano())
		}
	}
}

// ctxReader blocks on Read until ctx is done, then returns the context error.
// It mimics how an upstream transport body unblocks once the request context
// is cancelled.
type ctxReader struct {
	ctx context.Context
}

func (c *ctxReader) Read(p []byte) (int, error) {
	select {
	case <-c.ctx.Done():
		return 0, c.ctx.Err()
	case <-time.After(5 * time.Second):
		return 0, fmt.Errorf("read timeout")
	}
}

// TestCopyResponseBody_AbortsIdleUpstream proves the body copier cancels a
// stalled upstream and returns rather than hanging forever (review M5).
func TestCopyResponseBody_AbortsIdleUpstream(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rec := httptest.NewRecorder()
	reader := &ctxReader{ctx: ctx}

	done := make(chan struct{})
	go func() {
		copyResponseBody(rec, reader, 60*time.Millisecond, cancel, nil, "test")
		close(done)
	}()

	select {
	case <-done:
		// expected
	case <-time.After(2 * time.Second):
		t.Fatal("copyResponseBody did not return after idle cancellation")
	}
	assert.Contains(t, rec.Body.String(), "") // nothing streamed from a stalled body
}

// TestCopyResponseBody_StreamsActiveBody proves bytes are copied through when
// the body produces data within the idle window.
func TestCopyResponseBody_StreamsActiveBody(t *testing.T) {
	_, cancel := context.WithCancel(context.Background())
	defer cancel()
	rec := httptest.NewRecorder()
	payload := bytes.Repeat([]byte("agentlab\n"), 1000)
	reader := bytes.NewReader(payload)

	done := make(chan struct{})
	go func() {
		copyResponseBody(rec, reader, 1*time.Second, cancel, nil, "test")
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("copyResponseBody did not return")
	}
	assert.Equal(t, string(payload), rec.Body.String())
}

// --- End-to-end handler tests against a real upstream ---

func newLLMInteg(target string) *Integration {
	return &Integration{
		Name:   "test",
		Type:   TypeLLMProxy,
		Target: target,
	}
}

// TestLLMProxy_AbortsStalledUpstreamBody: the upstream returns headers and one
// chunk, then hangs. The proxy must abort via the idle watchdog and return.
func TestLLMProxy_AbortsStalledUpstreamBody(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("first\n"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		// Block until the proxy aborts (closes the connection), which cancels
		// this request context. Avoid select{} so the test server can shut down.
		<-r.Context().Done()
	}))
	defer upstream.Close()

	handler := LLMProxyHandler(newLLMInteg(upstream.URL), nil, ProxyHandlerOptions{
		ResponseBodyIdleTimeout: 100 * time.Millisecond,
	})

	srv := httptest.NewServer(handler)
	defer srv.Close()

	done := make(chan struct{})
	var resp *http.Response
	var err error
	go func() {
		resp, err = http.Get(srv.URL + "/v1/chat/completions")
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("proxy did not return after upstream stall")
	}
	require.NoError(t, err)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	assert.Contains(t, string(body), "first")
}

// TestLLMProxy_StreamsActiveBody: the upstream trickles chunks faster than the
// idle window; the proxy must relay all of them without aborting.
func TestLLMProxy_StreamsActiveBody(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		for i := 0; i < 10; i++ {
			_, _ = fmt.Fprintf(w, "chunk-%d\n", i)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			time.Sleep(15 * time.Millisecond) // well within the idle window
		}
	}))
	defer upstream.Close()

	handler := LLMProxyHandler(newLLMInteg(upstream.URL), nil, ProxyHandlerOptions{
		ResponseBodyIdleTimeout: 500 * time.Millisecond,
	})

	srv := httptest.NewServer(handler)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/v1/chat/completions")
	require.NoError(t, err)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	for i := 0; i < 10; i++ {
		assert.Contains(t, string(body), fmt.Sprintf("chunk-%d", i))
	}
}
