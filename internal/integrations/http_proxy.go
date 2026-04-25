package integrations

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/agentlab/agentlab/internal/offline"
)

// HTTPProxyHandler returns an http.Handler that proxies requests to the
// integration's target URL, injecting the configured secret.
//
// Supported secret injection modes:
//   - bearer: Sets Authorization: Bearer <secret>
//   - header: Sets a custom header (SecretHeader) to the secret value
//   - basic-auth: Sets Authorization: Basic <base64(username:secret)>
func HTTPProxyHandler(integ *Integration, logger *log.Logger, opts ...ProxyHandlerOptions) http.Handler {
	var opt ProxyHandlerOptions
	if len(opts) > 0 {
		opt = opts[0]
	}
	if integ == nil || integ.Type != TypeHTTPProxy {
		return http.NotFoundHandler()
	}
	if logger == nil {
		logger = log.Default()
	}
	transport := http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     90 * time.Second,
	}
	if opt.Offline {
		transport.Proxy = nil
	}
	var roundTripper http.RoundTripper = &transport
	if opt.Offline {
		roundTripper = offline.NewOfflineTransport(&transport)
	}
	client := &http.Client{
		Transport: roundTripper,
		Timeout:   60 * time.Second,
	}
	target := strings.TrimRight(integ.Target, "/")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Strip the /proxy/{name}/ prefix to get the sub-path.
		prefix := "/proxy/" + integ.Name + "/"
		subPath := strings.TrimPrefix(r.URL.Path, prefix)
		if subPath == "" {
			subPath = "/"
		}

		targetURL := target + "/" + subPath
		if r.URL.RawQuery != "" {
			targetURL += "?" + r.URL.RawQuery
		}

		proxyReq, err := http.NewRequestWithContext(r.Context(), r.Method, targetURL, r.Body)
		if err != nil {
			logger.Printf("http-proxy %s: create request error: %v", integ.Name, err)
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

		// Inject the secret.
		injectSecret(proxyReq, integ)

		resp, err := client.Do(proxyReq)
		if err != nil {
			if _, ok := err.(offline.ErrBlocked); ok {
				logger.Printf("http-proxy %s: blocked in offline mode: %v", integ.Name, err)
				http.Error(w, "upstream unavailable in offline mode", http.StatusServiceUnavailable)
				return
			}
			logger.Printf("http-proxy %s: upstream error: %v", integ.Name, err)
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
		_, _ = io.Copy(w, resp.Body)
	})
}

// injectSecret adds the configured secret to the request headers.
func injectSecret(req *http.Request, integ *Integration) {
	if integ == nil || integ.Secret == "" {
		return
	}
	secretType := integ.SecretType
	if secretType == "" {
		secretType = "bearer"
	}
	switch secretType {
	case "bearer":
		req.Header.Set("Authorization", "Bearer "+integ.Secret)
	case "header":
		headerName := integ.SecretHeader
		if headerName == "" {
			headerName = "X-Api-Key"
		}
		req.Header.Set(headerName, integ.Secret)
	case "basic-auth":
		user := integ.Username
		if user == "" {
			user = "git"
		}
		req.SetBasicAuth(user, integ.Secret)
	}
}

// isHopByHop returns true for hop-by-hop headers that should not be forwarded.
func isHopByHop(header string) bool {
	switch strings.ToLower(header) {
	case "connection", "keep-alive", "proxy-authenticate",
		"proxy-authorization", "te", "trailers",
		"transfer-encoding", "upgrade":
		return true
	}
	return false
}

// FormatIntegrationTarget returns a display-safe version of the target (redacting secrets).
func FormatIntegrationTarget(integ *Integration) string {
	if integ == nil {
		return ""
	}
	target := integ.Target
	if target == "" {
		return ""
	}
	// Redact any embedded credentials in URL.
	if idx := strings.Index(target, "://"); idx >= 0 {
		scheme := target[:idx+3]
		rest := target[idx+3:]
		if atIdx := strings.Index(rest, "@"); atIdx >= 0 {
			rest = "***@" + rest[atIdx+1:]
		}
		target = scheme + rest
	}
	return fmt.Sprintf("%s (via /proxy/%s/)", target, integ.Name)
}
