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
//	agentlab-dashboard --socket /run/agentlab/agentlabd.sock
//	agentlab-dashboard --listen 127.0.0.1:8080 --token my-secret-token
//
// The default listener binds to loopback only, where the dashboard trusts the
// local browser. To expose the dashboard on another interface, pass --listen
// and --browser-token: the token gates every /api/* request from the browser
// and is required for any non-loopback bind. Place a non-loopback deployment
// behind TLS or a trusted encrypted tunnel, since the token travels in
// cleartext over HTTP otherwise (see docs/review-2026-08-11.md C2).
//
// # Flags
//
//	--listen         Address to bind (default "127.0.0.1:8080")
//	--socket         Path to agentlabd Unix socket
//	--token          Bearer token for daemon (outbound) authentication
//	--browser-token  Inbound token gating /api/* (required for non-loopback)
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
		showVersion  bool
		listen       string
		socketPath   string
		token        string
		browserToken string
	)

	flag.BoolVar(&showVersion, "version", false, "print version and exit")
	flag.StringVar(&listen, "listen", "127.0.0.1:8080", "address to listen on (default loopback-only; a non-loopback bind requires --browser-token)")
	flag.StringVar(&socketPath, "socket", "", "path to agentlabd socket (default: /run/agentlab/agentlabd.sock)")
	flag.StringVar(&token, "token", "", "bearer token for daemon authentication")
	flag.StringVar(&browserToken, "browser-token", "", "inbound token gating browser /api/* requests (required for non-loopback binds)")
	flag.Parse()

	if showVersion {
		fmt.Println(buildinfo.String())
		return
	}

	if socketPath == "" {
		socketPath = dashboard.DefaultSocketPath()
	}

	cfg := dashboard.Config{
		Listen:       listen,
		SocketPath:   socketPath,
		Token:        token,
		BrowserToken: browserToken,
	}

	srv := dashboard.NewServer(cfg, log.Default())

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := srv.ListenAndServe(ctx); err != nil {
		log.Fatalf("dashboard error: %v", err)
	}
}
