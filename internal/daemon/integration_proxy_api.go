package daemon

import (
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/agentlab/agentlab/internal/db"
	"github.com/agentlab/agentlab/internal/integrations"
	"github.com/agentlab/agentlab/internal/models"
)

// IntegrationProxyAPI serves integration proxy routes on the bootstrap mux
// so that sandboxes can access integrations through the metadata endpoint
// at http://169.254.169.254/proxy/{name}/...
//
// The proxy identifies the requesting sandbox by source IP, then requires the
// per-sandbox secret issued at bootstrap before it trusts that identification.
// The source IP selects the candidate; the secret authorizes it (review F4).
// It then checks that the integration is attached to that sandbox before
// proxying the request. All proxy requests are audit-logged with sandbox
// identity and integration name.
type IntegrationProxyAPI struct {
	intStore    *integrations.Store
	dbStore     *db.Store
	agentSubnet *net.IPNet
	rateLimiter *IPRateLimiter
	logger      *log.Logger
	offline     bool
	// trustSubnet, when true, serves auto:all integrations to any host in the
	// agent subnet without resolving it to a registered live sandbox. Insecure;
	// off by default (review H4).
	trustSubnet bool
}

// NewIntegrationProxyAPI creates a new integration proxy API for sandbox access.
func NewIntegrationProxyAPI(intStore *integrations.Store, dbStore *db.Store, agentSubnet *net.IPNet, rateLimiter *IPRateLimiter, logger *log.Logger, offline bool, trustSubnet bool) *IntegrationProxyAPI {
	if logger == nil {
		logger = log.Default()
	}
	if trustSubnet {
		logger.Printf("credential-proxy: WARNING trust_agent_subnet is enabled — auto:all integrations are served to any host in the agent subnet without sandbox identification")
	}
	return &IntegrationProxyAPI{
		intStore:    intStore,
		dbStore:     dbStore,
		agentSubnet: agentSubnet,
		rateLimiter: rateLimiter,
		logger:      logger,
		offline:     offline,
		trustSubnet: trustSubnet,
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

	// Identify the calling sandbox from its source IP. Only a unique LIVE
	// sandbox is accepted: unidentified, stale, destroyed, or ambiguous sources
	// are rejected by default (review H4).
	sandbox, identified := api.sandboxBySourceIP(r)
	if identified {
		// The source IP only selects the candidate row. The per-sandbox
		// secret issued at bootstrap is what the caller must prove, so a
		// neighbor that spoofs this address gets nothing (review F4).
		if !verifySandboxSecret(r.Context(), api.dbStore, r, sandbox.VMID) {
			writeError(w, http.StatusForbidden, "invalid or missing sandbox secret")
			return
		}
	} else {
		// The sole permitted unidentified path is auto:all under an explicit,
		// warned trust_agent_subnet opt-in (subnet-wide trust). Every other
		// attachment mode requires a positively identified live sandbox.
		if !api.trustSubnet || integ.AttachMode != integrations.AttachAutoAll {
			writeError(w, http.StatusForbidden, "sandbox not identified for integration access")
			return
		}
	}
	sandboxName := sandbox.Name
	sandboxTags := parseTags(sandbox.Tags)

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

// sandboxBySourceIP resolves the calling sandbox from the request's source IP.
// Only a unique sandbox in an eligible live state counts as identified; a
// missing, stale, destroyed, or ambiguous source returns identified=false so
// the caller is rejected by default (review H4).
func (api *IntegrationProxyAPI) sandboxBySourceIP(r *http.Request) (models.Sandbox, bool) {
	if api.dbStore == nil {
		return models.Sandbox{}, false
	}
	ip := parseRemoteIP(r.RemoteAddr)
	if ip == nil || ip.IsUnspecified() {
		return models.Sandbox{}, false
	}
	sb, err := api.dbStore.GetLiveSandboxByIP(r.Context(), ip.String())
	if err != nil {
		return models.Sandbox{}, false
	}
	return sb, true
}

// resolveSandbox identifies the calling sandbox by source IP and returns its
// name and tags. Identification alone does not authorize proxy access; the
// caller must also present the sandbox secret (review F4).
func (api *IntegrationProxyAPI) resolveSandbox(r *http.Request) (name string, tags []string, identified bool) {
	sb, identified := api.sandboxBySourceIP(r)
	if !identified {
		return "", nil, false
	}
	return sb.Name, parseTags(sb.Tags), true
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
