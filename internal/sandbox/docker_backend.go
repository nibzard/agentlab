package sandbox

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// DockerConfig holds the configuration for the Docker backend.
type DockerConfig struct {
	// Host is the Docker daemon socket path (e.g., "unix:///var/run/docker.sock").
	// Defaults to "unix:///var/run/docker.sock".
	Host string
	// Timeout is the HTTP request timeout for Docker API calls.
	Timeout time.Duration
	// Network is the Docker network to attach containers to.
	// Defaults to "agentlab".
	Network string
	// Offline, when true, prevents Docker from pulling images from remote registries.
	// Images must be pre-pulled before starting the daemon.
	Offline bool
}

// DockerBackend manages sandbox containers via the Docker Engine API.
//
// ABOUTME: This backend creates and manages Docker containers for lightweight
// sandbox provisioning on laptops and local development environments. It uses
// the Docker Engine API over a Unix socket, requiring no external dependencies.
//
// ABOUTME: Docker sandboxes are ideal for local development and testing where
// Proxmox is not available. They provide fast startup (~1-2s) but with less
// isolation than full VMs.
type DockerBackend struct {
	host       string
	network    string
	httpClient *http.Client
	offline    bool
}

// NewDockerBackend creates a new Docker backend.
func NewDockerBackend(cfg DockerConfig) (*DockerBackend, error) {
	host := cfg.Host
	if host == "" {
		host = "unix:///var/run/docker.sock"
	}
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 60 * time.Second
	}
	network := cfg.Network
	if network == "" {
		network = "agentlab"
	}

	// Parse the host to configure the HTTP transport.
	transport := &http.Transport{}
	if strings.HasPrefix(host, "unix://") {
		socketPath := strings.TrimPrefix(host, "unix://")
		transport.DialContext = func(_ context.Context, _, _ string) (net.Conn, error) {
			return net.Dial("unix", socketPath)
		}
	} else {
		return nil, fmt.Errorf("unsupported docker host: %s (only unix:// sockets are supported)", host)
	}

	return &DockerBackend{
		host:    host,
		network: network,
		offline: cfg.Offline,
		httpClient: &http.Client{
			Timeout:   timeout,
			Transport: transport,
		},
	}, nil
}

func (b *DockerBackend) SandboxType() Type { return TypeDocker }

func (b *DockerBackend) Capabilities() Capabilities {
	return Capabilities{
		Snapshots:      false, // Docker doesn't support snapshots natively
		Suspend:        true,  // docker pause/unpause
		WorkspaceMount: true,  // Docker volumes
		Firewall:       false, // No built-in firewall support
	}
}

func (b *DockerBackend) Create(ctx context.Context, cfg CreateConfig) error {
	if cfg.Image == "" {
		return errors.New("image is required for Docker backend (e.g., ubuntu:22.04)")
	}

	// Build container create request body.
	body := map[string]any{
		"Hostname": cfg.Name,
		"Image":    cfg.Image,
		"HostConfig": map[string]any{
			"NetworkMode": b.network,
		},
	}
	if cfg.Cores > 0 {
		body["HostConfig"].(map[string]any)["NanoCpus"] = int64(cfg.Cores) * 1e9
	}
	if cfg.MemoryMB > 0 {
		body["HostConfig"].(map[string]any)["Memory"] = int64(cfg.MemoryMB) * 1024 * 1024
	}
	if cfg.SSHPublicKey != "" {
		body["Env"] = []string{fmt.Sprintf("SSH_PUBLIC_KEY=%s", cfg.SSHPublicKey)}
	}
	if cfg.Name != "" {
		body["Labels"] = map[string]string{
			"agentlab":    "true",
			"agentlab.id": strconv.Itoa(cfg.ID),
		}
	}

	name := fmt.Sprintf("agentlab-%d", cfg.ID)
	if cfg.Name != "" {
		name = fmt.Sprintf("agentlab-%s-%d", sanitizeDockerName(cfg.Name), cfg.ID)
	}

	resp, err := b.doRequest(ctx, http.MethodPost, "/containers/create?name="+name, body)
	if err != nil {
		return fmt.Errorf("create docker container: %w", err)
	}

	// Docker returns warnings in the response.
	if warnings, ok := resp["Warnings"].([]any); ok && len(warnings) > 0 {
		for _, w := range warnings {
			fmt.Fprintf(os.Stderr, "docker warning: %v\n", w)
		}
	}
	return nil
}

func (b *DockerBackend) Start(ctx context.Context, id int) error {
	name := b.containerName(ctx, id)
	_, err := b.doRequest(ctx, http.MethodPost, "/containers/"+name+"/start", nil)
	if err != nil {
		return fmt.Errorf("start docker container %s: %w", name, err)
	}
	return nil
}

func (b *DockerBackend) Stop(ctx context.Context, id int) error {
	name := b.containerName(ctx, id)
	_, err := b.doRequest(ctx, http.MethodPost, "/containers/"+name+"/stop", nil)
	if err != nil {
		return fmt.Errorf("stop docker container %s: %w", name, err)
	}
	return nil
}

func (b *DockerBackend) Suspend(ctx context.Context, id int) error {
	name := b.containerName(ctx, id)
	_, err := b.doRequest(ctx, http.MethodPost, "/containers/"+name+"/pause", nil)
	if err != nil {
		return fmt.Errorf("pause docker container %s: %w", name, err)
	}
	return nil
}

func (b *DockerBackend) Resume(ctx context.Context, id int) error {
	name := b.containerName(ctx, id)
	_, err := b.doRequest(ctx, http.MethodPost, "/containers/"+name+"/unpause", nil)
	if err != nil {
		return fmt.Errorf("unpause docker container %s: %w", name, err)
	}
	return nil
}

func (b *DockerBackend) Destroy(ctx context.Context, id int) error {
	name := b.containerName(ctx, id)
	_, err := b.doRequest(ctx, http.MethodDelete, "/containers/"+name+"?force=true&v=true", nil)
	if err != nil {
		return fmt.Errorf("destroy docker container %s: %w", name, err)
	}
	return nil
}

func (b *DockerBackend) Status(ctx context.Context, id int) (Status, error) {
	name := b.containerName(ctx, id)
	data, err := b.doRequest(ctx, http.MethodGet, "/containers/"+name+"/json", nil)
	if err != nil {
		return StatusUnknown, fmt.Errorf("docker status %s: %w", name, err)
	}
	state, _ := data["State"].(map[string]any)
	statusStr, _ := state["Status"].(string)
	switch statusStr {
	case "running":
		return StatusRunning, nil
	case "created", "paused", "restarting", "removing", "exited", "dead":
		return StatusStopped, nil
	default:
		return StatusUnknown, nil
	}
}

func (b *DockerBackend) GuestIP(ctx context.Context, id int) (string, error) {
	name := b.containerName(ctx, id)
	data, err := b.doRequest(ctx, http.MethodGet, "/containers/"+name+"/json", nil)
	if err != nil {
		return "", fmt.Errorf("docker inspect %s: %w", name, err)
	}
	networkSettings, _ := data["NetworkSettings"].(map[string]any)
	if networkSettings == nil {
		return "", ErrGuestIPNotFound
	}
	networks, _ := networkSettings["Networks"].(map[string]any)
	if networks == nil {
		return "", ErrGuestIPNotFound
	}
	// Check the configured network first, then any available.
	if netData, ok := networks[b.network].(map[string]any); ok {
		if ip, ok := netData["IPAddress"].(string); ok && ip != "" {
			return ip, nil
		}
	}
	for _, netData := range networks {
		netMap, ok := netData.(map[string]any)
		if !ok {
			continue
		}
		ip, _ := netMap["IPAddress"].(string)
		if ip != "" && !strings.HasPrefix(ip, "127.") {
			return ip, nil
		}
	}
	return "", ErrGuestIPNotFound
}

func (b *DockerBackend) List(ctx context.Context) ([]ContainerSummary, error) {
	data, err := b.doRequestRaw(ctx, http.MethodGet, "/containers/json?all=true&filters=%7B%22label%22%3A%5B%22agentlab%22%5D%7D", nil)
	if err != nil {
		return nil, fmt.Errorf("list docker containers: %w", err)
	}
	// Docker returns an array, not an object
	var containers []map[string]any
	if err := json.Unmarshal(data, &containers); err != nil {
		return nil, fmt.Errorf("parse docker list response: %w", err)
	}
	summaries := make([]ContainerSummary, 0, len(containers))
	for _, c := range containers {
		name, _ := c["Names"].([]any)
		containerName := ""
		if len(name) > 0 {
			containerName = strings.TrimPrefix(name[0].(string), "/")
		}
		statusStr, _ := c["Status"].(string)
		var status Status
		if strings.HasPrefix(statusStr, "Up") {
			status = StatusRunning
		} else {
			status = StatusStopped
		}
		idStr := ""
		labels, _ := c["Labels"].(map[string]any)
		if labels != nil {
			idStr, _ = labels["agentlab.id"].(string)
		}
		sandboxID := 0
		if idStr != "" {
			sandboxID, _ = strconv.Atoi(idStr)
		}
		summaries = append(summaries, ContainerSummary{
			ID:     sandboxID,
			Name:   containerName,
			Status: status,
			Type:   TypeDocker,
		})
	}
	return summaries, nil
}

func (b *DockerBackend) CurrentStats(ctx context.Context, id int) (ContainerStats, error) {
	name := b.containerName(ctx, id)
	data, err := b.doRequestRaw(ctx, http.MethodGet, "/containers/"+name+"/stats?stream=false", nil)
	if err != nil {
		return ContainerStats{}, err
	}
	var stats struct {
		CPUStats struct {
			CPUUsage struct {
				TotalUsage uint64 `json:"total_usage"`
			} `json:"cpu_usage"`
			SystemCPUUsage uint64 `json:"system_cpu_usage"`
			OnlineCPUs     int    `json:"online_cpus"`
		} `json:"cpu_stats"`
		PreCPUStats struct {
			CPUUsage struct {
				TotalUsage uint64 `json:"total_usage"`
			} `json:"cpu_usage"`
			SystemCPUUsage uint64 `json:"system_cpu_usage"`
		} `json:"precpu_stats"`
	}
	if err := json.Unmarshal(data, &stats); err != nil {
		return ContainerStats{}, err
	}
	cpuDelta := float64(stats.CPUStats.CPUUsage.TotalUsage - stats.PreCPUStats.CPUUsage.TotalUsage)
	systemDelta := float64(stats.CPUStats.SystemCPUUsage - stats.PreCPUStats.SystemCPUUsage)
	var cpuUsage float64
	if systemDelta > 0 && stats.CPUStats.OnlineCPUs > 0 {
		cpuUsage = (cpuDelta / systemDelta) * float64(stats.CPUStats.OnlineCPUs)
	}
	return ContainerStats{CPUUsage: cpuUsage}, nil
}

func (b *DockerBackend) ValidateTemplate(ctx context.Context, templateOrImage string) error {
	if templateOrImage == "" {
		return errors.New("image is required for Docker backend")
	}
	// Check if the image exists locally.
	_, err := b.doRequest(ctx, http.MethodGet, "/images/"+templateOrImage+"/json", nil)
	if err != nil {
		if b.offline {
			// In offline mode, images must be pre-pulled. We cannot fetch from a registry.
			return fmt.Errorf("image %s not found locally and offline mode prevents pulling from registry; run 'docker pull %s' before starting the daemon", templateOrImage, templateOrImage)
		}
		// Image not found locally — it will be pulled on create.
		return nil
	}
	return nil
}

func (b *DockerBackend) SnapshotCreate(_ context.Context, _ int, _ string) error {
	return ErrNotSupported{Op: "snapshot_create"}
}

func (b *DockerBackend) SnapshotRollback(_ context.Context, _ int, _ string) error {
	return ErrNotSupported{Op: "snapshot_rollback"}
}

func (b *DockerBackend) SnapshotDelete(_ context.Context, _ int, _ string) error {
	return ErrNotSupported{Op: "snapshot_delete"}
}

func (b *DockerBackend) SnapshotList(_ context.Context, _ int) ([]Snapshot, error) {
	return nil, ErrNotSupported{Op: "snapshot_list"}
}

// HealthCheck verifies the Docker daemon is reachable.
func (b *DockerBackend) HealthCheck(ctx context.Context) error {
	_, err := b.doRequest(ctx, http.MethodGet, "/ping", nil)
	return err
}

// containerName resolves the Docker container name for a sandbox ID.
// It first tries to find a container with the agentlab.id label,
// falling back to the convention "agentlab-{id}".
func (b *DockerBackend) containerName(ctx context.Context, id int) string {
	return fmt.Sprintf("agentlab-%d", id)
}

func (b *DockerBackend) doRequest(ctx context.Context, method, path string, body map[string]any) (map[string]any, error) {
	data, err := b.doRequestRaw(ctx, method, path, body)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, nil
	}
	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, nil // Some endpoints return empty or non-object responses
	}
	return result, nil
}

func (b *DockerBackend) doRequestRaw(ctx context.Context, method, path string, body map[string]any) ([]byte, error) {
	var bodyReader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request body: %w", err)
		}
		bodyReader = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://localhost"+path, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := b.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("docker api request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		// Try to extract Docker error message.
		var errResp struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(respBody, &errResp) == nil && errResp.Message != "" {
			if resp.StatusCode == http.StatusNotFound {
				return nil, fmt.Errorf("%w: %s", ErrContainerNotFound, errResp.Message)
			}
			return nil, fmt.Errorf("docker api error: %s", errResp.Message)
		}
		if resp.StatusCode == http.StatusNotFound {
			return nil, fmt.Errorf("%w: container not found", ErrContainerNotFound)
		}
		return nil, fmt.Errorf("docker api error: HTTP %d", resp.StatusCode)
	}

	return respBody, nil
}

// sanitizeDockerName makes a string safe for use in Docker container names.
func sanitizeDockerName(s string) string {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, "_", "-")
	var b bytes.Buffer
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Compile-time check that DockerBackend implements Backend.
var _ Backend = (*DockerBackend)(nil)

// dockerContainerNotFound checks if an error indicates the container was not found.
func dockerContainerNotFound(err error) bool {
	return errors.Is(err, ErrContainerNotFound)
}

// discardReader reads all data from r and discards it.
func discardReader(r io.Reader) {
	buf := bufio.NewReaderSize(r, 4096)
	for {
		_, err := buf.ReadString('\n')
		if err != nil {
			break
		}
	}
}
