// Package tailcatexperiment implements the isolated, opt-in transport prototype.
package tailcatexperiment

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"
)

const IdentityHeader = "X-AgentLab-Server-Identity"

// APITarget permits a fixed, literal loopback API endpoint. It excludes the
// trusted Unix socket, user information, query parameters, and path rewrites.
func APITarget(raw string) (*url.URL, error) {
	target, err := url.Parse(raw)
	if err != nil || target.User != nil || target.RawQuery != "" || target.Fragment != "" || (target.Path != "" && target.Path != "/") || target.RawPath != "" || (target.Scheme != "http" && target.Scheme != "https") {
		return nil, errors.New("select a loopback HTTP or HTTPS API endpoint")
	}
	ip := net.ParseIP(target.Hostname())
	if ip == nil || !ip.IsLoopback() {
		return nil, errors.New("the prototype requires a literal loopback API address")
	}
	return target, nil
}

// APIProxy forwards only to the fixed authenticated TCP API. It never selects
// another target from request headers, paths, or environment proxy settings.
func APIProxy(target *url.URL, identity string, transport http.RoundTripper) (http.Handler, error) {
	if identity == "" || len(identity) > 128 || strings.ContainsAny(identity, "\x00\r\n") {
		return nil, errors.New("provide the expected server identity")
	}
	if target == nil {
		return nil, errors.New("provide a fixed API target")
	}
	checked, err := APITarget(target.String())
	if err != nil {
		return nil, err
	}
	proxy := httputil.NewSingleHostReverseProxy(checked)
	if transport == nil {
		transport = &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 10 * time.Second}).DialContext, ResponseHeaderTimeout: 30 * time.Second}
	}
	proxy.Transport = transport
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, _ error) {
		prototypeError(w, http.StatusBadGateway, "tailcat_upstream_unavailable", "Restore the selected TCP API before retrying.")
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" && !strings.HasPrefix(r.URL.Path, "/v1/") && r.URL.Path != "/v1" {
			prototypeError(w, http.StatusNotFound, "tailcat_api_path_required", "Use the authenticated AgentLab API.")
			return
		}
		values := r.Header.Values(IdentityHeader)
		if len(values) > 1 || (len(values) == 1 && values[0] != identity) {
			prototypeError(w, http.StatusConflict, "server_identity_mismatch", "Verify the original server identity before retrying.")
			return
		}
		request := r.Clone(r.Context())
		request.Header.Set(IdentityHeader, identity)
		request.Host = checked.Host
		request.Header.Del("Forwarded")
		request.Header.Del("X-Forwarded-For")
		request.Header.Del("X-Forwarded-Host")
		request.Header.Del("X-Forwarded-Proto")
		proxy.ServeHTTP(w, request)
	}), nil
}

func prototypeError(w http.ResponseWriter, status int, code, action string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]any{"state": "blocked", "code": code, "error": action, "action": action, "retryable": status == http.StatusBadGateway})
}
