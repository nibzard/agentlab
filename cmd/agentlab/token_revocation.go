package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"

	"github.com/agentlab/agentlab/internal/auth"
	"github.com/agentlab/agentlab/internal/protocol"
)

func runTokenRevocation(ctx context.Context, command string, args []string, base commonFlags) error {
	fs := newFlagSet("token " + command)
	base.bind(fs)
	help := bindHelpFlag(fs)
	issuer := fs.String("issuer", "", "SSH issuer fingerprint from secure token inspection")
	usage := func() {
		fmt.Fprintf(os.Stdout, "Usage: agentlab token %s --issuer FINGERPRINT REVOCATION_ID [--json]\n", command)
	}
	if err := parseFlags(fs, args, usage, help, base.jsonOutput); err != nil {
		return err
	}
	if fs.NArg() != 1 || !auth.ValidRevocationTarget(*issuer, fs.Arg(0)) {
		return newUsageError(fmt.Errorf("provide the issuer and revocation ID from secure token inspection"), false)
	}
	client, err := apiClientFromFlags(base)
	if err != nil {
		return err
	}
	caps, err := fetchCapabilities(ctx, client)
	if err != nil {
		return err
	}
	if caps.ServerIdentity == "" {
		return &blockedRequirement{Code: "server_identity_unavailable", Requirement: "verified_server_identity", Action: "restore server identity discovery before managing credential status"}
	}
	if !caps.Features["token_revocation"] {
		return &blockedRequirement{Code: "unsupported_feature", Requirement: "token_revocation", Action: "use a daemon with durable SSH token revocation support"}
	}

	if base.contextName != "" {
		store, err := contextStore()
		if err != nil {
			return err
		}
		settings, _, found, err := store.LookupContext(ctx, base.contextName, false)
		if err != nil {
			return err
		}
		if found && settings.ServerIdentity != "" {
			endpoint, err := normalizeEndpoint(settings.Endpoint)
			if err != nil {
				return err
			}
			if endpoint == client.endpoint && settings.ServerIdentity != caps.ServerIdentity {
				return &blockedRequirement{Code: "server_identity_mismatch", Requirement: "verified_server_identity", Action: "preserve the saved context and verify the intended server before revoking a credential"}
			}
		}
	}
	if !base.jsonOutput {
		fmt.Fprintf(os.Stderr, "Selected server: %s (%s)\n", client.target(), caps.ServerIdentity)
	}
	method := "GET"
	path := "/v1/token-revocations/" + fs.Arg(0) + "?issuer=" + url.QueryEscape(*issuer)
	var body any
	if command == "revoke" {
		method = "POST"
		path = "/v1/token-revocations"
		body = protocol.TokenRevocationRequest{Issuer: *issuer, RevocationID: fs.Arg(0), ServerIdentity: caps.ServerIdentity}
	}
	payload, err := client.doJSON(ctx, method, path, body)
	if err != nil {
		return err
	}
	var record protocol.TokenRevocation
	if err := json.Unmarshal(payload, &record); err != nil {
		return err
	}
	if record.Issuer != *issuer || record.RevocationID != fs.Arg(0) || record.ServerIdentity != caps.ServerIdentity || (command == "revoke" && !record.Revoked) {
		return fmt.Errorf("token revocation response does not match the selected server and target")
	}
	if base.jsonOutput {
		return json.NewEncoder(os.Stdout).Encode(record)
	}
	fmt.Fprintf(os.Stdout, "Revocation ID: %s\nRevoked: %t\n", record.RevocationID, record.Revoked)
	return nil
}
