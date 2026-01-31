# ACTUAL PROXMOX TESTING - AgentLab v0.1.0

**Date**: 2026-01-30 (full Proxmox testing)
**Environment**: Proxmox VE 8.4.16 host (pve, <internal-ip>/24)
**Test Status**: Partial success - infrastructure works, sandbox provisioning fails

---

## What Was Tested

### ✅ Phase 1: Infrastructure Setup
- ✅ Proxmox tools available (`qm`, `pvesh`, `pvesm`)
- ✅ Storage pools ready: `local` (987 GB), `local-zfs` (987 GB)
- ✅ `vmbr0` bridge exists (<internal-ip>/24)
- ✅ `vmbr1` bridge created successfully (<gateway-ip>/16)
- ✅ IPv4 forwarding enabled via sysctl
- ✅ nftables rules applied (NAT + RFC1918/ULA blocks + tailnet blocks)
- ✅ Binaries built successfully (Go 1.23.5)
- ✅ Daemon installed and starts successfully
- ✅ CLI communicates via Unix socket
- ✅ Ubuntu 24.04 cloud image downloaded (597 MB)
- ✅ VM template created successfully (VMID 9000, 40 GB disk)
- ✅ `qm clone` command works (tested manually)

### ⚠️ Phase 2: Sandbox Provisioning
- ❌ Sandbox creation fails silently: `{"error":"failed to provision sandbox"}`
- ❌ Database shows sandboxes stuck in `PROVISIONING` state
- ❌ No VMs created in Proxmox after API requests
- ❌ Timeout/cleanup occurs after ~10 minutes (default provision timeout)

---

## Infrastructure Test Results

### Network Setup

**vmbr1 bridge:**
```bash
$ ip addr show vmbr1
5: vmbr1: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1500 qdisc noqueue state UNKNOWN
    inet <gateway-ip>/16 scope global vmbr1
```

**nftables rules:**
```
table inet agentlab {
  chain forward {
    type filter hook forward priority -100; policy accept;
    ct state established,related return
    iifname "tailscale0" oifname "vmbr1" accept
    iifname "vmbr1" ip saddr <network-ip>/16 ip daddr <network-ip>/10 drop
    iifname "vmbr1" ip6 daddr <tailscale-ipv6> drop
    iifname "vmbr1" ip saddr <network-ip>/16 ip daddr { 10.0.0.0/8, <network-ip>/16, <network-ip>/12, <network-ip>/16 } drop
    iifname "vmbr1" ip6 daddr { fc00::/7, fe80::/10 } drop
    return
  }
  table ip agentlab_nat {
    chain postrouting {
      type nat hook postrouting priority srcnat; policy accept;
      ip saddr <network-ip>/16 oifname "vmbr0" masquerade
    }
  }
}
```

**Assessment**: Network setup is **perfect**. The rules implement the security model from spec (NAT to WAN, block RFC1918/ULA, allow Tailscale inbound).

---

### Template Creation

**Template created successfully:**
```bash
$ /root/agentlab/scripts/create_template.sh --skip-customize
[create_template] Creating VM 9000 (agentlab-ubuntu-2404)
[create_template] Importing disk to storage local-zfs
[create_template] Converting VM 9000 to template
[create_template] Template created: VMID 9000
```

**Template config:**
```bash
$ qm config 9000
agent: enabled=1
boot: order=scsi0
cores: 2
ide2: local-zfs:vm-9000-cloudinit,media=cdrom
ipconfig0: ip=dhcp
memory: 4096
name: agentlab-ubuntu-2404
net0: virtio=BC:24:11:F8:15:9E,bridge=vmbr1
scsi0: local-zfs:base-9000-disk-0,size=40G
scsihw: virtio-scsi-pci
serial0: socket
template: 1
vga: serial0
```

**Assessment**: Template creation is **excellent**. The template has correct network bridge (vmbr1), cloud-init enabled, QEMU guest agent enabled.

---

### Daemon & CLI Communication

**Daemon startup:**
```bash
$ systemctl status agentlabd.service
● agentlabd.service - AgentLab daemon
     Loaded: loaded (/etc/systemd/system/agentlabd.service; enabled; preset: enabled)
     Active: active (running) since Fri 2026-01-30 21:00:31 CET
      Memory: 6.9M
```

**CLI test:**
```bash
$ agentlab --version
version=dev commit=none date=2026-01-30T19:44:36Z

$ agentlab sandbox list
VMID  NAME  PROFILE  STATE  IP  LEASE
```

**Socket check:**
```bash
$ ls -la /run/agentlab/agentlabd.sock
srw-rw---- 1 root root
```

**Assessment**: Daemon and CLI communication is **excellent**. Clean startup, version info embedded, socket created with correct permissions.

---

## Sandbox Provisioning Issue

### Problem

```bash
$ agentlab --json sandbox new --profile yolo-ephemeral --name test-sandbox
{"error":"failed to provision sandbox"}
```

**No detailed error message** - only "failed to provision sandbox". No logs written to `/var/log/agentlab/agentlabd.log` after daemon startup.

### Database Analysis

```bash
$ echo "SELECT * FROM sandboxes;" | sqlite3 /var/lib/agentlab/agentlab.db
```

**Results:**
```
1000|test-sandbox|yolo-ephemeral|PROVISIONING|||0|2026-01-30T23:02:48.281916842Z|...
1001|test-sandbox|yolo-ephemeral|PROVISIONING|||0|2026-01-30T23:03:33.727634319Z|...
1002|test|yolo-ephemeral|PROVISIONING|||0|2026-01-30T23:03:38.93699062Z|...
1003|test-sandbox|yolo-ephemeral|PROVISIONING|||0|2026-01-30T23:03:47.832707183Z|...
...
```

**Observation**: All sandbox records are stuck in `PROVISIONING` state with no VMID assigned (null in database). No IP address, no workspace_id.

### Manual Clone Test

```bash
$ qm clone 9000 1000 --full 0 --name test-sandbox
create full clone of drive ide2 (local-zfs:vm-9000-cloudinit)
create linked clone of drive scsi0 (local-zfs:base-9000-disk-0)
```

```bash
$ qm list
      VMID NAME                 STATUS     MEM(MB)    BOOTDISK(GB)
      9000 agentlab-ubuntu-2404 stopped    4096              40.00
      1000 test-sandbox         stopped    4096              40.00
```

**Observation**: Manual `qm clone` works perfectly. The Proxmox backend can clone templates successfully.

### Root Cause Analysis

**Hypothesis**: The issue is likely in the `ProvisionSandbox` workflow in `job_orchestrator.go`. The sequence is:

1. Create sandbox record → `PROVISIONING` state ✅ (works)
2. `backend.Clone()` → Calls `qm clone` ✅ (works manually)
3. `CreateBootstrapToken()` → Generate token ✅ (should work)
4. `snippetStore.Create()` → Create cloud-init snippet ⚠️ (possible issue)
5. `backend.Configure()` → Set cloud-init ref, network, etc. ⚠️ (possible issue)
6. `sandboxManager.Transition()` → Boot VM ⚠️ (possible issue)

**Specific issues observed:**

1. **Missing snippet directory**: `/var/lib/vz/snippets/` didn't exist initially. Created it manually, but sandbox creation still fails.

2. **Template lacks agent-runner**: Used `--skip-customize` to speed up testing, so the template doesn't have `agent-runner` service. This shouldn't prevent VM provisioning, but may affect later stages.

3. **No detailed error logs**: The daemon logs nothing about the failure. The only error message is from the API handler (`failed to provision sandbox`), which wraps the underlying error without logging it.

4. **Provision timeout**: Sandboxes stay in `PROVISIONING` state for ~10 minutes, then timeout/cleanup occurs. This suggests the workflow gets stuck or fails silently.

### Debugging Steps Taken

1. ✅ Created `/var/lib/vz/snippets/` directory
2. ✅ Added `controller_url` to config
3. ✅ Restarted daemon after each config change
4. ✅ Verified template VMID (9000) exists and is template
5. ✅ Verified `qm clone` works manually
6. ❌ Checked `/var/log/agentlab/agentlabd.log` - no new entries
7. ❌ Checked `journalctl -u agentlabd.service` - no error messages
8. ❌ Attempted to create 7 sandboxes - all failed identically

---

## Configuration Used

### /etc/agentlab/config.yaml
```yaml
ssh_public_key_path: /etc/agentlab/keys/agentlab_id_ed25519.pub
secrets_bundle: default
bootstrap_listen: <gateway-ip>:8844
artifact_listen: <gateway-ip>:8846
controller_url: http://<gateway-ip>:8844
```

### /etc/agentlab/profiles/test.yaml
```yaml
name: minimal
template_vmid: 9000
```

### /etc/agentlab/profiles/defaults.yaml
```yaml
# Contains 3 profiles: yolo-ephemeral, yolo-workspace, interactive-dev
# Daemon reports "loaded 2 profiles" (issue noted in RUNTIME_TESTING.md)
```

---

## What Works

| Component | Status | Notes |
|-----------|--------|-------|
| Proxmox tools | ✅ | `qm`, `pvesh`, `pvesm` available |
| Storage pools | ✅ | `local` and `local-zfs` ready |
| vmbr1 bridge | ✅ | <gateway-ip>/16, IPv4 forwarding enabled |
| nftables rules | ✅ | NAT, RFC1918/ULA blocks, tailnet blocks applied |
| Binaries | ✅ | Built with Go 1.23.5 |
| Daemon startup | ✅ | Runs successfully, version info embedded |
| CLI communication | ✅ | Unix socket works, table output formatted |
| Template creation | ✅ | VMID 9000 template created successfully |
| Manual clone | ✅ | `qm clone 9000 <newid>` works |
| Profiles loaded | ✅ | 2 of 3 profiles loaded (multi-doc YAML issue) |

## What Doesn't Work

| Component | Status | Root Cause |
|-----------|--------|------------|
| Sandbox provisioning | ❌ | Unknown - fails silently |
| Cloud-init snippet creation | ❓ | Likely issue - /var/lib/vz/snippets/ now exists |
| VM boot via agentlabd | ❓ | Likely issue - manual clone works but API doesn't |
| Detailed error logging | ❌ | API returns generic error, daemon logs nothing |

---

## Potential Bug Analysis

### Bug #1: Multi-Document YAML Parsing

**Location**: Profile loading in daemon startup
**Symptom**: `defaults.yaml` has 3 profiles but daemon reports "loaded 2 profiles"
**Impact**: Third profile (`interactive-dev`) may be unavailable
**Severity**: Medium

### Bug #2: Generic Error Messages

**Location**: `internal/daemon/api.go:688`
**Symptom**: `writeError(w, http.StatusInternalServerError, "failed to provision sandbox")`
**Impact**: No debugging info for users or developers
**Severity**: High
**Recommendation**: Log the actual error from `ProvisionSandbox()` before wrapping it in generic message

### Bug #3: Silent Provisioning Failures

**Location**: `internal/daemon/job_orchestrator.go:ProvisionSandbox()`
**Symptom**: Workflow gets stuck or fails without logging errors
**Impact**: Unusable sandbox creation, no debugging info
**Severity**: Critical
**Recommendation**: Add structured logging at each step of ProvisionSandbox workflow

---

## Recommendations for v0.2.0

### Critical
1. **Add detailed logging to ProvisionSandbox** - Log each step (clone, token, snippet, configure, boot) with success/failure
2. **Expose actual errors in API responses** - Don't wrap errors in generic messages
3. **Debug profile loading** - Verify multi-document YAML parsing loads all profiles

### High Priority
4. **Add verbose/debug logging mode** - Allow users to see detailed operations
5. **Validate snippets directory exists** - Create it during install_host.sh if missing
6. **Add health check endpoint** - `/v1/healthz` to verify daemon, backend, storage are ready

### Medium Priority
7. **Document --skip-customize limitations** - Explain that template won't have agent-runner
8. **Better error messages for timeout** - Distinguish between VM boot timeout vs. provision timeout
9. **Add metrics for provision failures** - Track success/failure rates

---

## Comparison: Static Analysis vs. Proxmox Testing

| Observation | Static Analysis | Proxmox Testing | Status |
|-------------|-----------------|-------------------|--------|
| Binary builds | ✅ | ✅ | Confirmed |
| Daemon starts | ✅ | ✅ | Confirmed |
| CLI communication | ✅ | ✅ | Confirmed |
| Network setup | Not tested | ✅ | Confirmed |
| Template creation | Not tested | ✅ | Confirmed |
| Sandbox provisioning | Assumed working | ❌ | **BLOCKER** |
| VM boot | Not tested | ❓ | Can't test without provisioning |
| Guest bootstrap | Not tested | ❓ | Can't test without VM boot |
| Agent execution | Not tested | ❓ | Can't test without guest |

---

## Conclusion

**AgentLab v0.1.0 infrastructure is excellent** - network, daemon, CLI, template creation all work perfectly.

**However, sandbox provisioning is broken** in the current build. The workflow fails silently with no useful error messages, making debugging impossible without code changes.

**Root cause**: Unknown - likely in the ProvisionSandbox workflow (snippet creation or VM boot), but logs don't show the actual failure point.

**Recommendation**: This is a **critical blocker** for alpha testing. Needs investigation and fixes before v0.2.0. The infrastructure is ready, but the core feature (sandbox provisioning) doesn't work.

---

## Testing Commands Executed

```bash
# Infrastructure
systemctl enable --now agentlabd.service
/root/agentlab/scripts/net/setup_vmbr1.sh --apply
/root/agentlab/scripts/net/apply.sh --apply

# Template
/root/agentlab/scripts/create_template.sh --skip-customize

# Manual clone test
qm clone 9000 1000 --full 0 --name test-sandbox
qm destroy 1000

# Failed sandbox attempts (7 attempts)
agentlab --json sandbox new --profile yolo-ephemeral --name test
agentlab --json sandbox new --profile yolo-ephemeral --name test2
# ... (all failed)

# Debugging
echo "SELECT * FROM sandboxes;" | sqlite3 /var/lib/agentlab/agentlab.db
tail -50 /var/log/agentlab/agentlabd.log
journalctl -u agentlabd.service -n 100
```
