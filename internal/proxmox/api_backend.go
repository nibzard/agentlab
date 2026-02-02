package proxmox

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// APIBackend implements Backend using Proxmox REST API.
type APIBackend struct {
	// HTTP client configuration
	HTTPClient    *http.Client
	BaseURL       string // e.g., "https://localhost:8006/api2/json"
	APIToken      string // format: "USER@REALM!TOKENID=TOKEN"
	APITokenID    string // extracted from APIToken
	APITokenValue string // extracted from APIToken

	// Proxmox configuration
	Node           string
	AgentCIDR      string
	CommandTimeout time.Duration

	// DHCP configuration for GuestIP fallback
	DHCPLeasePaths []string

	// Testing hooks
	Sleep func(ctx context.Context, d time.Duration) error
}

var _ Backend = (*APIBackend)(nil)

// APIResponse represents the standard Proxmox API response structure.
type APIResponse struct {
	Data interface{} `json:"data"`
}

// Clone creates a new VM by cloning a template.
func (b *APIBackend) Clone(ctx context.Context, template VMID, target VMID, name string) error {
	node, err := b.ensureNode(ctx)
	if err != nil {
		return err
	}

	params := url.Values{}
	params.Set("newid", strconv.Itoa(int(target)))
	params.Set("full", "1")
	if name != "" {
		params.Set("name", name)
	}

	endpoint := fmt.Sprintf("/nodes/%s/qemu/%d/clone", node, template)
	_, err = b.doPost(ctx, endpoint, params)
	return err
}

// Configure updates VM configuration.
func (b *APIBackend) Configure(ctx context.Context, vmid VMID, cfg VMConfig) error {
	node, err := b.ensureNode(ctx)
	if err != nil {
		return err
	}

	params := url.Values{}
	if cfg.Name != "" {
		params.Set("name", cfg.Name)
	}
	if cfg.Cores > 0 {
		params.Set("cores", strconv.Itoa(cfg.Cores))
	}
	if cfg.MemoryMB > 0 {
		params.Set("memory", strconv.Itoa(cfg.MemoryMB))
	}
	if cfg.CPUPinning != "" {
		params.Set("cpulimit", cfg.CPUPinning)
	}
	if cfg.Bridge != "" || cfg.NetModel != "" {
		net0 := buildNet0(cfg.NetModel, cfg.Bridge)
		params.Set("net0", net0)
	}
	if cfg.CloudInit != "" {
		params.Set("cicustom", formatCICustom(cfg.CloudInit))
	}

	if len(params) == 0 {
		return nil
	}

	endpoint := fmt.Sprintf("/nodes/%s/qemu/%d/config", node, vmid)
	_, err = b.doPut(ctx, endpoint, params)
	return err
}

// Start starts a VM.
func (b *APIBackend) Start(ctx context.Context, vmid VMID) error {
	node, err := b.ensureNode(ctx)
	if err != nil {
		return err
	}

	endpoint := fmt.Sprintf("/nodes/%s/qemu/%d/status/start", node, vmid)
	_, err = b.doPost(ctx, endpoint, nil)
	return err
}

// Stop stops a VM.
func (b *APIBackend) Stop(ctx context.Context, vmid VMID) error {
	node, err := b.ensureNode(ctx)
	if err != nil {
		return err
	}

	endpoint := fmt.Sprintf("/nodes/%s/qemu/%d/status/stop", node, vmid)
	_, err = b.doPost(ctx, endpoint, nil)
	if err != nil {
		if isAPIVMNotFound(err) {
			return fmt.Errorf("%w: %v", ErrVMNotFound, err)
		}
		return err
	}
	return nil
}

// Destroy deletes a VM.
func (b *APIBackend) Destroy(ctx context.Context, vmid VMID) error {
	node, err := b.ensureNode(ctx)
	if err != nil {
		return err
	}

	params := url.Values{}
	params.Set("purge", "1")

	endpoint := fmt.Sprintf("/nodes/%s/qemu/%d", node, vmid)
	_, err = b.doDelete(ctx, endpoint, params)
	if err != nil {
		if isAPIVMNotFound(err) {
			return fmt.Errorf("%w: %v", ErrVMNotFound, err)
		}
		return err
	}
	return nil
}

// Status retrieves VM status.
func (b *APIBackend) Status(ctx context.Context, vmid VMID) (Status, error) {
	node, err := b.ensureNode(ctx)
	if err != nil {
		return StatusUnknown, err
	}

	endpoint := fmt.Sprintf("/nodes/%s/qemu/%d/status/current", node, vmid)
	data, err := b.doGet(ctx, endpoint)
	if err != nil {
		return StatusUnknown, err
	}

	var result struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return StatusUnknown, fmt.Errorf("parse status: %w", err)
	}

	switch strings.ToLower(result.Status) {
	case "running":
		return StatusRunning, nil
	case "stopped":
		return StatusStopped, nil
	default:
		return StatusUnknown, nil
	}
}

// GuestIP retrieves the guest IP address.
func (b *APIBackend) GuestIP(ctx context.Context, vmid VMID) (string, error) {
	node, err := b.ensureNode(ctx)
	if err != nil {
		return "", err
	}

	ip, err := b.pollGuestAgentIP(ctx, node, vmid)
	if err == nil {
		return ip, nil
	}
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	qgaErr := err

	ip, dhcpErr := b.dhcpLeaseIP(ctx, vmid)
	if dhcpErr == nil {
		return ip, nil
	}

	if dhcpErr == ErrGuestIPNotFound {
		return "", fmt.Errorf("%w: qemu-guest-agent=%v dhcp=%v", ErrGuestIPNotFound, qgaErr, dhcpErr)
	}
	return "", dhcpErr
}

// CreateVolume creates a new volume.
func (b *APIBackend) CreateVolume(ctx context.Context, storage, name string, sizeGB int) (string, error) {
	storage = strings.TrimSpace(storage)
	if storage == "" {
		return "", fmt.Errorf("storage is required")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("volume name is required")
	}
	if sizeGB <= 0 {
		return "", fmt.Errorf("size_gb must be positive")
	}

	node, err := b.ensureNode(ctx)
	if err != nil {
		return "", err
	}

	params := url.Values{}
	params.Set("vmid", "0")
	params.Set("filename", name)
	params.Set("size", fmt.Sprintf("%dG", sizeGB))

	endpoint := fmt.Sprintf("/nodes/%s/storage/%s/content", node, storage)
	data, err := b.doPost(ctx, endpoint, params)
	if err != nil {
		return "", err
	}

	var result struct {
		VolID string `json:"volid"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return "", fmt.Errorf("parse create volume response: %w", err)
	}

	if result.VolID == "" {
		return "", fmt.Errorf("empty volume id in response")
	}

	return result.VolID, nil
}

// AttachVolume attaches a volume to a VM.
func (b *APIBackend) AttachVolume(ctx context.Context, vmid VMID, volumeID, slot string) error {
	volumeID = strings.TrimSpace(volumeID)
	if volumeID == "" {
		return fmt.Errorf("volume id is required")
	}
	slot = strings.TrimSpace(slot)
	if slot == "" {
		return fmt.Errorf("slot is required")
	}

	node, err := b.ensureNode(ctx)
	if err != nil {
		return err
	}

	params := url.Values{}
	params.Set(slot, volumeID)

	endpoint := fmt.Sprintf("/nodes/%s/qemu/%d/config", node, vmid)
	_, err = b.doPut(ctx, endpoint, params)
	return err
}

// DetachVolume detaches a volume from a VM.
func (b *APIBackend) DetachVolume(ctx context.Context, vmid VMID, slot string) error {
	slot = strings.TrimSpace(slot)
	if slot == "" {
		return fmt.Errorf("slot is required")
	}

	node, err := b.ensureNode(ctx)
	if err != nil {
		return err
	}

	params := url.Values{}
	params.Set("delete", slot)

	endpoint := fmt.Sprintf("/nodes/%s/qemu/%d/config", node, vmid)
	_, err = b.doPut(ctx, endpoint, params)
	if err != nil {
		if isAPIVMNotFound(err) {
			return fmt.Errorf("%w: %v", ErrVMNotFound, err)
		}
		return err
	}
	return nil
}

// DeleteVolume deletes a volume.
func (b *APIBackend) DeleteVolume(ctx context.Context, volumeID string) error {
	volumeID = strings.TrimSpace(volumeID)
	if volumeID == "" {
		return fmt.Errorf("volume id is required")
	}

	parts := strings.SplitN(volumeID, ":", 2)
	if len(parts) != 2 {
		return fmt.Errorf("invalid volume id format: %s", volumeID)
	}

	storage := parts[0]
	_ = parts[1] // volume name not used, but present for validation

	node, err := b.ensureNode(ctx)
	if err != nil {
		return err
	}

	endpoint := fmt.Sprintf("/nodes/%s/storage/%s/content/%s", node, storage, volumeID)
	_, err = b.doDelete(ctx, endpoint, nil)
	return err
}

// ValidateTemplate checks if a template VM is suitable for provisioning.
func (b *APIBackend) ValidateTemplate(ctx context.Context, template VMID) error {
	node, err := b.ensureNode(ctx)
	if err != nil {
		return err
	}

	endpoint := fmt.Sprintf("/nodes/%s/qemu/%d/config", node, template)
	data, err := b.doGet(ctx, endpoint)
	if err != nil {
		if isAPIVMNotFound(err) {
			return fmt.Errorf("template VM %d does not exist", template)
		}
		return fmt.Errorf("failed to query template VM %d: %w", template, err)
	}

	// Proxmox API returns config with mixed types (strings, numbers, etc.)
	// We need to handle this by checking the agent field which can be a number or string
	var config map[string]interface{}
	if err := json.Unmarshal(data, &config); err != nil {
		return fmt.Errorf("parse template config: %w", err)
	}

	agentConfig, exists := config["agent"]
	if !exists {
		return fmt.Errorf("template VM %d does not have qemu-guest-agent enabled (missing 'agent:' config)", template)
	}

	// Check if agent is disabled
	agentStr := ""
	switch v := agentConfig.(type) {
	case string:
		agentStr = v
	case float64:
		if v == 0 {
			return fmt.Errorf("template VM %d has qemu-guest-agent explicitly disabled (agent: 0)", template)
		}
		// agent: 1 means enabled
		return nil
	case int:
		if v == 0 {
			return fmt.Errorf("template VM %d has qemu-guest-agent explicitly disabled (agent: 0)", template)
		}
		return nil
	default:
		return fmt.Errorf("template VM %d has unknown agent config type", template)
	}

	if strings.Contains(agentStr, "0") || strings.Contains(agentStr, "disabled=1") {
		return fmt.Errorf("template VM %d has qemu-guest-agent explicitly disabled", template)
	}

	return nil
}

// HTTP client methods

func (b *APIBackend) client() *http.Client {
	if b.HTTPClient != nil {
		return b.HTTPClient
	}

	// Create default client with insecure TLS skip for self-signed certs
	return &http.Client{
		Timeout: b.commandTimeout(),
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true, // Skip cert verification for local Proxmox
			},
		},
	}
}

func (b *APIBackend) commandTimeout() time.Duration {
	if b.CommandTimeout > 0 {
		return b.CommandTimeout
	}
	return 2 * time.Minute
}

func (b *APIBackend) doRequest(ctx context.Context, method, endpoint string, body io.Reader) ([]byte, error) {
	url := b.BaseURL + endpoint
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	// Set authentication header
	if b.APIToken != "" {
		req.Header.Set("Authorization", "PVEAPIToken="+b.APIToken)
	}

	// Set content type for body requests
	if body != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}

	// Make request
	client := b.client()
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	// Read response
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	// Check for API errors
	if resp.StatusCode != http.StatusOK {
		var apiErr struct {
			Errors map[string]string `json:"errors"`
		}
		if json.Unmarshal(respBody, &apiErr) == nil && len(apiErr.Errors) > 0 {
			var errStrings []string
			for _, v := range apiErr.Errors {
				errStrings = append(errStrings, v)
			}
			return nil, fmt.Errorf("API error (status %d): %s", resp.StatusCode, strings.Join(errStrings, ", "))
		}

		// Try parsing as error response
		var simpleErr struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(respBody, &simpleErr) == nil && simpleErr.Message != "" {
			return nil, fmt.Errorf("API error (status %d): %s", resp.StatusCode, simpleErr.Message)
		}

		return nil, fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	// Parse standard API response
	var apiResp APIResponse
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		// Some endpoints return data directly
		return respBody, nil
	}

	// Extract data as JSON
	data, err := json.Marshal(apiResp.Data)
	if err != nil {
		return nil, fmt.Errorf("marshal response data: %w", err)
	}

	return data, nil
}

func (b *APIBackend) doGet(ctx context.Context, endpoint string) ([]byte, error) {
	return b.doRequest(ctx, http.MethodGet, endpoint, nil)
}

func (b *APIBackend) doPost(ctx context.Context, endpoint string, params url.Values) ([]byte, error) {
	var body io.Reader
	if params != nil {
		body = strings.NewReader(params.Encode())
	}
	return b.doRequest(ctx, http.MethodPost, endpoint, body)
}

func (b *APIBackend) doPut(ctx context.Context, endpoint string, params url.Values) ([]byte, error) {
	var body io.Reader
	if params != nil {
		body = strings.NewReader(params.Encode())
	}
	return b.doRequest(ctx, http.MethodPut, endpoint, body)
}

func (b *APIBackend) doDelete(ctx context.Context, endpoint string, params url.Values) ([]byte, error) {
	if params != nil && len(params) > 0 {
		endpoint += "?" + params.Encode()
	}
	return b.doRequest(ctx, http.MethodDelete, endpoint, nil)
}

// Helper methods

func (b *APIBackend) ensureNode(ctx context.Context) (string, error) {
	if b.Node != "" {
		return b.Node, nil
	}

	data, err := b.doGet(ctx, "/nodes")
	if err != nil {
		return "", err
	}

	var nodes []struct {
		Node string `json:"node"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(data, &nodes); err != nil {
		return "", fmt.Errorf("parse node list: %w", err)
	}

	if len(nodes) == 0 {
		return "", fmt.Errorf("no nodes found")
	}

	b.Node = nodes[0].Node
	return b.Node, nil
}

func (b *APIBackend) pollGuestAgentIP(ctx context.Context, node string, vmid VMID) (string, error) {
	attempts := 30
	initialWait := 500 * time.Millisecond
	maxWait := 10 * time.Second
	wait := initialWait

	var lastErr error
	for i := 0; i < attempts; i++ {
		ip, err := b.guestAgentIP(ctx, node, vmid)
		if err == nil && ip != "" {
			return ip, nil
		}
		if err != nil {
			lastErr = err
		} else {
			lastErr = ErrGuestIPNotFound
		}
		if i == attempts-1 {
			break
		}
		if err := b.sleep(ctx, wait); err != nil {
			return "", err
		}
		wait = nextBackoff(wait, maxWait)
	}

	if lastErr == nil {
		lastErr = ErrGuestIPNotFound
	}
	return "", lastErr
}

func (b *APIBackend) guestAgentIP(ctx context.Context, node string, vmid VMID) (string, error) {
	endpoint := fmt.Sprintf("/nodes/%s/qemu/%d/agent/network-get-interfaces", node, vmid)
	data, err := b.doGet(ctx, endpoint)
	if err != nil {
		return "", err
	}

	var resp agentNetResp
	if err := json.Unmarshal(data, &resp); err != nil {
		return "", fmt.Errorf("parse agent response: %w", err)
	}

	ips := collectIPv4(resp.Result)
	if len(ips) == 0 {
		return "", ErrGuestIPNotFound
	}

	return b.selectIP(ips)
}

func (b *APIBackend) selectIP(ips []net.IP) (string, error) {
	if len(ips) == 0 {
		return "", ErrGuestIPNotFound
	}
	if b.AgentCIDR != "" {
		ip, err := selectIPByCIDR(ips, b.AgentCIDR)
		if err != nil {
			return "", err
		}
		if ip == "" {
			return "", ErrGuestIPNotFound
		}
		return ip, nil
	}
	return ips[0].String(), nil
}

func (b *APIBackend) dhcpLeaseIP(ctx context.Context, vmid VMID) (string, error) {
	var netblock *net.IPNet
	if b.AgentCIDR != "" {
		_, parsed, err := net.ParseCIDR(b.AgentCIDR)
		if err != nil {
			return "", fmt.Errorf("invalid agent CIDR %q: %w", b.AgentCIDR, err)
		}
		netblock = parsed
	}

	node, err := b.ensureNode(ctx)
	if err != nil {
		return "", err
	}

	// Get VM config to find MAC addresses
	endpoint := fmt.Sprintf("/nodes/%s/qemu/%d/config", node, vmid)
	data, err := b.doGet(ctx, endpoint)
	if err != nil {
		return "", err
	}

	var config map[string]string
	if err := json.Unmarshal(data, &config); err != nil {
		return "", fmt.Errorf("parse vm config: %w", err)
	}

	macs := []string{}
	for k, v := range config {
		if strings.HasPrefix(k, "net") {
			if mac := extractMAC(v); mac != "" {
				macs = append(macs, normalizeMAC(mac))
			}
		}
	}

	if len(macs) == 0 {
		return "", fmt.Errorf("%w: no MAC addresses found", ErrGuestIPNotFound)
	}

	leaseFiles := b.leasePaths()
	if len(leaseFiles) == 0 {
		return "", fmt.Errorf("%w: no DHCP lease files configured", ErrGuestIPNotFound)
	}

	var readErr error
	for _, path := range leaseFiles {
		content, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			readErr = err
			continue
		}
		if ip := findLeaseIP(content, macs, netblock); ip != "" {
			return ip, nil
		}
	}

	if readErr != nil {
		return "", readErr
	}
	return "", ErrGuestIPNotFound
}

func (b *APIBackend) leasePaths() []string {
	paths := b.DHCPLeasePaths
	if len(paths) == 0 {
		paths = []string{
			"/var/lib/misc/dnsmasq.leases",
			"/var/lib/dnsmasq/dnsmasq.leases",
			"/var/lib/misc/dnsmasq.*.leases",
			"/var/lib/misc/dnsmasq*.leases",
			"/var/lib/dhcp/dhcpd.leases",
			"/var/lib/dhcp/dhcpd.leases~",
			"/var/lib/dhcp3/dhcpd.leases",
			"/var/lib/pve-firewall/dhcpd.leases",
		}
	}

	expanded := make([]string, 0, len(paths))
	seen := map[string]struct{}{}
	for _, path := range paths {
		if hasGlob(path) {
			matches, err := filepath.Glob(path)
			if err != nil {
				continue
			}
			for _, match := range matches {
				if _, ok := seen[match]; ok {
					continue
				}
				seen[match] = struct{}{}
				expanded = append(expanded, match)
			}
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		expanded = append(expanded, path)
	}

	sort.Strings(expanded)
	return expanded
}

func (b *APIBackend) sleep(ctx context.Context, d time.Duration) error {
	if b.Sleep != nil {
		return b.Sleep(ctx, d)
	}
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// NewAPIBackend creates a new APIBackend.
func NewAPIBackend(baseURL, apiToken, node, agentCIDR string, timeout time.Duration) (*APIBackend, error) {
	// Parse API token
	var tokenID, tokenValue string
	if strings.Contains(apiToken, "=") {
		parts := strings.SplitN(apiToken, "=", 2)
		tokenID = parts[0]
		tokenValue = parts[1]
	}

	// Ensure base URL ends with /api2/json
	if !strings.HasSuffix(baseURL, "/api2/json") {
		baseURL = strings.TrimSuffix(baseURL, "/") + "/api2/json"
	}

	return &APIBackend{
		BaseURL:        baseURL,
		APIToken:       apiToken,
		APITokenID:     tokenID,
		APITokenValue:  tokenValue,
		Node:           node,
		AgentCIDR:      agentCIDR,
		CommandTimeout: timeout,
	}, nil
}

// Helper functions

func extractMAC(netConfig string) string {
	// net0: virtio=BC:24:11:D5:49:57,bridge=vmbr1
	fields := strings.Split(netConfig, ",")
	for _, field := range fields {
		if strings.Contains(field, "=") {
			kv := strings.SplitN(field, "=", 2)
			if len(kv) == 2 && strings.ToLower(kv[0]) == "mac" {
				return strings.TrimSpace(kv[1])
			}
		}
	}
	return ""
}

func isAPIVMNotFound(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	indicators := []string{
		"does not exist",
		"no such vm",
		"no such qemu",
		"no such vmid",
		"vmid does not exist",
		"no vmid found",
	}
	for _, indicator := range indicators {
		if strings.Contains(msg, indicator) {
			return true
		}
	}
	return strings.Contains(msg, "not found") && strings.Contains(msg, "vm")
}
