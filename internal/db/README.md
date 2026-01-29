# SQLite migrations

Agentlabd uses a simple, append-only migration system backed by the
`schema_migrations` table. Each migration is applied in a transaction on
startup, so the daemon always runs against a fully migrated schema.

Backward-compatible evolution policy:
- Add new tables or columns without removing existing ones.
- Prefer nullable columns or defaults, then backfill in application logic.
- Avoid renaming/dropping columns; introduce new columns and migrate data
  forward, then remove legacy fields in a later major version.
- Keep migrations ordered and immutable once released.
