package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/agentlab/agentlab/internal/auth"
	"github.com/agentlab/agentlab/internal/connection"
	"github.com/agentlab/agentlab/internal/daemon"
	"github.com/agentlab/agentlab/internal/db"
	"github.com/agentlab/agentlab/internal/hostidentity"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
)

// The parent owns the server. The child uses real files, locks, credentials,
// and HTTP calls. No production crash hook is needed.
func TestCredentialRenewalProcessHelper(t *testing.T) {
	root := os.Getenv("AGENTLAB_TEST_RENEWAL_ROOT")
	if root == "" {
		return
	}
	require.True(t, filepath.IsAbs(root))
	clientConfigRootOverride = root
	args := []string{"--context", "task", "--request-id", "process-rotation", "--json"}
	if key := os.Getenv("AGENTLAB_TEST_RENEWAL_KEY"); key != "" {
		args = append(args, "--key", key)
	}
	if err := runTokenRenew(context.Background(), args, commonFlags{timeout: 20 * time.Second}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		var pending *renewalRequirement
		if errors.As(err, &pending) {
			fmt.Fprintln(os.Stderr, pending.Cause)
		}
		os.Exit(41)
	}
}

func TestCredentialRenewalAfterClientTermination(t *testing.T) {
	for _, phase := range []string{"before_prepare", "prepared", "committed"} {
		t.Run(phase, func(t *testing.T) {
			t.Setenv("AGENTLAB_ENDPOINT", "")
			t.Setenv("AGENTLAB_TOKEN", "")
			t.Setenv("AGENTLAB_CREDENTIAL_STORE", "file")
			root := t.TempDir()
			_, private, err := ed25519.GenerateKey(rand.Reader)
			require.NoError(t, err)
			signer, err := ssh.NewSignerFromKey(private)
			require.NoError(t, err)
			keys, err := auth.ParseAuthorizedKeys(ssh.MarshalAuthorizedKey(signer.PublicKey()))
			require.NoError(t, err)
			database, err := db.Open(filepath.Join(t.TempDir(), "daemon.db"))
			require.NoError(t, err)
			defer database.Close()
			mux := http.NewServeMux()
			daemon.NewControlAPI(database, nil, nil, nil, nil, "", nil).Register(mux)
			middleware, err := auth.NewMiddlewareWithStore(keys, "", nil)
			require.NoError(t, err)
			handler := middleware.WithRevocationChecker(database).WrapNetwork(mux)
			arrived, release := make(chan struct{}), make(chan struct{})
			var gate, releaseGate sync.Once
			unblock := func() { releaseGate.Do(func() { close(release) }) }
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, r)
				pause := (phase == "before_prepare" && r.URL.Path == "/v1/token-renewals/commit" && response.Code == 404) ||
					(phase == "prepared" && r.URL.Path == "/v1/token-renewals/prepare" && response.Code == 200) ||
					(phase == "committed" && r.URL.Path == "/v1/token-renewals/commit" && response.Code == 200)
				if pause {
					gate.Do(func() { close(arrived); <-release })
				}
				for key, values := range response.Header() {
					w.Header()[key] = values
				}
				w.WriteHeader(response.Code)
				_, _ = w.Write(response.Body.Bytes())
			}))
			defer server.Close()
			// Release blocked handlers before closing the server on failure.
			defer unblock()
			identity, err := hostidentity.Local()
			require.NoError(t, err)
			parent, err := auth.CreateToken(signer, auth.TokenCreateRequest{Commands: []string{"schema.read", "token.renew"}, Scope: []string{"sandbox:1001"}, Subject: "worker", TTL: time.Hour})
			require.NoError(t, err)
			store := connection.ContextStore{Root: filepath.Join(root, clientConfigDir), CredentialMode: "file"}
			require.NoError(t, store.Put("task", connection.Settings{Endpoint: server.URL, Token: parent, ServerIdentity: identity}))
			block, err := ssh.MarshalPrivateKey(private, "")
			require.NoError(t, err)
			keyPath := filepath.Join(t.TempDir(), "operator-key")
			require.NoError(t, os.WriteFile(keyPath, pem.EncodeToMemory(block), 0600))
			command := func(key string) (*exec.Cmd, *bytes.Buffer) {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				t.Cleanup(cancel)
				cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCredentialRenewalProcessHelper$")
				if client := os.Getenv("AGENTLAB_TEST_CLIENT"); client != "" {
					require.True(t, filepath.IsAbs(client))
					args := []string{"token", "renew", "--context", "task", "--request-id", "process-rotation", "--json"}
					if key != "" {
						args = append(args, "--key", key)
					}
					cmd = exec.CommandContext(ctx, client, args...)
				}
				cmd.Env = append(os.Environ(), "AGENTLAB_TEST_RENEWAL_ROOT="+root, "AGENTLAB_TEST_RENEWAL_KEY="+key)
				cmd.Env = append(cmd.Env, "AGENTLAB_CONFIG_DIR="+store.Root)
				var output bytes.Buffer
				cmd.Stdout, cmd.Stderr = &output, &output
				return cmd, &output
			}
			child, output := command(keyPath)
			require.NoError(t, child.Start())
			defer func() {
				if child.ProcessState == nil {
					_ = child.Process.Kill()
					_ = child.Wait()
				}
			}()
			select {
			case <-arrived:
			case <-time.After(15 * time.Second):
				_ = child.Process.Kill()
				_ = child.Wait()
				t.Fatalf("renewal client did not reach the server checkpoint: %s", output.String())
			}
			journal := filepath.Join(store.Root, "credential-renewals", credentialHash("task")+"-"+credentialHash("process-rotation")+".json")
			pending, err := loadRenewal(journal)
			require.NoError(t, err)
			require.Equal(t, "staged", pending.State)
			staged, _, _, err := store.Resolve(pending.Stage.Name)
			require.NoError(t, err)
			require.Equal(t, pending.CandidateSHA256, credentialHash(staged.Token))
			cfg, _, _, err := store.Resolve("task")
			require.NoError(t, err)
			require.Equal(t, parent, cfg.Token)
			require.NoError(t, child.Process.Kill())
			require.Error(t, child.Wait())
			unblock()
			require.NoError(t, os.Remove(keyPath))
			require.NotContains(t, output.String(), parent)
			require.NotContains(t, output.String(), staged.Token)
			resume, resumedOutput := command("")
			require.NoError(t, resume.Run(), "recovery failed: %s", resumedOutput.String())
			completed, err := loadRenewal(journal)
			require.NoError(t, err)
			require.Equal(t, "published", completed.State)
			require.Equal(t, "verified", completed.Cleanup)
			require.Equal(t, pending.CandidateID, completed.CandidateID)
			require.Equal(t, pending.CandidateSHA256, completed.CandidateSHA256)
			require.Equal(t, "committed", completed.Receipt.State)
			cfg, _, _, err = store.Resolve("task")
			require.NoError(t, err)
			require.Equal(t, staged.Token, cfg.Token)
			_, _, exists, err := store.LookupContext(context.Background(), pending.Stage.Name, true)
			require.NoError(t, err)
			require.False(t, exists)
			for raw, expected := range map[string]int{parent: 401, cfg.Token: 200} {
				request := httptest.NewRequest("GET", "/v1/capabilities", nil)
				request.Header.Set("Authorization", "Bearer "+raw)
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, request)
				require.Equal(t, expected, response.Code)
			}
			// A second process returns the same historical receipt.
			replay, replayOutput := command("")
			require.NoError(t, replay.Run())
			require.Contains(t, replayOutput.String(), completed.CandidateID)
			for _, data := range []string{resumedOutput.String(), replayOutput.String()} {
				require.NotContains(t, data, parent)
				require.NotContains(t, data, cfg.Token)
				require.NotContains(t, data, "PRIVATE KEY")
			}
		})
	}
}
