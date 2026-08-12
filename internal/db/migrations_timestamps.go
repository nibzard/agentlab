package db

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// backfillTimestampColumns rewrites every stored timestamp value to the
// fixed-width 9-digit-fractional form (review M1). It walks all user tables,
// inspects each TEXT column, and rewrites any value that parses as an RFC3339
// timestamp but is not already in the canonical fixed-width form.
//
// It is safe and idempotent:
//   - non-timestamp TEXT (names, ids, yaml, tokens) does not parse as a time
//     and is left untouched;
//   - values already in fixed-width form re-format identically and are skipped.
//
// The whole pass runs within the migration transaction.
func backfillTimestampColumns(tx *sql.Tx) error {
	tables, err := userTables(tx)
	if err != nil {
		return fmt.Errorf("list tables: %w", err)
	}
	for _, table := range tables {
		columns, err := textColumns(tx, table)
		if err != nil {
			return fmt.Errorf("columns of %s: %w", table, err)
		}
		for _, col := range columns {
			if err := normalizeTimestampColumn(tx, table, col); err != nil {
				return fmt.Errorf("normalize %s.%s: %w", table, col, err)
			}
		}
	}
	return nil
}

func userTables(tx *sql.Tx) ([]string, error) {
	rows, err := tx.Query(`SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		tables = append(tables, name)
	}
	return tables, rows.Err()
}

// textColumns returns the TEXT-typed columns of table (per PRAGMA table_info).
func textColumns(tx *sql.Tx, table string) ([]string, error) {
	rows, err := tx.Query(fmt.Sprintf(`PRAGMA table_info(%s)`, quoteIdent(table)))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var cols []string
	for rows.Next() {
		var (
			cid       int
			name      string
			ctype     string
			notnull   int
			dfltValue sql.NullString
			pk        int
		)
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dfltValue, &pk); err != nil {
			return nil, err
		}
		if strings.EqualFold(strings.TrimSpace(ctype), "TEXT") {
			cols = append(cols, name)
		}
	}
	return cols, rows.Err()
}

func normalizeTimestampColumn(tx *sql.Tx, table, col string) error {
	qTable := quoteIdent(table)
	qCol := quoteIdent(col)
	rows, err := tx.Query(fmt.Sprintf(`SELECT rowid, %s FROM %s WHERE %s IS NOT NULL`, qCol, qTable, qCol))
	if err != nil {
		return err
	}
	type update struct {
		rowid int64
		value string
	}
	var pending []update
	for rows.Next() {
		var (
			rowid int64
			val   string
		)
		if err := rows.Scan(&rowid, &val); err != nil {
			rows.Close()
			return err
		}
		t, err := time.Parse(time.RFC3339Nano, val)
		if err != nil {
			// Not a timestamp; leave it alone.
			continue
		}
		fixed := t.UTC().Format(timeLayoutFixed)
		if fixed != val {
			pending = append(pending, update{rowid: rowid, value: fixed})
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, u := range pending {
		if _, err := tx.Exec(fmt.Sprintf(`UPDATE %s SET %s = ? WHERE rowid = ?`, qTable, qCol), u.value, u.rowid); err != nil {
			return err
		}
	}
	return nil
}

// quoteIdent wraps a SQL identifier in double quotes, escaping any embedded
// double quotes. The schema uses simple unquoted identifiers, so this is purely
// defensive.
func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}
