package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestEndToEndWithSampleData(t *testing.T) {
	matches, _ := filepath.Glob("../../../data/*.xlsx")
	if len(matches) == 0 {
		t.Skip("no sample workbook in data/")
	}
	xf, err := os.Open(matches[0])
	if err != nil {
		t.Skip("sample workbook not present")
	}
	defer xf.Close()
	a := testApp(t)
	rep, err := a.ImportWorkbook(xf)
	if err != nil {
		t.Fatal(err)
	}
	j, _ := json.MarshalIndent(rep, "", " ")
	t.Logf("import: %s", j)

	pdf, _ := os.ReadFile("../../../data/Acct_Statement_XXXXXXXX3893_03102026.pdf")
	st, err := a.ImportStatement("sep.pdf", pdf)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("statement: %+v", *st)
	txns, _ := a.BankTxns(TxnFilter{})
	for _, x := range txns {
		fl := "-"
		if x.Flat != nil {
			fl = *x.Flat
		}
		t.Logf("%s %-10s %-3s %-22s cr=%7d dr=%7d %.50s", x.Date, x.Status, fl, x.Payee, x.Credit/100, x.Debit/100, x.Narration)
	}
	if _, err := a.ImportStatement("sep.pdf", pdf); err == nil {
		t.Fatal("expected duplicate import to fail")
	}

	if _, err := a.CreatePeriod("Q3FY27"); err != nil {
		t.Fatal(err)
	}
	n, err := a.RaiseInvoices("Q3FY27")
	if err != nil || n != rep.Flats {
		t.Fatalf("raised %d err %v", n, err)
	}
	inv, err := a.Invoice("3B", "Q3FY27")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("invoice 3B: %+v", *inv)
	f, _ := os.Create(t.TempDir() + "/inv.pdf")
	if err := a.InvoicePDF(f, inv); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"Q1FY27", "Q2FY27", "Q3FY27"} {
		p, err := a.PnLFor(id)
		if err != nil {
			t.Fatal(err)
		}
		j, _ := json.Marshal(p)
		t.Logf("pnl %s", j)
	}
	sf, _ := a.SinkingFundReport()
	j, _ = json.Marshal(sf.Years)
	t.Logf("sinking years %s current %+v", j, sf.Current)
	// allocation must agree with the ledger for every flat
	flats, _ := a.Flats()
	for _, f := range flats {
		acc, err := a.FlatAccount(f.Code)
		if err != nil {
			t.Fatal(err)
		}
		if acc.Summary.Balance != f.Balance {
			t.Errorf("%s: allocation balance %d != ledger balance %d", f.Code, acc.Summary.Balance, f.Balance)
		}
	}
	acc, _ := a.FlatAccount("8D")
	for _, c := range acc.Charges[:3] {
		t.Logf("8D charge %-28s amt=%d settled=%d out=%d %s via %d settlements", c.Label, c.Amount/100, c.Settled/100, c.Outstanding/100, c.Status, len(c.Settlements))
	}
	t.Logf("8D summary %+v, pre-cutover receipts %d", acc.Summary, len(acc.PreCutoverReceipt))
	sts, _ := a.Statements()
	for _, s := range sts {
		if !s.Balanced || len(s.Issues) > 0 {
			t.Errorf("statement %s not balanced: %v", s.Filename, s.Issues)
		}
	}
	txs, _ := a.BankTxns(TxnFilter{Q: "12600"})
	t.Logf("search 12600 → %d txns", len(txs))

	r, err := a.BuildReminder("Q3FY27")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("reminder:\n%s", r.Text)
}
