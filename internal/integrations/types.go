// Package integrations provides network-level secret injection for sandboxes.
//
// Integrations allow secrets (API keys, tokens, credentials) to be injected
// into sandbox requests at the network layer so secrets never exist inside
// the VM. Supported integration types are:
//   - http-proxy: Intercept HTTP requests, inject headers/tokens, forward to target
//   - git-proxy: Proxy git clone/push through a gateway, inject credentials
//
// Integrations can be attached to sandboxes by:
//   - Specific sandbox: --attach=sandbox:mybox
//   - Tag: --attach=tag:production
//   - Auto: --attach=auto:all (applies to all sandboxes)
package integrations

import "time"

// IntegrationType defines the type of integration.
type IntegrationType string

const (
	// TypeHTTPProxy is an HTTP reverse proxy that injects headers/tokens into requests.
	TypeHTTPProxy IntegrationType = "http-proxy"
	// TypeGitProxy is a git credential proxy that injects credentials into clone/push.
	TypeGitProxy IntegrationType = "git-proxy"
)

// AttachmentMode defines how an integration is attached to sandboxes.
type AttachmentMode string

const (
	// AttachSandbox attaches to a specific sandbox by name.
	AttachSandbox AttachmentMode = "sandbox"
	// AttachTag attaches to all sandboxes with a given tag/label.
	AttachTag AttachmentMode = "tag"
	// AttachAutoAll attaches to all sandboxes automatically.
	AttachAutoAll AttachmentMode = "auto:all"
)

// Integration represents a named integration configuration.
//
// Fields:
//   - ID: Unique identifier (auto-generated)
//   - Name: Human-readable name (unique, e.g., "myapi")
//   - Type: Integration type (http-proxy, git-proxy)
//   - Target: Target URL for HTTP proxy (e.g., "https://api.example.com")
//   - Secret: The secret value (API key, token, password) - encrypted at rest
//   - SecretType: How the secret is injected (bearer, header, basic-auth)
//   - SecretHeader: Custom header name for header-type secrets
//   - AttachMode: How the integration is attached to sandboxes
//   - AttachSelector: Specific sandbox name or tag value
//   - CreatedAt: When the integration was created
//   - UpdatedAt: When the integration was last updated
type Integration struct {
	ID             int64
	Name           string
	Type           IntegrationType
	Target         string
	Secret         string // plaintext secret (only in memory, encrypted in DB)
	SecretType     string // "bearer", "header", "basic-auth"
	SecretHeader   string // custom header name (for SecretType="header")
	Username       string // username for basic-auth / git
	AttachMode     AttachmentMode
	AttachSelector string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// IntegrationStatus describes the status of an integration for a sandbox.
type IntegrationStatus struct {
	Name       string         `json:"name"`
	Type       IntegrationType `json:"type"`
	Target     string         `json:"target,omitempty"`
	AttachMode AttachmentMode  `json:"attach_mode"`
	ProxyPath  string         `json:"proxy_path,omitempty"`
}

// MatchesSandbox checks if this integration should apply to the given sandbox.
//
// A sandbox matches if:
//   - AttachMode is "auto:all"
//   - AttachMode is "sandbox" and AttachSelector equals the sandbox name
//   - AttachMode is "tag" and the sandbox has the tag in its labels
func (i *Integration) MatchesSandbox(sandboxName string, sandboxTags []string) bool {
	if i == nil {
		return false
	}
	switch i.AttachMode {
	case AttachAutoAll:
		return true
	case AttachSandbox:
		return i.AttachSelector == sandboxName
	case AttachTag:
		for _, tag := range sandboxTags {
			if tag == i.AttachSelector {
				return true
			}
		}
	}
	return false
}

// ProxyPath returns the URL path for proxying requests through this integration.
// For HTTP proxy: /proxy/{name}/*
// For git proxy: /proxy/{name}/*
func (i *Integration) ProxyPath() string {
	if i == nil || i.Name == "" {
		return ""
	}
	return "/proxy/" + i.Name + "/"
}

// Validate checks that the integration configuration is valid.
func (i *Integration) Validate() error {
	if i.Name == "" {
		return ErrNameRequired
	}
	switch i.Type {
	case TypeHTTPProxy, TypeGitProxy:
		// valid
	default:
		return ErrInvalidType
	}
	switch i.AttachMode {
	case AttachSandbox, AttachTag, AttachAutoAll:
		// valid
	default:
		return ErrInvalidAttachMode
	}
	if i.AttachMode == AttachSandbox && i.AttachSelector == "" {
		return ErrAttachSelectorRequired
	}
	if i.AttachMode == AttachTag && i.AttachSelector == "" {
		return ErrAttachSelectorRequired
	}
	if i.Type == TypeHTTPProxy && i.Target == "" {
		return ErrTargetRequired
	}
	if i.Secret == "" {
		return ErrSecretRequired
	}
	switch i.SecretType {
	case "bearer", "header", "basic-auth", "":
		// valid (empty defaults to "bearer")
	default:
		return ErrInvalidSecretType
	}
	return nil
}
