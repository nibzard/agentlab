package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	agentconfig "github.com/agentlab/agentlab/internal/config"
	bundles "github.com/agentlab/agentlab/internal/secrets"
)

type secretsLocalOptions struct {
	configPath     string
	bundle         string
	dir            string
	ageKeyPath     string
	sopsPath       string
	allowPlaintext bool
}

type stringListFlag []string

func (s *stringListFlag) String() string {
	if s == nil {
		return ""
	}
	return strings.Join(*s, ",")
}

func (s *stringListFlag) Set(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("value cannot be empty")
	}
	*s = append(*s, value)
	return nil
}

func runSecretsCommand(ctx context.Context, args []string, base commonFlags) error {
	_ = ctx
	if len(args) == 0 {
		if !base.jsonOutput {
			printSecretsUsage()
		}
		return newUsageError(fmt.Errorf("secrets command is required"), false)
	}
	switch args[0] {
	case "show":
		return runSecretsShowCommand(args[1:], base)
	case "validate":
		return runSecretsValidateCommand(args[1:], base)
	case "add-ssh-key":
		return runSecretsAddSSHKeyCommand(args[1:], base)
	case "remove-ssh-key":
		return runSecretsRemoveSSHKeyCommand(args[1:], base)
	case "set-tailscale":
		return runSecretsSetTailscaleCommand(args[1:], base)
	case "clear-tailscale":
		return runSecretsClearTailscaleCommand(args[1:], base)
	default:
		return unknownSubcommandError("secrets", args[0], []string{"show", "validate", "add-ssh-key", "remove-ssh-key", "set-tailscale", "clear-tailscale"})
	}
}

func runSecretsShowCommand(args []string, base commonFlags) error {
	fs := newFlagSet("secrets show")
	opts := bindSecretsLocalFlags(fs)
	format := "yaml"
	reveal := false
	help := bindHelpFlag(fs)
	fs.StringVar(&format, "format", format, "output format: yaml or json")
	fs.BoolVar(&reveal, "reveal", false, "show sensitive values instead of redacting them")
	if err := parseFlags(fs, args, printSecretsShowUsage, help, base.jsonOutput); err != nil {
		return err
	}
	if fs.NArg() > 1 {
		if !base.jsonOutput {
			printSecretsShowUsage()
		}
		return fmt.Errorf("expected at most one bundle argument")
	}
	if fs.NArg() == 1 {
		opts.bundle = strings.TrimSpace(fs.Arg(0))
	}
	store, bundleName, _, err := secretsStoreFromOptions(*opts)
	if err != nil {
		return err
	}
	bundle, err := store.Load(context.Background(), bundleName)
	if err != nil {
		return err
	}
	if !reveal {
		bundle = bundle.Redacted()
	}
	if base.jsonOutput || strings.EqualFold(strings.TrimSpace(format), "json") {
		data, err := json.MarshalIndent(bundle.Normalized(), "", "  ")
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(os.Stdout, "%s\n", data)
		return err
	}
	if !strings.EqualFold(strings.TrimSpace(format), "yaml") {
		return fmt.Errorf("unsupported format %q", format)
	}
	data, err := bundles.MarshalYAML(bundle)
	if err != nil {
		return err
	}
	_, err = os.Stdout.Write(data)
	return err
}

func runSecretsValidateCommand(args []string, base commonFlags) error {
	fs := newFlagSet("secrets validate")
	opts := bindSecretsLocalFlags(fs)
	help := bindHelpFlag(fs)
	if err := parseFlags(fs, args, printSecretsValidateUsage, help, base.jsonOutput); err != nil {
		return err
	}
	if fs.NArg() > 1 {
		if !base.jsonOutput {
			printSecretsValidateUsage()
		}
		return fmt.Errorf("expected at most one bundle argument")
	}
	if fs.NArg() == 1 {
		opts.bundle = strings.TrimSpace(fs.Arg(0))
	}
	store, bundleName, cfg, err := secretsStoreFromOptions(*opts)
	if err != nil {
		return err
	}
	path, err := store.ResolvePath(bundleName)
	if err != nil {
		return err
	}
	bundle, err := store.Load(context.Background(), bundleName)
	if err != nil {
		return err
	}
	summary := map[string]any{
		"valid":                true,
		"bundle":               bundleName,
		"path":                 path,
		"ssh_key_count":        sshKeyCount(bundle),
		"tailscale_configured": bundle.Tailscale != nil,
		"secrets_dir":          cfg.SecretsDir,
	}
	if base.jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetEscapeHTML(false)
		return enc.Encode(summary)
	}
	fmt.Fprintf(os.Stdout, "Valid bundle: %s\n", path)
	fmt.Fprintf(os.Stdout, "SSH keys: %d\n", sshKeyCount(bundle))
	if bundle.Tailscale != nil {
		fmt.Fprintln(os.Stdout, "Tailscale: configured")
	} else {
		fmt.Fprintln(os.Stdout, "Tailscale: not configured")
	}
	return nil
}

func runSecretsAddSSHKeyCommand(args []string, base commonFlags) error {
	fs := newFlagSet("secrets add-ssh-key")
	opts := bindSecretsLocalFlags(fs)
	name := ""
	key := ""
	keyFile := ""
	help := bindHelpFlag(fs)
	fs.StringVar(&name, "name", "", "key name in the bundle")
	fs.StringVar(&key, "key", "", "ssh public key value")
	fs.StringVar(&keyFile, "key-file", "", "path to ssh public key file")
	if err := parseFlags(fs, args, printSecretsAddSSHKeyUsage, help, base.jsonOutput); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		if !base.jsonOutput {
			printSecretsAddSSHKeyUsage()
		}
		return fmt.Errorf("unexpected extra arguments")
	}
	store, bundleName, _, err := secretsStoreFromOptions(*opts)
	if err != nil {
		return err
	}
	publicKey, err := resolvePublicKeyInput(key, keyFile)
	if err != nil {
		return err
	}
	keyRecord, err := parseSSHPublicKey(publicKey)
	if err != nil {
		return err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("name is required")
	}
	result, path, err := mutateSecretBundle(store, bundleName, func(bundle *bundles.Bundle) error {
		if bundle.SSH == nil {
			bundle.SSH = &bundles.SSHKeysBundle{Keys: map[string]bundles.SSHPublicKey{}}
		}
		if bundle.SSH.Keys == nil {
			bundle.SSH.Keys = map[string]bundles.SSHPublicKey{}
		}
		bundle.SSH.Keys[name] = keyRecord
		return nil
	})
	if err != nil {
		return err
	}
	return printSecretsMutationResult(base.jsonOutput, map[string]any{
		"updated":       true,
		"path":          path,
		"bundle":        bundleName,
		"ssh_key_count": sshKeyCount(result),
		"key_name":      name,
	}, fmt.Sprintf("Updated %s (%d SSH keys)", path, sshKeyCount(result)))
}

func runSecretsRemoveSSHKeyCommand(args []string, base commonFlags) error {
	fs := newFlagSet("secrets remove-ssh-key")
	opts := bindSecretsLocalFlags(fs)
	name := ""
	help := bindHelpFlag(fs)
	fs.StringVar(&name, "name", "", "key name in the bundle")
	if err := parseFlags(fs, args, printSecretsRemoveSSHKeyUsage, help, base.jsonOutput); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		if !base.jsonOutput {
			printSecretsRemoveSSHKeyUsage()
		}
		return fmt.Errorf("unexpected extra arguments")
	}
	store, bundleName, _, err := secretsStoreFromOptions(*opts)
	if err != nil {
		return err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("name is required")
	}
	removed := false
	result, path, err := mutateSecretBundle(store, bundleName, func(bundle *bundles.Bundle) error {
		if bundle.SSH == nil || len(bundle.SSH.Keys) == 0 {
			return fmt.Errorf("ssh key %q not found", name)
		}
		if _, ok := bundle.SSH.Keys[name]; !ok {
			return fmt.Errorf("ssh key %q not found", name)
		}
		delete(bundle.SSH.Keys, name)
		removed = true
		if len(bundle.SSH.Keys) == 0 {
			bundle.SSH = nil
		}
		return nil
	})
	if err != nil {
		return err
	}
	return printSecretsMutationResult(base.jsonOutput, map[string]any{
		"updated":       removed,
		"path":          path,
		"bundle":        bundleName,
		"ssh_key_count": sshKeyCount(result),
		"key_name":      name,
	}, fmt.Sprintf("Updated %s (%d SSH keys)", path, sshKeyCount(result)))
}

func runSecretsSetTailscaleCommand(args []string, base commonFlags) error {
	fs := newFlagSet("secrets set-tailscale")
	opts := bindSecretsLocalFlags(fs)
	authKey := ""
	hostnameTemplate := ""
	var tags stringListFlag
	var extraArgs stringListFlag
	help := bindHelpFlag(fs)
	fs.StringVar(&authKey, "authkey", "", "tailscale auth key")
	fs.StringVar(&hostnameTemplate, "hostname-template", "", "hostname template (supports {vmid} and {name})")
	fs.Var(&tags, "tag", "tailscale tag (repeatable)")
	fs.Var(&extraArgs, "extra-arg", "additional tailscale up arg (repeatable)")
	if err := parseFlags(fs, args, printSecretsSetTailscaleUsage, help, base.jsonOutput); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		if !base.jsonOutput {
			printSecretsSetTailscaleUsage()
		}
		return fmt.Errorf("unexpected extra arguments")
	}
	store, bundleName, _, err := secretsStoreFromOptions(*opts)
	if err != nil {
		return err
	}
	result, path, err := mutateSecretBundle(store, bundleName, func(bundle *bundles.Bundle) error {
		if bundle.Tailscale == nil {
			bundle.Tailscale = &bundles.TailscaleBundle{}
		}
		if trimmed := strings.TrimSpace(authKey); trimmed != "" {
			bundle.Tailscale.AuthKey = trimmed
		}
		if trimmed := strings.TrimSpace(hostnameTemplate); trimmed != "" {
			bundle.Tailscale.HostnameTemplate = trimmed
		}
		if len(tags) > 0 {
			bundle.Tailscale.Tags = dedupeNonEmpty([]string(tags))
		}
		if len(extraArgs) > 0 {
			bundle.Tailscale.ExtraArgs = dedupeNonEmpty([]string(extraArgs))
		}
		if strings.TrimSpace(bundle.Tailscale.AuthKey) == "" {
			return fmt.Errorf("tailscale authkey is required")
		}
		return nil
	})
	if err != nil {
		return err
	}
	return printSecretsMutationResult(base.jsonOutput, map[string]any{
		"updated":              true,
		"path":                 path,
		"bundle":               bundleName,
		"tailscale_configured": result.Tailscale != nil,
		"tags":                 result.GetTailscaleTags(),
	}, fmt.Sprintf("Updated %s (tailscale configured)", path))
}

func runSecretsClearTailscaleCommand(args []string, base commonFlags) error {
	fs := newFlagSet("secrets clear-tailscale")
	opts := bindSecretsLocalFlags(fs)
	help := bindHelpFlag(fs)
	if err := parseFlags(fs, args, printSecretsClearTailscaleUsage, help, base.jsonOutput); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		if !base.jsonOutput {
			printSecretsClearTailscaleUsage()
		}
		return fmt.Errorf("unexpected extra arguments")
	}
	store, bundleName, _, err := secretsStoreFromOptions(*opts)
	if err != nil {
		return err
	}
	result, path, err := mutateSecretBundle(store, bundleName, func(bundle *bundles.Bundle) error {
		bundle.Tailscale = nil
		return nil
	})
	if err != nil {
		return err
	}
	return printSecretsMutationResult(base.jsonOutput, map[string]any{
		"updated":              true,
		"path":                 path,
		"bundle":               bundleName,
		"tailscale_configured": result.Tailscale != nil,
	}, fmt.Sprintf("Updated %s (tailscale cleared)", path))
}

func bindSecretsLocalFlags(fs *flag.FlagSet) *secretsLocalOptions {
	cfg := agentconfig.DefaultConfig()
	opts := &secretsLocalOptions{
		configPath: cfg.ConfigPath,
	}
	fs.StringVar(&opts.configPath, "config", opts.configPath, "path to agentlab config file")
	fs.StringVar(&opts.bundle, "bundle", "", "bundle name or path (defaults to config secrets_bundle)")
	fs.StringVar(&opts.dir, "dir", "", "secrets directory override")
	fs.StringVar(&opts.ageKeyPath, "age-key", "", "age key path override")
	fs.StringVar(&opts.sopsPath, "sops-path", "", "sops binary path override")
	fs.BoolVar(&opts.allowPlaintext, "allow-plaintext", false, "allow reading or writing plaintext bundles")
	return opts
}

func secretsStoreFromOptions(opts secretsLocalOptions) (bundles.Store, string, agentconfig.Config, error) {
	cfg, err := loadSecretsConfig(opts.configPath)
	if err != nil {
		return bundles.Store{}, "", agentconfig.Config{}, err
	}
	store := bundles.Store{
		Dir:            cfg.SecretsDir,
		AgeKeyPath:     cfg.SecretsAgeKeyPath,
		SopsPath:       cfg.SecretsSopsPath,
		AllowPlaintext: opts.allowPlaintext,
	}
	if trimmed := strings.TrimSpace(opts.dir); trimmed != "" {
		store.Dir = trimmed
	}
	if trimmed := strings.TrimSpace(opts.ageKeyPath); trimmed != "" {
		store.AgeKeyPath = trimmed
	}
	if trimmed := strings.TrimSpace(opts.sopsPath); trimmed != "" {
		store.SopsPath = trimmed
	}
	bundleName := strings.TrimSpace(opts.bundle)
	if bundleName == "" {
		bundleName = strings.TrimSpace(cfg.SecretsBundle)
	}
	if bundleName == "" {
		bundleName = "default"
	}
	return store, bundleName, cfg, nil
}

func loadSecretsConfig(path string) (agentconfig.Config, error) {
	defaults := agentconfig.DefaultConfig()
	path = strings.TrimSpace(path)
	if path == "" {
		return defaults, nil
	}
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) && path == defaults.ConfigPath {
			return defaults, nil
		}
		return defaults, fmt.Errorf("read config %s: %w", path, err)
	}
	if info.IsDir() {
		return defaults, fmt.Errorf("config path %s is a directory", path)
	}
	return agentconfig.Load(path)
}

func resolvePublicKeyInput(key, keyFile string) (string, error) {
	key = strings.TrimSpace(key)
	keyFile = strings.TrimSpace(keyFile)
	if key != "" && keyFile != "" {
		return "", fmt.Errorf("--key and --key-file are mutually exclusive")
	}
	if key == "" && keyFile == "" {
		return "", fmt.Errorf("one of --key or --key-file is required")
	}
	if key != "" {
		return key, nil
	}
	data, err := os.ReadFile(keyFile)
	if err != nil {
		return "", fmt.Errorf("read key file %s: %w", keyFile, err)
	}
	return strings.TrimSpace(string(data)), nil
}

func parseSSHPublicKey(value string) (bundles.SSHPublicKey, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return bundles.SSHPublicKey{}, fmt.Errorf("ssh public key is required")
	}
	if strings.Contains(value, "\n") {
		return bundles.SSHPublicKey{}, fmt.Errorf("ssh public key must be a single line")
	}
	fields := strings.Fields(value)
	if len(fields) < 2 {
		return bundles.SSHPublicKey{}, fmt.Errorf("ssh public key must include key type and data")
	}
	record := bundles.SSHPublicKey{
		Key:  value,
		Type: fields[0],
	}
	if len(fields) > 2 {
		record.Comment = strings.Join(fields[2:], " ")
	}
	return record, nil
}

func mutateSecretBundle(store bundles.Store, bundleName string, mutate func(bundle *bundles.Bundle) error) (bundles.Bundle, string, error) {
	bundle, path, err := loadBundleForMutation(store, bundleName)
	if err != nil {
		return bundles.Bundle{}, "", err
	}
	if err := mutate(&bundle); err != nil {
		return bundles.Bundle{}, "", err
	}
	bundle = bundle.Normalized()
	if err := writeSecretBundle(path, bundle, store); err != nil {
		return bundles.Bundle{}, "", err
	}
	return bundle, path, nil
}

func loadBundleForMutation(store bundles.Store, bundleName string) (bundles.Bundle, string, error) {
	path, err := store.ResolvePath(bundleName)
	if err != nil {
		if !strings.Contains(err.Error(), "not found") {
			return bundles.Bundle{}, "", err
		}
		path, err = defaultBundleWritePath(store, bundleName)
		if err != nil {
			return bundles.Bundle{}, "", err
		}
		return bundles.Bundle{Version: bundles.BundleVersion}, path, nil
	}
	bundle, err := store.Load(context.Background(), bundleName)
	if err != nil {
		return bundles.Bundle{}, "", err
	}
	return bundle, path, nil
}

func defaultBundleWritePath(store bundles.Store, bundleName string) (string, error) {
	bundleName = strings.TrimSpace(bundleName)
	if bundleName == "" {
		return "", fmt.Errorf("bundle name is required")
	}
	base := bundleName
	if !filepath.IsAbs(base) && strings.TrimSpace(store.Dir) != "" {
		base = filepath.Join(store.Dir, base)
	}
	if ext := filepath.Ext(base); ext != "" {
		return base, nil
	}
	if strings.TrimSpace(store.AgeKeyPath) != "" && !store.AllowPlaintext {
		return base + ".age", nil
	}
	return base + ".yaml", nil
}

func writeSecretBundle(path string, bundle bundles.Bundle, store bundles.Store) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("bundle path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	normalized := bundle.Normalized()
	lower := strings.ToLower(filepath.Base(path))
	switch {
	case strings.HasSuffix(lower, ".age"):
		plaintext, err := bundles.MarshalYAML(normalized)
		if err != nil {
			return err
		}
		encrypted, err := bundles.EncryptAge(plaintext, store.AgeKeyPath)
		if err != nil {
			return err
		}
		return os.WriteFile(path, encrypted, 0o600)
	case strings.Contains(lower, ".sops."):
		return fmt.Errorf("writing sops bundles is not supported yet; re-save as .age or plaintext")
	case strings.HasSuffix(lower, ".yaml"), strings.HasSuffix(lower, ".yml"):
		if !store.AllowPlaintext {
			return fmt.Errorf("refusing to write plaintext bundle %s without --allow-plaintext", path)
		}
		data, err := bundles.MarshalYAML(normalized)
		if err != nil {
			return err
		}
		return os.WriteFile(path, data, 0o600)
	case strings.HasSuffix(lower, ".json"):
		if !store.AllowPlaintext {
			return fmt.Errorf("refusing to write plaintext bundle %s without --allow-plaintext", path)
		}
		data, err := json.MarshalIndent(normalized, "", "  ")
		if err != nil {
			return err
		}
		data = append(data, '\n')
		return os.WriteFile(path, data, 0o600)
	default:
		return fmt.Errorf("unsupported bundle format for %s", path)
	}
}

func dedupeNonEmpty(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func sshKeyCount(bundle bundles.Bundle) int {
	if bundle.SSH == nil {
		return 0
	}
	return len(bundle.SSH.Keys)
}

func printSecretsMutationResult(jsonOutput bool, payload map[string]any, text string) error {
	if jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetEscapeHTML(false)
		return enc.Encode(payload)
	}
	_, err := fmt.Fprintln(os.Stdout, text)
	return err
}

func printSecretsUsage() {
	fmt.Fprintln(os.Stdout, "Usage: agentlab secrets <show|validate|add-ssh-key|remove-ssh-key|set-tailscale|clear-tailscale>")
}

func printSecretsShowUsage() {
	fmt.Fprintln(os.Stdout, "Usage: agentlab secrets show [--config <path>] [--bundle <name|path>] [--dir <path>] [--age-key <path>] [--sops-path <path>] [--allow-plaintext] [--format yaml|json] [--reveal] [bundle]")
}

func printSecretsValidateUsage() {
	fmt.Fprintln(os.Stdout, "Usage: agentlab secrets validate [--config <path>] [--bundle <name|path>] [--dir <path>] [--age-key <path>] [--sops-path <path>] [--allow-plaintext] [bundle]")
}

func printSecretsAddSSHKeyUsage() {
	fmt.Fprintln(os.Stdout, "Usage: agentlab secrets add-ssh-key [--config <path>] [--bundle <name|path>] [--dir <path>] [--age-key <path>] [--allow-plaintext] --name <name> (--key <public-key> | --key-file <path>)")
}

func printSecretsRemoveSSHKeyUsage() {
	fmt.Fprintln(os.Stdout, "Usage: agentlab secrets remove-ssh-key [--config <path>] [--bundle <name|path>] [--dir <path>] [--age-key <path>] [--allow-plaintext] --name <name>")
}

func printSecretsSetTailscaleUsage() {
	fmt.Fprintln(os.Stdout, "Usage: agentlab secrets set-tailscale [--config <path>] [--bundle <name|path>] [--dir <path>] [--age-key <path>] [--allow-plaintext] [--authkey <key>] [--hostname-template <template>] [--tag <tag>]... [--extra-arg <arg>]...")
}

func printSecretsClearTailscaleUsage() {
	fmt.Fprintln(os.Stdout, "Usage: agentlab secrets clear-tailscale [--config <path>] [--bundle <name|path>] [--dir <path>] [--age-key <path>] [--allow-plaintext]")
}
