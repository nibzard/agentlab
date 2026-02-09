package daemon

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/agentlab/agentlab/internal/db"
)

const (
	workspaceLeaseDefaultTTL       = 30 * time.Minute
	workspaceLeaseNonceBytes       = 16
	workspaceLeaseMinRenewInterval = 30 * time.Second
	workspaceLeaseMaxRenewInterval = 10 * time.Minute
)

func workspaceLeaseOwnerForJob(jobID string) string {
	jobID = strings.TrimSpace(jobID)
	if jobID == "" {
		return ""
	}
	return "job:" + jobID
}

func workspaceLeaseOwnerForSandbox(vmid int) string {
	if vmid <= 0 {
		return ""
	}
	return fmt.Sprintf("sandbox:%d", vmid)
}

func workspaceLeaseDuration(ttlMinutes int) time.Duration {
	if ttlMinutes > 0 {
		return time.Duration(ttlMinutes) * time.Minute
	}
	return workspaceLeaseDefaultTTL
}

func workspaceLeaseRenewInterval(ttl time.Duration) time.Duration {
	if ttl <= 0 {
		return workspaceLeaseMinRenewInterval
	}
	interval := ttl / 2
	if interval < workspaceLeaseMinRenewInterval {
		return workspaceLeaseMinRenewInterval
	}
	if interval > workspaceLeaseMaxRenewInterval {
		return workspaceLeaseMaxRenewInterval
	}
	return interval
}

func newWorkspaceLeaseNonce(r io.Reader) (string, error) {
	if r == nil {
		r = rand.Reader
	}
	buf := make([]byte, workspaceLeaseNonceBytes)
	if _, err := io.ReadFull(r, buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func recordWorkspaceLeaseEvent(ctx context.Context, store *db.Store, kind string, vmid *int, jobID *string, workspaceID, owner string, expiresAt time.Time) {
	if store == nil {
		return
	}
	msg := fmt.Sprintf("workspace_id=%s owner=%s", workspaceID, owner)
	if !expiresAt.IsZero() {
		msg = fmt.Sprintf("workspace_id=%s owner=%s expires_at=%s", workspaceID, owner, expiresAt.UTC().Format(time.RFC3339Nano))
	}
	_ = store.RecordEvent(ctx, kind, vmid, jobID, msg, "")
}
