# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

Treasure Dash: treasury app for an apartment owners' association (billing, HDFC statement reconciliation, payables, deposits, reminders, P&L, sinking fund). Go backend + Next.js static frontend, one binary / one distroless container. No login: production runs behind webauthn-proxy (Traefik forward-auth).

## Commands

```sh
task dev            # loads .env.local; starts local Postgres 14 (./.pg, :55432), fills an empty DB, API :8080 + Next dev :3000
task test           # backend tests on SQLite
task test:pg        # same tests on the local Postgres (TEST_PG_URL → throwaway schema per test)
task build          # next build → backend/internal/server/dist → ./bin/treasuredash
docker compose up --build   # production image + postgres:14

cd backend && go test ./internal/app -run TestOverlappingStatements -v       # single test
cd backend && TEST_PG_URL=postgres://postgres@localhost:55432/treasuredash go test ./internal/app -run X
cd web && npx next build    # frontend type-check
```

`DATABASE_URL` set → PostgreSQL (schema via `search_path`), unset → SQLite in `DATA_DIR`. `pdftotext` (poppler) must be on PATH. Flags: `-import FILE.xlsx`, `-copy-from-sqlite PATH/treasuredash.db [-force]`, `-bootstrap`. CI: `.github/workflows/image.yml` (tests on both engines, distroless image to `ghcr.io/ashishbhatiya18/treasure-dash`).

`backend/internal/server/dist` is embedded (`go:embed all:dist`, only `.gitkeep` is committed). The binary serves whatever was last copied there; `task build` refreshes it. `task dev` serves the frontend from Next dev instead.

## Architecture

- `backend/internal/db`: the **only** database-aware code. Opens SQLite (`modernc.org/sqlite`) or PostgreSQL (`pgx`), applies `schema_sqlite.sql` / `schema_pg.sql` (keep both in step: `TestSchemasInStep`), rewrites `?` placeholders for Postgres, and offers the dialect helpers (`Now`, `Days`, `GroupConcat`, `Like`, `Bool`, `InsertID`, `EnsureColumn`, `Snapshot`, `CopyAll`). Domain code writes one set of portable SQL and calls these for anything engine-specific.
- `backend/internal/app`: all domain logic on one `*App`. `OpenWith` runs: schema → `seedDefaults` → `migrateFYNaming` (SQLite only) → `migrateColumns` → `upgradeDefaultTexts` → `RegisterBookedDeposits`.
- `backend/internal/bank`: pure HDFC statement parser (`pdftotext -layout` output) and the credit matcher. No DB access.
- `backend/internal/server`: chi HTTP handlers (`/api/...`), cookie auth, and the static file handler for the embedded export.
- `main.go`: flags, cron (07:00 IST late fees + reminder draft, 02:00 IST `VACUUM INTO` backups).
- `web/`: Next.js 16 App Router (see `web/AGENTS.md`: APIs differ from older Next; check `node_modules/next/dist/docs/` before using framework features) with `output: "export"` (static). Every page is a client component that fetches `/api`. No dynamic routes, so detail pages use query params (`/flats/view?code=3B`). TS types in `web/lib/api.ts` mirror the Go JSON structs by hand; update both sides together.

### Invariants that span files

- **Money is integer paise everywhere** (DB, Go, JSON). The UI formats with `money()` / `moneyR()`. Request bodies that take rupees use `*_rupees` fields converted with `app.Paise`.
- **Portable SQL only** in `internal/app`: `?` placeholders, `ON CONFLICT … DO NOTHING` (not `INSERT OR IGNORE`), `a.DB.InsertID` (no `LastInsertId`), `a.DB.Now()/Days()/GroupConcat()/Like()`, `db.Bool(b)` for INTEGER flag columns, an alias on every subquery in FROM, aggregates (not aliases) in HAVING, and tie-breakers on ORDER BY counts. Postgres text uses `COLLATE "C"` so ordering matches SQLite. Run `task test:pg` after SQL changes.
- **SQLite has one connection** (`SetMaxOpenConns(1)`). Never call another `a.DB` query while a `tx` or an open `rows` is in use; read settings etc. before `Begin()`, and close rows before issuing more queries. Violations deadlock on SQLite (Postgres has a pool, so they'd pass there and still break locally).
- **Quarter naming = Indian fiscal year, named by the year it ends.** Oct–Dec 2026 is `Q3FY27`; `Quarter.FY` stores the *starting* calendar year. Always go through `ParseQuarter` / `Quarter.ID()` / `FYLabel()` in `period.go`. Never slice period-ID strings. The treasury spreadsheet labels years by their *start*; `importxlsx.go` pins each sheet column block to explicit months.
- **Per-flat ledger** (`ledger` table): + = owed (opening, invoice, late_fee, adjustment), − = credit (payment, waiver). Balance = sum. Invoices/late fees are unique per `(flat, period_id, kind, seq)`.
- **FIFO allocation** (`allocation.go`) is the single source of truth for "which payment settled which charge": invoice status, receipts "applied to", dashboard receivables and P&L *actual* income (`recognitions`). Payments clear the oldest charges first; an advance is recognised when the charge it pays falls due. Don't compute settlement anywhere else.
- **Late fees** (`ApplyLateFees`) are recomputed idempotently from the ledger: ₹/month (or part) after the due date, per unpaid quarter. Only periods with `auto_late_fee=1` (created in-app); quarters imported from the sheet carry the sheet's penalties instead. Call it after anything that changes payments or due dates.
- **Cut-over date** (setting, default 2026-10-01): bank transactions before it are matched (to learn payer aliases) but not posted, because the imported sheet balances already include them (`status='historical'`).
- **Bank matching** (`bank.MatchCredit` + `App.autoMatch`): scores flats by learned payer alias, flat code in narration, owner name, amount due. Auto-posts only clear winners; the rest go to `status='review'`. FD/RD narrations become `kind='transfer'`.
- **P&L**: income has receivable (billed) vs actual (received); surplus and the sinking fund use actuals. Sinking fund = `sinking_fund_opening` (back-calculated from the bank via `CalibrateSinkingFund`) + each quarter's surplus. FY figures come from `fundYears()` (closed-quarter and to-date ratios).
- **Total funds = savings + FD/RD.** Deposits booked as "from savings" create `deposit_fundings` that reduce the savings balance (`PendingDepositTransfers`) until `MatchFundings` links the real bank line. "FD BOOKED - <no>" statement lines auto-create register entries; FD/RD transfers not linked to the register still count via `UnregisteredDepositTransfers` (inside `DepositsValueOn`). Use these helpers wherever a savings or deposit balance is shown.
- **Corrections are reversible**: wrong sheet payments are voided into `ledger_voids` (restorable), not deleted. Bank-linked ledger rows are managed only through bank assignment.
- **PDFs** use `go-pdf/fpdf` core fonts (Latin-1: write "Rs." not "₹"). `dashpdf.go` and `paymentrequest.go` are A4 landscape with `AutoPageBreak` off and explicit layout so each block fits its page; invoices / P&L statement in `pdf.go` are portrait.

## Data and privacy

The GitHub repo is **public**. `data/` (treasury workbook, HDFC statements, `sample_flats.json` test fixture), `data-store/`, `.pg/` and `.env.local` are git-ignored and must stay out of commits and the Docker context: they hold residents' names, emails and bank details. Tests that need them skip when absent. Association-specific values (name, address, bank account, treasurer contact) live in the database via Settings; code defaults are placeholders. Never hardcode residents', vendors' or the association's identifying details in code, tests or docs.
