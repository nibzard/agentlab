package db

import (
	"context"
	"testing"
	"time"
)

func TestHashBootstrapToken(t *testing.T) {
	hash, err := HashBootstrapToken("token-123")
	if err != nil {
		t.Fatalf("hash token: %v", err)
	}
	if len(hash) != 64 {
		t.Fatalf("expected hash length 64, got %d", len(hash))
	}
	if _, err := HashBootstrapToken(" "); err == nil {
		t.Fatal("expected error for empty token")
	}
}

func TestConsumeBootstrapToken(t *testing.T) {
	store := openTestStore(t)
	defer func() {
		_ = store.Close()
	}()
	ctx := context.Background()
	now := time.Now().UTC()
	tokenHash, err := HashBootstrapToken("token-abc")
	if err != nil {
		t.Fatalf("hash token: %v", err)
	}
	if err := store.CreateBootstrapToken(ctx, tokenHash, 1234, now.Add(5*time.Minute)); err != nil {
		t.Fatalf("create token: %v", err)
	}

	consumed, err := store.ConsumeBootstrapToken(ctx, tokenHash, 1234, now.Add(1*time.Minute))
	if err != nil {
		t.Fatalf("consume token: %v", err)
	}
	if !consumed {
		t.Fatal("expected token to be consumed")
	}

	consumed, err = store.ConsumeBootstrapToken(ctx, tokenHash, 1234, now.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("consume token again: %v", err)
	}
	if consumed {
		t.Fatal("expected token to be rejected after consumption")
	}

	expiredHash, err := HashBootstrapToken("expired-token")
	if err != nil {
		t.Fatalf("hash expired token: %v", err)
	}
	if err := store.CreateBootstrapToken(ctx, expiredHash, 5678, now.Add(-1*time.Minute)); err != nil {
		t.Fatalf("create expired token: %v", err)
	}
	consumed, err = store.ConsumeBootstrapToken(ctx, expiredHash, 5678, now)
	if err != nil {
		t.Fatalf("consume expired token: %v", err)
	}
	if consumed {
		t.Fatal("expected expired token to be rejected")
	}
}

func TestValidateBootstrapToken(t *testing.T) {
	store := openTestStore(t)
	defer func() {
		_ = store.Close()
	}()
	ctx := context.Background()
	now := time.Now().UTC()
	tokenHash, err := HashBootstrapToken("token-validate")
	if err != nil {
		t.Fatalf("hash token: %v", err)
	}
	if err := store.CreateBootstrapToken(ctx, tokenHash, 4321, now.Add(5*time.Minute)); err != nil {
		t.Fatalf("create token: %v", err)
	}

	valid, err := store.ValidateBootstrapToken(ctx, tokenHash, 4321, now)
	if err != nil {
		t.Fatalf("validate token: %v", err)
	}
	if !valid {
		t.Fatal("expected token to be valid")
	}

	valid, err = store.ValidateBootstrapToken(ctx, tokenHash, 9999, now)
	if err != nil {
		t.Fatalf("validate token wrong vmid: %v", err)
	}
	if valid {
		t.Fatal("expected token to be invalid for wrong vmid")
	}

	if _, err := store.ConsumeBootstrapToken(ctx, tokenHash, 4321, now); err != nil {
		t.Fatalf("consume token: %v", err)
	}
	valid, err = store.ValidateBootstrapToken(ctx, tokenHash, 4321, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("validate consumed token: %v", err)
	}
	if valid {
		t.Fatal("expected consumed token to be invalid")
	}

	expiredHash, err := HashBootstrapToken("token-expired")
	if err != nil {
		t.Fatalf("hash expired token: %v", err)
	}
	if err := store.CreateBootstrapToken(ctx, expiredHash, 7777, now.Add(-time.Minute)); err != nil {
		t.Fatalf("create expired token: %v", err)
	}
	valid, err = store.ValidateBootstrapToken(ctx, expiredHash, 7777, now)
	if err != nil {
		t.Fatalf("validate expired token: %v", err)
	}
	if valid {
		t.Fatal("expected expired token to be invalid")
	}
}
