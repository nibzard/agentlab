package daemon

import (
	"database/sql"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/agentlab/agentlab/internal/db"
	"github.com/agentlab/agentlab/internal/models"
	"github.com/agentlab/agentlab/internal/secrets"
)

// MetadataAPI serves the cloud-standard metadata endpoint for guest sandboxes.
//
// The metadata API is accessible at 169.254.169.254 (via routing) and provides
// sandbox self-discovery, metadata key-value pairs, and on-demand secret access.
// It replaces the bootstrap API for sandbox self-awareness and reduces per-sandbox
// configuration by making sandbox identity and metadata available at runtime.
//
// Endpoints:
//   - GET  /metadata/             - Index listing available endpoints
//   - GET  /metadata/identity     - Sandbox identity (name, ID, profile, state)
//   - GET  /metadata/metadata     - Sandbox metadata key-value pairs
//   - GET  /metadata/secrets/{name} - Access a specific secret value
type MetadataAPI struct {
	store         *db.Store
	secretsStore  secrets.Store
	secretsBundle string
	agentSubnet   *net.IPNet
	rateLimiter   *IPRateLimiter
	logger        *log.Logger
}

// NewMetadataAPI creates a new metadata API instance.
func NewMetadataAPI(store *db.Store, secretsStore secrets.Store, secretsBundle string, agentSubnet *net.IPNet, rateLimiter *IPRateLimiter, logger *log.Logger) *MetadataAPI {
	bundle := strings.TrimSpace(secretsBundle)
	if bundle == "" {
		bundle = "default"
	}
	if logger == nil {
		logger = log.Default()
	}
	return &MetadataAPI{
		store:         store,
		secretsStore:  secretsStore,
		secretsBundle: bundle,
		agentSubnet:   agentSubnet,
		rateLimiter:   rateLimiter,
		logger:        logger,
	}
}

// Register mounts all metadata routes onto the given mux.
func (api *MetadataAPI) Register(mux *http.ServeMux) {
	if mux == nil {
		return
	}
	mux.HandleFunc("/metadata/", api.handleIndex)
	mux.HandleFunc("/metadata/identity", api.handleIdentity)
	mux.HandleFunc("/metadata/metadata", api.handleMetadata)
	mux.HandleFunc("/metadata/secrets/", api.handleSecrets)
}

func (api *MetadataAPI) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, []string{http.MethodGet})
		return
	}
	if !api.remoteAllowed(r.RemoteAddr) {
		writeError(w, http.StatusForbidden, "metadata access restricted to agent subnet")
		return
	}
	if api.rateLimiter != nil && !api.rateLimiter.Allow(r.RemoteAddr) {
		writeRateLimitExceeded(w)
		return
	}
	resp := MetadataIndexResponse{
		Endpoints: []MetadataEndpoint{
			{Path: "/metadata/identity", Method: http.MethodGet, Description: "Sandbox identity (vmid, name, profile, state)"},
			{Path: "/metadata/metadata", Method: http.MethodGet, Description: "Sandbox metadata key-value pairs"},
			{Path: "/metadata/secrets/{name}", Method: http.MethodGet, Description: "Access a specific secret value by name"},
			{Path: "/proxy/{name}/...", Method: http.MethodGet, Description: "Credential proxy: forward requests with injected credentials (HTTP, Git, LLM)"},
			{Path: "/proxy/{name}/...", Method: http.MethodPost, Description: "Credential proxy: forward requests with injected credentials (HTTP, Git, LLM)"},
		},
	}
	writeJSON(w, http.StatusOK, resp)
	api.auditLog(r.RemoteAddr, "/metadata/", r.Method, nil)
}

func (api *MetadataAPI) handleIdentity(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, []string{http.MethodGet})
		return
	}
	if !api.remoteAllowed(r.RemoteAddr) {
		writeError(w, http.StatusForbidden, "metadata access restricted to agent subnet")
		return
	}
	if api.rateLimiter != nil && !api.rateLimiter.Allow(r.RemoteAddr) {
		writeRateLimitExceeded(w)
		return
	}
	sandbox, err := api.sandboxByRemoteIP(r)
	if err != nil {
		writeError(w, http.StatusNotFound, "sandbox not found for source IP")
		return
	}
	resp := MetadataIdentityResponse{
		VMID:        sandbox.VMID,
		Name:        sandbox.Name,
		Profile:     sandbox.Profile,
		State:       string(sandbox.State),
		IP:          sandbox.IP,
		CreatedAt:   sandbox.CreatedAt.Format("2006-01-02T15:04:05Z"),
	}
	if sandbox.WorkspaceID != nil {
		resp.WorkspaceID = *sandbox.WorkspaceID
	}
	writeJSON(w, http.StatusOK, resp)
	api.auditLog(r.RemoteAddr, "/metadata/identity", r.Method, sandbox)
}

func (api *MetadataAPI) handleMetadata(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, []string{http.MethodGet})
		return
	}
	if !api.remoteAllowed(r.RemoteAddr) {
		writeError(w, http.StatusForbidden, "metadata access restricted to agent subnet")
		return
	}
	if api.rateLimiter != nil && !api.rateLimiter.Allow(r.RemoteAddr) {
		writeRateLimitExceeded(w)
		return
	}
	sandbox, err := api.sandboxByRemoteIP(r)
	if err != nil {
		writeError(w, http.StatusNotFound, "sandbox not found for source IP")
		return
	}
	// Load metadata from the secrets bundle associated with this sandbox's profile.
	metadata := api.loadMetadata(r)
	resp := MetadataMetadataResponse{
		Metadata: metadata,
	}
	// Always include sandbox identity metadata.
	if resp.Metadata == nil {
		resp.Metadata = make(map[string]string)
	}
	resp.Metadata["sandbox_name"] = sandbox.Name
	resp.Metadata["sandbox_vmid"] = formatVMID(sandbox.VMID)
	resp.Metadata["sandbox_profile"] = sandbox.Profile
	writeJSON(w, http.StatusOK, resp)
	api.auditLog(r.RemoteAddr, "/metadata/metadata", r.Method, sandbox)
}

func (api *MetadataAPI) handleSecrets(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, []string{http.MethodGet})
		return
	}
	if !api.remoteAllowed(r.RemoteAddr) {
		writeError(w, http.StatusForbidden, "metadata access restricted to agent subnet")
		return
	}
	if api.rateLimiter != nil && !api.rateLimiter.Allow(r.RemoteAddr) {
		writeRateLimitExceeded(w)
		return
	}
	// Extract secret name from path: /metadata/secrets/{name}
	path := strings.TrimPrefix(r.URL.Path, "/metadata/secrets/")
	name := strings.TrimSpace(path)
	if name == "" {
		writeError(w, http.StatusBadRequest, "secret name is required")
		return
	}
	sandbox, err := api.sandboxByRemoteIP(r)
	if err != nil {
		writeError(w, http.StatusNotFound, "sandbox not found for source IP")
		return
	}
	value, err := api.loadSecret(r, name)
	if err != nil {
		writeError(w, http.StatusNotFound, "secret not found")
		return
	}
	writeJSON(w, http.StatusOK, MetadataSecretResponse{
		Name:  name,
		Value: value,
	})
	api.auditLog(r.RemoteAddr, "/metadata/secrets/"+name, r.Method, sandbox)
}

// remoteAllowed checks if the request originates from the agent subnet.
func (api *MetadataAPI) remoteAllowed(addr string) bool {
	if api.agentSubnet == nil {
		return true
	}
	ip := parseRemoteIP(addr)
	if ip == nil || ip.IsUnspecified() {
		return false
	}
	return api.agentSubnet.Contains(ip)
}

// sandboxByRemoteIP identifies the sandbox from the request's source IP.
func (api *MetadataAPI) sandboxByRemoteIP(r *http.Request) (*models.Sandbox, error) {
	if api == nil || api.store == nil {
		return nil, sql.ErrNoRows
	}
	ip := parseRemoteIP(r.RemoteAddr)
	if ip == nil || ip.IsUnspecified() {
		return nil, sql.ErrNoRows
	}
	sb, err := api.store.GetSandboxByIP(r.Context(), ip.String())
	if err != nil {
		return nil, err
	}
	return &sb, nil
}

// loadMetadata loads the metadata map from the secrets bundle.
func (api *MetadataAPI) loadMetadata(r *http.Request) map[string]string {
	if api.secretsStore.Dir == "" {
		return nil
	}
	bundle, err := api.secretsStore.Load(r.Context(), api.secretsBundle)
	if err != nil {
		return nil
	}
	if len(bundle.Metadata) == 0 {
		return nil
	}
	// Return a copy to prevent mutation.
	out := make(map[string]string, len(bundle.Metadata))
	for k, v := range bundle.Metadata {
		out[k] = v
	}
	return out
}

// loadSecret loads a specific secret value from the bundle's env section.
func (api *MetadataAPI) loadSecret(r *http.Request, name string) (string, error) {
	if api.secretsStore.Dir == "" {
		return "", sql.ErrNoRows
	}
	bundle, err := api.secretsStore.Load(r.Context(), api.secretsBundle)
	if err != nil {
		return "", err
	}
	// Check env section first.
	if bundle.Env != nil {
		if v, ok := bundle.Env[name]; ok {
			return v, nil
		}
	}
	// Check metadata section.
	if bundle.Metadata != nil {
		if v, ok := bundle.Metadata[name]; ok {
			return v, nil
		}
	}
	return "", sql.ErrNoRows
}

func formatVMID(vmid int) string {
	return strconv.Itoa(vmid)
}

// auditLog records a metadata API access event. It logs the sandbox IP,
// the endpoint accessed, and optionally the sandbox name if identified.
func (api *MetadataAPI) auditLog(remoteAddr, endpoint, method string, sandbox *models.Sandbox) {
	if api.logger == nil {
		return
	}
	ip := parseRemoteIP(remoteAddr)
	ipStr := ""
	if ip != nil {
		ipStr = ip.String()
	}
	name := ""
	vmid := ""
	if sandbox != nil {
		name = sandbox.Name
		vmid = formatVMID(sandbox.VMID)
	}
	api.logger.Printf("metadata: %s %s sandbox=%s vmid=%s ip=%s", method, endpoint, name, vmid, ipStr)
}
