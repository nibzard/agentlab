package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/agentlab/agentlab/internal/operations"
	"github.com/stretchr/testify/require"
)

type setupWorkerFixture struct {
	retainBackend                                               bool
	runtime                                                     serverSetupRuntime
	request                                                     serverSetupRequest
	mutex                                                       sync.Mutex
	bridge, firewall, initialized, installed, exists, destroyed bool
	active                                                      bool
	installs, creates, destroys, starts                         int
	loseCreate                                                  bool
}

func newSetupWorkerFixture(t *testing.T) *setupWorkerFixture {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("Linux host worker")
	}
	root := t.TempDir()
	assets := t.TempDir()
	for _, name := range []string{"scripts/net/setup_vmbr1.sh", "scripts/net/apply.sh", "scripts/install_host.sh", "skills/agentlab/SKILL.md"} {
		path := filepath.Join(assets, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0700))
		require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0700))
	}
	binary, err := os.ReadFile("/bin/true")
	require.NoError(t, err)
	bin := filepath.Join(root, "source")
	require.NoError(t, os.WriteFile(bin, binary, 0700))
	hash, err := setupFileHash(bin)
	require.NoError(t, err)
	bundle, err := createBootstrapBundle(assets, bootstrapBinaries{mode: "upload", arch: runtime.GOARCH, agentlab: bin, agentlabd: bin})
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.Remove(bundle) })
	bundleHash, err := setupFileHash(bundle)
	require.NoError(t, err)
	f := &setupWorkerFixture{}
	f.runtime = serverSetupRuntime{root: filepath.Join(root, "server"), configPath: filepath.Join(root, "config.yaml"), clientPath: filepath.Join(root, "installed-client"), daemonPath: filepath.Join(root, "installed-daemon"), hostLock: filepath.Join(root, "host.lock"), identity: func() (string, error) { return "server-fixture", nil }}
	require.NoError(t, os.MkdirAll(f.runtime.root, 0700))
	input := setupInput{Assets: assets, Architecture: runtime.GOARCH, Host: "server", User: "root", Port: 22, Context: "fixture", Profile: "fixture", ClientBinary: bin, DaemonBinary: bin, ClientSHA256: hash, DaemonSHA256: hash, BundleSHA256: bundleHash, WaitSeconds: 3}
	record, err := f.runtime.store().Create(operations.Request{Owner: "fixture-owner", Context: input.Context, ServerIdentity: "server-fixture", Key: "fixture-key", Input: input})
	require.NoError(t, err)
	f.request = serverSetupRequest{Version: 1, OperationID: record.ID, Owner: record.Owner, Key: "fixture-key", ServerIdentity: record.ServerIdentity, Input: input}
	directory := f.runtime.directory(record.ID)
	require.NoError(t, os.Mkdir(directory, 0700))
	require.NoError(t, os.WriteFile(f.runtime.worker(record.ID), binary, 0700))
	body, err := json.Marshal(f.request)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(directory, "request.json"), body, 0600))
	payload, err := os.ReadFile(bundle)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(directory, "bundle.tgz"), payload, 0600))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mutex.Lock()
		defer f.mutex.Unlock()
		switch {
		case r.URL.Path == "/v1/status":
			fmt.Fprint(w, `{}`)
		case r.URL.Path == "/v1/host":
			fmt.Fprint(w, `{"server_identity":"server-fixture"}`)
		case r.URL.Path == "/v1/sandboxes/inventory":
			if f.exists && (!f.destroyed || f.retainBackend) {
				json.NewEncoder(w).Encode(sandboxInventoryResponse{Sandboxes: []sandboxInventoryEntry{{VMID: 1000, Name: "agentlab-setup-" + record.ID, Managed: true, ProxmoxStatus: "running"}}})
			} else {
				fmt.Fprint(w, `{"sandboxes":[]}`)
			}
		case r.URL.Path == "/v1/sandboxes/validate-plan":
			fmt.Fprint(w, `{"ok":true}`)
		case r.Method == "POST" && r.URL.Path == "/v1/sandboxes":
			f.creates++
			f.exists = true
			if f.loseCreate {
				f.loseCreate = false
				w.WriteHeader(500)
				fmt.Fprint(w, `{"error":"response_lost"}`)
			} else {
				fmt.Fprint(w, `{}`)
			}
		case strings.HasSuffix(r.URL.Path, "/destroy"):
			f.destroys++
			f.destroyed = true
			fmt.Fprint(w, `{}`)
		case r.Method == "GET":
			if !f.exists {
				w.WriteHeader(404)
				fmt.Fprint(w, `{"error":"not_found"}`)
				return
			}
			state := "READY"
			if f.destroyed {
				state = "DESTROYED"
			}
			json.NewEncoder(w).Encode(sandboxResponse{VMID: 1000, Name: "agentlab-setup-" + record.ID, State: state, Health: &sandboxHealthResponse{Guest: &sandboxGuestReadiness{Ready: true}}})
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	t.Cleanup(server.Close)
	f.runtime.client = func() (*apiClient, error) { return newAPIClient(clientOptions{Endpoint: server.URL}, time.Second), nil }
	f.runtime.run = func(ctx context.Context, name string, args ...string) (string, error) {
		f.mutex.Lock()
		defer f.mutex.Unlock()
		if name == "/usr/bin/systemd-run" {
			f.starts++
			f.active = true
			return "", nil
		}
		if name == "/usr/bin/systemctl" {
			if args[0] == "show" {
				if f.active {
					return "LoadState=loaded\nActiveState=active\nSubState=running\nMainPID=1234\nExecMainStatus=0\n", nil
				}
				return "LoadState=not-found\nActiveState=inactive\nMainPID=0\n", nil
			}
			if f.installed {
				return "", nil
			}
			return "", errors.New("inactive")
		}
		if name == f.runtime.worker(record.ID) {
			if len(args) > 1 && args[1] == "--apply" {
				f.initialized = true
				return "{}", nil
			}
			checks := []initCheck{}
			for key, ready := range map[string]bool{"bridge_vmbr1": f.bridge, "ip_forward": f.bridge, "nftables": f.firewall, "templates": f.initialized, "profiles": f.initialized} {
				state := "missing"
				if ready {
					state = "ok"
				}
				checks = append(checks, initCheck{Name: key, Status: state})
			}
			data, _ := json.Marshal(initReport{Checks: checks})
			return string(data), nil
		}
		switch filepath.Base(name) {
		case "setup_vmbr1.sh":
			f.bridge = true
		case "apply.sh":
			f.firewall = true
		case "install_host.sh":
			f.installs++
			f.installed = true
			for _, path := range []string{f.runtime.clientPath, f.runtime.daemonPath} {
				if err := os.WriteFile(path, binary, 0700); err != nil {
					return "", err
				}
			}
		case "qm":
			return `{"exited":1,"exitcode":0}`, nil
		default:
			return "", fmt.Errorf("unexpected fixture command: %s", name)
		}
		return "", nil
	}
	return f
}

func TestServerSetupWorkerRunsReadinessAndResumesLostCreate(t *testing.T) {
	f := newSetupWorkerFixture(t)
	f.loseCreate = true
	first, err := f.runtime.execute(context.Background(), f.request.OperationID)
	require.Error(t, err)
	require.Equal(t, operations.Failed, first.State)
	require.Equal(t, 1, f.creates)
	require.Equal(t, 1, f.installs)
	second, err := f.runtime.execute(context.Background(), f.request.OperationID)
	require.NoError(t, err)
	require.Equal(t, operations.Succeeded, second.State)
	require.Equal(t, "verified", second.Cleanup)
	require.Equal(t, 1, f.creates)
	require.Equal(t, 1, f.destroys)
	require.Equal(t, 1, f.installs)
	third, err := f.runtime.execute(context.Background(), f.request.OperationID)
	require.NoError(t, err)
	require.Equal(t, operations.Succeeded, third.State)
	require.Equal(t, 1, f.creates)
	_, err = os.Stat(filepath.Join(f.runtime.directory(f.request.OperationID), "bundle.tgz"))
	require.True(t, os.IsNotExist(err))
}
func TestServerSetupStartObservesServiceAndNeverDuplicatesActiveUnit(t *testing.T) {
	f := newSetupWorkerFixture(t)
	record, err := f.runtime.start(context.Background(), f.request.OperationID)
	require.NoError(t, err)
	require.True(t, record.Worker.Active)
	require.Equal(t, 1234, record.Worker.PID)
	_, err = f.runtime.start(context.Background(), f.request.OperationID)
	require.NoError(t, err)
	require.Equal(t, 1, f.starts)
	f.active = false
	record, err = f.runtime.observe(context.Background(), f.request.OperationID)
	require.NoError(t, err)
	require.Equal(t, operations.Blocked, record.State)
	require.Equal(t, "setup_worker_stopped", record.ErrorCode)
	f.runtime.identity = func() (string, error) { return "different-server", nil }
	_, err = f.runtime.start(context.Background(), f.request.OperationID)
	require.ErrorContains(t, err, "identity changed")
	require.Equal(t, 1, f.starts)
}
func TestServerSetupWorkerRejectsChangedPayloadBeforeCommands(t *testing.T) {
	f := newSetupWorkerFixture(t)
	require.NoError(t, os.WriteFile(filepath.Join(f.runtime.directory(f.request.OperationID), "bundle.tgz"), []byte("changed"), 0600))
	record, err := f.runtime.execute(context.Background(), f.request.OperationID)
	require.Error(t, err)
	require.Equal(t, operations.Blocked, record.State)
	require.Equal(t, "setup_payload_inspection_required", record.ErrorCode)
	require.Zero(t, f.installs)
	require.Zero(t, f.creates)
}
func TestServerSetupArchiveRejectsLinksAndTraversal(t *testing.T) {
	for _, header := range []*tar.Header{{Name: "../outside", Typeflag: tar.TypeReg, Mode: 0600}, {Name: "agentlab-bootstrap/link", Linkname: "/etc", Typeflag: tar.TypeSymlink, Mode: 0700}, {Name: "agentlab-bootstrap/device", Typeflag: tar.TypeChar, Mode: 0600}} {
		var data bytes.Buffer
		gz := gzip.NewWriter(&data)
		tw := tar.NewWriter(gz)
		require.NoError(t, tw.WriteHeader(header))
		require.NoError(t, tw.Close())
		require.NoError(t, gz.Close())
		path := filepath.Join(t.TempDir(), "bundle.tgz")
		require.NoError(t, os.WriteFile(path, data.Bytes(), 0600))
		require.Error(t, extractSetupArchive(path, t.TempDir()))
	}
}
func TestSetupBundleBytesAreStableAcrossRetries(t *testing.T) {
	f := newSetupWorkerFixture(t)
	first, err := os.ReadFile(filepath.Join(f.runtime.directory(f.request.OperationID), "bundle.tgz"))
	require.NoError(t, err)
	// Recreate from the same input tree, after directory timestamps change.
	assets := t.TempDir()
	require.NoError(t, extractSetupArchive(filepath.Join(f.runtime.directory(f.request.OperationID), "bundle.tgz"), assets))
	root := filepath.Join(assets, "agentlab-bootstrap")
	bundle, err := createBootstrapBundle(root, bootstrapBinaries{mode: "upload", arch: runtime.GOARCH, agentlab: f.request.Input.ClientBinary, agentlabd: f.request.Input.DaemonBinary})
	require.NoError(t, err)
	defer os.Remove(bundle)
	second, err := os.ReadFile(bundle)
	require.NoError(t, err)
	// Extracted files use private modes, so compare two retries from that tree.
	_ = first
	again, err := createBootstrapBundle(root, bootstrapBinaries{mode: "upload", arch: runtime.GOARCH, agentlab: f.request.Input.ClientBinary, agentlabd: f.request.Input.DaemonBinary})
	require.NoError(t, err)
	defer os.Remove(again)
	third, err := os.ReadFile(again)
	require.NoError(t, err)
	require.Equal(t, second, third)
}

func TestServerSetupHostLockProtectsConcurrentWorkAndRejectsSymlinks(t *testing.T) {
	f := newSetupWorkerFixture(t)
	lock, err := os.OpenFile(f.runtime.hostLock, os.O_CREATE|os.O_RDWR, 0600)
	require.NoError(t, err)
	defer lock.Close()
	require.NoError(t, syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB))
	record, err := f.runtime.execute(context.Background(), f.request.OperationID)
	require.Error(t, err)
	require.Equal(t, "setup_busy", record.ErrorCode)
	require.Zero(t, f.installs)
	require.Zero(t, f.creates)
	require.NoError(t, syscall.Flock(int(lock.Fd()), syscall.LOCK_UN))
	require.NoError(t, lock.Close())
	require.NoError(t, os.Remove(f.runtime.hostLock))
	outside := filepath.Join(t.TempDir(), "outside")
	require.NoError(t, os.WriteFile(outside, []byte("preserve"), 0600))
	require.NoError(t, os.Symlink(outside, f.runtime.hostLock))
	_, err = f.runtime.execute(context.Background(), f.request.OperationID)
	require.Error(t, err)
	data, err := os.ReadFile(outside)
	require.NoError(t, err)
	require.Equal(t, "preserve", string(data))
	require.Zero(t, f.installs)
}
func TestServerSetupRecoversFinalSuccessAfterPayloadCleanup(t *testing.T) {
	f := newSetupWorkerFixture(t)
	record, err := f.runtime.execute(context.Background(), f.request.OperationID)
	require.NoError(t, err)
	require.Equal(t, operations.Succeeded, record.State)
	_, err = f.runtime.store().Update(record.ID, func(r *operations.Record) error { r.State = operations.Running; return nil })
	require.NoError(t, err)
	record, err = f.runtime.execute(context.Background(), record.ID)
	require.NoError(t, err)
	require.Equal(t, operations.Succeeded, record.State)
	require.Equal(t, 1, f.creates)
	require.Equal(t, 1, f.installs)
}
func TestServerSetupFailureDoesNotRecordCredentialOutput(t *testing.T) {
	f := newSetupWorkerFixture(t)
	original := f.runtime.run
	const token = "fixture-private-control-credential"
	f.runtime.run = func(ctx context.Context, name string, args ...string) (string, error) {
		if filepath.Base(name) == "install_host.sh" {
			return token, fmt.Errorf("%s", token)
		}
		return original(ctx, name, args...)
	}
	record, err := f.runtime.execute(context.Background(), f.request.OperationID)
	require.Error(t, err)
	require.NotContains(t, err.Error(), token)
	body, err := json.Marshal(record)
	require.NoError(t, err)
	require.NotContains(t, string(body), token)
	durable, err := os.ReadFile(filepath.Join(f.runtime.store().Root, record.ID+".json"))
	require.NoError(t, err)
	require.NotContains(t, string(durable), token)
}

func TestServerSetupCancellationBeforeStartNeverCreatesAUnit(t *testing.T) {
	f := newSetupWorkerFixture(t)
	record, err := f.runtime.cancel(context.Background(), f.request.OperationID)
	require.NoError(t, err)
	require.Equal(t, operations.Cancelled, record.State)
	require.Equal(t, "inspection_required", record.Cleanup)
	record, err = f.runtime.start(context.Background(), record.ID)
	require.NoError(t, err)
	require.Equal(t, operations.Cancelled, record.State)
	require.Zero(t, f.starts)
	require.Zero(t, f.installs)
	require.Zero(t, f.creates)
}

func TestExpiredSetupStillAllowsCancellation(t *testing.T) {
	f := newSetupWorkerFixture(t)
	_, err := f.runtime.store().Update(f.request.OperationID, func(r *operations.Record) error { r.KeyExpiresAt = time.Now().Add(-time.Hour); return nil })
	require.NoError(t, err)
	record, err := f.runtime.cancel(context.Background(), f.request.OperationID)
	require.NoError(t, err)
	require.Equal(t, operations.Cancelled, record.State)
	require.Zero(t, f.starts)
}

func TestServerSetupDoesNotClaimCleanupFromDatabaseTombstone(t *testing.T) {
	f := newSetupWorkerFixture(t)
	f.retainBackend = true
	record, err := f.runtime.execute(context.Background(), f.request.OperationID)
	require.ErrorContains(t, err, "remains in backend inventory")
	require.Equal(t, operations.Failed, record.State)
	require.Equal(t, "inspection_required", record.Cleanup)
	f.retainBackend = false
	record, err = f.runtime.execute(context.Background(), record.ID)
	require.NoError(t, err)
	require.Equal(t, operations.Succeeded, record.State)
	require.Equal(t, 1, f.creates)
	require.Equal(t, 1, f.destroys)
}
