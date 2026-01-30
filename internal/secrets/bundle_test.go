package secrets

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"filippo.io/age"
	"gopkg.in/yaml.v3"
)

func TestLoadBundleAge(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	bundle := Bundle{
		Version:  BundleVersion,
		Git:      GitBundle{Token: "ghp_test", Username: "x-access-token"},
		Env:      map[string]string{"ANTHROPIC_API_KEY": "test-1"},
		Claude:   ClaudeBundle{Settings: map[string]interface{}{"model": "sonnet", "max_tokens": 4000}},
		Artifact: ArtifactBundle{Endpoint: "http://10.77.0.1:8846/upload", Token: "artifact-token"},
		Metadata: map[string]string{"owner": "platform"},
	}
	payload, err := yaml.Marshal(bundle)
	if err != nil {
		t.Fatalf("marshal bundle: %v", err)
	}
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatalf("generate age identity: %v", err)
	}
	var encrypted bytes.Buffer
	writer, err := age.Encrypt(&encrypted, identity.Recipient())
	if err != nil {
		t.Fatalf("age encrypt: %v", err)
	}
	if _, err := writer.Write(payload); err != nil {
		t.Fatalf("write age payload: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close age writer: %v", err)
	}

	bundlePath := filepath.Join(tmp, "default.age")
	if err := osWriteFile(bundlePath, encrypted.Bytes()); err != nil {
		t.Fatalf("write bundle: %v", err)
	}
	keyPath := filepath.Join(tmp, "age.key")
	if err := osWriteFile(keyPath, []byte(identity.String()+"\n")); err != nil {
		t.Fatalf("write age key: %v", err)
	}

	store := Store{Dir: tmp, AgeKeyPath: keyPath}
	loaded, err := store.Load(context.Background(), "default")
	if err != nil {
		t.Fatalf("load bundle: %v", err)
	}
	if loaded.Git.Token != bundle.Git.Token {
		t.Fatalf("git token = %q, want %q", loaded.Git.Token, bundle.Git.Token)
	}
	if loaded.Env["ANTHROPIC_API_KEY"] != "test-1" {
		t.Fatalf("env missing anthropic key")
	}
	settingsJSON, err := loaded.ClaudeSettingsJSON()
	if err != nil {
		t.Fatalf("claude settings json: %v", err)
	}
	if settingsJSON == "" {
		t.Fatalf("claude settings json empty")
	}
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(settingsJSON), &parsed); err != nil {
		t.Fatalf("parse claude settings json: %v", err)
	}
	if parsed["model"] != "sonnet" {
		t.Fatalf("claude model = %v", parsed["model"])
	}
}

func TestLoadBundleSops(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	plaintext := `version: 1
artifact:
  endpoint: http://10.77.0.1:8846/upload
  token: test-token
`
	bundlePath := filepath.Join(tmp, "bundle.sops.yaml")
	stub := []byte("artifact: ENC[...]\nsops:\n  version: 3.9.0\n")
	if err := osWriteFile(bundlePath, stub); err != nil {
		t.Fatalf("write sops stub: %v", err)
	}
	called := false
	store := Store{
		Dir:        tmp,
		AgeKeyPath: filepath.Join(tmp, "age.key"),
		SopsDecrypt: func(ctx context.Context, path string, env []string) ([]byte, error) {
			called = true
			if !strings.Contains(path, "bundle.sops.yaml") {
				return nil, fmt.Errorf("unexpected bundle path: %s", path)
			}
			return []byte(plaintext), nil
		},
	}
	bundle, err := store.Load(context.Background(), "bundle")
	if err != nil {
		t.Fatalf("load sops bundle: %v", err)
	}
	if !called {
		t.Fatalf("expected sops decrypt to be called")
	}
	if bundle.Artifact.Token != "test-token" {
		t.Fatalf("artifact token = %q", bundle.Artifact.Token)
	}
}

func osWriteFile(path string, data []byte) error {
	return os.WriteFile(path, data, 0o600)
}
