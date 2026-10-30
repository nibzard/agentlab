package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/agentlab/agentlab/internal/auth"
	"github.com/agentlab/agentlab/internal/connection"
)

func enrollmentTTL(value string) (time.Duration, error) {
	ttl, err := time.ParseDuration(value)
	if err != nil || ttl < time.Minute || ttl > 24*time.Hour {
		return 0, fmt.Errorf("credential lifetime must be from 1m to 24h")
	}
	return ttl, nil
}
func runTokenEnroll(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("token enroll")
	base.bind(fs)
	help := bindHelpFlag(fs)
	key := fs.String("key", "", "operator SSH signing key")
	commands := fs.String("cmds", "", "exact command permissions")
	scope := fs.String("scope", "", "sandbox scopes")
	subject := fs.String("subject", "", "task identity label")
	ttlText := fs.String("ttl", "1h", "credential lifetime, at most 24h")
	destination := fs.String("save-context", "", "destination for the verified task credential")
	source := fs.String("context", base.contextName, "source server context")
	mode := fs.String("credential-store", "", "credential storage: auto, os, file")
	usage := func() {
		fmt.Fprintln(os.Stdout, "Usage: agentlab token enroll --save-context NAME --key PATH --cmds PERMISSIONS [--scope SCOPES] [--ttl 1h]")
	}
	if err := parseFlags(fs, args, usage, help, base.jsonOutput); err != nil {
		return err
	}
	if fs.NArg() != 0 || *commands == "" || *destination == "" {
		return fmt.Errorf("provide --save-context and --cmds")
	}
	if err := connection.ValidateContextName(*destination); err != nil {
		return err
	}
	ttl, err := enrollmentTTL(*ttlText)
	if err != nil {
		return err
	}
	if _, err := connection.CredentialStorageMode(*mode); err != nil {
		return err
	}
	base.contextName = *source
	resolved, err := base.resolvedConnection()
	if err != nil {
		return err
	}
	if resolved.Endpoint == "" {
		return &blockedRequirement{Code: "remote_endpoint_required", Requirement: "authenticated_remote_endpoint", Action: "select a remote server context before enrolling a task credential"}
	}
	store, err := contextStore()
	if err != nil {
		return err
	}
	store.CredentialMode = *mode
	expected, _, exists, err := store.LookupContext(ctx, *destination, true)
	if err != nil {
		return err
	}
	var guard *connection.Context
	if exists {
		guard = &expected
	}
	client := newAPIClient(clientOptions{ServerIdentity: resolved.ServerIdentity, Endpoint: resolved.Endpoint, Token: resolved.Token, AllowInsecureHTTP: resolved.AllowInsecureHTTP}, base.timeout)
	caps, err := fetchCapabilities(ctx, client)
	if err != nil {
		return err
	}
	if caps.ServerIdentity == "" || (resolved.ServerIdentity != "" && resolved.ServerIdentity != caps.ServerIdentity) {
		return &blockedRequirement{Code: "server_identity_mismatch", Requirement: "verified_server_identity", Action: "verify the selected server identity before credential enrollment"}
	}
	path, err := resolveKeyPath(*key)
	if err != nil {
		return &blockedRequirement{Code: "signing_authority_required", Requirement: "operator_signing_key", Action: "provide an authorized operator SSH key; do not give its private key to a delegated agent"}
	}
	signer, err := loadTokenSigner(path)
	if err != nil {
		return &blockedRequirement{Code: "signing_authority_required", Requirement: "operator_signing_key", Action: "restore non-interactive access to the operator signing key before enrollment"}
	}
	raw, err := auth.CreateToken(signer, auth.TokenCreateRequest{Commands: splitAndTrim(*commands), Scope: splitAndTrim(*scope), Subject: *subject, TTL: ttl})
	if err != nil {
		return err
	}
	candidate := newAPIClient(clientOptions{ServerIdentity: caps.ServerIdentity, Endpoint: resolved.Endpoint, Token: raw, AllowInsecureHTTP: resolved.AllowInsecureHTTP}, base.timeout)
	verified, err := fetchCapabilities(ctx, candidate)
	if err != nil {
		return err
	}
	if verified.ServerIdentity != caps.ServerIdentity {
		return &blockedRequirement{Code: "server_identity_mismatch", Requirement: "verified_server_identity", Action: "preserve the working context and inspect the server endpoint"}
	}
	cfg := resolved.Settings
	cfg.Token = raw
	cfg.ServerIdentity = caps.ServerIdentity
	if err := store.PutContextIfMatch(ctx, *destination, guard, cfg); err != nil {
		return err
	}
	token, err := auth.VerifyTokenSignature(raw, signer.PublicKey())
	if err != nil {
		return err
	}
	output := map[string]any{"context": *destination, "server_identity": caps.ServerIdentity, "issuer": token.Claims.Issuer, "revocation_id": auth.RevocationID(token.Claims.Issuer, token.Claims.TokenID), "commands": token.Claims.Commands, "scope": token.Claims.Scope, "expires_at": token.Claims.ExpiresAt, "verified": true}
	if base.jsonOutput {
		return json.NewEncoder(os.Stdout).Encode(output)
	}
	fmt.Fprintf(os.Stdout, "Enrolled context: %s\nExpires: %s\n", *destination, time.Unix(token.Claims.ExpiresAt, 0).UTC().Format(time.RFC3339))
	return nil
}

func renewalContextOverrides(base commonFlags) bool {
	return base.connectionFields&(endpointField|tokenField|socketField) != 0 || base.endpoint != "" || base.token != "" || strings.TrimSpace(os.Getenv("AGENTLAB_ENDPOINT")) != "" || strings.TrimSpace(os.Getenv("AGENTLAB_TOKEN")) != "" || strings.TrimSpace(os.Getenv("AGENTLAB_SOCKET")) != ""
}
