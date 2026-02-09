// ABOUTME: This file implements the Backend interface using Proxmox's REST API.
// The API backend is recommended for production use due to better reliability,
// error handling, and avoidance of Proxmox IPC layer issues.
package proxmox

import (
	"context"
	"crypto/tls"
	"crypto/x509"
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
// ABOUTME: This backend uses HTTP requests to Proxmox's API at /api2/json.
// It requires an API token for authentication and supports automatic node detection.
type APIBackend struct {
	// HTTP client configuration
	HTTPClient    *http.Client // Custom HTTP client (optional, defaults to TLS-configured client)
	BaseURL       string       // Proxmox API base URL (e.g., "https://localhost:8006/api2/json")
	APIToken      string       // Full API token in format "USER@REALM!TOKENID=TOKEN"
	APITokenID    string       // Extracted token ID from APIToken
	APITokenValue string       // Extracted token value from APIToken

	// Proxmox configuration
	Node           string        // Proxmox node name (empty for auto-detection)
	AgentCIDR      string        // CIDR block for selecting guest IPs (e.g., "10.77.0.0/16")
	CommandTimeout time.Duration // Timeout for API commands (defaults to 2 minutes)
	CloneMode      string        // "linked" or "full" clone mode (default: linked)

	// DHCP configuration for GuestIP fallback
	DHCPLeasePaths []string // Paths to DHCP lease files for fallback IP discovery

	// Testing hooks
	Sleep func(ctx context.Context, d time.Duration) error // Custom sleep function for testing
}

var _ Backend = (*APIBackend)(nil)

// APIResponse represents the standard Proxmox API response structure.
type APIResponse struct {
	Data interface{} `json:"data"`
}

// Clone creates a new VM by cloning a template.
// ABOUTME: Uses linked clones by default; set CloneMode="full" for full clones.
func (b *APIBackend) Clone(ctx context.Context, template VMID, target VMID, name string) error {
	node, err := b.ensureNode(ctx)
	if err != nil {
		return err
	}

	params := url.Values{}
	params.Set("newid", strconv.Itoa(int(target)))
	full := "0"
	if strings.EqualFold(strings.TrimSpace(b.CloneMode), "full") {
		full = "1"
	}
	params.Set("full", full)
	if name != "" {
		params.Set("name", name)
	}

	endpoint := fmt.Sprintf("/nodes/%s/qemu/%d/clone", node, template)
	task, err := b.doPost(ctx, endpoint, params)
	if err != nil {
		if !shouldRetryFullClone(err) {
			return err
		}
		linkedErr := err
		params.Set("full", "1")
		task, err = b.doPost(ctx, endpoint, params)
		if err != nil {
			return fmt.Errorf("linked clone failed: %w; full clone retry failed: %v", linkedErr, err)
		}
	}
	if upid := parseTaskUPID(task); upid != "" {
		return b.waitForTask(ctx, node, upid)
	}
	return nil
}

// Configure updates VM configuration.
// ABOUTME: Only non-zero/non-empty fields are applied. Network config requires both bridge and model.
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
	if cfg.SCSIHW != "" {
		params.Set("scsihw", cfg.SCSIHW)
	}
	if cfg.CPUPinning != "" {
		params.Set("cpulist", cfg.CPUPinning)
	}
	if cfg.Bridge != "" || cfg.NetModel != "" || cfg.Firewall != nil || cfg.FirewallGroup != "" {
		net0 := buildNet0(cfg.NetModel, cfg.Bridge, cfg.Firewall, cfg.FirewallGroup)
		params.Set("net0", net0)
	}
	if cfg.CloudInit != "" {
		params.Set("cicustom", formatCICustom(cfg.CloudInit))
	}

	if len(params) > 0 {
		endpoint := fmt.Sprintf("/nodes/%s/qemu/%d/config", node, vmid)
		if _, err := b.doPut(ctx, endpoint, params); err != nil {
			return err
		}
	}

	if cfg.RootDiskGB > 0 {
		if err := b.ensureRootDiskSize(ctx, node, vmid, cfg.RootDisk, cfg.RootDiskGB); err != nil {
			return err
		}
	}
	return nil
}

// Start starts a VM.
// ABOUTME: Sends a start command to the Proxmox API for the specified VM.
func (b *APIBackend) Start(ctx context.Context, vmid VMID) error {
	node, err := b.ensureNode(ctx)
	if err != nil {
		return err
	}

	endpoint := fmt.Sprintf("/nodes/%s/qemu/%d/status/start", node, vmid)
	task, err := b.doPost(ctx, endpoint, nil)
	if err != nil {
		return err
	}
	if upid := parseTaskUPID(task); upid != "" {
		return b.waitForTask(ctx, node, upid)
	}
	return nil
}

// Stop stops a VM.
// ABOUTME: Sends a stop command to the Proxmox API. Returns ErrVMNotFound if the VM doesn't exist.
func (b *APIBackend) Stop(ctx context.Context, vmid VMID) error {
	node, err := b.ensureNode(ctx)
	if err != nil {
		return err
	}

	endpoint := fmt.Sprintf("/nodes/%s/qemu/%d/status/stop", node, vmid)
	task, err := b.doPost(ctx, endpoint, nil)
	if err != nil {
		if isAPIVMNotFound(err) {
			return fmt.Errorf("%w: %v", ErrVMNotFound, err)
		}
		return err
	}
	if upid := parseTaskUPID(task); upid != "" {
		return b.waitForTask(ctx, node, upid)
	}
	return nil
}

// Destroy deletes a VM.
// ABOUTME: Permanently deletes the VM and purges associated disks. Returns ErrVMNotFound if the VM doesn't exist.
func (b *APIBackend) Destroy(ctx context.Context, vmid VMID) error {
	node, err := b.ensureNode(ctx)
	if err != nil {
		return err
	}

	params := url.Values{}
	params.Set("purge", "1")

	endpoint := fmt.Sprintf("/nodes/%s/qemu/%d", node, vmid)
	task, err := b.doDelete(ctx, endpoint, params)
	if err != nil {
		if isAPIVMNotFound(err) {
			return fmt.Errorf("%w: %v", ErrVMNotFound, err)
		}
		return err
	}
	if upid := parseTaskUPID(task); upid != "" {
		return b.waitForTask(ctx, node, upid)
	}
	return nil
}

// SnapshotCreate creates a disk-only snapshot of a VM.
// ABOUTME: The snapshot excludes VM memory state (no vmstate).
func (b *APIBackend) SnapshotCreate(ctx context.Context, vmid VMID, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("snapshot name is required")
	}

	node, err := b.ensureNode(ctx)
	if err != nil {
		return err
	}

	params := url.Values{}
	params.Set("snapname", name)
	params.Set("vmstate", "0")

	endpoint := fmt.Sprintf("/nodes/%s/qemu/%d/snapshot", node, vmid)
	_, err = b.doPost(ctx, endpoint, params)
	return err
}

// SnapshotRollback reverts a VM to the named snapshot.
// ABOUTME: The VM should be stopped before rollback; no vmstate snapshots are used.
func (b *APIBackend) SnapshotRollback(ctx context.Context, vmid VMID, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("snapshot name is required")
	}

	node, err := b.ensureNode(ctx)
	if err != nil {
		return err
	}

	endpoint := fmt.Sprintf("/nodes/%s/qemu/%d/snapshot/%s/rollback", node, vmid, url.PathEscape(name))
	_, err = b.doPost(ctx, endpoint, nil)
	return err
}

// SnapshotDelete removes a snapshot from a VM.
func (b *APIBackend) SnapshotDelete(ctx context.Context, vmid VMID, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("snapshot name is required")
	}

	node, err := b.ensureNode(ctx)
	if err != nil {
		return err
	}

	endpoint := fmt.Sprintf("/nodes/%s/qemu/%d/snapshot/%s", node, vmid, url.PathEscape(name))
	_, err = b.doDelete(ctx, endpoint, nil)
	return err
}

// Status retrieves VM status.
// ABOUTME: Returns StatusRunning, StatusStopped, or StatusUnknown.
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

// CurrentStats retrieves VM runtime statistics.
// ABOUTME: CPUUsage is reported by Proxmox status/current.
func (b *APIBackend) CurrentStats(ctx context.Context, vmid VMID) (VMStats, error) {
	node, err := b.ensureNode(ctx)
	if err != nil {
		return VMStats{}, err
	}

	endpoint := fmt.Sprintf("/nodes/%s/qemu/%d/status/current", node, vmid)
	data, err := b.doGet(ctx, endpoint)
	if err != nil {
		return VMStats{}, err
	}

	var result struct {
		CPU float64 `json:"cpu"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return VMStats{}, fmt.Errorf("parse current stats: %w", err)
	}

	return VMStats{CPUUsage: result.CPU}, nil
}

// GuestIP retrieves the guest IP address.
// ABOUTME: Polls for an IP via DHCP lease lookup and the guest agent in parallel (interleaved),
// since DHCP commonly arrives before the guest agent is installed/started by cloud-init.
// Returns ErrGuestIPNotFound if no IP can be determined.
func (b *APIBackend) GuestIP(ctx context.Context, vmid VMID) (string, error) {
	node, err := b.ensureNode(ctx)
	if err != nil {
		return "", err
	}

	var netblock *net.IPNet
	if b.AgentCIDR != "" {
		_, parsed, err := net.ParseCIDR(b.AgentCIDR)
		if err != nil {
			return "", fmt.Errorf("invalid agent CIDR %q: %w", b.AgentCIDR, err)
		}
		netblock = parsed
	}

	dhcpErr := ErrGuestIPNotFound
	macs := []string{}
	leaseFiles := b.leasePaths()
	if len(leaseFiles) == 0 {
		dhcpErr = fmt.Errorf("%w: no DHCP lease files configured", ErrGuestIPNotFound)
	} else {
		endpoint := fmt.Sprintf("/nodes/%s/qemu/%d/config", node, vmid)
		data, err := b.doGet(ctx, endpoint)
		if err != nil {
			dhcpErr = err
		} else {
			// Proxmox config values can be strings, numbers, etc. We only care about "net*" keys.
			var config map[string]interface{}
			if err := json.Unmarshal(data, &config); err != nil {
				dhcpErr = fmt.Errorf("parse vm config: %w", err)
			} else {
				for k, v := range config {
					if !strings.HasPrefix(k, "net") {
						continue
					}
					s, ok := v.(string)
					if !ok {
						continue
					}
					if mac := extractMAC(s); mac != "" {
						macs = append(macs, normalizeMAC(mac))
					}
				}
				macs = uniqueStrings(macs)
				if len(macs) == 0 {
					dhcpErr = fmt.Errorf("%w: no MAC addresses found", ErrGuestIPNotFound)
				}
			}
		}
	}

	attempts := 0
	if _, ok := ctx.Deadline(); !ok {
		// Avoid infinite polling with a background context.
		attempts = 30
	}
	wait := 250 * time.Millisecond
	maxWait := 2 * time.Second

	qgaErr := ErrGuestIPNotFound
	for i := 0; ; i++ {
		if len(macs) > 0 && len(leaseFiles) > 0 {
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
				dhcpErr = readErr
			} else {
				dhcpErr = ErrGuestIPNotFound
			}
		}

		ip, err := b.guestAgentIP(ctx, node, vmid)
		if err == nil && strings.TrimSpace(ip) != "" {
			return strings.TrimSpace(ip), nil
		}
		if err != nil {
			qgaErr = err
		} else {
			qgaErr = ErrGuestIPNotFound
		}

		if attempts > 0 && i >= attempts-1 {
			break
		}
		if err := b.sleep(ctx, wait); err != nil {
			return "", err
		}
		wait = nextBackoff(wait, maxWait)
	}

	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	return "", fmt.Errorf("%w: dhcp=%v qemu-guest-agent=%v", ErrGuestIPNotFound, dhcpErr, qgaErr)
}

// VMConfig retrieves the raw VM configuration map.
func (b *APIBackend) VMConfig(ctx context.Context, vmid VMID) (map[string]string, error) {
	node, err := b.ensureNode(ctx)
	if err != nil {
		return nil, err
	}

	endpoint := fmt.Sprintf("/nodes/%s/qemu/%d/config", node, vmid)
	data, err := b.doGet(ctx, endpoint)
	if err != nil {
		if isAPIVMNotFound(err) {
			return nil, fmt.Errorf("%w: %v", ErrVMNotFound, err)
		}
		return nil, err
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse vm config: %w", err)
	}

	out := make(map[string]string, len(raw))
	for key, value := range raw {
		if value == nil {
			continue
		}
		switch v := value.(type) {
		case string:
			out[key] = v
		case float64:
			out[key] = strconv.FormatFloat(v, 'f', -1, 64)
		case bool:
			out[key] = strconv.FormatBool(v)
		default:
			out[key] = fmt.Sprint(v)
		}
	}
	return out, nil
}

// CreateVolume creates a new volume.
// ABOUTME: Creates a disk volume in the specified storage with the given size.
// Returns the volume ID (e.g., "local-zfs:vm-0-disk-0").
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
// ABOUTME: Attaches the specified volume to the VM at the given slot (e.g., "scsi1").
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
// ABOUTME: Detaches the volume from the VM slot. The volume is not deleted.
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
// ABOUTME: Permanently deletes the volume from storage.
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

// VolumeInfo retrieves volume metadata.
func (b *APIBackend) VolumeInfo(ctx context.Context, volumeID string) (VolumeInfo, error) {
	volumeID = strings.TrimSpace(volumeID)
	if volumeID == "" {
		return VolumeInfo{}, fmt.Errorf("volume id is required")
	}
	storage := volumeStorage(volumeID)
	if storage == "" {
		return VolumeInfo{}, fmt.Errorf("invalid volume id format: %s", volumeID)
	}

	node, err := b.ensureNode(ctx)
	if err != nil {
		return VolumeInfo{}, err
	}

	endpoint := fmt.Sprintf("/nodes/%s/storage/%s/content/%s", node, storage, url.PathEscape(volumeID))
	data, err := b.doGet(ctx, endpoint)
	if err != nil {
		if isMissingVolumeError(err) {
			return VolumeInfo{}, fmt.Errorf("%w: %v", ErrVolumeNotFound, err)
		}
		return VolumeInfo{}, err
	}

	var result struct {
		VolID string `json:"volid"`
		Path  string `json:"path"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return VolumeInfo{}, fmt.Errorf("parse volume info: %w", err)
	}
	info := VolumeInfo{
		VolumeID: volumeID,
		Storage:  storage,
		Path:     result.Path,
	}
	if strings.TrimSpace(result.VolID) != "" {
		info.VolumeID = strings.TrimSpace(result.VolID)
	}
	return info, nil
}

// ValidateTemplate checks if a template VM is suitable for provisioning.
// ABOUTME: Returns an error if the template doesn't exist or doesn't have qemu-guest-agent enabled.
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

	enabled, err := agentConfigEnabled(agentConfig)
	if err != nil {
		return fmt.Errorf("template VM %d has invalid agent config: %w", template, err)
	}
	if !enabled {
		return fmt.Errorf("template VM %d has qemu-guest-agent explicitly disabled", template)
	}

	stringCfg := make(map[string]string, len(config))
	for k, v := range config {
		if s, ok := v.(string); ok {
			stringCfg[k] = s
		}
	}
	if !hasCloudInitDrive(stringCfg) {
		return fmt.Errorf("template VM %d does not have a cloud-init drive configured", template)
	}

	return nil
}

// HTTP client methods

func (b *APIBackend) client() *http.Client {
	if b.HTTPClient != nil {
		return b.HTTPClient
	}

	// Create default client with insecure TLS skip for self-signed certs.
	// Prefer using NewAPIBackend to get a client configured from settings.
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

func parseTaskUPID(data []byte) string {
	if len(data) == 0 {
		return ""
	}
	var upid string
	if err := json.Unmarshal(data, &upid); err != nil {
		return ""
	}
	upid = strings.TrimSpace(upid)
	if strings.HasPrefix(upid, "UPID:") {
		return upid
	}
	return ""
}

func (b *APIBackend) waitForTask(ctx context.Context, node, upid string) error {
	node = strings.TrimSpace(node)
	upid = strings.TrimSpace(upid)
	if node == "" || upid == "" {
		return nil
	}
	wait := 500 * time.Millisecond
	maxWait := 5 * time.Second

	for {
		endpoint := fmt.Sprintf("/nodes/%s/tasks/%s/status", node, url.PathEscape(upid))
		data, err := b.doGet(ctx, endpoint)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		var status struct {
			Status     string `json:"status"`
			ExitStatus string `json:"exitstatus"`
		}
		if err := json.Unmarshal(data, &status); err != nil {
			return fmt.Errorf("parse task status: %w", err)
		}
		if strings.EqualFold(status.Status, "stopped") {
			if status.ExitStatus != "" && !strings.EqualFold(status.ExitStatus, "OK") {
				return fmt.Errorf("task %s failed: exitstatus=%s", upid, status.ExitStatus)
			}
			return nil
		}
		if err := b.sleep(ctx, wait); err != nil {
			return err
		}
		wait = nextBackoff(wait, maxWait)
	}
}

func (b *APIBackend) pollGuestAgentIP(ctx context.Context, node string, vmid VMID) (string, error) {
	attempts := 0
	if _, ok := ctx.Deadline(); !ok {
		// Avoid infinite polling with a background context.
		attempts = 30
	}
	wait := 500 * time.Millisecond
	maxWait := 10 * time.Second

	var lastErr error
	for i := 0; ; i++ {
		ip, err := b.guestAgentIP(ctx, node, vmid)
		if err == nil && ip != "" {
			return ip, nil
		}
		if err != nil {
			lastErr = err
		} else {
			lastErr = ErrGuestIPNotFound
		}
		if attempts > 0 && i >= attempts-1 {
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
		if isGuestAgentNotRunningError(err) {
			return "", ErrGuestIPNotFound
		}
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
		if ip != "" {
			return ip, nil
		}
	}
	for _, ip := range ips {
		if ip != nil && ip.IsPrivate() {
			return ip.String(), nil
		}
	}
	return ips[0].String(), nil
}

func isGuestAgentNotRunningError(err error) bool {
	if err == nil {
		return false
	}
	// Proxmox returns status=500 for agent calls when the GA socket is present but not responding.
	// Treat this as "not found" so callers can fall back to other IP discovery mechanisms.
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "qemu guest agent is not running") || strings.Contains(msg, "guest agent is not running")
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

	// Proxmox config values can be strings, numbers, etc. We only care about "net*" keys.
	var config map[string]interface{}
	if err := json.Unmarshal(data, &config); err != nil {
		return "", fmt.Errorf("parse vm config: %w", err)
	}

	macs := []string{}
	for k, v := range config {
		if strings.HasPrefix(k, "net") {
			s, ok := v.(string)
			if !ok {
				continue
			}
			if mac := extractMAC(s); mac != "" {
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

func (b *APIBackend) ensureRootDiskSize(ctx context.Context, node string, vmid VMID, disk string, targetGB int) error {
	if targetGB <= 0 {
		return nil
	}

	endpoint := fmt.Sprintf("/nodes/%s/qemu/%d/config", node, vmid)
	data, err := b.doGet(ctx, endpoint)
	if err != nil {
		return err
	}

	var config map[string]interface{}
	if err := json.Unmarshal(data, &config); err != nil {
		return fmt.Errorf("parse vm config: %w", err)
	}

	stringCfg := make(map[string]string, len(config))
	for k, v := range config {
		if s, ok := v.(string); ok {
			stringCfg[k] = s
		}
	}

	disk = strings.TrimSpace(disk)
	if disk == "" {
		disk = detectRootDisk(stringCfg)
	}
	if disk == "" {
		return fmt.Errorf("unable to determine root disk for vm %d", vmid)
	}
	diskCfg, ok := stringCfg[disk]
	if !ok {
		return fmt.Errorf("root disk %q missing from vm %d config", disk, vmid)
	}
	token := extractDiskSizeToken(diskCfg)
	if token == "" {
		return fmt.Errorf("unable to determine current size for vm %d disk %q", vmid, disk)
	}
	currentGB, err := parseSizeGB(token)
	if err != nil {
		return fmt.Errorf("parse vm %d disk %q size %q: %w", vmid, disk, token, err)
	}
	delta := resizeDeltaGB(currentGB, targetGB)
	if delta <= 0 {
		return nil
	}

	params := url.Values{}
	params.Set("disk", disk)
	params.Set("size", fmt.Sprintf("+%dG", delta))
	resizeEndpoint := fmt.Sprintf("/nodes/%s/qemu/%d/resize", node, vmid)
	task, err := b.doPut(ctx, resizeEndpoint, params)
	if err != nil {
		return err
	}
	if upid := parseTaskUPID(task); upid != "" {
		if err := b.waitForTask(ctx, node, upid); err != nil {
			return err
		}
	}
	return nil
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
// ABOUTME: The baseURL should be the Proxmox API URL (e.g., "https://localhost:8006").
// The apiToken must be in format "USER@REALM!TOKENID=TOKEN". If node is empty, it will be auto-detected.
// TLS verification can be controlled with tlsInsecure or a custom CA bundle path.
func NewAPIBackend(baseURL, apiToken, node, agentCIDR string, timeout time.Duration, tlsInsecure bool, tlsCAPath string) (*APIBackend, error) {
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

	httpClient, err := newAPIHTTPClient(timeout, tlsInsecure, tlsCAPath)
	if err != nil {
		return nil, err
	}

	return &APIBackend{
		HTTPClient:     httpClient,
		BaseURL:        baseURL,
		APIToken:       apiToken,
		APITokenID:     tokenID,
		APITokenValue:  tokenValue,
		Node:           node,
		AgentCIDR:      agentCIDR,
		CommandTimeout: timeout,
	}, nil
}

func newAPIHTTPClient(timeout time.Duration, tlsInsecure bool, caPath string) (*http.Client, error) {
	caPath = strings.TrimSpace(caPath)
	if tlsInsecure && caPath != "" {
		return nil, fmt.Errorf("proxmox_tls_insecure cannot be true when proxmox_tls_ca_path is set")
	}

	tlsConfig := &tls.Config{
		InsecureSkipVerify: tlsInsecure,
	}

	if caPath != "" {
		caPEM, err := os.ReadFile(caPath)
		if err != nil {
			return nil, fmt.Errorf("read proxmox_tls_ca_path %q: %w", caPath, err)
		}
		pool, err := x509.SystemCertPool()
		if err != nil || pool == nil {
			pool = x509.NewCertPool()
		}
		if ok := pool.AppendCertsFromPEM(caPEM); !ok {
			return nil, fmt.Errorf("proxmox_tls_ca_path %q did not contain any certificates", caPath)
		}
		tlsConfig.RootCAs = pool
	}

	clientTimeout := timeout
	if clientTimeout <= 0 {
		clientTimeout = 2 * time.Minute
	}

	return &http.Client{
		Timeout: clientTimeout,
		Transport: &http.Transport{
			TLSClientConfig: tlsConfig,
		},
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
