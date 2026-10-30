package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/agentlab/agentlab/internal/dashboard"
)

func runDashboardCommand(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("dashboard")
	help := bindHelpFlag(fs)
	listen := fs.String("listen", "127.0.0.1:8080", "local dashboard address")
	if err := parseFlags(fs, args, func() {
		fmt.Fprintln(os.Stdout, "Usage: agentlab [connection options] dashboard [--listen 127.0.0.1:8080]")
	}, help, base.jsonOutput); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("dashboard accepts no positional arguments")
	}
	opts, err := base.clientOptions()
	if err != nil {
		return err
	}
	client := newAPIClient(opts, base.timeout)
	if _, err := client.doJSON(ctx, "GET", "/v1/status", nil); err != nil {
		return fmt.Errorf("cannot open dashboard: check the selected connection with agentlab status: %w", err)
	}
	fmt.Fprintf(os.Stderr, "Selected server: %s\n", client.target())
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	return dashboard.NewServer(dashboard.Config{ServerIdentity: opts.ServerIdentity, CredentialBinding: opts.CredentialBinding, Listen: *listen, Endpoint: opts.Endpoint, SocketPath: opts.SocketPath, Token: opts.Token, AllowInsecureHTTP: opts.AllowInsecureHTTP}, log.Default()).ListenAndServe(ctx)
}
