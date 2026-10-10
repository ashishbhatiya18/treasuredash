package app

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strconv"
)

// Databases created before the switch to Indian fiscal-year naming stored
// quarters by the FY's starting year (Oct–Dec 2026 = "Q3FY26"). This renames
// every stored quarter id and the quarter names inside ledger descriptions to
// the ending-year convention (Q3FY27), once, inside a transaction.

const fyNamingKey = "fy_naming"

var oldQuarterRe = regexp.MustCompile(`Q([1-4])FY(\d{2})`)

func shiftQuarterNames(s string) string {
	return oldQuarterRe.ReplaceAllStringFunc(s, func(m string) string {
		sub := oldQuarterRe.FindStringSubmatch(m)
		yy, _ := strconv.Atoi(sub[2])
		return fmt.Sprintf("Q%sFY%02d", sub[1], (yy+1)%100)
	})
}

func (a *App) migrateFYNaming() error {
	var v string
	a.DB.QueryRow(`SELECT value FROM settings WHERE key=?`, fyNamingKey).Scan(&v)
	if v == "indian" {
		return nil
	}
	var n int
	a.DB.QueryRow(`SELECT COUNT(*) FROM periods`).Scan(&n)
	// PostgreSQL databases are created by this version (or copied from an
	// already-migrated SQLite file), so only SQLite can hold old names.
	if n > 0 && !a.DB.PG {
		if err := a.renameQuarters(); err != nil {
			return fmt.Errorf("fy naming migration: %w", err)
		}
	}
	_, err := a.DB.Exec(`INSERT INTO settings(key,value) VALUES(?, 'indian') ON CONFLICT(key) DO UPDATE SET value='indian'`, fyNamingKey)
	return err
}

func (a *App) renameQuarters() error {
	// period ids are referenced by ledger/reminders without ON UPDATE CASCADE,
	// so relax FK checks for the rename (the db package verifies afterwards)
	return a.DB.WithoutForeignKeys(a.renameQuarterRows)
}

func (a *App) renameQuarterRows() error {
	tx, err := a.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	type per struct {
		id      string
		fy, qtr int
	}
	rows, err := tx.Query(`SELECT id,fy,quarter FROM periods ORDER BY start_date DESC`) // newest first: no id collisions
	if err != nil {
		return err
	}
	var ps []per
	for rows.Next() {
		var p per
		if err := rows.Scan(&p.id, &p.fy, &p.qtr); err != nil {
			rows.Close()
			return err
		}
		ps = append(ps, p)
	}
	rows.Close()
	for _, p := range ps {
		newID := Quarter{p.fy, p.qtr}.ID() // fy column holds the starting year
		if newID == p.id {
			continue
		}
		for _, q := range []string{
			`UPDATE periods SET id=? WHERE id=?`,
			`UPDATE ledger SET period_id=? WHERE period_id=?`,
			`UPDATE reminders SET period_id=? WHERE period_id=?`,
			`UPDATE ledger_voids SET period_id=? WHERE period_id=?`,
		} {
			if _, err := tx.Exec(q, newID, p.id); err != nil {
				return err
			}
		}
	}
	// quarter names written into descriptions (reminder texts stay as sent)
	for _, table := range []string{"ledger", "ledger_voids"} {
		rows, err := tx.Query(`SELECT id,description FROM ` + table + ` WHERE description LIKE '%FY%'`)
		if err != nil {
			return err
		}
		type upd struct {
			id   int64
			desc string
		}
		var us []upd
		for rows.Next() {
			var u upd
			if err := rows.Scan(&u.id, &u.desc); err != nil {
				rows.Close()
				return err
			}
			if nd := shiftQuarterNames(u.desc); nd != u.desc {
				us = append(us, upd{u.id, nd})
			}
		}
		rows.Close()
		for _, u := range us {
			if _, err := tx.Exec(`UPDATE `+table+` SET description=? WHERE id=?`, u.desc, u.id); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func (a *App) migrateColumns() error {
	for _, c := range [][3]string{
		{"deposits", "from_savings", "INTEGER NOT NULL DEFAULT 0"},
		{"deposits", "to_savings", "INTEGER NOT NULL DEFAULT 0"},
		{"deposits", "closing_amount", "INTEGER NOT NULL DEFAULT 0"},
		{"reminders", "kind", "TEXT NOT NULL DEFAULT 'followup'"}, // followup / day0
		{"reminders", "subject", "TEXT NOT NULL DEFAULT ''"},
	} {
		if err := a.DB.EnsureColumn(c[0], c[1], c[2]); err != nil {
			return fmt.Errorf("add %s.%s: %w", c[0], c[1], err)
		}
	}
	return nil
}

// Fingerprints of earlier default wordings of the day-0 email. A saved copy
// whose hash matches was never customised, so it is upgraded to the current default.
var previousDefaults = map[string][]string{ // sha256 of earlier default texts
	"payment_request_subject":  {"327d195b85279e2cb41c49721b69d25a337e4a34e7c68b6c10e96a33696152fe", "bb7cd4e9a49ae098f6dfe6c480c7950cd837dc8e29ef70d76d6e9eb690d11b40"},
	"payment_request_template": {"09ff5bb6e2933a5f3da2cbe345f68cd58adf277d06c908c81ae724e4f85ec53b", "25f9bf1d320c520950d4a8e323be2d30aad9694bdbaaa22240a876abe992d69a"},
}

func (a *App) upgradeDefaultTexts() error {
	for key, olds := range previousDefaults {
		sum := sha256.Sum256([]byte(a.Setting(key)))
		cur := hex.EncodeToString(sum[:])
		for _, o := range olds {
			if cur == o {
				if _, err := a.DB.Exec(`UPDATE settings SET value=? WHERE key=?`, defaultSettings[key], key); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// ResetSetting puts a setting back to its built-in default.
func (a *App) ResetSetting(key string) error {
	def, ok := defaultSettings[key]
	if !ok {
		return fmt.Errorf("unknown setting %q", key)
	}
	_, err := a.DB.Exec(`UPDATE settings SET value=? WHERE key=?`, def, key)
	return err
}
