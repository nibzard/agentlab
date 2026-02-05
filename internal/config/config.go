// ABOUTME: Package config provides configuration loading and validation for the AgentLab daemon.
//
// The configuration is loaded from a YAML file at /etc/agentlab/config.yaml by default.
// Environment variables can override any configuration value by using the AGENTLAB_ prefix
// (e.g., AGENTLAB_DATA_DIR for the data_dir field).
//
// Configuration values have sensible defaults and are validated on load.
package config

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config holds daemon configuration paths and listener settings.
//
// It contains all runtime configuration for the agentlabd daemon, including
// file paths, network listen addresses, Proxmox backend settings, and various
// timeouts and limits.
//
// Use DefaultConfig() to get a configuration with all defaults set,
// then Load() to read and apply overrides from a YAML file.
//
// Example:
//
//	cfg, err := config.Load("/etc/agentlab/config.yaml")
//	if err != nil {
//	    log.Fatal(err)
//	}
type Config struct {
	ConfigPath              string
	ProfilesDir             string
	DataDir                 string
	LogDir                  string
	RunDir                  string
	SocketPath              string
	DBPath                  string
	BootstrapListen         string
	ArtifactListen          string
	MetricsListen           string
	AgentSubnet             string
	ControllerURL           string
	ArtifactUploadURL       string
	ArtifactDir             string
	ArtifactMaxBytes        int64
	ArtifactTokenTTLMinutes int
	SecretsDir              string
	SecretsBundle           string
	SecretsAgeKeyPath       string
	SecretsSopsPath         string
	SnippetsDir             string
	SnippetStorage          string
	SSHPublicKey            string
	SSHPublicKeyPath        string
	ProxmoxCommandTimeout   time.Duration
	ProvisioningTimeout     time.Duration
	// Proxmox API backend configuration
	ProxmoxBackend  string // "shell" or "api"
	ProxmoxAPIURL   string // e.g., "https://localhost:8006/api2/json"
	ProxmoxAPIToken string // e.g., "root@pam!token=uuid"
	ProxmoxNode     string // Proxmox node name (optional, auto-detected if empty)
}

// FileConfig represents supported YAML config overrides.
//
// This struct uses YAML tags to map configuration file fields to their
// corresponding Config struct fields. Fields are loaded from the YAML
// file and applied to the default configuration.
//
// Empty string fields in the YAML file are ignored, allowing partial
// configuration overrides. Duration fields accept Go duration format
// strings (e.g., "30s", "5m", "1h").
type FileConfig struct {
	ProfilesDir             string `yaml:"profiles_dir"`
	DataDir                 string `yaml:"data_dir"`
	LogDir                  string `yaml:"log_dir"`
	RunDir                  string `yaml:"run_dir"`
	SocketPath              string `yaml:"socket_path"`
	DBPath                  string `yaml:"db_path"`
	BootstrapListen         string `yaml:"bootstrap_listen"`
	ArtifactListen          string `yaml:"artifact_listen"`
	MetricsListen           string `yaml:"metrics_listen"`
	AgentSubnet             string `yaml:"agent_subnet"`
	ControllerURL           string `yaml:"controller_url"`
	ArtifactUploadURL       string `yaml:"artifact_upload_url"`
	ArtifactDir             string `yaml:"artifact_dir"`
	ArtifactMaxBytes        int64  `yaml:"artifact_max_bytes"`
	ArtifactTokenTTLMinutes int    `yaml:"artifact_token_ttl_minutes"`
	SecretsDir              string `yaml:"secrets_dir"`
	SecretsBundle           string `yaml:"secrets_bundle"`
	SecretsAgeKeyPath       string `yaml:"secrets_age_key_path"`
	SecretsSopsPath         string `yaml:"secrets_sops_path"`
	SnippetsDir             string `yaml:"snippets_dir"`
	SnippetStorage          string `yaml:"snippet_storage"`
	SSHPublicKey            string `yaml:"ssh_public_key"`
	SSHPublicKeyPath        string `yaml:"ssh_public_key_path"`
	ProxmoxCommandTimeout   string `yaml:"proxmox_command_timeout"`
	ProvisioningTimeout     string `yaml:"provisioning_timeout"`
	ProxmoxBackend          string `yaml:"proxmox_backend"`
	ProxmoxAPIURL           string `yaml:"proxmox_api_url"`
	ProxmoxAPIToken         string `yaml:"proxmox_api_token"`
	ProxmoxNode             string `yaml:"proxmox_node"`
}

// DefaultConfig returns a Config struct with all default values set.
//
// The defaults use standard Linux filesystem paths and are suitable for
// a typical Proxmox VE host installation. Key defaults include:
//
//   - ConfigPath: /etc/agentlab/config.yaml
//   - DataDir: /var/lib/agentlab
//   - RunDir: /run/agentlab
//   - SocketPath: /run/agentlab/agentlabd.sock
//   - BootstrapListen: 10.77.0.1:8844
//   - ArtifactListen: 10.77.0.1:8846
//   - MetricsListen: "" (disabled)
//   - ArtifactMaxBytes: 256 MB
//   - ArtifactTokenTTLMinutes: 1440 (24 hours)
//   - ProxmoxBackend: "shell"
//   - ProxmoxCommandTimeout: 2 minutes
//   - ProvisioningTimeout: 10 minutes
//
// The returned configuration is valid and ready to use without modification.
// Use Load() to apply overrides from a configuration file.
func DefaultConfig() Config {
	dataDir := "/var/lib/agentlab"
	runDir := "/run/agentlab"
	return Config{
		ConfigPath:              "/etc/agentlab/config.yaml",
		ProfilesDir:             "/etc/agentlab/profiles",
		DataDir:                 dataDir,
		LogDir:                  "/var/log/agentlab",
		RunDir:                  runDir,
		SocketPath:              filepath.Join(runDir, "agentlabd.sock"),
		DBPath:                  filepath.Join(dataDir, "agentlab.db"),
		BootstrapListen:         "10.77.0.1:8844",
		ArtifactListen:          "10.77.0.1:8846",
		MetricsListen:           "",
		ArtifactDir:             filepath.Join(dataDir, "artifacts"),
		ArtifactMaxBytes:        256 * 1024 * 1024,
		ArtifactTokenTTLMinutes: 1440,
		SecretsDir:              "/etc/agentlab/secrets",
		SecretsBundle:           "default",
		SecretsAgeKeyPath:       "/etc/agentlab/keys/age.key",
		SecretsSopsPath:         "sops",
		SnippetsDir:             "/var/lib/vz/snippets",
		SnippetStorage:          "local",
		ProxmoxCommandTimeout:   2 * time.Minute,
		ProvisioningTimeout:     10 * time.Minute,
		ProxmoxBackend:          "shell",
		ProxmoxAPIURL:           "https://localhost:8006",
		ProxmoxAPIToken:         "", // Must be configured
		ProxmoxNode:             "", // Auto-detected if empty
	}
}

// Load reads the YAML config file and applies overrides to defaults.
//
// If path is empty, the default config path (/etc/agentlab/config.yaml) is used.
// The file must exist and contain valid YAML. Any fields specified in the file
// override the defaults; unspecified fields retain their default values.
//
// After loading, SSH public keys are read from files if specified by path,
// derived paths are computed (e.g., socket_path from run_dir if not set),
// and the full configuration is validated.
//
// Returns an error if the config file cannot be read, contains invalid YAML,
// fails validation, or references inaccessible files (like SSH keys).
//
// Example:
//
//	cfg, err := config.Load("")  // uses default path
//	if err != nil {
//	    log.Fatal(err)
//	}
func Load(path string) (Config, error) {
	cfg := DefaultConfig()
	if path != "" {
		cfg.ConfigPath = path
	}
	data, err := os.ReadFile(cfg.ConfigPath)
	if err != nil {
		return cfg, fmt.Errorf("read config %s: %w", cfg.ConfigPath, err)
	}
	var fileCfg FileConfig
	if err := yaml.Unmarshal(data, &fileCfg); err != nil {
		return cfg, fmt.Errorf("parse config %s: %w", cfg.ConfigPath, err)
	}
	if err := applyFileConfig(&cfg, fileCfg); err != nil {
		return cfg, err
	}
	if fileCfg.DataDir != "" && fileCfg.DBPath == "" {
		cfg.DBPath = filepath.Join(cfg.DataDir, "agentlab.db")
	}
	if fileCfg.DataDir != "" && fileCfg.ArtifactDir == "" {
		cfg.ArtifactDir = filepath.Join(cfg.DataDir, "artifacts")
	}
	if fileCfg.RunDir != "" && fileCfg.SocketPath == "" {
		cfg.SocketPath = filepath.Join(cfg.RunDir, "agentlabd.sock")
	}
	if cfg.SSHPublicKey == "" && cfg.SSHPublicKeyPath != "" {
		keyData, err := os.ReadFile(cfg.SSHPublicKeyPath)
		if err != nil {
			return cfg, fmt.Errorf("read ssh public key %s: %w", cfg.SSHPublicKeyPath, err)
		}
		cfg.SSHPublicKey = strings.TrimSpace(string(keyData))
	}
	if err := cfg.Validate(); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func applyFileConfig(cfg *Config, fileCfg FileConfig) error {
	if fileCfg.ProfilesDir != "" {
		cfg.ProfilesDir = fileCfg.ProfilesDir
	}
	if fileCfg.DataDir != "" {
		cfg.DataDir = fileCfg.DataDir
	}
	if fileCfg.LogDir != "" {
		cfg.LogDir = fileCfg.LogDir
	}
	if fileCfg.RunDir != "" {
		cfg.RunDir = fileCfg.RunDir
	}
	if fileCfg.SocketPath != "" {
		cfg.SocketPath = fileCfg.SocketPath
	}
	if fileCfg.DBPath != "" {
		cfg.DBPath = fileCfg.DBPath
	}
	if fileCfg.BootstrapListen != "" {
		cfg.BootstrapListen = fileCfg.BootstrapListen
	}
	if fileCfg.ArtifactListen != "" {
		cfg.ArtifactListen = fileCfg.ArtifactListen
	}
	if fileCfg.MetricsListen != "" {
		cfg.MetricsListen = fileCfg.MetricsListen
	}
	if fileCfg.AgentSubnet != "" {
		cfg.AgentSubnet = fileCfg.AgentSubnet
	}
	if fileCfg.ControllerURL != "" {
		cfg.ControllerURL = fileCfg.ControllerURL
	}
	if fileCfg.ArtifactUploadURL != "" {
		cfg.ArtifactUploadURL = fileCfg.ArtifactUploadURL
	}
	if fileCfg.ArtifactDir != "" {
		cfg.ArtifactDir = fileCfg.ArtifactDir
	}
	if fileCfg.ArtifactMaxBytes > 0 {
		cfg.ArtifactMaxBytes = fileCfg.ArtifactMaxBytes
	}
	if fileCfg.ArtifactTokenTTLMinutes > 0 {
		cfg.ArtifactTokenTTLMinutes = fileCfg.ArtifactTokenTTLMinutes
	}
	if fileCfg.SecretsDir != "" {
		cfg.SecretsDir = fileCfg.SecretsDir
	}
	if fileCfg.SecretsBundle != "" {
		cfg.SecretsBundle = fileCfg.SecretsBundle
	}
	if fileCfg.SecretsAgeKeyPath != "" {
		cfg.SecretsAgeKeyPath = fileCfg.SecretsAgeKeyPath
	}
	if fileCfg.SecretsSopsPath != "" {
		cfg.SecretsSopsPath = fileCfg.SecretsSopsPath
	}
	if fileCfg.SnippetsDir != "" {
		cfg.SnippetsDir = fileCfg.SnippetsDir
	}
	if fileCfg.SnippetStorage != "" {
		cfg.SnippetStorage = fileCfg.SnippetStorage
	}
	if fileCfg.SSHPublicKey != "" {
		cfg.SSHPublicKey = fileCfg.SSHPublicKey
	}
	if fileCfg.SSHPublicKeyPath != "" {
		cfg.SSHPublicKeyPath = fileCfg.SSHPublicKeyPath
	}
	if fileCfg.ProxmoxCommandTimeout != "" {
		timeout, err := parseDurationField(fileCfg.ProxmoxCommandTimeout, "proxmox_command_timeout")
		if err != nil {
			return err
		}
		cfg.ProxmoxCommandTimeout = timeout
	}
	if fileCfg.ProvisioningTimeout != "" {
		timeout, err := parseDurationField(fileCfg.ProvisioningTimeout, "provisioning_timeout")
		if err != nil {
			return err
		}
		cfg.ProvisioningTimeout = timeout
	}
	if fileCfg.ProxmoxBackend != "" {
		cfg.ProxmoxBackend = fileCfg.ProxmoxBackend
	}
	if fileCfg.ProxmoxAPIURL != "" {
		cfg.ProxmoxAPIURL = fileCfg.ProxmoxAPIURL
	}
	if fileCfg.ProxmoxAPIToken != "" {
		cfg.ProxmoxAPIToken = fileCfg.ProxmoxAPIToken
	}
	if fileCfg.ProxmoxNode != "" {
		cfg.ProxmoxNode = fileCfg.ProxmoxNode
	}
	return nil
}

// Validate performs basic validation without exposing secrets.
//
// It checks that all required fields are non-empty, that numeric values
// are within valid ranges, and that network addresses and URLs are
// properly formatted. It enforces security constraints like requiring
// metrics_listen to be localhost-only.
//
// Validation rules include:
//
//   - All path fields (profiles_dir, run_dir, etc.) must be non-empty
//   - Listen addresses must be in host:port format
//   - artifact_max_bytes and artifact_token_ttl_minutes must be positive
//   - Timeouts must be non-negative
//   - agent_subnet must be valid CIDR if specified
//   - When using wildcard listen addresses (0.0.0.0 or [::]),
//     agent_subnet and appropriate controller URLs must be set
//   - metrics_listen must bind to loopback only
//   - proxmox_backend must be "shell" or "api"
//   - proxmox_api_token is required when using "api" backend
//   - URLs must use http or https schemes
//
// Returns an error describing the first validation failure encountered.
func (c Config) Validate() error {
	if c.ConfigPath == "" {
		return fmt.Errorf("config_path is required")
	}
	if c.ProfilesDir == "" {
		return fmt.Errorf("profiles_dir is required")
	}
	if c.RunDir == "" {
		return fmt.Errorf("run_dir is required")
	}
	if c.SocketPath == "" {
		return fmt.Errorf("socket_path is required")
	}
	if c.BootstrapListen == "" {
		return fmt.Errorf("bootstrap_listen is required")
	}
	if c.ArtifactListen == "" {
		return fmt.Errorf("artifact_listen is required")
	}
	if c.ArtifactDir == "" {
		return fmt.Errorf("artifact_dir is required")
	}
	if c.ArtifactMaxBytes <= 0 {
		return fmt.Errorf("artifact_max_bytes must be positive")
	}
	if c.ArtifactTokenTTLMinutes <= 0 {
		return fmt.Errorf("artifact_token_ttl_minutes must be positive")
	}
	if c.SecretsDir == "" {
		return fmt.Errorf("secrets_dir is required")
	}
	bootstrapHost, _, err := net.SplitHostPort(c.BootstrapListen)
	if err != nil {
		return fmt.Errorf("bootstrap_listen must be host:port: %w", err)
	}
	artifactHost, _, err := net.SplitHostPort(c.ArtifactListen)
	if err != nil {
		return fmt.Errorf("artifact_listen must be host:port: %w", err)
	}
	agentSubnet := strings.TrimSpace(c.AgentSubnet)
	if agentSubnet != "" {
		if _, _, err := net.ParseCIDR(agentSubnet); err != nil {
			return fmt.Errorf("agent_subnet must be CIDR: %w", err)
		}
	}
	bootstrapWildcard := isWildcardHost(bootstrapHost)
	artifactWildcard := isWildcardHost(artifactHost)
	if (bootstrapWildcard || artifactWildcard) && agentSubnet == "" {
		return fmt.Errorf("agent_subnet is required when bootstrap_listen or artifact_listen binds to wildcard")
	}
	if bootstrapWildcard && strings.TrimSpace(c.ControllerURL) == "" {
		return fmt.Errorf("controller_url is required when bootstrap_listen binds to wildcard")
	}
	if artifactWildcard && strings.TrimSpace(c.ArtifactUploadURL) == "" {
		return fmt.Errorf("artifact_upload_url is required when artifact_listen binds to wildcard")
	}
	if strings.TrimSpace(c.ControllerURL) != "" {
		if err := validateURL(c.ControllerURL, "controller_url"); err != nil {
			return err
		}
	}
	if strings.TrimSpace(c.ArtifactUploadURL) != "" {
		if err := validateURL(c.ArtifactUploadURL, "artifact_upload_url"); err != nil {
			return err
		}
	}
	if strings.TrimSpace(c.MetricsListen) != "" {
		host, _, err := net.SplitHostPort(c.MetricsListen)
		if err != nil {
			return fmt.Errorf("metrics_listen must be host:port: %w", err)
		}
		if !isLoopbackHost(host) {
			return fmt.Errorf("metrics_listen must be localhost-only (got %q)", host)
		}
	}
	if c.SSHPublicKeyPath != "" && c.SSHPublicKey == "" {
		return fmt.Errorf("ssh_public_key_path is set but empty or unreadable")
	}
	if c.ProxmoxCommandTimeout < 0 {
		return fmt.Errorf("proxmox_command_timeout must be non-negative")
	}
	if c.ProvisioningTimeout < 0 {
		return fmt.Errorf("provisioning_timeout must be non-negative")
	}
	if c.ProxmoxBackend != "" && c.ProxmoxBackend != "shell" && c.ProxmoxBackend != "api" {
		return fmt.Errorf("proxmox_backend must be either 'shell' or 'api'")
	}
	if c.ProxmoxBackend == "api" && c.ProxmoxAPIToken == "" {
		return fmt.Errorf("proxmox_api_token is required when using api backend")
	}
	return nil
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback()
}

func isWildcardHost(host string) bool {
	host = strings.TrimSpace(strings.Trim(host, "[]"))
	if host == "" {
		return true
	}
	if i := strings.LastIndex(host, "%"); i != -1 {
		host = host[:i]
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsUnspecified()
}

func validateURL(value, field string) error {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil {
		return fmt.Errorf("%s must be a valid URL: %w", field, err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return fmt.Errorf("%s must include scheme and host", field)
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https":
		return nil
	default:
		return fmt.Errorf("%s must use http or https", field)
	}
}

func parseDurationField(value, field string) (time.Duration, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, nil
	}
	dur, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("%s must be a valid duration: %w", field, err)
	}
	if dur < 0 {
		return 0, fmt.Errorf("%s must be non-negative", field)
	}
	return dur, nil
}
