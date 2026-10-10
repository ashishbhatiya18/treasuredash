package app

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
)

func editFixture(t *testing.T) *App {
	t.Helper()
	a := testApp(t)
	for _, f := range []Flat{{Code: "1A", Owner: "Asha", AreaSqft: 1000}, {Code: "1B", Owner: "Bala", AreaSqft: 1200}} {
		if err := a.UpsertFlat(f); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := a.CreatePeriod("Q3FY27"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.RaiseInvoices("Q3FY27"); err != nil {
		t.Fatal(err)
	}
	if err := a.AddPayment("1A", "2026-10-05", 1200000, "cash", "Cash to treasurer"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SaveExpense(Expense{Date: "2026-10-03", Description: "Bescom bill", Payee: "Bescom", Amount: 500000}); err != nil {
		t.Fatal(err)
	}
	return a
}

func rowsOf(t *testing.T, a *App, sheet string) []SheetRow {
	t.Helper()
	d, err := a.Sheet(sheet)
	if err != nil {
		t.Fatal(err)
	}
	return d.Rows
}

func find(rows []SheetRow, key, val string) SheetRow {
	for _, r := range rows {
		if r.Values[key] == val {
			return r
		}
	}
	return SheetRow{}
}

func edit(r SheetRow, kv ...string) InRow {
	v := map[string]string{}
	for k, x := range r.Values {
		v[k] = x
	}
	for i := 0; i+1 < len(kv); i += 2 {
		v[kv[i]] = kv[i+1]
	}
	return InRow{Values: v, Version: r.Version}
}

func mustApply(t *testing.T, a *App, cs Changeset) *ApplyResult {
	t.Helper()
	res, err := a.ApplyChanges(cs)
	if err != nil {
		if res != nil {
			for _, c := range res.Changes {
				if c.Error != "" {
					t.Logf("%s row %d %s: %s", c.Sheet, c.Row, c.Label, c.Error)
				}
			}
		}
		t.Fatal(err)
	}
	return res
}

func TestWorkbookRoundTripIsNoop(t *testing.T) {
	a := editFixture(t)
	var buf bytes.Buffer
	if err := a.WriteWorkbook(&buf); err != nil {
		t.Fatal(err)
	}
	cs, err := a.ReadWorkbook(&buf)
	if err != nil {
		t.Fatal(err)
	}
	res, err := a.PlanChanges(cs)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Changes) != 0 || res.Unchanged == 0 {
		t.Fatalf("expected no changes, got %+v", res)
	}
}

func TestBulkEditJSON(t *testing.T) {
	a := editFixture(t)
	flat1B := find(rowsOf(t, a, "flats"), "code", "1B")
	pay := find(rowsOf(t, a, "ledger"), "kind", "payment")
	exp := find(rowsOf(t, a, "expenses"), "description", "Bescom bill")

	res := mustApply(t, a, Changeset{
		"flats": {
			edit(flat1B, "owner", "Bala K", "phone1", "98450 00000"),
			{Values: map[string]string{"code": "2a", "owner": "Chitra", "area_sqft": "1,100"}},
		},
		"quarters": {{Values: map[string]string{"id": "Q4FY27"}}},
		"ledger": {
			{Values: map[string]string{"flat": "2A", "date": "01/10/2026", "kind": "opening", "amount": "2500"}},
			{Values: map[string]string{"flat": "1B", "date": "2026-10-10", "kind": "Payment", "amount": "₹14,400", "mode": "gpay_personal"}},
			{Values: pay.Values, Version: pay.Version, Delete: true},
		},
		"expenses": {
			edit(exp, "amount", "5200.50"),
			{Values: map[string]string{"date": "2026-10-04", "description": "Lift AMC Q3", "payee": "Schindler", "amount": "9000"}},
		},
		"income": {{Values: map[string]string{"date": "2026-10-01", "category": "Move-in charges", "amount": "2000", "flat": "1b"}}},
	})
	if (res.Backup == "" && !a.DB.PG) || res.Applied != 9 { // file snapshots are SQLite-only
		t.Fatalf("applied %d, backup %q", res.Applied, res.Backup)
	}

	f, _ := a.Flat("1B")
	if f.Owner != "Bala K" || f.Phone1 != "98450 00000" || f.Balance != 0 {
		t.Errorf("1B: %+v", f)
	}
	f, _ = a.Flat("2A")
	if f.AreaSqft != 1100 || f.Balance != 250000 {
		t.Errorf("2A: %+v", f)
	}
	f, _ = a.Flat("1A")
	if f.Balance != 1200000 {
		t.Errorf("1A balance after deleting its payment: %d", f.Balance)
	}
	if v, _ := a.Voids("1A"); len(v) != 1 || v[0].Amount != -1200000 {
		t.Errorf("voids: %+v", v)
	}
	p, err := a.Period("Q4FY27")
	if err != nil || p.DueDate != "2027-02-28" || !p.AutoLateFee || p.Status != "open" || p.RatePerSqft != 4 {
		t.Errorf("Q4FY27: %+v %v", p, err)
	}
	es, _ := a.Expenses(ExpenseFilter{})
	if len(es) != 2 || es[1].Amount != 520050 || es[0].Category != "Lift AMC" {
		t.Errorf("expenses: %+v", es)
	}

	// nothing left to do on a second run of the same edits
	res2, err := a.PlanChanges(Changeset{"flats": {edit(find(rowsOf(t, a, "flats"), "code", "1B"))}})
	if err != nil || len(res2.Changes) != 0 || res2.Unchanged != 1 {
		t.Errorf("re-plan: %+v %v", res2, err)
	}
}

func TestBulkEditValidationAndConflicts(t *testing.T) {
	a := editFixture(t)
	flats := rowsOf(t, a, "flats")
	old := find(flats, "code", "1A")
	// changed in the portal after "download"
	if err := a.UpsertFlat(Flat{Code: "1A", Owner: "Asha R", AreaSqft: 1000}); err != nil {
		t.Fatal(err)
	}
	inv := find(rowsOf(t, a, "ledger"), "kind", "invoice")
	res, err := a.PlanChanges(Changeset{
		"flats": {
			edit(old, "notes", "corner flat"),                   // conflict
			edit(find(flats, "code", "1B"), "area_sqft", "abc"), // bad number
			{Values: map[string]string{"code": "1B"}, Delete: true},
		},
		"ledger": {
			{Values: map[string]string{"flat": "9Z", "date": "2026-10-01", "kind": "payment", "amount": "10", "mode": "cash"}},
			{Values: map[string]string{"flat": "1A", "date": "2026-10-01", "kind": "payment", "amount": "10"}},
			{Values: map[string]string{"flat": "1A", "date": "2026-10-01", "kind": "invoice", "quarter": "Q3FY27", "amount": "10"}},
			{Values: map[string]string{"flat": "1A", "date": "2026-12-01", "kind": "late_fee", "quarter": "Q3FY27", "amount": "1000"}},
			edit(inv, "kind", "adjustment"),
		},
		"bank": {{Values: map[string]string{"link": "ignore"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"changed in the portal", "not a number", "still has", "does not exist", "mode is required", "already has an invoice", "calculated automatically", "can't change", "cannot be added"}
	if res.Errors != len(want) {
		for _, c := range res.Changes {
			t.Logf("%s %s: %s", c.Sheet, c.Label, c.Error)
		}
		t.Fatalf("errors = %d, want %d", res.Errors, len(want))
	}
	var all []string
	for _, c := range res.Changes {
		all = append(all, c.Error)
	}
	for _, w := range want {
		if !strings.Contains(strings.Join(all, "|"), w) {
			t.Errorf("no error mentions %q: %q", w, all)
		}
	}
	if _, err := a.ApplyChanges(Changeset{"flats": {edit(old, "notes", "x")}}); err == nil {
		t.Error("apply with errors should fail")
	}
	if f, _ := a.Flat("1A"); f.Owner != "Asha R" || f.Notes != "" {
		t.Errorf("1A changed despite error: %+v", f)
	}

	// an unedited stale row is left alone (the portal change wins)
	res, _ = a.PlanChanges(Changeset{"flats": {edit(old)}})
	if len(res.Changes) != 0 || res.Unchanged != 1 {
		t.Errorf("stale unedited row: %+v", res)
	}
}

func TestBulkEditBankLinks(t *testing.T) {
	a := editFixture(t)
	a.SaveSettings(map[string]string{"cutover_date": "2026-01-01"})
	a.DB.Exec(`INSERT INTO bank_statements(id,filename,sha256) VALUES(1,'s','h')`)
	a.DB.Exec(`INSERT INTO bank_txns(id,statement_id,date,narration,ref_no,credit,balance,status,payer_key) VALUES(7,1,'2026-10-06','UPI-BALA-1B','R1',2640000,0,'review','bala@okaxis')`)
	a.DB.Exec(`INSERT INTO bank_txns(id,statement_id,date,narration,ref_no,debit,balance,status,payer_key) VALUES(8,1,'2026-10-07','NEFT-BESCOM','R2',500000,0,'review','acct:9999')`)
	a.DB.Exec(`INSERT INTO bank_txns(id,statement_id,date,narration,ref_no,credit,balance,status,payer_key) VALUES(9,1,'2026-10-08','UPI-ASHA','R3',100000,0,'review','asha@ybl')`)
	bank := rowsOf(t, a, "bank")
	exp := find(rowsOf(t, a, "expenses"), "description", "Bescom bill")

	mustApply(t, a, Changeset{"bank": {
		edit(find(bank, "id", "7"), "link", "maintenance", "flats", "1A=12000; 1B=14400"),
		edit(find(bank, "id", "8"), "link", "expense", "payee", "Bescom", "category", "Electricity", "expense", exp.Values["id"]),
	}})
	for code, want := range map[string]int64{"1A": -1200000, "1B": 0} {
		if f, _ := a.Flat(code); f.Balance != want {
			t.Errorf("%s balance %d, want %d", code, f.Balance, want)
		}
	}
	bank = rowsOf(t, a, "bank")
	if r := find(bank, "id", "7"); r.Values["flats"] != "1A=12000; 1B=14400" || r.Values["status"] != "matched" {
		t.Errorf("txn 7: %+v", r.Values)
	}
	if r := find(bank, "id", "8"); r.Values["expense"] != exp.Values["id"] {
		t.Errorf("txn 8: %+v", r.Values)
	}
	// bank-posted ledger rows are locked
	for _, r := range rowsOf(t, a, "ledger") {
		if r.Values["bank_txn"] == "7" && !strings.Contains(r.Locked, "Bank links") {
			t.Errorf("bank payment not locked: %+v", r)
		}
	}
	// links made in the sheet are remembered, so the next statement auto-matches
	mustApply(t, a, Changeset{"bank": {edit(find(bank, "id", "9"), "link", "maintenance", "flats", "1B")}})
	a.DB.Exec(`INSERT INTO bank_txns(id,statement_id,date,narration,ref_no,credit,balance,status,payer_key) VALUES(10,1,'2026-11-05','UPI-ASHA','R4',150000,0,'review','asha@ybl')`)
	a.DB.Exec(`INSERT INTO bank_txns(id,statement_id,date,narration,ref_no,debit,balance,status,payer_key) VALUES(11,1,'2026-11-07','NEFT-XYZ','R5',420000,0,'review','acct:9999')`)
	for _, id := range []int64{10, 11} {
		if err := a.autoMatch(id); err != nil {
			t.Fatal(err)
		}
	}
	if x, _ := a.txn(10); x.Status != "matched" || x.Flat == nil || *x.Flat != "1B" {
		t.Errorf("next credit from remembered payer: %s %v", x.Status, x.Flat)
	}
	if x, _ := a.txn(11); x.Status != "matched" || x.Payee != "Bescom" || x.Category != "Electricity" {
		t.Errorf("next debit to remembered payee: %s %q %q", x.Status, x.Payee, x.Category)
	}
	if l, _ := a.Ledger("1B"); l[len(l)-1].BankTxnID == nil || *l[len(l)-1].BankTxnID != 10 {
		t.Errorf("auto-matched credit not posted to 1B")
	}
	bank = rowsOf(t, a, "bank")

	res, _ := a.PlanChanges(Changeset{"bank": {edit(find(bank, "id", "7"), "flats", "1A=100")}})
	if res.Errors != 1 || !strings.Contains(res.Changes[0].Error, "add up to") {
		t.Errorf("split mismatch: %+v", res.Changes)
	}
}

func TestWorkbookUploadEdits(t *testing.T) {
	a := editFixture(t)
	var buf bytes.Buffer
	if err := a.WriteWorkbook(&buf); err != nil {
		t.Fatal(err)
	}
	x, err := excelize.OpenReader(&buf)
	if err != nil {
		t.Fatal(err)
	}
	cell := func(tab, header string, row int) string {
		rows, _ := x.GetRows(tab)
		for i, h := range rows[0] {
			if h == header {
				n, _ := excelize.CoordinatesToCellName(i+1, row)
				return n
			}
		}
		t.Fatalf("no %s column on %s", header, tab)
		return ""
	}
	// Flats row 2 is 1A: rename owner. Delete the expense. Add a cash payment as a real Excel date.
	x.SetCellStr("Flats", cell("Flats", "Owner", 2), "Asha Rao")
	x.SetCellStr("Expenses", cell("Expenses", "Delete", 2), "Yes")
	rows, _ := x.GetRows("Dues & payments")
	nr := len(rows) + 1
	x.SetCellStr("Dues & payments", cell("Dues & payments", "Flat", nr), "1b")
	x.SetCellValue("Dues & payments", cell("Dues & payments", "Date", nr), 46301) // 2026-10-06 as an Excel serial
	x.SetCellStr("Dues & payments", cell("Dues & payments", "Type", nr), "payment")
	x.SetCellValue("Dues & payments", cell("Dues & payments", "Amount ₹", nr), 14400)
	x.SetCellStr("Dues & payments", cell("Dues & payments", "Mode", nr), "cash")
	var out bytes.Buffer
	x.Write(&out)

	cs, err := a.ReadWorkbook(&out)
	if err != nil {
		t.Fatal(err)
	}
	res := mustApply(t, a, cs)
	if res.Applied != 3 {
		var s []string
		for _, c := range res.Changes {
			s = append(s, fmt.Sprintf("%s %s %s", c.Sheet, c.Action, c.Label))
		}
		t.Fatalf("applied %d: %v", res.Applied, s)
	}
	if f, _ := a.Flat("1A"); f.Owner != "Asha Rao" {
		t.Errorf("owner %q", f.Owner)
	}
	if f, _ := a.Flat("1B"); f.Balance != 0 {
		t.Errorf("1B balance %d", f.Balance)
	}
	if es, _ := a.Expenses(ExpenseFilter{}); len(es) != 0 {
		t.Errorf("expense not deleted")
	}
	l, _ := a.Ledger("1B")
	if last := l[len(l)-1]; last.Date != "2026-10-06" || last.Mode != "cash" {
		t.Errorf("payment %+v", last)
	}
}
