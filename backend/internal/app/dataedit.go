package app

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"treasuredash/internal/db"

	"github.com/xuri/excelize/v2"
)

// Bulk editing. Every editable table is exposed as a "sheet" of rows: flats,
// quarters, dues & payments, expenses, other income and bank links. The same
// sheets are downloaded as an Excel workbook for offline editing and served as
// JSON to the online editor; both come back through PlanChanges (preview) and
// ApplyChanges.
//
// Each exported row carries a version stamp (_v) of its editable values, so an
// upload only touches rows the user actually changed, and refuses rows that
// were also changed in the portal after the download instead of reverting them.

type ColType string

const (
	ColText   ColType = "text"
	ColCode   ColType = "code" // flat code, upper-cased
	ColDate   ColType = "date"
	ColMoney  ColType = "money" // rupees
	ColNumber ColType = "number"
	ColEnum   ColType = "enum"
	ColBool   ColType = "bool" // yes / no
)

type Column struct {
	Key      string   `json:"key"`
	Header   string   `json:"header"`
	Type     ColType  `json:"type"`
	ReadOnly bool     `json:"read_only,omitempty"`
	Required bool     `json:"required,omitempty"`
	Options  []string `json:"options,omitempty"`
	Width    float64  `json:"width,omitempty"`
	Help     string   `json:"help,omitempty"`
}

type SheetSpec struct {
	Name      string   `json:"name"`
	Title     string   `json:"title"` // workbook tab name
	Help      string   `json:"help"`
	Key       string   `json:"key"`      // column identifying the row
	AutoKey   bool     `json:"auto_key"` // key is a database id: blank for new rows
	CanAdd    bool     `json:"can_add"`
	CanDelete bool     `json:"can_delete"`
	Columns   []Column `json:"columns"`
}

func (s SheetSpec) col(key string) (Column, bool) {
	for _, c := range s.Columns {
		if c.Key == key {
			return c, true
		}
	}
	return Column{}, false
}

// SheetRow is one row as exported.
type SheetRow struct {
	Values  map[string]string `json:"values"`
	Version string            `json:"_v"`
	Locked  string            `json:"locked,omitempty"` // why the row can't be changed here
}

type SheetData struct {
	SheetSpec
	Rows []SheetRow `json:"rows"`
}

// InRow is one row sent back for saving.
type InRow struct {
	Row     int               `json:"row"` // spreadsheet row / grid position, for messages
	Values  map[string]string `json:"values"`
	Version string            `json:"_v"`
	Delete  bool              `json:"delete"`
}

// Changeset maps a sheet name to its edited rows.
type Changeset map[string][]InRow

type FieldChange struct {
	Column string `json:"column"`
	From   string `json:"from"`
	To     string `json:"to"`
}

type Change struct {
	Sheet    string        `json:"sheet"`
	Row      int           `json:"row"`
	Action   string        `json:"action"` // add / update / delete
	Key      string        `json:"key"`
	Label    string        `json:"label"`
	Fields   []FieldChange `json:"fields,omitempty"`
	Error    string        `json:"error,omitempty"`
	Warnings []string      `json:"warnings,omitempty"`

	order int
	apply func(tx *db.Tx) error // inside the database transaction
	after func() error          // after commit (bank links use their own workflow)
}

type PlanResult struct {
	Changes   []Change `json:"changes"`
	Errors    int      `json:"errors"`
	Unchanged int      `json:"unchanged"`
	Gone      int      `json:"gone"` // unedited rows deleted in the portal since download
}

type ApplyResult struct {
	PlanResult
	Applied int    `json:"applied"`
	Backup  string `json:"backup"`
}

var sheetOrder = []string{"flats", "quarters", "ledger", "expenses", "income", "bank"}

var ledgerKinds = []string{"opening", "invoice", "late_fee", "payment", "adjustment"}

var bankLinks = []string{"maintenance", "other_income", "expense", "transfer", "ignore", "review"}

// SheetSpecs describes the editable sheets; option lists come from the database.
func (a *App) SheetSpecs() ([]SheetSpec, error) {
	cats, err := a.Categories()
	if err != nil {
		return nil, err
	}
	catNames := make([]string, len(cats))
	for i, c := range cats {
		catNames[i] = c.Name
	}
	return []SheetSpec{
		{Name: "flats", Title: "Flats", Key: "code", CanAdd: true, CanDelete: true,
			Help: "Owner and contact details. Area changes apply to quarters invoiced after the change.",
			Columns: []Column{
				{Key: "code", Header: "Flat", Type: ColCode, Required: true, Width: 8, Help: "e.g. 3B, GA. Cannot be renamed."},
				{Key: "owner", Header: "Owner", Type: ColText, Required: true, Width: 28},
				{Key: "area_sqft", Header: "Area (sqft)", Type: ColNumber, Required: true, Width: 11},
				{Key: "phone1", Header: "Phone 1", Type: ColText, Width: 14},
				{Key: "phone2", Header: "Phone 2", Type: ColText, Width: 14},
				{Key: "email1", Header: "Email 1", Type: ColText, Width: 26},
				{Key: "email2", Header: "Email 2", Type: ColText, Width: 26},
				{Key: "notes", Header: "Notes", Type: ColText, Width: 30},
				{Key: "balance", Header: "Balance ₹", Type: ColMoney, ReadOnly: true, Width: 12, Help: "Outstanding (negative = advance)."},
			}},
		{Name: "quarters", Title: "Quarters", Key: "id", CanAdd: true, CanDelete: true,
			Help: "Open a quarter by adding a row (e.g. Q4FY27); blanks take the defaults from Settings. Invoices are raised on the Billing page. Deleting a quarter also removes its invoices, late fees and reminders.",
			Columns: []Column{
				{Key: "id", Header: "Quarter", Type: ColText, Required: true, Width: 10, Help: "Q1–Q4 + FY, e.g. Q3FY27 = Oct–Dec 2026."},
				{Key: "start_date", Header: "Starts", Type: ColDate, ReadOnly: true, Width: 11},
				{Key: "end_date", Header: "Ends", Type: ColDate, ReadOnly: true, Width: 11},
				{Key: "due_date", Header: "Due date", Type: ColDate, Width: 11},
				{Key: "rate_per_sqft", Header: "Rate ₹/sqft/month", Type: ColNumber, Width: 10, Help: "Fixed once invoices are raised."},
				{Key: "late_fee", Header: "Late fee ₹/month", Type: ColMoney, Width: 10},
				{Key: "auto_late_fee", Header: "Auto late fee", Type: ColBool, Width: 9, Help: "no = late fees entered by hand on the Dues & payments sheet."},
				{Key: "status", Header: "Status", Type: ColEnum, Options: []string{"open", "closed"}, Width: 9},
				{Key: "invoiced_at", Header: "Invoiced on", Type: ColDate, ReadOnly: true, Width: 11},
			}},
		{Name: "ledger", Title: "Dues & payments", Key: "id", AutoKey: true, CanAdd: true, CanDelete: true,
			Help: "Every flat's account: opening dues, invoices, late fees, payments and adjustments. Payments are positive amounts received. Opening and adjustment amounts are signed: + adds to dues, − is an advance / waiver. Payments are applied to the oldest dues first. Deleted rows can be restored from the flat's page.",
			Columns: []Column{
				{Key: "id", Header: "ID", Type: ColNumber, ReadOnly: true, Width: 7, Help: "Leave blank for new rows."},
				{Key: "flat", Header: "Flat", Type: ColCode, Required: true, Width: 7},
				{Key: "date", Header: "Date", Type: ColDate, Required: true, Width: 11},
				{Key: "kind", Header: "Type", Type: ColEnum, Required: true, Options: ledgerKinds, Width: 11},
				{Key: "quarter", Header: "Quarter", Type: ColText, Width: 11, Help: "Required for invoices and late fees."},
				{Key: "amount", Header: "Amount ₹", Type: ColMoney, Required: true, Width: 11},
				{Key: "mode", Header: "Mode", Type: ColEnum, Options: PaymentModes, Width: 13, Help: "Payments only."},
				{Key: "description", Header: "Description", Type: ColText, Width: 40},
				{Key: "bank_txn", Header: "Bank txn", Type: ColNumber, ReadOnly: true, Width: 8},
				{Key: "source", Header: "Source", Type: ColText, ReadOnly: true, Width: 7},
			}},
		{Name: "expenses", Title: "Expenses", Key: "id", AutoKey: true, CanAdd: true, CanDelete: true,
			Help: "Bills paid by the association. Leave Category blank to pick it from the description.",
			Columns: []Column{
				{Key: "id", Header: "ID", Type: ColNumber, ReadOnly: true, Width: 7, Help: "Leave blank for new rows."},
				{Key: "date", Header: "Date", Type: ColDate, Required: true, Width: 11},
				{Key: "description", Header: "Particulars", Type: ColText, Required: true, Width: 40},
				{Key: "category", Header: "Category", Type: ColEnum, Options: catNames, Width: 20},
				{Key: "payee", Header: "Payee", Type: ColText, Width: 20},
				{Key: "amount", Header: "Amount ₹", Type: ColMoney, Required: true, Width: 11},
				{Key: "notes", Header: "Notes", Type: ColText, Width: 30},
				{Key: "bank_txn", Header: "Bank txn", Type: ColNumber, ReadOnly: true, Width: 8},
				{Key: "bill_file", Header: "Bill attached", Type: ColText, ReadOnly: true, Width: 10},
			}},
		{Name: "income", Title: "Other income", Key: "id", AutoKey: true, CanAdd: true, CanDelete: true,
			Help: "Income other than maintenance (interest, move-in charges…). Rows created from the bank statement are changed on the Bank links sheet.",
			Columns: []Column{
				{Key: "id", Header: "ID", Type: ColNumber, ReadOnly: true, Width: 7, Help: "Leave blank for new rows."},
				{Key: "date", Header: "Date", Type: ColDate, Required: true, Width: 11},
				{Key: "category", Header: "Category", Type: ColEnum, Required: true, Options: IncomeCategories, Width: 20},
				{Key: "description", Header: "Description", Type: ColText, Width: 40},
				{Key: "amount", Header: "Amount ₹", Type: ColMoney, Required: true, Width: 11},
				{Key: "flat", Header: "Flat", Type: ColCode, Width: 7},
				{Key: "bank_txn", Header: "Bank txn", Type: ColNumber, ReadOnly: true, Width: 8},
			}},
		{Name: "bank", Title: "Bank links", Key: "id", AutoKey: true,
			Help: "How each bank statement line is reconciled. Change 'Link as' and the columns it needs: maintenance → Flat (several flats: 3B=5000; 4C=3000); other_income → Category (income) and optional Flat; expense → Payee, Category and Bill (an expense ID, 'new' to record the debit as a bill, or blank); transfer; ignore; review. The payer is remembered, so their future payments are matched automatically (except payments split across flats).",
			Columns: []Column{
				{Key: "id", Header: "ID", Type: ColNumber, ReadOnly: true, Width: 7},
				{Key: "date", Header: "Date", Type: ColDate, ReadOnly: true, Width: 11},
				{Key: "narration", Header: "Narration", Type: ColText, ReadOnly: true, Width: 44},
				{Key: "credit", Header: "Credit ₹", Type: ColMoney, ReadOnly: true, Width: 11},
				{Key: "debit", Header: "Debit ₹", Type: ColMoney, ReadOnly: true, Width: 11},
				{Key: "status", Header: "Status", Type: ColText, ReadOnly: true, Width: 10},
				{Key: "link", Header: "Link as", Type: ColEnum, Required: true, Options: bankLinks, Width: 13},
				{Key: "flats", Header: "Flat(s)", Type: ColText, Width: 16},
				{Key: "payee", Header: "Payee", Type: ColText, Width: 18},
				{Key: "category", Header: "Category", Type: ColText, Width: 20},
				{Key: "expense", Header: "Bill", Type: ColText, Width: 8},
			}},
	}, nil
}

func specByName(specs []SheetSpec, name string) (SheetSpec, bool) {
	for _, s := range specs {
		if s.Name == name {
			return s, true
		}
	}
	return SheetSpec{}, false
}

// ---- value normalisation ------------------------------------------------------

func moneyStr(p int64) string {
	if p%100 == 0 {
		return strconv.FormatInt(p/100, 10)
	}
	return strconv.FormatFloat(float64(p)/100, 'f', 2, 64)
}

func parseMoney(s string) (int64, error) {
	r := strings.NewReplacer("₹", "", ",", "", " ", "", "−", "-", "Rs.", "", "Rs", "")
	t := r.Replace(s)
	if strings.HasPrefix(t, "(") && strings.HasSuffix(t, ")") {
		t = "-" + t[1:len(t)-1]
	}
	f, err := strconv.ParseFloat(t, 64)
	if err != nil {
		return 0, fmt.Errorf("%q is not an amount", s)
	}
	return Paise(f), nil
}

var dateLayouts = []string{"2006-01-02", "02/01/2006", "2/1/2006", "02-01-2006", "2-1-2006", "02.01.2006", "2.1.2006",
	"2006/01/02", "02-Jan-2006", "2-Jan-2006", "02 Jan 2006", "2 Jan 2006", "02-Jan-06", "2-Jan-06", "02/01/06", "2/1/06"}

// parseFlexDate accepts ISO dates, Indian dd/mm/yyyy forms and Excel serials.
func parseFlexDate(s string) (string, error) {
	s = strings.TrimSpace(s)
	if len(s) > 10 && s[4] == '-' && (s[10] == ' ' || s[10] == 'T') { // 2026-10-02 00:00:00
		s = s[:10]
	}
	for _, l := range dateLayouts {
		if t, err := time.Parse(l, s); err == nil {
			return t.Format(DateFmt), nil
		}
	}
	if serialRe.MatchString(s) {
		f, _ := strconv.ParseFloat(s, 64)
		if t, err := excelize.ExcelDateToTime(f, false); err == nil {
			return t.Format(DateFmt), nil
		}
	}
	return "", fmt.Errorf("%q is not a date (use YYYY-MM-DD or DD/MM/YYYY)", s)
}

func normalize(c Column, raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		if c.Type == ColBool {
			return "no", nil
		}
		return "", nil
	}
	switch c.Type {
	case ColCode:
		return strings.ToUpper(s), nil
	case ColDate:
		return parseFlexDate(s)
	case ColMoney:
		p, err := parseMoney(s)
		if err != nil {
			return "", err
		}
		return moneyStr(p), nil
	case ColNumber:
		f, err := strconv.ParseFloat(strings.ReplaceAll(s, ",", ""), 64)
		if err != nil {
			return "", fmt.Errorf("%q is not a number", s)
		}
		return strconv.FormatFloat(f, 'f', -1, 64), nil
	case ColEnum:
		k := strings.ReplaceAll(strings.ToLower(s), " ", "_")
		for _, o := range c.Options {
			if strings.EqualFold(o, s) || strings.ReplaceAll(strings.ToLower(o), " ", "_") == k {
				return o, nil
			}
		}
		return "", fmt.Errorf("%q is not one of: %s", s, strings.Join(c.Options, ", "))
	case ColBool:
		switch strings.ToLower(s) {
		case "y", "yes", "true", "1", "x", "✓":
			return "yes", nil
		case "n", "no", "false", "0", "-":
			return "no", nil
		}
		return "", fmt.Errorf("%q should be yes or no", s)
	}
	return s, nil
}

// stamp fingerprints a row's editable values.
func stamp(spec SheetSpec, v map[string]string) string {
	h := sha256.New()
	for _, c := range spec.Columns {
		if c.ReadOnly && c.Key != spec.Key {
			continue
		}
		fmt.Fprintf(h, "%s\x1f", v[c.Key])
	}
	return hex.EncodeToString(h.Sum(nil))[:12]
}

func (a *App) finishRows(spec SheetSpec, rows []SheetRow) []SheetRow {
	for i := range rows {
		for _, c := range spec.Columns {
			if n, err := normalize(c, rows[i].Values[c.Key]); err == nil {
				rows[i].Values[c.Key] = n
			}
		}
		rows[i].Version = stamp(spec, rows[i].Values)
	}
	return rows
}

// ---- current data -----------------------------------------------------------

func nz(s sql.NullString) string { return s.String }

func nid(n sql.NullInt64) string {
	if !n.Valid {
		return ""
	}
	return strconv.FormatInt(n.Int64, 10)
}

// Sheet returns the current rows of one sheet.
func (a *App) Sheet(name string) (*SheetData, error) {
	specs, err := a.SheetSpecs()
	if err != nil {
		return nil, err
	}
	spec, ok := specByName(specs, name)
	if !ok {
		return nil, fmt.Errorf("unknown sheet %q", name)
	}
	rows, err := a.sheetRows(name)
	if err != nil {
		return nil, err
	}
	return &SheetData{SheetSpec: spec, Rows: a.finishRows(spec, rows)}, nil
}

func (a *App) sheetRows(name string) ([]SheetRow, error) {
	out := []SheetRow{}
	add := func(locked string, kv ...string) {
		m := map[string]string{}
		for i := 0; i+1 < len(kv); i += 2 {
			m[kv[i]] = kv[i+1]
		}
		out = append(out, SheetRow{Values: m, Locked: locked})
	}
	switch name {
	case "flats":
		fs, err := a.Flats()
		if err != nil {
			return nil, err
		}
		for _, f := range fs {
			add("", "code", f.Code, "owner", f.Owner, "area_sqft", strconv.FormatFloat(f.AreaSqft, 'f', -1, 64), "phone1", f.Phone1, "phone2", f.Phone2,
				"email1", f.Email1, "email2", f.Email2, "notes", f.Notes, "balance", moneyStr(f.Balance))
		}
	case "quarters":
		rows, err := a.DB.Query(`SELECT id,start_date,end_date,due_date,rate_per_sqft,late_fee,auto_late_fee,status,invoiced_at FROM periods ORDER BY start_date DESC`)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var id, s, e, due, status string
			var rate float64
			var fee int64
			var auto bool
			var inv sql.NullString
			if err := rows.Scan(&id, &s, &e, &due, &rate, &fee, &auto, &status, &inv); err != nil {
				return nil, err
			}
			invd := nz(inv)
			if len(invd) > 10 {
				invd = invd[:10]
			}
			add("", "id", id, "start_date", s, "end_date", e, "due_date", due, "rate_per_sqft", strconv.FormatFloat(rate, 'f', -1, 64),
				"late_fee", moneyStr(fee), "auto_late_fee", map[bool]string{true: "yes", false: "no"}[auto], "status", status, "invoiced_at", invd)
		}
		return out, rows.Err()
	case "ledger":
		rows, err := a.DB.Query(`SELECT l.id,l.flat,l.date,l.kind,l.period_id,l.amount,l.mode,l.description,l.bank_txn_id,l.source,COALESCE(p.auto_late_fee,0)
			FROM ledger l LEFT JOIN periods p ON p.id=l.period_id
			ORDER BY l.date DESC, l.id DESC`)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var id, amount int64
			var flat, date, kind, mode, desc, source string
			var pid sql.NullString
			var bt sql.NullInt64
			var auto bool
			if err := rows.Scan(&id, &flat, &date, &kind, &pid, &amount, &mode, &desc, &bt, &source, &auto); err != nil {
				return nil, err
			}
			if kind == "payment" {
				amount = -amount
			}
			locked := ""
			if bt.Valid {
				locked = fmt.Sprintf("Posted from bank transaction #%d – change it on the Bank links sheet", bt.Int64)
			} else if kind == "late_fee" && auto {
				locked = "Late fees for " + pid.String + " are calculated automatically"
			}
			add(locked, "id", strconv.FormatInt(id, 10), "flat", flat, "date", date, "kind", kind, "quarter", nz(pid), "amount", moneyStr(amount),
				"mode", mode, "description", desc, "bank_txn", nid(bt), "source", source)
		}
		return out, rows.Err()
	case "expenses":
		es, err := a.Expenses(ExpenseFilter{})
		if err != nil {
			return nil, err
		}
		for _, e := range es {
			bt := ""
			if e.BankTxnID != nil {
				bt = strconv.FormatInt(*e.BankTxnID, 10)
			}
			bill := ""
			if e.BillFile != "" {
				bill = "yes"
			}
			add("", "id", strconv.FormatInt(e.ID, 10), "date", e.Date, "description", e.Description, "category", e.Category, "payee", e.Payee,
				"amount", moneyStr(e.Amount), "notes", e.Notes, "bank_txn", bt, "bill_file", bill)
		}
	case "income":
		in, err := a.OtherIncomes("", "")
		if err != nil {
			return nil, err
		}
		for _, o := range in {
			bt, locked, fl := "", "", ""
			if o.BankTxnID != nil {
				bt = strconv.FormatInt(*o.BankTxnID, 10)
				locked = fmt.Sprintf("Posted from bank transaction #%s – change it on the Bank links sheet", bt)
			}
			if o.Flat != nil {
				fl = *o.Flat
			}
			add(locked, "id", strconv.FormatInt(o.ID, 10), "date", o.Date, "category", o.Category, "description", o.Description,
				"amount", moneyStr(o.Amount), "flat", fl, "bank_txn", bt)
		}
	case "bank":
		ts, err := a.BankTxns(TxnFilter{})
		if err != nil {
			return nil, err
		}
		splits, err := a.bankSplits()
		if err != nil {
			return nil, err
		}
		incFlat := map[int64]string{}
		rows, err := a.DB.Query(`SELECT bank_txn_id,flat FROM other_income WHERE bank_txn_id IS NOT NULL AND flat IS NOT NULL`)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id int64
			var f string
			if err := rows.Scan(&id, &f); err != nil {
				rows.Close()
				return nil, err
			}
			incFlat[id] = f
		}
		rows.Close()
		autoBills, err := a.autoBills()
		if err != nil {
			return nil, err
		}
		for _, t := range ts {
			link := t.Kind
			switch t.Status {
			case "ignored":
				link = "ignore"
			case "review":
				link = "review"
			}
			flats := ""
			if sp := splits[t.ID]; len(sp) == 1 {
				flats = sp[0].Flat
			} else if len(sp) > 1 {
				var parts []string
				for _, s := range sp {
					parts = append(parts, s.Flat+"="+moneyStr(Paise(s.Amount)))
				}
				flats = strings.Join(parts, "; ")
			} else if f, ok := incFlat[t.ID]; ok {
				flats = f
			} else if t.Flat != nil {
				flats = *t.Flat
			}
			var bills []string
			for _, id := range t.ExpenseIDs {
				if autoBills[id] == t.ID {
					bills = append(bills, "new")
				} else {
					bills = append(bills, strconv.FormatInt(id, 10))
				}
			}
			add("", "id", strconv.FormatInt(t.ID, 10), "date", t.Date, "narration", t.Narration, "credit", moneyStr(t.Credit), "debit", moneyStr(t.Debit),
				"status", t.Status, "link", link, "flats", flats, "payee", t.Payee, "category", t.Category, "expense", strings.Join(bills, ", "))
		}
	default:
		return nil, fmt.Errorf("unknown sheet %q", name)
	}
	return out, nil
}

// bankSplits lists the flats each bank credit was posted to.
func (a *App) bankSplits() (map[int64][]Split, error) {
	rows, err := a.DB.Query(`SELECT bank_txn_id,flat,-amount FROM ledger WHERE bank_txn_id IS NOT NULL AND kind='payment' ORDER BY bank_txn_id,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64][]Split{}
	for rows.Next() {
		var id, amt int64
		var f string
		if err := rows.Scan(&id, &f, &amt); err != nil {
			return nil, err
		}
		out[id] = append(out[id], Split{Flat: f, Amount: float64(amt) / 100})
	}
	return out, rows.Err()
}

// autoBills maps expenses recorded from a bank debit to that transaction.
func (a *App) autoBills() (map[int64]int64, error) {
	rows, err := a.DB.Query(`SELECT id,bank_txn_id FROM expenses WHERE bank_txn_id IS NOT NULL AND notes='Created from bank statement'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]int64{}
	for rows.Next() {
		var id, bt int64
		if err := rows.Scan(&id, &bt); err != nil {
			return nil, err
		}
		out[id] = bt
	}
	return out, rows.Err()
}

// ---- planning ---------------------------------------------------------------

type pendingRow struct {
	sheet string
	spec  SheetSpec
	in    InRow
	vals  map[string]string // normalised; missing columns filled from the current row
	cur   *SheetRow
	ch    *Change
}

type planCtx struct {
	a        *App
	cur      map[string]map[string]*SheetRow // sheet → key → row
	rows     []*pendingRow
	flatAdd  map[string]bool
	flatDel  map[string]bool
	qAdd     map[string]map[string]string // quarter → values
	qDel     map[string]bool
	ledDel   map[int64]bool
	expDel   map[int64]bool
	invoices map[string]int64 // flat|quarter → ledger id holding the invoice
	cats     map[string]bool
}

func (p *planCtx) flatOK(code string) error {
	if code == "" {
		return nil
	}
	if p.flatDel[code] {
		return fmt.Errorf("flat %s is being deleted in this upload", code)
	}
	if p.cur["flats"][code] == nil && !p.flatAdd[code] {
		return fmt.Errorf("flat %s does not exist (add it on the Flats sheet)", code)
	}
	return nil
}

// quarter returns the quarter's values (existing or added in this upload).
func (p *planCtx) quarter(id string) (map[string]string, error) {
	if p.qDel[id] {
		return nil, fmt.Errorf("quarter %s is being deleted in this upload", id)
	}
	if v, ok := p.qAdd[id]; ok {
		return v, nil
	}
	if r := p.cur["quarters"][id]; r != nil {
		return r.Values, nil
	}
	return nil, fmt.Errorf("quarter %s does not exist (add it on the Quarters sheet)", id)
}

func allBlank(m map[string]string) bool {
	for _, v := range m {
		if strings.TrimSpace(v) != "" {
			return false
		}
	}
	return true
}

// PlanChanges validates a changeset and describes what applying it would do.
func (a *App) PlanChanges(cs Changeset) (*PlanResult, error) {
	res, _, err := a.plan(cs)
	return res, err
}

func (a *App) plan(cs Changeset) (*PlanResult, *planCtx, error) {
	specs, err := a.SheetSpecs()
	if err != nil {
		return nil, nil, err
	}
	for name := range cs {
		if _, ok := specByName(specs, name); !ok {
			return nil, nil, fmt.Errorf("unknown sheet %q", name)
		}
	}
	p := &planCtx{a: a, cur: map[string]map[string]*SheetRow{}, flatAdd: map[string]bool{}, flatDel: map[string]bool{},
		qAdd: map[string]map[string]string{}, qDel: map[string]bool{}, ledDel: map[int64]bool{}, expDel: map[int64]bool{},
		invoices: map[string]int64{}, cats: map[string]bool{}}
	res := &PlanResult{Changes: []Change{}}

	for _, name := range sheetOrder {
		spec, _ := specByName(specs, name)
		rows, err := a.sheetRows(name)
		if err != nil {
			return nil, nil, err
		}
		rows = a.finishRows(spec, rows)
		m := map[string]*SheetRow{}
		for i := range rows {
			m[rows[i].Values[spec.Key]] = &rows[i]
		}
		p.cur[name] = m
	}
	for _, r := range p.cur["ledger"] {
		if r.Values["kind"] == "invoice" {
			id, _ := strconv.ParseInt(r.Values["id"], 10, 64)
			p.invoices[r.Values["flat"]+"|"+r.Values["quarter"]] = id
		}
	}
	cats, err := a.Categories()
	if err != nil {
		return nil, nil, err
	}
	for _, c := range cats {
		p.cats[c.Name] = true
	}

	// pass 1: classify every row
	for _, name := range sheetOrder {
		spec, _ := specByName(specs, name)
		for _, in := range cs[name] {
			if !in.Delete && allBlank(in.Values) {
				continue
			}
			pr := &pendingRow{sheet: name, spec: spec, in: in, vals: map[string]string{}}
			ch := &Change{Sheet: name, Row: in.Row}
			pr.ch = ch
			var errs []string
			for _, c := range spec.Columns {
				raw, given := in.Values[c.Key]
				if !given {
					continue
				}
				v, err := normalize(c, raw)
				if err != nil {
					errs = append(errs, c.Header+": "+err.Error())
					continue
				}
				pr.vals[c.Key] = v
			}
			key := pr.vals[spec.Key]
			ch.Key = key
			if key != "" {
				pr.cur = p.cur[name][key]
			}
			if pr.cur != nil { // columns not sent keep their current value
				for k, v := range pr.cur.Values {
					if _, ok := pr.vals[k]; !ok {
						pr.vals[k] = v
					}
				}
			}
			if len(errs) > 0 {
				ch.Action = "update"
				if pr.cur == nil {
					ch.Action = "add"
				}
				ch.Error = strings.Join(errs, "; ")
				res.Changes = append(res.Changes, *ch)
				continue
			}
			edited := in.Version == "" || stamp(spec, pr.vals) != in.Version
			switch {
			case in.Delete:
				ch.Action = "delete"
				switch {
				case !spec.CanDelete:
					ch.Error = "rows on this sheet cannot be deleted"
				case pr.cur == nil && key == "":
					continue // a new row that was added and deleted again
				case pr.cur == nil:
					res.Gone++
					continue // already gone
				case in.Version != "" && pr.cur.Version != in.Version:
					ch.Error = "changed in the portal after you downloaded it – download again and redo this row"
				case pr.cur.Locked != "":
					ch.Error = pr.cur.Locked
				}
			case pr.cur == nil:
				ch.Action = "add"
				switch {
				case in.Version != "" && !edited:
					res.Gone++
					continue // deleted in the portal and not edited here
				case in.Version != "" && !spec.AutoKey:
					ch.Error = "this row no longer exists, or its key was changed (" + spec.Columns[0].Header + " cannot be renamed – add a new row instead)"
				case in.Version != "" || (spec.AutoKey && key != ""):
					ch.Error = fmt.Sprintf("ID %s no longer exists – it was deleted in the portal", key)
				case !spec.CanAdd:
					ch.Error = "rows cannot be added to this sheet"
				}
			default:
				ch.Action = "update"
				if !edited || stamp(spec, pr.vals) == pr.cur.Version {
					res.Unchanged++
					continue
				}
				if in.Version != "" && pr.cur.Version != in.Version {
					ch.Error = "changed in the portal after you downloaded it – download again and redo this row"
				} else if pr.cur.Locked != "" {
					ch.Error = pr.cur.Locked
				}
				for _, c := range spec.Columns {
					if !c.ReadOnly && pr.vals[c.Key] != pr.cur.Values[c.Key] {
						ch.Fields = append(ch.Fields, FieldChange{Column: c.Header, From: pr.cur.Values[c.Key], To: pr.vals[c.Key]})
					}
				}
			}
			if ch.Error == "" {
				for _, c := range spec.Columns {
					if c.ReadOnly && c.Key != spec.Key && pr.cur != nil {
						pr.vals[c.Key] = pr.cur.Values[c.Key] // read-only columns are never written
					}
				}
				switch name {
				case "flats":
					if ch.Action == "add" {
						p.flatAdd[key] = true
					} else if ch.Action == "delete" {
						p.flatDel[key] = true
					}
				case "quarters":
					if ch.Action == "add" {
						p.qAdd[key] = pr.vals
					} else if ch.Action == "delete" {
						p.qDel[key] = true
					}
				case "ledger":
					if ch.Action == "delete" {
						id, _ := strconv.ParseInt(key, 10, 64)
						p.ledDel[id] = true
					}
				case "expenses":
					if ch.Action == "delete" {
						id, _ := strconv.ParseInt(key, 10, 64)
						p.expDel[id] = true
					}
				}
			}
			p.rows = append(p.rows, pr)
		}
	}

	// pass 2: validate against everything else in the upload and build the writes
	for _, pr := range p.rows {
		ch := pr.ch
		if ch.Error == "" {
			var err error
			switch pr.sheet {
			case "flats":
				err = p.planFlat(pr)
			case "quarters":
				err = p.planQuarter(pr)
			case "ledger":
				err = p.planLedger(pr)
			case "expenses":
				err = p.planExpense(pr)
			case "income":
				err = p.planIncome(pr)
			case "bank":
				err = p.planBank(pr)
			}
			if err != nil {
				ch.Error = err.Error()
			}
		}
		if ch.Label == "" {
			ch.Label = pr.spec.Title + " " + ch.Key
		}
		res.Changes = append(res.Changes, *ch)
	}
	for _, c := range res.Changes {
		if c.Error != "" {
			res.Errors++
		}
	}
	return res, p, nil
}

func (p *planCtx) planFlat(pr *pendingRow) error {
	v, ch, code := pr.vals, pr.ch, pr.ch.Key
	ch.Label = "Flat " + code
	if ch.Action == "delete" {
		ch.Label += " · " + pr.cur.Values["owner"]
		var ledger, bank, income int
		rows, err := p.a.DB.Query(`SELECT id FROM ledger WHERE flat=?`, code)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id int64
			rows.Scan(&id)
			if !p.ledDel[id] {
				ledger++
			}
		}
		rows.Close()
		p.a.DB.QueryRow(`SELECT COUNT(*) FROM bank_txns WHERE flat=?`, code).Scan(&bank)
		p.a.DB.QueryRow(`SELECT COUNT(*) FROM other_income WHERE flat=?`, code).Scan(&income)
		for _, o := range p.rows {
			if o.ch.Action != "delete" && (o.sheet == "ledger" || o.sheet == "income") && o.vals["flat"] == code {
				ledger++
			}
		}
		switch {
		case ledger > 0:
			return fmt.Errorf("flat %s still has %d entries on Dues & payments – delete those too, or keep the flat", code, ledger)
		case bank > 0:
			return fmt.Errorf("flat %s is linked to %d bank transactions – re-link them first", code, bank)
		case income > 0:
			return fmt.Errorf("flat %s has %d other-income entries", code, income)
		}
		ch.order = 90
		ch.apply = func(tx *db.Tx) error {
			if _, err := tx.Exec(`DELETE FROM payer_aliases WHERE flat=?`, code); err != nil {
				return err
			}
			_, err := tx.Exec(`DELETE FROM flats WHERE code=?`, code)
			return err
		}
		return nil
	}
	if !flatCodeRe.MatchString(code) {
		return fmt.Errorf("flat code %q should look like 3B or GA", code)
	}
	area, _ := strconv.ParseFloat(v["area_sqft"], 64)
	if v["owner"] == "" || area <= 0 {
		return fmt.Errorf("owner and a positive area are required")
	}
	ch.Label += " · " + v["owner"]
	if ch.Action == "update" && v["area_sqft"] != pr.cur.Values["area_sqft"] {
		ch.Warnings = append(ch.Warnings, "Invoices already raised keep the old area; quarters invoiced from now on use the new one.")
	}
	ch.order = 10
	ch.apply = func(tx *db.Tx) error {
		_, err := tx.Exec(`
			INSERT INTO flats(code,owner,area_sqft,phone1,phone2,email1,email2,notes,sort_key) VALUES(?,?,?,?,?,?,?,?,?)
			ON CONFLICT(code) DO UPDATE SET owner=excluded.owner, area_sqft=excluded.area_sqft, phone1=excluded.phone1,
			  phone2=excluded.phone2, email1=excluded.email1, email2=excluded.email2, notes=excluded.notes`,
			code, v["owner"], area, v["phone1"], v["phone2"], v["email1"], v["email2"], v["notes"], FlatSortKey(code))
		return err
	}
	return nil
}

var flatCodeRe = regexp.MustCompile(`^(G|[0-9])[A-Z]{1,2}$`)

func (p *planCtx) planQuarter(pr *pendingRow) error {
	v, ch, id := pr.vals, pr.ch, pr.ch.Key
	ch.Label = "Quarter " + id
	if ch.Action == "delete" {
		var inv, fees, other, rem int
		p.a.DB.QueryRow(`SELECT COUNT(*) FROM ledger WHERE period_id=? AND kind='invoice'`, id).Scan(&inv)
		p.a.DB.QueryRow(`SELECT COUNT(*) FROM ledger WHERE period_id=? AND kind='late_fee'`, id).Scan(&fees)
		p.a.DB.QueryRow(`SELECT COUNT(*) FROM ledger WHERE period_id=? AND kind NOT IN ('invoice','late_fee')`, id).Scan(&other)
		p.a.DB.QueryRow(`SELECT COUNT(*) FROM reminders WHERE period_id=?`, id).Scan(&rem)
		for _, o := range p.rows {
			if o.sheet == "ledger" && o.ch.Action != "delete" && o.vals["quarter"] == id {
				return fmt.Errorf("rows on Dues & payments still refer to %s", id)
			}
		}
		if inv+fees+other > 0 {
			ch.Warnings = append(ch.Warnings, fmt.Sprintf("Also removes %d invoices, %d late fees and %d other entries of %s (kept in each flat's removed-entries list).", inv, fees, other, id))
		}
		if rem > 0 {
			ch.Warnings = append(ch.Warnings, fmt.Sprintf("Also deletes %d reminders of %s.", rem, id))
		}
		ch.order = 80
		ch.apply = func(tx *db.Tx) error {
			if _, err := tx.Exec(`INSERT INTO ledger_voids(ledger_id,flat,date,kind,period_id,seq,amount,description,mode,source,reason)
				SELECT id,flat,date,kind,period_id,seq,amount,description,mode,source,? FROM ledger WHERE period_id=?`, "Quarter "+id+" deleted (bulk edit)", id); err != nil {
				return err
			}
			for _, q := range []string{`DELETE FROM ledger WHERE period_id=?`, `DELETE FROM reminders WHERE period_id=?`, `DELETE FROM periods WHERE id=?`} {
				if _, err := tx.Exec(q, id); err != nil {
					return err
				}
			}
			return nil
		}
		return nil
	}
	q, err := ParseQuarter(id)
	if err != nil {
		return err
	}
	if ch.Action == "add" { // blanks take the defaults
		if v["due_date"] == "" {
			v["due_date"] = q.DefaultDue().Format(DateFmt)
		}
		if v["rate_per_sqft"] == "" {
			v["rate_per_sqft"] = p.a.Setting("rate_per_sqft")
		}
		if v["late_fee"] == "" {
			v["late_fee"] = moneyStr(Paise(p.a.SettingFloat("late_fee")))
		}
		if _, ok := pr.in.Values["auto_late_fee"]; !ok || strings.TrimSpace(pr.in.Values["auto_late_fee"]) == "" {
			v["auto_late_fee"] = "yes"
		}
		if v["status"] == "" {
			v["status"] = "open"
		}
		ch.Label += fmt.Sprintf(" · %s to %s", q.Start().Format("02 Jan 2006"), q.End().Format("02 Jan 2006"))
	}
	rate, err := strconv.ParseFloat(v["rate_per_sqft"], 64)
	if err != nil || rate <= 0 {
		return fmt.Errorf("rate must be a positive number")
	}
	fee, err := parseMoney(v["late_fee"])
	if err != nil || fee < 0 {
		return fmt.Errorf("late fee must be zero or more")
	}
	if v["due_date"] == "" || v["status"] == "" {
		return fmt.Errorf("due date and status are required")
	}
	if ch.Action == "update" && pr.cur.Values["invoiced_at"] != "" && v["rate_per_sqft"] != pr.cur.Values["rate_per_sqft"] {
		return fmt.Errorf("invoices already raised for %s; rate cannot change (post adjustments instead)", id)
	}
	auto := v["auto_late_fee"] == "yes"
	ch.order = 20
	ch.apply = func(tx *db.Tx) error {
		if ch.Action == "add" {
			_, err := tx.Exec(`INSERT INTO periods(id,fy,quarter,start_date,end_date,due_date,rate_per_sqft,late_fee,auto_late_fee,status)
				VALUES(?,?,?,?,?,?,?,?,?,?)`, q.ID(), q.FY, q.Q, q.Start().Format(DateFmt), q.End().Format(DateFmt), v["due_date"], rate, fee, db.Bool(auto), v["status"])
			return err
		}
		_, err := tx.Exec(`UPDATE periods SET due_date=?, rate_per_sqft=?, late_fee=?, auto_late_fee=?, status=? WHERE id=?`, v["due_date"], rate, fee, db.Bool(auto), v["status"], id)
		return err
	}
	return nil
}

var ledgerDefaultDesc = map[string]string{"opening": "Dues brought forward", "payment": "Payment received", "invoice": "Maintenance", "late_fee": "Late fee"}

func (p *planCtx) planLedger(pr *pendingRow) error {
	v, ch := pr.vals, pr.ch
	id, _ := strconv.ParseInt(ch.Key, 10, 64)
	ch.Label = fmt.Sprintf("%s %s ₹%s on %s", v["flat"], strings.ReplaceAll(v["kind"], "_", " "), v["amount"], v["date"])
	if id != 0 {
		ch.Label = fmt.Sprintf("#%d · ", id) + ch.Label
	}
	if ch.Action == "delete" {
		ch.order = 30
		ch.apply = func(tx *db.Tx) error {
			if _, err := tx.Exec(`INSERT INTO ledger_voids(ledger_id,flat,date,kind,period_id,seq,amount,description,mode,source,reason)
				SELECT id,flat,date,kind,period_id,seq,amount,description,mode,source,'Removed in bulk edit' FROM ledger WHERE id=?`, id); err != nil {
				return err
			}
			_, err := tx.Exec(`DELETE FROM ledger WHERE id=?`, id)
			return err
		}
		return nil
	}
	kind, flat, quarter := v["kind"], v["flat"], v["quarter"]
	for _, k := range []string{"flat", "date", "kind", "amount"} {
		if v[k] == "" {
			c, _ := pr.spec.col(k)
			return fmt.Errorf("%s is required", c.Header)
		}
	}
	if ch.Action == "update" && kind != pr.cur.Values["kind"] {
		return fmt.Errorf("the type of an entry can't change – delete it and add a new row")
	}
	if err := p.flatOK(flat); err != nil {
		return err
	}
	amt, _ := parseMoney(v["amount"])
	switch kind {
	case "payment", "invoice", "late_fee":
		if amt <= 0 {
			return fmt.Errorf("amount must be positive for %s", strings.ReplaceAll(kind, "_", " "))
		}
	default:
		if amt == 0 {
			return fmt.Errorf("amount can't be zero")
		}
	}
	if kind == "payment" {
		if v["mode"] == "" {
			return fmt.Errorf("mode is required for payments (%s)", strings.Join(PaymentModes, ", "))
		}
	} else {
		v["mode"] = ""
	}
	if quarter != "" {
		if _, err := ParseQuarter(quarter); err != nil {
			return err
		}
	}
	var auto bool
	switch kind {
	case "invoice", "late_fee":
		if quarter == "" {
			return fmt.Errorf("quarter is required for %s", strings.ReplaceAll(kind, "_", " "))
		}
		qv, err := p.quarter(quarter)
		if err != nil {
			return err
		}
		auto = qv["auto_late_fee"] == "yes"
	case "payment", "opening":
		if quarter != "" {
			ch.Warnings = append(ch.Warnings, "Quarter ignored: payments and openings are applied to the oldest dues first.")
			v["quarter"], quarter = "", ""
		}
	default:
		if quarter != "" {
			if _, err := p.quarter(quarter); err != nil {
				return err
			}
		}
	}
	if kind == "late_fee" && auto {
		return fmt.Errorf("late fees for %s are calculated automatically – set 'Auto late fee' to no on the Quarters sheet to enter them by hand", quarter)
	}
	if kind == "invoice" {
		k := flat + "|" + quarter
		if other, ok := p.invoices[k]; ok && other != id && !p.ledDel[other] {
			return fmt.Errorf("flat %s already has an invoice for %s (entry #%d)", flat, quarter, other)
		}
		p.invoices[k] = id
	}
	if v["description"] == "" {
		v["description"] = ledgerDefaultDesc[kind]
		if quarter != "" && (kind == "invoice" || kind == "late_fee") {
			v["description"] += " " + quarter
		}
	}
	if kind == "adjustment" && amt != 0 && v["description"] == "" {
		return fmt.Errorf("description is required for adjustments")
	}
	stored := amt
	if kind == "payment" {
		stored = -amt
	}
	var pid any
	if quarter != "" {
		pid = quarter
	}
	desc, mode, date := v["description"], v["mode"], v["date"]
	ch.order = 40
	ch.apply = func(tx *db.Tx) error {
		var seq any
		switch kind {
		case "invoice":
			seq = 0
		case "late_fee":
			if ch.Action == "update" && flat == pr.cur.Values["flat"] && quarter == pr.cur.Values["quarter"] {
				var s sql.NullInt64
				tx.QueryRow(`SELECT seq FROM ledger WHERE id=?`, id).Scan(&s)
				seq = s.Int64
			} else {
				var n int64
				tx.QueryRow(`SELECT COALESCE(MAX(seq),0)+1 FROM ledger WHERE flat=? AND period_id=? AND kind='late_fee'`, flat, quarter).Scan(&n)
				seq = n
			}
		}
		if ch.Action == "add" {
			_, err := tx.Exec(`INSERT INTO ledger(flat,date,kind,period_id,seq,amount,description,mode) VALUES(?,?,?,?,?,?,?,?)`,
				flat, date, kind, pid, seq, stored, desc, mode)
			return err
		}
		_, err := tx.Exec(`UPDATE ledger SET flat=?,date=?,period_id=?,seq=?,amount=?,description=?,mode=? WHERE id=?`,
			flat, date, pid, seq, stored, desc, mode, id)
		return err
	}
	return nil
}

func (p *planCtx) planExpense(pr *pendingRow) error {
	v, ch := pr.vals, pr.ch
	id, _ := strconv.ParseInt(ch.Key, 10, 64)
	ch.Label = fmt.Sprintf("%s · %s ₹%s", v["date"], v["description"], v["amount"])
	if id != 0 {
		ch.Label = fmt.Sprintf("#%d · ", id) + ch.Label
	}
	if ch.Action == "delete" {
		if v["bank_txn"] != "" {
			ch.Warnings = append(ch.Warnings, "Bank transaction #"+v["bank_txn"]+" stays reconciled as an expense, without a bill.")
		}
		ch.order = 50
		ch.apply = func(tx *db.Tx) error {
			_, err := tx.Exec(`DELETE FROM expenses WHERE id=?`, id)
			return err
		}
		return nil
	}
	if v["date"] == "" || v["description"] == "" || v["amount"] == "" {
		return fmt.Errorf("date, particulars and amount are required")
	}
	amt, _ := parseMoney(v["amount"])
	if amt == 0 {
		return fmt.Errorf("amount can't be zero")
	}
	if v["category"] == "" {
		cats, err := p.a.Categories()
		if err != nil {
			return err
		}
		v["category"] = Categorize(cats, v["description"]+" "+v["payee"], amt)
		ch.Warnings = append(ch.Warnings, "Category set to "+v["category"]+".")
	}
	ch.order = 50
	ch.apply = func(tx *db.Tx) error {
		if ch.Action == "add" {
			_, err := tx.Exec(`INSERT INTO expenses(date,description,category,payee,amount,notes,source) VALUES(?,?,?,?,?,?,'app')`,
				v["date"], v["description"], v["category"], v["payee"], amt, v["notes"])
			return err
		}
		_, err := tx.Exec(`UPDATE expenses SET date=?,description=?,category=?,payee=?,amount=?,notes=? WHERE id=?`,
			v["date"], v["description"], v["category"], v["payee"], amt, v["notes"], id)
		return err
	}
	return nil
}

func (p *planCtx) planIncome(pr *pendingRow) error {
	v, ch := pr.vals, pr.ch
	id, _ := strconv.ParseInt(ch.Key, 10, 64)
	ch.Label = fmt.Sprintf("%s · %s ₹%s", v["date"], v["category"], v["amount"])
	if id != 0 {
		ch.Label = fmt.Sprintf("#%d · ", id) + ch.Label
	}
	if ch.Action == "delete" {
		ch.order = 60
		ch.apply = func(tx *db.Tx) error {
			_, err := tx.Exec(`DELETE FROM other_income WHERE id=?`, id)
			return err
		}
		return nil
	}
	if v["date"] == "" || v["category"] == "" || v["amount"] == "" {
		return fmt.Errorf("date, category and amount are required")
	}
	amt, _ := parseMoney(v["amount"])
	if amt <= 0 {
		return fmt.Errorf("amount must be positive")
	}
	if err := p.flatOK(v["flat"]); err != nil {
		return err
	}
	var fl any
	if v["flat"] != "" {
		fl = v["flat"]
	}
	ch.order = 60
	ch.apply = func(tx *db.Tx) error {
		if ch.Action == "add" {
			_, err := tx.Exec(`INSERT INTO other_income(date,category,description,amount,flat) VALUES(?,?,?,?,?)`, v["date"], v["category"], v["description"], amt, fl)
			return err
		}
		_, err := tx.Exec(`UPDATE other_income SET date=?,category=?,description=?,amount=?,flat=? WHERE id=?`, v["date"], v["category"], v["description"], amt, fl, id)
		return err
	}
	return nil
}

// parseSplits reads "3B" or "3B=5000; 4C=3000".
func parseSplits(s string, total int64) ([]Split, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	parts := strings.FieldsFunc(s, func(r rune) bool { return r == ';' || r == ',' || r == '\n' })
	var out []Split
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		flat, amt, hasAmt := strings.Cut(strings.NewReplacer(":", "=").Replace(part), "=")
		sp := Split{Flat: strings.ToUpper(strings.TrimSpace(flat))}
		if hasAmt {
			p, err := parseMoney(amt)
			if err != nil {
				return nil, err
			}
			sp.Amount = float64(p) / 100
		} else if len(parts) == 1 {
			sp.Amount = float64(total) / 100
		} else {
			return nil, fmt.Errorf("give each flat an amount, e.g. 3B=5000; 4C=3000")
		}
		out = append(out, sp)
	}
	return out, nil
}

func (p *planCtx) planBank(pr *pendingRow) error {
	v, ch := pr.vals, pr.ch
	id, _ := strconv.ParseInt(ch.Key, 10, 64)
	credit, _ := parseMoney(v["credit"])
	debit, _ := parseMoney(v["debit"])
	amt := "+₹" + v["credit"]
	if debit > 0 {
		amt = "−₹" + v["debit"]
	}
	narr := v["narration"]
	if len(narr) > 48 {
		narr = narr[:48] + "…"
	}
	ch.Label = fmt.Sprintf("#%d · %s %s %s", id, v["date"], amt, narr)
	// remember the payer like the Bank page does by default, so later statements auto-match
	as := Assignment{Kind: v["link"], Payee: v["payee"], Remember: true}
	switch as.Kind {
	case "maintenance":
		if credit == 0 {
			return fmt.Errorf("only incoming payments can be maintenance")
		}
		sp, err := parseSplits(v["flats"], credit)
		if err != nil {
			return err
		}
		if len(sp) == 0 {
			return fmt.Errorf("Flat(s) is required for maintenance")
		}
		var total int64
		for _, s := range sp {
			if err := p.flatOK(s.Flat); err != nil {
				return err
			}
			total += Paise(s.Amount)
		}
		if total != credit {
			return fmt.Errorf("flat amounts add up to ₹%s but the credit is ₹%s", Rupees(total), Rupees(credit))
		}
		as.Splits = sp
	case "other_income":
		if credit == 0 {
			return fmt.Errorf("only incoming payments can be income")
		}
		as.IncomeCategory = v["category"]
		if as.IncomeCategory == "" {
			as.IncomeCategory = "Other"
		}
		ok := false
		for _, c := range IncomeCategories {
			ok = ok || c == as.IncomeCategory
		}
		if !ok {
			return fmt.Errorf("income category must be one of: %s", strings.Join(IncomeCategories, ", "))
		}
		if strings.ContainsAny(v["flats"], ";,=") {
			return fmt.Errorf("other income can name only one flat")
		}
		if err := p.flatOK(v["flats"]); err != nil {
			return err
		}
		as.Flat = v["flats"]
	case "expense":
		if debit == 0 {
			return fmt.Errorf("only outgoing payments can be expenses")
		}
		if as.Payee == "" {
			return fmt.Errorf("payee is required for expenses")
		}
		as.Category = v["category"]
		if as.Category != "" && !p.cats[as.Category] {
			return fmt.Errorf("unknown expense category %q", as.Category)
		}
		switch bill := strings.ToLower(strings.TrimSpace(v["expense"])); bill {
		case "":
		case "new":
			as.CreateExpense = true
		default:
			eid, err := strconv.ParseInt(strings.TrimPrefix(bill, "#"), 10, 64)
			if err != nil {
				return fmt.Errorf("Bill must be an expense ID, 'new' or blank")
			}
			if p.cur["expenses"][strconv.FormatInt(eid, 10)] == nil || p.expDel[eid] {
				return fmt.Errorf("expense #%d does not exist", eid)
			}
			auto, err := p.a.autoBills()
			if err != nil {
				return err
			}
			if auto[eid] == id {
				as.CreateExpense = true // re-recording the bill this debit created
			} else {
				as.ExpenseID = eid
			}
		}
	case "transfer", "ignore":
	case "review":
		ch.Warnings = append(ch.Warnings, "Set 'Link as' to reconcile it; 'review' only puts it back in the review queue.")
	default:
		return fmt.Errorf("unknown link %q", as.Kind)
	}
	if pr.cur.Values["status"] == "historical" || p.a.beforeCutover(v["date"]) {
		ch.Warnings = append(ch.Warnings, "Before the cut-over date: only the link is recorded; nothing is posted (it's already in the imported balances).")
	}
	ch.order = 100
	ch.after = func() error { return p.a.Assign(id, as) }
	return nil
}

// ApplyChanges re-validates the changeset and, if it has no errors, applies
// it in one transaction after snapshotting the database.
func (a *App) ApplyChanges(cs Changeset) (*ApplyResult, error) {
	res, p, err := a.plan(cs)
	if err != nil {
		return nil, err
	}
	out := &ApplyResult{PlanResult: *res}
	if res.Errors > 0 {
		return out, fmt.Errorf("%d rows have problems; nothing was saved", res.Errors)
	}
	chs := make([]*Change, 0, len(p.rows))
	for _, r := range p.rows {
		chs = append(chs, r.ch)
	}
	sort.SliceStable(chs, func(i, j int) bool { return chs[i].order < chs[j].order })
	if len(chs) == 0 {
		return out, nil
	}
	// SQLite: keep a file snapshot to roll back to. PostgreSQL is backed up by
	// the platform, so there is nothing to snapshot here.
	if out.Backup, err = a.snapshot("pre-edit"); err != nil && !errors.Is(err, db.ErrNoSnapshot) {
		return nil, fmt.Errorf("backup before saving: %w", err)
	}

	a.mu.Lock()
	tx, err := a.DB.Begin()
	if err != nil {
		a.mu.Unlock()
		return nil, err
	}
	for _, c := range chs {
		if c.apply == nil {
			continue
		}
		if err := c.apply(tx); err != nil {
			tx.Rollback()
			a.mu.Unlock()
			return nil, fmt.Errorf("%s: %w; nothing was saved", c.Label, err)
		}
		out.Applied++
	}
	err = tx.Commit()
	a.mu.Unlock()
	if err != nil {
		return nil, err
	}
	var failed []string
	for _, c := range chs {
		if c.after == nil {
			continue
		}
		if err := c.after(); err != nil {
			failed = append(failed, c.Label+": "+err.Error())
			continue
		}
		out.Applied++
	}
	if _, err := a.ApplyLateFees(Today()); err != nil {
		failed = append(failed, "late fees: "+err.Error())
	}
	if len(failed) > 0 {
		return out, fmt.Errorf("saved, except: %s", strings.Join(failed, "; "))
	}
	return out, nil
}

// snapshot copies the database into the backups folder, keeping the last 30
// snapshots with this prefix.
func (a *App) snapshot(prefix string) (string, error) {
	dir := filepath.Join(a.DataDir, "backups")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	name := fmt.Sprintf("%s-%s.db", prefix, time.Now().In(time.FixedZone("IST", 5*3600+1800)).Format("2006-01-02-150405.000"))
	if err := a.DB.Snapshot(filepath.Join(dir, name)); err != nil {
		return "", err
	}
	if old, _ := filepath.Glob(filepath.Join(dir, prefix+"-*.db")); len(old) > 30 {
		sort.Strings(old)
		for _, f := range old[:len(old)-30] {
			os.Remove(f)
		}
	}
	return name, nil
}
