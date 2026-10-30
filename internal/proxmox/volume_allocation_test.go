package proxmox

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"
)

func TestAPIBackendCreateVolumeUsesProxmoxStringReceipt(t *testing.T) {
	for _, kind := range []string{"zfspool", "dir"} {
		t.Run(kind, func(t *testing.T) {
			const base = "vm-900000001-agentlab-workspace-test"
			expected := "storage:" + base
			filename := base
			if kind == "dir" {
				filename += ".raw"
				expected = "storage:900000001/" + filename
			}
			posts := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api2/json/nodes/pve/storage/storage/status":
					if r.Method != "GET" {
						t.Errorf("unexpected method %s", r.Method)
					}
					fmt.Fprintf(w, `{"data":{"type":%q}}`, kind)
				case "/api2/json/nodes/pve/storage/storage/content":
					posts++
					if r.Method != "POST" {
						t.Errorf("unexpected method %s", r.Method)
					}
					if err := r.ParseForm(); err != nil {
						t.Error(err)
					}
					for key, value := range map[string]string{"vmid": "900000001", "filename": filename, "size": "80G", "format": "raw"} {
						if r.Form.Get(key) != value {
							t.Errorf("%s=%q, want %q", key, r.Form.Get(key), value)
						}
					}
					json.NewEncoder(w).Encode(map[string]any{"data": expected})
				default:
					t.Errorf("unexpected request %s", r.URL.String())
					w.WriteHeader(500)
				}
			}))
			defer server.Close()
			backend := &APIBackend{BaseURL: server.URL + "/api2/json", Node: "pve", HTTPClient: server.Client()}
			volume, err := backend.CreateVolume(context.Background(), "storage", base, 80)
			if err != nil || volume != expected || posts != 1 {
				t.Fatalf("allocation=%q posts=%d error=%v", volume, posts, err)
			}
		})
	}
}

func TestVolumeAllocationRejectsInvalidInputsBeforeIO(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); w.WriteHeader(500) }))
	defer server.Close()

	for _, name := range []string{"workspace-test", "vm-0-test", "vm-99-test", "vm-1000000000-test", "vm-0100-test", "vm-100-../test", "vm-100-test.raw", "vm-100-test;echo"} {
		runner := &fakeRunner{}
		shell := &ShellBackend{Runner: runner, Node: "pve"}
		if _, err := shell.CreateVolume(context.Background(), "storage", name, 10); err == nil {
			t.Fatalf("accepted %q", name)
		}
		if len(runner.calls) != 0 {
			t.Fatalf("invalid name issued commands: %q", name)
		}
		api := &APIBackend{BaseURL: server.URL, HTTPClient: server.Client(), Node: "pve"}
		if _, err := api.CreateVolume(context.Background(), "storage", name, 10); err == nil {
			t.Fatalf("API accepted %q", name)
		}
		if requests.Load() != 0 {
			t.Fatalf("invalid name caused API calls: %q", name)
		}
	}
}

func TestVolumeAllocationReceiptMismatchDoesNotRetry(t *testing.T) {
	for _, receipt := range []string{`{"volid":"storage:vm-900000001-test"}`, `"storage:vm-900000002-other"`, `""`, `null`, `successfully created 'storage:vm-900000001-test'`} {
		runner := &fakeRunner{responses: []runnerResponse{{stdout: `{"type":"zfspool"}`}, {stdout: receipt}}}
		backend := &ShellBackend{Runner: runner, Node: "pve"}
		if _, err := backend.CreateVolume(context.Background(), "storage", "vm-900000001-test", 10); err == nil {
			t.Fatalf("accepted receipt %s", receipt)
		}
		if len(runner.calls) != 2 {
			t.Fatalf("receipt failure repeated allocation: %#v", runner.calls)
		}
	}
}

func TestClusterVolumeOwnerInventoryIncludesContainersAndOtherNodes(t *testing.T) {
	payload := `[{"vmid":900000001,"type":"qemu","node":"pve-one"},{"vmid":900000002,"type":"lxc","node":"pve-two"}]`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api2/json/cluster/resources" || r.URL.Query().Get("type") != "vm" {
			t.Errorf("unexpected URL %s", r.URL.String())
		}
		fmt.Fprint(w, `{"data":`+payload+`}`)
	}))
	defer server.Close()
	api := &APIBackend{BaseURL: server.URL + "/api2/json", HTTPClient: server.Client()}
	ids, err := api.VolumeOwnerVMIDs(context.Background())
	if err != nil || !reflect.DeepEqual(ids, []VMID{900000001, 900000002}) {
		t.Fatalf("inventory=%v error=%v", ids, err)
	}
	runner := &fakeRunner{responses: []runnerResponse{{stdout: payload}}}
	shell := &ShellBackend{Runner: runner}
	ids, err = shell.VolumeOwnerVMIDs(context.Background())
	if err != nil || !reflect.DeepEqual(ids, []VMID{900000001, 900000002}) {
		t.Fatalf("shell inventory=%v error=%v", ids, err)
	}
	want := []runnerCall{{name: "pvesh", args: []string{"get", "/cluster/resources", "--type", "vm", "--output-format", "json"}}}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("inventory calls=%#v", runner.calls)
	}
}

func TestFakeBackendDoesNotRenameDuplicateAllocation(t *testing.T) {
	backend := NewFakeBackend()
	name := "vm-900000001-agentlab-workspace-duplicate"
	volume, err := backend.CreateVolume(context.Background(), "storage", name, 10)
	if err != nil || volume != "storage:"+name {
		t.Fatalf("allocation=%q error=%v", volume, err)
	}
	if _, err := backend.CreateVolume(context.Background(), "storage", name, 10); err == nil {
		t.Fatal("duplicate allocation silently renamed")
	}
}

func TestWorkspaceVolumeOwnerFromIDPreservesLegacyIDs(t *testing.T) {
	for _, id := range []string{"local-zfs:vm-0-disk-1", "storage:900000001/vm-900000002-agentlab-workspace-other.raw", "storage:vm-900000001-unmanaged", "storage:../../vm-900000001-agentlab-workspace-other"} {
		if _, _, ok := WorkspaceVolumeOwnerFromID(id); ok {
			t.Fatalf("claimed generated owner for %s", id)
		}
	}
	for _, id := range []string{"storage:vm-900000001-agentlab-workspace-a", "storage:900000001/vm-900000001-agentlab-workspace-a.raw"} {
		owner, name, ok := WorkspaceVolumeOwnerFromID(id)
		if !ok || owner != 900000001 || name != "vm-900000001-agentlab-workspace-a" {
			t.Fatalf("owner decode: %d %q %t", owner, name, ok)
		}
	}
}
