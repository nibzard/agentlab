---
name: agentlab
version: 1.0.0
agentlab: "1+"
description: Task-focused command bundles for AgentLab operations in Claude workflows.
allowed-tools:
  - Bash
---

# AgentLab Claude Skill Bundle

Use the following focused guides for AgentLab workflows.

- `job-execution.md` - run/validate jobs and inspect artifacts.
- `workspace-recovery.md` - workspace and sandbox recovery actions.
- `networking.md` - reachability, SSH, and routing checks.
- `artifact-triage.md` - artifact and diagnostic bundle workflows.
- `troubleshooting.md` - recovery and cleanup diagnostics.

The bundle manifest is `bundle/manifest.json`.

The top-level bundle version is `1.0.0`; install/upgrade scripts apply
compatibility checks against the host manifest and skip work when already current.

## Compatibility

- `manifest.json` declares `supported_agent_versions`.
- Update this manifest when skill behavior changes across agentlab releases.
- `scripts/install_host.sh` uses `manifest.json` to decide upgrade vs. no-op and
  writes installed bundle metadata to `/etc/agentlab/config.yaml`.

## Rollback

- To revert a skill upgrade, checkout a prior revision of
  `skills/agentlab/bundle/manifest.json` and rerun:

```bash
scripts/install_host.sh --install-skills-only
```

- For forced downgrade/restore, remove the installed bundle directory first:

```bash
rm -rf ~/.claude/skills/agentlab
scripts/install_host.sh --install-skills-only
```
