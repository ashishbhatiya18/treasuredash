package server

import (
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"treasuredash/internal/app"
)

func call(t *testing.T, s *Server, method, url, body string) (int, string) {
	t.Helper()
	w := httptest.NewRecorder()
	s.Routes().ServeHTTP(w, httptest.NewRequest(method, url, strings.NewReader(body)))
	return w.Code, w.Body.String()
}

// The expense and income forms send their state back as-is, including the
// paise "amount" field as text; amount_rupees must win.
func TestFormsSaveWithTextAmount(t *testing.T) {
	a, err := app.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := New(a)
	id, _ := a.SaveExpense(app.Expense{Date: "2026-10-01", Description: "Bescom", Amount: 50000})

	if code, body := call(t, s, "PUT", "/api/expenses/1",
		`{"id":1,"date":"2026-10-02","description":"Bescom bill","category":"","payee":"Bescom","amount":"500","notes":"","bill_file":"","bank_txn_id":null,"amount_rupees":612.5}`); code != 200 {
		t.Fatalf("edit expense: %d %s", code, body)
	}
	es, _ := a.Expenses(app.ExpenseFilter{})
	if len(es) != 1 || es[0].ID != id || es[0].Amount != 61250 || es[0].Date != "2026-10-02" {
		t.Errorf("expense after edit: %+v", es)
	}
	if code, body := call(t, s, "POST", "/api/expenses", `{"date":"2026-10-03","description":"Diesel","amount":"","amount_rupees":900}`); code != 200 {
		t.Fatalf("add expense: %d %s", code, body)
	}
	if code, body := call(t, s, "POST", "/api/income", `{"date":"2026-10-03","category":"Other","description":"x","amount":"250","flat":null,"amount_rupees":250}`); code != 200 {
		t.Fatalf("add income: %d %s", code, body)
	}
}

func TestEditPayment(t *testing.T) {
	a, err := app.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := New(a)
	a.UpsertFlat(app.Flat{Code: "1A", Owner: "Asha", AreaSqft: 1000})
	if err := a.AddPayment("1A", "2026-10-05", 1000000, "cash", ""); err != nil {
		t.Fatal(err)
	}
	if err := a.AddAdjustment("1A", "2026-10-06", -50000, "Waiver"); err != nil {
		t.Fatal(err)
	}
	l, _ := a.Ledger("1A")
	pay, adj := l[0], l[1]

	if code, body := call(t, s, "PUT", "/api/ledger/"+itoa(pay.ID), `{"date":"2026-10-04","amount":12000,"mode":"gpay_personal","description":"GPay ref 99"}`); code != 200 {
		t.Fatalf("edit payment: %d %s", code, body)
	}
	if code, body := call(t, s, "PUT", "/api/ledger/"+itoa(adj.ID), `{"date":"2026-10-06","amount":-750,"description":"Late fee waived"}`); code != 200 {
		t.Fatalf("edit adjustment: %d %s", code, body)
	}
	l, _ = a.Ledger("1A")
	if p := l[0]; p.Amount != -1200000 || p.Date != "2026-10-04" || p.Mode != "gpay_personal" || p.Description != "GPay ref 99" {
		t.Errorf("payment after edit: %+v", p)
	}
	if j := l[1]; j.Amount != -75000 || j.Description != "Late fee waived" {
		t.Errorf("adjustment after edit: %+v", j)
	}
	if code, _ := call(t, s, "PUT", "/api/ledger/"+itoa(pay.ID), `{"date":"2026-10-04","amount":0,"mode":"cash"}`); code != 400 {
		t.Errorf("zero payment accepted: %d", code)
	}

	// bank-posted payments are changed through the bank page
	a.DB.Exec(`INSERT INTO bank_statements(id,filename,sha256) VALUES(1,'s','h')`)
	a.DB.Exec(`INSERT INTO bank_txns(id,statement_id,date,narration,ref_no,credit,balance) VALUES(5,1,'2026-10-07','UPI','R',100,0)`)
	bid, _ := a.DB.InsertID(`INSERT INTO ledger(flat,date,kind,amount,mode,bank_txn_id) VALUES('1A','2026-10-07','payment',-100,'bank',5)`)
	if code, body := call(t, s, "PUT", "/api/ledger/"+itoa(bid), `{"date":"2026-10-07","amount":2,"mode":"bank"}`); code != 400 || !strings.Contains(body, "Bank reconciliation") {
		t.Errorf("bank payment edit: %d %s", code, body)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
