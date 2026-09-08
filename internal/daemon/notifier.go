package daemon

// ABOUTME: Webhook notifier for operational notifications (lease pre-expiry
// warnings). Delivers a plain-text body with structured X-* headers so any
// HTTP receiver can consume it: ntfy renders the body as the message (and
// understands the Title header), healthchecks.io-style dead-man switches
// accept any POST, and custom receivers can read the headers.

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/agentlab/agentlab/internal/models"
)

const notifierHTTPTimeout = 10 * time.Second

// Notifier posts operational notifications to a configured webhook URL.
//
// Delivery is a plain-text POST with structured headers:
//   - X-Agentlab-Event: event kind (e.g. "sandbox.lease.expiring")
//   - X-Agentlab-VMID: sandbox VMID
//   - X-Agentlab-Expires-At: RFC3339 lease expiration
//   - Title: human-friendly title (ntfy renders it as the notification title)
//
// A zero-value or URL-less Notifier is a safe no-op.
type Notifier struct {
	url    string
	client *http.Client
	logger *log.Logger
}

// NewNotifier builds a notifier for url. An empty URL disables delivery.
func NewNotifier(url string, logger *log.Logger) *Notifier {
	if logger == nil {
		logger = log.Default()
	}
	return &Notifier{
		url:    url,
		client: &http.Client{Timeout: notifierHTTPTimeout},
		logger: logger,
	}
}

// Enabled reports whether a webhook URL is configured.
func (n *Notifier) Enabled() bool {
	return n != nil && n.url != ""
}

// NotifyLeaseExpiring delivers a pre-expiry lease warning for sb.
//
// The body is a human-readable one-liner suitable for direct display (e.g.
// as an ntfy push message). The error is non-nil only when delivery was
// attempted and failed, so callers can retry on the next cycle.
func (n *Notifier) NotifyLeaseExpiring(ctx context.Context, sb models.Sandbox, remaining time.Duration) error {
	if !n.Enabled() {
		return nil
	}
	body := fmt.Sprintf("agentlab: sandbox %d (%s) lease expires in %s at %s — renew with: agentlab sandbox lease renew --ttl <minutes> %d",
		sb.VMID, sb.Name, remaining, sb.LeaseExpires.UTC().Format(time.RFC3339), sb.VMID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.url, bytes.NewReader([]byte(body)))
	if err != nil {
		return fmt.Errorf("build lease notify request: %w", err)
	}
	req.Header.Set("Content-Type", "text/plain; charset=utf-8")
	req.Header.Set("User-Agent", "agentlabd-notifier")
	req.Header.Set("X-Agentlab-Event", string(EventKindSandboxLeaseExpiring))
	req.Header.Set("X-Agentlab-VMID", fmt.Sprintf("%d", sb.VMID))
	req.Header.Set("X-Agentlab-Expires-At", sb.LeaseExpires.UTC().Format(time.RFC3339))
	req.Header.Set("Title", fmt.Sprintf("Sandbox %d lease expiring", sb.VMID))
	resp, err := n.client.Do(req)
	if err != nil {
		return fmt.Errorf("post lease notify: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("lease notify webhook returned %s", resp.Status)
	}
	return nil
}
