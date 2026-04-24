package proxy

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDNSResolver_AddAndRemove(t *testing.T) {
	hostsFile := filepath.Join(t.TempDir(), "hosts")
	ctx := context.Background()

	resolver := NewDNSResolver(hostsFile, nil)

	// Add entry
	if err := resolver.AddEntry(ctx, "10.77.0.1", "mybox.agentlab.local"); err != nil {
		t.Fatalf("AddEntry: %v", err)
	}

	// Verify it was written
	data, err := os.ReadFile(hostsFile)
	if err != nil {
		t.Fatalf("read hosts: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, "10.77.0.1 mybox.agentlab.local") {
		t.Errorf("hosts file doesn't contain entry:\n%s", content)
	}
	if !strings.Contains(content, agentlabHostsMarker) {
		t.Error("hosts file missing begin marker")
	}
	if !strings.Contains(content, agentlabHostsEnd) {
		t.Error("hosts file missing end marker")
	}

	// Add another entry
	if err := resolver.AddEntry(ctx, "10.77.0.1", "other.agentlab.local"); err != nil {
		t.Fatalf("AddEntry second: %v", err)
	}

	data, err = os.ReadFile(hostsFile)
	if err != nil {
		t.Fatalf("read hosts second: %v", err)
	}

	content = string(data)
	if !strings.Contains(content, "other.agentlab.local") {
		t.Errorf("hosts file doesn't contain second entry:\n%s", content)
	}

	// Remove first entry
	if err := resolver.RemoveEntry(ctx, "mybox.agentlab.local"); err != nil {
		t.Fatalf("RemoveEntry: %v", err)
	}

	data, err = os.ReadFile(hostsFile)
	if err != nil {
		t.Fatalf("read hosts after remove: %v", err)
	}

	content = string(data)
	if strings.Contains(content, "mybox.agentlab.local") {
		t.Errorf("hosts file still contains removed entry:\n%s", content)
	}
	if !strings.Contains(content, "other.agentlab.local") {
		t.Errorf("hosts file lost remaining entry:\n%s", content)
	}
}

func TestDNSResolver_UpdateExisting(t *testing.T) {
	hostsFile := filepath.Join(t.TempDir(), "hosts")
	ctx := context.Background()

	resolver := NewDNSResolver(hostsFile, nil)

	// Add entry
	if err := resolver.AddEntry(ctx, "10.77.0.1", "mybox.agentlab.local"); err != nil {
		t.Fatalf("AddEntry: %v", err)
	}

	// Update with different IP
	if err := resolver.AddEntry(ctx, "10.77.0.2", "mybox.agentlab.local"); err != nil {
		t.Fatalf("AddEntry update: %v", err)
	}

	data, err := os.ReadFile(hostsFile)
	if err != nil {
		t.Fatalf("read hosts: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, "10.77.0.2 mybox.agentlab.local") {
		t.Errorf("hosts file doesn't have updated entry:\n%s", content)
	}
	if strings.Contains(content, "10.77.0.1 mybox.agentlab.local") {
		t.Errorf("hosts file still has old entry:\n%s", content)
	}
}

func TestDNSResolver_PreservesOtherContent(t *testing.T) {
	hostsFile := filepath.Join(t.TempDir(), "hosts")
	ctx := context.Background()

	// Write existing content
	existing := "127.0.0.1 localhost\n::1 localhost\n"
	if err := os.WriteFile(hostsFile, []byte(existing), 0o644); err != nil {
		t.Fatalf("write initial hosts: %v", err)
	}

	resolver := NewDNSResolver(hostsFile, nil)

	// Add managed entry
	if err := resolver.AddEntry(ctx, "10.77.0.1", "mybox.agentlab.local"); err != nil {
		t.Fatalf("AddEntry: %v", err)
	}

	data, err := os.ReadFile(hostsFile)
	if err != nil {
		t.Fatalf("read hosts: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, "127.0.0.1 localhost") {
		t.Errorf("hosts file lost existing content:\n%s", content)
	}
	if !strings.Contains(content, "10.77.0.1 mybox.agentlab.local") {
		t.Errorf("hosts file doesn't contain managed entry:\n%s", content)
	}
}

func TestDNSResolver_RemoveNonExistent(t *testing.T) {
	hostsFile := filepath.Join(t.TempDir(), "hosts")
	ctx := context.Background()

	resolver := NewDNSResolver(hostsFile, nil)

	// Remove from empty file should not error
	if err := resolver.RemoveEntry(ctx, "nonexistent.agentlab.local"); err != nil {
		t.Fatalf("RemoveEntry from empty: %v", err)
	}
}
