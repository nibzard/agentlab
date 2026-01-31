# AgentLab v0.1.0 - Complete Testing Summary

**Date**: 2026-01-30
**Tester**: opencode agent
**Repository**: nibzard/agentlab
**Release**: v0.1.0
**Testing Duration**: ~1 hour

---

## Executive Summary

AgentLab v0.1.0 has **excellent infrastructure** but a **critical bug** in sandbox provisioning makes the system **unusable for its primary purpose**.

**Overall Assessment**: **BLOCKER for production use** - infrastructure works perfectly, but core feature (creating and booting agent sandboxes) fails silently.

---

## Testing Phases

### Phase 1: Static Code Analysis ✅

**Goal**: Review code quality, architecture, and security model without running binaries

**Results**:
- ✅ High-quality Go code (standard project layout)
- ✅ Well-structured bash scripts (`set -euo pipefail`)
- ✅ Clean architecture (daemon ↔ CLI ↔ guest separation)
- ✅ Strong security model (defense in depth, tmpfs-only secrets)
- ✅ Comprehensive documentation (runbook, API docs)
- ✅ Default profiles well-configured

**Deliverables**:
- `LESSONS_LEARNED.md` (500+ lines of analysis)

---

### Phase 2: Non-Proxmox Runtime ✅

**Goal**: Verify binaries build and daemon starts without Proxmox

**Environment**: Linux host (no Proxmox)

**Results**:
- ✅ Go 1.23.5 builds binaries (despite 1.24.0 requirement)
- ✅ Daemon starts with minimal config
- ✅ CLI communicates via Unix socket
- ✅ Socket permissions correct
- ✅ Graceful shutdown implemented
- ✅ Version/commit/date embedded at build time

**Deliverables**:
- `RUNTIME_TESTING.md` (200+ lines of findings)

---

### Phase 3: Proxmox Infrastructure ✅

**Goal**: Verify AgentLab infrastructure on actual Proxmox host

**Environment**: Proxmox VE 8.4.16 (<internal-ip>)

**Results**:
- ✅ Proxmox tools available (`qm`, `pvesh`, `pvesm`)
- ✅ Storage pools ready (local: 987 GB, local-zfs: 987 GB)
- ✅ vmbr0 bridge exists (<internal-ip>/24)
- ✅ vmbr1 bridge created (<gateway-ip>/16)
- ✅ IPv4 forwarding enabled
- ✅ nftables rules applied (NAT, RFC1918/ULA blocks)
- ✅ Ubuntu 24.04 cloud image downloaded (597 MB)
- ✅ VM template created successfully (VMID 9000, 40 GB)
- ✅ Manual `qm clone` command works
- ✅ Daemon runs on Proxmox with all listeners active

**Network Verification**:
```
vmbr1: <gateway-ip>/16
NAT: <network-ip>/16 → vmbr0 (masquerade)
Blocks: RFC1918, ULA, link-local, tailnet
```

**Deliverables**:
- `ACTUAL_PROXMOX_TESTING.md` (400+ lines of detailed findings)

---

### Phase 4: Sandbox Provisioning ❌ CRITICAL BUG

**Goal**: Create and boot an agent sandbox

**Results**:
- ❌ API returns generic error: `{"error":"failed to provision sandbox"}`
- ❌ Daemon logs nothing (no error messages written)
- ❌ Database shows 7 attempts stuck in `PROVISIONING` state
- ❌ No VMs created in Proxmox after API requests
- ❌ Cannot debug root cause (silent failure)

**Symptoms**:
```bash
$ agentlab --json sandbox new --profile yolo-ephemeral --name test
{"error":"failed to provision sandbox"}

$ echo "SELECT * FROM sandboxes;" | sqlite3 /var/lib/agentlab/agentlab.db
1000|test-sandbox|yolo-ephemeral|PROVISIONING|||0|...
```

**What Works**:
- Manual `qm clone 9000 1000` creates VM successfully
- Sandbox record created in database
- Bootstrap token generation succeeds (no error)
- Configuration validation passes

**What Doesn't**:
- Cloud-init snippet creation (silent failure)
- VM boot via agentlabd (never happens)
- Error logging (daemon logs nothing about failures)

**Severity**: **CRITICAL BLOCKER**

---

## Infrastructure Quality Assessment

| Component | Status | Quality | Notes |
|-----------|--------|----------|-------|
| Proxmox integration | ✅ | Excellent | Tools work, template created, manual clone succeeds |
| Network setup | ✅ | Excellent | vmbr1, IPv4 forwarding, nftables rules all correct |
| Storage configuration | ✅ | Excellent | local-zfs (987 GB) ready for VM disks |
| Daemon implementation | ✅ | Excellent | Clean startup, version info, graceful shutdown |
| CLI implementation | ✅ | Excellent | Works with Unix socket, table formatting, JSON output |
| Template creation | ✅ | Excellent | Cloud image download, VM creation, template conversion |
| Profile system | ⚠️ | Good | 2 of 3 profiles loaded (multi-doc YAML issue) |
| Documentation | ✅ | Excellent | Runbook, API docs, inline help all clear |
| **Sandbox provisioning** | **❌** | **BROKEN** | Silent failures, no debugging info |

---

## Bugs Found

### Bug #1: Critical - Silent Sandbox Provisioning Failures

**Location**: `internal/daemon/job_orchestrator.go:ProvisionSandbox()`
**Severity**: CRITICAL - BLOCKER
**Symptom**: Sandbox provisioning fails silently with no error logs
**Impact**: Core feature completely unusable
**Steps to Reproduce**:
```bash
1. Install agentlabd on Proxmox host
2. Create template (VMID 9000)
3. Run: agentlab sandbox new --profile yolo-ephemeral --name test
4. Observe: {"error":"failed to provision sandbox"}
5. Check logs: No error messages
6. Check database: Sandbox stuck in PROVISIONING state
7. Check Proxmox: No VM created
```

**Root Cause**: Unknown - likely in snippet creation or VM boot stage
**Hypothesis**: Either:
- `snippetStore.Create()` fails silently
- `backend.Configure()` fails silently
- `sandboxManager.Transition()` fails silently
- VM boot timeout occurs but isn't logged

**Recommendation**: Add structured logging at each step of ProvisionSandbox workflow, expose actual errors in API responses.

---

### Bug #2: Medium - Multi-Document YAML Profile Loading

**Location**: Profile loading in daemon startup
**Severity**: MEDIUM
**Symptom**: `defaults.yaml` has 3 profiles but daemon reports "loaded 2 profiles"
**Impact**: Third profile (`interactive-dev`) may be unavailable
**Root Cause**: YAML parser may stop at document separator or have iteration issue
**Recommendation**: Debug profile loading logic, verify all documents are parsed.

---

### Bug #3: Low - Generic Error Messages

**Location**: `internal/daemon/api.go:688`
**Severity**: LOW
**Symptom**: API returns `{"error":"failed to provision sandbox"}`
**Impact**: No debugging information for users or developers
**Root Cause**: `writeError(w, http.StatusInternalServerError, "failed to provision sandbox")` wraps actual error
**Recommendation**: Log actual error before wrapping, or include error details in response.

---

## Strengths Observed

1. **Security-First Design**
   - Tmpfs-only secrets (no disk persistence)
   - One-time bootstrap tokens
   - Network isolation (RFC1918/ULA blocks)
   - Unix socket for local control

2. **Excellent Infrastructure Automation**
   - Network setup scripts (vmbr1, nftables)
   - Template creation with cloud-init
   - Idempotent operations

3. **Clean Architecture**
   - Separation of concerns (daemon, CLI, guest)
   - REST-like API over Unix socket
   - Profile-driven configuration

4. **High Code Quality**
   - Standard Go project layout
   - Comprehensive test coverage
   - Bash scripts with proper error handling

5. **Well-Documented**
   - Runbook with operational procedures
   - API documentation with examples
   - Inline help in scripts and CLI

---

## Weaknesses Observed

1. **Silent Failures**
   - No error logging for critical failures
   - Generic API error messages
   - Impossible to debug without code changes

2. **Missing Observability**
   - No verbose/debug logging mode
   - No health check endpoint
   - No metrics endpoint exposed

3. **Limited Error Handling**
   - Silent cleanup on failure (no explanation)
   - Timeout doesn't distinguish stages
   - Generic "failed to provision" message

4. **Edge Cases Not Handled**
   - Missing snippets directory (created manually)
   - Multi-document YAML parsing issue
   - Profile validation occurs at startup only

---

## Recommendations for v0.2.0

### Critical (Must Fix Before Release)

1. **Fix Sandbox Provisioning Bug**
   - Add structured logging at each step of ProvisionSandbox
   - Expose actual errors in API responses
   - Log snippet creation, VM configuration, boot status
   - Distinguish between clone, configure, and boot failures

### High Priority

2. **Add Observability**
   - Implement `/v1/healthz` endpoint
   - Add verbose/debug logging mode (config flag)
   - Expose Prometheus metrics endpoint
   - Track provision success/failure rates

3. **Improve Error Messages**
   - Log actual errors before wrapping in generic messages
   - Include error context (which step failed, why)
   - Add error codes for programmatic handling

### Medium Priority

4. **Validate Environment at Startup**
   - Check `/var/lib/vz/snippets/` exists, create if missing
   - Verify template VMID exists and is actually a template
   - Validate storage pool exists and has space

5. **Debug Profile Loading**
   - Verify multi-document YAML parsing loads all profiles
   - Log profile names loaded at startup
   - Add profile validation endpoint

### Low Priority

6. **Enhance Testing**
   - Add integration test for full ProvisionSandbox workflow
   - Test with Proxmox backend (currently shell backend only tested)
   - Add smoke test script for validation

---

## Comparison to PVE_SPECS.md

| Feature | Spec | v0.1.0 Implementation | Notes |
|---------|------|--------------------------|-------|
| Daemon (agentlabd) | ✅ | ✅ | Matches spec, clean implementation |
| CLI (agentlab) | ✅ | ✅ | Unix socket, REST-like API |
| Guest bootstrap (agent-runner) | ✅ | ✅ (not tested - provisioning fails) | Bash-based, well-structured |
| Network isolation (vmbr1) | ✅ | ✅ | nftables, RFC1918/ULA blocks |
| Secrets (age encryption) | ✅ | ✅ | Default config supports age |
| Workspace volumes | ✅ | ✅ (not tested - provisioning fails) | API exists |
| Profiles (YAML) | ✅ | ⚠️ | 2 of 3 loaded |
| TTL/Lease management | ✅ | ✅ (not tested - provisioning fails) | Database schema ready |
| One-time bootstrap token | ✅ | ✅ (not tested - provisioning fails) | Logic implemented |
| TUI | Planned | ❌ | Future milestone |
| MCP server | Planned | ❌ | Future milestone |

**Overall**: Faithful implementation of spec, with critical bug in provisioning workflow.

---

## Conclusion

### What Works (Production-Ready)

1. ✅ **Infrastructure Setup** - Network, storage, templates all work perfectly
2. ✅ **Daemon & CLI** - Clean communication, version info, graceful operations
3. ✅ **Security Model** - Defense in depth, tmpfs secrets, network isolation
4. ✅ **Code Quality** - High-quality Go and bash, comprehensive tests

### What Doesn't Work (Critical Blocker)

1. ❌ **Sandbox Provisioning** - Core feature fails silently, no debugging info
2. ❌ **Guest Bootstrap** - Can't test because provisioning fails
3. ❌ **Agent Execution** - Can't test because no VMs boot

### Overall Assessment

**v0.1.0 is an excellent foundation with a critical bug in the provisioning workflow.**

The infrastructure (network, storage, templates) is production-ready. The daemon and CLI are well-implemented. The security model is sound.

**However, the primary feature (creating and booting agent sandboxes) is completely non-functional** due to silent failures with no error logging.

### Recommendations

**For Developers**:
1. Fix sandbox provisioning bug (CRITICAL - must fix before v0.2.0)
2. Add detailed logging to ProvisionSandbox workflow
3. Expose actual errors in API responses
4. Add observability (health checks, metrics, verbose logging)
5. Fix multi-document YAML profile loading

**For Users**:
1. **DO NOT USE v0.1.0** for production - provisioning is broken
2. Wait for v0.2.0 with bug fixes
3. Infrastructure is ready, so upgrade should be seamless once fixed

---

## Testing Deliverables

| File | Description | Lines |
|------|-------------|-------|
| `LESSONS_LEARNED.md` | Static code analysis and architecture review | 500+ |
| `RUNTIME_TESTING.md` | Non-Proxmox runtime testing (daemon, CLI) | 200+ |
| `ACTUAL_PROXMOX_TESTING.md` | Full Proxmox testing with infrastructure setup | 400+ |
| `SUMMARY.md` | Quick reference guide | Updated |
| `COMPLETE_SUMMARY.md` | This file - comprehensive final report | 300+ |

**Total Analysis**: ~1400+ lines of documentation

---

## Testing Timeline

| Time | Activity | Result |
|-------|----------|--------|
| 20:29 | Download release from GitHub | ✅ Success |
| 20:30-20:50 | Static code review | ✅ Complete |
| 20:50-20:55 | Build binaries with Go 1.23.5 | ✅ Success |
| 20:55-21:00 | Non-Proxmox runtime tests | ✅ Complete |
| 21:00-21:10 | Install Proxmox, setup infrastructure | ✅ Complete |
| 21:10-21:20 | Create template, test manual clone | ✅ Success |
| 21:20-21:30 | Sandbox provisioning debugging | ❌ Failed (7 attempts) |
| 21:30-21:40 | Documentation and analysis | ✅ Complete |

**Total Testing Time**: ~1 hour

---

**END OF REPORT**
