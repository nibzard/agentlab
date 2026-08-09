package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/agentlab/agentlab/internal/secrets"
)

func TestSecretsShowRedactsByDefault(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bundle.yaml")
	content := `version: 1
env:
  OPENAI_API_KEY: sk-test-123456
tailscale:
  authkey: shared-auth-key-fixture
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write bundle: %v", err)
	}

	out := captureStdout(t, func() {
		err := runSecretsShowCommand([]string{"--bundle", path, "--allow-plaintext"}, commonFlags{})
		if err != nil {
			t.Fatalf("run secrets show: %v", err)
		}
	})

	if strings.Contains(out, "sk-test-123456") {
		t.Fatalf("expected env secret to be redacted, got %q", out)
	}
	if strings.Contains(out, "shared-auth-key-fixture") {
		t.Fatalf("expected tailscale authkey to be redacted, got %q", out)
	}
	if !strings.Contains(out, "[REDACTED]") {
		t.Fatalf("expected redacted output, got %q", out)
	}
}

func TestSecretsAddSSHKeyAndSetTailscaleAgeBundle(t *testing.T) {
	dir := t.TempDir()
	keyPath := writeAgeIdentity(t, dir)

	_ = captureStdout(t, func() {
		if err := runSecretsAddSSHKeyCommand([]string{
			"--dir", dir,
			"--age-key", keyPath,
			"--bundle", "default",
			"--name", "laptop",
			"--key", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITestKey user@test",
		}, commonFlags{jsonOutput: true}); err != nil {
			t.Fatalf("add ssh key: %v", err)
		}
	})

	_ = captureStdout(t, func() {
		if err := runSecretsSetTailscaleCommand([]string{
			"--dir", dir,
			"--age-key", keyPath,
			"--bundle", "default",
			"--authkey", "shared-auth-key-fixture",
			"--hostname-template", "sandbox-{vmid}",
			"--tag", "tag:agentlab",
			"--extra-arg", "--ssh",
		}, commonFlags{jsonOutput: true}); err != nil {
			t.Fatalf("set tailscale: %v", err)
		}
	})

	store := secrets.Store{Dir: dir, AgeKeyPath: keyPath}
	bundle, err := store.Load(context.Background(), "default")
	if err != nil {
		t.Fatalf("load bundle: %v", err)
	}
	if got := bundle.SSH.Keys["laptop"].Key; !strings.Contains(got, "AAAAC3NzaC1lZDI1NTE5AAAAITestKey") {
		t.Fatalf("unexpected ssh key: %q", got)
	}
	if got := bundle.GetTailscaleAuthKey(); got != "shared-auth-key-fixture" {
		t.Fatalf("tailscale authkey = %q", got)
	}
	if got := bundle.GetTailscaleHostname(1042); got != "sandbox-1042" {
		t.Fatalf("tailscale hostname = %q", got)
	}
	if len(bundle.GetTailscaleTags()) != 1 || bundle.GetTailscaleTags()[0] != "tag:agentlab" {
		t.Fatalf("unexpected tailscale tags: %#v", bundle.GetTailscaleTags())
	}
}

func TestSecretsRemoveSSHKeyAndClearTailscale(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "default.yaml")
	content := `version: 1
ssh:
  keys:
    laptop:
      key: ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITestKey user@test
tailscale:
  authkey: shared-auth-key-fixture
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write bundle: %v", err)
	}

	_ = captureStdout(t, func() {
		if err := runSecretsRemoveSSHKeyCommand([]string{
			"--bundle", path,
			"--allow-plaintext",
			"--name", "laptop",
		}, commonFlags{}); err != nil {
			t.Fatalf("remove ssh key: %v", err)
		}
	})
	_ = captureStdout(t, func() {
		if err := runSecretsClearTailscaleCommand([]string{
			"--bundle", path,
			"--allow-plaintext",
		}, commonFlags{}); err != nil {
			t.Fatalf("clear tailscale: %v", err)
		}
	})

	store := secrets.Store{Dir: dir, AllowPlaintext: true}
	bundle, err := store.Load(context.Background(), path)
	if err != nil {
		t.Fatalf("load updated bundle: %v", err)
	}
	if bundle.SSH != nil {
		t.Fatalf("expected ssh keys to be removed, got %#v", bundle.SSH)
	}
	if bundle.Tailscale != nil {
		t.Fatalf("expected tailscale to be cleared, got %#v", bundle.Tailscale)
	}
}

func writeAgeIdentity(t *testing.T, dir string) string {
	t.Helper()
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatalf("generate age identity: %v", err)
	}
	path := filepath.Join(dir, "age.key")
	if err := os.WriteFile(path, []byte(identity.String()+"\n"), 0o600); err != nil {
		t.Fatalf("write age identity: %v", err)
	}
	return path
}

// requestRecorder captures the method/path/body of the last request and returns
// a canned redacted V1SecretsMutationResponse.
func requestRecorder(t *testing.T, methodOut, pathOut, bodyOut *string) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		*methodOut = r.Method
		*pathOut = r.URL.Path
		data, _ := io.ReadAll(r.Body)
		*bodyOut = string(data)
		writeJSON(t, w, http.StatusOK, map[string]any{
			"bundle": "default",
			"path":   "/tmp/default.age",
			"secrets": map[string]any{
				"env": map[string]string{"ANTHROPIC_API_KEY": "[REDACTED]"},
			},
		})
	}
}

func TestSecretsSetEnvRemoteSingleAndBulk(t *testing.T) {
	var gotMethod, gotPath, gotBody string
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/secrets/env", requestRecorder(t, &gotMethod, &gotPath, &gotBody))
	server := httptest.NewServer(mux)
	defer server.Close()

	base := commonFlags{jsonOutput: true, timeout: time.Second, endpoint: server.URL}
	out := captureStdout(t, func() {
		if err := runSecretsCommand(context.Background(), []string{
			"set-env", "--name", "ANTHROPIC_API_KEY", "--value", "sk-test",
		}, base); err != nil {
			t.Fatalf("set-env: %v", err)
		}
	})

	if gotMethod != http.MethodPut || gotPath != "/v1/secrets/env" {
		t.Fatalf("request = %s %s", gotMethod, gotPath)
	}
	var reqBody map[string]any
	if err := json.Unmarshal([]byte(gotBody), &reqBody); err != nil {
		t.Fatalf("parse request body: %v", err)
	}
	if env, _ := reqBody["env"].(map[string]any); env["ANTHROPIC_API_KEY"] != "sk-test" {
		t.Fatalf("request env = %#v", env)
	}
	if !strings.Contains(out, "ANTHROPIC_API_KEY") || !strings.Contains(out, "[REDACTED]") {
		t.Fatalf("expected redacted JSON output, got %q", out)
	}

	// Bulk via --from-file (KEY=VAL lines).
	envFile := filepath.Join(t.TempDir(), "env.txt")
	if err := os.WriteFile(envFile, []byte("ANTHROPIC_API_KEY=sk-bulk\nOPENAI_API_KEY=sk-openai\n"), 0o600); err != nil {
		t.Fatalf("write env file: %v", err)
	}
	gotBody = ""
	_ = captureStdout(t, func() {
		if err := runSecretsCommand(context.Background(), []string{
			"set-env", "--from-file", envFile,
		}, base); err != nil {
			t.Fatalf("set-env bulk: %v", err)
		}
	})
	if err := json.Unmarshal([]byte(gotBody), &reqBody); err != nil {
		t.Fatalf("parse bulk request body: %v", err)
	}
	if env, _ := reqBody["env"].(map[string]any); env["ANTHROPIC_API_KEY"] != "sk-bulk" || env["OPENAI_API_KEY"] != "sk-openai" {
		t.Fatalf("bulk request env = %#v", env)
	}
}

func TestSecretsSetGitRemote(t *testing.T) {
	var gotMethod, gotPath, gotBody string
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/secrets/git", requestRecorder(t, &gotMethod, &gotPath, &gotBody))
	server := httptest.NewServer(mux)
	defer server.Close()

	tokenFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenFile, []byte("ghp_from-file-token"), 0o600); err != nil {
		t.Fatalf("write token file: %v", err)
	}

	base := commonFlags{jsonOutput: true, timeout: time.Second, endpoint: server.URL}
	_ = captureStdout(t, func() {
		if err := runSecretsCommand(context.Background(), []string{
			"set-git", "--git-token-file", tokenFile, "--username", "bot",
		}, base); err != nil {
			t.Fatalf("set-git: %v", err)
		}
	})

	if gotMethod != http.MethodPut || gotPath != "/v1/secrets/git" {
		t.Fatalf("request = %s %s", gotMethod, gotPath)
	}
	var reqBody map[string]any
	if err := json.Unmarshal([]byte(gotBody), &reqBody); err != nil {
		t.Fatalf("parse request body: %v", err)
	}
	if reqBody["token"] != "ghp_from-file-token" {
		t.Fatalf("request token = %#v", reqBody["token"])
	}
	if reqBody["username"] != "bot" {
		t.Fatalf("request username = %#v", reqBody["username"])
	}
}

func TestSecretsSetEnvRequiresInput(t *testing.T) {
	server := httptest.NewServer(http.NewServeMux())
	defer server.Close()
	base := commonFlags{jsonOutput: true, timeout: time.Second, endpoint: server.URL}
	err := runSecretsCommand(context.Background(), []string{"set-env"}, base)
	if err == nil || !strings.Contains(err.Error(), "required") {
		t.Fatalf("expected a required-input error, got %v", err)
	}
}

// TestSecretsSetTailscaleRemoteAdminFlags: the per-VM minting flags populate the
// request body with the admin api key + tailnet, and the shared authkey is
// absent when only minting flags are supplied.
func TestSecretsSetTailscaleRemoteAdminFlags(t *testing.T) {
	var gotMethod, gotPath, gotBody string
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/secrets/tailscale", requestRecorder(t, &gotMethod, &gotPath, &gotBody))
	server := httptest.NewServer(mux)
	defer server.Close()

	adminKeyFile := filepath.Join(t.TempDir(), "admin.key")
	if err := os.WriteFile(adminKeyFile, []byte("admin-api-key-fixture"), 0o600); err != nil {
		t.Fatalf("write admin key file: %v", err)
	}

	base := commonFlags{jsonOutput: true, timeout: time.Second, endpoint: server.URL}
	_ = captureStdout(t, func() {
		if err := runSecretsCommand(context.Background(), []string{
			"set-tailscale",
			"--admin-api-key-file", adminKeyFile,
			"--tailnet", "example.com",
			"--tag", "tag:agent",
		}, base); err != nil {
			t.Fatalf("set-tailscale admin: %v", err)
		}
	})

	if gotMethod != http.MethodPut || gotPath != "/v1/secrets/tailscale" {
		t.Fatalf("request = %s %s", gotMethod, gotPath)
	}
	var reqBody map[string]any
	if err := json.Unmarshal([]byte(gotBody), &reqBody); err != nil {
		t.Fatalf("parse request body: %v", err)
	}
	if reqBody["admin_api_key"] != "admin-api-key-fixture" {
		t.Fatalf("admin_api_key = %#v", reqBody["admin_api_key"])
	}
	if reqBody["tailnet"] != "example.com" {
		t.Fatalf("tailnet = %#v", reqBody["tailnet"])
	}
	if _, ok := reqBody["authkey"]; ok {
		t.Fatalf("authkey must be absent in an admin-only call, body=%s", gotBody)
	}
}

func TestSecretsSetTailscaleRemoteInlineAdminKey(t *testing.T) {
	var gotMethod, gotPath, gotBody string
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/secrets/tailscale", requestRecorder(t, &gotMethod, &gotPath, &gotBody))
	server := httptest.NewServer(mux)
	defer server.Close()

	base := commonFlags{jsonOutput: true, timeout: time.Second, endpoint: server.URL}
	_ = captureStdout(t, func() {
		if err := runSecretsCommand(context.Background(), []string{
			"set-tailscale",
			"--admin-api-key", "admin-api-key-fixture",
		}, base); err != nil {
			t.Fatalf("set-tailscale inline: %v", err)
		}
	})

	var reqBody map[string]any
	if err := json.Unmarshal([]byte(gotBody), &reqBody); err != nil {
		t.Fatalf("parse request body: %v", err)
	}
	if reqBody["admin_api_key"] != "admin-api-key-fixture" {
		t.Fatalf("admin_api_key = %#v", reqBody["admin_api_key"])
	}
}

func TestSecretsSetTailscaleRemoteRequiresInput(t *testing.T) {
	server := httptest.NewServer(http.NewServeMux())
	defer server.Close()
	base := commonFlags{jsonOutput: true, timeout: time.Second, endpoint: server.URL}
	err := runSecretsCommand(context.Background(), []string{"set-tailscale"}, base)
	if err == nil || !strings.Contains(err.Error(), "required") {
		t.Fatalf("expected a required-input error, got %v", err)
	}
}

func TestSecretsSetTailscaleRemoteAdminKeyMutualExclusivity(t *testing.T) {
	server := httptest.NewServer(http.NewServeMux())
	defer server.Close()
	base := commonFlags{jsonOutput: true, timeout: time.Second, endpoint: server.URL}
	err := runSecretsCommand(context.Background(), []string{
		"set-tailscale",
		"--admin-api-key", "admin-api-key-fixture",
		"--admin-api-key-file", "/nonexistent",
	}, base)
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("expected a mutual-exclusivity error, got %v", err)
	}
}
