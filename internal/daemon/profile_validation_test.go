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
