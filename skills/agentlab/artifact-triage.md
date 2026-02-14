# Artifact Triage

## /job-artifacts
Find the most recent bundle for incident analysis.

```bash
agentlab job artifacts <job_id>
agentlab job artifacts download <job_id> --latest --out /tmp/artifacts
```

## /job-doctor
Create a diagnostic bundle for job failures.

```bash
agentlab job doctor <job_id>
```

## /sandbox-doctor
Collect sandbox lifecycle evidence.

```bash
agentlab sandbox doctor <vmid>
```

## /session-doctor (when enabled)
Capture session and workspace recovery context:

```bash
agentlab session doctor <id>
```

Bundle names include artifact hashes and lifecycle metadata; retain latest for post-mortems.
