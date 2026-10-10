package app

import (
	"archive/zip"
	"fmt"
	"io"

	"github.com/go-pdf/fpdf"
)

// Core PDF fonts are Latin-1 only, so amounts use "Rs." rather than the rupee sign.
func rs(p int64) string { return "Rs. " + Rupees(p) }

type InvoiceData struct {
	Number      string `json:"number"`
	Flat        Flat   `json:"flat"`
	Period      Period `json:"period"`
	Charge      int64  `json:"charge"`
	BroughtFwd  int64  `json:"brought_forward"` // balance before the quarter started
	LateFees    int64  `json:"late_fees"`       // charged since quarter start
	Payments    int64  `json:"payments"`        // received since quarter start (positive)
	Adjustments int64  `json:"adjustments"`
	Payable     int64  `json:"payable"`
	Issued      string `json:"issued"`
}

func (a *App) Invoice(flatCode, periodID string) (*InvoiceData, error) {
	f, err := a.Flat(flatCode)
	if err != nil {
		return nil, err
	}
	p, err := a.Period(periodID)
	if err != nil {
		return nil, err
	}
	d := &InvoiceData{Number: fmt.Sprintf("VL/%s/%s", p.ID, f.Code), Flat: f, Period: p, Issued: p.StartDate}
	if p.InvoicedAt != nil && len(*p.InvoicedAt) >= 10 {
		d.Issued = (*p.InvoicedAt)[:10]
	}
	if err := a.DB.QueryRow(`SELECT amount FROM ledger WHERE flat=? AND period_id=? AND kind='invoice'`, f.Code, p.ID).Scan(&d.Charge); err != nil {
		return nil, fmt.Errorf("no invoice raised for %s in %s", f.Code, p.ID)
	}
	a.DB.QueryRow(`SELECT COALESCE(SUM(amount),0) FROM ledger WHERE flat=? AND date<?`, f.Code, p.StartDate).Scan(&d.BroughtFwd)
	a.DB.QueryRow(`SELECT COALESCE(SUM(amount),0) FROM ledger WHERE flat=? AND date>=? AND kind='late_fee'`, f.Code, p.StartDate).Scan(&d.LateFees)
	a.DB.QueryRow(`SELECT COALESCE(-SUM(amount),0) FROM ledger WHERE flat=? AND date>=? AND kind='payment'`, f.Code, p.StartDate).Scan(&d.Payments)
	a.DB.QueryRow(`SELECT COALESCE(SUM(amount),0) FROM ledger WHERE flat=? AND date>=? AND kind IN ('adjustment','opening')`, f.Code, p.StartDate).Scan(&d.Adjustments)
	// invoices for later quarters are deliberately left out of this document
	d.Payable = d.BroughtFwd + d.Charge + d.LateFees + d.Adjustments - d.Payments
	return d, nil
}

func newDoc() *fpdf.Fpdf {
	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.SetMargins(18, 16, 18)
	pdf.SetAutoPageBreak(true, 16)
	return pdf
}

func (a *App) letterhead(pdf *fpdf.Fpdf, title string) {
	s, _ := a.Settings()
	pdf.SetFont("Helvetica", "B", 15)
	pdf.SetTextColor(20, 40, 70)
	pdf.CellFormat(0, 8, s["association_name"], "", 1, "L", false, 0, "")
	pdf.SetFont("Helvetica", "", 9)
	pdf.SetTextColor(90, 90, 90)
	pdf.MultiCell(0, 4.5, s["association_address"], "", "L", false)
	pdf.Ln(3)
	pdf.SetDrawColor(20, 40, 70)
	pdf.SetLineWidth(0.6)
	pdf.Line(18, pdf.GetY(), 192, pdf.GetY())
	pdf.Ln(5)
	pdf.SetFont("Helvetica", "B", 13)
	pdf.SetTextColor(0, 0, 0)
	pdf.CellFormat(0, 7, title, "", 1, "L", false, 0, "")
	pdf.Ln(2)
}

func kv(pdf *fpdf.Fpdf, k, v string, w float64) {
	pdf.SetFont("Helvetica", "", 9.5)
	pdf.SetTextColor(100, 100, 100)
	pdf.CellFormat(32, 5.5, k, "", 0, "L", false, 0, "")
	pdf.SetTextColor(0, 0, 0)
	pdf.SetFont("Helvetica", "B", 9.5)
	pdf.CellFormat(w, 5.5, v, "", 0, "L", false, 0, "")
}

func row(pdf *fpdf.Fpdf, label, amount string, bold bool, fill bool) {
	style := ""
	if bold {
		style = "B"
	}
	pdf.SetFont("Helvetica", style, 10)
	if fill {
		pdf.SetFillColor(234, 240, 248)
	}
	pdf.CellFormat(124, 8, "  "+label, "B", 0, "L", fill, 0, "")
	pdf.CellFormat(50, 8, amount+"  ", "B", 1, "R", fill, 0, "")
}

func fmtDate(s string) string {
	t, err := ParseDate(s)
	if err != nil {
		return s
	}
	return t.Format("02 Jan 2006")
}

func (a *App) InvoicePDF(w io.Writer, d *InvoiceData) error {
	s, _ := a.Settings()
	pdf := newDoc()
	pdf.AddPage()
	a.letterhead(pdf, "Maintenance Invoice")

	y := pdf.GetY()
	kv(pdf, "Invoice no.", d.Number, 60)
	pdf.Ln(5.5)
	kv(pdf, "Issued on", fmtDate(d.Issued), 60)
	pdf.Ln(5.5)
	kv(pdf, "Period", fmt.Sprintf("%s (%s - %s)", d.Period.ID, fmtDate(d.Period.StartDate), fmtDate(d.Period.EndDate)), 60)
	pdf.Ln(5.5)
	kv(pdf, "Due date", fmtDate(d.Period.DueDate), 60)
	pdf.SetXY(110, y)
	kv(pdf, "Flat", d.Flat.Code, 40)
	pdf.SetXY(110, y+5.5)
	kv(pdf, "Owner", d.Flat.Owner, 40)
	pdf.SetXY(110, y+11)
	kv(pdf, "Area", fmt.Sprintf("%.0f sq ft", d.Flat.AreaSqft), 40)
	pdf.SetXY(110, y+16.5)
	kv(pdf, "Rate", fmt.Sprintf("Rs. %g / sq ft / month", d.Period.RatePerSqft), 40)
	pdf.SetXY(18, y+28)

	pdf.SetFont("Helvetica", "B", 9)
	pdf.SetFillColor(20, 40, 70)
	pdf.SetTextColor(255, 255, 255)
	pdf.CellFormat(124, 7, "  Particulars", "", 0, "L", true, 0, "")
	pdf.CellFormat(50, 7, "Amount  ", "", 1, "R", true, 0, "")
	pdf.SetTextColor(0, 0, 0)
	if d.BroughtFwd != 0 {
		label := "Previous dues brought forward"
		if d.BroughtFwd < 0 {
			label = "Advance / credit brought forward"
		}
		row(pdf, label, rs(d.BroughtFwd), false, false)
	}
	row(pdf, fmt.Sprintf("Maintenance %s: %.0f sq ft x Rs. %g x 3 months", d.Period.ID, d.Flat.AreaSqft, d.Period.RatePerSqft), rs(d.Charge), false, false)
	if d.LateFees != 0 {
		row(pdf, "Late payment fees", rs(d.LateFees), false, false)
	}
	if d.Adjustments != 0 {
		row(pdf, "Adjustments", rs(d.Adjustments), false, false)
	}
	if d.Payments != 0 {
		row(pdf, "Less: payments received", "- "+rs(d.Payments), false, false)
	}
	label := "Amount payable"
	if d.Payable <= 0 {
		label = "Amount payable (fully paid - thank you)"
	}
	row(pdf, label, rs(max(d.Payable, 0)), true, true)
	pdf.Ln(6)

	pdf.SetFont("Helvetica", "B", 10)
	pdf.CellFormat(0, 6, "How to pay", "", 1, "L", false, 0, "")
	pdf.SetFont("Helvetica", "", 9.5)
	for _, l := range []string{
		"Account name: " + s["bank_account_name"],
		"Account no.: " + s["bank_account_no"] + "    IFSC: " + s["bank_ifsc"],
		"Bank: " + s["bank_name"],
	} {
		pdf.CellFormat(0, 5, l, "", 1, "L", false, 0, "")
	}
	pdf.Ln(2)
	pdf.SetFont("Helvetica", "B", 9.5)
	pdf.SetTextColor(160, 40, 30)
	pdf.MultiCell(0, 5, fmt.Sprintf("Please write \"%s %s\" in the payment remarks so your payment is credited to your flat.", d.Flat.Code, d.Period.ID), "", "L", false)
	pdf.SetTextColor(0, 0, 0)
	pdf.SetFont("Helvetica", "", 9)
	pdf.MultiCell(0, 4.8, fmt.Sprintf("A late fee of %s applies for every month (or part of a month) the payment is delayed beyond the due date of %s.",
		rs(d.Period.LateFee), fmtDate(d.Period.DueDate)), "", "L", false)
	pdf.Ln(10)
	pdf.SetFont("Helvetica", "I", 8.5)
	pdf.SetTextColor(120, 120, 120)
	pdf.CellFormat(0, 5, s["signatory"]+", "+s["association_name"], "", 1, "R", false, 0, "")
	pdf.CellFormat(0, 5, "This is a computer-generated invoice and does not require a signature.", "", 1, "R", false, 0, "")
	return pdf.Output(w)
}

func (a *App) InvoicesZip(w io.Writer, periodID string) error {
	flats, err := a.Flats()
	if err != nil {
		return err
	}
	zw := zip.NewWriter(w)
	for _, f := range flats {
		d, err := a.Invoice(f.Code, periodID)
		if err != nil {
			continue
		}
		fw, err := zw.Create(fmt.Sprintf("%s_%s_invoice.pdf", periodID, f.Code))
		if err != nil {
			return err
		}
		if err := a.InvoicePDF(fw, d); err != nil {
			return err
		}
	}
	return zw.Close()
}

func (a *App) PnLPDF(w io.Writer, p *PnL) error {
	pdf := newDoc()
	pdf.AddPage()
	title := fmt.Sprintf("Income & Expenditure Statement - %s", p.PeriodID)
	a.letterhead(pdf, title)
	pdf.SetFont("Helvetica", "", 9.5)
	period := fmt.Sprintf("%s to %s", fmtDate(p.From), fmtDate(p.To))
	if p.Running {
		period += fmt.Sprintf("  (running - figures up to %s)", fmtDate(Today().Format(DateFmt)))
	}
	pdf.CellFormat(0, 5, period, "", 1, "L", false, 0, "")
	pdf.Ln(3)
	section := func(name string, lines []Line, totalLabel string, total int64) {
		pdf.SetFont("Helvetica", "B", 9)
		pdf.SetFillColor(20, 40, 70)
		pdf.SetTextColor(255, 255, 255)
		pdf.CellFormat(124, 7, "  "+name, "", 0, "L", true, 0, "")
		pdf.CellFormat(50, 7, "Amount  ", "", 1, "R", true, 0, "")
		pdf.SetTextColor(0, 0, 0)
		for _, l := range lines {
			lbl := l.Label
			if l.Count > 0 {
				lbl += fmt.Sprintf(" (%d)", l.Count)
			}
			row(pdf, lbl, rs(l.Amount), false, false)
		}
		row(pdf, totalLabel, rs(total), true, true)
		pdf.Ln(4)
	}
	// income: charged in the quarter (receivable) vs money received (actual)
	pdf.SetFont("Helvetica", "B", 9)
	pdf.SetFillColor(20, 40, 70)
	pdf.SetTextColor(255, 255, 255)
	pdf.CellFormat(94, 7, "  Income", "", 0, "L", true, 0, "")
	pdf.CellFormat(40, 7, "Receivable", "", 0, "R", true, 0, "")
	pdf.CellFormat(40, 7, "Actual  ", "", 1, "R", true, 0, "")
	pdf.SetTextColor(0, 0, 0)
	incomeRow := func(label, recv, act string, bold bool) {
		style := ""
		if bold {
			style = "B"
		}
		pdf.SetFont("Helvetica", style, 10)
		pdf.SetFillColor(234, 240, 248)
		pdf.CellFormat(94, 8, "  "+label, "B", 0, "L", bold, 0, "")
		pdf.CellFormat(40, 8, recv, "B", 0, "R", bold, 0, "")
		pdf.CellFormat(40, 8, act+"  ", "B", 1, "R", bold, 0, "")
	}
	for _, l := range p.Income {
		recv := rs(l.Receivable)
		if l.NoReceivable {
			recv = "-"
		}
		incomeRow(l.Label, recv, rs(l.Actual), false)
	}
	incomeRow("Total income", rs(p.TotalReceivable), rs(p.TotalIncome), true)
	pdf.SetFont("Helvetica", "I", 8)
	pdf.SetTextColor(110, 110, 110)
	pdf.MultiCell(0, 4.5, "Receivable: charged for the period. Actual: money received during the period, applied to the oldest dues first. Surplus is on actuals.", "", "L", false)
	pdf.SetTextColor(0, 0, 0)
	pdf.Ln(3)
	section("Expenditure", p.Expenses, "Total expenditure", p.TotalExpenses)
	label := "Surplus transferred to sinking fund (actual income - expenditure)"
	if p.Running {
		label = "Surplus to date (to be transferred to sinking fund)"
	}
	if p.Surplus < 0 {
		label = "Deficit for the period"
	}
	row(pdf, label, rs(p.Surplus), true, true)
	pdf.Ln(4)

	// sinking fund movement
	closing := "Sinking fund balance carried forward"
	if p.Running {
		closing = "Sinking fund balance (to date)"
	}
	section("Sinking fund", []Line{
		{Label: "Balance brought forward", Amount: p.FundOpening},
		{Label: "Add: surplus for " + p.PeriodID, Amount: p.Surplus},
	}, closing, p.FundClosing)
	pdf.SetFont("Helvetica", "B", 9.5)
	pdf.CellFormat(124, 6, "Current sinking fund balance as of "+fmtDate(p.FundAsOf), "", 0, "L", false, 0, "")
	pdf.CellFormat(50, 6, rs(p.FundCurrent), "", 1, "R", false, 0, "")
	pdf.Ln(4)

	// bank position: what the account holds versus what the books attribute
	if p.HasBankBalance {
		section("Bank account vs sinking fund", []Line{
			{Label: "Savings account balance as of " + fmtDate(p.BankBalanceOn), Amount: p.BankBalance},
			{Label: "Add: fixed / recurring deposits", Amount: p.DepositsBalance},
			{Label: "Less: sinking fund (recorded surpluses)", Amount: -p.FundClosing},
			{Label: "Less: advances held for members", Amount: -p.AdvancesHeld},
		}, "Reserves not yet allocated to a fund", p.UnallocatedReserve)
		pdf.SetFont("Helvetica", "I", 8)
		pdf.SetTextColor(110, 110, 110)
		pdf.MultiCell(0, 4.5, "Money held before the recorded history (corpus / handover deposits, surpluses before the first recorded quarter) and timing differences "+
			"(bills paid later, petty-cash float). Set the sinking fund opening balance in Settings to allocate it.", "", "L", false)
		pdf.SetTextColor(0, 0, 0)
		pdf.Ln(3)
	} else {
		pdf.SetFont("Helvetica", "I", 8.5)
		pdf.CellFormat(0, 5, "No bank statement imported for "+fmtDate(p.To)+"; import one to reconcile the account balance.", "", 1, "L", false, 0, "")
		pdf.Ln(2)
	}
	if p.BankCurrentOn != "" {
		pdf.SetFont("Helvetica", "B", 9.5)
		pdf.CellFormat(124, 6, "Current savings account balance as of "+fmtDate(p.BankCurrentOn), "", 0, "L", false, 0, "")
		pdf.CellFormat(50, 6, rs(p.BankCurrent), "", 1, "R", false, 0, "")
		if dep := a.DepositsValueOn(Today().Format(DateFmt)); dep > 0 {
			pdf.CellFormat(124, 6, "Current FD / RD holdings", "", 0, "L", false, 0, "")
			pdf.CellFormat(50, 6, rs(dep), "", 1, "R", false, 0, "")
		}
		pdf.Ln(4)
	}

	pdf.SetFont("Helvetica", "B", 9.5)
	pdf.CellFormat(0, 6, "Supporting figures", "", 1, "L", false, 0, "")
	pdf.SetFont("Helvetica", "", 9)
	for _, l := range [][2]string{
		{"Surplus if all receivables were collected", rs(p.SurplusIfPaid)},
		{"Maintenance collected during the period (incl. advances)", rs(p.Collections)},
		{"Outstanding member dues at period end", rs(p.Receivables)},
	} {
		pdf.CellFormat(124, 5.5, l[0], "", 0, "L", false, 0, "")
		pdf.CellFormat(50, 5.5, l[1], "", 1, "R", false, 0, "")
	}
	pdf.Ln(4)

	// itemised entries kept out of the P&L
	tr := pdf.UnicodeTranslatorFromDescriptor("")
	pdf.SetFont("Helvetica", "B", 9)
	pdf.SetFillColor(20, 40, 70)
	pdf.SetTextColor(255, 255, 255)
	pdf.CellFormat(24, 7, "  Date", "", 0, "L", true, 0, "")
	pdf.CellFormat(80, 7, "Non P&L items (advances, adjustments)", "", 0, "L", true, 0, "")
	pdf.CellFormat(34, 7, "Payee", "", 0, "L", true, 0, "")
	pdf.CellFormat(36, 7, "Amount  ", "", 1, "R", true, 0, "")
	pdf.SetTextColor(0, 0, 0)
	pdf.SetFont("Helvetica", "", 9)
	if len(p.NonPnLItems) == 0 {
		pdf.CellFormat(174, 7, "  None in this period", "B", 1, "L", false, 0, "")
	}
	for _, it := range p.NonPnLItems {
		desc := tr(it.Description)
		for pdf.GetStringWidth(desc) > 78 && len(desc) > 3 {
			desc = desc[:len(desc)-4] + "..."
		}
		pdf.CellFormat(24, 7, "  "+fmtDate(it.Date), "B", 0, "L", false, 0, "")
		pdf.CellFormat(80, 7, desc, "B", 0, "L", false, 0, "")
		pdf.CellFormat(34, 7, tr(it.Payee), "B", 0, "L", false, 0, "")
		pdf.CellFormat(36, 7, rs(it.Amount)+"  ", "B", 1, "R", false, 0, "")
	}
	pdf.SetFont("Helvetica", "B", 9)
	pdf.SetFillColor(234, 240, 248)
	pdf.CellFormat(138, 7, "  Total non P&L items", "B", 0, "L", true, 0, "")
	pdf.CellFormat(36, 7, rs(p.NonPnLOutflow)+"  ", "B", 1, "R", true, 0, "")
	pdf.Ln(4)
	pdf.SetFont("Helvetica", "I", 8)
	pdf.SetTextColor(120, 120, 120)
	pdf.MultiCell(0, 4.5, "Income on actuals (money received); expenses on the bill date.", "", "L", false)
	return pdf.Output(w)
}
