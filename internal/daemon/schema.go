package daemon

import (
	"encoding/json"
	"github.com/agentlab/agentlab/internal/templatebuild"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"time"
)

const controlAPISchemaVersion = 1

type schemaResource struct {
	Path         string   `json:"path"`
	Methods      []string `json:"methods"`
	Summary      string   `json:"summary"`
	RequestType  string   `json:"request_type,omitempty"`
	ResponseType string   `json:"response_type"`
	Notes        string   `json:"notes,omitempty"`
}

type schemaResponse struct {
	Definitions        map[string]any       `json:"definitions"`
	GeneratedAt        string               `json:"generated_at"`
	APISchemaVersion   int                  `json:"api_schema_version"`
	EventSchemaVersion int                  `json:"event_schema_version"`
	Resources          []schemaResource     `json:"resources"`
	EventKinds         []EventPayloadSchema `json:"event_kinds"`
	Compatibility      map[string]string    `json:"compatibility"`
}

func buildSchemaResponse() schemaResponse {
	return schemaResponse{
		Definitions:        schemaDefinitions(),
		GeneratedAt:        time.Now().UTC().Format(time.RFC3339Nano),
		APISchemaVersion:   controlAPISchemaVersion,
		EventSchemaVersion: eventContractSchemaVersion,
		Resources:          schemaResources(),
		EventKinds:         schemaEventKinds(),
		Compatibility:      schemaCompatibilityPolicy(),
	}
}

func schemaResources() []schemaResource {
	resources := []schemaResource{
		resourceSchema("/v1/templates/capabilities", methods("GET"), "Inspect local template build support", "", "templatebuild.Capabilities", "Host-wide; sandbox-scoped tokens are denied."),
		resourceSchema("/v1/templates/validate-plan", methods("POST"), "Validate without allocating or downloading", "templatebuild.Plan", "templatebuild.Validation", ""),
		resourceSchema("/v1/template-builds", methods("GET", "POST"), "List or submit durable template builds", "templatebuild.Plan", "templatebuild.Build", "POST returns 202; Idempotency-Key must match request_id UUID."),
		resourceSchema("/v1/template-builds/{id}", methods("GET"), "Read durable build state", "", "templatebuild.Build", ""),
		resourceSchema("/v1/template-builds/{id}/logs", methods("GET"), "Read ordered build logs", "", "templatebuild.Logs", "after is exclusive; limit is at most 500."),
		resourceSchema("/v1/template-builds/{id}/cancel", methods("POST"), "Request build cancellation", "", "templatebuild.Build", "Idempotent."),
		resourceSchema("/v1/templates", methods("GET"), "List successful managed templates", "", "templatebuild.Manifest", "Paginated with offset and limit."),
		resourceSchema("/v1/templates/{vmid}", methods("GET"), "Inspect manifest and live template drift", "", "templatebuild.Manifest", ""),
		resourceSchema("/v1/templates/{vmid}/activate", methods("POST"), "Activate a template for an existing profile", "V1TemplateActivateRequest", "V1TemplateActivateResponse", "Requires profile and expected_template_vmid."),
		resourceSchema("/v1/exposures", methods("GET", "POST"), "List/create exposures", "V1ExposureCreateRequest", "V1ExposuresResponse", ""),
		resourceSchema("/v1/exposures/{name}", methods("DELETE"), "Delete exposure", "", "V1Exposure", ""),
		resourceSchema("/v1/host", methods("GET"), "Fetch host metadata", "", "V1HostResponse", "Includes daemon version, configured subnet, and tailscale hostname when available."),
		resourceSchema("/v1/jobs", methods("POST"), "Create jobs", "V1JobCreateRequest", "V1JobResponse", "Creation returns status 201."),
		resourceSchema("/v1/jobs/{id}", methods("GET"), "Fetch job details", "", "V1JobResponse", "Includes event history when events_tail is provided."),
		resourceSchema("/v1/jobs/{id}/artifacts", methods("GET"), "List job artifacts", "", "V1ArtifactsResponse", ""),
		resourceSchema("/v1/jobs/{id}/artifacts/download", methods("GET"), "Download a job artifact", "", "application/octet-stream", "Query path or name to select artifact."),
		resourceSchema("/v1/jobs/{id}/doctor", methods("POST"), "Create job doctor bundle", "", "V1ArtifactUploadResponse", ""),
		resourceSchema("/v1/jobs/{id}/events", methods("GET"), "Fetch job events", "", "V1EventsResponse", "Not currently exposed as dedicated endpoint."),
		resourceSchema("/v1/jobs/validate-plan", methods("POST"), "Validate job plan", "V1JobValidatePlanRequest", "V1JobValidatePlanResponse", ""),
		resourceSchema("/v1/messages", methods("GET"), "Read messagebox entries", "", "V1MessagesResponse", ""),
		resourceSchema("/v1/messages", methods("POST"), "Post messagebox entry", "V1MessageCreateRequest", "V1Message", ""),
		resourceSchema("/v1/profiles", methods("GET"), "List configured profiles", "", "V1ProfilesResponse", ""),
		resourceSchema("/v1/sandboxes", methods("GET"), "List sandbox records", "", "V1SandboxesResponse", ""),
		resourceSchema("/v1/sandboxes", methods("POST"), "Create sandbox record", "V1SandboxCreateRequest", "V1SandboxResponse", ""),
		resourceSchema("/v1/sandboxes/inventory", methods("GET"), "List live Proxmox VM inventory annotated with AgentLab state and Tailscale metadata", "", "V1SandboxInventoryResponse", ""),
		resourceSchema("/v1/sandboxes/reconcile", methods("POST"), "Detect or apply sandbox state reconciliation against live Proxmox inventory", "V1SandboxReconcileRequest", "V1SandboxReconcileResponse", ""),
		resourceSchema("/v1/sandboxes/{vmid}", methods("GET"), "Fetch sandbox details", "", "V1SandboxResponse", ""),
		resourceSchema("/v1/sandboxes/{vmid}/destroy", methods("POST"), "Destroy sandbox", "", "V1SandboxResponse", ""),
		resourceSchema("/v1/sandboxes/{vmid}/doctor", methods("POST"), "Create sandbox doctor bundle", "", "V1ArtifactUploadResponse", ""),
		resourceSchema("/v1/sandboxes/{vmid}/events", methods("GET"), "List sandbox events", "", "V1EventsResponse", "Supports tail and after query parameters."),
		resourceSchema("/v1/sandboxes/{vmid}/lease/renew", methods("POST"), "Renew sandbox lease", "V1LeaseRenewRequest", "V1LeaseRenewResponse", ""),
		resourceSchema("/v1/sandboxes/{vmid}/pause", methods("POST"), "Pause sandbox", "", "V1SandboxResponse", ""),
		resourceSchema("/v1/sandboxes/{vmid}/revert", methods("POST"), "Revert sandbox", "V1SandboxRevertRequest", "V1SandboxRevertResponse", ""),
		resourceSchema("/v1/sandboxes/{vmid}/resume", methods("POST"), "Resume sandbox", "", "V1SandboxResponse", ""),
		resourceSchema("/v1/sandboxes/{vmid}/snapshot", methods("POST"), "Create sandbox snapshot", "V1SandboxSnapshotCreateRequest", "V1SandboxSnapshotResponse", "Deprecated alias; prefer /snapshots."),
		resourceSchema("/v1/sandboxes/{vmid}/snapshots", methods("GET"), "List sandbox snapshots", "", "V1SandboxSnapshotsResponse", ""),
		resourceSchema("/v1/sandboxes/{vmid}/snapshots", methods("POST"), "Create sandbox snapshot", "V1SandboxSnapshotCreateRequest", "V1SandboxSnapshotResponse", ""),
		resourceSchema("/v1/sandboxes/{vmid}/snapshots/{name}/restore", methods("POST"), "Restore sandbox snapshot", "V1SandboxSnapshotRestoreRequest", "V1SandboxSnapshotResponse", ""),
		resourceSchema("/v1/sandboxes/{vmid}/start", methods("POST"), "Start sandbox", "", "V1SandboxResponse", ""),
		resourceSchema("/v1/sandboxes/{vmid}/stop", methods("POST"), "Stop sandbox", "", "V1SandboxResponse", ""),
		resourceSchema("/v1/sandboxes/{vmid}/touch", methods("POST"), "Touch sandbox usage timestamp", "", "V1SandboxResponse", ""),
		resourceSchema("/v1/sandboxes/{vmid}/update", methods("POST"), "Update sandbox resources", "V1SandboxUpdateRequest", "V1SandboxResponse", ""),
		resourceSchema("/v1/sandboxes/prune", methods("POST"), "Prune orphaned sandbox entries", "", "map[string]int", ""),
		resourceSchema("/v1/sandboxes/stop_all", methods("POST"), "Stop all sandboxes", "V1SandboxStopAllRequest", "V1SandboxStopAllResponse", ""),
		resourceSchema("/v1/sandboxes/validate-plan", methods("POST"), "Validate sandbox plan", "V1SandboxValidatePlanRequest", "V1SandboxValidatePlanResponse", ""),
		resourceSchema("/v1/schema", methods("GET"), "Discover API and event schema catalog", "", "schemaResponse", ""),
		resourceSchema("/v1/sessions", methods("GET"), "List sessions", "", "V1SessionsResponse", ""),
		resourceSchema("/v1/sessions", methods("POST"), "Create session", "V1SessionCreateRequest", "V1SessionResponse", ""),
		resourceSchema("/v1/sessions/{id}", methods("GET"), "Fetch session details", "", "V1SessionResponse", ""),
		resourceSchema("/v1/sessions/{id}/doctor", methods("POST"), "Create session doctor bundle", "", "V1ArtifactUploadResponse", ""),
		resourceSchema("/v1/sessions/{id}/fork", methods("POST"), "Fork session", "V1SessionForkRequest", "V1SessionResponse", ""),
		resourceSchema("/v1/sessions/{id}/resume", methods("POST"), "Resume session", "", "V1SessionResumeResponse", ""),
		resourceSchema("/v1/sessions/{id}/stop", methods("POST"), "Stop session", "", "V1SessionResponse", ""),
		resourceSchema("/v1/status", methods("GET"), "Fetch control-plane status", "", "V1StatusResponse", "Includes schema versions for diagnostics."),
		resourceSchema("/v1/workspaces", methods("GET"), "List workspaces", "", "V1WorkspacesResponse", ""),
		resourceSchema("/v1/workspaces", methods("POST"), "Create workspace", "V1WorkspaceCreateRequest", "V1WorkspaceResponse", ""),
		resourceSchema("/v1/workspaces/{id}", methods("GET"), "Fetch workspace details", "", "V1WorkspaceResponse", ""),
		resourceSchema("/v1/workspaces/{id}/attach", methods("POST"), "Attach workspace", "V1WorkspaceAttachRequest", "V1WorkspaceResponse", ""),
		resourceSchema("/v1/workspaces/{id}/check", methods("GET"), "Run workspace consistency checks", "", "V1WorkspaceCheckResponse", ""),
		resourceSchema("/v1/workspaces/{id}/detach", methods("POST"), "Detach workspace", "", "V1WorkspaceResponse", ""),
		resourceSchema("/v1/workspaces/{id}/fsck", methods("POST"), "Run workspace fsck", "V1WorkspaceFSCKRequest", "V1WorkspaceFSCKResponse", ""),
		resourceSchema("/v1/workspaces/{id}/fork", methods("POST"), "Fork workspace", "V1WorkspaceForkRequest", "V1WorkspaceResponse", ""),
		resourceSchema("/v1/workspaces/{id}/lease/clear", methods("POST"), "Force-clear workspace lease metadata", "", "V1WorkspaceLeaseClearResponse", ""),
		resourceSchema("/v1/workspaces/{id}/rebind", methods("POST"), "Rebind workspace", "V1WorkspaceRebindRequest", "V1WorkspaceRebindResponse", ""),
		resourceSchema("/v1/workspaces/{id}/snapshots", methods("GET"), "List workspace snapshots", "", "V1WorkspaceSnapshotsResponse", ""),
		resourceSchema("/v1/workspaces/{id}/snapshots", methods("POST"), "Create workspace snapshot", "V1WorkspaceSnapshotCreateRequest", "V1WorkspaceSnapshotResponse", ""),
		resourceSchema("/v1/workspaces/{id}/snapshots/{name}/restore", methods("POST"), "Restore workspace snapshot", "", "V1WorkspaceSnapshotResponse", ""),
	}
	sort.Slice(resources, func(i, j int) bool {
		if resources[i].Path == resources[j].Path {
			return len(resources[i].Methods) < len(resources[j].Methods)
		}
		return resources[i].Path < resources[j].Path
	})
	return resources
}

func schemaEventKinds() []EventPayloadSchema {
	events := make([]EventPayloadSchema, 0, len(EventCatalog))
	for _, schema := range EventCatalog {
		events = append(events, cloneEventPayloadSchema(schema))
	}
	sort.Slice(events, func(i, j int) bool {
		if events[i].Kind == events[j].Kind {
			return events[i].Schema < events[j].Schema
		}
		return events[i].Kind < events[j].Kind
	})
	return events
}

func schemaCompatibilityPolicy() map[string]string {
	return map[string]string{
		"api":    "Additive endpoint, path, and optional field changes are preferred. Breaking changes bump the API schema version.",
		"events": "Event kinds, required payload fields, and required version values are managed as an additive contract.",
		"errors": "Unknown event kinds or fields should be ignored by clients.",
	}
}

func cloneEventPayloadSchema(v EventPayloadSchema) EventPayloadSchema {
	v.Optional = copyStrings(v.Optional)
	v.Required = copyStrings(v.Required)
	return v
}

func copyStrings(src []string) []string {
	if len(src) == 0 {
		return nil
	}
	dst := make([]string, len(src))
	copy(dst, src)
	return dst
}

func methods(values ...string) []string {
	if len(values) == 0 {
		return nil
	}
	methods := make([]string, len(values))
	copy(methods, values)
	sort.Strings(methods)
	return methods
}

func resourceSchema(path string, methods []string, summary string, requestType string, responseType string, notes string) schemaResource {
	return schemaResource{
		Path:         path,
		Methods:      methods,
		Summary:      summary,
		RequestType:  requestType,
		ResponseType: responseType,
		Notes:        notes,
	}
}

func (api *ControlAPI) handleSchema(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, []string{http.MethodGet})
		return
	}
	if !api.authorize(w, r, permSchemaRead, nil, false) {
		return
	}
	resp := buildSchemaResponse()
	writeJSON(w, http.StatusOK, resp)
}

// schemaDefinitions describes JSON fields from the same Go types used by handlers.
// The catalog describes wire types; endpoint validation can impose extra constraints.
func schemaDefinitions() map[string]any {
	definitions := map[string]any{}
	var describe func(reflect.Type) map[string]any
	var shape func(reflect.Type) map[string]any
	describe = func(t reflect.Type) map[string]any {
		if t == reflect.TypeOf(json.RawMessage{}) {
			return map[string]any{}
		}
		if t == reflect.TypeOf(time.Time{}) {
			return map[string]any{"type": "string", "format": "date-time"}
		}
		if t.Kind() == reflect.Pointer {
			return map[string]any{"anyOf": []any{describe(t.Elem()), map[string]any{"type": "null"}}}
		}
		if t.Name() != "" && t.Kind() == reflect.Struct {
			name := t.Name()
			if strings.HasSuffix(t.PkgPath(), "/templatebuild") {
				name = "templatebuild." + name
			}
			if _, ok := definitions[name]; !ok {
				definitions[name] = map[string]any{}
				definitions[name] = shape(t)
			}
			return map[string]any{"$ref": "#/definitions/" + name}
		}
		return shape(t)
	}
	shape = func(t reflect.Type) map[string]any {
		switch t.Kind() {
		case reflect.Struct:
			properties := map[string]any{}
			for i := 0; i < t.NumField(); i++ {
				field := t.Field(i)
				if !field.IsExported() {
					continue
				}
				tag := strings.Split(field.Tag.Get("json"), ",")[0]
				if tag == "-" {
					continue
				}
				if tag == "" {
					tag = field.Name
				}
				properties[tag] = describe(field.Type)
			}
			return map[string]any{"type": "object", "properties": properties}
		case reflect.Map:
			return map[string]any{"type": "object", "additionalProperties": describe(t.Elem())}
		case reflect.Slice, reflect.Array:
			if t.Elem().Kind() == reflect.Uint8 {
				return map[string]any{"type": "string", "contentEncoding": "base64"}
			}
			return map[string]any{"type": "array", "items": describe(t.Elem())}
		case reflect.String:
			return map[string]any{"type": "string"}
		case reflect.Bool:
			return map[string]any{"type": "boolean"}
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			return map[string]any{"type": "integer"}
		case reflect.Float32, reflect.Float64:
			return map[string]any{"type": "number"}
		default:
			return map[string]any{}
		}
	}
	roots := map[string]reflect.Type{
		"V1TemplateActivateRequest":        reflect.TypeOf(V1TemplateActivateRequest{}),
		"V1TemplateActivateResponse":       reflect.TypeOf(V1TemplateActivateResponse{}),
		"V1ArtifactUploadResponse":         reflect.TypeOf(V1ArtifactUploadResponse{}),
		"V1ArtifactsResponse":              reflect.TypeOf(V1ArtifactsResponse{}),
		"V1EventsResponse":                 reflect.TypeOf(V1EventsResponse{}),
		"V1Exposure":                       reflect.TypeOf(V1Exposure{}),
		"V1ExposureCreateRequest":          reflect.TypeOf(V1ExposureCreateRequest{}),
		"V1ExposuresResponse":              reflect.TypeOf(V1ExposuresResponse{}),
		"V1HostResponse":                   reflect.TypeOf(V1HostResponse{}),
		"V1JobCreateRequest":               reflect.TypeOf(V1JobCreateRequest{}),
		"V1JobResponse":                    reflect.TypeOf(V1JobResponse{}),
		"V1JobValidatePlanRequest":         reflect.TypeOf(V1JobValidatePlanRequest{}),
		"V1JobValidatePlanResponse":        reflect.TypeOf(V1JobValidatePlanResponse{}),
		"V1LeaseRenewRequest":              reflect.TypeOf(V1LeaseRenewRequest{}),
		"V1LeaseRenewResponse":             reflect.TypeOf(V1LeaseRenewResponse{}),
		"V1Message":                        reflect.TypeOf(V1Message{}),
		"V1MessageCreateRequest":           reflect.TypeOf(V1MessageCreateRequest{}),
		"V1MessagesResponse":               reflect.TypeOf(V1MessagesResponse{}),
		"V1ProfilesResponse":               reflect.TypeOf(V1ProfilesResponse{}),
		"V1SandboxCreateRequest":           reflect.TypeOf(V1SandboxCreateRequest{}),
		"V1SandboxInventoryResponse":       reflect.TypeOf(V1SandboxInventoryResponse{}),
		"V1SandboxReconcileRequest":        reflect.TypeOf(V1SandboxReconcileRequest{}),
		"V1SandboxReconcileResponse":       reflect.TypeOf(V1SandboxReconcileResponse{}),
		"V1SandboxResponse":                reflect.TypeOf(V1SandboxResponse{}),
		"V1SandboxRevertRequest":           reflect.TypeOf(V1SandboxRevertRequest{}),
		"V1SandboxRevertResponse":          reflect.TypeOf(V1SandboxRevertResponse{}),
		"V1SandboxSnapshotCreateRequest":   reflect.TypeOf(V1SandboxSnapshotCreateRequest{}),
		"V1SandboxSnapshotResponse":        reflect.TypeOf(V1SandboxSnapshotResponse{}),
		"V1SandboxSnapshotRestoreRequest":  reflect.TypeOf(V1SandboxSnapshotRestoreRequest{}),
		"V1SandboxSnapshotsResponse":       reflect.TypeOf(V1SandboxSnapshotsResponse{}),
		"V1SandboxStopAllRequest":          reflect.TypeOf(V1SandboxStopAllRequest{}),
		"V1SandboxStopAllResponse":         reflect.TypeOf(V1SandboxStopAllResponse{}),
		"V1SandboxUpdateRequest":           reflect.TypeOf(V1SandboxUpdateRequest{}),
		"V1SandboxValidatePlanRequest":     reflect.TypeOf(V1SandboxValidatePlanRequest{}),
		"V1SandboxValidatePlanResponse":    reflect.TypeOf(V1SandboxValidatePlanResponse{}),
		"V1SandboxesResponse":              reflect.TypeOf(V1SandboxesResponse{}),
		"V1SessionCreateRequest":           reflect.TypeOf(V1SessionCreateRequest{}),
		"V1SessionForkRequest":             reflect.TypeOf(V1SessionForkRequest{}),
		"V1SessionResponse":                reflect.TypeOf(V1SessionResponse{}),
		"V1SessionResumeResponse":          reflect.TypeOf(V1SessionResumeResponse{}),
		"V1SessionsResponse":               reflect.TypeOf(V1SessionsResponse{}),
		"V1StatusResponse":                 reflect.TypeOf(V1StatusResponse{}),
		"V1WorkspaceAttachRequest":         reflect.TypeOf(V1WorkspaceAttachRequest{}),
		"V1WorkspaceCheckResponse":         reflect.TypeOf(V1WorkspaceCheckResponse{}),
		"V1WorkspaceCreateRequest":         reflect.TypeOf(V1WorkspaceCreateRequest{}),
		"V1WorkspaceFSCKRequest":           reflect.TypeOf(V1WorkspaceFSCKRequest{}),
		"V1WorkspaceFSCKResponse":          reflect.TypeOf(V1WorkspaceFSCKResponse{}),
		"V1WorkspaceForkRequest":           reflect.TypeOf(V1WorkspaceForkRequest{}),
		"V1WorkspaceLeaseClearResponse":    reflect.TypeOf(V1WorkspaceLeaseClearResponse{}),
		"V1WorkspaceRebindRequest":         reflect.TypeOf(V1WorkspaceRebindRequest{}),
		"V1WorkspaceRebindResponse":        reflect.TypeOf(V1WorkspaceRebindResponse{}),
		"V1WorkspaceResponse":              reflect.TypeOf(V1WorkspaceResponse{}),
		"V1WorkspaceSnapshotCreateRequest": reflect.TypeOf(V1WorkspaceSnapshotCreateRequest{}),
		"V1WorkspaceSnapshotResponse":      reflect.TypeOf(V1WorkspaceSnapshotResponse{}),
		"V1WorkspaceSnapshotsResponse":     reflect.TypeOf(V1WorkspaceSnapshotsResponse{}),
		"V1WorkspacesResponse":             reflect.TypeOf(V1WorkspacesResponse{}),
		"templatebuild.Build":              reflect.TypeOf(templatebuild.Build{}),
		"templatebuild.Capabilities":       reflect.TypeOf(templatebuild.Capabilities{}),
		"templatebuild.Logs":               reflect.TypeOf(templatebuild.Logs{}),
		"templatebuild.Manifest":           reflect.TypeOf(templatebuild.Manifest{}),
		"templatebuild.Plan":               reflect.TypeOf(templatebuild.Plan{}),
		"templatebuild.Validation":         reflect.TypeOf(templatebuild.Validation{}),
		"schemaResponse":                   reflect.TypeOf(schemaResponse{}),
		"map[string]int":                   reflect.TypeOf(map[string]int{}),
	}
	for name, typ := range roots {
		describe(typ)
		if _, ok := definitions[name]; !ok {
			definitions[name] = shape(typ)
		}
	}
	return definitions
}
