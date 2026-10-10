package app

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

// A database written with start-year names must be renamed to Indian FY names,
// including chains where an old id equals another quarter's new id.
func TestMigrateFYNaming(t *testing.T) {
	a := testApp(t)
	if a.DB.PG {
		t.Skip("old start-year quarter names only ever existed in SQLite databases")
	}
	a.DB.Exec(`DELETE FROM settings WHERE key=?`, fyNamingKey)
	a.UpsertFlat(Flat{Code: "3D", Owner: "X", AreaSqft: 1000})
	for _, p := range []struct {
		id      string
		fy, q   int
		s, e, d string
	}{
		{"Q2FY25", 2025, 2, "2025-07-01", "2025-09-30", "2025-08-31"},
		{"Q2FY26", 2026, 2, "2026-07-01", "2026-09-30", "2026-08-31"},
	} {
		if _, err := a.DB.Exec(`INSERT INTO periods(id,fy,quarter,start_date,end_date,due_date,rate_per_sqft,late_fee,auto_late_fee,status)
			VALUES(?,?,?,?,?,?,4,100000,0,'closed')`, p.id, p.fy, p.q, p.s, p.e, p.d); err != nil {
			t.Fatal(err)
		}
		a.DB.Exec(`INSERT INTO ledger(flat,date,kind,period_id,seq,amount,description) VALUES('3D',?,'invoice',?,0,1200000,?)`, p.s, p.id, "Maintenance "+p.id+" (imported)")
	}
	if err := a.migrateFYNaming(); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	rows, _ := a.DB.Query(`SELECT period_id,description FROM ledger ORDER BY date`)
	for rows.Next() {
		var p, d string
		rows.Scan(&p, &d)
		got[p] = d
	}
	rows.Close()
	if got["Q2FY26"] != "Maintenance Q2FY26 (imported)" || got["Q2FY27"] != "Maintenance Q2FY27 (imported)" || len(got) != 2 {
		t.Fatalf("after migration: %v", got)
	}
	var n int
	a.DB.QueryRow(`SELECT COUNT(*) FROM periods WHERE id IN ('Q2FY26','Q2FY27')`).Scan(&n)
	if n != 2 {
		t.Fatalf("periods not renamed: %d", n)
	}
	// second run is a no-op
	if err := a.migrateFYNaming(); err != nil {
		t.Fatal(err)
	}
	a.DB.QueryRow(`SELECT COUNT(*) FROM periods WHERE id='Q2FY28'`).Scan(&n)
	if n != 0 {
		t.Fatal("migration ran twice")
	}
}

func TestUpgradeUneditedEmailText(t *testing.T) {
	a := testApp(t)
	old := "an earlier default wording"
	sum := sha256.Sum256([]byte(old))
	saved := previousDefaults["payment_request_template"]
	previousDefaults["payment_request_template"] = append(append([]string{}, saved...), hex.EncodeToString(sum[:]))
	defer func() { previousDefaults["payment_request_template"] = saved }()
	a.DB.Exec(`UPDATE settings SET value=? WHERE key='payment_request_template'`, old)
	a.DB.Exec(`UPDATE settings SET value='my own subject' WHERE key='payment_request_subject'`)
	if err := a.upgradeDefaultTexts(); err != nil {
		t.Fatal(err)
	}
	if a.Setting("payment_request_template") != defaultSettings["payment_request_template"] {
		t.Error("unedited old template should be upgraded")
	}
	if a.Setting("payment_request_subject") != "my own subject" {
		t.Error("a customised subject must be kept")
	}
}
