# Upgrade and Migration Guide

This document covers upgrading AgentLab between versions, including database migrations, configuration changes, and rollback procedures.

## Table of Contents

- [Versioning Strategy](#versioning-strategy)
- [Checking Current Version](#checking-current-version)
- [Upgrade Procedures](#upgrade-procedures)
- [Database Migrations](#database-migrations)
- [Breaking Changes](#breaking-changes)
- [Configuration Changes](#configuration-changes)
- [Profile Compatibility](#profile-compatibility)
- [Rollback Procedures](#rollback-procedures)
- [Testing Upgrades Safely](#testing-upgrades-safely)

## Versioning Strategy

AgentLab uses [Semantic Versioning](https://semver.org/):

- **MAJOR**: Incompatible API changes
- **MINOR**: Backwards-compatible functionality additions
- **PATCH**: Backwards-compatible bug fixes

Example: `v1.2.3`
- `1` = MAJOR version
- `2` = MINOR version
- `3` = PATCH version

### Pre-release Versions

Pre-release versions are indicated with a suffix:
- `v1.2.3-rc.1` - Release candidate 1 for version 1.2.3
- `v1.2.3-beta.1` - Beta release 1 for version 1.2.3
- `v1.2.3-alpha.1` - Alpha release 1 for version 1.2.3

### Development Versions

Development builds are labeled as `dev` and include commit information:
```
version=dev commit=abc1234 date=2025-01-15T10:30:00Z
```

## Checking Current Version

### Using the CLI

```bash
agentlab --version
```

Output:
```
AgentLab version=v1.2.3 commit=abc1234 date=2025-01-15T10:30:00Z
```

### Using the Daemon

```bash
agentlab status
```

The daemon status output includes version information.

### Checking Binary Version

```bash
agentlabd --version
```

### Database Schema Version

To check the applied database migrations:

```bash
sqlite3 /var/lib/agentlab/agentlab.db "SELECT version, name, applied_at FROM schema_migrations ORDER BY version;"
```

Output:
```
1|init_core_tables|2025-01-15T10:00:00Z
2|add_job_spec_fields|2025-01-20T15:30:00Z
3|add_artifacts|2025-02-01T09:15:00Z
```

## Upgrade Procedures

### Standard Upgrade (PATCH and MINOR versions)

For PATCH and MINOR version upgrades, the process is straightforward:

1. **Backup your data** (always recommended)
2. **Stop the daemon**
3. **Install the new version**
4. **Start the daemon**
5. **Verify the upgrade**

#### Step-by-Step

```bash
# 1. Stop the daemon
sudo systemctl stop agentlabd.service

# 2. Backup the database (recommended)
sudo cp /var/lib/agentlab/agentlab.db /var/lib/agentlab/agentlab.db.backup.$(date +%Y%m%d)

# 3. Backup configuration
sudo cp -r /etc/agentlab /etc/agentlab.backup.$(date +%Y%m%d)

# 4. Install new version
sudo dpkg -i agentlab_1.2.4_amd64.deb
# or
sudo systemctl stop agentlabd.service
sudo cp agentlabd /usr/local/bin/
sudo cp agentlab /usr/local/bin/

# 5. Start the daemon
sudo systemctl start agentlabd.service

# 6. Verify the upgrade
agentlab --version
agentlab status
```

### MAJOR Version Upgrades

MAJOR versions may include breaking changes. Follow these steps:

1. **Review breaking changes** (see [Breaking Changes](#breaking-changes))
2. **Test in a staging environment** (see [Testing Upgrades Safely](#testing-upgrades-safely))
3. **Backup all data**
4. **Export critical data** (if required by breaking changes)
5. **Stop the daemon**
6. **Install the new version**
7. **Update configuration** (if required)
8. **Update profiles** (if required)
9. **Start the daemon**
10. **Verify functionality**

#### Step-by-Step for MAJOR Upgrade

```bash
# 1. Review release notes for breaking changes
# (Check GitHub releases or CHANGELOG.md)

# 2. Stop the daemon
sudo systemctl stop agentlabd.service

# 3. Create comprehensive backup
sudo systemctl stop agentlabd.service
BACKUP_DATE=$(date +%Y%m%d_%H%M%S)
sudo cp /var/lib/agentlab/agentlab.db /var/lib/agentlab/agentlab.db.backup.$BACKUP_DATE
sudo tar czf /tmp/agentlab_config_backup_$BACKUP_DATE.tar.gz /etc/agentlab
sudo tar czf /tmp/agentlab_data_backup_$BACKUP_DATE.tar.gz /var/lib/agentlab

# 4. Export data if needed (example: if schema changed significantly)
agentlab sandbox list --output json > sandboxes_backup.json
agentlab workspace list --output json > workspaces_backup.json

# 5. Update configuration files (if required)
# Edit /etc/agentlab/config.yaml according to migration guide

# 6. Update profiles (if required)
# Update profile YAML files in /etc/agentlab/profiles/

# 7. Install new version
sudo dpkg -i agentlab_2.0.0_amd64.deb

# 8. Start the daemon
sudo systemctl start agentlabd.service

# 9. Check logs for migration issues
sudo journalctl -u agentlabd.service -f

# 10. Verify functionality
agentlab status
agentlab sandbox list
```

## Database Migrations

AgentLab uses SQLite with automatic schema migrations. Migrations are applied on daemon startup.

### Migration System

- Migrations are versioned and run in order
- Each migration is recorded in the `schema_migrations` table
- Migrations are transactional - all statements succeed or the entire migration fails
- Failed migrations prevent daemon startup

### Current Migrations

As of AgentLab v1.x, the following migrations exist:

| Version | Name | Description |
|---------|------|-------------|
| 1 | `init_core_tables` | Creates core tables: sandboxes, jobs, profiles, workspaces, bootstrap_tokens, events |
| 2 | `add_job_spec_fields` | Adds task, mode, ttl_minutes, keepalive to jobs table |
| 3 | `add_artifacts` | Creates artifact_tokens and artifacts tables |

### Migration Process

When you upgrade:

1. Daemon starts and checks the `schema_migrations` table
2. Pending migrations are identified
3. Each migration runs in a transaction:
   - All SQL statements execute
   - On success, migration is recorded
   - On failure, transaction rolls back and daemon exits with error
4. Daemon continues startup if all migrations succeed

### Manual Migration Inspection

To see what migrations would be applied without running them:

```bash
# Check current version
sqlite3 /var/lib/agentlab/agentlab.db "SELECT MAX(version) FROM schema_migrations;"
```

### Failed Migration Recovery

If a migration fails:

1. Check the error message in logs:
   ```bash
   sudo journalctl -u agentlabd.service -n 50
   ```

2. Common failure causes:
   - Database file permissions
   - Insufficient disk space
   - Database corruption
   - Incompatible manual schema changes

3. Restore from backup:
   ```bash
   sudo systemctl stop agentlabd.service
   sudo cp /var/lib/agentlab/agentlab.db.backup.YYYYMMDD /var/lib/agentlab/agentlab.db
   sudo systemctl start agentlabd.service
   ```

4. Investigate the failure cause before retrying

### Data Preservation

Migrations preserve existing data:

- `ALTER TABLE ADD COLUMN` adds new columns with default values
- New tables start empty
- No data is deleted unless specified in a breaking change

**WARNING**: MAJOR version upgrades may include data migrations or schema changes that transform data. Always backup before upgrading.

## Breaking Changes

This section documents breaking changes by version.

### Version 2.0.0 (Future - Example)

#### Example of potential breaking changes in a future major version

**Configuration Changes:**
- `proxmox_backend` default changed from `shell` to `api`
- Existing shell configurations must explicitly set `proxmox_backend: shell`

**API Changes:**
- Bootstrap API endpoint changed from `/bootstrap` to `/api/v1/bootstrap`
- Artifact upload endpoint changed from `/upload` to `/api/v1/artifacts/upload`

**Profile Changes:**
- `template_vm` field renamed to `template_vmid`
- `cpu` field renamed to `cores`

**Migration Required:**
- Update configuration files
- Update profile YAML files
- Update API client code

### Version 1.x to 1.2.0 (Proxmox API Backend)

**Configuration Changes:**
- New: `proxmox_backend` field (defaults to `api`)
- New: `proxmox_api_url` field
- New: `proxmox_api_token` field
- New: `proxmox_node` field

**Migration Required:**
- Create Proxmox API token
- Update configuration with API credentials

**Steps:**
```bash
# Create API token
pveum user token add root@pam agentlab-api --privsep=0

# Update /etc/agentlab/config.yaml
# proxmox_backend: api
# proxmox_api_url: https://localhost:8006
# proxmox_api_token: root@pam!agentlab-api=<token-uuid>
```

### Version 1.0.0 to 1.1.0 (Artifact Support)

**Database Changes:**
- Migration 3: Adds `artifact_tokens` and `artifacts` tables

**Configuration Changes:**
- New: `artifact_listen` field
- New: `artifact_dir` field
- New: `artifact_max_bytes` field
- New: `artifact_token_ttl_minutes` field

**No migration required** - features are opt-in.

## Configuration Changes

### Deprecated Configuration Options

When configuration options are deprecated:

1. They continue to work but generate a warning
2. They are removed in the next MAJOR version
3. Migration guide specifies replacement options

### Example: Deprecated Option

```yaml
# Old (deprecated in v1.3.0)
proxmox_host: localhost

# New (use instead)
proxmox_api_url: https://localhost:8006
```

### Configuration Migration Procedure

1. Review deprecation warnings in logs
2. Update configuration file with new options
3. Test configuration with `agentlabd --config-check` (if available)
4. Restart daemon

## Profile Compatibility

### Profile Format Evolution

Profile formats are generally backwards compatible:

- New fields are optional
- Old fields continue to work
- Default values apply for missing fields

### Profile Migration

When profile fields change:

#### Example: Field Renaming

```yaml
# Old (v1.0)
name: my-profile
template_vm: 9000
cpu: 4

# New (v1.1+)
name: my-profile
template_vmid: 9000  # renamed from template_vm
cores: 4  # renamed from cpu
```

#### Migration
1. Update profile YAML files
2. Reload profiles: `sudo systemctl reload agentlabd.service`
3. Verify with `agentlab profile list`

### Multi-Version Profile Support

AgentLab supports multiple profile versions simultaneously:

- Old profiles continue to work
- New profiles can use new features
- Daemon validates profiles at load time

## Rollback Procedures

### Quick Rollback (Binary Only)

If you need to roll back quickly without data changes:

```bash
# 1. Stop daemon
sudo systemctl stop agentlabd.service

# 2. Restore previous binary
sudo cp /usr/local/bin/agentlabd.backup /usr/local/bin/agentlabd
sudo cp /usr/local/bin/agentlab.backup /usr/local/bin/agentlab

# 3. Start daemon
sudo systemctl start agentlabd.service

# 4. Verify
agentlab --version
```

### Full Rollback (Binary + Database)

If database schema changed and you need to roll back:

```bash
# 1. Stop daemon
sudo systemctl stop agentlabd.service

# 2. Restore previous binary
sudo cp /usr/local/bin/agentlabd.backup /usr/local/bin/agentlabd
sudo cp /usr/local/bin/agentlab.backup /usr/local/bin/agentlab

# 3. Restore database from backup
sudo cp /var/lib/agentlab/agentlab.db.backup.YYYYMMDD /var/lib/agentlab/agentlab.db

# 4. Restore configuration (if changed)
sudo cp -r /etc/agentlab.backup.YYYYMMDD/* /etc/agentlab/

# 5. Start daemon
sudo systemctl start agentlabd.service

# 6. Verify
agentlab --version
agentlab status
```

### Rollback Considerations

**Issues during rollback:**

1. **Database incompatibility**: Newer schema may not work with older binary
   - Solution: Always restore database backup when downgrading

2. **Configuration incompatibility**: New config options may not be recognized
   - Solution: Restore configuration backup or remove new options

3. **Lost data**: Data created after upgrade is lost
   - Solution: Export critical data before rollback if possible

**Best practice:**
- Keep backups for at least 1 week
- Document configuration changes
- Test rollback procedure in staging

## Testing Upgrades Safely

### Staging Environment

Always test upgrades in a staging environment first:

1. Clone production configuration
2. Use test Proxmox instance or separate cluster
3. Run through full upgrade procedure
4. Test all critical functionality
5. Verify no data loss

### Test Upgrade Procedure

```bash
# 1. Create staging environment
# (Set up separate Proxmox instance or use test VMs)

# 2. Copy production database (anonymized if needed)
sudo cp /var/lib/agentlab/agentlab.db /var/lib/agentlab/agentlab.staging.db

# 3. Copy configuration
sudo cp -r /etc/agentlab /etc/agentlab.staging

# 4. Install new version in staging
sudo cp agentlabd.staging /usr/local/bin/agentlabd.staging
sudo cp agentlab.staging /usr/local/bin/agentlab.staging

# 5. Test with staging config
sudo -u agentlab agentlabd.staging --config /etc/agentlab.staging/config.yaml --db /var/lib/agentlab/agentlab.staging.db

# 6. Run tests
# - Create sandbox
# - Run job
# - Upload artifacts
# - Create workspace
# - List/query operations

# 7. Check logs for errors
sudo journalctl -u agentlabd-staging.service -n 100
```

### Smoke Tests

After any upgrade, run these smoke tests:

```bash
# 1. Check daemon is running
agentlab status

# 2. List existing sandboxes
agentlab sandbox list

# 3. Check profile loading
agentlab profile list

# 4. Create test sandbox
agentlab sandbox new --name test-upgrade --profile yolo-ephemeral

# 5. Check sandbox state
agentlab sandbox show test-upgrade

# 6. Clean up test sandbox
agentlab sandbox destroy test-upgrade

# 7. Check logs for errors
sudo journalctl -u agentlabd.service --since "5 minutes ago" | grep -i error
```

### Pre-Upgrade Checklist

Before upgrading production:

- [ ] Review release notes and breaking changes
- [ ] Test upgrade in staging environment
- [ ] Backup database
- [ ] Backup configuration
- [ ] Document current configuration
- [ ] Prepare rollback plan
- [ ] Schedule maintenance window
- [ ] Notify stakeholders

### Post-Upgrade Verification

After upgrading production:

- [ ] Verify daemon started successfully
- [ ] Check logs for errors or warnings
- [ ] Run smoke tests
- [ ] Verify all sandboxes accessible
- [ ] Test job creation and execution
- [ ] Verify artifact upload
- [ ] Check workspace operations
- [ ] Monitor metrics (if enabled)
- [ ] Verify profile compatibility
- [ ] Document any issues

### Automated Testing

For continuous integration, include upgrade tests:

```bash
#!/bin/bash
# test_upgrade.sh

set -e

OLD_VERSION=$1
NEW_VERSION=$2

# Install old version
./install.sh $OLD_VERSION

# Create test data
agentlab sandbox new --name test1 --profile yolo-ephemeral
agentlab job run --repo https://github.com/example/repo --task "test" --profile yolo-ephemeral

# Export state
agentlab sandbox list --output json > before.json

# Stop daemon
sudo systemctl stop agentlabd.service

# Backup database
sudo cp /var/lib/agentlab/agentlab.db /tmp/backup.db

# Upgrade to new version
./install.sh $NEW_VERSION

# Start daemon
sudo systemctl start agentlabd.service

# Wait for startup
sleep 5

# Verify data
agentlab sandbox list --output json > after.json

# Compare (allowing for state changes)
diff <(jq '.[] | {name, profile}' before.json) <(jq '.[] | {name, profile}' after.json)

# Run smoke tests
agentlab sandbox new --name test2 --profile yolo-ephemeral
agentlab sandbox destroy test2

echo "Upgrade test passed!"
```

## Upgrade FAQ

### Do I need to stop running sandboxes during upgrade?

No. Running sandboxes continue running during daemon upgrades. However:

- New sandbox creation is unavailable during upgrade
- In-progress jobs may experience delays in status updates
- Plan upgrades during low-usage periods

### Can I skip versions?

Yes, you can upgrade from v1.0.0 directly to v1.3.0. All intermediate migrations will be applied.

### What if migration fails?

1. Check logs for specific error
2. Verify database file permissions
3. Ensure sufficient disk space
4. Restore from backup and investigate
5. Report issue with full error logs

### How long do migrations take?

Typical migrations complete in seconds. Large databases may take longer:

- < 1000 records: < 1 second
- 1000-10000 records: 1-5 seconds
- > 10000 records: 5-30 seconds

Plan maintenance windows accordingly for large databases.

### Can I downgrade after schema migration?

Only if you restore the database from backup. The schema is not backwards compatible with older binaries after migration.

### Do I need to restart sandboxes after upgrade?

No. Existing sandboxes continue with their current configuration. New sandboxes will use updated profiles and configuration.

## Support

For upgrade issues:

1. Check this guide first
2. Review logs: `sudo journalctl -u agentlabd.service -n 100`
3. Check GitHub Issues for known problems
4. Create new issue with:
   - Versions (before and after)
   - Error messages
   - Relevant log excerpts
   - Configuration (redacted)
