package daemon

import (
	"context"
	"errors"
	"log"

	"github.com/agentlab/agentlab/internal/proxy"
)

// CaddyProxyPublisher wraps proxy.CaddyPublisher to implement ExposurePublisher.
type CaddyProxyPublisher struct {
	inner *proxy.CaddyPublisher
}

// NewCaddyProxyPublisher creates a daemon-compatible publisher backed by Caddy.
func NewCaddyProxyPublisher(p *proxy.CaddyPublisher) *CaddyProxyPublisher {
	return &CaddyProxyPublisher{inner: p}
}

func (p *CaddyProxyPublisher) Publish(ctx context.Context, name string, targetIP string, port int) (ExposurePublishResult, error) {
	result, err := p.inner.Publish(ctx, name, targetIP, port)
	if err != nil {
		return ExposurePublishResult{}, err
	}
	return ExposurePublishResult{
		URL:   result.URL,
		State: result.State,
	}, nil
}

func (p *CaddyProxyPublisher) Unpublish(ctx context.Context, name string, port int) error {
	return p.inner.Unpublish(ctx, name, port)
}

// MultiPublisher delegates to multiple ExposurePublisher implementations.
//
// Publish tries all publishers and returns the first successful result.
// Unpublish removes from all publishers.
// This allows both Tailscale and Caddy publishers to coexist.
type MultiPublisher struct {
	publishers []ExposurePublisher
	logger     *log.Logger
}

// NewMultiPublisher creates a publisher that fans out to multiple backends.
func NewMultiPublisher(logger *log.Logger, publishers ...ExposurePublisher) *MultiPublisher {
	if logger == nil {
		logger = log.Default()
	}
	return &MultiPublisher{
		publishers: publishers,
		logger:     logger,
	}
}

func (m *MultiPublisher) Publish(ctx context.Context, name string, targetIP string, port int) (ExposurePublishResult, error) {
	var lastResult ExposurePublishResult
	var lastErr error
	successCount := 0

	for _, p := range m.publishers {
		result, err := p.Publish(ctx, name, targetIP, port)
		if err != nil {
			m.logger.Printf("multi-publisher: publish failed for %s: %v", name, err)
			lastErr = err
			continue
		}
		lastResult = result
		successCount++
	}

	if successCount == 0 {
		return ExposurePublishResult{}, lastErr
	}

	// If any publisher succeeded, report success with the last successful result
	return lastResult, nil
}

func (m *MultiPublisher) Unpublish(ctx context.Context, name string, port int) error {
	var lastErr error
	for _, p := range m.publishers {
		if err := p.Unpublish(ctx, name, port); err != nil && !errors.Is(err, ErrServeRuleNotFound) {
			m.logger.Printf("multi-publisher: unpublish failed for %s: %v", name, err)
			lastErr = err
		}
	}
	return lastErr
}
