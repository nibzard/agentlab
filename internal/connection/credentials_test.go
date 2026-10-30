package connection

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

type memoryVault struct {
	mu                                      sync.Mutex
	values                                  map[string][]byte
	failSave, failLoad, failDelete, corrupt bool
}

func (*memoryVault) Name() string { return "secret-service" }
func (v *memoryVault) Save(_ context.Context, ref string, data []byte) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.failSave {
		return credentialFailure("credential_store_locked", "unlock the fixture credential store")
	}
	if v.values == nil {
		v.values = map[string][]byte{}
	}
	v.values[ref] = bytes.Clone(data)
	return nil
}
func (v *memoryVault) Load(_ context.Context, ref string) ([]byte, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.failLoad {
		return nil, credentialFailure("credential_store_locked", "unlock the fixture credential store")
	}
	if v.corrupt {
		return []byte(`{"token":"wrong-record"}`), nil
	}
	data, ok := v.values[ref]
	if !ok {
		return nil, credentialFailure("credential_not_found", "restore the fixture credential")
	}
	return bytes.Clone(data), nil
}
func (v *memoryVault) Delete(_ context.Context, ref string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.failDelete {
		return credentialFailure("credential_cleanup_required", "restore fixture deletion")
	}
	delete(v.values, ref)
	return nil
}

func TestCredentialStoreMigrationKeepsReferencesAndSelection(t *testing.T) {
	vault := &memoryVault{}
	store := ContextStore{Root: t.TempDir(), CredentialMode: "file", Vault: vault}
	cfg := Settings{Endpoint: "https://server.example", Token: "private-credential", ServerIdentity: "server-identity", JumpHost: "jump.example", JumpUser: "operator"}
	if err := store.Put("server", cfg); err != nil {
		t.Fatal(err)
	}
	old, _, _ := store.List()
	store.CredentialMode = "os"
	if err := store.Put("server", cfg); err != nil {
		t.Fatal(err)
	}
	items, active, err := store.List()
	if err != nil || active != "server" || len(items) != 1 || items[0].CredentialStorage != "secret-service" {
		t.Fatalf("metadata: %v %v", items, err)
	}
	if _, err := os.Stat(filepath.Join(store.Root, "credentials", old[0].CredentialRef+".json")); !os.IsNotExist(err) {
		t.Fatal("old file credential remains")
	}
	data, err := os.ReadFile(filepath.Join(store.Root, "contexts.json"))
	if err != nil || bytes.Contains(data, []byte(cfg.Token)) {
		t.Fatal("secret in context metadata")
	}
	var index contextIndex
	if err := json.Unmarshal(data, &index); err != nil || index.Version != 2 {
		t.Fatal("native reference did not version the context format")
	}
	// Choosing file storage for future writes must not change an existing read.
	store.CredentialMode = "file"
	actual, _, found, err := store.Resolve("")
	if err != nil || !found || actual != cfg {
		t.Fatalf("reference resolution: %v", err)
	}
	if err := store.Put("server", cfg); err != nil {
		t.Fatal(err)
	}
	if len(vault.values) != 0 {
		t.Fatal("old native credential remains after migration")
	}
}

func TestCredentialStoreFailurePreservesWorkingConnection(t *testing.T) {
	for _, failure := range []string{"save", "verification"} {
		t.Run(failure, func(t *testing.T) {
			vault := &memoryVault{}
			store := ContextStore{Root: t.TempDir(), CredentialMode: "file", Vault: vault}
			cfg := Settings{Endpoint: "https://server.example", Token: "working-secret"}
			if err := store.Put("server", cfg); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(store.Root, "contexts.json")
			before, _ := os.ReadFile(path)
			store.CredentialMode = "os"
			vault.failSave = failure == "save"
			vault.corrupt = failure == "verification"
			if err := store.Put("server", Settings{Endpoint: cfg.Endpoint, Token: "replacement-secret"}); err == nil {
				t.Fatal("failed storage succeeded")
			}
			after, _ := os.ReadFile(path)
			if !bytes.Equal(before, after) {
				t.Fatal("failed storage changed the working context")
			}
			actual, _, _, err := store.Resolve("server")
			if err != nil || actual.Token != cfg.Token {
				t.Fatal("working file credential changed")
			}
			pending, err := store.PendingCredentials()
			if err != nil {
				t.Fatal(err)
			}
			if failure == "verification" {
				if len(pending) != 1 || len(vault.values) != 1 {
					t.Fatal("unverified record was deleted without ownership proof")
				}
				vault.corrupt = false
				if err := store.CleanupCredentials(); err != nil {
					t.Fatal(err)
				}
			} else if len(pending) != 0 {
				t.Fatal("failed creation left an unnecessary intent")
			}
			if len(vault.values) != 0 {
				t.Fatal("restored storage retained an unreferenced credential")
			}
		})
	}
}

func TestCredentialReadNeverFallsBackToFile(t *testing.T) {
	vault := &memoryVault{}
	store := ContextStore{Root: t.TempDir(), CredentialMode: "os", Vault: vault}
	if err := store.Put("server", Settings{Endpoint: "https://server.example", Token: "native-secret"}); err != nil {
		t.Fatal(err)
	}
	items, _, _ := store.List()
	directory, err := (fileCredentialVault{root: store.Root}).directory(true)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, items[0].CredentialRef+".json"), []byte(`{"token":"must-not-use"}`), 0600); err != nil {
		t.Fatal(err)
	}
	vault.failLoad = true
	store.CredentialMode = "file"
	actual, _, found, err := store.Resolve("server")
	if err == nil || found || actual.Token != "" {
		t.Fatal("failed native read selected a file credential")
	}
}

func TestCredentialRetirementSurvivesFailureAndRestart(t *testing.T) {
	vault := &memoryVault{}
	store := ContextStore{Root: t.TempDir(), CredentialMode: "os", Vault: vault}
	if err := store.Put("server", Settings{Endpoint: "https://server.example", Token: "old-secret"}); err != nil {
		t.Fatal(err)
	}
	vault.failDelete = true
	err := store.Put("server", Settings{Endpoint: "https://server.example", Token: "new-secret"})
	var status *CredentialError
	if !errors.As(err, &status) || !status.Saved || status.Code != "credential_cleanup_required" {
		t.Fatalf("cleanup failure lost saved state: %v", err)
	}
	actual, _, _, err := store.Resolve("server")
	if err != nil || actual.Token != "new-secret" {
		t.Fatal("cleanup failure invalidated new credential")
	}
	data, _ := os.ReadFile(filepath.Join(store.Root, "contexts.json"))
	var index contextIndex
	if err := json.Unmarshal(data, &index); err != nil || len(index.Retired) != 1 {
		t.Fatal("retirement was not durable")
	}
	vault.failDelete = false
	restarted := ContextStore{Root: store.Root, Vault: vault}
	if err := restarted.CleanupCredentials(); err != nil {
		t.Fatal(err)
	}
	if len(vault.values) != 1 {
		t.Fatal("cleanup deleted the current credential or retained the old credential")
	}
	if err := restarted.CleanupCredentials(); err != nil {
		t.Fatal("cleanup retry is not idempotent")
	}
	if err := restarted.Remove("server"); err != nil || len(vault.values) != 0 {
		t.Fatal("context removal did not retire native credential")
	}
}

func TestCredentialCleanupProtectsAnActiveReference(t *testing.T) {
	vault := &memoryVault{}
	store := ContextStore{Root: t.TempDir(), CredentialMode: "os", Vault: vault}
	if err := store.Put("server", Settings{Endpoint: "https://server.example", Token: "active-secret"}); err != nil {
		t.Fatal(err)
	}
	if err := store.locked(context.Background(), func(_ context.Context, index *contextIndex) error {
		index.Retired = append(index.Retired, index.Contexts["server"])
		return store.save(index)
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.CleanupCredentials(); err == nil || len(vault.values) != 1 {
		t.Fatal("cleanup removed an active credential")
	}
}

func TestCredentialFileRejectsSymlinksPermissionsAndOversize(t *testing.T) {
	for _, failure := range []string{"symlink", "permissions", "oversize"} {
		t.Run(failure, func(t *testing.T) {
			store := ContextStore{Root: t.TempDir(), CredentialMode: "file"}
			if err := store.Put("server", Settings{Endpoint: "https://server.example", Token: "file-secret"}); err != nil {
				t.Fatal(err)
			}
			items, _, _ := store.List()
			path := filepath.Join(store.Root, "credentials", items[0].CredentialRef+".json")
			switch failure {
			case "symlink":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(store.Root, "missing"), path); err != nil {
					t.Fatal(err)
				}
			case "permissions":
				if err := os.Chmod(path, 0644); err != nil {
					t.Fatal(err)
				}
			case "oversize":
				if err := os.WriteFile(path, bytes.Repeat([]byte("x"), maxCredentialBytes+1), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if _, _, _, err := store.Resolve("server"); err == nil {
				t.Fatal("unsafe credential file accepted")
			}
		})
	}
}

func TestContextLockWaitUsesCallerCancellation(t *testing.T) {
	store := ContextStore{Root: t.TempDir(), CredentialMode: "file"}
	file, err := os.OpenFile(filepath.Join(store.Root, "contexts.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	defer syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	start := time.Now()
	err = store.PutContext(ctx, "server", Settings{Endpoint: "https://server.example"})
	if err == nil || time.Since(start) > time.Second {
		t.Fatal("context lock ignored cancellation")
	}
}

func TestDamagedContextEntriesDoNotSelectLocalTrust(t *testing.T) {
	for _, failure := range []string{"dangling-symlink", "fifo", "oversize", "lock-fifo"} {
		t.Run(failure, func(t *testing.T) {
			store := ContextStore{Root: t.TempDir(), CredentialMode: "file"}
			if err := store.Put("server", Settings{Endpoint: "https://server.example", Token: "preserved-secret"}); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(store.Root, "contexts.json")
			if failure == "lock-fifo" {
				path = filepath.Join(store.Root, "contexts.lock")
			}
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			switch failure {
			case "dangling-symlink":
				if err := os.Symlink(filepath.Join(store.Root, "missing"), path); err != nil {
					t.Fatal(err)
				}
			case "fifo", "lock-fifo":
				if err := syscall.Mkfifo(path, 0600); err != nil {
					t.Fatal(err)
				}
			case "oversize":
				if err := os.WriteFile(path, bytes.Repeat([]byte("x"), (1<<20)+1), 0600); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			cfg, _, found, err := store.ResolveContext(ctx, "")
			if err == nil || found || cfg.Token != "" || ctx.Err() != nil {
				t.Fatal("damaged context selected a credential or waited on hidden input")
			}
		})
	}
}

func TestCredentialModeUnavailableAndMetadataDoNotExposeSecrets(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("an absent session bus tests the Linux adapter")
	}
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "")
	store := ContextStore{Root: t.TempDir(), CredentialMode: "os"}
	err := store.Put("server", Settings{Endpoint: "https://server.example", Token: "must-not-appear"})
	var status *CredentialError
	if !errors.As(err, &status) || status.Code != "credential_store_unavailable" || strings.Contains(err.Error(), "must-not-appear") {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(store.Root, "contexts.json")); !os.IsNotExist(err) {
		t.Fatal("failed native selection published a context")
	}
	store.CredentialMode = "auto"
	if err := store.Put("server", Settings{Endpoint: "https://server.example", Token: "fallback-secret"}); err != nil {
		t.Fatal(err)
	}
	items, _, _ := store.List()
	if items[0].CredentialStorage != "file" {
		t.Fatal("unavailable native store did not select documented file storage")
	}
}
