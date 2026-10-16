package templatebuild

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizeBoundaryInputs(t *testing.T) {
	good := Plan{Name: "ubuntu-2404", Storage: "local-zfs"}
	v := Normalize(good, Limits{})
	if !v.OK || v.Plan.MemoryMB != 4096 || v.Plan.AgentTools == nil || !*v.Plan.AgentTools {
		t.Fatalf("defaults: %+v", v)
	}
	for _, change := range []func(*Plan){func(p *Plan) { p.Name = "../escape" }, func(p *Plan) { p.Storage = "x;touch /tmp/unsafe" }, func(p *Plan) { p.Cores = -1 }, func(p *Plan) { p.VMID = 100 }, func(p *Plan) { p.Recipe = "shell" }, func(p *Plan) { p.TimeoutSeconds = 7201 }, func(p *Plan) { p.ImageURL = "https://cloud-images.ubuntu.com/image" }, func(p *Plan) {
		p.ImageURL = "http://cloud-images.ubuntu.com/image"
		p.ImageSHA256 = strings.Repeat("a", 64)
	}, func(p *Plan) { p.RequestID = "not-a-uuid" }} {
		p := good
		change(&p)
		if Normalize(p, Limits{}).OK {
			t.Fatalf("accepted unsafe plan: %+v", p)
		}
	}
}
func TestImageURLAndAddressPolicy(t *testing.T) {
	hosts := []string{"cloud-images.ubuntu.com"}
	for _, raw := range []string{"http://cloud-images.ubuntu.com/a", "https://user:password@cloud-images.ubuntu.com/a", "https://cloud-images.ubuntu.com:8443/a", "https://evil.example/a", "https://127.0.0.1/a", "https://cloud-images.ubuntu.com/a#fragment"} {
		if CheckURL(raw, hosts) == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	for _, raw := range []string{"127.0.0.1", "::1", "10.0.0.1", "172.16.2.3", "192.168.0.1", "169.254.169.254", "fe80::1", "fc00::1", "0.0.0.0", "224.0.0.1"} {
		if publicIP(net.ParseIP(raw)) {
			t.Fatalf("accepted private address %s", raw)
		}
	}
	if !publicIP(net.ParseIP("8.8.8.8")) {
		t.Fatal("public address rejected")
	}
	u, _ := url.Parse("https://metadata.google.internal/")
	if ImageClient(hosts).CheckRedirect(&http.Request{URL: u}, nil) == nil {
		t.Fatal("unsafe redirect accepted")
	}
}

type responseTransport func(*http.Request) (*http.Response, error)

func (f responseTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestPublishedChecksumExactFilename(t *testing.T) {
	digest := strings.Repeat("a", 64)
	content := digest + " *wrong.img\n"
	client := &http.Client{Transport: responseTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(content)), Header: http.Header{}}, nil
	})}
	if _, e := ResolveChecksum(context.Background(), client); e == nil {
		t.Fatal("accepted unrelated checksum")
	}
	content += digest + " *noble-server-cloudimg-amd64.img\n"
	got, e := ResolveChecksum(context.Background(), client)
	if e != nil || got != digest {
		t.Fatalf("checksum: %s %v", got, e)
	}
}
func TestChecksumMismatchAndIncompleteLogs(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "image")
	_ = os.WriteFile(file, []byte("image"), 0600)
	sum := sha256.Sum256([]byte("image"))
	if e := verifyFile(file, hex.EncodeToString(sum[:])); e != nil {
		t.Fatal(e)
	}
	if e := verifyFile(file, strings.Repeat("a", 64)); e == nil {
		t.Fatal("mismatch accepted")
	}
	logs := filepath.Join(dir, "logs")
	_ = os.WriteFile(logs, []byte("{\"sequence\":1,\"message\":\"a\"}\n{\"sequence\":2"), 0600)
	out, e := ReadWorkerLogs(logs, 0)
	if e != nil || len(out) != 1 || out[0].Sequence != 1 {
		t.Fatalf("partial logs: %+v %v", out, e)
	}
	out, e = ReadWorkerLogs(logs, 1)
	if e != nil || len(out) != 0 {
		t.Fatal("cursor duplicated logs")
	}
}
func TestOwnershipAndDrift(t *testing.T) {
	c := map[string]string{"tags": "tb_owned", "template": "1", "scsi0": "local:disk", "ide2": "local:cloudinit", "boot": "order=scsi0", "agent": "enabled=1"}
	if VerifyConfig(c, "tb_other") == nil {
		t.Fatal("unowned resource accepted")
	}
	if e := VerifyConfig(c, "tb_owned"); e != nil {
		t.Fatal(e)
	}
	m := Manifest{BuildID: "tb_owned", Config: map[string]string{}}
	for k, v := range c {
		m.Config[k] = v
	}
	c["scsi0"] = "local:other"
	if VerifyDrift(c, m) == nil {
		t.Fatal("changed disk accepted")
	}
	if strings.Contains(Redact("tskey-auth-abc123 password=secret"), "abc123") {
		t.Fatal("secret leaked")
	}
}

func TestCleanupNeverDestroysUnownedResources(t *testing.T) {
	for _, test := range []struct {
		name, tags   string
		destroyError bool
		wantCleanup  bool
		wantDestroy  int
	}{{"unowned", "some-other-build", false, true, 0}, {"owned", "tb_ours", false, false, 1}, {"destroy-fails", "tb_ours", true, true, 1}} {
		t.Run(test.name, func(t *testing.T) {
			destroy := 0
			w := &worker{cfg: WorkerConfig{Build: Build{ID: "tb_ours", VMID: 9010}, Directory: t.TempDir()}, status: WorkerStatus{Resources: []string{"vm:9010"}}}
			w.command = func(ctx context.Context, name string, args ...string) (string, error) {
				if name == "pvesh" {
					return "[]", nil
				}
				if name != "qm" {
					t.Fatalf("unexpected command %s", name)
				}
				switch args[0] {
				case "config":
					return "tags: " + test.tags + "\n", nil
				case "destroy":
					destroy++
					if test.destroyError {
						return "", fmt.Errorf("storage unavailable")
					}
					return "", nil
				}
				t.Fatalf("unexpected operation %v", args)
				return "", nil
			}
			w.cleanup()
			if destroy != test.wantDestroy || w.status.CleanupRequired != test.wantCleanup {
				t.Fatalf("ownership cleanup: destroys=%d status=%+v", destroy, w.status)
			}
		})
	}
}

func TestCleanupOwnershipIncludesEveryDisk(t *testing.T) {
	w := &worker{status: WorkerStatus{Resources: []string{"vm:9010", "disk:local:vm-9010-disk-0", "cloudinit:local:vm-9010-cloudinit,media=cdrom"}}}
	config := map[string]string{"scsi0": "local:vm-9010-disk-0,size=40G", "ide2": "local:vm-9010-cloudinit,media=cdrom"}
	if !w.ownedDisks(config) {
		t.Fatal("recorded disks rejected")
	}
	config["scsi1"] = "local:external-disk,size=40G"
	if w.ownedDisks(config) {
		t.Fatal("unowned attached disk would be destroyed")
	}
}

func TestWorkerRedactsSecretsBeforeWritingLogs(t *testing.T) {
	for _, secret := range []string{"password=secret-starting-with-s", "token=supersecret", "api_key=sk-example-secret", "client_secret=secret-value", `"token": "supersecret"`, `"Authorization": "Bearer secret-value"`, "Authorization: Bearer secret-value", "Authorization=Basic secret-value"} {
		got := Redact(secret)
		if strings.Contains(got, "secret") || strings.Contains(got, "sk-example") {
			t.Fatalf("secret persisted: %q -> %q", secret, got)
		}
	}
	file, err := os.CreateTemp(t.TempDir(), "logs-")
	if err != nil {
		t.Fatal(err)
	}
	w := &worker{log: file}
	w.record("customize", "-----BEGIN OPENSSH PRIVATE KEY-----")
	w.record("customize", "c2VjcmV0LXByaXZhdGUta2V5")
	w.record("customize", "-----END OPENSSH PRIVATE KEY-----")
	w.record("customize", "safe progress")
	if err = file.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(file.Name())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "c2VjcmV0") || !strings.Contains(string(raw), "safe progress") {
		t.Fatalf("private key redaction failed: %s", raw)
	}
}

func TestWorkerLogFailureStopsBeforeNextStage(t *testing.T) {
	dir := t.TempDir()
	file, err := os.CreateTemp(dir, "logs-")
	if err != nil {
		t.Fatal(err)
	}
	if err = file.Close(); err != nil {
		t.Fatal(err)
	}
	w := &worker{cfg: WorkerConfig{Directory: dir}, log: file}
	if err = w.stage("import"); err == nil {
		t.Fatal("closed log accepted")
	}
	if _, err = os.Stat(filepath.Join(dir, "status.json")); !os.IsNotExist(err) {
		t.Fatal("advanced to import without durable logs")
	}
	if _, err = w.run(context.Background(), "a-command-that-must-never-run"); err == nil || !strings.Contains(err.Error(), "persist worker logs") {
		t.Fatalf("log failure did not prevent command execution: %v", err)
	}
}

func TestTemplateConfigurationChecksExactOptions(t *testing.T) {
	valid := map[string]string{"tags": "tb_test", "template": "1", "scsi0": "local:base-disk", "ide2": "local:vm-9010-cloudinit,media=cdrom", "boot": "order=scsi0;ide2", "agent": "enabled=1,fstrim_cloned_disks=1"}
	if err := VerifyConfig(valid, "tb_test"); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ field, value string }{{"boot", "order=scsi01"}, {"boot", "order=net0;scsi0"}, {"boot", "other=scsi0"}, {"agent", "enabled=10"}, {"agent", "enabled=1,disabled=1"}, {"agent", "disabled=1"}, {"ide2", "local:not-cloudinit-disk,media=cdrom"}} {
		c := map[string]string{}
		for k, v := range valid {
			c[k] = v
		}
		c[test.field] = test.value
		if err := VerifyConfig(c, "tb_test"); err == nil {
			t.Fatalf("accepted invalid %s: %s", test.field, test.value)
		}
	}
}

func TestTemplateConversionTracksOnlyPreviouslyOwnedDisks(t *testing.T) {
	for _, test := range []struct {
		original, converted string
		ok                  bool
	}{
		{"zfs:vm-9010-disk-0", "zfs:base-9010-disk-0,size=40G", true},
		{"local:9010/vm-9010-disk-0.qcow2", "local:9010/base-9010-disk-0.qcow2,size=40G", true},
		{"local:vm-9010-cloudinit", "local:vm-9010-cloudinit,media=cdrom", true},
		{"local:vm-9010-disk-0", "local:base-8888-disk-0", false},
		{"local:vm-9010-disk-0", "other:base-9010-disk-0", false},
		{"local:9010/vm-9010-disk-0.raw", "local:8888/base-9010-disk-0.raw", false},
	} {
		if got := templateVolumeConversion(test.original, test.converted, 9010); got != test.ok {
			t.Fatalf("conversion %q -> %q: %v", test.original, test.converted, got)
		}
	}
	w := &worker{cfg: WorkerConfig{Build: Build{VMID: 9010}}, status: WorkerStatus{Resources: []string{"vm:9010", "disk:local:vm-9010-disk-0", "cloudinit:local:vm-9010-cloudinit"}}}
	before := strings.Join(w.status.Resources, ",")
	if err := w.recordConvertedVolumes(map[string]string{"scsi0": "local:base-9010-disk-0", "ide2": "other:vm-8888-cloudinit"}); err == nil {
		t.Fatal("unowned replacement accepted")
	}
	if strings.Join(w.status.Resources, ",") != before {
		t.Fatal("failed ownership check partially published resources")
	}
}

func TestWorkerUsesPrivateWritableRuntimeDirectories(t *testing.T) {
	dir := t.TempDir()
	log, err := os.Create(filepath.Join(dir, "logs.ndjson"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	t.Setenv("AGENTLAB_TEST_SECRET", "must-not-be-inherited")
	w := &worker{cfg: WorkerConfig{Directory: dir}, log: log}
	output, err := w.run(context.Background(), "/bin/sh", "-c", `printf '%s\n' "$TMPDIR" "$LIBGUESTFS_TMPDIR" "$LIBGUESTFS_CACHEDIR" "$XDG_RUNTIME_DIR" "${AGENTLAB_TEST_SECRET-unset}"; mktemp "$TMPDIR/agentlab.XXXXXX"`)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) != 6 {
		t.Fatalf("unexpected runtime evidence: %q", output)
	}
	expected := []string{filepath.Join(dir, "tmp"), filepath.Join(dir, "tmp"), filepath.Join(dir, "guestfs-cache"), filepath.Join(dir, "runtime"), "unset"}
	for i, value := range expected {
		if lines[i] != value {
			t.Fatalf("environment %d: %q want %q", i, lines[i], value)
		}
	}
	if filepath.Dir(lines[5]) != filepath.Join(dir, "tmp") {
		t.Fatalf("temporary file escaped private build directory: %s", lines[5])
	}
	if err = w.removePrivateDirectories(); err != nil {
		t.Fatal(err)
	}
	for _, name := range workerPrivateDirectories {
		if _, err = os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Fatalf("private %s working data retained", name)
		}
	}
}
func TestWorkerRejectsSymlinkRuntimeDirectory(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dir, "tmp")); err != nil {
		t.Fatal(err)
	}
	w := &worker{cfg: WorkerConfig{Directory: dir}}
	if err := w.preparePrivateDirectories(); err == nil {
		t.Fatal("private runtime directory followed a symlink")
	}
}
