package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/agentlab/agentlab/internal/buildinfo"
	"github.com/agentlab/agentlab/internal/config"
	"github.com/agentlab/agentlab/internal/daemon"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfigLoadFailure(t *testing.T) {
	t.Run("non-existent config path", func(t *testing.T) {
		temp := t.TempDir()
		nonExistentPath := filepath.Join(temp, "nonexistent", "config.yaml")

		_, err := config.Load(nonExistentPath)
		assert.Error(t, err)
	})

	t.Run("invalid yaml", func(t *testing.T) {
		temp := t.TempDir()
		configPath := filepath.Join(temp, "config.yaml")

		err := os.WriteFile(configPath, []byte("invalid: yaml: content: ["), 0644)
		require.NoError(t, err)

		_, err = config.Load(configPath)
		assert.Error(t, err)
	})
}

func TestConfigLoadSuccess(t *testing.T) {
	t.Run("valid config file", func(t *testing.T) {
		temp := t.TempDir()
		configPath := filepath.Join(temp, "config.yaml")

		// Create a minimal valid config
		err := os.WriteFile(configPath, []byte(`
profiles_dir: /etc/agentlab/profiles
data_dir: /var/lib/agentlab
log_dir: /var/log/agentlab
run_dir: /run/agentlab
`), 0644)
		require.NoError(t, err)

		cfg, err := config.Load(configPath)
		require.NoError(t, err)

		assert.Equal(t, configPath, cfg.ConfigPath)
		assert.Equal(t, "/etc/agentlab/profiles", cfg.ProfilesDir)
		assert.Equal(t, "/var/lib/agentlab", cfg.DataDir)
	})

	t.Run("config overrides defaults", func(t *testing.T) {
		dir := t.TempDir()
		configPath := filepath.Join(dir, "config.yaml")

		customRunDir := filepath.Join(dir, "custom-run")
		err := os.WriteFile(configPath, []byte(`
run_dir: `+customRunDir+`
`), 0644)
		require.NoError(t, err)

		cfg, err := config.Load(configPath)
		require.NoError(t, err)

		assert.Equal(t, customRunDir, cfg.RunDir)
		assert.Equal(t, filepath.Join(customRunDir, "agentlabd.sock"), cfg.SocketPath)
	})
}

func TestMainStartupSequence(t *testing.T) {
	t.Run("validate main flow components", func(t *testing.T) {
		// This test validates the components of main() flow
		// without actually running the daemon

		_ = t.TempDir()

		// 1. Verify buildinfo.String() is accessible and returns something
		version := buildinfo.String()
		assert.NotEmpty(t, version)
		// The version format is "version=X commit=Y date=Z"
		assert.Contains(t, version, "version=")
	})

	t.Run("config validation requirements", func(t *testing.T) {
		// Test that various config scenarios are handled
		tests := []struct {
			name    string
			setup   func(t *testing.T) config.Config
			wantErr bool
		}{
			{
				name: "minimal valid config",
				setup: func(t *testing.T) config.Config {
					_ = t.TempDir()
					return config.DefaultConfig()
				},
				wantErr: false,
			},
			{
				name: "invalid - empty run_dir",
				setup: func(t *testing.T) config.Config {
					cfg := config.DefaultConfig()
					cfg.RunDir = ""
					return cfg
				},
				wantErr: true,
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				cfg := tt.setup(t)

				err := cfg.Validate()
				if tt.wantErr {
					assert.Error(t, err)
				} else {
					assert.NoError(t, err)
				}
			})
		}
	})
}

func TestVersionOutput(t *testing.T) {
	t.Run("buildinfo.String format", func(t *testing.T) {
		version := buildinfo.String()
		assert.NotEmpty(t, version)
		// The version format is "version=X commit=Y date=Z"
		assert.Contains(t, version, "version=")
		assert.Contains(t, version, "commit=")
	})
}

func TestErrorHandling(t *testing.T) {
	t.Run("config error handling", func(t *testing.T) {
		// Verify that config errors are properly reported
		temp := t.TempDir()
		nonExistentPath := filepath.Join(temp, "nonexistent", "config.yaml")

		_, err := config.Load(nonExistentPath)
		assert.Error(t, err, "Load should fail for non-existent config")
	})

	t.Run("daemon error handling", func(t *testing.T) {
		// Verify daemon.Run error handling
		// Since daemon.Run requires valid config, we test with invalid config

		cfg := config.Config{
			// Invalid config - missing required fields
		}

		ctx := context.Background()
		err := daemon.Run(ctx, cfg)
		assert.Error(t, err, "daemon.Run should fail with invalid config")
	})
}
