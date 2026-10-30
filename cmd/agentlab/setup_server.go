package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/agentlab/agentlab/internal/connection"
	"github.com/agentlab/agentlab/internal/hostidentity"
	"github.com/agentlab/agentlab/internal/operations"
	"github.com/agentlab/agentlab/internal/proxmox"
)

const serverSetupRoot = "/var/lib/agentlab/setup"

var setupOperationID = regexp.MustCompile(`^[0-9a-f]{32}$`)
var setupRequestKey = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$`)
var setupDigest = regexp.MustCompile(`^[0-9a-f]{64}$`)

type serverSetupRequest struct {
	Version        int        `json:"version"`
	OperationID    string     `json:"operation_id"`
	Owner          string     `json:"owner"`
	Key            string     `json:"key"`
	ServerIdentity string     `json:"server_identity"`
	Input          setupInput `json:"input"`
}

type serverSetupRuntime struct {
	root                                         string
	run                                          func(context.Context, string, ...string) (string, error)
	identity                                     func() (string, error)
	client                                       func() (*apiClient, error)
	configPath, clientPath, daemonPath, hostLock string
}

func productionSetupRuntime() serverSetupRuntime {
	return serverSetupRuntime{root: serverSetupRoot, configPath: defaultConfigPath, clientPath: "/usr/local/bin/agentlab", daemonPath: "/usr/local/bin/agentlabd", hostLock: "/run/lock/agentlab-setup.lock", run: (proxmox.ExecRunner{}).Run, identity: hostidentity.Local, client: func() (*apiClient, error) {
		state, err := loadInitState(defaultConfigPath)
		if err != nil {
			return nil, err
		}
		return newAPIClient(clientOptions{SocketPath: state.Config.SocketPath}, defaultRequestTimeout), nil
	}}
}
func (s serverSetupRuntime) store() operations.Store {
	return operations.Store{Root: filepath.Join(s.root, "journal")}
}
func (s serverSetupRuntime) directory(id string) string { return filepath.Join(s.root, id) }
func (s serverSetupRuntime) worker(id string) string    { return filepath.Join(s.directory(id), "worker") }
func setupUnit(id string) string                        { return "agentlab-setup-" + id + ".service" }

func privateSetupPath(path string, directory bool) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(owner.Uid) != os.Geteuid() || info.Mode()&0077 != 0 || (!directory && info.Size() > 512<<20) || (directory && !info.IsDir()) || (!directory && !info.Mode().IsRegular()) {
		return fmt.Errorf("setup path is not private and owned by the worker")
	}
	return nil
}
func (s serverSetupRuntime) request(id string) (serverSetupRequest, error) {
	var request serverSetupRequest
	if !setupOperationID.MatchString(id) {
		return request, fmt.Errorf("invalid setup operation ID")
	}
	for _, path := range []string{s.root, s.directory(id)} {
		if err := privateSetupPath(path, true); err != nil {
			return request, err
		}
	}
	path := filepath.Join(s.directory(id), "request.json")
	if err := privateSetupPath(path, false); err != nil {
		return request, err
	}
	file, err := os.Open(path)
	if err != nil {
		return request, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 65537))
	if err != nil || len(data) > 65536 {
		return request, fmt.Errorf("invalid setup request size")
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&request) != nil {
		return request, fmt.Errorf("invalid setup request")
	}
	if request.Input.Architecture != runtime.GOARCH || request.Version != 1 || request.OperationID != id || request.Owner == "" || !setupRequestKey.MatchString(request.Key) || request.OperationID != setupScopedOperationID(request.Owner, request.ServerIdentity, request.Key) || request.Input.Context == "" || !setupDigest.MatchString(request.Input.ClientSHA256) || !setupDigest.MatchString(request.Input.DaemonSHA256) || !setupDigest.MatchString(request.Input.BundleSHA256) || request.Input.WaitSeconds <= 0 || request.Input.WaitSeconds > 86400 {
		return request, fmt.Errorf("incomplete setup request")
	}
	if err := connection.ValidateContextName(request.Input.Context); err != nil {
		return request, err
	}
	identity, err := s.identity()
	if err != nil || identity != request.ServerIdentity {
		return request, fmt.Errorf("setup server identity changed")
	}
	if err := privateSetupPath(s.worker(id), false); err != nil {
		return request, err
	}
	digest, err := setupFileHash(s.worker(id))
	if err != nil || digest != request.Input.ClientSHA256 {
		return request, fmt.Errorf("setup worker digest changed")
	}
	return request, nil
}
func (s serverSetupRuntime) admit(ctx context.Context, id string) (operations.Record, error) {
	request, err := s.request(id)
	if err != nil {
		return operations.Record{}, err
	}
	if err := os.MkdirAll(s.store().Root, 0700); err != nil {
		return operations.Record{}, err
	}
	if err := privateSetupPath(s.store().Root, true); err != nil {
		return operations.Record{}, err
	}
	record, err := s.store().Create(operations.Request{Owner: request.Owner, Context: request.Input.Context, ServerIdentity: request.ServerIdentity, Key: request.Key, Input: request.Input})
	if err != nil {
		return record, err
	}
	if record.ID != id || record.Owner != request.Owner || record.ServerIdentity != request.ServerIdentity || record.Context != request.Input.Context {
		return record, fmt.Errorf("setup request identity does not match its journal")
	}
	if record.State == operations.Planned && len(record.Resources) == 0 {
		return s.store().Update(id, func(r *operations.Record) error {
			r.Resources = []operations.Resource{{Kind: "setup_worker", ID: s.worker(id), State: "retained"}, {Kind: "setup_request", ID: filepath.Join(s.directory(id), "request.json"), State: "retained"}, {Kind: "setup_assets", ID: filepath.Join(s.directory(id), "bundle.tgz"), State: "planned"}}
			r.Cleanup = "pending"
			return nil
		})
	}
	return record, nil
}

// Extract only bounded regular files and directories into a fresh private tree.
// Archive links and special files never enter the root-owned execution directory.
func extractSetupArchive(archivePath, destination string) error {
	file, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer file.Close()
	gz, err := gzip.NewReader(file)
	if err != nil {
		return err
	}
	defer gz.Close()
	gz.Multistream(false)
	bounded := &io.LimitedReader{R: gz, N: (512 << 20) + 1}
	reader := tar.NewReader(bounded)
	seen := map[string]bool{}
	total := int64(0)
	for count := 0; ; count++ {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		name := strings.TrimSuffix(header.Name, "/")
		if count >= 10000 || !strings.HasPrefix(name, "agentlab-bootstrap/") && name != "agentlab-bootstrap" || filepath.IsAbs(name) || filepath.Clean(name) != name || strings.ContainsAny(name, "\\\x00\r\n") || seen[name] || header.Linkname != "" || header.Mode&07000 != 0 {
			return fmt.Errorf("unsafe setup archive entry")
		}
		seen[name] = true
		target := filepath.Join(destination, name)
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0700); err != nil {
				return err
			}
		case tar.TypeReg:
			if header.Size < 0 || header.Size > 128<<20 || total+header.Size > 512<<20 {
				return fmt.Errorf("setup archive exceeds size limits")
			}
			total += header.Size
			if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
				return err
			}
			mode := os.FileMode(0600)
			if header.Mode&0111 != 0 {
				mode = 0700
			}
			output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(output, reader)
			syncErr := output.Sync()
			closeErr := output.Close()
			if err := errors.Join(copyErr, syncErr, closeErr); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unsupported setup archive entry")
		}
	}
	// Reading through EOF also verifies the gzip checksum.
	_, err = io.Copy(io.Discard, bounded)
	if bounded.N <= 0 {
		return fmt.Errorf("setup archive exceeds expanded limit")
	}
	return err
}
func (s serverSetupRuntime) prepareAssets(request serverSetupRequest) (string, error) {
	directory := s.directory(request.OperationID)
	archive := filepath.Join(directory, "bundle.tgz")
	if err := privateSetupPath(archive, false); err != nil {
		return "", err
	}
	digest, err := setupFileHash(archive)
	if err != nil || digest != request.Input.BundleSHA256 {
		return "", fmt.Errorf("setup bundle digest changed")
	}
	staging, err := os.MkdirTemp(directory, ".assets-")
	if err != nil {
		return "", err
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.RemoveAll(staging)
		}
	}()
	if err := extractSetupArchive(archive, staging); err != nil {
		return "", err
	}
	assets := filepath.Join(staging, "agentlab-bootstrap")
	for _, binary := range []struct{ name, digest string }{{"agentlab", request.Input.ClientSHA256}, {"agentlabd", request.Input.DaemonSHA256}} {
		path := filepath.Join(assets, "dist", binary.name+"_linux_"+runtime.GOARCH)
		digest, err := setupFileHash(path)
		if err != nil || digest != binary.digest {
			return "", fmt.Errorf("setup payload binary digest changed")
		}
		if err := validateBootstrapBinary(path, runtime.GOARCH); err != nil {
			return "", err
		}
	}
	keep = true
	return staging, nil
}

func (s serverSetupRuntime) observe(ctx context.Context, id string) (operations.Record, error) {
	if _, err := s.request(id); err != nil {
		return operations.Record{}, err
	}
	record, err := s.store().Read(id)
	if err != nil {
		return record, err
	}
	output, err := s.run(ctx, "/usr/bin/systemctl", "show", setupUnit(id), "--property=LoadState", "--property=ActiveState", "--property=SubState", "--property=MainPID", "--property=ExecMainStatus")
	if err != nil {
		return record, fmt.Errorf("cannot inspect setup service state")
	}
	fields := map[string]string{}
	for _, line := range strings.Split(output, "\n") {
		key, value, ok := strings.Cut(line, "=")
		if ok {
			fields[key] = value
		}
	}
	active := fields["ActiveState"]
	if fields["LoadState"] != "loaded" && fields["LoadState"] != "not-found" {
		return record, fmt.Errorf("setup service load state is unavailable")
	}
	if fields["LoadState"] == "not-found" {
		active = "absent"
	} else if active != "active" && active != "activating" && active != "deactivating" && active != "inactive" && active != "failed" {
		return record, fmt.Errorf("invalid setup service state")
	}
	pid, _ := strconv.Atoi(fields["MainPID"])
	observation := &operations.WorkerObservation{ObservedAt: time.Now().UTC(), Kind: "systemd", Unit: setupUnit(id), State: active, Active: active == "active" || active == "activating" || active == "deactivating", PID: pid}
	if status, err := strconv.Atoi(fields["ExecMainStatus"]); err == nil && active != "absent" {
		observation.ExitStatus = &status
	}
	record.Worker = observation
	if !observation.Active && (record.State == operations.Running || record.State == operations.Planned) {
		record.State = operations.Blocked
		record.ErrorCode = "setup_worker_stopped"
		record.Retryable = true
		record.Action = "inspect retained resources, then resume the same setup operation"
	}
	return record, nil
}
func (s serverSetupRuntime) start(ctx context.Context, id string) (operations.Record, error) {
	record, err := s.admit(ctx, id)
	if err != nil {
		return record, err
	}
	current, err := s.observe(ctx, id)
	if err != nil {
		return record, err
	}
	if current.Worker.Active || record.State == operations.Succeeded || record.State == operations.Cancelled {
		return current, nil
	}
	args := []string{"--quiet", "--no-ask-password", "--collect", "--unit=" + setupUnit(id), "--service-type=exec", "--property=KillMode=control-group", "--property=TimeoutStopSec=30s", "--property=UMask=0077", "--property=StandardOutput=null", "--property=StandardError=null", "--property=Environment=PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "--working-directory=/", "--", s.worker(id), "__setup-worker", "run", id}
	if _, err := s.run(ctx, "/usr/bin/systemd-run", args...); err != nil {
		return record, fmt.Errorf("setup service start is uncertain; observe the same operation before retrying")
	}
	return s.observe(ctx, id)
}
func (s serverSetupRuntime) execute(ctx context.Context, id string) (operations.Record, error) {
	record, err := s.admit(ctx, id)
	if err != nil {
		return record, err
	}
	request, err := s.request(id)
	if err != nil {
		return record, err
	}
	if record.State == operations.Succeeded || record.State == operations.Cancelled {
		return record, nil
	}
	lock, err := os.OpenFile(s.hostLock, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return record, err
	}
	defer lock.Close()
	lockInfo, err := lock.Stat()
	if err != nil {
		return record, err
	}
	owner, ok := lockInfo.Sys().(*syscall.Stat_t)
	if !ok || int(owner.Uid) != os.Geteuid() || !lockInfo.Mode().IsRegular() || lockInfo.Mode()&0022 != 0 {
		return record, fmt.Errorf("setup host lock is not owned and protected")
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		blocked, saveErr := s.store().Update(id, func(r *operations.Record) error {
			r.State = operations.Blocked
			r.ErrorCode = "setup_busy"
			r.Action = "another server setup owns the host; observe it before resuming"
			r.Retryable = true
			return nil
		})
		return blocked, errors.Join(err, saveErr)
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	// Cleanup can commit before the final success write. Recheck every
	// external condition without reintroducing a payload or repeating mutations.
	if _, err := os.Lstat(filepath.Join(s.directory(id), "bundle.tgz")); errors.Is(err, os.ErrNotExist) && record.Cleanup == "verified" {
		steps := s.steps(request, "")
		for i := range steps {
			steps[i].Apply = func(context.Context, *operations.Record, func() error) error {
				return &operations.Requirement{Code: "setup_payload_required", Action: "restore the same pinned setup payload before repairing changed server state"}
			}
		}
		return s.store().Run(ctx, id, steps)
	}
	if err := clearAbandonedSetupPayloads(s.directory(id)); err != nil {
		return record, err
	}
	staging, err := s.prepareAssets(request)
	if err != nil {
		blocked, saveErr := s.store().Update(id, func(r *operations.Record) error {
			r.State = operations.Blocked
			r.ErrorCode = "setup_payload_inspection_required"
			r.Action = "inspect the pinned setup payload before resuming the same operation"
			r.Retryable = false
			return nil
		})
		return blocked, errors.Join(err, saveErr)
	}
	defer os.RemoveAll(staging)
	assets := filepath.Join(staging, "agentlab-bootstrap")
	record, err = s.store().Update(id, func(r *operations.Record) error {
		r.Resources = appendSetupResource(r.Resources, operations.Resource{Kind: "setup_assets", ID: filepath.Join(s.directory(id), "bundle.tgz"), State: "retained"})
		r.Resources = appendSetupResource(r.Resources, operations.Resource{Kind: "setup_payload", ID: staging, State: "retained"})
		r.Resources = appendSetupResource(r.Resources, operations.Resource{Kind: "setup_worker", ID: s.worker(id), State: "retained"})
		r.Resources = appendSetupResource(r.Resources, operations.Resource{Kind: "setup_request", ID: filepath.Join(s.directory(id), "request.json"), State: "retained"})
		for i := range r.Resources {
			resource := &r.Resources[i]
			if resource.Kind == "setup_assets" {
				resource.State = "retained"
			}
			if resource.Kind == "setup_payload" && resource.ID != staging {
				if _, err := os.Lstat(resource.ID); errors.Is(err, os.ErrNotExist) {
					resource.State = "cleaned"
				}
			}
		}
		r.Cleanup = "pending"
		return nil
	})
	if err != nil {
		return record, err
	}
	work, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan struct{})
	defer close(done)
	go func() {
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-work.Done():
				return
			case <-ticker.C:
				if _, err := os.Stat(filepath.Join(s.store().Root, id+".cancel")); err == nil {
					cancel()
					return
				}
			}
		}
	}()
	result, runErr := s.store().Run(work, id, s.steps(request, assets))
	if work.Err() != nil || result.State == operations.Cancelled {
		cleanup, cancelCleanup := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancelCleanup()
		var cleanupErr error
		reconciled, saveErr := s.store().Update(id, func(r *operations.Record) error {
			if _, err := os.Stat(filepath.Join(s.store().Root, id+".cancel")); err == nil {
				r.State = operations.Cancelled
				r.ErrorCode = ""
				r.Action = "cancellation stopped the worker; inspect retained installation changes"
			}
			cleanupErr = cleanupSetupSandboxClient(cleanup, r, s.client, id)
			if cleanupErr != nil {
				r.Cleanup = "inspection_required"
			}
			return nil
		})
		if saveErr == nil {
			result = reconciled
		}
		runErr = errors.Join(runErr, saveErr, cleanupErr)
	}
	return result, runErr
}

func (s serverSetupRuntime) initReport(ctx context.Context, id string) (initReport, error) {
	var report initReport
	output, err := s.run(ctx, s.worker(id), "init", "--json")
	if err != nil {
		return report, fmt.Errorf("cannot inspect server initialization")
	}
	if json.Unmarshal([]byte(output), &report) != nil {
		return report, fmt.Errorf("invalid server initialization report")
	}
	return report, nil
}

func (s serverSetupRuntime) steps(request serverSetupRequest, assets string) []operations.Step {
	id := request.OperationID
	checkInit := func(names ...string) func(context.Context, *operations.Record) (bool, error) {
		return func(ctx context.Context, r *operations.Record) (bool, error) {
			report, err := s.initReport(ctx, id)
			if err != nil {
				return false, err
			}
			for _, name := range names {
				if findCheckStatus(report.Checks, name) != "ok" {
					return false, nil
				}
			}
			return true, nil
		}
	}
	scriptStep := func(name, script, check string) operations.Step {
		return operations.Step{Name: name, Check: checkInit(check), Apply: func(ctx context.Context, r *operations.Record, save func() error) error {
			_, err := s.run(ctx, filepath.Join(assets, script), "--apply")
			if err != nil {
				return fmt.Errorf("setup %s command failed", name)
			}
			return nil
		}}
	}
	return []operations.Step{
		scriptStep("bridge", "scripts/net/setup_vmbr1.sh", "bridge_"+defaultAgentBridge),
		scriptStep("firewall", "scripts/net/apply.sh", "nftables"),
		{Name: "backup", Check: func(ctx context.Context, r *operations.Record) (bool, error) {
			for _, resource := range r.Resources {
				if resource.Kind == "configuration_backup" {
					return true, privateSetupPath(resource.ID, false)
				}
			}
			_, err := os.Lstat(s.configPath)
			if errors.Is(err, os.ErrNotExist) {
				return true, nil
			}
			return false, err
		}, Apply: func(ctx context.Context, r *operations.Record, save func() error) error {
			backup := filepath.Join(s.directory(id), "configuration.backup")
			if err := copySetupBackup(s.configPath, backup); err != nil {
				return err
			}
			r.Resources = appendSetupResource(r.Resources, operations.Resource{Kind: "configuration_backup", ID: backup, State: "retained"})
			return save()
		}},
		{Name: "install", Check: func(ctx context.Context, r *operations.Record) (bool, error) {
			for _, binary := range []struct{ path, digest string }{{s.clientPath, request.Input.ClientSHA256}, {s.daemonPath, request.Input.DaemonSHA256}} {
				digest, err := setupFileHash(binary.path)
				if errors.Is(err, os.ErrNotExist) {
					return false, nil
				}
				if err != nil {
					return false, err
				}
				if digest != binary.digest {
					return false, nil
				}
			}
			if _, err := s.run(ctx, "/usr/bin/systemctl", "is-active", "--quiet", "agentlabd.service"); err != nil {
				return false, nil
			}
			client, err := s.client()
			if err != nil {
				return false, err
			}
			if _, err := client.doJSON(ctx, "GET", "/v1/status", nil); err != nil {
				return false, err
			}
			return true, verifySetupIdentity(ctx, client, request.ServerIdentity)
		}, Apply: func(ctx context.Context, r *operations.Record, save func() error) error {
			if _, err := s.run(ctx, filepath.Join(assets, "scripts/install_host.sh"), "--enable-remote-control", "--control-port", strconv.Itoa(defaultControlPort)); err != nil {
				return fmt.Errorf("server installation failed; inspect its saved operation")
			}
			return nil
		}},
		{Name: "initialize", Check: checkInit("bridge_"+defaultAgentBridge, "ip_forward", "nftables", "templates", "profiles"), Apply: func(ctx context.Context, r *operations.Record, save func() error) error {
			if _, err := s.run(ctx, s.worker(id), "init", "--apply", "--assets", assets, "--json"); err != nil {
				return fmt.Errorf("server initialization failed")
			}
			return nil
		}},
		{Name: "readiness", Check: func(ctx context.Context, r *operations.Record) (bool, error) {
			for _, resource := range r.Resources {
				if resource.Kind == "readiness_sandbox" && (resource.State == "verified" || resource.State == "verified_cleaned") {
					return true, nil
				}
			}
			return false, nil
		}, Apply: func(ctx context.Context, r *operations.Record, save func() error) error {
			client, err := s.client()
			if err != nil {
				return err
			}
			guest := func(ctx context.Context, vmid string) (string, error) {
				return s.run(ctx, "/usr/sbin/qm", "guest", "exec", vmid, "--", "/bin/true")
			}
			return testSetupSandboxClient(ctx, r, save, request.Input, client, guest, id, time.Duration(request.Input.WaitSeconds)*time.Second)
		}},
		{Name: "cleanup", Check: func(ctx context.Context, r *operations.Record) (bool, error) { return r.Cleanup == "verified", nil }, Apply: func(ctx context.Context, r *operations.Record, save func() error) error {
			if err := cleanupSetupSandboxClient(ctx, r, s.client, id); err != nil {
				return err
			}
			// Only this attempt's extracted payload is removed. The worker, request,
			// and any original configuration backup remain available for recovery.
			if err := os.RemoveAll(filepath.Dir(assets)); err != nil {
				return err
			}
			bundle := filepath.Join(s.directory(id), "bundle.tgz")
			if err := os.Remove(bundle); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			parent, err := os.Open(s.directory(id))
			if err != nil {
				return err
			}
			syncErr := parent.Sync()
			closeErr := parent.Close()
			if err := errors.Join(syncErr, closeErr); err != nil {
				return err
			}
			for i := range r.Resources {
				if r.Resources[i].Kind == "setup_assets" || r.Resources[i].Kind == "setup_payload" {
					r.Resources[i].State = "cleaned"
				}
			}
			r.Cleanup = "verified"
			return save()
		}},
	}
}
func copySetupBackup(source, destination string) error {
	if err := privateSetupPath(destination, false); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	sourceInfo, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if !sourceInfo.Mode().IsRegular() || sourceInfo.Size() > 8<<20 {
		return fmt.Errorf("configuration is not a bounded regular file")
	}
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	temporary, err := os.CreateTemp(filepath.Dir(destination), ".backup-")
	if err != nil {
		return err
	}
	defer os.Remove(temporary.Name())
	_, copyErr := io.Copy(temporary, io.LimitReader(input, (8<<20)+1))
	syncErr := temporary.Sync()
	closeErr := temporary.Close()
	if err := errors.Join(copyErr, syncErr, closeErr); err != nil {
		return err
	}
	if err := os.Link(temporary.Name(), destination); err != nil {
		return err
	}
	directory, err := os.Open(filepath.Dir(destination))
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func runServerSetupCommand(ctx context.Context, args []string) error {
	if len(args) == 1 && args[0] == "probe" {
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"setup_worker_protocol": 1})
	}
	if runtime.GOOS != "linux" || os.Geteuid() != 0 {
		return fmt.Errorf("server setup worker requires root on Linux")
	}
	if len(args) != 2 || !setupOperationID.MatchString(args[1]) {
		return fmt.Errorf("invalid server setup worker arguments")
	}
	runtime := productionSetupRuntime()
	id := args[1]
	var record operations.Record
	var err error
	switch args[0] {
	case "start":
		record, err = runtime.start(ctx, id)
	case "run":
		record, err = runtime.execute(ctx, id)
	case "show":
		record, err = runtime.observe(ctx, id)
	case "cancel":
		record, err = runtime.cancel(ctx, id)
	default:
		return fmt.Errorf("invalid server setup worker action")
	}
	if record.ID != "" {
		if outputErr := json.NewEncoder(os.Stdout).Encode(record); outputErr != nil {
			return outputErr
		}
	}
	return err
}

// Only abandoned private extraction directories belong to this cleanup. A
// symbolic link or unexpected ownership blocks cleanup instead of following it.
func clearAbandonedSetupPayloads(directory string) error {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return err
	}
	paths := []string{}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".assets-") {
			path := filepath.Join(directory, entry.Name())
			if err := privateSetupPath(path, true); err != nil {
				return err
			}
			paths = append(paths, path)
		}
	}
	for _, path := range paths {
		if err := os.RemoveAll(path); err != nil {
			return err
		}
	}
	return nil
}

func setupScopedOperationID(owner, identity, key string) string {
	scope, _ := json.Marshal([]string{owner, identity, key})
	digest := sha256.Sum256(scope)
	return hex.EncodeToString(digest[:16])
}

func (s serverSetupRuntime) cancel(ctx context.Context, id string) (operations.Record, error) {
	request, err := s.request(id)
	if err != nil {
		return operations.Record{}, err
	}
	record, err := s.store().Read(id)
	if errors.Is(err, os.ErrNotExist) {
		record, err = s.admit(ctx, id)
	}
	if err != nil {
		return record, err
	}
	normalized, _ := json.Marshal(request.Input)
	hash := sha256.Sum256(normalized)
	if record.ID != id || record.Owner != request.Owner || record.Context != request.Input.Context || record.ServerIdentity != request.ServerIdentity || record.InputHash != hex.EncodeToString(hash[:]) {
		return record, fmt.Errorf("setup cancellation binding changed")
	}
	if err := s.store().Cancel(id); err != nil {
		return record, err
	}
	observed, err := s.observe(ctx, id)
	if err != nil {
		return record, err
	}
	if !observed.Worker.Active && record.State != operations.Succeeded {
		if _, err := s.store().Update(id, func(r *operations.Record) error {
			r.State = operations.Cancelled
			r.Cleanup = "inspection_required"
			r.Resources = appendSetupResource(r.Resources, operations.Resource{Kind: "setup_staging", ID: s.directory(id), State: "retained"})
			r.Action = "inspect retained staging files and any completed installation changes"
			return nil
		}); err != nil {
			return observed, err
		}
		return s.observe(ctx, id)
	}
	return observed, nil
}
