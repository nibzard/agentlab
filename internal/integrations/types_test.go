package integrations

import (
	"errors"
	"testing"
)

func TestMatchesSandbox(t *testing.T) {
	tests := []struct {
		name      string
		integ     *Integration
		sbName    string
		sbTags    []string
		wantMatch bool
	}{
		{
			name:      "auto:all matches any sandbox",
			integ:     &Integration{AttachMode: AttachAutoAll},
			sbName:    "mybox",
			wantMatch: true,
		},
		{
			name:      "sandbox mode matches exact name",
			integ:     &Integration{AttachMode: AttachSandbox, AttachSelector: "mybox"},
			sbName:    "mybox",
			wantMatch: true,
		},
		{
			name:      "sandbox mode does not match different name",
			integ:     &Integration{AttachMode: AttachSandbox, AttachSelector: "otherbox"},
			sbName:    "mybox",
			wantMatch: false,
		},
		{
			name:      "tag mode matches sandbox with tag",
			integ:     &Integration{AttachMode: AttachTag, AttachSelector: "production"},
			sbName:    "mybox",
			sbTags:    []string{"production", "web"},
			wantMatch: true,
		},
		{
			name:      "tag mode does not match sandbox without tag",
			integ:     &Integration{AttachMode: AttachTag, AttachSelector: "staging"},
			sbName:    "mybox",
			sbTags:    []string{"production"},
			wantMatch: false,
		},
		{
			name:      "nil integration does not match",
			integ:     nil,
			sbName:    "mybox",
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
		{
			name: "file scheme target rejected",
			integ: &Integration{
				Name:       "myapi",
				Type:       TypeHTTPProxy,
				Target:     "file:///etc/passwd",
				Secret:     "sk-test",
				AttachMode: AttachAutoAll,
			},
			wantErr: ErrInvalidTargetScheme,
		},
		{
			name: "loopback target rejected",
			integ: &Integration{
				Name:       "myapi",
				Type:       TypeHTTPProxy,
				Target:     "http://127.0.0.1:8006",
				Secret:     "sk-test",
				AttachMode: AttachAutoAll,
			},
			wantErr: ErrInvalidTargetHost,
		},
		{
			name: "localhost hostname rejected",
			integ: &Integration{
				Name:       "myapi",
				Type:       TypeLLMProxy,
				Target:     "http://localhost:11434",
				AttachMode: AttachAutoAll,
			},
			wantErr: ErrInvalidTargetHost,
		},
		{
			name: "link-local metadata target rejected",
			integ: &Integration{
				Name:       "myapi",
				Type:       TypeGitProxy,
				Target:     "http://169.254.169.254/latest/meta-data",
				Secret:     "sk-test",
				AttachMode: AttachAutoAll,
			},
			wantErr: ErrInvalidTargetHost,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.integ.Validate()
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("Validate() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateTarget(t *testing.T) {
	allow := []string{"api.example.com", "10.1.2.3"}
	tests := []struct {
		name      string
		target    string
		allowlist []string
		wantErr   error
	}{
		{name: "https URL allowed", target: "https://api.example.com/v1", wantErr: nil},
		{name: "http URL with port allowed", target: "http://10.77.0.5:8080", wantErr: nil},
		{name: "empty target rejected", target: "", wantErr: ErrTargetRequired},
		{name: "missing scheme rejected", target: "api.example.com", wantErr: ErrInvalidTargetScheme},
		{name: "file scheme rejected", target: "file:///etc/passwd", wantErr: ErrInvalidTargetScheme},
		{name: "ftp scheme rejected", target: "ftp://api.example.com", wantErr: ErrInvalidTargetScheme},
		{name: "scheme-only URL rejected", target: "http://", wantErr: ErrTargetRequired},
		{name: "loopback IPv4 rejected", target: "http://127.0.0.1:8006", wantErr: ErrInvalidTargetHost},
		{name: "loopback IPv6 rejected", target: "http://[::1]:80", wantErr: ErrInvalidTargetHost},
		{name: "link-local IPv4 rejected", target: "http://169.254.169.254", wantErr: ErrInvalidTargetHost},
		{name: "link-local IPv6 rejected", target: "http://[fe80::1]", wantErr: ErrInvalidTargetHost},
		{name: "unspecified address rejected", target: "http://0.0.0.0:8080", wantErr: ErrInvalidTargetHost},
		{name: "localhost hostname rejected", target: "http://localhost:8080", wantErr: ErrInvalidTargetHost},
		{name: "localhost subdomain rejected", target: "http://api.localhost:8080", wantErr: ErrInvalidTargetHost},
		{
			name:      "allowlist hit by hostname",
			target:    "https://API.Example.Com/v1",
			allowlist: allow,
			wantErr:   nil,
		},
		{
			name:      "allowlist hit by IP",
			target:    "http://10.1.2.3:9",
			allowlist: allow,
			wantErr:   nil,
		},
		{
			name:      "allowlist miss rejected",
			target:    "https://evil.example.com",
			allowlist: allow,
			wantErr:   ErrInvalidTargetHost,
		},
		{
			name:      "allowlist miss by IP rejected",
			target:    "http://10.4.5.6",
			allowlist: allow,
			wantErr:   ErrInvalidTargetHost,
		},
		{
			name:      "allowlist does not bypass loopback rejection",
			target:    "http://127.0.0.1:8006",
			allowlist: []string{"127.0.0.1"},
			wantErr:   ErrInvalidTargetHost,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateTarget(tt.target, tt.allowlist)
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("ValidateTarget(%q, %v) error = %v, want %v", tt.target, tt.allowlist, err, tt.wantErr)
			}
		})
	}
}

func TestProxyPath(t *testing.T) {
	tests := []struct {
		name  string
		integ *Integration
		want  string
	}{
		{
			name:  "returns proxy path",
			integ: &Integration{Name: "myapi"},
			want:  "/proxy/myapi/",
		},
		{
			name:  "empty name returns empty path",
			integ: &Integration{Name: ""},
			want:  "",
		},
		{
			name:  "nil returns empty",
			integ: nil,
			want:  "",
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
