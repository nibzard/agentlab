# How to upgrade and migrate

Move the `agentlabd` daemon and `agentlab` CLI between AgentLab releases without
losing state.

## Prerequisites

- Operator access to the Proxmox host that runs `agentlabd`.
- The current version recorded: run `agentlab --version` and `agentlab status`.
- A backup target for the database and config.

## Steps

1. Stop the daemon and back up state while it is down:

    ```bash
    sudo systemctl stop agentlabd.service
    sudo cp /var/lib/agentlab/agentlab.db \
       /var/lib/agentlab/agentlab.db.backup.$(date +%Y%m%d)
    sudo cp -r /etc/agentlab /etc/agentlab.backup.$(date +%Y%m%d)
    ```

2. Install the new binaries or package over the old ones:

    ```bash
    sudo cp agentlabd /usr/local/bin/
    sudo cp agentlab /usr/local/bin/
    ```

3. Start the daemon. Schema migrations are SQLite-based, versioned,
   transactional, and applied automatically on startup. A failed migration
   prevents startup, which protects the database from a partial upgrade.

    ```bash
    sudo systemctl start agentlabd.service
    ```

4. Confirm the applied migrations if you want a record:

    ```bash
    sqlite3 /var/lib/agentlab/agentlab.db \
       "SELECT version, name FROM schema_migrations ORDER BY version;"
    ```

## Verify

- `agentlab --version` reports the new version.
- `agentlab status` reaches the daemon and returns a healthy status.
- `journalctl -u agentlabd.service` shows no migration errors.

## Roll back

A binary-only rollback swaps the old binaries back and restarts. A full rollback
also restores the database and config backups. A newer database schema is not
backwards-compatible with older binaries after a migration runs, so restore the
database backup as part of a full rollback.

!!! note "Running sandboxes survive an upgrade"
    Running sandbox VMs keep running while the daemon is stopped. New sandbox
    creation is unavailable only while `agentlabd` is down.

## Cut a release

Releases are produced by GoReleaser on a `v*` git tag. The release version
comes from the tag (`git describe` / GoReleaser `{{.Version}}`); the `VERSION`
file is a hand-maintained marker the build does not read. To cut a release,
tag and push:

```bash
git tag vX.Y.Z
git push origin vX.Y.Z
```

GoReleaser publishes per-OS/arch archives and a `checksums.txt` file to the
GitHub release. See ../reference/releases.md for the archive layout.

!!! note "Compatibility statements are not yet documented"
    `schema_version` compatibility guarantees across releases, and
    version-by-version migration notes, are not yet documented. Test upgrades
    against a non-production host first and keep database backups.
