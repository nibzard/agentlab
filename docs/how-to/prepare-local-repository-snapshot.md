# Prepare a local repository snapshot

Use `agentlab repo snapshot` to prepare a local Git worktree for transfer.
This command creates local artifacts. It does not upload them or submit a job.
Upload the artifacts separately to run a stateless VM job. Collect its patch with `job result`.

## Create a snapshot

1. Select a Git worktree with an existing commit. Finish concurrent edits first.
2. Create the snapshot:

   ```bash
   agentlab repo snapshot --repo . --json
   ```

3. Read `manifest_path` from the result. Inspect that file for included files,
   local changes, and additional exclusions.

The command works offline. It does not require a working server context.
By default, artifacts use the operating system cache directory under
`agentlab/repositories`. Use `--out DIRECTORY` to choose another directory.
The output directory must be outside the source worktree.

The result includes the original source revision, snapshot revision, bundle
checksum, file count, change count, and artifact paths. Full file lists remain
in the protected manifest. The bundle, manifest, and patch have mode `0600`.

## Upload and run a job

Use a VM template that contains the updated guest runner. Existing templates must
be rebuilt before they can run snapshot jobs. Check `agentlab capabilities` for
`repository_upload` and `repository_snapshot_jobs` before using this workflow.

1. Upload the manifest and its matching `source.bundle`:

   ```bash
   agentlab repo upload --manifest "$manifest_path" --json
   ```

2. Save the returned `id`, `repo_url`, manifest `revision`, and `expires_at`.
   Select a valid profile from `agentlab profile list --json`.
3. Validate and submit the task with those values:

   ```bash
   agentlab job validate --repo "$repo_url" --ref "$revision" --profile "$profile" --task "Run the tests" --json
   agentlab job run --request-id repository-task-1 --repo "$repo_url" --ref "$revision" --profile "$profile" --task "Run the tests" --json
   ```

4. Observe the job. Collect its verified results after it reaches a terminal state:

   ```bash
   agentlab job result "$job_id" --out ./results --json
   ```

5. Read the returned resource list. Remove the source upload after its jobs finish:

   ```bash
   agentlab repo show "$repository_id" --json
   agentlab repo remove "$repository_id" --json
   ```

Use one request key per task. Save the key before submission. Reuse that key and
the same inputs after a lost response. The job creates its own sandbox.
Snapshot jobs cannot use a workspace, a branch session, or an LXC container.
The guest uses a new private repository. It preserves an existing guest worktree.

Uploads belong to the acting identity and server. A different identity cannot
inspect, submit, or remove them. The remote API requires `repository.create`,
`repository.read`, and `repository.delete` for their respective actions.
Job submission also requires `job.create` and `repository.read`.
These repository permissions cannot use a sandbox scope because uploads have
no sandbox binding. Use a task credential with an appropriate expiry.

Upload retries return the same source record and its original expiry. The server
verifies the bundle hash, manifest, object limits, sanitized history, and known
credential policy. It bounds decompression before importing Git objects.
No uploaded code runs on the daemon host. The guest must authenticate its
sandbox identity and active job before downloading the pinned source bundle.

New submissions stop after the upload expires in 30 days. Expiry does not delete
the source bytes or interrupt an admitted job. Remove the upload explicitly.
Removal refuses queued and running jobs. It verifies file cleanup before removing
the metadata. An interrupted upload can remove its abandoned temporary files
when you retry the same snapshot. Automatic removal of abandoned uploads is
not implemented.

## Collect task changes

Check `repository_results` before using `job result`. Updated VM templates contain
a local exporter. It captures task commits and working changes against the initial
snapshot. It excludes known credentials and ignored files from both patch trees.
An excluded file does not become a deletion in the returned patch. The manifest
records additional exclusions. Inspect them when reviewing the result.

The guest uploads the manifest, sanitized bundle, patch, and execution archive.
Result artifact paths are immutable. Repeated uploads accept the same bytes.
The server verifies their stored hashes and the bundle contents. It compares the
base file hashes and modes with the original uploaded source. Explicit exclusions
can omit files from both trees. They cannot replace the original base content.
The server regenerates the patch hash from the sanitized bundle.
It validates the execution archive without extraction or code execution.
Archive validation bounds compressed bytes, expanded bytes, file size, and entries.
It rejects links, special files, executable files, duplicate paths, and unsafe paths.
The archived report must identify the job, source, result manifest, and terminal status.
Its exit code and duration must match the terminal report.

The receipt records execution as `guest_reported` and tests as `not_reported`.
An agent exit code of zero does not prove tests passed.
Before upload, the guest checks the archive for recognized credential paths and
contents. It also checks the bootstrap credentials and supplied environment values.
A rejected archive uploads no result artifacts and retains the sandbox.
The result exporter excludes known runtime credentials from returned source files.
Exclusions apply to both patch trees and preserve the original worktree.
A credential in a file name blocks export to protect the manifest.
Credentials travel through a private file to the local validator.
The daemon and client repeat the recognized credential checks.
The guest also redacts known credentials from the runner log.

These checks cannot identify every unknown or encoded secret.
Review artifact content before sharing it. Structured test evidence remains pending.

A collection failure marks the job failed and retains its sandbox for inspection.
The result records the lease expiry and corrective action. Inspect the sandbox
before that expiry. A successful collection permits normal sandbox cleanup.
Backend identity changes, other active jobs, or uncertain cleanup produce an
explicit blocked cleanup state. Keepalive jobs record the retained sandbox.

The result pins the server, sandbox creation time, VM generation, and guest
incarnation. Cleanup checks those values before deleting a VM. Missing or changed
identity information blocks deletion. Enabling keepalive on the sandbox preserves
it, including during recovery.

After a daemon restart, a saved pending cleanup resumes in tracked background work.
Recovery repeats artifact and execution checks before cleanup. If the VM is already
absent, recovery updates local records without sending another backend destroy.
Source removal refuses pending cleanup and verified results with blocked cleanup.

After resolving a cleanup error, save a retry without rerunning the task:

```bash
agentlab job cleanup "$job_id" --json
agentlab job show "$job_id" --json
```

Check `repository_result_cleanup` before retrying. The request requires
`job.cleanup` and access to the job's sandbox. It returns `accepted:true` after
saving pending cleanup. Observe `cleanup_status` through `job show` or `job result`.
Repeating a completed or retained cleanup returns `accepted:false`.
A retry preserves the saved target identity. It cannot adopt a replacement VM.
Old results without a saved target require inspection. Recovery of incomplete
collection before a verified result is saved remains pending.

`job result` downloads files into a private directory. It verifies their hashes,
manifest, bundle, and execution evidence again. Interrupted downloads can be retried. A retry removes
abandoned staging files for that job and server. Completed cache entries receive
integrity checks before reuse. The command preserves existing cache entries and
source worktrees. A failed integrity check publishes no result directory.

The directory contains `manifest.json`, `source.bundle`, `changes.patch`,
`execution.tar.gz`, and `receipt.json`. The receipt records the server, job,
status, verified source, artifacts, and resources. An unchanged task produces an
empty patch. A failed task can still return verified changes. Read the job status
and execution archive before reporting a test result.

Review the patch against your unchanged local worktree:

```bash
git apply --check "$patch_path"
```

Apply it only after review. Push repository changes only with user authorization.
You can run the local exporter directly when inspecting a retained guest:

```bash
agentlab repo result --repo "$repository_path" --base-revision "$initial_revision" --out "$private_output" --json
```

Automatic resource reconciliation after a server crash, structured test report
validation, and full task accounting remain pending. The broader
`repository_transfer` capability stays false until that work is complete.

## Included content

The snapshot contains the effective working tree. This includes unstaged changes,
staged changes, deletions, and allowed untracked files. When staged and unstaged
content differ, the snapshot uses the current file content.

The command preserves the source files, index, and current branch. It does not
create commits in the source repository. Source hooks, file monitoring, and Git
content filters do not run. Missing local objects cause an error instead of a
network fetch.

The bundle contains two new commits: a sanitized base and the current snapshot.
It does not contain prior source history, author details, remote configuration,
or the original commits. The manifest records the original source revision.
Regular files retain their executable mode. Internal file symlinks can remain
when their targets exist in the allowed snapshot. Link chains cannot exceed 32 links.

## Excluded content

Git ignore rules apply to tracked and untracked files. This includes repository,
local, and global ignore settings. Additional filters exclude:

- Credential directories, including `.ssh`, `.aws`, `.kube`, and `.docker`.
- Environment files, private key files, credential files, and common secret files.
- Recognized private keys, access token formats, credential URLs, and literal
  credential assignments.
- External, dangling, and cyclic symlinks, plus unsupported file types.

Content filters recognize known patterns. They cannot identify every application
secret. Inspect the manifest and bundle before transferring sensitive code.
Add private files to your ignore rules before creating the snapshot.

The manifest lists additional exclusions with stable reason codes. Git-ignored
files do not appear in this list. An exclusion can refer to the sanitized base,
the current tree, or both.

## Limits and recovery

Each tree can contain up to 100 MiB of content. Each file can contain up to 8 MiB.
The source listing can contain up to 10,000 paths. The bundle limit is 256 MiB.
Reduce the repository scope when these limits prevent a snapshot.

Submodules and repositories without an initial commit are unsupported. A changed
source tree produces `repository_changed`. Retry after edits finish.

Equivalent content produces the same snapshot revision and bundle checksum.
Exclusion metadata can differ without changing those values. Each manifest gets
its own cache entry. Cache verification rejects changed bundles or manifests.
Remove the reported cache entry and retry after a cache verification error.

Snapshot caches have no automatic expiry. Remove an artifact directory when you
no longer need it. The command does not change the source repository during
cache removal.
