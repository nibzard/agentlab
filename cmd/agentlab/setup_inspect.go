package main

import (
	"context"
	"debug/elf"
	"fmt"
	"github.com/agentlab/agentlab/internal/hostidentity"
	"io"
	"strconv"
	"strings"
)

type bootstrapHostInfo struct {
	System           string `json:"system"`
	Architecture     string `json:"architecture"`
	Identity         string `json:"identity"`
	AvailableDiskKiB uint64 `json:"available_disk_kib"`
}

type bootstrapHostRunner interface {
	runArgs(context.Context, []string, io.Reader) (string, error)
}

func parseBootstrapPlatform(raw string) (string, error) {
	fields := strings.Fields(raw)
	if len(fields) != 2 || fields[0] != "Linux" {
		return "", fmt.Errorf("unsupported server platform; bootstrap requires Linux")
	}
	switch fields[1] {
	case "x86_64", "amd64":
		return "amd64", nil
	case "aarch64", "arm64":
		return "arm64", nil
	default:
		return "", fmt.Errorf("unsupported server architecture %q", fields[1])
	}
}

// inspectBootstrapHost performs read-only checks before uploading or installing.
func inspectBootstrapHost(ctx context.Context, runner bootstrapHostRunner) (bootstrapHostInfo, error) {
	var host bootstrapHostInfo
	platform, err := runner.runArgs(ctx, []string{"uname", "-sm"}, nil)
	if err != nil {
		return host, &blockedRequirement{Code: "credentials_required", Requirement: "ssh_access", Action: "establish SSH access with a trusted host key and retry setup"}
	}
	arch, err := parseBootstrapPlatform(platform)
	if err != nil {
		return host, err
	}
	host.System = "linux"
	host.Architecture = arch
	identity, err := runner.runArgs(ctx, []string{"cat", "/etc/machine-id"}, nil)
	if err != nil {
		return host, fmt.Errorf("cannot read server identity")
	}
	identity = strings.TrimSpace(identity)
	if len(identity) != 32 || strings.Trim(identity, "0123456789abcdef") != "" {
		return host, fmt.Errorf("server machine identity is invalid")
	}
	host.Identity, err = hostidentity.FromMachineID(identity)
	if err != nil {
		return host, err
	}
	uid, err := runner.runArgs(ctx, []string{"id", "-u"}, nil)
	if err != nil {
		return host, fmt.Errorf("cannot inspect server permissions")
	}
	if strings.TrimSpace(uid) != "0" {
		if _, err := runner.runArgs(ctx, []string{"sudo", "-n", "true"}, nil); err != nil {
			return host, &blockedRequirement{Code: "authorization_required", Requirement: "server_installation", Action: "grant non-interactive sudo access for server setup, then retry"}
		}
	}
	if _, err := runner.runArgs(ctx, []string{"sh", "-c", "command -v qm >/dev/null && command -v pvesm >/dev/null && command -v systemctl >/dev/null && command -v tar >/dev/null && command -v bash >/dev/null && command -v flock >/dev/null && command -v systemd-run >/dev/null && command -v base64 >/dev/null && command -v sha256sum >/dev/null && command -v sync >/dev/null && command -v stat >/dev/null"}, nil); err != nil {
		return host, fmt.Errorf("unsupported host: bootstrap requires Proxmox tools, systemd, tar, bash, and flock")
	}
	disk, err := runner.runArgs(ctx, []string{"df", "-Pk", "/var/lib/vz"}, nil)
	if err != nil {
		return host, fmt.Errorf("cannot inspect server template storage at /var/lib/vz")
	}
	lines := strings.Split(strings.TrimSpace(disk), "\n")
	if len(lines) < 2 {
		return host, fmt.Errorf("invalid server storage report")
	}
	values := strings.Fields(lines[len(lines)-1])
	if len(values) < 4 {
		return host, fmt.Errorf("invalid server storage report")
	}
	host.AvailableDiskKiB, err = strconv.ParseUint(values[3], 10, 64)
	if err != nil {
		return host, fmt.Errorf("invalid server storage capacity")
	}
	if host.AvailableDiskKiB < 10*1024*1024 {
		return host, fmt.Errorf("insufficient server template storage: at least 10 GiB free is required")
	}
	return host, nil
}

// validateBootstrapBinary rejects client-platform binaries before any upload.
func validateBootstrapBinary(path, arch string) error {
	binary, err := elf.Open(path)
	if err != nil {
		return fmt.Errorf("server binary must be a Linux ELF executable: %s", path)
	}
	defer binary.Close()
	expected := elf.EM_X86_64
	if arch == "arm64" {
		expected = elf.EM_AARCH64
	}
	if binary.Machine != expected || binary.Class != elf.ELFCLASS64 {
		return fmt.Errorf("server binary does not match Linux %s: %s", arch, path)
	}
	return nil
}
