# AgentLab Improvement Plan

**Version:** 1.0  
**Date:** 2026-03-07  
**Author:** Based on AGENTLAB-REPORT.md analysis  
**Status:** Draft for Review

---

## Executive Summary

This plan addresses critical gaps in AgentLab CLI discovered through analysis of 10+ opencode sessions (418+ messages). The agent frequently bypassed AgentLab for:
- Direct Proxmox commands (debugging limitations)
- Manual service configuration (missing helpers)
- Manual network setup (no Tailscale integration)
- Direct git operations (overcomplicated templates)

**Goal:** Transform AgentLab from a thin VM wrapper into a comprehensive AI coding environment orchestrator.

**Timeline:** 4 phases over 6 months  
**Investment:** ~120-160 hours development time  
**ROI:** 10x faster environment setup, 90% reduction in manual intervention

---

## Current State Analysis

### Critical Problems Identified

#### 1. Insufficient Abstraction Depth
**Problem:** AgentLab too high-level for debugging  
**Evidence:** Agent used `pct exec` instead of `agentlab inspect`  
**Impact:** Users abandon AgentLab for troubleshooting  

#### 2. Missing Service Integrations
**Problem:** No native support for common AI/ML services  
**Evidence:** Manual Tailscale, OpenClaw, Docker setup in every session  
**Impact:** 2-3 hours of manual config per VM  

#### 3. Overengineered Template Workflow
**Problem:** Templates require 10+ files for simple setups  
**Evidence:** Agent used `git clone` instead of templates  
**Impact:** 5-minute tasks become 30-minute ordeals  

#### 4. Incomplete Resource Management
**Problem:** Cleanup leaves orphaned resources  
**Evidence:** Manual `qm destroy` needed after `agentlab destroy`  
**Impact:** Resource leaks, wasted storage  

#### 5. Poor Debugging Experience
**Problem:** No log access, limited inspection tools  
**Evidence:** Manual `tail -f` inside VMs  
**Impact:** Debugging requires SSH + manual work  

---

## Improvement Plan

### Phase 1: Quick Wins (Week 1-2)

**Goal:** Address most painful immediate issues  
**Investment:** 20-30 hours  
**Impact:** 50% reduction in manual intervention

#### 1.1 Enhanced VM Inspection
**Priority:** P0 (Critical)  
**Effort:** 8 hours  
**Impact:** High

**Current State:**
```bash
# User must do this:
pct exec 1047 -- openclaw gateway status
```

**Target State:**
```bash
# AgentLab should support:
agentlab inspect 1047 --format json
agentlab inspect 1047 --processes
agentlab inspect 1047 --logs --follow
agentlab exec 1047 -- openclaw gateway status  # Simpler syntax
```

**Implementation:**
```go
// cmd/inspect.go
type InspectOptions struct {
    Format   string // json, yaml, table
    Processes bool
    Logs     bool
    Follow   bool
}

func (c *CLI) Inspect(vmID string, opts InspectOptions) error {
    vm, err := c.client.GetVM(vmID)
    if err != nil {
        return err
    }
    
    if opts.Processes {
        return c.showProcesses(vm)
    }
    if opts.Logs {
        return c.streamLogs(vm, opts.Follow)
    }
    
    return c.showDetails(vm, opts.Format)
}
```

**Success Criteria:**
- `agentlab inspect <vmid>` shows VM details without SSH
- `agentlab exec <vmid> -- <cmd>` runs commands easier than `--exec -- bash -c`
- `agentlab logs <vmid> --follow` streams logs in real-time

---

#### 1.2 Resource Cleanup Improvements
**Priority:** P0 (Critical)  
**Effort:** 6 hours  
**Impact:** High

**Current State:**
```bash
agentlab destroy 1047
# VM destroyed but...
ls /var/lib/vz/images/1047  # Still exists!
qm list | grep 1047  # Orphaned disk image
```

**Target State:**
```bash
agentlab destroy 1047 --clean-all
# Or
agentlab cleanup --orphans
```

**Implementation:**
```go
// cmd/destroy.go
func (c *CLI) Destroy(vmID string, opts DestroyOptions) error {
    // 1. Stop VM if running
    if err := c.client.StopVM(vmID); err != nil {
        return err
    }
    
    // 2. Delete VM from Proxmox
    if err := c.client.DeleteVM(vmID); err != nil {
        return err
    }
    
    // 3. Clean up disk images
    if opts.CleanAll {
        if err := c.cleanupDiskImages(vmID); err != nil {
            log.Warnf("Failed to cleanup disks: %v", err)
        }
    }
    
    // 4. Remove from agentlab database
    return c.db.DeleteVM(vmID)
}

// cmd/cleanup.go
func (c *CLI) Cleanup(opts CleanupOptions) error {
    if opts.Orphans {
        orphans := c.findOrphanedResources()
        for _, orphan := range orphans {
            log.Infof("Removing orphan: %s", orphan)
            c.deleteResource(orphan)
        }
    }
    return nil
}
```

**Success Criteria:**
- `agentlab destroy <vmid>` removes all resources
- `agentlab cleanup --orphans` finds and removes leaked resources
- Zero manual `qm destroy` needed after agentlab operations

---

#### 1.3 Template Quick-Start
**Priority:** P1 (High)  
**Effort:** 6 hours  
**Impact:** High

**Current State:**
```bash
# User must create:
# - template.yaml
# - cloud-init.yaml
# - packages.yaml
# - network.yaml
# Just to clone a git repo!
```

**Target State:**
```bash
# Option 1: Quick clone (no template)
agentlab quick-clone https://github.com/user/repo --name my-vm

# Option 2: One-line template
agentlab template create minimal --repo https://github.com/user/repo --packages python3,nodejs

# Option 3: Inline template
agentlab create my-vm --inline 'ubuntu + python3 + git clone https://...'
```

**Implementation:**
```go
// cmd/quickclone.go
func (c *CLI) QuickClone(repoURL, name string, opts QuickCloneOptions) error {
    // 1. Create minimal VM
    vm, err := c.Create(name, CreateOptions{
        Template: "ubuntu-minimal",
        Memory:   "2G",
        CPU:      2,
        Disk:     "10G",
    })
    if err != nil {
        return err
    }
    
    // 2. Clone repo
    _, err = c.Exec(vm.ID, fmt.Sprintf("git clone %s /root/repo", repoURL))
    if err != nil {
        return err
    }
    
    // 3. Install detected dependencies
    if opts.AutoInstall {
        c.installDependencies(vm.ID, repoURL)
    }
    
    return nil
}

// cmd/template.go
func (c *CLI) CreateInlineTemplate(spec string) error {
    // Parse "ubuntu + python3 + git clone https://..."
    // Generate temporary template
    // Use it to create VM
    // Delete temporary template
}
```

**Success Criteria:**
- 5-minute tasks take 5 minutes, not 30
- No YAML files needed for simple use cases
- Git repos can be cloned with single command

---

#### 1.4 Better Error Messages
**Priority:** P1 (High)  
**Effort:** 4 hours  
**Impact:** Medium

**Current State:**
```
Error: failed to create VM
```

**Target State:**
```
Error: failed to create VM
  Reason: Insufficient memory (requested: 8G, available: 2G)
  Suggestion: Reduce memory to 2G or free up resources
  Hint: Run 'agentlab status' to see available resources
```

**Implementation:**
```go
// pkg/errors.go
type AgentLabError struct {
    Op         string // Operation that failed
    Reason     string // Why it failed
    Suggestion string // How to fix it
    Hint       string // Helpful command
}

func (e *AgentLabError) Error() string {
    return fmt.Sprintf("Error: %s\n  Reason: %s\n  Suggestion: %s\n  Hint: %s",
        e.Op, e.Reason, e.Suggestion, e.Hint)
}
```

**Success Criteria:**
- Every error includes actionable guidance
- Users can self-service fix 80% of issues
- No cryptic "operation failed" messages

---

### Phase 2: Core Improvements (Week 3-6)

**Goal:** Add critical missing integrations  
**Investment:** 40-60 hours  
**Impact:** 80% reduction in manual config

#### 2.1 Tailscale Integration
**Priority:** P0 (Critical)  
**Effort:** 12 hours  
**Impact:** Very High

**Current State:**
```bash
# Manual installation required EVERY TIME:
curl -fsSL https://tailscale.com/install.sh | sh
tailscale up --authkey=${TAILSCALE_AUTH_KEY}
tailscale serve https / http://127.0.0.1:18789
tailscale funnel on
```

**Target State:**
```yaml
# template.yaml
network:
  type: tailscale
  authkey: ${TAILSCALE_AUTH_KEY}
  hostname: my-vm
  serve:
    - port: 18789
      https: true
      funnel: true
```

**Or CLI:**
```bash
agentlab create my-vm --tailscale --hostname my-vm --expose 18789
```

**Implementation:**
```go
// pkg/network/tailscale.go
type TailscaleConfig struct {
    AuthKey  string
    Hostname string
    Serve    []ServeConfig
    Funnel   bool
}

func (t *TailscaleProvisioner) Provision(vm *VM, config TailscaleConfig) error {
    // 1. Install Tailscale
    if err := t.install(vm); err != nil {
        return err
    }
    
    // 2. Connect to network
    if err := t.connect(vm, config.AuthKey, config.Hostname); err != nil {
        return err
    }
    
    // 3. Configure serve
    for _, serve := range config.Serve {
        if err := t.configureServe(vm, serve); err != nil {
            return err
        }
    }
    
    // 4. Enable funnel if requested
    if config.Funnel {
        if err := t.enableFunnel(vm); err != nil {
            return err
        }
    }
    
    return nil
}
```

**Success Criteria:**
- Zero manual Tailscale setup
- Templates support Tailscale as first-class citizen
- `agentlab create --tailscale` works in < 30 seconds

---

#### 2.2 Service Helpers (OpenClaw, Docker, etc.)
**Priority:** P0 (Critical)  
**Effort:** 16 hours  
**Impact:** Very High

**Current State:**
```bash
# Manual installation + configuration:
curl -fsSL https://openclaw.ai/install.sh | bash
cat > /root/.openclaw/config.json << EOF
{
  "agents": {
    "defaults": {
      "model": { "primary": "glm-4.7" },
      "models": {
        "glm-4.7": {
          "endpoint": "https://api.z.ai/v1",
          "apiKey": "${ZAI_API_KEY}"
        }
      }
    }
  }
}
EOF
```

**Target State:**
```bash
# Install
agentlab service install openclaw --version latest

# Configure
agentlab service config openclaw \
  --set model.primary=glm-4.7 \
  --set model.glm-4.7.endpoint=https://api.z.ai/v1 \
  --set model.glm-4.7.apiKey=${ZAI_API_KEY}

# Manage
agentlab service start openclaw
agentlab service logs openclaw --follow
agentlab service status openclaw
```

**Implementation:**
```go
// pkg/services/manager.go
type ServiceManager interface {
    Install(vm *VM, version string) error
    Configure(vm *VM, config map[string]interface{}) error
    Start(vm *VM) error
    Stop(vm *VM) error
    Restart(vm *VM) error
    Status(vm *VM) (ServiceStatus, error)
    Logs(vm *VM, follow bool) (io.Reader, error)
}

// pkg/services/openclaw.go
type OpenClawService struct{}

func (o *OpenClawService) Install(vm *VM, version string) error {
    // Download and install OpenClaw
    _, err := vm.Exec(fmt.Sprintf(
        "curl -fsSL https://openclaw.ai/install.sh | bash -s -- --version %s",
        version,
    ))
    return err
}

func (o *OpenClawService) Configure(vm *VM, config map[string]interface{}) error {
    // Generate config.json from structured input
    configPath := "/root/.openclaw/config.json"
    configJSON, err := json.MarshalIndent(config, "", "  ")
    if err != nil {
        return err
    }
    
    return vm.WriteFile(configPath, configJSON)
}

// Register services
func init() {
    RegisterService("openclaw", &OpenClawService{})
    RegisterService("docker", &DockerService{})
    RegisterService("nginx", &NginxService{})
    RegisterService("postgresql", &PostgreSQLService{})
}
```

**Success Criteria:**
- Common services installable with single command
- Configuration via flags, not manual file editing
- Service logs accessible via `agentlab service logs`

---

#### 2.3 Improved Template System
**Priority:** P1 (High)  
**Effort:** 10 hours  
**Impact:** High

**Current State:**
```yaml
# template.yaml (complex)
metadata:
  name: my-template
  version: 1.0
spec:
  hardware:
    cpu: 2
    memory: 2G
    disk: 10G
  os:
    distribution: ubuntu
    version: "24.04"
  packages:
    - name: python3
    - name: nodejs
      version: "22"
  network:
    type: bridge
  cloudInit:
    userData: |
      #cloud-config
      ...
```

**Target State:**
```yaml
# Option 1: Minimal (for 80% use cases)
name: python-dev
base: ubuntu-24.04
packages: [python3, python3-pip, git]
```

```yaml
# Option 2: With services
name: openclaw-server
base: ubuntu-24.04
packages: [curl, git]
services:
  openclaw:
    version: latest
    config:
      model.primary: glm-4.7
```

```yaml
# Option 3: Full control (only when needed)
name: complex-setup
base: ubuntu-24.04
hardware:
  cpu: 4
  memory: 8G
  disk: 50G
packages: [python3, docker.io]
services:
  docker: {}
  openclaw:
    config: {model.primary: glm-4.7}
network:
  tailscale:
    hostname: openclaw-server
    expose: [18789]
```

**Implementation:**
```go
// pkg/template/v2.go
type TemplateV2 struct {
    Name     string                 `yaml:"name"`
    Base     string                 `yaml:"base"`
    Packages []string               `yaml:"packages,omitempty"`
    Services map[string]ServiceConfig `yaml:"services,omitempty"`
    Hardware *HardwareConfig        `yaml:"hardware,omitempty"`
    Network  *NetworkConfig         `yaml:"network,omitempty"`
}

func (t *TemplateV2) ToLegacy() (*Template, error) {
    // Convert v2 to internal representation
    // Defaults:
    // - Hardware: 2 CPU, 2G RAM, 10G disk
    // - Network: bridge
    // - Cloud-Init: auto-generated
}
```

**Success Criteria:**
- 5-line templates for 80% of use cases
- No YAML complexity for simple setups
- Backward compatible with v1 templates

---

#### 2.4 Snapshot & Rollback
**Priority:** P2 (Medium)  
**Effort:** 8 hours  
**Impact:** Medium

**Current State:**
```bash
# No built-in snapshot support
# User must use Proxmox directly:
qm snapshot 1047 before-upgrade
# ... later ...
qm rollback 1047 before-upgrade
```

**Target State:**
```bash
agentlab snapshot create 1047 --name before-upgrade
agentlab snapshot list 1047
agentlab snapshot rollback 1047 before-upgrade
agentlab snapshot delete 1047 before-upgrade
```

**Implementation:**
```go
// cmd/snapshot.go
func (c *CLI) SnapshotCreate(vmID, name string) error {
    return c.client.QMSnapshot(vmID, name)
}

func (c *CLI) SnapshotRollback(vmID, name string) error {
    return c.client.QMRollback(vmID, name)
}
```

**Success Criteria:**
- Snapshots managed through AgentLab CLI
- Automatic snapshots before risky operations
- Snapshot metadata stored in AgentLab DB

---

### Phase 3: Advanced Features (Month 2-3)

**Goal:** Enable complex workflows and automation  
**Investment:** 40-50 hours  
**Impact:** Enable enterprise use cases

#### 3.1 Environment Variables & Secrets Management
**Priority:** P1 (High)  
**Effort:** 12 hours  
**Impact:** High

**Current State:**
```bash
# Secrets in plain text:
cat > /root/.openclaw/config.json << EOF
{
  "apiKey": "sk-real-key-here"  # 😱
}
EOF
```

**Target State:**
```bash
# Store secrets securely
agentlab secret add ZAI_API_KEY --value "sk-..." --scope global
agentlab secret add OPENCLAW_TOKEN --value "..." --scope vm:1047

# Use in templates
agentlab service config openclaw \
  --set model.glm-4.7.apiKey=${ZAI_API_KEY}

# Or in templates
services:
  openclaw:
    config:
      model.glm-4.7.apiKey: ${ZAI_API_KEY}  # Injected at create time
```

**Implementation:**
```go
// pkg/secrets/manager.go
type SecretScope string

const (
    ScopeGlobal SecretScope = "global"
    ScopeVM     SecretScope = "vm"
    ScopeTemplate SecretScope = "template"
)

type SecretsManager interface {
    Add(key, value string, scope SecretScope, target string) error
    Get(key string, scope SecretScope, target string) (string, error)
    List(scope SecretScope, target string) ([]string, error)
    Delete(key string, scope SecretScope, target string) error
}

// Encrypted storage
type EncryptedSecretsStore struct {
    keyfile string
    db      *sql.DB
}
```

**Success Criteria:**
- Secrets encrypted at rest
- Secrets injected at VM creation
- No secrets in template files
- Audit log of secret access

---

#### 3.2 Multi-VM Orchestration
**Priority:** P1 (High)  
**Effort:** 16 hours  
**Impact:** High

**Current State:**
```bash
# Manual coordination:
agentlab create vm1 ...
agentlab create vm2 ...
# SSH between VMs manually configured
# Service discovery manual
```

**Target State:**
```yaml
# environment.yaml
name: ml-pipeline
vms:
  - name: database
    template: postgresql
    hostname: db.internal
    
  - name: backend
    template: python-api
    depends_on: [database]
    env:
      DATABASE_URL: postgresql://database:5432/ml
    
  - name: frontend
    template: nginx
    depends_on: [backend]
    expose: [80]
```

```bash
agentlab env create ml-pipeline --file environment.yaml
agentlab env status ml-pipeline
agentlab env destroy ml-pipeline
```

**Implementation:**
```go
// pkg/environment/manager.go
type Environment struct {
    Name    string
    VMs     []VMSpec
    Network NetworkSpec
}

func (e *EnvironmentManager) Create(env *Environment) error {
    // 1. Topological sort (respect dependencies)
    // 2. Create VMs in order
    // 3. Configure networking
    // 4. Inject service discovery
    // 5. Run health checks
}

func (e *EnvironmentManager) Status(name string) (*EnvironmentStatus, error) {
    // Show status of all VMs
    // Show connections between them
    // Show health checks
}
```

**Success Criteria:**
- Multi-VM environments defined in single file
- Automatic service discovery between VMs
- Dependency-aware creation order
- Single command to destroy entire environment

---

#### 3.3 Health Checks & Auto-Recovery
**Priority:** P2 (Medium)  
**Effort:** 10 hours  
**Impact:** Medium

**Current State:**
```bash
# Manual health checks:
agentlab ssh 1047 -- systemctl status openclaw
# No auto-restart on failure
```

**Target State:**
```bash
# Define health checks
agentlab health-check add 1047 --name openclaw \
  --command "curl -f http://localhost:18789/health" \
  --interval 30s \
  --timeout 5s \
  --on-failure restart

# Monitor
agentlab health-check status 1047
agentlab health-check logs 1047
```

**Implementation:**
```go
// pkg/health/monitor.go
type HealthCheck struct {
    Name       string
    Command    string
    Interval   time.Duration
    Timeout    time.Duration
    OnFailure  string // restart, alert, ignore
}

func (h *HealthMonitor) Start(vm *VM, check HealthCheck) error {
    ticker := time.NewTicker(check.Interval)
    for range ticker.C {
        if err := h.runCheck(vm, check); err != nil {
            h.handleFailure(vm, check, err)
        }
    }
}
```

**Success Criteria:**
- Services automatically restarted on failure
- Health status visible in `agentlab status`
- Alerting integration (webhooks, Slack)

---

#### 3.4 Plugin System
**Priority:** P2 (Medium)  
**Effort:** 12 hours  
**Impact:** Medium

**Target State:**
```go
// Community plugins extend AgentLab
type Plugin interface {
    Name() string
    Initialize(config map[string]interface{}) error
    Hooks() []Hook
}

type Hook struct {
    Event string // pre-create, post-create, pre-destroy
    Handler func(ctx *HookContext) error
}

// Example: Custom monitoring plugin
type PrometheusPlugin struct{}

func (p *PrometheusPlugin) Hooks() []Hook {
    return []Hook{
        {
            Event: "post-create",
            Handler: func(ctx *HookContext) error {
                // Add VM to Prometheus scraping
                return p.addScrapeTarget(ctx.VM)
            },
        },
        {
            Event: "pre-destroy",
            Handler: func(ctx *HookContext) error {
                // Remove from Prometheus
                return p.removeScrapeTarget(ctx.VM)
            },
        },
    }
}
```

**Success Criteria:**
- Third-party plugins supported
- Plugin marketplace/repository
- Core features implemented as plugins

---

### Phase 4: Long-term Architecture (Month 4-6)

**Goal:** Enterprise readiness and scalability  
**Investment:** 40-50 hours  
**Impact:** Production-grade reliability

#### 4.1 Web UI Dashboard
**Priority:** P2 (Medium)  
**Effort:** 40 hours  
**Impact:** Medium

**Target State:**
- React-based dashboard
- VM visualization and management
- Template builder with GUI
- Real-time logs and metrics
- Multi-user support

**MVP Features:**
- VM list and status
- Create/destroy VMs
- View logs
- Template browser

---

#### 4.2 API Server
**Priority:** P2 (Medium)  
**Effort:** 20 hours  
**Impact:** Medium

**Target State:**
```bash
# Start API server
agentlab serve --port 8080 --auth token

# API usage
curl -H "Authorization: Bearer ${TOKEN}" \
  http://localhost:8080/api/v1/vms

curl -X POST -H "Authorization: Bearer ${TOKEN}" \
  -d '{"name":"test","template":"ubuntu-24.04"}' \
  http://localhost:8080/api/v1/vms
```

**Success Criteria:**
- RESTful API for all CLI operations
- Authentication (token, OAuth)
- Rate limiting
- OpenAPI documentation

---

#### 4.3 Distributed Mode
**Priority:** P3 (Low)  
**Effort:** 30 hours  
**Impact:** Low (enterprise only)

**Target State:**
- Multiple Proxmox clusters
- Central AgentLab controller
- VM placement optimization
- Cross-cluster networking

---

#### 4.4 Integration with CI/CD
**Priority:** P2 (Medium)  
**Effort:** 12 hours  
**Impact:** Medium

**Target State:**
```yaml
# .github/workflows/test.yml
- name: Create test environment
  run: |
    agentlab env create test-env --file environment.yaml
    agentlab env wait test-env --timeout 5m

- name: Run tests
  run: |
    agentlab exec test-env:backend -- pytest

- name: Cleanup
  if: always()
  run: agentlab env destroy test-env
```

**Success Criteria:**
- GitHub Actions integration
- GitLab CI integration
- Ephemeral environments for testing
- Automatic cleanup

---

## Success Metrics

### Phase 1 Success Criteria
- [ ] `agentlab inspect` shows VM details without SSH
- [ ] `agentlab destroy` cleans up all resources 100% of time
- [ ] `agentlab quick-clone` works in < 5 minutes
- [ ] Error messages include actionable guidance
- [ ] 50% reduction in manual `pct`/`qm` commands

### Phase 2 Success Criteria
- [ ] Zero manual Tailscale setup in templates
- [ ] OpenClaw installs with single command
- [ ] 80% of templates are < 10 lines
- [ ] Snapshots managed through CLI
- [ ] 80% reduction in manual config editing

### Phase 3 Success Criteria
- [ ] Secrets encrypted and injected automatically
- [ ] Multi-VM environments created from single file
- [ ] Health checks auto-restart failed services
- [ ] 3+ community plugins available
- [ ] Zero-downtime deployments possible

### Phase 4 Success Criteria
- [ ] Web UI available for non-CLI users
- [ ] API server enables remote management
- [ ] CI/CD integrations documented
- [ ] Multi-cluster support for enterprises
- [ ] Production-ready (SLA > 99.9%)

---

## Implementation Guidelines

### Development Workflow
1. **Feature Branches:** `feat/inspect-enhancement`, `feat/tailscale-integration`
2. **Testing:** Unit tests + integration tests with real Proxmox
3. **Documentation:** Update README.md and docs/ for each feature
4. **Review:** PR reviews before merging
5. **Release:** Semantic versioning (v0.1.0 → v0.2.0 → v1.0.0)

### Testing Strategy
```go
// Unit tests
func TestInspectVM(t *testing.T) {
    mockClient := &MockProxmoxClient{}
    cli := &CLI{client: mockClient}
    
    err := cli.Inspect("1047", InspectOptions{})
    assert.NoError(t, err)
}

// Integration tests
func TestCreateAndDestroy(t *testing.T) {
    if testing.Short() {
        t.Skip("Integration test")
    }
    
    cli := NewCLIFromEnv()
    vm, err := cli.Create("test-vm", CreateOptions{Template: "ubuntu-minimal"})
    require.NoError(t, err)
    
    defer cli.Destroy(vm.ID, DestroyOptions{CleanAll: true})
    
    // Test operations...
}
```

### Documentation Requirements
- [ ] README.md updated with new features
- [ ] CLI help text comprehensive
- [ ] Examples for common use cases
- [ ] Migration guide from v1 to v2 templates
- [ ] Architecture diagrams
- [ ] Video tutorials for complex features

---

## Risk Assessment

### High Risk
- **Breaking Changes:** v2 template format may break existing templates
  - *Mitigation:* Backward compatibility layer, migration tool
  
- **Proxmox API Changes:** Upstream changes may break integration
  - *Mitigation:* Version pinning, API compatibility checks

### Medium Risk
- **Feature Creep:** Too many features may complicate CLI
  - *Mitigation:* Plugin system for non-core features
  
- **Performance:** Complex operations may be slow
  - *Mitigation:* Async operations, progress indicators

### Low Risk
- **Adoption:** Users may not migrate from manual workflows
  - *Mitigation:* Clear value demonstration, migration guides

---

## Resource Requirements

### Personnel
- **Lead Developer:** 1 FTE (full-time equivalent)
- **Contributors:** 2-3 part-time
- **Technical Writer:** 0.5 FTE for documentation

### Infrastructure
- **Proxmox Cluster:** 3+ nodes for testing
- **CI/CD:** GitHub Actions runners
- **Documentation Hosting:** GitHub Pages or Netlify

### Budget Estimate
- **Phase 1:** $5,000-8,000 (contractor rates)
- **Phase 2:** $12,000-16,000
- **Phase 3:** $10,000-14,000
- **Phase 4:** $12,000-16,000
- **Total:** $39,000-54,000

---

## Next Steps

### Immediate Actions (This Week)
1. [ ] Review and approve this plan
2. [ ] Set up development environment
3. [ ] Create GitHub project board
4. [ ] Start Phase 1, Item 1.1 (Enhanced VM Inspection)

### Short-term (Next 2 Weeks)
1. [ ] Complete Phase 1 (Quick Wins)
2. [ ] Gather user feedback
3. [ ] Adjust Phase 2 priorities based on feedback

### Long-term (Next 3 Months)
1. [ ] Complete Phase 2 and 3
2. [ ] Beta release with early adopters
3. [ ] Plan Phase 4 based on adoption metrics

---

## Appendix A: Template Migration Guide

### v1 → v2 Template Conversion

**Before (v1 - Complex):**
```yaml
metadata:
  name: python-dev
  version: 1.0.0
spec:
  hardware:
    cpu: 2
    memory: 2G
    disk: 10G
  os:
    distribution: ubuntu
    version: "24.04"
  packages:
    - name: python3
    - name: python3-pip
  network:
    type: bridge
```

**After (v2 - Simple):**
```yaml
name: python-dev
base: ubuntu-24.04
packages: [python3, python3-pip]
```

**Migration Tool:**
```bash
agentlab template migrate v1-to-v2 old-template.yaml > new-template.yaml
```

---

## Appendix B: Command Reference

### New Commands (Phase 1)

```bash
# Inspection
agentlab inspect <vmid> [--format json|yaml|table] [--processes] [--logs] [--follow]
agentlab exec <vmid> -- <command>
agentlab logs <vmid> [--follow]

# Cleanup
agentlab destroy <vmid> --clean-all
agentlab cleanup --orphans

# Quick operations
agentlab quick-clone <repo-url> --name <name>
agentlab template create <name> --inline 'ubuntu + python3'

# Snapshots
agentlab snapshot create <vmid> --name <name>
agentlab snapshot list <vmid>
agentlab snapshot rollback <vmid> --name <name>
agentlab snapshot delete <vmid> --name <name>
```

### New Commands (Phase 2)

```bash
# Services
agentlab service install <service> --version <version>
agentlab service config <service> --set key=value
agentlab service start <service>
agentlab service stop <service>
agentlab service logs <service> [--follow]

# Network
agentlab network setup tailscale --authkey <key> --hostname <name>
agentlab network expose <port> --https --funnel
```

### New Commands (Phase 3)

```bash
# Secrets
agentlab secret add <key> --value <value> [--scope global|vm:<id>]
agentlab secret list [--scope global|vm:<id>]
agentlab secret delete <key>

# Environments
agentlab env create <name> --file environment.yaml
agentlab env status <name>
agentlab env destroy <name>

# Health checks
agentlab health-check add <vmid> --name <name> --command <cmd>
agentlab health-check status <vmid>
```

---

**Document Status:** Draft  
**Next Review:** After Phase 1 completion  
**Feedback:** Create GitHub issue with label `plan-feedback`
