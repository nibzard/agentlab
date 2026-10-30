package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/agentlab/agentlab/internal/auth"
)

// runTokenCommand dispatches token subcommands: create, list, inspect.
func runTokenCommand(ctx context.Context, args []string, base commonFlags) error {
	if len(args) == 0 || isHelpToken(args[0]) {
		fmt.Fprint(os.Stdout, tokenUsage)
		return errHelp
	}
	switch args[0] {
	case "enroll":
		return runTokenEnroll(ctx, args[1:], base)
	case "renew":
		return runTokenRenew(ctx, args[1:], base)
	case "create":
		return runTokenCreate(ctx, args[1:], base)
	case "list":
		return runTokenList(ctx, args[1:], base)
	case "inspect":
		return runTokenInspect(ctx, args[1:], base)
	case "revoke", "status":
		return runTokenRevocation(ctx, args[0], args[1:], base)
	default:
		return newUsageError(fmt.Errorf("unknown token subcommand %q", args[0]), true)
	}
}

const tokenUsage = `Usage:
  agentlab token enroll --save-context NAME --key PATH --cmds PERMISSIONS [--ttl 1h]
  agentlab token renew --context NAME --request-id KEY [--key PATH] [--ttl 1h]
  agentlab token create --key <path> --cmds <commands> [--scope <scope>] [--ttl <duration>] [--subject <label>]
  agentlab token list --key <path>
  agentlab token inspect <token-string>
  agentlab token inspect --token-stdin [--json]
  agentlab token revoke --issuer FINGERPRINT REVOCATION_ID [--json]
  agentlab token status --issuer FINGERPRINT REVOCATION_ID [--json]

Token subcommands:
  enroll   Verify and save an operator-signed task credential
  renew    Resume an exact-scope credential replacement and revocation
  create   Create a new scoped API token signed with an SSH key
  list     Show SSH key fingerprint for token signing
  inspect  Decode and display token claims without verifying signature
  revoke   Revoke one SSH-signed token on the selected server
  status   Read a token revocation record on the selected server

Examples:
  # Create a token for all commands, valid for 24 hours:
  agentlab token create --key ~/.ssh/id_ed25519 --cmds "*" --ttl 24h

  # Create a scoped token for specific sandbox operations:
  agentlab token create --key ~/.ssh/id_ed25519 --cmds "sandbox" --scope "sandbox:1001" --ttl 8h

  # Inspect a token:
  agentlab token inspect --token-stdin --json < /protected/task-token

Flags:
  --key     Path to SSH private key for signing (auto-detects ~/.ssh/id_ed25519 or id_rsa)
  --cmds    Comma-separated list of allowed command prefixes (use "*" for all)
  --scope   Comma-separated list of sandbox scopes (e.g., "sandbox:1001,sandbox:1002")
  --ttl     Token lifetime (e.g., "30m", "24h", "72h"). Default: 1h
  --subject Optional human-readable label for the token
`

func runTokenCreate(_ context.Context, args []string, base commonFlags) error {
	fs := flag.NewFlagSet("token create", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	var (
		keyPath string
		cmds    string
		scope   string
		ttl     string
		subject string
		help    bool
	)
	fs.StringVar(&keyPath, "key", "", "path to SSH private key")
	fs.StringVar(&cmds, "cmds", "", "comma-separated list of allowed command prefixes")
	fs.StringVar(&scope, "scope", "", "comma-separated list of sandbox scopes (e.g., sandbox:1001)")
	fs.StringVar(&ttl, "ttl", "1h", "token lifetime (e.g., 30m, 24h)")
	fs.StringVar(&subject, "subject", "", "optional human-readable label")
	fs.BoolVar(&help, "help", false, "show help")
	fs.BoolVar(&help, "h", false, "show help")
	fs.BoolVar(&base.jsonOutput, "json", base.jsonOutput, jsonFlagDescription)

	if err := fs.Parse(interspersedArgs(fs, args)); err != nil {
		return newUsageError(err, true)
	}
	if validateCommandOnly {
		return errCommandValidated
	}
	if help {
		fmt.Fprint(os.Stdout, tokenUsage)
		return errHelp
	}

	keyPath, err := resolveKeyPath(keyPath)
	if err != nil {
		return err
	}
	if cmds == "" {
		return newUsageError(errors.New("--cmds is required"), true)
	}

	signer, err := loadTokenSigner(keyPath)
	if err != nil {
		return fmt.Errorf("load SSH key: %w", err)
	}

	duration, err := time.ParseDuration(ttl)
	if err != nil {
		return fmt.Errorf("invalid --ttl %q: %w", ttl, err)
	}

	commandList := splitAndTrim(cmds)
	if len(commandList) == 0 {
		return errors.New("--cmds must contain at least one command")
	}

	var scopeList []string
	if scope != "" {
		scopeList = splitAndTrim(scope)
	}

	tokenStr, err := auth.CreateToken(signer, auth.TokenCreateRequest{
		Commands: commandList,
		Scope:    scopeList,
		TTL:      duration,
		Subject:  subject,
	})
	if err != nil {
		return fmt.Errorf("create token: %w", err)
	}

	if base.jsonOutput {
		pubKey := signer.PublicKey()
		fp := auth.FingerprintForPublicKey(pubKey)
		parsed, err := auth.ParseTokenUnverified(tokenStr)
		if err != nil {
			return err
		}
		out := map[string]any{
			"revocation_id": auth.RevocationID(parsed.Claims.Issuer, parsed.Claims.TokenID),
			"token":         tokenStr,
			"fingerprint":   fp,
			"commands":      commandList,
			"scope":         scopeList,
			"ttl":           ttl,
			"subject":       subject,
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}

	fmt.Println(tokenStr)
	return nil
}

func runTokenList(_ context.Context, args []string, base commonFlags) error {
	fs := flag.NewFlagSet("token list", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	var (
		keyPath string
		help    bool
	)
	fs.StringVar(&keyPath, "key", "", "path to SSH private key to show fingerprint for")
	fs.BoolVar(&help, "help", false, "show help")
	fs.BoolVar(&help, "h", false, "show help")
	fs.BoolVar(&base.jsonOutput, "json", base.jsonOutput, jsonFlagDescription)

	if err := fs.Parse(interspersedArgs(fs, args)); err != nil {
		return newUsageError(err, true)
	}
	if validateCommandOnly {
		return errCommandValidated
	}
	if help {
		fmt.Fprint(os.Stdout, tokenUsage)
		return errHelp
	}

	keyPath, err := resolveKeyPath(keyPath)
	if err != nil {
		return err
	}

	signer, err := loadTokenSigner(keyPath)
	if err != nil {
		return fmt.Errorf("load SSH key: %w", err)
	}

	pubKey := signer.PublicKey()
	fp := auth.FingerprintForPublicKey(pubKey)

	if base.jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]string{
			"key_path":    keyPath,
			"fingerprint": fp,
			"key_type":    pubKey.Type(),
		})
	}

	fmt.Printf("Key:         %s\n", keyPath)
	fmt.Printf("Type:        %s\n", pubKey.Type())
	fmt.Printf("Fingerprint: %s\n", fp)
	return nil
}

func runTokenInspect(_ context.Context, args []string, base commonFlags) error {
	fs := flag.NewFlagSet("token inspect", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	var help bool
	fs.BoolVar(&help, "help", false, "show help")
	fs.BoolVar(&help, "h", false, "show help")
	fs.BoolVar(&base.jsonOutput, "json", base.jsonOutput, jsonFlagDescription)

	var tokenStdin bool
	fs.BoolVar(&tokenStdin, "token-stdin", false, "read the target token from protected standard input")

	if err := fs.Parse(interspersedArgs(fs, args)); err != nil {
		return newUsageError(err, true)
	}
	if validateCommandOnly {
		return errCommandValidated
	}
	if help {
		fmt.Fprint(os.Stdout, tokenUsage)
		return errHelp
	}

	if (tokenStdin && fs.NArg() != 0) || (!tokenStdin && fs.NArg() != 1) {
		return newUsageError(errors.New("provide a target token through --token-stdin or one legacy positional argument"), false)
	}
	tokenStr := ""
	if tokenStdin {
		data, err := io.ReadAll(io.LimitReader(os.Stdin, 16*1024+1))
		if err != nil {
			return fmt.Errorf("read token input: %w", err)
		}
		if len(data) > 16*1024 {
			return newUsageError(errors.New("target token exceeds 16 KiB"), false)
		}
		tokenStr = strings.TrimSpace(string(data))
		if tokenStr == "" {
			return newUsageError(errors.New("target token input is empty"), false)
		}
	} else {
		tokenStr = fs.Arg(0)
	}

	tok, err := auth.ParseTokenUnverified(tokenStr)
	if err != nil {
		return fmt.Errorf("parse token: %w", err)
	}

	expiry := "never"
	if tok.Claims.ExpiresAt > 0 {
		expiry = time.Unix(tok.Claims.ExpiresAt, 0).Format(time.RFC3339)
	}
	nbf := "immediately"
	if tok.Claims.NotBefore > 0 {
		nbf = time.Unix(tok.Claims.NotBefore, 0).Format(time.RFC3339)
	}
	scope := "all sandboxes"
	if len(tok.Claims.Scope) > 0 {
		scope = strings.Join(tok.Claims.Scope, ", ")
	}

	if base.jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]any{
			"issuer":        tok.Claims.Issuer,
			"subject":       tok.Claims.Subject,
			"commands":      tok.Claims.Commands,
			"scope":         tok.Claims.Scope,
			"expires":       expiry,
			"not_before":    nbf,
			"token_id":      tok.Claims.TokenID,
			"revocation_id": auth.RevocationID(tok.Claims.Issuer, tok.Claims.TokenID),
			"verified":      false,
		})
	}

	fmt.Printf("Issuer:    %s\n", tok.Claims.Issuer)
	fmt.Printf("Subject:   %s\n", tok.Claims.Subject)
	fmt.Printf("Commands:  %s\n", strings.Join(tok.Claims.Commands, ", "))
	fmt.Printf("Scope:     %s\n", scope)
	fmt.Printf("Expires:   %s\n", expiry)
	fmt.Printf("NotBefore: %s\n", nbf)
	fmt.Printf("TokenID:   %s\n", tok.Claims.TokenID)
	fmt.Printf("RevocationID: %s\n", auth.RevocationID(tok.Claims.Issuer, tok.Claims.TokenID))
	fmt.Println("Signature: not verified")
	return nil
}

// --- helpers ---

func loadTokenSigner(path string) (ssh.Signer, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read key %s: %w", path, err)
	}
	return ssh.ParsePrivateKey(data)
}

func resolveKeyPath(keyPath string) (string, error) {
	if keyPath != "" {
		return keyPath, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", errors.New("--key is required (cannot determine home directory)")
	}
	for _, candidate := range []string{
		home + "/.ssh/id_ed25519",
		home + "/.ssh/id_rsa",
	} {
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}
	return "", errors.New("--key is required (no default SSH key found in ~/.ssh/)")
}

func splitAndTrim(s string) []string {
	parts := strings.Split(s, ",")
	var result []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}
