package app

import (
	"database/sql"
	"fmt"
	"math/rand"
	"net/url"
	"os"
	"testing"
)

// testApp opens a fresh App for a test: SQLite in a temp dir by default, or,
// when TEST_PG_URL points at a PostgreSQL database (role allowed to create
// schemas), a throwaway schema in it, so the same tests cover both engines.
func testApp(t *testing.T) *App {
	t.Helper()
	base := os.Getenv("TEST_PG_URL")
	if base == "" {
		a, err := Open(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	schema := fmt.Sprintf("test_%d", rand.Int63())
	admin, err := sql.Open("pgx", base)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(`CREATE SCHEMA ` + schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		admin.Exec(`DROP SCHEMA ` + schema + ` CASCADE`)
		admin.Close()
	})
	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	a, err := OpenWith(t.TempDir(), u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.DB.Close() })
	return a
}
