// Command assofin runs the apartment association treasury app: JSON API,
// embedded static frontend and the background scheduler in one process.
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/robfig/cron/v3"

	"assofin/internal/app"
	"assofin/internal/db"
	"assofin/internal/server"
)

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func main() {
	dataDir := flag.String("data", env("DATA_DIR", "./data-store"), "directory for the SQLite database and uploaded bills")
	addr := flag.String("addr", env("ADDR", ":8080"), "listen address")
	dbURL := flag.String("database-url", env("DATABASE_URL", ""), "PostgreSQL URL (empty = SQLite in -data)")
	importXLSX := flag.String("import", "", "import a treasury workbook (.xlsx) into an empty database and exit")
	copyFrom := flag.String("copy-from-sqlite", "", "copy every table from this SQLite file into -database-url and exit")
	force := flag.Bool("force", false, "with -copy-from-sqlite: replace data already in PostgreSQL")
	health := flag.Bool("healthcheck", false, "exit 0 if the server on -addr answers (for container health checks; the image has no shell or curl)")
	bootstrap := flag.Bool("bootstrap", false, "fill an empty database and exit: copy -data/assofin.db into PostgreSQL if present, else import -import")
	flag.Parse()

	if *health {
		port := *addr
		if strings.HasPrefix(port, ":") {
			port = "127.0.0.1" + port
		}
		c := http.Client{Timeout: 3 * time.Second}
		res, err := c.Get("http://" + port + "/api/settings")
		if err != nil || res.StatusCode != http.StatusOK {
			os.Exit(1)
		}
		return
	}

	if *bootstrap {
		if err := bootstrapDB(*dataDir, *dbURL, *importXLSX); err != nil {
			log.Fatal(err)
		}
		return
	}

	if *copyFrom != "" {
		if err := copySQLite(*copyFrom, *dataDir, *dbURL, *force); err != nil {
			log.Fatal(err)
		}
		return
	}

	a, err := app.OpenWith(*dataDir, *dbURL)
	if err != nil {
		log.Fatal(err)
	}

	if *importXLSX != "" {
		f, err := os.Open(*importXLSX)
		if err != nil {
			log.Fatal(err)
		}
		rep, err := a.ImportWorkbook(f)
		f.Close()
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("Imported %d flats, %d periods, %d ledger entries, %d expenses (%d dates corrected)\n",
			rep.Flats, rep.Periods, rep.LedgerEntries, rep.Expenses, rep.DatesCorrected)
		for _, w := range rep.Warnings {
			fmt.Println("  warning:", w)
		}
		return
	}

	ist := time.FixedZone("IST", 5*3600+1800)
	c := cron.New(cron.WithLocation(ist))
	// 07:00 IST daily: post late fees that fell due, then prepare a reminder draft if one is due.
	c.AddFunc("0 7 * * *", func() {
		if n, err := a.ApplyLateFees(app.Today()); err != nil {
			log.Printf("late fees: %v", err)
		} else if n > 0 {
			log.Printf("late fees: %d entries changed", n)
		}
		if msg, err := a.AutoReminder(); err != nil {
			log.Printf("auto reminder: %v", err)
		} else {
			log.Printf("auto reminder: %s", msg)
		}
	})
	// 02:00 IST daily: keep the last 14 nightly snapshots of the database.
	c.AddFunc("0 2 * * *", func() {
		dir := filepath.Join(*dataDir, "backups")
		os.MkdirAll(dir, 0o755)
		name := filepath.Join(dir, "assofin-"+app.Today().Format(app.DateFmt)+".db")
		os.Remove(name)
		if err := a.DB.Snapshot(name); err != nil {
			log.Printf("backup: %v", err)
		}
		if old, _ := filepath.Glob(filepath.Join(dir, "assofin-*.db")); len(old) > 14 {
			for _, f := range old[:len(old)-14] {
				os.Remove(f)
			}
		}
	})
	c.Start()
	if _, err := a.ApplyLateFees(app.Today()); err != nil {
		log.Printf("late fees: %v", err)
	}

	srv := &http.Server{Addr: *addr, Handler: server.New(a).Routes(), ReadHeaderTimeout: 10 * time.Second}
	store := "SQLite in " + *dataDir
	if a.DB.PG {
		store = "PostgreSQL (bills in " + *dataDir + ")"
	}
	log.Printf("Treasure Dash listening on %s, %s", *addr, store)
	log.Fatal(srv.ListenAndServe())
}

// copySQLite moves an existing SQLite database into PostgreSQL: the source is
// opened through the app first so it carries every migration.
func copySQLite(file, dataDir, url string, force bool) error {
	if url == "" {
		return fmt.Errorf("-copy-from-sqlite needs -database-url / DATABASE_URL")
	}
	if filepath.Base(file) != "assofin.db" {
		return fmt.Errorf("expected the app's assofin.db file, got %s", file)
	}
	src, err := app.Open(filepath.Dir(file))
	if err != nil {
		return fmt.Errorf("open source: %w", err)
	}
	dst, err := app.OpenWith(dataDir, url)
	if err != nil {
		return fmt.Errorf("open destination: %w", err)
	}
	if has, err := dst.DB.HasUserData(); err != nil {
		return err
	} else if has && !force {
		return fmt.Errorf("PostgreSQL already has data; re-run with -force to replace it")
	}
	counts, err := db.CopyAll(src.DB, dst.DB)
	if err != nil {
		return err
	}
	for _, t := range db.Tables {
		fmt.Printf("%-18s %d rows\n", t, counts[t])
	}
	return nil
}

// bootstrapDB fills an empty database for local development: an existing
// SQLite file is copied into PostgreSQL, otherwise the workbook is imported.
// A database that already has data is left alone.
func bootstrapDB(dataDir, url, workbook string) error {
	a, err := app.OpenWith(dataDir, url)
	if err != nil {
		return err
	}
	if has, err := a.DB.HasUserData(); err != nil || has {
		if err == nil {
			fmt.Println("database already has data")
		}
		return err
	}
	lite := filepath.Join(dataDir, "assofin.db")
	if _, err := os.Stat(lite); err == nil && a.DB.PG {
		a.DB.Close()
		fmt.Println("copying", lite, "into PostgreSQL")
		return copySQLite(lite, dataDir, url, false)
	}
	if workbook == "" {
		fmt.Println("empty database and no workbook: import one from Settings & import")
		return nil
	}
	f, err := os.Open(workbook)
	if err != nil {
		return err
	}
	defer f.Close()
	rep, err := a.ImportWorkbook(f)
	if err != nil {
		return err
	}
	fmt.Printf("Imported %d flats, %d periods, %d ledger entries, %d expenses\n", rep.Flats, rep.Periods, rep.LedgerEntries, rep.Expenses)
	return nil
}
