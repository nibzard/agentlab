// ABOUTME: Web dashboard entry point for the agentlab-dashboard binary.
// ABOUTME: Provides a browser-based UI for managing AgentLab sandboxes, jobs, and workspaces.

// Package main implements the agentlab-dashboard web server.
//
// agentlab-dashboard is an optional component that provides a web UI for
// AgentLab sandbox management. It connects to the agentlabd daemon via
// its Unix socket and proxies API requests.
//
// # Usage
//
//	agentlab-dashboard --listen :8080 --socket /run/agentlab/agentlabd.sock
//	agentlab-dashboard --listen :8080 --token my-secret-token
//
// # Flags
//
//	--listen   Address to bind (default ":8080")
//	--socket   Path to agentlabd Unix socket
//	--token    Bearer token for daemon authentication
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
	"github.com/agentlab/agentlab/internal/dashboard"
)

func main() {
	var (
		showVersion bool
		listen      string
		socketPath  string
		token       string
	)

	flag.BoolVar(&showVersion, "version", false, "print version and exit")
	flag.StringVar(&listen, "listen", ":8080", "address to listen on")
	flag.StringVar(&socketPath, "socket", "", "path to agentlabd socket (default: /run/agentlab/agentlabd.sock)")
	flag.StringVar(&token, "token", "", "bearer token for daemon authentication")
	flag.Parse()

	if showVersion {
		fmt.Println(buildinfo.String())
		return
	}

	if socketPath == "" {
		socketPath = dashboard.DefaultSocketPath()
	}

	cfg := dashboard.Config{
		Listen:     listen,
		SocketPath: socketPath,
		Token:      token,
	}

	srv := dashboard.NewServer(cfg, log.Default())

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := srv.ListenAndServe(ctx); err != nil {
		log.Fatalf("dashboard error: %v", err)
	}
}
