# AgentLab v0.1.2 - Proxmox Setup Guide

This guide covers setting up and running AgentLab v0.1.2 on a Proxmox host, including template creation, DHCP configuration, and troubleshooting.

## Table of Contents

- [Overview](#overview)
- [System Requirements](#system-requirements)
- [Installation](#installation)
- [Template Setup](#template-setup)
- [DHCP Server Configuration](#dhcp-server-configuration)
- [Testing](#testing)
- [Troubleshooting](#troubleshooting)
- [Reference](#reference)

---

## Overview

AgentLab is a daemon for provisioning sandboxed VMs on Proxmox. Key components:

- **agentlabd**: The main daemon that handles sandbox provisioning
- **agentlab CLI**: Command-line tool for creating and managing sandboxes
- **Template VMs**: Base images used for cloning sandboxes
- **QEMU Guest Agent**: Required for IP detection and VM communication

**Version**: v0.1.2
**Commit**: 9779d08
**Release Date**: 2026-01-31

---

## System Requirements

- Proxmox VE 8.x (tested with kernel 6.8+)
- Debian-based host (for dnsmasq installation)
- Network bridge for VM connectivity (vmbr1 recommended)
- 2048+ MB RAM per sandbox
- 3+ GB disk space per sandbox

---

## Installation

### 1. Install AgentLab Binaries

```bash
# Download and install the v0.1.2 binaries
scp agentlab-v0.1.2-linux-amd64 root@<hostname>:/tmp/
ssh root@<hostname>

# Install binaries
install -m 755 /tmp/agentlab-v0.1.2-linux-amd64 /usr/local/bin/agentlab
install -m 755 /tmp/agentlab-v0.1.2-linux-amd64 /usr/local/bin/agentlabd
```

### 2. Configure Systemd Service

Create `/etc/systemd/system/agentlabd.service`:

```ini
[Unit]
Description=AgentLab daemon
After=network.target
Wants=network-online.target

[Service]
Type=notify
NotifyAccess=all
ExecStart=/usr/local/bin/agentlabd --config /etc/agentlab/config.yaml
Restart=on-failure
RestartSec=5s
User=root
Group=agentlab

# Proxmox IPC compatibility - these MUST be disabled for qm clone to work
# NoNewPrivileges=true
# PrivateTmp=true

[Install]
WantedBy=multi-user.target
```

**Important**: The `NoNewPrivileges` and `PrivateTmp` directives MUST be commented out or removed for Proxmox IPC compatibility. The `qm clone` command will fail with `ipcc_send_rec: Unknown error -1` if these are enabled.

### 3. Create Configuration

Create `/etc/agentlab/config.yaml`:

```yaml
ssh_public_key_path: /etc/agentlab/keys/agentlab_id_ed25519.pub
secrets_bundle: default
bootstrap_listen: <gateway-ip>:8844
artifact_listen: <gateway-ip>:8846
controller_url: http://<gateway-ip>:8844
```

### 4. Create SSH Keys

```bash
mkdir -p /etc/agentlab/keys
ssh-keygen -t ed25519 -f /etc/agentlab/keys/agentlab_id_ed25519 -N ""
```

### 5. Enable and Start

```bash
useradd -r agentlab 2>/dev/null || true
systemctl daemon-reload
systemctl enable agentlabd
systemctl start agentlabd
systemctl status agentlabd
```

---

## Template Setup

AgentLab requires a template VM with QEMU guest agent installed. This guide uses Debian 12 due to its stability and pre-installed tools.

### Step 1: Download Debian Cloud Image

```bash
wget -O /var/lib/vz/template/iso/debian-12-generic-amd64.img \
  https://cloud.debian.org/images/cloud/bookworm/latest/debian-12-generic-amd64.qcow2
```

### Step 2: Create Template VM

```bash
qm create 9000 \
  --name agentlab-debian-12 \
  --memory 2048 \
  --cores 2 \
  --net0 virtio,bridge=vmbr1 \
  --scsihw virtio-scsi-pci \
  --scsi0 local-zfs:0,import-from=/var/lib/vz/template/iso/debian-12-generic-amd64.img \
  --boot order=scsi0 \
  --agent enabled=1
```

### Step 3: Install QEMU Guest Agent

**Using virt-customize** (recommended - modifies disk directly):

```bash
DISK="/dev/zvol/rpool/data/vm-9000-disk-0"  # Adjust path if using different storage

# Install qemu-guest-agent with network support
virt-customize --format=raw --network -a "$DISK" --install qemu-guest-agent

# Verify installation
guestfish --ro -a "$DISK" -i ls /usr/bin/qemu-guest-agent
```

### Step 4: Configure DHCP Networking

Create systemd-networkd configuration for DHCP:

```bash
# Create network config file
cat > /tmp/10-ens18.network << 'EOF'
[Match]
Name=ens18

[Network]
DHCP=yes
EOF

# Upload to template disk
virt-customize --format=raw --network -a "$DISK" \
  --upload /tmp/10-ens18.network:/etc/systemd/network/10-ens18.network

# Verify
guestfish --ro -a "$DISK" -i cat /etc/systemd/network/10-ens18.network
```

### Step 5: Convert to Template

```bash
qm template 9000
qm list | grep 9000
```

Expected output:
```
9000 agentlab-debian-12 stopped 2048 3.00 0
```

---

## DHCP Server Configuration

VMs connected to `vmbr1` need DHCP to obtain IP addresses. Configure dnsmasq:

### Install dnsmasq

```bash
apt-get install -y dnsmasq
```

### Create DHCP Configuration

Create `/etc/dnsmasq.d/agentlab.conf`:

```conf
# DHCP server for vmbr1 (<network-range>)
interface=vmbr1
dhcp-range=<gateway-ip>00,<end-ip>,255.255.0.0,12h
dhcp-option=option:router,<gateway-ip>
dhcp-option=option:dns-server,<gateway-ip>
# Disable DHCP on other interfaces
no-dhcp-interface=vmbr0
```

### Start dnsmasq

```bash
systemctl restart dnsmasq
systemctl enable dnsmasq
systemctl status dnsmasq
```

### Verify DHCP is Listening

```bash
ss -ulnp | grep :67
```

Expected output:
```
UNCONN 0 0 0.0.0.0:67 0.0.0.0:* users:(("dnsmasq",pid=1234,fd=4))
```

---

## Testing

### 1. Verify Daemon Status

```bash
systemctl status agentlabd
ls -la /run/agentlab/agentlabd.sock
```

### 2. Create Test Sandbox

```bash
agentlab sandbox new --profile minimal --ttl 5m
```

### 3. Monitor Provisioning

```bash
# Watch journal logs
journalctl -u agentlabd -f

# List sandboxes
agentlab sandbox list

# Check Proxmox VMs
qm list | grep sandbox
```

### 4. Verify VM Connectivity

```bash
# Get sandbox IP
agentlab sandbox show <vmid>

# SSH into sandbox (if SSH is enabled)
ssh root@<ip-address> hostname
```

### 5. Cleanup Test Sandbox

```bash
agentlab sandbox destroy <vmid>
# Or manually
qm stop <vmid>
qm destroy <vmid>
```

---

## Troubleshooting

### Port Conflicts

**Symptom**: `agentlabd error: listen bootstrap <gateway-ip>:8844: bind: address already in use`

**Solution**:
```bash
fuser -k 8844/tcp
fuser -k 8846/tcp
systemctl restart agentlabd
```

### QEMU Guest Agent Not Running

**Symptom**: `QEMU guest agent is not running`

**Solution**: Verify qemu-guest-agent is installed in template:
```bash
DISK="/dev/zvol/rpool/data/base-9000-disk-0"  # Use base disk for templates
guestfish --ro -a "$DISK" -i ls /usr/bin/qemu-guest-agent
```

If missing, install it:
```bash
virt-customize --format=raw --network -a "$DISK" --install qemu-guest-agent
```

### No IP Address (DHCP Failure)

**Symptom**: Sandbox VM boots but has no IP address

**Solutions**:

1. **Check dnsmasq is running**:
   ```bash
   systemctl status dnsmasq
   ss -ulnp | grep :67
   ```

2. **Check vmbr1 has connectivity**:
   ```bash
   ip addr show vmbr1
   bridge link show vmbr1
   ```

3. **Verify network config in template**:
   ```bash
   qm guest cmd <vmid> networkctl status ens18
   ```

4. **Check dnsmasq logs**:
   ```bash
   journalctl -u dnsmasq -n 20
   ```

### Sandbox Stuck in REQUESTED State

**Symptom**: `agentlab sandbox list` shows `REQUESTED` but VM never created

**Solution**: Check daemon logs for errors:
```bash
journalctl -u agentlabd -n 50
```

Common causes:
- Template VM doesn't exist
- Template validation failed
- Insufficient resources (disk, RAM)

### virt-customize Installation Fails

**Symptom**: virt-customize reports success but binary not found

**Solution**: Ensure `--network` flag is used:
```bash
virt-customize --format=raw --network -a "$DISK" --install qemu-guest-agent
```

Also verify with guestfish:
```bash
guestfish --ro -a "$DISK" -i sh -c "dpkg -l | grep qemu-guest-agent"
```

### Template Clone Fails

**Symptom**: `failed to provision sandbox` with clone errors

**Solutions**:

1. **Verify template exists**:
   ```bash
   qm list | grep 9000
   ```

2. **Check template status**:
   ```bash
   qm config 9000 | grep template
   qm config 9000 | grep agent
   ```

3. **Validate template** (if using AgentLab with ValidateTemplate):
   ```bash
   qm status 9000
   qm cmd 9000 ping  # Should work if template is a stopped VM
   ```

### IPC Error with qm clone

**Symptom**: `ipcc_send_rec[1] failed: Unknown error -1`

**Solution**: This is caused by `NoNewPrivileges` or `PrivateTmp` in systemd unit. Ensure these are disabled in `/etc/systemd/system/agentlabd.service`:

```ini
# NoNewPrivileges=true  # MUST be disabled
# PrivateTmp=true         # MUST be disabled
```

Then reload and restart:
```bash
systemctl daemon-reload
systemctl restart agentlabd
```

### Stale Sandbox Database Entries

**Symptom**: Old sandboxes appear in list but VMs don't exist

**Solution**: Clean up Proxmox VMs:
```bash
# List all VMs
qm list

# Destroy specific VMs
qm stop <vmid>
qm destroy <vmid>
```

The lease garbage collector will clean up entries over time.

---

## Reference

### File Locations

| File | Purpose |
|------|---------|
| `/usr/local/bin/agentlabd` | Daemon binary |
| `/usr/local/bin/agentlab` | CLI binary |
| `/etc/systemd/system/agentlabd.service` | Systemd service file |
| `/etc/agentlab/config.yaml` | Main configuration |
| `/etc/agentlab/profiles/` | Profile definitions |
| `/etc/agentlab/keys/` | SSH keys |
| `/run/agentlab/agentlabd.sock` | Unix socket |
| `/etc/dnsmasq.d/agentlab.conf` | DHCP configuration |

### Common Commands

```bash
# Service management
systemctl start|stop|restart|status agentlabd

# Sandbox operations
agentlab sandbox new --profile <name> --ttl <duration>
agentlab sandbox list
agentlab sandbox show <vmid>
agentlab sandbox destroy <vmid>

# VM operations
qm list
qm start <vmid>
qm stop <vmid>
qm config <vmid>
qm guest cmd <vmid> ping
qm guest cmd <vmid> network-get-interfaces

# Template operations
qm template <vmid>
qm clone <source> <dest> --name <name>
qm destroy <vmid>

# Disk operations (for virt-customize)
zfs list | grep vm-9000  # Find disk path
guestfish --ro -a /dev/zvol/rpool/data/vm-9000-disk-0 -i <command>
```

### Profile Configuration

Profiles are stored in `/etc/agentlab/profiles/` as YAML files:

```yaml
name: minimal
template_vmid: 9000
network:
  bridge: vmbr1
  model: virtio
resources:
  cores: 2
  memory_mb: 2048
storage:
  root_size_gb: 30
behavior:
  mode: dangerous
  keepalive_default: false
  ttl_minutes_default: 60
```

### Network Configuration

- **vmbr0**: Primary bridge with external connectivity
- **vmbr1**: Isolated bridge for sandbox VMs (<network-range>)
- **DHCP Range**: <gateway-ip>00 - <end-ip>
- **Gateway/DNS**: <gateway-ip> (Proxmox host)

### Logging

```bash
# Daemon logs
journalctl -u agentlabd -f

# dnsmasq logs
journalctl -u dnsmasq -f

# Proxmox logs
tail -f /var/log/pve/tasks/index
```

---

## Appendix: Complete Setup Script

```bash
#!/bin/bash
set -e

# Variables
TEMPLATE_VMID=9000
TEMPLATE_NAME="agentlab-debian-12"
BRIDGE="vmbr1"
STORAGE="local-zfs"
DISK_IMAGE="/var/lib/vz/template/iso/debian-12-generic-amd64.img"

echo "=== Installing dnsmasq ==="
apt-get install -y dnsmasq

echo "=== Configuring dnsmasq ==="
cat > /etc/dnsmasq.d/agentlab.conf << 'EOF'
interface=vmbr1
dhcp-range=<gateway-ip>00,<end-ip>,255.255.0.0,12h
dhcp-option=option:router,<gateway-ip>
dhcp-option=option:dns-server,<gateway-ip>
no-dhcp-interface=vmbr0
EOF
systemctl restart dnsmasq

echo "=== Creating template VM ==="
qm create $TEMPLATE_VMID \
  --name $TEMPLATE_NAME \
  --memory 2048 \
  --cores 2 \
  --net0 virtio,bridge=$BRIDGE \
  --scsihw virtio-scsi-pci \
  --scsi0 $STORAGE:0,import-from=$DISK_IMAGE \
  --boot order=scsi0 \
  --agent enabled=1

echo "=== Installing qemu-guest-agent ==="
DISK=$(zfs list | grep "vm-$TEMPLATE_VMID-disk" | head -1 | awk '{print "/dev/zvol/rpool/data/"$1}')
virt-customize --format=raw --network -a "$DISK" --install qemu-guest-agent

echo "=== Configuring DHCP ==="
cat > /tmp/10-ens18.network << 'EOF'
[Match]
Name=ens18
[Network]
DHCP=yes
EOF
virt-customize --format=raw --network -a "$DISK" \
  --upload /tmp/10-ens18.network:/etc/systemd/network/10-ens18.network

echo "=== Converting to template ==="
qm template $TEMPLATE_VMID

echo "=== Verifying template ==="
qm list | grep $TEMPLATE_VMID

echo "=== Setup complete! ==="
```

---

## Support

For issues or questions:

1. Check daemon logs: `journalctl -u agentlabd -n 50`
2. Check Proxmox tasks: `/var/log/pve/tasks/index`
3. Verify template: `qm config 9000`
4. Verify network: `systemctl status dnsmasq`

For bug reports, include:
- AgentLab version: `agentlabd --version`
- Template configuration: `qm config 9000`
- Daemon logs: `journalctl -u agentlabd -n 100`
- Error messages from failed operations
