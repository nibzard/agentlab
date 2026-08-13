# AgentLab documentation

AgentLab is an open-source, Go-based platform that provisions unattended,
network-isolated Proxmox VE virtual machine sandboxes for running AI coding
agents in "dangerous" or YOLO mode. It ships as four binaries: the
`agentlabd` control-plane daemon that owns all Proxmox and policy access, the
`agentlab` CLI that is the single supported user and automation surface, an
optional `agentlab-dashboard` web UI, and an `agentlab-ssh-gateway` that is
built behind a build tag and excluded from releases. The daemon drives a
sandbox state machine through provisioning, execution, and teardown over a
local Unix-socket control API plus guest-only bootstrap and artifact
listeners, with an optional remote TCP control plane for tailnet access. Host
secrets are stored in age- or sops-encrypted bundles and delivered into guest
tmpfs through one-time tokens, while sandboxes get full outbound Internet with
RFC1918 and IPv6 ULA egress blocked by default. Persistent state is carried by
workspace volumes that you can detach and reattach across ephemeral sandbox
VMs. AgentLab is built for platform operators who self-host a Proxmox cluster
and for automation engineers who drive batches of agent jobs through the CLI,
the HTTP API, or the Claude Code Skill bundle.

## Get started

New here? Follow the tutorial path in order. It assumes no prior knowledge and
ends with a running job inside a sandbox.

1. [Install AgentLab](tutorials/install-agentlab.md)
1. [Set up the Proxmox host](tutorials/set-up-proxmox-host.md)
1. [Create your first sandbox](tutorials/create-first-sandbox.md)
1. [Run your first job](tutorials/run-first-job.md)

Once the daemon is running, confirm it answers on the local socket:

```bash
agentlab status
```

## For coding agents

If you are a coding agent driving AgentLab to manage sandboxes for yourself,
your user, or other agents, start with the agent track:

- [JSON output for agents](reference/agent-json-output.md) — the `--json`
  contract every command emits, and how to parse it.
- [Drive AgentLab as a coding agent](how-to/drive-agentlab-as-a-coding-agent.md)
  — the full loop: discover, provision, run, collect, tear down.
- [Coordinate multiple agents](tutorials/coordinate-multiple-agents.md) — share
  state and hand off work between agents and humans.
- [Mint scoped tokens for an agent](how-to/mint-scoped-tokens-for-an-agent.md)
  — delegate least-privilege access to another agent.
- [Isolation and credentials for agents](explanation/how-agentlab-isolates-and-credentials-an-agent.md)
  — the boundaries and credential model from an agent's point of view.

## Browse by quadrant

The docs follow a Diátaxis structure. A page lives in exactly one quadrant, so
you can tell by its location whether you are learning, doing, looking something
up, or understanding.

| Quadrant | Use it when | Start here |
| --- | --- | --- |
| Tutorials | You are learning AgentLab and want a guided path. | [Install AgentLab](tutorials/install-agentlab.md) |
| How-to guides | You have a goal and want a focused recipe. | [Renew a sandbox lease](how-to/renew-a-lease.md) |
| Reference | You need to look up an exact command, flag, route, or default. | [CLI reference](reference/cli.md) |
| Explanation | You want to understand the design and trade-offs. | [Architecture](explanation/architecture.md) |
| Meta | You need shared terms, style rules, or contribution steps. | [Glossary](meta/glossary.md) |

!!! tip "Not sure where to look?"
    Operators running a Proxmox host usually start in
    [Tutorials](tutorials/install-agentlab.md). Automation engineers driving
    batches of jobs skip to [How-to guides](how-to/renew-a-lease.md) and
    [Reference](reference/cli.md). Contributors head to
    [Contributing](meta/contributing.md) and the
    [Documentation style guide](meta/style-guide.md).
