package bank

import (
	"encoding/json"
	"os"
	"testing"
)

const samplePDF = "../../../data/Acct_Statement_XXXXXXXX3893_03102026.pdf"

// The sample statement and the flats it should match live in data/ (not in
// git: they contain residents' details). Tests skip when they're absent.
type sampleFixture struct {
	Flats []struct {
		Code      string `json:"code"`
		Owner     string `json:"owner"`
		Quarterly int64  `json:"quarterly"`
	} `json:"flats"`
	Want map[string]string `json:"want"` // bank ref → flat
}

func loadFixture(t *testing.T) ([]FlatInfo, map[string]string) {
	b, err := os.ReadFile("../../../data/sample_flats.json")
	if err != nil {
		t.Skip("data/sample_flats.json not present")
	}
	var f sampleFixture
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	var fs []FlatInfo
	for _, x := range f.Flats {
		fs = append(fs, FlatInfo{Code: x.Code, Owner: x.Owner, Quarterly: x.Quarterly * 100})
	}
	return fs, f.Want
}

func TestParseAndMatchSample(t *testing.T) {
	pdf, err := os.ReadFile(samplePDF)
	if err != nil {
		t.Skip("sample statement not present")
	}
	text, err := PDFToText(pdf)
	if err != nil {
		t.Fatal(err)
	}
	st, err := ParseHDFC(text)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Txns) != 37 || st.Opening != 150776682 || st.Closing != 183977382 {
		t.Fatalf("got %d txns opening %d closing %d", len(st.Txns), st.Opening, st.Closing)
	}
	fs, want := loadFixture(t)
	for _, tx := range st.Txns {
		p := ExtractPayer(tx.Narration)
		if tx.Credit == 0 {
			t.Logf("DR %8d %-60.60s payer=%s", tx.Debit/100, tx.Narration, p.Key)
			continue
		}
		r := MatchCredit(tx.Narration, tx.Credit, p, fs, nil)
		t.Logf("CR %8d %-3s auto=%-5v conf=%3d %-70.70s %v", tx.Credit/100, r.Flat, r.Auto, r.Confidence, tx.Narration, r.Reasons)
		if w, ok := want[tx.RefNo]; ok && (r.Flat != w || !r.Auto) {
			t.Errorf("ref %s: want %s auto, got %s auto=%v (%v)", tx.RefNo, w, r.Flat, r.Auto, r.Reasons)
		}
	}
}

func TestFindFlatCodes(t *testing.T) {
	cases := map[string]string{
		"00771140012345-TPT-ANITA8D Q2 2026 27-ANITA R":                                      "8D", // glued to a name
		"50100100000000-TPT-HDFC75D2466230E0-RAHUL MEHTA":                                    "",   // glued to digits: a ref, not a flat
		"IMPS-627000000000-RAHULK-UTIB-XXXXXXXXXX1234-OTHERSRAHUL1DMAINTENANCEJULYAUGSEPT26": "1D",
		"00771610200000-TPT-FLATGFMAINTQ2-PRIYA SEN":                                         "GF", // right after FLAT
		"FT-XX0710-FLAT GE-KIRAN RAO-F6":                                                     "GE",
	}
	for in, want := range cases {
		hits := FindFlatCodes(in)
		got := ""
		if len(hits) > 0 {
			got = hits[0].Code
		}
		if got != want {
			t.Errorf("%q: got %v want %s", in, hits, want)
		}
	}
}
