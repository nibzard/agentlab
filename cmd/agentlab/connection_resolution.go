package main

import (
	"context"
	"flag"

	"github.com/agentlab/agentlab/internal/connection"
)

type connectionFields uint8

const (
	endpointField connectionFields = 1 << iota
	tokenField
	socketField
	httpField
)

func connectionField(name string) connectionFields {
	switch name {
	case "endpoint":
		return endpointField
	case "token":
		return tokenField
	case "socket":
		return socketField
	case "allow-insecure-http":
		return httpField
	}
	return 0
}

// trackedConnectionFlag preserves the standard parser while recording explicit
// values, including an empty token and an explicit false HTTP acknowledgement.
type trackedConnectionFlag struct {
	flag.Value
	fields *connectionFields
	bit    connectionFields
}

func (f trackedConnectionFlag) Set(value string) error {
	if err := f.Value.Set(value); err != nil {
		return err
	}
	*f.fields |= f.bit
	return nil
}
func (f trackedConnectionFlag) IsBoolFlag() bool { return f.bit == httpField }
func (f trackedConnectionFlag) Get() any         { return f.Value.(flag.Getter).Get() }

func (c commonFlags) connectionOverrides() connection.Overrides {
	result := connection.Overrides{}
	if c.connectionFields&endpointField != 0 {
		result.Endpoint = &c.endpoint
	}
	if c.connectionFields&tokenField != 0 {
		result.Token = &c.token
	}
	if c.connectionFields&socketField != 0 {
		result.Socket = &c.socketPath
	}
	if c.connectionFields&httpField != 0 {
		result.AllowInsecureHTTP = &c.allowInsecureHTTP
	}
	return result
}

func (o globalOptions) common() commonFlags {
	return commonFlags{contextName: o.contextName, socketPath: o.socketPath, endpoint: o.endpoint, token: o.token, jsonOutput: o.jsonOutput, timeout: o.timeout, tailscaleAdmin: o.tailscaleAdmin, allowInsecureHTTP: o.allowInsecureHTTP, selection: o.selection, connectionFields: o.connectionFields}
}

func (c commonFlags) resolvedConnection() (connection.Resolved, error) {
	if c.selection != nil {
		return c.selection.Resolve(context.Background(), c.connectionOverrides())
	}
	endpoint, err := normalizeEndpoint(c.endpoint)
	if err != nil {
		return connection.Resolved{}, err
	}
	if err := validateEndpointPolicy(endpoint, c.allowInsecureHTTP); err != nil {
		return connection.Resolved{}, err
	}
	socket := c.socketPath
	if socket == "" {
		socket = defaultSocketPath
	}
	return connection.Resolved{Settings: connection.Settings{Endpoint: endpoint, Token: c.token, AllowInsecureHTTP: c.allowInsecureHTTP}, SocketPath: socket, ContextName: c.contextName}, nil
}

func localConnectionCommand(args []string) bool {
	for _, arg := range args {
		if arg == "--" {
			break
		}
		if arg == "--help" || arg == "-h" || arg == "--version" {
			return true
		}
	}
	command := globalCommandArgs(args)
	if len(command) == 0 {
		return true
	}
	switch command[0] {
	case "skill", "context", "operation", "defaults", "completion", "help", "version":
		return true
	case "repo":
		return len(command) > 1 && (command[1] == "snapshot" || command[1] == "result")
	case "token":
		return len(command) < 2 || (command[1] != "revoke" && command[1] != "status" && command[1] != "enroll")
	}
	return false
}
