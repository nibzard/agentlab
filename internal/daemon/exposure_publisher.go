package daemon

import (
	"context"
	"errors"
)

const (
	exposureStateServing   = "serving"
	exposureStateHealthy   = "healthy"
	exposureStateUnhealthy = "unhealthy"
)

var ErrServeRuleNotFound = errors.New("serve rule not found")

// ExposurePublishResult captures the outcome of publishing a sandbox exposure.
type ExposurePublishResult struct {
	URL   string
	State string
}

// ExposurePublisher installs and removes host-level exposure routes.
type ExposurePublisher interface {
	Publish(ctx context.Context, name string, targetIP string, port int) (ExposurePublishResult, error)
	Unpublish(ctx context.Context, name string, port int) error
}

// noopPublisher is a no-op publisher used when no real publisher is available
// (e.g., offline mode without proxy_enabled).
type noopPublisher struct{}

func (n *noopPublisher) Publish(_ context.Context, name string, targetIP string, port int) (ExposurePublishResult, error) {
	return ExposurePublishResult{URL: "", State: exposureStateServing}, nil
}

func (n *noopPublisher) Unpublish(_ context.Context, _ string, _ int) error {
	return nil
}
