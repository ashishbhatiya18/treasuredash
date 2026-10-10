package db

import (
	"fmt"
	"strings"
)

// Tables lists every table in foreign-key order (referenced tables first).
var Tables = []string{
	"settings", "flats", "periods", "bank_statements", "bank_txns", "ledger", "payer_aliases", "categories",
	"expenses", "other_income", "reminders", "deposits", "deposit_fundings", "ledger_voids", "payees",
}

// HasUserData reports whether any table besides seeded settings/categories has rows.
func (d *DB) HasUserData() (bool, error) {
	for _, t := range Tables {
		if t == "settings" || t == "categories" {
			continue
		}
		var n int
		if err := d.DB.QueryRow(`SELECT COUNT(*) FROM ` + t).Scan(&n); err != nil {
			return false, err
		}
		if n > 0 {
			return true, nil
		}
	}
	return false, nil
}

// CopyAll replaces every table in dst (PostgreSQL) with the rows of src,
// keeping ids, in one transaction, then moves the id sequences past them.
func CopyAll(src, dst *DB) (map[string]int, error) {
	if !dst.PG {
		return nil, fmt.Errorf("destination must be PostgreSQL")
	}
	tx, err := dst.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := tx.Tx.Exec(`TRUNCATE ` + strings.Join(Tables, ", ") + ` RESTART IDENTITY CASCADE`); err != nil {
		return nil, err
	}
	counts := map[string]int{}
	for _, t := range Tables {
		rows, err := src.DB.Query(`SELECT * FROM ` + t)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", t, err)
		}
		cols, _ := rows.Columns()
		ph := strings.TrimSuffix(strings.Repeat("?,", len(cols)), ",")
		ins := `INSERT INTO ` + t + `(` + strings.Join(cols, ",") + `) VALUES(` + ph + `)`
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				rows.Close()
				return nil, err
			}
			for i, v := range vals {
				if b, ok := v.([]byte); ok {
					vals[i] = string(b)
				}
			}
			if _, err := tx.Exec(ins, vals...); err != nil {
				rows.Close()
				return nil, fmt.Errorf("write %s: %w", t, err)
			}
			counts[t]++
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
		var hasID bool
		tx.QueryRow(`SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name=? AND column_name='id' AND data_type='bigint')`, t).Scan(&hasID)
		if hasID {
			if _, err := tx.Tx.Exec(`SELECT setval(pg_get_serial_sequence('` + t + `','id'), COALESCE((SELECT MAX(id) FROM ` + t + `),1), (SELECT MAX(id) IS NOT NULL FROM ` + t + `))`); err != nil {
				return nil, fmt.Errorf("sequence %s: %w", t, err)
			}
		}
	}
	return counts, tx.Commit()
}
