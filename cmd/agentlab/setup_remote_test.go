package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/agentlab/agentlab/internal/connection"
	"github.com/agentlab/agentlab/internal/hostidentity"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/agentlab/agentlab/internal/operations"
	"github.com/stretchr/testify/require"
)

func TestSetupImmutableUploadUsesLiteralArgumentsAndPreservesExistingInput(t *testing.T) {
	for _, dep := range []string{"sh", "flock", "sha256sum", "base64", "sync"} {
		if _, err := exec.LookPath(dep); err != nil {
			t.Skip(dep + " required")
		}
	}
	root := filepath.Join(t.TempDir(), "private ' $path")
	require.NoError(t, os.Chmod(filepath.Dir(root), 0700))
	directory := filepath.Join(root, "operation")
	target := filepath.Join(directory, "request.json")
	data := []byte("input $(touch unexpected) `touch unexpected`\n")
	hash := sha256.Sum256(data)
	run := func(digest string) error {
		args := setupScriptArgs(setupUploadDirectoryScript, root, directory, target, digest, "600", strconv.Itoa(os.Geteuid()))
		_, err := formatRemoteCommand(args)
		require.NoError(t, err)
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Stdin = bytes.NewReader(data)
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Logf("immutable upload: %s", output)
		}
		return err
	}
	require.NoError(t, run(hex.EncodeToString(hash[:])))
	require.NoError(t, run(hex.EncodeToString(hash[:])))
	require.Error(t, run(hex.EncodeToString(make([]byte, 32))))
	actual, err := os.ReadFile(target)
	require.NoError(t, err)
	require.Equal(t, data, actual)
	require.NoError(t, os.Remove(target))
	outside := filepath.Join(t.TempDir(), "outside")
	require.NoError(t, os.WriteFile(outside, []byte("preserve"), 0600))
	require.NoError(t, os.Symlink(outside, target))
	require.Error(t, run(hex.EncodeToString(hash[:])))
	actual, err = os.ReadFile(outside)
	require.NoError(t, err)
	require.Equal(t, "preserve", string(actual))
}
func TestSetupReferencePreservesInputBindingAndRejectsChanges(t *testing.T) {
	f := newSetupWorkerFixture(t)
	local := operations.Store{Root: filepath.Join(t.TempDir(), "operations")}
	original, err := local.Create(operations.Request{Owner: f.request.Owner, Context: f.request.Input.Context, ServerIdentity: f.request.ServerIdentity, Key: f.request.Key, Input: f.request.Input})
	require.NoError(t, err)
	require.Equal(t, f.request.OperationID, original.ID)
	require.NoError(t, saveSetupReference(local, f.request))
	require.NoError(t, saveSetupReference(local, f.request))
	read, err := readSetupReference(local, original.ID)
	require.NoError(t, err)
	require.Equal(t, f.request, read)
	changed := f.request
	changed.Input.Host = "different"
	require.ErrorIs(t, saveSetupReference(local, changed), operations.ErrConflict)
	data, err := json.Marshal(changed)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(setupReferencePath(local, original.ID), data, 0600))
	_, err = readSetupReference(local, original.ID)
	require.ErrorContains(t, err, "binding changed")
}
func TestSetupObservationTimeoutDoesNotCancelServer(t *testing.T) {
	f := newSetupWorkerFixture(t)
	// The transport is cancelled before any response. Its persisted operation
	// remains available; no local cancellation file is written.
	local := f.runtime.store()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ssh := &bootstrapSSHClient{target: "unused"}
	record, err := waitRemoteSetup(ctx, local, f.request, ssh, bootstrapOptions{}, time.Second)
	require.Error(t, err)
	require.Equal(t, f.request.OperationID, record.ID)
	_, err = os.Stat(filepath.Join(local.Root, record.ID+".cancel"))
	require.True(t, os.IsNotExist(err))
	saved, err := local.Read(record.ID)
	require.NoError(t, err)
	require.Equal(t, operations.Planned, saved.State)
}

func TestSetupResumeRequiresTheOriginalPayload(t *testing.T) {
	f := newSetupWorkerFixture(t)
	bundle, err := setupResumeBundle(f.request)
	require.NoError(t, err)
	defer os.Remove(bundle)
	hash, err := setupFileHash(bundle)
	require.NoError(t, err)
	require.Equal(t, f.request.Input.BundleSHA256, hash)
	script := filepath.Join(f.request.Input.Assets, "scripts/install_host.sh")
	require.NoError(t, os.WriteFile(script, []byte("changed"), 0700))
	_, err = setupResumeBundle(f.request)
	require.ErrorContains(t, err, "original pinned setup assets")
}

func TestSetupResumePublishesConnectionOnlyAfterIdentityVerification(t *testing.T) {
	for _, match := range []bool{true, false} {
		t.Run(strconv.FormatBool(match), func(t *testing.T) {
			previous := clientConfigRootOverride
			clientConfigRootOverride = t.TempDir()
			t.Cleanup(func() { clientConfigRootOverride = previous })
			t.Setenv("AGENTLAB_CREDENTIAL_STORE", "file")
			const machine = "0123456789abcdef0123456789abcdef"
			identity, err := hostidentity.FromMachineID(machine)
			require.NoError(t, err)
			const token = "fixture-generated-control-token"
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "Bearer "+token, r.Header.Get("Authorization"))
				if r.URL.Path == "/v1/host" {
					value := identity
					if !match {
						value = "different-server"
					}
					_ = json.NewEncoder(w).Encode(map[string]string{"server_identity": value})
					return
				}
				_, _ = w.Write([]byte(`{}`))
			}))
			defer server.Close()
			contexts, err := contextStore()
			require.NoError(t, err)
			original := connection.Settings{Endpoint: "https://working.example", Token: "working-token", ServerIdentity: "working-server"}
			require.NoError(t, contexts.PutContext(context.Background(), "destination", original))
			require.NoError(t, contexts.Use("destination"))
			input := setupInput{Host: "root@server.example", User: "root", Port: 22, Context: "destination", Architecture: runtime.GOARCH, ClientSHA256: strings.Repeat("a", 64), ClientBinary: "/missing/original-client", Assets: "/missing/original-assets", WaitSeconds: 3}
			store := operations.Store{Root: filepath.Join(t.TempDir(), "operations")}
			record, err := store.Create(operations.Request{Owner: "caller", Context: input.Context, ServerIdentity: identity, Key: "key", Input: input})
			require.NoError(t, err)
			request := serverSetupRequest{Version: 1, OperationID: record.ID, Owner: record.Owner, Key: "key", ServerIdentity: identity, Input: input}
			require.NoError(t, saveSetupReference(store, request))
			record.State = operations.Succeeded
			record.Cleanup = "verified"
			record.Worker = &operations.WorkerObservation{Kind: "systemd", Unit: setupUnit(record.ID), State: "absent", ObservedAt: time.Now().UTC()}
			fixture := t.TempDir()
			recordPath := filepath.Join(fixture, "record.json")
			data, err := json.Marshal(record)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(recordPath, data, 0600))
			initPath := filepath.Join(fixture, "init.json")
			data, err = json.Marshal(initReport{ConnectCommand: "agentlab connect --endpoint " + server.URL + " --token " + token})
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(initPath, data, 0600))
			script := `#!/usr/bin/env bash
remote="${!#}"
case "$remote" in
 *machine-id*) printf '0123456789abcdef0123456789abcdef\n' ;;
 *__setup-worker*show*) cat "$AGENTLAB_TEST_SETUP_RECORD" ;;
 *init*--json*) cat "$AGENTLAB_TEST_SETUP_INIT" ;;
 *journal*) printf present ;;
 *) exit 88 ;;
esac
`
			require.NoError(t, os.WriteFile(filepath.Join(fixture, "ssh"), []byte(script), 0700))
			t.Setenv("PATH", fixture+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("AGENTLAB_TEST_SETUP_RECORD", recordPath)
			t.Setenv("AGENTLAB_TEST_SETUP_INIT", initPath)
			output := captureStdout(t, func() {
				err := runRemoteOperationCommand(context.Background(), store, request, "resume", commonFlags{jsonOutput: true})
				if match {
					require.NoError(t, err)
				} else {
					var pending *setupRemoteError
					require.ErrorAs(t, err, &pending)
					require.Equal(t, "server_identity_mismatch", pending.Code)
				}
			})
			require.NotContains(t, output, token)
			saved, _, found, err := contexts.Resolve("destination")
			require.NoError(t, err)
			require.True(t, found)
			observed, err := store.Read(record.ID)
			require.NoError(t, err)
			if match {
				require.Equal(t, token, saved.Token)
				require.Equal(t, "server.example", saved.JumpHost)
				require.Equal(t, "verified", observed.ClientConnection)
				require.NotNil(t, observed.ClientConnectionVerifiedAt)
			} else {
				require.Equal(t, original.Token, saved.Token)
				require.Equal(t, original.ServerIdentity, saved.ServerIdentity)
				require.Equal(t, "pending", observed.ClientConnection)
			}
			metadata, err := os.ReadFile(filepath.Join(store.Root, record.ID+".json"))
			require.NoError(t, err)
			require.NotContains(t, string(metadata), token)
		})
	}
}

func TestExpiredSetupDoesNotStartRemoteWork(t *testing.T) {
	f := newSetupWorkerFixture(t)
	_, err := f.runtime.store().Update(f.request.OperationID, func(r *operations.Record) error { r.KeyExpiresAt = time.Now().Add(-time.Hour); return nil })
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = executeRemoteSetup(ctx, f.runtime.store(), f.request, "", false, time.Second)
	require.ErrorIs(t, err, operations.ErrExpired)
}

func TestSetupWorkerInvocationChecksOwnerAndDigestBeforeExecution(t *testing.T) {
	root := filepath.Join(t.TempDir(), "private")
	require.NoError(t, os.Chmod(filepath.Dir(root), 0700))
	directory := filepath.Join(root, "operation")
	require.NoError(t, os.MkdirAll(directory, 0700))
	marker := filepath.Join(t.TempDir(), "executed")
	worker := filepath.Join(directory, "worker")
	script := []byte("#!/bin/sh\nprintf executed > \"$1\"\n")
	require.NoError(t, os.WriteFile(worker, script, 0700))
	digest := sha256.Sum256(script)
	run := func(hash string) error {
		args := setupScriptArgs(verifySetupWorkerScript, root, directory, worker, hash, strconv.Itoa(os.Geteuid()), marker)
		_, err := formatRemoteCommand(args)
		require.NoError(t, err)
		return exec.Command(args[0], args[1:]...).Run()
	}
	require.Error(t, run(strings.Repeat("0", 64)))
	_, err := os.Stat(marker)
	require.True(t, os.IsNotExist(err))
	require.NoError(t, run(hex.EncodeToString(digest[:])))
	actual, err := os.ReadFile(marker)
	require.NoError(t, err)
	require.Equal(t, "executed", string(actual))
	require.NoError(t, os.Remove(marker))
	require.NoError(t, os.Chmod(root, 0777))
	require.Error(t, run(hex.EncodeToString(digest[:])))
	_, err = os.Stat(marker)
	require.True(t, os.IsNotExist(err))
}
