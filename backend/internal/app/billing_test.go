package app

import (
	"testing"
	"time"
)

func TestLateFeesPerMonthOfDelay(t *testing.T) {
	a := testApp(t)
	for _, f := range []Flat{{Code: "1A", Owner: "A", AreaSqft: 1000}, {Code: "1B", Owner: "B", AreaSqft: 1000}} {
		if err := a.UpsertFlat(f); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := a.CreatePeriod("Q3FY27"); err != nil { // due 2026-11-30
		t.Fatal(err)
	}
	if _, err := a.RaiseInvoices("Q3FY27"); err != nil {
		t.Fatal(err)
	}
	// 1A pays 15 days late; 1B never pays.
	a.DB.Exec(`INSERT INTO ledger(flat,date,kind,amount,description) VALUES('1A','2026-12-15','payment',-1200000,'x')`)
	fees := func(flat string) (n int, sum int64) {
		a.DB.QueryRow(`SELECT COUNT(*),COALESCE(SUM(amount),0) FROM ledger WHERE flat=? AND kind='late_fee'`, flat).Scan(&n, &sum)
		return
	}
	cases := []struct {
		asOf   string
		a1, b1 int
	}{
		{"2026-11-30", 0, 0}, // due date itself: no fee
		{"2026-12-01", 1, 1},
		{"2026-12-30", 1, 1}, // 1 month of delay (1–30 Dec)
		{"2026-12-31", 1, 2}, // second month of delay has started
		{"2027-01-01", 1, 2}, // 1A paid on 15 Dec, so only one month
		{"2027-02-01", 1, 3},
	}
	for _, c := range cases {
		d, _ := time.Parse(DateFmt, c.asOf)
		if _, err := a.ApplyLateFees(d); err != nil {
			t.Fatal(err)
		}
		if n, _ := fees("1A"); n != c.a1 {
			t.Errorf("%s: 1A fees %d want %d", c.asOf, n, c.a1)
		}
		if n, sum := fees("1B"); n != c.b1 || sum != int64(c.b1)*100000 {
			t.Errorf("%s: 1B fees %d (₹%s) want %d", c.asOf, n, Rupees(sum), c.b1)
		}
	}
	// back-dated payment before the due date removes fees
	a.DB.Exec(`INSERT INTO ledger(flat,date,kind,amount,description) VALUES('1B','2026-11-20','payment',-1200000,'x')`)
	d, _ := time.Parse(DateFmt, "2027-02-01")
	a.ApplyLateFees(d)
	if n, _ := fees("1B"); n != 0 {
		t.Errorf("after back-dated payment 1B still has %d fees", n)
	}
}

func TestQuarterNaming(t *testing.T) {
	q := QuarterOf(time.Date(2026, 10, 7, 0, 0, 0, 0, time.UTC))
	if q.ID() != "Q3FY27" || q.DefaultDue().Format(DateFmt) != "2026-11-30" {
		t.Fatalf("got %s due %s", q.ID(), q.DefaultDue())
	}
	if QuarterOf(time.Date(2027, 2, 1, 0, 0, 0, 0, time.UTC)).ID() != "Q4FY27" {
		t.Fatal("Feb 2027 should be Q4FY27")
	}
	if FlatsByFloor([]string{"GD", "1C", "2B", "2D"}) != "    GD\n    1C\n    2B, 2D" {
		t.Fatalf("got %q", FlatsByFloor([]string{"GD", "1C", "2B", "2D"}))
	}
}

// A payment recorded by the sheet but never received is voided; the next real
// payment must then settle the older quarter, not the current one.
func TestVoidImportedPaymentReallocates(t *testing.T) {
	a := testApp(t)
	a.UpsertFlat(Flat{Code: "3D", Owner: "Mithun", AreaSqft: 1310})
	a.CreatePeriod("Q2FY27")
	a.CreatePeriod("Q3FY27")
	a.RaiseInvoices("Q2FY27")
	a.RaiseInvoices("Q3FY27")
	a.DB.Exec(`UPDATE periods SET auto_late_fee=0`) // like quarters imported from the sheet
	sheetPay, _ := a.DB.InsertID(`INSERT INTO ledger(flat,date,kind,amount,description,mode,source) VALUES('3D','2026-09-30','payment',-1572000,'Payment for Q2FY27 (as per treasury sheet)','bank','import')`)
	a.DB.Exec(`INSERT INTO ledger(flat,date,kind,amount,description,mode) VALUES('3D','2026-10-02','payment',-1572000,'UPI Vinay','bank')`)

	settledBy := func() map[string]int64 {
		acc, err := a.FlatAccount("3D")
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]int64{}
		for _, c := range acc.Charges {
			if c.Kind == "invoice" {
				out[*c.PeriodID] = c.Outstanding
			}
		}
		return out
	}
	if o := settledBy(); o["Q2FY27"] != 0 || o["Q3FY27"] != 0 {
		t.Fatalf("before: %v", o)
	}
	if err := a.VoidPayment(sheetPay, "Q2 not received by 30 Sep; paid 2 Oct"); err != nil {
		t.Fatal(err)
	}
	if o := settledBy(); o["Q2FY27"] != 0 || o["Q3FY27"] != 1572000 {
		t.Fatalf("after void: Q2 should be cleared by the 2 Oct payment and Q3 open, got %v", o)
	}
	vs, _ := a.Voids("3D")
	if len(vs) != 1 || a.RestoreVoid(vs[0].ID) != nil {
		t.Fatalf("restore failed: %+v", vs)
	}
	if o := settledBy(); o["Q3FY27"] != 0 {
		t.Fatalf("after restore: %v", o)
	}
}
