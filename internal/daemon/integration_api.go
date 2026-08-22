package daemon

import (
	"log"
	"net/http"
	"strings"

	"github.com/agentlab/agentlab/internal/integrations"
)

// IntegrationAPI handles integration CRUD operations on the control API.
//
// The registry is a global resource: integrations resolve by name for every
// sandbox at request time, so remote callers are held to the
// integration.read / integration.write permissions and any sandbox-scoped
// token is refused (review F11). Deleting an integration needs the separate
// integration.delete grant, so a bare integration.write token cannot replace
// a live integration by deleting and recreating its name (review F11).
//
// Endpoints:
//   - POST   /v1/integrations          - Create a new integration
//   - GET    /v1/integrations          - List all integrations
//   - GET    /v1/integrations/{name}   - Get a specific integration
//   - DELETE /v1/integrations/{name}   - Delete an integration
type IntegrationAPI struct {
	store           *integrations.Store
	logger          *log.Logger
	targetAllowlist []string
}

// NewIntegrationAPI creates a new integration API handler.
func NewIntegrationAPI(store *integrations.Store, logger *log.Logger) *IntegrationAPI {
	if logger == nil {
		logger = log.Default()
	}
	return &IntegrationAPI{store: store, logger: logger}
}

// WithTargetAllowlist restricts the hosts a proxy integration may target.
// An empty allowlist (the default) keeps only the scheme and host sanity
// checks that Integration.Validate already performs.
func (api *IntegrationAPI) WithTargetAllowlist(hosts []string) *IntegrationAPI {
	if api == nil {
		return api
	}
	api.targetAllowlist = hosts
	return api
}

// Register mounts integration API routes onto the given mux.
func (api *IntegrationAPI) Register(mux *http.ServeMux) {
	if mux == nil || api == nil {
		return
	}
	mux.HandleFunc("/v1/integrations", api.handleIntegrations)
	mux.HandleFunc("/v1/integrations/", api.handleIntegrationByName)
}

// authorizeRead gates the registry listing on the integration.read
// permission. Targets and usernames are internal, and the registry is global,
// so sandbox-scoped tokens are denied.
func (api *IntegrationAPI) authorizeRead(w http.ResponseWriter, r *http.Request) bool {
	return authorizeStandalone(w, r, permIntegrationRead, true)
}

// authorizeWrite gates integration creation on the integration.write
// permission. A new integration can capture traffic from every matching
// sandbox, so sandbox-scoped tokens are denied outright.
func (api *IntegrationAPI) authorizeWrite(w http.ResponseWriter, r *http.Request) bool {
	return authorizeStandalone(w, r, permIntegrationWrite, true)
}

// authorizeDelete gates integration deletion on the integration.delete
// permission, a grant above bare integration.write: removing a live name and
// recreating it redirects every sandbox that resolves that name.
func (api *IntegrationAPI) authorizeDelete(w http.ResponseWriter, r *http.Request) bool {
	return authorizeStandalone(w, r, permIntegrationDelete, true)
}

func (api *IntegrationAPI) handleIntegrations(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		api.handleList(w, r)
	case http.MethodPost:
		api.handleCreate(w, r)
	default:
		writeMethodNotAllowed(w, []string{http.MethodGet, http.MethodPost})
	}
}

func (api *IntegrationAPI) handleIntegrationByName(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		api.handleGet(w, r)
	case http.MethodDelete:
		api.handleDelete(w, r)
	default:
		writeMethodNotAllowed(w, []string{http.MethodGet, http.MethodDelete})
	}
}

func (api *IntegrationAPI) handleCreate(w http.ResponseWriter, r *http.Request) {
	if !api.authorizeWrite(w, r) {
		return
	}
	var req V1IntegrationCreateRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeJSONDecodeError(w, err)
		return
	}

	// Parse attachment mode.
	attachMode, selector, err := parseAttachment(req.Attach)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// Tag selectors are normalized to match sandbox tags case-insensitively
	// (review M6). Sandbox-name attachment is left untouched since names are
	// case-sensitive identifiers.
	if attachMode == integrations.AttachTag {
		selector, err = integrations.NormalizeTagSelector(selector)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}

	integ := &integrations.Integration{
		Name:           strings.TrimSpace(req.Name),
		Type:           integrations.IntegrationType(req.Type),
		Target:         strings.TrimSpace(req.Target),
		Secret:         req.Secret,
		SecretType:     strings.TrimSpace(req.SecretType),
		SecretHeader:   strings.TrimSpace(req.SecretHeader),
		Username:       strings.TrimSpace(req.Username),
		Provider:       strings.TrimSpace(req.Provider),
		AttachMode:     attachMode,
		AttachSelector: selector,
	}

	// Default secret type.
	if integ.SecretType == "" {
		integ.SecretType = "bearer"
	}
	// Default type.
	if integ.Type == "" {
		integ.Type = integrations.TypeHTTPProxy
	}

	if err := integ.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// Operator policy on top of Validate: when an allowlist is configured,
	// a proxy target host must appear in it (review F11).
	if integ.Target != "" {
		if err := integrations.ValidateTarget(integ.Target, api.targetAllowlist); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}

	if err := api.store.Create(r.Context(), integ); err != nil {
		if err == integrations.ErrDuplicateName {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		api.logger.Printf("integration create error: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to create integration")
		return
	}

	api.logger.Printf("integration created: name=%s type=%s attach=%s:%s",
		integ.Name, integ.Type, integ.AttachMode, integ.AttachSelector)

	writeJSON(w, http.StatusCreated, integrationToResponse(integ))
}

func (api *IntegrationAPI) handleList(w http.ResponseWriter, r *http.Request) {
	if !api.authorizeRead(w, r) {
		return
	}
	integrations, err := api.store.List(r.Context())
	if err != nil {
		api.logger.Printf("integration list error: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to list integrations")
		return
	}
	items := make([]V1IntegrationResponse, 0, len(integrations))
	for _, integ := range integrations {
		items = append(items, integrationToResponse(integ))
	}
	writeJSON(w, http.StatusOK, V1IntegrationsResponse{Integrations: items})
}

func (api *IntegrationAPI) handleGet(w http.ResponseWriter, r *http.Request) {
	if !api.authorizeRead(w, r) {
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/v1/integrations/")
	name = strings.TrimSpace(name)
	if name == "" {
		writeError(w, http.StatusBadRequest, "integration name is required")
		return
	}
	integ, err := api.store.Get(r.Context(), name)
	if err != nil {
		if err == integrations.ErrNotFound {
			writeError(w, http.StatusNotFound, "integration not found")
			return
		}
		api.logger.Printf("integration get error: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to get integration")
		return
	}
	writeJSON(w, http.StatusOK, integrationToResponse(integ))
}

func (api *IntegrationAPI) handleDelete(w http.ResponseWriter, r *http.Request) {
	if !api.authorizeDelete(w, r) {
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/v1/integrations/")
	name = strings.TrimSpace(name)
	if name == "" {
		writeError(w, http.StatusBadRequest, "integration name is required")
		return
	}
	if err := api.store.Delete(r.Context(), name); err != nil {
		if err == integrations.ErrNotFound {
			writeError(w, http.StatusNotFound, "integration not found")
			return
		}
		api.logger.Printf("integration delete error: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to delete integration")
		return
	}
	api.logger.Printf("integration deleted: name=%s", name)
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted", "name": name})
}

// V1IntegrationCreateRequest is the request body for creating an integration.
type V1IntegrationCreateRequest struct {
	Name         string `json:"name"`
	Type         string `json:"type"`          // "http-proxy", "git-proxy", or "llm-proxy"
	Target       string `json:"target"`        // Target URL for proxy
	Secret       string `json:"secret"`        // Secret value (API key, token, etc.)
	SecretType   string `json:"secret_type"`   // "bearer", "header", "basic-auth"
	SecretHeader string `json:"secret_header"` // Custom header name (for header type)
	Username     string `json:"username"`      // Username for basic-auth / git
	Provider     string `json:"provider"`      // LLM provider: "openai", "anthropic", "ollama" (for llm-proxy)
	Attach       string `json:"attach"`        // "sandbox:name", "tag:value", or "auto:all"
}

// V1IntegrationResponse is the response body for an integration.
// The secret is never included in API responses.
type V1IntegrationResponse struct {
	Name           string `json:"name"`
	Type           string `json:"type"`
	Target         string `json:"target,omitempty"`
	SecretType     string `json:"secret_type"`
	SecretHeader   string `json:"secret_header,omitempty"`
	Username       string `json:"username,omitempty"`
	Provider       string `json:"provider,omitempty"`
	AttachMode     string `json:"attach_mode"`
	AttachSelector string `json:"attach_selector,omitempty"`
	ProxyPath      string `json:"proxy_path"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
}

// V1IntegrationsResponse is the response for listing integrations.
type V1IntegrationsResponse struct {
	Integrations []V1IntegrationResponse `json:"integrations"`
}

// V1IntegrationStatusResponse shows integrations active for a sandbox.
type V1IntegrationStatusResponse struct {
	SandboxName  string                           `json:"sandbox_name"`
	Integrations []integrations.IntegrationStatus `json:"integrations"`
}

func integrationToResponse(integ *integrations.Integration) V1IntegrationResponse {
	return V1IntegrationResponse{
		Name:           integ.Name,
		Type:           string(integ.Type),
		Target:         integ.Target,
		SecretType:     integ.SecretType,
		SecretHeader:   integ.SecretHeader,
		Username:       integ.Username,
		Provider:       integ.Provider,
		AttachMode:     string(integ.AttachMode),
		AttachSelector: integ.AttachSelector,
		ProxyPath:      integ.ProxyPath(),
		CreatedAt:      integ.CreatedAt.Format("2006-01-02T15:04:05Z"),
		UpdatedAt:      integ.UpdatedAt.Format("2006-01-02T15:04:05Z"),
	}
}

// parseAttachment parses the --attach flag format into an AttachmentMode and selector.
// Formats: "sandbox:name", "tag:value", "auto:all"
func parseAttachment(attach string) (integrations.AttachmentMode, string, error) {
	attach = strings.TrimSpace(attach)
	if attach == "" {
		return integrations.AttachAutoAll, "", nil
	}
	parts := strings.SplitN(attach, ":", 2)
	if len(parts) != 2 {
		return "", "", BadAttachmentFormatError(attach)
	}
	mode, selector := parts[0], parts[1]
	switch mode {
	case "sandbox":
		return integrations.AttachSandbox, selector, nil
	case "tag":
		return integrations.AttachTag, selector, nil
	case "auto":
		if selector != "all" {
			return "", "", BadAttachmentFormatError(attach)
		}
		return integrations.AttachAutoAll, "", nil
	default:
		return "", "", BadAttachmentFormatError(attach)
	}
}

func BadAttachmentFormatError(attach string) error {
	return BadAttachmentFormat(attach)
}

type badAttachFormat string

func (e badAttachFormat) Error() string {
	return "invalid attach format " + string(e) + ": must be sandbox:<name>, tag:<value>, or auto:all"
}

func BadAttachmentFormat(attach string) error {
	return badAttachFormat(attach)
}
