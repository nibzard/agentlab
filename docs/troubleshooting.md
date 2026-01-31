# AgentLab Troubleshooting

This guide helps resolve common issues with AgentLab.

## Table of Contents

- [Sandbox Operations](#sandbox-operations)
- [Job Failures](#job-failures)
- [Database Issues](#database-issues)
- [Networking Issues](#networking-issues)
- [Common Error Messages](#common-error-messages)

---

## Sandbox Operations

### Sandbox destroy fails

**Problem:** `agentlab sandbox destroy <vmid>` fails with error

**Cause:** Sandbox is in a state that doesn't allow normal destroy (e.g., TIMEOUT), or VM no longer exists in Proxmox

**Solutions:**

1. Check the sandbox state:
```bash
agentlab sandbox show <vmid>
```

2. If in TIMEOUT state and VM doesn't exist in Proxmox, force destroy:
```bash
agentlab sandbox destroy --force <vmid>
```

3. Or remove orphaned entries:
```bash
agentlab sandbox prune
```

4. Manual cleanup (requires direct Proxmox access):
```bash
qm destroy <vmid>
```

### Sandbox lease not renewable

**Problem:** `agentlab sandbox lease renew --ttl <ttl> <vmid>` fails

**Cause:** Sandbox is not in RUNNING state (only RUNNING state supports lease renewal)

**Solution:**
```bash
# Check current state
agentlab sandbox show <vmid>

# Only renewal works in RUNNING state
# If sandbox is in TIMEOUT, consider destroying it
agentlab sandbox destroy --force <vmid>
# Then create a new one
agentlab sandbox new --profile <profile> --ttl <ttl>
```

### Stale sandbox entries

**Problem:** Old sandboxes appear in list but don't exist in Proxmox

**Solution:**
```bash
# Remove all orphaned TIMEOUT sandboxes
agentlab sandbox prune
```

---

## Job Failures

### Job stays in QUEUED state

**Problem:** Job created but never starts

**Possible causes:**
1. No available SSH public key configured
2. Profile not found
3. Insufficient resources in Proxmox
4. Template doesn't exist

**Solutions:**

1. Check daemon logs:
```bash
journalctl -u agentlabd -f
```

2. Verify profile exists:
```bash
ls /etc/agentlab/profiles/
```

3. Check SSH key:
```bash
ls /etc/agentlab/keys/
```

4. Verify template:
```bash
qm list
```

### Job fails in PROVISIONING

**Problem:** Job fails during VM creation

**Possible causes:**
1. Template doesn't exist or is corrupted
2. Storage pool not available
3. Network bridge missing
4. Insufficient resources

**Solutions:**

1. Check template:
```bash
qm list | grep <template_name>
```

2. Verify storage:
```bash
pvesh get /storage
```

3. Check network bridges:
```bash
ip link show | grep vmbr
```

4. Check daemon logs for specific error:
```bash
journalctl -u agentlabd -n 100
```

---

## Database Issues

### Corrupted database

**Problem:** AgentLab fails to start or returns errors when querying sandboxes/jobs

**Symptoms:**
- `database is locked` errors
- `database disk image is malformed` errors
- Commands hang indefinitely

**Solution:**

1. Stop the daemon:
```bash
systemctl stop agentlabd
```

2. Backup existing database:
```bash
cp /var/lib/agentlab/agentlab.db /var/lib/agentlab/agentlab.db.backup
```

3. Check and repair database:
```bash
sqlite3 /var/lib/agentlab/agentlab.db "PRAGMA integrity_check;"
sqlite3 /var/lib/agentlab/agentlab.db "VACUUM;"
```

4. If repair fails, start fresh (WARNING: This deletes all data):
```bash
rm /var/lib/agentlab/agentlab.db
systemctl start agentlabd
```

### Orphaned records

**Problem:** Database has sandbox records that don't exist in Proxmox

**Solution:**
```bash
# Remove all orphaned entries
agentlab sandbox prune
```

---

## Networking Issues

### Cannot reach VMs

**Problem:** Cannot SSH into sandbox VMs

**Possible causes:**
1. Agent subnet not configured
2. Firewall rules blocking access
3. VM not started properly
4. Tailscale routing not configured

**Solutions:**

1. Check VM IP:
```bash
agentlab sandbox show <vmid>
```

2. Verify agent subnet:
```bash
ip addr show vmbr1
```

3. Check firewall:
```bash
iptables -L -n | grep 10.77
```

4. Test connectivity from host:
```bash
ping <vm_ip>
```

5. Check Tailscale if configured:
```bash
tailscale status
```

### VM cannot access internet

**Problem:** Sandbox VM has no internet access (by design for some modes)

**Check current configuration:**

1. Review profile configuration:
```bash
cat /etc/agentlab/profiles/<profile>.yaml
```

2. Check if egress blocks are enabled:
```bash
iptables -L -n | grep EGRESS
```

3. If internet access is needed, update profile to remove RFC1918/ULA egress blocks

---

## Common Error Messages

### `failed to destroy sandbox`

**Cause:** Sandbox in invalid state for destroy operation

**Solution:** Use `--force` flag
```bash
agentlab sandbox destroy --force <vmid>
```

### `sandbox lease not renewable`

**Cause:** Sandbox not in RUNNING state

**Solution:** Check state, only RUNNING supports lease renewal
```bash
agentlab sandbox show <vmid>
```

### `invalid socket path`

**Cause:** Socket file doesn't exist or daemon not running

**Solution:**
1. Check daemon status:
```bash
systemctl status agentlabd
```

2. Check socket file:
```bash
ls -l /run/agentlab/agentlabd.sock
```

3. Start daemon if stopped:
```bash
systemctl start agentlabd
```

### `timeout waiting for daemon response`

**Cause:** Daemon busy or unresponsive

**Solution:**
1. Check daemon logs:
```bash
journalctl -u agentlabd -f
```

2. Restart daemon:
```bash
systemctl restart agentlabd
```

3. Increase timeout:
```bash
agentlab --timeout 5m <command>
```

### `database locked`

**Cause:** Another process has exclusive lock on database

**Solution:**
1. Check for running daemon:
```bash
ps aux | grep agentlabd
```

2. Check for other processes accessing database:
```bash
lsof /var/lib/agentlab/agentlab.db
```

3. Kill stale processes if found, or restart daemon

---

## Getting Help

### Collect diagnostic information

```bash
# Save system information
{
  echo "=== Version ==="
  bin/agentlab --version
  echo "=== Daemon status ==="
  systemctl status agentlabd
  echo "=== Recent logs ==="
  journalctl -u agentlabd -n 50
  echo "=== Sandboxes ==="
  bin/agentlab sandbox list
  echo "=== Proxmox VMs ==="
  qm list
} > agentlab-diagnostic-$(date +%Y%m%d).log
```

### Check logs

```bash
# Daemon logs
journalctl -u agentlabd -f

# Specific time range
journalctl -u agentlabd --since "1 hour ago"

# Save logs to file
journalctl -u agentlabd > agentlab.log
```

### Report issues

When reporting issues, include:
1. AgentLab version (`agentlab --version`)
2. Go version (`go version`)
3. Proxmox version (`pveversion`)
4. Error messages
5. Diagnostic log file
6. Steps to reproduce

---

## Maintenance

### Regular cleanup

```bash
# Weekly cleanup of orphaned sandboxes
0 2 * * 0 root /usr/local/bin/agentlab sandbox prune

# Monthly database optimization
0 3 1 * * root sqlite3 /var/lib/agentlab/agentlab.db "VACUUM;"
```

### Monitor disk space

```bash
# Check database size
du -sh /var/lib/agentlab/agentlab.db

# Check artifacts directory
du -sh /var/lib/agentlab/artifacts/
```

### Backup database

```bash
# Automated backup
cp /var/lib/agentlab/agentlab.db /backup/agentlab-$(date +%Y%m%d-%H%M%S).db

# Retention: Keep last 7 days
find /backup/agentlab-*.db -mtime +7 -delete
```

---

## Performance Tips

### Sandbox creation speed

1. Use SSD-backed storage pools
2. Keep templates on fast storage
3. Pre-create worker VMs if possible
4. Adjust Proxmox timeouts in config if network is slow

### Database performance

1. Run `VACUUM` regularly
2. Keep database on fast storage
3. Monitor query times in logs
4. Consider indexing if many records

---

**Last updated:** 2026-01-31
