package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strings"
)

// secretsRemoteMode reports whether secrets subcommands should talk to the
// daemon over HTTP rather than editing bundle files locally. A configured
// endpoint means a remote/laptop agent (which has no local age key); an absent
// endpoint means the host operator editing files directly. set-env/set-git are
// always remote — they have no local-file variant.
func secretsRemoteMode(base commonFlags) bool {
	return strings.TrimSpace(base.endpoint) != ""
}

// runSecretsRemote dispatches secrets subcommands over the control API. It
// serves every mutation and the redacted view, so a laptop-resident agent can
// stage LLM keys, git credentials, SSH keys, and per-VM Tailscale enrollment
// without ever touching the host filesystem.
func runSecretsRemote(ctx context.Context, args []string, base commonFlags) error {
	if len(args) == 0 {
		if !base.jsonOutput {
			printSecretsUsage()
		}
		return newUsageError(fmt.Errorf("secrets command is required"), false)
	}
	rest := args[1:]
	switch args[0] {
	case "show":
		return runSecretsShowRemote(ctx, rest, base)
	case "set-env":
		return runSecretsSetEnvCommand(ctx, rest, base)
	case "set-git":
		return runSecretsSetGitCommand(ctx, rest, base)
	case "set-tailscale":
		return runSecretsSetTailscaleRemote(ctx, rest, base)
	case "clear-tailscale":
		return runSecretsClearTailscaleRemote(ctx, rest, base)
	case "add-ssh-key":
		return runSecretsAddSSHKeyRemote(ctx, rest, base)
	case "remove-ssh-key":
		return runSecretsRemoveSSHKeyRemote(ctx, rest, base)
	case "validate":
		return newUsageError(errors.New("secrets validate is a local-only diagnostic; run it on the host without --endpoint"), true)
	default:
		return unknownSubcommandError("secrets", args[0], []string{"show", "set-env", "set-git", "set-tailscale", "clear-tailscale", "add-ssh-key", "remove-ssh-key"})
	}
}

func runSecretsShowRemote(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("secrets show")
	opts := base
	opts.bind(fs)
	help := bindHelpFlag(fs)
	if err := parseFlags(fs, args, printSecretsShowUsage, help, opts.jsonOutput); err != nil {
		return err
	}
	client, err := apiClientFromFlags(opts)
	if err != nil {
		return err
	}
	data, err := client.doJSON(ctx, "GET", "/v1/secrets", nil)
	if err != nil {
		return fmt.Errorf("show secrets: %w", err)
	}
	return printSecretsRemoteResult(opts.jsonOutput, data, "Secrets bundle")
}

func runSecretsSetEnvCommand(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("secrets set-env")
	opts := base
	opts.bind(fs)
	var (
		name      string
		value     string
		valueFile string
		fromFile  string
	)
	help := bindHelpFlag(fs)
	fs.StringVar(&name, "name", "", "environment variable name (single-variable mode)")
	fs.StringVar(&value, "value", "", "environment variable value (single-variable mode)")
	fs.StringVar(&valueFile, "value-file", "", "read the single value from a file (single-variable mode)")
	fs.StringVar(&fromFile, "from-file", "", "bulk: path to a KEY=VAL line file or a JSON object")
	if err := parseFlags(fs, args, printSecretsSetEnvUsage, help, opts.jsonOutput); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return newUsageError(fmt.Errorf("unexpected extra arguments"), true)
	}

	env := map[string]string{}
	if strings.TrimSpace(fromFile) != "" {
		if strings.TrimSpace(name) != "" || strings.TrimSpace(value) != "" || strings.TrimSpace(valueFile) != "" {
			return newUsageError(errors.New("--from-file is mutually exclusive with --name/--value/--value-file"), true)
		}
		parsed, err := parseSecretsEnvFile(fromFile)
		if err != nil {
			return err
		}
		env = parsed
	} else {
		if strings.TrimSpace(name) == "" {
			return newUsageError(errors.New("--name (or --from-file) is required"), true)
		}
		val, err := resolveSecretValue(value, valueFile)
		if err != nil {
			return err
		}
		env[strings.TrimSpace(name)] = val
	}
	if len(env) == 0 {
		return newUsageError(errors.New("no environment variables provided"), true)
	}

	client, err := apiClientFromFlags(opts)
	if err != nil {
		return err
	}
	data, err := client.doJSON(ctx, "PUT", "/v1/secrets/env", map[string]any{"env": env})
	if err != nil {
		return fmt.Errorf("set env: %w", err)
	}
	return printSecretsRemoteResult(opts.jsonOutput, data, fmt.Sprintf("Updated secrets bundle (%d env keys)", len(env)))
}

func runSecretsSetGitCommand(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("secrets set-git")
	opts := base
	opts.bind(fs)
	var (
		token          string
		tokenFile      string
		username       string
		sshKeyFile     string
		knownHostsFile string
	)
	help := bindHelpFlag(fs)
	fs.StringVar(&token, "git-token", "", "git access token (e.g. GitHub PAT)")
	fs.StringVar(&tokenFile, "git-token-file", "", "read the git token from a file")
	fs.StringVar(&username, "username", "", "git username (for HTTPS auth)")
	fs.StringVar(&sshKeyFile, "ssh-key-file", "", "path to an SSH private key")
	fs.StringVar(&knownHostsFile, "known-hosts-file", "", "path to a known_hosts file")
	if err := parseFlags(fs, args, printSecretsSetGitUsage, help, opts.jsonOutput); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return newUsageError(fmt.Errorf("unexpected extra arguments"), true)
	}

	body := map[string]any{}
	if t, err := resolveSecretValue(token, tokenFile); err != nil {
		return err
	} else if t != "" {
		body["token"] = t
	}
	if u := strings.TrimSpace(username); u != "" {
		body["username"] = u
	}
	if f := strings.TrimSpace(sshKeyFile); f != "" {
		data, err := os.ReadFile(f)
		if err != nil {
			return fmt.Errorf("read ssh key %s: %w", f, err)
		}
		body["ssh_private_key"] = string(data)
	}
	if f := strings.TrimSpace(knownHostsFile); f != "" {
		data, err := os.ReadFile(f)
		if err != nil {
			return fmt.Errorf("read known_hosts %s: %w", f, err)
		}
		body["known_hosts"] = string(data)
	}
	if len(body) == 0 {
		return newUsageError(errors.New("at least one of --git-token/--git-token-file/--username/--ssh-key-file/--known-hosts-file is required"), true)
	}

	client, err := apiClientFromFlags(opts)
	if err != nil {
		return err
	}
	data, err := client.doJSON(ctx, "PUT", "/v1/secrets/git", body)
	if err != nil {
		return fmt.Errorf("set git credentials: %w", err)
	}
	return printSecretsRemoteResult(opts.jsonOutput, data, "Updated secrets bundle (git credentials)")
}

func runSecretsSetTailscaleRemote(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("secrets set-tailscale")
	opts := base
	opts.bind(fs)
	var (
		authKey          string
		authKeyFile      string
		adminAPIKey      string
		adminAPIKeyFile  string
		tailnet          string
		hostnameTemplate string
		tags             stringListFlag
		extraArgs        stringListFlag
	)
	help := bindHelpFlag(fs)
	fs.StringVar(&authKey, "authkey", "", "shared reusable Tailscale auth key (fallback for per-VM minting)")
	fs.StringVar(&authKeyFile, "authkey-file", "", "read the shared Tailscale auth key from a file")
	fs.StringVar(&adminAPIKey, "admin-api-key", "", "Tailscale Admin API key (tskey-api-...) to mint per-VM auth keys")
	fs.StringVar(&adminAPIKeyFile, "admin-api-key-file", "", "read the Tailscale Admin API key from a file")
	fs.StringVar(&tailnet, "tailnet", "", "tailnet to mint keys in (default: the key's own tailnet, \"-\")")
	fs.StringVar(&hostnameTemplate, "hostname-template", "", "per-VM hostname template (supports {vmid} and {name})")
	fs.Var(&tags, "tag", "Tailscale tag (repeatable)")
	fs.Var(&extraArgs, "extra-arg", "additional `tailscale up` arg (repeatable)")
	if err := parseFlags(fs, args, printSecretsSetTailscaleUsage, help, opts.jsonOutput); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return newUsageError(fmt.Errorf("unexpected extra arguments"), true)
	}

	body := map[string]any{}
	if k, err := resolveSecretValue(authKey, authKeyFile); err != nil {
		return err
	} else if k != "" {
		body["authkey"] = k
	}
	if k, err := resolveSecretValue(adminAPIKey, adminAPIKeyFile); err != nil {
		return err
	} else if k != "" {
		body["admin_api_key"] = k
	}
	if t := strings.TrimSpace(tailnet); t != "" {
		body["tailnet"] = t
	}
	if h := strings.TrimSpace(hostnameTemplate); h != "" {
		body["hostname_template"] = h
	}
	if len(tags) > 0 {
		body["tags"] = []string(tags)
	}
	if len(extraArgs) > 0 {
		body["extra_args"] = []string(extraArgs)
	}
	if len(body) == 0 {
		return newUsageError(errors.New("at least one of --authkey/--authkey-file/--admin-api-key/--admin-api-key-file/--tailnet/--hostname-template/--tag/--extra-arg is required"), true)
	}

	client, err := apiClientFromFlags(opts)
	if err != nil {
		return err
	}
	data, err := client.doJSON(ctx, "PUT", "/v1/secrets/tailscale", body)
	if err != nil {
		return fmt.Errorf("set tailscale: %w", err)
	}
	return printSecretsRemoteResult(opts.jsonOutput, data, "Updated secrets bundle (tailscale enrollment)")
}

func runSecretsClearTailscaleRemote(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("secrets clear-tailscale")
	opts := base
	opts.bind(fs)
	help := bindHelpFlag(fs)
	if err := parseFlags(fs, args, printSecretsClearTailscaleUsage, help, opts.jsonOutput); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return newUsageError(fmt.Errorf("unexpected extra arguments"), true)
	}
	client, err := apiClientFromFlags(opts)
	if err != nil {
		return err
	}
	data, err := client.doJSON(ctx, "DELETE", "/v1/secrets/tailscale", nil)
	if err != nil {
		return fmt.Errorf("clear tailscale: %w", err)
	}
	return printSecretsRemoteResult(opts.jsonOutput, data, "Updated secrets bundle (tailscale cleared)")
}

func runSecretsAddSSHKeyRemote(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("secrets add-ssh-key")
	opts := base
	opts.bind(fs)
	var (
		name    string
		key     string
		keyFile string
	)
	help := bindHelpFlag(fs)
	fs.StringVar(&name, "name", "", "key name in the bundle")
	fs.StringVar(&key, "key", "", "SSH public key value")
	fs.StringVar(&keyFile, "key-file", "", "path to an SSH public key file")
	if err := parseFlags(fs, args, printSecretsAddSSHKeyUsage, help, opts.jsonOutput); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return newUsageError(fmt.Errorf("unexpected extra arguments"), true)
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return newUsageError(errors.New("--name is required"), true)
	}
	publicKey, err := resolvePublicKeyInput(key, keyFile)
	if err != nil {
		return err
	}
	if _, err := parseSSHPublicKey(publicKey); err != nil {
		return err
	}

	client, err := apiClientFromFlags(opts)
	if err != nil {
		return err
	}
	data, err := client.doJSON(ctx, "POST", "/v1/secrets/ssh-keys", map[string]any{"name": name, "key": publicKey})
	if err != nil {
		return fmt.Errorf("add ssh key: %w", err)
	}
	return printSecretsRemoteResult(opts.jsonOutput, data, fmt.Sprintf("Updated secrets bundle (ssh key %q)", name))
}

func runSecretsRemoveSSHKeyRemote(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("secrets remove-ssh-key")
	opts := base
	opts.bind(fs)
	var name string
	help := bindHelpFlag(fs)
	fs.StringVar(&name, "name", "", "key name in the bundle")
	if err := parseFlags(fs, args, printSecretsRemoveSSHKeyUsage, help, opts.jsonOutput); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return newUsageError(fmt.Errorf("unexpected extra arguments"), true)
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return newUsageError(errors.New("--name is required"), true)
	}

	client, err := apiClientFromFlags(opts)
	if err != nil {
		return err
	}
	data, err := client.doJSON(ctx, "DELETE", "/v1/secrets/ssh-keys/"+url.PathEscape(name), nil)
	if err != nil {
		return fmt.Errorf("remove ssh key: %w", err)
	}
	return printSecretsRemoteResult(opts.jsonOutput, data, fmt.Sprintf("Updated secrets bundle (ssh key %q removed)", name))
}

// printSecretsRemoteResult renders a V1SecretsMutationResponse. In JSON mode it
// pretty-prints the raw payload; otherwise it prints a human-readable summary of
// the redacted bundle state.
func printSecretsRemoteResult(jsonOutput bool, data []byte, action string) error {
	if jsonOutput {
		var buf bytes.Buffer
		if err := json.Indent(&buf, data, "", "  "); err == nil {
			fmt.Fprintln(os.Stdout, buf.String())
		} else {
			fmt.Fprintln(os.Stdout, string(data))
		}
		return nil
	}
	var resp secretsRemoteResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		fmt.Fprintln(os.Stdout, action)
		return nil
	}
	fmt.Fprintln(os.Stdout, action)
	if resp.Bundle != "" {
		fmt.Fprintf(os.Stdout, "  bundle: %s\n", resp.Bundle)
	}
	if resp.Path != "" {
		fmt.Fprintf(os.Stdout, "  path: %s\n", resp.Path)
	}
	if n := len(resp.Secrets.Env); n > 0 {
		fmt.Fprintf(os.Stdout, "  env (%d): %s\n", n, strings.Join(sortedStringKeys(resp.Secrets.Env), ", "))
	}
	if resp.Secrets.Git != nil {
		fmt.Fprintln(os.Stdout, "  git: configured")
	}
	if resp.Secrets.Tailscale != nil {
		fmt.Fprintf(os.Stdout, "  tailscale: configured (authkey=%v, admin-key=%v", resp.Secrets.Tailscale.AuthKeyConfigured, resp.Secrets.Tailscale.AdminAPIKeyConfigured)
		if t := resp.Secrets.Tailscale.Tailnet; t != "" {
			fmt.Fprintf(os.Stdout, ", tailnet=%s", t)
		}
		fmt.Fprintln(os.Stdout, ")")
	}
	if n := len(resp.Secrets.SSH); n > 0 {
		fmt.Fprintf(os.Stdout, "  ssh keys (%d): %s\n", n, strings.Join(sortedStringKeys(resp.Secrets.SSH), ", "))
	}
	return nil
}

func sortedStringKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// resolveSecretValue returns a secret from an inline flag or a file, enforcing
// mutual exclusivity. Reading from a file keeps the value out of shell history.
func resolveSecretValue(value, valueFile string) (string, error) {
	value = strings.TrimSpace(value)
	valueFile = strings.TrimSpace(valueFile)
	if value != "" && valueFile != "" {
		return "", errors.New("inline value and --value-file are mutually exclusive")
	}
	if valueFile != "" {
		data, err := os.ReadFile(valueFile)
		if err != nil {
			return "", fmt.Errorf("read %s: %w", valueFile, err)
		}
		return strings.TrimSpace(string(data)), nil
	}
	return value, nil
}

// parseSecretsEnvFile reads a bulk env file: a JSON object ({"KEY":"VAL"}) or
// KEY=VAL lines (with optional `export ` prefix, `#` comments, and quoted
// values).
func parseSecretsEnvFile(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return map[string]string{}, nil
	}
	if strings.HasPrefix(trimmed, "{") {
		var parsed map[string]string
		if err := json.Unmarshal([]byte(trimmed), &parsed); err != nil {
			return nil, fmt.Errorf("parse JSON env file %s: %w", path, err)
		}
		env := map[string]string{}
		for k, v := range parsed {
			if k = strings.TrimSpace(k); k != "" {
				env[k] = v
			}
		}
		return env, nil
	}
	env := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		eq := strings.Index(line, "=")
		if eq < 0 {
			return nil, fmt.Errorf("invalid env line in %s (expected KEY=VALUE): %q", path, line)
		}
		key := strings.TrimSpace(line[:eq])
		val := line[eq+1:]
		if len(val) >= 2 && (val[0] == '"' && val[len(val)-1] == '"' || val[0] == '\'' && val[len(val)-1] == '\'') {
			val = val[1 : len(val)-1]
		}
		if key == "" {
			return nil, fmt.Errorf("invalid env line in %s (empty key): %q", path, line)
		}
		env[key] = val
	}
	return env, nil
}

type secretsRemoteResponse struct {
	Bundle  string            `json:"bundle"`
	Path    string            `json:"path,omitempty"`
	Secrets secretsRemoteView `json:"secrets"`
}

type secretsRemoteView struct {
	Env       map[string]string                  `json:"env,omitempty"`
	Git       *secretsRemoteGitView              `json:"git,omitempty"`
	SSH       map[string]secretsRemoteSSHKeyView `json:"ssh,omitempty"`
	Tailscale *secretsRemoteTailscaleView        `json:"tailscale,omitempty"`
}

type secretsRemoteGitView struct {
	Token         string `json:"token,omitempty"`
	Username      string `json:"username,omitempty"`
	SSHPrivateKey string `json:"ssh_private_key,omitempty"`
	SSHPublicKey  string `json:"ssh_public_key,omitempty"`
	KnownHosts    string `json:"known_hosts,omitempty"`
}

type secretsRemoteSSHKeyView struct {
	Key     string `json:"key"`
	Type    string `json:"type,omitempty"`
	Comment string `json:"comment,omitempty"`
}

type secretsRemoteTailscaleView struct {
	HostnameTemplate      string   `json:"hostname_template,omitempty"`
	Tags                  []string `json:"tags,omitempty"`
	ExtraArgs             []string `json:"extra_args,omitempty"`
	Tailnet               string   `json:"tailnet,omitempty"`
	AuthKeyConfigured     bool     `json:"authkey_configured"`
	AdminAPIKeyConfigured bool     `json:"admin_api_key_configured"`
}
