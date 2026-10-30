package protocol

import "github.com/agentlab/agentlab/internal/executionarchive"

// RepositoryResult records server verification of returned source and artifacts.
// The guest supplies only the manifest hash. Other fields are server evidence.
type RepositoryResult struct {
	SourceID         string                     `json:"source_id,omitempty"`
	OriginalRevision string                     `json:"original_revision,omitempty"`
	State            string                     `json:"state"`
	ManifestSHA256   string                     `json:"manifest_sha256,omitempty"`
	SourceRevision   string                     `json:"source_revision,omitempty"`
	Revision         string                     `json:"revision,omitempty"`
	ManifestPath     string                     `json:"manifest_path,omitempty"`
	BundlePath       string                     `json:"bundle_path,omitempty"`
	PatchPath        string                     `json:"patch_path,omitempty"`
	PatchSHA256      string                     `json:"patch_sha256,omitempty"`
	PatchBytes       int64                      `json:"patch_bytes"`
	ExcludedCount    int                        `json:"excluded_count"`
	VerifiedAt       string                     `json:"verified_at,omitempty"`
	ErrorCode        string                     `json:"error_code,omitempty"`
	Action           string                     `json:"action,omitempty"`
	CleanupStatus    string                     `json:"cleanup_status"`
	Resources        []JobResource              `json:"resources"`
	Execution        *executionarchive.Evidence `json:"execution,omitempty"`
	Target           *RepositoryResultTarget    `json:"target,omitempty"`
}

// RepositoryResultTarget pins the server and sandbox before result cleanup.
type RepositoryResultTarget struct {
	ServerIdentity string `json:"server_identity"`
	VMID           int    `json:"vmid"`
	Name           string `json:"name"`
	CreatedAt      string `json:"created_at"`
	GenerationID   string `json:"generation_id"`
	Incarnation    string `json:"incarnation,omitempty"`
}
