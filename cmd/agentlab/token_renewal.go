package main

import (
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
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/agentlab/agentlab/internal/auth"
	"github.com/agentlab/agentlab/internal/connection"
	"github.com/agentlab/agentlab/internal/protocol"
	"golang.org/x/crypto/ssh"
)

type credentialRenewal struct {
	CreatedAt       string                 `json:"created_at"`
	UpdatedAt       string                 `json:"updated_at"`
	Version         int                    `json:"version"`
	Context         string                 `json:"context"`
	KeyHash         string                 `json:"request_key_hash"`
	Parent          connection.Context     `json:"parent_context"`
	Stage           connection.Context     `json:"staged_context"`
	PublicKey       string                 `json:"signing_public_key"`
	ParentID        string                 `json:"parent_revocation_id"`
	CandidateID     string                 `json:"candidate_revocation_id"`
	CandidateSHA256 string                 `json:"candidate_sha256"`
	TTL             string                 `json:"ttl"`
	State           string                 `json:"state"`
	Cleanup         string                 `json:"cleanup_status"`
	Receipt         *protocol.TokenRenewal `json:"receipt,omitempty"`
}
type renewalRequirement struct {
	Cause  error
	Record credentialRenewal
}

func (e *renewalRequirement) Error() string {
	return "credential renewal requires inspection; resume the same request with its staged credential"
}
func (e *renewalRequirement) Unwrap() error { return e.Cause }
func (e *renewalRequirement) ErrorMetadata() map[string]any {
	code, action := "credential_renewal_pending", "retry token renew with the same context and request ID; preserve the staged context if its binding changed"
	var blocked *blockedRequirement
	if errors.As(e.Cause, &blocked) {
		code, action = blocked.Code, blocked.Action
	}
	var response apiResponseError
	if errors.As(e.Cause, &response) {
		if response.Status == 404 && response.Code != "renewal_not_prepared" {
			code = "unsupported_feature"
			action = "use a server with token_renewal support; preserve the staged credential"
		}
		switch response.Code {
		case "renewal_credential_revoked", "v1/auth/token_revoked":
			code = response.Code
			action = "inspect revocation and obtain explicit operator enrollment; preserve the staged credential for inspection"
		case "server_identity_mismatch":
			code = response.Code
			action = "verify the original context and server identity before resuming"
		case "renewal_conflict":
			code = response.Code
			action = "inspect the original renewal candidate instead of minting another one"
		case "renewal_store_unavailable":
			code = response.Code
			action = "restore renewal storage, then retry the same request"
		}
	}
	var storage *connection.CredentialError
	if errors.As(e.Cause, &storage) {
		code, action = storage.Code, storage.Action
	}
	return map[string]any{"renewal": e.Record, "state": "blocked", "code": code, "action": action}
}

func credentialHash(value string) string {
	hash := sha256.Sum256([]byte(value))
	return hex.EncodeToString(hash[:])
}
func writeRenewal(path string, record *credentialRenewal) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if record.CreatedAt == "" {
		record.CreatedAt = now
	}
	record.UpdatedAt = now
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".renewal-")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	_, writeErr := file.Write(data)
	syncErr := file.Sync()
	closeErr := file.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		return err
	}
	if err := os.Rename(file.Name(), path); err != nil {
		return err
	}
	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
func loadRenewal(path string) (credentialRenewal, error) {
	var record credentialRenewal
	if err := privateSetupPath(path, false); err != nil {
		return record, err
	}
	file, err := os.Open(path)
	if err != nil {
		return record, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 65537))
	if err != nil || len(data) > 65536 {
		return record, fmt.Errorf("invalid renewal journal")
	}
	if json.Unmarshal(data, &record) != nil || record.Version != 1 || record.Context == "" || record.Parent.ServerIdentity == "" || !setupDigest.MatchString(record.CandidateSHA256) {
		return record, fmt.Errorf("invalid renewal journal")
	}
	return record, nil
}
func sameRenewalClaims(parent, candidate *auth.Token) bool {
	return parent.Claims.Issuer == candidate.Claims.Issuer && parent.Claims.Subject == candidate.Claims.Subject && slices.Equal(parent.Claims.Commands, candidate.Claims.Commands) && slices.Equal(parent.Claims.Scope, candidate.Claims.Scope)
}
func renewalAPI(cfg connection.Settings, timeout time.Duration) *apiClient {
	return newAPIClient(clientOptions{ServerIdentity: cfg.ServerIdentity, Endpoint: cfg.Endpoint, Token: cfg.Token, AllowInsecureHTTP: cfg.AllowInsecureHTTP}, timeout)
}
func decodeRenewal(data []byte, record credentialRenewal) (protocol.TokenRenewal, error) {
	var receipt protocol.TokenRenewal
	if json.Unmarshal(data, &receipt) != nil || receipt.ParentID != record.ParentID || receipt.CandidateID != record.CandidateID || receipt.CandidateSHA256 != record.CandidateSHA256 || receipt.ServerIdentity != record.Parent.ServerIdentity {
		return receipt, fmt.Errorf("renewal receipt binding changed")
	}
	return receipt, nil
}
func runTokenRenew(ctx context.Context, args []string, base commonFlags) (resultErr error) {
	fs := newFlagSet("token renew")
	base.bind(fs)
	help := bindHelpFlag(fs)
	name := fs.String("context", base.contextName, "named context to renew")
	requestKey := fs.String("request-id", "", "stable renewal request key")
	key := fs.String("key", "", "operator signing key, needed for a new renewal")
	ttlText := fs.String("ttl", "1h", "replacement lifetime, from 1m to 24h")
	usage := func() {
		fmt.Fprintln(os.Stdout, "Usage: agentlab token renew --context NAME --request-id KEY [--key PATH] [--ttl 1h]")
	}
	if err := parseFlags(fs, args, usage, help, base.jsonOutput); err != nil {
		return err
	}
	if fs.NArg() != 0 || !setupRequestKey.MatchString(*requestKey) {
		return fmt.Errorf("provide --context and a stable --request-id")
	}
	if err := connection.ValidateContextName(*name); err != nil {
		return err
	}
	if renewalContextOverrides(base) {
		return &blockedRequirement{Code: "context_binding_required", Requirement: "named_context", Action: "renew the named context without endpoint, token, or socket overrides"}
	}
	ttl, err := enrollmentTTL(*ttlText)
	if err != nil {
		return err
	}
	*ttlText = ttl.String()
	store, err := contextStore()
	if err != nil {
		return err
	}
	directory := filepath.Join(store.Root, "credential-renewals")
	if err := os.MkdirAll(directory, 0700); err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(directory, credentialHash(*name)+".lock"), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return &blockedRequirement{Code: "credential_renewal_busy", Requirement: "renewal_lock", Action: "observe the existing renewal before retrying this context"}
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	prefix := credentialHash(*name) + "-"
	journal := filepath.Join(directory, prefix+credentialHash(*requestKey)+".json")
	record, loadErr := loadRenewal(journal)
	if loadErr != nil && !errors.Is(loadErr, os.ErrNotExist) {
		return loadErr
	}
	if loadErr == nil && (record.Context != *name || record.KeyHash != credentialHash(*requestKey) || record.TTL != *ttlText) {
		return &renewalRequirement{Cause: fmt.Errorf("renewal key belongs to other inputs"), Record: record}
	}
	if errors.Is(loadErr, os.ErrNotExist) {
		entries, err := os.ReadDir(directory)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), prefix) && strings.HasSuffix(entry.Name(), ".json") {
				pending, err := loadRenewal(filepath.Join(directory, entry.Name()))
				if err != nil {
					return err
				}
				if pending.State != "published" || pending.Cleanup != "verified" {
					return &renewalRequirement{Cause: fmt.Errorf("another renewal is pending"), Record: pending}
				}
			}
		}
		current, _, found, err := store.LookupContext(ctx, *name, false)
		if err != nil {
			return err
		}
		if !found {
			return fmt.Errorf("context is unavailable")
		}
		parentCfg, _, _, err := store.ResolveContext(ctx, *name)
		if err != nil {
			return err
		}
		if parentCfg.Endpoint == "" || parentCfg.ServerIdentity == "" {
			return &blockedRequirement{Code: "server_identity_required", Requirement: "verified_remote_context", Action: "verify and save the named remote context before renewal"}
		}
		path, err := resolveKeyPath(*key)
		if err != nil {
			return &blockedRequirement{Code: "signing_authority_required", Requirement: "operator_signing_key", Action: "provide the operator key for a new renewal; pending renewals reuse their protected staged credential"}
		}
		signer, err := loadTokenSigner(path)
		if err != nil {
			return &blockedRequirement{Code: "signing_authority_required", Requirement: "operator_signing_key", Action: "restore non-interactive access to the operator signing key"}
		}
		parent, err := auth.VerifyTokenSignature(parentCfg.Token, signer.PublicKey())
		if err != nil || parent.Claims.TokenID == "" || parent.Claims.ExpiresAt <= time.Now().Unix() || parent.Claims.NotBefore > time.Now().Unix() || !parent.IsCommandAllowed("token.renew") {
			return &blockedRequirement{Code: "credential_renewal_not_authorized", Requirement: "renewable_signed_credential", Action: "use a valid finite SSH credential with token.renew and its matching operator key"}
		}
		parentID := auth.RevocationID(parent.Claims.Issuer, parent.Claims.TokenID)
		stageName := "renewal-" + credentialHash(*name + "\x00" + parentCfg.ServerIdentity + "\x00" + parentID + "\x00" + *requestKey + "\x00" + *ttlText)[:24]
		stageCfg, _, stageExists, err := store.ResolveContext(ctx, stageName)
		if err != nil { // Resolve reports an absent named context; metadata lookup distinguishes it from vault failure.
			_, _, present, lookupErr := store.LookupContext(ctx, stageName, true)
			if lookupErr != nil || present {
				return err
			}
			stageExists = false
		}
		if !stageExists {
			raw, err := auth.CreateToken(signer, auth.TokenCreateRequest{Commands: parent.Claims.Commands, Scope: parent.Claims.Scope, Subject: parent.Claims.Subject, TTL: ttl})
			if err != nil {
				return err
			}
			stageCfg = parentCfg
			stageCfg.Token = raw
			if err := store.PutContextIfMatch(ctx, stageName, nil, stageCfg); err != nil {
				return err
			}
		}
		candidate, err := auth.VerifyTokenSignature(stageCfg.Token, signer.PublicKey())
		if err != nil || !sameRenewalClaims(parent, candidate) || stageCfg.Endpoint != parentCfg.Endpoint || stageCfg.ServerIdentity != parentCfg.ServerIdentity {
			return fmt.Errorf("staged renewal conflicts with the original credential")
		}
		stage, _, _, err := store.LookupContext(ctx, stageName, false)
		if err != nil {
			return err
		}
		record = credentialRenewal{CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), Version: 1, Context: *name, KeyHash: credentialHash(*requestKey), Parent: current, Stage: stage, PublicKey: string(ssh.MarshalAuthorizedKey(signer.PublicKey())), ParentID: parentID, CandidateID: auth.RevocationID(candidate.Claims.Issuer, candidate.Claims.TokenID), CandidateSHA256: credentialHash(stageCfg.Token), TTL: *ttlText, State: "staged", Cleanup: "pending"}
		if err := writeRenewal(journal, &record); err != nil {
			return err
		}
	}
	defer func() {
		if resultErr != nil {
			resultErr = &renewalRequirement{Cause: resultErr, Record: record}
		}
	}()
	// A published receipt is idempotent, even after the token expires. It does not
	// claim the saved credential still authenticates.
	if record.State == "published" && record.Cleanup == "verified" {
		return printRenewal(record, base.jsonOutput)
	}
	if record.State == "published" {
		cfg, _, _, err := store.ResolveContext(ctx, record.Context)
		if err != nil {
			return err
		}
		if cfg.Endpoint != record.Parent.Endpoint || cfg.ServerIdentity != record.Parent.ServerIdentity || credentialHash(cfg.Token) != record.CandidateSHA256 {
			return fmt.Errorf("published context changed; inspect the retained staging credential")
		}
		if err := store.RemoveContextIfMatch(ctx, record.Stage.Name, record.Stage); err != nil {
			return err
		}
		record.Cleanup = "verified"
		if err := writeRenewal(journal, &record); err != nil {
			return err
		}
		return printRenewal(record, base.jsonOutput)
	}
	var candidateCfg connection.Settings
	stageCfg, _, stageFound, stageErr := store.ResolveContext(ctx, record.Stage.Name)
	if stageErr == nil && stageFound {
		candidateCfg = stageCfg
	} else {
		currentCfg, _, _, err := store.ResolveContext(ctx, record.Context)
		if err != nil {
			return err
		}
		if currentCfg.Endpoint != record.Parent.Endpoint || currentCfg.ServerIdentity != record.Parent.ServerIdentity {
			return fmt.Errorf("destination context changed; preserve the staged credential")
		}
		if credentialHash(currentCfg.Token) != record.CandidateSHA256 {
			return fmt.Errorf("staged renewal credential is unavailable")
		}
		candidateCfg = currentCfg
	}
	public, _, _, _, err := ssh.ParseAuthorizedKey([]byte(record.PublicKey))
	if err != nil {
		return fmt.Errorf("renewal signer metadata is invalid")
	}
	candidate, err := auth.VerifyTokenSignature(candidateCfg.Token, public)
	if err != nil || credentialHash(candidateCfg.Token) != record.CandidateSHA256 || auth.RevocationID(candidate.Claims.Issuer, candidate.Claims.TokenID) != record.CandidateID || candidateCfg.Endpoint != record.Parent.Endpoint || candidateCfg.ServerIdentity != record.Parent.ServerIdentity {
		return fmt.Errorf("renewal credential binding changed")
	}
	if candidate.Claims.ExpiresAt <= time.Now().Unix() {
		return &blockedRequirement{Code: "renewal_credential_expired", Requirement: "new_enrollment", Action: "enroll a new credential with explicit operator authority; preserve the expired renewal receipt for inspection"}
	}
	client := renewalAPI(candidateCfg, base.timeout)
	commit := func() (protocol.TokenRenewal, error) {
		data, err := client.doJSON(ctx, "POST", "/v1/token-renewals/commit", protocol.TokenRenewalCommit{ParentID: record.ParentID, ServerIdentity: record.Parent.ServerIdentity})
		if err != nil {
			return protocol.TokenRenewal{}, err
		}
		return decodeRenewal(data, record)
	}
	receipt, err := commit()
	if err != nil {
		var response apiResponseError
		if !errors.As(err, &response) || response.Code != "renewal_not_prepared" {
			return err
		}
		parentCfg, _, _, err := store.ResolveContext(ctx, record.Context)
		if err != nil {
			return err
		}
		parent, err := auth.VerifyTokenSignature(parentCfg.Token, public)
		if err != nil || auth.RevocationID(parent.Claims.Issuer, parent.Claims.TokenID) != record.ParentID || !sameRenewalClaims(parent, candidate) || parentCfg.Endpoint != record.Parent.Endpoint || parentCfg.ServerIdentity != record.Parent.ServerIdentity {
			return fmt.Errorf("parent context binding changed before preparation")
		}
		data, err := renewalAPI(parentCfg, base.timeout).doJSON(ctx, "POST", "/v1/token-renewals/prepare", protocol.TokenRenewalPrepare{CandidateToken: candidateCfg.Token, ServerIdentity: record.Parent.ServerIdentity})
		if err != nil {
			return err
		}
		if _, err := decodeRenewal(data, record); err != nil {
			return err
		}
		receipt, err = commit()
		if err != nil {
			return err
		}
	}
	if receipt.Issuer != candidate.Claims.Issuer || receipt.Subject != candidate.Claims.Subject || !slices.Equal(receipt.Commands, candidate.Claims.Commands) || !slices.Equal(receipt.Scope, candidate.Claims.Scope) {
		return fmt.Errorf("renewal receipt changed credential permissions")
	}
	if receipt.State != "committed" {
		return fmt.Errorf("renewal has no committed server receipt")
	}
	record.Receipt = &receipt
	record.State = "committed"
	if err := writeRenewal(journal, &record); err != nil {
		return err
	}
	current, _, _, err := store.LookupContext(ctx, record.Context, false)
	if err != nil {
		return err
	}
	currentCfg, _, _, err := store.ResolveContext(ctx, record.Context)
	if err != nil {
		return err
	}
	if credentialHash(currentCfg.Token) != record.CandidateSHA256 {
		if !reflect.DeepEqual(current, record.Parent) {
			return fmt.Errorf("destination context changed; preserve the staged credential")
		}
		if err := store.PutContextIfMatch(ctx, record.Context, &record.Parent, candidateCfg); err != nil {
			return err
		}
	}
	record.State = "published"
	if err := writeRenewal(journal, &record); err != nil {
		return err
	}
	if err := store.RemoveContextIfMatch(ctx, record.Stage.Name, record.Stage); err != nil {
		return err
	}
	record.Cleanup = "verified"
	if err := writeRenewal(journal, &record); err != nil {
		return err
	}
	return printRenewal(record, base.jsonOutput)
}
func printRenewal(record credentialRenewal, jsonOutput bool) error {
	if jsonOutput {
		return json.NewEncoder(os.Stdout).Encode(record)
	}
	fmt.Fprintf(os.Stdout, "Renewed context: %s\nState: %s\nCleanup: %s\n", record.Context, record.State, record.Cleanup)
	return nil
}
