package integrations

import (
	"testing"
)

func TestMatchesSandbox(t *testing.T) {
	tests := []struct {
		name       string
		integ      *Integration
		sbName     string
		sbTags     []string
		wantMatch  bool
	}{
		{
			name: "auto:all matches any sandbox",
			integ: &Integration{AttachMode: AttachAutoAll},
			sbName: "mybox",
			wantMatch: true,
		},
		{
			name: "sandbox mode matches exact name",
			integ: &Integration{AttachMode: AttachSandbox, AttachSelector: "mybox"},
			sbName: "mybox",
			wantMatch: true,
		},
		{
			name: "sandbox mode does not match different name",
			integ: &Integration{AttachMode: AttachSandbox, AttachSelector: "otherbox"},
			sbName: "mybox",
			wantMatch: false,
		},
		{
			name: "tag mode matches sandbox with tag",
			integ: &Integration{AttachMode: AttachTag, AttachSelector: "production"},
			sbName: "mybox",
			sbTags: []string{"production", "web"},
			wantMatch: true,
		},
		{
			name: "tag mode does not match sandbox without tag",
			integ: &Integration{AttachMode: AttachTag, AttachSelector: "staging"},
			sbName: "mybox",
			sbTags: []string{"production"},
			wantMatch: false,
		},
		{
			name: "nil integration does not match",
			integ: nil,
			sbName: "mybox",
			wantMatch: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.integ.MatchesSandbox(tt.sbName, tt.sbTags)
			if got != tt.wantMatch {
				t.Errorf("MatchesSandbox() = %v, want %v", got, tt.wantMatch)
			}
		})
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		integ   *Integration
		wantErr error
	}{
		{
			name: "valid http-proxy integration",
			integ: &Integration{
				Name:       "myapi",
				Type:       TypeHTTPProxy,
				Target:     "https://api.example.com",
				Secret:     "sk-test",
				SecretType: "bearer",
				AttachMode: AttachAutoAll,
			},
			wantErr: nil,
		},
		{
			name: "valid git-proxy integration",
			integ: &Integration{
				Name:       "github",
				Type:       TypeGitProxy,
				Secret:     "ghp-test",
				Username:   "x-access-token",
				AttachMode: AttachAutoAll,
			},
			wantErr: nil,
		},
		{
			name: "missing name",
			integ: &Integration{
				Type:       TypeHTTPProxy,
				Target:     "https://api.example.com",
				Secret:     "sk-test",
				AttachMode: AttachAutoAll,
			},
			wantErr: ErrNameRequired,
		},
		{
			name: "missing target for http-proxy",
			integ: &Integration{
				Name:       "myapi",
				Type:       TypeHTTPProxy,
				Secret:     "sk-test",
				AttachMode: AttachAutoAll,
			},
			wantErr: ErrTargetRequired,
		},
		{
			name: "missing secret",
			integ: &Integration{
				Name:       "myapi",
				Type:       TypeHTTPProxy,
				Target:     "https://api.example.com",
				AttachMode: AttachAutoAll,
			},
			wantErr: ErrSecretRequired,
		},
		{
			name: "invalid type",
			integ: &Integration{
				Name:       "myapi",
				Type:       "invalid",
				Target:     "https://api.example.com",
				Secret:     "sk-test",
				AttachMode: AttachAutoAll,
			},
			wantErr: ErrInvalidType,
		},
		{
			name: "sandbox attach without selector",
			integ: &Integration{
				Name:       "myapi",
				Type:       TypeHTTPProxy,
				Target:     "https://api.example.com",
				Secret:     "sk-test",
				AttachMode: AttachSandbox,
			},
			wantErr: ErrAttachSelectorRequired,
		},
		{
			name: "tag attach without selector",
			integ: &Integration{
				Name:       "myapi",
				Type:       TypeHTTPProxy,
				Target:     "https://api.example.com",
				Secret:     "sk-test",
				AttachMode: AttachTag,
			},
			wantErr: ErrAttachSelectorRequired,
		},
		{
			name: "valid sandbox attach with selector",
			integ: &Integration{
				Name:           "myapi",
				Type:           TypeHTTPProxy,
				Target:         "https://api.example.com",
				Secret:         "sk-test",
				AttachMode:     AttachSandbox,
				AttachSelector: "mybox",
			},
			wantErr: nil,
		},
		{
			name: "valid tag attach with selector",
			integ: &Integration{
				Name:           "myapi",
				Type:           TypeHTTPProxy,
				Target:         "https://api.example.com",
				Secret:         "sk-test",
				AttachMode:     AttachTag,
				AttachSelector: "production",
			},
			wantErr: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.integ.Validate()
			if err != tt.wantErr {
				t.Errorf("Validate() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestProxyPath(t *testing.T) {
	tests := []struct {
		name string
		integ *Integration
		want  string
	}{
		{
			name: "returns proxy path",
			integ: &Integration{Name: "myapi"},
			want: "/proxy/myapi/",
		},
		{
			name: "empty name returns empty path",
			integ: &Integration{Name: ""},
			want: "",
		},
		{
			name: "nil returns empty",
			integ: nil,
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.integ.ProxyPath()
			if got != tt.want {
				t.Errorf("ProxyPath() = %v, want %v", got, tt.want)
			}
		})
	}
}
