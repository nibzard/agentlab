package sandbox

import (
	"testing"
)

func TestParseType(t *testing.T) {
	tests := []struct {
		input string
		want  Type
	}{
		{"", TypeVM},
		{"vm", TypeVM},
		{"qemu", TypeVM},
		{"lxc", TypeLXC},
		{"container", TypeLXC},
		{"lxd", TypeLXC},
		{"unknown", TypeVM},
	}
	for _, tc := range tests {
		got := ParseType(tc.input)
		if got != tc.want {
			t.Errorf("ParseType(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestResolveLXCImage(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{
			"ubuntu:22.04",
			"local:vztmpl/ubuntu-22.04-standard_22.04-1_amd64.tar.zst",
		},
		{
			"debian:12",
			"local:vztmpl/debian-12-standard_12.2-1_amd64.tar.zst",
		},
		{
			"alpine:3.19",
			"local:vztmpl/alpine-3.19-default_20231218_amd64.tar.xz",
		},
		{
			"centos:9",
			"local:vztmpl/centos-9-default_9_amd64.tar.xz",
		},
		{
			"fedora:39",
			"local:vztmpl/fedora-39-default_39_amd64.tar.xz",
		},
		{
			"archlinux:latest",
			"local:vztmpl/archlinux-base_latest_amd64.tar.zst",
		},
		{
			// Already a storage path — returned as-is
			"local:vztmpl/custom-image.tar.zst",
			"local:vztmpl/custom-image.tar.zst",
		},
		{
			// Unknown distro — returned as-is
			"gentoo:23.0",
			"gentoo:23.0",
		},
	}
	for _, tc := range tests {
		got := resolveLXCImage(tc.input)
		if got != tc.want {
			t.Errorf("resolveLXCImage(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestNewLXCBackendValidation(t *testing.T) {
	_, err := NewLXCBackend(ProxmoxAPIConfig{})
	if err == nil {
		t.Fatal("expected error for empty URL")
	}

	_, err = NewLXCBackend(ProxmoxAPIConfig{URL: "https://localhost:8006"})
	if err == nil {
		t.Fatal("expected error for empty token")
	}

	_, err = NewLXCBackend(ProxmoxAPIConfig{URL: "https://localhost:8006", Token: "test"})
	if err == nil {
		t.Fatal("expected error for empty node")
	}

	_, err = NewLXCBackend(ProxmoxAPIConfig{
		URL:   "https://localhost:8006",
		Token: "root@pam!test=abc",
		Node:  "pve",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLXCBackendType(t *testing.T) {
	b, _ := NewLXCBackend(ProxmoxAPIConfig{
		URL: "https://localhost:8006", Token: "test", Node: "pve",
	})
	if b.SandboxType() != TypeLXC {
		t.Errorf("SandboxType() = %q, want %q", b.SandboxType(), TypeLXC)
	}
}

func TestLXCBackendCapabilities(t *testing.T) {
	b, _ := NewLXCBackend(ProxmoxAPIConfig{
		URL: "https://localhost:8006", Token: "test", Node: "pve",
	})
	caps := b.Capabilities()
	if !caps.Snapshots {
		t.Error("LXC should support snapshots")
	}
	if !caps.Suspend {
		t.Error("LXC should support suspend (freeze)")
	}
	if caps.WorkspaceMount {
		t.Error("LXC should not support workspace mount via SCSI")
	}
}

func TestErrNotSupported(t *testing.T) {
	err := ErrNotSupported{Op: "test_op"}
	if err.Error() != "operation not supported: test_op" {
		t.Errorf("unexpected error message: %s", err.Error())
	}
}
