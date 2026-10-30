package db

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/agentlab/agentlab/internal/auth"
	"github.com/agentlab/agentlab/internal/protocol"
	"github.com/stretchr/testify/require"
)

func TestTokenRevocationPersistsAndKeepsFirstAuditRecord(t *testing.T) {
	store := openTestStore(t)
	issuer := "SHA256:" + base64.RawStdEncoding.EncodeToString(make([]byte, 32))
	nonce := "signed-nonce-that-must-not-be-stored"
	id := auth.RevocationID(issuer, nonce)
	ctx := context.Background()
	revoked, err := store.TokenRevoked(ctx, issuer, nonce)
	require.NoError(t, err)
	require.False(t, revoked)
	record := protocol.TokenRevocation{Issuer: issuer, RevocationID: id, Revoked: true, Actor: "ssh-token:first-operator", ServerIdentity: "fixture-server", RevokedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	first, err := store.RevokeToken(ctx, record)
	require.NoError(t, err)
	record.Actor = "other-operator"
	record.RevokedAt = time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)
	again, err := store.RevokeToken(ctx, record)
	require.NoError(t, err)
	require.Equal(t, first, again)
	var data string
	require.NoError(t, store.DB.QueryRow(`SELECT record_json FROM ssh_token_revocations`).Scan(&data))
	require.NotContains(t, data, nonce)
	require.NoError(t, store.Close())
	restarted, err := Open(store.Path)
	require.NoError(t, err)
	defer restarted.Close()
	revoked, err = restarted.TokenRevoked(ctx, issuer, nonce)
	require.NoError(t, err)
	require.True(t, revoked)
	revoked, err = restarted.TokenRevoked(ctx, issuer, "another-nonce")
	require.NoError(t, err)
	require.False(t, revoked)
	revoked, err = restarted.TokenRevoked(ctx, "other-issuer", nonce)
	require.NoError(t, err)
	require.False(t, revoked)
	_, err = restarted.RevokeToken(ctx, protocol.TokenRevocation{Issuer: issuer, RevocationID: strings.Repeat("a", 64)})
	require.Error(t, err)
}

func TestTokenRevocationLookupReportsStoreFailure(t *testing.T) {
	store := openTestStore(t)
	require.NoError(t, store.Close())
	for _, id := range []string{"nonce", ""} {
		_, err := store.TokenRevoked(context.Background(), "issuer", id)
		require.Error(t, err)
	}
}

func TestTokenRevocationConcurrentRequestsKeepOneAuditIdentity(t *testing.T) {
	store := openTestStore(t)
	issuer := "SHA256:" + base64.RawStdEncoding.EncodeToString(make([]byte, 32))
	id := auth.RevocationID(issuer, "concurrent-nonce")
	type result struct {
		record protocol.TokenRevocation
		err    error
	}
	results := make(chan result, 8)
	for i := 0; i < 8; i++ {
		go func(i int) {
			record, err := store.RevokeToken(context.Background(), protocol.TokenRevocation{Issuer: issuer, RevocationID: id, Revoked: true, Actor: fmt.Sprintf("operator-%d", i), ServerIdentity: "server", RevokedAt: time.Now().UTC().Format(time.RFC3339Nano)})
			results <- result{record, err}
		}(i)
	}
	first := <-results
	require.NoError(t, first.err)
	for i := 1; i < 8; i++ {
		next := <-results
		require.NoError(t, next.err)
		require.Equal(t, first.record, next.record)
	}
	var count int
	require.NoError(t, store.DB.QueryRow(`SELECT COUNT(*) FROM ssh_token_revocations`).Scan(&count))
	require.Equal(t, 1, count)
}
