// ABOUTME: Main daemon entry point for agentlabd service.
// ABOUTME: Loads configuration and starts the daemon which manages sandboxes, jobs, and workspaces.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/agentlab/agentlab/internal/buildinfo"
	"github.com/agentlab/agentlab/internal/config"
	"github.com/agentlab/agentlab/internal/daemon"
)

// main is the entry point for agentlabd.
//
// It parses command-line flags, loads the configuration, and starts the daemon.
// The daemon runs until interrupted by SIGINT or SIGTERM.
//
// Flags:
//   - -version: Print version information and exit
//   - -config: Path to configuration file (optional, uses defaults if not specified)
//
// The daemon will exit with status 1 if configuration fails to load or if
// the daemon encounters a fatal error during execution.
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

	cfg, err := config.Load(configPath)
	if err != nil {
		log.Fatalf("config error: %v", err)
	}

	log.Printf("agentlabd starting (%s)", buildinfo.String())
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := daemon.Run(ctx, cfg); err != nil {
		log.Fatalf("agentlabd error: %v", err)
	}
}
