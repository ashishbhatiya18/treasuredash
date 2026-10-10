package db

import (
	"database/sql"
	_ "embed"
	"errors"
	"fmt"

	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"
)

//go:embed schema_sqlite.sql
var schemaSQLite string

//go:embed schema_pg.sql
var schemaPG string

// Open connects to PostgreSQL when url is set (e.g.
// postgres://user:pass@host:5432/treasuredash?search_path=treasuredash),
// otherwise to the SQLite file at sqlitePath, and applies the schema.
// Schema statements are idempotent (CREATE … IF NOT EXISTS).
func Open(url, sqlitePath string) (*DB, error) {
	if url != "" {
		raw, err := sql.Open("pgx", url)
		if err != nil {
			return nil, err
		}
		raw.SetMaxOpenConns(8)
		if err := raw.Ping(); err != nil {
			return nil, fmt.Errorf("postgres: %w", err)
		}
		if _, err := raw.Exec(schemaPG); err != nil {
			return nil, fmt.Errorf("postgres schema: %w", err)
		}
		return &DB{DB: raw, PG: true}, nil
	}
	dsn := "file:" + sqlitePath + "?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
	raw, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// One connection: SQLite serialises writers anyway, and callers must not
	// nest queries inside an open transaction or result set.
	raw.SetMaxOpenConns(1)
	if _, err := raw.Exec(schemaSQLite); err != nil {
		return nil, fmt.Errorf("sqlite schema: %w", err)
	}
	return &DB{DB: raw}, nil
}

// EnsureColumn adds a column to a table created by an older version.
func (d *DB) EnsureColumn(table, column, def string) error {
	if d.PG {
		_, err := d.DB.Exec(`ALTER TABLE ` + table + ` ADD COLUMN IF NOT EXISTS ` + column + ` ` + def)
		return err
	}
	rows, err := d.DB.Query(`SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		return err
	}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			rows.Close()
			return err
		}
		if n == column {
			rows.Close()
			return nil
		}
	}
	rows.Close()
	_, err = d.DB.Exec(`ALTER TABLE ` + table + ` ADD COLUMN ` + column + ` ` + def)
	return err
}

// ErrNoSnapshot: file snapshots exist only for SQLite; PostgreSQL is backed up
// by the platform (pg_dump / `home br`).
var ErrNoSnapshot = errors.New("file snapshots are only available with SQLite; back up PostgreSQL with pg_dump")

// Snapshot writes a consistent copy of the database to path.
func (d *DB) Snapshot(path string) error {
	if d.PG {
		return ErrNoSnapshot
	}
	_, err := d.DB.Exec(`VACUUM INTO ?`, path)
	return err
}

// WithoutForeignKeys runs fn with foreign-key enforcement relaxed (SQLite only:
// used by one-off renames of referenced keys). It reports violations left behind.
func (d *DB) WithoutForeignKeys(fn func() error) error {
	if d.PG {
		return errors.New("WithoutForeignKeys is SQLite-only")
	}
	if _, err := d.DB.Exec(`PRAGMA foreign_keys=OFF`); err != nil {
		return err
	}
	defer d.DB.Exec(`PRAGMA foreign_keys=ON`)
	if err := fn(); err != nil {
		return err
	}
	var bad int
	d.DB.QueryRow(`SELECT COUNT(*) FROM pragma_foreign_key_check`).Scan(&bad)
	if bad > 0 {
		return fmt.Errorf("%d foreign-key problems after the change", bad)
	}
	return nil
}
