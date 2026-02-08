package daemon

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/agentlab/agentlab/internal/models"
)

func TestProfileHostMountPaths(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    []string
		wantErr bool
	}{
		{
			name: "clean",
			raw: `
name: yolo
template_vmid: 9000
storage:
  workspace: none
`,
		},
		{
			name: "host-path",
			raw: `
name: yolo
template_vmid: 9000
mounts:
  - host_path: /etc
    guest_path: /mnt/host
`,
			want: []string{"mounts", "mounts[0].host_path"},
		},
		{
			name: "host-mounts",
			raw: `
name: yolo
template_vmid: 9000
host_mounts:
  - /var/lib/agentlab
`,
			want: []string{"host_mounts"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := profileHostMountPaths(tc.raw)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			sort.Strings(got)
			sort.Strings(tc.want)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("expected %v, got %v", tc.want, got)
			}
		})
	}
}

func TestValidateProfileForProvisioning(t *testing.T) {
	profile := models.Profile{
		Name: "yolo",
		RawYAML: `
name: yolo
template_vmid: 9000
host_path: /etc
`,
	}
	err := validateProfileForProvisioning(profile)
	if err == nil {
		t.Fatalf("expected error")
	}
	if !strings.Contains(err.Error(), "host bind mounts are not allowed") {
		t.Fatalf("expected host mount rationale, got %v", err)
	}
}

func TestValidateProfileFirewallGroup(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		wantErr bool
	}{
		{
			name: "valid-firewall-group",
			raw: `
name: yolo
template_vmid: 9000
network:
  firewall_group: agent_nat_default
`,
		},
		{
			name: "mode-only",
			raw: `
name: yolo
template_vmid: 9000
network:
  mode: nat
`,
		},
		{
			name: "empty-firewall-group",
			raw: `
name: yolo
template_vmid: 9000
network:
  firewall_group: ""
`,
			wantErr: true,
		},
		{
			name: "firewall-group-disabled-firewall",
			raw: `
name: yolo
template_vmid: 9000
network:
  firewall: false
  firewall_group: agent_nat_default
`,
			wantErr: true,
		},
		{
			name: "invalid-mode",
			raw: `
name: yolo
template_vmid: 9000
network:
  mode: open
`,
			wantErr: true,
		},
		{
			name: "mode-mismatch-firewall-group",
			raw: `
name: yolo
template_vmid: 9000
network:
  mode: allowlist
  firewall_group: agent_nat_default
`,
			wantErr: true,
		},
		{
			name: "mode-with-firewall-false",
			raw: `
name: yolo
template_vmid: 9000
network:
  mode: nat
  firewall: false
`,
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			profile := models.Profile{
				Name:    "yolo",
				RawYAML: tc.raw,
			}
			err := validateProfileForProvisioning(profile)
			if tc.wantErr && err == nil {
				t.Fatalf("expected error")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}
