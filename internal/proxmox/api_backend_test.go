package proxmox

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
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

	if len(calls) != 3 {
		t.Fatalf("expected 3 API calls, got %d", len(calls))
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
