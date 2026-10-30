package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"regexp"
	"time"
)

type WorkspaceVolumeOwner struct {
	WorkspaceID    string
	VMID           int
	AllocationName string
}

var ErrWorkspaceVolumeOwner = errors.New("workspace volume owner is unavailable or changed")
var workspaceVolumeIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,95}$`)

func volumeOwnerIdentity(workspaceID string) string { return "workspace-volume:" + workspaceID }

func (s *Store) WorkspaceVolumeOwner(ctx context.Context, workspaceID string) (WorkspaceVolumeOwner, error) {
	var result WorkspaceVolumeOwner
	var reserved string
	err := s.DB.QueryRowContext(ctx, `SELECT o.workspace_id,o.vmid,o.allocation_name,COALESCE(r.owner,'') FROM workspace_volume_owners o LEFT JOIN vmid_reservations r ON r.vmid=o.vmid WHERE o.workspace_id=?`, workspaceID).Scan(&result.WorkspaceID, &result.VMID, &result.AllocationName, &reserved)
	if err != nil {
		return result, err
	}
	if reserved != volumeOwnerIdentity(workspaceID) || result.VMID < 100 || result.VMID > 999999999 || result.AllocationName != fmt.Sprintf("vm-%d-agentlab-%s", result.VMID, workspaceID) {
		return result, ErrWorkspaceVolumeOwner
	}
	return result, nil
}

// ReserveWorkspaceVolumeOwner retains a separate storage owner ID. It does not
// create a VM. Sandbox and template creation share the same reservation table.
// Reservations survive failed allocation and must not be silently reused.
func (s *Store) ReserveWorkspaceVolumeOwner(ctx context.Context, workspaceID string, occupied map[int]bool) (WorkspaceVolumeOwner, error) {
	if !workspaceVolumeIDPattern.MatchString(workspaceID) {
		return WorkspaceVolumeOwner{}, errors.New("invalid workspace storage identity")
	}
	if existing, err := s.WorkspaceVolumeOwner(ctx, workspaceID); err == nil {
		return existing, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return existing, err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return WorkspaceVolumeOwner{}, err
	}
	defer tx.Rollback()
	// A second process can finish the reservation before this transaction starts.
	var previous int
	err = tx.QueryRowContext(ctx, `SELECT vmid FROM workspace_volume_owners WHERE workspace_id=?`, workspaceID).Scan(&previous)
	if err == nil {
		tx.Rollback()
		return s.WorkspaceVolumeOwner(ctx, workspaceID)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return WorkspaceVolumeOwner{}, err
	}
	const minimum = 900000000
	const count = 100000000
	digest := sha256.Sum256([]byte(workspaceID))
	start := int(binary.BigEndian.Uint64(digest[:8]) % count)
	for attempt := 0; attempt < 1024; attempt++ {
		candidate := minimum + (start+attempt)%count
		if occupied[candidate] {
			continue
		}
		var used int
		if err := tx.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM sandboxes WHERE vmid=?)+(SELECT COUNT(*) FROM vmid_reservations WHERE vmid=?)`, candidate, candidate).Scan(&used); err != nil {
			return WorkspaceVolumeOwner{}, err
		}
		if used != 0 {
			continue
		}
		result := WorkspaceVolumeOwner{WorkspaceID: workspaceID, VMID: candidate, AllocationName: fmt.Sprintf("vm-%d-agentlab-%s", candidate, workspaceID)}
		if _, err := tx.ExecContext(ctx, `INSERT INTO vmid_reservations(vmid,owner) VALUES(?,?)`, candidate, volumeOwnerIdentity(workspaceID)); err != nil {
			return WorkspaceVolumeOwner{}, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO workspace_volume_owners(workspace_id,vmid,allocation_name,created_at) VALUES(?,?,?,?)`, workspaceID, candidate, result.AllocationName, formatTime(time.Now().UTC())); err != nil {
			return WorkspaceVolumeOwner{}, err
		}
		if err := tx.Commit(); err != nil {
			return WorkspaceVolumeOwner{}, err
		}
		return result, nil
	}
	return WorkspaceVolumeOwner{}, ErrWorkspaceVolumeOwner
}
