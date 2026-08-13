# How to collect and download job artifacts

Pull the artifact bundle a job produced after it finishes.

## Prerequisites

- A job that has reached `COMPLETED`, `FAILED`, or `TIMEOUT`. The guest runner
  tars its logs and `report.json` into `agentlab-artifacts.tar.gz` and uploads
  the bundle through the artifact endpoint.
- The `agentlab` CLI can reach the daemon.

## Steps

1. List the artifacts recorded for the job:

    ```bash
    agentlab job artifacts <job_id>
    ```

2. Download the default bundle. With no selector, the CLI downloads the latest
   `agentlab-artifacts.tar.gz`:

    ```bash
    agentlab job artifacts download <job_id>
    ```

3. Choose what to download with one selector. `--path` and `--name` are mutually
   exclusive:

    ```bash
    agentlab job artifacts download <job_id> --bundle --out ./out/
    agentlab job artifacts download <job_id> --name agentlab-artifacts.tar.gz
    agentlab job artifacts download <job_id> --latest --out latest.bin
    ```

## Verify

- The file exists at the `--out` path you set, or in the current directory.
- `agentlab job artifacts <job_id>` lists artifact pointers for the job.

!!! warning "Artifacts are stored in plaintext"
    Artifacts are kept under `/var/lib/agentlab/artifacts` with no at-rest
    encryption. Use host full-disk encryption, or encrypt files inside the VM
    before upload, if the contents are sensitive.

!!! note "Retention is not yet documented"
    An artifact garbage collector runs in the daemon, but its retention schedule
    and limits are not yet documented. Manage disk use on the artifact directory
    manually until guidance exists.

The daemon endpoints behind these commands are
`GET /v1/jobs/{id}/artifacts` and `GET /v1/jobs/{id}/artifacts/download`. See
the [HTTP API reference](../reference/http-api.md). For the upload path from the
guest side, see [Guest runner flow](../explanation/guest-runner-flow.md).
