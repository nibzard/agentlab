package main

import (
	"context"
	"fmt"
	"net/http"
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
	_, _ = client.doJSON(touchCtx, http.MethodPost, fmt.Sprintf("/v1/sandboxes/%d/touch", vmid), nil)
}
