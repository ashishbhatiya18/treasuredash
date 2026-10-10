package app

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"treasuredash/internal/bank"
)

// Default rules for outgoing payments, learned from the treasury sheet's payees.
// Any other payee is recognised when its name appears in the narration
// (see payeeFromNames).
var payeeRules = []struct{ keywords, payee string }{
	{"BANGALOREEL,BESCOM", "Bescom"},
	{"SECURITY,ONE INDIA", "One India FMS"},
	{"STP,KAVERY", "Kavery STP Maintenance"},
	{"SCHINDLER", "Schindler"},
	{"KIRLOSKAR", "Kirloskar Care"},
}

type BankTxn struct {
	ID          int64     `json:"id"`
	StatementID int64     `json:"statement_id"`
	Date        string    `json:"date"`
	Narration   string    `json:"narration"`
	RefNo       string    `json:"ref_no"`
	Debit       int64     `json:"debit"`
	Credit      int64     `json:"credit"`
	Balance     int64     `json:"balance"`
	PayerKey    string    `json:"payer_key"`
	PayerName   string    `json:"payer_name"`
	Status      string    `json:"status"`
	Kind        string    `json:"kind"`
	Flat        *string   `json:"flat"`
	Payee       string    `json:"payee"`
	Category    string    `json:"category"`
	Confidence  int       `json:"confidence"`
	Reasons     []string  `json:"reasons"`
	ExpenseIDs  []int64   `json:"expense_ids"`
	Postings    []Posting `json:"postings"` // flats credited and the charges each credit settled
}

type StatementSummary struct {
	ID         int64  `json:"id"`
	Filename   string `json:"filename"`
	AccountNo  string `json:"account_no"`
	From       string `json:"from_date"`
	To         string `json:"to_date"`
	Opening    int64  `json:"opening"`
	Closing    int64  `json:"closing"`
	UploadedAt string `json:"uploaded_at"`
	Txns       int    `json:"txns"`
	New        int    `json:"new"`
	// set when this upload recalculated the sinking-fund opening
	Calibration string `json:"calibration,omitempty"`
	Review      int    `json:"review"`
	// integrity: transactions on record for the statement's dates must take the
	// opening balance to the closing balance
	Credits         int64    `json:"credits"`
	Debits          int64    `json:"debits"`
	CreditsResolved int64    `json:"credits_resolved"` // assigned / ignored (not waiting for review)
	DebitsResolved  int64    `json:"debits_resolved"`
	Balanced        bool     `json:"balanced"`
	Issues          []string `json:"issues"`
}

func (a *App) Statements() ([]StatementSummary, error) {
	rows, err := a.DB.Query(`SELECT s.id,s.filename,s.account_no,COALESCE(s.from_date,''),COALESCE(s.to_date,''),COALESCE(s.opening,0),COALESCE(s.closing,0),s.uploaded_at,
		(SELECT COUNT(*) FROM bank_txns t WHERE t.statement_id=s.id),
		(SELECT COUNT(*) FROM bank_txns t WHERE t.statement_id=s.id AND t.status='review')
		FROM bank_statements s ORDER BY s.to_date DESC, s.id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []StatementSummary{}
	for rows.Next() {
		var s StatementSummary
		if err := rows.Scan(&s.ID, &s.Filename, &s.AccountNo, &s.From, &s.To, &s.Opening, &s.Closing, &s.UploadedAt, &s.Txns, &s.Review); err != nil {
			return nil, err
		}
		s.New = s.Txns
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	for i := range out {
		st := &out[i]
		st.Issues = []string{}
		a.DB.QueryRow(`SELECT COALESCE(SUM(credit),0),COALESCE(SUM(debit),0),
			COALESCE(SUM(CASE WHEN status<>'review' THEN credit END),0),COALESCE(SUM(CASE WHEN status<>'review' THEN debit END),0)
			FROM bank_txns WHERE date BETWEEN ? AND ?`, st.From, st.To).Scan(&st.Credits, &st.Debits, &st.CreditsResolved, &st.DebitsResolved)
		st.Balanced = st.Opening+st.Credits-st.Debits == st.Closing
		if !st.Balanced {
			st.Issues = append(st.Issues, fmt.Sprintf("opening ₹%s + credits − debits = ₹%s, but closing is ₹%s",
				Rupees(st.Opening), Rupees(st.Opening+st.Credits-st.Debits), Rupees(st.Closing)))
		}
	}
	// continuity between consecutive statements (out is newest first)
	for i := len(out) - 1; i > 0; i-- {
		prev, next := out[i], &out[i-1]
		pt, _ := ParseDate(prev.To)
		nf, _ := ParseDate(next.From)
		switch {
		case nf.After(pt.AddDate(0, 0, 1)):
			next.Issues = append(next.Issues, fmt.Sprintf("gap: no statement covers %s – %s", fmtDate(pt.AddDate(0, 0, 1).Format(DateFmt)), fmtDate(nf.AddDate(0, 0, -1).Format(DateFmt))))
		case nf.Equal(pt.AddDate(0, 0, 1)) && next.Opening != prev.Closing:
			next.Issues = append(next.Issues, fmt.Sprintf("opening ₹%s does not match previous closing ₹%s", Rupees(next.Opening), Rupees(prev.Closing)))
		}
	}
	return out, nil
}

// ImportStatement parses an HDFC PDF, stores new transactions (overlapping
// statements are de-duplicated) and runs auto-matching on them.
func (a *App) ImportStatement(filename string, pdf []byte) (*StatementSummary, error) {
	sum := sha256.Sum256(pdf)
	hash := hex.EncodeToString(sum[:])
	var exists int
	a.DB.QueryRow(`SELECT COUNT(*) FROM bank_statements WHERE sha256=?`, hash).Scan(&exists)
	if exists > 0 {
		return nil, fmt.Errorf("this statement file was already imported")
	}
	text, err := bank.PDFToText(pdf)
	if err != nil {
		return nil, err
	}
	st, err := bank.ParseHDFC(text)
	if err != nil && st == nil {
		return nil, err
	}
	return a.importParsed(filename, hash, st, err) // a count mismatch is imported with a warning
}

// importParsed stores a parsed statement. Statements may overlap (or repeat
// with a different file): each transaction is unique on date, reference,
// amounts and running balance, so lines already on record are skipped and only
// new ones are matched and posted.
func (a *App) importParsed(filename, hash string, st *bank.Statement, warn error) (*StatementSummary, error) {
	a.mu.Lock()
	sid, err := a.DB.InsertID(`INSERT INTO bank_statements(filename,sha256,account_no,from_date,to_date,opening,closing) VALUES(?,?,?,?,?,?,?)`,
		filename, hash, st.AccountNo, st.From, st.To, st.Opening, st.Closing)
	if err != nil {
		a.mu.Unlock()
		return nil, err
	}

	var newIDs []int64
	for _, t := range st.Txns {
		p := bank.ExtractPayer(t.Narration)
		var id int64
		err := a.DB.QueryRow(`INSERT INTO bank_txns(statement_id,date,value_date,narration,ref_no,debit,credit,balance,payer_key,payer_name)
			VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT DO NOTHING RETURNING id`, sid, t.Date, t.ValueDate, t.Narration, t.RefNo, t.Debit, t.Credit, t.Balance, p.Key, p.Name).Scan(&id)
		if err == sql.ErrNoRows {
			continue // already imported from an overlapping statement
		}
		if err != nil {
			a.mu.Unlock()
			return nil, err
		}
		newIDs = append(newIDs, id)
	}
	a.mu.Unlock()
	for _, id := range newIDs {
		if err := a.autoMatch(id); err != nil {
			return nil, err
		}
	}
	if err := a.MatchFundings(); err != nil {
		return nil, err
	}
	if _, err := a.RegisterBookedDeposits(); err != nil {
		return nil, err
	}
	if _, err := a.ApplyLateFees(Today()); err != nil {
		return nil, err
	}
	calibration, err := a.AutoCalibrateSinkingFund()
	if err != nil {
		return nil, err
	}
	ss, err := a.Statements()
	if err != nil {
		return nil, err
	}
	for _, s := range ss {
		if s.ID == sid {
			s.New = len(newIDs)
			s.Calibration = calibration
			if warn != nil {
				return &s, fmt.Errorf("imported with warning: %v", warn)
			}
			return &s, nil
		}
	}
	return nil, fmt.Errorf("statement vanished")
}

func (a *App) txn(id int64) (BankTxn, error) {
	if id <= 0 {
		return BankTxn{}, fmt.Errorf("invalid transaction id")
	}
	ts, err := a.BankTxns(TxnFilter{ID: id})
	if err != nil {
		return BankTxn{}, err
	}
	if len(ts) == 0 {
		return BankTxn{}, fmt.Errorf("transaction %d not found", id)
	}
	return ts[0], nil
}

type TxnFilter struct {
	ID          int64
	StatementID int64
	Status      string
	Dir         string // in/out
	Flat        string // credited to this flat (directly or via a split)
	Q           string // narration / ref / payer / payee / amount
	From, To    string
}

func (a *App) BankTxns(f TxnFilter) ([]BankTxn, error) {
	q := `SELECT id,statement_id,date,narration,ref_no,debit,credit,balance,payer_key,payer_name,status,kind,flat,payee,category,confidence,reasons,
		(SELECT ` + a.DB.GroupConcat("e.id") + ` FROM expenses e WHERE e.bank_txn_id=bank_txns.id)
		FROM bank_txns WHERE 1=1`
	var args []any
	if f.ID != 0 {
		q += ` AND id=?`
		args = append(args, f.ID)
	}
	if f.StatementID != 0 {
		q += ` AND statement_id=?`
		args = append(args, f.StatementID)
	}
	if f.Status != "" {
		q += ` AND status=?`
		args = append(args, f.Status)
	}
	if f.Flat != "" {
		q += ` AND (flat=? OR id IN (SELECT bank_txn_id FROM ledger WHERE flat=? AND bank_txn_id IS NOT NULL))`
		args = append(args, f.Flat, f.Flat)
	}
	if f.From != "" {
		q += ` AND date>=?`
		args = append(args, f.From)
	}
	if f.To != "" {
		q += ` AND date<=?`
		args = append(args, f.To)
	}
	if f.Q != "" {
		like := "%" + f.Q + "%"
		l := a.DB.Like()
		q += ` AND (narration ` + l + ` ? OR ref_no ` + l + ` ? OR payer_name ` + l + ` ? OR payee ` + l + ` ? OR flat=? OR CAST(credit/100 AS TEXT)=? OR CAST(debit/100 AS TEXT)=?)`
		amt := strings.NewReplacer(",", "", "₹", "").Replace(strings.TrimSpace(f.Q))
		args = append(args, like, like, like, like, strings.ToUpper(f.Q), amt, amt)
	}
	switch f.Dir {
	case "in":
		q += ` AND credit>0`
	case "out":
		q += ` AND debit>0`
	}
	q += ` ORDER BY date DESC, id DESC`
	rows, err := a.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []BankTxn{}
	for rows.Next() {
		var t BankTxn
		var flat, exp sql.NullString
		var reasons string
		if err := rows.Scan(&t.ID, &t.StatementID, &t.Date, &t.Narration, &t.RefNo, &t.Debit, &t.Credit, &t.Balance, &t.PayerKey, &t.PayerName,
			&t.Status, &t.Kind, &flat, &t.Payee, &t.Category, &t.Confidence, &reasons, &exp); err != nil {
			return nil, err
		}
		if flat.Valid {
			t.Flat = &flat.String
		}
		json.Unmarshal([]byte(reasons), &t.Reasons)
		t.ExpenseIDs = []int64{}
		for _, s := range strings.Split(exp.String, ",") {
			var id int64
			if _, err := fmt.Sscan(s, &id); err == nil {
				t.ExpenseIDs = append(t.ExpenseIDs, id)
			}
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (a *App) aliases() (flats map[string]string, payees map[string][2]string, err error) {
	rows, err := a.DB.Query(`SELECT key,COALESCE(flat,''),payee,category FROM payer_aliases`)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	flats, payees = map[string]string{}, map[string][2]string{}
	for rows.Next() {
		var k, f, p, c string
		if err := rows.Scan(&k, &f, &p, &c); err != nil {
			return nil, nil, err
		}
		if f != "" {
			flats[k] = f
		}
		if p != "" {
			payees[k] = [2]string{p, c}
		}
	}
	return flats, payees, rows.Err()
}

func (a *App) learn(key, flat, payee, category string) error {
	if key == "" {
		return nil
	}
	var fl any
	if flat != "" {
		fl = flat
	}
	_, err := a.DB.Exec(`INSERT INTO payer_aliases(key,flat,payee,category) VALUES(?,?,?,?)
		ON CONFLICT(key) DO UPDATE SET flat=excluded.flat, payee=excluded.payee, category=excluded.category`, key, fl, payee, category)
	return err
}

// flatInfos returns each flat's standard charge and balance on a given date.
func (a *App) flatInfos(date string) ([]bank.FlatInfo, error) {
	rate := a.SettingFloat("rate_per_sqft")
	rows, err := a.DB.Query(`SELECT f.code,f.owner,f.area_sqft,
		COALESCE((SELECT SUM(amount) FROM ledger l WHERE l.flat=f.code AND l.date<=?),0) FROM flats f`, date)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []bank.FlatInfo
	for rows.Next() {
		var fi bank.FlatInfo
		var area float64
		if err := rows.Scan(&fi.Code, &fi.Owner, &area, &fi.Outstanding); err != nil {
			return nil, err
		}
		fi.Quarterly = QuarterlyCharge(area, rate)
		out = append(out, fi)
	}
	return out, rows.Err()
}

func (a *App) setMatch(id int64, status, kind string, flat *string, payee, category string, conf int, reasons []string) error {
	rj, _ := json.Marshal(reasons)
	_, err := a.DB.Exec(`UPDATE bank_txns SET status=?,kind=?,flat=?,payee=?,category=?,confidence=?,reasons=? WHERE id=?`,
		status, kind, flat, payee, category, conf, string(rj), id)
	return err
}

func (a *App) beforeCutover(date string) bool { return date < a.Setting("cutover_date") }

// autoMatch classifies a freshly imported transaction.
func (a *App) autoMatch(id int64) error {
	t, err := a.txn(id)
	if err != nil {
		return err
	}
	historical := a.beforeCutover(t.Date)
	matchedStatus := "matched"
	if historical {
		matchedStatus = "historical"
	}
	flatAliases, payeeAliases, err := a.aliases()
	if err != nil {
		return err
	}
	u := strings.ToUpper(t.Narration)

	// money moving between savings and FD/RD stays the association's: not income, not expense
	if isDepositTransfer(t.Narration) {
		dir := "to FD/RD"
		if t.Credit > 0 {
			dir = "from FD/RD (maturity / closure)"
		}
		return a.setMatch(id, matchedStatus, "transfer", nil, "FD/RD", "", 90, []string{"deposit transfer " + dir + " – update Deposits if this opened or closed one"})
	}

	if t.Credit > 0 {
		if strings.Contains(u, "INTEREST PAID") || strings.HasPrefix(u, "INT.PD") || strings.HasPrefix(u, "CREDIT INTEREST") {
			if err := a.setMatch(id, matchedStatus, "other_income", nil, "", "Bank interest", 100, []string{"bank interest credit"}); err != nil {
				return err
			}
			if !historical {
				return a.SaveOtherIncome(OtherIncome{Date: t.Date, Category: "Bank interest", Description: t.Narration, Amount: t.Credit, BankTxnID: &t.ID})
			}
			return nil
		}
		fis, err := a.flatInfos(t.Date)
		if err != nil {
			return err
		}
		r := bank.MatchCredit(t.Narration, t.Credit, bank.Payer{Key: t.PayerKey, Name: t.PayerName}, fis, flatAliases)
		var flat *string
		if r.Flat != "" {
			flat = &r.Flat
		}
		if !r.Auto {
			status := "review"
			if historical {
				status = "historical"
				r.Reasons = append(r.Reasons, "before cut-over: already counted in imported balances")
			}
			return a.setMatch(id, status, "maintenance", flat, "", "", r.Confidence, r.Reasons)
		}
		if historical {
			r.Reasons = append(r.Reasons, "before cut-over: already counted in imported balances")
		}
		var warn []string
		if !historical {
			warn = a.paymentWarnings(t, r.Flat)
			r.Reasons = append(r.Reasons, warn...)
		}
		if len(warn) > 0 {
			// a likely duplicate needs a human look before it is posted
			for _, w := range warn {
				if strings.HasPrefix(w, "possible duplicate") {
					return a.setMatch(id, "review", "maintenance", flat, "", "", r.Confidence, r.Reasons)
				}
			}
		}
		if err := a.setMatch(id, matchedStatus, "maintenance", flat, "", "", r.Confidence, r.Reasons); err != nil {
			return err
		}
		if err := a.learn(t.PayerKey, r.Flat, "", ""); err != nil {
			return err
		}
		if !historical {
			return a.postPayment(t, r.Flat)
		}
		return nil
	}

	// outgoing
	payee, category, why := "", "", ""
	if pc, ok := payeeAliases[t.PayerKey]; ok && t.PayerKey != "" {
		payee, category, why = pc[0], pc[1], "payee previously confirmed for "+t.PayerKey
	} else {
		for _, rule := range payeeRules {
			for _, kw := range strings.Split(rule.keywords, ",") {
				if strings.Contains(u, kw) {
					payee, why = rule.payee, "narration mentions "+kw
					break
				}
			}
			if payee != "" {
				break
			}
		}
	}
	if payee == "" {
		payee, why = a.payeeFromNames(u)
	}
	reasons := []string{}
	if why != "" {
		reasons = append(reasons, why)
	}
	// look for a recorded bill of the same amount (±45 days) not yet linked
	var expID int64
	err = a.DB.QueryRow(`SELECT id FROM expenses WHERE amount=? AND bank_txn_id IS NULL
		AND `+a.DB.Days("date")+` BETWEEN `+a.DB.Days("?")+`-45 AND `+a.DB.Days("?")+`+15
		AND (?='' OR payee=?) ORDER BY ABS(`+a.DB.Days("date")+`-`+a.DB.Days("?")+`) LIMIT 1`,
		t.Debit, t.Date, t.Date, payee, payee, t.Date).Scan(&expID)
	if err == nil {
		var desc, ep, ec string
		a.DB.QueryRow(`SELECT description,payee,category FROM expenses WHERE id=?`, expID).Scan(&desc, &ep, &ec)
		if payee == "" {
			payee = ep
		}
		if category == "" {
			category = ec
		}
		reasons = append(reasons, fmt.Sprintf("matches recorded bill #%d %q of the same amount", expID, desc))
		if _, err := a.DB.Exec(`UPDATE expenses SET bank_txn_id=? WHERE id=?`, id, expID); err != nil {
			return err
		}
	} else if payee != "" {
		reasons = append(reasons, "no single bill of this amount – likely settles several bills (e.g. petty-cash reimbursement)")
	}
	status, conf := "review", 0
	if payee != "" {
		status, conf = matchedStatus, 80
		if expID != 0 {
			conf = 100
		}
	} else {
		reasons = append(reasons, "unknown payee – assign one so future payments are recognised")
		if historical {
			status = "historical"
		}
	}
	return a.setMatch(id, status, "expense", nil, payee, category, conf, reasons)
}

// paymentWarnings flags receipts that need attention: a flat with nothing due
// (the money becomes an advance) or a second bank payment of the same amount
// from the same flat within a week.
func (a *App) paymentWarnings(t BankTxn, flat string) []string {
	var out []string
	var bal int64
	a.DB.QueryRow(`SELECT COALESCE(SUM(amount),0) FROM ledger WHERE flat=? AND date<=? AND (bank_txn_id IS NULL OR bank_txn_id<>?)`, flat, t.Date, t.ID).Scan(&bal)
	if bal <= 0 {
		out = append(out, fmt.Sprintf("%s had nothing due on %s – this will be held as an advance", flat, fmtDate(t.Date)))
	} else if t.Credit > bal {
		out = append(out, fmt.Sprintf("₹%s more than %s owed – the excess is held as an advance", Rupees(t.Credit-bal), flat))
	}
	var dup string
	err := a.DB.QueryRow(`SELECT b.date FROM bank_txns b WHERE b.id<>? AND b.credit=? AND b.status IN ('matched','historical')
		AND b.kind='maintenance' AND b.flat=? AND ABS(`+a.DB.Days("b.date")+`-`+a.DB.Days("?")+`)<=7 LIMIT 1`, t.ID, t.Credit, flat, t.Date).Scan(&dup)
	if err == nil {
		out = append(out, fmt.Sprintf("possible duplicate: %s already paid ₹%s on %s", flat, Rupees(t.Credit), fmtDate(dup)))
	}
	return out
}

// payeeFromNames matches a debit to a payee already used in expenses when the
// payee's first name (4+ letters) appears in the narration, e.g. a caretaker
// reimbursed by name.
func (a *App) payeeFromNames(upperNarration string) (string, string) {
	names, err := a.Payees()
	if err != nil {
		return "", ""
	}
	for _, n := range names {
		f := strings.Fields(strings.ToUpper(n))
		if len(f) == 0 || len(f[0]) < 4 {
			continue
		}
		if strings.Contains(upperNarration, f[0]) {
			return n, "narration mentions payee " + n
		}
	}
	return "", ""
}

func (a *App) postPayment(t BankTxn, flat string) error {
	if _, err := a.DB.Exec(`DELETE FROM ledger WHERE bank_txn_id=?`, t.ID); err != nil {
		return err
	}
	_, err := a.DB.Exec(`INSERT INTO ledger(flat,date,kind,amount,description,mode,bank_txn_id) VALUES(?,?,'payment',?,?,'bank',?)`,
		flat, t.Date, -t.Credit, "Bank: "+t.Narration, t.ID)
	return err
}

type Split struct {
	Flat   string  `json:"flat"`
	Amount float64 `json:"amount"` // rupees
}

type Assignment struct {
	Kind           string  `json:"kind"` // maintenance / other_income / expense / transfer / ignore
	Flat           string  `json:"flat"`
	Splits         []Split `json:"splits"` // maintenance paid for several flats in one transfer
	Payee          string  `json:"payee"`
	Category       string  `json:"category"`
	IncomeCategory string  `json:"income_category"`
	ExpenseID      int64   `json:"expense_id"`     // link an existing bill
	CreateExpense  bool    `json:"create_expense"` // record the debit itself as a bill
	Description    string  `json:"description"`
	Remember       bool    `json:"remember"`
}

// Assign applies a reviewer's decision to a transaction, replacing any earlier one.
func (a *App) Assign(id int64, as Assignment) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	t, err := a.txn(id)
	if err != nil {
		return err
	}
	if err := a.clearPostings(id); err != nil {
		return err
	}
	historical := a.beforeCutover(t.Date)
	status := "matched"
	if historical {
		status = "historical"
	}
	reasons := []string{"confirmed manually"}
	switch as.Kind {
	case "maintenance":
		if t.Credit == 0 {
			return fmt.Errorf("only incoming payments can be maintenance")
		}
		splits := as.Splits
		if len(splits) == 0 {
			splits = []Split{{Flat: as.Flat, Amount: float64(t.Credit) / 100}}
		}
		var total int64
		for _, sp := range splits {
			if _, err := a.Flat(sp.Flat); err != nil {
				return err
			}
			if Paise(sp.Amount) <= 0 {
				return fmt.Errorf("split amounts must be positive")
			}
			total += Paise(sp.Amount)
		}
		if total != t.Credit {
			return fmt.Errorf("split amounts add up to ₹%s but the credit is ₹%s", Rupees(total), Rupees(t.Credit))
		}
		primary := splits[0].Flat
		if len(splits) > 1 {
			var parts []string
			for _, sp := range splits {
				parts = append(parts, fmt.Sprintf("%s ₹%s", sp.Flat, Rupees(Paise(sp.Amount))))
			}
			reasons = append(reasons, "split: "+strings.Join(parts, ", "))
		}
		if err := a.setMatch(id, status, "maintenance", &primary, "", "", 100, reasons); err != nil {
			return err
		}
		if as.Remember && len(splits) == 1 {
			if err := a.learn(t.PayerKey, primary, "", ""); err != nil {
				return err
			}
		}
		if !historical {
			if _, err := a.DB.Exec(`DELETE FROM ledger WHERE bank_txn_id=?`, t.ID); err != nil {
				return err
			}
			for _, sp := range splits {
				if _, err := a.DB.Exec(`INSERT INTO ledger(flat,date,kind,amount,description,mode,bank_txn_id) VALUES(?,?,'payment',?,?,'bank',?)`,
					sp.Flat, t.Date, -Paise(sp.Amount), "Bank: "+t.Narration, t.ID); err != nil {
					return err
				}
			}
		}
	case "other_income":
		if t.Credit == 0 {
			return fmt.Errorf("only incoming payments can be income")
		}
		if as.IncomeCategory == "" {
			as.IncomeCategory = "Other"
		}
		if err := a.setMatch(id, status, "other_income", nil, "", as.IncomeCategory, 100, reasons); err != nil {
			return err
		}
		if !historical {
			var fl *string
			if as.Flat != "" {
				fl = &as.Flat
			}
			desc := as.Description
			if desc == "" {
				desc = t.Narration
			}
			if err := a.SaveOtherIncome(OtherIncome{Date: t.Date, Category: as.IncomeCategory, Description: desc, Amount: t.Credit, Flat: fl, BankTxnID: &t.ID}); err != nil {
				return err
			}
		}
	case "expense":
		if t.Debit == 0 {
			return fmt.Errorf("only outgoing payments can be expenses")
		}
		if as.Payee == "" {
			return fmt.Errorf("payee required")
		}
		if err := a.setMatch(id, status, "expense", nil, as.Payee, as.Category, 100, reasons); err != nil {
			return err
		}
		if as.ExpenseID != 0 {
			if _, err := a.DB.Exec(`UPDATE expenses SET bank_txn_id=? WHERE id=?`, id, as.ExpenseID); err != nil {
				return err
			}
		}
		if as.CreateExpense {
			desc := as.Description
			if desc == "" {
				desc = as.Payee + " payment"
			}
			if _, err := a.SaveExpense(Expense{Date: t.Date, Description: desc, Category: as.Category, Payee: as.Payee, Amount: t.Debit, BankTxnID: &t.ID, Notes: "Created from bank statement"}); err != nil {
				return err
			}
		}
		if as.Remember {
			if err := a.learn(t.PayerKey, "", as.Payee, as.Category); err != nil {
				return err
			}
		}
	case "transfer":
		if err := a.setMatch(id, status, "transfer", nil, as.Payee, "", 100, []string{"own-account transfer / non P&L"}); err != nil {
			return err
		}
	case "ignore":
		if err := a.setMatch(id, "ignored", "", nil, "", "", 0, []string{"ignored"}); err != nil {
			return err
		}
	case "review":
		if err := a.setMatch(id, "review", t.Kind, t.Flat, t.Payee, t.Category, 0, []string{"reopened for review"}); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown kind %q", as.Kind)
	}
	_, err = a.ApplyLateFees(Today())
	return err
}

func (a *App) clearPostings(id int64) error {
	for _, q := range []string{
		`DELETE FROM ledger WHERE bank_txn_id=?`,
		`DELETE FROM other_income WHERE bank_txn_id=?`,
		`DELETE FROM expenses WHERE bank_txn_id=? AND notes='Created from bank statement'`,
		`UPDATE expenses SET bank_txn_id=NULL WHERE bank_txn_id=?`,
	} {
		if _, err := a.DB.Exec(q, id); err != nil {
			return err
		}
	}
	return nil
}

// Rematch re-runs auto-matching for everything still waiting for review,
// e.g. after adding flats, owner names or remembered payers.
func (a *App) Rematch() (int, error) {
	ts, err := a.BankTxns(TxnFilter{Status: "review"})
	if err != nil {
		return 0, err
	}
	n := 0
	for _, t := range ts {
		if err := a.autoMatch(t.ID); err != nil {
			return n, err
		}
		u, _ := a.txn(t.ID)
		if u.Status != "review" {
			n++
		}
	}
	return n, nil
}

// LatestBankBalance is the closing balance on the newest imported transaction.
func (a *App) LatestBankBalance() (int64, string) {
	var bal int64
	var date string
	a.DB.QueryRow(`SELECT balance,date FROM bank_txns ORDER BY date DESC, id DESC LIMIT 1`).Scan(&bal, &date)
	return bal, date
}

// BankBalanceOn is the closing balance at the end of `date` according to the
// imported statements. ok is false when no statement covers that date.
func (a *App) BankBalanceOn(date string) (bal int64, on string, ok bool) {
	err := a.DB.QueryRow(`SELECT balance,date FROM bank_txns WHERE date<=? ORDER BY date DESC, id DESC LIMIT 1`, date).Scan(&bal, &on)
	if err == nil {
		// only trust it if a statement actually spans the requested date
		var n int
		a.DB.QueryRow(`SELECT COUNT(*) FROM bank_statements WHERE from_date<=? AND to_date>=?`, date, date).Scan(&n)
		if n > 0 {
			return bal, date, true
		}
		return bal, on, true
	}
	// before the first transaction: use the opening balance of a statement covering the date
	err = a.DB.QueryRow(`SELECT opening FROM bank_statements WHERE from_date<=? AND to_date>=? ORDER BY from_date LIMIT 1`, date, date).Scan(&bal)
	return bal, date, err == nil
}
