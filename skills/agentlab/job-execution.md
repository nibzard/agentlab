# Job Execution

## /job-run
Run an unattended job in a sandbox.

Required inputs:
- `repo`
- `task`
- `profile`

Optional inputs:
- `ref`, `mode`, `ttl`, `keepalive`, `socket`, `json`

Command template:
```bash
agentlab job run --repo "<repo>" --task "<task>" --profile "<profile>" [--ref "<ref>"] [--mode <mode>] [--ttl <ttl>] [--keepalive] [--socket <path>] [--json]
```

Notes:
- Default mode is `dangerous`; require explicit confirmation before using `--mode dangerous` in any manual workflow.
- `--keepalive` leaves the sandbox running after completion.

## /job-validate
Validate a run plan without creating infrastructure.

```bash
agentlab job validate --repo "<repo>" --task "<task>" --profile "<profile>" [--json]
```

## /job-show
Inspect plan/result and events.

```bash
agentlab job show <job_id>
```

Use:
- `--events-tail` for event history
- `--json` for machine-readability

## /job-artifacts
List artifacts and download the latest bundle by default.

```bash
agentlab job artifacts <job_id>
agentlab job artifacts download <job-id> --latest
agentlab job artifacts download <job-id> --bundle
```
