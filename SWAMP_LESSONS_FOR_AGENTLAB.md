# What AgentLab Can Learn from `systeminit/swamp`

Reviewed project: `systeminit/swamp` at commit `506a480` (2026-02-14).

## High-Value Lessons

1. Treat agent guidance as product code, not just docs.
- Swamp ships a full skill pack and installs/upgrades it with the CLI.
- AgentLab currently has one skill file (`skills/agentlab/SKILL.md`).
- Improvement: ship a versioned multi-skill pack (provisioning, workspace recovery, networking, artifact triage, incident response) and update it automatically via CLI.

2. Move from scattered event recording to typed domain events + projections.
- Swamp uses a central event bus and subscribers to keep read models synchronized.
- AgentLab has useful events, but emission is distributed across handlers and managers.
- Improvement: introduce typed domain events and projection handlers to drive status views, diagnostics, and eventual dashboards from one consistent event stream.

3. Add an agent-friendly logical filesystem view.
- Swamp keeps internal storage plus stable symlinked logical views for exploration.
- Improvement: add `/var/lib/agentlab/views/{sandboxes,jobs,workspaces,artifacts}` with stable JSON/symlink pointers and support `index rebuild/verify`.

4. Add schema-first and preflight execution contracts.
- Swamp strongly supports schema discovery and validate/evaluate-before-run flows.
- AgentLab has solid API docs but fewer machine-readable contracts for agent tooling.
- Improvement: add `agentlab schema` and `/v1/schema`, plus `job validate/plan` preflight mode.

5. Improve artifact model with immutable version history.
- Swamp models output data as immutable, versioned artifacts with ownership checks.
- AgentLab has retention GC, but less first-class versioned artifact lineage.
- Improvement: persist artifact versions and lineage metadata for reproducibility, diffing, and rollback/debug workflows.

6. Adopt issue-driven AI planning workflow in CI.
- Swamp supports `/plan` and `/plan-update` issue workflows with automated plan comments.
- AgentLab CI already has strong quality checks.
- Improvement: add issue planning automation for larger/complex changes to improve consistency and traceability.

7. Copy patterns, not platform assumptions.
- Swamp is repo-local automation; AgentLab is a privileged host control plane.
- Improvement: adopt Swamp’s architectural patterns (skills, event projections, schema contracts) while keeping AgentLab’s security and orchestration boundaries intact.

## Suggested Priority for AgentLab

1. Multi-skill bundle + automatic install/upgrade.
2. Typed event layer + projections.
3. Schema and preflight APIs.
4. Logical views and index maintenance commands.
5. Versioned artifact lineage.
6. Issue planner workflow in GitHub Actions.

## Claude/Codex Compatibility Checklist

To keep this direction compatible with both Claude Code and OpenAI Codex style agents:

1. Keep skills and instructions agent-neutral.
- Avoid provider-specific assumptions in core skill files.
- Keep command examples valid from plain shell contexts.

2. Ensure deterministic machine output.
- Every automation-critical command should support stable `--json` output.
- Keep field names/versioning explicit for long-lived agent integrations.

3. Support non-interactive operation.
- Provide non-interactive flags and defaults for CI and unattended agent runs.
- Make destructive operations explicit and confirmable through API/CLI contract.

4. Stabilize runtime contract.
- Document canonical env vars and flags (`endpoint`, `token`, `socket`, `profile`, TTL).
- Keep redaction behavior predictable for agent debugging.

5. Make failures diagnosable by agents.
- Return structured error codes/messages, not only human prose.
- Keep event streams queryable with clear kinds/stages for retry logic.

## Sources

- Swamp README: <https://github.com/systeminit/swamp/blob/506a4807fe7a671cafb88674559b882d905e4363/README.md>
- Swamp high-level design: <https://github.com/systeminit/swamp/blob/506a4807fe7a671cafb88674559b882d905e4363/design/high-level.md>
- Swamp event bus: <https://github.com/systeminit/swamp/blob/506a4807fe7a671cafb88674559b882d905e4363/src/domain/events/event_bus.ts>
- Swamp repository factory + event wiring: <https://github.com/systeminit/swamp/blob/506a4807fe7a671cafb88674559b882d905e4363/src/infrastructure/persistence/repository_factory.ts>
- Swamp bundled skills: <https://github.com/systeminit/swamp/blob/506a4807fe7a671cafb88674559b882d905e4363/src/infrastructure/assets/skill_assets.ts>
- Swamp expression evaluation: <https://github.com/systeminit/swamp/blob/506a4807fe7a671cafb88674559b882d905e4363/src/domain/expressions/expression_evaluation_service.ts>
- Swamp unified data repository: <https://github.com/systeminit/swamp/blob/506a4807fe7a671cafb88674559b882d905e4363/src/infrastructure/persistence/unified_data_repository.ts>
- Swamp issue planner workflow: <https://github.com/systeminit/swamp/blob/506a4807fe7a671cafb88674559b882d905e4363/.github/workflows/issue-planner.yml>
- AgentLab skill file: `skills/agentlab/SKILL.md`
- AgentLab events: `internal/db/events.go`
- AgentLab provisioning flow: `internal/daemon/job_orchestrator.go`
- AgentLab artifact GC: `internal/daemon/artifact_gc.go`
- AgentLab CI: `.github/workflows/ci.yml`
- AgentLab API docs: `docs/api.md`
