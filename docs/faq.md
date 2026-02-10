# AgentLab Frequently Asked Questions

This FAQ addresses common questions about AgentLab setup, operations, troubleshooting, security, and performance.

## Table of Contents

- [Getting Started](#getting-started)
- [Operations](#operations)
- [Troubleshooting](#troubleshooting)
- [Security](#security)
- [Performance](#performance)

---

## Getting Started

### What do I need before installing AgentLab?

Before installing AgentLab, you need:

- **Proxmox VE 9.x+** host (8.x+ supported with shell backend)
- **Storage pool** suitable for VM templates and clones (ZFS or LVM-thin recommended)
- **Network bridge** `vmbr0` for LAN/WAN connectivity
- **Ability to create** `vmbr1` bridge for agent subnet (defaults to `10.77.0.0/16`)
- **Go 1.24.0+** to build binaries (auto-downloaded if needed)
- **Template VM** with qemu-guest-agent enabled

See the [README](../README.md) quickstart for detailed setup steps.

### Do I need a dedicated Proxmox host?

While not strictly required, a **dedicated Proxmox host** is strongly recommended for production use:

**Advantages of dedicated host:**
- Isolated failure domain - sandbox issues don't affect other workloads
- Predictable resource availability for sandboxes
- Simplified networking and security boundaries
- Better performance due to resource contention avoidance

**Shared host considerations:**
- Sandboxes will compete with other VMs for CPU, memory, and I/O
- Network isolation must be carefully configured
- Resource limits in profiles become more critical
- Test thoroughly under load before production use

### Can I run this without Proxmox?

**No.** AgentLab is built specifically for **Proxmox VE** and cannot run without it.

The system relies on Proxmox APIs for:
- VM provisioning and cloning
- Network bridge management
- Storage operations
- QEMU guest agent integration

**Alternatives to consider:**
- For Docker-based workflows, consider tools like Earthly or Act
- For cloud VMs, look at cloud-specific orchestration tools
- For local development, use Docker/Podman directly

---

## Operations

### How do I clean up failed sandboxes?

Failed sandboxes can accumulate over time. Clean them up using:

**List failed/timeout sandboxes:**
```bash
agentlab sandbox list --filter state=TIMEOUT
agentlab sandbox list --filter state=FAILED
```

**Remove individual failed sandboxes:**
```bash
# Normal destroy (works for most states)
agentlab sandbox destroy <vmid>

# Force destroy (for stuck or TIMEOUT sandboxes)
agentlab sandbox destroy --force <vmid>
```

**Remove all orphaned sandboxes:**
```bash
# Removes sandboxes that exist in DB but not Proxmox
agentlab sandbox prune
```

**Automated cleanup:**
```bash
# Add to crontab for weekly cleanup
0 2 * * 0 root /usr/local/bin/agentlab sandbox prune
```

See also: [Troubleshooting - Sandbox Operations](troubleshooting.md#sandbox-operations)

### What happens when the daemon restarts?

When `agentlabd` restarts:

1. **State preservation**: All sandbox and job records persist in SQLite database
2. **State reconciliation**: The reconciler syncs database state with actual Proxmox VM states
3. **Lease GC resumes**: Expired lease enforcement continues
4. **In-flight operations**:
   - Jobs in QUEUED state remain queued
   - Jobs in RUNNING state continue running in VMs
   - Provisioning sandboxes may transition to TIMEOUT if startup completes
   - Artifact uploads may fail if interrupted

**Manual reconciliation after restart:**
```bash
# Check if reconciliation worked correctly
agentlab sandbox list

# If issues persist, force reconciliation
systemctl restart agentlabd
```

### What persists between sandboxes?

**Short answer:** the workspace persists, the sandbox root does not.

- **Sandbox root** is ephemeral. Destroying or reverting a sandbox resets the root filesystem.
- **Workspace** is the durable `/work` volume. It survives sandbox destroy/restart and can be reattached.
- **Session** records the workspace/profile and the current sandbox VMID; it survives sandbox replacement.

See `docs/state-model.md` for diagrams and stateful workflows.

### What is a session and how do I resume it?

A **session** binds a persistent workspace to a current sandbox and a profile.
When you resume, AgentLab provisions a fresh sandbox and rebinds the workspace.

**Create a session:**
```bash
agentlab session create \
  --name dev-session \
  --profile yolo-workspace \
  --workspace new:dev-workspace \
  --workspace-size 80G
```

**Resume (new sandbox + rebind workspace):**
```bash
agentlab session resume dev-session
```

**Stop (destroy sandbox, keep workspace reserved):**
```bash
agentlab session stop dev-session
```

**List/show sessions:**
```bash
agentlab session list
agentlab session show dev-session
```

**Fork a session (new workspace, same profile by default):**
```bash
agentlab session fork dev-session --name dev-session-exp --workspace new:dev-workspace-exp
```

### How do I snapshot a workspace?

Workspace snapshots capture the persistent `/work` volume state. For safety, snapshots
require the workspace to be detached (no active sandbox attached).

**Create a snapshot:**
```bash
agentlab workspace snapshot create dev-workspace baseline
```

**List snapshots:**
```bash
agentlab workspace snapshot list dev-workspace
```

**Restore a snapshot (destructive):**
```bash
agentlab workspace snapshot restore dev-workspace baseline
```

If you want a safe experiment, use a fork instead:
```bash
agentlab workspace fork dev-workspace --name dev-workspace-exp --from-snapshot baseline
```

### Why did my sandbox stop while I was away?

AgentLab includes an **idle auto-stop** policy for RUNNING sandboxes:

- No active SSH sessions (detected via host conntrack flows to `sandbox_ip:22`)
- CPU usage stays below a low threshold for N minutes (from Proxmox `status/current`)

Defaults (in `/etc/agentlab/config.yaml`):
```yaml
idle_stop_enabled: true
idle_stop_interval: 1m
idle_stop_minutes_default: 30
idle_stop_cpu_threshold: 0.05
```

Per-profile override (set `0` to disable for that profile):
```yaml
behavior:
  idle_stop_minutes_default: 0
```

If SSH detection fails (missing `conntrack` or insufficient privileges), the
idle stop loop skips stopping that sandbox.

### How do I backup my data?

AgentLab data should be backed up regularly:

**Critical paths to backup:**
```bash
# Database (all state)
/var/lib/agentlab/agentlab.db

# Configuration
/etc/agentlab/config.yaml
/etc/agentlab/profiles/

# Secrets (encrypted)
/etc/agentlab/secrets/
/etc/agentlab/keys/age.key

# Artifacts (if important)
/var/lib/agentlab/artifacts/
```

**Automated backup script:**
```bash
#!/bin/bash
# /usr/local/bin/backup-agentlab.sh
BACKUP_DIR="/backup/agentlab"
DATE=$(date +%Y%m%d-%H%M%S)

mkdir -p "$BACKUP_DIR"

# Backup database
cp /var/lib/agentlab/agentlab.db "$BACKUP_DIR/agentlab-$DATE.db"

# Backup config and profiles
tar -czf "$BACKUP_DIR/config-$DATE.tar.gz" -C /etc agentlab/

# Backup secrets
tar -czf "$BACKUP_DIR/secrets-$DATE.tar.gz" -C /etc/agentlab secrets keys

# Retention: keep last 7 days
find "$BACKUP_DIR" -name "agentlab-*.db" -mtime +7 -delete
find "$BACKUP_DIR" -name "config-*.tar.gz" -mtime +7 -delete
find "$BACKUP_DIR" -name "secrets-*.tar.gz" -mtime +30 -delete
```

**Restore procedure:**
```bash
# Stop daemon
systemctl stop agentlabd

# Restore database
cp /backup/agentlab/agentlab-YYYYMMDD.db /var/lib/agentlab/agentlab.db

# Restore config and secrets
tar -xzf /backup/agentlab/config-YYYYMMDD.tar.gz -C /etc
tar -xzf /backup/agentlab/secrets-YYYYMMDD.tar.gz -C /etc/agentlab

# Start daemon
systemctl start agentlabd
```

---

## Troubleshooting

### Why is sandbox provisioning stuck?

Stuck provisioning usually indicates one of these issues:

**1. Template VM missing or invalid**
```bash
# Check if template exists
qm list | grep 9000

# Verify template status
qm status 9000
```

**2. Storage pool issues**
```bash
# Check storage availability
pvesm status

# Verify storage has space
df -h /var/lib/vz
```

**3. Network bridge missing**
```bash
# Verify vmbr1 exists
ip addr show vmbr1

# Check if it's configured properly
brctl show vmbr1
```

**4. Timeout too short for environment**
```bash
# Check current timeout
grep provision /etc/agentlab/config.yaml

# Increase in config.yaml
# provisioning_timeout: 15m  # default is 10m
```

**5. Proxmox API issues (if using API backend)**
```bash
# Verify API token works
pveum user token list

# Test API connectivity
curl -k https://localhost:8006/api2/json/version
```

**Check daemon logs for specific errors:**
```bash
journalctl -u agentlabd -n 100 --no-pager
```

See also: [Troubleshooting - Job Failures](troubleshooting.md#job-fails-in-provisioning)

### How do I debug network issues?

Network problems typically manifest as:
- Cannot SSH into sandboxes
- Sandboxes cannot access internet
- Artifact uploads fail

**Debug steps:**

1. **Check VM got an IP:**
```bash
agentlab sandbox show <vmid>
# Look for IP address in output
```

2. **Verify agent bridge:**
```bash
ip addr show vmbr1
# Should show 10.77.0.1/16 (or your configured subnet)
```

3. **Test connectivity from host:**
```bash
ping <vm_ip>
# Should get responses
```

4. **Check firewall rules:**
```bash
# Show NAT rules
iptables -t nat -L -n | grep 10.77

# Show egress blocks
iptables -L -n | grep EGRESS
```

5. **Verify qemu-guest-agent:**
```bash
# From Proxmox host
qm guest exec <vmid> -- ip addr
```

6. **Check Tailscale (if configured):**
```bash
tailscale status
tailscale ping <vm_ip>
```

**Common fixes:**
```bash
# Reapply network rules
sudo scripts/net/apply.sh --apply

# Restart network services
systemctl restart agentlab-nftables

# Re-create bridge if missing
sudo scripts/net/setup_vmbr1.sh --apply
```

See also: [Troubleshooting - Networking Issues](troubleshooting.md#networking-issues)

### What does "state reconciliation" do?

**State reconciliation** is the process of syncing AgentLab's database with the actual state of VMs in Proxmox.

**Why it's needed:**
- VMs can be stopped/started outside AgentLab
- Crashes can leave inconsistent state
- Network issues can cause state drift

**What it does:**
1. Queries Proxmox for all VMs managed by AgentLab
2. Compares VM states (running/stopped) with database records
3. Transitions sandboxes to correct states
4. Marks orphaned records for cleanup
5. Runs automatically every 30 seconds (default)

**Manual reconciliation:**
```bash
# Restart daemon to trigger immediate reconciliation
systemctl restart agentlabd

# Or wait for automatic cycle (30s default)
```

**Reconciliation intervals:**
```yaml
# In /etc/agentlab/config.yaml (future enhancement)
# reconcile_interval: 30s  # default
```

---

## Security

### Are my artifacts encrypted?

**No.** Artifacts are **stored in plaintext** on the host.

**Security considerations:**
- Artifacts directory: `/var/lib/agentlab/artifacts/`
- File permissions: Restricted to `agentlabd` process
- Upload protection: Requires valid one-time tokens
- No at-rest encryption

**If you need encrypted artifacts:**
1. Enable full-disk encryption on the Proxmox host
2. Store artifacts on encrypted storage (LUKS, ZFS encryption)
3. Have VMs encrypt artifacts before upload
4. Use external artifact storage with encryption

**See also:** [Secrets bundles documentation](secrets.md)

### Can sandboxes access my host?

**By default: NO.** AgentLab provides strong host isolation:

**Network isolation:**
- Sandboxes on separate subnet (`10.77.0.0/16`)
- RFC1918/ULA egress blocks prevent private network access
- No port forwarding from host to sandboxes by default
- Host at `10.77.0.1` but no inbound services exposed

**Storage isolation:**
- **No host bind mounts** - critical security feature
- VMs use virtual disks only
- Optional persistent workspaces via separate disk volumes
- Artifacts uploaded out-of-band via HTTP API

**Process isolation:**
- Full VM isolation via QEMU/KVM
- No container escape risks
- Separate kernel per sandbox

**Risks to mitigate:**
- Tailscale subnet routing exposes sandboxes to your tailnet
- Malicious agents could exploit QEMU vulnerabilities (keep QEMU updated)
- Resource exhaustion could affect host (use profile limits)

**Security best practices:**
```bash
# 1. Never enable host bind mounts
# 2. Use strict firewall groups
# 3. Keep Proxmox and QEMU updated
# 4. Monitor resource usage
# 5. Use Tailscale ACLs if enabling subnet routing
# 6. Limit concurrent sandboxes to resource availability
```

### How are secrets managed?

AgentLab uses **encrypted secrets bundles** for secure credential delivery:

**Architecture:**
1. Secrets stored encrypted on disk (age or sops)
2. Daemon decrypts in-memory on demand
3. Injected into sandboxes via cloud-init (tmpfs only)
4. Never written to sandbox disk

**Secret delivery:**
- Delivered at VM boot time via cloud-init
- Stored in `/run/agentlab/secrets` (tmpfs - RAM only)
- Available to `agent-runner` service
- Not accessible to unprivileged processes
- Lost on VM shutdown

**Supported encryption:**
- **age** (recommended): Simple, modern encryption
- **sops**: For teams with existing SOPS workflows

**Example secret flow:**
```bash
# 1. Create encrypted bundle
age -e -a /etc/agentlab/secrets/default.age < secret.yaml

# 2. Reference in config
secrets_bundle: default

# 3. Injected at VM boot
# Inside VM: /run/agentlab/secrets contains decrypted values

# 4. Used by agent-runner
export ANTHROPIC_API_KEY=$(jq -r .env.ANTHROPIC_API_KEY /run/agentlab/secrets/bundle.json)
```

**Security properties:**
- ✅ Secrets never touch disk inside VM
- ✅ Encrypted at rest on host
- ✅ One-time delivery (not re-delivered on restart)
- ✅ Memory-only during runtime
- ❌ Not rotated during VM lifetime
- ❌ Accessible to root inside VM

**See also:** [Secrets bundles documentation](secrets.md)

---

## Performance

### How many concurrent jobs can I run?

**Concurrent job capacity** depends on your host resources:

**Default profile resource usage:**
```
yolo-ephemeral: 4 cores, 6GB RAM, 30GB disk
yolo-workspace: 4 cores, 6GB RAM, 30GB disk + workspace
interactive-dev: 6 cores, 8GB RAM, 60GB disk
```

**Example calculations:**

For a host with:
- 16 cores, 64GB RAM, 1TB SSD

**Concurrent capacity:**
- **yolo-ephemeral:** ~3-4 concurrent (overcommit CPUs)
- **yolo-workspace:** ~3-4 concurrent (if enough disk)
- **interactive-dev:** ~2 concurrent

**Real-world limits:**
- **CPU:** Can overcommit 2:1 for typical workloads
- **RAM:** Hard limit - do not overcommit
- **Storage:** Depends on IOPS and free space
- **Network:** Usually not bottleneck with gigabit

**Tuning for concurrency:**
```yaml
# In profile YAML, reduce resources for higher concurrency
resources:
  cores: 2              # Reduce from 4
  memory_mb: 3072       # Reduce from 6144
storage:
  root_size_gb: 20      # Reduce from 30
```

**Monitoring:**
```bash
# Watch host resources during operation
htop
iostat -x 5
df -h /var/lib/vz

# Check active sandboxes
agentlab sandbox list --filter state=RUNNING
```

### What are the resource limits?

**Resource limits are enforced at multiple levels:**

**1. Profile level (per-sandbox):**
```yaml
resources:
  cores: 4              # CPU cores (hard limit)
  memory_mb: 6144       # RAM in MB (hard limit)
  balloon: false        # Memory ballooning
storage:
  root_size_gb: 30      # Disk size (hard limit)
```

**2. Host level (physical constraints):**
- CPU: Total cores on Proxmox host
- RAM: Total minus Proxmox overhead (reserve 4GB)
- Storage: Free space on storage pool
- Network: 1Gbps typically (vmbr0)

**3. Daemon level (configuration):**
```yaml
# /etc/agentlab/config.yaml
artifact_max_bytes: 268435456  # 256MB max artifact size
proxmox_command_timeout: 2m    # Proxmox operation timeout
provisioning_timeout: 10m      # Sandbox creation timeout
```

**Default limits:**
| Resource | Default | Can be changed |
|----------|---------|----------------|
| Sandbox CPU cores | Per profile | Yes (profile) |
| Sandbox RAM | Per profile | Yes (profile) |
| Sandbox disk | Per profile | Yes (profile) |
| Artifact size | 256MB | Yes (config) |
| Concurrent sandboxes | Unlimited (RAM-bound) | No (manage via profiles) |
| Sandbox TTL | Per job | Yes (job flag) |
| Lease renewal | Yes (running only) | No (state machine) |

**Tuning recommendations:**
```yaml
# For high-throughput, low-resource jobs:
resources:
  cores: 2
  memory_mb: 2048
storage:
  root_size_gb: 15

# For intensive workloads:
resources:
  cores: 8
  memory_mb: 16384
storage:
  root_size_gb: 100

# In config.yaml for slow networks:
proxmox_command_timeout: 5m
provisioning_timeout: 20m
```

**See also:** [Architecture documentation](architecture.md) for system design details

---

**Last updated:** 2026-02-08

**Related documentation:**
- [README](../README.md) - Overview and quickstart
- [Runbook](runbook.md) - Day-2 operations
- [Troubleshooting](troubleshooting.md) - Common issues and solutions
- [Secrets](secrets.md) - Secret management
- [API](api.md) - HTTP API reference
