# AgentLab Architecture Diagrams

This document provides visual architecture diagrams for AgentLab using Mermaid syntax. All diagrams render natively on GitHub and can be previewed using tools like [mermaid.live](https://mermaid.live).

## Table of Contents

1. [System Architecture](#system-architecture)
2. [Sandbox State Machine](#sandbox-state-machine)
3. [Network Topology](#network-topology)
4. [Job Execution Data Flow](#job-execution-data-flow)
5. [Database Schema](#database-schema)
6. [Request Lifecycle](#request-lifecycle)
7. [Component Interaction](#component-interaction)

---

## System Architecture

The high-level system architecture shows all major components and their relationships.

```mermaid
graph TB
    subgraph "Client Layer"
        CLI[agentlab CLI]
        Scripts[Automation Scripts]
    end

    subgraph "Daemon Layer (agentlabd)"
        UnixSock[Unix Socket API<br/>/run/agentlab/agentlabd.sock]
        ControlAPI[Control API Handler]

        subgraph "Managers"
            SandboxMgr[Sandbox Manager<br/>lifecycle & state]
            WorkspaceMgr[Workspace Manager<br/>persistent volumes]
            JobOrch[Job Orchestrator<br/>provisioning & execution]
            ArtifactGC[Artifact GC<br/>cleanup & retention]
        end

        subgraph "Proxmox Backend"
            APIBackend[API Backend<br/>HTTP/REST]
            ShellBackend[Shell Backend<br/>qm/pvesh commands]
        end

        SQLite[(SQLite Database<br/>state & events)]
        Metrics[Prometheus Metrics<br/>:9090/metrics]
    end

    subgraph "Guest Bootstrap API"
        BootstrapAPI[Bootstrap API<br/>10.77.0.1:4242]
        Secrets[Secrets Store<br/>age/sops bundles]
    end

    subgraph "Artifact API"
        ArtifactAPI[Artifact API<br/>10.77.0.1:4243]
        ArtifactStorage[(Artifact Storage<br/>/var/lib/agentlab/artifacts)]
    end

    subgraph "Proxmox VE"
        Template[Template VM<br/>ID: 9000]
        Hypervisor[Hypervisor & Cluster]
        Storage[(Storage Pools<br/>local-zfs, local-lvm)]
    end

    subgraph "Sandbox VMs"
        VM1[Sandbox VM 1000]
        VM2[Sandbox VM 1001]
        Runner[agent-runner<br/>in-sandbox service]
    end

    CLI -->|Unix Socket| UnixSock
    Scripts -->|Unix Socket| UnixSock

    UnixSock --> ControlAPI
    ControlAPI --> SandboxMgr
    ControlAPI --> WorkspaceMgr
    ControlAPI --> JobOrch

    SandboxMgr --> SQLite
    WorkspaceMgr --> SQLite
    JobOrch --> SQLite

    SandboxMgr --> APIBackend
    SandboxMgr --> ShellBackend
    WorkspaceMgr --> APIBackend
    JobOrch --> APIBackend

    APIBackend --> Hypervisor
    ShellBackend --> Hypervisor

    Hypervisor --> Template
    Hypervisor --> Storage
    Hypervisor --> VM1
    Hypervisor --> VM2

    JobOrch --> BootstrapAPI
    BootstrapAPI --> Secrets

    Runner -->|HTTP| BootstrapAPI
    Runner -->|HTTP| ArtifactAPI

    ArtifactAPI --> ArtifactStorage
    JobOrch --> ArtifactStorage

    ControlAPI --> Metrics
    SandboxMgr --> Metrics
    JobOrch --> Metrics

    classDef client fill:#e1f5fe,stroke:#01579b,stroke-width:2px
    classDef daemon fill:#f3e5f5,stroke:#4a148c,stroke-width:2px
    classDef storage fill:#fff3e0,stroke:#e65100,stroke-width:2px
    classDef proxmox fill:#e8f5e9,stroke:#1b5e20,stroke-width:2px
    classDef vm fill:#ffebee,stroke:#b71c1c,stroke-width:2px

    class CLI,Scripts client
    class UnixSock,ControlAPI,SandboxMgr,WorkspaceMgr,JobOrch,ArtifactGC,APIBackend,ShellBackend,BootstrapAPI,ArtifactAPI,Metrics,Secrets daemon
    class SQLite,ArtifactStorage storage
    class Hypervisor,Template,Storage proxmox
    class VM1,VM2,Runner vm
```

### Component Descriptions

| Component | Description |
|-----------|-------------|
| **agentlab CLI** | Command-line interface for users to control sandboxes, jobs, and workspaces |
| **Unix Socket API** | Local-only API endpoint for secure daemon communication |
| **Sandbox Manager** | Manages VM lifecycle, state transitions, lease enforcement, and reconciliation |
| **Workspace Manager** | Handles persistent workspace volume creation, attachment, and detachment |
| **Job Orchestrator** | Coordinates job provisioning, bootstrap, execution, and artifact collection |
| **Proxmox Backend** | Abstraction layer supporting both API (HTTP) and Shell (CLI) backends |
| **SQLite Database** | Embedded database for sandboxes, jobs, workspaces, tokens, and events |
| **Bootstrap API** | Guest-facing API for VM initialization, secrets delivery, and job startup |
| **Artifact API** | Guest-facing API for uploading job results and artifacts |
| **agent-runner** | Service running inside template VMs for bootstrap and job execution |

---

## Sandbox State Machine

The sandbox state machine defines all valid states and transitions for VM sandboxes.

```mermaid
stateDiagram-v2
    [*] --> REQUESTED: Create sandbox

    REQUESTED --> PROVISIONING: Clone template & configure
    PROVISIONING --> BOOTING: VM start initiated
    BOOTING --> READY: Guest agent reports ready
    READY --> RUNNING: Job execution starts

    RUNNING --> COMPLETED: Job finished successfully
    RUNNING --> FAILED: Job failed
    RUNNING --> STOPPED: Manual stop
    RUNNING --> TIMEOUT: Lease expired

    COMPLETED --> STOPPED: Auto-stop after job
    FAILED --> STOPPED: Auto-stop after failure
    TIMEOUT --> STOPPED: Auto-stop on timeout

    STOPPED --> DESTROYED: Destroy VM
    TIMEOUT --> DESTROYED: Force destroy
    COMPLETED --> DESTROYED: Immediate cleanup
    FAILED --> DESTROYED: Immediate cleanup

    REQUESTED --> DESTROYED: Cancel before provisioning
    PROVISIONING --> DESTROYED: Cancel during provisioning
    BOOTING --> DESTROYED: Cancel during boot
    READY --> DESTROYED: Destroy before running

    DESTROYED --> [*]

    note right of REQUESTED
        Initial state
        VM not yet created
    end note

    note right of RUNNING
        Active execution
        Lease may be renewed
        if keepalive=true
    end note

    note right of DESTROYED
        Terminal state
        VM removed from Proxmox
        Database record retained
    end note
```

### State Descriptions

| State | Description | Allowed Operations |
|-------|-------------|-------------------|
| **REQUESTED** | Sandbox creation requested, VM not yet created | show, logs, destroy |
| **PROVISIONING** | VM being cloned and configured | show, logs, destroy |
| **BOOTING** | VM starting, waiting for guest agent | show, logs, destroy |
| **READY** | VM ready, waiting for job or manual use | show, logs, destroy |
| **RUNNING** | Job actively executing | show, logs, destroy, lease renew |
| **COMPLETED** | Job finished successfully | show, logs, destroy |
| **FAILED** | Job execution failed | show, logs, destroy |
| **TIMEOUT** | Lease expired, VM may be stopped | show, logs, destroy |
| **STOPPED** | VM stopped but not destroyed | show, logs, start, destroy |
| **DESTROYED** | VM destroyed, terminal state | show |

---

## Network Topology

The network architecture shows how AgentLab isolates sandbox VMs while providing controlled Internet access.

```mermaid
graph TB
    Internet[(Internet)]

    subgraph "Proxmox Host"
        Firewall[iptables/nftables<br/>egress filtering]

        subgraph "vmbr0 - LAN/WAN Bridge"
            HostEth0[eth0<br/>host network]
        end

        subgraph "vmbr1 - Agent Subnet Bridge"
            BridgeIP[10.77.0.1/16<br/>host address]
            DHCP[dnsmasq DHCP<br/>10.77.0.0/16 pool]
            NAT[NAT/Masquerade<br/>vmbr1 → vmbr0]
        end

        subgraph "Daemon Services"
            BootstrapSrv[Bootstrap API<br/>10.77.0.1:4242]
            ArtifactSrv[Artifact API<br/>10.77.0.1:4243]
            Tailscale[Tailscale Router<br/>optional subnet route]
        end
    end

    subgraph "Sandbox VMs"
        VM1[Sandbox VM 1000<br/>10.77.0.10]
        VM2[Sandbox VM 1001<br/>10.77.0.11]
        VM3[Sandbox VM 1002<br/>10.77.0.12]
    end

    Internet --> HostEth0
    HostEth0 --> Firewall

    Firewall --> NAT
    NAT --> Internet

    BridgeIP --> DHCP
    DHCP --> VM1
    DHCP --> VM2
    DHCP --> VM3

    VM1 --> BridgeIP
    VM2 --> BridgeIP
    VM3 --> BridgeIP

    BridgeIP --> BootstrapSrv
    BridgeIP --> ArtifactSrv
    BridgeIP --> Tailscale

    Tailscale --> Internet

    classDef external fill:#e3f2fd,stroke:#1565c0,stroke-width:3px
    classDef bridge fill:#fff8e1,stroke:#ff6f00,stroke-width:2px
    classDef service fill:#f3e5f5,stroke:#7b1fa2,stroke-width:2px
    classDef vm fill:#ffebee,stroke:#c62828,stroke-width:2px

    class Internet external
    class HostEth0,BridgeIP,DHCP,NAT,Firewall bridge
    class BootstrapSrv,ArtifactSrv,Tailscale service
    class VM1,VM2,VM3 vm
```

### Network Configuration

| Component | Configuration | Purpose |
|-----------|---------------|---------|
| **vmbr0** | LAN/WAN bridge (existing) | Host connectivity to Internet |
| **vmbr1** | Agent subnet (10.77.0.0/16) | Isolated sandbox network |
| **Host Address** | 10.77.0.1/16 | Gateway for sandbox VMs |
| **DHCP Pool** | 10.77.0.100 - 10.77.255.254 | IP assignment for sandboxes |
| **NAT/Masquerade** | vmbr1 → vmbr0 | Outbound Internet access |
| **Egress Rules** | Block RFC1918/ULA | Prevent private network access |
| **Bootstrap API** | 10.77.0.1:4242 | Guest initialization endpoint |
| **Artifact API** | 10.77.0.1:4243 | Artifact upload endpoint |
| **Tailscale** | Optional subnet router | Remote SSH access |

### Security Rules

```bash
# Allow outbound from agent subnet
iptables -A FORWARD -i vmbr1 -o vmbr0 -j ACCEPT
iptables -A FORWARD -i vmbr0 -o vmbr1 -m state --state RELATED,ESTABLISHED -j ACCEPT

# Masquerade outbound traffic
iptables -t nat -A POSTROUTING -s 10.77.0.0/16 -o vmbr0 -j MASQUERADE

# Block RFC1918 egress from agent subnet
iptables -A FORWARD -i vmbr1 -o vmbr0 -d 10.0.0.0/8 -j REJECT
iptables -A FORWARD -i vmbr1 -o vmbr0 -d 172.16.0.0/12 -j REJECT
iptables -A FORWARD -i vmbr1 -o vmbr0 -d 192.168.0.0/16 -j REJECT
iptables -A FORWARD -i vmbr1 -o vmbr0 -d fc00::/7 -j REJECT
```

---

## Job Execution Data Flow

The job execution flow shows how a job progresses from CLI command to artifact collection.

```mermaid
sequenceDiagram
    participant User as User
    participant CLI as agentlab CLI
    participant API as Control API
    participant Orch as Job Orchestrator
    participant Prox as Proxmox Backend
    participant VM as Sandbox VM
    participant Runner as agent-runner
    participant Bootstrap as Bootstrap API
    participant Artifacts as Artifact API

    User->>CLI: job run --repo URL --task TASK
    CLI->>API: POST /v1/jobs
    API-->>CLI: job_id: job_abc123

    Note over Orch: Job queued in database

    Orch->>Orch: Start(job_id) in goroutine

    Orch->>Prox: Allocate VMID
    Prox-->>Orch: vmid: 1000

    Orch->>Prox: Clone template VM
    Prox-->>Orch: Clone started

    Orch->>Prox: Create cloud-init snippet
    Orch->>Prox: Apply profile resources
    Orch->>Prox: Start VM
    Prox-->>Orch: VM running

    VM->>Bootstrap: GET /bootstrap?token=TOKEN
    Bootstrap->>Bootstrap: Validate token & VMID
    Bootstrap-->>VM: 200 OK { secrets, config, job }

    Runner->>Runner: Extract secrets to tmpfs
    Runner->>Runner: Clone git repository
    Runner->>Runner: Install dependencies

    Note over Runner: Execute job task

    Runner->>Runner: Run task: TASK
    Runner->>Runner: Collect artifacts

    Runner->>Artifacts: POST /upload?token=TOKEN
    Artifacts->>Artifacts: Validate token & job
    Artifacts->>Artifacts: Store artifact
    Artifacts-->>Runner: 201 Created { artifact_id }

    Runner->>Bootstrap: POST /report?token=TOKEN
    Bootstrap->>Orch: Update job status
    Bootstrap-->>Runner: 200 OK

    Runner->>Runner: Cleanup secrets from tmpfs

    User->>CLI: job show job_abc123
    CLI->>API: GET /v1/jobs/job_abc123
    API-->>CLI: Job status: COMPLETED

    User->>CLI: job artifacts download job_abc123
    CLI->>API: GET /v1/jobs/job_abc123/artifacts/download
    API-->>CLI: Artifact tarball

    Note over Orch,VM: Lease enforcement continues<br/>in background
```

### Job Execution Stages

| Stage | Description | Duration |
|-------|-------------|----------|
| **Queued** | Job created, waiting for orchestration | < 1s |
| **Provisioning** | Clone VM, configure resources, start | 30-120s |
| **Booting** | VM boot, guest agent startup | 10-30s |
| **Bootstrap** | Agent initialization, secrets delivery | 5-10s |
| **Execution** | Task execution, variable by workload | User-defined |
| **Collection** | Artifact upload, cleanup | < 5s |
| **Finalization** | VM stop/destroy based on keepalive | < 10s |

---

## Database Schema

The database schema shows all tables and their relationships.

```mermaid
erDiagram
    sandboxes ||--o{ jobs : "executes in"
    sandboxes ||--o{ events : "logs"
    jobs ||--o{ messages : "scope (job)"
    workspaces ||--o{ messages : "scope (workspace)"
    sandboxes }o--|| workspaces : "uses"
    jobs ||--o{ events : "logs"
    jobs ||--o{ artifacts : "produces"
    jobs ||--o{ artifact_tokens : "authorizes"
    sandboxes ||--o{ bootstrap_tokens : "bootstraps"
    sandboxes ||--o{ artifact_tokens : "uploads"

    sandboxes {
        int vmid PK "Virtual Machine ID"
        string name "Sandbox name"
        string profile "Profile used"
        string state "Current state"
        string ip "Assigned IP"
        string workspace_id FK "Optional workspace"
        boolean keepalive "Lease renewal allowed"
        datetime lease_expires_at "TTL deadline"
        datetime created_at "Creation time"
        datetime updated_at "Last update"
        json meta_json "Additional metadata"
    }

    jobs {
        string id PK "Job UUID"
        string repo_url "Git repository"
        string ref "Branch/commit"
        string profile "Profile name"
        string task "Task description"
        string mode "Execution mode"
        int ttl_minutes "Sandbox TTL"
        boolean keepalive "Don't auto-destroy"
        string status "Job status"
        int sandbox_vmid FK "Assigned sandbox"
        datetime created_at "Creation time"
        datetime updated_at "Last update"
        json result_json "Job result"
    }

    workspaces {
        string id PK "Workspace UUID"
        string name UK "Unique name"
        string storage "Proxmox storage"
        string volid "Volume ID"
        int size_gb "Size in GB"
        int attached_vmid "Current VM"
        datetime created_at "Creation time"
        datetime updated_at "Last update"
        json meta_json "Additional metadata"
    }

    events {
        int id PK "Event ID"
        datetime ts "Timestamp"
        string kind "Event type"
        int sandbox_vmid FK "Related sandbox"
        string job_id FK "Related job"
        string msg "Event message"
        json json "Structured data"
    }

    messages {
        int id PK "Message ID"
        datetime ts "Timestamp"
        string scope_type "job|workspace|session"
        string scope_id "Scope identifier"
        string author "Author label"
        string kind "Message kind"
        string text "Message text"
        json json "Structured data"
    }

    artifacts {
        int id PK "Artifact ID"
        string job_id FK "Owner job"
        int vmid "Source sandbox"
        string name "Artifact name"
        string path "Storage path"
        int size_bytes "File size"
        string sha256 "SHA256 hash"
        string mime "MIME type"
        datetime created_at "Upload time"
    }

    bootstrap_tokens {
        string token PK "Token value"
        int vmid FK "Target sandbox"
        datetime expires_at "Expiration"
        datetime consumed_at "Consumption time"
        datetime created_at "Creation time"
    }

    artifact_tokens {
        string token PK "Token value"
        string job_id FK "Owner job"
        int vmid FK "Source sandbox"
        datetime expires_at "Expiration"
        datetime created_at "Creation time"
        datetime last_used_at "Last use"
    }

    profiles {
        string name PK "Profile name"
        int template_vmid "Template VM"
        string yaml "Profile YAML"
        datetime updated_at "Last update"
    }

    schema_migrations {
        int version PK "Migration version"
        string name "Migration name"
        datetime applied_at "Application time"
    }
```

### Table Indexes

| Table | Indexes | Purpose |
|-------|---------|---------|
| **sandboxes** | `idx_sandboxes_state`, `idx_sandboxes_profile` | Filter by state/profile |
| **jobs** | `idx_jobs_status`, `idx_jobs_sandbox` | Filter by status/VM |
| **workspaces** | `idx_workspaces_attached` | Find attached workspaces |
| **events** | `idx_events_sandbox`, `idx_events_job` | Query by entity |
| **messages** | `idx_messages_scope`, `idx_messages_ts` | Query by scope/retention |
| **bootstrap_tokens** | `idx_bootstrap_tokens_vmid` | Token lookup by VM |
| **artifact_tokens** | `idx_artifact_tokens_job`, `idx_artifact_tokens_vmid` | Token validation |
| **artifacts** | `idx_artifacts_job`, `idx_artifacts_vmid` | Artifact lookup |

---

## Messagebox

Messagebox provides an append-only coordination log scoped to a job, workspace, or session.
Scopes are polymorphic: `scope_type` and `scope_id` identify the target entity without enforcing a foreign key.

Common usage patterns:
- **Job scope**: capture agent handoffs and decisions for a single job (`scope_type=job`, `scope_id=<job_id>`).
- **Workspace scope**: keep durable notes tied to a workspace (`scope_type=workspace`, `scope_id=<workspace_id>`).
- **Session scope**: share ad-hoc context across multiple agents (`scope_type=session`, `scope_id=<session_id>`).

Retention notes:
- Messages are stored in SQLite and are not auto-pruned today.
- Operators should implement retention externally (periodic cleanup/backup + vacuum) if needed.

## Request Lifecycle

The request lifecycle shows how a CLI command flows through the system to Proxmox.

```mermaid
graph TB
    Start([User Command]) --> Parse{Command Type?}

    Parse -->|Sandbox| SandboxCmd[Sandbox Command]
    Parse -->|Job| JobCmd[Job Command]
    Parse -->|Workspace| WorkspaceCmd[Workspace Command]

    SandboxCmd --> BuildReq[Build HTTP Request]
    JobCmd --> BuildReq
    WorkspaceCmd --> BuildReq

    BuildReq --> UnixSock[/run/agentlab/agentlabd.sock]

    UnixSock --> Auth{Authenticated?}
    Auth -->|No| Unauthorized([401 Unauthorized])
    Auth -->|Yes| Route[Route to Handler]

    Route --> Validate{Validate Input}
    Validate -->|Invalid| BadRequest([400 Bad Request])
    Validate -->|Valid| CheckManager{Manager Ready?}

    CheckManager -->|No| ServiceUnavailable([503 Service Unavailable])
    CheckManager -->|Yes| Execute[Execute Operation]

    Execute --> DBOp[(Database Operation)]
    DBOp --> ProxmoxOp{Proxmox Needed?}

    ProxmoxOp -->|Yes| Backend[Backend Call]
    Backend --> APIBackend{Backend Type?}
    APIBackend -->|API| HTTPCall[HTTP Request to<br/>Proxmox API]
    APIBackend -->|Shell| ShellCall[Shell Command<br/>qm/pvesh]

    HTTPCall --> ProxmoxResp[Proxmox Response]
    ShellCall --> ProxmoxResp

    ProxmoxOp -->|No| SkipProxmox[Skip Proxmox Call]

    ProxmoxResp --> RecordEvent[Record Event]
    SkipProxmox --> RecordEvent
    RecordEvent --> UpdateMetrics[Update Metrics]

    UpdateMetrics --> BuildResp[Build Response]
    BuildResp --> Serialize[Serialize JSON]

    Serialize --> Success([200 OK<br/>Response Body])

    Success --> Display[CLI Displays Result]
    Display --> End([Command Complete])

    Unauthorized --> End
    BadRequest --> End
    ServiceUnavailable --> End

    classDef startend fill:#e8f5e9,stroke:#2e7d32,stroke-width:2px
    classDef error fill:#ffebee,stroke:#c62828,stroke-width:2px
    classDef process fill:#e3f2fd,stroke:#1565c0,stroke-width:2px
    classDef storage fill:#fff3e0,stroke:#ef6c00,stroke-width:2px
    classDef decision fill:#fff9c4,stroke:#f9a825,stroke-width:2px

    class Start,End startend
    class Unauthorized,BadRequest,ServiceUnavailable error
    class SandboxCmd,JobCmd,WorkspaceCmd,BuildReq,Route,Execute,RecordEvent,UpdateMetrics,BuildResp,Serialize,Display process
    class DBOp storage
    class Parse,Auth,Validate,CheckManager,ProxmoxOp,APIBackend decision
```

### Error Handling

| Error Type | HTTP Status | Recovery |
|------------|-------------|----------|
| **Invalid Input** | 400 Bad Request | Fix command syntax |
| **Unauthorized** | 401 Unauthorized | Check socket permissions |
| **Not Found** | 404 Not Found | Verify resource exists |
| **Conflict** | 409 Conflict | Resolve state conflict |
| **Backend Error** | 502 Bad Gateway | Check Proxmox connectivity |
| **Service Unavailable** | 503 Service Unavailable | Restart daemon |
| **Internal Error** | 500 Internal Server Error | Check daemon logs |

---

## Component Interaction

Detailed component interaction showing internal daemon communication flows.

```mermaid
graph LR
    subgraph "API Layer"
        ControlAPI[Control API]
        BootstrapAPI[Bootstrap API]
        ArtifactAPI[Artifact API]
    end

    subgraph "Manager Layer"
        SandboxMgr[Sandbox Manager]
        WorkspaceMgr[Workspace Manager]
        JobOrch[Job Orchestrator]
        ArtifactGC[Artifact GC]
    end

    subgraph "Backend Layer"
        APIBackend[API Backend]
        ShellBackend[Shell Backend]
    end

    subgraph "Data Layer"
        Store[(SQLite Store)]
        Secrets[Secrets Store]
        Metrics[Metrics Registry]
    end

    subgraph "External Services"
        ProxmoxAPI[Proxmox API]
        ProxmoxCLI[Proxmox CLI]
    end

    ControlAPI --> SandboxMgr
    ControlAPI --> WorkspaceMgr
    ControlAPI --> JobOrch
    ControlAPI --> Store

    BootstrapAPI --> Store
    BootstrapAPI --> Secrets

    ArtifactAPI --> Store
    ArtifactGC --> Store

    SandboxMgr --> Store
    SandboxMgr --> APIBackend
    SandboxMgr --> ShellBackend
    SandboxMgr --> Metrics
    SandboxMgr --> WorkspaceMgr

    WorkspaceMgr --> Store
    WorkspaceMgr --> APIBackend
    WorkspaceMgr --> ShellBackend

    JobOrch --> Store
    JobOrch --> APIBackend
    JobOrch --> ShellBackend
    JobOrch --> SandboxMgr
    JobOrch --> WorkspaceMgr
    JobOrch --> Secrets
    JobOrch --> Metrics

    APIBackend --> ProxmoxAPI
    ShellBackend --> ProxmoxCLI

    ArtifactGC --> Metrics

    classDef api fill:#e1f5fe,stroke:#0277bd,stroke-width:2px
    classDef manager fill:#f3e5f5,stroke:#7b1fa2,stroke-width:2px
    classDef backend fill:#fff9c4,stroke:#f57f17,stroke-width:2px
    classDef data fill:#ffe0b2,stroke:#e65100,stroke-width:2px
    classDef external fill:#c8e6c9,stroke:#2e7d32,stroke-width:2px

    class ControlAPI,BootstrapAPI,ArtifactAPI api
    class SandboxMgr,WorkspaceMgr,JobOrch,ArtifactGC manager
    class APIBackend,ShellBackend backend
    class Store,Secrets,Metrics data
    class ProxmoxAPI,ProxmoxCLI external
```

### Communication Patterns

| Pattern | Components | Description |
|---------|-----------|-------------|
| **API → Manager** | Direct method calls | Synchronous, in-process |
| **Manager → Backend** | Interface abstraction | Pluggable implementations |
| **Manager → Store** | Database transactions | ACID-compliant |
| **Manager → Metrics** | Prometheus counters | Non-blocking updates |
| **Backend → Proxmox** | HTTP/CLI | Network/Process execution |
| **Guest → Daemon** | HTTP callbacks | Token-authenticated |

---

## Key Design Principles

1. **Security First**
   - Unix socket for local-only control
   - Token-based guest authentication
   - Network isolation with controlled egress
   - Temporary secrets delivery (tmpfs only)

2. **State Management**
   - Single source of truth (SQLite)
   - State machine enforcement
   - Periodic reconciliation
   - Event logging for audit trail

3. **Backend Abstraction**
   - Pluggable Proxmox backends
   - API preferred (reliable)
   - Shell fallback (compatible)
   - Easy testing with mocks

4. **Observability**
   - Prometheus metrics
   - Structured events
   - Detailed logging
   - Health check endpoints

5. **Operational Safety**
   - Lease-based TTL enforcement
   - Automatic resource cleanup
   - Graceful shutdown
   - State recovery after restart

---

## Usage in Documentation

To include these diagrams in other documentation:

```markdown
<!-- Include system architecture -->
[AgentLab System Architecture](docs/architecture.md#system-architecture)

<!-- Embed specific diagram -->
```mermaid
paste-diagram-here
```
```

---

**Last Updated**: 2026-02-06
**Maintained By**: AgentLab Team
**License**: MIT
