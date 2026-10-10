-- All money columns are integer paise.

CREATE TABLE IF NOT EXISTS settings (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS flats (
  code      TEXT PRIMARY KEY,
  owner     TEXT NOT NULL,
  area_sqft REAL NOT NULL,
  phone1    TEXT NOT NULL DEFAULT '',
  phone2    TEXT NOT NULL DEFAULT '',
  email1    TEXT NOT NULL DEFAULT '',
  email2    TEXT NOT NULL DEFAULT '',
  notes     TEXT NOT NULL DEFAULT '',
  sort_key  INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS periods (
  id            TEXT PRIMARY KEY,           -- Q3FY26
  fy            INTEGER NOT NULL,
  quarter       INTEGER NOT NULL,
  start_date    TEXT NOT NULL,
  end_date      TEXT NOT NULL,
  due_date      TEXT NOT NULL,
  rate_per_sqft REAL NOT NULL,              -- per month
  late_fee      INTEGER NOT NULL,           -- per month of delay
  auto_late_fee INTEGER NOT NULL DEFAULT 1, -- 0 for periods imported from the sheet
  status        TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open','closed')),
  invoiced_at   TEXT
);

-- Per-flat running account. Positive = owner owes, negative = owner paid/credited.
CREATE TABLE IF NOT EXISTS ledger (
  id          INTEGER PRIMARY KEY,
  flat        TEXT NOT NULL REFERENCES flats(code),
  date        TEXT NOT NULL,
  kind        TEXT NOT NULL CHECK (kind IN ('opening','invoice','late_fee','payment','adjustment')),
  period_id   TEXT REFERENCES periods(id),
  seq         INTEGER,                      -- late fee month number; 0 for invoices
  amount      INTEGER NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  mode        TEXT NOT NULL DEFAULT '',     -- bank / gpay_personal / cash / via_caretaker
  bank_txn_id INTEGER REFERENCES bank_txns(id) ON DELETE CASCADE,
  source      TEXT NOT NULL DEFAULT 'app',  -- app / import
  created_at  TEXT NOT NULL DEFAULT (datetime('now')),
  UNIQUE (flat, period_id, kind, seq)
);
CREATE INDEX IF NOT EXISTS ledger_flat_date ON ledger(flat, date);

CREATE TABLE IF NOT EXISTS bank_statements (
  id          INTEGER PRIMARY KEY,
  filename    TEXT NOT NULL,
  sha256      TEXT NOT NULL UNIQUE,
  account_no  TEXT NOT NULL DEFAULT '',
  from_date   TEXT,
  to_date     TEXT,
  opening     INTEGER,
  closing     INTEGER,
  uploaded_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS bank_txns (
  id           INTEGER PRIMARY KEY,
  statement_id INTEGER NOT NULL REFERENCES bank_statements(id),
  date         TEXT NOT NULL,
  value_date   TEXT NOT NULL DEFAULT '',
  narration    TEXT NOT NULL,
  ref_no       TEXT NOT NULL,
  debit        INTEGER NOT NULL DEFAULT 0,
  credit       INTEGER NOT NULL DEFAULT 0,
  balance      INTEGER NOT NULL,
  payer_key    TEXT NOT NULL DEFAULT '',
  payer_name   TEXT NOT NULL DEFAULT '',
  -- review workflow
  status       TEXT NOT NULL DEFAULT 'review' CHECK (status IN ('review','matched','ignored','historical')),
  kind         TEXT NOT NULL DEFAULT '' CHECK (kind IN ('','maintenance','other_income','expense','transfer')),
  flat         TEXT REFERENCES flats(code),
  payee        TEXT NOT NULL DEFAULT '',
  category     TEXT NOT NULL DEFAULT '',
  confidence   INTEGER NOT NULL DEFAULT 0,
  reasons      TEXT NOT NULL DEFAULT '[]',  -- JSON array of strings
  UNIQUE (date, ref_no, debit, credit, balance)
);

-- Learned identities of payers (UPI handle, source account, name) → flat or payee.
CREATE TABLE IF NOT EXISTS payer_aliases (
  key        TEXT PRIMARY KEY,
  flat       TEXT REFERENCES flats(code),
  payee      TEXT NOT NULL DEFAULT '',
  category   TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS categories (
  name     TEXT PRIMARY KEY,
  in_pnl   INTEGER NOT NULL DEFAULT 1,
  keywords TEXT NOT NULL DEFAULT '',        -- comma separated, matched against description
  priority INTEGER NOT NULL DEFAULT 100     -- lower wins
);

CREATE TABLE IF NOT EXISTS expenses (
  id          INTEGER PRIMARY KEY,
  date        TEXT NOT NULL,
  description TEXT NOT NULL,
  category    TEXT NOT NULL REFERENCES categories(name) ON UPDATE CASCADE,
  payee       TEXT NOT NULL DEFAULT '',
  amount      INTEGER NOT NULL,
  bank_txn_id INTEGER REFERENCES bank_txns(id) ON DELETE SET NULL,
  bill_file   TEXT NOT NULL DEFAULT '',
  notes       TEXT NOT NULL DEFAULT '',
  source      TEXT NOT NULL DEFAULT 'app',
  created_at  TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS expenses_date ON expenses(date);

CREATE TABLE IF NOT EXISTS other_income (
  id          INTEGER PRIMARY KEY,
  date        TEXT NOT NULL,
  category    TEXT NOT NULL,                -- Bank interest / Move-in charges / Other
  description TEXT NOT NULL DEFAULT '',
  amount      INTEGER NOT NULL,
  flat        TEXT REFERENCES flats(code),
  bank_txn_id INTEGER REFERENCES bank_txns(id) ON DELETE CASCADE,
  created_at  TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS reminders (
  id         INTEGER PRIMARY KEY,
  period_id  TEXT NOT NULL REFERENCES periods(id),
  seq        INTEGER NOT NULL,
  text       TEXT NOT NULL,
  flats      TEXT NOT NULL,                 -- JSON array
  status     TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft','sent')),
  created_at TEXT NOT NULL DEFAULT (datetime('now')),
  sent_at    TEXT
);

-- Fixed / recurring deposits: association money held outside the savings account.
CREATE TABLE IF NOT EXISTS deposits (
  id              INTEGER PRIMARY KEY,
  kind            TEXT NOT NULL CHECK (kind IN ('FD','RD')),
  bank            TEXT NOT NULL DEFAULT '',
  number          TEXT NOT NULL DEFAULT '',
  opened_on       TEXT NOT NULL,
  principal       INTEGER NOT NULL DEFAULT 0, -- FD amount
  installment     INTEGER NOT NULL DEFAULT 0, -- RD monthly instalment
  tenure_months   INTEGER NOT NULL DEFAULT 12,
  rate            REAL NOT NULL DEFAULT 0,    -- % p.a.
  maturity_date   TEXT NOT NULL DEFAULT '',
  maturity_amount INTEGER NOT NULL DEFAULT 0,
  closed_on       TEXT,                       -- matured / broken
  notes           TEXT NOT NULL DEFAULT ''
);

-- Entries removed as corrections (e.g. a payment the sheet recorded but that was
-- never received). The original row is kept so the change can be undone.
CREATE TABLE IF NOT EXISTS ledger_voids (
  id          INTEGER PRIMARY KEY,
  ledger_id   INTEGER NOT NULL,
  flat        TEXT NOT NULL,
  date        TEXT NOT NULL,
  kind        TEXT NOT NULL,
  period_id   TEXT,
  seq         INTEGER,
  amount      INTEGER NOT NULL,
  description TEXT NOT NULL,
  mode        TEXT NOT NULL,
  source      TEXT NOT NULL,
  reason      TEXT NOT NULL,
  voided_at   TEXT NOT NULL DEFAULT (datetime('now')),
  restored_at TEXT
);

-- Money moved between the savings account and a deposit, as booked by the
-- treasurer. Until the matching bank line is imported it adjusts the savings
-- balance; once matched (bank_txn_id) the statement already reflects it.
CREATE TABLE IF NOT EXISTS deposit_fundings (
  id          INTEGER PRIMARY KEY,
  deposit_id  INTEGER NOT NULL REFERENCES deposits(id) ON DELETE CASCADE,
  date        TEXT NOT NULL,
  kind        TEXT NOT NULL CHECK (kind IN ('open','installment','close')),
  amount      INTEGER NOT NULL,             -- + savings → deposit, − deposit → savings
  bank_txn_id INTEGER REFERENCES bank_txns(id) ON DELETE SET NULL,
  income_id   INTEGER REFERENCES other_income(id) ON DELETE SET NULL
);

-- Beneficiary details for vendors / staff the association pays.
CREATE TABLE IF NOT EXISTS payees (
  name         TEXT PRIMARY KEY,           -- as used in expenses.payee
  method       TEXT NOT NULL DEFAULT 'NEFT', -- NEFT / IMPS / UPI / BillPay / Cheque / Cash
  account_name TEXT NOT NULL DEFAULT '',
  account_no   TEXT NOT NULL DEFAULT '',
  ifsc         TEXT NOT NULL DEFAULT '',
  upi          TEXT NOT NULL DEFAULT '',
  reference    TEXT NOT NULL DEFAULT '',  -- e.g. Bescom consumer / account ID
  notes        TEXT NOT NULL DEFAULT ''
);
