package main

import (
	"context"
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/agentlab/agentlab/internal/connection"
)

func dashboardFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("AGENTLAB_CONFIG_DIR", root)
	for _, key := range []string{"AGENTLAB_ENDPOINT", "AGENTLAB_TOKEN", "AGENTLAB_CONTEXT", "AGENTLAB_ALLOW_INSECURE_HTTP"} {
		t.Setenv(key, "")
	}
	store := connection.ContextStore{Root: root, CredentialMode: "file"}
	if err := store.Put("saved", connection.Settings{Endpoint: "https://saved.example", Token: "saved-secret"}); err != nil {
		t.Fatal(err)
	}
	items, _, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "credentials", items[0].CredentialRef+".json")); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestDashboardParsesBeforeCredentialRead(t *testing.T) {
	root := dashboardFixture(t)
	for _, tt := range []struct {
		name                    string
		args                    []string
		endpoint, token, socket string
	}{
		{"replacement token", []string{"--token", "replacement"}, "https://saved.example", "replacement", ""},
		{"replacement endpoint", []string{"--endpoint", "https://new.example"}, "https://new.example", "", ""},
		{"local socket", []string{"--socket", "/tmp/local.sock"}, "", "", "/tmp/local.sock"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg, version, err := parseOptions(context.Background(), tt.args, io.Discard)
			if err != nil || version || cfg.Endpoint != tt.endpoint || cfg.Token != tt.token || (tt.socket != "" && cfg.SocketPath != tt.socket) {
				t.Fatalf("options: %+v %v", cfg, err)
			}
		})
	}
	if _, _, err := parseOptions(context.Background(), nil, io.Discard); err == nil {
		t.Fatal("missing saved credential ignored")
	}
	if err := os.WriteFile(filepath.Join(root, "contexts.json"), []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, version, err := parseOptions(context.Background(), []string{"--version"}, io.Discard); err != nil || !version {
		t.Fatalf("version read broken settings: %v", err)
	}
	if _, _, err := parseOptions(context.Background(), []string{"--help"}, io.Discard); !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("help read broken settings: %v", err)
	}
}

func TestDashboardExplicitContextAndTransportConflict(t *testing.T) {
	dashboardFixture(t)
	if _, _, err := parseOptions(context.Background(), []string{"--context", "missing", "--token", "replacement"}, io.Discard); err == nil {
		t.Fatal("unknown context fell back")
	}
	if _, _, err := parseOptions(context.Background(), []string{"--endpoint", "https://new.example", "--socket", "/tmp/local.sock"}, io.Discard); err == nil {
		t.Fatal("conflicting transports accepted")
	}
}
