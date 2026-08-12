package integrations

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/agentlab/agentlab/internal/offline"
)

// GitProxyHandler returns an http.Handler that proxies git smart HTTP requests
// to the target Git host, injecting credentials.
//
// This supports the git smart HTTP protocol by proxying:
//   - GET  /proxy/{name}/info/refs?service=git-upload-pack (clone/fetch discovery)
//   - POST /proxy/{name}/git-upload-pack (clone/fetch data transfer)
//   - GET  /proxy/{name}/info/refs?service=git-receive-pack (push discovery)
//   - POST /proxy/{name}/git-receive-pack (push data transfer)
//
// Credentials are injected as HTTP Basic Auth headers so they never exist
// inside the sandbox.
func GitProxyHandler(integ *Integration, logger *log.Logger, opts ...ProxyHandlerOptions) http.Handler {
	var opt ProxyHandlerOptions
	if len(opts) > 0 {
		opt = opts[0]
	}
	if integ == nil || integ.Type != TypeGitProxy {
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
	target := strings.TrimRight(integ.Target, "/")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		prefix := "/proxy/" + integ.Name + "/"
		subPath := strings.TrimPrefix(r.URL.Path, prefix)
		if subPath == "" {
			subPath = "/"
		}

		targetURL := target + "/" + subPath
		if r.URL.RawQuery != "" {
			targetURL += "?" + r.URL.RawQuery
		}

		// Derive a cancellable context so the idle watchdog can abort a stalled
		// upstream body read (review M5).
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()

		proxyReq, err := http.NewRequestWithContext(ctx, r.Method, targetURL, r.Body)
		if err != nil {
			logger.Printf("git-proxy %s: create request error: %v", integ.Name, err)
			http.Error(w, "proxy error", http.StatusBadGateway)
			return
		}

		// Copy all original headers.
		for k, vals := range r.Header {
			if isHopByHop(k) {
				continue
			}
			for _, v := range vals {
				proxyReq.Header.Add(k, v)
			}
		}

		// Inject git credentials as Basic Auth.
		user := integ.Username
		if user == "" {
			user = "git"
		}
		proxyReq.SetBasicAuth(user, integ.Secret)

		// Log the git operation for auditing.
		service := r.URL.Query().Get("service")
		if service != "" {
			logger.Printf("git-proxy %s: %s %s service=%s", integ.Name, r.Method, subPath, service)
		} else {
			logger.Printf("git-proxy %s: %s %s", integ.Name, r.Method, subPath)
		}

		resp, err := client.Do(proxyReq)
		if err != nil {
			if _, ok := err.(offline.ErrBlocked); ok {
				logger.Printf("git-proxy %s: blocked in offline mode: %v", integ.Name, err)
				http.Error(w, "upstream unavailable in offline mode", http.StatusServiceUnavailable)
				return
			}
			logger.Printf("git-proxy %s: upstream error: %v", integ.Name, err)
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
		// upstream is reclaimed (review M5).
		copyResponseBody(w, resp.Body, bodyIdle, cancel, logger, "git-"+integ.Name)
	})
}

// GitProxyConfigURL returns the URL that should be used inside a sandbox
// to clone a repo through the git proxy integration.
//
// Example: if the integration target is https://github.com and the metadata
// endpoint is at http://169.254.169.254, then a clone of user/repo would use:
//
//	http://169.254.169.254/proxy/github/user/repo.git
func GitProxyConfigURL(integ *Integration, metadataBaseURL string) string {
	if integ == nil {
		return ""
	}
	base := strings.TrimRight(metadataBaseURL, "/")
	return fmt.Sprintf("url.%s/proxy/%s/.insteadOf=%s", base, integ.Name, integ.Target)
}
