// Package db is the only place that knows which database is in use. It opens
// SQLite (local development, tests) or PostgreSQL (production), applies the
// schema, and hides the few SQL differences behind small helpers so the domain
// code in internal/app writes one set of queries.
package db

import (
	"context"
	"database/sql"
	"strings"
)

// DB wraps database/sql so the same queries run on SQLite and PostgreSQL.
// Queries are written with `?` placeholders in the SQL both engines share; the
// few constructs that differ go through the helpers below.
type DB struct {
	*sql.DB
	PG bool
}

// Tx mirrors DB inside a transaction.
type Tx struct {
	*sql.Tx
	pg bool
}

// rebind turns `?` placeholders into Postgres `$1, $2…`, leaving quoted text alone.
func rebind(q string) string {
	var b strings.Builder
	b.Grow(len(q) + 8)
	n := 0
	inStr := false
	for i := 0; i < len(q); i++ {
		c := q[i]
		switch {
		case c == '\'':
			inStr = !inStr
			b.WriteByte(c)
		case c == '?' && !inStr:
			n++
			b.WriteByte('$')
			b.WriteString(itoa(n))
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

func itoa(n int) string {
	if n < 10 {
		return string(rune('0' + n))
	}
	return itoa(n/10) + string(rune('0'+n%10))
}

func (d *DB) q(s string) string {
	if d.PG {
		return rebind(s)
	}
	return s
}

func (d *DB) Exec(q string, args ...any) (sql.Result, error) { return d.DB.Exec(d.q(q), args...) }
func (d *DB) Query(q string, args ...any) (*sql.Rows, error) { return d.DB.Query(d.q(q), args...) }
func (d *DB) QueryRow(q string, args ...any) *sql.Row        { return d.DB.QueryRow(d.q(q), args...) }

func (d *DB) Begin() (*Tx, error) {
	tx, err := d.DB.BeginTx(context.Background(), nil)
	if err != nil {
		return nil, err
	}
	return &Tx{tx, d.PG}, nil
}

func (t *Tx) q(s string) string {
	if t.pg {
		return rebind(s)
	}
	return s
}

func (t *Tx) Exec(q string, args ...any) (sql.Result, error) { return t.Tx.Exec(t.q(q), args...) }
func (t *Tx) Query(q string, args ...any) (*sql.Rows, error) { return t.Tx.Query(t.q(q), args...) }
func (t *Tx) QueryRow(q string, args ...any) *sql.Row        { return t.Tx.QueryRow(t.q(q), args...) }

// InsertID runs an INSERT and returns the new row's id (RETURNING works on both
// engines; Postgres has no LastInsertId).
func (d *DB) InsertID(q string, args ...any) (int64, error) {
	var id int64
	err := d.QueryRow(q+" RETURNING id", args...).Scan(&id)
	return id, err
}

func (t *Tx) InsertID(q string, args ...any) (int64, error) {
	var id int64
	err := t.QueryRow(q+" RETURNING id", args...).Scan(&id)
	return id, err
}

// ---- dialect helpers (SQL fragments) --------------------------------------------

// Now is the current UTC time as 'YYYY-MM-DD HH:MM:SS' text (timestamps are TEXT columns).
func (d *DB) Now() string {
	if d.PG {
		return `to_char(now() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS')`
	}
	return `datetime('now')`
}

// Days turns a 'YYYY-MM-DD' expression into a day number, so Days(a)-Days(b)
// is a difference in days on both engines.
func (d *DB) Days(expr string) string {
	if d.PG {
		return "((" + expr + ")::date - DATE '2000-01-01')"
	}
	return "julianday(" + expr + ")"
}

// GroupConcat joins values with commas.
func (d *DB) GroupConcat(expr string) string {
	if d.PG {
		return "string_agg((" + expr + ")::text, ',')"
	}
	return "group_concat(" + expr + ")"
}

// Like is a case-insensitive LIKE (SQLite's LIKE already is, for ASCII).
func (d *DB) Like() string {
	if d.PG {
		return "ILIKE"
	}
	return "LIKE"
}

// Bool stores a boolean in an INTEGER column (Postgres won't cast bool → int).
func Bool(b bool) int {
	if b {
		return 1
	}
	return 0
}
