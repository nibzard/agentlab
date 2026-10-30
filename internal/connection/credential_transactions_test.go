package connection

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func interruptedCredential(t *testing.T, store ContextStore, name string, cfg Settings, commit bool) credentialIntent {
	t.Helper()
	var prepared *credentialIntent
	err := store.locked(context.Background(), func(ctx context.Context, index *contextIndex) error {
		previous := index.Contexts[name]
		var err error
		prepared, err = store.put(ctx, index, name, cfg)
		if err != nil {
			return err
		}
		if commit {
			if previous.CredentialRef != "" {
				index.Retired = append(index.Retired, previous)
			}
			return store.save(index)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return *prepared
}

func assertNoPendingCredentials(t *testing.T, store ContextStore) {
	t.Helper()
	items, err := store.PendingCredentials()
	if err != nil || len(items) != 0 {
		t.Fatalf("pending credential transactions remain: %d %v", len(items), err)
	}
}

func TestCredentialIntentRecoveryBeforeAndAfterContextCommit(t *testing.T) {
	for _, native := range []bool{false, true} {
		for _, commit := range []bool{false, true} {
			t.Run(map[bool]string{false: "file", true: "native"}[native]+map[bool]string{false: "-uncommitted", true: "-committed"}[commit], func(t *testing.T) {
				vault := &memoryVault{}
				store := ContextStore{Root: t.TempDir(), CredentialMode: "file", Vault: vault}
				if native {
					store.CredentialMode = "os"
				}
				cfg := Settings{Endpoint: "https://server.example", Token: "private-interrupted-credential", ServerIdentity: "expected-server"}
				intent := interruptedCredential(t, store, "server", cfg, commit)
				pending, err := store.PendingCredentials()
				if err != nil || len(pending) != 1 || pending[0].ID != intent.ID {
					t.Fatal("interrupted write is not observable")
				}
				public, err := json.Marshal(pending)
				if err != nil || bytes.Contains(public, []byte(cfg.Token)) || bytes.Contains(public, []byte(intent.PayloadSHA256)) {
					t.Fatal("pending metadata exposed credential material")
				}
				restarted := ContextStore{Root: store.Root, CredentialMode: "file", Vault: vault}
				if err := restarted.CleanupCredentials(); err != nil {
					t.Fatal(err)
				}
				assertNoPendingCredentials(t, restarted)
				actual, _, found, err := restarted.Resolve("")
				if commit {
					if err != nil || !found || actual != cfg {
						t.Fatal("recovery removed the committed credential")
					}
					if err := restarted.Remove("server"); err != nil {
						t.Fatal(err)
					}
				} else {
					if err != nil || found {
						t.Fatal("uncommitted recovery invented a context")
					}
					if _, err := os.Lstat(filepath.Join(store.Root, "contexts.json")); !os.IsNotExist(err) {
						t.Fatal("cleanup created an empty context index")
					}
				}
				if native && len(vault.values) != 0 {
					t.Fatal("native recovery left an unreferenced credential")
				}
			})
		}
	}
}

func TestCredentialIntentRecoveryPreservesWorkingConnection(t *testing.T) {
	store := ContextStore{Root: t.TempDir(), CredentialMode: "file"}
	original := Settings{Endpoint: "https://working.example", Token: "working-secret"}
	if err := store.Put("server", original); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(store.Root, "contexts.json"))
	interruptedCredential(t, store, "server", Settings{Endpoint: "https://replacement.example", Token: "uncommitted-secret"}, false)
	if err := store.CleanupCredentials(); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(filepath.Join(store.Root, "contexts.json"))
	if !bytes.Equal(before, after) {
		t.Fatal("orphan cleanup changed the working context")
	}
	actual, _, _, err := store.Resolve("server")
	if err != nil || actual != original {
		t.Fatal("orphan cleanup changed the working credential")
	}
	files, err := os.ReadDir(filepath.Join(store.Root, "credentials"))
	if err != nil || len(files) != 1 {
		t.Fatal("orphan credential or stage remains")
	}
}

func TestCredentialIntentRecoveryBeforeSameContextRetry(t *testing.T) {
	vault := &memoryVault{}
	store := ContextStore{Root: t.TempDir(), CredentialMode: "os", Vault: vault}
	intent := interruptedCredential(t, store, "server", Settings{Endpoint: "https://server.example", Token: "uncommitted-secret"}, false)
	if err := store.Put("server", Settings{Endpoint: "https://server.example", Token: "retried-secret"}); err != nil {
		t.Fatal(err)
	}
	if len(vault.values) != 1 || vault.values[intent.CredentialRef] != nil {
		t.Fatal("retry duplicated an uncommitted credential")
	}
	assertNoPendingCredentials(t, store)
	actual, _, _, err := store.Resolve("server")
	if err != nil || actual.Token != "retried-secret" {
		t.Fatal("retry did not save the requested credential")
	}
}

func TestCredentialRetirementFailureDoesNotBlockOtherContexts(t *testing.T) {
	vault := &memoryVault{}
	store := ContextStore{Root: t.TempDir(), CredentialMode: "os", Vault: vault}
	if err := store.Put("one", Settings{Endpoint: "https://one.example", Token: "old-secret"}); err != nil {
		t.Fatal(err)
	}
	vault.failDelete = true
	if err := store.Put("one", Settings{Endpoint: "https://one.example", Token: "new-secret"}); err == nil {
		t.Fatal("retirement failure was hidden")
	}
	store.CredentialMode = "file"
	if err := store.Put("two", Settings{Endpoint: "https://two.example", Token: "independent-secret"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Remove("two"); err != nil {
		t.Fatal(err)
	}
	vault.failDelete = false
	if err := store.CleanupCredentials(); err != nil {
		t.Fatal(err)
	}
	if len(vault.values) != 1 {
		t.Fatal("cleanup changed the current native credential")
	}
}

type lostCredentialAcknowledgement struct{ *memoryVault }

func (v lostCredentialAcknowledgement) Save(ctx context.Context, ref string, data []byte) error {
	if err := v.memoryVault.Save(ctx, ref, data); err != nil {
		return err
	}
	return credentialFailure("credential_store_unavailable", "fixture store accepted the write but its response was lost")
}

func TestCredentialIntentRecoversLostWriteAcknowledgement(t *testing.T) {
	for _, cleanupFails := range []bool{false, true} {
		t.Run(map[bool]string{false: "immediate", true: "retry"}[cleanupFails], func(t *testing.T) {
			vault := &memoryVault{failDelete: cleanupFails}
			store := ContextStore{Root: t.TempDir(), CredentialMode: "file", Vault: lostCredentialAcknowledgement{vault}}
			cfg := Settings{Endpoint: "https://working.example", Token: "working-secret"}
			if err := store.Put("server", cfg); err != nil {
				t.Fatal(err)
			}
			before, _ := os.ReadFile(filepath.Join(store.Root, "contexts.json"))
			store.CredentialMode = "os"
			err := store.Put("server", Settings{Endpoint: cfg.Endpoint, Token: "unconfirmed-secret"})
			if err == nil {
				t.Fatal("lost acknowledgement was reported as a completed connection")
			}
			after, _ := os.ReadFile(filepath.Join(store.Root, "contexts.json"))
			if !bytes.Equal(before, after) {
				t.Fatal("unknown write outcome changed the working context")
			}
			if cleanupFails {
				var status *CredentialError
				if !errors.As(err, &status) || status.Transaction == nil || status.Saved || len(vault.values) != 1 {
					t.Fatal("pending write lost its recovery metadata")
				}
				vault.failDelete = false
				restarted := ContextStore{Root: store.Root, Vault: vault}
				if err := restarted.CleanupCredentials(); err != nil {
					t.Fatal(err)
				}
			}
			assertNoPendingCredentials(t, store)
			if len(vault.values) != 0 {
				t.Fatal("a write with a lost response leaked a credential")
			}
		})
	}
}

func TestCredentialIntentPreservesChangedRecordAndOtherContexts(t *testing.T) {
	vault := &memoryVault{}
	store := ContextStore{Root: t.TempDir(), CredentialMode: "os", Vault: vault}
	intent := interruptedCredential(t, store, "server", Settings{Endpoint: "https://server.example", Token: "original-secret"}, false)
	var record credentialRecord
	if err := json.Unmarshal(vault.values[intent.CredentialRef], &record); err != nil {
		t.Fatal(err)
	}
	record.Token = "user-edited-secret"
	changed, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	vault.values[intent.CredentialRef] = changed
	err = store.CleanupCredentials()
	var status *CredentialError
	if !errors.As(err, &status) || status.Code != "credential_transaction_changed" || status.Transaction == nil {
		t.Fatalf("changed record was not blocked: %v", err)
	}
	if !bytes.Equal(vault.values[intent.CredentialRef], changed) {
		t.Fatal("cleanup changed an unverified credential")
	}
	if err := store.Put("server", Settings{Endpoint: "https://server.example", Token: "must-not-duplicate"}); err == nil || len(vault.values) != 1 {
		t.Fatal("retry bypassed unresolved ownership for the same context")
	}
	// A pending write for one context must not redirect or block another one.
	if err := store.Put("other", Settings{Endpoint: "https://other.example", Token: "other-secret"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Remove("other"); err != nil {
		t.Fatal(err)
	}
	if len(vault.values) != 1 || !bytes.Equal(vault.values[intent.CredentialRef], changed) {
		t.Fatal("another context deleted the pending record")
	}
}

func TestCredentialIntentPreservesCommittedRecordWhenProviderChanges(t *testing.T) {
	vault := &memoryVault{}
	store := ContextStore{Root: t.TempDir(), CredentialMode: "os", Vault: vault}
	intent := interruptedCredential(t, store, "server", Settings{Endpoint: "https://server.example", Token: "committed-secret"}, true)
	vault.values[intent.CredentialRef] = []byte(`{"token":"user-edited-secret"}`)
	vault.failLoad = true
	if err := store.CleanupCredentials(); err != nil {
		t.Fatal(err)
	}
	assertNoPendingCredentials(t, store)
	if len(vault.values) != 1 {
		t.Fatal("recovery deleted an active reference")
	}
}

func TestCredentialIntentCleansPartialFileStage(t *testing.T) {
	store := ContextStore{Root: t.TempDir(), CredentialMode: "file"}
	intent := interruptedCredential(t, store, "server", Settings{Endpoint: "https://server.example", Token: "partial-fixture-secret"}, false)
	vault := fileCredentialVault{root: store.Root}
	path, _ := vault.path(intent.CredentialRef)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	stage, _ := vault.stagePath(intent.CredentialRef, intent.ID)
	if err := os.WriteFile(stage, data[:len(data)/2], 0600); err != nil {
		t.Fatal(err)
	}
	if err := store.CleanupCredentials(); err != nil {
		t.Fatal(err)
	}
	assertNoPendingCredentials(t, store)
	if _, err := os.Lstat(stage); !os.IsNotExist(err) {
		t.Fatal("partial owned stage remains")
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatal("recovery promoted an incomplete credential")
	}
}

func TestCredentialIntentPreservesUnexpectedStageAndActiveHardLink(t *testing.T) {
	for _, active := range []bool{false, true} {
		t.Run(map[bool]string{false: "unexpected-symlink", true: "active-hard-link"}[active], func(t *testing.T) {
			store := ContextStore{Root: t.TempDir(), CredentialMode: "file"}
			cfg := Settings{Endpoint: "https://server.example", Token: "preserved-secret"}
			intent := interruptedCredential(t, store, "server", cfg, active)
			vault := fileCredentialVault{root: store.Root}
			path, _ := vault.path(intent.CredentialRef)
			stage, _ := vault.stagePath(intent.CredentialRef, intent.ID)
			if active {
				if err := os.Link(path, stage); err != nil {
					t.Fatal(err)
				}
				if err := store.CleanupCredentials(); err != nil {
					t.Fatal(err)
				}
				actual, _, _, err := store.Resolve("server")
				if err != nil || actual != cfg {
					t.Fatal("stage cleanup damaged the active hard link")
				}
			} else {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				outside := filepath.Join(store.Root, "outside")
				if err := os.WriteFile(outside, []byte("preserve user content"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, stage); err != nil {
					t.Fatal(err)
				}
				if err := store.CleanupCredentials(); err == nil {
					t.Fatal("unexpected stage was removed")
				}
				content, err := os.ReadFile(outside)
				if err != nil || string(content) != "preserve user content" {
					t.Fatal("cleanup followed the stage symlink")
				}
			}
		})
	}
}

func TestCredentialIntentRejectsChangedJournal(t *testing.T) {
	for _, mode := range []string{"invalid-json", "foreign-root", "wrong-hash", "wrong-store", "symlink", "permissions", "oversize", "unknown-entry"} {
		t.Run(mode, func(t *testing.T) {
			store := ContextStore{Root: t.TempDir(), CredentialMode: "file", Vault: &memoryVault{}}
			intent := interruptedCredential(t, store, "server", Settings{Endpoint: "https://server.example", Token: "must-not-appear"}, false)
			journal := filepath.Join(store.Root, "credential-transactions", intent.CredentialRef+".json")
			data, _ := os.ReadFile(journal)
			switch mode {
			case "invalid-json":
				data = []byte(`{"token":"must-not-appear"`)
			case "foreign-root":
				intent.Configuration = strings.Repeat("a", 64)
				data, _ = json.Marshal(intent)
			case "wrong-hash":
				intent.PayloadSHA256 = strings.Repeat("a", 64)
				data, _ = json.Marshal(intent)
			case "wrong-store":
				intent.CredentialStorage = "secret-service"
				data, _ = json.Marshal(intent)
			case "oversize":
				data = bytes.Repeat([]byte("x"), 16*1024+1)
			case "symlink":
				if err := os.Remove(journal); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(store.Root, "missing"), journal); err != nil {
					t.Fatal(err)
				}
			case "permissions":
				if err := os.Chmod(journal, 0644); err != nil {
					t.Fatal(err)
				}
			case "unknown-entry":
				if err := os.WriteFile(filepath.Join(filepath.Dir(journal), "user-notes"), []byte("preserve"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "invalid-json" || mode == "foreign-root" || mode == "wrong-hash" || mode == "wrong-store" || mode == "oversize" {
				if err := os.WriteFile(journal, data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			err := store.CleanupCredentials()
			if err == nil || strings.Contains(err.Error(), "must-not-appear") {
				t.Fatalf("changed journal not safely blocked: %v", err)
			}
			path, _ := (fileCredentialVault{root: store.Root}).path(intent.CredentialRef)
			if _, err := os.Stat(path); err != nil {
				t.Fatal("invalid ownership metadata deleted a credential")
			}
		})
	}
}

func TestCredentialIntentMetadataStageAndLimit(t *testing.T) {
	store := ContextStore{Root: t.TempDir(), CredentialMode: "file"}
	directory, err := store.transactionDirectory(true)
	if err != nil {
		t.Fatal(err)
	}
	stage := filepath.Join(directory, ".intent-"+strings.Repeat("a", 32))
	if err := os.WriteFile(stage, []byte(`{"version":`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := store.CleanupCredentials(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(stage); !os.IsNotExist(err) {
		t.Fatal("interrupted metadata stage remains")
	}
	configuration, err := credentialConfiguration(store.Root)
	if err != nil {
		t.Fatal(err)
	}
	for range maxCredentialTransactions {
		ref, err := newCredentialID()
		if err != nil {
			t.Fatal(err)
		}
		id, err := newCredentialID()
		if err != nil {
			t.Fatal(err)
		}
		intent := credentialIntent{Version: 1, Configuration: configuration, PayloadSHA256: strings.Repeat("b", 64), CredentialTransaction: CredentialTransaction{ID: id, CredentialRef: ref, CredentialStorage: "file", Context: "other", Endpoint: "https://fixture.example", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}}
		if err := intent.seal(); err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(intent)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, ref+".json"), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	err = store.Put("new", Settings{Endpoint: "https://new.example", Token: "must-not-store"})
	var status *CredentialError
	if !errors.As(err, &status) || status.Code != "credential_journal_limit" {
		t.Fatalf("pending write limit was not enforced: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(store.Root, "credentials")); !os.IsNotExist(err) {
		t.Fatal("journal overflow created a credential")
	}
}

// The child exits without defers, as a terminated client does. Only its private
// test root is writable. The parent proves recovery from the persisted files.
type crashFileCredentialVault struct {
	fileCredentialVault
	phase string
}

func (v crashFileCredentialVault) Save(ctx context.Context, ref string, data []byte) error {
	if v.phase == "intent" {
		os.Exit(42)
	}
	if v.phase == "stage" {
		var record credentialRecord
		if err := json.Unmarshal(data, &record); err != nil {
			return err
		}
		if _, err := v.directory(true); err != nil {
			return err
		}
		stage, err := v.stagePath(ref, record.TransactionID)
		if err != nil {
			return err
		}
		if err := os.WriteFile(stage, data[:len(data)/2], 0600); err != nil {
			return err
		}
		if err := syncCredentialDirectory(filepath.Dir(stage)); err != nil {
			return err
		}
		os.Exit(42)
	}
	if err := v.fileCredentialVault.Save(ctx, ref, data); err != nil {
		return err
	}
	if v.phase == "linked-stage" {
		var record credentialRecord
		if err := json.Unmarshal(data, &record); err != nil {
			return err
		}
		stage, _ := v.stagePath(ref, record.TransactionID)
		path, _ := v.path(ref)
		if err := os.Link(path, stage); err != nil {
			return err
		}
		os.Exit(42)
	}
	if v.phase == "stored" {
		os.Exit(42)
	}
	return nil
}

func TestCredentialCrashWorker(t *testing.T) {
	root := os.Getenv("AGENTLAB_CREDENTIAL_CRASH_ROOT")
	if root == "" {
		t.Skip("subprocess fixture")
	}
	if !filepath.IsAbs(root) {
		t.Fatal("crash fixture requires an absolute private root")
	}
	phase := os.Getenv("AGENTLAB_CREDENTIAL_CRASH_PHASE")
	store := ContextStore{Root: root, CredentialMode: "os", Vault: crashFileCredentialVault{fileCredentialVault: fileCredentialVault{root: root}, phase: phase}}
	cfg := Settings{Endpoint: "https://crash-fixture.example", Token: "private-crash-fixture-token"}
	if phase == "committed" || phase == "deleted" {
		err := store.locked(context.Background(), func(ctx context.Context, index *contextIndex) error {
			intent, err := store.put(ctx, index, "server", cfg)
			if err != nil {
				return err
			}
			if phase == "committed" {
				if err := store.save(index); err != nil {
					return err
				}
			} else {
				if err := store.deleteCredential(ctx, Context{CredentialRef: intent.CredentialRef, CredentialStorage: "file"}); err != nil {
					return err
				}
			}
			os.Exit(42)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	} else if err := store.Put("server", cfg); err != nil {
		t.Fatal(err)
	}
	t.Fatal("crash fixture did not reach its checkpoint")
}

func TestCredentialRecoveryAfterClientProcessExit(t *testing.T) {
	for _, phase := range []string{"intent", "stage", "stored", "linked-stage", "committed", "deleted"} {
		t.Run(phase, func(t *testing.T) {
			root := t.TempDir()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCredentialCrashWorker$")
			command.Env = append(os.Environ(), "AGENTLAB_CREDENTIAL_CRASH_ROOT="+root, "AGENTLAB_CREDENTIAL_CRASH_PHASE="+phase)
			output, err := command.CombinedOutput()
			var status *exec.ExitError
			if !errors.As(err, &status) || status.ExitCode() != 42 {
				t.Fatalf("child did not exit at its checkpoint: %v %s", err, output)
			}
			store := ContextStore{Root: root, CredentialMode: "file"}
			pending, err := store.PendingCredentials()
			if err != nil || len(pending) != 1 {
				t.Fatalf("crashed write lost its ownership record: %v", err)
			}
			if client := os.Getenv("AGENTLAB_TEST_CLIENT"); client != "" {
				if !filepath.IsAbs(client) {
					t.Fatal("snapshot client path must be absolute")
				}
				for _, action := range []string{"credentials-pending", "credentials-cleanup"} {
					command := exec.CommandContext(ctx, client, "context", action, "--json")
					command.Dir = root
					command.Env = []string{"PATH=" + os.Getenv("PATH"), "AGENTLAB_CONFIG_DIR=" + root, "AGENTLAB_CREDENTIAL_STORE=invalid"}
					output, err := command.Output()
					if err != nil {
						t.Fatal("snapshot client could not recover the terminated client's write")
					}
					if bytes.Contains(output, []byte("private-crash-fixture-token")) {
						t.Fatal("snapshot recovery exposed a credential")
					}
					if action == "credentials-pending" && !bytes.Contains(output, []byte(pending[0].ID)) {
						t.Fatal("snapshot client omitted the pending transaction ID")
					}
				}
			}
			if err := store.CleanupCredentials(); err != nil {
				t.Fatal(err)
			}
			assertNoPendingCredentials(t, store)
			cfg, _, found, err := store.Resolve("")
			if phase == "committed" {
				if err != nil || !found || cfg.Token != "private-crash-fixture-token" {
					t.Fatal("recovery deleted a committed credential")
				}
				if err := store.Remove("server"); err != nil {
					t.Fatal(err)
				}
			} else {
				if err != nil || found {
					t.Fatal("recovery published an incomplete connection")
				}
				if _, err := os.Lstat(filepath.Join(root, "contexts.json")); !os.IsNotExist(err) {
					t.Fatal("orphan recovery changed legacy or local connection selection")
				}
			}
			if entries, err := os.ReadDir(filepath.Join(root, "credentials")); err == nil && len(entries) != 0 {
				t.Fatal("crash recovery left credential files or stages")
			}
		})
	}
}
