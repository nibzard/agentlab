---
name: agentlab
description: Set up AgentLab connections, run authorized sandbox tasks, and collect results through the AgentLab CLI. Use for operating AgentLab, not for developing its code.
metadata:
  version: "2.0.0"
  agentlab: "0.4.x; discover optional features through capabilities"
allowed-tools:
  - Bash
---

# Operate AgentLab

Use the installed `agentlab` CLI. Keep provisioning, authorization, retries, and
cleanup in AgentLab. The bundled guides describe supported commands; installed
`--help`, `schema`, and `capabilities` establish what the selected server supports.

## Discover the connection

Run `agentlab context list --json` to inspect saved contexts. Pass an explicit
`--context NAME` for server operations when more than one server is available.
Do not change the global active context for concurrent tasks.

Read `agentlab --context NAME capabilities --json`. Record `server_identity`,
versions, feature flags, allowed commands, sandbox scope, and credential expiry.
Check `status`, `profile list`, and the chosen profile before provisioning.
An absent or false feature is unsupported. Do not infer support from the version.
An authentication, scope, or identity failure requires a corrective action.
Do not bypass it through the trusted local socket or another transport.

## Choose the workflow

Read only the guide needed for the user's task:

- [Discovery and setup](discovery-and-setup.md): connect this computer, inspect a
  new host, resume setup, and verify readiness.
- [Job execution](job-execution.md): transfer local source or use a repository
  URL, validate, submit once, observe, and collect a reviewable result.
- [Standalone work](standalone-work.md): create one interactive sandbox instead
  of a job and account for its lease.
- [Workspace recovery](workspace-recovery.md): inspect existing state and choose
  an authorized recovery action without discarding user changes.
- [Networking](networking.md): diagnose access with the selected connection.
- [Artifact triage](artifact-triage.md): inspect result evidence and diagnostics.
- [Troubleshooting](troubleshooting.md): distinguish missing access, unsupported
  features, interrupted work, cancellation, credential retirement, and cleanup.

A job creates its own sandbox. Do not create an additional sandbox for that job.
Use the user's existing authority. Request only missing access, information, or
permission for an action outside that authority.

## Preserve the task contract

Use `--json` in automation. Save operation IDs, job IDs, source IDs, request keys,
and the server identity before waiting or retrying. Keep one request key for one
logical submission. A lost response does not authorize a second task.

Keep credentials out of prompts, command arguments, logs, and artifacts. Use the
terminal credential prompt or `connect --token-stdin` with a protected input.
A blocked non-interactive command needs the stated requirement; do not poll it
until access or configuration changes.

Collect returned changes for review. Do not apply patches or push without the
user's authority. A completed agent process does not prove tests passed.
Report the source, changes, execution evidence, test verdict, artifact paths,
and every removed or retained resource with its lease or corrective action.
