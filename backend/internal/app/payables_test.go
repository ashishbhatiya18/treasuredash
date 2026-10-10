package app

import "testing"

// Bank payments to a payee settle that payee's oldest bills first; the rest are
// itemised as still payable, and deductions reduce the amount.
func TestPayablesItemisedOldestFirst(t *testing.T) {
	a := testApp(t)
	a.SaveSettings(map[string]string{"cutover_date": "2026-10-01"})
	for _, e := range []Expense{
		{Date: "2026-10-01", Description: "Housekeeping Bill", Payee: "Caretaker", Amount: 10000000},
		{Date: "2026-10-03", Description: "Diesel for DG", Payee: "Caretaker", Amount: 5000000},
		{Date: "2026-10-04", Description: "Payment on hold", Payee: "Caretaker", Amount: -1000000},
		{Date: "2026-09-20", Description: "Before cut-over", Payee: "Caretaker", Amount: 999900},
	} {
		if _, err := a.SaveExpense(e); err != nil {
			t.Fatal(err)
		}
	}
	a.DB.Exec(`INSERT INTO bank_statements(id,filename,sha256) VALUES(1,'s','h')`)
	a.DB.Exec(`INSERT INTO bank_txns(statement_id,date,narration,ref_no,debit,balance,status,kind,payee) VALUES(1,'2026-10-05','TPT-CARETAKER','R1',12000000,0,'matched','expense','Caretaker')`)
	p, err := a.PayablesDetail()
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Payees) != 1 {
		t.Fatalf("payees: %+v", p.Payees)
	}
	s := p.Payees[0]
	// billed 1,00,000 + 50,000 − 10,000 = 1,40,000; paid 1,20,000 → 20,000 to pay
	if s.Balance != 2000000 || p.Total != 2000000 {
		t.Fatalf("balance %d total %d", s.Balance, p.Total)
	}
	// housekeeping fully settled; diesel 30,000 left; deduction −10,000 listed
	if len(s.Items) != 2 || s.Items[0].Description != "Diesel for DG" || s.Items[0].Outstanding != 3000000 || s.Items[1].Outstanding != -1000000 {
		t.Fatalf("items: %+v", s.Items)
	}
	if s.Complete {
		t.Fatal("payee without bank details must not be complete")
	}
	a.SavePayee(Payee{Name: "Caretaker", Method: "NEFT", AccountNo: "123", IFSC: "hdfc0005493"})
	if !a.Payee("Caretaker").Complete() || a.Payee("Caretaker").IFSC != "HDFC0005493" {
		t.Fatal("payee details not saved / normalised")
	}
}
