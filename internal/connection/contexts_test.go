package connection

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestContextsMigrateAndBind(t *testing.T) {
	store := ContextStore{Root: t.TempDir(), CredentialMode: "file"}
	legacy := Settings{Endpoint: "https://one.example", Token: "secret-one"}
	if err := store.Migrate(legacy); err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(Settings{Endpoint: "https://other.example", Token: "other"}); err != nil {
		t.Fatal(err)
	}
	bound, name, found, err := store.Resolve("")
	if err != nil || !found || name != "default" || bound.Token != legacy.Token {
		t.Fatalf("migration: %s %v %v", name, found, err)
	}
	if err := store.Put("two", Settings{Endpoint: "https://two.example", Token: "secret-two"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Use("two"); err != nil {
		t.Fatal(err)
	}
	active, _, _, err := store.Resolve("")
	if err != nil {
		t.Fatal(err)
	}
	if active.Endpoint != "https://two.example" || bound.Endpoint != legacy.Endpoint {
		t.Fatal("switch changed a bound connection")
	}
	explicit, _, _, err := store.Resolve("default")
	if err != nil || explicit.Token != legacy.Token {
		t.Fatal("explicit context changed")
	}
	if _, _, _, err := store.Resolve("missing"); err == nil {
		t.Fatal("unknown context selected another server")
	}
	data, err := os.ReadFile(filepath.Join(store.Root, "contexts.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "secret-") {
		t.Fatal("credential in public metadata")
	}
	items, _, _ := store.List()
	for _, item := range items {
		info, err := os.Stat(filepath.Join(store.Root, "credentials", item.CredentialRef+".json"))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0600 {
			t.Fatal("credential is not protected")
		}
	}
}

func TestContextConcurrentWrites(t *testing.T) {
	store := ContextStore{Root: t.TempDir(), CredentialMode: "file"}
	var wg sync.WaitGroup
	for _, name := range []string{"one", "two", "three"} {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			if err := store.Put(name, Settings{Endpoint: "https://" + name + ".example"}); err != nil {
				t.Error(err)
			}
		}(name)
	}
	wg.Wait()
	items, _, err := store.List()
	if err != nil || len(items) != 3 {
		t.Fatalf("lost concurrent write: %d %v", len(items), err)
	}
}

func TestContextRejectsInvalidInput(t *testing.T) {
	store := ContextStore{Root: t.TempDir(), CredentialMode: "file"}
	for _, name := range []string{"../escape", "", "a/b"} {
		if err := store.Put(name, Settings{Endpoint: "https://example.com"}); err == nil {
			t.Fatalf("accepted %q", name)
		}
	}
	if err := store.Put("unsafe", Settings{Endpoint: "http://remote.example"}); err == nil {
		t.Fatal("accepted insecure connection")
	}
}

func TestRemovingActiveContextDoesNotSelectAnotherServer(t *testing.T) {
	store := ContextStore{Root: t.TempDir(), CredentialMode: "file"}
	legacy := Settings{Endpoint: "https://legacy.example", Token: "old"}
	if err := store.Migrate(legacy); err != nil {
		t.Fatal(err)
	}
	if err := store.Remove("default"); err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(legacy); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := store.Resolve(""); err == nil {
		t.Fatal("removed context silently restored")
	}
	if err := store.Put("new", Settings{Endpoint: "https://new.example"}); err != nil {
		t.Fatal(err)
	}
	cfg, _, _, err := store.Resolve("")
	if err != nil || cfg.Endpoint != "https://new.example" {
		t.Fatalf("reconnect: %v", err)
	}
}

func TestUpdatingContextRemovesPreviousCredential(t *testing.T) {
	store := ContextStore{Root: t.TempDir(), CredentialMode: "file"}
	if err := store.Put("server", Settings{Endpoint: "https://example.com", Token: "old"}); err != nil {
		t.Fatal(err)
	}
	previous, _, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Put("server", Settings{Endpoint: "https://example.com", Token: "new"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(store.Root, "credentials", previous[0].CredentialRef+".json")); !os.IsNotExist(err) {
		t.Fatalf("previous credential remains: %v", err)
	}
	cfg, _, _, err := store.Resolve("server")
	if err != nil || cfg.Token != "new" {
		t.Fatalf("replacement failed: %v", err)
	}
}
