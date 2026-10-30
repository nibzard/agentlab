package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agentlab/agentlab/internal/connection"
	"github.com/agentlab/agentlab/internal/operations"
	"github.com/stretchr/testify/require"
)

func setupSSHFixture(t *testing.T) *bootstrapSSHClient {
	t.Helper()
	dir := t.TempDir()
	script := `#!/usr/bin/env bash
remote="${!#}"
case "$remote" in
 *pvesh*) printf '8100\n' ;;
 *qm*guest*exec*) printf '{"exited":1,"exitcode":0}\n' ;;
 *) remote="${remote//\/run\/lock\/agentlab-setup.lock/$SETUP_TEST_LOCK}"; exec bash -c "$remote" ;;
esac
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "ssh"), []byte(script), 0700))
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("SETUP_TEST_LOCK", filepath.Join(dir, "server.lock"))
	return &bootstrapSSHClient{target: "fixture"}
}

func TestSetupReadinessResumesLostCreateAndCleansOwnedSandbox(t *testing.T) {
	useTempClientConfig(t)
	ssh := setupSSHFixture(t)
	const operationID = "0123456789abcdef0123456789abcdef"
	exists := false
	creates := 0
	destroys := 0
	destroyed := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/sandboxes/inventory":
			entries := []sandboxInventoryEntry{{VMID: 8099}}
			if exists && !destroyed {
				entries = append(entries, sandboxInventoryEntry{VMID: 8100, Name: "agentlab-setup-" + operationID, Managed: true, ProxmoxStatus: "running"})
			}
			json.NewEncoder(w).Encode(sandboxInventoryResponse{Sandboxes: entries})
		case r.Method == "POST" && r.URL.Path == "/v1/sandboxes/validate-plan":
			fmt.Fprint(w, `{"ok":true}`)
		case r.Method == "POST" && r.URL.Path == "/v1/sandboxes":
			creates++
			exists = true
			w.WriteHeader(500)
			fmt.Fprint(w, `{"error":"response_lost"}`)
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/destroy"):
			destroys++
			destroyed = true
			fmt.Fprint(w, `{}`)
		case r.Method == "GET":
			if !exists {
				w.WriteHeader(404)
				fmt.Fprint(w, `{"error":"not_found"}`)
				return
			}
			state := "READY"
			if destroyed {
				state = "DESTROYED"
			}
			json.NewEncoder(w).Encode(sandboxResponse{VMID: 8100, Name: "agentlab-setup-" + operationID, State: state, Health: &sandboxHealthResponse{Guest: &sandboxGuestReadiness{Ready: true}}})
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	defer server.Close()
	contexts, err := contextStore()
	require.NoError(t, err)
	require.NoError(t, contexts.Put("test", connection.Settings{Endpoint: server.URL, Token: "fixture"}))
	record := operations.Record{ID: operationID}
	var durable []byte
	save := func() error { var err error; durable, err = json.Marshal(record); return err }
	input := setupInput{Context: "test", Profile: "test"}
	bootstrap := bootstrapOptions{sshUser: "root"}
	err = testSetupSandbox(context.Background(), &record, save, input, bootstrap, ssh, operationID, time.Second)
	require.Error(t, err)
	require.NoError(t, json.Unmarshal(durable, &record))
	require.NoError(t, testSetupSandbox(context.Background(), &record, save, input, bootstrap, ssh, operationID, time.Second))
	require.Equal(t, 1, creates)
	require.Equal(t, "verified", record.Resources[0].State)
	require.NoError(t, cleanupSetupSandbox(context.Background(), &record, "test", operationID))
	require.Equal(t, 1, destroys)
	require.Equal(t, "verified_cleaned", record.Resources[0].State)
	require.NoError(t, cleanupSetupSandbox(context.Background(), &record, "test", operationID))
	require.Equal(t, 1, destroys)
}

func TestSetupCleanupPreservesOtherSandbox(t *testing.T) {
	useTempClientConfig(t)
	mutations := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			mutations++
		}
		fmt.Fprint(w, `{"vmid":8100,"name":"user-work","state":"READY"}`)
	}))
	defer server.Close()
	contexts, err := contextStore()
	require.NoError(t, err)
	require.NoError(t, contexts.Put("test", connection.Settings{Endpoint: server.URL}))
	record := operations.Record{Resources: []operations.Resource{{Kind: "readiness_sandbox", ID: "8100", State: "created"}}}
	require.ErrorContains(t, cleanupSetupSandbox(context.Background(), &record, "test", "different-operation"), "not owned")
	require.Equal(t, 0, mutations)
}

func TestSetupRejectsDifferentAPIServer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"server_identity":"server_other"}`) }))
	defer server.Close()
	client := newAPIClient(clientOptions{Endpoint: server.URL}, time.Second)
	err := verifySetupIdentity(context.Background(), client, "server_expected")
	var required *operations.Requirement
	require.ErrorAs(t, err, &required)
	require.Equal(t, "server_identity_mismatch", required.Code)
}
