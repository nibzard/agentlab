# Run your first job

Goal: clone a repository into a fresh sandbox, run an agent task end to end, and download the artifacts the job produced.

## Prerequisites

- A working sandbox setup, as described in [Create your first sandbox](create-first-sandbox.md).
- A git repository the daemon can clone. Provide credentials through a secrets bundle if the repository is private.
- A profile that allows the task mode you want. The bundled `yolo-ephemeral` profile uses behavior `mode: dangerous` and an artifact upload endpoint of `http://10.77.0.1:8846/upload`.

## Steps

1. Validate the job plan before you create any resources. `job validate` runs the same preflight as `job run` but provisions nothing.

    ```bash
    agentlab job validate --repo https://github.com/user/repo --task "run tests" --profile yolo-ephemeral
    ```

2. Run the job. The daemon creates a sandbox, injects a one-time bootstrap token, and starts the in-guest runner. The runner fetches the bootstrap payload, clones the repository, runs the task, and uploads artifacts.

    ```bash
    agentlab job run --repo https://github.com/user/repo --task "run tests" --profile yolo-ephemeral --ttl 30m
    ```

   Note the job id that the command prints. The job status moves through `QUEUED`, `RUNNING`, and then `COMPLETED`, `FAILED`, or `TIMEOUT`.

3. Follow the job status and its event stream.

    ```bash
    agentlab job show <job_id> --events-tail 20
    ```

4. Stream the sandbox logs while the agent runs.

    ```bash
    agentlab logs <vmid> --follow
    ```

5. When the job reaches `COMPLETED`, download the artifacts. The runner tars its logs and `report.json` into `agentlab-artifacts.tar.gz` and uploads them to the artifact listener.

    ```bash
    agentlab job artifacts download <job_id>
    ```

   Pass `--out` to choose a destination, `--bundle` to fetch a named bundle, or `--latest` for the most recent one.

## Expected result

`agentlab job show <job_id>` reports status `COMPLETED` with the event history. For exit diagnostics, run `agentlab job doctor <job_id>`. The artifact download writes `agentlab-artifacts.tar.gz` to your working directory. Unless you pass `--keepalive`, the daemon destroys the sandbox after the job finishes, so the VM is gone from `agentlab sandbox list`.

!!! tip
    Add `--keepalive` to `job run` to leave the sandbox running with a renewable lease so you can inspect its filesystem after the task completes. You can then SSH in with `agentlab ssh <vmid>`.

## Next

To turn this into a resumable workflow that survives sandbox restarts, see [Run a stateful dev session](stateful-dev-session.md). For artifact and job lifecycle details, see [State machine reference](../reference/state-machine.md). For routine artifact collection, see [Collect and download job artifacts](../how-to/collect-job-artifacts.md).
