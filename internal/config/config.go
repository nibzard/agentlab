package config

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config holds daemon configuration paths and listener settings.
type Config struct {
	ConfigPath       string
	ProfilesDir      string
	DataDir          string
	LogDir           string
	RunDir           string
	SocketPath       string
	DBPath           string
	BootstrapListen  string
	SnippetsDir      string
	SnippetStorage   string
	SSHPublicKey     string
	SSHPublicKeyPath string
}

// FileConfig represents supported YAML config overrides.
type FileConfig struct {
	ProfilesDir      string `yaml:"profiles_dir"`
	DataDir          string `yaml:"data_dir"`
	LogDir           string `yaml:"log_dir"`
	RunDir           string `yaml:"run_dir"`
	SocketPath       string `yaml:"socket_path"`
	DBPath           string `yaml:"db_path"`
	BootstrapListen  string `yaml:"bootstrap_listen"`
	SnippetsDir      string `yaml:"snippets_dir"`
	SnippetStorage   string `yaml:"snippet_storage"`
	SSHPublicKey     string `yaml:"ssh_public_key"`
	SSHPublicKeyPath string `yaml:"ssh_public_key_path"`
}

func DefaultConfig() Config {
	dataDir := "/var/lib/agentlab"
	runDir := "/run/agentlab"
	return Config{
		ConfigPath:      "/etc/agentlab/config.yaml",
		ProfilesDir:     "/etc/agentlab/profiles",
		DataDir:         dataDir,
		LogDir:          "/var/log/agentlab",
		RunDir:          runDir,
		SocketPath:      filepath.Join(runDir, "agentlabd.sock"),
		DBPath:          filepath.Join(dataDir, "agentlab.db"),
		BootstrapListen: "10.77.0.1:8844",
		SnippetsDir:     "/var/lib/vz/snippets",
		SnippetStorage:  "local",
	}
}

// Load reads the YAML config file and applies overrides to defaults.
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
	applyFileConfig(&cfg, fileCfg)
	if fileCfg.DataDir != "" && fileCfg.DBPath == "" {
		cfg.DBPath = filepath.Join(cfg.DataDir, "agentlab.db")
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

func applyFileConfig(cfg *Config, fileCfg FileConfig) {
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
}

// Validate performs basic validation without exposing secrets.
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
	if _, _, err := net.SplitHostPort(c.BootstrapListen); err != nil {
		return fmt.Errorf("bootstrap_listen must be host:port: %w", err)
	}
	if c.SSHPublicKeyPath != "" && c.SSHPublicKey == "" {
		return fmt.Errorf("ssh_public_key_path is set but empty or unreadable")
	}
	return nil
}
