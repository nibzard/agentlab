package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/agentlab/agentlab/internal/operations"
	"github.com/stretchr/testify/require"
)

type setupProcessFixture struct{ Root, Config, Client, Daemon, Lock, Endpoint, ID, Gate string }

// This helper runs in real child processes. It substitutes host commands only;
// execution, journals, locks, HTTP calls, cancellation, and cleanup use real code.
func TestSetupDetachedProcessHelper(t *testing.T) {
	role := os.Getenv("AGENTLAB_TEST_SETUP_PROCESS_ROLE")
	if role == "" {
		return
	}
	data, err := os.ReadFile(os.Getenv("AGENTLAB_TEST_SETUP_PROCESS_FILE"))
	if err != nil {
		os.Exit(91)
	}
	var fixture setupProcessFixture
	if json.Unmarshal(data, &fixture) != nil {
		os.Exit(92)
	}
	if role == "launcher" {
		worker := exec.Command(os.Args[0], "-test.run=^TestSetupDetachedProcessHelper$")
		worker.Env = append(os.Environ(), "AGENTLAB_TEST_SETUP_PROCESS_ROLE=worker")
		worker.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		if worker.Start() != nil {
			os.Exit(93)
		}
		if os.WriteFile(fixture.Gate+".pid", []byte(strconv.Itoa(worker.Process.Pid)), 0600) != nil {
			os.Exit(94)
		}
		_ = worker.Process.Release()
		time.Sleep(time.Hour)
		os.Exit(95)
	}
	runtime := serverSetupRuntime{root: fixture.Root, configPath: fixture.Config, clientPath: fixture.Client, daemonPath: fixture.Daemon, hostLock: fixture.Lock, identity: func() (string, error) { return "server-fixture", nil }, client: func() (*apiClient, error) {
		return newAPIClient(clientOptions{Endpoint: fixture.Endpoint}, time.Second), nil
	}}
	runtime.run = func(ctx context.Context, name string, args ...string) (string, error) {
		if name == runtime.worker(fixture.ID) {
			checks := []initCheck{}
			for _, key := range []string{"bridge_vmbr1", "ip_forward", "nftables", "templates", "profiles"} {
				checks = append(checks, initCheck{Name: key, Status: "ok"})
			}
			data, _ := json.Marshal(initReport{Checks: checks})
			return string(data), nil
		}
		if name == "/usr/bin/systemctl" {
			return "", nil
		}
		if filepath.Base(name) == "qm" {
			if err := os.WriteFile(fixture.Gate+".arrived", []byte("ready"), 0600); err != nil {
				return "", err
			}
			ticker := time.NewTicker(10 * time.Millisecond)
			defer ticker.Stop()
			for {
				if _, err := os.Stat(fixture.Gate); err == nil {
					return `{"exited":1,"exitcode":0}`, nil
				}
				select {
				case <-ctx.Done():
					return "", ctx.Err()
				case <-ticker.C:
				}
			}
		}
		return "", fmt.Errorf("unexpected process fixture command: %s", name)
	}
	_, err = runtime.execute(context.Background(), fixture.ID)
	if err != nil {
		os.Exit(96)
	}
	os.Exit(0)
}
func setupProcessCommand(t *testing.T, f *setupWorkerFixture, role string) (*exec.Cmd, setupProcessFixture) {
	t.Helper()
	client, err := f.runtime.client()
	require.NoError(t, err)
	fixture := setupProcessFixture{Root: f.runtime.root, Config: f.runtime.configPath, Client: f.runtime.clientPath, Daemon: f.runtime.daemonPath, Lock: f.runtime.hostLock, Endpoint: client.baseURL, ID: f.request.OperationID, Gate: filepath.Join(f.runtime.root, "gate")}
	// All installed read-only checks pass. The process reaches real readiness
	// allocation before it pauses in the substituted guest command.
	data, err := os.ReadFile(f.request.Input.ClientBinary)
	require.NoError(t, err)
	for _, path := range []string{fixture.Client, fixture.Daemon} {
		require.NoError(t, os.WriteFile(path, data, 0700))
	}
	metadata := filepath.Join(f.runtime.root, "process.json")
	data, err = json.Marshal(fixture)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(metadata, data, 0600))
	cmd := exec.Command(os.Args[0], "-test.run=^TestSetupDetachedProcessHelper$")
	cmd.Env = append(os.Environ(), "AGENTLAB_TEST_SETUP_PROCESS_ROLE="+role, "AGENTLAB_TEST_SETUP_PROCESS_FILE="+metadata)
	return cmd, fixture
}
func waitSetupCondition(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("setup process condition did not complete")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
func TestServerSetupContinuesAfterClientProcessIsKilled(t *testing.T) {
	f := newSetupWorkerFixture(t)
	launcher, fixture := setupProcessCommand(t, f, "launcher")
	require.NoError(t, launcher.Start())
	t.Cleanup(func() {
		if launcher.ProcessState == nil {
			_ = launcher.Process.Kill()
			_ = launcher.Wait()
		}
	})
	waitSetupCondition(t, func() bool { _, err := os.Stat(fixture.Gate + ".arrived"); return err == nil })
	data, err := os.ReadFile(fixture.Gate + ".pid")
	require.NoError(t, err)
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	require.NoError(t, err)
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	require.NoError(t, syscall.Kill(pid, 0))
	require.NoError(t, launcher.Process.Kill())
	require.Error(t, launcher.Wait())
	require.NoError(t, syscall.Kill(pid, 0), "worker stopped with its client")
	require.NoError(t, os.WriteFile(fixture.Gate, []byte("continue"), 0600))
	waitSetupCondition(t, func() bool {
		record, err := f.runtime.store().Read(fixture.ID)
		return err == nil && record.State == operations.Succeeded
	})
	record, err := f.runtime.store().Read(fixture.ID)
	require.NoError(t, err)
	require.Equal(t, "verified", record.Cleanup)
	f.mutex.Lock()
	defer f.mutex.Unlock()
	require.Equal(t, 1, f.creates)
	require.Equal(t, 1, f.destroys)
}
func TestServerSetupRecoversAfterWorkerProcessIsKilled(t *testing.T) {
	f := newSetupWorkerFixture(t)
	worker, fixture := setupProcessCommand(t, f, "worker")
	require.NoError(t, worker.Start())
	t.Cleanup(func() {
		if worker.ProcessState == nil {
			_ = worker.Process.Kill()
			_ = worker.Wait()
		}
	})
	waitSetupCondition(t, func() bool { _, err := os.Stat(fixture.Gate + ".arrived"); return err == nil })
	require.NoError(t, worker.Process.Kill())
	require.Error(t, worker.Wait())
	interrupted, err := f.runtime.store().Read(fixture.ID)
	require.NoError(t, err)
	require.Equal(t, operations.Running, interrupted.State)
	require.NoError(t, os.WriteFile(fixture.Gate, []byte("continue"), 0600))
	restarted, _ := setupProcessCommand(t, f, "worker")
	output, err := restarted.CombinedOutput()
	require.NoError(t, err, string(output))
	record, err := f.runtime.store().Read(fixture.ID)
	require.NoError(t, err)
	require.Equal(t, operations.Succeeded, record.State)
	f.mutex.Lock()
	defer f.mutex.Unlock()
	require.Equal(t, 1, f.creates)
	require.Equal(t, 1, f.destroys)
}
func TestServerSetupCancellationStopsWorkerAndCleansReadiness(t *testing.T) {
	f := newSetupWorkerFixture(t)
	worker, fixture := setupProcessCommand(t, f, "worker")
	require.NoError(t, worker.Start())
	t.Cleanup(func() {
		if worker.ProcessState == nil {
			_ = worker.Process.Kill()
			_ = worker.Wait()
		}
	})
	waitSetupCondition(t, func() bool { _, err := os.Stat(fixture.Gate + ".arrived"); return err == nil })
	require.NoError(t, f.runtime.store().Cancel(fixture.ID))
	require.Error(t, worker.Wait())
	record, err := f.runtime.store().Read(fixture.ID)
	require.NoError(t, err)
	require.Equal(t, operations.Cancelled, record.State)
	f.mutex.Lock()
	defer f.mutex.Unlock()
	require.Equal(t, 1, f.creates)
	require.Equal(t, 1, f.destroys)
}
