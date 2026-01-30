---
name: agentlab
description: Manage AgentLab jobs and sandboxes through the agentlab CLI. Use when the user asks to run jobs, create/list/show/destroy sandboxes, renew leases, follow logs, or rebind workspaces.
allowed-tools:
  - Bash
---

# AgentLab CLI Skill

Use the `agentlab` CLI to manage AgentLab sandboxes and jobs. Never call `qm`, `pvesh`, or edit Proxmox directly.

## Guardrails

- Only run `agentlab` commands. Refuse to call `qm`, `pvesh`, or any host-level VM tooling.
- Require explicit confirmation for dangerous job execution. If the user does not explicitly request dangerous/yolo mode, ask before running `/job-run` with `--mode dangerous`.
- Confirm destructive actions. For `/sandbox-destroy`, ask for confirmation with the VMID before executing.
- Validate inputs before running:
  - `vmid` must be a positive integer.
  - `repo` must be a valid git URL (https or ssh).
  - `profile` and `task` must be non-empty.
  - `ttl` accepts minutes or duration (e.g., `120` or `2h`).
- Use the default socket (`/run/agentlab/agentlabd.sock`) unless the user provides `--socket`.
- Use `--json` only when the user requests machine-readable output.
- If a requested command is not supported by the installed `agentlab` CLI, explain that and stop.

## Commands

### /job-run
Run an unattended job in a sandbox.

Required inputs:
- `repo`, `task`, `profile`

Optional inputs:
- `ref`, `mode`, `ttl`, `keepalive`, `socket`, `json`

Command template:
```bash
agentlab job run --repo "<repo>" --task "<task>" --profile "<profile>" [--ref "<ref>"] [--mode <mode>] [--ttl <ttl>] [--keepalive] [--socket <path>] [--json]
```

Notes:
- Default mode is dangerous; require explicit user confirmation before using `--mode dangerous`.
- `--keepalive` leaves the sandbox running after job completion; confirm intent.

---

### /sandbox-new
Create a sandbox without running a job.

Required inputs:
- `profile`

Optional inputs:
- `name`, `ttl`, `keepalive`, `workspace`, `vmid`, `job`, `socket`, `json`

Command template:
```bash
agentlab sandbox new --profile "<profile>" [--name "<name>"] [--ttl <ttl>] [--keepalive] [--workspace "<id>"] [--vmid <vmid>] [--job "<id>"] [--socket <path>] [--json]
```

Notes:
- `--vmid` is an override; only use if the user explicitly requests it.
- If `--keepalive` is set, confirm the user wants a long-running sandbox.

---

### /sandbox-list
List sandboxes.

Command template:
```bash
agentlab sandbox list [--socket <path>] [--json]
```

---

### /sandbox-show
Show details for a sandbox.

Required inputs:
- `vmid`

Command template:
```bash
agentlab sandbox show <vmid> [--socket <path>] [--json]
```

---

### /sandbox-destroy
Destroy a sandbox.

Required inputs:
- `vmid`

Command template:
```bash
agentlab sandbox destroy <vmid> [--socket <path>] [--json]
```

Notes:
- Always ask for explicit confirmation before running.

---

### /lease-renew
Renew a sandbox lease.

Required inputs:
- `vmid`, `ttl`

Command template:
```bash
agentlab sandbox lease renew <vmid> --ttl <ttl> [--socket <path>] [--json]
```

---

### /logs-follow
Fetch logs (events) for a sandbox, optionally follow.

Required inputs:
- `vmid`

Optional inputs:
- `follow`, `tail`, `socket`, `json`

Command template:
```bash
agentlab logs <vmid> [--follow] [--tail <n>] [--socket <path>] [--json]
```

Notes:
- Default tail is 50; cap tail at 1000 if a larger value is requested.

---

### /workspace-rebind
Rebind a workspace to a new sandbox.

Required inputs:
- `name`, `profile`

Command template:
```bash
agentlab workspace rebind "<name>" --profile "<profile>" [--socket <path>] [--json]
```

Notes:
- This command requires workspace support in the `agentlab` CLI. If unavailable, explain that workspace commands are not yet installed.
