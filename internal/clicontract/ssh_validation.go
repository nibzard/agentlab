package clicontract

import (
	"fmt"
	"net"
	"regexp"
	"strings"
)

var sshUserPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._+-]*$`)
var sshHostPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)

func ValidateSSHUser(user string) error {
	if strings.TrimSpace(user) == "" {
		return fmt.Errorf("ssh user is required")
	}
	if !sshUserPattern.MatchString(user) {
		return fmt.Errorf("ssh user contains invalid characters")
	}
	return nil
}

func ValidateSSHHost(host string) error {
	host = strings.TrimSpace(host)
	if host == "" {
		return fmt.Errorf("ssh host is required")
	}
	if strings.HasPrefix(host, "-") {
		return fmt.Errorf("ssh host must not start with '-'")
	}
	if strings.ContainsAny(host, " \t\r\n") {
		return fmt.Errorf("ssh host must not contain whitespace")
	}
	trimmed := strings.TrimPrefix(host, "[")
	trimmed = strings.TrimSuffix(trimmed, "]")
	if ip := parseIPWithZone(trimmed); ip != nil {
		return nil
	}
	if strings.Contains(host, ":") {
		return fmt.Errorf("ssh host must not include port; use --ssh-port")
	}
	if !sshHostPattern.MatchString(host) {
		return fmt.Errorf("ssh host contains invalid characters")
	}
	return nil
}

func parseIPWithZone(value string) net.IP {
	ip := value
	if idx := strings.LastIndex(ip, "%"); idx != -1 {
		ip = ip[:idx]
	}
	return net.ParseIP(ip)
}

// SplitUserHost preserves bootstrap's accepted user@host target syntax.
func SplitUserHost(raw string) (string, string) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", ""
	}
	index := strings.LastIndex(value, "@")
	if index < 0 {
		return "", value
	}
	return value[:index], value[index+1:]
}
