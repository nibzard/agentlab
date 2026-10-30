package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/agentlab/agentlab/internal/protocol"
)

func runCapabilitiesCommand(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("capabilities")
	base.bind(fs)
	help := bindHelpFlag(fs)
	usage := func() { fmt.Fprintln(os.Stdout, "Usage: agentlab capabilities [--json]") }
	if err := parseFlags(fs, args, usage, help, base.jsonOutput); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("capabilities accepts no positional arguments")
	}
	client, err := apiClientFromFlags(base)
	if err != nil {
		return err
	}
	capabilities, err := fetchCapabilities(ctx, client)
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(capabilities)
}

func fetchCapabilities(ctx context.Context, client *apiClient) (protocol.Capabilities, error) {
	var capabilities protocol.Capabilities
	payload, err := client.doJSON(ctx, "GET", "/v1/capabilities", nil)
	if err != nil {
		var apiErr apiResponseError
		if errors.As(err, &apiErr) && apiErr.Status == 404 {
			return capabilities, &blockedRequirement{Code: "capabilities_unavailable", Requirement: "compatible_server", Action: "this server does not advertise capabilities; inspect agentlab schema or upgrade the daemon"}
		}
		return capabilities, err
	}
	if err := json.Unmarshal(payload, &capabilities); err != nil {
		return capabilities, fmt.Errorf("invalid server capabilities")
	}
	if !capabilities.Compatible() {
		return capabilities, &blockedRequirement{Code: "incompatible_server", Requirement: "compatible_server", Action: "use a client and daemon that support the same API schema version"}
	}
	if client.serverIdentity != "" {
		if capabilities.ServerIdentity != client.serverIdentity {
			return capabilities, &blockedRequirement{Code: "server_identity_mismatch", Requirement: "verified_server_identity", Action: "restore the original server or connect a different named context; the saved binding is not changed"}
		}
		if !capabilities.Features["request_server_identity"] {
			return capabilities, &blockedRequirement{Code: "unsupported_feature", Requirement: "request_server_identity", Action: "upgrade the daemon to enforce the saved server identity before using this verified context"}
		}
	}
	return capabilities, nil
}
