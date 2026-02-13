package main

import (
	"context"
	"net/http"
	"strconv"
	"time"
)

const touchRequestTimeout = 2 * time.Second

func touchSandboxBestEffort(ctx context.Context, client *apiClient, vmid int) {
	if client == nil || vmid <= 0 {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	touchCtx, cancel := context.WithTimeout(ctx, touchRequestTimeout)
	defer cancel()
	path, err := endpointPath("/v1/sandboxes", strconv.Itoa(vmid), "touch")
	if err != nil {
		return
	}
	_, _ = client.doJSON(touchCtx, http.MethodPost, path, nil)
}
