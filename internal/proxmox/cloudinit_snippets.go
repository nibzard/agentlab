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
	VMID           VMID    // Target VM identifier
	Hostname       string  // VM hostname (defaults to "sandbox-{vmid}" if empty)
	SSHPublicKey   string  // SSH public key for agent user (required)
	BootstrapToken string  // Authentication token for bootstrap API (required)
	ControllerURL  string  // URL of the controller API endpoint (required)
}

// CloudInitSnippet represents a stored snippet file and its Proxmox reference.
	VMID        VMID    // Associated VM identifier
	Filename    string  // Base filename of the snippet
	FullPath    string  // Absolute filesystem path to the snippet file
	Storage     string  // Proxmox storage name (e.g., "local")
	StoragePath string  // Proxmox storage path (e.g., "local:snippets/agentlab-1000-abc123.yaml")
}

// SnippetStore manages cloud-init snippet files for Proxmox.
// ABOUTME: Snippets are stored as YAML files in Proxmox's snippets directory and
// referenced during VM cloning via the cicustom parameter.
	Storage string // Proxmox storage name for snippets (defaults to "local")
	Dir     string // Filesystem directory for snippet files (defaults to "/var/lib/vz/snippets")
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
	if strings.ContainsAny(hostname, " \t\n") {
		return CloudInitSnippet{}, fmt.Errorf("hostname contains whitespace: %q", hostname)
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

	storage := strings.TrimSpace(s.Storage)
	if storage == "" {
		storage = defaultSnippetStorage
	}
	dir := strings.TrimSpace(s.Dir)
	if dir == "" {
		dir = defaultSnippetDir
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
		"users:",
		"  - name: agent",
		"    ssh_authorized_keys:",
		"      - " + sshKey,
		"write_files:",
		"  - path: /etc/agentlab/bootstrap.json",
		"    owner: agent:agent",
		"    permissions: \"0600\"",
		"    content: |",
		"      " + string(jsonBytes),
		"",
	}, "\n")

	return content, nil
}
