package secrets

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"filippo.io/age"
)

func writeAgeKey(t *testing.T, dir string) string {
	t.Helper()
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatalf("generate age identity: %v", err)
	}
	keyPath := filepath.Join(dir, "age.key")
	if err := os.WriteFile(keyPath, []byte(identity.String()+"\n"), 0o600); err != nil {
		t.Fatalf("write age key: %v", err)
	}
	return keyPath
}

func TestStoreMutateCreatesAgeBundle(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	keyPath := writeAgeKey(t, dir)
	store := Store{Dir: dir, AgeKeyPath: keyPath}

	bundle, path, err := store.Mutate(context.Background(), "default", func(b *Bundle) error {
		b.Env = map[string]string{"ANTHROPIC_API_KEY": "sk-test"}
		return nil
	})
	if err != nil {
		t.Fatalf("mutate: %v", err)
	}
	if bundle.Env["ANTHROPIC_API_KEY"] != "sk-test" {
		t.Fatalf("returned bundle missing env: %#v", bundle.Env)
	}
	if !strings.HasSuffix(path, ".age") {
		t.Fatalf("expected .age path, got %s", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read bundle: %v", err)
	}
	if !strings.HasPrefix(string(data), "age-encryption.org") {
		t.Fatalf("expected age-encrypted file, got %q", string(data[:40]))
	}

	// A fresh store can decrypt what Mutate wrote.
	reload, err := Store{Dir: dir, AgeKeyPath: keyPath}.Load(context.Background(), "default")
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reload.Env["ANTHROPIC_API_KEY"] != "sk-test" {
		t.Fatalf("reload missing env")
	}
}

func TestStoreMutateUpdatesExisting(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	keyPath := writeAgeKey(t, dir)
	store := Store{Dir: dir, AgeKeyPath: keyPath}

	if _, _, err := store.Mutate(context.Background(), "default", func(b *Bundle) error {
		b.Env = map[string]string{"A": "1"}
		return nil
	}); err != nil {
		t.Fatalf("first mutate: %v", err)
	}
	if _, _, err := store.Mutate(context.Background(), "default", func(b *Bundle) error {
		b.Env["B"] = "2"
		return nil
	}); err != nil {
		t.Fatalf("second mutate: %v", err)
	}
	reload, err := Store{Dir: dir, AgeKeyPath: keyPath}.Load(context.Background(), "default")
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reload.Env["A"] != "1" || reload.Env["B"] != "2" {
		t.Fatalf("merge lost keys: %#v", reload.Env)
	}
}

func TestStoreMutatePlaintextRefused(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// No age key, plaintext disallowed: a new bundle resolves to .yaml and the
	// write must be refused.
	store := Store{Dir: dir}
	_, _, err := store.Mutate(context.Background(), "default", func(b *Bundle) error {
		b.Env = map[string]string{"A": "1"}
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "allow-plaintext") {
		t.Fatalf("expected allow-plaintext refusal, got %v", err)
	}
}

func TestStoreMutatePlaintextAllowed(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store := Store{Dir: dir, AllowPlaintext: true}
	bundle, path, err := store.Mutate(context.Background(), "default", func(b *Bundle) error {
		b.Env = map[string]string{"A": "1"}
		return nil
	})
	if err != nil {
		t.Fatalf("mutate: %v", err)
	}
	if !strings.HasSuffix(path, ".yaml") {
		t.Fatalf("expected .yaml path, got %s", path)
	}
	reload, err := Store{Dir: dir, AllowPlaintext: true}.Load(context.Background(), "default")
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reload.Env["A"] != bundle.Env["A"] {
		t.Fatalf("reload mismatch")
	}
}
