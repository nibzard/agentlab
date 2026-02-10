// ABOUTME: Helpers to retrieve host metadata and derive defaults for client-side checks.

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

func fetchHostInfo(ctx context.Context, client *apiClient) (*hostResponse, error) {
	if client == nil {
		return nil, nil
	}
	payload, err := client.doJSON(ctx, http.MethodGet, "/v1/host", nil)
	if err != nil {
		return nil, err
	}
	var resp hostResponse
	if err := json.Unmarshal(payload, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func agentSubnetFromHost(host *hostResponse) string {
	if host == nil {
		return defaultAgentSubnet
	}
	if subnet := strings.TrimSpace(host.AgentSubnet); subnet != "" {
		return subnet
	}
	return defaultAgentSubnet
}
