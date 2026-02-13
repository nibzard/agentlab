package proxmox

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSnippetStoreCreateAndDelete(t *testing.T) {
	dir := t.TempDir()
	randBytes := []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}
	store := SnippetStore{
		Storage: "local",
		Dir:     dir,
		Rand:    bytes.NewReader(randBytes),
	}

	input := SnippetInput{
		VMID:           101,
		Hostname:       "sandbox-101",
		SSHPublicKey:   "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIBtestkey agent@test",
		BootstrapToken: "token-123",
		ControllerURL:  "http://10.77.0.1:8844",
	}

	snippet, err := store.Create(input)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	wantFilename := "agentlab-101-0102030405060708.yaml"
	if snippet.Filename != wantFilename {
		t.Fatalf("Filename = %q, want %q", snippet.Filename, wantFilename)
	}
	wantStorage := "local:snippets/" + wantFilename
	if snippet.StoragePath != wantStorage {
		t.Fatalf("StoragePath = %q, want %q", snippet.StoragePath, wantStorage)
	}
	if snippet.FullPath != filepath.Join(dir, wantFilename) {
		t.Fatalf("FullPath = %q, want %q", snippet.FullPath, filepath.Join(dir, wantFilename))
	}

	info, err := os.Stat(snippet.FullPath)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("file mode = %v, want %v", info.Mode().Perm(), 0o600)
	}

	contentBytes, err := os.ReadFile(snippet.FullPath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	content := string(contentBytes)
	if !strings.Contains(content, "hostname: sandbox-101") {
		t.Fatalf("content missing hostname")
	}
	if !strings.Contains(content, "ssh_authorized_keys:") {
		t.Fatalf("content missing ssh_authorized_keys")
	}
	if !strings.Contains(content, input.SSHPublicKey) {
		t.Fatalf("content missing ssh public key")
	}
	if !strings.Contains(content, "{\"token\":\"token-123\",\"controller\":\"http://10.77.0.1:8844\",\"vmid\":101}") {
		t.Fatalf("content missing bootstrap json")
	}
	if !strings.Contains(content, "owner: agent:agent") {
		t.Fatalf("content missing bootstrap owner")
	}
	if strings.Contains(content, "runcmd") {
		t.Fatalf("content should not contain runcmd")
	}

	if err := store.Delete(snippet); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if _, err := os.Stat(snippet.FullPath); !os.IsNotExist(err) {
		t.Fatalf("snippet file still exists after delete")
	}
}

func TestSnippetStoreDefaultsHostname(t *testing.T) {
	dir := t.TempDir()
	store := SnippetStore{Dir: dir, Rand: bytes.NewReader([]byte{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff, 0x00, 0x11})}

	snippet, err := store.Create(SnippetInput{
		VMID:           7,
		SSHPublicKey:   "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIBtestkey agent@test",
		BootstrapToken: "token-abc",
		ControllerURL:  "http://10.77.0.1:8844",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	contentBytes, err := os.ReadFile(snippet.FullPath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	content := string(contentBytes)
	if !strings.Contains(content, "hostname: sandbox-7") {
		t.Fatalf("default hostname missing")
	}
}

func TestSnippetStoreDeleteMissingIsOk(t *testing.T) {
	err := SnippetStore{}.Delete(CloudInitSnippet{FullPath: "/tmp/agentlab-snippet-missing.yaml"})
	if err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
}

func TestSnippetStoreRequiresToken(t *testing.T) {
	store := SnippetStore{Dir: t.TempDir()}
	_, err := store.Create(SnippetInput{
		VMID:          9,
		SSHPublicKey:  "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIBtestkey agent@test",
		ControllerURL: "http://10.77.0.1:8844",
	})
	if err == nil {
		t.Fatalf("expected error for missing bootstrap token")
	}
}

func TestSnippetStoreRejectsInvalidHostname(t *testing.T) {
	tests := []string{
		"bad host",
		"bad\thost",
		"bad\nhost",
		".leadingdot",
		"trailingdot.",
		"double..dot",
		"-leading-hyphen",
		"trailing-hyphen-",
		"has_underscore",
		strings.Repeat("a", 64),
		"label." + strings.Repeat("b", 64),
	}
	for i, hostname := range tests {
		t.Run(fmt.Sprintf("case-%d", i), func(t *testing.T) {
			store := SnippetStore{Dir: t.TempDir()}
			_, err := store.Create(SnippetInput{
				VMID:           12,
				Hostname:       hostname,
				SSHPublicKey:   "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIBtestkey agent@test",
				BootstrapToken: "token-xyz",
				ControllerURL:  "http://10.77.0.1:8844",
			})
			if err == nil {
				t.Fatalf("expected error for hostname %q", hostname)
			}
		})
	}
}

func TestSnippetStoreRejectsInvalidStorageName(t *testing.T) {
	tests := []string{
		"local:snippets",
		"local/snippets",
		"local snippets",
		"local\nsnippets",
		"local\tbad",
		"local@bad",
	}
	for i, storage := range tests {
		t.Run(fmt.Sprintf("case-%d", i), func(t *testing.T) {
			store := SnippetStore{Dir: t.TempDir(), Storage: storage}
			_, err := store.Create(SnippetInput{
				VMID:           13,
				Hostname:       "sandbox-13",
				SSHPublicKey:   "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIBtestkey agent@test",
				BootstrapToken: "token-xyz",
				ControllerURL:  "http://10.77.0.1:8844",
			})
			if err == nil {
				t.Fatalf("expected error for storage %q", storage)
			}
		})
	}
}

func TestSnippetStoreRejectsRelativeDir(t *testing.T) {
	store := SnippetStore{Dir: "snippets"}
	_, err := store.Create(SnippetInput{
		VMID:           14,
		Hostname:       "sandbox-14",
		SSHPublicKey:   "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIBtestkey agent@test",
		BootstrapToken: "token-xyz",
		ControllerURL:  "http://10.77.0.1:8844",
	})
	if err == nil {
		t.Fatalf("expected error for relative snippets dir")
	}
}
