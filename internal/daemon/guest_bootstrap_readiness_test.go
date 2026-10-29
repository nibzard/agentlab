package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/agentlab/agentlab/internal/db"
	"github.com/agentlab/agentlab/internal/models"
	"github.com/agentlab/agentlab/internal/proxmox"
)

func TestStandaloneBootstrapAndAuthenticatedDiagnostics(t *testing.T) {
	api, token, id := mintTestEnv(t, "tailscale:\n  authkey: standalone-enrollment-fixture\n")
	if _, err := api.store.DB.Exec(`DELETE FROM jobs WHERE sandbox_vmid=?`, id); err != nil {
		t.Fatal(err)
	}
	rr := doMintFetch(t, api, token, id)
	if rr.Code != 200 {
		t.Fatal(rr.Code, rr.Body.String())
	}
	var payload V1BootstrapFetchResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Sandbox == nil || payload.Sandbox.VMID != id || payload.Job.ID != "" || payload.SandboxSecret == "" || payload.Artifact != nil {
		t.Fatalf("invalid standalone payload: identity/job/artifact shape")
	}
	if payload.Tailscale == nil || payload.Tailscale.AuthKey != "standalone-enrollment-fixture" {
		t.Fatal("standalone sandbox did not receive enrollment credentials")
	}
	hash, _ := api.store.GetSandboxSecretHash(context.Background(), id)
	if again := doMintFetch(t, api, token, id); again.Code != 403 {
		t.Fatal("single-use token reused", again.Code)
	}
	after, _ := api.store.GetSandboxSecretHash(context.Background(), id)
	if hash != after {
		t.Fatal("losing fetch rotated delivered sandbox secret")
	}
	reporter := NewRunnerAPI(nil, api.store, api.agentSubnet)
	send := func(secret, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/v1/sandbox/bootstrap-status", strings.NewReader(body))
		req.RemoteAddr = "10.77.0.55:1000"
		req.Header.Set(sandboxSecretHeader, secret)
		out := httptest.NewRecorder()
		reporter.handleSandboxBootstrapStatus(out, req)
		return out
	}
	body := `{"vmid":5101,"runner_ready":true,"tailscale_state":"FAILED","error_code":"tailscale_enrollment_failed"}`
	if got := send("wrong", body); got.Code != 403 {
		t.Fatal(got.Code)
	}
	if got := send(payload.SandboxSecret, body); got.Code != 200 {
		t.Fatal(got.Code, got.Body.String())
	}
	if got := send(payload.SandboxSecret, strings.Replace(body, "tailscale_enrollment_failed", "tskey-auth-should-not-persist", 1)); got.Code != 400 {
		t.Fatal("untrusted diagnostic accepted")
	}
	events, err := api.store.ListEventsBySandboxAll(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	projection := NewEventProjection()
	projection.Replay(events)
	health := projection.SandboxHealth[id]
	if !health.RunnerReady || health.TailscaleState != "FAILED" || health.BootstrapError != "tailscale_enrollment_failed" {
		t.Fatalf("missing independent enrollment diagnostic: %+v", health)
	}
	for _, event := range events {
		if strings.Contains(event.JSON, "tskey-auth-should-not-persist") {
			t.Fatal("secret-like text persisted")
		}
	}
}
func TestBootstrapMintFailureReleasesUndeliveredClaim(t *testing.T) {
	api, token, id := mintTestEnv(t, "tailscale:\n  admin_api_key: admin-fixture\n  tailnet: example.com\n")
	minter := &fakeTailscaleMinter{err: errors.New("temporary unavailable")}
	api.tailscaleMinter = minter
	if got := doMintFetch(t, api, token, id); got.Code != 503 {
		t.Fatal(got.Code)
	}
	hash, _ := db.HashBootstrapToken(token)
	valid, err := api.store.ValidateBootstrapToken(context.Background(), hash, id, api.now())
	if err != nil || !valid {
		t.Fatal("failed mint stranded bootstrap token", err)
	}
	minter.err = nil
	minter.key = "tskey-auth-new-fixture"
	if got := doMintFetch(t, api, token, id); got.Code != http.StatusOK {
		t.Fatal("retry failed", got.Code)
	}
}

type readinessBackend struct {
	stubBackend
	probeErr error
}

func (b *readinessBackend) ProbeGuest(context.Context, proxmox.VMID) (string, error) {
	if b.probeErr != nil {
		return "", b.probeErr
	}
	return "10.77.0.22", nil
}
func TestReadinessDoesNotPromoteBootloaderFailure(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	id := 7123
	if err := store.CreateSandbox(ctx, models.Sandbox{VMID: id, Name: "bad-boot", Profile: "test", State: models.SandboxBooting}); err != nil {
		t.Fatal(err)
	}
	backend := &readinessBackend{probeErr: errors.New("guest agent unavailable")}
	backend.status = proxmox.StatusRunning
	manager := NewSandboxManager(store, backend, nil)
	if err := manager.Transition(ctx, id, models.SandboxReady); err == nil {
		t.Fatal("running QEMU mistaken for ready guest")
	}
	short, cancel := context.WithTimeout(ctx, 10*time.Millisecond)
	defer cancel()
	if _, err := manager.WaitGuestReady(short, id); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("readiness wait did not honor deadline", err)
	}
	if err := manager.ReconcileState(ctx); err != nil {
		t.Fatal(err)
	}
	sb, _ := store.GetSandbox(ctx, id)
	if sb.State != models.SandboxBooting {
		t.Fatalf("recovery claimed readiness for broken guest: %s", sb.State)
	}
	backend.probeErr = nil
	if err := manager.Transition(ctx, id, models.SandboxReady); err != nil {
		t.Fatal(err)
	}
}

func TestReadinessProjectionRequiresGuestEvidenceAndDeduplicatesSamples(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	id := 7200
	manager := NewSandboxManager(store, &stubBackend{}, nil)
	if err := emitEvent(ctx, NewStoreEventRecorder(store), EventKindSandboxState, &id, nil, "hypervisor running", map[string]any{"from_state": "BOOTING", "to_state": "RUNNING"}); err != nil {
		t.Fatal(err)
	}
	read := func() *EventProjection {
		events, err := store.ListEventsBySandboxAll(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		p := NewEventProjection()
		p.Replay(events)
		return p
	}
	if read().SandboxHealth[id].Healthy {
		t.Fatal("hypervisor state alone claimed guest health")
	}
	manager.recordGuestReadiness(ctx, id, true)
	manager.recordGuestReadiness(ctx, id, true)
	if !read().SandboxHealth[id].Healthy {
		t.Fatal("verified ready guest not healthy")
	}
	manager.recordGuestReadiness(ctx, id, false)
	p := read()
	if p.SandboxHealth[id].Healthy || p.SandboxHealth[id].Guest.ErrorCode != "guest_not_ready" {
		t.Fatal("aggregate health ignored failed guest probe")
	}
	events, _ := store.ListEventsBySandboxAll(ctx, id)
	if len(events) != 3 {
		t.Fatalf("duplicate readiness samples persisted: %d", len(events))
	}
}
func TestStandaloneBootstrapWithoutDefaultSecretsBundle(t *testing.T) {
	api, token, id := mintTestEnv(t, "")
	if _, err := api.store.DB.Exec(`DELETE FROM jobs WHERE sandbox_vmid=?`, id); err != nil {
		t.Fatal(err)
	}
	api.secretsStore.Dir = t.TempDir()
	if got := doMintFetch(t, api, token, id); got.Code != 200 {
		t.Fatal("unconfigured standalone bootstrap failed", got.Code, got.Body.String())
	}
}
