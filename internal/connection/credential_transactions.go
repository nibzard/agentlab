package connection

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const maxCredentialTransactions = 128

// CredentialTransaction reports an interrupted local write without its token
// or the private digest used to verify ownership during cleanup.
type CredentialTransaction struct {
	ID                string `json:"id"`
	Context           string `json:"context"`
	CredentialRef     string `json:"credential_ref"`
	CredentialStorage string `json:"credential_storage"`
	Endpoint          string `json:"endpoint"`
	ServerIdentity    string `json:"server_identity,omitempty"`
	CreatedAt         string `json:"created_at"`
}

type credentialIntent struct {
	CredentialTransaction
	Version       int    `json:"version"`
	Configuration string `json:"configuration"`
	PayloadSHA256 string `json:"payload_sha256"`
	IntentSHA256  string `json:"intent_sha256,omitempty"`
}

func (intent *credentialIntent) seal() error {
	intent.IntentSHA256 = ""
	data, err := json.Marshal(intent)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(data)
	intent.IntentSHA256 = hex.EncodeToString(digest[:])
	return nil
}

func (intent credentialIntent) validSeal() bool {
	expected := intent.IntentSHA256
	if len(expected) != 64 {
		return false
	}
	if err := intent.seal(); err != nil {
		return false
	}
	return intent.IntentSHA256 == expected
}

// New records bind the generated reference and transaction ID. Legacy records
// without these fields remain readable, but cannot be claimed by an intent.
type credentialRecord struct {
	Version       int    `json:"credential_record_version"`
	TransactionID string `json:"credential_transaction"`
	Reference     string `json:"credential_reference"`
	Settings
}

func credentialConfiguration(root string) (string, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256([]byte(filepath.Clean(absolute)))
	return hex.EncodeToString(digest[:]), nil
}

func newCredentialID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}

func syncCredentialDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func (s ContextStore) transactionDirectory(create bool) (string, error) {
	path := filepath.Join(s.Root, "credential-transactions")
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) && create {
		if err = os.Mkdir(path, 0700); err != nil {
			return "", credentialFailure("credential_journal_unavailable", "restore the private credential transaction directory before saving a connection")
		}
		// The journal directory must survive before a credential can be created.
		if err = syncCredentialDirectory(s.Root); err != nil {
			return "", credentialFailure("credential_journal_unavailable", "restore directory durability before saving a connection")
		}
		info, err = os.Lstat(path)
	}
	if err != nil {
		return "", err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0077 != 0 {
		return "", credentialFailure("credential_journal_invalid", "preserve the unexpected credential transaction directory and restore a private regular directory")
	}
	return path, nil
}

func (s ContextStore) writeCredentialIntent(intent credentialIntent) error {
	if !intent.validSeal() {
		return credentialFailure("credential_journal_invalid", "prepare valid credential ownership metadata before storing a credential")
	}
	existing, err := s.readCredentialIntents()
	if err != nil {
		return err
	}
	if len(existing) >= maxCredentialTransactions {
		return credentialFailure("credential_journal_limit", "recover pending credential transactions before creating more connections")
	}
	directory, err := s.transactionDirectory(true)
	if err != nil {
		return err
	}
	data, err := json.Marshal(intent)
	if err != nil {
		return err
	}
	if len(data) > 16*1024 {
		return credentialFailure("credential_journal_limit", "use connection metadata small enough for a 16 KiB credential transaction record")
	}
	temp, err := os.OpenFile(filepath.Join(directory, ".intent-"+intent.ID), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return credentialFailure("credential_journal_unavailable", "restore write access to the credential transaction directory")
	}
	defer os.Remove(temp.Name())
	_, writeErr := temp.Write(data)
	syncErr := temp.Sync()
	closeErr := temp.Close()
	if writeErr != nil || syncErr != nil || closeErr != nil {
		return credentialFailure("credential_journal_unavailable", "restore durable storage for credential transaction records before retrying")
	}
	// Link publishes a complete record without replacing an existing entry.
	if err := os.Link(temp.Name(), filepath.Join(directory, intent.CredentialRef+".json")); err != nil {
		return credentialFailure("credential_journal_conflict", "preserve the existing credential transaction entry and inspect pending credentials before retrying")
	}
	if err := syncCredentialDirectory(directory); err != nil {
		return credentialFailure("credential_journal_unavailable", "the credential intent is saved but its durability is unverified; inspect pending credentials before retrying")
	}
	if err := syncCredentialDirectory(s.Root); err != nil {
		return credentialFailure("credential_journal_unavailable", "restore durable access to the configuration directory before storing a credential")
	}
	return nil
}

func (s ContextStore) readCredentialIntents() ([]credentialIntent, error) {
	directory, err := s.transactionDirectory(false)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	file, err := os.Open(directory)
	if err != nil {
		return nil, credentialFailure("credential_journal_unavailable", "restore read access to the credential transaction directory")
	}
	defer file.Close()
	// Bound the scan before allocating or reading an unbounded set of files.
	entries, err := file.ReadDir(2*maxCredentialTransactions + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	if len(entries) > 2*maxCredentialTransactions {
		return nil, credentialFailure("credential_journal_limit", "inspect and recover pending credential transactions before creating more connections")
	}
	configuration, err := credentialConfiguration(s.Root)
	if err != nil {
		return nil, err
	}
	var intents []credentialIntent
	for _, entry := range entries {
		// A crash while publishing an intent can leave a temporary metadata
		// file. It contains no credential and is never executed or adopted.
		if strings.HasPrefix(entry.Name(), ".intent-") && validCredentialReference(strings.TrimPrefix(entry.Name(), ".intent-")) {
			continue
		}
		if !strings.HasSuffix(entry.Name(), ".json") || !validCredentialReference(strings.TrimSuffix(entry.Name(), ".json")) {
			return nil, credentialFailure("credential_journal_invalid", "preserve unexpected entries in the credential transaction directory before retrying cleanup")
		}
		reader, err := os.OpenFile(filepath.Join(directory, entry.Name()), os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
		if err != nil {
			return nil, credentialFailure("credential_journal_invalid", "restore a regular credential transaction record before retrying cleanup")
		}
		info, statErr := reader.Stat()
		if statErr != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
			reader.Close()
			return nil, credentialFailure("credential_journal_invalid", "preserve the unexpected transaction entry and restore a private regular record")
		}
		data, readErr := io.ReadAll(io.LimitReader(reader, 16*1024+1))
		reader.Close()
		if readErr != nil || len(data) > 16*1024 {
			return nil, credentialFailure("credential_journal_invalid", "restore a credential transaction record smaller than 16 KiB")
		}
		var intent credentialIntent
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		decodeErr := decoder.Decode(&intent)
		var extra any
		endpoint, endpointErr := NormalizeEndpoint(intent.Endpoint)
		_, timeErr := time.Parse(time.RFC3339Nano, intent.CreatedAt)
		if decodeErr != nil || decoder.Decode(&extra) != io.EOF || intent.Version != 1 || !intent.validSeal() || !validCredentialReference(intent.ID) || !validCredentialReference(intent.CredentialRef) || entry.Name() != intent.CredentialRef+".json" || !contextNamePattern.MatchString(intent.Context) || intent.Configuration != configuration || len(intent.PayloadSHA256) != 64 || strings.Trim(intent.PayloadSHA256, "0123456789abcdef") != "" || endpointErr != nil || endpoint == "" || endpoint != intent.Endpoint || timeErr != nil || (intent.CredentialStorage != "file" && intent.CredentialStorage != "secret-service" && intent.CredentialStorage != "macos-keychain") {
			return nil, credentialFailure("credential_journal_invalid", "preserve the changed transaction record; restore its original configuration directory and valid ownership metadata before cleanup")
		}
		intents = append(intents, intent)
		if len(intents) > maxCredentialTransactions {
			return nil, credentialFailure("credential_journal_limit", "recover pending credential transactions before creating more connections")
		}
	}
	return intents, nil
}

func (s ContextStore) forgetCredentialIntent(intent credentialIntent) error {
	directory, err := s.transactionDirectory(false)
	if err != nil {
		return err
	}
	// Validate all records before any removal. Do not remove a replaced intent.
	intents, err := s.readCredentialIntents()
	if err != nil {
		return err
	}
	for _, actual := range intents {
		if actual.CredentialRef != intent.CredentialRef {
			continue
		}
		if actual != intent {
			return credentialFailure("credential_journal_changed", "preserve the changed credential transaction record and inspect it before retrying")
		}
		if err := os.Remove(filepath.Join(directory, intent.CredentialRef+".json")); err != nil {
			return err
		}
		return syncCredentialDirectory(directory)
	}
	return nil
}

func (s ContextStore) recoverCredentialIntent(ctx context.Context, index *contextIndex, intent credentialIntent) error {
	for _, item := range index.Contexts {
		if item.CredentialRef == intent.CredentialRef && credentialStorageName(item.CredentialStorage) == intent.CredentialStorage {
			// A committed reference is owned by the context. Never remove its
			// secret, even when the provider is locked or its value has changed.
			if err := syncCredentialDirectory(s.Root); err != nil {
				return &CredentialError{Code: "context_durability_unverified", Action: "restore durable access to the saved context metadata before completing credential recovery", Saved: true}
			}
			if intent.CredentialStorage == "file" {
				if err := (fileCredentialVault{root: s.Root}).removeStage(intent.CredentialRef, intent.ID); err != nil {
					return err
				}
			}
			return s.forgetCredentialIntent(intent)
		}
	}
	vault, err := s.readVault(ctx, intent.CredentialStorage)
	if err != nil {
		return err
	}
	data, err := vault.Load(ctx, intent.CredentialRef)
	var status *CredentialError
	missing := errors.As(err, &status) && status.Code == "credential_not_found"
	if err != nil && !missing {
		return err
	}
	if !missing {
		var record credentialRecord
		digest := sha256.Sum256(data)
		if json.Unmarshal(data, &record) != nil || record.Version != 1 || record.TransactionID != intent.ID || record.Reference != intent.CredentialRef || hex.EncodeToString(digest[:]) != intent.PayloadSHA256 {
			return credentialFailure("credential_transaction_changed", "preserve the changed unreferenced credential and its transaction record; inspect the original store before retrying cleanup")
		}
		if err := vault.Delete(ctx, intent.CredentialRef); err != nil {
			return err
		}
	}
	if intent.CredentialStorage == "file" {
		if err := (fileCredentialVault{root: s.Root}).removeStage(intent.CredentialRef, intent.ID); err != nil {
			return err
		}
	}
	return s.forgetCredentialIntent(intent)
}

func (s ContextStore) recoverCredentialIntents(ctx context.Context, index *contextIndex, name string) error {
	if err := s.cleanCredentialIntentStages(); err != nil {
		return err
	}
	intents, err := s.readCredentialIntents()
	if err != nil {
		return err
	}
	for _, intent := range intents {
		if name != "" && intent.Context != name {
			continue
		}
		if err := s.recoverCredentialIntent(ctx, index, intent); err != nil {
			committed := false
			for _, item := range index.Contexts {
				if item.CredentialRef == intent.CredentialRef && credentialStorageName(item.CredentialStorage) == intent.CredentialStorage {
					committed = true
				}
			}
			return pendingCredentialError(err, intent, committed)
		}
	}
	if directory, err := s.transactionDirectory(false); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	} else if err := syncCredentialDirectory(directory); err != nil {
		return credentialFailure("credential_journal_unavailable", "restore durable access to the credential transaction directory before reporting cleanup complete")
	}
	return syncCredentialDirectory(s.Root)
}

// The context lock excludes an active intent writer. These stages contain only
// transaction metadata; credential stages have separate recorded ownership.
func (s ContextStore) cleanCredentialIntentStages() error {
	directory, err := s.transactionDirectory(false)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	file, err := os.Open(directory)
	if err != nil {
		return err
	}
	defer file.Close()
	entries, err := file.ReadDir(2*maxCredentialTransactions + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	if len(entries) > 2*maxCredentialTransactions {
		return credentialFailure("credential_journal_limit", "inspect the credential transaction directory before retrying cleanup")
	}
	changed := false
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), ".intent-") || !validCredentialReference(strings.TrimPrefix(entry.Name(), ".intent-")) {
			continue
		}
		info, err := os.Lstat(filepath.Join(directory, entry.Name()))
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
			return credentialFailure("credential_journal_invalid", "preserve the unexpected transaction stage and restore a private regular metadata stage before cleanup")
		}
		if err := os.Remove(filepath.Join(directory, entry.Name())); err != nil {
			return err
		}
		changed = true
	}
	if changed {
		return syncCredentialDirectory(directory)
	}
	return nil
}

func pendingCredentialError(err error, intent credentialIntent, saved bool) error {
	result := &CredentialError{Code: "credential_transaction_pending", Action: "preserve the saved connection; restore the recorded credential store, inspect agentlab context credentials-pending, then run agentlab context credentials-cleanup", Saved: saved, Transaction: &intent.CredentialTransaction}
	var original *CredentialError
	if errors.As(err, &original) {
		result.Code = original.Code
		result.Action = original.Action + "; inspect agentlab context credentials-pending and retry agentlab context credentials-cleanup"
		result.Saved = saved || original.Saved
	}
	return result
}

// recoverFailedCredentialWrite runs while the caller still owns the context
// lock. Re-read committed metadata, rather than trusting a modified candidate.
func (s ContextStore) recoverFailedCredentialWrite(intent credentialIntent) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	index, err := s.readIndex()
	if err != nil {
		return err
	}
	return s.recoverCredentialIntent(ctx, index, intent)
}

func (s ContextStore) PendingCredentials() ([]CredentialTransaction, error) {
	items := []CredentialTransaction{}
	err := s.locked(context.Background(), func(_ context.Context, _ *contextIndex) error {
		intents, err := s.readCredentialIntents()
		if err != nil {
			return err
		}
		for _, intent := range intents {
			items = append(items, intent.CredentialTransaction)
		}
		return nil
	})
	return items, err
}
