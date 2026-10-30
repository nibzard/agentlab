package connection

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"syscall"
	"time"
)

// Context stores public connection metadata and a credential reference.
type Context struct {
	Name              string            `json:"name"`
	Endpoint          string            `json:"endpoint"`
	Transport         string            `json:"transport"`
	ServerIdentity    string            `json:"server_identity,omitempty"`
	CredentialRef     string            `json:"credential_ref,omitempty"`
	CredentialStorage string            `json:"credential_storage,omitempty"`
	JumpHost          string            `json:"jump_host,omitempty"`
	JumpUser          string            `json:"jump_user,omitempty"`
	AllowInsecureHTTP bool              `json:"allow_insecure_http,omitempty"`
	Defaults          map[string]string `json:"defaults,omitempty"`
}

type contextIndex struct {
	Version  int                `json:"version"`
	Active   string             `json:"active"`
	Contexts map[string]Context `json:"contexts"`
	Retired  []Context          `json:"retired_credentials,omitempty"`
}

var contextNamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,63}$`)

// ContextStore uses a protected directory and serializes changes across clients.
// Root is the AgentLab configuration directory, not its parent.
type ContextStore struct {
	Root           string
	CredentialMode string
	// Vault is an optional native adapter, used by isolated callers and tests.
	Vault CredentialVault
}

func (s ContextStore) locked(parent context.Context, fn func(context.Context, *contextIndex) error) error {
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	if err := os.MkdirAll(s.Root, 0700); err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(s.Root, "contexts.lock"), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if info, err := lock.Stat(); err != nil || !info.Mode().IsRegular() {
		return credentialFailure("invalid_context_lock", "preserve the unexpected lock entry and restore a regular context lock file before retrying")
	}
	for {
		if err := ctx.Err(); err != nil {
			return credentialFailure("context_store_busy", "wait for the active context operation to finish, then retry")
		}
		err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			return err
		}
		select {
		case <-ctx.Done():
			return credentialFailure("context_store_busy", "wait for the active context operation to finish, then retry")
		case <-time.After(10 * time.Millisecond):
		}
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	index, err := s.readIndex()
	if err != nil {
		return err
	}
	return fn(ctx, index)
}

func (s ContextStore) readIndex() (*contextIndex, error) {
	index := contextIndex{Version: 1, Contexts: map[string]Context{}}
	data, err := readContextIndex(filepath.Join(s.Root, "contexts.json"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if len(data) > 0 {
		if err := json.Unmarshal(data, &index); err != nil {
			return nil, fmt.Errorf("invalid contexts: %w", err)
		}
		if (index.Version != 1 && index.Version != 2) || index.Contexts == nil {
			return nil, fmt.Errorf("unsupported contexts file; restore its backup")
		}
	}
	return &index, nil
}

func readContextIndex(path string) ([]byte, error) {
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	if info, err := file.Stat(); err != nil || !info.Mode().IsRegular() {
		return nil, credentialFailure("invalid_context_file", "preserve the unexpected context entry and restore a regular context metadata file")
	}
	data, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	if err != nil || len(data) > 1<<20 {
		return nil, credentialFailure("invalid_context_file", "restore valid context metadata smaller than 1 MiB")
	}
	return data, nil
}

func atomicProtectedJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	if len(data) > 1<<20 {
		return credentialFailure("context_file_too_large", "reduce context metadata to less than 1 MiB before saving")
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".agentlab-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err := file.Write(append(data, '\n')); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(file.Name(), path); err != nil {
		return err
	}
	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		return &CredentialError{Code: "context_durability_unverified", Action: "connection metadata is saved, but directory durability could not be verified; restore access and inspect the context before retrying", Saved: true}
	}
	defer directory.Close()
	if err := directory.Sync(); err != nil {
		return &CredentialError{Code: "context_durability_unverified", Action: "connection metadata is saved, but directory durability could not be verified; restore access and inspect the context before retrying", Saved: true}
	}
	return nil
}

func (s ContextStore) save(index *contextIndex) error {
	for _, item := range index.Contexts {
		if item.CredentialStorage != "" && item.CredentialStorage != "file" {
			index.Version = 2
		}
	}
	for _, item := range index.Retired {
		if item.CredentialStorage != "" && item.CredentialStorage != "file" {
			index.Version = 2
		}
	}
	return atomicProtectedJSON(filepath.Join(s.Root, "contexts.json"), index)
}

// ValidateContextName checks a destination before a workflow changes a server.
func ValidateContextName(name string) error {
	if !contextNamePattern.MatchString(name) {
		return fmt.Errorf("invalid context name; use 1 to 64 letters, digits, dots, underscores, or hyphens")
	}
	return nil
}
func (s ContextStore) put(ctx context.Context, index *contextIndex, name string, cfg Settings) (*credentialIntent, error) {
	if err := ValidateContextName(name); err != nil {
		return nil, err
	}
	endpoint, err := NormalizeEndpoint(cfg.Endpoint)
	if err != nil {
		return nil, err
	}
	if endpoint == "" {
		return nil, fmt.Errorf("a context requires a remote endpoint")
	}
	if err := ValidateEndpointPolicy(endpoint, cfg.AllowInsecureHTTP); err != nil {
		return nil, err
	}
	// Resolve interrupted writes for this context before allocating another
	// credential. Other contexts can proceed independently.
	if err := s.recoverCredentialIntents(ctx, index, name); err != nil {
		return nil, err
	}
	ref, err := newCredentialID()
	if err != nil {
		return nil, err
	}
	transactionID, err := newCredentialID()
	if err != nil {
		return nil, err
	}
	vault, err := s.writeVault(ctx)
	if err != nil {
		return nil, err
	}
	configuration, err := credentialConfiguration(s.Root)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(credentialRecord{Version: 1, TransactionID: transactionID, Reference: ref, Settings: cfg})
	if err != nil {
		return nil, err
	}
	if len(data) > maxCredentialBytes {
		return nil, credentialFailure("credential_too_large", "use a connection credential record smaller than 128 KiB")
	}
	digest := sha256.Sum256(data)
	intent := credentialIntent{CredentialTransaction: CredentialTransaction{ID: transactionID, Context: name, CredentialRef: ref, CredentialStorage: vault.Name(), Endpoint: endpoint, ServerIdentity: cfg.ServerIdentity, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}, Version: 1, Configuration: configuration, PayloadSHA256: hex.EncodeToString(digest[:])}
	if err := intent.seal(); err != nil {
		return nil, err
	}
	if err := s.writeCredentialIntent(intent); err != nil {
		return nil, err
	}
	fail := func(cause error) (*credentialIntent, error) {
		if cleanupErr := s.recoverFailedCredentialWrite(intent); cleanupErr != nil {
			return nil, pendingCredentialError(cleanupErr, intent, false)
		}
		return nil, cause
	}
	if err := vault.Save(ctx, ref, data); err != nil {
		return fail(err)
	}
	actual, err := vault.Load(ctx, ref)
	if err != nil || !bytes.Equal(data, actual) {
		return fail(credentialFailure("credential_store_verification_failed", "credential storage did not verify; the previous connection is preserved; restore the credential store before retrying"))
	}
	transport := "https"
	if strings.HasPrefix(endpoint, "http://") {
		transport = "http"
	}
	index.Contexts[name] = Context{Name: name, Endpoint: endpoint, Transport: transport, ServerIdentity: cfg.ServerIdentity, CredentialRef: ref, CredentialStorage: vault.Name(), JumpHost: cfg.JumpHost, JumpUser: cfg.JumpUser, AllowInsecureHTTP: cfg.AllowInsecureHTTP}
	if index.Active == "" {
		index.Active = name
	}
	return &intent, nil
}

// Migrate preserves the legacy file and creates a default context only once.
func (s ContextStore) Migrate(legacy Settings) error {
	return s.locked(context.Background(), func(ctx context.Context, index *contextIndex) error {
		if _, err := os.Lstat(filepath.Join(s.Root, "contexts.json")); err == nil {
			return nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if len(index.Contexts) != 0 || legacy.Endpoint == "" {
			return nil
		}
		intent, err := s.put(ctx, index, "default", legacy)
		if err != nil {
			return err
		}
		if err := s.save(index); err != nil {
			if !contextCommitSaved(err) {
				if recoveryErr := s.recoverFailedCredentialWrite(*intent); recoveryErr != nil {
					return pendingCredentialError(recoveryErr, *intent, false)
				}
			} else {
				return pendingCredentialError(err, *intent, true)
			}
			return err
		}
		if err := s.recoverCredentialIntent(ctx, index, *intent); err != nil {
			return pendingCredentialError(err, *intent, true)
		}
		return nil
	})
}

// Put saves a verified connection without changing an existing active context.
func (s ContextStore) Put(name string, cfg Settings) error {
	return s.PutContext(context.Background(), name, cfg)
}

func (s ContextStore) PutContext(parent context.Context, name string, cfg Settings) error {
	return s.putContextChecked(parent, name, cfg, nil, false)
}

// PutContextIfMatch preserves a context changed by another client. A nil
// expectation creates a new context only when that name is still absent.
func (s ContextStore) PutContextIfMatch(parent context.Context, name string, expected *Context, cfg Settings) error {
	return s.putContextChecked(parent, name, cfg, expected, true)
}
func (s ContextStore) putContextChecked(parent context.Context, name string, cfg Settings, expected *Context, conditional bool) error {
	return s.locked(parent, func(ctx context.Context, index *contextIndex) error {
		if conditional {
			if err := s.recoverCredentialIntents(ctx, index, name); err != nil {
				return err
			}
			actual, exists := index.Contexts[name]
			if (expected == nil && exists) || (expected != nil && (!exists || !reflect.DeepEqual(actual, *expected))) {
				return credentialFailure("context_binding_changed", "the destination context changed; inspect the retained credential before publishing it")
			}
		}
		previous := index.Contexts[name]
		intent, err := s.put(ctx, index, name, cfg)
		if err != nil {
			return err
		}
		if previous.Defaults != nil && previous.Endpoint == cfg.Endpoint && previous.ServerIdentity == cfg.ServerIdentity {
			current := index.Contexts[name]
			current.Defaults = previous.Defaults
			index.Contexts[name] = current
		}
		if previous.CredentialRef != "" {
			index.Retired = append(index.Retired, previous)
		}
		if err := s.save(index); err != nil {
			if !contextCommitSaved(err) {
				if recoveryErr := s.recoverFailedCredentialWrite(*intent); recoveryErr != nil {
					return pendingCredentialError(recoveryErr, *intent, false)
				}
			} else {
				return pendingCredentialError(err, *intent, true)
			}
			return err
		}
		if err := s.recoverCredentialIntent(ctx, index, *intent); err != nil {
			return pendingCredentialError(err, *intent, true)
		}
		return s.cleanCredentials(ctx, index, name)
	})
}

// Use changes the default for future commands. Running clients retain settings.
func (s ContextStore) Use(name string) error {
	return s.locked(context.Background(), func(ctx context.Context, index *contextIndex) error {
		if _, ok := index.Contexts[name]; !ok {
			return fmt.Errorf("unknown context %q; run agentlab context list", name)
		}
		index.Active = name
		return s.save(index)
	})
}

// List returns public metadata only. Credential contents never enter the output.
func (s ContextStore) List() ([]Context, string, error) {
	var items []Context
	var active string
	err := s.locked(context.Background(), func(ctx context.Context, index *contextIndex) error {
		active = index.Active
		for _, item := range index.Contexts {
			items = append(items, item)
		}
		return nil
	})
	return items, active, err
}

// Resolve selects the requested or active context and loads its credential.
// A missing named context is an error; it never selects the trusted socket.
func (s ContextStore) Resolve(name string) (Settings, string, bool, error) {
	return s.ResolveContext(context.Background(), name)
}

func (s ContextStore) ResolveContext(parent context.Context, name string) (Settings, string, bool, error) {
	var cfg Settings
	var selected string
	var found bool
	// Reads without a contexts file must not create configuration directories.
	if _, err := os.Lstat(filepath.Join(s.Root, "contexts.json")); errors.Is(err, os.ErrNotExist) && name == "" {
		return cfg, "", false, nil
	}
	err := s.locked(parent, func(ctx context.Context, index *contextIndex) error {
		selected = name
		if selected == "" {
			selected = index.Active
		}
		if selected == "" {
			return fmt.Errorf("no active context; run agentlab context use NAME")
		}
		item, ok := index.Contexts[selected]
		if !ok {
			return fmt.Errorf("unknown context %q; run agentlab context list", selected)
		}
		var loadErr error
		cfg, loadErr = s.loadContextCredential(ctx, item)
		if loadErr != nil {
			return loadErr
		}
		found = true
		return nil
	})
	if err != nil {
		cfg = Settings{}
	}
	return cfg, selected, found, err
}

// Remove forgets a context. It does not revoke its daemon credential.
// Removing the active context requires an explicit choice before more work.
func (s ContextStore) Remove(name string) error {
	return s.removeContextChecked(context.Background(), name, nil, false)
}
func (s ContextStore) RemoveContextIfMatch(parent context.Context, name string, expected Context) error {
	return s.removeContextChecked(parent, name, &expected, true)
}
func (s ContextStore) removeContextChecked(parent context.Context, name string, expected *Context, conditional bool) error {
	return s.locked(parent, func(ctx context.Context, index *contextIndex) error {
		if conditional {
			actual, exists := index.Contexts[name]
			if !exists {
				return s.cleanCredentials(ctx, index, name)
			}
			if !reflect.DeepEqual(actual, *expected) {
				return credentialFailure("context_binding_changed", "the staged context changed; preserve it and inspect credential cleanup")
			}
		}
		item, ok := index.Contexts[name]
		if !ok {
			return fmt.Errorf("unknown context %q", name)
		}
		if err := s.recoverCredentialIntents(ctx, index, name); err != nil {
			return err
		}
		delete(index.Contexts, name)
		index.Retired = append(index.Retired, item)
		if index.Active == name {
			index.Active = ""
		}
		if err := s.save(index); err != nil {
			return err
		}
		return s.cleanCredentials(ctx, index, name)
	})
}

func contextCommitSaved(err error) bool {
	var status *CredentialError
	return errors.As(err, &status) && status.Saved
}

// CleanupCredentials reconciles durable write intents and retirement records.
// It never deletes
// a currently referenced credential or searches other application credentials.
func (s ContextStore) CleanupCredentials() error {
	return s.locked(context.Background(), func(ctx context.Context, index *contextIndex) error {
		if err := s.recoverCredentialIntents(ctx, index, ""); err != nil {
			return err
		}
		return s.cleanCredentials(ctx, index, "")
	})
}

func (s ContextStore) cleanCredentials(ctx context.Context, index *contextIndex, name string) error {
	for position := 0; position < len(index.Retired); {
		item := index.Retired[position]
		if name != "" && item.Name != name {
			position++
			continue
		}
		for _, active := range index.Contexts {
			if active.CredentialRef == item.CredentialRef && credentialStorageName(active.CredentialStorage) == credentialStorageName(item.CredentialStorage) {
				return &CredentialError{Code: "credential_cleanup_conflict", Action: "preserve the credential referenced by an active context and repair the retirement metadata before retrying cleanup", Saved: true}
			}
		}
		if err := s.deleteCredential(ctx, item); err != nil {
			return &CredentialError{Code: "credential_cleanup_required", Action: "connection metadata is saved; restore access to the recorded credential store, then run agentlab context credentials-cleanup", Saved: true}
		}
		index.Retired = append(index.Retired[:position], index.Retired[position+1:]...)
		if err := s.save(index); err != nil {
			return err
		}
	}
	return nil
}

// LookupContext reads public metadata without opening a credential store.
func (s ContextStore) LookupContext(parent context.Context, name string, allowUnselected bool) (Context, string, bool, error) {
	var item Context
	selected := name
	found := false
	if _, err := os.Lstat(filepath.Join(s.Root, "contexts.json")); errors.Is(err, os.ErrNotExist) && name == "" {
		return item, "", false, nil
	}
	err := s.locked(parent, func(_ context.Context, index *contextIndex) error {
		if selected == "" {
			selected = index.Active
		}
		if selected == "" {
			if allowUnselected {
				return nil
			}
			return &ResolutionError{Code: "context_required", Requirement: "server_context", Action: "select a context with agentlab context use NAME before continuing"}
		}
		var ok bool
		item, ok = index.Contexts[selected]
		if !ok {
			if allowUnselected {
				return nil
			}
			return &ResolutionError{Code: "unknown_context", Requirement: "server_context", Action: "unknown context; inspect agentlab context list and select an existing context"}
		}
		if item.Name != selected {
			return &ResolutionError{Code: "invalid_context", Requirement: "server_context", Action: "restore consistent context names before resolving the connection"}
		}
		found = true
		return nil
	})
	return item, selected, found, err
}

func sameContextBinding(a, b Context) bool {
	return a.Name == b.Name && a.Transport == b.Transport && a.Endpoint == b.Endpoint && a.ServerIdentity == b.ServerIdentity && a.CredentialRef == b.CredentialRef && credentialStorageName(a.CredentialStorage) == credentialStorageName(b.CredentialStorage) && a.AllowInsecureHTTP == b.AllowInsecureHTTP && a.JumpHost == b.JumpHost && a.JumpUser == b.JumpUser
}

// LoadBoundContext refuses a replacement reference instead of mixing a new
// credential with a previously selected endpoint or server identity.
func (s ContextStore) LoadBoundContext(parent context.Context, expected Context) (Settings, error) {
	var cfg Settings
	err := s.locked(parent, func(ctx context.Context, index *contextIndex) error {
		actual, ok := index.Contexts[expected.Name]
		if !ok || !sameContextBinding(actual, expected) {
			return &ResolutionError{Code: "context_changed", Requirement: "stable_connection_context", Action: "the selected context changed; retry with the intended explicit context"}
		}
		var err error
		cfg, err = s.loadContextCredential(ctx, actual)
		return err
	})
	if err != nil {
		return Settings{}, err
	}
	return cfg, nil
}

func (s ContextStore) loadContextCredential(ctx context.Context, item Context) (Settings, error) {
	if !validCredentialReference(item.CredentialRef) {
		return Settings{}, credentialFailure("invalid_credential_reference", "restore the context credential reference before retrying")
	}
	vault, err := s.readVault(ctx, item.CredentialStorage)
	if err != nil {
		return Settings{}, err
	}
	data, err := vault.Load(ctx, item.CredentialRef)
	if err != nil {
		return Settings{}, err
	}
	var record credentialRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return Settings{}, credentialFailure("invalid_credential_record", "restore a valid credential record, or reconnect securely")
	}
	if record.Version != 0 || record.Reference != "" || record.TransactionID != "" {
		if record.Version != 1 || record.Reference != item.CredentialRef || !validCredentialReference(record.TransactionID) {
			return Settings{}, credentialFailure("credential_binding_mismatch", "preserve the mismatched credential record and reconnect to the intended server")
		}
	}
	endpoint, err := NormalizeEndpoint(record.Endpoint)
	if err != nil || endpoint != item.Endpoint || record.ServerIdentity != item.ServerIdentity {
		return Settings{}, credentialFailure("credential_binding_mismatch", "preserve the credential and restore its original context binding, or reconnect securely")
	}
	cfg := record.Settings
	cfg.Endpoint = item.Endpoint
	cfg.ServerIdentity = item.ServerIdentity
	cfg.AllowInsecureHTTP = item.AllowInsecureHTTP
	return cfg, nil
}
