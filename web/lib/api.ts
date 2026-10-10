// Thin client for the Go API. All money from the API is integer paise.

export class ApiError extends Error {
  constructor(public status: number, message: string) {
    super(message);
  }
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const init: RequestInit = { method, credentials: "same-origin", headers: {} };
  if (body instanceof FormData) {
    init.body = body;
  } else if (body !== undefined) {
    init.body = JSON.stringify(body);
    (init.headers as Record<string, string>)["Content-Type"] = "application/json";
  }
  const res = await fetch(`/api${path}`, init);
  const data = await res.json().catch(() => ({}));
  if (!res.ok) {
    throw new ApiError(res.status, data.error ?? res.statusText);
  }
  return data as T;
}

export const api = {
  get: <T,>(p: string) => request<T>("GET", p),
  post: <T,>(p: string, b?: unknown) => request<T>("POST", p, b ?? {}),
  put: <T,>(p: string, b?: unknown) => request<T>("PUT", p, b ?? {}),
  del: <T,>(p: string) => request<T>("DELETE", p),
  upload: <T,>(p: string, file: File, field = "file") => {
    const fd = new FormData();
    fd.append(field, file);
    return request<T>("POST", p, fd);
  },
};

const inr = new Intl.NumberFormat("en-IN", { maximumFractionDigits: 2, minimumFractionDigits: 0 });

/** ₹1,23,456 from paise. */
export function money(paise: number | null | undefined, opts: { sign?: boolean } = {}): string {
  const p = paise ?? 0;
  const s = "₹" + inr.format(Math.abs(p) / 100);
  if (p < 0) return "−" + s;
  return opts.sign && p > 0 ? "+" + s : s;
}

/** Whole-rupee amount for headline tiles: ₹18,39,774. */
export function moneyR(paise: number | null | undefined): string {
  return money(Math.round((paise ?? 0) / 100) * 100);
}

export function fmtDate(iso?: string | null): string {
  if (!iso) return "—";
  const d = new Date(iso.length === 10 ? iso + "T00:00:00" : iso.replace(" ", "T") + "Z");
  if (isNaN(d.getTime())) return iso;
  return d.toLocaleDateString("en-IN", { day: "2-digit", month: "short", year: "numeric" });
}

export function today(): string {
  const d = new Date();
  const off = d.getTimezoneOffset();
  return new Date(d.getTime() - off * 60000).toISOString().slice(0, 10);
}

// ---- types mirrored from the Go structs -------------------------------------------

export type Flat = {
  code: string; owner: string; area_sqft: number;
  phone1: string; phone2: string; email1: string; email2: string; notes: string;
  balance: number;
};

export type LedgerEntry = {
  id: number; flat: string; date: string;
  kind: "opening" | "invoice" | "late_fee" | "payment" | "adjustment";
  period_id: string | null; seq: number | null; amount: number; description: string;
  mode: string; bank_txn_id: number | null; source: string; balance: number;
};

export type Period = {
  id: string; fy: number; quarter: number; start_date: string; end_date: string; due_date: string;
  rate_per_sqft: number; late_fee: number; auto_late_fee: boolean; status: "open" | "closed";
  invoiced_at: string | null; billed: number; collected: number; outstanding: number; unpaid_flats: number;
};

export type FlatPeriodStatus = {
  flat: string; owner: string; invoice: number; outstanding: number; late_fees: number; balance: number; invoiced: boolean;
};

export type BankTxn = {
  id: number; statement_id: number; date: string; narration: string; ref_no: string;
  debit: number; credit: number; balance: number; payer_key: string; payer_name: string;
  status: "review" | "matched" | "ignored" | "historical";
  kind: "" | "maintenance" | "other_income" | "expense" | "transfer";
  flat: string | null; payee: string; category: string; confidence: number; reasons: string[]; expense_ids: number[];
  postings: Posting[];
};

export type Application = { charge_id: number; label: string; period_id: string; amount: number };
export type Posting = { flat: string; amount: number; applied: Application[]; unapplied: number };

export type Settlement = {
  credit_id: number; credit_kind: "payment" | "adjustment" | "opening"; credit_date: string; date: string; amount: number;
  mode: string; description: string; bank_txn_id: number | null; bank_ref: string;
};

export type ChargeView = LedgerEntry & {
  label: string; due_date: string; settled: number; outstanding: number;
  status: "paid" | "partial" | "unpaid" | "overdue"; days_overdue: number; settlements: Settlement[];
};

export type CreditView = LedgerEntry & { label: string; applied: Application[]; unapplied: number; bank_ref: string; bank_narration: string };

export type AccountSummary = { charged: number; paid: number; waived: number; outstanding: number; advance: number; balance: number; overdue: number };

export type FlatAccount = {
  flat: Flat; summary: AccountSummary; charges: ChargeView[]; credits: CreditView[];
  pre_cutover_receipts: BankTxn[]; cutover_date: string; voids: LedgerVoid[];
};

export type LedgerVoid = {
  id: number; ledger_id: number; flat: string; date: string; kind: string; amount: number; description: string;
  reason: string; voided_at: string; restored_at: string | null;
};

export type Receipt = CreditView & { flat: string; owner: string };

export type Statement = {
  id: number; filename: string; account_no: string; from_date: string; to_date: string;
  opening: number; closing: number; uploaded_at: string; txns: number; new: number; review: number;
  credits: number; debits: number; credits_resolved: number; debits_resolved: number; balanced: boolean; issues: string[];
};

export type Category = { name: string; in_pnl: boolean; keywords: string; priority: number };

export type Expense = {
  id: number; date: string; description: string; category: string; payee: string; amount: number;
  bank_txn_id: number | null; bill_file: string; notes: string; source: string;
};

export type OtherIncome = {
  id: number; date: string; category: string; description: string; amount: number; flat: string | null; bank_txn_id: number | null;
};

export type Line = { label: string; amount: number; count?: number };

export type IncomeLine = { label: string; receivable: number; actual: number; no_receivable?: boolean };

export type NonPnLItem = { id: number; date: string; description: string; category: string; payee: string; amount: number };

export type PnL = {
  period_id: string; from: string; to: string; running: boolean;
  income: IncomeLine[]; total_receivable: number; total_income: number; // total_income = actuals
  expenses: Line[]; total_expenses: number;
  surplus: number; surplus_if_collected: number;
  collections: number; receivables: number; non_pnl_outflow: number; non_pnl_items: NonPnLItem[];
  fund_opening: number; fund_closing: number; fund_current: number; fund_as_of: string;
  bank_balance: number; bank_balance_on: string; has_bank_balance: boolean;
  bank_current: number; bank_current_on: string; advances_held: number; unallocated_reserve: number;
  deposits_balance: number; total_funds: number; pending_transfers: number;
};

export type FundQuarter = { period_id: string; income: number; expenses: number; surplus: number; running: boolean; balance: number };
export type FundYear = {
  fy: string; income: number; surplus: number; percent: number; complete: boolean;
  completed_quarters: number; completed_income: number; completed_surplus: number; completed_percent: number;
};
export type SinkingFund = { opening: number; opening_note: string; balance: number; projected: number; quarters: FundQuarter[]; years: FundYear[]; current: FundYear };

export type Reminder = {
  id: number; period_id: string; seq: number; text: string; flats: string[];
  status: "draft" | "sent"; created_at: string; sent_at: string | null;
};

export type CashWalk = {
  has_data: boolean; opening: number; opening_on: string; receipts: number; payments: number; closing: number; closing_on: string;
  receipts_maintenance: number; receipts_other: number; receipts_unassigned: number; payments_unassigned: number;
  deposits_opening: number; deposits_closing: number; total_opening: number; total_closing: number;
  to_deposits: number; from_deposits: number; payments_operating: number; deposits: Deposit[];
  booked_to_deposits: number; pending_closing: number;
};

export type Deposit = {
  id: number; kind: "FD" | "RD"; bank: string; number: string; opened_on: string; principal: number; installment: number;
  tenure_months: number; rate: number; maturity_date: string; maturity_amount: number; closed_on: string | null; notes: string;
  value: number; active: boolean; label: string;
  from_savings: boolean; to_savings: boolean; closing_amount: number; fundings: Funding[];
};

export type Funding = {
  id: number; deposit_id: number; date: string; kind: "open" | "installment" | "close"; amount: number;
  bank_txn_id: number | null; bank_ref: string; bank_date: string;
};
export type ReceivableRow = { flat: string; owner: string; current: number; arrears: number; late_fees: number; total: number; days_overdue: number };
export type PayableRow = { payee: string; billed: number; paid: number; balance: number; last_bill: string; paid_qtr: number };

export type Dashboard = {
  today: string; as_of: string; running: boolean; today_period: string; prev_period: string; next_period: string; first_period: string;
  current_period: string; period: Period | null; has_data: boolean; needs_invoicing: boolean;
  review_count: number; draft_reminder: Reminder | null;
  pnl: PnL; cash: CashWalk; sinking: SinkingFund;
  receivables: { total: number; current: number; arrears: number; late_fees: number; overdue: number; flats: ReceivableRow[] };
  payables: { since: string; total: number; prepaid_total: number; paid_qtr: number; payees: PayableRow[] };
  advances: { total: number; flats: { flat: string; owner: string; amount: number }[] };
  collections: { count: number; amount: number; off_bank: number; flats_paid: number; flats_billed: number; recent: Receipt[] };
};

export type ImportReport = {
  flats: number; periods: number; ledger_entries: number; expenses: number; dates_corrected: number;
  warnings: string[] | null; outstanding_flats: string[] | null;
};

export const PAYMENT_MODES: Record<string, string> = {
  bank: "Bank transfer", gpay_personal: "GPay/PhonePe to treasurer", cash: "Cash", via_caretaker: "Via caretaker", cheque: "Cheque",
};

export type PaymentRequestRow = {
  flat: string; owner: string; quarterly: number; prev_due: number; penalties: number; adjustments: number; payment: number; paid: number; due: number;
};
export type PaymentRequest = {
  period_id: string; prev_period_id: string; as_of: string; subject: string; body: string; emails: string[]; missing_email: string[];
  rows: PaymentRequestRow[]; total: PaymentRequestRow; sent: { id: number; subject: string; text: string; sent_at: string }[];
};

// ---- bulk edit -------------------------------------------------------------------

export type SheetColumn = {
  key: string; header: string; type: "text" | "code" | "date" | "money" | "number" | "enum" | "bool";
  read_only?: boolean; required?: boolean; options?: string[]; width?: number; help?: string;
};
export type SheetRow = { values: Record<string, string>; _v: string; locked?: string };
export type SheetData = {
  name: string; title: string; help: string; key: string; auto_key: boolean; can_add: boolean; can_delete: boolean;
  columns: SheetColumn[]; rows: SheetRow[];
};
export type InRow = { row: number; values: Record<string, string>; _v?: string; delete?: boolean };
export type Changeset = Record<string, InRow[]>;
export type DataChange = {
  sheet: string; row: number; action: "add" | "update" | "delete"; key: string; label: string;
  fields?: { column: string; from: string; to: string }[]; error?: string; warnings?: string[];
};
export type PlanResult = { changes: DataChange[]; errors: number; unchanged: number; gone: number };
export type ApplyResult = PlanResult & { applied: number; backup: string };

export type BillTemplate = {
  description: string; payee: string; category: string; last_amount: number; typical: boolean; count: number; last_date: string;
};

export type Payee = {
  name: string; method: string; account_name: string; account_no: string; ifsc: string; upi: string; reference: string; notes: string;
};
export type PayableItem = { expense_id: number; date: string; description: string; category: string; amount: number; outstanding: number };
export type PayeePayable = { payee: Payee; complete: boolean; billed: number; paid: number; balance: number; items: PayableItem[] };
export type Payables = {
  as_of: string; since: string; payees: PayeePayable[]; total: number;
  account: { name: string; no: string; ifsc: string; bank: string };
  sent: { id: number; subject: string; text: string; sent_at: string }[];
};
