// Package bank parses HDFC account statements and matches transactions to flats.
package bank

import (
	"bytes"
	"fmt"
	"math"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type Txn struct {
	Date      string // YYYY-MM-DD
	ValueDate string
	Narration string // wrapped lines joined exactly as printed (no added spaces)
	RefNo     string
	Debit     int64 // paise
	Credit    int64
	Balance   int64
}

type Statement struct {
	AccountNo string
	From, To  string
	Opening   int64
	Closing   int64
	HasTotals bool
	DrCount   int
	CrCount   int
	Txns      []Txn
}

// PDFToText shells out to poppler's pdftotext in layout mode, which keeps the
// statement's fixed columns intact.
func PDFToText(pdf []byte) (string, error) {
	cmd := exec.Command("pdftotext", "-layout", "-enc", "UTF-8", "-", "-")
	cmd.Stdin = bytes.NewReader(pdf)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("pdftotext: %v: %s", err, errb.String())
	}
	return out.String(), nil
}

var (
	txnLine    = regexp.MustCompile(`^\s*(\d{2}/\d{2}/\d{2})\s{1,}(.*?)\s{2,}(\S+)\s+(\d{2}/\d{2}/\d{2})\s+([\d,]+\.\d{2})\s+([\d,]+\.\d{2})\s*$`)
	headerLine = regexp.MustCompile(`Narration.*Withdrawal Amt.*Deposit Amt.*Closing Balance`)
	accountRe  = regexp.MustCompile(`Account No\s*:\s*(\d+)`)
	periodRe   = regexp.MustCompile(`Statement From\s*:\s*(\d{2}/\d{2}/\d{4})\s+To\s*:\s*(\d{2}/\d{2}/\d{4})`)
	amountRe   = regexp.MustCompile(`[\d,]+\.\d{2}`)
)

func parseAmount(s string) int64 {
	f, _ := strconv.ParseFloat(strings.ReplaceAll(s, ",", ""), 64)
	return int64(math.Round(f * 100))
}

func parseDDMMYY(s string) string {
	t, err := time.Parse("02/01/06", s)
	if err != nil {
		return ""
	}
	return t.Format("2006-01-02")
}

func parseDDMMYYYY(s string) string {
	t, err := time.Parse("02/01/2006", s)
	if err != nil {
		return ""
	}
	return t.Format("2006-01-02")
}

// ParseHDFC parses `pdftotext -layout` output of an HDFC "Statement of account".
func ParseHDFC(text string) (*Statement, error) {
	st := &Statement{}
	if m := accountRe.FindStringSubmatch(text); m != nil {
		st.AccountNo = m[1]
	}
	if m := periodRe.FindStringSubmatch(text); m != nil {
		st.From, st.To = parseDDMMYYYY(m[1]), parseDDMMYYYY(m[2])
	}

	lines := strings.Split(text, "\n")
	inTable := false
	depositEnd, withdrawEnd := 0, 0
	var cur *Txn
	var conts [][]string // raw continuation lines per txn
	var curCont []string
	flush := func() {
		if cur != nil {
			st.Txns = append(st.Txns, *cur)
			conts = append(conts, curCont)
			cur, curCont = nil, nil
		}
	}
	type pending struct {
		idx    int
		amount int64
		end    int
	}
	var pend []pending

	for i := 0; i < len(lines); i++ {
		ln := lines[i]
		if headerLine.MatchString(ln) {
			inTable = true
			withdrawEnd = strings.Index(ln, "Withdrawal Amt.") + len("Withdrawal Amt.")
			depositEnd = strings.Index(ln, "Deposit Amt.") + len("Deposit Amt.")
			continue
		}
		if strings.Contains(ln, "STATEMENT SUMMARY") {
			flush()
			inTable = false
			// totals are on the line(s) after the labels
			for j := i + 1; j < len(lines) && j < i+6; j++ {
				nums := amountRe.FindAllString(lines[j], -1)
				fields := strings.Fields(lines[j])
				if len(nums) == 4 && len(fields) == 6 {
					st.Opening = parseAmount(fields[0])
					st.DrCount, _ = strconv.Atoi(fields[1])
					st.CrCount, _ = strconv.Atoi(fields[2])
					st.Closing = parseAmount(fields[5])
					st.HasTotals = true
					break
				}
			}
			continue
		}
		// later pages repeat the account header but not the column header
		if withdrawEnd > 0 && periodRe.MatchString(ln) {
			inTable = true
			continue
		}
		if !inTable {
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(ln), "HDFC BANK LIMITED") || strings.Contains(ln, "Page No .:") {
			flush()
			inTable = false
			continue
		}
		if m := txnLine.FindStringSubmatchIndex(ln); m != nil {
			flush()
			g := func(k int) string { return ln[m[2*k]:m[2*k+1]] }
			t := &Txn{
				Date:      parseDDMMYY(g(1)),
				Narration: g(2),
				RefNo:     g(3),
				ValueDate: parseDDMMYY(g(4)),
				Balance:   parseAmount(g(6)),
			}
			pend = append(pend, pending{idx: len(st.Txns), amount: parseAmount(g(5)), end: m[11]})
			cur = t
			continue
		}
		if cur != nil && strings.TrimSpace(ln) != "" {
			curCont = append(curCont, ln)
		}
	}
	flush()

	// Wrapped narration lines share one left edge; anything indented further
	// carries a real leading space (a word boundary the bank printed).
	base := -1
	for _, cs := range conts {
		for _, c := range cs {
			ind := len(c) - len(strings.TrimLeft(c, " "))
			if base < 0 || ind < base {
				base = ind
			}
		}
	}
	for i, cs := range conts {
		for _, c := range cs {
			seg := c[base:]
			if k := strings.Index(strings.TrimLeft(seg, " "), "   "); k >= 0 { // other columns never wrap; drop stray text
				lead := len(seg) - len(strings.TrimLeft(seg, " "))
				seg = seg[:lead+k]
			}
			st.Txns[i].Narration += strings.TrimRight(seg, " ")
		}
		st.Txns[i].Narration = strings.TrimSpace(st.Txns[i].Narration)
	}

	// Resolve withdrawal vs deposit: balance arithmetic first, column position as fallback.
	prevBal := st.Opening
	havePrev := st.HasTotals
	mid := (withdrawEnd + depositEnd) / 2
	for _, p := range pend {
		t := &st.Txns[p.idx]
		t.Credit, t.Debit = 0, 0
		switch {
		case havePrev && prevBal+p.amount == t.Balance:
			t.Credit = p.amount
		case havePrev && prevBal-p.amount == t.Balance:
			t.Debit = p.amount
		case p.end > mid && mid > 0:
			t.Credit = p.amount
		default:
			t.Debit = p.amount
		}
		prevBal, havePrev = t.Balance, true
	}

	if len(st.Txns) == 0 {
		return nil, fmt.Errorf("no transactions found – is this an HDFC 'Statement of account' PDF?")
	}
	if st.HasTotals {
		var dr, cr int
		for _, t := range st.Txns {
			if t.Debit > 0 {
				dr++
			} else {
				cr++
			}
		}
		if dr != st.DrCount || cr != st.CrCount {
			return st, fmt.Errorf("parsed %d debits/%d credits but statement summary says %d/%d", dr, cr, st.DrCount, st.CrCount)
		}
	}
	return st, nil
}
