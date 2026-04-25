package sandbox

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// ProxmoxAPIConfig holds the connection details for the Proxmox API.
type ProxmoxAPIConfig struct {
	// URL is the Proxmox API base URL (e.g., "https://localhost:8006/api2/json").
	URL string
	// Token is the API token (e.g., "root@pam!token=uuid").
	Token string
	// Node is the Proxmox node name.
	Node string
	// Timeout is the HTTP request timeout.
	Timeout time.Duration
	// TLSInsecure skips TLS certificate verification.
	TLSInsecure bool
	// TLSCAPath is the path to the CA certificate for Proxmox API.
	TLSCAPath string
}

// LXCBackend manages LXC containers via the Proxmox REST API.
//
// ABOUTME: This backend creates and manages lightweight LXC containers for fast
// sandbox provisioning. LXC containers share the host kernel, providing near-native
// performance with much faster startup times (~2-5s) compared to full VMs.
//
// ABOUTME: The backend supports common container images (ubuntu:22.04, debian:12,
// alpine:3.19, etc.) and leverages Proxmox's built-in container image management.
type LXCBackend struct {
	apiURL     string
	apiToken   string
	node       string
	httpClient *http.Client
}

// NewLXCBackend creates a new LXC backend using the Proxmox REST API.
func NewLXCBackend(cfg ProxmoxAPIConfig) (*LXCBackend, error) {
	if cfg.URL == "" {
		return nil, errors.New("proxmox API URL is required for LXC backend")
	}
	if cfg.Token == "" {
		return nil, errors.New("proxmox API token is required for LXC backend")
	}
	if cfg.Node == "" {
		return nil, errors.New("proxmox node name is required for LXC backend")
	}
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	transport := &http.Transport{}
	if cfg.TLSInsecure {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	return &LXCBackend{
		apiURL:   strings.TrimRight(cfg.URL, "/"),
		apiToken: cfg.Token,
		node:     cfg.Node,
		httpClient: &http.Client{
			Timeout:   timeout,
			Transport: transport,
		},
	}, nil
}

func (b *LXCBackend) SandboxType() Type { return TypeLXC }

func (b *LXCBackend) Capabilities() Capabilities {
	return Capabilities{
		Snapshots:      true,
		Suspend:        true,
		WorkspaceMount: false, // LXC containers use mount points, not SCSI volumes
		Firewall:       true,
	}
}

func (b *LXCBackend) Create(ctx context.Context, cfg CreateConfig) error {
	if cfg.Image == "" {
		return fmt.Errorf("image is required for LXC backend (e.g., ubuntu:22.04)")
	}
	// Proxmox LXC create API expects a POST to /nodes/{node}/lxc
	// with JSON body containing the container configuration.
	body := map[string]any{
		"vmid":     cfg.ID,
		"hostname": cfg.Name,
		"ostemplate": resolveLXCImage(cfg.Image),
		"rootfs":   "local-lvm:vm-" + strconv.Itoa(cfg.ID) + "-disk-0",
		"password": cfg.Password,
		"ssh-public-keys": cfg.SSHPublicKey,
		"unprivileged": 1,
		"onboot":       0,
		"start":        0, // Don't auto-start; we'll start explicitly
	}
	if cfg.Cores > 0 {
		body["cores"] = cfg.Cores
	}
	if cfg.MemoryMB > 0 {
		body["memory"] = cfg.MemoryMB
	}
	if cfg.Bridge != "" {
		body["net0"] = fmt.Sprintf("name=eth0,bridge=%s,ip=dhcp", cfg.Bridge)
	} else {
		body["net0"] = "name=eth0,bridge=vmbr1,ip=dhcp"
	}
	if cfg.RootDiskGB > 0 {
		// Proxmox LXC rootfs size is specified as rootfs property suffix
		body["rootfs"] = fmt.Sprintf("local-lvm:vm-%d-disk-0,size=%dG", cfg.ID, cfg.RootDiskGB)
	}
	// Remove empty password
	if body["password"] == "" {
		delete(body, "password")
	}

	_, err := b.doRequest(ctx, http.MethodPost, "/nodes/"+b.node+"/lxc", body)
	if err != nil {
		return fmt.Errorf("create lxc container %d: %w", cfg.ID, err)
	}

	// Wait for the create task to complete (container creation is async in Proxmox).
	// The API returns a task UPID that we should wait for.
	// For now, we do a brief poll to confirm the container exists.
	return b.waitForContainer(ctx, cfg.ID, StatusStopped, 15*time.Second)
}

func (b *LXCBackend) Start(ctx context.Context, id int) error {
	_, err := b.doRequest(ctx, http.MethodPost, fmt.Sprintf("/nodes/%s/lxc/%d/status/start", b.node, id), nil)
	if err != nil {
		return fmt.Errorf("start lxc %d: %w", id, err)
	}
	return nil
}

func (b *LXCBackend) Stop(ctx context.Context, id int) error {
	_, err := b.doRequest(ctx, http.MethodPost, fmt.Sprintf("/nodes/%s/lxc/%d/status/stop", b.node, id), nil)
	if err != nil {
		return fmt.Errorf("stop lxc %d: %w", id, err)
	}
	return nil
}

func (b *LXCBackend) Suspend(ctx context.Context, id int) error {
	// LXC containers use "freeze" instead of "suspend"
	_, err := b.doRequest(ctx, http.MethodPost, fmt.Sprintf("/nodes/%s/lxc/%d/status/freeze", b.node, id), nil)
	if err != nil {
		return fmt.Errorf("freeze lxc %d: %w", id, err)
	}
	return nil
}

func (b *LXCBackend) Resume(ctx context.Context, id int) error {
	// LXC containers use "unfreeze" instead of "resume"
	_, err := b.doRequest(ctx, http.MethodPost, fmt.Sprintf("/nodes/%s/lxc/%d/status/unfreeze", b.node, id), nil)
	if err != nil {
		return fmt.Errorf("unfreeze lxc %d: %w", id, err)
	}
	return nil
}

func (b *LXCBackend) Destroy(ctx context.Context, id int) error {
	// Force destroy (stop if running first)
	_, err := b.doRequest(ctx, http.MethodDelete, fmt.Sprintf("/nodes/%s/lxc/%d?force=1", b.node, id), nil)
	if err != nil {
		return fmt.Errorf("destroy lxc %d: %w", id, err)
	}
	return nil
}

func (b *LXCBackend) Status(ctx context.Context, id int) (Status, error) {
	data, err := b.doRequest(ctx, http.MethodGet, fmt.Sprintf("/nodes/%s/lxc/%d/status/current", b.node, id), nil)
	if err != nil {
		return StatusUnknown, fmt.Errorf("lxc status %d: %w", id, err)
	}
	statusStr, _ := data["status"].(string)
	switch statusStr {
	case "running":
		return StatusRunning, nil
	case "stopped":
		return StatusStopped, nil
	default:
		return StatusUnknown, nil
	}
}

func (b *LXCBackend) GuestIP(ctx context.Context, id int) (string, error) {
	data, err := b.doRequest(ctx, http.MethodGet, fmt.Sprintf("/nodes/%s/lxc/%d/interfaces", b.node, id), nil)
	if err != nil {
		return "", fmt.Errorf("lxc guest ip %d: %w", id, err)
	}
	// The interfaces API returns a list of network interfaces
	result, ok := data["result"].([]any)
	if !ok {
		return "", ErrGuestIPNotFound
	}
	for _, iface := range result {
		ifaceMap, ok := iface.(map[string]any)
		if !ok {
			continue
		}
		name, _ := ifaceMap["name"].(string)
		if name == "lo" {
			continue
		}
		// Look for IPv4 addresses
		if addrs, ok := ifaceMap["ip-addresses"].([]any); ok {
			for _, addr := range addrs {
				addrMap, ok := addr.(map[string]any)
				if !ok {
					continue
				}
				ipType, _ := addrMap["ip-address-type"].(string)
				if ipType != "v4" {
					continue
				}
				ip, _ := addrMap["ip-address"].(string)
				if ip != "" && !strings.HasPrefix(ip, "127.") {
					return ip, nil
				}
			}
		}
	}
	return "", ErrGuestIPNotFound
}

func (b *LXCBackend) List(ctx context.Context) ([]ContainerSummary, error) {
	data, err := b.doRequest(ctx, http.MethodGet, fmt.Sprintf("/nodes/%s/lxc", b.node), nil)
	if err != nil {
		return nil, fmt.Errorf("list lxc: %w", err)
	}
	result, ok := data["result"].([]any)
	if !ok {
		return nil, nil
	}
	summaries := make([]ContainerSummary, 0, len(result))
	for _, item := range result {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		vmid, _ := toInt(m["vmid"])
		name, _ := m["name"].(string)
		statusStr, _ := m["status"].(string)
		var status Status
		switch statusStr {
		case "running":
			status = StatusRunning
		case "stopped":
			status = StatusStopped
		default:
			status = StatusUnknown
		}
		summaries = append(summaries, ContainerSummary{
			ID:     vmid,
			Name:   name,
			Status: status,
			Type:   TypeLXC,
		})
	}
	return summaries, nil
}

func (b *LXCBackend) CurrentStats(ctx context.Context, id int) (ContainerStats, error) {
	data, err := b.doRequest(ctx, http.MethodGet, fmt.Sprintf("/nodes/%s/lxc/%d/status/current", b.node, id), nil)
	if err != nil {
		return ContainerStats{}, err
	}
	cpu, _ := toFloat64(data["cpu"])
	return ContainerStats{CPUUsage: cpu}, nil
}

func (b *LXCBackend) ValidateTemplate(ctx context.Context, templateOrImage string) error {
	if templateOrImage == "" {
		return errors.New("image is required for LXC backend")
	}
	// Check if the image is available in Proxmox's local appliance list or storage.
	// We check the available container templates on the node.
	data, err := b.doRequest(ctx, http.MethodGet, fmt.Sprintf("/nodes/%s/aplinfo", b.node), nil)
	if err != nil {
		// If we can't query the appliance list, assume the image is valid
		// and let creation fail if it's not actually available.
		return nil
	}
	result, ok := data["result"].([]any)
	if !ok {
		return nil
	}
	resolved := resolveLXCImage(templateOrImage)
	for _, item := range result {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		tmpl, _ := m["template"].(string)
		if strings.Contains(tmpl, resolved) || strings.Contains(tmpl, templateOrImage) {
			return nil
		}
	}
	// Image not found in local storage, but it might be downloadable.
	// Return nil to allow the create operation to attempt a download.
	return nil
}

func (b *LXCBackend) SnapshotCreate(ctx context.Context, id int, name string) error {
	body := map[string]any{
		"snapname":    name,
		"description": "AgentLab auto-snapshot",
	}
	_, err := b.doRequest(ctx, http.MethodPost, fmt.Sprintf("/nodes/%s/lxc/%d/snapshot", b.node, id), body)
	return err
}

func (b *LXCBackend) SnapshotRollback(ctx context.Context, id int, name string) error {
	_, err := b.doRequest(ctx, http.MethodPost, fmt.Sprintf("/nodes/%s/lxc/%d/snapshot/%s/rollback", b.node, id, name), nil)
	return err
}

func (b *LXCBackend) SnapshotDelete(ctx context.Context, id int, name string) error {
	_, err := b.doRequest(ctx, http.MethodDelete, fmt.Sprintf("/nodes/%s/lxc/%d/snapshot/%s", b.node, id, name), nil)
	return err
}

func (b *LXCBackend) SnapshotList(ctx context.Context, id int) ([]Snapshot, error) {
	data, err := b.doRequest(ctx, http.MethodGet, fmt.Sprintf("/nodes/%s/lxc/%d/snapshot", b.node, id), nil)
	if err != nil {
		return nil, err
	}
	result, ok := data["result"].([]any)
	if !ok {
		return nil, nil
	}
	snaps := make([]Snapshot, 0, len(result))
	for _, item := range result {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		name, _ := m["name"].(string)
		// Skip the "NOW" pseudo-snapshot
		if name == "current" || name == "NOW" {
			continue
		}
		desc, _ := m["description"].(string)
		var createdAt time.Time
		if ts, ok := m["snaptime"].(float64); ok {
			createdAt = time.Unix(int64(ts), 0)
		}
		snaps = append(snaps, Snapshot{
			Name:        name,
			Description: desc,
			CreatedAt:   createdAt,
		})
	}
	return snaps, nil
}

func (b *LXCBackend) doRequest(ctx context.Context, method, path string, body map[string]any) (map[string]any, error) {
	url := b.apiURL + path
	var bodyReader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request body: %w", err)
		}
		bodyReader = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "PVEAPIToken="+b.apiToken)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := b.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	// Proxmox API wraps responses in {"data": ...}
	var result map[string]any
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w (body: %s)", err, string(respBody))
	}

	if resp.StatusCode >= 400 {
		msg, _ := result["errors"].(string)
		if msg == "" {
			// Proxmox sometimes returns errors as a map
			if errs, ok := result["errors"].(map[string]any); ok && len(errs) > 0 {
				parts := make([]string, 0, len(errs))
				for k, v := range errs {
					parts = append(parts, fmt.Sprintf("%s: %v", k, v))
				}
				msg = strings.Join(parts, "; ")
			}
		}
		if msg == "" {
			msg = fmt.Sprintf("HTTP %d", resp.StatusCode)
		}
		if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusInternalServerError {
			return nil, fmt.Errorf("%w: %s", ErrContainerNotFound, msg)
		}
		return nil, fmt.Errorf("proxmox api error: %s", msg)
	}

	return result, nil
}

func (b *LXCBackend) waitForContainer(ctx context.Context, id int, expected Status, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		status, err := b.Status(ctx, id)
		if err != nil {
			// Container might not be fully created yet
			if errors.Is(err, ErrContainerNotFound) {
				time.Sleep(500 * time.Millisecond)
				continue
			}
			return err
		}
		if status == expected {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("timeout waiting for lxc %d to reach status %s", id, expected)
}

// resolveLXCImage maps a user-friendly image name to a Proxmox storage path.
//
// Common aliases:
//   - "ubuntu:22.04" → "local:vztmpl/ubuntu-22.04-standard_22.04-1_amd64.tar.zst"
//   - "debian:12" → "local:vztmpl/debian-12-standard_12.2-1_amd64.tar.zst"
//   - "alpine:3.19" → "local:vztmpl/alpine-3.19-default_20231218_amd64.tar.xz"
//
// If the image already looks like a storage path (contains ":"), it's returned as-is.
func resolveLXCImage(image string) string {
	if strings.Contains(image, ":vztmpl/") || strings.Contains(image, ".tar") {
		return image
	}
	parts := strings.SplitN(image, ":", 2)
	if len(parts) != 2 {
		return image
	}
	dist := strings.ToLower(parts[0])
	release := parts[1]
	switch dist {
	case "ubuntu":
		return fmt.Sprintf("local:vztmpl/ubuntu-%s-standard_%s-1_amd64.tar.zst", release, release)
	case "debian":
		return fmt.Sprintf("local:vztmpl/debian-%s-standard_%s.2-1_amd64.tar.zst", release, release)
	case "alpine":
		return fmt.Sprintf("local:vztmpl/alpine-%s-default_20231218_amd64.tar.xz", release)
	case "centos":
		return fmt.Sprintf("local:vztmpl/centos-%s-default_%s_amd64.tar.xz", release, release)
	case "fedora":
		return fmt.Sprintf("local:vztmpl/fedora-%s-default_%s_amd64.tar.xz", release, release)
	case "archlinux":
		return fmt.Sprintf("local:vztmpl/archlinux-base_%s_amd64.tar.zst", release)
	default:
		return image
	}
}

// ErrContainerNotFound indicates an LXC container was not found in Proxmox.
var ErrContainerNotFound = errors.New("lxc container not found")

// ErrGuestIPNotFound indicates the guest IP could not be determined.
var ErrGuestIPNotFound = errors.New("guest ip not found")

func toInt(v any) (int, bool) {
	switch n := v.(type) {
	case float64:
		return int(n), true
	case json.Number:
		i, err := n.Int64()
		if err != nil {
			return 0, false
		}
		return int(i), true
	case string:
		i, err := strconv.Atoi(n)
		if err != nil {
			return 0, false
		}
		return i, true
	default:
		return 0, false
	}
}

func toFloat64(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case json.Number:
		f, err := n.Float64()
		if err != nil {
			return 0, false
		}
		return f, true
	case string:
		f, err := strconv.ParseFloat(n, 64)
		if err != nil {
			return 0, false
		}
		return f, true
	default:
		return 0, false
	}
}

// Compile-time check that LXCBackend implements Backend.
var _ Backend = (*LXCBackend)(nil)
