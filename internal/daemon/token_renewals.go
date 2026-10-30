package daemon

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"time"

	"github.com/agentlab/agentlab/internal/auth"
	"github.com/agentlab/agentlab/internal/db"
	"github.com/agentlab/agentlab/internal/hostidentity"
	"github.com/agentlab/agentlab/internal/protocol"
	"golang.org/x/crypto/ssh"
)

func renewalDigest(raw string) string {
	digest := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(digest[:])
}
func (api *ControlAPI) renewalCaller(w http.ResponseWriter, r *http.Request) (*auth.Token, bool) {
	if !api.authorize(w, r, permTokenRenew, nil, false) {
		return nil, false
	}
	caller := auth.FromContext(r.Context())
	if caller == nil || caller.Token == nil || caller.Token.Claims.TokenID == "" || caller.Token.Claims.ExpiresAt <= 0 {
		submissionError(w, 403, "signed_credential_required", "renewal requires a finite SSH-signed credential", "enroll an authorized SSH-signed task credential", false)
		return nil, false
	}
	return caller.Token, true
}
func renewalBody(w http.ResponseWriter, r *http.Request, value any) bool {
	if r.Body == nil {
		submissionError(w, 400, "invalid_renewal_request", "invalid credential renewal request", "provide the renewal fields without credentials in arguments", false)
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 24<<10)
	defer r.Body.Close()
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var extra any
	if decoder.Decode(value) != nil || decoder.Decode(&extra) != io.EOF {
		submissionError(w, 400, "invalid_renewal_request", "invalid credential renewal request", "provide a bounded renewal request", false)
		return false
	}
	return true
}
func renewalIdentity(w http.ResponseWriter, expected string) (string, bool) {
	identity, err := hostidentity.Local()
	if err != nil {
		submissionError(w, 503, "server_identity_unavailable", "server identity unavailable", "restore the server identity before renewal", true)
		return "", false
	}
	if identity != expected {
		submissionError(w, 409, "server_identity_mismatch", "credential renewal targets another server", "verify the bound context before renewal", false)
		return "", false
	}
	return identity, true
}
func renewalFailure(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, db.ErrRenewalConflict):
		submissionError(w, 409, "renewal_conflict", "another credential owns this renewal", "inspect the original staged credential; do not mint another candidate", false)
	case errors.Is(err, db.ErrRenewalRevoked):
		submissionError(w, 401, "renewal_credential_revoked", "a renewal credential was revoked", "inspect credential revocation before further enrollment", false)
	case errors.Is(err, sql.ErrNoRows):
		submissionError(w, 404, "renewal_not_prepared", "credential renewal is not prepared", "prepare this same candidate with its parent credential", true)
	default:
		submissionError(w, 503, "renewal_store_unavailable", "credential renewal storage unavailable", "retry with the same staged candidate after restoring storage", true)
	}
}
func (api *ControlAPI) handleTokenRenewalPrepare(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		writeMethodNotAllowed(w, []string{"POST"})
		return
	}
	parent, ok := api.renewalCaller(w, r)
	if !ok {
		return
	}
	var input protocol.TokenRenewalPrepare
	if !renewalBody(w, r, &input) {
		return
	}
	identity, ok := renewalIdentity(w, input.ServerIdentity)
	if !ok {
		return
	}
	keys, err := auth.ParseAuthorizedKeys(ssh.MarshalAuthorizedKey(parent.Signer))
	if err != nil {
		renewalFailure(w, err)
		return
	}
	candidate, err := auth.ParseToken(input.CandidateToken, keys)
	if err != nil || len(input.CandidateToken) > 16<<10 || candidate.Claims.TokenID == "" || candidate.Claims.TokenID == parent.Claims.TokenID || candidate.Claims.Issuer != parent.Claims.Issuer || candidate.Claims.Subject != parent.Claims.Subject || !slices.Equal(candidate.Claims.Commands, parent.Claims.Commands) || !slices.Equal(candidate.Claims.Scope, parent.Claims.Scope) || candidate.Claims.NotBefore <= 0 || candidate.Claims.ExpiresAt-candidate.Claims.NotBefore > int64((24*time.Hour)/time.Second) || candidate.Claims.NotBefore < parent.Claims.NotBefore || candidate.Claims.ExpiresAt <= time.Now().Unix() || candidate.Claims.ExpiresAt > time.Now().Add(24*time.Hour).Unix() {
		submissionError(w, 400, "invalid_renewal_candidate", "candidate does not preserve the admitted credential", "sign a fresh candidate with the same issuer, subject, commands, and scope; use at most 24 hours", false)
		return
	}
	record, err := api.store.PrepareTokenRenewal(r.Context(), protocol.TokenRenewal{Issuer: parent.Claims.Issuer, ParentID: auth.RevocationID(parent.Claims.Issuer, parent.Claims.TokenID), CandidateID: auth.RevocationID(candidate.Claims.Issuer, candidate.Claims.TokenID), CandidateSHA256: renewalDigest(candidate.Raw), ServerIdentity: identity, Subject: candidate.Claims.Subject, Commands: candidate.Claims.Commands, Scope: candidate.Claims.Scope, CandidateExpiresAt: candidate.Claims.ExpiresAt})
	if err != nil {
		renewalFailure(w, err)
		return
	}
	writeJSON(w, 200, record)
}
func (api *ControlAPI) handleTokenRenewalCommit(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		writeMethodNotAllowed(w, []string{"POST"})
		return
	}
	candidate, ok := api.renewalCaller(w, r)
	if !ok {
		return
	}
	var input protocol.TokenRenewalCommit
	if !renewalBody(w, r, &input) {
		return
	}
	identity, ok := renewalIdentity(w, input.ServerIdentity)
	if !ok {
		return
	}
	if !auth.ValidRevocationTarget(candidate.Claims.Issuer, input.ParentID) {
		submissionError(w, 400, "invalid_renewal_request", "invalid parent credential identifier", "use the parent identifier in the staged renewal", false)
		return
	}
	record, err := api.store.CommitTokenRenewal(r.Context(), candidate.Claims.Issuer, input.ParentID, auth.RevocationID(candidate.Claims.Issuer, candidate.Claims.TokenID), renewalDigest(candidate.Raw), identity)
	if err != nil {
		renewalFailure(w, err)
		return
	}
	writeJSON(w, 200, record)
}
