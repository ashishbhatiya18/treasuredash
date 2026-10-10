package app

import (
	"database/sql"
	"fmt"
	"math"
	"strings"
	"time"
)

type Period struct {
	ID          string  `json:"id"`
	FY          int     `json:"fy"`
	Quarter     int     `json:"quarter"`
	StartDate   string  `json:"start_date"`
	EndDate     string  `json:"end_date"`
	DueDate     string  `json:"due_date"`
	RatePerSqft float64 `json:"rate_per_sqft"`
	LateFee     int64   `json:"late_fee"`
	AutoLateFee bool    `json:"auto_late_fee"`
	Status      string  `json:"status"`
	InvoicedAt  *string `json:"invoiced_at"`
	// computed
	Billed      int64 `json:"billed"`
	Collected   int64 `json:"collected"` // payments dated inside the quarter (cash view)
	Outstanding int64 `json:"outstanding"`
	UnpaidFlats int   `json:"unpaid_flats"`
}

func (a *App) Periods() ([]Period, error) {
	rows, err := a.DB.Query(`SELECT id,fy,quarter,start_date,end_date,due_date,rate_per_sqft,late_fee,auto_late_fee,status,invoiced_at
		FROM periods ORDER BY start_date DESC`)
	if err != nil {
		return nil, err
	}
	var out []Period
	for rows.Next() {
		var p Period
		var inv sql.NullString
		if err := rows.Scan(&p.ID, &p.FY, &p.Quarter, &p.StartDate, &p.EndDate, &p.DueDate, &p.RatePerSqft, &p.LateFee, &p.AutoLateFee, &p.Status, &inv); err != nil {
			rows.Close()
			return nil, err
		}
		if inv.Valid {
			p.InvoicedAt = &inv.String
		}
		out = append(out, p)
	}
	rows.Close()
	for i := range out {
		p := &out[i]
		a.DB.QueryRow(`SELECT COALESCE(SUM(amount),0) FROM ledger WHERE period_id=? AND kind='invoice'`, p.ID).Scan(&p.Billed)
		a.DB.QueryRow(`SELECT COALESCE(-SUM(amount),0) FROM ledger WHERE kind='payment' AND date BETWEEN ? AND ?`, p.StartDate, p.EndDate).Scan(&p.Collected)
		st, err := a.PeriodStatus(p.ID, Today())
		if err != nil {
			return nil, err
		}
		for _, s := range st {
			p.Outstanding += s.Outstanding
			if s.Outstanding > 0 {
				p.UnpaidFlats++
			}
		}
	}
	return out, nil
}

func (a *App) Period(id string) (Period, error) {
	ps, err := a.Periods()
	if err != nil {
		return Period{}, err
	}
	for _, p := range ps {
		if p.ID == id {
			return p, nil
		}
	}
	return Period{}, fmt.Errorf("period %s not found", id)
}

// CreatePeriod opens a quarter using the current rate and late-fee settings.
func (a *App) CreatePeriod(id string) (Period, error) {
	q, err := ParseQuarter(id)
	if err != nil {
		return Period{}, err
	}
	_, err = a.DB.Exec(`INSERT INTO periods(id,fy,quarter,start_date,end_date,due_date,rate_per_sqft,late_fee,auto_late_fee,status)
		VALUES(?,?,?,?,?,?,?,?,1,'open')`,
		q.ID(), q.FY, q.Q, q.Start().Format(DateFmt), q.End().Format(DateFmt), q.DefaultDue().Format(DateFmt),
		a.SettingFloat("rate_per_sqft"), Paise(a.SettingFloat("late_fee")))
	if err != nil {
		return Period{}, fmt.Errorf("create %s: %w", id, err)
	}
	return a.Period(q.ID())
}

func (a *App) UpdatePeriod(id, dueDate string, rate float64, lateFee int64, status string) error {
	if _, err := ParseDate(dueDate); err != nil {
		return fmt.Errorf("due date must be YYYY-MM-DD")
	}
	if status != "open" && status != "closed" {
		return fmt.Errorf("status must be open or closed")
	}
	var invoiced sql.NullString
	a.DB.QueryRow(`SELECT invoiced_at FROM periods WHERE id=?`, id).Scan(&invoiced)
	var curRate float64
	a.DB.QueryRow(`SELECT rate_per_sqft FROM periods WHERE id=?`, id).Scan(&curRate)
	if invoiced.Valid && rate != curRate {
		return fmt.Errorf("invoices already raised for %s; rate cannot change (post an adjustment instead)", id)
	}
	_, err := a.DB.Exec(`UPDATE periods SET due_date=?, rate_per_sqft=?, late_fee=?, status=? WHERE id=?`, dueDate, rate, lateFee, status, id)
	if err != nil {
		return err
	}
	_, err = a.ApplyLateFees(Today())
	return err
}

// QuarterlyCharge is area × monthly rate × 3 months.
func QuarterlyCharge(area, rate float64) int64 { return Paise(math.Round(area * rate * 3)) }

// RaiseInvoices posts one invoice per flat for the period. Safe to re-run:
// flats that already have an invoice are skipped.
func (a *App) RaiseInvoices(periodID string) (int, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	p, err := a.Period(periodID)
	if err != nil {
		return 0, err
	}
	flats, err := a.Flats()
	if err != nil {
		return 0, err
	}
	tx, err := a.DB.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	n := 0
	for _, f := range flats {
		res, err := tx.Exec(`INSERT INTO ledger(flat,date,kind,period_id,seq,amount,description)
			VALUES(?,?, 'invoice', ?, 0, ?, ?) ON CONFLICT DO NOTHING`,
			f.Code, p.StartDate, p.ID, QuarterlyCharge(f.AreaSqft, p.RatePerSqft),
			fmt.Sprintf("Maintenance %s (%.0f sqft × ₹%g × 3)", p.ID, f.AreaSqft, p.RatePerSqft))
		if err != nil {
			return 0, err
		}
		c, _ := res.RowsAffected()
		n += int(c)
	}
	if _, err := tx.Exec(`UPDATE periods SET invoiced_at=COALESCE(invoiced_at, `+a.DB.Now()+`) WHERE id=?`, p.ID); err != nil {
		return 0, err
	}
	return n, tx.Commit()
}

// ---- ledger ----------------------------------------------------------------

type LedgerEntry struct {
	ID          int64   `json:"id"`
	Flat        string  `json:"flat"`
	Date        string  `json:"date"`
	Kind        string  `json:"kind"`
	PeriodID    *string `json:"period_id"`
	Seq         *int64  `json:"seq"`
	Amount      int64   `json:"amount"`
	Description string  `json:"description"`
	Mode        string  `json:"mode"`
	BankTxnID   *int64  `json:"bank_txn_id"`
	Source      string  `json:"source"`
	Balance     int64   `json:"balance"`
}

func (a *App) Ledger(flat string) ([]LedgerEntry, error) {
	rows, err := a.DB.Query(`SELECT id,flat,date,kind,period_id,seq,amount,description,mode,bank_txn_id,source
		FROM ledger WHERE flat=? ORDER BY date, CASE kind WHEN 'opening' THEN 0 WHEN 'invoice' THEN 1 WHEN 'late_fee' THEN 2 ELSE 3 END, id`, flat)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LedgerEntry{}
	var bal int64
	for rows.Next() {
		var e LedgerEntry
		var pid sql.NullString
		var seq, bt sql.NullInt64
		if err := rows.Scan(&e.ID, &e.Flat, &e.Date, &e.Kind, &pid, &seq, &e.Amount, &e.Description, &e.Mode, &bt, &e.Source); err != nil {
			return nil, err
		}
		if pid.Valid {
			e.PeriodID = &pid.String
		}
		if seq.Valid {
			e.Seq = &seq.Int64
		}
		if bt.Valid {
			e.BankTxnID = &bt.Int64
		}
		bal += e.Amount
		e.Balance = bal
		out = append(out, e)
	}
	return out, rows.Err()
}

var PaymentModes = []string{"bank", "gpay_personal", "cash", "via_caretaker", "cheque"}

func (a *App) AddPayment(flat, date string, amount int64, mode, desc string) error {
	if _, err := ParseDate(date); err != nil || amount <= 0 {
		return fmt.Errorf("valid date and positive amount required")
	}
	if desc == "" {
		desc = "Payment received"
	}
	_, err := a.DB.Exec(`INSERT INTO ledger(flat,date,kind,amount,description,mode) VALUES(?,?,'payment',?,?,?)`,
		flat, date, -amount, desc, mode)
	if err != nil {
		return err
	}
	_, err = a.ApplyLateFees(Today())
	return err
}

// AddAdjustment posts a signed correction: negative waives, positive charges.
func (a *App) AddAdjustment(flat, date string, amount int64, desc string) error {
	if _, err := ParseDate(date); err != nil || amount == 0 || desc == "" {
		return fmt.Errorf("date, non-zero amount and description required")
	}
	_, err := a.DB.Exec(`INSERT INTO ledger(flat,date,kind,amount,description) VALUES(?,?,'adjustment',?,?)`, flat, date, amount, desc)
	return err
}

// DeleteLedgerEntry removes manual payments/adjustments. Invoices and
// bank-linked payments are managed through their own workflows.
func (a *App) DeleteLedgerEntry(id int64) error {
	res, err := a.DB.Exec(`DELETE FROM ledger WHERE id=? AND kind IN ('payment','adjustment') AND bank_txn_id IS NULL`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("only manual payments and adjustments can be deleted")
	}
	_, err = a.ApplyLateFees(Today())
	return err
}

// UpdateLedgerEntry corrects a payment or adjustment recorded by hand (or
// imported from the sheet). Bank-posted payments are changed by re-assigning
// the bank transaction. amount follows AddPayment/AddAdjustment: positive for
// payments, signed for adjustments.
func (a *App) UpdateLedgerEntry(id int64, date string, amount int64, mode, desc string) error {
	var kind string
	var bt sql.NullInt64
	if err := a.DB.QueryRow(`SELECT kind,bank_txn_id FROM ledger WHERE id=?`, id).Scan(&kind, &bt); err != nil {
		return fmt.Errorf("entry %d not found", id)
	}
	if bt.Valid {
		return fmt.Errorf("this payment came from the bank statement – change it on the Bank reconciliation page")
	}
	if _, err := ParseDate(date); err != nil {
		return fmt.Errorf("valid date required")
	}
	switch kind {
	case "payment":
		if amount <= 0 {
			return fmt.Errorf("amount must be positive")
		}
		if desc == "" {
			desc = "Payment received"
		}
		amount = -amount
	case "adjustment":
		if amount == 0 || desc == "" {
			return fmt.Errorf("non-zero amount and description required")
		}
		mode = ""
	default:
		return fmt.Errorf("only payments and adjustments can be edited here")
	}
	if _, err := a.DB.Exec(`UPDATE ledger SET date=?, amount=?, mode=?, description=? WHERE id=?`, date, amount, mode, desc, id); err != nil {
		return err
	}
	_, err := a.ApplyLateFees(Today())
	return err
}

// ---- late fees ---------------------------------------------------------------

type entry struct {
	date   string
	kind   string
	period string
	amount int64
	id     int64
}

func (a *App) entries(flat string) ([]entry, error) {
	rows, err := a.DB.Query(`SELECT date,kind,COALESCE(period_id,''),amount,id FROM ledger WHERE flat=?
		ORDER BY date, CASE kind WHEN 'opening' THEN 0 WHEN 'invoice' THEN 1 WHEN 'late_fee' THEN 2 ELSE 3 END, id`, flat)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []entry
	for rows.Next() {
		var e entry
		if err := rows.Scan(&e.date, &e.kind, &e.period, &e.amount, &e.id); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// unpaidOn returns how much of a period's invoice is still unpaid at the end of
// day `on`, settling charges oldest-first (FIFO). Late fees of the same period
// are excluded so they never trigger further fees.
func unpaidOn(es []entry, periodID string, on string) int64 {
	var debitsThrough, invoice int64
	found := false
	for _, e := range es { // es is date-ordered, so this sums every charge up to and including the invoice
		if e.amount <= 0 || (e.kind == "late_fee" && e.period == periodID) {
			continue
		}
		debitsThrough += e.amount
		if e.kind == "invoice" && e.period == periodID {
			invoice, found = e.amount, true
			break
		}
	}
	if !found {
		return 0
	}
	var credits int64
	for _, e := range es {
		if e.amount < 0 && e.date <= on {
			credits += -e.amount
		}
	}
	u := debitsThrough - credits
	if u < 0 {
		return 0
	}
	if u > invoice {
		return invoice
	}
	return u
}

// lateFeeMonths counts how many monthly fees apply: fee n is charged if the
// invoice is still unpaid at the end of (due date + n-1 months).
func lateFeeMonths(es []entry, periodID string, due, asOf time.Time) int {
	n := 0
	for n < 120 {
		checkpoint := due.AddDate(0, n, 0)
		if !checkpoint.Before(asOf) { // the day after the checkpoint must have arrived
			return n
		}
		if unpaidOn(es, periodID, checkpoint.Format(DateFmt)) <= 0 {
			return n
		}
		n++
	}
	return n
}

// ApplyLateFees reconciles late-fee entries for all auto periods so the ledger
// reflects exactly the fees owed as of asOf. Idempotent and self-correcting when
// back-dated payments are added later.
func (a *App) ApplyLateFees(asOf time.Time) (int, error) {
	rows, err := a.DB.Query(`SELECT id,due_date,late_fee FROM periods WHERE auto_late_fee=1 AND invoiced_at IS NOT NULL`)
	if err != nil {
		return 0, err
	}
	type pinfo struct {
		id  string
		due time.Time
		fee int64
	}
	var ps []pinfo
	for rows.Next() {
		var p pinfo
		var due string
		if err := rows.Scan(&p.id, &due, &p.fee); err != nil {
			rows.Close()
			return 0, err
		}
		p.due, _ = ParseDate(due)
		ps = append(ps, p)
	}
	rows.Close()
	if len(ps) == 0 {
		return 0, nil
	}
	flats, err := a.Flats()
	if err != nil {
		return 0, err
	}
	changed := 0
	for _, f := range flats {
		es, err := a.entries(f.Code)
		if err != nil {
			return 0, err
		}
		for _, p := range ps {
			want := lateFeeMonths(es, p.id, p.due, asOf)
			res, err := a.DB.Exec(`DELETE FROM ledger WHERE flat=? AND period_id=? AND kind='late_fee' AND seq>?`, f.Code, p.id, want)
			if err != nil {
				return 0, err
			}
			c, _ := res.RowsAffected()
			changed += int(c)
			for n := 1; n <= want; n++ {
				date := p.due.AddDate(0, n-1, 1).Format(DateFmt)
				res, err := a.DB.Exec(`INSERT INTO ledger(flat,date,kind,period_id,seq,amount,description)
					VALUES(?,?,'late_fee',?,?,?,?) ON CONFLICT DO NOTHING`, f.Code, date, p.id, n, p.fee,
					fmt.Sprintf("Late fee %s – month %d", p.id, n))
				if err != nil {
					return 0, err
				}
				c, _ := res.RowsAffected()
				changed += int(c)
			}
		}
	}
	return changed, nil
}

// ---- period status ---------------------------------------------------------------

type FlatPeriodStatus struct {
	Flat        string `json:"flat"`
	Owner       string `json:"owner"`
	Invoice     int64  `json:"invoice"`
	Outstanding int64  `json:"outstanding"` // of this period's invoice
	LateFees    int64  `json:"late_fees"`
	Balance     int64  `json:"balance"` // full account balance
	Invoiced    bool   `json:"invoiced"`
}

func (a *App) PeriodStatus(periodID string, asOf time.Time) ([]FlatPeriodStatus, error) {
	flats, err := a.Flats()
	if err != nil {
		return nil, err
	}
	on := asOf.Format(DateFmt)
	out := make([]FlatPeriodStatus, 0, len(flats))
	for _, f := range flats {
		es, err := a.entries(f.Code)
		if err != nil {
			return nil, err
		}
		s := FlatPeriodStatus{Flat: f.Code, Owner: f.Owner, Balance: f.Balance}
		for _, e := range es {
			if e.period == periodID && e.kind == "invoice" {
				s.Invoice, s.Invoiced = e.amount, true
			}
			if e.period == periodID && e.kind == "late_fee" {
				s.LateFees += e.amount
			}
		}
		s.Outstanding = unpaidOn(es, periodID, on)
		out = append(out, s)
	}
	return out, nil
}

// Defaulters returns flats with a positive balance as of today.
func (a *App) Defaulters() ([]Flat, error) {
	fs, err := a.Flats()
	if err != nil {
		return nil, err
	}
	var out []Flat
	for _, f := range fs {
		if f.Balance > 0 {
			out = append(out, f)
		}
	}
	return out, nil
}

// CurrentPeriodID is the quarter containing today.
func CurrentPeriodID() string { return QuarterOf(Today()).ID() }

type LedgerVoid struct {
	ID          int64   `json:"id"`
	LedgerID    int64   `json:"ledger_id"`
	Flat        string  `json:"flat"`
	Date        string  `json:"date"`
	Kind        string  `json:"kind"`
	Amount      int64   `json:"amount"`
	Description string  `json:"description"`
	Reason      string  `json:"reason"`
	VoidedAt    string  `json:"voided_at"`
	RestoredAt  *string `json:"restored_at"`
}

// VoidPayment removes a payment that was recorded but never actually received
// (typically an imported sheet entry). Payments are re-allocated oldest-first,
// so later receipts move back onto the dues this one wrongly cleared.
func (a *App) VoidPayment(id int64, reason string) error {
	if strings.TrimSpace(reason) == "" {
		return fmt.Errorf("a reason is required")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	tx, err := a.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`INSERT INTO ledger_voids(ledger_id,flat,date,kind,period_id,seq,amount,description,mode,source,reason)
		SELECT id,flat,date,kind,period_id,seq,amount,description,mode,source,? FROM ledger
		WHERE id=? AND kind='payment' AND bank_txn_id IS NULL`, reason, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("only payments not linked to a bank transaction can be voided (re-assign bank payments on the Bank page)")
	}
	if _, err := tx.Exec(`DELETE FROM ledger WHERE id=?`, id); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	_, err = a.ApplyLateFees(Today())
	return err
}

func (a *App) RestoreVoid(voidID int64) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	tx, err := a.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`INSERT INTO ledger(flat,date,kind,period_id,seq,amount,description,mode,source)
		SELECT flat,date,kind,period_id,seq,amount,description,mode,source FROM ledger_voids WHERE id=? AND restored_at IS NULL`, voidID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("nothing to restore")
	}
	if _, err := tx.Exec(`UPDATE ledger_voids SET restored_at=`+a.DB.Now()+` WHERE id=?`, voidID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	_, err = a.ApplyLateFees(Today())
	return err
}

func (a *App) Voids(flat string) ([]LedgerVoid, error) {
	rows, err := a.DB.Query(`SELECT id,ledger_id,flat,date,kind,amount,description,reason,voided_at,restored_at FROM ledger_voids
		WHERE flat=? ORDER BY id DESC`, flat)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LedgerVoid{}
	for rows.Next() {
		var v LedgerVoid
		if err := rows.Scan(&v.ID, &v.LedgerID, &v.Flat, &v.Date, &v.Kind, &v.Amount, &v.Description, &v.Reason, &v.VoidedAt, &v.RestoredAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
