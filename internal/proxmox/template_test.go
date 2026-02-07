package proxmox

import "testing"

func TestParseAgentConfigString(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		want    bool
		wantErr bool
	}{
		{name: "one", value: "1", want: true},
		{name: "zero", value: "0", want: false},
		{name: "enabled_one", value: "enabled=1", want: true},
		{name: "enabled_zero", value: "enabled=0", want: false},
		{name: "enabled_one_with_options", value: "enabled=1,fstrim_cloned_disks=0", want: true},
		{name: "disabled_one", value: "disabled=1", want: false},
		{name: "empty", value: "", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseAgentConfigString(tt.value)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("parseAgentConfigString() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("parseAgentConfigString() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHasCloudInitDrive(t *testing.T) {
	if hasCloudInitDrive(nil) {
		t.Fatalf("expected false for nil config")
	}
	if hasCloudInitDrive(map[string]string{"scsi0": "local-zfs:vm-1-disk-0,size=10G"}) {
		t.Fatalf("expected false when missing cloudinit drive")
	}
	if !hasCloudInitDrive(map[string]string{"ide2": "local-zfs:vm-1-cloudinit,media=cdrom,size=4M"}) {
		t.Fatalf("expected true when cloudinit drive present")
	}
}
