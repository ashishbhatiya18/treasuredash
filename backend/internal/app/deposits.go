package app

import (
	"assofin/internal/db"
	"database/sql"
	"fmt"
	"regexp"
	"strings"
	"time"
)

type Deposit struct {
	ID             int64     `json:"id"`
	Kind           string    `json:"kind"` // FD / RD
	Bank           string    `json:"bank"`
	Number         string    `json:"number"`
	OpenedOn       string    `json:"opened_on"`
	Principal      int64     `json:"principal"`
	Installment    int64     `json:"installment"`
	TenureMonths   int       `json:"tenure_months"`
	Rate           float64   `json:"rate"`
	MaturityDate   string    `json:"maturity_date"`
	MaturityAmount int64     `json:"maturity_amount"`
	ClosedOn       *string   `json:"closed_on"`
	Notes          string    `json:"notes"`
	FromSavings    bool      `json:"from_savings"`   // opened / instalments paid from the savings account
	ToSavings      bool      `json:"to_savings"`     // proceeds credited back to savings on closing
	ClosingAmount  int64     `json:"closing_amount"` // amount received on closing (0 = book value)
	Fundings       []Funding `json:"fundings"`
	// computed for the requested date
	Value  int64  `json:"value"` // book value (amount deposited) on the date
	Active bool   `json:"active"`
	Label  string `json:"label"`
}

func monthsBetween(a, b time.Time) int {
	m := (b.Year()-a.Year())*12 + int(b.Month()-a.Month())
	if b.Day() < a.Day() {
		m--
	}
	return m
}

// valueOn: FD = principal while open; RD = instalments paid so far (one on the
// opening date and one each month after, up to the tenure).
func (d *Deposit) valueOn(date string) {
	d.Label = d.Kind
	if d.Number != "" {
		d.Label += " " + d.Number
	}
	if d.Bank != "" {
		d.Label += " · " + d.Bank
	}
	d.Active = d.OpenedOn <= date && (d.ClosedOn == nil || *d.ClosedOn > date)
	if !d.Active {
		d.Value = 0
		return
	}
	if d.Kind == "FD" {
		d.Value = d.Principal
		return
	}
	o, _ := ParseDate(d.OpenedOn)
	t, _ := ParseDate(date)
	n := monthsBetween(o, t) + 1
	if d.TenureMonths > 0 && n > d.TenureMonths {
		n = d.TenureMonths
	}
	d.Value = int64(n) * d.Installment
}

func (a *App) Deposits(on string) ([]Deposit, error) {
	rows, err := a.DB.Query(`SELECT id,kind,bank,number,opened_on,principal,installment,tenure_months,rate,maturity_date,maturity_amount,closed_on,notes,
		from_savings,to_savings,closing_amount
		FROM deposits ORDER BY COALESCE(closed_on,'9999') DESC, opened_on DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Deposit{}
	for rows.Next() {
		var d Deposit
		var closed sql.NullString
		if err := rows.Scan(&d.ID, &d.Kind, &d.Bank, &d.Number, &d.OpenedOn, &d.Principal, &d.Installment, &d.TenureMonths, &d.Rate,
			&d.MaturityDate, &d.MaturityAmount, &closed, &d.Notes, &d.FromSavings, &d.ToSavings, &d.ClosingAmount); err != nil {
			return nil, err
		}
		if closed.Valid {
			d.ClosedOn = &closed.String
		}
		d.valueOn(on)
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	fs, err := a.fundings()
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Fundings = []Funding{}
		for _, f := range fs {
			if f.DepositID == out[i].ID {
				out[i].Fundings = append(out[i].Fundings, f)
			}
		}
	}
	return out, nil
}

// DepositsValueOn is the total book value of deposits open on a date, plus
// FD/RD transfers on the bank statement that aren't in the register yet (so
// money moved out of savings never disappears from total funds).
func (a *App) DepositsValueOn(on string) int64 {
	ds, err := a.Deposits(on)
	if err != nil {
		return 0
	}
	var v int64
	for _, d := range ds {
		v += d.Value
	}
	return v + a.UnregisteredDepositTransfers(on)
}

// UnregisteredDepositTransfers: net money moved savings → FD/RD by `on`
// according to the bank, on lines not linked to any deposit in the register.
func (a *App) UnregisteredDepositTransfers(on string) int64 {
	var v int64
	a.DB.QueryRow(`SELECT COALESCE(SUM(debit-credit),0) FROM bank_txns
		WHERE kind='transfer' AND payee='FD/RD' AND status IN ('matched','historical') AND date<=?
		AND id NOT IN (SELECT bank_txn_id FROM deposit_fundings WHERE bank_txn_id IS NOT NULL)`, on).Scan(&v)
	return v
}

var fdBooked = regexp.MustCompile(`(?i)\b(FD|RD)\s+BOOKED\s*-\s*(\d{6,})`)

// RegisterBookedDeposits creates register entries for "FD BOOKED - <number>"
// lines on the bank statement that aren't in the register yet. Rate, tenure
// and maturity are left for the treasurer to fill in. Safe to run repeatedly.
func (a *App) RegisterBookedDeposits() (int, error) {
	rows, err := a.DB.Query(`SELECT id,date,narration,debit FROM bank_txns
		WHERE kind='transfer' AND payee='FD/RD' AND debit>0
		AND id NOT IN (SELECT bank_txn_id FROM deposit_fundings WHERE bank_txn_id IS NOT NULL)
		ORDER BY date, id`)
	if err != nil {
		return 0, err
	}
	type line struct {
		date, narr, kind, number string
		amount                   int64
	}
	var ls []line
	for rows.Next() {
		var id int64
		var l line
		if err := rows.Scan(&id, &l.date, &l.narr, &l.amount); err != nil {
			rows.Close()
			return 0, err
		}
		if m := fdBooked.FindStringSubmatch(l.narr); m != nil {
			l.kind, l.number = strings.ToUpper(m[1]), m[2]
			ls = append(ls, l)
		}
	}
	rows.Close()
	n := 0
	for _, l := range ls {
		var exists int
		a.DB.QueryRow(`SELECT COUNT(*) FROM deposits WHERE number=?`, l.number).Scan(&exists)
		if exists > 0 {
			continue
		}
		d := Deposit{Kind: l.kind, Bank: strings.TrimSpace(strings.Split(a.Setting("bank_name"), ",")[0]), Number: l.number,
			OpenedOn: l.date, TenureMonths: 12, FromSavings: true, ToSavings: true,
			Notes: "Created from the bank statement – check tenure, rate and maturity"}
		if l.kind == "FD" {
			d.Principal = l.amount
		} else {
			d.Installment = l.amount
		}
		if _, err := a.SaveDeposit(d); err != nil { // books the transfer and links the bank line
			return n, err
		}
		n++
	}
	return n, nil
}

func (a *App) SaveDeposit(d Deposit) (int64, error) {
	d.Kind = strings.ToUpper(d.Kind)
	if d.Kind != "FD" && d.Kind != "RD" {
		return 0, fmt.Errorf("kind must be FD or RD")
	}
	if _, err := ParseDate(d.OpenedOn); err != nil {
		return 0, fmt.Errorf("opened on must be YYYY-MM-DD")
	}
	if d.Kind == "FD" && d.Principal <= 0 || d.Kind == "RD" && d.Installment <= 0 {
		return 0, fmt.Errorf("FD needs a principal, RD a monthly instalment")
	}
	if d.ClosedOn != nil && *d.ClosedOn == "" {
		d.ClosedOn = nil
	}
	if d.MaturityDate == "" && d.TenureMonths > 0 {
		o, _ := ParseDate(d.OpenedOn)
		d.MaturityDate = o.AddDate(0, d.TenureMonths, 0).Format(DateFmt)
	}
	if d.ClosedOn != nil && *d.ClosedOn < d.OpenedOn {
		return 0, fmt.Errorf("closed date is before the opening date")
	}
	if d.ID == 0 {
		id, err := a.DB.InsertID(`INSERT INTO deposits(kind,bank,number,opened_on,principal,installment,tenure_months,rate,maturity_date,maturity_amount,closed_on,notes,
			from_savings,to_savings,closing_amount) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, d.Kind, d.Bank, d.Number, d.OpenedOn, d.Principal, d.Installment,
			d.TenureMonths, d.Rate, d.MaturityDate, d.MaturityAmount, d.ClosedOn, d.Notes, db.Bool(d.FromSavings), db.Bool(d.ToSavings), d.ClosingAmount)
		if err != nil {
			return 0, err
		}
		d.ID = id
	} else if _, err := a.DB.Exec(`UPDATE deposits SET kind=?,bank=?,number=?,opened_on=?,principal=?,installment=?,tenure_months=?,rate=?,maturity_date=?,
		maturity_amount=?,closed_on=?,notes=?,from_savings=?,to_savings=?,closing_amount=? WHERE id=?`, d.Kind, d.Bank, d.Number, d.OpenedOn, d.Principal,
		d.Installment, d.TenureMonths, d.Rate, d.MaturityDate, d.MaturityAmount, d.ClosedOn, d.Notes, db.Bool(d.FromSavings), db.Bool(d.ToSavings), d.ClosingAmount, d.ID); err != nil {
		return 0, err
	}
	if err := a.rebuildFundings(d); err != nil {
		return d.ID, err
	}
	return d.ID, a.MatchFundings()
}

func (a *App) DeleteDeposit(id int64) error {
	if _, err := a.DB.Exec(`DELETE FROM other_income WHERE id IN (SELECT income_id FROM deposit_fundings WHERE deposit_id=? AND income_id IS NOT NULL)`, id); err != nil {
		return err
	}
	_, err := a.DB.Exec(`DELETE FROM deposits WHERE id=?`, id)
	return err
}

// ---- savings ⇄ deposit transfers ------------------------------------------------

type Funding struct {
	ID        int64  `json:"id"`
	DepositID int64  `json:"deposit_id"`
	Date      string `json:"date"`
	Kind      string `json:"kind"`   // open / installment / close
	Amount    int64  `json:"amount"` // + out of savings, − back into savings
	BankTxnID *int64 `json:"bank_txn_id"`
	BankRef   string `json:"bank_ref"`
	BankDate  string `json:"bank_date"`
}

func (a *App) fundings() ([]Funding, error) {
	rows, err := a.DB.Query(`SELECT f.id,f.deposit_id,f.date,f.kind,f.amount,f.bank_txn_id,COALESCE(b.ref_no,''),COALESCE(b.date,'')
		FROM deposit_fundings f LEFT JOIN bank_txns b ON b.id=f.bank_txn_id ORDER BY f.date, f.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Funding
	for rows.Next() {
		var f Funding
		var bt sql.NullInt64
		if err := rows.Scan(&f.ID, &f.DepositID, &f.Date, &f.Kind, &f.Amount, &bt, &f.BankRef, &f.BankDate); err != nil {
			return nil, err
		}
		if bt.Valid {
			f.BankTxnID = &bt.Int64
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// rebuildFundings re-creates the booked transfers for a deposit from its
// current settings. Bank links are re-established by MatchFundings.
func (a *App) rebuildFundings(d Deposit) error {
	if _, err := a.DB.Exec(`DELETE FROM other_income WHERE id IN (SELECT income_id FROM deposit_fundings WHERE deposit_id=? AND income_id IS NOT NULL)`, d.ID); err != nil {
		return err
	}
	if _, err := a.DB.Exec(`DELETE FROM deposit_fundings WHERE deposit_id=?`, d.ID); err != nil {
		return err
	}
	ins := func(date, kind string, amount int64) (int64, error) {
		return a.DB.InsertID(`INSERT INTO deposit_fundings(deposit_id,date,kind,amount) VALUES(?,?,?,?)`, d.ID, date, kind, amount)
	}
	if d.FromSavings {
		if d.Kind == "FD" {
			if _, err := ins(d.OpenedOn, "open", d.Principal); err != nil {
				return err
			}
		} else {
			o, _ := ParseDate(d.OpenedOn)
			for i := 0; i < max(1, d.TenureMonths); i++ {
				date := o.AddDate(0, i, 0).Format(DateFmt)
				if d.ClosedOn != nil && date >= *d.ClosedOn {
					break
				}
				if _, err := ins(date, "installment", d.Installment); err != nil {
					return err
				}
			}
		}
	}
	if d.ClosedOn != nil && d.ToSavings {
		c, _ := ParseDate(*d.ClosedOn)
		book := d
		book.valueOn(c.AddDate(0, 0, -1).Format(DateFmt)) // value just before closing
		amount := d.ClosingAmount
		if amount == 0 {
			amount = book.Value
		}
		fid, err := ins(*d.ClosedOn, "close", -amount)
		if err != nil {
			return err
		}
		// anything received above the amount deposited is interest income
		if interest := amount - book.Value; interest > 0 {
			iid, err := a.DB.InsertID(`INSERT INTO other_income(date,category,description,amount) VALUES(?,?,?,?)`,
				*d.ClosedOn, "FD/RD interest", "Interest on "+book.Label, interest)
			if err != nil {
				return err
			}
			if _, err := a.DB.Exec(`UPDATE deposit_fundings SET income_id=? WHERE id=?`, iid, fid); err != nil {
				return err
			}
		}
	}
	return nil
}

// MatchFundings links booked transfers to the bank lines that carry them:
// same amount and direction, dated 3 days before to 10 days after. Linked lines
// are marked as own-account transfers.
func (a *App) MatchFundings() error {
	rows, err := a.DB.Query(`SELECT id,date,amount FROM deposit_fundings WHERE bank_txn_id IS NULL ORDER BY date, id`)
	if err != nil {
		return err
	}
	type pend struct {
		id     int64
		date   string
		amount int64
	}
	var ps []pend
	for rows.Next() {
		var p pend
		if err := rows.Scan(&p.id, &p.date, &p.amount); err != nil {
			rows.Close()
			return err
		}
		ps = append(ps, p)
	}
	rows.Close()
	for _, p := range ps {
		col, amt := "debit", p.amount
		if p.amount < 0 {
			col, amt = "credit", -p.amount
		}
		var tid int64
		var tdate string
		err := a.DB.QueryRow(`SELECT id,date FROM bank_txns WHERE `+col+`=?
			AND `+a.DB.Days("date")+` BETWEEN `+a.DB.Days("?")+`-3 AND `+a.DB.Days("?")+`+10
			AND (status='review' OR kind='transfer')
			AND id NOT IN (SELECT bank_txn_id FROM deposit_fundings WHERE bank_txn_id IS NOT NULL)
			ORDER BY ABS(`+a.DB.Days("date")+`-`+a.DB.Days("?")+`) LIMIT 1`, amt, p.date, p.date, p.date).Scan(&tid, &tdate)
		if err != nil {
			continue // not on a statement yet
		}
		if _, err := a.DB.Exec(`UPDATE deposit_fundings SET bank_txn_id=? WHERE id=?`, tid, p.id); err != nil {
			return err
		}
		status := "matched"
		if a.beforeCutover(tdate) {
			status = "historical"
		}
		if err := a.setMatch(tid, status, "transfer", nil, "FD/RD", "", 100, []string{"savings ⇄ FD/RD transfer booked in Deposits"}); err != nil {
			return err
		}
	}
	return nil
}

// PendingDepositTransfers: net amount booked out of savings into deposits by
// `on` that the imported statements don't show yet as of that date.
func (a *App) PendingDepositTransfers(on string) int64 {
	var v int64
	a.DB.QueryRow(`SELECT COALESCE(SUM(f.amount),0) FROM deposit_fundings f LEFT JOIN bank_txns b ON b.id=f.bank_txn_id
		WHERE f.date<=? AND (f.bank_txn_id IS NULL OR b.date>?)`, on, on).Scan(&v)
	return v
}

var (
	depositToken  = regexp.MustCompile(`(^|[^A-Z0-9])(FD|RD|TD)([^A-Z0-9]|$)`)
	depositPhrase = regexp.MustCompile(`FIXED DEP|RECURRING DEP|TERM DEP|SWEEP|AUTO RENEW|DEPOSIT (BOOK|CLOS|MATUR)`)
)

// isDepositTransfer recognises sweeps into / out of FD and RD accounts in a
// narration, so they are treated as own-account transfers (not expenses or
// income). Tokens must stand alone so IFSC codes (FDRL…) or "3RD" don't match.
func isDepositTransfer(narration string) bool {
	u := strings.ToUpper(narration)
	return depositToken.MatchString(u) || depositPhrase.MatchString(u)
}
