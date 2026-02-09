package db

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestMessageCreateAndList(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	base := time.Date(2026, 2, 9, 12, 0, 0, 0, time.UTC)
	msg1, err := store.CreateMessage(ctx, Message{
		Timestamp: base,
		ScopeType: "job",
		ScopeID:   "job-1",
		Author:    "alice",
		Kind:      "note",
		Text:      "hello",
	})
	require.NoError(t, err)
	msg2, err := store.CreateMessage(ctx, Message{
		Timestamp: base.Add(2 * time.Second),
		ScopeType: "job",
		ScopeID:   "job-1",
		Author:    "bob",
		Kind:      "note",
		Text:      "world",
	})
	require.NoError(t, err)
	_, err = store.CreateMessage(ctx, Message{
		Timestamp: base,
		ScopeType: "workspace",
		ScopeID:   "ws-1",
		Text:      "other",
	})
	require.NoError(t, err)

	tail, err := store.ListMessagesByScopeTail(ctx, "job", "job-1", 1)
	require.NoError(t, err)
	require.Len(t, tail, 1)
	require.Equal(t, msg2.ID, tail[0].ID)

	list, err := store.ListMessagesByScope(ctx, "job", "job-1", msg1.ID, 10)
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, msg2.ID, list[0].ID)
}

func TestMessageCreateValidation(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	_, err := store.CreateMessage(ctx, Message{ScopeID: "missing-scope"})
	require.EqualError(t, err, "message scope_type is required")

	_, err = store.CreateMessage(ctx, Message{ScopeType: "job"})
	require.EqualError(t, err, "message scope_id is required")
}
