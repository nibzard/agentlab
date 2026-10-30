package protocol

import "time"

// WorkspaceForkOperation retains resource identities even without a workspace row.
type WorkspaceForkOperation struct {
	ServerIdentity    string            `json:"server_identity"`
	ID                string            `json:"id"`
	Owner             string            `json:"owner"`
	SourceWorkspaceID string            `json:"source_workspace_id"`
	SourceVolumeID    string            `json:"source_volume_id"`
	FromSnapshot      string            `json:"from_snapshot,omitempty"`
	CopySnapshot      string            `json:"copy_snapshot"`
	TargetName        string            `json:"target_name"`
	TargetWorkspaceID string            `json:"target_workspace_id"`
	TargetVolumeID    string            `json:"target_volume_id"`
	SizeGB            int               `json:"size_gb,omitempty"`
	TargetState       string            `json:"target_state"`
	State             string            `json:"state"`
	Cleanup           string            `json:"cleanup_status"`
	Receipt           *StorageOperation `json:"storage_receipt,omitempty"`
	CreatedAt         time.Time         `json:"created_at"`
	UpdatedAt         time.Time         `json:"updated_at"`
}

type StorageOperation struct {
	Version                  int    `json:"version"`
	Operation                string `json:"operation"`
	Source                   string `json:"source"`
	Snapshot                 string `json:"snapshot"`
	Target                   string `json:"target"`
	State                    string `json:"state"`
	Cleanup                  string `json:"cleanup_status"`
	OK                       bool   `json:"ok"`
	Code                     string `json:"code,omitempty"`
	SourceGUID               string `json:"source_guid,omitempty"`
	SnapshotGUID             string `json:"snapshot_guid,omitempty"`
	TargetGUID               string `json:"target_guid,omitempty"`
	TemporarySnapshotCreated bool   `json:"temporary_snapshot_created,omitempty"`
	JournalID                string `json:"journal_id,omitempty"`
	Recovered                bool   `json:"recovered,omitempty"`
}
