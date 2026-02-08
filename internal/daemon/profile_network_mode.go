package daemon

import (
	"fmt"
	"strings"
)

const (
	networkModeOff       = "off"
	networkModeNat       = "nat"
	networkModeAllowlist = "allowlist"

	defaultNetworkMode = networkModeNat
)

const (
	firewallGroupNetOff       = "agent_nat_off"
	firewallGroupNatDefault   = "agent_nat_default"
	firewallGroupNatAllowlist = "agent_nat_allowlist"
)

var networkModeOrder = []string{
	networkModeOff,
	networkModeNat,
	networkModeAllowlist,
}

var networkModeFirewallGroups = map[string]string{
	networkModeOff:       firewallGroupNetOff,
	networkModeNat:       firewallGroupNatDefault,
	networkModeAllowlist: firewallGroupNatAllowlist,
}

func normalizeNetworkMode(value string) (string, error) {
	mode := strings.ToLower(strings.TrimSpace(value))
	switch mode {
	case networkModeOff, networkModeNat, networkModeAllowlist:
		return mode, nil
	case "":
		return "", fmt.Errorf("network.mode must be one of off, nat, allowlist")
	default:
		return "", fmt.Errorf("network.mode %q is invalid (valid: off, nat, allowlist)", value)
	}
}

func networkModeForFirewallGroup(group string) (string, bool) {
	for mode, mapped := range networkModeFirewallGroups {
		if mapped == group {
			return mode, true
		}
	}
	return "", false
}

func resolveNetworkMode(spec profileNetworkSpec) (string, error) {
	if spec.Mode != nil {
		return normalizeNetworkMode(*spec.Mode)
	}
	if spec.FirewallGroup != nil {
		group, err := normalizeFirewallGroup(*spec.FirewallGroup)
		if err != nil {
			return "", err
		}
		if mode, ok := networkModeForFirewallGroup(group); ok {
			return mode, nil
		}
	}
	return defaultNetworkMode, nil
}

func resolveFirewallGroup(spec profileNetworkSpec) (string, error) {
	if spec.Mode != nil {
		mode, err := normalizeNetworkMode(*spec.Mode)
		if err != nil {
			return "", err
		}
		expected := networkModeFirewallGroups[mode]
		if spec.FirewallGroup != nil {
			group, err := normalizeFirewallGroup(*spec.FirewallGroup)
			if err != nil {
				return "", err
			}
			if group != expected {
				return "", fmt.Errorf("network.mode %q requires network.firewall_group %q (got %q)", mode, expected, group)
			}
		}
		return expected, nil
	}
	if spec.FirewallGroup != nil {
		group, err := normalizeFirewallGroup(*spec.FirewallGroup)
		if err != nil {
			return "", err
		}
		return group, nil
	}
	return "", nil
}
