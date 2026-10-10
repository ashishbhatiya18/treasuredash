package app

import (
	"fmt"
	"io"
	"math"

	"github.com/go-pdf/fpdf"
)

// DashboardPDF renders the dashboard's quarter position on two A4 landscape
// pages: (1) headline figures, running P&L and cash & balances side by side,
// (2) sinking-fund growth. Member-level sections (receivables, payables,
// receipts, advances) are left out on purpose. Every block is laid out to fit
// its page; row heights shrink slightly when a quarter has many categories.

const (
	lsMargin = 14.0
	lsPageW  = 297.0
	lsPageH  = 210.0
	lsWidth  = lsPageW - 2*lsMargin // 269
	lsBottom = lsPageH - 12         // last usable y before the footer
)

var (
	cBlue = [3]int{42, 120, 214}
	cRed  = [3]int{227, 73, 72}
	cGrid = [3]int{226, 223, 216}
	cInk  = [3]int{20, 40, 70}
	cMute = [3]int{110, 110, 110}
	cGood = [3]int{31, 122, 77}
	cBand = [3]int{234, 240, 248}
)

func setText(pdf *fpdf.Fpdf, c [3]int) { pdf.SetTextColor(c[0], c[1], c[2]) }
func setDraw(pdf *fpdf.Fpdf, c [3]int) { pdf.SetDrawColor(c[0], c[1], c[2]) }
func setFill(pdf *fpdf.Fpdf, c [3]int) { pdf.SetFillColor(c[0], c[1], c[2]) }

// table writes rows into a fixed column: label on the left, amounts right-aligned.
type table struct {
	pdf    *fpdf.Fpdf
	x, w   float64
	colW   float64 // width of each amount column
	rowH   float64
	y      float64
	fsBase float64
}

func (t *table) labelW(cols int) float64 { return t.w - float64(cols)*t.colW }

func (t *table) heading(label string, cols ...string) {
	p := t.pdf
	p.SetFont("Helvetica", "B", t.fsBase-1.6)
	setText(p, cMute)
	p.SetXY(t.x, t.y)
	p.CellFormat(t.labelW(len(cols)), t.rowH*0.85, label, "", 0, "L", false, 0, "")
	for _, c := range cols {
		p.CellFormat(t.colW, t.rowH*0.85, c, "", 0, "R", false, 0, "")
	}
	t.y += t.rowH * 0.85
	p.SetTextColor(0, 0, 0)
}

type rowStyle int

const (
	rowNormal rowStyle = iota
	rowSub             // indented, muted
	rowTotal           // bold
	rowBand            // bold on a tinted band
)

func (t *table) row(style rowStyle, label string, cols ...string) {
	p := t.pdf
	fs := t.fsBase
	fontStyle := ""
	p.SetTextColor(0, 0, 0)
	switch style {
	case rowSub:
		fs -= 1
		setText(p, cMute)
		label = "     " + label
	case rowTotal, rowBand:
		fontStyle = "B"
	}
	p.SetFont("Helvetica", fontStyle, fs)
	// keep long labels on one line
	lw := t.labelW(len(cols)) - 1
	for p.GetStringWidth(label) > lw && len(label) > 4 {
		label = label[:len(label)-4] + "..."
	}
	fill := style == rowBand
	if fill {
		setFill(p, cBand)
	}
	setDraw(p, cGrid)
	p.SetLineWidth(0.2)
	p.SetXY(t.x, t.y)
	p.CellFormat(t.labelW(len(cols)), t.rowH, label, "B", 0, "L", fill, 0, "")
	for _, c := range cols {
		p.CellFormat(t.colW, t.rowH, c, "B", 0, "R", fill, 0, "")
	}
	t.y += t.rowH
	p.SetTextColor(0, 0, 0)
}

func (t *table) gap(h float64) { t.y += h }

func blockTitle(pdf *fpdf.Fpdf, x, y, w float64, title string) float64 {
	pdf.SetFont("Helvetica", "B", 10.5)
	setText(pdf, cInk)
	pdf.SetXY(x, y)
	pdf.CellFormat(w, 6, title, "", 0, "L", false, 0, "")
	setDraw(pdf, cInk)
	pdf.SetLineWidth(0.4)
	pdf.Line(x, y+6.5, x+w, y+6.5)
	pdf.SetTextColor(0, 0, 0)
	return y + 8.5
}

// pageHeader: association on the left, document title on the right, one rule.
func (a *App) pageHeader(pdf *fpdf.Fpdf, title, sub string) float64 {
	s, _ := a.Settings()
	y := lsMargin
	pdf.SetXY(lsMargin, y)
	pdf.SetFont("Helvetica", "B", 13)
	setText(pdf, cInk)
	pdf.CellFormat(lsWidth/2, 6, s["association_name"], "", 0, "L", false, 0, "")
	pdf.SetFont("Helvetica", "B", 12)
	pdf.CellFormat(lsWidth/2, 6, title, "", 0, "R", false, 0, "")
	pdf.SetXY(lsMargin, y+6.5)
	pdf.SetFont("Helvetica", "", 7.8)
	setText(pdf, cMute)
	pdf.CellFormat(lsWidth/2, 4, s["association_address"], "", 0, "L", false, 0, "")
	pdf.CellFormat(lsWidth/2, 4, sub, "", 0, "R", false, 0, "")
	setDraw(pdf, cInk)
	pdf.SetLineWidth(0.6)
	pdf.Line(lsMargin, y+12.5, lsMargin+lsWidth, y+12.5)
	pdf.SetTextColor(0, 0, 0)
	return y + 16
}

func pageFooter(pdf *fpdf.Fpdf, note string, page, pages int) {
	pdf.SetXY(lsMargin, lsPageH-9)
	pdf.SetFont("Helvetica", "I", 7)
	setText(pdf, cMute)
	pdf.CellFormat(lsWidth-20, 4, note, "", 0, "L", false, 0, "")
	pdf.CellFormat(20, 4, fmt.Sprintf("Page %d of %d", page, pages), "", 0, "R", false, 0, "")
	pdf.SetTextColor(0, 0, 0)
}

// tile draws one headline figure box.
func tile(pdf *fpdf.Fpdf, x, y, w, h float64, label, value, hint string, tone [3]int) {
	setDraw(pdf, cGrid)
	pdf.SetLineWidth(0.3)
	pdf.Rect(x, y, w, h, "D")
	pdf.SetXY(x+3, y+2.5)
	pdf.SetFont("Helvetica", "", 7.2)
	setText(pdf, cMute)
	pdf.CellFormat(w-6, 3.8, label, "", 2, "L", false, 0, "")
	pdf.SetFont("Helvetica", "B", 14)
	setText(pdf, tone)
	pdf.CellFormat(w-6, 8, value, "", 2, "L", false, 0, "")
	pdf.SetFont("Helvetica", "", 7)
	setText(pdf, cMute)
	pdf.MultiCell(w-6, 3.2, hint, "", "L", false)
	pdf.SetTextColor(0, 0, 0)
}

func niceTicksGo(min, max float64, n int) []float64 {
	span := max - min
	if span <= 0 {
		span = 1
	}
	step0 := span / float64(n)
	mag := math.Pow(10, math.Floor(math.Log10(step0)))
	step := step0
	for _, m := range []float64{1, 2, 2.5, 5, 10} {
		if m*mag >= step0 {
			step = m * mag
			break
		}
	}
	lo, hi := math.Floor(min/step)*step, math.Ceil(max/step)*step
	var out []float64
	for v := lo; v <= hi+step/2; v += step {
		out = append(out, v)
	}
	return out
}

func lakh(p float64) string {
	r := p / 100
	a := math.Abs(r)
	sign := ""
	if r < 0 {
		sign = "-"
	}
	switch {
	case a >= 1e5:
		return fmt.Sprintf("%s%.1fL", sign, a/1e5)
	case a >= 1e3:
		return fmt.Sprintf("%s%.0fk", sign, a/1e3)
	}
	return fmt.Sprintf("%s%.0f", sign, a)
}

// fundCharts draws the balance line and the surplus/deficit bars on the same
// quarter slots (two charts, one axis each). Returns the y below the charts.
func fundCharts(pdf *fpdf.Fpdf, qs []FundQuarter, x, y, w, lineH, barH float64) float64 {
	if len(qs) == 0 {
		return y
	}
	padL := 15.0
	iw := w - padL
	slot := iw / float64(len(qs))
	cx := func(i int) float64 { return x + padL + slot*float64(i) + slot/2 }
	axis := func(ticks []float64, yOf func(float64) float64, zero bool) {
		pdf.SetFont("Helvetica", "", 7)
		for _, t := range ticks {
			c := cGrid
			if zero && t == 0 {
				c = [3]int{150, 150, 150}
			}
			setDraw(pdf, c)
			pdf.SetLineWidth(0.2)
			pdf.Line(x+padL, yOf(t), x+w, yOf(t))
			setText(pdf, cMute)
			pdf.SetXY(x, yOf(t)-1.5)
			pdf.CellFormat(padL-2, 3, lakh(t), "", 0, "R", false, 0, "")
		}
	}
	labels := func(yy float64) {
		pdf.SetFont("Helvetica", "", 7)
		setText(pdf, cMute)
		for i, q := range qs {
			pdf.SetXY(cx(i)-slot/2, yy)
			pdf.CellFormat(slot, 3.5, q.PeriodID, "", 0, "C", false, 0, "")
		}
	}
	caption := func(yy float64, s string) {
		pdf.SetFont("Helvetica", "", 8)
		setText(pdf, cMute)
		pdf.SetXY(x, yy)
		pdf.CellFormat(w, 4, s, "", 0, "L", false, 0, "")
	}

	// fund balance
	caption(y, "Fund balance at quarter end")
	top, h := y+8, lineH
	mn, mx := math.Inf(1), math.Inf(-1)
	for _, q := range qs {
		mn, mx = math.Min(mn, float64(q.Balance)), math.Max(mx, float64(q.Balance))
	}
	ticks := niceTicksGo(mn*0.97, mx*1.04, 4)
	y0, y1 := ticks[0], ticks[len(ticks)-1]
	yOf := func(v float64) float64 { return top + h - (v-y0)/(y1-y0)*h }
	axis(ticks, yOf, false)
	setDraw(pdf, cBlue)
	pdf.SetLineWidth(0.7)
	for i := 1; i < len(qs); i++ {
		pdf.Line(cx(i-1), yOf(float64(qs[i-1].Balance)), cx(i), yOf(float64(qs[i].Balance)))
	}
	for i, q := range qs {
		if q.Running {
			pdf.SetFillColor(255, 255, 255)
		} else {
			setFill(pdf, cBlue)
		}
		setDraw(pdf, cBlue)
		pdf.SetLineWidth(0.5)
		pdf.Circle(cx(i), yOf(float64(q.Balance)), 1.1, "FD")
		// value above every point: few points, and print has no hover
		pdf.SetFont("Helvetica", "", 6.8)
		setText(pdf, cMute)
		if i == len(qs)-1 {
			pdf.SetFont("Helvetica", "B", 7.5)
			setText(pdf, cInk)
		}
		pdf.SetXY(cx(i)-slot/2, yOf(float64(q.Balance))-5.5)
		pdf.CellFormat(slot, 3.5, lakh(float64(q.Balance)), "", 0, "C", false, 0, "")
	}
	labels(top + h + 2)

	// surplus / deficit bars
	y = top + h + 9
	caption(y, "Added each quarter (actual income - expenses). Blue = surplus, red = deficit, hatched = running quarter")
	top, h = y+9, barH
	mn, mx = 0, 0
	for _, q := range qs {
		mn, mx = math.Min(mn, float64(q.Surplus)), math.Max(mx, float64(q.Surplus))
	}
	ticks = niceTicksGo(mn, mx, 3)
	y0, y1 = ticks[0], ticks[len(ticks)-1]
	yOf = func(v float64) float64 { return top + h - (v-y0)/(y1-y0)*h }
	axis(ticks, yOf, true)
	bw := math.Min(14, slot*0.5)
	for i, q := range qs {
		v := float64(q.Surplus)
		t, b := yOf(math.Max(0, v)), yOf(math.Min(0, v))
		c := cBlue
		if v < 0 {
			c = cRed
		}
		setFill(pdf, c)
		pdf.Rect(cx(i)-bw/2, t, bw, math.Max(0.3, b-t), "F")
		if q.Running { // 45° hatch, clipped to the bar
			pdf.SetDrawColor(255, 255, 255)
			pdf.SetLineWidth(0.4)
			for k := -bw; k < b-t; k += 1.8 {
				x1, ya := cx(i)-bw/2, t+k
				x2, yb := cx(i)+bw/2, t+k+bw
				if ya < t {
					x1 += t - ya
					ya = t
				}
				if yb > b {
					x2 -= yb - b
					yb = b
				}
				if x2 > x1 {
					pdf.Line(x1, ya, x2, yb)
				}
			}
		}
		// value at the data end of each bar
		pdf.SetFont("Helvetica", "", 6.8)
		setText(pdf, cMute)
		ly := t - 4
		if v < 0 {
			ly = b + 0.5
		}
		pdf.SetXY(cx(i)-slot/2, ly)
		pdf.CellFormat(slot, 3.5, lakh(v), "", 0, "C", false, 0, "")
	}
	labels(top + h + 5)
	return top + h + 10
}

func (a *App) DashboardPDF(w io.Writer, d *Dashboard) error {
	pdf := fpdf.New("L", "mm", "A4", "")
	pdf.SetMargins(lsMargin, lsMargin, lsMargin)
	pdf.SetAutoPageBreak(false, 0) // every block is placed explicitly
	p, c, sf := d.PnL, d.Cash, d.Sinking

	title := "Quarterly position - " + d.CurrentPeriod
	if d.Running {
		title += " (running)"
	}
	sub := fmt.Sprintf("%s to %s", fmtDate(p.From), fmtDate(p.To))
	if d.Period != nil {
		sub += " | due " + fmtDate(d.Period.DueDate)
	}
	sub += " | as of " + fmtDate(d.AsOf)
	if c.HasData && c.ClosingOn != d.AsOf {
		sub += " | bank to " + fmtDate(c.ClosingOn)
	}
	footer := "Income on actuals (money received, applied to the oldest dues first); expenses on the bill date."

	// ---------------- page 1 ----------------
	pdf.AddPage()
	y := a.pageHeader(pdf, title, sub)

	maintPct := 0
	if len(p.Income) > 0 && p.Income[0].Receivable > 0 {
		maintPct = int(math.Round(float64(p.Income[0].Actual) * 100 / float64(p.Income[0].Receivable)))
	}
	fyHint := fmt.Sprintf("%s: %.1f%% of income", sf.Current.FY, sf.Current.Percent)
	if sf.Current.CompletedQuarters > 0 && !sf.Current.Complete && sf.Current.CompletedSurplus != sf.Current.Surplus {
		fyHint = fmt.Sprintf("%s: %.1f%% (closed qtrs), %.1f%% to date", sf.Current.FY, sf.Current.CompletedPercent, sf.Current.Percent)
	}
	payHint := "bank debits in the quarter"
	if c.ToDeposits > 0 {
		payHint = "+ " + rs(c.ToDeposits) + " moved to FD/RD"
	}
	round := func(v int64) string { return rs((v + 50) / 100 * 100) }
	tiles := []struct {
		l, v, h string
		tone    [3]int
	}{
		{"Receivable (billed)", round(p.TotalReceivable), d.CurrentPeriod + " charges + other income", cInk},
		{"Actual received", round(p.TotalIncome), fmt.Sprintf("%d%% of maintenance; %d/%d flats paid", maintPct, d.Collections.FlatsPaid, d.Collections.FlatsBilled), cGood},
		{"Payments made", round(c.PaymentsOperating), payHint, cInk},
		{"Total funds", round(c.TotalClosing), "savings " + round(c.Closing) + " + FD/RD " + round(c.DepositsClosing), cInk},
		{"Sinking fund", round(sf.Projected), fyHint, cGood},
	}
	gap, th := 4.0, 24.0
	tw := (lsWidth - gap*float64(len(tiles)-1)) / float64(len(tiles))
	for i, t := range tiles {
		tile(pdf, lsMargin+float64(i)*(tw+gap), y, tw, th, t.l, t.v, t.h, t.tone)
	}
	y += th + 6

	// two columns sized to fit what's left of the page
	leftW, colGap := 152.0, 9.0
	rightX, rightW := lsMargin+leftW+colGap, lsWidth-leftW-colGap
	leftRows := 1 + len(p.Income) + 1 + 1 + len(p.Expenses) + 1 + 2 + 1 // headings, rows, totals, results, gaps
	rightRows := 12 + 6 + len(c.Deposits)
	avail := lsBottom - (y + 8.5) - 4
	rowH := math.Max(4.2, math.Min(6.2, avail/float64(max(leftRows, rightRows))))
	fs := math.Max(7.2, math.Min(9, rowH*1.5))

	// left: running P&L
	ly := blockTitle(pdf, lsMargin, y, leftW, "Running P&L - "+d.CurrentPeriod)
	lt := &table{pdf: pdf, x: lsMargin, w: leftW, colW: 30, rowH: rowH, y: ly, fsBase: fs}
	lt.heading("INCOME", "RECEIVABLE", "ACTUAL")
	for _, l := range p.Income {
		recv := rs(l.Receivable)
		if l.NoReceivable {
			recv = "-"
		}
		lt.row(rowNormal, l.Label, recv, rs(l.Actual))
	}
	lt.row(rowTotal, "Total income", rs(p.TotalReceivable), rs(p.TotalIncome))
	lt.gap(rowH * 0.5)
	lt.heading("EXPENSES (BILLS)", "", "AMOUNT")
	for _, l := range p.Expenses {
		lt.row(rowNormal, fmt.Sprintf("%s (%d)", l.Label, l.Count), "", rs(l.Amount))
	}
	lt.row(rowTotal, "Total expenses", "", rs(p.TotalExpenses))
	lt.gap(rowH * 0.6)
	lbl := "Surplus to date (actual)"
	if !d.Running {
		lbl = "Surplus for the quarter (actual)"
	}
	if p.Surplus < 0 {
		lbl = "Deficit to date (actual)"
		if !d.Running {
			lbl = "Deficit for the quarter (actual)"
		}
	}
	lt.row(rowBand, lbl, "", rs(p.Surplus))
	lt.row(rowNormal, "If all dues are collected", "", rs(p.SurplusIfPaid))

	// right: cash & balances
	ry := blockTitle(pdf, rightX, y, rightW, "Cash & balances")
	rt := &table{pdf: pdf, x: rightX, w: rightW, colW: 32, rowH: rowH, y: ry, fsBase: fs}
	if !c.HasData {
		rt.row(rowSub, "No bank statement covers this quarter.")
	} else {
		one := func(st rowStyle, label string, v int64) { rt.row(st, label, rs(v)) }
		one(rowTotal, "Opening total funds - "+fmtDate(c.OpeningOn), c.TotalOpening)
		one(rowSub, "Savings account", c.Opening)
		one(rowSub, "FD / RD", c.DepositsOpening)
		one(rowNormal, "+ Maintenance received", c.ReceiptsMaintenance)
		one(rowNormal, "+ Other income (interest etc.)", c.ReceiptsOther)
		if c.ReceiptsUnassigned > 0 {
			one(rowNormal, "+ Received, not yet assigned", c.ReceiptsUnassigned)
		}
		one(rowNormal, "- Payments to vendors / staff", -c.PaymentsOperating)
		if c.BookedToDeposits != 0 {
			one(rowNormal, "- Moved to FD/RD (booked, not yet on statement)", -c.BookedToDeposits)
		}
		if c.ToDeposits > 0 || c.FromDeposits > 0 {
			one(rowSub, fmt.Sprintf("Savings <-> FD/RD, net %s (total unchanged)", rs(c.ToDeposits-c.FromDeposits)), 0)
		}
		one(rowTotal, "Closing total funds - "+fmtDate(c.ClosingOn), c.TotalClosing)
		one(rowSub, "Savings account", c.Closing)
		one(rowSub, "FD / RD", c.DepositsClosing)
		rt.gap(rowH * 0.5)
		rt.heading("HELD FOR", "")
		payable := d.Payables.Total - d.Payables.PrepaidTotal
		one(rowNormal, "Sinking fund (books)", sf.Projected)
		one(rowNormal, "Advances from members", d.Advances.Total)
		one(rowNormal, "Bills payable (recorded, not yet paid)", payable)
		one(rowSub, "Unreconciled difference", c.TotalClosing-sf.Projected-d.Advances.Total-payable)
		if len(c.Deposits) > 0 {
			rt.gap(rowH * 0.5)
			rt.heading("DEPOSITS HELD", "")
			for _, dep := range c.Deposits {
				l := dep.Label
				if dep.MaturityDate != "" {
					l += ", matures " + fmtDate(dep.MaturityDate)
				}
				one(rowSub, l, dep.Value)
			}
		}
	}
	pageFooter(pdf, footer, 1, 2)

	// ---------------- page 2: sinking fund ----------------
	pdf.AddPage()
	y = a.pageHeader(pdf, "Sinking fund growth - "+d.CurrentPeriod, sub)
	chartW := 172.0
	cy := blockTitle(pdf, lsMargin, y+2, chartW, "Fund balance and quarterly additions")
	fundCharts(pdf, sf.Quarters, lsMargin, cy, chartW, 62, 42)

	x := lsMargin + chartW + 10
	tw2 := lsWidth - chartW - 10
	ty := blockTitle(pdf, x, y+2, tw2, "Share of income added to the fund")
	ft := &table{pdf: pdf, x: x, w: tw2, colW: 28, rowH: 5.6, y: ty, fsBase: 8.6}
	ft.heading("FY", "CLOSED QTRS", "TO DATE")
	for _, fy := range sf.Years {
		closed, todate := "-", "same"
		if fy.CompletedQuarters > 0 {
			closed = fmt.Sprintf("%.1f%%", fy.CompletedPercent)
		}
		if !fy.Complete && fy.CompletedSurplus != fy.Surplus {
			todate = fmt.Sprintf("%.1f%%", fy.Percent)
		}
		ft.row(rowTotal, fy.FY, closed, todate)
		note := "full year"
		if !fy.Complete {
			note = fmt.Sprintf("%d of 4 closed", fy.CompletedQuarters)
		}
		ft.row(rowSub, note, rs(fy.CompletedSurplus), rs(fy.Surplus))
	}
	ft.gap(4)
	bt := &table{pdf: pdf, x: x, w: tw2, colW: 36, rowH: 6, y: ft.y, fsBase: 9}
	openLabel := "Opening balance"
	if len(sf.Quarters) > 0 {
		openLabel = "Opening (before " + sf.Quarters[0].PeriodID + ")"
	}
	bt.row(rowNormal, openLabel, rs(sf.Opening))
	bt.row(rowNormal, "After closed quarters", rs(sf.Balance))
	if sf.Projected != sf.Balance {
		bt.row(rowBand, "Including running quarter", rs(sf.Projected))
	}
	bt.gap(5)
	qt := &table{pdf: pdf, x: x, w: tw2, colW: 30, rowH: 5, y: bt.y, fsBase: 8}
	qt.heading("QUARTER", "ADDED", "BALANCE")
	for _, q := range sf.Quarters {
		lbl := q.PeriodID
		if q.Running {
			lbl += " (running)"
		}
		qt.row(rowNormal, lbl, rs(q.Surplus), rs(q.Balance))
	}
	pdf.SetXY(x, qt.y+3)
	pdf.SetFont("Helvetica", "I", 7)
	setText(pdf, cMute)
	pdf.MultiCell(tw2, 3.4, "Share = surplus / actual income. 'To date' includes the running quarter, which starts with bills in but maintenance not yet due, so it dips early in each quarter.", "", "L", false)
	pageFooter(pdf, footer, 2, 2)
	return pdf.Output(w)
}
