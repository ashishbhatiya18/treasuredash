package app

import (
	"fmt"
	"sort"
)

// Dashboard is the treasurer's one-screen view of the running quarter:
// what is owed to us, what we owe, cash in/out, and the sinking fund.
type Dashboard struct {
	Today          string    `json:"today"`
	AsOf           string    `json:"as_of"` // quarter end for past quarters, today for the running one
	Running        bool      `json:"running"`
	CurrentPeriod  string    `json:"current_period"` // the quarter being shown
	TodayPeriod    string    `json:"today_period"`   // the quarter containing today
	PrevPeriod     string    `json:"prev_period"`
	NextPeriod     string    `json:"next_period"` // empty when already at today's quarter
	FirstPeriod    string    `json:"first_period"`
	Period         *Period   `json:"period"`
	HasData        bool      `json:"has_data"`
	NeedsInvoicing bool      `json:"needs_invoicing"`
	ReviewCount    int       `json:"review_count"`
	DraftReminder  *Reminder `json:"draft_reminder"`

	PnL         *PnL            `json:"pnl"`
	Cash        CashWalk        `json:"cash"`
	Receivables ReceivablesView `json:"receivables"`
	Payables    PayablesView    `json:"payables"`
	Advances    AdvancesView    `json:"advances"`
	Collections CollectionsView `json:"collections"`
	Sinking     *SinkingFund    `json:"sinking"`
}

// CashWalk: bank opening at quarter start + receipts − payments = closing.
type CashWalk struct {
	HasData   bool   `json:"has_data"`
	Opening   int64  `json:"opening"`
	OpeningOn string `json:"opening_on"`
	Receipts  int64  `json:"receipts"`
	Payments  int64  `json:"payments"`
	Closing   int64  `json:"closing"`
	ClosingOn string `json:"closing_on"`
	// FD/RD: savings + deposits = total funds
	DepositsOpening   int64     `json:"deposits_opening"`
	DepositsClosing   int64     `json:"deposits_closing"`
	TotalOpening      int64     `json:"total_opening"`
	TotalClosing      int64     `json:"total_closing"`
	ToDeposits        int64     `json:"to_deposits"`        // savings → FD/RD transfers in the quarter
	BookedToDeposits  int64     `json:"booked_to_deposits"` // booked in Deposits, not yet on a statement (net, this quarter)
	PendingClosing    int64     `json:"pending_closing"`    // booked transfers outstanding at the closing date
	FromDeposits      int64     `json:"from_deposits"`      // FD/RD → savings (maturity / closure)
	PaymentsOperating int64     `json:"payments_operating"`
	Deposits          []Deposit `json:"deposits"` // open at the closing date
	// split of receipts/payments by what they were
	ReceiptsMaintenance int64 `json:"receipts_maintenance"`
	ReceiptsOther       int64 `json:"receipts_other"`
	ReceiptsUnassigned  int64 `json:"receipts_unassigned"`
	PaymentsUnassigned  int64 `json:"payments_unassigned"`
}

type ReceivableRow struct {
	Flat        string `json:"flat"`
	Owner       string `json:"owner"`
	Current     int64  `json:"current"` // this quarter's maintenance
	Arrears     int64  `json:"arrears"` // earlier quarters / dues b/f
	LateFees    int64  `json:"late_fees"`
	Total       int64  `json:"total"`
	DaysOverdue int    `json:"days_overdue"`
}

type ReceivablesView struct {
	Total    int64           `json:"total"`
	Current  int64           `json:"current"`
	Arrears  int64           `json:"arrears"`
	LateFees int64           `json:"late_fees"`
	Overdue  int64           `json:"overdue"`
	Flats    []ReceivableRow `json:"flats"`
}

type PayableRow struct {
	Payee    string `json:"payee"`
	Billed   int64  `json:"billed"`    // bills recorded since cut-over
	Paid     int64  `json:"paid"`      // bank payments since cut-over
	Balance  int64  `json:"balance"`   // + we owe them, − paid in advance (e.g. petty-cash float)
	LastBill string `json:"last_bill"` // date
	PaidQtr  int64  `json:"paid_qtr"`  // paid during the running quarter
}

type PayablesView struct {
	Since        string       `json:"since"`
	Total        int64        `json:"total"`         // sum of positive balances
	PrepaidTotal int64        `json:"prepaid_total"` // sum of negative balances (advances to vendors/caretaker)
	PaidQtr      int64        `json:"paid_qtr"`
	Payees       []PayableRow `json:"payees"`
}

type AdvanceRow struct {
	Flat   string `json:"flat"`
	Owner  string `json:"owner"`
	Amount int64  `json:"amount"`
}

type AdvancesView struct {
	Total int64        `json:"total"`
	Flats []AdvanceRow `json:"flats"`
}

type CollectionsView struct {
	Count       int       `json:"count"`
	Amount      int64     `json:"amount"`
	OffBank     int64     `json:"off_bank"`
	FlatsPaid   int       `json:"flats_paid"`   // flats whose current-quarter invoice is fully settled
	FlatsBilled int       `json:"flats_billed"` // flats invoiced this quarter
	Recent      []Receipt `json:"recent"`
}

// DashboardData builds the view for a quarter ("" = the running quarter).
// Past quarters are shown as they stood at quarter end.
func (a *App) DashboardData(periodID string) (*Dashboard, error) {
	todayT := Today()
	today := todayT.Format(DateFmt)
	todayQ := QuarterOf(todayT)
	q := todayQ
	if periodID != "" {
		var err error
		if q, err = ParseQuarter(periodID); err != nil {
			return nil, err
		}
		if q.Start().After(todayT) {
			return nil, fmt.Errorf("%s has not started yet", periodID)
		}
	}
	cur := q.ID()
	from := q.Start().Format(DateFmt)
	asOfT := q.End()
	if asOfT.After(todayT) {
		asOfT = todayT
	}
	asOf := asOfT.Format(DateFmt)
	d := &Dashboard{Today: today, AsOf: asOf, Running: asOf == today && q == todayQ, CurrentPeriod: cur, TodayPeriod: todayQ.ID(), PrevPeriod: q.Prev().ID()}
	if q != todayQ {
		d.NextPeriod = q.Next().ID()
	}
	a.DB.QueryRow(`SELECT COALESCE(MIN(id),'') FROM periods WHERE start_date=(SELECT MIN(start_date) FROM periods)`).Scan(&d.FirstPeriod)

	var n int
	a.DB.QueryRow(`SELECT COUNT(*) FROM flats`).Scan(&n)
	d.HasData = n > 0
	if p, err := a.Period(cur); err == nil {
		d.Period = &p
		d.NeedsInvoicing = p.InvoicedAt == nil
	} else {
		d.NeedsInvoicing = true
	}
	a.DB.QueryRow(`SELECT COUNT(*) FROM bank_txns WHERE status='review'`).Scan(&d.ReviewCount)
	if rs, err := a.Reminders(cur); err == nil {
		for _, r := range rs {
			if r.Status == "draft" {
				r := r
				d.DraftReminder = &r
				break
			}
		}
	}

	var err error
	if d.PnL, err = a.PnLFor(cur); err != nil {
		return nil, err
	}
	if d.Sinking, err = a.SinkingFundReport(); err != nil {
		return nil, err
	}

	// receivables & advances from the FIFO allocation
	flats, err := a.Flats()
	if err != nil {
		return nil, err
	}
	dues, err := a.dueDates()
	if err != nil {
		return nil, err
	}
	d.Receivables.Flats = []ReceivableRow{}
	d.Advances.Flats = []AdvanceRow{}
	for _, f := range flats {
		ch, _, sum, err := a.allocateFlatAt(f.Code, dues, asOfT)
		if err != nil {
			return nil, err
		}
		row := ReceivableRow{Flat: f.Code, Owner: f.Owner}
		for _, c := range ch {
			if c.Kind == "invoice" && c.PeriodID != nil && *c.PeriodID == cur {
				d.Collections.FlatsBilled++
				if c.Outstanding == 0 {
					d.Collections.FlatsPaid++
				}
			}
			if c.Outstanding == 0 {
				continue
			}
			switch {
			case c.Kind == "invoice" && c.PeriodID != nil && *c.PeriodID == cur:
				row.Current += c.Outstanding
			case c.Kind == "late_fee":
				row.LateFees += c.Outstanding
			default:
				row.Arrears += c.Outstanding
			}
			row.DaysOverdue = max(row.DaysOverdue, c.DaysOverdue)
		}
		row.Total = row.Current + row.Arrears + row.LateFees
		if row.Total > 0 {
			d.Receivables.Flats = append(d.Receivables.Flats, row)
			d.Receivables.Total += row.Total
			d.Receivables.Current += row.Current
			d.Receivables.Arrears += row.Arrears
			d.Receivables.LateFees += row.LateFees
		}
		d.Receivables.Overdue += sum.Overdue
		if sum.Advance > 0 {
			d.Advances.Flats = append(d.Advances.Flats, AdvanceRow{f.Code, f.Owner, sum.Advance})
			d.Advances.Total += sum.Advance
		}
	}
	// receivables stay in flat order (GD, GE, …, 8F) – Flats() is already sorted that way
	sort.Slice(d.Advances.Flats, func(i, j int) bool { return d.Advances.Flats[i].Amount > d.Advances.Flats[j].Amount })

	// collections this quarter
	rs, err := a.Receipts(ReceiptFilter{From: from, To: asOf})
	if err != nil {
		return nil, err
	}
	for _, r := range rs {
		d.Collections.Count++
		d.Collections.Amount += -r.Amount
		if r.Mode != "bank" {
			d.Collections.OffBank += -r.Amount
		}
	}
	d.Collections.Recent = rs[:min(len(rs), 8)]

	if err := a.cashWalk(&d.Cash, from, asOf); err != nil {
		return nil, err
	}
	if err := a.payables(&d.Payables, from, asOf); err != nil {
		return nil, err
	}
	// sinking fund trend stops at the quarter being viewed
	var qs []FundQuarter
	for _, fq := range d.Sinking.Quarters {
		if fq.PeriodID == cur || func() bool { x, _ := ParseQuarter(fq.PeriodID); return x.Start().Before(q.Start()) }() {
			qs = append(qs, fq)
		}
	}
	if qs != nil {
		d.Sinking.Quarters = qs
		d.Sinking.Projected = qs[len(qs)-1].Balance
		// FY shares only from quarters up to the one being viewed
		for _, fq := range qs {
			if !fq.Running {
				d.Sinking.Balance = fq.Balance
			}
		}
		d.Sinking.Years = fundYears(qs)
		d.Sinking.Current = yearFor(d.Sinking.Years, cur)
	}
	return d, nil
}

func (a *App) cashWalk(c *CashWalk, from, to string) error {
	// opening: balance just before the first transaction on/after quarter start,
	// else the last balance before the quarter
	var bal, cr, dr int64
	err := a.DB.QueryRow(`SELECT balance,credit,debit FROM bank_txns WHERE date>=? AND date<=? ORDER BY date, id LIMIT 1`, from, to).Scan(&bal, &cr, &dr)
	if err == nil {
		c.Opening, c.OpeningOn, c.HasData = bal-cr+dr, from, true
	} else if err := a.DB.QueryRow(`SELECT balance FROM bank_txns WHERE date<? ORDER BY date DESC, id DESC LIMIT 1`, from).Scan(&bal); err == nil {
		c.Opening, c.OpeningOn, c.HasData = bal, from, true
	} else {
		return nil
	}
	a.DB.QueryRow(`SELECT COALESCE(SUM(credit),0),COALESCE(SUM(debit),0),
		COALESCE(SUM(CASE WHEN kind='maintenance' AND status<>'review' THEN credit END),0),
		COALESCE(SUM(CASE WHEN kind='other_income' THEN credit END),0),
		COALESCE(SUM(CASE WHEN status='review' THEN credit END),0),
		COALESCE(SUM(CASE WHEN status='review' THEN debit END),0),
		COALESCE(SUM(CASE WHEN kind='transfer' AND payee='FD/RD' THEN debit END),0),
		COALESCE(SUM(CASE WHEN kind='transfer' AND payee='FD/RD' THEN credit END),0)
		FROM bank_txns WHERE date BETWEEN ? AND ?`, from, to).
		Scan(&c.Receipts, &c.Payments, &c.ReceiptsMaintenance, &c.ReceiptsOther, &c.ReceiptsUnassigned, &c.PaymentsUnassigned, &c.ToDeposits, &c.FromDeposits)
	c.PaymentsOperating = c.Payments - c.ToDeposits
	c.Closing = c.Opening + c.Receipts - c.Payments
	c.ClosingOn = to
	// transfers booked in Deposits that the statements don't show yet
	openBeforeT, _ := ParseDate(from)
	pendOpen := a.PendingDepositTransfers(openBeforeT.AddDate(0, 0, -1).Format(DateFmt))
	c.PendingClosing = a.PendingDepositTransfers(to)
	c.BookedToDeposits = c.PendingClosing - pendOpen
	c.Opening -= pendOpen
	c.Closing -= c.PendingClosing
	var last string
	a.DB.QueryRow(`SELECT COALESCE(MAX(date),'') FROM bank_txns WHERE date<=?`, to).Scan(&last)
	if last != "" && last < to && c.PendingClosing == 0 {
		c.ClosingOn = last // statements don't reach the as-of date yet
	}
	openBefore, _ := ParseDate(from)
	c.DepositsOpening = a.DepositsValueOn(openBefore.AddDate(0, 0, -1).Format(DateFmt))
	c.DepositsClosing = a.DepositsValueOn(to)
	c.TotalOpening = c.Opening + c.DepositsOpening
	c.TotalClosing = c.Closing + c.DepositsClosing
	c.Deposits = []Deposit{}
	if ds, err := a.Deposits(to); err == nil {
		for _, d := range ds {
			if d.Active {
				c.Deposits = append(c.Deposits, d)
			}
		}
	}
	return nil
}

// payables: per payee, bills recorded since the cut-over minus bank payments
// to them since the cut-over. Positive = we owe them; negative = they hold an
// advance (e.g. the caretaker's petty-cash float).
func (a *App) payables(p *PayablesView, qStart, asOf string) error {
	since := a.Setting("cutover_date")
	p.Since = since
	m := map[string]*PayableRow{}
	get := func(name string) *PayableRow {
		if name == "" {
			name = "Unspecified"
		}
		if r, ok := m[name]; ok {
			return r
		}
		r := &PayableRow{Payee: name}
		m[name] = r
		return r
	}
	if asOf < since {
		p.Payees = []PayableRow{}
		return nil // before the cut-over everything was settled in the sheet
	}
	rows, err := a.DB.Query(`SELECT payee,SUM(amount),MAX(date) FROM expenses WHERE date>=? AND date<=? GROUP BY payee`, since, asOf)
	if err != nil {
		return err
	}
	for rows.Next() {
		var payee, last string
		var amt int64
		if err := rows.Scan(&payee, &amt, &last); err != nil {
			rows.Close()
			return err
		}
		r := get(payee)
		r.Billed, r.LastBill = amt, last
	}
	rows.Close()
	rows, err = a.DB.Query(`SELECT payee,SUM(debit),SUM(CASE WHEN date>=? THEN debit ELSE 0 END) FROM bank_txns
		WHERE debit>0 AND kind='expense' AND status='matched' AND date>=? AND date<=? GROUP BY payee`, qStart, since, asOf)
	if err != nil {
		return err
	}
	for rows.Next() {
		var payee string
		var amt, qtr int64
		if err := rows.Scan(&payee, &amt, &qtr); err != nil {
			rows.Close()
			return err
		}
		r := get(payee)
		r.Paid, r.PaidQtr = amt, qtr
	}
	rows.Close()
	p.Payees = []PayableRow{}
	for _, r := range m {
		r.Balance = r.Billed - r.Paid
		if r.Balance > 0 {
			p.Total += r.Balance
		} else {
			p.PrepaidTotal += -r.Balance
		}
		p.PaidQtr += r.PaidQtr
		if r.Balance != 0 || r.PaidQtr != 0 {
			p.Payees = append(p.Payees, *r)
		}
	}
	sort.Slice(p.Payees, func(i, j int) bool { return p.Payees[i].Balance > p.Payees[j].Balance })
	return nil
}
