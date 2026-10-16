package daemon

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/agentlab/agentlab/internal/db"
	tb "github.com/agentlab/agentlab/internal/templatebuild"
	assets "github.com/agentlab/agentlab/scripts"
)

// TemplateHost isolates local host operations so API validation and recovery can
// be exercised without giving tests access to a Proxmox host.
type TemplateHost interface {
	Prerequisites(context.Context) []tb.Issue
	Validate(context.Context, tb.Plan) []tb.Issue
	Inventory(context.Context) (map[int]bool, error)
	Start(context.Context, tb.Build, string) error
	Active(context.Context, string) (bool, error)
	Stop(context.Context, string) error
	Verify(context.Context, tb.Manifest) error
}
type localTemplateHost struct{}

func (localTemplateHost) Verify(ctx context.Context, m tb.Manifest) error {
	raw, e := hostCommand(ctx, "qm", "config", strconv.Itoa(m.VMID))
	if e != nil {
		return e
	}
	config := map[string]string{}
	for _, line := range strings.Split(string(raw), "\n") {
		k, v, ok := strings.Cut(line, ":")
		if ok {
			config[k] = strings.TrimSpace(v)
		}
	}
	node, e := os.Hostname()
	if e != nil {
		return e
	}
	if m.Node != node {
		return fmt.Errorf("template node mismatch")
	}
	return tb.VerifyDrift(config, m)
}

func hostCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "HOME=/root", "LANG=C.UTF-8"}
	return cmd.Output()
}
func (localTemplateHost) Prerequisites(ctx context.Context) []tb.Issue {
	out := []tb.Issue{}
	add := func(f, m string) {
		out = append(out, tb.Issue{Code: "template_prerequisite_missing", Field: f, Message: m})
	}
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		return []tb.Issue{{Code: "template_build_unsupported", Field: "host", Message: "requires a local Linux amd64 Proxmox node"}}
	}
	if os.Geteuid() != 0 || os.Getegid() != 0 {
		add("privileges", "template workers require root with root's primary group; check the daemon service User/Group policy")
	}
	for _, tool := range []string{"qm", "pvesh", "pvesm", "virt-customize", "virt-resize", "virt-cat", "qemu-img", "bash", "systemd-run", "systemctl", "sha256sum"} {
		if _, e := exec.LookPath(tool); e != nil {
			add(tool, "install the documented host prerequisites")
		}
	}
	for _, p := range []string{"/dev/kvm", "/etc/pve/local", "/run/systemd/system", "/var/log/pve/tasks"} {
		if _, e := os.Stat(p); e != nil {
			add(p, "required host path unavailable; check worker access policy")
		}
	}
	if f, e := os.OpenFile("/dev/kvm", os.O_RDWR, 0); e != nil {
		add("kvm", "worker requires read/write access to /dev/kvm")
	} else {
		f.Close()
	}
	return out
}
func (localTemplateHost) Inventory(ctx context.Context) (map[int]bool, error) {
	raw, e := hostCommand(ctx, "pvesh", "get", "/cluster/resources", "--type", "vm", "--output-format", "json")
	if e != nil {
		return nil, e
	}
	var rows []struct {
		VMID int `json:"vmid"`
	}
	if e = json.Unmarshal(raw, &rows); e != nil {
		return nil, e
	}
	out := map[int]bool{}
	for _, r := range rows {
		out[r.VMID] = true
	}
	return out, nil
}
func (localTemplateHost) Validate(ctx context.Context, p tb.Plan) []tb.Issue {
	out := []tb.Issue{}
	add := func(f, c, m string) { out = append(out, tb.Issue{Code: c, Field: f, Message: m}) }
	node, e := os.Hostname()
	if e != nil {
		add("host", "template_build_unsupported", "cannot identify local node")
		return out
	}
	raw, e := hostCommand(ctx, "pvesh", "get", "/nodes/"+node+"/storage", "--output-format", "json")
	if e != nil {
		add("storage", "template_storage_unavailable", "cannot read local storage; check Proxmox IPC and permissions")
		return out
	}
	var rows []struct {
		Storage string `json:"storage"`
		Active  int    `json:"active"`
		Enabled int    `json:"enabled"`
		Shared  int    `json:"shared"`
		Content string `json:"content"`
		Avail   int64  `json:"avail"`
	}
	if e = json.Unmarshal(raw, &rows); e != nil {
		add("storage", "template_storage_unavailable", "cannot decode local storage status")
		return out
	}
	for field, name := range map[string]string{"storage": p.Storage, "cloudinit_storage": p.CloudInitStorage} {
		found := false
		for _, s := range rows {
			if s.Storage != name {
				continue
			}
			found = true
			if s.Active != 1 || s.Enabled != 1 || s.Shared == 1 || !strings.Contains(","+s.Content+",", ",images,") {
				add(field, "template_storage_unavailable", "requires enabled, active local disk storage with images content")
			}
			if field == "storage" && s.Avail < int64(p.DiskSizeGB+5)<<30 {
				add(field, "template_storage_unavailable", "insufficient disk headroom for requested template")
			}
		}
		if !found {
			add(field, "template_storage_unavailable", "storage does not exist on the local node")
		}
	}
	if _, e = os.Stat(filepath.Join("/sys/class/net", p.Bridge, "bridge")); e != nil {
		add("bridge", "template_prerequisite_missing", "bridge does not exist on local node; configure host networking explicitly")
	}
	if e = templateHeadroom("/var/lib/agentlab", int64(p.DiskSizeGB+5)<<30); e != nil {
		add("disk_size_gb", "template_storage_unavailable", "insufficient working-image headroom under /var/lib/agentlab")
	}
	return out
}
func (localTemplateHost) Start(ctx context.Context, b tb.Build, config string) error {
	exe, e := os.Executable()
	if e != nil {
		return e
	}
	// The unit is independent of agentlabd.service. KillMode=control-group and
	// RuntimeMaxSec bound children even if the API process disappears.
	// Debian's libguestfs 1.48 uses /tmp for its short appliance socket paths
	// even when TMPDIR and XDG_RUNTIME_DIR point into the build directory.
	// Keep this exception on the worker; the API daemon needs no /tmp writes.
	_, e = hostCommand(ctx, "systemd-run", "--unit", b.Unit, "--property=Type=exec", "--property=KillMode=control-group", "--property=TimeoutStopSec=240", "--property=UMask=0077", "--property=MemoryMax=8G", "--property=TasksMax=512", fmt.Sprintf("--property=RuntimeMaxSec=%d", b.Plan.TimeoutSeconds+240), "--property=ProtectSystem=strict", "--property=ReadWritePaths=/var/lib/agentlab /etc/pve /var/lib/vz /var/log/pve /run /dev /tmp", exe, "template-worker", "--config", config)
	return e
}
func (localTemplateHost) Active(ctx context.Context, unit string) (bool, error) {
	raw, err := hostCommand(ctx, "systemctl", "show", unit, "--property=LoadState", "--property=ActiveState")
	return templateUnitActive(raw, err)
}

// systemctl can exit nonzero for an absent unit while still returning its
// properties. Only LoadState=not-found proves absence; transport/permission
// errors remain uncertain and must not trigger a replacement worker.
func templateUnitActive(raw []byte, commandErr error) (bool, error) {
	properties := map[string]string{}
	for _, line := range strings.Split(string(raw), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if ok {
			properties[key] = strings.TrimSpace(value)
		}
	}
	if properties["LoadState"] == "not-found" {
		return false, nil
	}
	if commandErr != nil {
		return false, commandErr
	}
	switch properties["ActiveState"] {
	case "active", "activating", "deactivating", "reloading":
		return true, nil
	case "inactive", "failed":
		return false, nil
	default:
		return false, fmt.Errorf("cannot determine template worker unit state")
	}
}
func (localTemplateHost) Stop(ctx context.Context, unit string) error {
	// A stop job activates TimeoutStopSec and eventual cgroup killing. Sending
	// a signal alone leaves those guarantees inactive if a child ignores it.
	_, e := hostCommand(ctx, "systemctl", "stop", "--no-block", unit)
	return e
}

type TemplateBuildManager struct {
	offline                bool
	lastRetention          time.Time
	store                  *db.Store
	host                   TemplateHost
	limits                 tb.Limits
	root, assetRoot, cache string
	mu                     sync.Mutex
	redact                 func(string) string
}

func NewTemplateBuildManager(store *db.Store, limits tb.Limits) *TemplateBuildManager {
	return &TemplateBuildManager{store: store, host: localTemplateHost{}, limits: limits.Defaults(), root: "/var/lib/agentlab/template-builds", assetRoot: "/var/lib/agentlab/template-assets", cache: "/var/lib/agentlab/images", redact: tb.Redact}
}
func (m *TemplateBuildManager) prerequisites(ctx context.Context) []tb.Issue {
	if m.offline {
		return []tb.Issue{{Code: "template_build_unsupported", Field: "offline", Message: "remote template builds require image and package downloads; daemon is offline"}}
	}
	issues := m.host.Prerequisites(ctx)
	digest := assets.Digest()
	dir := filepath.Join(m.assetRoot, digest)
	if _, err := os.Lstat(dir); err == nil {
		if err = assets.VerifyDigest(dir, digest); err != nil {
			issues = append(issues, tb.Issue{Code: "template_prerequisite_missing", Field: "recipe", Message: "installed recipe assets fail digest verification; restore the pinned asset directory before building"})
		}
	} else if !os.IsNotExist(err) {
		issues = append(issues, tb.Issue{Code: "template_prerequisite_missing", Field: "recipe", Message: "cannot inspect installed recipe assets; check template-assets directory access"})
	}
	return issues
}
func (m *TemplateBuildManager) Capabilities(ctx context.Context) tb.Capabilities {
	errs := m.prerequisites(ctx)
	d := tb.Normalize(tb.Plan{}, m.limits).Plan
	return tb.Capabilities{Supported: len(errs) == 0, Recipes: []string{tb.Recipe}, Defaults: d, Limits: m.limits, Errors: errs}
}
func (m *TemplateBuildManager) Validate(ctx context.Context, p tb.Plan) tb.Validation {
	v := tb.Normalize(p, m.limits)
	if !v.OK {
		return v
	}
	errs := m.prerequisites(ctx)
	v.Errors = append(v.Errors, errs...)
	if len(errs) == 0 {
		v.Errors = append(v.Errors, m.host.Validate(ctx, v.Plan)...)
		ids, e := m.host.Inventory(ctx)
		if e != nil {
			v.Errors = append(v.Errors, tb.Issue{Code: "template_build_unsupported", Field: "host", Message: "cannot query Proxmox inventory"})
		} else if p.VMID != 0 && ids[p.VMID] {
			v.Errors = append(v.Errors, tb.Issue{Code: "template_vmid_conflict", Field: "vmid", Message: "VMID already exists"})
		} else if p.VMID == 0 {
			available, err := m.templateRangeAvailable(ctx, ids)
			if err != nil {
				v.Errors = append(v.Errors, tb.Issue{Code: "template_worker_unavailable", Field: "vmid", Message: "cannot inspect VMID reservations; check daemon database access"})
			} else if !available {
				v.Errors = append(v.Errors, tb.Issue{Code: "template_vmid_conflict", Field: "vmid", Message: "two unused VMIDs are required in the template range for the template and boot verification"})
			}
		}
	}
	if p.VMID != 0 {
		var count int
		e := m.store.DB.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM vmid_reservations WHERE vmid=?) + (SELECT COUNT(*) FROM sandboxes WHERE vmid=?)`, p.VMID, p.VMID).Scan(&count)
		if e != nil {
			v.Errors = append(v.Errors, tb.Issue{Code: "template_worker_unavailable", Field: "vmid", Message: "cannot inspect VMID reservations; check daemon database access"})
		} else if count > 0 {
			v.Errors = append(v.Errors, tb.Issue{Code: "template_vmid_conflict", Field: "vmid", Message: "VMID reserved or unavailable"})
		}
	}
	v.OK = len(v.Errors) == 0
	return v
}

// Availability is observational only; submission still reserves atomically.
func (m *TemplateBuildManager) templateRangeAvailable(ctx context.Context, inventory map[int]bool) (bool, error) {
	used := map[int]bool{}
	for id, occupied := range inventory {
		if occupied && id >= m.limits.VMIDMin && id <= m.limits.VMIDMax {
			used[id] = true
		}
	}
	rows, err := m.store.DB.QueryContext(ctx, `SELECT vmid FROM sandboxes WHERE vmid BETWEEN ? AND ? UNION SELECT vmid FROM vmid_reservations WHERE vmid BETWEEN ? AND ?`, m.limits.VMIDMin, m.limits.VMIDMax, m.limits.VMIDMin, m.limits.VMIDMax)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int
		if err = rows.Scan(&id); err != nil {
			return false, err
		}
		used[id] = true
	}
	if err = rows.Err(); err != nil {
		return false, err
	}
	return len(used)+2 <= m.limits.VMIDMax-m.limits.VMIDMin+1, nil
}
func (m *TemplateBuildManager) Submit(ctx context.Context, p tb.Plan, caller string) (tb.Build, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var b tb.Build
	v := tb.Normalize(p, m.limits)
	if !v.OK {
		return b, &templatePlanError{v}
	}
	if p.RequestID == "" {
		v.OK = false
		v.Errors = append(v.Errors, tb.Issue{Code: "template_invalid_plan", Field: "request_id", Message: "UUID required"})
		return b, &templatePlanError{v}
	}
	raw, _ := json.Marshal(v.Plan)
	b, e := m.store.TemplateBuildByRequest(ctx, p.RequestID, string(raw))
	if e == nil {
		return b, nil
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return b, e
	}
	v = m.Validate(ctx, p)
	if !v.OK {
		return b, &templatePlanError{v}
	}
	ids, e := m.host.Inventory(ctx)
	if e != nil {
		return b, fmt.Errorf("template_build_unsupported")
	}
	if _, e = assets.Extract(m.assetRoot); e != nil {
		return b, fmt.Errorf("template_prerequisite_missing: cannot install immutable recipe assets")
	}
	id, e := newSessionID()
	if e != nil {
		return b, e
	}
	id = "tb_" + strings.TrimPrefix(id, "session_")
	now := time.Now().UTC()
	b = tb.Build{ID: id, State: "QUEUED", Stage: "preflight", Plan: v.Plan, Caller: caller, AssetDigest: assets.Digest(), Unit: "agentlab-template-" + id + ".service", CreatedAt: now, UpdatedAt: now}
	b, _, e = m.store.SubmitTemplateBuild(ctx, b, string(raw), ids, m.limits)
	return b, e
}

type templatePlanError struct{ Validation tb.Validation }

func (e *templatePlanError) Error() string { return "template plan validation failed" }
func (m *TemplateBuildManager) Cancel(ctx context.Context, id string) (tb.Build, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, e := m.store.TemplateBuild(ctx, id)
	if e != nil || b.Terminal() {
		return b, e
	}
	b.CancelRequested = true
	if b.State == "QUEUED" {
		b.State = "CANCELLED"
		now := time.Now().UTC()
		b.FinishedAt = &now
	}
	e = m.store.SaveTemplateBuild(ctx, b)
	if e == nil {
		_ = m.store.RecordEvent(ctx, "template.build.cancel_requested", nil, nil, id, templateAuditJSON(EventKindTemplateCancelled, map[string]any{"build_id": id}))
	}
	return b, e
}

// Run serializes work per host and reconciles durable units before launching queued work.
func (m *TemplateBuildManager) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		m.tick(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
func (m *TemplateBuildManager) tick(ctx context.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if time.Since(m.lastRetention) > time.Hour {
		m.retain(ctx)
		m.lastRetention = time.Now()
	}
	running, e := m.store.TemplateBuilds(ctx, "RUNNING", 0, 100)
	if e != nil {
		return
	}
	for _, b := range running {
		m.reconcile(ctx, b)
	}
	if len(running) > 0 {
		return
	}
	queued, e := m.store.TemplateBuilds(ctx, "QUEUED", 0, 1)
	if e != nil || len(queued) == 0 {
		return
	}
	b := queued[0]
	var owner string
	if e = m.store.DB.QueryRowContext(ctx, `SELECT owner FROM vmid_reservations WHERE vmid=?`, b.VMID).Scan(&owner); e != nil || owner != b.ID {
		m.fail(ctx, b, "template_vmid_conflict", "reserved VMID ownership is missing or changed; inspect database reservations before retrying", false)
		return
	}
	if b.SmokeVMID == 0 || b.SmokeVMID == b.VMID {
		m.fail(ctx, b, "template_vmid_conflict", "boot verification reservation missing; submit a new build", false)
		return
	}
	if e = m.store.DB.QueryRowContext(ctx, `SELECT owner FROM vmid_reservations WHERE vmid=?`, b.SmokeVMID).Scan(&owner); e != nil || owner != b.ID {
		m.fail(ctx, b, "template_vmid_conflict", "boot verification VMID ownership changed", false)
		return
	}
	v := m.Validate(ctx, b.Plan)
	// The build owns its own reservation, so remove that expected validation issue.
	filtered := []tb.Issue{}
	for _, i := range v.Errors {
		if i.Code == "template_vmid_conflict" && i.Message == "VMID reserved or unavailable" {
			continue
		}
		filtered = append(filtered, i)
	}
	if len(filtered) > 0 {
		m.fail(ctx, b, filtered[0].Code, filtered[0].Message, false)
		return
	}
	assetDir := filepath.Join(m.assetRoot, b.AssetDigest)
	e = assets.VerifyDigest(assetDir, b.AssetDigest)
	if e != nil {
		m.fail(ctx, b, "template_prerequisite_missing", "immutable recipe extraction failed", false)
		return
	}
	var claimed bool
	b, claimed, e = m.store.ClaimTemplateBuild(ctx, b.ID)
	if e != nil || !claimed {
		return
	}
	dir := filepath.Join(m.root, b.ID)
	if e = os.MkdirAll(dir, 0700); e != nil {
		m.fail(ctx, b, "template_build_failed", "cannot create private build directory", false)
		return
	}
	cfg := tb.WorkerConfig{Build: b, Assets: assetDir, Cache: m.cache, Directory: dir, ImageHosts: m.limits.ImageHosts}
	if e = tb.WriteJSON(filepath.Join(dir, "config.json"), cfg); e != nil {
		m.fail(ctx, b, "template_build_failed", "cannot persist worker configuration", false)
		return
	}
	if e = m.host.Start(ctx, b, filepath.Join(dir, "config.json")); e != nil {
		// systemd-run can lose its reply after accepting a unit. Reconcile rather
		// than rerun or assume no resources exist.
		m.reconcile(ctx, b)
	} else {
		_ = m.store.MarkTemplateWorkerLaunched(ctx, b.ID)
	}
}
func (m *TemplateBuildManager) fail(ctx context.Context, b tb.Build, code, message string, cleanup bool) {
	b.State = "FAILED"
	b.ErrorCode = code
	b.Error = message
	b.CleanupRequired = cleanup
	if cleanup {
		b.CleanupErrorCode = "template_cleanup_required"
	}
	now := time.Now().UTC()
	b.FinishedAt = &now
	_ = m.store.SaveTemplateBuild(ctx, b)
	if cleanup {
		_ = m.store.RecordEvent(ctx, "template.build.cleanup_failed", nil, nil, b.ID, templateAuditJSON(EventKindTemplateCleanup, map[string]any{"build_id": b.ID}))
	}
}
func (m *TemplateBuildManager) reconcile(ctx context.Context, b tb.Build) {
	dir := filepath.Join(m.root, b.ID)
	var e error
	var after int64
	_ = m.store.DB.QueryRowContext(ctx, `SELECT COALESCE(MAX(sequence),0) FROM template_build_logs WHERE build_id=?`, b.ID).Scan(&after)
	logs, e := tb.ReadWorkerLogs(filepath.Join(dir, "logs.ndjson"), after)
	if e == nil {
		for i := range logs {
			logs[i].Message = m.redact(logs[i].Message)
		}
		if e = m.store.ImportTemplateLogs(ctx, b.ID, logs); e != nil {
			return
		}
	}
	var status tb.WorkerStatus
	raw, e := os.ReadFile(filepath.Join(dir, "status.json"))
	if e == nil {
		if e = json.Unmarshal(raw, &status); e != nil {
			m.fail(ctx, b, "template_build_interrupted", "invalid worker result; inspect recorded resources", true)
			return
		}
	}
	active, unitErr := m.host.Active(ctx, b.Unit)
	if status.Done {
		// The result writer may have appended its final log after the first read.
		_ = m.store.DB.QueryRowContext(ctx, `SELECT COALESCE(MAX(sequence),0) FROM template_build_logs WHERE build_id=?`, b.ID).Scan(&after)
		finalLogs, readErr := tb.ReadWorkerLogs(filepath.Join(dir, "logs.ndjson"), after)
		if readErr != nil {
			if unitErr == nil && !active {
				b.Resources = status.Resources
				b.SmokeResources = status.SmokeResources
				m.fail(ctx, b, "template_build_interrupted", "worker logs unavailable; inspect the unit and recorded resources before retrying", true)
			}
			return
		}
		for i := range finalLogs {
			finalLogs[i].Message = m.redact(finalLogs[i].Message)
		}
		if e = m.store.ImportTemplateLogs(ctx, b.ID, finalLogs); e != nil {
			return
		}

		if status.Stage != "" {
			b.Stage = status.Stage
		}
		b.ResolvedImageSHA256 = status.ResolvedImageSHA256
		b.Resources = status.Resources
		b.SmokeResources = status.SmokeResources
		b.CleanupRequired = status.CleanupRequired
		if b.CleanupRequired {
			b.CleanupErrorCode = "template_cleanup_required"
		}
		b.ExitStatus = &status.ExitStatus
		b.Result = status.Result
		b.ErrorCode = status.ErrorCode
		b.Error = status.Error
		now := time.Now().UTC()
		b.FinishedAt = &now
		if status.ErrorCode != "" || status.Result == nil || status.ExitStatus != 0 || status.CleanupRequired {
			b.State = "FAILED"
			if b.ErrorCode == "" {
				b.ErrorCode = "template_build_failed"
				b.Error = "worker result was unsuccessful; inspect build logs and cleanup status"
			}
		} else {
			b.State = "SUCCEEDED"
		}
		if b.CancelRequested && b.State != "SUCCEEDED" {
			b.State = "CANCELLED"
		}
		if b.State == "SUCCEEDED" {
			if e = verifyTemplateResult(b); e != nil {
				m.fail(ctx, b, "template_build_interrupted", e.Error(), true)
				return
			}
			if e = m.host.Verify(ctx, *b.Result); e != nil {
				m.fail(ctx, b, "template_build_interrupted", "worker result verification failed", true)
				return
			}
		}
		_ = m.store.SaveTemplateBuild(ctx, b)
		if b.CleanupRequired {
			_ = m.store.RecordEvent(ctx, "template.build.cleanup_failed", nil, nil, b.ID, templateAuditJSON(EventKindTemplateCleanup, map[string]any{"build_id": b.ID}))
		}
		return
	}
	if unitErr != nil {
		return
	} // backend uncertainty never justifies duplicate work
	if !active {
		// A RUNNING record must precede systemd-run. Another daemon may observe
		// that short launch window, including during restart. Allow the bounded
		// launcher request (30s) to settle before declaring an unacknowledged unit
		// absent. Positive launch/stage evidence makes disappearance immediate.
		if !b.WorkerLaunched && status.Stage == "" && b.StartedAt != nil && time.Since(*b.StartedAt) < time.Minute {
			return
		}
		b.Resources = status.Resources
		b.SmokeResources = status.SmokeResources
		m.fail(ctx, b, "template_build_interrupted", "worker disappeared; inspect unit logs and recorded resources; submit a new build with a new VMID", true)
		return
	}
	if b.CancelRequested {
		_ = m.host.Stop(ctx, b.Unit)
	}
	if status.Stage != "" && status.Stage != b.Stage {
		b.Stage = status.Stage
		b.ResolvedImageSHA256 = status.ResolvedImageSHA256
		b.Resources = status.Resources
		b.SmokeResources = status.SmokeResources
		_ = m.store.SaveTemplateBuild(ctx, b)
	}
}

// Retention removes terminal logs only. It never frees a reserved VMID or
// deletes templates, manifests, active work, or recovery configuration.
func (m *TemplateBuildManager) retain(ctx context.Context) {
	cutoff := time.Now().Add(-30 * 24 * time.Hour)
	for offset := 0; ; offset += 500 {
		builds, err := m.store.TemplateBuilds(ctx, "", offset, 500)
		if err != nil {
			return
		}
		for _, b := range builds {
			if !b.Terminal() || b.FinishedAt == nil || !b.FinishedAt.Before(cutoff) {
				continue
			}
			if _, err = m.store.DB.ExecContext(ctx, `DELETE FROM template_build_logs WHERE build_id=?`, b.ID); err != nil {
				continue
			}
			_ = os.Remove(filepath.Join(m.root, b.ID, "logs.ndjson"))
		}
		if len(builds) < 500 {
			return
		}
	}
}

// Bind completion to the reserved build and its pinned recipe. A valid live VM
// alone is insufficient evidence that this worker produced the requested image.
func verifyTemplateResult(b tb.Build) error {
	m := b.Result
	if m == nil || m.BuildID != b.ID || m.VMID != b.VMID || m.Recipe != b.Plan.Recipe || m.AssetDigest != b.AssetDigest || m.Name != b.Plan.Name || m.Enrolled || !m.BootVerified || m.BootVerifiedAt == nil {
		return fmt.Errorf("worker result identity differs from reserved build; inspect recorded resources")
	}
	if _, err := hex.DecodeString(m.ImageSHA256); err != nil {
		return fmt.Errorf("worker image checksum evidence is malformed; inspect recorded resources")
	}
	source := b.Plan.ImageURL
	if source == "" {
		source = tb.DefaultImage
	}
	if m.ImageURL != source || len(m.ImageSHA256) != 64 || m.ImageSHA256 != b.ResolvedImageSHA256 || (b.Plan.ImageSHA256 != "" && m.ImageSHA256 != b.Plan.ImageSHA256) {
		return fmt.Errorf("worker image checksum evidence differs from the build; inspect recorded resources")
	}
	for _, capability := range []string{"tailscale", "agent_runner", "ssh", "qemu_guest_agent"} {
		if m.Capabilities[capability] == "" {
			return fmt.Errorf("worker result is missing required guest capabilities; inspect build logs")
		}
	}
	if b.Plan.AgentTools != nil && *b.Plan.AgentTools {
		for _, capability := range []string{"claude", "codex", "opencode"} {
			if m.Capabilities[capability] == "" {
				return fmt.Errorf("worker result is missing requested agent CLI versions; inspect build logs")
			}
		}
	}
	return nil
}

func templateAuditJSON(kind EventKind, payload any) string {
	raw, _ := NewEventPayloadForKind(kind, payload)
	return raw
}
