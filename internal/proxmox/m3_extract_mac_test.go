package proxmox

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestExtractMAC_StandardProxmoxSyntax verifies extractMAC recognizes the MAC
// regardless of which model key carries it — the standard virtio=/e1000= forms
// that the old mac=-only matcher missed — plus explicit mac= and malformed
// input (review M3).
func TestExtractMAC_StandardProxmoxSyntax(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"virtio model", "virtio=BC:24:11:D5:49:57,bridge=vmbr1", "BC:24:11:D5:49:57"},
		{"e1000 model", "e1000=00:11:22:33:44:55,bridge=vmbr0", "00:11:22:33:44:55"},
		{"rtl8139 model", "rtl8139=AA:BB:CC:DD:EE:FF,bridge=vmbr0", "AA:BB:CC:DD:EE:FF"},
		{"explicit mac key", "mac=11:22:33:44:55:66,bridge=vmbr1", "11:22:33:44:55:66"},
		{"mac not first field", "bridge=vmbr1,virtio=AA:BB:CC:DD:EE:FF", "AA:BB:CC:DD:EE:FF"},
		{"no mac present", "bridge=vmbr1,rate=100", ""},
		{"malformed value", "virtio=not-a-mac,bridge=vmbr1", ""},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := extractMAC(tt.in); got != tt.want {
				t.Errorf("extractMAC(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// TestAPIBackendGuestIP_DHCPFallbackResolvesViaVirtioMAC is the end-to-end
// review-M3 case: a standard Proxmox net config (virtio=MAC) populates the MAC
// list, a real dnsmasq lease fixture maps that MAC to an IP, and the DHCP path
// returns it — even though the guest agent is configured as unavailable (and is
// never reached because DHCP resolves on the first pass).
func TestAPIBackendGuestIP_DHCPFallbackResolvesViaVirtioMAC(t *testing.T) {
	leaseDir := t.TempDir()
	leasePath := filepath.Join(leaseDir, "dnsmasq.leases")
	lease := "1738159200 bc:24:11:d5:49:57 10.77.0.55 sandbox-101 *\n"
	if err := os.WriteFile(leasePath, []byte(lease), 0o600); err != nil {
		t.Fatalf("write lease: %v", err)
	}

	var agentCalled bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && hasSuffix(r.URL.Path, "/qemu/101/config"):
			_, _ = io.Copy(io.Discard, r.Body)
			r.Body.Close()
			_, _ = w.Write([]byte(`{"data":{"net0":"virtio=BC:24:11:D5:49:57,bridge=vmbr1"}}`))
		case r.Method == http.MethodGet && hasSuffix(r.URL.Path, "/qemu/101/agent/network-get-interfaces"):
			agentCalled = true
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"errors":{"agent":"QEMU guest agent is not running"}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)

	backend := &APIBackend{
		BaseURL:        srv.URL + "/api2/json",
		Node:           "pve",
		HTTPClient:     srv.Client(),
		AgentCIDR:      "10.77.0.0/16",
		DHCPLeasePaths: []string{leasePath},
		Sleep: func(_ context.Context, _ time.Duration) error { return nil },
	}

	ip, err := backend.GuestIP(context.Background(), 101)
	if err != nil {
		t.Fatalf("GuestIP() error = %v", err)
	}
	if ip != "10.77.0.55" {
		t.Fatalf("GuestIP() = %q, want 10.77.0.55", ip)
	}
	if agentCalled {
		t.Fatal("guest agent should not be consulted when DHCP resolves")
	}
}

// hasSuffix is strings.HasSuffix without pulling the import into the handler.
func hasSuffix(s, suffix string) bool {
	return len(s) >= len(suffix) && s[len(s)-len(suffix):] == suffix
}
