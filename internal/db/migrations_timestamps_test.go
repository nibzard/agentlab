package db

import (
	"database/sql"
	"testing"
	"time"
)

// TestFormatTime_FixedWidthBoundaries verifies the fixed-width formatter always
// emits a 9-digit fractional component so TEXT ordering matches chronological
// order, including the zero, short, and full-width cases (review M1).
func TestFormatTime_FixedWidthBoundaries(t *testing.T) {
	cases := []struct {
		name string
		t    time.Time
		want string
	}{
		{"zero fractional", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), "2026-01-01T00:00:00.000000000Z"},
		{"one digit (100ms)", time.Date(2026, 1, 1, 0, 0, 0, 100_000_000, time.UTC), "2026-01-01T00:00:00.100000000Z"},
		{"trailing-zero trimmed before (1ns)", time.Date(2026, 1, 1, 0, 0, 0, 1, time.UTC), "2026-01-01T00:00:00.000000001Z"},
		{"full width", time.Date(2026, 1, 1, 0, 0, 0, 123_456_789, time.UTC), "2026-01-01T00:00:00.123456789Z"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := formatTime(c.t)
			if got != c.want {
				t.Errorf("formatTime = %q, want %q", got, c.want)
			}
			// Round-trip.
			parsed, err := parseTime(got)
			if err != nil {
				t.Fatalf("parseTime: %v", err)
			}
			if !parsed.Equal(c.t) {
				t.Errorf("round-trip mismatch: %v != %v", parsed, c.t)
			}
		})
	}
}

// TestFormatTime_LexicographicOrdering verifies that two timestamps within the
// same second order lexicographically the same as chronologically — the exact
// hazard RFC3339Nano's trimming introduces (review M1).
func TestFormatTime_LexicographicOrdering(t *testing.T) {
	// Earlier instant that, under RFC3339Nano, would render as ".1" (100ms);
	// later instant renders as ".100000001".
	earlier := time.Date(2026, 1, 1, 0, 0, 0, 100_000_000, time.UTC)
	later := time.Date(2026, 1, 1, 0, 0, 0, 100_000_001, time.UTC)
	fe, fl := formatTime(earlier), formatTime(later)
	if !(fe < fl) {
		t.Errorf("expected %q < %q lexicographically", fe, fl)
	}
	// And the trimmed form would have inverted this order:
	te := earlier.Format(time.RFC3339Nano)
	tl := later.Format(time.RFC3339Nano)
	if !(te > tl) {
		t.Errorf("precondition: expected trimmed %q > %q to demonstrate the bug", te, tl)
	}
}

// TestParseTime_AcceptsLegacyAndFixed verifies reads remain compatible across
// the migration: both the legacy trimmed form and the fixed-width form parse to
// the same instant (review M1).
func TestParseTime_AcceptsLegacyAndFixed(t *testing.T) {
	t1 := time.Date(2026, 1, 1, 0, 0, 0, 100_000_000, time.UTC)
	legacy := "2026-01-01T00:00:00.1Z" // RFC3339Nano trimmed
	fixed := "2026-01-01T00:00:00.100000000Z"
	pLegacy, err := parseTime(legacy)
	if err != nil {
		t.Fatalf("parse legacy: %v", err)
	}
	pFixed, err := parseTime(fixed)
	if err != nil {
		t.Fatalf("parse fixed: %v", err)
	}
	if !pLegacy.Equal(t1) || !pFixed.Equal(t1) {
		t.Errorf("legacy=%v fixed=%v, both want %v", pLegacy, pFixed, t1)
	}
}

// TestBackfillTimestampColumns_NormalizesLegacy verifies the M1 migration
// rewrites legacy trimmed timestamps to fixed-width form and leaves
// non-timestamp TEXT untouched.
func TestBackfillTimestampColumns_NormalizesLegacy(t *testing.T) {
	db := openTestDB(t)
	// Insert legacy-form timestamps directly (bypassing formatTime).
	legacy := "2026-01-01T00:00:00.1Z"
	alreadyFixed := "2026-01-01T00:00:00.000000000Z"
	_, err := db.Exec(`INSERT INTO sandboxes (vmid, name, profile, state, ip, workspace_id, keepalive, lease_expires_at, last_used_at, created_at, updated_at, meta_json, type, image, tags, prompt, owner)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		5001, "sb", "default", "READY", "", nil, 0, nil, nil, legacy, alreadyFixed, nil, "", "", "", "", "")
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	// Seed a profiles row with non-timestamp yaml to ensure it is untouched.
	if _, err := db.Exec(`INSERT INTO profiles (name, template_vmid, yaml, updated_at) VALUES (?,?,?,?)`,
		"yamlprof", 100, "resources:\n  cores: 2\n", legacy); err != nil {
		t.Fatalf("seed profile: %v", err)
	}

	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := backfillTimestampColumns(tx); err != nil {
		_ = tx.Rollback()
		t.Fatalf("backfill: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	var created, updated string
	if err := db.QueryRow(`SELECT created_at, updated_at FROM sandboxes WHERE vmid=5001`).Scan(&created, &updated); err != nil {
		t.Fatalf("read: %v", err)
	}
	if created != "2026-01-01T00:00:00.100000000Z" {
		t.Errorf("created_at=%q, want fixed-width", created)
	}
	if updated != alreadyFixed {
		t.Errorf("updated_at changed from already-fixed: %q", updated)
	}
	// Non-timestamp TEXT must be unchanged.
	var yaml string
	if err := db.QueryRow(`SELECT yaml FROM profiles WHERE name='yamlprof'`).Scan(&yaml); err != nil {
		t.Fatalf("read yaml: %v", err)
	}
	if yaml != "resources:\n  cores: 2\n" {
		t.Errorf("yaml altered by backfill: %q", yaml)
	}
	// The profile's updated_at (legacy timestamp) should have been normalized.
	var profUpdated string
	if err := db.QueryRow(`SELECT updated_at FROM profiles WHERE name='yamlprof'`).Scan(&profUpdated); err != nil {
		t.Fatalf("read prof updated_at: %v", err)
	}
	if profUpdated != "2026-01-01T00:00:00.100000000Z" {
		t.Errorf("profiles.updated_at=%q, want fixed-width", profUpdated)
	}
}

// TestMigration17_RunsOnOpen verifies that opening a fresh DB applies migration
// 17 and that the schema_migrations table records it.
func TestMigration17_RunsOnOpen(t *testing.T) {
	db := openTestDB(t)
	var version int
	err := db.QueryRow(`SELECT version FROM schema_migrations WHERE version=17`).Scan(&version)
	if err == sql.ErrNoRows {
		t.Fatal("migration 17 was not recorded")
	}
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if version != 17 {
		t.Errorf("version=%d want 17", version)
	}
}

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}
