// ABOUTME: Helpers for validating tailnet subnet routing availability from the client.

package main

import (
	"context"
	"fmt"
	"net"
	"os/exec"
	"runtime"
	"strings"
)

type tailnetRouteCheck struct {
	Subnet string `json:"subnet,omitempty"`
	Target string `json:"target,omitempty"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

func checkTailnetRoute(ctx context.Context, subnet string) tailnetRouteCheck {
	target, err := subnetProbeIP(subnet)
	if err != nil {
		return tailnetRouteCheck{
			Subnet: strings.TrimSpace(subnet),
			Status: "unknown",
			Detail: fmt.Sprintf("invalid subnet %q: %v", strings.TrimSpace(subnet), err),
		}
	}
	return checkTailnetRouteToIP(ctx, target, subnet)
}

func checkTailnetRouteToIP(ctx context.Context, ip net.IP, subnet string) tailnetRouteCheck {
	check := tailnetRouteCheck{Subnet: strings.TrimSpace(subnet)}
	if check.Subnet == "" {
		check.Subnet = defaultAgentSubnet
	}
	if ip == nil {
		check.Status = "unknown"
		check.Detail = "invalid target IP"
		return check
	}
	check.Target = ip.String()
	if !ip.IsPrivate() {
		check.Status = "ok"
		check.Detail = "target is public"
		return check
	}
	if runtime.GOOS != "linux" {
		check.Status = "unknown"
		check.Detail = fmt.Sprintf("unable to verify tailnet route on %s; ensure the agent subnet route (%s) is enabled", runtime.GOOS, check.Subnet)
		return check
	}
	path, err := exec.LookPath("ip")
	if err != nil {
		check.Status = "unknown"
		check.Detail = fmt.Sprintf("unable to verify tailnet route (missing ip command); ensure the agent subnet route (%s) is enabled", check.Subnet)
		return check
	}
	args := []string{"-4", "route", "get", ip.String()}
	if ip.To4() == nil {
		args[0] = "-6"
	}
	ctx, cancel := context.WithTimeout(ctx, routeCheckTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, args...).Output()
	if err != nil {
		check.Status = "warn"
		check.Detail = fmt.Sprintf("no route to %s detected; enable the agent subnet route (%s)", ip.String(), check.Subnet)
		return check
	}
	info := parseIPRouteGet(string(out))
	if info.Device == "" {
		check.Status = "unknown"
		check.Detail = fmt.Sprintf("unable to determine route to %s; ensure the agent subnet route (%s) is enabled", ip.String(), check.Subnet)
		return check
	}
	if strings.HasPrefix(info.Device, tailscaleInterfaceID) {
		check.Status = "ok"
		if info.Via != "" {
			check.Detail = fmt.Sprintf("via %s (%s)", info.Device, info.Via)
		} else {
			check.Detail = fmt.Sprintf("via %s", info.Device)
		}
		return check
	}
	check.Status = "warn"
	if info.Via != "" {
		check.Detail = fmt.Sprintf("route to %s goes via %s on %s, not Tailscale; enable the agent subnet route (%s)", ip.String(), info.Via, info.Device, check.Subnet)
	} else {
		check.Detail = fmt.Sprintf("route to %s goes via %s, not Tailscale; enable the agent subnet route (%s)", ip.String(), info.Device, check.Subnet)
	}
	return check
}

func formatTailnetRouteCheck(check tailnetRouteCheck) string {
	status := strings.TrimSpace(check.Status)
	if status == "" {
		status = "unknown"
	}
	switch status {
	case "ok":
		if check.Detail != "" {
			return fmt.Sprintf("ok (%s)", check.Detail)
		}
		if check.Subnet != "" {
			return fmt.Sprintf("ok (%s)", check.Subnet)
		}
		return "ok"
	case "warn":
		if check.Detail != "" {
			return "warning: " + check.Detail
		}
		if check.Subnet != "" {
			return fmt.Sprintf("warning: subnet %s not reachable", check.Subnet)
		}
		return "warning"
	default:
		if check.Detail != "" {
			return "note: " + check.Detail
		}
		if check.Subnet != "" {
			return fmt.Sprintf("note: unable to verify route for %s", check.Subnet)
		}
		return "note: unable to verify route"
	}
}

func subnetProbeIP(cidr string) (net.IP, error) {
	cidr = strings.TrimSpace(cidr)
	if cidr == "" {
		cidr = defaultAgentSubnet
	}
	ip, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil, err
	}
	if ip4 := ip.To4(); ip4 != nil {
		probe := append(net.IP(nil), ip4...)
		incrementIPv4(probe)
		return probe, nil
	}
	if ipnet == nil || ipnet.IP == nil {
		return nil, fmt.Errorf("invalid subnet")
	}
	probe := append(net.IP(nil), ipnet.IP...)
	return probe, nil
}

func incrementIPv4(ip net.IP) {
	if len(ip) < 4 {
		return
	}
	for i := len(ip) - 1; i >= 0; i-- {
		ip[i]++
		if ip[i] != 0 {
			break
		}
	}
}
