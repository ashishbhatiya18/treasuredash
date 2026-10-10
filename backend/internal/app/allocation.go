package app

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Payments and credits are applied to a flat's charges oldest-first (FIFO):
// opening dues, then invoices, late fees and positive adjustments in date order.
// This single allocation drives invoice status, receipts "applied to", and the
// actual-income figures in the P&L.

type Settlement struct {
	CreditID    int64  `json:"credit_id"`
	CreditKind  string `json:"credit_kind"` // payment / adjustment (waiver) / opening (advance b/f)
	CreditDate  string `json:"credit_date"`
	Date        string `json:"date"` // when it took effect: later of charge date and credit date
	Amount      int64  `json:"amount"`
	Mode        string `json:"mode"`
	Description string `json:"description"`
	BankTxnID   *int64 `json:"bank_txn_id"`
	BankRef     string `json:"bank_ref"`
}

type ChargeView struct {
	LedgerEntry
	Label       string       `json:"label"`
	DueDate     string       `json:"due_date"`
	Settled     int64        `json:"settled"`
	Outstanding int64        `json:"outstanding"`
	Status      string       `json:"status"` // paid / partial / unpaid / overdue
	DaysOverdue int          `json:"days_overdue"`
	Settlements []Settlement `json:"settlements"`
}

type Application struct {
	ChargeID int64  `json:"charge_id"`
	Label    string `json:"label"`
	PeriodID string `json:"period_id"`
	Amount   int64  `json:"amount"`
}

type CreditView struct {
	LedgerEntry
	Label         string        `json:"label"`
	Applied       []Application `json:"applied"`
	Unapplied     int64         `json:"unapplied"` // advance waiting for future charges
	BankRef       string        `json:"bank_ref"`
	BankNarration string        `json:"bank_narration"`
}

type AccountSummary struct {
	Charged     int64 `json:"charged"`
	Paid        int64 `json:"paid"`
	Waived      int64 `json:"waived"` // waivers / credit corrections / advances b/f
	Outstanding int64 `json:"outstanding"`
	Advance     int64 `json:"advance"`
	Balance     int64 `json:"balance"` // outstanding − advance
	Overdue     int64 `json:"overdue"`
}

type FlatAccount struct {
	Flat              Flat           `json:"flat"`
	Summary           AccountSummary `json:"summary"`
	Charges           []ChargeView   `json:"charges"`
	Credits           []CreditView   `json:"credits"`
	PreCutoverReceipt []BankTxn      `json:"pre_cutover_receipts"` // matched to this flat, already inside sheet balances
	Voids             []LedgerVoid   `json:"voids"`
	CutoverDate       string         `json:"cutover_date"`
}

func chargeLabel(e LedgerEntry) string {
	switch e.Kind {
	case "invoice":
		if e.PeriodID != nil {
			return "Maintenance " + *e.PeriodID
		}
	case "late_fee":
		if e.PeriodID != nil && e.Seq != nil {
			return fmt.Sprintf("Late fee %s (month %d)", *e.PeriodID, *e.Seq)
		}
		if e.PeriodID != nil {
			return "Late fee " + *e.PeriodID
		}
	case "opening":
		return "Dues brought forward"
	}
	return e.Description
}

func creditLabel(e LedgerEntry) string {
	switch e.Kind {
	case "payment":
		return "Payment"
	case "opening":
		return "Advance brought forward"
	}
	return "Waiver / credit adjustment"
}

type bankInfo struct{ ref, narration string }

func (a *App) bankInfoFor(ids []int64) (map[int64]bankInfo, error) {
	out := map[int64]bankInfo{}
	if len(ids) == 0 {
		return out, nil
	}
	q := `SELECT id,ref_no,narration FROM bank_txns WHERE id IN (?` + strings.Repeat(",?", len(ids)-1) + `)`
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := a.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var b bankInfo
		if err := rows.Scan(&id, &b.ref, &b.narration); err != nil {
			return nil, err
		}
		out[id] = b
	}
	return out, rows.Err()
}

func (a *App) dueDates() (map[string]string, error) {
	rows, err := a.DB.Query(`SELECT id,due_date FROM periods`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := map[string]string{}
	for rows.Next() {
		var id, d string
		if err := rows.Scan(&id, &d); err != nil {
			return nil, err
		}
		m[id] = d
	}
	return m, rows.Err()
}

// allocate runs the FIFO allocation for one flat's ledger.
func allocate(ledger []LedgerEntry, banks map[int64]bankInfo, dues map[string]string, today time.Time) ([]ChargeView, []CreditView, AccountSummary) {
	var charges []ChargeView
	var credits []CreditView
	for _, e := range ledger {
		if e.Amount > 0 {
			c := ChargeView{LedgerEntry: e, Label: chargeLabel(e), Outstanding: e.Amount, Settlements: []Settlement{}}
			if e.Kind == "invoice" && e.PeriodID != nil {
				c.DueDate = dues[*e.PeriodID]
			} else {
				c.DueDate = e.Date
			}
			charges = append(charges, c)
		} else if e.Amount < 0 {
			cv := CreditView{LedgerEntry: e, Label: creditLabel(e), Unapplied: -e.Amount, Applied: []Application{}}
			if e.BankTxnID != nil {
				b := banks[*e.BankTxnID]
				cv.BankRef, cv.BankNarration = b.ref, b.narration
			}
			credits = append(credits, cv)
		}
	}
	i, j := 0, 0
	for i < len(charges) && j < len(credits) {
		c, cr := &charges[i], &credits[j]
		n := min(c.Outstanding, cr.Unapplied)
		if n > 0 {
			pid := ""
			if c.PeriodID != nil {
				pid = *c.PeriodID
			}
			c.Settlements = append(c.Settlements, Settlement{
				CreditID: cr.ID, CreditKind: cr.Kind, CreditDate: cr.Date, Date: max(c.Date, cr.Date), Amount: n,
				Mode: cr.Mode, Description: cr.Description, BankTxnID: cr.BankTxnID, BankRef: cr.BankRef,
			})
			cr.Applied = append(cr.Applied, Application{ChargeID: c.ID, Label: c.Label, PeriodID: pid, Amount: n})
			c.Outstanding -= n
			c.Settled += n
			cr.Unapplied -= n
		}
		if c.Outstanding == 0 {
			i++
		}
		if cr.Unapplied == 0 {
			j++
		}
	}
	var s AccountSummary
	todayS := today.Format(DateFmt)
	for k := range charges {
		c := &charges[k]
		s.Charged += c.Amount
		s.Outstanding += c.Outstanding
		switch {
		case c.Outstanding == 0:
			c.Status = "paid"
		case c.DueDate != "" && todayS > c.DueDate:
			c.Status = "overdue"
			if d, err := ParseDate(c.DueDate); err == nil {
				c.DaysOverdue = int(today.Sub(d).Hours() / 24)
			}
			s.Overdue += c.Outstanding
		case c.Settled > 0:
			c.Status = "partial"
		default:
			c.Status = "unpaid"
		}
	}
	for _, cr := range credits {
		if cr.Kind == "payment" {
			s.Paid += -cr.Amount
		} else {
			s.Waived += -cr.Amount
		}
		s.Advance += cr.Unapplied
	}
	s.Balance = s.Outstanding - s.Advance
	if charges == nil {
		charges = []ChargeView{}
	}
	if credits == nil {
		credits = []CreditView{}
	}
	return charges, credits, s
}

// allocateFlat loads one flat's ledger and allocates it as of today.
func (a *App) allocateFlat(code string, dues map[string]string) ([]ChargeView, []CreditView, AccountSummary, error) {
	return a.allocateFlatAt(code, dues, Today())
}

// allocateFlatAt allocates only the entries dated on or before `at`, with
// overdue status judged on that date (used for past-quarter views).
func (a *App) allocateFlatAt(code string, dues map[string]string, at time.Time) ([]ChargeView, []CreditView, AccountSummary, error) {
	all, err := a.Ledger(code)
	if err != nil {
		return nil, nil, AccountSummary{}, err
	}
	on := at.Format(DateFmt)
	var ledger []LedgerEntry
	for _, e := range all {
		if e.Date <= on {
			ledger = append(ledger, e)
		}
	}
	var ids []int64
	for _, e := range ledger {
		if e.BankTxnID != nil {
			ids = append(ids, *e.BankTxnID)
		}
	}
	banks, err := a.bankInfoFor(ids)
	if err != nil {
		return nil, nil, AccountSummary{}, err
	}
	ch, cr, s := allocate(ledger, banks, dues, at)
	return ch, cr, s, nil
}

func (a *App) FlatAccount(code string) (*FlatAccount, error) {
	f, err := a.Flat(code)
	if err != nil {
		return nil, err
	}
	dues, err := a.dueDates()
	if err != nil {
		return nil, err
	}
	ch, cr, s, err := a.allocateFlat(code, dues)
	if err != nil {
		return nil, err
	}
	// newest first reads best on screen
	sort.SliceStable(ch, func(i, j int) bool { return ch[i].Date > ch[j].Date })
	sort.SliceStable(cr, func(i, j int) bool { return cr[i].Date > cr[j].Date })
	acc := &FlatAccount{Flat: f, Summary: s, Charges: ch, Credits: cr, CutoverDate: a.Setting("cutover_date")}
	if acc.Voids, err = a.Voids(code); err != nil {
		return nil, err
	}
	all, err := a.BankTxns(TxnFilter{Status: "historical", Flat: code})
	if err != nil {
		return nil, err
	}
	acc.PreCutoverReceipt = []BankTxn{}
	for _, t := range all {
		if t.Credit > 0 && t.Kind == "maintenance" {
			acc.PreCutoverReceipt = append(acc.PreCutoverReceipt, t)
		}
	}
	return acc, nil
}

// recognitions: portions of charges settled by real payments, dated when they
// took effect. Waivers and advances brought forward are not income.
func (a *App) recognitions() ([]recognition, error) {
	flats, err := a.Flats()
	if err != nil {
		return nil, err
	}
	dues, err := a.dueDates()
	if err != nil {
		return nil, err
	}
	var out []recognition
	for _, f := range flats {
		ch, _, _, err := a.allocateFlat(f.Code, dues)
		if err != nil {
			return nil, err
		}
		for _, c := range ch {
			pid := ""
			if c.PeriodID != nil {
				pid = *c.PeriodID
			}
			for _, s := range c.Settlements {
				if s.CreditKind == "payment" {
					out = append(out, recognition{c.Kind, pid, s.Date, s.Amount})
				}
			}
		}
	}
	return out, nil
}

// ---- receipts register ---------------------------------------------------------

type Receipt struct {
	CreditView
	Flat  string `json:"flat"`
	Owner string `json:"owner"`
}

type ReceiptFilter struct{ From, To, Flat, Mode, Period string }

// Receipts lists every payment received across flats with what it settled.
func (a *App) Receipts(f ReceiptFilter) ([]Receipt, error) {
	flats, err := a.Flats()
	if err != nil {
		return nil, err
	}
	dues, err := a.dueDates()
	if err != nil {
		return nil, err
	}
	out := []Receipt{}
	for _, fl := range flats {
		if f.Flat != "" && fl.Code != f.Flat {
			continue
		}
		_, cr, _, err := a.allocateFlat(fl.Code, dues)
		if err != nil {
			return nil, err
		}
		for _, c := range cr {
			if c.Kind != "payment" || (f.From != "" && c.Date < f.From) || (f.To != "" && c.Date > f.To) || (f.Mode != "" && c.Mode != f.Mode) {
				continue
			}
			if f.Period != "" {
				hit := false
				for _, ap := range c.Applied {
					hit = hit || ap.PeriodID == f.Period
				}
				if !hit {
					continue
				}
			}
			out = append(out, Receipt{CreditView: c, Flat: fl.Code, Owner: fl.Owner})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Date != out[j].Date {
			return out[i].Date > out[j].Date
		}
		return out[i].Flat < out[j].Flat
	})
	return out, nil
}

// ---- bank txn postings ------------------------------------------------------------

type Posting struct {
	Flat      string        `json:"flat"`
	Amount    int64         `json:"amount"`
	Applied   []Application `json:"applied"`
	Unapplied int64         `json:"unapplied"`
}

// AttachPostings fills, for maintenance receipts, which flats were credited and
// which charges each credit settled.
func (a *App) AttachPostings(ts []BankTxn) error {
	if len(ts) == 0 {
		return nil
	}
	rows, err := a.DB.Query(`SELECT id,flat,bank_txn_id FROM ledger WHERE kind='payment' AND bank_txn_id IS NOT NULL`)
	if err != nil {
		return err
	}
	byTxn := map[int64][]struct {
		ledgerID int64
		flat     string
	}{}
	need := map[string]bool{}
	for rows.Next() {
		var lid, tid int64
		var fl string
		if err := rows.Scan(&lid, &fl, &tid); err != nil {
			rows.Close()
			return err
		}
		byTxn[tid] = append(byTxn[tid], struct {
			ledgerID int64
			flat     string
		}{lid, fl})
	}
	rows.Close()
	for i := range ts {
		for _, p := range byTxn[ts[i].ID] {
			need[p.flat] = true
		}
	}
	dues, err := a.dueDates()
	if err != nil {
		return err
	}
	credit := map[int64]CreditView{}
	for fl := range need {
		_, cr, _, err := a.allocateFlat(fl, dues)
		if err != nil {
			return err
		}
		for _, c := range cr {
			credit[c.ID] = c
		}
	}
	for i := range ts {
		ts[i].Postings = []Posting{}
		for _, p := range byTxn[ts[i].ID] {
			c := credit[p.ledgerID]
			ts[i].Postings = append(ts[i].Postings, Posting{Flat: p.flat, Amount: -c.Amount, Applied: c.Applied, Unapplied: c.Unapplied})
		}
	}
	return nil
}
