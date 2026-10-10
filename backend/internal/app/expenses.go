package app

import (
	"assofin/internal/db"
	"database/sql"
	"fmt"
	"sort"
	"strings"
)

type Category struct {
	Name     string `json:"name"`
	InPnL    bool   `json:"in_pnl"`
	Keywords string `json:"keywords"`
	Priority int    `json:"priority"`
}

// Seed rules derived from the particulars already used in the treasury sheet.
// Lower priority wins, so specific phrases sit above broad ones.
var defaultCategories = []Category{
	{"Security", true, "security fms,security guard,security services,security mobile,security gate", 10},
	{"Pest Control", true, "fogging,snake,pest", 15},
	{"DG & Diesel", true, "diesel,dg service,dg -,kirloskar,generator", 20},
	{"Electricity", true, "bescom,electricity bill,light bill,electricity", 25},
	{"Lift AMC", true, "lift,schindler,elevator", 30},
	{"STP Maintenance", true, "stp,sewage,kavery", 35},
	{"Housekeeping", true, "housekeeping,garbage,sansar,lizol,hygiene,jadu,rangoli,cleaning,phenyl", 40},
	{"Water", true, "water tanker,tanker,drinking water,water can,water", 65},
	{"Staff Salary", true, "salary,wages", 50},
	{"Events & Festivals", true, "republic,independence,diwali,pooja,puja,festival,celebration,snacks,gifts,arrangements", 55},
	{"Repairs & Maintenance", true, "plumbing,electrical,common area,repair,fix,tiling,pipe,transformer,light,bulb,elc box,acid wash,hose,vinyl,tap", 60},
	{"Admin & Misc", true, "mobile recharge,print,lamination,stationary,stationery,recharge,speaker,display", 70},
	{"Miscellaneous", true, "", 900},
	// Not part of P&L: petty-cash advances, member reimbursements, corrections.
	{"Adjustment (non P&L)", false, "advance,paid by,on hold,transferred to", 5},
}

func (a *App) Categories() ([]Category, error) {
	rows, err := a.DB.Query(`SELECT name,in_pnl,keywords,priority FROM categories ORDER BY priority,name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Category
	for rows.Next() {
		var c Category
		if err := rows.Scan(&c.Name, &c.InPnL, &c.Keywords, &c.Priority); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (a *App) SaveCategory(c Category) error {
	if strings.TrimSpace(c.Name) == "" {
		return fmt.Errorf("name required")
	}
	_, err := a.DB.Exec(`INSERT INTO categories(name,in_pnl,keywords,priority) VALUES(?,?,?,?)
		ON CONFLICT(name) DO UPDATE SET in_pnl=excluded.in_pnl, keywords=excluded.keywords, priority=excluded.priority`,
		c.Name, db.Bool(c.InPnL), c.Keywords, c.Priority)
	return err
}

// Categorize picks a category for free-text particulars using keyword rules.
func Categorize(cats []Category, text string, amount int64) string {
	t := strings.ToLower(text)
	if amount < 0 {
		return "Adjustment (non P&L)"
	}
	for _, c := range cats { // already sorted by priority
		for _, kw := range strings.Split(c.Keywords, ",") {
			kw = strings.TrimSpace(strings.ToLower(kw))
			if kw != "" && strings.Contains(t, kw) {
				return c.Name
			}
		}
	}
	return "Miscellaneous"
}

type Expense struct {
	ID          int64  `json:"id"`
	Date        string `json:"date"`
	Description string `json:"description"`
	Category    string `json:"category"`
	Payee       string `json:"payee"`
	Amount      int64  `json:"amount"`
	BankTxnID   *int64 `json:"bank_txn_id"`
	BillFile    string `json:"bill_file"`
	Notes       string `json:"notes"`
	Source      string `json:"source"`
}

type ExpenseFilter struct {
	From, To, Category, Q string
}

func (a *App) Expenses(f ExpenseFilter) ([]Expense, error) {
	q := `SELECT id,date,description,category,payee,amount,bank_txn_id,bill_file,notes,source FROM expenses WHERE 1=1`
	var args []any
	if f.From != "" {
		q += ` AND date>=?`
		args = append(args, f.From)
	}
	if f.To != "" {
		q += ` AND date<=?`
		args = append(args, f.To)
	}
	if f.Category != "" {
		q += ` AND category=?`
		args = append(args, f.Category)
	}
	if f.Q != "" {
		q += ` AND (description ` + a.DB.Like() + ` ? OR payee ` + a.DB.Like() + ` ?)`
		args = append(args, "%"+f.Q+"%", "%"+f.Q+"%")
	}
	q += ` ORDER BY date DESC, id DESC`
	rows, err := a.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Expense{}
	for rows.Next() {
		var e Expense
		var bt sql.NullInt64
		if err := rows.Scan(&e.ID, &e.Date, &e.Description, &e.Category, &e.Payee, &e.Amount, &bt, &e.BillFile, &e.Notes, &e.Source); err != nil {
			return nil, err
		}
		if bt.Valid {
			e.BankTxnID = &bt.Int64
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (a *App) SaveExpense(e Expense) (int64, error) {
	if _, err := ParseDate(e.Date); err != nil {
		return 0, fmt.Errorf("date must be YYYY-MM-DD")
	}
	if strings.TrimSpace(e.Description) == "" || e.Amount == 0 {
		return 0, fmt.Errorf("description and amount are required")
	}
	if e.Category == "" {
		cats, err := a.Categories()
		if err != nil {
			return 0, err
		}
		e.Category = Categorize(cats, e.Description+" "+e.Payee, e.Amount)
	}
	if e.Source == "" {
		e.Source = "app"
	}
	if e.ID == 0 {
		return a.DB.InsertID(`INSERT INTO expenses(date,description,category,payee,amount,bank_txn_id,bill_file,notes,source) VALUES(?,?,?,?,?,?,?,?,?)`,
			e.Date, e.Description, e.Category, e.Payee, e.Amount, e.BankTxnID, e.BillFile, e.Notes, e.Source)
	}
	_, err := a.DB.Exec(`UPDATE expenses SET date=?,description=?,category=?,payee=?,amount=?,bank_txn_id=?,bill_file=?,notes=? WHERE id=?`,
		e.Date, e.Description, e.Category, e.Payee, e.Amount, e.BankTxnID, e.BillFile, e.Notes, e.ID)
	return e.ID, err
}

func (a *App) DeleteExpense(id int64) error {
	_, err := a.DB.Exec(`DELETE FROM expenses WHERE id=?`, id)
	return err
}

func (a *App) Payees() ([]string, error) {
	rows, err := a.DB.Query(`SELECT payee FROM expenses WHERE payee<>'' GROUP BY payee ORDER BY COUNT(*) DESC, payee`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ---- other income ----------------------------------------------------------

type OtherIncome struct {
	ID          int64   `json:"id"`
	Date        string  `json:"date"`
	Category    string  `json:"category"`
	Description string  `json:"description"`
	Amount      int64   `json:"amount"`
	Flat        *string `json:"flat"`
	BankTxnID   *int64  `json:"bank_txn_id"`
}

var IncomeCategories = []string{"Bank interest", "FD/RD interest", "Move-in charges", "Hall / amenity charges", "Donation", "Other"}

func (a *App) OtherIncomes(from, to string) ([]OtherIncome, error) {
	rows, err := a.DB.Query(`SELECT id,date,category,description,amount,flat,bank_txn_id FROM other_income
		WHERE (?='' OR date>=?) AND (?='' OR date<=?) ORDER BY date DESC`, from, from, to, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []OtherIncome{}
	for rows.Next() {
		var o OtherIncome
		var fl sql.NullString
		var bt sql.NullInt64
		if err := rows.Scan(&o.ID, &o.Date, &o.Category, &o.Description, &o.Amount, &fl, &bt); err != nil {
			return nil, err
		}
		if fl.Valid {
			o.Flat = &fl.String
		}
		if bt.Valid {
			o.BankTxnID = &bt.Int64
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

func (a *App) SaveOtherIncome(o OtherIncome) error {
	if _, err := ParseDate(o.Date); err != nil || o.Amount <= 0 || o.Category == "" {
		return fmt.Errorf("date, category and positive amount are required")
	}
	_, err := a.DB.Exec(`INSERT INTO other_income(date,category,description,amount,flat,bank_txn_id) VALUES(?,?,?,?,?,?)`,
		o.Date, o.Category, o.Description, o.Amount, o.Flat, o.BankTxnID)
	return err
}

func (a *App) DeleteOtherIncome(id int64) error {
	_, err := a.DB.Exec(`DELETE FROM other_income WHERE id=? AND bank_txn_id IS NULL`, id)
	return err
}

// BillTemplate is a bill the treasurer has entered before, offered as autofill.
type BillTemplate struct {
	Description string `json:"description"`
	Payee       string `json:"payee"`
	Category    string `json:"category"`
	LastAmount  int64  `json:"last_amount"`
	Typical     bool   `json:"typical"` // the last few bills had the same amount, so it's safe to prefill
	Count       int    `json:"count"`
	LastDate    string `json:"last_date"`
}

// BillTemplates learns recurring bills from the last 18 months: one entry per
// particulars+payee (case-insensitive), most frequent first.
func (a *App) BillTemplates() ([]BillTemplate, error) {
	since := Today().AddDate(0, -18, 0).Format(DateFmt)
	rows, err := a.DB.Query(`SELECT description,payee,category,amount,date FROM expenses
		WHERE amount>0 AND date>=? ORDER BY date DESC, id DESC`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type agg struct {
		t       BillTemplate
		amounts []int64 // newest first
	}
	byKey := map[string]*agg{}
	var order []string
	for rows.Next() {
		var d, p, c, date string
		var amt int64
		if err := rows.Scan(&d, &p, &c, &amt, &date); err != nil {
			return nil, err
		}
		key := strings.ToLower(strings.Join(strings.Fields(d), " ")) + "|" + strings.ToLower(strings.TrimSpace(p))
		g, ok := byKey[key]
		if !ok { // newest row wins for spelling, category and amount
			g = &agg{t: BillTemplate{Description: strings.TrimSpace(d), Payee: strings.TrimSpace(p), Category: c, LastAmount: amt, LastDate: date}}
			byKey[key] = g
			order = append(order, key)
		}
		g.t.Count++
		g.amounts = append(g.amounts, amt)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]BillTemplate, 0, len(order))
	for _, k := range order {
		g := byKey[k]
		n := min(3, len(g.amounts))
		g.t.Typical = n >= 2
		for _, x := range g.amounts[:n] {
			if x != g.amounts[0] {
				g.t.Typical = false
			}
		}
		out = append(out, g.t)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].LastDate > out[j].LastDate
	})
	return out, nil
}
