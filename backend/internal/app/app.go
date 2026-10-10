package app

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"treasuredash/internal/db"
)

type App struct {
	DB      *db.DB
	DataDir string     // uploaded bills (and the SQLite file / snapshots when not on Postgres)
	mu      sync.Mutex // serialises multi-statement writes
}

// Open uses a SQLite file in dataDir (local development and tests).
func Open(dataDir string) (*App, error) { return OpenWith(dataDir, "") }

// OpenWith uses PostgreSQL when databaseURL is set, otherwise SQLite in
// dataDir. Bills are stored under dataDir either way.
func OpenWith(dataDir, databaseURL string) (*App, error) {
	if err := os.MkdirAll(filepath.Join(dataDir, "bills"), 0o755); err != nil {
		return nil, err
	}
	if databaseURL == "" {
		if err := renameLegacyDB(dataDir); err != nil {
			return nil, err
		}
	}
	conn, err := db.Open(databaseURL, SQLitePath(dataDir))
	if err != nil {
		return nil, err
	}
	a := &App{DB: conn, DataDir: dataDir}
	if err := a.seedDefaults(); err != nil {
		return nil, err
	}
	if err := a.migrateFYNaming(); err != nil {
		return nil, err
	}
	if err := a.migrateColumns(); err != nil {
		return nil, err
	}
	if err := a.upgradeDefaultTexts(); err != nil {
		return nil, err
	}
	// FDs booked on statements imported before auto-registration existed
	if _, err := a.RegisterBookedDeposits(); err != nil {
		return nil, err
	}
	return a, nil
}

// ---- money helpers -------------------------------------------------------

// Paise converts rupees to integer paise.
func Paise(rupees float64) int64 { return int64(math.Round(rupees * 100)) }

// Rupees formats paise as an Indian-grouped rupee string without symbol, e.g. 1,23,456 or 527.25.
func Rupees(p int64) string {
	neg := p < 0
	if neg {
		p = -p
	}
	whole, frac := p/100, p%100
	s := strconv.FormatInt(whole, 10)
	if len(s) > 3 {
		head, tail := s[:len(s)-3], s[len(s)-3:]
		var parts []string
		for len(head) > 2 {
			parts = append([]string{head[len(head)-2:]}, parts...)
			head = head[:len(head)-2]
		}
		if head != "" {
			parts = append([]string{head}, parts...)
		}
		s = strings.Join(parts, ",") + "," + tail
	}
	if frac != 0 {
		s += fmt.Sprintf(".%02d", frac)
	}
	if neg {
		s = "-" + s
	}
	return s
}

// ---- settings ------------------------------------------------------------

var defaultSettings = map[string]string{
	// association-specific values are entered in Settings; these are placeholders
	"association_name":    "Owners Association",
	"association_address": "",
	"bank_name":           "",
	"bank_account_name":   "",
	"bank_account_no":     "",
	"bank_ifsc":           "",
	"rate_per_sqft":       "4",
	"late_fee":            "1000",
	"signatory":           "Treasurer",
	"treasurer_name":      "",
	"treasurer_phone":     "",
	// Day 0 payment request (sent when a quarter's invoices are raised)
	"payment_request_subject": "Action needed: pay {{quarter_label}} maintenance by {{due_date}}",
	"payment_request_template": `Dear Resident,

Maintenance for {{quarter_label}} is now due.
>> Please pay by {{due_date}}.
Your amount is listed against your flat number in the attached payment request.

HOW TO PAY - 3 QUICK STEPS
1. Find your flat in the attached sheet and pay the amount in the "{{due_column}}" column.
2. Transfer it to the association account below, preferably by NEFT.
3. Write your flat number in the transaction remarks (e.g. "3B {{quarter}}") - this is how we credit your payment.

ASSOCIATION ACCOUNT
Name: {{account_name}}
Bank: {{bank_name}}
A/c No.: {{account_no}}
IFSC: {{ifsc}}

PLEASE NOTE
- After {{due_date}}, a late fee of Rs.{{late_fee}} per month applies. Pending dues and earlier penalties are already included in your amount.
- Pay only into the association account above. Please don't GPay the caretaker or any committee member (including me).
- No penny-drop test transfers, please.
- Owners: please forward this email to your tenants, if applicable.

Questions or a mismatch in your dues? WhatsApp me on {{treasurer_phone}} with your payment proof. Please avoid calls on weekdays.

Coming up next: {{next_quarter_label}} maintenance, due by {{next_due_date}}.

Thank you for paying on time - it keeps our society running smoothly.

Regards,
{{treasurer_name}}
{{signatory}}`,
	// Transactions dated before the cut-over are already reflected in the
	// opening balances imported from the spreadsheet.
	"cutover_date":           "2026-10-01",
	"sinking_fund_opening":   "0",
	"sinking_fund_note":      "",
	"reminder_interval_days": "3",
	"reminder_auto":          "1",
	"reminder_template": `Reminder#{{seq}}

Hi GM all,
{{status_line}}
Below {{count}} Flat owners are yet to clear their dues:

{{flats}}

For others who have cleared their dues, thanks much for being prompt.
TBH - {{due_line}}`,
}

func (a *App) seedDefaults() error {
	for k, v := range defaultSettings {
		if _, err := a.DB.Exec(`INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT DO NOTHING`, k, v); err != nil {
			return err
		}
	}
	for _, c := range defaultCategories {
		if _, err := a.DB.Exec(`INSERT INTO categories(name,in_pnl,keywords,priority) VALUES(?,?,?,?) ON CONFLICT DO NOTHING`,
			c.Name, db.Bool(c.InPnL), c.Keywords, c.Priority); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) Settings() (map[string]string, error) {
	rows, err := a.DB.Query(`SELECT key,value FROM settings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

func (a *App) Setting(key string) string {
	var v string
	if err := a.DB.QueryRow(`SELECT value FROM settings WHERE key=?`, key).Scan(&v); err != nil {
		return defaultSettings[key]
	}
	return v
}

func (a *App) SettingFloat(key string) float64 {
	f, _ := strconv.ParseFloat(a.Setting(key), 64)
	return f
}

func (a *App) SaveSettings(m map[string]string) error {
	for k, v := range m {
		if _, ok := defaultSettings[k]; !ok {
			return fmt.Errorf("unknown setting %q", k)
		}
		if _, err := a.DB.Exec(`INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, k, v); err != nil {
			return err
		}
	}
	return nil
}

// ---- flats ---------------------------------------------------------------

type Flat struct {
	Code     string  `json:"code"`
	Owner    string  `json:"owner"`
	AreaSqft float64 `json:"area_sqft"`
	Phone1   string  `json:"phone1"`
	Phone2   string  `json:"phone2"`
	Email1   string  `json:"email1"`
	Email2   string  `json:"email2"`
	Notes    string  `json:"notes"`
	Balance  int64   `json:"balance"` // paise outstanding (negative = advance)
}

// FlatSortKey orders G floor first, then by floor and letter.
func FlatSortKey(code string) int {
	if code == "" {
		return 0
	}
	floor := 0
	if code[0] != 'G' {
		floor = int(code[0]-'0') + 1
	}
	return floor*100 + int(code[len(code)-1])
}

// Floor returns "G" or the digit for grouping.
func Floor(code string) string { return code[:1] }

func (a *App) Flats() ([]Flat, error) {
	rows, err := a.DB.Query(`
		SELECT f.code,f.owner,f.area_sqft,f.phone1,f.phone2,f.email1,f.email2,f.notes,
		       COALESCE((SELECT SUM(amount) FROM ledger l WHERE l.flat=f.code),0)
		FROM flats f ORDER BY f.sort_key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Flat
	for rows.Next() {
		var f Flat
		if err := rows.Scan(&f.Code, &f.Owner, &f.AreaSqft, &f.Phone1, &f.Phone2, &f.Email1, &f.Email2, &f.Notes, &f.Balance); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func (a *App) Flat(code string) (Flat, error) {
	fs, err := a.Flats()
	if err != nil {
		return Flat{}, err
	}
	for _, f := range fs {
		if f.Code == code {
			return f, nil
		}
	}
	return Flat{}, fmt.Errorf("flat %s not found", code)
}

func (a *App) UpsertFlat(f Flat) error {
	f.Code = strings.ToUpper(strings.TrimSpace(f.Code))
	if f.Code == "" || f.Owner == "" || f.AreaSqft <= 0 {
		return fmt.Errorf("code, owner and area are required")
	}
	_, err := a.DB.Exec(`
		INSERT INTO flats(code,owner,area_sqft,phone1,phone2,email1,email2,notes,sort_key) VALUES(?,?,?,?,?,?,?,?,?)
		ON CONFLICT(code) DO UPDATE SET owner=excluded.owner, area_sqft=excluded.area_sqft, phone1=excluded.phone1,
		  phone2=excluded.phone2, email1=excluded.email1, email2=excluded.email2, notes=excluded.notes`,
		f.Code, f.Owner, f.AreaSqft, f.Phone1, f.Phone2, f.Email1, f.Email2, f.Notes, FlatSortKey(f.Code))
	return err
}

func sortedKeys[V any](m map[string]V) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

// SQLitePath is where the SQLite database lives inside dataDir.
func SQLitePath(dataDir string) string { return filepath.Join(dataDir, "treasuredash.db") }

// renameLegacyDB moves a database created under the app's old name
// (assofin.db, with its WAL/SHM files) to treasuredash.db.
func renameLegacyDB(dataDir string) error {
	old := filepath.Join(dataDir, "assofin.db")
	if _, err := os.Stat(SQLitePath(dataDir)); err == nil {
		return nil
	}
	if _, err := os.Stat(old); err != nil {
		return nil
	}
	for _, sfx := range []string{"", "-wal", "-shm"} {
		if _, err := os.Stat(old + sfx); err == nil {
			if err := os.Rename(old+sfx, SQLitePath(dataDir)+sfx); err != nil {
				return err
			}
		}
	}
	return nil
}
