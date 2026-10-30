package db

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSubmissionReservationPersistsAndConflicts(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	request := JobSubmission{Owner: "actor", KeyHash: "request-hash", InputHash: "inputs", JobID: "job-original", ExpiresAt: now.Add(30 * 24 * time.Hour)}
	first, err := store.ReserveJobSubmission(ctx, request, now)
	require.NoError(t, err)
	request.JobID = "job-new"
	again, err := store.ReserveJobSubmission(ctx, request, now)
	require.NoError(t, err)
	require.Equal(t, first.JobID, again.JobID)
	request.InputHash = "changed"
	_, err = store.ReserveJobSubmission(ctx, request, now)
	require.ErrorIs(t, err, ErrSubmissionConflict)
	request.InputHash = "inputs"
	_, err = store.ReserveJobSubmission(ctx, request, now.Add(31*24*time.Hour))
	require.ErrorIs(t, err, ErrSubmissionExpired)
	require.NoError(t, store.BindSubmissionWorkspace(ctx, first, "workspace-one"))
	require.NoError(t, store.BindSubmissionWorkspace(ctx, first, "workspace-one"))
	require.ErrorIs(t, store.BindSubmissionWorkspace(ctx, first, "workspace-two"), ErrSubmissionConflict)
}

func TestSubmissionLockCancellationAndRelease(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	release, err := store.LockJobSubmission(ctx, "actor", "key")
	require.NoError(t, err)
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = store.LockJobSubmission(cancelled, "actor", "key")
	require.True(t, errors.Is(err, context.Canceled))
	release()
	releaseAgain, err := store.LockJobSubmission(ctx, "actor", "key")
	require.NoError(t, err)
	releaseAgain()
}
