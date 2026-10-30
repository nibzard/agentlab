package protocol

import (
	"time"

	"github.com/agentlab/agentlab/internal/reposnapshot"
)

const RepositorySnapshotPrefix = "agentlab-snapshot:"

// RepositorySnapshot identifies immutable uploaded source content.
type RepositorySnapshot struct {
	ID        string                `json:"id"`
	RepoURL   string                `json:"repo_url"`
	Manifest  reposnapshot.Manifest `json:"manifest"`
	CreatedAt time.Time             `json:"created_at"`
	ExpiresAt time.Time             `json:"expires_at"`
}

// RepositorySource contains only the content identity needed by a bound guest.
type RepositorySource struct {
	ID             string `json:"id"`
	SourceRevision string `json:"source_revision"`
	Revision       string `json:"revision"`
	BundleSHA256   string `json:"bundle_sha256"`
	BundleBytes    int64  `json:"bundle_bytes"`
}
