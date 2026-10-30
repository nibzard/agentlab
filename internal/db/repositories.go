package db

import (
	"context"
	"encoding/json"
	"time"

	"github.com/agentlab/agentlab/internal/protocol"
)

const SnapshotRepositoryPrefix = protocol.RepositorySnapshotPrefix

// SaveRepositorySnapshot preserves the first upload time and expiry on retries.
func (s *Store) SaveRepositorySnapshot(ctx context.Context, owner string, record protocol.RepositorySnapshot) (protocol.RepositorySnapshot, error) {
	data, err := json.Marshal(record.Manifest)
	if err != nil {
		return protocol.RepositorySnapshot{}, err
	}
	_, err = s.DB.ExecContext(ctx, `INSERT INTO repository_snapshots(id,owner,manifest_json,created_at,expires_at) VALUES(?,?,?,?,?) ON CONFLICT(id) DO NOTHING`, record.ID, owner, string(data), record.CreatedAt.UTC().Format(timeLayoutFixed), record.ExpiresAt.UTC().Format(timeLayoutFixed))
	if err != nil {
		return protocol.RepositorySnapshot{}, err
	}
	result, _, err := s.GetRepositorySnapshot(ctx, record.ID)
	return result, err
}

func (s *Store) GetRepositorySnapshot(ctx context.Context, id string) (protocol.RepositorySnapshot, string, error) {
	result := protocol.RepositorySnapshot{ID: id, RepoURL: SnapshotRepositoryPrefix + id}
	var owner, metadata, created, expires string
	err := s.DB.QueryRowContext(ctx, `SELECT owner,manifest_json,created_at,expires_at FROM repository_snapshots WHERE id=?`, id).Scan(&owner, &metadata, &created, &expires)
	if err != nil {
		return result, "", err
	}
	err = json.Unmarshal([]byte(metadata), &result.Manifest)
	if err != nil {
		return result, "", err
	}
	result.CreatedAt, err = time.Parse(timeLayoutFixed, created)
	if err != nil {
		return result, "", err
	}
	result.ExpiresAt, err = time.Parse(timeLayoutFixed, expires)
	return result, owner, err
}

// DeleteRepositorySnapshot removes metadata only after callers verify file cleanup.
func (s *Store) DeleteRepositorySnapshot(ctx context.Context, id, owner string) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM repository_snapshots WHERE id=? AND owner=?`, id, owner)
	return err
}

func (s *Store) RepositorySnapshotActive(ctx context.Context, id string) (bool, error) {
	var count int
	err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM jobs WHERE repo_url=? AND (status IN ('QUEUED','RUNNING') OR CASE WHEN json_valid(result_json) THEN json_extract(result_json,'$.result.repository_result.cleanup_status')='pending' OR (json_extract(result_json,'$.result.repository_result.cleanup_status')='blocked' AND json_extract(result_json,'$.result.repository_result.state')='verified' AND json_type(result_json,'$.result.repository_result.target')='object') ELSE 0 END)`, SnapshotRepositoryPrefix+id).Scan(&count)
	return count > 0, err
}
