package app

import (
	"fmt"
	"io"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/xuri/excelize/v2"
)

// The treasury workbook keeps one block of columns per quarter on the
// "Maintenance" sheet. The sheet labels a financial year by its *starting*
// year (its "Q1FY25" is Apr–Jun 2025), so each block is pinned to its months
// here and stored under the Indian FY name (Apr–Jun 2025 = Q1FY26).
// From the first block the chain is consistent:
//
//	Payment = previous due + penalty + quarterly amount;  Due = Payment − Paid.
type sheetQuarter struct {
	sheetLabel               string
	q                        Quarter
	penaltyCol, paidCol, due string
}

var sheetQuarters = []sheetQuarter{
	{"Q1FY25", Quarter{2025, 1}, "M", "O", "P"},
	{"Q2FY25", Quarter{2025, 2}, "Q", "S", "T"},
	{"Q3FY25", Quarter{2025, 3}, "U", "W", "X"},
	{"Q4FY25", Quarter{2025, 4}, "Y", "AA", "AB"},
	{"Q1FY26", Quarter{2026, 1}, "AC", "AE", "AF"},
	{"Q2FY26", Quarter{2026, 2}, "AG", "AI", "AJ"},
}

type ImportReport struct {
	Flats           int      `json:"flats"`
	Periods         int      `json:"periods"`
	LedgerEntries   int      `json:"ledger_entries"`
	Expenses        int      `json:"expenses"`
	DatesCorrected  int      `json:"dates_corrected"`
	Warnings        []string `json:"warnings"`
	OutstandingFlat []string `json:"outstanding_flats"`
}

func num(s string) float64 {
	s = strings.ReplaceAll(strings.TrimSpace(s), ",", "")
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(f) {
		return 0
	}
	return f
}

var (
	ddmmyyyy = regexp.MustCompile(`^(\d{1,2})/(\d{1,2})/(\d{2}|\d{4})$`)
	serialRe = regexp.MustCompile(`^\d{5}(\.\d+)?$`)
)

// fixDate interprets a cell from the Expenses sheet. Text cells are already
// dd/mm/yyyy. Real date cells were typed as dd/mm but stored by Excel as mm/dd,
// so day and month are swapped back.
func fixDate(raw string) (string, bool, error) {
	raw = strings.TrimSpace(raw)
	if m := ddmmyyyy.FindStringSubmatch(raw); m != nil {
		d, _ := strconv.Atoi(m[1])
		mo, _ := strconv.Atoi(m[2])
		y, _ := strconv.Atoi(m[3])
		if y < 100 {
			y += 2000
		}
		t := time.Date(y, time.Month(mo), d, 0, 0, 0, 0, time.UTC)
		if t.Day() != d {
			return "", false, fmt.Errorf("invalid date %q", raw)
		}
		return t.Format(DateFmt), false, nil
	}
	if serialRe.MatchString(raw) {
		f, _ := strconv.ParseFloat(raw, 64)
		t, err := excelize.ExcelDateToTime(f, false)
		if err != nil {
			return "", false, err
		}
		if t.Day() <= 12 {
			t = time.Date(t.Year(), time.Month(t.Day()), int(t.Month()), 0, 0, 0, 0, time.UTC)
			return t.Format(DateFmt), true, nil
		}
		return t.Format(DateFmt), false, nil
	}
	return "", false, fmt.Errorf("unrecognised date %q", raw)
}

// ImportWorkbook loads flats, maintenance history and expenses from the
// treasury spreadsheet into an empty database.
func (a *App) ImportWorkbook(r io.Reader) (*ImportReport, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	var n int
	a.DB.QueryRow(`SELECT (SELECT COUNT(*) FROM flats)+(SELECT COUNT(*) FROM expenses)`).Scan(&n)
	if n > 0 {
		return nil, fmt.Errorf("database already has data; import is only allowed into an empty database")
	}
	x, err := excelize.OpenReader(r)
	if err != nil {
		return nil, err
	}
	defer x.Close()
	rep := &ImportReport{}
	cats, err := a.Categories()
	if err != nil {
		return nil, err
	}

	// read settings before the transaction: the pool has a single connection
	rate, lateFee := a.SettingFloat("rate_per_sqft"), Paise(a.SettingFloat("late_fee"))

	tx, err := a.DB.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	// 1. flats from Master List (source of truth; the QUARTER sheet has stale entries)
	ml, err := x.GetRows("Master List")
	if err != nil {
		return nil, fmt.Errorf("sheet 'Master List': %w", err)
	}
	notes := map[string]string{}
	if qrows, err := x.GetRows("QUARTER"); err == nil {
		for _, r := range qrows {
			if len(r) >= 5 && strings.TrimSpace(r[4]) != "" {
				notes[strings.ToUpper(strings.TrimSpace(r[0]))] = strings.TrimSpace(r[4])
			}
		}
	}
	col := func(r []string, i int) string {
		if i < len(r) {
			return strings.TrimSpace(r[i])
		}
		return ""
	}
	known := map[string]bool{}
	for _, r := range ml[1:] {
		code := strings.ToUpper(col(r, 0))
		if code == "" {
			continue
		}
		area := num(col(r, 2))
		if area <= 0 {
			rep.Warnings = append(rep.Warnings, fmt.Sprintf("Master List: %s has no area, skipped", code))
			continue
		}
		note := notes[code]
		if note != "" {
			note = "From sheet: " + note
		}
		if _, err := tx.Exec(`INSERT INTO flats(code,owner,area_sqft,phone1,phone2,email1,email2,notes,sort_key) VALUES(?,?,?,?,?,?,?,?,?)`,
			code, col(r, 1), area, col(r, 3), col(r, 4), col(r, 5), col(r, 6), note, FlatSortKey(code)); err != nil {
			return nil, err
		}
		known[code] = true
		rep.Flats++
	}

	// 2. historical quarters (closed, no automatic late fees — the sheet already has penalties)
	for _, sq := range sheetQuarters {
		q := sq.q
		if _, err := tx.Exec(`INSERT INTO periods(id,fy,quarter,start_date,end_date,due_date,rate_per_sqft,late_fee,auto_late_fee,status,invoiced_at)
			VALUES(?,?,?,?,?,?,?,?,0,'closed',`+a.DB.Now()+`)`,
			q.ID(), q.FY, q.Q, q.Start().Format(DateFmt), q.End().Format(DateFmt), q.DefaultDue().Format(DateFmt),
			rate, lateFee); err != nil {
			return nil, err
		}
		rep.Periods++
	}

	// 3. maintenance ledger
	const ms = "Maintenance"
	mrows, err := x.GetRows(ms)
	if err != nil {
		return nil, fmt.Errorf("sheet 'Maintenance': %w", err)
	}
	calc := func(cell string) string {
		v, err := x.CalcCellValue(ms, cell)
		if err != nil {
			v, _ = x.GetCellValue(ms, cell)
		}
		return v
	}
	ins := func(flat, date, kind string, period *string, seq *int, amount int64, desc string) error {
		if amount == 0 && kind != "invoice" {
			return nil
		}
		mode := ""
		if kind == "payment" {
			mode = "bank"
		}
		_, err := tx.Exec(`INSERT INTO ledger(flat,date,kind,period_id,seq,amount,description,mode,source) VALUES(?,?,?,?,?,?,?,?, 'import')`,
			flat, date, kind, period, seq, amount, desc, mode)
		if err == nil {
			rep.LedgerEntries++
		}
		return err
	}
	for i := 2; i <= len(mrows); i++ {
		row := strconv.Itoa(i)
		code := strings.ToUpper(strings.TrimSpace(calc("A" + row)))
		if code == "" {
			continue
		}
		if !known[code] {
			rep.Warnings = append(rep.Warnings, fmt.Sprintf("Maintenance row %d: flat %q not in Master List, skipped", i, code))
			continue
		}
		quarterly := Paise(num(calc("F" + row)))
		first := sheetQuarters[0].q
		openDate := first.Start().AddDate(0, 0, -1).Format(DateFmt)
		running := Paise(num(calc("L" + row)))
		if err := ins(code, openDate, "opening", nil, nil, running, "Dues brought forward from treasury sheet (before "+first.ID()+")"); err != nil {
			return nil, err
		}
		for _, sq := range sheetQuarters {
			q := sq.q
			pid := q.ID()
			zero := 0
			start, end := q.Start().Format(DateFmt), q.End().Format(DateFmt)
			if err := ins(code, start, "invoice", &pid, &zero, quarterly, "Maintenance "+pid+" (imported, sheet "+sq.sheetLabel+")"); err != nil {
				return nil, err
			}
			pen := Paise(num(calc(sq.penaltyCol + row)))
			one := 1
			if err := ins(code, end, "late_fee", &pid, &one, pen, "Late payment penalty "+pid+" (imported)"); err != nil {
				return nil, err
			}
			paid := Paise(num(calc(sq.paidCol + row)))
			if err := ins(code, end, "payment", nil, nil, -paid, "Payment for "+pid+" (as per treasury sheet, "+sq.sheetLabel+")"); err != nil {
				return nil, err
			}
			running += quarterly + pen - paid
			sheetDue := Paise(num(calc(sq.due + row)))
			if diff := sheetDue - running; diff != 0 {
				if err := ins(code, end, "adjustment", nil, nil, diff, "Correction to match sheet balance for "+pid); err != nil {
					return nil, err
				}
				rep.Warnings = append(rep.Warnings, fmt.Sprintf("%s %s: sheet due ₹%s differs from computed ₹%s; adjustment posted",
					code, pid, Rupees(sheetDue), Rupees(running)))
				running = sheetDue
			}
		}
		if running != 0 {
			rep.OutstandingFlat = append(rep.OutstandingFlat, fmt.Sprintf("%s: ₹%s", code, Rupees(running)))
		}
	}

	// 4. expenses
	erows, err := x.GetRows("Expenses", excelize.Options{RawCellValue: true})
	if err != nil {
		return nil, fmt.Errorf("sheet 'Expenses': %w", err)
	}
	for i, r := range erows {
		if i == 0 || strings.TrimSpace(col(r, 1)) == "" {
			continue
		}
		desc := col(r, 1)
		amtStr := col(r, 3)
		if _, err := strconv.ParseFloat(strings.ReplaceAll(amtStr, ",", ""), 64); err != nil {
			rep.Warnings = append(rep.Warnings, fmt.Sprintf("Expenses row %d (%s): amount %q is not a number, skipped — add it manually", i+1, desc, amtStr))
			continue
		}
		date, swapped, err := fixDate(col(r, 0))
		if err != nil {
			rep.Warnings = append(rep.Warnings, fmt.Sprintf("Expenses row %d (%s): %v, skipped", i+1, desc, err))
			continue
		}
		if swapped {
			rep.DatesCorrected++
		}
		amount := Paise(num(amtStr))
		payee := col(r, 5)
		cat := Categorize(cats, desc+" "+payee, amount)
		note := ""
		if swapped {
			note = "Date corrected from Excel mm/dd interpretation"
		}
		if _, err := tx.Exec(`INSERT INTO expenses(date,description,category,payee,amount,notes,source) VALUES(?,?,?,?,?,?,'import')`,
			date, desc, cat, payee, amount, note); err != nil {
			return nil, err
		}
		rep.Expenses++
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return rep, nil
}
