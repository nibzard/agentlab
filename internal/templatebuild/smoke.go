package templatebuild

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/agentlab/agentlab/internal/proxmox"
)

// The smoke clone receives no bootstrap token, SSH key, job or enrollment key.
// Booting a disposable clone preserves the template's pristine machine identity.
func (w *worker) smokeTest(ctx context.Context) error {
	id := w.cfg.Build.SmokeVMID
	if id <= 0 || id == w.cfg.Build.VMID {
		return fmt.Errorf("template_boot_failed: independent smoke VMID reservation missing")
	}
	raw, err := w.run(ctx, "pvesh", "get", "/cluster/resources", "--type", "vm", "--output-format", "json")
	if err != nil {
		return err
	}
	var inventory []struct {
		VMID int `json:"vmid"`
	}
	if json.Unmarshal([]byte(raw), &inventory) != nil {
		return fmt.Errorf("cannot inspect smoke VMID inventory")
	}
	for _, vm := range inventory {
		if vm.VMID == id {
			return fmt.Errorf("template_vmid_conflict: smoke VMID occupied")
		}
	}
	w.status.SmokeResources = []string{"vm:" + strconv.Itoa(id)}
	if err = w.saveSmoke(); err != nil {
		return err
	}
	sid := strconv.Itoa(id)
	if _, err = w.run(ctx, "qm", "clone", strconv.Itoa(w.cfg.Build.VMID), sid, "--name", "agentlab-verify-"+sid, "--full", "0"); err != nil {
		return err
	}
	cfg, err := w.smokeConfig(ctx)
	if err != nil {
		return err
	}
	if !owned(cfg, w.cfg.Build.ID) {
		return fmt.Errorf("template_boot_failed: smoke clone ownership mismatch")
	}
	for key, value := range cfg {
		if diskSlotRE.MatchString(key) {
			w.status.SmokeResources = append(w.status.SmokeResources, "disk:"+value)
		}
	}
	if err = w.saveSmoke(); err != nil {
		return err
	}
	// Never start a clone with inherited client bootstrap configuration.
	if cfg["cicustom"] != "" || cfg["sshkeys"] != "" {
		return fmt.Errorf("template_boot_failed: unexpected inherited guest credentials")
	}
	if _, err = w.run(ctx, "qm", "start", sid); err != nil {
		return err
	}
	checkCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	var last string
	for {
		if err = checkCtx.Err(); err != nil {
			return fmt.Errorf("template_boot_failed: guest did not become usable (%s): %w", last, err)
		}
		raw, err = w.run(checkCtx, "qm", "guest", "exec", sid, "--", "/bin/sh", "-c", `set -eu; export PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin; for executable in tailscale agent-runner sshd qemu-ga jq curl; do command -v "$executable" >/dev/null; done; systemctl is-active --quiet ssh; systemctl is-active --quiet qemu-guest-agent; tailscale status --json | jq -e '.BackendState == "NeedsLogin"' >/dev/null; printf 'agentlab-boot-ok\n'`)
		var result struct {
			Exited   int    `json:"exited"`
			ExitCode int    `json:"exitcode"`
			Out      string `json:"out-data"`
		}
		if err == nil && json.Unmarshal([]byte(raw), &result) == nil && result.Exited == 1 && result.ExitCode == 0 && strings.Contains(result.Out, "agentlab-boot-ok") {
			ip, ipErr := w.smokeIP(checkCtx, sid)
			probe := w.probeSSH
			if probe == nil {
				probe = proxmox.ProbeSSH
			}
			if ipErr == nil && probe(checkCtx, ip) == nil {
				break
			}
			last = "guest network or SSH unavailable"
		} else {
			last = "guest agent or required guest services unavailable"
		}
		select {
		case <-checkCtx.Done():
		case <-time.After(2 * time.Second):
		}
	}
	w.record("boot_verify", "isolated clone booted; guest agent, networking, SSH, runner and unenrolled Tailscale verified")
	if err = w.cleanupSmoke(ctx); err != nil {
		return fmt.Errorf("template_boot_failed: smoke cleanup failed: %w", err)
	}
	return nil
}
func (w *worker) saveSmoke() error {
	return WriteJSON(filepath.Join(w.cfg.Directory, "status.json"), w.status)
}
func (w *worker) smokeConfig(ctx context.Context) (map[string]string, error) {
	raw, err := w.run(ctx, "qm", "config", strconv.Itoa(w.cfg.Build.SmokeVMID))
	if err != nil {
		return nil, err
	}
	cfg := map[string]string{}
	for _, line := range strings.Split(raw, "\n") {
		k, v, ok := strings.Cut(line, ":")
		if ok {
			cfg[k] = strings.TrimSpace(v)
		}
	}
	return cfg, nil
}
func (w *worker) smokeIP(ctx context.Context, id string) (string, error) {
	raw, err := w.run(ctx, "qm", "guest", "cmd", id, "network-get-interfaces")
	if err != nil {
		return "", err
	}
	var interfaces []struct {
		IPs []struct {
			Address string `json:"ip-address"`
		} `json:"ip-addresses"`
	}
	if err = json.Unmarshal([]byte(raw), &interfaces); err != nil {
		return "", err
	}
	for _, iface := range interfaces {
		for _, addr := range iface.IPs {
			ip := net.ParseIP(addr.Address)
			if ip != nil && ip.To4() != nil && ip.IsGlobalUnicast() && !ip.IsLinkLocalUnicast() {
				return ip.String(), nil
			}
		}
	}
	return "", fmt.Errorf("guest has no usable IPv4 address")
}
func (w *worker) cleanupSmoke(ctx context.Context) error {
	if len(w.status.SmokeResources) == 0 {
		return nil
	}
	sid := strconv.Itoa(w.cfg.Build.SmokeVMID)
	for {
		cfg, err := w.smokeConfig(ctx)
		if err != nil {
			raw, inspectErr := w.run(ctx, "pvesh", "get", "/cluster/resources", "--type", "vm", "--output-format", "json")
			var inventory []struct {
				VMID int `json:"vmid"`
			}
			if inspectErr == nil && json.Unmarshal([]byte(raw), &inventory) == nil {
				found := false
				for _, vm := range inventory {
					found = found || vm.VMID == w.cfg.Build.SmokeVMID
				}
				if !found {
					w.status.SmokeResources = nil
					return w.saveSmoke()
				}
			}
			return fmt.Errorf("cannot inspect recorded smoke VM %s", sid)
		}
		if !owned(cfg, w.cfg.Build.ID) {
			return fmt.Errorf("smoke VM %s is not owned by this build", sid)
		}
		known := map[string]bool{}
		for _, r := range w.status.SmokeResources {
			if strings.HasPrefix(r, "disk:") {
				v, _, _ := strings.Cut(strings.TrimPrefix(r, "disk:"), ",")
				known[v] = true
			}
		}
		for key, value := range cfg {
			if diskSlotRE.MatchString(key) {
				v, _, _ := strings.Cut(value, ",")
				if v != "none" && !known[v] {
					return fmt.Errorf("smoke VM %s contains an unrecorded disk", sid)
				}
			}
		}
		node, err := os.Hostname()
		if err != nil {
			return err
		}
		raw, err := w.run(ctx, "pvesh", "get", "/nodes/"+node+"/tasks", "--source", "active", "--vmid", sid, "--output-format", "json")
		var tasks []json.RawMessage
		if err != nil || json.Unmarshal([]byte(raw), &tasks) != nil {
			return fmt.Errorf("cannot inspect smoke VM tasks")
		}
		if cfg["lock"] == "" && len(tasks) == 0 {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	if _, err := w.run(ctx, "qm", "stop", sid); err != nil {
		return fmt.Errorf("stop smoke VM: %w", err)
	}
	if _, err := w.run(ctx, "qm", "destroy", sid, "--purge", "1"); err != nil {
		return fmt.Errorf("destroy smoke VM: %w", err)
	}
	w.status.SmokeResources = nil
	return w.saveSmoke()
}
