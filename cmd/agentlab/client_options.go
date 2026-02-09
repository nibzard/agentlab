// ABOUTME: Helpers to resolve connection settings from common CLI flags.
// ABOUTME: Selects remote endpoint vs local unix socket and normalizes inputs.

package main

import "strings"

type clientOptions struct {
	SocketPath string
	Endpoint   string
	Token      string
}

func (c commonFlags) clientOptions() (clientOptions, error) {
	endpoint, err := normalizeEndpoint(c.endpoint)
	if err != nil {
		return clientOptions{}, err
	}
	socketPath := strings.TrimSpace(c.socketPath)
	if socketPath == "" {
		socketPath = defaultSocketPath
	}
	return clientOptions{
		SocketPath: socketPath,
		Endpoint:   endpoint,
		Token:      strings.TrimSpace(c.token),
	}, nil
}

func apiClientFromFlags(c commonFlags) (*apiClient, error) {
	opts, err := c.clientOptions()
	if err != nil {
		return nil, err
	}
	return newAPIClient(opts, c.timeout), nil
}
