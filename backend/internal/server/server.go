package server

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"treasuredash/internal/app"
)

// dist holds the static Next.js export; the Docker build copies web/out here.
//
//go:embed all:dist
var dist embed.FS

// Server has no login of its own: it is meant to run behind an
// authenticating reverse proxy (webauthn-proxy via Traefik forward-auth).
type Server struct {
	A *app.App
}

func New(a *app.App) *Server { return &Server{A: a} }

func (s *Server) Routes() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RealIP, middleware.Recoverer, middleware.Logger)

	r.Route("/api", func(r chi.Router) {
		r.Group(func(r chi.Router) {
			r.Get("/me", func(w http.ResponseWriter, r *http.Request) {
				ok(w, map[string]any{"ok": true, "scan_enabled": app.ScanEnabled()})
			})
			r.Get("/dashboard", s.dashboard)
			r.Get("/dashboard.pdf", s.dashboardPDF)

			r.Get("/settings", s.getSettings)
			r.Put("/settings", s.putSettings)
			r.Post("/settings/reset", s.resetSetting)
			r.Post("/import/workbook", s.importWorkbook)

			r.Get("/flats", s.listFlats)
			r.Put("/flats/{code}", s.putFlat)
			r.Get("/flats/{code}/ledger", s.flatLedger)
			r.Get("/flats/{code}/account", s.flatAccount)
			r.Get("/receipts", s.receipts)
			r.Get("/receipts.csv", s.receiptsCSV)
			r.Post("/flats/{code}/payments", s.addPayment)
			r.Post("/flats/{code}/adjustments", s.addAdjustment)
			r.Put("/ledger/{id}", s.updateLedger)
			r.Delete("/ledger/{id}", s.deleteLedger)
			r.Post("/ledger/{id}/void", s.voidPayment)
			r.Post("/voids/{id}/restore", s.restoreVoid)

			r.Get("/periods", s.listPeriods)
			r.Post("/periods", s.createPeriod)
			r.Put("/periods/{id}", s.updatePeriod)
			r.Get("/periods/{id}/status", s.periodStatus)
			r.Post("/periods/{id}/invoices", s.raiseInvoices)
			r.Get("/periods/{id}/invoices.zip", s.invoicesZip)
			r.Get("/periods/{id}/invoices/{flat}.pdf", s.invoicePDF)
			r.Get("/periods/{id}/payment-request", s.paymentRequest)
			r.Get("/periods/{id}/payment-request.pdf", s.paymentRequestPDF)
			r.Post("/periods/{id}/payment-request/sent", s.paymentRequestSent)
			r.Post("/late-fees/apply", s.applyLateFees)

			r.Get("/bank/statements", s.listStatements)
			r.Post("/bank/statements", s.uploadStatement)
			r.Get("/bank/txns", s.listTxns)
			r.Post("/bank/txns/{id}/assign", s.assignTxn)
			r.Post("/bank/rematch", s.rematch)

			r.Get("/payables", s.payables)
			r.Get("/payables/instruction.pdf", s.payablesPDF)
			r.Post("/payables/instruction", s.payablesInstruction)
			r.Post("/payables/sent", s.payablesSent)
			r.Put("/payees/{name}", s.savePayee)
			r.Get("/deposits", s.listDeposits)
			r.Post("/deposits", s.saveDeposit)
			r.Put("/deposits/{id}", s.saveDeposit)
			r.Delete("/deposits/{id}", s.deleteDeposit)
			r.Get("/categories", s.listCategories)
			r.Put("/categories", s.putCategory)
			r.Get("/expenses", s.listExpenses)
			r.Post("/expenses", s.saveExpense)
			r.Put("/expenses/{id}", s.saveExpense)
			r.Delete("/expenses/{id}", s.deleteExpense)
			r.Post("/expenses/scan", s.scanBill)
			r.Get("/expenses/templates", s.billTemplates)
			r.Post("/expenses/{id}/bill", s.uploadBill)
			r.Get("/bills/{name}", s.getBill)
			r.Get("/income", s.listIncome)
			r.Post("/income", s.addIncome)
			r.Delete("/income/{id}", s.deleteIncome)

			r.Get("/reminders", s.listReminders)
			r.Post("/reminders/preview", s.previewReminder)
			r.Post("/reminders", s.createReminder)
			r.Post("/reminders/{id}/sent", s.markSent)
			r.Delete("/reminders/{id}", s.deleteReminder)

			r.Get("/reports/pnl", s.pnl)
			r.Get("/reports/pnl.pdf", s.pnlPDF)
			r.Get("/reports/pnl.csv", s.pnlCSV)
			r.Get("/reports/sinking-fund", s.sinking)
			r.Post("/reports/sinking-fund/calibrate", s.calibrateSinking)
			r.Get("/export/expenses.csv", s.expensesCSV)
			r.Get("/export/backup.db", s.backup)

			r.Get("/data/sheets/{name}", s.dataSheet)
			r.Get("/data/workbook.xlsx", s.dataWorkbook)
			r.Post("/data/preview", s.dataPreview)
			r.Post("/data/apply", s.dataApply)
		})
	})
	r.NotFound(s.static())
	return r
}

// ---- helpers ------------------------------------------------------------------

func ok(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, code int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

func bad(w http.ResponseWriter, err error) { fail(w, http.StatusBadRequest, err) }

func decode(r *http.Request, v any) error {
	defer r.Body.Close()
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(v); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	return nil
}

func idParam(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err == nil && id <= 0 {
		err = errors.New("invalid id")
	}
	return id, err
}

func upload(r *http.Request, field string, max int64) ([]byte, string, error) {
	if err := r.ParseMultipartForm(max); err != nil {
		return nil, "", err
	}
	f, h, err := r.FormFile(field)
	if err != nil {
		return nil, "", fmt.Errorf("missing file field %q", field)
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, max))
	return b, h.Filename, err
}

// ---- static frontend ------------------------------------------------------------

func (s *Server) static() http.HandlerFunc {
	sub, _ := fs.Sub(dist, "dist")
	return func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			fail(w, http.StatusNotFound, errors.New("not found"))
			return
		}
		p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if p == "" {
			p = "index"
		}
		// Next's static export writes /flats as flats.html and may also create a
		// flats/ directory for route data, so try the exact file, then .html.
		for _, c := range []string{p, p + ".html", path.Join(p, "index.html")} {
			if st, err := fs.Stat(sub, c); err == nil && !st.IsDir() {
				if strings.HasPrefix(c, "_next/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				http.ServeFileFS(w, r, sub, c)
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)
		if b, err := fs.ReadFile(sub, "404.html"); err == nil {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write(b)
		}
	}
}

// ---- handlers -------------------------------------------------------------------

func (s *Server) dashboard(w http.ResponseWriter, r *http.Request) {
	d, err := s.A.DashboardData(r.URL.Query().Get("period"))
	if err != nil {
		bad(w, err)
		return
	}
	ok(w, d)
}

func (s *Server) dashboardPDF(w http.ResponseWriter, r *http.Request) {
	d, err := s.A.DashboardData(r.URL.Query().Get("period"))
	if err != nil {
		bad(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="Position_%s.pdf"`, d.CurrentPeriod))
	if err := s.A.DashboardPDF(w, d); err != nil {
		log.Printf("dashboard pdf: %v", err)
	}
}

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	m, err := s.A.Settings()
	if err != nil {
		fail(w, 500, err)
		return
	}
	ok(w, map[string]any{"settings": m, "payment_modes": app.PaymentModes, "income_categories": app.IncomeCategories, "scan_enabled": app.ScanEnabled()})
}

func (s *Server) putSettings(w http.ResponseWriter, r *http.Request) {
	var m map[string]string
	if err := decode(r, &m); err != nil {
		bad(w, err)
		return
	}
	if err := s.A.SaveSettings(m); err != nil {
		bad(w, err)
		return
	}
	s.getSettings(w, r)
}

func (s *Server) resetSetting(w http.ResponseWriter, r *http.Request) {
	var b struct{ Key string }
	if err := decode(r, &b); err != nil {
		bad(w, err)
		return
	}
	if err := s.A.ResetSetting(b.Key); err != nil {
		bad(w, err)
		return
	}
	s.getSettings(w, r)
}

func (s *Server) importWorkbook(w http.ResponseWriter, r *http.Request) {
	b, _, err := upload(r, "file", 20<<20)
	if err != nil {
		bad(w, err)
		return
	}
	rep, err := s.A.ImportWorkbook(strings.NewReader(string(b)))
	if err != nil {
		bad(w, err)
		return
	}
	ok(w, rep)
}

func (s *Server) listFlats(w http.ResponseWriter, r *http.Request) {
	fs, err := s.A.Flats()
	if err != nil {
		fail(w, 500, err)
		return
	}
	if fs == nil {
		fs = []app.Flat{}
	}
	ok(w, fs)
}

func (s *Server) putFlat(w http.ResponseWriter, r *http.Request) {
	var f app.Flat
	if err := decode(r, &f); err != nil {
		bad(w, err)
		return
	}
	f.Code = chi.URLParam(r, "code")
	if err := s.A.UpsertFlat(f); err != nil {
		bad(w, err)
		return
	}
	ok(w, f)
}

func (s *Server) flatLedger(w http.ResponseWriter, r *http.Request) {
	code := chi.URLParam(r, "code")
	f, err := s.A.Flat(code)
	if err != nil {
		fail(w, 404, err)
		return
	}
	l, err := s.A.Ledger(code)
	if err != nil {
		fail(w, 500, err)
		return
	}
	ok(w, map[string]any{"flat": f, "ledger": l})
}

type moneyBody struct {
	Date        string  `json:"date"`
	Amount      float64 `json:"amount"` // rupees
	Mode        string  `json:"mode"`
	Description string  `json:"description"`
}

func (s *Server) flatAccount(w http.ResponseWriter, r *http.Request) {
	acc, err := s.A.FlatAccount(chi.URLParam(r, "code"))
	if err != nil {
		fail(w, 404, err)
		return
	}
	ok(w, acc)
}

func receiptFilter(r *http.Request) app.ReceiptFilter {
	q := r.URL.Query()
	return app.ReceiptFilter{From: q.Get("from"), To: q.Get("to"), Flat: strings.ToUpper(q.Get("flat")), Mode: q.Get("mode"), Period: q.Get("period")}
}

func (s *Server) receipts(w http.ResponseWriter, r *http.Request) {
	rs, err := s.A.Receipts(receiptFilter(r))
	if err != nil {
		fail(w, 500, err)
		return
	}
	ok(w, rs)
}

func (s *Server) receiptsCSV(w http.ResponseWriter, r *http.Request) {
	rs, err := s.A.Receipts(receiptFilter(r))
	if err != nil {
		fail(w, 500, err)
		return
	}
	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", `attachment; filename="receipts.csv"`)
	fmt.Fprintln(w, "Date,Flat,Owner,Amount,Mode,Bank ref,Applied to,Advance (unapplied),Description")
	for _, x := range rs {
		var parts []string
		for _, ap := range x.Applied {
			parts = append(parts, fmt.Sprintf("%s: %s", ap.Label, rupee(ap.Amount)))
		}
		fmt.Fprintf(w, "%s,%s,%s,%s,%s,%s,%s,%s,%s\n", x.Date, x.Flat, csvEscape(x.Owner), rupee(-x.Amount), x.Mode, csvEscape(x.BankRef),
			csvEscape(strings.Join(parts, "; ")), rupee(x.Unapplied), csvEscape(x.Description))
	}
}

func (s *Server) addPayment(w http.ResponseWriter, r *http.Request) {
	var b moneyBody
	if err := decode(r, &b); err != nil {
		bad(w, err)
		return
	}
	if err := s.A.AddPayment(chi.URLParam(r, "code"), b.Date, app.Paise(b.Amount), b.Mode, b.Description); err != nil {
		bad(w, err)
		return
	}
	s.flatLedger(w, r)
}

func (s *Server) addAdjustment(w http.ResponseWriter, r *http.Request) {
	var b moneyBody
	if err := decode(r, &b); err != nil {
		bad(w, err)
		return
	}
	if err := s.A.AddAdjustment(chi.URLParam(r, "code"), b.Date, app.Paise(b.Amount), b.Description); err != nil {
		bad(w, err)
		return
	}
	s.flatLedger(w, r)
}

func (s *Server) deleteLedger(w http.ResponseWriter, r *http.Request) {
	id, _ := idParam(r)
	if err := s.A.DeleteLedgerEntry(id); err != nil {
		bad(w, err)
		return
	}
	ok(w, map[string]bool{"ok": true})
}

func (s *Server) updateLedger(w http.ResponseWriter, r *http.Request) {
	id, err := idParam(r)
	if err != nil {
		bad(w, err)
		return
	}
	var b moneyBody
	if err := decode(r, &b); err != nil {
		bad(w, err)
		return
	}
	if err := s.A.UpdateLedgerEntry(id, b.Date, app.Paise(b.Amount), b.Mode, b.Description); err != nil {
		bad(w, err)
		return
	}
	ok(w, map[string]bool{"ok": true})
}

func (s *Server) voidPayment(w http.ResponseWriter, r *http.Request) {
	id, err := idParam(r)
	if err != nil {
		bad(w, err)
		return
	}
	var b struct{ Reason string }
	if err := decode(r, &b); err != nil {
		bad(w, err)
		return
	}
	if err := s.A.VoidPayment(id, b.Reason); err != nil {
		bad(w, err)
		return
	}
	ok(w, map[string]bool{"ok": true})
}

func (s *Server) restoreVoid(w http.ResponseWriter, r *http.Request) {
	id, err := idParam(r)
	if err != nil {
		bad(w, err)
		return
	}
	if err := s.A.RestoreVoid(id); err != nil {
		bad(w, err)
		return
	}
	ok(w, map[string]bool{"ok": true})
}

func (s *Server) listPeriods(w http.ResponseWriter, r *http.Request) {
	ps, err := s.A.Periods()
	if err != nil {
		fail(w, 500, err)
		return
	}
	if ps == nil {
		ps = []app.Period{}
	}
	// suggest the next quarter to open
	next := app.CurrentPeriodID()
	have := map[string]bool{}
	for _, p := range ps {
		have[p.ID] = true
	}
	if have[next] {
		q, _ := app.ParseQuarter(next)
		next = q.Next().ID()
	}
	ok(w, map[string]any{"periods": ps, "current": app.CurrentPeriodID(), "suggested_next": next})
}

func (s *Server) createPeriod(w http.ResponseWriter, r *http.Request) {
	var b struct{ ID string }
	if err := decode(r, &b); err != nil {
		bad(w, err)
		return
	}
	p, err := s.A.CreatePeriod(b.ID)
	if err != nil {
		bad(w, err)
		return
	}
	ok(w, p)
}

func (s *Server) updatePeriod(w http.ResponseWriter, r *http.Request) {
	var b struct {
		DueDate     string  `json:"due_date"`
		RatePerSqft float64 `json:"rate_per_sqft"`
		LateFee     float64 `json:"late_fee"`
		Status      string  `json:"status"`
	}
	if err := decode(r, &b); err != nil {
		bad(w, err)
		return
	}
	id := chi.URLParam(r, "id")
	if err := s.A.UpdatePeriod(id, b.DueDate, b.RatePerSqft, app.Paise(b.LateFee), b.Status); err != nil {
		bad(w, err)
		return
	}
	p, _ := s.A.Period(id)
	ok(w, p)
}

func (s *Server) periodStatus(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	p, err := s.A.Period(id)
	if err != nil {
		fail(w, 404, err)
		return
	}
	st, err := s.A.PeriodStatus(id, app.Today())
	if err != nil {
		fail(w, 500, err)
		return
	}
	ok(w, map[string]any{"period": p, "flats": st})
}

func (s *Server) raiseInvoices(w http.ResponseWriter, r *http.Request) {
	n, err := s.A.RaiseInvoices(chi.URLParam(r, "id"))
	if err != nil {
		bad(w, err)
		return
	}
	if _, err := s.A.ApplyLateFees(app.Today()); err != nil {
		fail(w, 500, err)
		return
	}
	ok(w, map[string]int{"created": n})
}

func (s *Server) invoicesZip(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s_invoices.zip"`, id))
	if err := s.A.InvoicesZip(w, id); err != nil {
		log.Printf("zip: %v", err)
	}
}

func (s *Server) invoicePDF(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	flat := strings.TrimSuffix(chi.URLParam(r, "flat"), ".pdf")
	d, err := s.A.Invoice(flat, id)
	if err != nil {
		fail(w, 404, err)
		return
	}
	w.Header().Set("Content-Type", "application/pdf")
	disp := "inline"
	if r.URL.Query().Get("download") == "1" {
		disp = "attachment"
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf(`%s; filename="%s_%s_invoice.pdf"`, disp, id, flat))
	if err := s.A.InvoicePDF(w, d); err != nil {
		log.Printf("pdf: %v", err)
	}
}

func (s *Server) paymentRequest(w http.ResponseWriter, r *http.Request) {
	pr, err := s.A.PaymentRequestFor(chi.URLParam(r, "id"))
	if err != nil {
		bad(w, err)
		return
	}
	ok(w, pr)
}

func (s *Server) paymentRequestPDF(w http.ResponseWriter, r *http.Request) {
	pr, err := s.A.PaymentRequestFor(chi.URLParam(r, "id"))
	if err != nil {
		bad(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="Payment_request_%s.pdf"`, pr.PeriodID))
	if err := s.A.PaymentRequestPDF(w, pr); err != nil {
		log.Printf("payment request pdf: %v", err)
	}
}

func (s *Server) paymentRequestSent(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var b struct {
		Subject string `json:"subject"`
		Body    string `json:"body"`
	}
	if err := decode(r, &b); err != nil {
		bad(w, err)
		return
	}
	pr, err := s.A.PaymentRequestFor(id)
	if err != nil {
		bad(w, err)
		return
	}
	var flats []string
	for _, row := range pr.Rows {
		flats = append(flats, row.Flat)
	}
	if err := s.A.MarkPaymentRequestSent(id, b.Subject, b.Body, flats); err != nil {
		fail(w, 500, err)
		return
	}
	ok(w, map[string]bool{"ok": true})
}

func (s *Server) applyLateFees(w http.ResponseWriter, r *http.Request) {
	n, err := s.A.ApplyLateFees(app.Today())
	if err != nil {
		fail(w, 500, err)
		return
	}
	ok(w, map[string]int{"changed": n})
}

func (s *Server) listStatements(w http.ResponseWriter, r *http.Request) {
	ss, err := s.A.Statements()
	if err != nil {
		fail(w, 500, err)
		return
	}
	ok(w, ss)
}

func (s *Server) uploadStatement(w http.ResponseWriter, r *http.Request) {
	b, name, err := upload(r, "file", 20<<20)
	if err != nil {
		bad(w, err)
		return
	}
	st, err := s.A.ImportStatement(name, b)
	if err != nil && st == nil {
		bad(w, err)
		return
	}
	resp := map[string]any{"statement": st}
	if err != nil {
		resp["warning"] = err.Error()
	}
	ok(w, resp)
}

func (s *Server) listTxns(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	sid, _ := strconv.ParseInt(q.Get("statement"), 10, 64)
	ts, err := s.A.BankTxns(app.TxnFilter{StatementID: sid, Status: q.Get("status"), Dir: q.Get("dir"),
		Flat: strings.ToUpper(q.Get("flat")), Q: q.Get("q"), From: q.Get("from"), To: q.Get("to")})
	if err != nil {
		fail(w, 500, err)
		return
	}
	if err := s.A.AttachPostings(ts); err != nil {
		fail(w, 500, err)
		return
	}
	ok(w, ts)
}

func (s *Server) assignTxn(w http.ResponseWriter, r *http.Request) {
	id, err := idParam(r)
	if err != nil {
		bad(w, err)
		return
	}
	var as app.Assignment
	if err := decode(r, &as); err != nil {
		bad(w, err)
		return
	}
	if err := s.A.Assign(id, as); err != nil {
		bad(w, err)
		return
	}
	ts, _ := s.A.BankTxns(app.TxnFilter{ID: id})
	s.A.AttachPostings(ts)
	ok(w, ts[0])
}

func (s *Server) rematch(w http.ResponseWriter, r *http.Request) {
	n, err := s.A.Rematch()
	if err != nil {
		fail(w, 500, err)
		return
	}
	ok(w, map[string]int{"matched": n})
}

func (s *Server) payables(w http.ResponseWriter, r *http.Request) {
	p, err := s.A.PayablesDetail()
	if err != nil {
		fail(w, 500, err)
		return
	}
	ok(w, p)
}

func selectedPayees(r *http.Request) []string { return strings.Split(r.URL.Query().Get("payees"), ",") }

func (s *Server) payablesPDF(w http.ResponseWriter, r *http.Request) {
	p, err := s.A.PayablesDetail()
	if err != nil {
		fail(w, 500, err)
		return
	}
	p.Select(selectedPayees(r))
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="Payment_instruction_%s.pdf"`, p.AsOf))
	if err := s.A.InstructionPDF(w, p); err != nil {
		log.Printf("instruction pdf: %v", err)
	}
}

func (s *Server) payablesInstruction(w http.ResponseWriter, r *http.Request) {
	var b struct{ Payees []string }
	if err := decode(r, &b); err != nil {
		bad(w, err)
		return
	}
	p, err := s.A.PayablesDetail()
	if err != nil {
		fail(w, 500, err)
		return
	}
	p.Select(b.Payees)
	ok(w, map[string]any{"text": s.A.InstructionText(p), "total": p.Total, "payees": p.Payees})
}

func (s *Server) payablesSent(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Text   string
		Payees []string
	}
	if err := decode(r, &b); err != nil {
		bad(w, err)
		return
	}
	if err := s.A.MarkInstructionSent(b.Text, b.Payees); err != nil {
		fail(w, 500, err)
		return
	}
	ok(w, map[string]bool{"ok": true})
}

func (s *Server) savePayee(w http.ResponseWriter, r *http.Request) {
	var p app.Payee
	if err := decode(r, &p); err != nil {
		bad(w, err)
		return
	}
	name, err := url.PathUnescape(chi.URLParam(r, "name"))
	if err != nil {
		bad(w, err)
		return
	}
	p.Name = name
	if err := s.A.SavePayee(p); err != nil {
		bad(w, err)
		return
	}
	ok(w, s.A.Payee(p.Name))
}

func (s *Server) listDeposits(w http.ResponseWriter, r *http.Request) {
	on := r.URL.Query().Get("on")
	if on == "" {
		on = app.Today().Format(app.DateFmt)
	}
	ds, err := s.A.Deposits(on)
	if err != nil {
		fail(w, 500, err)
		return
	}
	ok(w, ds)
}

func (s *Server) saveDeposit(w http.ResponseWriter, r *http.Request) {
	var b struct {
		app.Deposit
		PrincipalRupees      float64 `json:"principal_rupees"`
		InstallmentRupees    float64 `json:"installment_rupees"`
		MaturityAmountRupees float64 `json:"maturity_amount_rupees"`
		ClosingAmountRupees  float64 `json:"closing_amount_rupees"`
	}
	if err := decode(r, &b); err != nil {
		bad(w, err)
		return
	}
	d := b.Deposit
	if id, err := idParam(r); err == nil {
		d.ID = id
	}
	d.Principal, d.Installment, d.MaturityAmount = app.Paise(b.PrincipalRupees), app.Paise(b.InstallmentRupees), app.Paise(b.MaturityAmountRupees)
	d.ClosingAmount = app.Paise(b.ClosingAmountRupees)
	id, err := s.A.SaveDeposit(d)
	if err != nil {
		bad(w, err)
		return
	}
	ok(w, map[string]int64{"id": id})
}

func (s *Server) deleteDeposit(w http.ResponseWriter, r *http.Request) {
	id, err := idParam(r)
	if err != nil {
		bad(w, err)
		return
	}
	if err := s.A.DeleteDeposit(id); err != nil {
		bad(w, err)
		return
	}
	ok(w, map[string]bool{"ok": true})
}

func (s *Server) listCategories(w http.ResponseWriter, r *http.Request) {
	cs, err := s.A.Categories()
	if err != nil {
		fail(w, 500, err)
		return
	}
	ok(w, cs)
}

func (s *Server) putCategory(w http.ResponseWriter, r *http.Request) {
	var c app.Category
	if err := decode(r, &c); err != nil {
		bad(w, err)
		return
	}
	if err := s.A.SaveCategory(c); err != nil {
		bad(w, err)
		return
	}
	s.listCategories(w, r)
}

func (s *Server) listExpenses(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	es, err := s.A.Expenses(app.ExpenseFilter{From: q.Get("from"), To: q.Get("to"), Category: q.Get("category"), Q: q.Get("q")})
	if err != nil {
		fail(w, 500, err)
		return
	}
	payees, _ := s.A.Payees()
	ok(w, map[string]any{"expenses": es, "payees": payees})
}

func (s *Server) saveExpense(w http.ResponseWriter, r *http.Request) {
	var b struct {
		app.Expense
		AmountRupees float64         `json:"amount_rupees"`
		Ignored      json.RawMessage `json:"amount"` // forms echo the paise field back as text; amount_rupees is authoritative
	}
	if err := decode(r, &b); err != nil {
		bad(w, err)
		return
	}
	e := b.Expense
	if id, err := idParam(r); err == nil {
		e.ID = id
	}
	e.Amount = app.Paise(b.AmountRupees)
	id, err := s.A.SaveExpense(e)
	if err != nil {
		bad(w, err)
		return
	}
	ok(w, map[string]int64{"id": id})
}

func (s *Server) billTemplates(w http.ResponseWriter, r *http.Request) {
	ts, err := s.A.BillTemplates()
	if err != nil {
		fail(w, 500, err)
		return
	}
	ok(w, ts)
}

func (s *Server) deleteExpense(w http.ResponseWriter, r *http.Request) {
	id, _ := idParam(r)
	if err := s.A.DeleteExpense(id); err != nil {
		bad(w, err)
		return
	}
	ok(w, map[string]bool{"ok": true})
}

var allowedBill = map[string]string{"image/jpeg": ".jpg", "image/png": ".png", "image/webp": ".webp", "application/pdf": ".pdf"}

// saveBillFile stores an uploaded bill and returns its file name.
func (s *Server) saveBillFile(b []byte) (string, error) {
	ext, okType := allowedBill[http.DetectContentType(b)]
	if !okType {
		return "", errors.New("bill must be JPEG, PNG, WebP or PDF")
	}
	sum := sha256.Sum256(b)
	name := hex.EncodeToString(sum[:8]) + ext
	return name, os.WriteFile(filepath.Join(s.A.DataDir, "bills", name), b, 0o644)
}

func (s *Server) scanBill(w http.ResponseWriter, r *http.Request) {
	b, _, err := upload(r, "file", 15<<20)
	if err != nil {
		bad(w, err)
		return
	}
	name, err := s.saveBillFile(b)
	if err != nil {
		bad(w, err)
		return
	}
	resp := map[string]any{"bill_file": name}
	if app.ScanEnabled() {
		bill, err := s.A.ScanBill(r.Context(), b)
		if err != nil {
			resp["scan_error"] = err.Error()
		} else {
			resp["scan"] = bill
		}
	}
	ok(w, resp)
}

func (s *Server) uploadBill(w http.ResponseWriter, r *http.Request) {
	id, _ := idParam(r)
	b, _, err := upload(r, "file", 15<<20)
	if err != nil {
		bad(w, err)
		return
	}
	name, err := s.saveBillFile(b)
	if err != nil {
		bad(w, err)
		return
	}
	if _, err := s.A.DB.Exec(`UPDATE expenses SET bill_file=? WHERE id=?`, name, id); err != nil {
		fail(w, 500, err)
		return
	}
	ok(w, map[string]string{"bill_file": name})
}

func (s *Server) getBill(w http.ResponseWriter, r *http.Request) {
	name := filepath.Base(chi.URLParam(r, "name"))
	http.ServeFile(w, r, filepath.Join(s.A.DataDir, "bills", name))
}

func (s *Server) listIncome(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	in, err := s.A.OtherIncomes(q.Get("from"), q.Get("to"))
	if err != nil {
		fail(w, 500, err)
		return
	}
	ok(w, in)
}

func (s *Server) addIncome(w http.ResponseWriter, r *http.Request) {
	var b struct {
		app.OtherIncome
		AmountRupees float64         `json:"amount_rupees"`
		Ignored      json.RawMessage `json:"amount"` // forms echo the paise field back as text; amount_rupees is authoritative
	}
	if err := decode(r, &b); err != nil {
		bad(w, err)
		return
	}
	b.OtherIncome.Amount = app.Paise(b.AmountRupees)
	if b.OtherIncome.Flat != nil && *b.OtherIncome.Flat == "" {
		b.OtherIncome.Flat = nil
	}
	if err := s.A.SaveOtherIncome(b.OtherIncome); err != nil {
		bad(w, err)
		return
	}
	ok(w, map[string]bool{"ok": true})
}

func (s *Server) deleteIncome(w http.ResponseWriter, r *http.Request) {
	id, _ := idParam(r)
	if err := s.A.DeleteOtherIncome(id); err != nil {
		bad(w, err)
		return
	}
	ok(w, map[string]bool{"ok": true})
}

func periodOrCurrent(r *http.Request) string {
	if p := r.URL.Query().Get("period"); p != "" {
		return p
	}
	return app.CurrentPeriodID()
}

func (s *Server) listReminders(w http.ResponseWriter, r *http.Request) {
	rs, err := s.A.Reminders(r.URL.Query().Get("period"))
	if err != nil {
		fail(w, 500, err)
		return
	}
	ok(w, rs)
}

func (s *Server) previewReminder(w http.ResponseWriter, r *http.Request) {
	rm, err := s.A.BuildReminder(periodOrCurrent(r))
	if err != nil {
		bad(w, err)
		return
	}
	ok(w, rm)
}

func (s *Server) createReminder(w http.ResponseWriter, r *http.Request) {
	rm, err := s.A.SaveReminderDraft(periodOrCurrent(r))
	if err != nil {
		bad(w, err)
		return
	}
	ok(w, rm)
}

func (s *Server) markSent(w http.ResponseWriter, r *http.Request) {
	id, _ := idParam(r)
	var b struct{ Text string }
	_ = decode(r, &b)
	if err := s.A.MarkReminderSent(id, b.Text); err != nil {
		bad(w, err)
		return
	}
	ok(w, map[string]bool{"ok": true})
}

func (s *Server) deleteReminder(w http.ResponseWriter, r *http.Request) {
	id, _ := idParam(r)
	if err := s.A.DeleteReminder(id); err != nil {
		bad(w, err)
		return
	}
	ok(w, map[string]bool{"ok": true})
}

func (s *Server) pnl(w http.ResponseWriter, r *http.Request) {
	p, err := s.A.PnLFor(periodOrCurrent(r))
	if err != nil {
		bad(w, err)
		return
	}
	ok(w, p)
}

func (s *Server) pnlPDF(w http.ResponseWriter, r *http.Request) {
	p, err := s.A.PnLFor(periodOrCurrent(r))
	if err != nil {
		bad(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="PnL_%s.pdf"`, p.PeriodID))
	s.A.PnLPDF(w, p)
}

func csvEscape(s string) string {
	if strings.ContainsAny(s, ",\"\n") {
		return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
	}
	return s
}

func rupee(p int64) string { return strconv.FormatFloat(float64(p)/100, 'f', 2, 64) }

func (s *Server) pnlCSV(w http.ResponseWriter, r *http.Request) {
	p, err := s.A.PnLFor(periodOrCurrent(r))
	if err != nil {
		bad(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="PnL_%s.csv"`, p.PeriodID))
	fmt.Fprintf(w, "Section,Item,Amount,Receivable\n")
	for _, l := range p.Income {
		recv := rupee(l.Receivable)
		if l.NoReceivable {
			recv = ""
		}
		fmt.Fprintf(w, "Income,%s,%s,%s\n", csvEscape(l.Label), rupee(l.Actual), recv)
	}
	fmt.Fprintf(w, "Income,Total income,%s,%s\n", rupee(p.TotalIncome), rupee(p.TotalReceivable))
	for _, l := range p.Expenses {
		fmt.Fprintf(w, "Expenditure,%s,%s\n", csvEscape(l.Label), rupee(l.Amount))
	}
	fmt.Fprintf(w, "Expenditure,Total expenditure,%s\n", rupee(p.TotalExpenses))
	fmt.Fprintf(w, "Result,Surplus to sinking fund,%s\n", rupee(p.Surplus))
	fmt.Fprintf(w, "Sinking fund,Balance brought forward,%s\n", rupee(p.FundOpening))
	fmt.Fprintf(w, "Sinking fund,Add: surplus for %s,%s\n", p.PeriodID, rupee(p.Surplus))
	fmt.Fprintf(w, "Sinking fund,Balance carried forward,%s\n", rupee(p.FundClosing))
	fmt.Fprintf(w, "Sinking fund,Current balance as of %s,%s\n", p.FundAsOf, rupee(p.FundCurrent))
	if p.HasBankBalance {
		fmt.Fprintf(w, "Bank,Bank balance as of %s,%s\n", p.BankBalanceOn, rupee(p.BankBalance))
		fmt.Fprintf(w, "Bank,Add: FD/RD deposits,%s\n", rupee(p.DepositsBalance))
		fmt.Fprintf(w, "Bank,Less: sinking fund,%s\n", rupee(-p.FundClosing))
		fmt.Fprintf(w, "Bank,Less: advances held for members,%s\n", rupee(-p.AdvancesHeld))
		fmt.Fprintf(w, "Bank,Reserves not yet allocated to a fund,%s\n", rupee(p.UnallocatedReserve))
	}
	if p.BankCurrentOn != "" {
		fmt.Fprintf(w, "Bank,Current account balance as of %s,%s\n", p.BankCurrentOn, rupee(p.BankCurrent))
	}
	for _, it := range p.NonPnLItems {
		fmt.Fprintf(w, "Non P&L,%s,%s\n", csvEscape(it.Date+" "+it.Description+" ("+it.Payee+")"), rupee(it.Amount))
	}
	fmt.Fprintf(w, "Non P&L,Total non P&L items,%s\n", rupee(p.NonPnLOutflow))
}

func (s *Server) sinking(w http.ResponseWriter, r *http.Request) {
	sf, err := s.A.SinkingFundReport()
	if err != nil {
		fail(w, 500, err)
		return
	}
	ok(w, sf)
}

func (s *Server) calibrateSinking(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Period string `json:"period"`
	}
	if err := decode(r, &b); err != nil {
		bad(w, err)
		return
	}
	opening, err := s.A.CalibrateSinkingFund(b.Period)
	if err != nil {
		bad(w, err)
		return
	}
	ok(w, map[string]int64{"opening": opening})
}

func (s *Server) expensesCSV(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	es, err := s.A.Expenses(app.ExpenseFilter{From: q.Get("from"), To: q.Get("to"), Category: q.Get("category")})
	if err != nil {
		fail(w, 500, err)
		return
	}
	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", `attachment; filename="expenses.csv"`)
	fmt.Fprintln(w, "Date,Particulars,Category,Payee,Amount,Notes")
	for _, e := range es {
		fmt.Fprintf(w, "%s,%s,%s,%s,%s,%s\n", e.Date, csvEscape(e.Description), csvEscape(e.Category), csvEscape(e.Payee), rupee(e.Amount), csvEscape(e.Notes))
	}
}

func (s *Server) backup(w http.ResponseWriter, r *http.Request) {
	tmp := filepath.Join(os.TempDir(), fmt.Sprintf("treasuredash-backup-%d.db", time.Now().UnixNano()))
	defer os.Remove(tmp)
	if err := s.A.DB.Snapshot(tmp); err != nil {
		fail(w, http.StatusNotImplemented, err)
		return
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="treasuredash-%s.db"`, app.Today().Format(app.DateFmt)))
	http.ServeFile(w, r, tmp)
}

// ---- bulk edit ------------------------------------------------------------------

func (s *Server) dataSheet(w http.ResponseWriter, r *http.Request) {
	d, err := s.A.Sheet(chi.URLParam(r, "name"))
	if err != nil {
		bad(w, err)
		return
	}
	ok(w, d)
}

func (s *Server) dataWorkbook(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="treasuredash-data-%s.xlsx"`, app.Today().Format(app.DateFmt)))
	if err := s.A.WriteWorkbook(w); err != nil {
		log.Printf("workbook: %v", err)
	}
}

// changeset reads either an uploaded workbook (multipart "file") or JSON rows
// from the online editor.
func (s *Server) changeset(r *http.Request) (app.Changeset, error) {
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
		b, _, err := upload(r, "file", 20<<20)
		if err != nil {
			return nil, err
		}
		return s.A.ReadWorkbook(strings.NewReader(string(b)))
	}
	var cs app.Changeset
	defer r.Body.Close()
	if err := json.NewDecoder(io.LimitReader(r.Body, 10<<20)).Decode(&cs); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	return cs, nil
}

func (s *Server) dataPreview(w http.ResponseWriter, r *http.Request) {
	cs, err := s.changeset(r)
	if err != nil {
		bad(w, err)
		return
	}
	res, err := s.A.PlanChanges(cs)
	if err != nil {
		bad(w, err)
		return
	}
	ok(w, res)
}

func (s *Server) dataApply(w http.ResponseWriter, r *http.Request) {
	cs, err := s.changeset(r)
	if err != nil {
		bad(w, err)
		return
	}
	res, err := s.A.ApplyChanges(cs)
	if err != nil {
		if res == nil {
			bad(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		json.NewEncoder(w).Encode(map[string]any{"error": err.Error(), "result": res})
		return
	}
	ok(w, res)
}
