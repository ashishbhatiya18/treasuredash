package app

import (
	"testing"

	"assofin/internal/bank"
)

// Overlapping or repeated statements must not duplicate transactions or
// postings, and each statement must still reconcile.
func TestOverlappingStatements(t *testing.T) {
	for _, order := range []string{"AB", "BA"} {
		t.Run(order, func(t *testing.T) {
			a := testApp(t)
			a.SaveSettings(map[string]string{"cutover_date": "2026-09-01"})
			a.UpsertFlat(Flat{Code: "1A", Owner: "Test Owner", AreaSqft: 1000})
			a.CreatePeriod("Q2FY27")
			a.RaiseInvoices("Q2FY27")

			tx := func(date, ref string, cr, dr, bal int64, narr string) bank.Txn {
				return bank.Txn{Date: date, ValueDate: date, RefNo: ref, Credit: cr * 100, Debit: dr * 100, Balance: bal * 100, Narration: narr}
			}
			t1 := tx("2026-09-05", "R1", 5000, 0, 105000, "UPI-TEST OWNER-1A MAINTENANCE")
			t2 := tx("2026-09-12", "R2", 0, 2000, 103000, "NEFT DR-XYZ-VENDOR-SERVICE")
			t3 := tx("2026-09-15", "R3", 7000, 0, 110000, "UPI-TEST OWNER-1A MAINTENANCE")
			t4 := tx("2026-09-25", "R4", 1000, 0, 111000, "UPI-SOMEONE-PAYMENT")
			A := &bank.Statement{From: "2026-09-01", To: "2026-09-15", Opening: 10000000, Closing: 11000000, Txns: []bank.Txn{t1, t2, t3}}
			B := &bank.Statement{From: "2026-09-10", To: "2026-09-30", Opening: 10500000, Closing: 11100000, Txns: []bank.Txn{t2, t3, t4}}
			first, second := A, B
			if order == "BA" {
				first, second = B, A
			}
			if _, err := a.importParsed("first.pdf", "h1", first, nil); err != nil {
				t.Fatal(err)
			}
			s2, err := a.importParsed("second.pdf", "h2", second, nil)
			if err != nil {
				t.Fatal(err)
			}
			if s2.New != 1 {
				t.Errorf("second statement should add only its 1 new line, added %d", s2.New)
			}
			// the same lines again under another file name: nothing new
			s3, err := a.importParsed("again.pdf", "h3", A, nil)
			if err != nil {
				t.Fatal(err)
			}
			if s3.New != 0 {
				t.Errorf("re-import added %d lines", s3.New)
			}

			var txns, payments int
			a.DB.QueryRow(`SELECT COUNT(*) FROM bank_txns`).Scan(&txns)
			a.DB.QueryRow(`SELECT COUNT(*) FROM ledger WHERE kind='payment' AND bank_txn_id IS NOT NULL`).Scan(&payments)
			if txns != 4 {
				t.Errorf("want 4 distinct bank lines, got %d", txns)
			}
			if payments != 2 { // the two 1A credits, each posted once
				t.Errorf("want 2 postings, got %d", payments)
			}
			f, _ := a.Flat("1A")
			// 12,000 invoice − (5,000 + 7,000) paid + one ₹1,000 late fee (paid after the 31 Aug due date)
			if f.Balance != 100000 {
				t.Errorf("1A balance %d", f.Balance)
			}
			sts, _ := a.Statements()
			for _, st := range sts {
				if !st.Balanced || len(st.Issues) > 0 {
					t.Errorf("%s: balanced=%v issues=%v", st.Filename, st.Balanced, st.Issues)
				}
			}
		})
	}
}
