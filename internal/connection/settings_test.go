package connection

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadEnvironmentOverridesSavedConnection(t *testing.T) {

	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	t.Setenv("AGENTLAB_CONFIG_DIR", filepath.Join(root, "agentlab"))
	t.Setenv("AGENTLAB_CONTEXT", "")
	t.Setenv("AGENTLAB_ENDPOINT", "https://environment.example")
	t.Setenv("AGENTLAB_TOKEN", "environment-token")
	t.Setenv("AGENTLAB_ALLOW_INSECURE_HTTP", "")
	if err := os.Mkdir(filepath.Join(root, "agentlab"), 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "agentlab", "client.json")
	if err := os.WriteFile(path, []byte(`{"endpoint":"https://saved.example","token":"saved-token","jump_host":"jump"}`), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Endpoint != "https://environment.example" || cfg.Token != "environment-token" {
		t.Fatal("environment does not override saved settings")
	}
	if err := os.WriteFile(path, []byte(`invalid`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(); err == nil {
		t.Fatal("invalid settings silently ignored")
	}
}

func TestExplicitConfigDirectoryDoesNotFallBackWhenRelative(t *testing.T) {
	t.Setenv("AGENTLAB_CONFIG_DIR", "relative/configuration")
	if _, err := ConfigDir(); err == nil {
		t.Fatal("relative configuration directory accepted")
	}
	if _, err := Load(); err == nil {
		t.Fatal("relative configuration silently selected another store")
	}
}
