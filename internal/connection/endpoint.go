package connection

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

func NormalizeEndpoint(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", nil
	}
	if !strings.Contains(trimmed, "://") {
		// Require an explicit scheme so a bare host:port never silently selects
		// plaintext HTTP, which would attach bearer credentials in cleartext
		// (review M8).
		return "", fmt.Errorf("endpoint %q must include an explicit scheme (http:// or https://); "+
			"bare host:port is rejected to avoid silently using plaintext HTTP", raw)
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("invalid endpoint %q", raw)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("endpoint scheme must be http or https")
	}
	if parsed.User != nil {
		return "", fmt.Errorf("endpoint must not contain credentials")
	}
	if parsed.Host == "" {
		return "", fmt.Errorf("endpoint must include host")
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return "", fmt.Errorf("endpoint must not include a path")
	}
	parsed.Path = ""
	parsed.RawQuery = ""
	parsed.Fragment = ""
	endpoint := strings.TrimRight(parsed.String(), "/")
	return endpoint, nil
}

// validateEndpointPolicy enforces the transport-security policy for a resolved
// endpoint (review M8): plaintext HTTP to a non-loopback host is allowed only
// with an explicit acknowledgement, since bearer credentials would otherwise
// travel in cleartext outside a trusted tunnel. HTTPS and loopback HTTP are
// always allowed.
func ValidateEndpointPolicy(endpoint string, allowInsecureHTTP bool) error {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return nil
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return fmt.Errorf("invalid endpoint %q", endpoint)
	}
	if strings.EqualFold(parsed.Scheme, "https") {
		return nil
	}
	if IsLoopbackHost(parsed.Hostname()) {
		return nil
	}
	if !allowInsecureHTTP {
		return fmt.Errorf("endpoint %q uses plaintext HTTP to a non-loopback host; "+
			"pass --allow-insecure-http (or set %s=1) only inside a trusted tunnel such as Tailscale",
			endpoint, "AGENTLAB_ALLOW_INSECURE_HTTP")
	}
	return nil
}

// isLoopbackHost reports whether host is a loopback address or name.
func IsLoopbackHost(host string) bool {
	host = strings.TrimSpace(host)
	if host == "localhost" {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	// Strip a bracketed IPv6 form just in case.
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		if ip := net.ParseIP(host[1 : len(host)-1]); ip != nil {
			return ip.IsLoopback()
		}
	}
	return false
}
