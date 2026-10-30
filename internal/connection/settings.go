package connection

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Settings is the connection subset of the existing client configuration.
// Legacy protected files remain readable; contexts resolve their recorded store.
type Settings struct {
	ServerIdentity    string `json:"server_identity,omitempty"`
	Endpoint          string `json:"endpoint,omitempty"`
	JumpHost          string `json:"jump_host,omitempty"`
	JumpUser          string `json:"jump_user,omitempty"`
	Token             string `json:"token,omitempty"`
	AllowInsecureHTTP bool   `json:"allow_insecure_http,omitempty"`
}

// ConfigDir returns the shared directory for CLI and dashboard client settings.
// An explicit directory must be absolute so changing directories cannot select
// a different server or credential store.
func ConfigDir() (string, error) {
	if directory := strings.TrimSpace(os.Getenv("AGENTLAB_CONFIG_DIR")); directory != "" {
		if !filepath.IsAbs(directory) {
			return "", fmt.Errorf("AGENTLAB_CONFIG_DIR must be an absolute directory")
		}
		return filepath.Clean(directory), nil
	}
	root, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "agentlab"), nil
}

// Load resolves saved settings and environment overrides. Interfaces with flags
// use Prepare and Resolve after parsing all explicit connection options.
func Load() (Settings, error) {
	root, err := ConfigDir()
	if err != nil {
		return Settings{}, err
	}
	selected, err := Prepare(context.Background(), PrepareOptions{
		Root: root, Context: strings.TrimSpace(os.Getenv("AGENTLAB_CONTEXT")), Environment: EnvironmentOverrides(),
	})
	if err != nil {
		return Settings{}, err
	}
	resolved, err := selected.Resolve(context.Background(), Overrides{})
	return resolved.Settings, err
}
