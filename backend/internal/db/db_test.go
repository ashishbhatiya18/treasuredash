package db

import (
	"regexp"
	"sort"
	"strings"
	"testing"
)

func TestRebind(t *testing.T) {
	got := rebind(`SELECT * FROM t WHERE a=? AND b='what?' AND c IN (?,?) AND d LIKE '%x%'`)
	want := `SELECT * FROM t WHERE a=$1 AND b='what?' AND c IN ($2,$3) AND d LIKE '%x%'`
	if got != want {
		t.Fatalf("\n got %s\nwant %s", got, want)
	}
	if rebind(strings.Repeat("?,", 11)) != "$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11," {
		t.Fatal("two-digit placeholders")
	}
}

// columns returns table → sorted column names declared in a schema file,
// including ALTER TABLE … ADD COLUMN lines.
func columns(schema string) map[string][]string {
	out := map[string][]string{}
	table := regexp.MustCompile(`(?s)CREATE TABLE IF NOT EXISTS (\w+) \((.*?)\n\);`)
	col := regexp.MustCompile(`^\s+(\w+)\s+[A-Z]`)
	for _, m := range table.FindAllStringSubmatch(schema, -1) {
		for _, line := range strings.Split(m[2], "\n") {
			if c := col.FindStringSubmatch(line); c != nil && c[1] != "UNIQUE" && c[1] != "CHECK" {
				out[m[1]] = append(out[m[1]], c[1])
			}
		}
	}
	for _, m := range regexp.MustCompile(`ALTER TABLE (\w+) ADD COLUMN IF NOT EXISTS (\w+)`).FindAllStringSubmatch(schema, -1) {
		out[m[1]] = append(out[m[1]], m[2])
	}
	for k := range out {
		sort.Strings(out[k])
		out[k] = dedupe(out[k])
	}
	return out
}

func dedupe(s []string) []string {
	var o []string
	for i, x := range s {
		if i == 0 || x != s[i-1] {
			o = append(o, x)
		}
	}
	return o
}

// The SQLite and PostgreSQL schemas must describe the same tables and columns
// (older SQLite databases gain columns through App migrations).
func TestSchemasInStep(t *testing.T) {
	lite, pg := columns(schemaSQLite), columns(schemaPG)
	for _, add := range [][2]string{{"deposits", "from_savings"}, {"deposits", "to_savings"}, {"deposits", "closing_amount"}, {"reminders", "kind"}, {"reminders", "subject"}} {
		lite[add[0]] = dedupe(append(lite[add[0]], add[1]))
		sort.Strings(lite[add[0]])
	}
	if len(lite) != len(pg) {
		t.Errorf("table count: sqlite %d, postgres %d", len(lite), len(pg))
	}
	for tbl, cols := range lite {
		if strings.Join(cols, ",") != strings.Join(pg[tbl], ",") {
			t.Errorf("%s:\n sqlite   %v\n postgres %v", tbl, cols, pg[tbl])
		}
	}
}
