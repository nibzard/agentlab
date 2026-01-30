package config

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateWildcard(t *testing.T) {
	tests := []struct {
		name        string
		setup       func(*Config)
		wantErr     bool
		errContains string
	}{
		{
			name: "wildcard bootstrap requires subnet",
			setup: func(c *Config) {
				c.BootstrapListen = "0.0.0.0:8844"
				c.ArtifactListen = "0.0.0.0:8846"
				c.ControllerURL = "http://10.77.0.1:8844"
				c.ArtifactUploadURL = "http://10.77.0.1:8846/upload"
				c.AgentSubnet = ""
			},
			wantErr:     true,
			errContains: "agent_subnet",
		},
		{
			name: "wildcard bootstrap requires controller_url",
			setup: func(c *Config) {
				c.BootstrapListen = "0.0.0.0:8844"
				c.ControllerURL = ""
				c.AgentSubnet = "10.77.0.0/16"
			},
			wantErr:     true,
			errContains: "controller_url",
		},
		{
			name: "wildcard artifact requires artifact_upload_url",
			setup: func(c *Config) {
				c.ArtifactListen = "0.0.0.0:8846"
				c.ArtifactUploadURL = ""
				c.AgentSubnet = "10.77.0.0/16"
				c.ControllerURL = "http://10.77.0.1:8844"
			},
			wantErr:     true,
			errContains: "artifact_upload_url",
		},
		{
			name: "wildcard with all required fields",
			setup: func(c *Config) {
				c.BootstrapListen = "0.0.0.0:8844"
				c.ArtifactListen = "0.0.0.0:8846"
				c.AgentSubnet = "10.77.0.0/16"
				c.ControllerURL = "http://10.77.0.1:8844"
				c.ArtifactUploadURL = "http://10.77.0.1:8846/upload"
			},
			wantErr: false,
		},
		{
			name: "IPv6 wildcard requires subnet",
			setup: func(c *Config) {
				c.BootstrapListen = "[::]:8844"
				c.ArtifactListen = "[::]:8846"
				c.ControllerURL = "http://10.77.0.1:8844"
				c.ArtifactUploadURL = "http://10.77.0.1:8846/upload"
				c.AgentSubnet = ""
			},
			wantErr:     true,
			errContains: "agent_subnet",
		},
		{
			name: "non-wildcard address without subnet",
			setup: func(c *Config) {
				c.BootstrapListen = "10.77.0.1:8844"
				c.ArtifactListen = "10.77.0.1:8846"
				c.AgentSubnet = ""
			},
			wantErr: false,
		},
		{
			name: "localhost address without subnet",
			setup: func(c *Config) {
				c.BootstrapListen = "127.0.0.1:8844"
				c.ArtifactListen = "127.0.0.1:8846"
				c.AgentSubnet = ""
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()
			if tt.setup != nil {
				tt.setup(&cfg)
			}
			err := cfg.Validate()
			if tt.wantErr {
				require.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateURLs(t *testing.T) {
	tests := []struct {
		name        string
		setup       func(*Config)
		wantErr     bool
		errContains string
	}{
		{
			name: "valid controller URL",
			setup: func(c *Config) {
				c.ControllerURL = "http://controller.example.com:8080"
			},
			wantErr: false,
		},
		{
			name: "valid HTTPS controller URL",
			setup: func(c *Config) {
				c.ControllerURL = "https://controller.example.com"
			},
			wantErr: false,
		},
		{
			name: "controller URL without scheme",
			setup: func(c *Config) {
				c.ControllerURL = "controller.example.com"
			},
			wantErr:     true,
			errContains: "controller_url",
		},
		{
			name: "controller URL without host",
			setup: func(c *Config) {
				c.ControllerURL = "http://"
			},
			wantErr:     true,
			errContains: "controller_url",
		},
		{
			name: "invalid URL scheme",
			setup: func(c *Config) {
				c.ControllerURL = "ftp://controller.example.com"
			},
			wantErr:     true,
			errContains: "controller_url",
		},
		{
			name: "valid artifact upload URL",
			setup: func(c *Config) {
				c.ArtifactUploadURL = "http://artifacts.example.com:8080/upload"
			},
			wantErr: false,
		},
		{
			name: "both URLs valid",
			setup: func(c *Config) {
				c.ControllerURL = "https://controller.example.com"
				c.ArtifactUploadURL = "https://artifacts.example.com/upload"
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()
			if tt.setup != nil {
				tt.setup(&cfg)
			}
			err := cfg.Validate()
			if tt.wantErr {
				require.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateCIDR(t *testing.T) {
	tests := []struct {
		name        string
		subnet      string
		wantErr     bool
		errContains string
	}{
		{
			name:    "valid IPv4 subnet",
			subnet:  "10.77.0.0/16",
			wantErr: false,
		},
		{
			name:    "valid IPv6 subnet",
			subnet:  "fd00::/64",
			wantErr: false,
		},
		{
			name:        "invalid CIDR format",
			subnet:      "not-a-cidr",
			wantErr:     true,
			errContains: "agent_subnet",
		},
		{
			name:        "missing prefix length",
			subnet:      "10.77.0.0",
			wantErr:     true,
			errContains: "agent_subnet",
		},
		{
			name:    "empty subnet is valid",
			subnet:  "",
			wantErr: false,
		},
		{
			name:    "single IP as subnet",
			subnet:  "10.77.0.1/32",
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.AgentSubnet = tt.subnet
			err := cfg.Validate()
			if tt.wantErr {
				require.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateMetricsListen(t *testing.T) {
	tests := []struct {
		name        string
		metricsListen string
		wantErr     bool
		errContains string
	}{
		{
			name:          "localhost is allowed",
			metricsListen: "localhost:9090",
			wantErr:       false,
		},
		{
			name:          "127.0.0.1 is allowed",
			metricsListen: "127.0.0.1:9090",
			wantErr:       false,
		},
		{
			name:          "IPv6 loopback is allowed",
			metricsListen: "[::1]:9090",
			wantErr:       false,
		},
		{
			name:          "empty is allowed",
			metricsListen: "",
			wantErr:       false,
		},
		{
			name:          "0.0.0.0 is not allowed",
			metricsListen: "0.0.0.0:9090",
			wantErr:       true,
			errContains:   "metrics_listen",
		},
		{
			name:          "non-loopback address is not allowed",
			metricsListen: "10.77.0.1:9090",
			wantErr:       true,
			errContains:   "metrics_listen",
		},
		{
			name:          "hostname other than localhost is not allowed",
			metricsListen: "example.com:9090",
			wantErr:       true,
			errContains:   "metrics_listen",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.MetricsListen = tt.metricsListen
			err := cfg.Validate()
			if tt.wantErr {
				require.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateRequiredFields(t *testing.T) {
	tests := []struct {
		name        string
		field       string
		clearValue  func(*Config)
		wantErr     bool
		errContains string
	}{
		{
			name: "empty config_path",
			field: "config_path",
			clearValue: func(c *Config) {
				c.ConfigPath = ""
			},
			wantErr:     true,
			errContains: "config_path",
		},
		{
			name: "empty profiles_dir",
			field: "profiles_dir",
			clearValue: func(c *Config) {
				c.ProfilesDir = ""
			},
			wantErr:     true,
			errContains: "profiles_dir",
		},
		{
			name: "empty run_dir",
			field: "run_dir",
			clearValue: func(c *Config) {
				c.RunDir = ""
			},
			wantErr:     true,
			errContains: "run_dir",
		},
		{
			name: "empty socket_path",
			field: "socket_path",
			clearValue: func(c *Config) {
				c.SocketPath = ""
			},
			wantErr:     true,
			errContains: "socket_path",
		},
		{
			name: "empty bootstrap_listen",
			field: "bootstrap_listen",
			clearValue: func(c *Config) {
				c.BootstrapListen = ""
			},
			wantErr:     true,
			errContains: "bootstrap_listen",
		},
		{
			name: "empty artifact_listen",
			field: "artifact_listen",
			clearValue: func(c *Config) {
				c.ArtifactListen = ""
			},
			wantErr:     true,
			errContains: "artifact_listen",
		},
		{
			name: "empty artifact_dir",
			field: "artifact_dir",
			clearValue: func(c *Config) {
				c.ArtifactDir = ""
			},
			wantErr:     true,
			errContains: "artifact_dir",
		},
		{
			name: "zero artifact_max_bytes",
			field: "artifact_max_bytes",
			clearValue: func(c *Config) {
				c.ArtifactMaxBytes = 0
			},
			wantErr:     true,
			errContains: "artifact_max_bytes",
		},
		{
			name: "negative artifact_max_bytes",
			field: "artifact_max_bytes",
			clearValue: func(c *Config) {
				c.ArtifactMaxBytes = -1
			},
			wantErr:     true,
			errContains: "artifact_max_bytes",
		},
		{
			name: "zero artifact_token_ttl_minutes",
			field: "artifact_token_ttl_minutes",
			clearValue: func(c *Config) {
				c.ArtifactTokenTTLMinutes = 0
			},
			wantErr:     true,
			errContains: "artifact_token_ttl_minutes",
		},
		{
			name: "negative artifact_token_ttl_minutes",
			field: "artifact_token_ttl_minutes",
			clearValue: func(c *Config) {
				c.ArtifactTokenTTLMinutes = -1
			},
			wantErr:     true,
			errContains: "artifact_token_ttl_minutes",
		},
		{
			name: "empty secrets_dir",
			field: "secrets_dir",
			clearValue: func(c *Config) {
				c.SecretsDir = ""
			},
			wantErr:     true,
			errContains: "secrets_dir",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()
			tt.clearValue(&cfg)
			err := cfg.Validate()
			if tt.wantErr {
				require.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateListenAddresses(t *testing.T) {
	tests := []struct {
		name        string
		setup       func(*Config)
		wantErr     bool
		errContains string
	}{
		{
			name: "invalid bootstrap_listen format",
			setup: func(c *Config) {
				c.BootstrapListen = "not-host-port"
			},
			wantErr:     true,
			errContains: "bootstrap_listen",
		},
		{
			name: "invalid artifact_listen format",
			setup: func(c *Config) {
				c.ArtifactListen = "not-host-port"
			},
			wantErr:     true,
			errContains: "artifact_listen",
		},
		{
			name: "valid IPv4 addresses",
			setup: func(c *Config) {
				c.BootstrapListen = "10.77.0.1:8844"
				c.ArtifactListen = "10.77.0.1:8846"
			},
			wantErr: false,
		},
		{
			name: "valid IPv6 addresses",
			setup: func(c *Config) {
				c.BootstrapListen = "[fd00::1]:8844"
				c.ArtifactListen = "[fd00::1]:8846"
			},
			wantErr: false,
		},
		{
			name: "valid localhost",
			setup: func(c *Config) {
				c.BootstrapListen = "localhost:8844"
				c.ArtifactListen = "localhost:8846"
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()
			if tt.setup != nil {
				tt.setup(&cfg)
			}
			err := cfg.Validate()
			if tt.wantErr {
				require.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateTimeouts(t *testing.T) {
	tests := []struct {
		name        string
		setup       func(*Config)
		wantErr     bool
		errContains string
	}{
		{
			name: "valid timeouts",
			setup: func(c *Config) {
				c.ProxmoxCommandTimeout = 2 * 60 * 1000000000 // 2 minutes
				c.ProvisioningTimeout = 10 * 60 * 1000000000 // 10 minutes
			},
			wantErr: false,
		},
		{
			name: "zero timeouts are valid",
			setup: func(c *Config) {
				c.ProxmoxCommandTimeout = 0
				c.ProvisioningTimeout = 0
			},
			wantErr: false,
		},
		{
			name: "negative proxmox_command_timeout",
			setup: func(c *Config) {
				c.ProxmoxCommandTimeout = -1
			},
			wantErr:     true,
			errContains: "proxmox_command_timeout",
		},
		{
			name: "negative provisioning_timeout",
			setup: func(c *Config) {
				c.ProvisioningTimeout = -1
			},
			wantErr:     true,
			errContains: "provisioning_timeout",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()
			if tt.setup != nil {
				tt.setup(&cfg)
			}
			err := cfg.Validate()
			if tt.wantErr {
				require.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	// Check that all required fields are set
	assert.NotEmpty(t, cfg.ConfigPath)
	assert.NotEmpty(t, cfg.ProfilesDir)
	assert.NotEmpty(t, cfg.DataDir)
	assert.NotEmpty(t, cfg.LogDir)
	assert.NotEmpty(t, cfg.RunDir)
	assert.NotEmpty(t, cfg.SocketPath)
	assert.NotEmpty(t, cfg.DBPath)
	assert.NotEmpty(t, cfg.BootstrapListen)
	assert.NotEmpty(t, cfg.ArtifactListen)
	assert.NotEmpty(t, cfg.ArtifactDir)
	assert.Greater(t, cfg.ArtifactMaxBytes, int64(0))
	assert.Greater(t, cfg.ArtifactTokenTTLMinutes, 0)
	assert.NotEmpty(t, cfg.SecretsDir)
	assert.NotEmpty(t, cfg.SecretsBundle)
	assert.NotEmpty(t, cfg.SecretsAgeKeyPath)
	assert.NotEmpty(t, cfg.SecretsSopsPath)
	assert.NotEmpty(t, cfg.SnippetsDir)
	assert.NotEmpty(t, cfg.SnippetStorage)
	assert.GreaterOrEqual(t, cfg.ProxmoxCommandTimeout, time.Duration(0))
	assert.GreaterOrEqual(t, cfg.ProvisioningTimeout, time.Duration(0))

	// Check that default config is valid
	err := cfg.Validate()
	assert.NoError(t, err)
}

func TestValidateSSHPublicKey(t *testing.T) {
	tests := []struct {
		name        string
		setup       func(*Config)
		wantErr     bool
		errContains string
	}{
		{
			name: "ssh_public_key without ssh_public_key_path is valid",
			setup: func(c *Config) {
				c.SSHPublicKey = "ssh-rsa AAAAB3..."
			},
			wantErr: false,
		},
		{
			name: "ssh_public_key_path with ssh_public_key is valid",
			setup: func(c *Config) {
				c.SSHPublicKey = "ssh-rsa AAAAB3..."
				c.SSHPublicKeyPath = "/etc/agentlab/ssh.pub"
			},
			wantErr: false,
		},
		{
			name: "ssh_public_key_path without ssh_public_key causes error",
			setup: func(c *Config) {
				c.SSHPublicKey = ""
				c.SSHPublicKeyPath = "/nonexistent/key.pub"
			},
			wantErr:     true,
			errContains: "ssh_public_key_path",
		},
		{
			name: "neither ssh_public_key nor ssh_public_key_path is valid",
			setup: func(c *Config) {
				c.SSHPublicKey = ""
				c.SSHPublicKeyPath = ""
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()
			if tt.setup != nil {
				tt.setup(&cfg)
			}
			err := cfg.Validate()
			if tt.wantErr {
				require.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestIsLoopbackHost(t *testing.T) {
	tests := []struct {
		host string
		want bool
	}{
		{"localhost", true},
		{"127.0.0.1", true},
		{"127.0.0.2", true},
		{"::1", true},
		{"0:0:0:0:0:0:0:1", true},
		{"10.77.0.1", false},
		{"192.168.1.1", false},
		{"example.com", false},
		{"", false},
		{"[::1]", true}, // IPv6 with brackets
	}
	for _, tt := range tests {
		t.Run(tt.host, func(t *testing.T) {
			got := isLoopbackHost(strings.Trim(tt.host, "[]"))
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestIsWildcardHost(t *testing.T) {
	tests := []struct {
		host string
		want bool
	}{
		{"0.0.0.0", true},
		{"::", true},
		{"[::]", true},
		{"", true},
		{"127.0.0.1", false},
		{"localhost", false},
		{"10.77.0.1", false},
		{"example.com", false},
		{"0.0.0.0%eth0", true}, // IPv4 zone ID doesn't make sense but our code handles it
		{"fe80::1", false},      // Link-local is not wildcard
		{"10.0.0.0", false},     // Not the wildcard
		{"255.255.255.255", false}, // Broadcast is not wildcard
	}
	for _, tt := range tests {
		t.Run(tt.host, func(t *testing.T) {
			got := isWildcardHost(tt.host)
			assert.Equal(t, tt.want, got)
		})
	}
}
