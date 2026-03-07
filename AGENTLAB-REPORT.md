# AgentLab Sessions Report

**Generated:** 2026-03-07
**Purpose:** Comprehensive review of all opencode sessions mentioning "agentlab"

---

## Table of Contents

1. [Executive Summary](#executive-summary)
2. [Session Timeline](#session-timeline)
3. [Detailed Session Analysis](#detailed-session-analysis)
4. [Key Learnings](#key-learnings)
5. [Failures & Issues](#failures--issues)
6. [Lessons Learned](#lessons-learned)
7. [Recommendations](#recommendations)

---

## Executive Summary

**Total Sessions Analyzed:** 10+ major sessions (418+ messages in main session alone)
**Date Range:** February 3-7, 2026
**Primary Topics:** OpenClaw VM setup, agentlab CLI usage, template creation, Tailscale integration, z.ai API configuration

AgentLab is a CLI tool for managing Proxmox VMs/sandboxes for AI coding environments. Sessions demonstrate its evolution from simple VM wrapper to full orchestrator, though significant gaps remain in service integration and debugging capabilities.

---

## Session Timeline

| Date | Session ID | Topic | Messages | Status |
|------|-----------|-------|----------|--------|
| Feb 3 | ses_3c61aca0cffebHFKiKG563HM5j | OpenCLAW VM setup with Tailscale + z.ai | 418 | Completed |
| Feb 7 | ses_3c7535b91ffemxnQSf9PCi8jD6 | Agentlab usage inquiry | 414 | Completed |
| Feb 8 | ses_3c2a08a5affe3cG7hMMA5gnfhQ | OpenClaw VM destruction/debugging | 183 | Completed |
| Feb 6 | ses_3444dbb0bffeslCLFwRcLix4Q2 | VM creation with agentlab to clone selkios/bare | ~50 | Completed |
| Feb 7 | ses_345a3835bffeSCQ0QHvh11Rek7 | Agentlab CLI VM 1053 issue investigation | ~40 | Completed |
| Feb 4 | ses_3da65b995ffeS3BSUyq7krsJVF | Agentlab artifact cleanup and versioning | ~260 | Completed |
| Feb 4 | ses_3d70f9e16ffeZ8wFIQzsOsIG27 | Agentlab project structure exploration | ~20 | Completed |

---

## Detailed Session Analysis

### Session 1: OpenCLAW VM Setup (ses_3c61aca0cffebHFKiKG563HM5j)
**Date:** Feb 2026
**Duration:** ~3 hours (418 messages)
**Goal:** Create long-running VM, install OpenClaw, configure Tailscale + z.ai GLM-4.7

**Key Actions:**
1. Created VM using `agentlab sandbox create`
2. Installed OpenClaw via `curl -fsSL https://openclaw.ai/install.sh | bash`
3. Configured Tailscale for secure network access
4. Set up z.ai GLM-4.7 model integration
5. Troubleshot "disconnected (1008): pairing required" errors

**Issues Encountered:**
- HTTPS requirement for browser access (Control UI needs secure context)
- Token authentication vs. device pairing confusion
- Model naming: `openai/glm-4.7` vs `glm-4.7`
- Tailscale serve vs funnel configuration

---

### Session 2: Agentlab Usage (ses_3c7535b91ffemxnQSf9PCi8jD6)
**Date:** Feb 7, 2026
**Duration:** ~2.5 hours (414 messages)
**Goal:** Explore agentlab capabilities, create OpenClaw dev environment

**Key Actions:**
1. Researched z.ai provider support in OpenClaw
2. Attempted custom provider implementation
3. Created feature branch for z.ai support
4. Documented lessons learned

**Issues Encountered:**
- OpenClaw lacks native z.ai provider
- Model format confusion (`openai/glm-4.7` invalid)
- Multiple VM creation/destruction cycles

---

### Session 3: OpenClaw VM Destruction (ses_3c2a08a5affe3cG7hMMA5gnfhQ)
**Date:** Feb 8, 2026
**Duration:** ~1.5 hours (183 messages)
**Goal:** Investigate why OpenClaw VM was destroyed

**Key Findings:**
- VMs with TTL expired automatically
- Resource cleanup wasn't thorough
- Orphaned VMs required manual cleanup via `qm destroy`

---

## Key Learnings

### 1. AgentLab CLI Architecture
- **Binary:** Go-based CLI tool
- **Backend:** Proxmox VE API via `pct`/`qm` commands
- **Templates:** YAML-based definitions in `~/.agentlab/templates/`
- **Network:** Tailscale integration for secure access
- **Storage:** ZFS datasets for VM images

### 2. VM Management Commands
```bash
agentlab sandbox create --name <name> --template <template> --memory 2G --cpu 2 --disk 10G
agentlab sandbox list
agentlab sandbox destroy <vmid>
agentlab ssh <vmid>
agentlab console <vmid>
```

### 3. Template System
- Templates define: CPU, memory, disk, network, packages
- Location: `~/.agentlab/templates/<name>.yaml`
- Cloud-init support for SSH keys and hostname

### 4. OpenClaw Integration
- **Installation:** `curl -fsSL https://openclaw.ai/install.sh | bash`
- **Requirements:** Node.js 22+, HTTPS for Control UI
- **z.ai Config:** Must use `glm-4.7` (no provider prefix)
- **Docs:** https://docs.openclaw.ai/

### 5. Network Configuration
- **Tailscale Funnel:** Required for public HTTPS access
- **Bind Mode:** Use `lan` (all interfaces) not `loopback`
- **Pairing:** Required even with Tailscale auth

---

## Failures & Issues

### Critical: Agent Bypassed AgentLab CLI

The agent frequently bypassed agentlab and used direct Proxmox commands or manual operations:

#### 1. **Direct Proxmox Commands for VM Debugging**
**When:** VM inspection/troubleshooting
**Bypass:**
```bash
# Instead of: agentlab inspect 1047
pct exec 1047 -- openclaw gateway restart
pct exec 1047 -- openclaw devices list
qm destroy 8888  # Direct VM destruction
```
**Reason:** Agentlab's abstraction was too limited for deep debugging

#### 2. **Manual Tailscale Installation**
**When:** Network setup in templates
**Bypass:**
```bash
curl -fsSL https://tailscale.com/install.sh | sh
tailscale up --authkey=${KEY}
tailscale serve https / http://127.0.0.1:18789
tailscale funnel on
```
**Reason:** No native Tailscale support in template YAML

#### 3. **OpenClaw Config Manual Editing**
**When:** z.ai provider configuration
**Bypass:**
```bash
cat > /root/.openclaw/config.json << EOF
{
  "agents": {
    "defaults": {
      "model": { "primary": "glm-4.7" },
      "models": {
        "glm-4.7": {
          "endpoint": "https://api.z.ai/v1",
          "apiKey": "${KEY}"
        }
      }
    }
  }
}
EOF
```
**Reason:** No OpenClaw-specific config helpers in agentlab

#### 4. **Direct Git Operations**
**When:** Quick repo cloning
**Bypass:**
```bash
git clone https://github.com/nibzard/openclaw.git
git clone https://github.com/selkios/bare.git
```
**Reason:** Template creation workflow too heavyweight for one-off tasks

#### 5. **Manual Log Access**
**When:** Debugging OpenClaw
**Bypass:**
```bash
pct exec 1056 -- tail -f /root/.openclaw/logs/gateway.log
```
**Reason:** Agentlab didn't provide log access mechanisms

### Root Causes of Bypasses
1. **Insufficient Abstraction Depth** - Too high-level for debugging
2. **Missing Service Integrations** - No Tailscale, OpenClaw native support
3. **Incomplete Resource Cleanup** - Orphaned VMs remained
4. **Overengineered Workflows** - Templates too complex for simple tasks
5. **Lack of Service-Specific Helpers** - No config generators for common services

### OpenClaw-Specific Issues

#### "disconnected (1008): pairing required" Error
**Cause:** Browser requires HTTPS for secure context
**Fix:** Use Tailscale funnel with proper HTTPS
```bash
tailscale serve https / http://127.0.0.1:18789
tailscale funnel on
```

#### "Unknown model: openai/glm-4.7" Error
**Cause:** Model name format incorrect
**Fix:** Use just `glm-4.7` without provider prefix
```json
"primary": "glm-4.7"  // NOT "openai/glm-4.7"
```

#### Token Authentication Issues
**Cause:** Tailscale proxy headers confuse OpenClaw
**Fix:** Configure trusted proxies
```json
"gateway": {
  "trustedProxies": ["*"],
  "auth": {
    "allowTailscale": true
  }
}
```

---

## Lessons Learned

### 1. Documentation Location
- **Main docs:** `/root/OPENCLAW_COMPLETE_GUIDE.md`
- **Lessons:** `/root/LESSONS_LEARNED.md`
- **Setup:** `/root/OPENCLAW-SETUP-INSTRUCTIONS.md`

### 2. Naming Conventions
- **VMs:** Use descriptive names (e.g., `openclaw-1`, `openclaw-gateway`)
- **Templates:** Keep simple (`ubuntu-24-04`, `ubuntu-python`)
- **Avoid:** Complex naming schemes

### 3. Security Considerations
- **API Keys:** Store in `.env` files, never commit
- **Tokens:** Regenerate regularly, don't hardcode
- **Network:** Always use Tailscale, never expose directly

### 4. Troubleshooting Workflow
1. Check VM status: `agentlab sandbox list`
2. SSH into VM: `agentlab ssh <vmid>`
3. Check logs: `pct exec <vmid> -- tail -f /path/to/log`
4. Restart services: `pct exec <vmid> -- systemctl restart <service>`
5. Verify network: `tailscale status`

### 5. AgentLab Limitations Discovered
- **No native Tailscale support** in templates
- **No OpenClaw helpers** for configuration
- **Limited debugging tools** - had to use direct `pct` commands
- **Incomplete cleanup** - orphaned VMs required manual deletion
- **Overcomplicated template workflow** for simple tasks

---

## Recommendations

### For AgentLab Development

1. **Add Tailscale Integration**
```yaml
# template.yaml
tailscale:
  enabled: true
  authkey: ${TAILSCALE_AUTH_KEY}
  serve:
    - port: 18789
      https: true
```

2. **Service-Specific Helpers**
```bash
agentlab service install openclaw --version latest
agentlab service config openclaw --set model.primary=glm-4.7
agentlab service logs openclaw --follow
```

3. **Better VM Inspection**
```bash
agentlab inspect <vmid> --deep  # Show processes, logs, network
agentlab exec <vmid> -- <command>  # Simpler than --exec -- bash -c
```

4. **Template Simplification**
```bash
# Quick clone without full template
agentlab quick-clone --repo https://github.com/user/repo --name my-vm
```

5. **Resource Cleanup**
```bash
agentlab cleanup --orphans  # Remove VMs not in agentlab's database
agentlab destroy <vmid> --force --clean-all
```

### For OpenClaw Usage

1. **Always use HTTPS** via Tailscale funnel
2. **Configure trusted proxies** when behind Tailscale
3. **Use correct model names** (no provider prefix for z.ai)
4. **Document token regeneration** procedure
5. **Keep setup instructions** updated in `/root/OPENCLAW-SETUP-INSTRUCTIONS.md`

### For Future Sessions

1. **Update lessons learned** after each session
2. **Document all bypasses** and why they were needed
3. **Create helper scripts** for common patterns
4. **Maintain this report** as living documentation
5. **Check existing docs** before starting new work

---

## Appendix: Common Commands Reference

### AgentLab CLI
```bash
# Sandbox management
agentlab sandbox create --name <name> --template <template> [options]
agentlab sandbox list
agentlab sandbox destroy <vmid>
agentlab sandbox start <vmid>
agentlab sandbox stop <vmid>

# Access
agentlab ssh <vmid>
agentlab console <vmid>

# Templates
agentlab template list
agentlab template create --name <name>
agentlab template show <name>
```

### Direct Proxmox (When AgentLab Falls Short)
```bash
pct exec <vmid> -- <command>
qm destroy <vmid>
pct destroy <vmid>
qm list
pct list
```

### Tailscale
```bash
tailscale status
tailscale up --authkey=<key>
tailscale serve https / http://127.0.0.1:<port>
tailscale funnel on
```

### OpenClaw
```bash
openclaw gateway start
openclaw gateway stop
openclaw gateway restart
openclaw gateway token
openclaw devices list
openclaw devices approve <requestId>
```

---

**Report Last Updated:** 2026-03-07 22:45 UTC
**Next Review:** After next agentlab session
