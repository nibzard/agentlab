// Package integrations provides network-level secret injection for sandboxes.
//
// Integrations allow secrets (API keys, tokens, credentials) to be injected
// into sandbox requests at the network layer so secrets never exist inside
// the VM. Supported integration types are:
//   - http-proxy: Intercept HTTP requests, inject headers/tokens, forward to target
//   - git-proxy: Proxy git clone/push through a gateway, inject credentials
//   - llm-proxy: Proxy LLM API requests (OpenAI-compatible) to configured providers
//
// Integrations can be attached to sandboxes by:
//   - Specific sandbox: --attach=sandbox:mybox
//   - Tag: --attach=tag:production
//   - Auto: --attach=auto:all (applies to all sandboxes)
package integrations

import (
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"
)

// IntegrationType defines the type of integration.
type IntegrationType string

const (
	// TypeHTTPProxy is an HTTP reverse proxy that injects headers/tokens into requests.
	TypeHTTPProxy IntegrationType = "http-proxy"
	// TypeGitProxy is a git credential proxy that injects credentials into clone/push.
	TypeGitProxy IntegrationType = "git-proxy"
	// TypeLLMProxy is an LLM API proxy that forwards OpenAI-compatible requests
	// to the configured provider (OpenAI, Anthropic, Ollama) with daemon-held credentials.
	TypeLLMProxy IntegrationType = "llm-proxy"
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
//   - Type: Integration type (http-proxy, git-proxy, llm-proxy)
//   - Target: Target URL for proxy (e.g., "https://api.example.com")
//   - Secret: The secret value (API key, token, password) - encrypted at rest
//   - SecretType: How the secret is injected (bearer, header, basic-auth)
//   - SecretHeader: Custom header name for header-type secrets
//   - Provider: LLM provider name for llm-proxy type (openai, anthropic, ollama; auto-detected from target if empty)
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
	Provider       string // LLM provider for llm-proxy: "openai", "anthropic", "ollama"
	AttachMode     AttachmentMode
	AttachSelector string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// IntegrationStatus describes the status of an integration for a sandbox.
type IntegrationStatus struct {
	Name       string          `json:"name"`
	Type       IntegrationType `json:"type"`
	Target     string          `json:"target,omitempty"`
	AttachMode AttachmentMode  `json:"attach_mode"`
	ProxyPath  string          `json:"proxy_path,omitempty"`
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
// For LLM proxy: /proxy/{name}/v1/...
func (i *Integration) ProxyPath() string {
	if i == nil || i.Name == "" {
		return ""
	}
	return "/proxy/" + i.Name + "/"
}

// DetectProvider auto-detects the LLM provider from the target URL.
// Returns "openai", "anthropic", or "ollama" based on the hostname.
// If the Provider field is already set, it is returned as-is.
func (i *Integration) DetectProvider() string {
	if i == nil {
		return ""
	}
	if i.Provider != "" {
		return i.Provider
	}
	if i.Target == "" {
		return ""
	}
	host := i.Target
	// Strip scheme.
	if idx := indexOf(host, "://"); idx >= 0 {
		host = host[idx+3:]
	}
	// Strip port and path.
	for _, sep := range []string{"/", ":"} {
		if idx := indexOf(host, sep); idx >= 0 {
			host = host[:idx]
		}
	}
	switch {
	case containsStr(host, "api.openai.com"):
		return "openai"
	case containsStr(host, "api.anthropic.com"):
		return "anthropic"
	default:
		// Assume Ollama or any OpenAI-compatible endpoint.
		return "ollama"
	}
}

func indexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

func containsStr(s, substr string) bool {
	return indexOf(s, substr) >= 0
}

// ValidateTarget checks that a proxy target URL is safe to forward to.
//
// The target must use the http or https scheme, must carry a host, and
// must not point at a loopback, link-local, or unspecified address. Such
// targets would let a sandbox reach daemon-side or host-local services
// through the proxy.
//
// When allowlist is non-empty, the host (hostname or literal IP, port
// excluded) must match one of its entries. Matching is case-insensitive;
// literal IPs are compared by value, so an entry may use a different
// notation than the target. An empty allowlist disables this check.
func ValidateTarget(target string, allowlist []string) error {
	target = strings.TrimSpace(target)
	if target == "" {
		return ErrTargetRequired
	}
	u, err := url.Parse(target)
	if err != nil {
		return fmt.Errorf("%w: %q", ErrInvalidTargetScheme, target)
	}
	switch u.Scheme {
	case "http", "https":
		// valid
	default:
		return fmt.Errorf("%w: %q", ErrInvalidTargetScheme, target)
	}
	host := u.Hostname()
	if host == "" {
		return ErrTargetRequired
	}

	allowed := len(allowlist) == 0
	for _, entry := range allowlist {
		entry = strings.ToLower(strings.TrimSpace(entry))
		if entry == "" {
			continue
		}
		if ip := net.ParseIP(entry); ip != nil {
			if tip := net.ParseIP(host); tip != nil && ip.Equal(tip) {
				allowed = true
				break
			}
			continue
		}
		if entry == strings.ToLower(host) {
			allowed = true
			break
		}
	}
	if !allowed {
		return fmt.Errorf("%w: %q is not in the target allowlist", ErrInvalidTargetHost, host)
	}

	if ip := net.ParseIP(host); ip != nil {
		if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
			return fmt.Errorf("%w: %s", ErrInvalidTargetHost, host)
		}
		return nil
	}
	name := strings.ToLower(host)
	if name == "localhost" || strings.HasSuffix(name, ".localhost") {
		return fmt.Errorf("%w: %s", ErrInvalidTargetHost, host)
	}
	return nil
}

// Validate checks that the integration configuration is valid.
func (i *Integration) Validate() error {
	if i.Name == "" {
		return ErrNameRequired
	}
	switch i.Type {
	case TypeHTTPProxy, TypeGitProxy, TypeLLMProxy:
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
	// LLM proxy requires a target URL (the upstream API base URL).
	if i.Type == TypeLLMProxy && i.Target == "" {
		return ErrTargetRequired
	}
	// A set target must be a safe upstream for the proxy to forward to.
	if i.Target != "" {
		if err := ValidateTarget(i.Target, nil); err != nil {
			return err
		}
	}
	// LLM proxy secret is optional for local providers (e.g., Ollama without auth).
	if i.Secret == "" && i.Type != TypeLLMProxy {
		return ErrSecretRequired
	}
	// Validate provider field for LLM proxy.
	if i.Type == TypeLLMProxy && i.Provider != "" {
		switch i.Provider {
		case "openai", "anthropic", "ollama":
			// valid
		default:
			return ErrInvalidProvider
		}
	}
	switch i.SecretType {
	case "bearer", "header", "basic-auth", "":
		// valid (empty defaults to "bearer")
	default:
		return ErrInvalidSecretType
	}
	return nil
}
