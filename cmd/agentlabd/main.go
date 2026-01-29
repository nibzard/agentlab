package main

import (
	"flag"
	"fmt"

	"github.com/agentlab/agentlab/internal/buildinfo"
	"github.com/agentlab/agentlab/internal/config"
)

func main() {
	var showVersion bool
	var configPath string

	flag.BoolVar(&showVersion, "version", false, "print version and exit")
	flag.StringVar(&configPath, "config", "", "path to config file")
	flag.Parse()

	if showVersion {
		fmt.Println(buildinfo.String())
		return
	}

	cfg := config.DefaultConfig()
	if configPath != "" {
		cfg.ConfigPath = configPath
	}

	fmt.Printf("agentlabd skeleton (config=%s)\n", cfg.ConfigPath)
}
