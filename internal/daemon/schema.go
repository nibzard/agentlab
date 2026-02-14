package daemon

import (
	"net/http"
	"sort"
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
	GeneratedAt        string               `json:"generated_at"`
	APISchemaVersion   int                  `json:"api_schema_version"`
	EventSchemaVersion int                  `json:"event_schema_version"`
	Resources          []schemaResource     `json:"resources"`
	EventKinds         []EventPayloadSchema `json:"event_kinds"`
	Compatibility      map[string]string    `json:"compatibility"`
}

func buildSchemaResponse() schemaResponse {
	return schemaResponse{
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
	resp := buildSchemaResponse()
	writeJSON(w, http.StatusOK, resp)
}
