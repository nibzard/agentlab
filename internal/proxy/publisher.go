package proxy

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// TLSMode determines how TLS certificates are provisioned.
type TLSMode string

const (
	// TLSModeOff disables TLS termination.
	TLSModeOff TLSMode = "off"
	// TLSModeSelfSigned uses a built-in CA for self-signed certificates.
	TLSModeSelfSigned TLSMode = "self-signed"
	// TLSModeLetsEncrypt uses Let's Encrypt via Caddy's automatic HTTPS.
	TLSModeLetsEncrypt TLSMode = "letsencrypt"
)

// ProxyConfig holds configuration for the reverse proxy publisher.
type ProxyConfig struct {
	// Enabled controls whether the proxy publisher is active.
	Enabled bool

	// Domain is the base domain for sandbox subdomains.
	// Example: "agentlab.local" produces "mybox.agentlab.local"
	Domain string

	// TLSMode controls how TLS certificates are obtained.
	TLSMode TLSMode

	// TLSEmail is the email for Let's Encrypt registration.
	TLSEmail string

	// CaddyAPI is the Caddy admin API endpoint.
	CaddyAPI string

	// HostsFile is the path to the DNS hosts file.
	HostsFile string

	// CADir is the directory for the self-signed CA cert/key.
	CADir string

	// TLSCertDir is the directory for issued TLS certificates.
	TLSCertDir string

	// ProxyIP is the IP address the proxy listens on.
	// Used for DNS entries to point subdomains at the proxy.
	ProxyIP string
}

// CaddyPublisher implements the daemon's ExposurePublisher interface
// using Caddy as a reverse proxy with automatic TLS.
//
// For each exposure, it:
//  1. Creates a DNS entry mapping subdomain.domain to the proxy IP
//  2. Adds a Caddy route proxying the subdomain to the sandbox IP:port
//  3. Provisions a TLS certificate (self-signed or via Let's Encrypt)
//  4. Cleans up all of the above on unexpose
type CaddyPublisher struct {
	config ProxyConfig
	client *CaddyClient
	dns    *DNSResolver
	ca     *CA
	logger *log.Logger
	mu     sync.Mutex
}

// NewCaddyPublisher creates a new Caddy-based exposure publisher.
func NewCaddyPublisher(cfg ProxyConfig, logger *log.Logger) (*CaddyPublisher, error) {
	if logger == nil {
		logger = log.Default()
	}

	if strings.TrimSpace(cfg.Domain) == "" {
		return nil, fmt.Errorf("proxy domain is required")
	}

	client := NewCaddyClient(cfg.CaddyAPI, logger)
	dns := NewDNSResolver(cfg.HostsFile, logger)

	p := &CaddyPublisher{
		config: cfg,
		client: client,
		dns:    dns,
		logger: logger,
	}

	// Initialize self-signed CA if needed
	if cfg.TLSMode == TLSModeSelfSigned {
		caDir := cfg.CADir
		if strings.TrimSpace(caDir) == "" {
			caDir = filepath.Join("/var/lib/agentlab", "ca")
		}
		ca, err := LoadOrGenerateCA(caDir)
		if err != nil {
			return nil, fmt.Errorf("initialize CA: %w", err)
		}
		p.ca = ca
		logger.Printf("proxy: CA loaded from %s", caDir)
	}

	// Ensure TLS cert directory exists
	if cfg.TLSMode == TLSModeSelfSigned && strings.TrimSpace(cfg.TLSCertDir) != "" {
		if err := os.MkdirAll(cfg.TLSCertDir, 0o700); err != nil {
			return nil, fmt.Errorf("create TLS cert dir: %w", err)
		}
	}

	return p, nil
}

// PublishResult captures the outcome of publishing a sandbox exposure.
type PublishResult struct {
	URL   string
	State string
}

// ValidateTargetIP reports whether ip is acceptable as an exposure target
// (review F2). Loopback, link-local, unspecified, multicast, and broadcast
// addresses are refused: an exposure must never forward tailnet or public
// traffic to a daemon-local or link-scoped listener. Subnet membership is
// enforced by the caller, which knows the configured agent subnet.
func ValidateTargetIP(ip string) error {
	parsed := net.ParseIP(strings.TrimSpace(ip))
	if parsed == nil {
		return fmt.Errorf("target %q is not a valid ip address", ip)
	}
	switch {
	case parsed.IsLoopback():
		return fmt.Errorf("target %s is a loopback address", parsed)
	case parsed.IsLinkLocalUnicast() || parsed.IsLinkLocalMulticast():
		return fmt.Errorf("target %s is a link-local address", parsed)
	case parsed.IsUnspecified():
		return fmt.Errorf("target %s is an unspecified address", parsed)
	case parsed.IsMulticast():
		return fmt.Errorf("target %s is a multicast address", parsed)
	case parsed.Equal(net.IPv4bcast):
		return fmt.Errorf("target %s is a broadcast address", parsed)
	default:
		return nil
	}
}

// Publish configures a reverse proxy route for the sandbox exposure.
//
// It assigns a subdomain, creates DNS and Caddy route entries, and
// provisions TLS if configured.
func (p *CaddyPublisher) Publish(ctx context.Context, name string, targetIP string, port int) (PublishResult, error) {
	if p == nil {
		return PublishResult{}, fmt.Errorf("caddy publisher not configured")
	}
	// Defense in depth (review F2): refuse to route an exposure to a
	// daemon-local or link-scoped address before any state changes.
	if err := ValidateTargetIP(targetIP); err != nil {
		return PublishResult{}, err
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	subdomain := SanitizeSubdomain(name)
	fqdn := fmt.Sprintf("%s.%s", subdomain, p.config.Domain)
	targetAddr := fmt.Sprintf("%s:%d", targetIP, port)

	// 1. Add DNS entry
	proxyIP := p.config.ProxyIP
	if strings.TrimSpace(proxyIP) == "" {
		proxyIP = resolveProxyIP(targetIP)
	}
	if err := p.dns.AddEntry(ctx, proxyIP, fqdn); err != nil {
		p.logger.Printf("proxy: dns add failed for %s: %v", fqdn, err)
		// Non-fatal: DNS is best-effort
	}

	// 2. Provision TLS certificate if self-signed mode
	if p.config.TLSMode == TLSModeSelfSigned && p.ca != nil {
		if err := p.provisionCert(fqdn); err != nil {
			p.logger.Printf("proxy: tls cert for %s: %v", fqdn, err)
			// Non-fatal: Caddy can still serve HTTP
		}
	}

	// 3. Add Caddy route
	if err := p.client.AddRoute(ctx, fqdn, targetAddr); err != nil {
		// Cleanup DNS on failure
		_ = p.dns.RemoveEntry(ctx, fqdn)
		return PublishResult{}, fmt.Errorf("add caddy route: %w", err)
	}

	// Build URL
	scheme := "http"
	if p.config.TLSMode != TLSModeOff {
		scheme = "https"
	}
	url := fmt.Sprintf("%s://%s", scheme, fqdn)

	// Health check
	state := "serving"
	if err := p.healthCheck(ctx, targetIP, port); err != nil {
		state = "unhealthy"
		p.logger.Printf("proxy: health check failed for %s: %v", targetAddr, err)
	} else {
		state = "healthy"
	}

	p.logger.Printf("proxy: published %s -> %s (%s)", fqdn, targetAddr, state)

	return PublishResult{
		URL:   url,
		State: state,
	}, nil
}

// Unpublish removes a reverse proxy route for the sandbox exposure.
func (p *CaddyPublisher) Unpublish(ctx context.Context, name string, port int) error {
	if p == nil {
		return fmt.Errorf("caddy publisher not configured")
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	subdomain := SanitizeSubdomain(name)
	fqdn := fmt.Sprintf("%s.%s", subdomain, p.config.Domain)

	// Remove Caddy route
	if err := p.client.RemoveRoute(ctx, fqdn); err != nil {
		p.logger.Printf("proxy: remove route %s: %v", fqdn, err)
	}

	// Remove DNS entry
	if err := p.dns.RemoveEntry(ctx, fqdn); err != nil {
		p.logger.Printf("proxy: remove dns %s: %v", fqdn, err)
	}

	// Remove TLS cert
	if p.config.TLSMode == TLSModeSelfSigned {
		p.removeCert(fqdn)
	}

	p.logger.Printf("proxy: unpublished %s", fqdn)
	return nil
}

// provisionCert creates a self-signed TLS certificate for the domain.
func (p *CaddyPublisher) provisionCert(domain string) error {
	if p.ca == nil {
		return fmt.Errorf("CA not initialized")
	}

	certPEM, err := p.ca.IssueCert(domain)
	if err != nil {
		return err
	}

	certDir := p.config.TLSCertDir
	if strings.TrimSpace(certDir) == "" {
		certDir = filepath.Join("/var/lib/agentlab", "tls")
	}
	if err := os.MkdirAll(certDir, 0o700); err != nil {
		return err
	}

	certFile := filepath.Join(certDir, SanitizeSubdomain(domain)+".pem")
	return os.WriteFile(certFile, certPEM, 0o600)
}

// removeCert removes the self-signed TLS certificate for the domain.
func (p *CaddyPublisher) removeCert(domain string) {
	certDir := p.config.TLSCertDir
	if strings.TrimSpace(certDir) == "" {
		certDir = filepath.Join("/var/lib/agentlab", "tls")
	}
	certFile := filepath.Join(certDir, SanitizeSubdomain(domain)+".pem")
	if err := os.Remove(certFile); err != nil && !os.IsNotExist(err) {
		p.logger.Printf("proxy: remove cert %s: %v", certFile, err)
	}
}

// healthCheck performs a TCP health check against the target.
func (p *CaddyPublisher) healthCheck(ctx context.Context, ip string, port int) error {
	addr := net.JoinHostPort(ip, fmt.Sprintf("%d", port))
	dialer := net.Dialer{Timeout: 2 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	return conn.Close()
}

// SanitizeSubdomain converts a sandbox name to a DNS-safe subdomain.
func SanitizeSubdomain(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = strings.ReplaceAll(s, "_", "-")
	s = strings.ReplaceAll(s, " ", "-")
	// Remove any character that isn't alphanumeric or hyphen
	var result strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			result.WriteRune(r)
		}
	}
	sub := result.String()
	sub = strings.Trim(sub, "-")
	if sub == "" {
		return "sandbox"
	}
	return sub
}

// resolveProxyIP attempts to determine the proxy's IP from the target subnet.
func resolveProxyIP(targetIP string) string {
	ip := net.ParseIP(targetIP)
	if ip == nil {
		return "127.0.0.1"
	}
	if ip.IsLoopback() {
		return "127.0.0.1"
	}
	// Use the gateway IP of the subnet (x.x.x.1)
	ip = ip.To4()
	if ip != nil {
		gw := make(net.IP, len(ip))
		copy(gw, ip)
		gw[3] = 1
		return gw.String()
	}
	return "127.0.0.1"
}
