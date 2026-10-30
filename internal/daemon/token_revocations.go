package daemon

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/agentlab/agentlab/internal/auth"
	"github.com/agentlab/agentlab/internal/hostidentity"
	"github.com/agentlab/agentlab/internal/protocol"
)

func (api *ControlAPI) handleTokenRevocations(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, []string{http.MethodPost})
		return
	}
	if !api.authorize(w, r, permTokenRevoke, nil, true) {
		return
	}
	var input protocol.TokenRevocationRequest
	if r.Body == nil {
		repositoryError(w, 400, "invalid_revocation_target", "provide the issuer and revocation_id from secure token inspection", false)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	defer r.Body.Close()
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	decodeErr := decoder.Decode(&input)
	var extra any
	if decodeErr != nil || decoder.Decode(&extra) != io.EOF || !auth.ValidRevocationTarget(input.Issuer, input.RevocationID) {
		submissionError(w, 400, "invalid_revocation_target", "invalid token revocation identifiers", "use the issuer and revocation_id from secure token inspection", false)
		return
	}
	identity, err := hostidentity.Local()
	if err != nil {
		repositoryError(w, 503, "server_identity_unavailable", "restore the server identity before revoking a token", true)
		return
	}
	if input.ServerIdentity != identity {
		repositoryError(w, 409, "server_identity_mismatch", "verify the selected context and server identity before retrying revocation", false)
		return
	}
	actor := "local_trusted"
	if caller := auth.FromContext(r.Context()); caller != nil {
		actor = caller.Method + ":" + caller.Fingerprint
	}
	record, err := api.store.RevokeToken(r.Context(), protocol.TokenRevocation{Issuer: input.Issuer, RevocationID: input.RevocationID, Revoked: true, Actor: actor, ServerIdentity: identity, RevokedAt: api.now().UTC().Format(time.RFC3339Nano)})
	if err != nil {
		repositoryError(w, 503, "revocation_store_unavailable", "restore credential status storage and retry the same revocation", true)
		return
	}
	writeJSON(w, 200, record)
}

func (api *ControlAPI) handleTokenRevocation(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, []string{http.MethodGet})
		return
	}
	if !api.authorize(w, r, permTokenRead, nil, true) {
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/v1/token-revocations/")
	issuer := r.URL.Query().Get("issuer")
	if !auth.ValidRevocationTarget(issuer, id) {
		repositoryError(w, 400, "invalid_revocation_target", "provide the issuer and revocation_id from secure token inspection", false)
		return
	}
	record, err := api.store.GetTokenRevocation(r.Context(), issuer, id)
	if errors.Is(err, sql.ErrNoRows) {
		identity, identityErr := hostidentity.Local()
		if identityErr != nil {
			repositoryError(w, 503, "server_identity_unavailable", "restore the server identity before checking credential status", true)
			return
		}
		writeJSON(w, 200, protocol.TokenRevocation{Issuer: issuer, RevocationID: id, Revoked: false, ServerIdentity: identity})
		return
	}
	if err != nil {
		repositoryError(w, 503, "revocation_store_unavailable", "restore credential status storage before retrying", true)
		return
	}
	writeJSON(w, 200, record)
}
