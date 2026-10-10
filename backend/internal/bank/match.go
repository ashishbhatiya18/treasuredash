package bank

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// Payer extracts a stable identity for whoever sent/received the money, so a
// confirmed mapping can be remembered and applied to future statements.
type Payer struct {
	Key  string // e.g. upi:name123@oksbi, acct:00771610000000
	Name string
}

var (
	upiRe    = regexp.MustCompile(`^UPI-([^-]+)-([^-]+@[^-]+)-`)
	neftRe   = regexp.MustCompile(`^NEFT (?:CR|DR)-([A-Z0-9]{11})-([^-]+)-`)
	impsRe   = regexp.MustCompile(`^IMPS-\d+-([^-]+)-([A-Z]{4})-X*(\d{3,})`)
	tptRe    = regexp.MustCompile(`^(\d{11,16})-TPT-(.*)-([^-]+)$`)
	billpay  = regexp.MustCompile(`-([A-Z]+)-BILLPAY`)
	ftRe     = regexp.MustCompile(`^FT-([A-Z0-9]+)-`)
	spaceRun = regexp.MustCompile(`\s+`)
)

func ExtractPayer(narration string) Payer {
	n := strings.ToUpper(strings.TrimSpace(narration))
	clean := func(s string) string { return strings.TrimSpace(spaceRun.ReplaceAllString(s, " ")) }
	if m := upiRe.FindStringSubmatch(n); m != nil {
		return Payer{"upi:" + strings.ToLower(strings.ReplaceAll(m[2], " ", "")), clean(m[1])}
	}
	if m := neftRe.FindStringSubmatch(n); m != nil {
		name := clean(m[2])
		return Payer{"neft:" + m[1] + ":" + name, name}
	}
	if m := impsRe.FindStringSubmatch(n); m != nil {
		name := clean(m[1])
		return Payer{"imps:" + m[2] + ":" + m[3], name}
	}
	if m := tptRe.FindStringSubmatch(n); m != nil {
		return Payer{"acct:" + m[1], clean(m[3])}
	}
	if m := billpay.FindStringSubmatch(n); m != nil {
		return Payer{"billpay:" + m[1], m[1]}
	}
	if m := ftRe.FindStringSubmatch(n); m != nil {
		return Payer{"ft:" + m[1], ""}
	}
	return Payer{}
}

// ---- flat code detection ------------------------------------------------------

type CodeHit struct {
	Code   string
	Strong bool
}

func isAlnum(b byte) bool { return (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') }
func isDigit(b byte) bool { return b >= '0' && b <= '9' }

// FindFlatCodes finds things that look like flat numbers (G/1-9 + A-F).
// Strong: delimited on both sides, or right after the word FLAT.
// Weak: glued to letters (e.g. "ANITA8D", "RAHUL1DMAINT") but never to digits,
// which keeps reference numbers like "HDFC75D24" from matching.
func FindFlatCodes(narration string) []CodeHit {
	u := strings.ToUpper(narration)
	seen := map[string]int{}
	var hits []CodeHit
	add := func(code string, strong bool) {
		if i, ok := seen[code]; ok {
			if strong {
				hits[i].Strong = true
			}
			return
		}
		seen[code] = len(hits)
		hits = append(hits, CodeHit{code, strong})
	}
	for i := 0; i+1 < len(u); i++ {
		f, l := u[i], u[i+1]
		gap := 0
		if (f == 'G' || (f >= '1' && f <= '9')) && l == ' ' && i+2 < len(u) && u[i+2] >= 'A' && u[i+2] <= 'F' {
			gap, l = 1, u[i+2] // "FLAT G E" style is rare; only honoured after FLAT
		}
		if !(f == 'G' || (f >= '1' && f <= '9')) || l < 'A' || l > 'F' {
			continue
		}
		var prev, next byte = ' ', ' '
		if i > 0 {
			prev = u[i-1]
		}
		if i+2+gap < len(u) {
			next = u[i+2+gap]
		}
		before := strings.TrimRight(u[:i], " -:#./")
		afterFlat := strings.HasSuffix(before, "FLAT") || strings.HasSuffix(before, "FLATNO") || strings.HasSuffix(before, "FLAT NO")
		code := string([]byte{f, l})
		switch {
		case afterFlat:
			add(code, true)
		case gap == 1:
			continue
		case !isAlnum(prev) && !isAlnum(next):
			add(code, true)
		case f != 'G' && !isDigit(prev) && !isDigit(next):
			add(code, false)
		}
	}
	return hits
}

// ---- matching ---------------------------------------------------------------------

type FlatInfo struct {
	Code        string
	Owner       string
	Quarterly   int64 // standard quarterly charge
	Outstanding int64 // balance due when the payment arrived
}

type Result struct {
	Flat       string
	Confidence int // 0-100
	Auto       bool
	Reasons    []string
}

var nonLetters = regexp.MustCompile(`[^A-Z]`)

func nameTokens(owner string) []string {
	var out []string
	for _, t := range strings.FieldsFunc(strings.ToUpper(owner), func(r rune) bool { return !unicode.IsLetter(r) }) {
		if len(t) >= 3 {
			out = append(out, t)
		}
	}
	return out
}

func rupees(p int64) string { return fmt.Sprintf("₹%d", p/100) }

// MatchCredit scores every flat for an incoming payment and picks the best.
func MatchCredit(narration string, amount int64, payer Payer, flats []FlatInfo, aliases map[string]string) Result {
	score := map[string]int{}
	why := map[string][]string{}
	add := func(code string, pts int, reason string) {
		score[code] += pts
		why[code] = append(why[code], reason)
	}
	known := map[string]bool{}
	for _, f := range flats {
		known[f.Code] = true
	}
	var notes []string

	// 1. learned payer identity
	if payer.Key != "" {
		if fl, ok := aliases[payer.Key]; ok && known[fl] {
			add(fl, 100, fmt.Sprintf("payer %s previously confirmed for %s", payer.Key, fl))
		}
	}

	// 2. flat number written in the remarks
	for _, h := range FindFlatCodes(narration) {
		if !known[h.Code] {
			if h.Strong {
				notes = append(notes, fmt.Sprintf("mentions %s, which is not a flat in the master list", h.Code))
			}
			continue
		}
		if h.Strong {
			add(h.Code, 60, "flat "+h.Code+" written in remarks")
		} else {
			add(h.Code, 35, "possible flat "+h.Code+" inside remarks")
		}
	}

	// 3. owner name
	letters := nonLetters.ReplaceAllString(strings.ToUpper(narration), "")
	for _, f := range flats {
		toks := nameTokens(f.Owner)
		if len(toks) == 0 {
			continue
		}
		hit := 0
		for _, t := range toks {
			if strings.Contains(letters, t) {
				hit++
			}
		}
		switch {
		case hit == len(toks) && len(toks) >= 2:
			add(f.Code, 50, "owner name "+f.Owner+" matches")
		case hit == len(toks):
			add(f.Code, 35, "owner name "+f.Owner+" matches")
		case hit > 0:
			add(f.Code, 10, "part of owner name "+f.Owner+" matches")
		}
	}

	// 4. amount
	byAmount := map[int64][]string{}
	for _, f := range flats {
		byAmount[f.Quarterly] = append(byAmount[f.Quarterly], f.Code)
		if f.Outstanding > 0 && f.Outstanding != f.Quarterly {
			byAmount[f.Outstanding] = append(byAmount[f.Outstanding], f.Code)
		}
	}
	if cands := byAmount[amount]; len(cands) == 1 {
		add(cands[0], 40, rupees(amount)+" is due only from "+cands[0])
	} else if len(cands) > 1 {
		for _, c := range cands {
			if score[c] > 0 { // only corroborates other evidence
				add(c, 15, rupees(amount)+" equals the amount due")
			}
		}
	}

	type cand struct {
		code string
		s    int
	}
	var cs []cand
	for c, s := range score {
		cs = append(cs, cand{c, s})
	}
	sort.Slice(cs, func(i, j int) bool {
		if cs[i].s != cs[j].s {
			return cs[i].s > cs[j].s
		}
		return cs[i].code < cs[j].code
	})
	if len(cs) == 0 {
		return Result{Reasons: append(notes, "no flat number, known payer or owner name found")}
	}
	best := cs[0]
	second := 0
	if len(cs) > 1 {
		second = cs[1].s
	}
	conf := best.s
	if conf > 100 {
		conf = 100
	}
	r := Result{Flat: best.code, Confidence: conf, Reasons: append(why[best.code], notes...)}
	r.Auto = best.s >= 60 && best.s-second >= 30
	if len(cs) > 1 && !r.Auto {
		r.Reasons = append(r.Reasons, fmt.Sprintf("also possible: %s (%s)", cs[1].code, strings.Join(why[cs[1].code], "; ")))
	}
	return r
}
