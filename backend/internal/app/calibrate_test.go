package app

import (
	"testing"

	"treasuredash/internal/bank"
)

func TestAutoCalibrateOnUpload(t *testing.T) {
	a := testApp(t)
	a.SaveSettings(map[string]string{"cutover_date": "2026-09-01"})
	a.UpsertFlat(Flat{Code: "1A", Owner: "Test Owner", AreaSqft: 1000})
	a.CreatePeriod("Q2FY27")
	a.RaiseInvoices("Q2FY27")

	tx := func(date, ref string, cr, bal int64) bank.Txn {
		return bank.Txn{Date: date, ValueDate: date, RefNo: ref, Credit: cr * 100, Balance: bal * 100, Narration: "UPI-TEST OWNER-1A MAINTENANCE"}
	}
	// ends before the quarter end (30 Sep): must not calibrate on a stale balance
	early := &bank.Statement{From: "2026-09-01", To: "2026-09-15", Opening: 50000000, Closing: 50500000, Txns: []bank.Txn{tx("2026-09-05", "R1", 5000, 505000)}}
	full := &bank.Statement{From: "2026-09-01", To: "2026-10-02", Opening: 50000000, Closing: 51200000, Txns: []bank.Txn{tx("2026-09-05", "R1", 5000, 505000), tx("2026-09-20", "R2", 7000, 512000)}}

	s, err := a.importParsed("early.pdf", "h1", early, nil)
	if err != nil {
		t.Fatal(err)
	}
	if s.Calibration != "" || a.Setting("sinking_fund_anchor") != "" {
		t.Fatalf("calibrated without a statement spanning the quarter end: %q", s.Calibration)
	}

	s, err = a.importParsed("full.pdf", "h2", full, nil)
	if err != nil {
		t.Fatal(err)
	}
	if s.Calibration == "" {
		t.Fatal("statement spanning 30 Sep should recalculate the opening")
	}
	if got := a.Setting("sinking_fund_anchor"); got != "2026-09-30" {
		t.Errorf("anchor %q", got)
	}
	p, _ := a.PnLFor("Q2FY27")
	if p.UnallocatedReserve != 0 {
		t.Errorf("fund should match the bank after calibration, off by %d", p.UnallocatedReserve)
	}
	opening := a.Setting("sinking_fund_opening")

	// the same period again: no further change
	s, err = a.importParsed("again.pdf", "h3", full, nil)
	if err != nil {
		t.Fatal(err)
	}
	if s.Calibration != "" || a.Setting("sinking_fund_opening") != opening {
		t.Errorf("re-upload recalculated: %q", s.Calibration)
	}

	// switched off: even an uncalibrated quarter end is left alone
	a.SaveSettings(map[string]string{"sinking_fund_auto": "0", "sinking_fund_anchor": "", "sinking_fund_opening": "0"})
	if msg, _ := a.AutoCalibrateSinkingFund(); msg != "" || a.Setting("sinking_fund_opening") != "0" {
		t.Errorf("ran while switched off: %q", msg)
	}
}
