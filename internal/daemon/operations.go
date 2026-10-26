package daemon

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/agentlab/agentlab/internal/auth"
	"github.com/agentlab/agentlab/internal/db"
)

// operationRuns is scoped by ControlAPI instance. Persisted unfinished work from
// another process is reported as unknown, never reallocated on key replay.
var operationRuns sync.Map

func operationCaller(r *http.Request) string {
	if id := auth.FromContext(r.Context()); id != nil {
		return id.Fingerprint + ":" + id.Subject
	}
	return "local"
}
func newOperationID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}

func (api *ControlAPI) handleSandboxCreateOperation(w http.ResponseWriter, r *http.Request) {
	var req V1SandboxCreateRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if allowed := sandboxScopeFilter(r); allowed != nil && (req.VMID == nil || !allowed(*req.VMID)) {
		writeError(w, 403, "scoped asynchronous creation requires an allowed explicit vmid")
		return
	}
	payload, err := json.Marshal(req)
	if err != nil {
		writeError(w, 400, "invalid create request")
		return
	}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if len(key) > 256 {
		writeError(w, 400, "idempotency key too long")
		return
	}
	if key == "" {
		key = newOperationID()
	}
	id := newOperationID()
	runKey := struct {
		api *ControlAPI
		id  string
	}{api, id}
	// Publish liveness before the reservation becomes visible to concurrent
	// retries. Otherwise a replay could briefly misreport live work as unknown.
	operationRuns.Store(runKey, true)
	cleanupReservation := true
	defer func() {
		if cleanupReservation {
			operationRuns.Delete(runKey)
		}
	}()
	op, created, err := api.store.BeginOperation(r.Context(), id, operationCaller(r), key, string(payload))
	if err != nil {
		writeError(w, 500, "failed to reserve operation", err)
		return
	}
	if op.Payload != string(payload) {
		writeError(w, 409, "idempotency key already used with a different request")
		return
	}
	if !created {
		api.writeOperation(w, r, op)
		return
	}
	cleanupReservation = false
	runner := api.runner
	if runner == nil {
		runner = DetachedRunner()
	}
	request := r.Clone(context.WithoutCancel(r.Context()))
	if !runner.Go("sandbox-create:"+op.ID, func(ctx context.Context) {
		defer operationRuns.Delete(runKey)
		// Preserve caller identity while binding work cancellation to daemon shutdown.
		if id := auth.FromContext(request.Context()); id != nil {
			ctx = auth.WithIdentity(ctx, id)
		}
		ctx = db.WithOperation(ctx, op.ID)
		request = request.WithContext(ctx)
		request.Body = io.NopCloser(bytes.NewReader(payload))
		recorder := httptest.NewRecorder()
		api.handleSandboxCreate(recorder, request)
		state := "succeeded"
		if recorder.Code >= 400 {
			state = "failed"
		}
		// Completion persistence gets a short independent lifetime during shutdown.
		completionCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if err := api.store.FinishOperation(completionCtx, op.ID, state, recorder.Code, recorder.Body.String()); err != nil && api.logger != nil {
			api.logger.Printf("persist creation operation %s completion: %v", op.ID, err)
		}
	}) {
		operationRuns.Delete(runKey)
		_ = api.store.FinishOperation(r.Context(), op.ID, "rejected", 503, "daemon is shutting down")
		writeError(w, 503, "daemon is shutting down")
		return
	}
	w.Header().Set("Idempotency-Key", key)
	api.writeOperation(w, r, op)
}

type V1OperationResponse struct {
	ID          string          `json:"id"`
	State       string          `json:"state"`
	VMID        *int            `json:"vmid,omitempty"`
	HTTPStatus  int             `json:"http_status,omitempty"`
	StatusURL   string          `json:"status_url"`
	ResourceURL string          `json:"resource_url,omitempty"`
	Result      json.RawMessage `json:"result,omitempty"`
}

func (api *ControlAPI) writeOperation(w http.ResponseWriter, r *http.Request, op db.Operation) {
	if current, err := api.store.GetOperation(r.Context(), op.ID); err == nil {
		op = current
	}
	if op.Caller != operationCaller(r) {
		writeError(w, 404, "operation not found")
		return
	}
	if !api.authorize(w, r, permSandboxCreate, func() int {
		if op.VMID != nil {
			return *op.VMID
		}
		return 0
	}, false) {
		return
	}
	var requested V1SandboxCreateRequest
	_ = json.Unmarshal([]byte(op.Payload), &requested)
	target := op.VMID
	if target == nil {
		target = requested.VMID
	}
	if allowed := sandboxScopeFilter(r); allowed != nil && (target == nil || !allowed(*target)) {
		writeError(w, 403, "operation is outside token scope")
		return
	}
	if op.State == "accepted" {
		if _, running := operationRuns.Load(struct {
			api *ControlAPI
			id  string
		}{api, op.ID}); !running {
			op.State = "unknown"
		}
	}
	response := V1OperationResponse{ID: op.ID, State: op.State, VMID: op.VMID, HTTPStatus: op.HTTPStatus, StatusURL: "/v1/operations/" + op.ID}
	if op.VMID != nil {
		response.ResourceURL = "/v1/sandboxes/" + strconv.Itoa(*op.VMID)
	}
	if json.Valid([]byte(op.Result)) {
		response.Result = json.RawMessage(op.Result)
	}
	w.Header().Set("Location", response.StatusURL)
	status := http.StatusOK
	if op.State == "accepted" {
		status = http.StatusAccepted
	}
	writeJSON(w, status, response)
}
func (api *ControlAPI) handleOperation(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, []string{http.MethodGet})
		return
	}
	if !api.authorize(w, r, permSandboxCreate, nil, false) {
		return
	}
	op, err := api.store.GetOperation(r.Context(), strings.TrimPrefix(r.URL.Path, "/v1/operations/"))
	if err != nil {
		writeError(w, 404, "operation not found")
		return
	}
	api.writeOperation(w, r, op)
}
