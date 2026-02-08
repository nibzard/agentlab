package daemon

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/agentlab/agentlab/internal/proxmox"
)

const (
	defaultTailscaleCommand        = "tailscale"
	defaultTailscaleCommandTimeout = 10 * time.Second
	defaultTailscaleTCPTimeout     = 2 * time.Second
	defaultTailscaleHTTPTimeout    = 2 * time.Second
)

var defaultHTTPHealthPorts = map[int]struct{}{
	80:   {},
	443:  {},
	8000: {},
	8080: {},
	3000: {},
}

// TailscaleServePublisher installs exposure rules using `tailscale serve`.
type TailscaleServePublisher struct {
	Runner         proxmox.CommandRunner
	Command        string
	CommandTimeout time.Duration
	TCPTimeout     time.Duration
	HTTPTimeout    time.Duration
	HTTPPorts      map[int]struct{}
}

// Publish configures tailscale serve for the exposure and performs health checks.
func (p *TailscaleServePublisher) Publish(ctx context.Context, name string, targetIP string, port int) (ExposurePublishResult, error) {
	if p == nil {
		return ExposurePublishResult{}, errors.New("tailscale serve publisher not configured")
	}
	targetIP = strings.TrimSpace(targetIP)
	if targetIP == "" {
		return ExposurePublishResult{}, errors.New("target ip is required")
	}
	if port <= 0 || port > 65535 {
		return ExposurePublishResult{}, fmt.Errorf("port must be between 1 and 65535")
	}
	serveTarget := fmt.Sprintf("tcp://%s:%d", targetIP, port)
	if _, err := p.run(ctx, "serve", fmt.Sprintf("--tcp=%d", port), serveTarget); err != nil {
		return ExposurePublishResult{}, err
	}
	dnsName, err := p.resolveDNSName(ctx)
	if err != nil {
		_ = p.Unpublish(ctx, name, port)
		return ExposurePublishResult{}, err
	}
	url := fmt.Sprintf("tcp://%s:%d", dnsName, port)
	state := exposureStateServing

	tcpErr := tcpHealthCheck(ctx, targetIP, port, p.tcpTimeout())
	if tcpErr != nil {
		state = exposureStateUnhealthy
		return ExposurePublishResult{URL: url, State: state}, nil
	}

	if p.shouldHTTPCheck(port) {
		if err := httpHealthCheck(ctx, targetIP, port, p.httpTimeout()); err == nil {
			state = exposureStateHealthy
		}
	}

	return ExposurePublishResult{URL: url, State: state}, nil
}

// Unpublish removes the tailscale serve rule for the exposure.
func (p *TailscaleServePublisher) Unpublish(ctx context.Context, name string, port int) error {
	if p == nil {
		return errors.New("tailscale serve publisher not configured")
	}
	if port <= 0 || port > 65535 {
		return fmt.Errorf("port must be between 1 and 65535")
	}
	_, err := p.run(ctx, "serve", fmt.Sprintf("--tcp=%d", port), "off")
	if err != nil {
		if isServeRuleNotFound(err) {
			return ErrServeRuleNotFound
		}
		return err
	}
	return nil
}

func (p *TailscaleServePublisher) run(ctx context.Context, args ...string) (string, error) {
	runner := p.Runner
	if runner == nil {
		runner = proxmox.ExecRunner{}
	}
	cmd := strings.TrimSpace(p.Command)
	if cmd == "" {
		cmd = defaultTailscaleCommand
	}
	ctx, cancel := withOptionalTimeout(ctx, p.commandTimeout())
	defer cancel()
	return runner.Run(ctx, cmd, args...)
}

func (p *TailscaleServePublisher) resolveDNSName(ctx context.Context) (string, error) {
	output, err := p.run(ctx, "status", "--json")
	if err != nil {
		return "", fmt.Errorf("tailscale status failed: %w", err)
	}
	var status struct {
		Self struct {
			DNSName  string `json:"DNSName"`
			HostName string `json:"HostName"`
		} `json:"Self"`
		MagicDNSSuffix string `json:"MagicDNSSuffix"`
	}
	if err := json.Unmarshal([]byte(output), &status); err != nil {
		return "", fmt.Errorf("parse tailscale status: %w", err)
	}
	dns := strings.TrimSpace(status.Self.DNSName)
	dns = strings.TrimSuffix(dns, ".")
	if dns == "" {
		host := strings.TrimSpace(status.Self.HostName)
		suffix := strings.TrimSpace(status.MagicDNSSuffix)
		suffix = strings.TrimSuffix(suffix, ".")
		if host != "" && suffix != "" {
			dns = fmt.Sprintf("%s.%s", host, suffix)
		}
	}
	if dns == "" {
		return "", errors.New("tailscale status missing dns name")
	}
	return dns, nil
}

func (p *TailscaleServePublisher) shouldHTTPCheck(port int) bool {
	ports := p.HTTPPorts
	if ports == nil {
		ports = defaultHTTPHealthPorts
	}
	_, ok := ports[port]
	return ok
}

func (p *TailscaleServePublisher) commandTimeout() time.Duration {
	if p.CommandTimeout > 0 {
		return p.CommandTimeout
	}
	return defaultTailscaleCommandTimeout
}

func (p *TailscaleServePublisher) tcpTimeout() time.Duration {
	if p.TCPTimeout > 0 {
		return p.TCPTimeout
	}
	return defaultTailscaleTCPTimeout
}

func (p *TailscaleServePublisher) httpTimeout() time.Duration {
	if p.HTTPTimeout > 0 {
		return p.HTTPTimeout
	}
	return defaultTailscaleHTTPTimeout
}

func withOptionalTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		return context.WithCancel(ctx)
	}
	if _, ok := ctx.Deadline(); ok {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, timeout)
}

func tcpHealthCheck(ctx context.Context, targetIP string, port int, timeout time.Duration) error {
	addr := net.JoinHostPort(targetIP, strconv.Itoa(port))
	dialer := net.Dialer{Timeout: timeout}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	return conn.Close()
}

func httpHealthCheck(ctx context.Context, targetIP string, port int, timeout time.Duration) error {
	addr := net.JoinHostPort(targetIP, strconv.Itoa(port))
	if port == 443 {
		if err := httpProbe(ctx, "https", addr, timeout, true); err == nil {
			return nil
		}
	}
	return httpProbe(ctx, "http", addr, timeout, false)
}

func httpProbe(ctx context.Context, scheme string, addr string, timeout time.Duration, skipVerify bool) error {
	client := &http.Client{Timeout: timeout}
	if scheme == "https" {
		client.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: skipVerify},
		}
	}
	url := fmt.Sprintf("%s://%s/", scheme, addr)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}

func isServeRuleNotFound(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "not found") {
		return true
	}
	if strings.Contains(msg, "no serve") {
		return true
	}
	if strings.Contains(msg, "no matching") {
		return true
	}
	if strings.Contains(msg, "no listener") {
		return true
	}
	return false
}
