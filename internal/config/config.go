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
	ControlListen           string
	ControlAuthToken        string
	ControlAllowCIDRs       []string
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
	BootstrapRateLimitQPS   float64
	BootstrapRateLimitBurst int
	ArtifactRateLimitQPS    float64
	ArtifactRateLimitBurst  int
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
	IdleStopEnabled         bool
	IdleStopInterval        time.Duration
	IdleStopMinutesDefault  int
	IdleStopCPUThreshold    float64
	// Proxmox backend configuration
	ProxmoxBackend          string // "shell" or "api"
	ProxmoxCloneMode        string // "linked" or "full"
	ProxmoxAPIURL           string // e.g., "https://localhost:8006/api2/json"
	ProxmoxAPIToken         string // e.g., "root@pam!token=uuid"
	ProxmoxNode             string // Proxmox node name (optional, auto-detected if empty)
	ProxmoxTLSInsecure      bool   // Skip TLS verification for Proxmox API
	ProxmoxTLSCAPath        string // Optional CA bundle path for Proxmox API TLS verification
	ProxmoxAPIShellFallback bool   // Allow shell fallback for API backend volume ops
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
	ProfilesDir             string   `yaml:"profiles_dir"`
	DataDir                 string   `yaml:"data_dir"`
	LogDir                  string   `yaml:"log_dir"`
	RunDir                  string   `yaml:"run_dir"`
	SocketPath              string   `yaml:"socket_path"`
	ControlListen           string   `yaml:"control_listen"`
	ControlAuthToken        string   `yaml:"control_auth_token"`
	ControlAllowCIDRs       []string `yaml:"control_allow_cidrs"`
	DBPath                  string   `yaml:"db_path"`
	BootstrapListen         string   `yaml:"bootstrap_listen"`
	ArtifactListen          string   `yaml:"artifact_listen"`
	MetricsListen           string   `yaml:"metrics_listen"`
	AgentSubnet             string   `yaml:"agent_subnet"`
	ControllerURL           string   `yaml:"controller_url"`
	ArtifactUploadURL       string   `yaml:"artifact_upload_url"`
	ArtifactDir             string   `yaml:"artifact_dir"`
	ArtifactMaxBytes        int64    `yaml:"artifact_max_bytes"`
	ArtifactTokenTTLMinutes int      `yaml:"artifact_token_ttl_minutes"`
	BootstrapRateLimitQPS   *float64 `yaml:"bootstrap_rate_limit_qps"`
	BootstrapRateLimitBurst *int     `yaml:"bootstrap_rate_limit_burst"`
	ArtifactRateLimitQPS    *float64 `yaml:"artifact_rate_limit_qps"`
	ArtifactRateLimitBurst  *int     `yaml:"artifact_rate_limit_burst"`
	SecretsDir              string   `yaml:"secrets_dir"`
	SecretsBundle           string   `yaml:"secrets_bundle"`
	SecretsAgeKeyPath       string   `yaml:"secrets_age_key_path"`
	SecretsSopsPath         string   `yaml:"secrets_sops_path"`
	SnippetsDir             string   `yaml:"snippets_dir"`
	SnippetStorage          string   `yaml:"snippet_storage"`
	SSHPublicKey            string   `yaml:"ssh_public_key"`
	SSHPublicKeyPath        string   `yaml:"ssh_public_key_path"`
	ProxmoxCommandTimeout   string   `yaml:"proxmox_command_timeout"`
	ProvisioningTimeout     string   `yaml:"provisioning_timeout"`
	IdleStopEnabled         *bool    `yaml:"idle_stop_enabled"`
	IdleStopInterval        string   `yaml:"idle_stop_interval"`
	IdleStopMinutesDefault  *int     `yaml:"idle_stop_minutes_default"`
	IdleStopCPUThreshold    *float64 `yaml:"idle_stop_cpu_threshold"`
	ProxmoxBackend          string   `yaml:"proxmox_backend"`
	ProxmoxCloneMode        string   `yaml:"proxmox_clone_mode"`
	ProxmoxAPIURL           string   `yaml:"proxmox_api_url"`
	ProxmoxAPIToken         string   `yaml:"proxmox_api_token"`
	ProxmoxNode             string   `yaml:"proxmox_node"`
	ProxmoxTLSInsecure      *bool    `yaml:"proxmox_tls_insecure"`
	ProxmoxTLSCAPath        string   `yaml:"proxmox_tls_ca_path"`
	ProxmoxAPIShellFallback *bool    `yaml:"proxmox_api_shell_fallback"`
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
//   - ControlListen: "" (disabled)
//   - BootstrapListen: 10.77.0.1:8844
//   - ArtifactListen: 10.77.0.1:8846
//   - MetricsListen: "" (disabled)
//   - ArtifactMaxBytes: 256 MB
//   - ArtifactTokenTTLMinutes: 1440 (24 hours)
//   - BootstrapRateLimitQPS: 1 (per IP)
//   - BootstrapRateLimitBurst: 3 (per IP)
//   - ArtifactRateLimitQPS: 5 (per IP)
//   - ArtifactRateLimitBurst: 10 (per IP)
//   - ProxmoxBackend: "shell"
//   - ProxmoxCloneMode: "linked"
//   - ProxmoxCommandTimeout: 2 minutes
//   - ProvisioningTimeout: 10 minutes
//   - ProxmoxTLSInsecure: false
//   - ProxmoxAPIShellFallback: false
//   - IdleStopEnabled: true
//   - IdleStopInterval: 1 minute
//   - IdleStopMinutesDefault: 30 minutes
//   - IdleStopCPUThreshold: 0.05
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
		ControlListen:           "",
		ControlAuthToken:        "",
		ControlAllowCIDRs:       nil,
		DBPath:                  filepath.Join(dataDir, "agentlab.db"),
		BootstrapListen:         "10.77.0.1:8844",
		ArtifactListen:          "10.77.0.1:8846",
		MetricsListen:           "",
		ArtifactDir:             filepath.Join(dataDir, "artifacts"),
		ArtifactMaxBytes:        256 * 1024 * 1024,
		ArtifactTokenTTLMinutes: 1440,
		BootstrapRateLimitQPS:   1,
		BootstrapRateLimitBurst: 3,
		ArtifactRateLimitQPS:    5,
		ArtifactRateLimitBurst:  10,
		SecretsDir:              "/etc/agentlab/secrets",
		SecretsBundle:           "default",
		SecretsAgeKeyPath:       "/etc/agentlab/keys/age.key",
		SecretsSopsPath:         "sops",
		SnippetsDir:             "/var/lib/vz/snippets",
		SnippetStorage:          "local",
		ProxmoxCommandTimeout:   2 * time.Minute,
		ProvisioningTimeout:     10 * time.Minute,
		IdleStopEnabled:         true,
		IdleStopInterval:        1 * time.Minute,
		IdleStopMinutesDefault:  30,
		IdleStopCPUThreshold:    0.05,
		ProxmoxBackend:          "shell",
		ProxmoxCloneMode:        "linked",
		ProxmoxAPIURL:           "https://localhost:8006",
		ProxmoxAPIToken:         "", // Must be configured
		ProxmoxNode:             "", // Auto-detected if empty
		ProxmoxTLSInsecure:      false,
		ProxmoxTLSCAPath:        "",
		ProxmoxAPIShellFallback: false,
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
	if fileCfg.ControlListen != "" {
		cfg.ControlListen = fileCfg.ControlListen
	}
	if fileCfg.ControlAuthToken != "" {
		cfg.ControlAuthToken = fileCfg.ControlAuthToken
	}
	if len(fileCfg.ControlAllowCIDRs) > 0 {
		cfg.ControlAllowCIDRs = append([]string(nil), fileCfg.ControlAllowCIDRs...)
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
	if fileCfg.BootstrapRateLimitQPS != nil {
		cfg.BootstrapRateLimitQPS = *fileCfg.BootstrapRateLimitQPS
	}
	if fileCfg.BootstrapRateLimitBurst != nil {
		cfg.BootstrapRateLimitBurst = *fileCfg.BootstrapRateLimitBurst
	}
	if fileCfg.ArtifactRateLimitQPS != nil {
		cfg.ArtifactRateLimitQPS = *fileCfg.ArtifactRateLimitQPS
	}
	if fileCfg.ArtifactRateLimitBurst != nil {
		cfg.ArtifactRateLimitBurst = *fileCfg.ArtifactRateLimitBurst
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
	if fileCfg.IdleStopEnabled != nil {
		cfg.IdleStopEnabled = *fileCfg.IdleStopEnabled
	}
	if fileCfg.IdleStopInterval != "" {
		timeout, err := parseDurationField(fileCfg.IdleStopInterval, "idle_stop_interval")
		if err != nil {
			return err
		}
		cfg.IdleStopInterval = timeout
	}
	if fileCfg.IdleStopMinutesDefault != nil {
		cfg.IdleStopMinutesDefault = *fileCfg.IdleStopMinutesDefault
	}
	if fileCfg.IdleStopCPUThreshold != nil {
		cfg.IdleStopCPUThreshold = *fileCfg.IdleStopCPUThreshold
	}
	if fileCfg.ProxmoxBackend != "" {
		cfg.ProxmoxBackend = fileCfg.ProxmoxBackend
	}
	if fileCfg.ProxmoxCloneMode != "" {
		cfg.ProxmoxCloneMode = fileCfg.ProxmoxCloneMode
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
	if fileCfg.ProxmoxTLSInsecure != nil {
		cfg.ProxmoxTLSInsecure = *fileCfg.ProxmoxTLSInsecure
	}
	if fileCfg.ProxmoxTLSCAPath != "" {
		cfg.ProxmoxTLSCAPath = fileCfg.ProxmoxTLSCAPath
	}
	if fileCfg.ProxmoxAPIShellFallback != nil {
		cfg.ProxmoxAPIShellFallback = *fileCfg.ProxmoxAPIShellFallback
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
//   - control_listen requires control_auth_token
//   - control_allow_cidrs entries must be valid CIDRs; required for wildcard control_listen
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
	controlListen := strings.TrimSpace(c.ControlListen)
	if controlListen != "" {
		controlHost, _, err := net.SplitHostPort(controlListen)
		if err != nil {
			return fmt.Errorf("control_listen must be host:port: %w", err)
		}
		if strings.TrimSpace(c.ControlAuthToken) == "" {
			return fmt.Errorf("control_auth_token is required when control_listen is set")
		}
		allowCIDRs, err := validateCIDRList(c.ControlAllowCIDRs, "control_allow_cidrs")
		if err != nil {
			return err
		}
		if isWildcardHost(controlHost) && len(allowCIDRs) == 0 {
			return fmt.Errorf("control_allow_cidrs is required when control_listen binds to wildcard")
		}
	} else {
		if _, err := validateCIDRList(c.ControlAllowCIDRs, "control_allow_cidrs"); err != nil {
			return err
		}
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
	if c.BootstrapRateLimitQPS < 0 {
		return fmt.Errorf("bootstrap_rate_limit_qps must be non-negative")
	}
	if c.BootstrapRateLimitBurst < 0 {
		return fmt.Errorf("bootstrap_rate_limit_burst must be non-negative")
	}
	if c.ArtifactRateLimitQPS < 0 {
		return fmt.Errorf("artifact_rate_limit_qps must be non-negative")
	}
	if c.ArtifactRateLimitBurst < 0 {
		return fmt.Errorf("artifact_rate_limit_burst must be non-negative")
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
	if c.IdleStopInterval < 0 {
		return fmt.Errorf("idle_stop_interval must be non-negative")
	}
	if c.IdleStopEnabled && c.IdleStopInterval <= 0 {
		return fmt.Errorf("idle_stop_interval must be positive when idle_stop_enabled is true")
	}
	if c.IdleStopMinutesDefault < 0 {
		return fmt.Errorf("idle_stop_minutes_default must be non-negative")
	}
	if c.IdleStopCPUThreshold < 0 || c.IdleStopCPUThreshold > 1 {
		return fmt.Errorf("idle_stop_cpu_threshold must be between 0 and 1")
	}
	if c.ProxmoxBackend != "" && c.ProxmoxBackend != "shell" && c.ProxmoxBackend != "api" {
		return fmt.Errorf("proxmox_backend must be either 'shell' or 'api'")
	}
	if c.ProxmoxCloneMode != "" && c.ProxmoxCloneMode != "linked" && c.ProxmoxCloneMode != "full" {
		return fmt.Errorf("proxmox_clone_mode must be either 'linked' or 'full'")
	}
	if c.ProxmoxBackend == "api" && c.ProxmoxAPIToken == "" {
		return fmt.Errorf("proxmox_api_token is required when using api backend")
	}
	if strings.TrimSpace(c.ProxmoxTLSCAPath) != "" && c.ProxmoxTLSInsecure {
		return fmt.Errorf("proxmox_tls_insecure cannot be true when proxmox_tls_ca_path is set")
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

func validateCIDRList(values []string, field string) ([]string, error) {
	clean := make([]string, 0, len(values))
	for _, raw := range values {
		value := strings.TrimSpace(raw)
		if value == "" {
			continue
		}
		if _, _, err := net.ParseCIDR(value); err != nil {
			return nil, fmt.Errorf("%s must contain valid CIDR entries: %w", field, err)
		}
		clean = append(clean, value)
	}
	return clean, nil
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
