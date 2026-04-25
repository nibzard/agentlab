package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
)

const integrationUsage = `Usage:
  agentlab integration add --name <name> --type <type> [--target <url>] --secret <secret> [flags]
  agentlab integration list
  agentlab integration rm <name>
  agentlab integration status [--sandbox <name>]

Integration types:
  http-proxy   HTTP reverse proxy that injects headers/tokens into requests
  git-proxy    Git credential proxy that injects credentials into clone/push

Secret injection modes (--secret-type):
  bearer      Set Authorization: Bearer <secret> (default)
  header      Set custom header (--secret-header, default X-Api-Key) to secret
  basic-auth  Set Authorization: Basic <base64(user:secret)>

Attachment modes (--attach):
  sandbox:<name>  Attach to a specific sandbox by name
  tag:<value>     Attach to all sandboxes with a given tag
  auto:all        Attach to all sandboxes (default)

Examples:
  # Add an HTTP proxy integration for an API:
  agentlab integration add --name myapi --type http-proxy --target https://api.example.com --secret sk-... --attach auto:all

  # Add a GitHub integration:
  agentlab integration add --name github --type git-proxy --target https://github.com --secret ghp_... --username x-access-token --attach auto:all

  # List all integrations:
  agentlab integration list

  # Remove an integration:
  agentlab integration rm myapi

  # Show integrations active for a sandbox:
  agentlab integration status --sandbox mybox
`

func printIntegrationUsage() {
	fmt.Fprint(os.Stdout, integrationUsage)
}

// runIntegrationCommand dispatches integration subcommands.
func runIntegrationCommand(ctx context.Context, args []string, base commonFlags) error {
	if len(args) == 0 || isHelpToken(args[0]) {
		printIntegrationUsage()
		return errHelp
	}
	switch args[0] {
	case "add":
		return runIntegrationAdd(ctx, args[1:], base)
	case "list":
		return runIntegrationList(ctx, args[1:], base)
	case "rm":
		return runIntegrationRm(ctx, args[1:], base)
	case "status":
		return runIntegrationStatus(ctx, args[1:], base)
	default:
		return newUsageError(fmt.Errorf("unknown integration subcommand %q", args[0]), true)
	}
}

func runIntegrationAdd(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("integration add")
	opts := base
	opts.bind(fs)

	var (
		name         string
		integType    string
		target       string
		secret       string
		secretType   string
		secretHeader string
		username     string
		attach       string
		help         bool
	)
	fs.StringVar(&name, "name", "", "integration name (unique identifier)")
	fs.StringVar(&integType, "type", "http-proxy", "integration type (http-proxy or git-proxy)")
	fs.StringVar(&target, "target", "", "target URL for HTTP proxy (e.g., https://api.example.com)")
	fs.StringVar(&secret, "secret", "", "secret value (API key, token, password)")
	fs.StringVar(&secretType, "secret-type", "bearer", "how to inject secret (bearer, header, basic-auth)")
	fs.StringVar(&secretHeader, "secret-header", "", "custom header name (for header secret-type)")
	fs.StringVar(&username, "username", "", "username for basic-auth or git proxy")
	fs.StringVar(&attach, "attach", "auto:all", "attachment mode (sandbox:<name>, tag:<value>, auto:all)")
	fs.BoolVar(&help, "help", false, "show help")
	fs.BoolVar(&help, "h", false, "show help")

	if err := parseFlags(fs, args, printIntegrationUsage, &help, opts.jsonOutput); err != nil {
		return err
	}

	name = strings.TrimSpace(name)
	if name == "" {
		return newUsageError(errors.New("--name is required"), true)
	}
	if secret == "" {
		return newUsageError(errors.New("--secret is required"), true)
	}

	reqBody := map[string]string{
		"name":          name,
		"type":          integType,
		"target":        target,
		"secret":        secret,
		"secret_type":   secretType,
		"secret_header": secretHeader,
		"username":      username,
		"attach":        attach,
	}

	client, err := apiClientFromFlags(opts)
	if err != nil {
		return err
	}
	data, err := client.doJSON(ctx, "POST", "/v1/integrations", reqBody)
	if err != nil {
		return fmt.Errorf("create integration: %w", err)
	}

	var resp V1IntegrationCLIResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return fmt.Errorf("parse response: %w", err)
	}

	if opts.jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(resp)
	}

	fmt.Printf("Integration %q created (type=%s, attach=%s)\n", resp.Name, resp.Type, resp.AttachMode)
	fmt.Printf("  Proxy path: %s\n", resp.ProxyPath)
	return nil
}

func runIntegrationList(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("integration list")
	opts := base
	opts.bind(fs)
	help := bindHelpFlag(fs)

	if err := parseFlags(fs, args, printIntegrationUsage, help, opts.jsonOutput); err != nil {
		return err
	}

	client, err := apiClientFromFlags(opts)
	if err != nil {
		return err
	}
	data, err := client.doJSON(ctx, "GET", "/v1/integrations", nil)
	if err != nil {
		return fmt.Errorf("list integrations: %w", err)
	}

	var resp V1IntegrationsCLIResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return fmt.Errorf("parse response: %w", err)
	}

	if opts.jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(resp)
	}

	if len(resp.Integrations) == 0 {
		fmt.Println("No integrations configured.")
		return nil
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tTYPE\tTARGET\tATTACH\tSECRET-TYPE\tPROXY-PATH")
	for _, integ := range resp.Integrations {
		target := integ.Target
		if target == "" {
			target = "-"
		}
		attach := integ.AttachMode
		if integ.AttachSelector != "" {
			attach += ":" + integ.AttachSelector
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
			integ.Name, integ.Type, target, attach, integ.SecretType, integ.ProxyPath)
	}
	return tw.Flush()
}

func runIntegrationRm(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("integration rm")
	opts := base
	opts.bind(fs)
	help := bindHelpFlag(fs)

	if err := parseFlags(fs, args, printIntegrationUsage, help, opts.jsonOutput); err != nil {
		return err
	}

	if fs.NArg() == 0 {
		return newUsageError(errors.New("integration name is required"), true)
	}
	name := fs.Arg(0)

	client, err := apiClientFromFlags(opts)
	if err != nil {
		return err
	}
	data, err := client.doJSON(ctx, "DELETE", "/v1/integrations/"+name, nil)
	if err != nil {
		return fmt.Errorf("delete integration: %w", err)
	}

	if opts.jsonOutput {
		var resp map[string]string
		if err := json.Unmarshal(data, &resp); err != nil {
			return fmt.Errorf("parse response: %w", err)
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(resp)
	}

	fmt.Printf("Integration %q deleted.\n", name)
	return nil
}

func runIntegrationStatus(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("integration status")
	opts := base
	opts.bind(fs)

	var sandboxName string
	help := bindHelpFlag(fs)
	fs.StringVar(&sandboxName, "sandbox", "", "sandbox name to show integrations for")

	if err := parseFlags(fs, args, printIntegrationUsage, help, opts.jsonOutput); err != nil {
		return err
	}

	if sandboxName == "" {
		return newUsageError(errors.New("--sandbox is required"), true)
	}

	client, err := apiClientFromFlags(opts)
	if err != nil {
		return err
	}
	data, err := client.doJSON(ctx, "GET", "/v1/integrations", nil)
	if err != nil {
		return fmt.Errorf("list integrations: %w", err)
	}

	var resp V1IntegrationsCLIResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return fmt.Errorf("parse response: %w", err)
	}

	var matched []V1IntegrationCLIResponse
	for _, integ := range resp.Integrations {
		if integ.AttachMode == "auto:all" ||
			(integ.AttachMode == "sandbox" && integ.AttachSelector == sandboxName) {
			matched = append(matched, integ)
		}
	}

	if opts.jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]any{
			"sandbox_name": sandboxName,
			"integrations": matched,
		})
	}

	if len(matched) == 0 {
		fmt.Printf("No integrations active for sandbox %q.\n", sandboxName)
		return nil
	}

	fmt.Printf("Integrations active for sandbox %q:\n", sandboxName)
	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tTYPE\tTARGET\tPROXY-PATH")
	for _, integ := range matched {
		target := integ.Target
		if target == "" {
			target = "-"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", integ.Name, integ.Type, target, integ.ProxyPath)
	}
	return tw.Flush()
}

// V1IntegrationCLIResponse mirrors the daemon API response type for CLI use.
type V1IntegrationCLIResponse struct {
	Name           string `json:"name"`
	Type           string `json:"type"`
	Target         string `json:"target,omitempty"`
	SecretType     string `json:"secret_type"`
	SecretHeader   string `json:"secret_header,omitempty"`
	Username       string `json:"username,omitempty"`
	AttachMode     string `json:"attach_mode"`
	AttachSelector string `json:"attach_selector,omitempty"`
	ProxyPath      string `json:"proxy_path"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
}

type V1IntegrationsCLIResponse struct {
	Integrations []V1IntegrationCLIResponse `json:"integrations"`
}
