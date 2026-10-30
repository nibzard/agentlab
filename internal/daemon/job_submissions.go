package daemon

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"time"

	"github.com/agentlab/agentlab/internal/auth"
	"github.com/agentlab/agentlab/internal/db"
	"github.com/agentlab/agentlab/internal/hostidentity"
)

var jobRequestKeyPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$`)

func submissionError(w http.ResponseWriter, status int, code, message, action string, retryable bool) {
	response := daemonErrorResponse(status, message)
	response.Code = code
	response.Action = action
	response.Retryable = &retryable
	writeJSON(w, status, response)
}

// prepareJobSubmission runs after normalization and before resolving mutable
// defaults or acquiring a workspace lease. The route has already authorized the
// request. Replays authorize the original job's current sandbox as well.
func (api *ControlAPI) prepareJobSubmission(w http.ResponseWriter, r *http.Request, request V1JobCreateRequest) (*db.JobSubmission, func(), bool) {
	key := r.Header.Get("Idempotency-Key")
	if key == "" {
		return nil, func() {}, false
	}
	if !jobRequestKeyPattern.MatchString(key) {
		submissionError(w, 400, "invalid_request_key", "invalid Idempotency-Key", "use 1 to 128 letters, digits, dots, colons, underscores, or hyphens", false)
		return nil, func() {}, true
	}

	owner, err := requestOwner(r)
	if err != nil {
		submissionError(w, 503, "server_identity_unavailable", "server identity is unavailable", "repair the server machine identity before retrying", false)
		return nil, func() {}, true
	}
	keyHash := sha256.Sum256([]byte(key))
	encodedKey := hex.EncodeToString(keyHash[:])
	normalized, _ := json.Marshal(request)
	inputHash := sha256.Sum256(normalized)
	release, err := api.store.LockJobSubmission(r.Context(), owner, encodedKey)
	if err != nil {
		submissionError(w, 409, "submission_busy", "job submission is already in progress", "retry the same key and inputs after the current request finishes", true)
		return nil, func() {}, true
	}
	done := func() { release() }
	var submission db.JobSubmission
	for i := 0; i < maxCreateJobIDIterations; i++ {
		candidate, createErr := newJobID()
		if createErr != nil {
			err = createErr
			break
		}
		now := api.now().UTC()
		submission, err = api.store.ReserveJobSubmission(r.Context(), db.JobSubmission{Owner: owner, KeyHash: encodedKey, InputHash: hex.EncodeToString(inputHash[:]), JobID: candidate, ExpiresAt: now.Add(30 * 24 * time.Hour)}, now)
		if !errors.Is(err, db.ErrSubmissionIDCollision) {
			break
		}
	}
	if err != nil {
		switch {
		case errors.Is(err, db.ErrSubmissionConflict):
			submissionError(w, 409, "idempotency_conflict", err.Error(), "reuse the original inputs or choose a new request key", false)
		case errors.Is(err, db.ErrSubmissionExpired):
			submissionError(w, 410, "idempotency_key_expired", err.Error(), "inspect the original job and its resources before choosing a new key", false)
		default:
			submissionError(w, 500, "submission_store_failed", "failed to record job submission", "retry the same key and inputs", true)
		}
		return nil, done, true
	}
	job, err := api.store.GetJob(r.Context(), submission.JobID)
	if err == nil {
		if api.authorize(w, r, permJobCreate, func() int { return api.jobSandboxVMID(r.Context(), job.ID) }, false) {
			w.Header().Set("Idempotency-Replayed", "true")
			response := jobToV1(job)
			if cancellation, loadErr := api.store.GetJobCancellation(r.Context(), job.ID); loadErr == nil {
				response.Cancellation = &cancellation
				if cancellation.State == "cancelled" {
					response.Status = "FAILED"
				}
			} else if !errors.Is(loadErr, sql.ErrNoRows) {
				writeError(w, 500, "failed to load job cancellation")
				return nil, done, true
			}
			writeJSON(w, http.StatusOK, response)
		}
		return &submission, done, true
	}
	if !errors.Is(err, sql.ErrNoRows) {
		submissionError(w, 500, "submission_store_failed", "failed to load the original job", "retry the same key and inputs", true)
		return nil, done, true
	}
	if submission.Committed {
		submissionError(w, 410, "submission_job_removed", "the original job record was removed", "inspect retained resources before choosing a new request key", false)
		return nil, done, true
	}
	return &submission, done, false
}

// requestOwner binds durable requests and uploaded source to the acting identity
// and this server. Credential renewal can preserve the same signing identity.
func requestOwner(r *http.Request) (string, error) {
	return requestOwnerContext(r.Context())
}
func requestOwnerContext(ctx context.Context) (string, error) {
	server, err := hostidentity.Local()
	if err != nil {
		return "", err
	}
	actor := []string{server, "local_trusted", "", ""}
	if id := auth.FromContext(ctx); id != nil {
		actor[1] = id.Method
		actor[2] = id.Fingerprint
		actor[3] = id.Subject
		if actor[2] == "" && id.Token != nil {
			actor[2] = id.Token.Claims.Issuer
		}
	}
	ownerJSON, _ := json.Marshal(actor)
	ownerHash := sha256.Sum256(ownerJSON)
	return hex.EncodeToString(ownerHash[:]), nil
}
