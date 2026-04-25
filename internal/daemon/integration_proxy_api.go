package daemon

import (
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/agentlab/agentlab/internal/db"
	"github.com/agentlab/agentlab/internal/integrations"
)

// IntegrationProxyAPI serves integration proxy routes on the bootstrap mux
// so that sandboxes can access integrations through the metadata endpoint
// at http://169.254.169.254/proxy/{name}/...
//
// The proxy identifies the requesting sandbox by source IP and checks that
// the integration is attached to that sandbox before proxying the request.
// All proxy requests are audit-logged with sandbox identity and integration name.
type IntegrationProxyAPI struct {
	intStore    *integrations.Store
	dbStore     *db.Store
	agentSubnet *net.IPNet
	rateLimiter *IPRateLimiter
	logger      *log.Logger
	offline     bool
}

// NewIntegrationProxyAPI creates a new integration proxy API for sandbox access.
func NewIntegrationProxyAPI(intStore *integrations.Store, dbStore *db.Store, agentSubnet *net.IPNet, rateLimiter *IPRateLimiter, logger *log.Logger, offline bool) *IntegrationProxyAPI {
	if logger == nil {
		logger = log.Default()
	}
	return &IntegrationProxyAPI{
		intStore:    intStore,
		dbStore:     dbStore,
		agentSubnet: agentSubnet,
		rateLimiter: rateLimiter,
		logger:      logger,
		offline:     offline,
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
	integ, err := api.intStore.Get(r.Context(), integrationName)
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
	sandboxName, sandboxTags := api.resolveSandbox(r)
	if sandboxName == "" && integ.AttachMode != integrations.AttachAutoAll {
		// If we can't identify the sandbox and integration isn't auto:all, deny.
		writeError(w, http.StatusForbidden, "sandbox not identified for integration access")
		return
	}

	// Verify the integration is attached to this sandbox.
	if !integ.MatchesSandbox(sandboxName, sandboxTags) {
		writeError(w, http.StatusForbidden, "integration not attached to this sandbox")
		return
	}

	// Audit log the proxy access.
	api.auditProxyAccess(r, integ, sandboxName)

	// Route to the appropriate proxy handler.
	opts := integrations.ProxyHandlerOptions{Offline: api.offline}
	switch integ.Type {
	case integrations.TypeHTTPProxy:
		handler := integrations.HTTPProxyHandler(integ, api.logger, opts)
		handler.ServeHTTP(w, r)
	case integrations.TypeGitProxy:
		handler := integrations.GitProxyHandler(integ, api.logger, opts)
		handler.ServeHTTP(w, r)
	case integrations.TypeLLMProxy:
		handler := integrations.LLMProxyHandler(integ, api.logger, opts)
		handler.ServeHTTP(w, r)
	default:
		writeError(w, http.StatusInternalServerError, "unknown integration type: "+string(integ.Type))
	}
}

// resolveSandbox identifies the sandbox from the request's source IP.
// Returns the sandbox name and its tags.
func (api *IntegrationProxyAPI) resolveSandbox(r *http.Request) (string, []string) {
	if api.dbStore == nil {
		return "", nil
	}
	ip := parseRemoteIP(r.RemoteAddr)
	if ip == nil || ip.IsUnspecified() {
		return "", nil
	}
	sb, err := api.dbStore.GetSandboxByIP(r.Context(), ip.String())
	if err != nil {
		return "", nil
	}
	tags := parseTags(sb.Tags)
	return sb.Name, tags
}

// parseTags splits a comma-separated tag string into a slice.
func parseTags(tagStr string) []string {
	if tagStr == "" {
		return nil
	}
	var result []string
	for _, t := range strings.Split(tagStr, ",") {
		t = strings.TrimSpace(t)
		if t != "" {
			result = append(result, t)
		}
	}
	return result
}

// auditProxyAccess logs credential proxy access with sandbox identity.
func (api *IntegrationProxyAPI) auditProxyAccess(r *http.Request, integ *integrations.Integration, sandboxName string) {
	ip := parseRemoteIP(r.RemoteAddr)
	ipStr := ""
	if ip != nil {
		ipStr = ip.String()
	}
	api.logger.Printf("credential-proxy: sandbox=%s integration=%s type=%s method=%s path=%s ip=%s",
		sandboxName, integ.Name, integ.Type, r.Method, r.URL.Path, ipStr)
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

// proxyResponseWriter wraps http.ResponseWriter to track bytes written
// for per-credential-type usage accounting.
type proxyResponseWriter struct {
	http.ResponseWriter
	bytesWritten int
}

func (pw *proxyResponseWriter) Write(b []byte) (int, error) {
	n, err := pw.ResponseWriter.Write(b)
	pw.bytesWritten += n
	return n, err
}

// CredentialAuditEntry records a credential proxy access for auditing.
type CredentialAuditEntry struct {
	SandboxName    string    `json:"sandbox_name"`
	Integration    string    `json:"integration"`
	Type           string    `json:"type"`
	Method         string    `json:"method"`
	Path           string    `json:"path"`
	SourceIP       string    `json:"source_ip"`
	ResponseStatus int       `json:"response_status"`
	BytesWritten   int       `json:"bytes_written"`
	Timestamp      time.Time `json:"timestamp"`
}
