package connection

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

const maxCredentialBytes = 128 << 10

var errCredentialStoreUnavailable = errors.New("operating system credential store unavailable")

// CredentialVault stores credential records without exposing secrets to logs
// or command arguments. Implementations must not open authentication prompts.
type CredentialVault interface {
	Name() string
	Save(context.Context, string, []byte) error
	Load(context.Context, string) ([]byte, error)
	Delete(context.Context, string) error
}

type CredentialError struct {
	Code, Action string
	Saved        bool
	Transaction  *CredentialTransaction
}

func (e *CredentialError) Error() string { return e.Action }
func (e *CredentialError) ErrorMetadata() map[string]any {
	metadata := map[string]any{"state": "blocked", "requirement": "credential_store", "code": e.Code, "action": e.Action, "retryable": e.Code == "context_store_busy", "context_metadata_saved": e.Saved}
	if e.Transaction != nil {
		metadata["credential_transaction_pending"] = true
		metadata["credential_transaction"] = e.Transaction
	}
	return metadata
}

func credentialFailure(code, action string) error {
	return &CredentialError{Code: code, Action: action}
}

func credentialStorageName(kind string) string {
	if kind == "" {
		return "file"
	}
	return kind
}

// CredentialStorageMode selects storage for new or replaced credentials only.
// Existing references always use their recorded store, without a fallback.
func CredentialStorageMode(explicit string) (string, error) {
	mode := strings.TrimSpace(explicit)
	if mode == "" {
		mode = strings.TrimSpace(os.Getenv("AGENTLAB_CREDENTIAL_STORE"))
	}
	if mode == "" {
		mode = "auto"
	}
	switch mode {
	case "auto", "file", "os":
		return mode, nil
	default:
		return "", credentialFailure("invalid_credential_store", "select credential storage with auto, file, or os")
	}
}

func (s ContextStore) nativeVault(ctx context.Context) (CredentialVault, error) {
	if s.Vault != nil {
		return s.Vault, nil
	}
	return openPlatformCredentialVault(ctx, s.Root)
}

func (s ContextStore) writeVault(ctx context.Context) (CredentialVault, error) {
	mode, err := CredentialStorageMode(s.CredentialMode)
	if err != nil {
		return nil, err
	}
	if mode != "file" {
		vault, err := s.nativeVault(ctx)
		if err == nil {
			return vault, nil
		}
		if mode == "os" || !errors.Is(err, errCredentialStoreUnavailable) {
			if errors.Is(err, errCredentialStoreUnavailable) {
				return nil, credentialFailure("credential_store_unavailable", "provide an unlocked operating system credential store, or explicitly select file storage for unattended use")
			}
			return nil, err
		}
	}
	return fileCredentialVault{root: s.Root}, nil
}

func (s ContextStore) readVault(ctx context.Context, kind string) (CredentialVault, error) {
	if kind == "" || kind == "file" {
		return fileCredentialVault{root: s.Root}, nil
	}
	if kind != "secret-service" && kind != "macos-keychain" {
		return nil, credentialFailure("unsupported_credential_store", "use a client that supports the credential store recorded by this context")
	}
	vault, err := s.nativeVault(ctx)
	if err != nil {
		var status *CredentialError
		if errors.As(err, &status) {
			return nil, status
		}
		return nil, credentialFailure("credential_store_unavailable", "restore the credential store recorded by this context, or reconnect with a replacement credential; no file fallback is used")
	}
	if vault.Name() != kind {
		return nil, credentialFailure("credential_store_unavailable", "restore the credential store recorded by this context, or reconnect with a replacement credential; no file fallback is used")
	}
	return vault, nil
}

func (s ContextStore) deleteCredential(ctx context.Context, item Context) error {
	if item.CredentialRef == "" {
		return nil
	}
	if !validCredentialReference(item.CredentialRef) {
		return credentialFailure("invalid_credential_reference", "restore the context metadata before removing its credential")
	}
	vault, err := s.readVault(ctx, item.CredentialStorage)
	if err != nil {
		return err
	}
	return vault.Delete(ctx, item.CredentialRef)
}

func validCredentialReference(ref string) bool {
	return len(ref) == 32 && strings.Trim(ref, "0123456789abcdef") == ""
}

type fileCredentialVault struct{ root string }

func (fileCredentialVault) Name() string { return "file" }

func (v fileCredentialVault) path(ref string) (string, error) {
	if !validCredentialReference(ref) {
		return "", credentialFailure("invalid_credential_reference", "restore the context credential reference before retrying")
	}
	return filepath.Join(v.root, "credentials", ref+".json"), nil
}

func (v fileCredentialVault) directory(create bool) (string, error) {
	directory := filepath.Join(v.root, "credentials")
	info, err := os.Lstat(directory)
	if errors.Is(err, os.ErrNotExist) && create {
		if err := os.Mkdir(directory, 0700); err != nil {
			return "", credentialFailure("credential_store_unavailable", "restore write access to the protected credential directory")
		}
		info, err = os.Lstat(directory)
	}
	if err != nil {
		return "", err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0077 != 0 {
		return "", credentialFailure("credential_file_permissions", "restore a private regular credential directory; preserve unexpected files and symlinks")
	}
	return directory, nil
}

func (v fileCredentialVault) stagePath(ref, transactionID string) (string, error) {
	if !validCredentialReference(ref) || !validCredentialReference(transactionID) {
		return "", credentialFailure("invalid_credential_reference", "restore valid credential transaction references before cleanup")
	}
	return filepath.Join(v.root, "credentials", "."+ref+"-"+transactionID+".pending"), nil
}

func (v fileCredentialVault) Save(ctx context.Context, ref string, data []byte) error {
	path, err := v.path(ref)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(data) > maxCredentialBytes {
		return credentialFailure("credential_too_large", "use a connection credential record smaller than 128 KiB")
	}
	var record credentialRecord
	if json.Unmarshal(data, &record) != nil || record.Version != 1 || record.Reference != ref || !validCredentialReference(record.TransactionID) {
		return credentialFailure("invalid_credential_record", "prepare a journaled credential record before saving it")
	}
	directory, err := v.directory(true)
	if err != nil {
		return err
	}
	stage, err := v.stagePath(ref, record.TransactionID)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(stage, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return credentialFailure("credential_stage_conflict", "preserve the existing credential stage and recover pending credentials before retrying")
	}
	defer os.Remove(stage)
	_, writeErr := file.Write(data)
	syncErr := file.Sync()
	closeErr := file.Close()
	if writeErr != nil || syncErr != nil || closeErr != nil {
		return credentialFailure("credential_store_unavailable", "cannot save the protected credential stage; restore the credential directory and retry cleanup")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.Link(stage, path); err != nil {
		if errors.Is(err, os.ErrExist) {
			return credentialFailure("credential_reference_exists", "preserve the existing credential reference and recover pending credentials before retrying")
		}
		return credentialFailure("credential_store_unavailable", "cannot publish a protected credential record; restore the credential directory and retry cleanup")
	}
	if err := syncCredentialDirectory(directory); err != nil {
		return credentialFailure("credential_store_unavailable", "credential publication durability is unverified; inspect pending credentials before retrying")
	}
	return v.removeStage(ref, record.TransactionID)
}

func (v fileCredentialVault) removeStage(ref, transactionID string) error {
	directory, err := v.directory(false)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	path, err := v.stagePath(ref, transactionID)
	if err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		// A prior unlink can be visible before its directory sync succeeds.
		return syncCredentialDirectory(directory)
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return credentialFailure("credential_stage_changed", "preserve the unexpected credential stage and restore a private regular stage before retrying cleanup")
	}
	if err := os.Remove(path); err != nil {
		return credentialFailure("credential_cleanup_required", "restore access to the owned credential stage before retrying cleanup")
	}
	return syncCredentialDirectory(directory)
}

func (v fileCredentialVault) Load(ctx context.Context, ref string) ([]byte, error) {
	path, err := v.path(ref)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, err := v.directory(false); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, credentialFailure("credential_not_found", "this context credential is absent; restore it or reconnect")
		}
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, credentialFailure("credential_not_found", "this context credential is absent; restore it or reconnect")
		}
		return nil, credentialFailure("credential_store_unavailable", "restore this context's protected credential file or reconnect with a replacement credential")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return nil, credentialFailure("credential_file_permissions", "use a regular credential file with mode 0600; preserve its contents while repairing access")
	}
	data, err := io.ReadAll(io.LimitReader(file, maxCredentialBytes+1))
	if err != nil || len(data) > maxCredentialBytes {
		return nil, credentialFailure("invalid_credential_record", "restore a valid credential record smaller than 128 KiB, or reconnect")
	}
	return data, nil
}

func (v fileCredentialVault) Delete(ctx context.Context, ref string) error {
	path, err := v.path(ref)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	directory, err := v.directory(false)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
		return credentialFailure("credential_cleanup_required", "preserve the unexpected credential entry and restore the original regular credential file before retrying cleanup")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return credentialFailure("credential_cleanup_required", "restore access to the credential directory before retrying cleanup")
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return credentialFailure("credential_cleanup_required", fmt.Sprintf("restore access to the credential directory and remove unused credential reference %s", ref))
	}
	return syncCredentialDirectory(directory)
}
