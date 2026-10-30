# Run and observe a job

## Discover and validate

Read capabilities and the selected profile first. Use `job validate` with the
same repository, revision, task, profile, and mode intended for submission.
A job provisions its own sandbox. Do not call `sandbox new` for this path.

For a repository URL, pin an explicit revision. Configure private repository
access through an existing credential reference. Do not embed credentials in URLs.

```bash
agentlab --context "$context" job validate --repo "$repo_url" --ref "$revision" --task "$task" --profile "$profile" --json
```

Choose a supported mode within the user's authority. Do not add unrelated
configuration changes or repeat an approval already provided for the task.

## Transfer a local repository

Check `repository_upload` and `repository_snapshot_jobs`. Local paths do not
become remote repository URLs. Capture the effective local worktree:

```bash
agentlab repo snapshot --repo "$repo" --json
```

Save `manifest_path` and the returned manifest. Snapshot output stays outside the
source worktree. Review `excluded` and `changes`. Exclusions can omit files needed
by the task; resolve that limitation without transferring secrets or ignored data.
The snapshot preserves local files, the index, commits, and the current branch.

Upload its manifest with the selected context:

```bash
agentlab --context "$context" repo upload --manifest "$manifest_path" --json
```

Use the returned `repo_url` and `manifest.revision` as the job repository and ref.
Record the source ID, original revision, transferred changes, and expiry.
Snapshot jobs currently require stateless VM jobs and updated templates.
Workspace and session requests are unsupported for this source path.

## Submit once

Check `idempotent_job_submission` before passing a request key. Persist one key
for the logical task. Validate, then submit with the same inputs:

```bash
agentlab --context "$context" job run --repo "$repo_url" --ref "$revision" --task "$task" --profile "$profile" --request-id "$request_id" --json
```

Save the returned job ID. If the response is lost, retry the same key and inputs.
A conflict or expired key needs inspection of the original job. Do not generate
a new key merely because the first request timed out.
Check `idempotent_workspace_create` before creating a workspace in the keyed
request. A blocked `workspace_allocation_inspection_required` response includes
`workspace_allocation` with the reserved ID and any known volume. Preserve it.
A saved complete receipt can resume publication. An unknown backend outcome
requires inspection; a new request key can allocate duplicate storage.
Keyed branch sessions remain unsupported. Do not create them as an untracked
workaround.

## Observe and collect

```bash
agentlab --context "$context" job show "$job_id" --events-tail 200 --json
agentlab --context "$context" job result "$job_id" --json
```

`job show` reads durable server state after the client restarts. Observe until
the job is terminal. Check `repository_results` before collection.
`job result` returns a verified patch, bundle, execution archive, and receipt in a
private directory. It does not apply changes or push them.
A failed task can still return verified changes. An empty patch is a valid outcome.

Read `repository_result.state`, `execution`, `cleanup_status`, and `resources`.
`execution.state=guest_reported` records the agent's exit code and duration.
`execution.tests=not_reported` does not prove tests passed. Do not turn a successful
agent exit code or prose claim into a verified test verdict.

Cleanup can still be pending when the job status is terminal. Continue observation
or report the retained resource, lease, and action explicitly.
See [troubleshooting](troubleshooting.md) for cancellation and cleanup retries.

## Request explicit test evidence

Check `job_test_commands` before using repeatable `--test-command` flags.
Choose commands from the repository's documented test procedure. Include the
same ordered commands in validation, submission, and retries. Commands run with
`bash -c` in the task repository after the agent succeeds. They share a five-minute
budget, bounded by the remaining job lease. A failed agent skips these commands.
A failed test makes the job fail without changing the recorded agent exit code.

```bash
agentlab --context "$context" job validate --repo "$repo_url" --ref "$revision" --task "$task" --profile "$profile" --test-command 'go test ./...' --json
agentlab --context "$context" job run --repo "$repo_url" --ref "$revision" --task "$task" --profile "$profile" --test-command 'go test ./...' --request-id "$request_id" --json
```

For uploaded repository jobs, inspect `execution.test_runs` in the verified
result receipt. Each executed command records its exit code, duration, log path,
and log digest. The archive verifier checks those logs against the admitted
commands. These are guest-reported command results. Review the commands and logs
before claiming that the task assertions pass. A command that exits successfully
does not establish test coverage. `skipped` and `not_reported` do not mean passed.
URL-based jobs expose command reports; they do not receive verified repository
receipts. Old templates that omit requested evidence cannot report success for
the requested plan. Rebuild the template before using this feature.
