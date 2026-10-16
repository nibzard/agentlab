package daemon

import (
	"github.com/agentlab/agentlab/internal/models"
	"net/http"
)

// Only fixed diagnostic codes are persisted: guest-provided text could contain
// enrollment keys or credentials. Tailnet state is separate from VM readiness.
type sandboxBootstrapStatus struct {
	VMID           int    `json:"vmid"`
	RunnerReady    bool   `json:"runner_ready"`
	TailscaleState string `json:"tailscale_state"`
	ErrorCode      string `json:"error_code,omitempty"`
}

func (api *RunnerAPI) handleSandboxBootstrapStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, []string{http.MethodPost})
		return
	}
	if !api.remoteAllowed(r.RemoteAddr) {
		writeError(w, 403, "bootstrap status restricted to agent subnet")
		return
	}
	var req sandboxBootstrapStatus
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, 400, "invalid bootstrap status")
		return
	}
	if !verifySandboxSecret(r.Context(), api.store, r, req.VMID) {
		writeError(w, 403, "invalid or missing sandbox secret")
		return
	}
	switch req.TailscaleState {
	case "CONNECTED", "UNCONFIGURED", "FAILED", "PENDING":
	default:
		writeError(w, 400, "invalid tailscale state")
		return
	}
	switch req.ErrorCode {
	case "", "tailscale_enrollment_failed", "tailscale_cli_missing", "tailscale_pending":
	default:
		writeError(w, 400, "invalid bootstrap diagnostic code")
		return
	}
	sb, err := api.store.GetSandbox(r.Context(), req.VMID)
	if err != nil || sb.State == models.SandboxDestroyed {
		writeError(w, 404, "sandbox not found")
		return
	}
	if err = emitEvent(r.Context(), NewStoreEventRecorder(api.store), EventKindSandboxBootstrap, &req.VMID, nil, "guest bootstrap status", req); err != nil {
		writeError(w, 500, "failed to persist bootstrap status")
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
