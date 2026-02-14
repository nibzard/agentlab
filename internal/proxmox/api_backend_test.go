package proxmox

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

type apiRequest struct {
	method   string
	path     string
	rawQuery string
	form     url.Values
}

func TestAPIBackendClone(t *testing.T) {
	tests := []struct {
		name      string
		cloneMode string
		wantFull  string
	}{
		{name: "linked clone", cloneMode: "linked", wantFull: "0"},
		{name: "full clone", cloneMode: "full", wantFull: "1"},
		{name: "default clone mode", cloneMode: "", wantFull: "0"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls []apiRequest
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				_ = r.Body.Close()
				form, _ := url.ParseQuery(string(body))
				calls = append(calls, apiRequest{
					method:   r.Method,
					path:     r.URL.Path,
					rawQuery: r.URL.RawQuery,
					form:     form,
				})
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"data":{}}`))
			}))
			defer srv.Close()

			backend := &APIBackend{
				BaseURL:    srv.URL + "/api2/json",
				Node:       "pve",
				HTTPClient: srv.Client(),
				CloneMode:  tt.cloneMode,
			}

			if err := backend.Clone(context.Background(), 9000, 101, "sandbox-101"); err != nil {
				t.Fatalf("Clone() error = %v", err)
			}
			if len(calls) != 1 {
				t.Fatalf("expected 1 API call, got %d", len(calls))
			}
			call := calls[0]
			if call.method != http.MethodPost || call.path != "/api2/json/nodes/pve/qemu/9000/clone" {
				t.Fatalf("Clone call = %s %s", call.method, call.path)
			}
			if call.form.Get("newid") != "101" {
				t.Fatalf("Clone newid = %q", call.form.Get("newid"))
			}
			if call.form.Get("name") != "sandbox-101" {
				t.Fatalf("Clone name = %q", call.form.Get("name"))
			}
			if call.form.Get("full") != tt.wantFull {
				t.Fatalf("Clone full = %q, want %q", call.form.Get("full"), tt.wantFull)
			}
		})
	}
}

func TestAPIBackendSnapshotEndpoints(t *testing.T) {
	var calls []apiRequest

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		form, _ := url.ParseQuery(string(body))
		calls = append(calls, apiRequest{
			method:   r.Method,
			path:     r.URL.Path,
			rawQuery: r.URL.RawQuery,
			form:     form,
		})
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/snapshot") {
			_, _ = w.Write([]byte(`{"data":[{"name":"clean","snaptime":1730000000,"description":"baseline"}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":{}}`))
	}))
	defer srv.Close()

	backend := &APIBackend{
		BaseURL:    srv.URL + "/api2/json",
		Node:       "pve",
		HTTPClient: srv.Client(),
	}

	ctx := context.Background()
	if err := backend.SnapshotCreate(ctx, 101, "clean"); err != nil {
		t.Fatalf("SnapshotCreate() error = %v", err)
	}
	if err := backend.SnapshotRollback(ctx, 101, "clean"); err != nil {
		t.Fatalf("SnapshotRollback() error = %v", err)
	}
	if err := backend.SnapshotDelete(ctx, 101, "clean"); err != nil {
		t.Fatalf("SnapshotDelete() error = %v", err)
	}
	snapshots, err := backend.SnapshotList(ctx, 101)
	if err != nil {
		t.Fatalf("SnapshotList() error = %v", err)
	}
	if len(snapshots) != 1 || snapshots[0].Name != "clean" {
		t.Fatalf("SnapshotList() = %#v, want [clean]", snapshots)
	}

	if len(calls) != 4 {
		t.Fatalf("expected 4 API calls, got %d", len(calls))
	}

	if calls[0].method != http.MethodPost || calls[0].path != "/api2/json/nodes/pve/qemu/101/snapshot" {
		t.Fatalf("SnapshotCreate call = %s %s", calls[0].method, calls[0].path)
	}
	if calls[0].rawQuery != "" {
		t.Fatalf("SnapshotCreate unexpected query: %q", calls[0].rawQuery)
	}
	if calls[0].form.Get("snapname") != "clean" {
		t.Fatalf("SnapshotCreate snapname = %q", calls[0].form.Get("snapname"))
	}
	if calls[0].form.Get("vmstate") != "0" {
		t.Fatalf("SnapshotCreate vmstate = %q", calls[0].form.Get("vmstate"))
	}

	if calls[1].method != http.MethodPost || calls[1].path != "/api2/json/nodes/pve/qemu/101/snapshot/clean/rollback" {
		t.Fatalf("SnapshotRollback call = %s %s", calls[1].method, calls[1].path)
	}
	if len(calls[1].form) != 0 {
		t.Fatalf("SnapshotRollback form = %#v", calls[1].form)
	}

	if calls[2].method != http.MethodDelete || calls[2].path != "/api2/json/nodes/pve/qemu/101/snapshot/clean" {
		t.Fatalf("SnapshotDelete call = %s %s", calls[2].method, calls[2].path)
	}
	if len(calls[2].form) != 0 {
		t.Fatalf("SnapshotDelete form = %#v", calls[2].form)
	}

	if calls[3].method != http.MethodGet || calls[3].path != "/api2/json/nodes/pve/qemu/101/snapshot" {
		t.Fatalf("SnapshotList call = %s %s", calls[3].method, calls[3].path)
	}
}

func TestAPIBackendSuspendResumeEndpoints(t *testing.T) {
	var calls []apiRequest

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		form, _ := url.ParseQuery(string(body))
		calls = append(calls, apiRequest{
			method:   r.Method,
			path:     r.URL.Path,
			rawQuery: r.URL.RawQuery,
			form:     form,
		})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{}}`))
	}))
	defer srv.Close()

	backend := &APIBackend{
		BaseURL:    srv.URL + "/api2/json",
		Node:       "pve",
		HTTPClient: srv.Client(),
	}

	ctx := context.Background()
	if err := backend.Suspend(ctx, 101); err != nil {
		t.Fatalf("Suspend() error = %v", err)
	}
	if err := backend.Resume(ctx, 101); err != nil {
		t.Fatalf("Resume() error = %v", err)
	}

	if len(calls) != 2 {
		t.Fatalf("expected 2 API calls, got %d", len(calls))
	}

	if calls[0].method != http.MethodPost || calls[0].path != "/api2/json/nodes/pve/qemu/101/status/suspend" {
		t.Fatalf("Suspend call = %s %s", calls[0].method, calls[0].path)
	}
	if got := calls[0].form.Get("todisk"); got != "0" {
		t.Fatalf("Suspend todisk = %q", got)
	}

	if calls[1].method != http.MethodPost || calls[1].path != "/api2/json/nodes/pve/qemu/101/status/resume" {
		t.Fatalf("Resume call = %s %s", calls[1].method, calls[1].path)
	}
	if len(calls[1].form) != 0 {
		t.Fatalf("Resume form = %#v", calls[1].form)
	}
}

func TestAPIBackendConfigure(t *testing.T) {
	var calls []apiRequest

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		form, _ := url.ParseQuery(string(body))
		calls = append(calls, apiRequest{
			method:   r.Method,
			path:     r.URL.Path,
			rawQuery: r.URL.RawQuery,
			form:     form,
		})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{}}`))
	}))
	defer srv.Close()

	backend := &APIBackend{
		BaseURL:    srv.URL + "/api2/json",
		Node:       "pve",
		HTTPClient: srv.Client(),
	}

	firewall := true
	cfg := VMConfig{
		Name:          "sandbox-101",
		Cores:         2,
		MemoryMB:      2048,
		Bridge:        "vmbr1",
		NetModel:      "virtio",
		Firewall:      &firewall,
		FirewallGroup: "agent_nat_default",
		CloudInit:     "local:snippets/ci.yaml",
		CPUPinning:    "0-3",
	}

	if err := backend.Configure(context.Background(), 101, cfg); err != nil {
		t.Fatalf("Configure() error = %v", err)
	}

	if len(calls) != 1 {
		t.Fatalf("expected 1 API call, got %d", len(calls))
	}
	call := calls[0]
	if call.method != http.MethodPut || call.path != "/api2/json/nodes/pve/qemu/101/config" {
		t.Fatalf("Configure call = %s %s", call.method, call.path)
	}
	if got := call.form.Get("name"); got != "sandbox-101" {
		t.Fatalf("name = %q", got)
	}
	if got := call.form.Get("cores"); got != "2" {
		t.Fatalf("cores = %q", got)
	}
	if got := call.form.Get("memory"); got != "2048" {
		t.Fatalf("memory = %q", got)
	}
	if got := call.form.Get("cpulist"); got != "0-3" {
		t.Fatalf("cpulist = %q", got)
	}
	if got := call.form.Get("net0"); got != "virtio,bridge=vmbr1,firewall=1,fwgroup=agent_nat_default" {
		t.Fatalf("net0 = %q", got)
	}
	if got := call.form.Get("cicustom"); got != "user=local:snippets/ci.yaml" {
		t.Fatalf("cicustom = %q", got)
	}
}

func TestAPIBackendConfigureRetriesWithoutFWGroup(t *testing.T) {
	var calls []apiRequest
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		form, _ := url.ParseQuery(string(body))
		calls = append(calls, apiRequest{
			method:   r.Method,
			path:     r.URL.Path,
			rawQuery: r.URL.RawQuery,
			form:     form,
		})
		w.Header().Set("Content-Type", "application/json")
		callCount++
		if callCount == 1 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"errors":{"net0.fwgroup":"property is not defined in schema and the schema does not allow additional properties"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":{}}`))
	}))
	defer srv.Close()

	backend := &APIBackend{
		BaseURL:    srv.URL + "/api2/json",
		Node:       "pve",
		HTTPClient: srv.Client(),
	}

	firewall := true
	cfg := VMConfig{
		Name:          "sandbox-101",
		Cores:         2,
		MemoryMB:      2048,
		Bridge:        "vmbr1",
		NetModel:      "virtio",
		Firewall:      &firewall,
		FirewallGroup: "agent_nat_default",
		CloudInit:     "local:snippets/ci.yaml",
	}

	if err := backend.Configure(context.Background(), 101, cfg); err != nil {
		t.Fatalf("Configure() error = %v", err)
	}
	if len(calls) != 2 {
		t.Fatalf("expected 2 API calls, got %d", len(calls))
	}
	if got := calls[0].form.Get("net0"); got != "virtio,bridge=vmbr1,firewall=1,fwgroup=agent_nat_default" {
		t.Fatalf("first net0 = %q", got)
	}
	if got := calls[1].form.Get("net0"); got != "virtio,bridge=vmbr1,firewall=1" {
		t.Fatalf("second net0 = %q", got)
	}
}

func TestAPIBackendVMConfig(t *testing.T) {
	var call apiRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		form, _ := url.ParseQuery(string(body))
		call = apiRequest{
			method:   r.Method,
			path:     r.URL.Path,
			rawQuery: r.URL.RawQuery,
			form:     form,
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"name":"vm-test","scsi1":"local-zfs:vm-101-disk-1,size=10G","cores":2}}`))
	}))
	defer srv.Close()

	backend := &APIBackend{
		BaseURL:    srv.URL + "/api2/json",
		Node:       "pve",
		HTTPClient: srv.Client(),
	}

	cfg, err := backend.VMConfig(context.Background(), 101)
	if err != nil {
		t.Fatalf("VMConfig() error = %v", err)
	}
	if cfg["name"] != "vm-test" {
		t.Fatalf("VMConfig name = %q", cfg["name"])
	}
	if cfg["scsi1"] != "local-zfs:vm-101-disk-1,size=10G" {
		t.Fatalf("VMConfig scsi1 = %q", cfg["scsi1"])
	}
	if cfg["cores"] != "2" {
		t.Fatalf("VMConfig cores = %q", cfg["cores"])
	}
	if call.method != http.MethodGet || call.path != "/api2/json/nodes/pve/qemu/101/config" {
		t.Fatalf("VMConfig call = %s %s", call.method, call.path)
	}
}

func TestAPIBackendVolumeInfo(t *testing.T) {
	var call apiRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		form, _ := url.ParseQuery(string(body))
		call = apiRequest{
			method:   r.Method,
			path:     r.URL.Path,
			rawQuery: r.URL.RawQuery,
			form:     form,
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"volid":"local-zfs:vm-0-disk-0","path":"/rpool/data/vm-0-disk-0"}}`))
	}))
	defer srv.Close()

	backend := &APIBackend{
		BaseURL:    srv.URL + "/api2/json",
		Node:       "pve",
		HTTPClient: srv.Client(),
	}

	info, err := backend.VolumeInfo(context.Background(), "local-zfs:vm-0-disk-0")
	if err != nil {
		t.Fatalf("VolumeInfo() error = %v", err)
	}
	if info.Path != "/rpool/data/vm-0-disk-0" {
		t.Fatalf("VolumeInfo path = %q", info.Path)
	}
	if info.Storage != "local-zfs" {
		t.Fatalf("VolumeInfo storage = %q", info.Storage)
	}
	if call.method != http.MethodGet || call.path != "/api2/json/nodes/pve/storage/local-zfs/content/local-zfs:vm-0-disk-0" {
		t.Fatalf("VolumeInfo call = %s %s", call.method, call.path)
	}
}

func TestAPIBackendVolumeSnapshotCreate(t *testing.T) {
	var calls []apiRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		form, _ := url.ParseQuery(string(body))
		calls = append(calls, apiRequest{
			method:   r.Method,
			path:     r.URL.Path,
			rawQuery: r.URL.RawQuery,
			form:     form,
		})
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api2/json/nodes/pve/storage/local-zfs/status" {
			_, _ = w.Write([]byte(`{"data":{"type":"zfspool"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":{}}`))
	}))
	defer srv.Close()

	backend := &APIBackend{
		BaseURL:    srv.URL + "/api2/json",
		Node:       "pve",
		HTTPClient: srv.Client(),
	}

	if err := backend.VolumeSnapshotCreate(context.Background(), "local-zfs:vm-0-disk-1", "snap1"); err != nil {
		t.Fatalf("VolumeSnapshotCreate() error = %v", err)
	}
	if len(calls) != 2 {
		t.Fatalf("expected 2 API calls, got %d", len(calls))
	}
	if calls[0].method != http.MethodGet || calls[0].path != "/api2/json/nodes/pve/storage/local-zfs/status" {
		t.Fatalf("storage status call = %s %s", calls[0].method, calls[0].path)
	}
	if calls[1].method != http.MethodPost || calls[1].path != "/api2/json/nodes/pve/storage/local-zfs/content/local-zfs:vm-0-disk-1/snapshot" {
		t.Fatalf("snapshot create call = %s %s", calls[1].method, calls[1].path)
	}
	if calls[1].form.Get("snapname") != "snap1" {
		t.Fatalf("snapname = %q", calls[1].form.Get("snapname"))
	}
}

func TestAPIBackendVolumeSnapshotRestore(t *testing.T) {
	var calls []apiRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		form, _ := url.ParseQuery(string(body))
		calls = append(calls, apiRequest{
			method:   r.Method,
			path:     r.URL.Path,
			rawQuery: r.URL.RawQuery,
			form:     form,
		})
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api2/json/nodes/pve/storage/local-zfs/status" {
			_, _ = w.Write([]byte(`{"data":{"type":"zfspool"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":{}}`))
	}))
	defer srv.Close()

	backend := &APIBackend{
		BaseURL:    srv.URL + "/api2/json",
		Node:       "pve",
		HTTPClient: srv.Client(),
	}

	if err := backend.VolumeSnapshotRestore(context.Background(), "local-zfs:vm-0-disk-1", "snap1"); err != nil {
		t.Fatalf("VolumeSnapshotRestore() error = %v", err)
	}
	if len(calls) != 2 {
		t.Fatalf("expected 2 API calls, got %d", len(calls))
	}
	if calls[1].method != http.MethodPost || calls[1].path != "/api2/json/nodes/pve/storage/local-zfs/content/local-zfs:vm-0-disk-1/snapshot/snap1/rollback" {
		t.Fatalf("snapshot restore call = %s %s", calls[1].method, calls[1].path)
	}
	if len(calls[1].form) != 0 {
		t.Fatalf("snapshot restore form = %#v", calls[1].form)
	}
}

func TestAPIBackendVolumeSnapshotDelete(t *testing.T) {
	var calls []apiRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		form, _ := url.ParseQuery(string(body))
		calls = append(calls, apiRequest{
			method:   r.Method,
			path:     r.URL.Path,
			rawQuery: r.URL.RawQuery,
			form:     form,
		})
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api2/json/nodes/pve/storage/local-zfs/status" {
			_, _ = w.Write([]byte(`{"data":{"type":"zfspool"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":{}}`))
	}))
	defer srv.Close()

	backend := &APIBackend{
		BaseURL:    srv.URL + "/api2/json",
		Node:       "pve",
		HTTPClient: srv.Client(),
	}

	if err := backend.VolumeSnapshotDelete(context.Background(), "local-zfs:vm-0-disk-1", "snap1"); err != nil {
		t.Fatalf("VolumeSnapshotDelete() error = %v", err)
	}
	if len(calls) != 2 {
		t.Fatalf("expected 2 API calls, got %d", len(calls))
	}
	if calls[1].method != http.MethodDelete || calls[1].path != "/api2/json/nodes/pve/storage/local-zfs/content/local-zfs:vm-0-disk-1/snapshot/snap1" {
		t.Fatalf("snapshot delete call = %s %s", calls[1].method, calls[1].path)
	}
}

func TestAPIBackendVolumeClone(t *testing.T) {
	var calls []apiRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		form, _ := url.ParseQuery(string(body))
		calls = append(calls, apiRequest{
			method:   r.Method,
			path:     r.URL.Path,
			rawQuery: r.URL.RawQuery,
			form:     form,
		})
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api2/json/nodes/pve/storage/local-zfs/status" {
			_, _ = w.Write([]byte(`{"data":{"type":"zfspool"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":{}}`))
	}))
	defer srv.Close()

	backend := &APIBackend{
		BaseURL:    srv.URL + "/api2/json",
		Node:       "pve",
		HTTPClient: srv.Client(),
	}

	if err := backend.VolumeClone(context.Background(), "local-zfs:vm-0-disk-1", "local-zfs:vm-0-disk-2"); err != nil {
		t.Fatalf("VolumeClone() error = %v", err)
	}
	if len(calls) != 2 {
		t.Fatalf("expected 2 API calls, got %d", len(calls))
	}
	if calls[1].method != http.MethodPost || calls[1].path != "/api2/json/nodes/pve/storage/local-zfs/content/local-zfs:vm-0-disk-1/clone" {
		t.Fatalf("clone call = %s %s", calls[1].method, calls[1].path)
	}
	if calls[1].form.Get("target") != "local-zfs:vm-0-disk-2" {
		t.Fatalf("clone target = %q", calls[1].form.Get("target"))
	}
}

func TestAPIBackendVolumeSnapshotUnsupportedStorage(t *testing.T) {
	var calls []apiRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		form, _ := url.ParseQuery(string(body))
		calls = append(calls, apiRequest{
			method:   r.Method,
			path:     r.URL.Path,
			rawQuery: r.URL.RawQuery,
			form:     form,
		})
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api2/json/nodes/pve/storage/local-lvm/status" {
			_, _ = w.Write([]byte(`{"data":{"type":"lvm"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":{}}`))
	}))
	defer srv.Close()

	backend := &APIBackend{
		BaseURL:    srv.URL + "/api2/json",
		Node:       "pve",
		HTTPClient: srv.Client(),
	}

	err := backend.VolumeSnapshotCreate(context.Background(), "local-lvm:vm-0-disk-1", "snap1")
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !errors.Is(err, ErrStorageUnsupported) {
		t.Fatalf("expected ErrStorageUnsupported, got %v", err)
	}
	if len(calls) != 1 {
		t.Fatalf("expected 1 API call, got %d", len(calls))
	}
}

func TestAPIBackendVolumeSnapshotFallbackToShell(t *testing.T) {
	var calls []apiRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		form, _ := url.ParseQuery(string(body))
		calls = append(calls, apiRequest{
			method:   r.Method,
			path:     r.URL.Path,
			rawQuery: r.URL.RawQuery,
			form:     form,
		})
		if r.URL.Path == "/api2/json/nodes/pve/storage/local-zfs/status" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":{"type":"zfspool"}}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"not implemented"}`))
	}))
	defer srv.Close()

	runner := &fakeRunner{responses: []runnerResponse{{stdout: `[{"storage":"local-zfs","type":"zfspool"}]`}, {}}}
	backend := &APIBackend{
		BaseURL:            srv.URL + "/api2/json",
		Node:               "pve",
		HTTPClient:         srv.Client(),
		AllowShellFallback: true,
		ShellFallback:      &ShellBackend{Runner: runner},
	}

	if err := backend.VolumeSnapshotCreate(context.Background(), "local-zfs:vm-0-disk-1", "snap1"); err != nil {
		t.Fatalf("VolumeSnapshotCreate() error = %v", err)
	}
	if len(calls) != 2 {
		t.Fatalf("expected 2 API calls, got %d", len(calls))
	}
	if len(runner.calls) != 2 {
		t.Fatalf("expected 2 shell calls, got %d", len(runner.calls))
	}
}

func TestAPIBackendDefaultClientVerifiesTLS(t *testing.T) {
	backend := &APIBackend{}
	client := backend.client()
	transport, ok := client.Transport.(*http.Transport)
	if !ok || transport == nil {
		t.Fatalf("expected http.Transport, got %T", client.Transport)
	}
	if transport.TLSClientConfig == nil {
		t.Fatalf("expected TLSClientConfig to be set")
	}
	if transport.TLSClientConfig.InsecureSkipVerify {
		t.Fatalf("expected TLS verification enabled by default")
	}
}

func TestNewAPIHTTPClientInsecureOverride(t *testing.T) {
	client, err := newAPIHTTPClient(0, true, "")
	if err != nil {
		t.Fatalf("newAPIHTTPClient() error = %v", err)
	}
	transport, ok := client.Transport.(*http.Transport)
	if !ok || transport == nil {
		t.Fatalf("expected http.Transport, got %T", client.Transport)
	}
	if transport.TLSClientConfig == nil {
		t.Fatalf("expected TLSClientConfig to be set")
	}
	if !transport.TLSClientConfig.InsecureSkipVerify {
		t.Fatalf("expected TLS verification disabled when insecure override is set")
	}
}
