# AgentLab Performance Characteristics

This document covers performance benchmarks, resource usage patterns, scaling guidance, and tuning options for AgentLab deployments.

## Table of Contents

- [Benchmarks](#benchmarks)
- [Resource Usage](#resource-usage)
- [Scaling Guidance](#scaling-guidance)
- [Tuning Options](#tuning-options)
- [Monitoring](#monitoring)

---

## Benchmarks

### Benchmark methodology

**Environment:**
- Proxmox VE 9.2
- Host: Intel Xeon E-2288G (8 cores/16 threads), 64GB RAM, NVMe SSD
- Storage: ZFS on NVMe (compression=lz4)
- Network: 1Gbps with vmbr0 and vmbr1 bridges
- Template: Ubuntu 24.04 with qemu-guest-agent 4.3+
- AgentLab version: (see `agentlab --version`)

**Running benchmarks:**
```bash
# 1. Build and install AgentLab
make build
sudo scripts/install_host.sh

# 2. Configure and start daemon
sudo systemctl start agentlabd

# 3. Run benchmark script
scripts/benchmark.sh --iterations 10 --profile yolo-ephemeral

# 4. Collect metrics
curl -s http://localhost:8847/metrics | grep agentlab_
```

### Sandbox provisioning performance

**Typical provisioning times:**

| Operation | Median | P95 | P99 | Notes |
|-----------|--------|-----|-----|-------|
| VM clone (ZFS) | 2-3s | 4s | 8s | Instant on ZFS, slower on LVM |
| VM start | 8-12s | 15s | 25s | QEMU boot time |
| Cloud-init | 5-8s | 12s | 20s | Network + package install |
| Guest agent ready | 2-4s | 6s | 10s | QEMU guest agent handshake |
| **Total provisioning** | **17-27s** | **37s** | **63s** | End-to-end |

**Factors affecting provisioning time:**

1. **Storage backend:**
   - ZFS: Fastest (instant clone)
   - LVM-thin: Fast (near-instant clone)
   - Directory: Slow (full copy)
   - Ceph: Moderate (network-dependent)

2. **Template size:**
   - Minimal template (5GB): ~15s total
   - Standard template (30GB): ~25s total
   - Large template (100GB): ~45s total

3. **Cloud-init complexity:**
   - No packages: +5s
   - With apt install: +15-30s
   - With large file downloads: +duration

4. **Network configuration:**
   - Static IP: Faster (no DHCP)
   - DHCP: +2-3s
   - Tailscale: +5-10s

### Job execution performance

**Typical job execution overhead:**

| Phase | Time | Notes |
|-------|------|-------|
| Job queue → provision | <1s | Database transaction |
| Provision → ready | 17-27s | See provisioning above |
| Git clone (large repo) | 5-15s | Network-dependent |
| Agent startup | 2-5s | Depends on agent type |
| Artifact upload | 1-5s | Size-dependent |
| **Total overhead** | **25-50s** | Before actual task execution |

**Throughput benchmarks:**

| Concurrency | Jobs/hour | Success rate | Notes |
|-------------|-----------|--------------|-------|
| 1 | 40-50 | 99% | Baseline |
| 3 | 120-150 | 98% | CPU-bound |
| 5 | 180-220 | 95% | Recommended limit |
| 8 | 250-300 | 85% | High failure rate |
| 10+ | 250-300 | 70% | Diminishing returns |

**Measured on:** 8-core host, 64GB RAM, yolo-ephemeral profile (4 cores/6GB)

### Database performance

**SQLite performance characteristics:**

| Operation | Time | Notes |
|-----------|------|-------|
| Sandbox create | <5ms | In-memory transaction |
| Sandbox update | <3ms | Indexed by VMID |
| Job create | <5ms | In-memory transaction |
| List all sandboxes | 10-50ms | Linear with record count |
| State reconciliation | 50-200ms | Depends on sandbox count |

**Database size growth:**
- Empty database: ~100KB
- Per sandbox record: ~500 bytes
- Per job record: ~800 bytes
- 1000 sandboxes + jobs: ~1MB

**Optimization:**
```bash
# Monthly vacuum to reclaim space
sqlite3 /var/lib/agentlab/agentlab.db "VACUUM;"

# Check integrity
sqlite3 /var/lib/agentlab/agentlab.db "PRAGMA integrity_check;"
```

### Artifact upload performance

**Upload throughput:**

| Artifact size | Time | Throughput |
|---------------|------|------------|
| 1MB | ~0.5s | 2MB/s |
| 10MB | ~2s | 5MB/s |
| 100MB | ~15s | 6.7MB/s |
| 256MB (max) | ~40s | 6.4MB/s |

**Bottleneck:** Single-threaded HTTP upload from VM to host

**Tuning:**
```yaml
# /etc/agentlab/config.yaml
artifact_max_bytes: 536870912  # Increase to 512MB if needed
```

---

## Resource Usage

### Daemon resource footprint

**agentlabd idle usage:**
- CPU: <1% (single core)
- RAM: ~50MB (Go runtime)
- Disk: Minimal (database only)
- Network: Minimal (heartbeat metrics)

**agentlabd under load:**
- CPU: 2-5% (reconciliation + API)
- RAM: 50-100MB (cached connections)
- Database I/O: ~10-50 ops/sec

### Per-sandbox overhead

**Resource overhead beyond profile:**

| Resource | Overhead | Notes |
|----------|----------|-------|
| RAM | ~200MB | QEMU + OS overhead |
| Disk | ~500MB | Metadata + logs |
| CPU | ~5% host | QEMU emulation |
| Network | ~100Mbps peak | During artifact upload |

**Profile resource allocation:**
```yaml
# Actual usage = profile + overhead
resources:
  cores: 4              # 4 vCPUs dedicated to VM
  memory_mb: 6144       # 6GB for VM + ~200MB for QEMU
storage:
  root_size_gb: 30      # 30GB disk + ~500MB metadata
```

### Storage I/O patterns

**Read/write patterns:**

**Provisioning phase:**
- Template clone: Write-heavy (ZFS: metadata only)
- VM boot: Read-heavy (template + packages)
- Cloud-init: Moderate reads/writes

**Execution phase:**
- Git clone: Mixed read/write
- Build/test: Variable (depends on workload)
- Logs: Continuous append (small files)

**Teardown phase:**
- Artifact upload: Read from VM
- VM destroy: Metadata deletion only

**IOPS requirements:**
- **Minimum:** 500 IOPS (spinning disk)
- **Recommended:** 5000+ IOPS (SSD)
- **Optimal:** 10000+ IOPS (NVMe)

**ZFS-specific tuning:**
```bash
# For high-write workloads
zfs set compression=lz4 agentlab
zfs set atime=off agentlab
zfs set recordsize=128K agentlab
```

---

## Scaling Guidance

### Host sizing recommendations

**Small deployments (1-3 concurrent jobs):**
```
CPU: 4 cores
RAM: 16GB
Storage: 250GB SSD
Network: 1Gbps
Expected throughput: 40-60 jobs/hour
```

**Medium deployments (3-5 concurrent jobs):**
```
CPU: 8 cores
RAM: 32GB
Storage: 500GB SSD
Network: 1Gbps
Expected throughput: 120-180 jobs/hour
```

**Large deployments (5-10 concurrent jobs):**
```
CPU: 16 cores
RAM: 64GB
Storage: 1TB SSD
Network: 1Gbps
Expected throughput: 180-300 jobs/hour
```

**Enterprise deployments (10+ concurrent jobs):**
```
Consider multiple Proxmox hosts in a cluster
Each host: 16 cores, 64GB RAM, 1TB SSD
Use load balancer for agentlabd instances
Expected throughput: 300+ jobs/hour per host
```

### Concurrency limits

**Recommended concurrency by host size:**

| Host RAM | Max concurrent (yolo-ephemeral) | Max concurrent (interactive-dev) |
|----------|----------------------------------|-----------------------------------|
| 16GB | 2 | 1 |
| 32GB | 4 | 2 |
| 64GB | 8 | 4 |
| 128GB | 16+ | 8 |

**Do not exceed:**
- **RAM:** Physical RAM - 4GB (Proxmox overhead)
- **CPU:** Physical cores × 2 (for typical workloads)
- **Storage:** Leave 20% free space

### Multi-host deployments

**For scaling beyond single host:**

**Option 1: Proxmox cluster**
```bash
# Set up Proxmox cluster with shared storage
# Each node runs agentlabd
# Use VM affinity rules to distribute load
```

**Option 2: Separate agent hosts**
```bash
# Dedicated Proxmox host for AgentLab
# Separate from production workloads
# Easier isolation and resource management
```

**Option 3: Job queue with multiple workers**
```bash
# External job scheduler (Redis, etc.)
# Multiple agentlabd instances pulling jobs
# Requires external coordination
```

---

## Tuning Options

### Timeout tuning

**Default timeouts:**
```yaml
proxmox_command_timeout: 2m       # Proxmox API/shell commands
provisioning_timeout: 10m        # End-to-end sandbox creation
artifact_token_ttl_minutes: 1440 # Artifact upload tokens (24h)
```

**When to adjust:**

**Increase `proxmox_command_timeout` if:**
- Slow storage (network storage, spinning disks)
- Congested network (multi-hop Proxmox)
- Large templates (>50GB)

**Increase `provisioning_timeout` if:**
- Complex cloud-init (many packages)
- Slow network for downloads
- Large file copies during boot

**Decrease timeouts if:**
- Fast storage (NVMe) and network
- Simple templates
- Want faster failure detection

### Profile tuning for throughput

**High-throughput profile (many small jobs):**
```yaml
name: high-throughput
template_vmid: 9000
resources:
  cores: 2
  memory_mb: 2048
storage:
  root_size_gb: 15
```

**Balanced profile (default):**
```yaml
name: balanced
template_vmid: 9000
resources:
  cores: 4
  memory_mb: 6144
storage:
  root_size_gb: 30
```

**High-performance profile (large jobs):**
```yaml
name: high-performance
template_vmid: 9000
resources:
  cores: 8
  memory_mb: 16384
storage:
  root_size_gb: 100
```

### Database tuning

**For large deployments (1000+ sandboxes):**

```bash
# Enable WAL mode for better concurrency
sqlite3 /var/lib/agentlab/agentlab.db "PRAGMA journal_mode=WAL;"

# Increase cache size (in /etc/agentlab/config.yaml)
# Add to database connection string:
# ?cache=shared&_pragma=synchronous(NORMAL)

# Regular maintenance
sqlite3 /var/lib/agentlab/agentlab.db "PRAGMA optimize;"
```

### Network tuning

**For high-throughput artifact uploads:**

```bash
# Increase TCP buffer sizes
sysctl -w net.core.rmem_max=134217728
sysctl -w net.core.wmem_max=134217728

# Make persistent in /etc/sysctl.d/99-agentlab.conf
net.core.rmem_max = 134217728
net.core.wmem_max = 134217728
```

**For high-concurrency deployments:**

```bash
# Increase connection tracking
sysctl -w net.netfilter.nf_conntrack_max=262144

# Make persistent
net.netfilter.nf_conntrack_max = 262144
```

---

## Monitoring

### Prometheus metrics

**Enable metrics:**
```yaml
# /etc/agentlab/config.yaml
metrics_listen: "127.0.0.1:8847"
```

**Available metrics:**

| Metric | Type | Description |
|--------|------|-------------|
| `agentlab_sandboxes_total` | gauge | Total sandboxes by state |
| `agentlab_jobs_total` | gauge | Total jobs by status |
| `agentlab_provisioning_duration_seconds` | histogram | Sandbox provisioning time |
| `agentlab_api_duration_seconds` | histogram | API request duration |
| `agentlab_lease_gc_duration_seconds` | histogram | Lease GC run time |

**Example Prometheus queries:**

```promql
# Provisioning success rate
rate(agentlab_sandboxes_total{state="READY"}[5m]) /
rate(agentlab_sandboxes_total{state="PROVISIONING"}[5m])

# Average provisioning time
histogram_quantile(0.95,
  rate(agentlab_provisioning_duration_seconds_bucket[5m])
)

# Active sandboxes
agentlab_sandboxes_total{state=~"RUNNING|BOOTING|READY"}

# Job failure rate
rate(agentlab_jobs_total{status="FAILED"}[1h]) /
rate(agentlab_jobs_total[1h])
```

### Health checks

**Daemon health:**
```bash
# Check if daemon is running
systemctl status agentlabd

# Check API responsiveness
curl --unix-socket /run/agentlab/agentlabd.sock http://localhost/health

# Check metrics endpoint
curl http://localhost:8847/metrics
```

**Proxmox health:**
```bash
# Check Proxmox API
pvesh get /cluster/status

# Check storage
pvesm status

# Check VMs
qm list
```

### Performance diagnostics

**Collect performance data:**
```bash
# System resources
top -b -n 1 > perf-$(date +%Y%m%d).log
vmstat 1 10 >> perf-$(date +%Y%m%d).log
iostat -x 1 10 >> perf-$(date +%Y%m%d).log

# AgentLab metrics
curl -s http://localhost:8847/metrics > metrics-$(date +%Y%m%d).log

# Daemon logs
journalctl -u agentlabd --since "1 hour ago" > agentlab-$(date +%Y%m%d).log
```

**Identify bottlenecks:**

1. **CPU bottleneck:**
   - Top shows 100% CPU
   - High load average
   - Solution: Reduce concurrent jobs or upgrade CPU

2. **Memory bottleneck:**
   - Swap usage increasing
   - OOM kills in logs
   - Solution: Reduce concurrent jobs or add RAM

3. **I/O bottleneck:**
   - High iowait in top
   - Slow provisioning times
   - Solution: Faster storage or reduce concurrent jobs

4. **Network bottleneck:**
   - Slow artifact uploads
   - High network latency
   - Solution: Better network or reduce artifact size

---

**Last updated:** 2026-02-06

**Related documentation:**
- [FAQ](faq.md) - Common performance questions
- [Runbook](runbook.md) - Operational procedures
- [Troubleshooting](troubleshooting.md) - Performance issues
- [API](api.md) - Metrics endpoint details
