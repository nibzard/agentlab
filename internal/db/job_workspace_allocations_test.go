package db

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestWorkspaceAllocationCommitRollsBackEveryBinding(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	now := time.Now()
	request, err := store.ReserveJobSubmission(ctx, JobSubmission{Owner: "actor", KeyHash: "key", InputHash: "inputs", JobID: "job-allocation", ExpiresAt: now.Add(time.Hour)}, now)
	require.NoError(t, err)
	allocation, err := store.ReserveJobWorkspaceAllocation(ctx, JobWorkspaceAllocation{JobID: request.JobID, WorkspaceID: "ws-allocation", Name: "workspace", Storage: "local-zfs", SizeGB: 10})
	require.NoError(t, err)
	// The database also protects the name from older callers and other paths
	// that do not acquire the manager's name lock.
	_, err = store.DB.Exec(`INSERT INTO workspaces(id,name,storage,volid,size_gb,created_at,updated_at) VALUES('ws-other','workspace','local-zfs','local-zfs:other',10,?,?)`, formatTime(now), formatTime(now))
	require.Error(t, err)
	require.NoError(t, store.BeginJobWorkspaceAllocation(ctx, request.JobID))
	require.ErrorIs(t, store.BeginJobWorkspaceAllocation(ctx, request.JobID), ErrWorkspaceAllocationUncertain)
	require.NoError(t, store.RecordJobWorkspaceVolume(ctx, request.JobID, "local-zfs:allocated-volume", true))
	_, err = store.DB.Exec(`CREATE TRIGGER fail_allocation_binding BEFORE INSERT ON job_submission_workspaces BEGIN SELECT RAISE(FAIL,'injected binding failure'); END`)
	require.NoError(t, err)
	_, err = store.CommitJobWorkspaceAllocation(ctx, request.JobID)
	require.Error(t, err)
	var count int
	require.NoError(t, store.DB.QueryRow(`SELECT COUNT(*) FROM workspaces`).Scan(&count))
	require.Zero(t, count)
	require.NoError(t, store.DB.QueryRow(`SELECT COUNT(*) FROM job_submission_workspaces`).Scan(&count))
	require.Zero(t, count)
	current, err := store.JobWorkspaceAllocation(ctx, request.JobID)
	require.NoError(t, err)
	require.Equal(t, "allocated", current.State)
	require.Equal(t, "local-zfs:allocated-volume", current.VolumeID)
	_, err = store.DB.Exec(`DROP TRIGGER fail_allocation_binding`)
	require.NoError(t, err)
	workspace, err := store.CommitJobWorkspaceAllocation(ctx, request.JobID)
	require.NoError(t, err)
	require.Equal(t, allocation.WorkspaceID, workspace.ID)
	repeated, err := store.CommitJobWorkspaceAllocation(ctx, request.JobID)
	require.NoError(t, err)
	require.Equal(t, workspace.ID, repeated.ID)
	current, err = store.JobWorkspaceAllocation(ctx, request.JobID)
	require.NoError(t, err)
	require.Equal(t, "committed", current.State)
}

func TestWorkspaceAllocationDoesNotAdoptExistingVolume(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	now := time.Now()
	for _, id := range []string{"job-one", "job-two"} {
		_, err := store.ReserveJobSubmission(ctx, JobSubmission{Owner: "actor", KeyHash: id, InputHash: "inputs", JobID: id, ExpiresAt: now.Add(time.Hour)}, now)
		require.NoError(t, err)
		_, err = store.ReserveJobWorkspaceAllocation(ctx, JobWorkspaceAllocation{JobID: id, WorkspaceID: "ws-" + id, Name: id, Storage: "local-zfs", SizeGB: 10})
		require.NoError(t, err)
		require.NoError(t, store.BeginJobWorkspaceAllocation(ctx, id))
		require.NoError(t, store.RecordJobWorkspaceVolume(ctx, id, "local-zfs:same-volume", true))
	}
	_, err := store.CommitJobWorkspaceAllocation(ctx, "job-one")
	require.NoError(t, err)
	_, err = store.CommitJobWorkspaceAllocation(ctx, "job-two")
	require.ErrorIs(t, err, ErrWorkspaceAllocationConflict)
	retained, err := store.JobWorkspaceAllocation(ctx, "job-two")
	require.NoError(t, err)
	require.Equal(t, "allocated", retained.State)
	var count int
	require.NoError(t, store.DB.QueryRow(`SELECT COUNT(*) FROM workspaces`).Scan(&count))
	require.Equal(t, 1, count)
}
