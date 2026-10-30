package templatebuild

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	assets "github.com/agentlab/agentlab/scripts"
)

type WorkerConfig struct {
	Build      Build    `json:"build"`
	Assets     string   `json:"assets"`
	Cache      string   `json:"cache"`
	Directory  string   `json:"directory"`
	ImageHosts []string `json:"image_hosts"`
}
type WorkerStatus struct {
	SmokeResources      []string  `json:"smoke_resources,omitempty"`
	ResolvedImageSHA256 string    `json:"resolved_image_sha256,omitempty"`
	Stage               string    `json:"stage"`
	Done                bool      `json:"done"`
	ErrorCode           string    `json:"error_code,omitempty"`
	Error               string    `json:"error,omitempty"`
	ExitStatus          int       `json:"exit_status"`
	CleanupRequired     bool      `json:"cleanup_required"`
	Resources           []string  `json:"resources,omitempty"`
	Result              *Manifest `json:"result,omitempty"`
}

func WriteJSON(path string, v any) error {
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".record-")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	if e = f.Chmod(0600); e != nil {
		f.Close()
		return e
	}
	if _, e = f.Write(b); e != nil {
		f.Close()
		return e
	}
	if e = f.Sync(); e != nil {
		f.Close()
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	return os.Rename(f.Name(), path)
}

var secretRE = regexp.MustCompile(`(?i)(tskey-[a-z0-9-]+|authorization["']?\s*[=: ]+\s*["']?(?:(?:bearer|basic)\s+)?[^\s"',;}]+|[a-z0-9_]*(?:token|password|secret|api_key)["']?\s*[=:]\s*["']?[^\s"',;}]+)`)

func Redact(s string) string { return secretRE.ReplaceAllString(s, "[REDACTED]") }

type worker struct {
	probeSSH            func(context.Context, string) error
	command             func(context.Context, string, ...string) (string, error)
	download            func(context.Context, Plan, string, []string) (string, string, error)
	cfg                 WorkerConfig
	status              WorkerStatus
	log                 *os.File
	mu                  sync.Mutex
	sequence            int64
	bytes               int64
	redactingPrivateKey bool
	logErr              error
}

func (w *worker) record(stage, message string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.logErr != nil || w.bytes >= 50<<20 {
		return
	}
	upper := strings.ToUpper(message)
	if strings.Contains(upper, "-----BEGIN ") && strings.Contains(upper, "PRIVATE KEY-----") {
		w.redactingPrivateKey = true
	}
	if w.redactingPrivateKey {
		if strings.Contains(upper, "-----END ") && strings.Contains(upper, "PRIVATE KEY-----") {
			w.redactingPrivateKey = false
		}
		message = "[REDACTED PRIVATE KEY]"
	} else {
		message = Redact(message)
	}
	if len(message) > 8192 {
		message = message[:8192] + " [line truncated]"
	}
	w.sequence++
	l := Log{w.sequence, time.Now().UTC(), stage, message}
	raw, _ := json.Marshal(l)
	truncated := false
	if w.bytes+int64(len(raw)+1) > (50<<20)-4096 {
		l.Message = "[log truncated at 50 MiB]"
		raw, _ = json.Marshal(l)
		truncated = true
	}
	if _, err := w.log.Write(append(raw, '\n')); err != nil {
		w.logErr = err
		return
	}
	if err := w.log.Sync(); err != nil {
		w.logErr = err
		return
	}
	w.bytes += int64(len(raw) + 1)
	if truncated {
		w.bytes = 50 << 20
	}
}
func (w *worker) logError() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.logErr != nil {
		return fmt.Errorf("persist worker logs: %w", w.logErr)
	}
	return nil
}
func (w *worker) stage(s string) error {
	w.status.Stage = s
	w.record(s, "stage started")
	if err := w.logError(); err != nil {
		return err
	}
	return WriteJSON(filepath.Join(w.cfg.Directory, "status.json"), w.status)
}
func (w *worker) run(ctx context.Context, name string, args ...string) (string, error) {
	if w.command != nil {
		return w.command(ctx, name, args...)
	}
	if err := w.logError(); err != nil {
		return "", err
	}
	if err := w.preparePrivateDirectories(); err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, name, args...)
	isolate(cmd)
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "HOME=/root", "LANG=C.UTF-8", "LIBGUESTFS_BACKEND=direct", "TMPDIR=" + filepath.Join(w.cfg.Directory, "tmp"), "LIBGUESTFS_TMPDIR=" + filepath.Join(w.cfg.Directory, "tmp"), "LIBGUESTFS_CACHEDIR=" + filepath.Join(w.cfg.Directory, "guestfs-cache"), "XDG_RUNTIME_DIR=" + filepath.Join(w.cfg.Directory, "runtime")}
	cmd.Dir = w.cfg.Directory
	r, e := cmd.StdoutPipe()
	if e != nil {
		return "", e
	}
	stderr, e := cmd.StderrPipe()
	if e != nil {
		return "", e
	}
	if e = cmd.Start(); e != nil {
		return "", e
	}
	stderrDone := make(chan error, 1)
	go func() {
		scan := bufio.NewScanner(stderr)
		scan.Buffer(make([]byte, 8192), 1<<20)
		for scan.Scan() {
			w.record(w.status.Stage, scan.Text())
		}
		if scan.Err() != nil {
			_ = cmd.Cancel()
		}
		stderrDone <- scan.Err()
	}()
	var out strings.Builder
	scan := bufio.NewScanner(r)
	scan.Buffer(make([]byte, 8192), 1<<20)
	for scan.Scan() {
		line := scan.Text()
		w.record(w.status.Stage, line)
		if out.Len() < 4<<20 {
			out.WriteString(line)
			out.WriteByte('\n')
		}
	}
	if e = scan.Err(); e != nil {
		_ = cmd.Cancel()
	}
	stderrErr := <-stderrDone
	wait := cmd.Wait()
	if stderrErr != nil {
		return out.String(), stderrErr
	}
	if e != nil {
		return out.String(), e
	}
	if err := w.logError(); err != nil {
		return out.String(), err
	}
	if wait != nil {
		return out.String(), fmt.Errorf("%s failed; inspect build logs", name)
	}
	return out.String(), nil
}
func (w *worker) config(ctx context.Context) (map[string]string, error) {
	out, e := w.run(ctx, "qm", "config", strconv.Itoa(w.cfg.Build.VMID))
	if e != nil {
		return nil, e
	}
	m := map[string]string{}
	for _, l := range strings.Split(out, "\n") {
		k, v, ok := strings.Cut(l, ":")
		if ok {
			m[k] = strings.TrimSpace(v)
		}
	}
	return m, nil
}
func owned(config map[string]string, id string) bool {
	for _, tag := range strings.Split(config["tags"], ";") {
		if tag == id {
			return true
		}
	}
	return false
}
func (w *worker) cleanup() {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if err := w.cleanupSmoke(ctx); err != nil {
		w.status.CleanupRequired = true
		w.record(w.status.Stage, "smoke clone cleanup requires recovery: "+err.Error())
		return
	}
	if len(w.status.Resources) > 0 {
		for {
			cfg, e := w.config(ctx)
			if e != nil {
				w.status.CleanupRequired = true
				return
			}
			if !owned(cfg, w.cfg.Build.ID) {
				w.status.CleanupRequired = true
				return
			}
			node, nodeErr := os.Hostname()
			if nodeErr != nil {
				w.status.CleanupRequired = true
				return
			}
			taskRaw, taskErr := w.run(ctx, "pvesh", "get", "/nodes/"+node+"/tasks", "--source", "active", "--vmid", strconv.Itoa(w.cfg.Build.VMID), "--output-format", "json")
			var activeTasks []json.RawMessage
			if taskErr != nil || json.Unmarshal([]byte(taskRaw), &activeTasks) != nil {
				w.status.CleanupRequired = true
				return
			}
			if cfg["lock"] == "" && len(activeTasks) == 0 {
				// A VM ownership tag does not authorize deleting disks attached later by
				// another operator. Unknown volumes require manual recovery.
				if !w.ownedDisks(cfg) {
					w.status.CleanupRequired = true
					return
				}
				if _, e = w.run(ctx, "qm", "destroy", strconv.Itoa(w.cfg.Build.VMID), "--purge", "1"); e != nil {
					w.status.CleanupRequired = true
					return
				}
				w.status.Resources = nil
				break
			}
			select {
			case <-ctx.Done():
				w.status.CleanupRequired = true
				return
			case <-time.After(time.Second):
			}
		}
	}
	if e := os.Remove(filepath.Join(w.cfg.Directory, "work.img")); e != nil && !os.IsNotExist(e) {
		w.status.CleanupRequired = true
	}
	if e := w.removePrivateDirectories(); e != nil {
		w.status.CleanupRequired = true
	}
}

// RunWorker is invoked only by the installed daemon in a dedicated systemd unit.
// A result is persisted before exit, independent of the API process lifetime.
func RunWorker(ctx context.Context, configPath string) error {
	data, e := os.ReadFile(configPath)
	if e != nil {
		return e
	}
	var cfg WorkerConfig
	if e = json.Unmarshal(data, &cfg); e != nil {
		return e
	}
	if filepath.Clean(configPath) != filepath.Join(cfg.Directory, "config.json") {
		return fmt.Errorf("invalid worker directory")
	}
	log, e := os.OpenFile(filepath.Join(cfg.Directory, "logs.ndjson"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	defer log.Close()
	w := &worker{cfg: cfg, log: log, status: WorkerStatus{Stage: "preflight"}}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(cfg.Build.Plan.TimeoutSeconds)*time.Second)
	defer cancel()
	return w.perform(ctx)
}

// perform owns completion and cleanup for the installed worker and lifecycle
// tests; the API never runs stages in its request goroutines.
func (w *worker) perform(ctx context.Context) error {
	e := w.execute(ctx)
	if e != nil {
		w.status.ExitStatus = 1
		w.status.ErrorCode = "template_build_failed"
		w.status.Error = Redact(e.Error())
		for _, code := range []string{"template_checksum_mismatch", "template_vmid_conflict", "template_capability_missing", "template_boot_failed"} {
			if strings.HasPrefix(e.Error(), code) {
				w.status.ErrorCode = code
				break
			}
		}
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			w.status.ErrorCode = "template_build_timeout"
		}
		w.cleanup()
		if w.status.CleanupRequired {
			w.status.Error += "; cleanup required: inspect recorded build-owned VM and tasks before manual recovery"
		}
		w.record(w.status.Stage, w.status.Error)
	}
	w.status.Done = true
	save := WriteJSON(filepath.Join(w.cfg.Directory, "status.json"), w.status)
	if save != nil {
		return save
	}
	return e
}

func (w *worker) execute(ctx context.Context) error {
	p := w.cfg.Build.Plan
	id := strconv.Itoa(w.cfg.Build.VMID)
	if e := assets.VerifyDigest(w.cfg.Assets, w.cfg.Build.AssetDigest); e != nil {
		return e
	}
	if e := w.stage("preflight"); e != nil {
		return e
	}
	if _, e := w.run(ctx, "pvesh", "get", "/version", "--output-format", "json"); e != nil {
		return e
	}
	if e := w.stage("download"); e != nil {
		return e
	}
	download := w.download
	if download == nil {
		download = DownloadImage
	}
	image, digest, e := download(ctx, p, w.cfg.Cache, w.cfg.ImageHosts)
	// Preserve the resolved expected digest even when transfer or verification
	// fails, so diagnostics retain the exact image evidence used by this build.
	w.status.ResolvedImageSHA256 = digest
	if e != nil {
		return e
	}
	if e = w.stage("customize"); e != nil {
		return e
	}
	tool, err := w.pinRepositoryTool()
	if err != nil {
		return err
	}
	args := []string{filepath.Join(w.cfg.Assets, "create_template.sh"), "--repository-tool", tool, "--customize-only", "--work-image", filepath.Join(w.cfg.Directory, "work.img"), "--image-file", image, "--image-sha256", digest, "--cache-dir", w.cfg.Cache, "--vmid", id, "--name", p.Name, "--disk-size", strconv.Itoa(p.DiskSizeGB) + "G"}
	if p.AgentTools != nil && !*p.AgentTools {
		args = append(args, "--skip-agent-tools")
	}
	if _, e = w.run(ctx, "bash", args...); e != nil {
		return e
	}
	raw, e := w.run(ctx, "virt-cat", "-a", filepath.Join(w.cfg.Directory, "work.img"), "/etc/agentlab/template-capabilities.json")
	if e != nil {
		return e
	}
	versions := map[string]string{}
	if e = json.Unmarshal([]byte(raw), &versions); e != nil {
		return fmt.Errorf("invalid guest capability manifest")
	}
	for _, key := range []string{"tailscale", "agent_runner", "repository_result", "ssh", "qemu_guest_agent"} {
		if versions[key] == "" {
			return fmt.Errorf("template_capability_missing: %s", key)
		}
	}
	if p.AgentTools != nil && *p.AgentTools {
		for _, key := range []string{"claude", "codex", "opencode"} {
			if versions[key] == "" {
				return fmt.Errorf("template_capability_missing: %s", key)
			}
		}
	}
	if e = w.stage("import"); e != nil {
		return e
	}
	inventoryRaw, inventoryErr := w.run(ctx, "pvesh", "get", "/cluster/resources", "--type", "vm", "--output-format", "json")
	if inventoryErr != nil {
		return fmt.Errorf("cannot recheck VMID inventory before creation; inspect Proxmox access and build logs")
	}
	var inventory []struct {
		VMID int `json:"vmid"`
	}
	if e = json.Unmarshal([]byte(inventoryRaw), &inventory); e != nil {
		return fmt.Errorf("cannot decode VMID inventory before creation")
	}
	for _, resource := range inventory {
		if resource.VMID == w.cfg.Build.VMID {
			return fmt.Errorf("template_vmid_conflict: target VMID is now occupied; submit a new build")
		}
	}
	// Record the allocation attempt before creating anything. qm create is atomic
	// with the ownership tag; an external winner never acquires our tag.
	w.status.Resources = []string{"vm:" + id}
	if e = WriteJSON(filepath.Join(w.cfg.Directory, "status.json"), w.status); e != nil {
		return e
	}
	_, e = w.run(ctx, "qm", "create", id, "--name", p.Name, "--memory", strconv.Itoa(p.MemoryMB), "--cores", strconv.Itoa(p.Cores), "--net0", "virtio,bridge="+p.Bridge, "--ostype", "l26", "--tags", w.cfg.Build.ID)
	if e != nil {
		cfg, checkErr := w.config(ctx)
		if checkErr == nil && !owned(cfg, w.cfg.Build.ID) {
			w.status.Resources = nil
			return fmt.Errorf("template_vmid_conflict: another resource acquired the VMID")
		}
		return e
	}
	if _, e = w.run(ctx, "qm", "importdisk", id, filepath.Join(w.cfg.Directory, "work.img"), p.Storage); e != nil {
		return e
	}
	config, e := w.config(ctx)
	if e != nil {
		return e
	}
	disk := config["unused0"]
	if disk == "" || !strings.HasPrefix(disk, p.Storage+":") {
		return fmt.Errorf("cannot identify imported disk; inspect owned VM")
	}
	w.status.Resources = append(w.status.Resources, "disk:"+disk)
	if e = WriteJSON(filepath.Join(w.cfg.Directory, "status.json"), w.status); e != nil {
		return e
	}
	if _, e = w.run(ctx, "qm", "set", id, "--scsihw", "virtio-scsi-pci", "--scsi0", disk, "--ide2", p.CloudInitStorage+":cloudinit", "--boot", "order=scsi0", "--serial0", "socket", "--vga", "serial0", "--agent", "enabled=1", "--ipconfig0", "ip=dhcp"); e != nil {
		return e
	}
	importedConfig, configErr := w.config(ctx)
	if configErr != nil {
		return configErr
	}
	w.status.Resources = append(w.status.Resources, "cloudinit:"+importedConfig["ide2"])
	if e = WriteJSON(filepath.Join(w.cfg.Directory, "status.json"), w.status); e != nil {
		return e
	}
	if _, e = w.run(ctx, "qm", "resize", id, "scsi0", fmt.Sprintf("%dG", p.DiskSizeGB)); e != nil {
		return e
	}
	if e = w.stage("verify"); e != nil {
		return e
	}
	if _, e = w.run(ctx, "qm", "template", id); e != nil {
		return e
	}
	config, e = w.config(ctx)
	if e != nil {
		return e
	}
	if e = VerifyConfig(config, w.cfg.Build.ID); e != nil {
		return e
	}
	if e = w.recordConvertedVolumes(config); e != nil {
		return e
	}
	if e = WriteJSON(filepath.Join(w.cfg.Directory, "status.json"), w.status); e != nil {
		return e
	}
	if e = w.stage("boot_verify"); e != nil {
		return e
	}
	if e = w.smokeTest(ctx); e != nil {
		return e
	}
	verifiedAt := time.Now().UTC()
	if e = w.stage("finalize"); e != nil {
		return e
	}
	node, e := os.Hostname()
	if e != nil {
		return e
	}
	rawURL := p.ImageURL
	if rawURL == "" {
		rawURL = DefaultImage
	}
	w.status.Result = &Manifest{BootVerified: true, BootVerifiedAt: &verifiedAt, BuildID: w.cfg.Build.ID, VMID: w.cfg.Build.VMID, Name: p.Name, Node: node, Recipe: p.Recipe, AssetDigest: w.cfg.Build.AssetDigest, ImageURL: rawURL, ImageSHA256: digest, Capabilities: versions, Config: config, CreatedAt: time.Now().UTC()}
	if e = os.Remove(filepath.Join(w.cfg.Directory, "work.img")); e != nil {
		return e
	}
	return w.removePrivateDirectories()
}
func VerifyConfig(c map[string]string, buildID string) error {
	boot := configOption(c["boot"], "order")
	firstBoot, _, _ := strings.Cut(boot, ";")
	cloudInit, _, _ := strings.Cut(c["ide2"], ",")
	if !owned(c, buildID) || c["template"] != "1" || c["scsi0"] == "" || !cloudInitVolumeRE.MatchString(cloudInit) || firstBoot != "scsi0" || !guestAgentEnabled(c["agent"]) {
		return fmt.Errorf("template_capability_missing: template configuration or ownership has drifted")
	}
	return nil
}

var cloudInitVolumeRE = regexp.MustCompile(`(^|[-/:])cloudinit(\.[a-zA-Z0-9]+)?$`)

func configOption(value, key string) string {
	for _, part := range strings.Split(value, ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if ok && k == key {
			return v
		}
	}
	return ""
}
func guestAgentEnabled(value string) bool {
	enabled := false
	for i, part := range strings.Split(value, ",") {
		part = strings.TrimSpace(part)
		if i == 0 && part == "1" {
			enabled = true
			continue
		}
		if i == 0 && part == "0" {
			return false
		}
		key, v, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		if key == "enabled" {
			if v != "1" {
				return false
			}
			enabled = true
		}
		if key == "disabled" && v == "1" {
			return false
		}
	}
	return enabled
}

// ReadWorkerLogs reads bounded NDJSON data, ignoring an incomplete trailing line.
func ReadWorkerLogs(path string, after int64) ([]Log, error) {
	f, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	r := bufio.NewReader(io.LimitReader(f, 60<<20))
	out := []Log{}
	for {
		line, e := r.ReadString('\n')
		if e != nil {
			if e == io.EOF {
				return out, nil
			}
			return nil, e
		}
		var l Log
		if e = json.Unmarshal([]byte(line), &l); e != nil {
			return nil, e
		}
		if l.Sequence > after {
			out = append(out, l)
		}
	}
}

func VerifyDrift(c map[string]string, m Manifest) error {
	if e := VerifyConfig(c, m.BuildID); e != nil {
		return e
	}
	for _, key := range []string{"scsi0", "ide2", "boot", "scsihw", "agent", "template", "name", "net0", "memory", "cores"} {
		if c[key] != m.Config[key] {
			return fmt.Errorf("template_capability_missing: %s drifted since verification", key)
		}
	}
	return nil
}

var diskSlotRE = regexp.MustCompile(`^(scsi|ide|sata|virtio|unused)[0-9]+$`)

func (w *worker) ownedDisks(config map[string]string) bool {
	known := map[string]bool{}
	for _, resource := range w.status.Resources {
		kind, value, ok := strings.Cut(resource, ":")
		if ok && (kind == "disk" || kind == "cloudinit") {
			volume, _, _ := strings.Cut(value, ",")
			known[volume] = true
		}
	}
	for key, value := range config {
		if !diskSlotRE.MatchString(key) {
			continue
		}
		volume, _, _ := strings.Cut(value, ",")
		if volume != "none" && !known[volume] {
			return false
		}
	}
	return true
}

// qm template may rename vm-* disk volumes to base-* volumes. Accept only
// that conversion of a previously recorded disk, never an arbitrary replacement
// attached by another operator, and persist the new identity before finalizing.
func (w *worker) recordConvertedVolumes(config map[string]string) error {
	updated := append([]string(nil), w.status.Resources...)
	for i, resource := range updated {
		kind, original, ok := strings.Cut(resource, ":")
		if !ok || (kind != "disk" && kind != "cloudinit") {
			continue
		}
		slot := "scsi0"
		if kind == "cloudinit" {
			slot = "ide2"
		}
		converted := config[slot]
		if !templateVolumeConversion(original, converted, w.cfg.Build.VMID) {
			return fmt.Errorf("template_capability_missing: %s changed unexpectedly during template conversion; inspect owned resources", slot)
		}
		updated[i] = kind + ":" + converted
	}
	w.status.Resources = updated
	return nil
}
func templateVolumeConversion(original, converted string, vmid int) bool {
	original, _, _ = strings.Cut(original, ",")
	converted, _, _ = strings.Cut(converted, ",")
	if original == converted {
		return original != ""
	}
	storage, volume, ok := strings.Cut(original, ":")
	if !ok {
		return false
	}
	filename := path.Base(volume)
	prefix := "vm-" + strconv.Itoa(vmid) + "-"
	if !strings.HasPrefix(filename, prefix) {
		return false
	}
	expected := storage + ":" + strings.TrimSuffix(volume, filename) + "base-" + strconv.Itoa(vmid) + "-" + strings.TrimPrefix(filename, prefix)
	return converted == expected
}

var workerPrivateDirectories = []string{"tmp", "guestfs-cache", "runtime"}

func (w *worker) preparePrivateDirectories() error {
	for _, name := range workerPrivateDirectories {
		dir := filepath.Join(w.cfg.Directory, name)
		if err := os.Mkdir(dir, 0700); err != nil && !os.IsExist(err) {
			return fmt.Errorf("create private worker %s directory: %w", name, err)
		}
		info, err := os.Lstat(dir)
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("unsafe private worker %s directory; remove unexpected symlink or file before retrying", name)
		}
		if info.Mode().Perm()&0077 != 0 {
			return fmt.Errorf("private worker %s directory must have mode 0700", name)
		}
	}
	return nil
}
func (w *worker) removePrivateDirectories() error {
	for _, name := range workerPrivateDirectories {
		if err := os.RemoveAll(filepath.Join(w.cfg.Directory, name)); err != nil {
			return fmt.Errorf("remove private worker %s directory: %w", name, err)
		}
	}
	return nil
}

// pinRepositoryTool copies the running trusted worker binary before image
// customization. Later host upgrades cannot change the helper during a build.
func (w *worker) pinRepositoryTool() (string, error) {
	source := "/proc/self/exe"
	if runtime.GOOS != "linux" {
		path, err := os.Executable()
		if err != nil {
			return "", err
		}
		source = path
	}
	input, err := os.Open(source)
	if err != nil {
		return "", err
	}
	defer input.Close()
	file, err := os.CreateTemp(w.cfg.Directory, ".repository-tool-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(file.Name())
	count, err := io.Copy(file, io.LimitReader(input, (128<<20)+1))
	if err != nil || count > 128<<20 {
		file.Close()
		return "", fmt.Errorf("cannot pin the repository result tool")
	}
	if err := file.Chmod(0500); err != nil {
		file.Close()
		return "", err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	path := filepath.Join(w.cfg.Directory, "repository-tool")
	if err := os.Rename(file.Name(), path); err != nil {
		return "", err
	}
	return path, nil
}
