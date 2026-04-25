package backend

import (
	"context"
	"fmt"
	"log"

	"github.com/agentlab/agentlab/internal/sandbox"
)

// CheckHealth attempts a health check on the given backend.
// If the backend does not implement sandbox.HealthChecker, it is assumed healthy.
func CheckHealth(ctx context.Context, b sandbox.Backend) error {
	if hc, ok := b.(sandbox.HealthChecker); ok {
		return hc.HealthCheck(ctx)
	}
	return nil
}

// HealthCheckAll checks all backends and reports results.
// Returns nil if all healthy, or an aggregate error.
func HealthCheckAll(ctx context.Context, backends map[sandbox.Type]sandbox.Backend) error {
	var errs []string
	for typ, b := range backends {
		if err := CheckHealth(ctx, b); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", typ, err))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("backend health checks failed: %v", errs)
	}
	return nil
}

// LogHealthCheck performs a health check and logs the result.
// Returns true if the backend is healthy.
func LogHealthCheck(ctx context.Context, b sandbox.Backend, logger *log.Logger) bool {
	if logger == nil {
		logger = log.Default()
	}
	typ := b.SandboxType()
	if err := CheckHealth(ctx, b); err != nil {
		logger.Printf("backend %s health check failed: %v", typ, err)
		return false
	}
	logger.Printf("backend %s health check passed", typ)
	return true
}
