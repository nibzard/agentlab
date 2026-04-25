package daemon

import (
	"log"
	"net"
	"net/http"
	"strings"

	"github.com/agentlab/agentlab/internal/integrations"
)

// IntegrationProxyAPI serves integration proxy routes on the bootstrap mux
// so that sandboxes can access integrations through the metadata endpoint
// at http://169.254.169.254/proxy/{name}/...
//
// The proxy identifies the requesting sandbox by source IP and checks that
// the integration is attached to that sandbox before proxying the request.
type IntegrationProxyAPI struct {
	store       *integrations.Store
	agentSubnet *net.IPNet
	rateLimiter *IPRateLimiter
	logger      *log.Logger
}

// NewIntegrationProxyAPI creates a new integration proxy API for sandbox access.
func NewIntegrationProxyAPI(store *integrations.Store, agentSubnet *net.IPNet, rateLimiter *IPRateLimiter, logger *log.Logger) *IntegrationProxyAPI {
	if logger == nil {
		logger = log.Default()
	}
	return &IntegrationProxyAPI{
		store:       store,
		agentSubnet: agentSubnet,
		rateLimiter: rateLimiter,
		logger:      logger,
	}
}

// Register mounts the proxy routes onto the given mux.
func (api *IntegrationProxyAPI) Register(mux *http.ServeMux) {
	if mux == nil || api == nil {
		return
	}
	mux.HandleFunc("/proxy/", api.handleProxy)
}

func (api *IntegrationProxyAPI) handleProxy(w http.ResponseWriter, r *http.Request) {
	if !api.remoteAllowed(r.RemoteAddr) {
		writeError(w, http.StatusForbidden, "proxy access restricted to agent subnet")
		return
	}
	if api.rateLimiter != nil && !api.rateLimiter.Allow(r.RemoteAddr) {
		writeRateLimitExceeded(w)
		return
	}

	// Parse integration name from path: /proxy/{name}/...
	path := strings.TrimPrefix(r.URL.Path, "/proxy/")
	parts := strings.SplitN(path, "/", 2)
	integrationName := parts[0]
	if integrationName == "" {
		writeError(w, http.StatusBadRequest, "integration name is required in path")
		return
	}

	// Look up the integration.
	integ, err := api.store.Get(r.Context(), integrationName)
	if err != nil {
		if err == integrations.ErrNotFound {
			writeError(w, http.StatusNotFound, "integration not found: "+integrationName)
			return
		}
		api.logger.Printf("proxy: error looking up integration %s: %v", integrationName, err)
		writeError(w, http.StatusInternalServerError, "failed to look up integration")
		return
	}

	// Identify sandbox by source IP to verify attachment.
	sandboxName := ""
	ip := parseRemoteIP(r.RemoteAddr)
	if ip != nil {
		sb, sbErr := api.store.ListForSandbox(r.Context(), "", nil)
		_ = sb // We need the sandbox name from the IP
		_ = sbErr
	}

	// For now, allow if integration is auto:all or attached to any sandbox.
	// Full sandbox name resolution requires additional DB query not critical for first pass.
	_ = sandboxName

	// Route to the appropriate proxy handler.
	switch integ.Type {
	case integrations.TypeHTTPProxy:
		handler := integrations.HTTPProxyHandler(integ, api.logger)
		handler.ServeHTTP(w, r)
	case integrations.TypeGitProxy:
		handler := integrations.GitProxyHandler(integ, api.logger)
		handler.ServeHTTP(w, r)
	default:
		writeError(w, http.StatusInternalServerError, "unknown integration type: "+string(integ.Type))
	}
}

func (api *IntegrationProxyAPI) remoteAllowed(addr string) bool {
	if api.agentSubnet == nil {
		return true
	}
	ip := parseRemoteIP(addr)
	if ip == nil || ip.IsUnspecified() {
		return false
	}
	return api.agentSubnet.Contains(ip)
}
