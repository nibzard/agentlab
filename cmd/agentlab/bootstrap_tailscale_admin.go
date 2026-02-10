// ABOUTME: Optional Tailscale Admin API integration for route approval during bootstrap.

package main

import (
	"context"
	"fmt"
	"net"
	"strings"
)

func maybeApproveTailscaleRoutes(ctx context.Context, opts bootstrapOptions, hostInfo *hostResponse, subnet string) (bootstrapStep, string) {
	step := bootstrapStep{Name: "approve_tailscale_routes"}
	cfg := opts.tailscaleAdmin
	if cfg == nil || !cfg.hasCredentials() {
		step.Status = "skipped"
		step.Detail = "tailscale admin api not configured"
		return step, manualTailscaleApprovalHint(subnet, hostInfo, opts)
	}
	hints := tailscaleDeviceHints(opts, hostInfo)
	approval, err := approveTailscaleSubnetRoute(ctx, cfg, hints, subnet)
	if err != nil {
		step.Status = "warn"
		step.Detail = err.Error()
		return step, manualTailscaleApprovalHint(subnet, hostInfo, opts)
	}
	switch approval.Status {
	case "already-approved":
		step.Status = "ok"
		step.Detail = fmt.Sprintf("route %s already approved", approval.Route)
	case "approved":
		step.Status = "ok"
		step.Detail = fmt.Sprintf("approved %s for %s", approval.Route, formatTailnetDeviceName(approval))
	case "pending":
		step.Status = "warn"
		step.Detail = fmt.Sprintf("route %s approval pending", approval.Route)
	default:
		step.Status = "warn"
		step.Detail = fmt.Sprintf("route %s approval status %s", approval.Route, approval.Status)
	}
	return step, ""
}

func tailscaleDeviceHints(opts bootstrapOptions, hostInfo *hostResponse) []string {
	hints := []string{}
	if hostInfo != nil {
		if dns := strings.TrimSpace(hostInfo.TailscaleDNS); dns != "" {
			hints = append(hints, dns)
		}
	}
	if opts.tailscaleHostname != "" {
		hints = append(hints, strings.TrimSpace(opts.tailscaleHostname))
	}
	if _, host := splitUserHost(opts.host); host != "" {
		hint := normalizeHostHint(host)
		if hint != "" {
			hints = append(hints, hint)
		}
	}
	return uniqueStrings(hints)
}

func manualTailscaleApprovalHint(subnet string, hostInfo *hostResponse, opts bootstrapOptions) string {
	subnet = strings.TrimSpace(subnet)
	if subnet == "" {
		subnet = defaultAgentSubnet
	}
	hint := ""
	if hostInfo != nil {
		hint = strings.TrimSpace(hostInfo.TailscaleDNS)
	}
	if hint == "" {
		hint = strings.TrimSpace(opts.tailscaleHostname)
	}
	if hint == "" {
		if _, host := splitUserHost(opts.host); host != "" {
			hint = normalizeHostHint(host)
		}
	}
	if hint != "" {
		return fmt.Sprintf("Approve the subnet route %s for %s in the Tailscale admin console (Routes), then ensure your client accepts routes.", subnet, hint)
	}
	return fmt.Sprintf("Approve the subnet route %s in the Tailscale admin console (Routes), then ensure your client accepts routes.", subnet)
}

func formatTailnetDeviceName(approval tailscaleRouteApproval) string {
	name := strings.TrimSpace(approval.DeviceName)
	if name == "" {
		return "device " + strings.TrimSpace(approval.DeviceID)
	}
	return name
}

func normalizeHostHint(host string) string {
	host = strings.TrimSpace(host)
	if host == "" {
		return ""
	}
	if strings.Contains(host, ":") {
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
	}
	host = strings.TrimPrefix(host, "[")
	host = strings.TrimSuffix(host, "]")
	return strings.TrimSpace(host)
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
