# Treasure Dash

A treasurer's dashboard for an apartment owners' association: quarterly maintenance billing,
bank-statement reconciliation, payables, deposits, reminders and a running P&L with a sinking fund.
One distroless container serves the UI, the API and the scheduler.

| Need | Where |
|---|---|
| Raise quarterly maintenance invoices (area × rate × 3), download PDFs / ZIP | **Billing & invoices** |
| Import HDFC statement PDFs, auto-map payments to flats, review the rest | **Bank reconciliation** |
| Every receipt and the invoices it settled | **Receipts register** |
| FDs / RDs (auto-registered from "FD BOOKED" statement lines) | **FD / RD deposits** |
| Day 0 payment request, WhatsApp-style follow-ups, payment instructions for payables | **Reminders** |
| Hard-copy bills with autofill from past bills (optional AI scan) | **Expenses & income** |
| Running and closed quarterly P&L, sinking-fund growth | **Dashboard**, **P&L & sinking fund** |

There is **no login** in the app: deploy it behind an authenticating proxy (here, webauthn-proxy
via Traefik forward-auth).

## Rules encoded
- Indian fiscal year, named by the year it ends: Apr 2026 – Mar 2027 is FY27, so Oct–Dec 2026 is **Q3FY27**.
  A treasury sheet that labels years by their start is mapped on import.
- Due date = last day of the quarter's 2nd month (editable per quarter).
- Late fee per month (or part) of delay, per unpaid quarter; recalculated when back-dated payments arrive.
- Payments clear the oldest dues first. Income is shown as receivable (billed) and actual (received);
  surplus and the sinking fund use actuals.
- Total funds = savings balance + FD/RD. Transfers into deposits are neither income nor expense.

## Bank matching
Each credit is scored by a remembered payer (UPI handle / account, learned when you confirm), a flat number
in the remarks (`1B MAINTENANCE`, `FLAT3E`, `NAME8D`, `FLATGF…`), the owner's name, and whether the amount equals
a flat's dues. Clear winners are posted automatically; the rest wait in **Needs review**. Debits are mapped to
payees and linked to a recorded bill of the same amount when one exists.

Statements may overlap or be uploaded twice: each line is unique on date, reference, amounts and running
balance, so repeats are skipped, and every statement is checked to reconcile (opening + credits − debits =
closing, no gaps between statements). Lines dated before the **cut-over date** are already inside imported
opening balances, so they are only used to learn payers.

## Run locally ([Task](https://taskfile.dev))
```sh
cp .env.local.example .env.local   # or write your own (git-ignored)
task dev        # starts local Postgres 14 (./.pg), fills an empty DB, API :8080 + web :3000
task test       # backend tests on SQLite
task test:pg    # the same tests on Postgres (throwaway schema per test)
```
`DATABASE_URL` set → PostgreSQL; empty → SQLite in `DATA_DIR`. Uploaded bills always live in `DATA_DIR`.
`ANTHROPIC_API_KEY` (optional) reads bill photos; extracted fields are always confirmed before saving.

Container with Postgres: `docker compose up --build` → http://localhost:8080.

## Deploy
`.github/workflows/image.yml` tests on SQLite and PostgreSQL 14, builds the distroless image, smoke-tests
it and pushes `ghcr.io/ashishbhatiya18/treasure-dash` (`:latest`, `:<sha>`) on `main`. The image expects
`DATABASE_URL` (schema in `search_path`) and a writable volume at `/data`.

## Layout
```
backend/
  internal/db      the only database-aware code: SQLite/PostgreSQL connection, schemas, dialect helpers
  internal/app     domain: billing, ledger, allocation, bank import, deposits, payables, reports, PDFs
  internal/bank    HDFC statement parser + payment matcher (pure)
  internal/server  HTTP API + embedded static frontend
web/      Next.js (static export) + Tailwind
```
