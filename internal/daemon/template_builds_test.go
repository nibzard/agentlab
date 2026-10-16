package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agentlab/agentlab/internal/auth"
	"github.com/agentlab/agentlab/internal/proxmox"
	tb "github.com/agentlab/agentlab/internal/templatebuild"
	assets "github.com/agentlab/agentlab/scripts"
)

type templateHostStub struct {
	starts, stops, checks int
	active                bool
	issues                []tb.Issue
	verifyErr             error
	beforeStart           func()
}

func (h *templateHostStub) Prerequisites(context.Context) []tb.Issue     { return h.issues }
func (h *templateHostStub) Validate(context.Context, tb.Plan) []tb.Issue { h.checks++; return nil }
func (h *templateHostStub) Inventory(context.Context) (map[int]bool, error) {
	return map[int]bool{}, nil
}
func (h *templateHostStub) Start(context.Context, tb.Build, string) error {
	h.starts++
	if h.beforeStart != nil {
		h.beforeStart()
	}
	h.active = true
	return nil
}
func (h *templateHostStub) Active(context.Context, string) (bool, error) { return h.active, nil }
func (h *templateHostStub) Stop(context.Context, string) error           { h.stops++; return nil }
func (h *templateHostStub) Verify(context.Context, tb.Manifest) error    { return h.verifyErr }
func testTemplateManager(t *testing.T) (*TemplateBuildManager, *templateHostStub) {
	t.Helper()
	s := newTestStore(t)
	m := NewTemplateBuildManager(s, tb.Limits{})
	h := &templateHostStub{}
	m.host = h
	root := t.TempDir()
	m.root = filepath.Join(root, "builds")
	m.assetRoot = filepath.Join(root, "assets")
	m.cache = filepath.Join(root, "cache")
	return m, h
}
func templateTestPlan(n int) tb.Plan {
	return tb.Plan{Name: "test", Storage: "local", RequestID: fmt.Sprintf("00000000-0000-4000-8000-%012d", n)}
}
func TestTemplateManagerValidationHasNoEffects(t *testing.T) {
	m, h := testTemplateManager(t)
	ctx := context.Background()
	p := templateTestPlan(1)
	p.Name = "../unsafe"
	if v := m.Validate(ctx, p); v.OK {
		t.Fatal("invalid plan accepted")
	}
	if _, e := m.Submit(ctx, p, "test"); e == nil {
		t.Fatal("invalid submission accepted")
	}
	if h.checks != 0 || h.starts != 0 {
		t.Fatal("invalid plan touched host")
	}
	if _, e := os.Stat(m.assetRoot); !os.IsNotExist(e) {
		t.Fatal("validation extracted assets")
	}
	p = templateTestPlan(1)
	h.issues = []tb.Issue{{Code: "template_build_unsupported", Field: "host", Message: "unsupported"}}
	if _, e := m.Submit(ctx, p, "test"); e == nil {
		t.Fatal("unsupported host accepted")
	}
	var count int
	_ = m.store.DB.QueryRow(`SELECT COUNT(*) FROM vmid_reservations`).Scan(&count)
	if count != 0 {
		t.Fatal("unsupported build reserved VMID")
	}
}
func TestTemplateManagerDurableQueueRecoveryAndCancellation(t *testing.T) {
	m, h := testTemplateManager(t)
	ctx := context.Background()
	b, e := m.Submit(ctx, templateTestPlan(1), "test")
	if e != nil {
		t.Fatal(e)
	}
	again, e := m.Submit(ctx, templateTestPlan(1), "test")
	if e != nil || again.ID != b.ID {
		t.Fatal("lost response retry duplicated")
	}
	m.tick(ctx)
	if h.starts != 1 {
		t.Fatal("worker not started")
	}
	// A new manager represents a daemon restart; an active unit is reattached.
	restarted := NewTemplateBuildManager(m.store, m.limits)
	restarted.host = h
	restarted.root = m.root
	restarted.assetRoot = m.assetRoot
	restarted.cache = m.cache
	restarted.tick(ctx)
	if h.starts != 1 {
		t.Fatal("restart repeated worker")
	}
	if _, e = restarted.Cancel(ctx, b.ID); e != nil {
		t.Fatal(e)
	}
	restarted.tick(ctx)
	if h.stops != 1 {
		t.Fatal("running cancellation did not signal worker")
	}
	// A disappeared unit has an uncertain result: never rerun customization.
	h.active = false
	restarted.tick(ctx)
	got, e := m.store.TemplateBuild(ctx, b.ID)
	if e != nil || got.State != "FAILED" || got.ErrorCode != "template_build_interrupted" {
		t.Fatalf("recovery: %+v %v", got, e)
	}
	if h.starts != 1 {
		t.Fatal("uncertain build repeated")
	}
	next, e := m.Submit(ctx, templateTestPlan(2), "test")
	if e != nil || next.VMID == b.VMID {
		t.Fatal("retry reused VMID")
	}
	next, e = m.Cancel(ctx, next.ID)
	if e != nil || next.State != "CANCELLED" {
		t.Fatal("queued cancellation failed")
	}
	m.tick(ctx)
	if h.starts != 1 {
		t.Fatal("cancelled queue launched")
	}
}
func TestTemplateManagerCollectsFinalLogsBeforeSuccess(t *testing.T) {
	m, h := testTemplateManager(t)
	ctx := context.Background()
	b, e := m.Submit(ctx, templateTestPlan(1), "test")
	if e != nil {
		t.Fatal(e)
	}
	m.tick(ctx)
	h.active = false
	dir := filepath.Join(m.root, b.ID)
	l := tb.Log{Sequence: 1, Timestamp: time.Now().UTC(), Stage: "finalize", Message: "complete"}
	raw, _ := json.Marshal(l)
	if e = os.WriteFile(filepath.Join(dir, "logs.ndjson"), append(raw, '\n'), 0600); e != nil {
		t.Fatal(e)
	}
	status := tb.WorkerStatus{Done: true, Stage: "finalize", ResolvedImageSHA256: strings.Repeat("a", 64), Result: validTemplateResult(b)}
	if e = tb.WriteJSON(filepath.Join(dir, "status.json"), status); e != nil {
		t.Fatal(e)
	}
	m.tick(ctx)
	got, e := m.store.TemplateBuild(ctx, b.ID)
	if e != nil || got.State != "SUCCEEDED" {
		t.Fatalf("result: %+v %v", got, e)
	}
	logs, e := m.store.TemplateLogs(ctx, b.ID, 0, 100)
	if e != nil || len(logs.Records) != 1 {
		t.Fatal("terminal state lost logs")
	}
	if _, e = m.store.TemplateManifest(ctx, b.VMID); e != nil {
		t.Fatal("success manifest missing")
	}
}
func TestTemplateRoutesDenySandboxScope(t *testing.T) {
	plane := newAuthzPlane(t)
	for _, r := range []struct{ method, path string }{{"GET", "/v1/templates"}, {"GET", "/v1/templates/capabilities"}, {"POST", "/v1/templates/validate-plan"}, {"POST", "/v1/template-builds"}, {"GET", "/v1/template-builds"}, {"GET", "/v1/template-builds/tb_example"}, {"GET", "/v1/template-builds/tb_example/logs"}, {"POST", "/v1/template-builds/tb_example/cancel"}, {"GET", "/v1/templates/9000"}, {"POST", "/v1/templates/9000/activate"}} {
		t.Run(r.path+r.method, func(t *testing.T) {
			req := httptest.NewRequest(r.method, r.path, strings.NewReader(`{}`))
			req = req.WithContext(auth.WithIdentity(req.Context(), &auth.RequestIdentity{Token: &auth.Token{Claims: auth.TokenClaims{Commands: []string{"template"}, Scope: []string{"sandbox:1001"}}}}))
			rr := httptest.NewRecorder()
			plane.mux.ServeHTTP(rr, req)
			if rr.Code != http.StatusForbidden {
				t.Fatalf("scoped token got %d: %s", rr.Code, rr.Body.String())
			}
		})
	}
}

func TestTemplateAPIIdempotencyAndProfileActivation(t *testing.T) {
	m, _ := testTemplateManager(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "profiles.yaml")
	if e := os.WriteFile(path, []byte("name: test\ntemplate_vmid: 9999\n"), 0600); e != nil {
		t.Fatal(e)
	}
	profiles, e := LoadProfiles(dir)
	if e != nil {
		t.Fatal(e)
	}
	ps, e := NewProfileStore(dir, profiles)
	if e != nil {
		t.Fatal(e)
	}
	api := NewControlAPI(m.store, profiles, nil, nil, nil, "", nil)
	api.templateBuilds = m
	api.profileStore = ps
	backend := &stubBackend{}
	api.backend = backend
	mux := http.NewServeMux()
	api.Register(mux)
	p := templateTestPlan(1)
	raw, _ := json.Marshal(p)
	submit := func(body []byte) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/v1/template-builds", strings.NewReader(string(body)))
		req.Header.Set("Idempotency-Key", p.RequestID)
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)
		return rr
	}
	rr := submit(raw)
	if rr.Code != 202 || rr.Header().Get("Location") == "" {
		t.Fatalf("submission: %d %s", rr.Code, rr.Body.String())
	}
	var b tb.Build
	if e = json.Unmarshal(rr.Body.Bytes(), &b); e != nil {
		t.Fatal(e)
	}
	again := submit(raw)
	var duplicate tb.Build
	_ = json.Unmarshal(again.Body.Bytes(), &duplicate)
	if again.Code != 202 || duplicate.ID != b.ID {
		t.Fatal("HTTP retry duplicated")
	}
	p.Name = "changed"
	raw, _ = json.Marshal(p)
	if rr = submit(raw); rr.Code != 409 {
		t.Fatal("idempotency conflict not rejected")
	}
	b.State = "RUNNING"
	if e = m.store.SaveTemplateBuild(context.Background(), b); e != nil {
		t.Fatal(e)
	}
	node, _ := os.Hostname()
	cfg := map[string]string{"tags": b.ID, "template": "1", "scsi0": "local:base-disk", "ide2": "local:cloudinit", "boot": "order=scsi0", "agent": "enabled=1"}
	backend.vmConfig = cfg
	b.State = "SUCCEEDED"
	b.Result = &tb.Manifest{BootVerified: true, BootVerifiedAt: &b.CreatedAt, BuildID: b.ID, VMID: b.VMID, Node: node, Config: cfg, Capabilities: map[string]string{"tailscale": "1", "agent_runner": "1", "ssh": "1", "qemu_guest_agent": "1"}}
	if e = m.store.SaveTemplateBuild(context.Background(), b); e != nil {
		t.Fatal(e)
	}
	activate := func(expected int) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", fmt.Sprintf("/v1/templates/%d/activate", b.VMID), strings.NewReader(fmt.Sprintf(`{"profile":"test","expected_template_vmid":%d}`, expected)))
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)
		return rr
	}
	if rr = activate(9998); rr.Code != 409 {
		t.Fatal("stale activation accepted")
	}
	if rr = activate(9999); rr.Code != 200 {
		t.Fatalf("activation: %d %s", rr.Code, rr.Body.String())
	}
	if ps.Snapshot()["test"].TemplateVM != b.VMID || api.profileSnapshot()["test"].TemplateVM != b.VMID {
		t.Fatal("activation not published")
	}
}

func TestTemplateUnitStateDistinguishesMissingFromUncertain(t *testing.T) {
	commandErr := fmt.Errorf("systemctl exited 1")
	for _, tc := range []struct {
		name, properties string
		commandErr       error
		active, wantErr  bool
	}{
		{"missing", "LoadState=not-found\nActiveState=inactive\n", commandErr, false, false},
		{"running", "LoadState=loaded\nActiveState=active\n", nil, true, false},
		{"reloading", "LoadState=loaded\nActiveState=reloading\n", nil, true, false},
		{"finished", "LoadState=loaded\nActiveState=inactive\n", nil, false, false},
		{"failed", "LoadState=loaded\nActiveState=failed\n", nil, false, false},
		{"unreachable", "", commandErr, false, true},
		{"unexpected", "LoadState=loaded\nActiveState=unknown\n", nil, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			active, err := templateUnitActive([]byte(tc.properties), tc.commandErr)
			if active != tc.active || (err != nil) != tc.wantErr {
				t.Fatalf("active=%v err=%v", active, err)
			}
		})
	}
}

func validTemplateResult(b tb.Build) *tb.Manifest {
	return &tb.Manifest{BootVerified: true, BootVerifiedAt: &b.CreatedAt, BuildID: b.ID, VMID: b.VMID, Name: b.Plan.Name, Recipe: b.Plan.Recipe, AssetDigest: b.AssetDigest, ImageURL: tb.DefaultImage, ImageSHA256: strings.Repeat("a", 64), Capabilities: map[string]string{"tailscale": "1", "agent_runner": "1", "ssh": "1", "qemu_guest_agent": "1", "claude": "1", "codex": "1", "opencode": "1"}}
}

func TestTemplateRecoveryRejectsUnsuccessfulAndMismatchedResults(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*tb.WorkerStatus)
	}{
		{"cleanup-required", func(s *tb.WorkerStatus) { s.CleanupRequired = true }},
		{"nonzero-exit", func(s *tb.WorkerStatus) { s.ExitStatus = 1 }},
		{"different-build", func(s *tb.WorkerStatus) { s.Result.BuildID = "tb_another" }},
		{"different-vmid", func(s *tb.WorkerStatus) { s.Result.VMID++ }},
		{"different-assets", func(s *tb.WorkerStatus) { s.Result.AssetDigest = "another-digest" }},
		{"different-image", func(s *tb.WorkerStatus) { s.Result.ImageSHA256 = strings.Repeat("b", 64) }},
		{"enrolled-template", func(s *tb.WorkerStatus) { s.Result.Enrolled = true }},
		{"missing-capability", func(s *tb.WorkerStatus) { delete(s.Result.Capabilities, "tailscale") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, h := testTemplateManager(t)
			ctx := context.Background()
			b, err := m.Submit(ctx, templateTestPlan(1), "test")
			if err != nil {
				t.Fatal(err)
			}
			m.tick(ctx)
			h.active = false
			status := tb.WorkerStatus{Done: true, Stage: "finalize", ResolvedImageSHA256: strings.Repeat("a", 64), Result: validTemplateResult(b)}
			tc.change(&status)
			dir := filepath.Join(m.root, b.ID)
			if err = os.WriteFile(filepath.Join(dir, "logs.ndjson"), nil, 0600); err != nil {
				t.Fatal(err)
			}
			if err = tb.WriteJSON(filepath.Join(dir, "status.json"), status); err != nil {
				t.Fatal(err)
			}
			m.tick(ctx)
			got, err := m.store.TemplateBuild(ctx, b.ID)
			if err != nil || got.State != "FAILED" || got.ErrorCode == "" {
				t.Fatalf("unsafe completion: %+v %v", got, err)
			}
			manifests, err := m.store.TemplateManifests(ctx, 0, 100)
			if err != nil || len(manifests) != 0 {
				t.Fatalf("unsafe template became selectable: %+v %v", manifests, err)
			}
		})
	}
}

func TestTemplateValidationRejectsTamperedAssetsBeforeReservation(t *testing.T) {
	m, h := testTemplateManager(t)
	dir, err := assets.Extract(m.assetRoot)
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "create_template.sh")
	if err = os.Chmod(script, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(script, []byte("tampered"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	caps := m.Capabilities(ctx)
	if caps.Supported || len(caps.Errors) == 0 {
		t.Fatal("tampered recipe advertised as supported")
	}
	validation := m.Validate(ctx, templateTestPlan(1))
	if validation.OK {
		t.Fatal("tampered recipe passed validation")
	}
	if _, err = m.Submit(ctx, templateTestPlan(1), "test"); err == nil {
		t.Fatal("tampered recipe submitted")
	}
	var count int
	if err = m.store.DB.QueryRow(`SELECT COUNT(*) FROM vmid_reservations`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 || h.starts != 0 {
		t.Fatal("tampered recipe allocated resources")
	}
	content, err := os.ReadFile(script)
	if err != nil || string(content) != "tampered" {
		t.Fatal("validation mutated installed assets")
	}
}

func TestTemplateValidationDetectsExhaustedAutomaticRange(t *testing.T) {
	m, _ := testTemplateManager(t)
	m.limits.VMIDMin = 9000
	m.limits.VMIDMax = 9001
	ctx := context.Background()
	b, err := m.Submit(ctx, templateTestPlan(1), "test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Cancel(ctx, b.ID); err != nil {
		t.Fatal(err)
	}
	result := m.Validate(ctx, templateTestPlan(2))
	if result.OK {
		t.Fatal("exhausted automatic range validated successfully")
	}
	found := false
	for _, issue := range result.Errors {
		if issue.Code == "template_vmid_conflict" && issue.Field == "vmid" {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing VMID diagnostic: %+v", result.Errors)
	}
	available, err := m.templateRangeAvailable(ctx, map[int]bool{9000: true, 1000: true})
	if err != nil || available {
		t.Fatalf("occupied inventory reported available: %v %v", available, err)
	}
	var count int
	if err = m.store.DB.QueryRow(`SELECT COUNT(*) FROM vmid_reservations`).Scan(&count); err != nil || count != 1 {
		t.Fatal("validation allocated another reservation")
	}
}

func TestTemplateWorkerRequiresItsDurableReservation(t *testing.T) {
	for _, owner := range []string{"", "tb_other"} {
		t.Run("owner="+owner, func(t *testing.T) {
			m, h := testTemplateManager(t)
			ctx := context.Background()
			b, err := m.Submit(ctx, templateTestPlan(1), "test")
			if err != nil {
				t.Fatal(err)
			}
			if owner == "" {
				_, err = m.store.DB.Exec(`DELETE FROM vmid_reservations WHERE vmid=?`, b.VMID)
			} else {
				_, err = m.store.DB.Exec(`UPDATE vmid_reservations SET owner=? WHERE vmid=?`, owner, b.VMID)
			}
			if err != nil {
				t.Fatal(err)
			}
			m.tick(ctx)
			got, err := m.store.TemplateBuild(ctx, b.ID)
			if err != nil || got.State != "FAILED" || got.ErrorCode != "template_vmid_conflict" || h.starts != 0 {
				t.Fatalf("launched without reservation: %+v starts=%d err=%v", got, h.starts, err)
			}
		})
	}
}

func TestManagedTemplateShowDistinguishesMissingFromBackendFailure(t *testing.T) {
	m, _ := testTemplateManager(t)
	ctx := context.Background()
	b, err := m.Submit(ctx, templateTestPlan(1), "test")
	if err != nil {
		t.Fatal(err)
	}
	b.State = "RUNNING"
	if err = m.store.SaveTemplateBuild(ctx, b); err != nil {
		t.Fatal(err)
	}
	b.State = "SUCCEEDED"
	b.Result = validTemplateResult(b)
	if err = m.store.SaveTemplateBuild(ctx, b); err != nil {
		t.Fatal(err)
	}
	backend := &stubBackend{vmConfigErr: proxmox.ErrVMNotFound}
	api := NewControlAPI(m.store, nil, nil, nil, nil, "", nil).WithBackend(backend)
	api.templateBuilds = m
	mux := http.NewServeMux()
	api.Register(mux)
	request := func() *httptest.ResponseRecorder {
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, httptest.NewRequest("GET", fmt.Sprintf("/v1/templates/%d", b.VMID), nil))
		return rr
	}
	rr := request()
	var missing struct {
		Manifest tb.Manifest `json:"manifest"`
		Exists   bool        `json:"exists"`
		Drift    bool        `json:"drift"`
	}
	if err = json.Unmarshal(rr.Body.Bytes(), &missing); err != nil || rr.Code != 200 || missing.Exists || !missing.Drift || missing.Manifest.BuildID != b.ID {
		t.Fatalf("missing template lost manifest: %d %s %v", rr.Code, rr.Body.String(), err)
	}
	backend.vmConfigErr = fmt.Errorf("fixture connection failure")
	rr = request()
	if rr.Code != 503 || !strings.Contains(rr.Body.String(), "template_worker_unavailable") {
		t.Fatalf("backend uncertainty misreported as absence: %d %s", rr.Code, rr.Body.String())
	}
}

func TestTemplateReconciliationDoesNotInterruptPendingLaunch(t *testing.T) {
	m, h := testTemplateManager(t)
	ctx := context.Background()
	b, err := m.Submit(ctx, templateTestPlan(1), "test")
	if err != nil {
		t.Fatal(err)
	}
	observer := NewTemplateBuildManager(m.store, m.limits)
	observer.host = h
	observer.root = m.root
	observer.assetRoot = m.assetRoot
	observer.cache = m.cache
	h.beforeStart = func() { observer.tick(ctx) }
	m.tick(ctx)
	got, err := m.store.TemplateBuild(ctx, b.ID)
	if err != nil || got.State != "RUNNING" || !got.WorkerLaunched || h.starts != 1 {
		t.Fatalf("observer interrupted pending launch: %+v starts=%d err=%v", got, h.starts, err)
	}
}
func TestTemplatePendingLaunchEventuallyReconcilesAsInterrupted(t *testing.T) {
	m, _ := testTemplateManager(t)
	ctx := context.Background()
	b, err := m.Submit(ctx, templateTestPlan(1), "test")
	if err != nil {
		t.Fatal(err)
	}
	b, claimed, err := m.store.ClaimTemplateBuild(ctx, b.ID)
	if err != nil || !claimed {
		t.Fatal(err)
	}
	started := time.Now().Add(-2 * time.Minute)
	b.StartedAt = &started
	if err = m.store.SaveTemplateBuild(ctx, b); err != nil {
		t.Fatal(err)
	}
	m.tick(ctx)
	got, err := m.store.TemplateBuild(ctx, b.ID)
	if err != nil || got.State != "FAILED" || got.ErrorCode != "template_build_interrupted" || !got.CleanupRequired {
		t.Fatalf("abandoned launch not reconciled safely: %+v %v", got, err)
	}
}
