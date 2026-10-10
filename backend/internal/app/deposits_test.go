package app

import "testing"

func TestDepositTransferDetection(t *testing.T) {
	yes := []string{"TO FD 50100XXXX", "SWEEP TRF TO 5010", "RD INSTALLMENT-123", "FD CLOSURE PROCEEDS", "TD BOOKING"}
	no := []string{"NEFT CR-FDRL0001234-JOHN", "3RD FLOOR PAINT", "UPI-HDFC0001230-4F Q2 MAINTENANCE", "LTD"}
	for _, s := range yes {
		if !isDepositTransfer(s) {
			t.Errorf("expected deposit transfer: %q", s)
		}
	}
	for _, s := range no {
		if isDepositTransfer(s) {
			t.Errorf("false positive: %q", s)
		}
	}
	d := Deposit{Kind: "RD", OpenedOn: "2026-04-10", Installment: 500000, TenureMonths: 12}
	d.valueOn("2026-07-09")
	if d.Value != 3*500000 {
		t.Errorf("RD value %d", d.Value)
	}
	d.valueOn("2028-01-01")
	if d.Value != 12*500000 {
		t.Errorf("RD capped value %d", d.Value)
	}
}

func TestFDFundedFromSavings(t *testing.T) {
	a := testApp(t)
	a.DB.Exec(`INSERT INTO bank_statements(id,filename,sha256,from_date,to_date,opening,closing) VALUES(1,'s','h','2026-10-01','2026-10-31',100000000,100000000)`)
	a.DB.Exec(`INSERT INTO bank_txns(statement_id,date,narration,ref_no,credit,balance,status,kind) VALUES(1,'2026-10-02','UPI-X','R1',0,100000000,'matched','other_income')`)
	savings := func(on string) int64 { b, _, _ := a.BankBalanceOn(on); return b - a.PendingDepositTransfers(on) }
	total := func(on string) int64 { return savings(on) + a.DepositsValueOn(on) }

	before := total("2026-10-20")
	id, err := a.SaveDeposit(Deposit{Kind: "FD", Number: "FD1", OpenedOn: "2026-10-15", Principal: 50000000, TenureMonths: 12, FromSavings: true})
	if err != nil {
		t.Fatal(err)
	}
	if s := savings("2026-10-20"); s != 50000000 {
		t.Fatalf("savings should dip to ₹5L before the statement shows the transfer, got %d", s)
	}
	if total("2026-10-20") != before {
		t.Fatalf("total funds must not change when money moves into an FD")
	}

	// the statement with the bank debit arrives: matched, not subtracted twice
	a.DB.Exec(`INSERT INTO bank_txns(id,statement_id,date,narration,ref_no,debit,balance,status) VALUES(99,1,'2026-10-16','TO FD 123','R2',50000000,50000000,'review')`)
	if err := a.MatchFundings(); err != nil {
		t.Fatal(err)
	}
	if s := savings("2026-10-20"); s != 50000000 {
		t.Fatalf("after matching the bank debit savings should still be ₹5L, got %d", s)
	}
	var kind, payee string
	a.DB.QueryRow(`SELECT kind,payee FROM bank_txns WHERE id=99`).Scan(&kind, &payee)
	if kind != "transfer" || payee != "FD/RD" {
		t.Fatalf("bank debit should become an FD/RD transfer, got %s/%s", kind, payee)
	}

	// close with interest credited to savings
	closed := "2027-10-15"
	if _, err := a.SaveDeposit(Deposit{ID: id, Kind: "FD", Number: "FD1", OpenedOn: "2026-10-15", Principal: 50000000, TenureMonths: 12,
		FromSavings: true, ToSavings: true, ClosedOn: &closed, ClosingAmount: 53500000}); err != nil {
		t.Fatal(err)
	}
	if s := savings("2027-10-20"); s != 50000000+53500000 {
		t.Fatalf("closing proceeds should return to savings, got %d", s)
	}
	var interest int64
	a.DB.QueryRow(`SELECT COALESCE(SUM(amount),0) FROM other_income WHERE category='FD/RD interest'`).Scan(&interest)
	if interest != 3500000 {
		t.Fatalf("interest income should be ₹35,000, got %d", interest)
	}
	if err := a.DeleteDeposit(id); err != nil {
		t.Fatal(err)
	}
	a.DB.QueryRow(`SELECT COUNT(*) FROM other_income WHERE category='FD/RD interest'`).Scan(&interest)
	if interest != 0 {
		t.Fatal("deleting the deposit should remove its interest income")
	}
}

// FDs booked on the bank statement land in the register (once), and FD/RD
// transfers the register doesn't know about still count in total funds.
func TestDepositsFromStatement(t *testing.T) {
	a := testApp(t)
	a.DB.Exec(`INSERT INTO bank_statements(id,filename,sha256) VALUES(1,'s','h')`)
	a.DB.Exec(`INSERT INTO bank_txns(statement_id,date,narration,ref_no,debit,balance,status,kind,payee)
		VALUES(1,'2026-10-03','FD BOOKED - 50301428040167 : ASSOCIATION','R1',50000000,0,'matched','transfer','FD/RD')`)
	a.DB.Exec(`INSERT INTO bank_txns(statement_id,date,narration,ref_no,debit,balance,status,kind,payee)
		VALUES(1,'2026-10-04','SWEEP TRF TO 5030','R2',10000000,0,'matched','transfer','FD/RD')`)

	if v := a.DepositsValueOn("2026-10-05"); v != 60000000 {
		t.Fatalf("before registering, unregistered transfers must still count: %d", v)
	}
	n, err := a.RegisterBookedDeposits()
	if err != nil || n != 1 {
		t.Fatalf("registered %d (%v)", n, err)
	}
	if n, _ := a.RegisterBookedDeposits(); n != 0 {
		t.Fatalf("second run registered %d again", n)
	}
	ds, _ := a.Deposits("2026-10-05")
	if len(ds) != 1 || ds[0].Number != "50301428040167" || ds[0].Principal != 50000000 || ds[0].OpenedOn != "2026-10-03" {
		t.Fatalf("deposit: %+v", ds)
	}
	if len(ds[0].Fundings) != 1 || ds[0].Fundings[0].BankTxnID == nil {
		t.Fatalf("FD should be linked to its bank line: %+v", ds[0].Fundings)
	}
	// ₹5L from the register + ₹1L sweep not in the register; nothing counted twice
	if v := a.DepositsValueOn("2026-10-05"); v != 60000000 {
		t.Fatalf("total FD/RD %d", v)
	}
	if p := a.PendingDepositTransfers("2026-10-05"); p != 0 {
		t.Fatalf("matched FD must not be pending: %d", p)
	}
}
