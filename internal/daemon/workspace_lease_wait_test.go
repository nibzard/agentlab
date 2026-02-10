package daemon

import (
	"context"
	"testing"
	"time"

	"github.com/agentlab/agentlab/internal/models"
	"github.com/stretchr/testify/require"
)

type seqClock struct {
	times []time.Time
	idx   int
}

func (c *seqClock) Now() time.Time {
	if len(c.times) == 0 {
		return time.Now()
	}
	if c.idx >= len(c.times) {
		return c.times[len(c.times)-1]
	}
	t := c.times[c.idx]
	c.idx++
	return t
}

func TestAcquireWorkspaceLeaseDeadline(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	api := NewControlAPI(store, nil, nil, nil, nil, "", nil)

	base := time.Date(2026, 2, 10, 12, 0, 0, 0, time.UTC)
	clock := &seqClock{times: []time.Time{
		base,                      // start
		base,                      // expiresAt
		base.Add(2 * time.Second), // deadline check
		base.Add(2 * time.Second), // waited return
	}}
	api.now = clock.Now

	workspace := models.Workspace{
		ID:           "ws-wait",
		Name:         "ws-wait",
		Storage:      "local-zfs",
		VolumeID:     "local-zfs:vm-500-disk-1",
		SizeGB:       5,
		LeaseOwner:   "job:other",
		LeaseNonce:   "nonce-held",
		LeaseExpires: base.Add(time.Hour),
		CreatedAt:    base,
		LastUpdated:  base,
	}
	require.NoError(t, store.CreateWorkspace(ctx, workspace))

	_, _, err := api.acquireWorkspaceLease(ctx, workspace, "job:me", 10*time.Minute, 1)
	require.ErrorIs(t, err, errWorkspaceWaitTimeout)
}

func TestAcquireWorkspaceLeaseNoWait(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	api := NewControlAPI(store, nil, nil, nil, nil, "", nil)

	base := time.Date(2026, 2, 10, 12, 30, 0, 0, time.UTC)
	workspace := models.Workspace{
		ID:           "ws-nowait",
		Name:         "ws-nowait",
		Storage:      "local-zfs",
		VolumeID:     "local-zfs:vm-501-disk-1",
		SizeGB:       5,
		LeaseOwner:   "job:other",
		LeaseNonce:   "nonce-held",
		LeaseExpires: base.Add(time.Hour),
		CreatedAt:    base,
		LastUpdated:  base,
	}
	require.NoError(t, store.CreateWorkspace(ctx, workspace))

	_, _, err := api.acquireWorkspaceLease(ctx, workspace, "job:me", 10*time.Minute, 0)
	require.ErrorIs(t, err, ErrWorkspaceLeaseHeld)
}
