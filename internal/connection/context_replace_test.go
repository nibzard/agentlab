package connection

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestConditionalCredentialPublicationPreservesChangedContext(t *testing.T) {
	store := ContextStore{Root: t.TempDir(), CredentialMode: "file"}
	require.NoError(t, store.Put("task", Settings{Endpoint: "https://first.example", Token: "first", ServerIdentity: "first"}))
	before, _, _, err := store.LookupContext(context.Background(), "task", false)
	require.NoError(t, err)
	require.NoError(t, store.Put("task", Settings{Endpoint: "https://second.example", Token: "second", ServerIdentity: "second"}))
	err = store.PutContextIfMatch(context.Background(), "task", &before, Settings{Endpoint: "https://first.example", Token: "replacement", ServerIdentity: "first"})
	require.Error(t, err)
	cfg, _, _, err := store.Resolve("task")
	require.NoError(t, err)
	require.Equal(t, "https://second.example", cfg.Endpoint)
	require.Equal(t, "second", cfg.Token)
	require.Error(t, store.RemoveContextIfMatch(context.Background(), "task", before))
}
