package config

import "path/filepath"

// Config holds daemon configuration paths and listener settings.
type Config struct {
	ConfigPath      string
	ProfilesDir     string
	DataDir         string
	LogDir          string
	RunDir          string
	SocketPath      string
	DBPath          string
	BootstrapListen string
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
	}
}
