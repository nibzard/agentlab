package templatebuild

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	assets "github.com/agentlab/agentlab/scripts"
)

// The fixture substitutes only host tools and the image transport. The actual
// worker runs its packaged-asset checks, stage persistence, ownership tracking,
// configuration verification, manifest publication and failure cleanup.
type templateWorkerFixture struct {
	smokeCancel                context.CancelFunc
	smokeConfig                map[string]string
	smokeDestroyed             bool
	smokeBootFailure           bool
	worker                     *worker
	config                     map[string]string
	creates, destroys, imports int
	inventoryCollision         bool
	importFailure              bool
	customizationDiskSize      string
}

func newTemplateWorkerFixture(t *testing.T) *templateWorkerFixture {
	t.Helper()
	root := t.TempDir()
	assetDir, err := assets.Extract(filepath.Join(root, "assets"))
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "build")
	if err = os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	log, err := os.Create(filepath.Join(dir, "logs.ndjson"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { log.Close() })
	p := Normalize(Plan{Name: "test", Storage: "local", VMID: 9010}, Limits{}).Plan
	w := &worker{cfg: WorkerConfig{Build: Build{ID: "tb_lifecycle", VMID: 9010, SmokeVMID: 9011, Plan: p, AssetDigest: assets.Digest()}, Assets: assetDir, Directory: dir, Cache: filepath.Join(root, "cache")}, log: log, status: WorkerStatus{Stage: "preflight"}}
	w.download = func(ctx context.Context, p Plan, cache string, hosts []string) (string, string, error) {
		if err := os.MkdirAll(cache, 0700); err != nil {
			return "", "", err
		}
		image := filepath.Join(cache, "source.img")
		content := []byte("verified image fixture")
		if err := os.WriteFile(image, content, 0600); err != nil {
			return "", "", err
		}
		sum := sha256.Sum256(content)
		return image, hex.EncodeToString(sum[:]), nil
	}
	fixture := &templateWorkerFixture{smokeConfig: map[string]string{}, worker: w, config: map[string]string{}}
	w.command = fixture.command
	w.probeSSH = func(context.Context, string) error { return nil }
	return fixture
}
func (f *templateWorkerFixture) command(ctx context.Context, name string, args ...string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	switch name {
	case "pvesh":
		if strings.Contains(strings.Join(args, " "), "/cluster/resources") && f.inventoryCollision {
			return `[{"vmid":9010}]`, nil
		}
		return `[]`, nil
	case "bash":
		for i := 0; i+1 < len(args); i++ {
			if args[i] == "--disk-size" {
				f.customizationDiskSize = args[i+1]
			}
		}
		return "", os.WriteFile(filepath.Join(f.worker.cfg.Directory, "work.img"), []byte("customized image"), 0600)
	case "virt-cat":
		return `{"tailscale":"1","agent_runner":"1","ssh":"1","qemu_guest_agent":"1","claude":"1","codex":"1","opencode":"1"}`, nil
	case "qm":
		if len(args) >= 3 && args[0] == "clone" {
			f.smokeConfig = map[string]string{"tags": f.worker.cfg.Build.ID, "scsi0": "local:base-9010-disk-0/vm-9011-disk-0", "ide2": "local:vm-9011-cloudinit"}
			return "", nil
		}
		if len(args) > 1 && args[1] == "9011" {
			switch args[0] {
			case "config":
				var lines []string
				for k, v := range f.smokeConfig {
					lines = append(lines, k+": "+v)
				}
				return strings.Join(lines, "\n"), nil
			case "destroy":
				f.smokeDestroyed = true
				return "", nil
			case "start", "stop":
				return "", nil
			}
		}
		if len(args) > 3 && args[0] == "guest" && args[2] == "9011" {
			if args[1] == "exec" {
				if f.smokeBootFailure {
					if f.smokeCancel != nil {
						f.smokeCancel()
					}
					return "", fmt.Errorf("guest not booted")
				}
				return `{"exited":1,"exitcode":0,"out-data":"agentlab-boot-ok"}`, nil
			}
			return `[{"ip-addresses":[{"ip-address":"10.77.0.22"}]}]`, nil
		}
		if len(args) < 2 {
			return "", fmt.Errorf("missing qm operation")
		}
		if args[1] != strconv.Itoa(f.worker.cfg.Build.VMID) {
			return "", fmt.Errorf("operation targeted unrelated VM %s", args[1])
		}
		switch args[0] {
		case "create":
			f.creates++
			f.config["tags"] = f.worker.cfg.Build.ID
			f.config["name"] = f.worker.cfg.Build.Plan.Name
			return "", nil
		case "importdisk":
			f.imports++
			if f.importFailure {
				return "", fmt.Errorf("fixture import failure")
			}
			f.config["unused0"] = "local:vm-9010-disk-0"
			return "", nil
		case "config":
			var lines []string
			for k, v := range f.config {
				lines = append(lines, k+": "+v)
			}
			return strings.Join(lines, "\n"), nil
		case "set":
			for i := 2; i+1 < len(args); i += 2 {
				key := strings.TrimPrefix(args[i], "--")
				value := args[i+1]
				if key == "ide2" {
					value = "local:vm-9010-cloudinit,media=cdrom"
				}
				f.config[key] = value
			}
			delete(f.config, "unused0")
			return "", nil
		case "resize":
			return "", nil
		case "template":
			f.config["template"] = "1"
			f.config["scsi0"] = strings.Replace(f.config["scsi0"], ":vm-9010-", ":base-9010-", 1)
			return "", nil
		case "destroy":
			f.destroys++
			f.config = map[string]string{}
			return "", nil
		}
	}
	return "", fmt.Errorf("unexpected fixture command %s %v", name, args)
}
func TestWorkerLifecycleExternalCollisionAllocatesNothing(t *testing.T) {
	f := newTemplateWorkerFixture(t)
	f.inventoryCollision = true
	err := f.worker.perform(context.Background())
	if err == nil || f.worker.status.ErrorCode != "template_vmid_conflict" || f.creates != 0 || f.destroys != 0 || len(f.worker.status.Resources) != 0 || f.worker.status.Result != nil {
		t.Fatalf("collision affected resources: status=%+v create=%d destroy=%d err=%v", f.worker.status, f.creates, f.destroys, err)
	}
	if _, err = os.Stat(filepath.Join(f.worker.cfg.Directory, "work.img")); !os.IsNotExist(err) {
		t.Fatal("collision left working image")
	}
}
func TestWorkerLifecycleImportFailureCleansOwnedVM(t *testing.T) {
	f := newTemplateWorkerFixture(t)
	f.importFailure = true
	err := f.worker.perform(context.Background())
	if err == nil || f.worker.status.ErrorCode != "template_build_failed" || !f.worker.status.Done || f.worker.status.CleanupRequired || f.creates != 1 || f.destroys != 1 || len(f.worker.status.Resources) != 0 || f.worker.status.Result != nil {
		t.Fatalf("failed import cleanup: status=%+v create=%d destroy=%d err=%v", f.worker.status, f.creates, f.destroys, err)
	}
	if _, err = os.Stat(filepath.Join(f.worker.cfg.Cache, "source.img")); err != nil {
		t.Fatal("cleanup removed shared image cache")
	}
}
func TestWorkerCustomizesWithRequestedDiskCapacity(t *testing.T) {
	f := newTemplateWorkerFixture(t)
	f.worker.cfg.Build.Plan.DiskSizeGB = 64
	if err := f.worker.perform(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.customizationDiskSize != "64G" {
		t.Fatalf("package installation must use requested capacity before import; got %q", f.customizationDiskSize)
	}
}

func TestWorkerLifecyclePublishesVerifiedResult(t *testing.T) {
	f := newTemplateWorkerFixture(t)
	if err := f.worker.perform(context.Background()); err != nil {
		t.Fatal(err)
	}
	status := f.worker.status
	if !status.Done || !f.smokeDestroyed || len(status.SmokeResources) != 0 || status.Result == nil || !status.Result.BootVerified || status.Result.Enrolled || status.ErrorCode != "" || f.creates != 1 || f.destroys != 0 || f.imports != 1 || len(status.Resources) != 3 {
		t.Fatalf("invalid successful build: %+v", status)
	}
	if !f.worker.ownedDisks(f.config) || !strings.Contains(strings.Join(status.Resources, ","), "base-9010-disk-0") {
		t.Fatalf("converted disk ownership was not retained: %v", status.Resources)
	}
	if err := VerifyDrift(f.config, *status.Result); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(f.worker.cfg.Directory, "status.json"))
	if err != nil {
		t.Fatal(err)
	}
	var persisted WorkerStatus
	if err = json.Unmarshal(raw, &persisted); err != nil || !persisted.Done || persisted.Result == nil || persisted.Result.ImageSHA256 != status.ResolvedImageSHA256 {
		t.Fatalf("result not durable: %s %v", raw, err)
	}
	logs, err := ReadWorkerLogs(filepath.Join(f.worker.cfg.Directory, "logs.ndjson"), 0)
	if err != nil {
		t.Fatal(err)
	}
	var stages []string
	for _, record := range logs {
		stages = append(stages, record.Stage)
	}
	if strings.Join(stages, ",") != "preflight,download,customize,import,verify,boot_verify,boot_verify,finalize" {
		t.Fatalf("stage sequence: %v", stages)
	}
}

func TestWorkerLifecycleChecksumFailureKeepsResolvedDigest(t *testing.T) {
	f := newTemplateWorkerFixture(t)
	digest := strings.Repeat("a", 64)
	f.worker.download = func(context.Context, Plan, string, []string) (string, string, error) {
		return "", digest, fmt.Errorf("template_checksum_mismatch")
	}
	err := f.worker.perform(context.Background())
	status := f.worker.status
	if err == nil || status.ErrorCode != "template_checksum_mismatch" || status.ResolvedImageSHA256 != digest || status.CleanupRequired || f.creates != 0 || f.destroys != 0 || status.Result != nil {
		t.Fatalf("checksum failure lost evidence or allocated resources: %+v creates=%d destroys=%d err=%v", status, f.creates, f.destroys, err)
	}
}

func TestSmokeBootFailureNeverPublishesTemplate(t *testing.T) {
	f := newTemplateWorkerFixture(t)
	f.smokeBootFailure = true
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.smokeCancel = cancel
	if err := f.worker.perform(ctx); err == nil {
		t.Fatal("non-booting image published")
	}
	if f.worker.status.Result != nil || !f.smokeDestroyed || f.destroys != 1 || f.worker.status.CleanupRequired {
		t.Fatalf("boot failure did not clean only its clone/template: %+v", f.worker.status)
	}
}
func TestSmokeCleanupRefusesUnrecordedDisk(t *testing.T) {
	f := newTemplateWorkerFixture(t)
	f.worker.status.SmokeResources = []string{"vm:9011", "disk:local:vm-9011-disk-0"}
	f.smokeConfig = map[string]string{"tags": f.worker.cfg.Build.ID, "scsi0": "local:vm-9011-disk-0", "scsi1": "local:operator-disk"}
	if err := f.worker.cleanupSmoke(context.Background()); err == nil {
		t.Fatal("unrecorded disk cleanup permitted")
	}
	if f.smokeDestroyed || len(f.worker.status.SmokeResources) == 0 {
		t.Fatal("unowned disk deleted or recovery evidence lost")
	}
}
