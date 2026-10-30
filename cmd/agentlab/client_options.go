// ABOUTME: Helpers to resolve connection settings from common CLI flags.
// ABOUTME: Selects remote endpoint vs local unix socket and normalizes inputs.

package main

import (
	"github.com/agentlab/agentlab/internal/connection"
	"strings"
)

type clientOptions struct {
	CredentialBinding  *connection.CredentialBinding
	ServerIdentity     string
	JumpHost, JumpUser string
	SocketPath         string
	Endpoint           string
	Token              string
	AllowInsecureHTTP  bool
}

func (c commonFlags) clientOptions() (clientOptions, error) {
	resolved, err := c.resolvedConnection()
	if err != nil {
		return clientOptions{}, err
	}
	return clientOptions{CredentialBinding: resolved.CredentialBinding, ServerIdentity: resolved.ServerIdentity, JumpHost: resolved.JumpHost, JumpUser: resolved.JumpUser, SocketPath: strings.TrimSpace(resolved.SocketPath), Endpoint: resolved.Endpoint, Token: strings.TrimSpace(resolved.Token), AllowInsecureHTTP: resolved.AllowInsecureHTTP}, nil
}

func apiClientFromFlags(c commonFlags) (*apiClient, error) {
	opts, err := c.clientOptions()
	if err != nil {
		return nil, err
	}
	return newAPIClient(opts, c.timeout), nil
}
