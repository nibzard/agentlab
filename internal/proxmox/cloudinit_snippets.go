// ABOUTME: This file provides cloud-init snippet management for Proxmox VMs.
// Snippets are used to inject SSH keys, bootstrap tokens, and controller configuration
// into VMs during the provisioning process.
package proxmox

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	// defaultSnippetStorage is the default Proxmox storage for snippets.
	defaultSnippetStorage = "local"
	// defaultSnippetDir is the default filesystem directory for snippet files.
	defaultSnippetDir = "/var/lib/vz/snippets"
)

// SnippetInput describes the minimal data needed to build a cloud-init snippet.
type SnippetInput struct {
	VMID           VMID   // Target VM identifier
	Hostname       string // VM hostname (defaults to "sandbox-{vmid}" if empty)
	SSHPublicKey   string // SSH public key for agent user (required)
	BootstrapToken string // Authentication token for bootstrap API (required)
	ControllerURL  string // URL of the controller API endpoint (required)
}

// CloudInitSnippet represents a stored snippet file and its Proxmox reference.
type CloudInitSnippet struct {
	VMID        VMID   // Associated VM identifier
	Filename    string // Base filename of the snippet
	FullPath    string // Absolute filesystem path to the snippet file
	Storage     string // Proxmox storage name (e.g., "local")
	StoragePath string // Proxmox storage path (e.g., "local:snippets/agentlab-1000-abc123.yaml")
}

// SnippetStore manages cloud-init snippet files for Proxmox.
// ABOUTME: Snippets are stored as YAML files in Proxmox's snippets directory and
// referenced during VM cloning via the cicustom parameter.
type SnippetStore struct {
	Storage string    // Proxmox storage name for snippets (defaults to "local")
	Dir     string    // Filesystem directory for snippet files (defaults to "/var/lib/vz/snippets")
	Rand    io.Reader // Random source for unique filename generation (defaults to crypto/rand)
}

// Create writes a cloud-init user-data snippet and returns its storage reference.
// ABOUTME: The snippet configures SSH access, bootstrap credentials, and controller URL.
// Returns an error if validation fails or file creation encounters issues.
func (s SnippetStore) Create(input SnippetInput) (CloudInitSnippet, error) {
	if input.VMID <= 0 {
		return CloudInitSnippet{}, errors.New("vmid must be greater than zero")
	}
	hostname := strings.TrimSpace(input.Hostname)
	if hostname == "" {
		hostname = fmt.Sprintf("sandbox-%d", input.VMID)
	}
	if err := validateHostname(hostname); err != nil {
		return CloudInitSnippet{}, err
	}
	sshKey := strings.TrimSpace(input.SSHPublicKey)
	if sshKey == "" {
		return CloudInitSnippet{}, errors.New("ssh public key is required")
	}
	if strings.ContainsAny(sshKey, "\n\r") {
		return CloudInitSnippet{}, errors.New("ssh public key must be a single line")
	}
	token := strings.TrimSpace(input.BootstrapToken)
	if token == "" {
		return CloudInitSnippet{}, errors.New("bootstrap token is required")
	}
	controller := strings.TrimSpace(input.ControllerURL)
	if controller == "" {
		return CloudInitSnippet{}, errors.New("controller URL is required")
	}

	storage, err := normalizeSnippetStorage(s.Storage)
	if err != nil {
		return CloudInitSnippet{}, err
	}
	dir, err := normalizeSnippetDir(s.Dir)
	if err != nil {
		return CloudInitSnippet{}, err
	}

	content, err := renderCloudInitUserData(hostname, sshKey, token, controller, int(input.VMID))
	if err != nil {
		return CloudInitSnippet{}, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return CloudInitSnippet{}, fmt.Errorf("create snippets dir: %w", err)
	}

	for attempt := 0; attempt < 5; attempt++ {
		suffix, err := randomSuffix(s.randReader(), 8)
		if err != nil {
			return CloudInitSnippet{}, fmt.Errorf("generate suffix: %w", err)
		}
		filename := fmt.Sprintf("agentlab-%d-%s.yaml", input.VMID, suffix)
		fullPath := filepath.Join(dir, filename)
		file, err := os.OpenFile(fullPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			if os.IsExist(err) {
				continue
			}
			return CloudInitSnippet{}, fmt.Errorf("create snippet file: %w", err)
		}
		if err := ensureWithinDir(dir, fullPath); err != nil {
			_ = file.Close()
			_ = os.Remove(fullPath)
			return CloudInitSnippet{}, err
		}
		_, writeErr := io.WriteString(file, content)
		closeErr := file.Close()
		if writeErr != nil {
			_ = os.Remove(fullPath)
			return CloudInitSnippet{}, fmt.Errorf("write snippet: %w", writeErr)
		}
		if closeErr != nil {
			_ = os.Remove(fullPath)
			return CloudInitSnippet{}, fmt.Errorf("close snippet: %w", closeErr)
		}
		storagePath := fmt.Sprintf("%s:snippets/%s", storage, filename)
		return CloudInitSnippet{
			VMID:        input.VMID,
			Filename:    filename,
			FullPath:    fullPath,
			Storage:     storage,
			StoragePath: storagePath,
		}, nil
	}

	return CloudInitSnippet{}, errors.New("unable to create unique snippet filename")
}

// Delete removes the snippet file from disk.
// ABOUTME: Silently succeeds if the file does not already exist.
func (s SnippetStore) Delete(snippet CloudInitSnippet) error {
	path := snippet.FullPath
	if path == "" {
		return nil
	}
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("remove snippet: %w", err)
	}
	return nil
}

func (s SnippetStore) randReader() io.Reader {
	if s.Rand != nil {
		return s.Rand
	}
	return rand.Reader
}

func randomSuffix(r io.Reader, bytesLen int) (string, error) {
	if bytesLen <= 0 {
		return "", errors.New("suffix length must be positive")
	}
	buf := make([]byte, bytesLen)
	if _, err := io.ReadFull(r, buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func normalizeSnippetStorage(storage string) (string, error) {
	storage = strings.TrimSpace(storage)
	if storage == "" {
		storage = defaultSnippetStorage
	}
	if len(storage) > 64 {
		return "", fmt.Errorf("snippet storage name too long: %d", len(storage))
	}
	for _, r := range storage {
		if !isStorageNameChar(r) {
			return "", fmt.Errorf("invalid snippet storage name: %q", storage)
		}
	}
	return storage, nil
}

func normalizeSnippetDir(dir string) (string, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		dir = defaultSnippetDir
	}
	cleaned := filepath.Clean(dir)
	if !filepath.IsAbs(cleaned) {
		return "", fmt.Errorf("snippets dir must be absolute: %q", dir)
	}
	return cleaned, nil
}

func ensureWithinDir(dir, path string) error {
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return fmt.Errorf("resolve snippet path: %w", err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("snippet path escapes base dir: %q", path)
	}
	return nil
}

func validateHostname(hostname string) error {
	if hostname == "" {
		return errors.New("hostname is required")
	}
	if len(hostname) > 253 {
		return fmt.Errorf("hostname too long: %d", len(hostname))
	}
	if strings.HasPrefix(hostname, ".") || strings.HasSuffix(hostname, ".") {
		return fmt.Errorf("hostname must not start or end with a dot: %q", hostname)
	}
	labels := strings.Split(hostname, ".")
	for _, label := range labels {
		if label == "" {
			return fmt.Errorf("hostname contains empty label: %q", hostname)
		}
		if len(label) > 63 {
			return fmt.Errorf("hostname label too long: %q", label)
		}
		if label[0] == '-' || label[len(label)-1] == '-' {
			return fmt.Errorf("hostname label must not start or end with hyphen: %q", label)
		}
		for _, r := range label {
			if !isHostnameLabelChar(r) {
				return fmt.Errorf("hostname contains invalid character: %q", hostname)
			}
		}
	}
	return nil
}

func isHostnameLabelChar(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z':
		return true
	case r >= 'A' && r <= 'Z':
		return true
	case r >= '0' && r <= '9':
		return true
	case r == '-':
		return true
	default:
		return false
	}
}

func isStorageNameChar(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z':
		return true
	case r >= 'A' && r <= 'Z':
		return true
	case r >= '0' && r <= '9':
		return true
	case r == '-', r == '_', r == '.':
		return true
	default:
		return false
	}
}

func renderCloudInitUserData(hostname, sshKey, token, controller string, vmid int) (string, error) {
	payload := struct {
		Token      string `json:"token"`
		Controller string `json:"controller"`
		VMID       int    `json:"vmid"`
	}{
		Token:      token,
		Controller: controller,
		VMID:       vmid,
	}
	jsonBytes, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal bootstrap payload: %w", err)
	}

	content := strings.Join([]string{
		"#cloud-config",
		"hostname: " + hostname,
		"ssh_pwauth: false",
		"users:",
		"  - name: agent",
		"    groups: [sudo]",
		"    sudo: ALL=(ALL) NOPASSWD:ALL",
		"    shell: /bin/bash",
		"    lock_passwd: true",
		"    ssh_authorized_keys:",
		"      - " + sshKey,
		// Grow the root partition/filesystem after AgentLab resizes the VM disk.
		"growpart:",
		"  mode: auto",
		"  devices: ['/']",
		"  ignore_growroot_disabled: false",
		"resize_rootfs: true",
		"write_files:",
		"  - path: /etc/agentlab/bootstrap.json",
		"    owner: agent:agent",
		"    permissions: \"0600\"",
		"    content: |",
		"      " + string(jsonBytes),
		"runcmd:",
		// Install qemu-guest-agent so AgentLab can discover guest IPs reliably.
		// Prefer runcmd+apt-get over cloud-init's packages module: some templates disable modules.
		"  - bash -lc 'if ! command -v qemu-guest-agent >/dev/null 2>&1; then (apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install -y qemu-guest-agent) || true; fi'",
		"  - bash -lc 'systemctl enable --now qemu-guest-agent || true'",
		// Ensure SSH is present and running for interactive access/debugging.
		"  - bash -lc 'if ! command -v sshd >/dev/null 2>&1; then (apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install -y openssh-server) || true; fi'",
		"  - bash -lc 'systemctl enable --now ssh || systemctl enable --now sshd || true'",
		"",
	}, "\n")

	return content, nil
}
