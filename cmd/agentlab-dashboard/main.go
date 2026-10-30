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
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/agentlab/agentlab/internal/buildinfo"
	"github.com/agentlab/agentlab/internal/connection"
	"github.com/agentlab/agentlab/internal/dashboard"
)

// parseOptions parses explicit flags before opening any saved credential store.
func parseOptions(ctx context.Context, args []string, output io.Writer) (dashboard.Config, bool, error) {
	cfg := dashboard.Config{}
	var showVersion bool
	var contextName string
	fs := flag.NewFlagSet("agentlab-dashboard", flag.ContinueOnError)
	fs.SetOutput(output)
	fs.BoolVar(&showVersion, "version", false, "print version and exit")
	fs.StringVar(&contextName, "context", "", "named server context")
	fs.StringVar(&cfg.Listen, "listen", "127.0.0.1:8080", "address to listen on (a non-loopback bind requires --browser-token)")
	fs.StringVar(&cfg.SocketPath, "socket", "", "path to agentlabd socket")
	fs.StringVar(&cfg.Endpoint, "endpoint", "", "remote control plane endpoint")
	fs.BoolVar(&cfg.AllowInsecureHTTP, "allow-insecure-http", false, "permit HTTP inside a trusted tunnel")
	fs.StringVar(&cfg.Token, "token", "", "bearer token for daemon authentication")
	fs.StringVar(&cfg.BrowserToken, "browser-token", "", "inbound browser token (required for non-loopback binds)")
	if err := fs.Parse(args); err != nil {
		return cfg, false, err
	}
	if showVersion {
		return cfg, true, nil
	}
	if fs.NArg() != 0 {
		return cfg, false, fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	overrides := connection.Overrides{}
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "endpoint":
			overrides.Endpoint = &cfg.Endpoint
		case "token":
			overrides.Token = &cfg.Token
		case "socket":
			overrides.Socket = &cfg.SocketPath
		case "allow-insecure-http":
			overrides.AllowInsecureHTTP = &cfg.AllowInsecureHTTP
		}
	})
	if contextName == "" {
		contextName = strings.TrimSpace(os.Getenv("AGENTLAB_CONTEXT"))
	}
	root, err := connection.ConfigDir()
	if err != nil {
		return cfg, false, err
	}
	selected, err := connection.Prepare(ctx, connection.PrepareOptions{
		Root: root, Context: contextName, Environment: connection.EnvironmentOverrides(), DefaultSocket: dashboard.DefaultSocketPath(),
		// An explicit target can repair a removed active selection. A named context
		// still requires a matching metadata record.
		AllowUnselected: contextName == "" && (overrides.Endpoint != nil || overrides.Socket != nil),
	})
	if err != nil {
		return cfg, false, err
	}
	resolved, err := selected.Resolve(ctx, overrides)
	if err != nil {
		return cfg, false, err
	}
	cfg.Endpoint, cfg.Token = resolved.Endpoint, resolved.Token
	cfg.ServerIdentity = resolved.ServerIdentity
	cfg.CredentialBinding = resolved.CredentialBinding
	cfg.SocketPath, cfg.AllowInsecureHTTP = resolved.SocketPath, resolved.AllowInsecureHTTP
	return cfg, false, nil
}

func main() {
	cfg, showVersion, err := parseOptions(context.Background(), os.Args[1:], os.Stderr)
	if errors.Is(err, flag.ErrHelp) {
		return
	}
	if err != nil {
		log.Fatalf("load connection: %v", err)
	}
	if showVersion {
		fmt.Println(buildinfo.String())
		return
	}
	srv := dashboard.NewServer(cfg, log.Default())
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := srv.ListenAndServe(ctx); err != nil {
		log.Fatalf("dashboard error: %v", err)
	}
}
