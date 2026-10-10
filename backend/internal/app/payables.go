package app

import (
	"fmt"
	"io"
	"sort"
	"strings"
)

// Payables: bills recorded since the cut-over that haven't been paid from the
// bank yet, itemised per payee, and the payment instruction sent to whoever
// operates the bank account. Bank payments to a payee settle that payee's
// oldest bills first; negative entries (e.g. payment on hold) are deductions.

type Payee struct {
	Name        string `json:"name"`
	Method      string `json:"method"`
	AccountName string `json:"account_name"`
	AccountNo   string `json:"account_no"`
	IFSC        string `json:"ifsc"`
	UPI         string `json:"upi"`
	Reference   string `json:"reference"`
	Notes       string `json:"notes"`
}

// Complete reports whether the account operator has enough to make the transfer.
func (p Payee) Complete() bool {
	switch strings.ToUpper(p.Method) {
	case "UPI":
		return p.UPI != ""
	case "BILLPAY":
		return p.Reference != ""
	case "CASH", "CHEQUE":
		return true
	}
	return p.AccountNo != "" && p.IFSC != ""
}

// BeneficiaryLines splits the beneficiary into a name line and a details line
// so account numbers / IFSC are never truncated in print.
func (p Payee) BeneficiaryLines() (string, string) {
	switch strings.ToUpper(p.Method) {
	case "UPI":
		return firstNonEmpty(p.AccountName, p.Name), "UPI ID " + p.UPI
	case "BILLPAY":
		return firstNonEmpty(p.AccountName, p.Name) + " (bill payment)", "Consumer / ref no. " + p.Reference
	case "CASH":
		return p.Name, "Cash"
	case "CHEQUE":
		return "Cheque in favour of " + firstNonEmpty(p.AccountName, p.Name), ""
	}
	if p.AccountNo == "" {
		return "Bank details not on record", "Add them under Reminders > Payables"
	}
	return firstNonEmpty(p.AccountName, p.Name), fmt.Sprintf("A/c %s   IFSC %s", p.AccountNo, p.IFSC)
}

// Beneficiary is a one-line description of where the money goes.
func (p Payee) Beneficiary() string {
	switch strings.ToUpper(p.Method) {
	case "UPI":
		return "UPI " + p.UPI
	case "BILLPAY":
		return "BillPay, consumer/ref " + p.Reference
	case "CASH":
		return "Cash"
	case "CHEQUE":
		return "Cheque in favour of " + firstNonEmpty(p.AccountName, p.Name)
	}
	if p.AccountNo == "" {
		return "bank details not on record"
	}
	return fmt.Sprintf("%s, A/c %s, IFSC %s", firstNonEmpty(p.AccountName, p.Name), p.AccountNo, p.IFSC)
}

func firstNonEmpty(s ...string) string {
	for _, x := range s {
		if strings.TrimSpace(x) != "" {
			return x
		}
	}
	return ""
}

func (a *App) Payee(name string) Payee {
	p := Payee{Name: name, Method: "NEFT"}
	a.DB.QueryRow(`SELECT method,account_name,account_no,ifsc,upi,reference,notes FROM payees WHERE name=?`, name).
		Scan(&p.Method, &p.AccountName, &p.AccountNo, &p.IFSC, &p.UPI, &p.Reference, &p.Notes)
	return p
}

func (a *App) SavePayee(p Payee) error {
	if strings.TrimSpace(p.Name) == "" {
		return fmt.Errorf("payee name required")
	}
	p.IFSC = strings.ToUpper(strings.TrimSpace(p.IFSC))
	if p.IFSC != "" && len(p.IFSC) != 11 {
		return fmt.Errorf("IFSC should be 11 characters (e.g. HDFC0005493)")
	}
	_, err := a.DB.Exec(`INSERT INTO payees(name,method,account_name,account_no,ifsc,upi,reference,notes) VALUES(?,?,?,?,?,?,?,?)
		ON CONFLICT(name) DO UPDATE SET method=excluded.method, account_name=excluded.account_name, account_no=excluded.account_no,
		ifsc=excluded.ifsc, upi=excluded.upi, reference=excluded.reference, notes=excluded.notes`,
		p.Name, p.Method, p.AccountName, strings.TrimSpace(p.AccountNo), p.IFSC, strings.TrimSpace(p.UPI), p.Reference, p.Notes)
	return err
}

type PayableItem struct {
	ExpenseID   int64  `json:"expense_id"`
	Date        string `json:"date"`
	Description string `json:"description"`
	Category    string `json:"category"`
	Amount      int64  `json:"amount"`      // bill amount (negative = deduction)
	Outstanding int64  `json:"outstanding"` // still to pay after bank payments (oldest first)
}

type PayeePayable struct {
	Payee    Payee         `json:"payee"`
	Complete bool          `json:"complete"`
	Billed   int64         `json:"billed"`
	Paid     int64         `json:"paid"`
	Balance  int64         `json:"balance"` // to transfer (negative = payee holds an advance)
	Items    []PayableItem `json:"items"`   // unpaid bills and deductions
}

type Payables struct {
	AsOf    string         `json:"as_of"`
	Since   string         `json:"since"`
	Payees  []PayeePayable `json:"payees"`
	Total   int64          `json:"total"` // sum of positive balances
	Account struct {
		Name string `json:"name"`
		No   string `json:"no"`
		IFSC string `json:"ifsc"`
		Bank string `json:"bank"`
	} `json:"account"`
	Sent []SentRequest `json:"sent"`
}

func (a *App) PayablesDetail() (*Payables, error) {
	since := a.Setting("cutover_date")
	out := &Payables{AsOf: Today().Format(DateFmt), Since: since, Payees: []PayeePayable{}, Sent: []SentRequest{}}
	s, _ := a.Settings()
	out.Account.Name, out.Account.No, out.Account.IFSC, out.Account.Bank = s["bank_account_name"], s["bank_account_no"], s["bank_ifsc"], s["bank_name"]

	type bill struct {
		id                    int64
		date, desc, cat, name string
		amt                   int64
	}
	rows, err := a.DB.Query(`SELECT id,date,description,category,payee,amount FROM expenses WHERE date>=? ORDER BY date, id`, since)
	if err != nil {
		return nil, err
	}
	byPayee := map[string][]bill{}
	for rows.Next() {
		var b bill
		if err := rows.Scan(&b.id, &b.date, &b.desc, &b.cat, &b.name, &b.amt); err != nil {
			rows.Close()
			return nil, err
		}
		b.name = firstNonEmpty(strings.TrimSpace(b.name), "Unspecified")
		byPayee[b.name] = append(byPayee[b.name], b)
	}
	rows.Close()
	paid := map[string]int64{}
	rows, err = a.DB.Query(`SELECT payee,SUM(debit) FROM bank_txns WHERE debit>0 AND kind='expense' AND status='matched' AND date>=? GROUP BY payee`, since)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var n string
		var v int64
		rows.Scan(&n, &v)
		paid[firstNonEmpty(n, "Unspecified")] = v
	}
	rows.Close()

	names := map[string]bool{}
	for n := range byPayee {
		names[n] = true
	}
	for n := range paid {
		names[n] = true
	}
	for n := range names {
		pp := PayeePayable{Payee: a.Payee(n), Paid: paid[n], Items: []PayableItem{}}
		pp.Complete = pp.Payee.Complete()
		pool := paid[n] // bank payments, applied to the oldest bills
		for _, b := range byPayee[n] {
			pp.Billed += b.amt
			it := PayableItem{ExpenseID: b.id, Date: b.date, Description: b.desc, Category: b.cat, Amount: b.amt, Outstanding: b.amt}
			if b.amt > 0 {
				used := min(pool, b.amt)
				pool -= used
				it.Outstanding = b.amt - used
			}
			if it.Outstanding != 0 {
				pp.Items = append(pp.Items, it)
			}
		}
		pp.Balance = pp.Billed - pp.Paid
		if pp.Balance != 0 || len(pp.Items) > 0 {
			out.Payees = append(out.Payees, pp)
		}
		if pp.Balance > 0 {
			out.Total += pp.Balance
		}
	}
	sort.Slice(out.Payees, func(i, j int) bool { return out.Payees[i].Balance > out.Payees[j].Balance })

	rs, err := a.DB.Query(`SELECT id,subject,text,COALESCE(sent_at,created_at) FROM reminders WHERE kind='payables' ORDER BY id DESC LIMIT 20`)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	for rs.Next() {
		var x SentRequest
		if err := rs.Scan(&x.ID, &x.Subject, &x.Text, &x.SentAt); err != nil {
			return nil, err
		}
		out.Sent = append(out.Sent, x)
	}
	return out, rs.Err()
}

// Select keeps only the chosen payees (all with something to pay when empty).
func (p *Payables) Select(names []string) {
	want := map[string]bool{}
	for _, n := range names {
		if n = strings.TrimSpace(n); n != "" {
			want[n] = true
		}
	}
	var keep []PayeePayable
	p.Total = 0
	for _, x := range p.Payees {
		if (len(want) == 0 && x.Balance > 0) || want[x.Payee.Name] {
			keep = append(keep, x)
			p.Total += x.Balance
		}
	}
	p.Payees = keep
}

// InstructionText is the message for the account operator: summary first, then items.
func (a *App) InstructionText(p *Payables) string {
	s, _ := a.Settings()
	var b strings.Builder
	fmt.Fprintf(&b, "Hi,\n\nPlease make the following %d payment(s) from the association account and share the transaction references once done.\n\n", len(p.Payees))
	fmt.Fprintf(&b, "FROM: %s, A/c %s (%s)\n\n", p.Account.Name, p.Account.No, p.Account.Bank)
	b.WriteString("SUMMARY\n")
	for i, x := range p.Payees {
		fmt.Fprintf(&b, "%d. %s - Rs.%s via %s\n   %s\n   Remarks: VLOA %s bills\n", i+1, x.Payee.Name, Rupees(x.Balance),
			firstNonEmpty(x.Payee.Method, "NEFT"), x.Payee.Beneficiary(), monthLabel(x.Items))
	}
	fmt.Fprintf(&b, "TOTAL: Rs.%s\n\nDETAILS\n", Rupees(p.Total))
	for _, x := range p.Payees {
		fmt.Fprintf(&b, "\n%s - Rs.%s\n", x.Payee.Name, Rupees(x.Balance))
		for _, it := range x.Items {
			amt := Rupees(it.Outstanding)
			if it.Outstanding != it.Amount && it.Amount > 0 {
				amt += fmt.Sprintf(" (balance of Rs.%s)", Rupees(it.Amount))
			}
			fmt.Fprintf(&b, "  - %s  %s  Rs.%s\n", fmtDate(it.Date), it.Description, amt)
		}
		if x.Paid > 0 {
			fmt.Fprintf(&b, "  (already paid since %s: Rs.%s)\n", fmtDate(p.Since), Rupees(x.Paid))
		}
	}
	fmt.Fprintf(&b, "\nRegards,\n%s\n%s", s["treasurer_name"], s["signatory"])
	return b.String()
}

// monthLabel names the bills' month(s) for the transfer remark, e.g. "Oct 2026".
func monthLabel(items []PayableItem) string {
	seen := map[string]bool{}
	var ms []string
	for _, it := range items {
		if t, err := ParseDate(it.Date); err == nil && it.Outstanding > 0 {
			m := t.Format("Jan 2006")
			if !seen[m] {
				seen[m] = true
				ms = append(ms, m)
			}
		}
	}
	if len(ms) == 0 {
		return "pending"
	}
	return strings.Join(ms, "/")
}

func (a *App) MarkInstructionSent(text string, names []string) error {
	_, err := a.DB.Exec(`INSERT INTO reminders(period_id,seq,text,flats,status,sent_at,kind,subject) VALUES(?,0,?,'[]','sent',`+a.DB.Now()+`,'payables',?)`,
		CurrentPeriodID(), text, "Payment instruction: "+strings.Join(names, ", "))
	return err
}

const pageLeft = 18.0 // newDoc() margin

// InstructionPDF: summary table on top, itemised bills per payee below (A4 portrait).
func (a *App) InstructionPDF(w io.Writer, p *Payables) error {
	pdf := newDoc()
	pdf.SetAutoPageBreak(true, 18)
	pdf.AddPage()
	a.letterhead(pdf, "Payment instruction - "+fmtDate(p.AsOf))
	pdf.SetFont("Helvetica", "", 9)
	pdf.MultiCell(0, 5, fmt.Sprintf("Please transfer the amounts below from %s, A/c %s, IFSC %s (%s), and share the transaction references once done.",
		p.Account.Name, p.Account.No, p.Account.IFSC, p.Account.Bank), "", "L", false)
	pdf.Ln(3)

	head := func(cols []string, widths []float64, aligns string) {
		pdf.SetFont("Helvetica", "B", 8.5)
		pdf.SetFillColor(20, 40, 70)
		pdf.SetTextColor(255, 255, 255)
		for i, c := range cols {
			pdf.CellFormat(widths[i], 7, " "+c+" ", "", 0, string(aligns[i]), true, 0, "")
		}
		pdf.Ln(-1)
		pdf.SetTextColor(0, 0, 0)
	}
	// summary
	w1 := []float64{7, 38, 17, 84, 28}
	head([]string{"#", "Payee", "Method", "Beneficiary", "Amount"}, w1, "LLLLR")
	pdf.SetFont("Helvetica", "", 8.5)
	for i, x := range p.Payees {
		l1, l2 := x.Payee.BeneficiaryLines()
		fill := !x.Complete
		if fill {
			pdf.SetFillColor(253, 230, 196)
		}
		y := pdf.GetY()
		h := 10.0
		pdf.SetFont("Helvetica", "", 8.5)
		pdf.CellFormat(w1[0], h, fmt.Sprintf(" %d", i+1), "B", 0, "L", fill, 0, "")
		pdf.CellFormat(w1[1], h, " "+x.Payee.Name, "B", 0, "L", fill, 0, "")
		pdf.CellFormat(w1[2], h, " "+firstNonEmpty(x.Payee.Method, "NEFT"), "B", 0, "L", fill, 0, "")
		bx := pdf.GetX()
		pdf.CellFormat(w1[3], h, "", "B", 0, "L", fill, 0, "") // background + rule
		pdf.SetFont("Helvetica", "B", 9)
		pdf.CellFormat(w1[4], h, rs(x.Balance)+" ", "B", 1, "R", fill, 0, "")
		end := pdf.GetY()
		pdf.SetXY(bx, y+1)
		pdf.SetFont("Helvetica", "", 8.5)
		pdf.CellFormat(w1[3], 4, " "+l1, "", 2, "L", false, 0, "")
		pdf.SetFont("Courier", "", 8.5) // fixed width: account numbers easy to read back
		pdf.CellFormat(w1[3], 4, " "+l2, "", 0, "L", false, 0, "")
		pdf.SetXY(pageLeft, end)
	}
	pdf.SetFont("Helvetica", "B", 9.5)
	pdf.SetFillColor(234, 240, 248)
	pdf.CellFormat(w1[0]+w1[1]+w1[2]+w1[3], 8, " Total to transfer", "B", 0, "L", true, 0, "")
	pdf.CellFormat(w1[4], 8, rs(p.Total)+" ", "B", 1, "R", true, 0, "")
	for _, x := range p.Payees {
		if !x.Complete {
			pdf.SetFont("Helvetica", "I", 7.5)
			pdf.SetTextColor(160, 90, 0)
			pdf.CellFormat(0, 5, "Highlighted payees have incomplete beneficiary details - confirm with the treasurer before transferring.", "", 1, "L", false, 0, "")
			pdf.SetTextColor(0, 0, 0)
			break
		}
	}

	// itemised
	pdf.Ln(5)
	pdf.SetFont("Helvetica", "B", 11)
	pdf.SetTextColor(20, 40, 70)
	pdf.CellFormat(0, 7, "Itemised bills", "", 1, "L", false, 0, "")
	pdf.SetTextColor(0, 0, 0)
	w2 := []float64{24, 92, 30, 28}
	for _, x := range p.Payees {
		if pdf.GetY() > 250 {
			pdf.AddPage()
		}
		pdf.Ln(2)
		pdf.SetFont("Helvetica", "B", 9.5)
		pdf.CellFormat(w2[0]+w2[1]+w2[2], 6, x.Payee.Name, "", 0, "L", false, 0, "")
		pdf.CellFormat(w2[3], 6, rs(x.Balance), "", 1, "R", false, 0, "")
		head([]string{"Date", "Particulars", "Category", "Amount"}, w2, "LLLR")
		pdf.SetFont("Helvetica", "", 8.5)
		tr := pdf.UnicodeTranslatorFromDescriptor("")
		for _, it := range x.Items {
			desc := tr(it.Description)
			if it.Outstanding != it.Amount && it.Amount > 0 {
				desc += " (part)"
			}
			for pdf.GetStringWidth(desc) > w2[1]-3 && len(desc) > 6 {
				desc = desc[:len(desc)-4] + "..."
			}
			pdf.CellFormat(w2[0], 6, " "+fmtDate(it.Date), "B", 0, "L", false, 0, "")
			pdf.CellFormat(w2[1], 6, " "+desc, "B", 0, "L", false, 0, "")
			pdf.CellFormat(w2[2], 6, " "+it.Category, "B", 0, "L", false, 0, "")
			pdf.CellFormat(w2[3], 6, rs(it.Outstanding)+" ", "B", 1, "R", false, 0, "")
		}
		if x.Paid > 0 {
			pdf.SetFont("Helvetica", "I", 7.5)
			pdf.SetTextColor(110, 110, 110)
			pdf.CellFormat(0, 5, fmt.Sprintf("Already paid since %s: %s (applied to the oldest bills).", fmtDate(p.Since), rs(x.Paid)), "", 1, "L", false, 0, "")
			pdf.SetTextColor(0, 0, 0)
		}
	}

	s, _ := a.Settings()
	pdf.Ln(12)
	pdf.SetFont("Helvetica", "", 9)
	pdf.CellFormat(0, 5, "Authorised by", "", 1, "R", false, 0, "")
	pdf.Ln(8)
	pdf.SetFont("Helvetica", "B", 9)
	pdf.CellFormat(0, 5, firstNonEmpty(s["treasurer_name"], "________________"), "", 1, "R", false, 0, "")
	pdf.SetFont("Helvetica", "", 8.5)
	pdf.CellFormat(0, 5, s["signatory"]+", "+s["association_name"], "", 1, "R", false, 0, "")
	return pdf.Output(w)
}
