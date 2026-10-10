package app

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type Line struct {
	Label  string `json:"label"`
	Amount int64  `json:"amount"`
	Count  int    `json:"count,omitempty"`
}

// IncomeLine shows what was charged in the quarter (receivable) next to the
// money actually received for it (actual).
type IncomeLine struct {
	Label        string `json:"label"`
	Receivable   int64  `json:"receivable"`
	Actual       int64  `json:"actual"`
	NoReceivable bool   `json:"no_receivable,omitempty"` // e.g. arrears: charged in an earlier quarter
}

type PnL struct {
	PeriodID        string       `json:"period_id"`
	From            string       `json:"from"`
	To              string       `json:"to"`
	Running         bool         `json:"running"` // quarter still in progress
	Income          []IncomeLine `json:"income"`
	TotalReceivable int64        `json:"total_receivable"`
	TotalIncome     int64        `json:"total_income"` // actuals
	Expenses        []Line       `json:"expenses"`
	TotalExpenses   int64        `json:"total_expenses"`
	Surplus         int64        `json:"surplus"`              // actual income − expenses → sinking fund
	SurplusIfPaid   int64        `json:"surplus_if_collected"` // receivable income − expenses
	// supporting figures (not part of the P&L)
	Collections   int64        `json:"collections"`
	Receivables   int64        `json:"receivables"`
	NonPnLOutflow int64        `json:"non_pnl_outflow"`
	NonPnLItems   []NonPnLItem `json:"non_pnl_items"`
	// sinking fund movement for this quarter
	FundOpening int64  `json:"fund_opening"` // balance brought forward
	FundClosing int64  `json:"fund_closing"` // opening + this quarter's surplus
	FundCurrent int64  `json:"fund_current"` // balance today, incl. the running quarter
	FundAsOf    string `json:"fund_as_of"`
	// bank position and how it ties to the fund
	BankBalance        int64  `json:"bank_balance"` // at period end (or today for a running quarter)
	BankBalanceOn      string `json:"bank_balance_on"`
	HasBankBalance     bool   `json:"has_bank_balance"`
	BankCurrent        int64  `json:"bank_current"` // latest imported statement
	BankCurrentOn      string `json:"bank_current_on"`
	DepositsBalance    int64  `json:"deposits_balance"`    // FD/RD book value at the same date
	PendingTransfers   int64  `json:"pending_transfers"`   // savings → FD/RD booked, not yet on a statement
	TotalFunds         int64  `json:"total_funds"`         // savings + deposits
	AdvancesHeld       int64  `json:"advances_held"`       // members' credit balances at the same date
	UnallocatedReserve int64  `json:"unallocated_reserve"` // total funds − sinking fund − advances
}

// NonPnLItem is an entry excluded from the P&L (petty-cash advances,
// reimbursements by members, corrections).
type NonPnLItem struct {
	ID          int64  `json:"id"`
	Date        string `json:"date"`
	Description string `json:"description"`
	Category    string `json:"category"`
	Payee       string `json:"payee"`
	Amount      int64  `json:"amount"`
}

// PnLFor returns the quarter's P&L together with its sinking fund movement.
func (a *App) PnLFor(periodID string) (*PnL, error) {
	p, err := a.pnlCore(periodID)
	if err != nil {
		return nil, err
	}
	sf, err := a.SinkingFundReport()
	if err != nil {
		return nil, err
	}
	p.FundOpening = sf.Opening
	for _, q := range sf.Quarters {
		if qq, _ := ParseQuarter(q.PeriodID); qq.Start().Format(DateFmt) < p.From {
			p.FundOpening = q.Balance
		}
	}
	p.FundClosing = p.FundOpening + p.Surplus
	p.FundCurrent = sf.Projected
	p.FundAsOf = Today().Format(DateFmt)

	at := p.To
	if p.Running {
		at = p.FundAsOf
	}
	p.BankBalance, p.BankBalanceOn, p.HasBankBalance = a.BankBalanceOn(at)
	// FD/RD transfers booked but not yet on a statement already left savings
	p.PendingTransfers = a.PendingDepositTransfers(at)
	p.BankBalance -= p.PendingTransfers
	if p.PendingTransfers != 0 {
		p.BankBalanceOn = at
	}
	p.BankCurrent, p.BankCurrentOn = a.LatestBankBalance()
	p.BankCurrent -= a.PendingDepositTransfers(Today().Format(DateFmt))
	a.DB.QueryRow(`SELECT COALESCE(-SUM(b),0) FROM (SELECT SUM(amount) AS b FROM ledger WHERE date<=? GROUP BY flat HAVING SUM(amount)<0) AS t`, at).Scan(&p.AdvancesHeld)
	p.DepositsBalance = a.DepositsValueOn(at)
	p.TotalFunds = p.BankBalance + p.DepositsBalance
	if p.HasBankBalance {
		p.UnallocatedReserve = p.TotalFunds - p.FundClosing - p.AdvancesHeld
	}
	return p, nil
}

// pnlCore builds an accrual-basis P&L: maintenance is income in the quarter it
// is billed for; expenses count on their bill date.
func (a *App) pnlCore(periodID string) (*PnL, error) {
	q, err := ParseQuarter(periodID)
	if err != nil {
		return nil, err
	}
	from, to := q.Start().Format(DateFmt), q.End().Format(DateFmt)
	today := Today().Format(DateFmt)
	p := &PnL{PeriodID: periodID, From: from, To: to, Running: today <= to && today >= from}
	upto := to
	if p.Running {
		upto = today
	}

	var maint, late, adj int64
	a.DB.QueryRow(`SELECT COALESCE(SUM(amount),0) FROM ledger WHERE kind='invoice' AND period_id=?`, periodID).Scan(&maint)
	a.DB.QueryRow(`SELECT COALESCE(SUM(amount),0) FROM ledger WHERE kind='late_fee' AND date BETWEEN ? AND ?`, from, upto).Scan(&late)
	a.DB.QueryRow(`SELECT COALESCE(SUM(amount),0) FROM ledger WHERE kind='adjustment' AND date BETWEEN ? AND ?`, from, upto).Scan(&adj)

	// actuals: payments allocated oldest-due-first, recognised in this quarter
	recs, err := a.recognitions()
	if err != nil {
		return nil, err
	}
	var actMaint, actArrears, actLate, actAdj int64
	for _, r := range recs {
		if r.date < from || r.date > upto {
			continue
		}
		switch {
		case r.kind == "invoice" && r.period == periodID:
			actMaint += r.amount
		case r.kind == "invoice" || r.kind == "opening":
			actArrears += r.amount
		case r.kind == "late_fee":
			actLate += r.amount
		default:
			actAdj += r.amount
		}
	}
	p.Income = append(p.Income, IncomeLine{Label: "Maintenance charges " + periodID, Receivable: maint, Actual: actMaint})
	if actArrears != 0 {
		p.Income = append(p.Income, IncomeLine{Label: "Arrears collected (earlier quarters)", Actual: actArrears, NoReceivable: true})
	}
	if late != 0 || actLate != 0 {
		p.Income = append(p.Income, IncomeLine{Label: "Late payment fees", Receivable: late, Actual: actLate})
	}
	if adj != 0 || actAdj != 0 {
		p.Income = append(p.Income, IncomeLine{Label: "Adjustments / waivers", Receivable: adj, Actual: actAdj})
	}
	rows, err := a.DB.Query(`SELECT category,SUM(amount) FROM other_income WHERE date BETWEEN ? AND ? GROUP BY category ORDER BY 2 DESC, 1`, from, upto)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var l IncomeLine
		rows.Scan(&l.Label, &l.Actual)
		l.Receivable = l.Actual
		p.Income = append(p.Income, l)
	}
	rows.Close()
	for _, l := range p.Income {
		p.TotalReceivable += l.Receivable
		p.TotalIncome += l.Actual
	}

	rows, err = a.DB.Query(`SELECT e.category,SUM(e.amount),COUNT(*) FROM expenses e JOIN categories c ON c.name=e.category
		WHERE c.in_pnl=1 AND e.date BETWEEN ? AND ? GROUP BY e.category ORDER BY 2 DESC, 1`, from, upto)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var l Line
		rows.Scan(&l.Label, &l.Amount, &l.Count)
		p.Expenses = append(p.Expenses, l)
		p.TotalExpenses += l.Amount
	}
	rows.Close()
	if p.Expenses == nil {
		p.Expenses = []Line{}
	}
	p.Surplus = p.TotalIncome - p.TotalExpenses
	p.SurplusIfPaid = p.TotalReceivable - p.TotalExpenses

	a.DB.QueryRow(`SELECT COALESCE(-SUM(amount),0) FROM ledger WHERE kind='payment' AND date BETWEEN ? AND ?`, from, upto).Scan(&p.Collections)
	a.DB.QueryRow(`SELECT COALESCE(SUM(b),0) FROM (SELECT SUM(amount) AS b FROM ledger WHERE date<=? GROUP BY flat HAVING SUM(amount)>0) AS t`, upto).Scan(&p.Receivables)
	rows, err = a.DB.Query(`SELECT e.id,e.date,e.description,e.category,e.payee,e.amount FROM expenses e JOIN categories c ON c.name=e.category
		WHERE c.in_pnl=0 AND e.date BETWEEN ? AND ? ORDER BY e.date, e.id`, from, upto)
	if err != nil {
		return nil, err
	}
	p.NonPnLItems = []NonPnLItem{}
	for rows.Next() {
		var it NonPnLItem
		if err := rows.Scan(&it.ID, &it.Date, &it.Description, &it.Category, &it.Payee, &it.Amount); err != nil {
			rows.Close()
			return nil, err
		}
		p.NonPnLItems = append(p.NonPnLItems, it)
		p.NonPnLOutflow += it.Amount
	}
	rows.Close()
	return p, nil
}

// recognition is part of a charge settled by a payment, dated when both the
// charge and the money existed (an advance is recognised when the bill falls due).
type recognition struct {
	kind, period, date string
	amount             int64
}

// ---- sinking fund ----------------------------------------------------------

type FundQuarter struct {
	PeriodID string `json:"period_id"`
	Income   int64  `json:"income"`
	Expenses int64  `json:"expenses"`
	Surplus  int64  `json:"surplus"`
	Running  bool   `json:"running"`
	Balance  int64  `json:"balance"` // fund balance after this quarter
}

type FundYear struct {
	FY       string  `json:"fy"`
	Income   int64   `json:"income"`
	Surplus  int64   `json:"surplus"`
	Percent  float64 `json:"percent"` // surplus ÷ income × 100, including a running quarter
	Complete bool    `json:"complete"`
	// the same ratio over completed quarters only, so an early running quarter
	// (bills in, maintenance not yet due) doesn't distort the year
	CompletedQuarters int     `json:"completed_quarters"`
	CompletedIncome   int64   `json:"completed_income"`
	CompletedSurplus  int64   `json:"completed_surplus"`
	CompletedPercent  float64 `json:"completed_percent"`
}

// fundYears groups quarters into financial years.
func fundYears(qs []FundQuarter) []FundYear {
	out := []FundYear{}
	idx := map[string]int{}
	for _, q := range qs {
		qq, _ := ParseQuarter(q.PeriodID)
		fy := qq.FYLabel()
		i, ok := idx[fy]
		if !ok {
			i = len(out)
			idx[fy] = i
			out = append(out, FundYear{FY: fy})
		}
		y := &out[i]
		y.Income += q.Income
		y.Surplus += q.Surplus
		if !q.Running {
			y.CompletedQuarters++
			y.CompletedIncome += q.Income
			y.CompletedSurplus += q.Surplus
		}
	}
	for i := range out {
		y := &out[i]
		y.Complete = y.CompletedQuarters == 4
		if y.Income != 0 {
			y.Percent = float64(y.Surplus) * 100 / float64(y.Income)
		}
		if y.CompletedIncome != 0 {
			y.CompletedPercent = float64(y.CompletedSurplus) * 100 / float64(y.CompletedIncome)
		}
	}
	return out
}

// yearFor returns the FY entry for a quarter id (zero value if absent).
func yearFor(years []FundYear, quarterID string) FundYear {
	qq, _ := ParseQuarter(quarterID)
	fy := qq.FYLabel()
	for _, y := range years {
		if y.FY == fy {
			return y
		}
	}
	return FundYear{FY: fy}
}

type SinkingFund struct {
	Opening     int64         `json:"opening"`
	OpeningNote string        `json:"opening_note"`
	AutoOpening bool          `json:"auto_opening"` // recalculated on statement upload
	Balance     int64         `json:"balance"`      // after completed quarters
	Projected   int64         `json:"projected"`    // including the running quarter
	Quarters    []FundQuarter `json:"quarters"`
	Years       []FundYear    `json:"years"`
	Current     FundYear      `json:"current"`
}

// SinkingFundReport: every quarter's surplus (profit) is transferred to the
// sinking fund, so contributions = surplus and the headline ratio is
// FY surplus ÷ FY income.
func (a *App) SinkingFundReport() (*SinkingFund, error) {
	ps, err := a.DB.Query(`SELECT id FROM periods ORDER BY start_date`)
	if err != nil {
		return nil, err
	}
	var ids []string
	for ps.Next() {
		var id string
		ps.Scan(&id)
		ids = append(ids, id)
	}
	ps.Close()
	sf := &SinkingFund{Opening: Paise(a.SettingFloat("sinking_fund_opening")), OpeningNote: a.Setting("sinking_fund_note"), AutoOpening: a.Setting("sinking_fund_auto") != "0", Quarters: []FundQuarter{}, Years: []FundYear{}}
	bal := sf.Opening
	sf.Balance = bal
	today := Today().Format(DateFmt)
	for _, id := range ids {
		p, err := a.pnlCore(id)
		if err != nil {
			return nil, err
		}
		if p.From > today {
			continue
		}
		bal += p.Surplus
		if !p.Running {
			sf.Balance = bal
		}
		sf.Quarters = append(sf.Quarters, FundQuarter{id, p.TotalIncome, p.TotalExpenses, p.Surplus, p.Running, bal})
	}
	sf.Projected = bal
	sf.Years = fundYears(sf.Quarters)
	sf.Current = yearFor(sf.Years, QuarterOf(Today()).ID())
	return sf, nil
}

// ---- reminders ----------------------------------------------------------------

type Reminder struct {
	ID        int64    `json:"id"`
	PeriodID  string   `json:"period_id"`
	Seq       int      `json:"seq"`
	Text      string   `json:"text"`
	Flats     []string `json:"flats"`
	Status    string   `json:"status"`
	CreatedAt string   `json:"created_at"`
	SentAt    *string  `json:"sent_at"`
}

func (a *App) Reminders(periodID string) ([]Reminder, error) {
	rows, err := a.DB.Query(`SELECT id,period_id,seq,text,flats,status,created_at,sent_at FROM reminders
		WHERE (?='' OR period_id=?) AND kind='followup' ORDER BY id DESC`, periodID, periodID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Reminder{}
	for rows.Next() {
		var r Reminder
		var flats string
		if err := rows.Scan(&r.ID, &r.PeriodID, &r.Seq, &r.Text, &flats, &r.Status, &r.CreatedAt, &r.SentAt); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(flats), &r.Flats)
		out = append(out, r)
	}
	return out, rows.Err()
}

func ordinal(d int) string {
	if d >= 11 && d <= 13 {
		return fmt.Sprintf("%dth", d)
	}
	switch d % 10 {
	case 1:
		return fmt.Sprintf("%dst", d)
	case 2:
		return fmt.Sprintf("%dnd", d)
	case 3:
		return fmt.Sprintf("%drd", d)
	}
	return fmt.Sprintf("%dth", d)
}

func longDate(t time.Time) string {
	return fmt.Sprintf("%s %s, %d", ordinal(t.Day()), t.Month(), t.Year())
}

// FlatsByFloor renders codes the way the WhatsApp reminders list them:
// one line per floor, comma separated, indented.
func FlatsByFloor(codes []string) string {
	var lines []string
	var cur []string
	floor := ""
	for _, c := range codes {
		if f := Floor(c); f != floor && len(cur) > 0 {
			lines = append(lines, "    "+strings.Join(cur, ", "))
			cur = nil
		}
		floor = Floor(c)
		cur = append(cur, c)
	}
	if len(cur) > 0 {
		lines = append(lines, "    "+strings.Join(cur, ", "))
	}
	return strings.Join(lines, "\n")
}

// BuildReminder prepares (but does not store) the next reminder for a period.
func (a *App) BuildReminder(periodID string) (Reminder, error) {
	p, err := a.Period(periodID)
	if err != nil {
		return Reminder{}, err
	}
	defs, err := a.Defaulters()
	if err != nil {
		return Reminder{}, err
	}
	var codes []string
	for _, f := range defs {
		codes = append(codes, f.Code)
	}
	var seq int
	a.DB.QueryRow(`SELECT COALESCE(MAX(seq),0) FROM reminders WHERE period_id=? AND status='sent' AND kind='followup'`, periodID).Scan(&seq)
	seq++

	due, _ := ParseDate(p.DueDate)
	q, _ := ParseQuarter(p.ID)
	days := int(due.Sub(Today()).Hours() / 24)
	var status string
	switch {
	case days > 1:
		status = fmt.Sprintf("Last %d days left to clear pending amount raised for maintenance up above.", days)
	case days == 1:
		status = "Only 1 day left to clear pending amount raised for maintenance up above."
	case days == 0:
		status = "Today is the last day to clear pending amount raised for maintenance up above."
	default:
		status = fmt.Sprintf("The due date has passed; a late fee of ₹%s per month is being added to pending dues.", Rupees(p.LateFee))
	}
	dueLine := "Last date of payment clearance is " + longDate(due) + "."
	if !due.Equal(q.DefaultDue()) {
		dueLine = "Revised last date of payment clearance is " + longDate(due) + "."
	}
	r := strings.NewReplacer(
		"{{seq}}", fmt.Sprint(seq),
		"{{status_line}}", status,
		"{{count}}", fmt.Sprint(len(codes)),
		"{{flats}}", FlatsByFloor(codes),
		"{{due_line}}", dueLine,
		"{{due_date}}", longDate(due),
		"{{period}}", p.ID,
	)
	if codes == nil {
		codes = []string{}
	}
	return Reminder{PeriodID: periodID, Seq: seq, Text: r.Replace(a.Setting("reminder_template")), Flats: codes, Status: "draft"}, nil
}

// SaveReminderDraft stores a draft, replacing any unsent draft for the period.
func (a *App) SaveReminderDraft(periodID string) (Reminder, error) {
	r, err := a.BuildReminder(periodID)
	if err != nil {
		return r, err
	}
	fj, _ := json.Marshal(r.Flats)
	if _, err := a.DB.Exec(`DELETE FROM reminders WHERE period_id=? AND status='draft' AND kind='followup'`, periodID); err != nil {
		return r, err
	}
	id, err := a.DB.InsertID(`INSERT INTO reminders(period_id,seq,text,flats,status) VALUES(?,?,?,?,'draft')`, periodID, r.Seq, r.Text, string(fj))
	if err != nil {
		return r, err
	}
	r.ID = id
	return r, nil
}

func (a *App) MarkReminderSent(id int64, text string) error {
	_, err := a.DB.Exec(`UPDATE reminders SET status='sent', sent_at=`+a.DB.Now()+`, text=CASE WHEN ?='' THEN text ELSE ? END WHERE id=?`, text, text, id)
	return err
}

func (a *App) DeleteReminder(id int64) error {
	_, err := a.DB.Exec(`DELETE FROM reminders WHERE id=? AND status='draft'`, id)
	return err
}

// AutoReminder runs from the scheduler: when enabled, it prepares a fresh
// draft every N days for the newest invoiced open period while dues remain.
func (a *App) AutoReminder() (string, error) {
	if a.Setting("reminder_auto") != "1" {
		return "auto reminders disabled", nil
	}
	var pid, invoiced string
	if err := a.DB.QueryRow(`SELECT id,invoiced_at FROM periods WHERE status='open' AND invoiced_at IS NOT NULL ORDER BY start_date DESC LIMIT 1`).Scan(&pid, &invoiced); err != nil {
		return "no open invoiced period", nil
	}
	defs, err := a.Defaulters()
	if err != nil || len(defs) == 0 {
		return "no dues pending", err
	}
	interval := int(a.SettingFloat("reminder_interval_days"))
	if interval < 1 {
		interval = 1
	}
	var last string
	a.DB.QueryRow(`SELECT COALESCE(MAX(created_at),?) FROM reminders WHERE period_id=?`, invoiced, pid).Scan(&last)
	lt, err := time.Parse("2006-01-02 15:04:05", last)
	if err != nil {
		lt = time.Time{}
	}
	if time.Since(lt) < time.Duration(interval)*24*time.Hour {
		return "not due yet", nil
	}
	r, err := a.SaveReminderDraft(pid)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("draft reminder #%d prepared for %s (%d flats)", r.Seq, pid, len(r.Flats)), nil
}

// CalibrateSinkingFund back-solves the sinking fund opening balance (reserves
// held before the recorded history) so that, at the end of the given quarter,
// bank balance = sinking fund + member advances.
func (a *App) CalibrateSinkingFund(periodID string) (int64, error) {
	p, err := a.PnLFor(periodID)
	if err != nil {
		return 0, err
	}
	if p.Running {
		return 0, fmt.Errorf("%s is still running; calibrate on a completed quarter", periodID)
	}
	if !p.HasBankBalance {
		return 0, fmt.Errorf("no bank statement covers %s; import one first", p.To)
	}
	opening := Paise(a.SettingFloat("sinking_fund_opening")) + p.UnallocatedReserve
	note := fmt.Sprintf("Back-calculated on %s: savings ₹%s + FD/RD ₹%s − recorded surpluses − member advances (₹%s)",
		p.BankBalanceOn, Rupees(p.BankBalance), Rupees(p.DepositsBalance), Rupees(p.AdvancesHeld))
	err = a.SaveSettings(map[string]string{
		"sinking_fund_opening": strconv.FormatFloat(float64(opening)/100, 'f', 2, 64),
		"sinking_fund_note":    note,
		"sinking_fund_anchor":  p.To,
	})
	return opening, err
}

// AutoCalibrateSinkingFund re-anchors the opening balance on the latest
// completed quarter whose end the bank statements cover, once per quarter:
// the first statement reaching a new quarter end triggers it, re-uploads and
// overlapping statements do not. It returns a message when the opening moved.
// Off when the sinking_fund_auto setting is "0".
func (a *App) AutoCalibrateSinkingFund() (string, error) {
	if a.Setting("sinking_fund_auto") == "0" {
		return "", nil
	}
	ps, err := a.Periods() // newest first
	if err != nil {
		return "", err
	}
	anchor := a.Setting("sinking_fund_anchor")
	for _, q := range ps {
		if q.EndDate <= anchor {
			return "", nil // nothing newer than the last calibration is covered
		}
		p, err := a.PnLFor(q.ID)
		if err != nil {
			return "", err
		}
		// a statement must span the quarter end: BankBalanceOn would otherwise
		// fall back to a stale balance from an earlier statement
		var spanning int
		if err := a.DB.QueryRow(`SELECT COUNT(*) FROM bank_statements WHERE from_date<=? AND to_date>=?`, q.EndDate, q.EndDate).Scan(&spanning); err != nil {
			return "", err
		}
		if p.Running || !p.HasBankBalance || spanning == 0 {
			continue
		}
		before := Paise(a.SettingFloat("sinking_fund_opening"))
		after, err := a.CalibrateSinkingFund(q.ID)
		if err != nil {
			return "", err
		}
		if after == before {
			return "", nil
		}
		return fmt.Sprintf("Sinking fund opening recalculated from the bank balance at the end of %s: ₹%s → ₹%s.",
			q.ID, Rupees(before), Rupees(after)), nil
	}
	return "", nil
}
