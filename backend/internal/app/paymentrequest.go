package app

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/go-pdf/fpdf"
)

// The "day 0" payment request goes out when a quarter's invoices are raised:
// an email to every owner plus a PDF listing each flat's dues, in the layout
// of the treasury sheet's maintenance tab.

type PaymentRequestRow struct {
	Flat        string `json:"flat"`
	Owner       string `json:"owner"`
	Quarterly   int64  `json:"quarterly"`
	PrevDue     int64  `json:"prev_due"`    // carried in from before the quarter (negative = advance)
	Penalties   int64  `json:"penalties"`   // late fees charged since the quarter began
	Adjustments int64  `json:"adjustments"` // waivers / corrections since the quarter began
	Payment     int64  `json:"payment"`     // total payable
	Paid        int64  `json:"paid"`        // received since the quarter began
	Due         int64  `json:"due"`
}

type SentRequest struct {
	ID      int64  `json:"id"`
	Subject string `json:"subject"`
	Text    string `json:"text"`
	SentAt  string `json:"sent_at"`
}

type PaymentRequest struct {
	PeriodID     string              `json:"period_id"`
	PrevPeriodID string              `json:"prev_period_id"`
	AsOf         string              `json:"as_of"`
	Subject      string              `json:"subject"`
	Body         string              `json:"body"`
	Emails       []string            `json:"emails"`
	MissingEmail []string            `json:"missing_email"` // flats without an email address
	Rows         []PaymentRequestRow `json:"rows"`
	Total        PaymentRequestRow   `json:"total"`
	Sent         []SentRequest       `json:"sent"`
}

var quarterMonths = map[int]string{1: "Apr-May-Jun", 2: "Jul-Aug-Sep", 3: "Oct-Nov-Dec", 4: "Jan-Feb-Mar"}

// plainDate: "31st August 2026" (the mail's style, no comma).
func plainDate(t time.Time) string {
	return fmt.Sprintf("%s %s %d", ordinal(t.Day()), t.Month(), t.Year())
}

func (a *App) PaymentRequestFor(periodID string) (*PaymentRequest, error) {
	p, err := a.Period(periodID)
	if err != nil {
		return nil, err
	}
	if p.InvoicedAt == nil {
		return nil, fmt.Errorf("raise the %s invoices first (Billing & invoices)", periodID)
	}
	q, _ := ParseQuarter(periodID)
	pr := &PaymentRequest{PeriodID: periodID, PrevPeriodID: q.Prev().ID(), AsOf: Today().Format(DateFmt), Emails: []string{}, MissingEmail: []string{}}

	flats, err := a.Flats()
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, f := range flats {
		inv, err := a.Invoice(f.Code, periodID)
		if err != nil {
			continue // flat added after the invoices were raised
		}
		r := PaymentRequestRow{
			Flat: f.Code, Owner: f.Owner, Quarterly: inv.Charge, PrevDue: inv.BroughtFwd,
			Penalties: inv.LateFees, Adjustments: inv.Adjustments, Paid: inv.Payments,
		}
		r.Payment = r.PrevDue + r.Penalties + r.Adjustments + r.Quarterly
		r.Due = r.Payment - r.Paid
		pr.Rows = append(pr.Rows, r)
		t := &pr.Total
		t.Quarterly += r.Quarterly
		t.PrevDue += r.PrevDue
		t.Penalties += r.Penalties
		t.Adjustments += r.Adjustments
		t.Payment += r.Payment
		t.Paid += r.Paid
		t.Due += r.Due

		has := false
		for _, e := range []string{f.Email1, f.Email2} {
			e = strings.ToLower(strings.TrimSpace(e))
			if e != "" && strings.Contains(e, "@") && !seen[e] {
				seen[e] = true
				pr.Emails = append(pr.Emails, e)
				has = true
			} else if e != "" && seen[e] {
				has = true
			}
		}
		if !has {
			pr.MissingEmail = append(pr.MissingEmail, f.Code)
		}
	}
	sort.Strings(pr.Emails)

	// mail text
	s, err := a.Settings()
	if err != nil {
		return nil, err
	}
	due, _ := ParseDate(p.DueDate)
	next := q.Next()
	nextDue := next.DefaultDue()
	if np, err := a.Period(next.ID()); err == nil {
		nextDue, _ = ParseDate(np.DueDate)
	}
	fyShort := func(x Quarter) string { return fmt.Sprintf("FY%02d-%02d", x.FY%100, (x.FY+1)%100) }
	rep := strings.NewReplacer(
		"{{quarter_name}}", fmt.Sprintf("Q%d, %d-%02d(%s)", q.Q, q.FY, (q.FY+1)%100, quarterMonths[q.Q]),
		"{{quarter}}", periodID,
		"{{quarter_label}}", fmt.Sprintf("Q%d %d-%02d (%s)", q.Q, q.FY, (q.FY+1)%100, quarterMonths[q.Q]),
		"{{due_column}}", periodID+" Due",
		"{{next_quarter_label}}", fmt.Sprintf("Q%d %d-%02d", next.Q, next.FY, (next.FY+1)%100),
		"{{due_date}}", plainDate(due),
		"{{next_quarter}}", fmt.Sprintf("Q%d %s", next.Q, fyShort(next)),
		"{{next_due_date}}", plainDate(nextDue),
		"{{late_fee}}", Rupees(p.LateFee),
		"{{treasurer_name}}", s["treasurer_name"],
		"{{treasurer_phone}}", s["treasurer_phone"],
		"{{signatory}}", s["signatory"],
		"{{account_name}}", s["bank_account_name"],
		"{{bank_name}}", s["bank_name"],
		"{{account_no}}", s["bank_account_no"],
		"{{ifsc}}", s["bank_ifsc"],
		"{{association_name}}", s["association_name"],
	)
	pr.Subject = rep.Replace(s["payment_request_subject"])
	pr.Body = rep.Replace(s["payment_request_template"])

	rows, err := a.DB.Query(`SELECT id,subject,text,COALESCE(sent_at,created_at) FROM reminders WHERE period_id=? AND kind='day0' ORDER BY id DESC`, periodID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	pr.Sent = []SentRequest{}
	for rows.Next() {
		var x SentRequest
		if err := rows.Scan(&x.ID, &x.Subject, &x.Text, &x.SentAt); err != nil {
			return nil, err
		}
		pr.Sent = append(pr.Sent, x)
	}
	return pr, rows.Err()
}

// MarkPaymentRequestSent records the request as sent (the text actually used).
func (a *App) MarkPaymentRequestSent(periodID, subject, body string, flats []string) error {
	fj := "[" + strings.Join(func() []string {
		out := make([]string, len(flats))
		for i, f := range flats {
			out[i] = `"` + f + `"`
		}
		return out
	}(), ",") + "]"
	_, err := a.DB.Exec(`INSERT INTO reminders(period_id,seq,text,flats,status,sent_at,kind,subject) VALUES(?,0,?,?,'sent',`+a.DB.Now()+`,'day0',?)`,
		periodID, body, fj, subject)
	return err
}

// PaymentRequestPDF: one landscape page listing every flat's dues.
func (a *App) PaymentRequestPDF(w io.Writer, pr *PaymentRequest) error {
	pdf := fpdf.New("L", "mm", "A4", "")
	pdf.SetMargins(lsMargin, lsMargin, lsMargin)
	pdf.SetAutoPageBreak(false, 0)
	pdf.AddPage()
	p, _ := a.Period(pr.PeriodID)
	sub := fmt.Sprintf("%s to %s | due %s | as of %s", fmtDate(p.StartDate), fmtDate(p.EndDate), fmtDate(p.DueDate), fmtDate(pr.AsOf))
	y := a.pageHeader(pdf, "Maintenance payment request - "+pr.PeriodID, sub)

	showAdj := pr.Total.Adjustments != 0
	cur, prev := pr.PeriodID, pr.PrevPeriodID
	heads := []string{"Apartment", "Owner Name", "Quarterly Amount", prev + " Due", cur + " Penalties"}
	if showAdj {
		heads = append(heads, cur+" Adjustments")
	}
	heads = append(heads, cur+" Payment", cur+" Paid", cur+" Due")
	widths := []float64{22, 58}
	numW := (lsWidth - 80) / float64(len(heads)-2)
	for i := 2; i < len(heads); i++ {
		widths = append(widths, numW)
	}
	// rows sized to fit one page
	rowH := (lsBottom - y - 8 - 12) / float64(len(pr.Rows))
	rowH = min(5.2, max(3.4, rowH))
	fs := min(8.5, max(6.5, rowH*1.65))

	num := func(v int64) string { return Rupees(v) }
	pdf.SetLineWidth(0.2)
	setDraw(pdf, [3]int{120, 120, 120})
	pdf.SetFont("Helvetica", "B", fs)
	setFill(pdf, cBand)
	pdf.SetXY(lsMargin, y)
	for i, h := range heads {
		align := "R"
		if i < 2 {
			align = "L"
		}
		pdf.CellFormat(widths[i], rowH+1.5, h, "1", 0, align, true, 0, "")
	}
	y += rowH + 1.5
	cell := func(i int, s string, bold, fill bool) {
		align := "R"
		if i < 2 {
			align = "L"
		}
		style := ""
		if bold {
			style = "B"
		}
		pdf.SetFont("Helvetica", style, fs)
		pdf.CellFormat(widths[i], rowH, s, "1", 0, align, fill, 0, "")
	}
	line := func(r PaymentRequestRow, bold bool) {
		// flats whose earlier dues or penalties are still unpaid stand out
		// (payments clear the oldest dues first)
		arrears := r.PrevDue+r.Penalties-r.Paid > 0
		if arrears {
			pdf.SetFillColor(253, 230, 196)
		}
		pdf.SetXY(lsMargin, y)
		vals := []string{r.Flat, r.Owner, num(r.Quarterly), num(r.PrevDue), num(r.Penalties)}
		if showAdj {
			vals = append(vals, num(r.Adjustments))
		}
		vals = append(vals, num(r.Payment), num(r.Paid), num(r.Due))
		for i, v := range vals {
			cell(i, v, bold || (arrears && i == len(vals)-1), arrears)
		}
		y += rowH
	}
	pdf.SetTextColor(0, 0, 0)
	for _, r := range pr.Rows {
		line(r, false)
	}

	s, _ := a.Settings()
	pdf.SetXY(lsMargin, y+2.5)
	pdf.SetFont("Helvetica", "", 7.5)
	setText(pdf, cMute)
	pdf.CellFormat(lsWidth, 4, fmt.Sprintf("Pay to %s, A/c %s, IFSC %s (%s). Mention your flat number in the transaction remarks. Late fee Rs. %s per month after the due date.",
		s["bank_account_name"], s["bank_account_no"], s["bank_ifsc"], s["bank_name"], Rupees(p.LateFee)), "", 0, "L", false, 0, "")
	pdf.SetXY(lsMargin, y+7)
	pdf.SetFillColor(253, 230, 196)
	setDraw(pdf, [3]int{120, 120, 120})
	pdf.Rect(lsMargin, y+7.6, 4, 2.8, "FD")
	pdf.SetXY(lsMargin+5, y+7)
	pdf.CellFormat(lsWidth-5, 4, "Highlighted: dues or penalties from earlier quarters still unpaid; they are included in the Due amount.", "", 0, "L", false, 0, "")
	pageFooter(pdf, "Payment = previous due + penalties + quarterly amount; Due = Payment - Paid. Amounts in Rs.", 1, 1)
	return pdf.Output(w)
}
