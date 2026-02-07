package proxmox

import "testing"

func TestParseSizeGB(t *testing.T) {
	tests := []struct {
		in   string
		want float64
	}{
		{"40G", 40},
		{"2.8G", 2.8},
		{"512M", 0.5},
		{"1T", 1024},
	}
	for _, tt := range tests {
		got, err := parseSizeGB(tt.in)
		if err != nil {
			t.Fatalf("parseSizeGB(%q) error = %v", tt.in, err)
		}
		if got != tt.want {
			t.Fatalf("parseSizeGB(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestExtractDiskSizeToken(t *testing.T) {
	got := extractDiskSizeToken("local-zfs:vm-100-disk-0,discard=on,size=2.8G")
	if got != "2.8G" {
		t.Fatalf("extractDiskSizeToken() = %q, want %q", got, "2.8G")
	}
}

func TestDetectRootDisk(t *testing.T) {
	cfg := map[string]string{
		"bootdisk": "virtio0",
		"virtio0":  "local-zfs:vm-100-disk-0,size=40G",
		"scsi0":    "local-zfs:vm-100-disk-1,size=10G",
	}
	if got := detectRootDisk(cfg); got != "virtio0" {
		t.Fatalf("detectRootDisk(bootdisk) = %q, want %q", got, "virtio0")
	}

	cfg = map[string]string{
		"boot":  "order=scsi0;ide2;net0",
		"scsi0": "local-zfs:vm-100-disk-0,size=40G",
	}
	if got := detectRootDisk(cfg); got != "scsi0" {
		t.Fatalf("detectRootDisk(boot order) = %q, want %q", got, "scsi0")
	}

	cfg = map[string]string{
		"scsi0": "local-zfs:vm-100-disk-0,size=40G",
	}
	if got := detectRootDisk(cfg); got != "scsi0" {
		t.Fatalf("detectRootDisk(candidate) = %q, want %q", got, "scsi0")
	}
}

func TestResizeDeltaGB(t *testing.T) {
	if got := resizeDeltaGB(2.8, 40); got != 38 {
		t.Fatalf("resizeDeltaGB(2.8, 40) = %d, want %d", got, 38)
	}
	if got := resizeDeltaGB(40.0, 40); got != 0 {
		t.Fatalf("resizeDeltaGB(40.0, 40) = %d, want %d", got, 0)
	}
	if got := resizeDeltaGB(40.9, 40); got != 0 {
		t.Fatalf("resizeDeltaGB(40.9, 40) = %d, want %d", got, 0)
	}
}
