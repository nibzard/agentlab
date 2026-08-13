# How to collect doctor diagnostics

Generate a redacted diagnostic bundle for a sandbox, session, or job to attach
to a support request or a code review. Doctor bundles are read-only and safe to
run at any time.

## Prerequisites

- The `agentlab` CLI can reach the daemon.
- A sandbox VMID, session name, or job ID to diagnose.

## Steps

1. Pick the scope that matches the problem. Each scope maps to one command and
   one `POST /v1/.../doctor` route:

   | Scope    | Command                                | Route                                 |
   |----------|----------------------------------------|---------------------------------------|
   | Sandbox  | `agentlab sandbox doctor <vmid>`       | `POST /v1/sandboxes/{vmid}/doctor`    |
   | Session  | `agentlab session doctor <session>`    | `POST /v1/sessions/{id}/doctor`       |
   | Job      | `agentlab job doctor <job_id>`         | `POST /v1/jobs/{id}/doctor`           |

2. Write the bundle to a directory with `--out`:

    ```bash
    agentlab sandbox doctor 1001 --out /tmp/
    agentlab session doctor dev-session --out /tmp/
    agentlab job doctor 42f3... --out /tmp/
    ```

3. Inspect or share the generated file. A sandbox bundle typically includes the
   database record, recent events, Proxmox status and config, and the artifact
   inventory.

## Verify

- The bundle exists at the `--out` path you set.
- `agentlab sandbox show <vmid>` (or the matching `session show` / `job show`)
  confirms the resource ID was correct.

!!! note "Doctor bundles are read-only"
    Doctor commands do not mutate state, so you can run them against a live
    sandbox or a stuck one without risk. Bundle contents are redacted before
    they leave the daemon. For broader troubleshooting, see
    ../how-to/debug-a-stuck-sandbox.md.
