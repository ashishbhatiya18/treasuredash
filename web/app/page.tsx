"use client";
import Link from "next/link";
import { useEffect, useState } from "react";
import { api, Dashboard, fmtDate, money, moneyR, PAYMENT_MODES } from "@/lib/api";
import { useLoad } from "@/lib/hooks";
import { Badge, Card, ErrorBox, LinkButton, Loading, Notice, PageHeader, Stat } from "@/components/ui";
import { FundBalanceChart, SurplusBars } from "@/components/FundCharts";

export default function DashboardPage() {
  const [period, setPeriod] = useState<string | null>(null);
  useEffect(() => {
    setPeriod(new URLSearchParams(window.location.search).get("period") ?? "");
  }, []);
  const { data: d, error } = useLoad(
    () => (period === null ? Promise.resolve(null) : api.get<Dashboard>(`/dashboard?period=${period}`)),
    [period],
  );
  const go = (id: string) => {
    setPeriod(id);
    history.replaceState(null, "", id ? `/?period=${id}` : "/");
  };
  if (error) return <ErrorBox error={error} />;
  if (!d) return <Loading />;

  if (!d.has_data) {
    return (
      <>
        <PageHeader title="Welcome" />
        <Notice>
          No data yet. Go to <Link className="font-medium underline" href="/settings">Settings &amp; import</Link> and upload the treasury workbook
          (your treasury .xlsx) to load flats, maintenance history and expenses.
        </Notice>
      </>
    );
  }

  const p = d.pnl;
  const maint = p.income[0];
  const collectedPct = maint && maint.receivable > 0 ? Math.round((maint.actual / maint.receivable) * 100) : 0;
  const sf = d.sinking;
  const c = d.cash;

  return (
    <>
      <PageHeader
        title={d.running ? `${d.current_period} · running position` : `${d.current_period} · position at quarter end`}
        sub={
          <>
            {fmtDate(p.from)} – {fmtDate(p.to)} · due {d.period ? fmtDate(d.period.due_date) : "—"} · figures as of {fmtDate(d.as_of)}
            {c.has_data && c.closing_on !== d.as_of && <> · bank statements up to {fmtDate(c.closing_on)}</>}
          </>
        }
        actions={
          <>
            <div className="flex items-center rounded-md border border-line bg-white">
              <button
                className="px-3 py-2 text-sm text-ink hover:bg-neutral-50 disabled:opacity-40"
                disabled={!!d.first_period && d.current_period === d.first_period}
                onClick={() => go(d.prev_period)}
                aria-label="Previous quarter"
              >
                ← {d.prev_period}
              </button>
              <span className="border-x border-line px-3 py-2 text-sm font-semibold text-ink">{d.current_period}</span>
              <button
                className="px-3 py-2 text-sm text-ink hover:bg-neutral-50 disabled:opacity-40"
                disabled={!d.next_period}
                onClick={() => go(d.next_period === d.today_period ? "" : d.next_period)}
                aria-label="Next quarter"
              >
                {d.next_period ? `${d.next_period} →` : "Next →"}
              </button>
            </div>
            {!d.running && <button className="text-sm text-ink-soft underline" onClick={() => go("")}>Back to {d.today_period}</button>}
            <LinkButton href={`/api/dashboard.pdf?period=${d.current_period}`}>Download P&amp;L</LinkButton>
          </>
        }
      />

      {d.needs_invoicing && (
        <Notice tone="warn">
          Invoices for {d.current_period} have not been raised yet. <Link className="font-medium underline" href="/billing">Raise them →</Link>
        </Notice>
      )}
      {d.review_count > 0 && (
        <Notice tone="warn">
          {d.review_count} bank transaction{d.review_count > 1 ? "s" : ""} waiting for a flat or payee — receivables and payables below exclude them.{" "}
          <Link className="font-medium underline" href="/bank?status=review">Review →</Link>
        </Notice>
      )}

      {/* headline numbers */}
      <div className="grid grid-cols-2 gap-3 md:grid-cols-4">
        <Stat label="Receivable (billed)" value={moneyR(p.total_receivable)} hint={`${d.current_period} charges + other income`} />
        <Stat label="Actual received" value={moneyR(p.total_income)} tone="good" hint={`${collectedPct}% of maintenance · ${d.collections.flats_paid}/${d.collections.flats_billed} flats paid`} />
        <Stat label="Outstanding receivables" value={moneyR(d.receivables.total)} tone={d.receivables.total ? "warn" : "good"} hint={`${d.receivables.flats.length} flats · ${money(d.receivables.overdue)} overdue`} />
        <Stat label="Advances held" value={moneyR(d.advances.total)} hint={`${d.advances.flats.length} flats paid ahead`} />
        <Stat label="Payables" value={moneyR(d.payables.total)} tone={d.payables.total ? "warn" : undefined} hint={`bills not yet paid from bank`} />
        <Stat label="Payments made" value={moneyR(c.payments_operating)} hint={c.to_deposits ? `+ ${money(c.to_deposits)} moved to FD/RD` : "bank debits in the quarter"} />
        <Stat label="Total funds" value={moneyR(c.total_closing)} hint={c.has_data ? `savings ${moneyR(c.closing)} + FD/RD ${moneyR(c.deposits_closing)}` : "no statement yet"} />
        <Stat
          label="Sinking fund"
          value={moneyR(sf.projected)}
          tone="good"
          hint={
            sf.current.completed_quarters > 0 && !sf.current.complete && sf.current.completed_surplus !== sf.current.surplus
              ? `${sf.current.fy}: ${sf.current.completed_percent.toFixed(1)}% (closed qtrs) · ${sf.current.percent.toFixed(1)}% to date`
              : `${sf.current.fy}: ${sf.current.percent.toFixed(1)}% of income`
          }
        />
      </div>

      {/* P&L + cash walk */}
      <div className="mt-4 grid gap-4 lg:grid-cols-5">
        <Card className="lg:col-span-3" title={`Running P&L · ${d.current_period}`} actions={<Link className="text-xs font-medium text-ink-soft hover:underline" href="/reports">Full statement →</Link>}>
          <div className="grid gap-6 md:grid-cols-2">
            <div>
              <table className="w-full text-sm">
                <thead>
                  <tr className="text-xs uppercase tracking-wide text-neutral-500">
                    <th className="pb-1 text-left font-semibold">Income</th>
                    <th className="pb-1 pl-3 text-right font-semibold">Receivable</th>
                    <th className="pb-1 pl-3 text-right font-semibold">Actual</th>
                  </tr>
                </thead>
                <tbody>
                  {p.income.map((l) => (
                    <tr key={l.label} className="border-b border-line/60">
                      <td className="py-1.5 pr-2">{l.label}</td>
                      <td className="num whitespace-nowrap py-1.5 pl-3 text-right text-neutral-500">{l.no_receivable ? "—" : money(l.receivable)}</td>
                      <td className="num whitespace-nowrap py-1.5 pl-3 text-right">{money(l.actual)}</td>
                    </tr>
                  ))}
                  <tr className="font-semibold">
                    <td className="pt-2">Total</td>
                    <td className="num whitespace-nowrap pt-2 pl-3 text-right text-neutral-500">{money(p.total_receivable)}</td>
                    <td className="num whitespace-nowrap pt-2 pl-3 text-right">{money(p.total_income)}</td>
                  </tr>
                </tbody>
              </table>
            </div>
            <div>
              <div className="mb-1 text-xs font-semibold uppercase tracking-wide text-neutral-500">Expenses (bills)</div>
              <table className="w-full text-sm">
                <tbody>
                  {p.expenses.slice(0, 6).map((l) => (
                    <tr key={l.label} className="border-b border-line/60">
                      <td className="py-1.5 pr-2">{l.label}</td>
                      <td className="num py-1.5 text-right">{money(l.amount)}</td>
                    </tr>
                  ))}
                  {p.expenses.length > 6 && (
                    <tr className="border-b border-line/60">
                      <td className="py-1.5 text-neutral-500">{p.expenses.length - 6} more</td>
                      <td className="num py-1.5 text-right">{money(p.expenses.slice(6).reduce((s, l) => s + l.amount, 0))}</td>
                    </tr>
                  )}
                  <tr className="font-semibold">
                    <td className="pt-2">Total</td>
                    <td className="num pt-2 text-right">{money(p.total_expenses)}</td>
                  </tr>
                </tbody>
              </table>
            </div>
          </div>
          <div className="mt-4 grid gap-3 sm:grid-cols-2">
            <div className={`flex items-center justify-between rounded-md px-4 py-3 ${p.surplus >= 0 ? "bg-green-50" : "bg-red-50"}`}>
              <span className="text-sm font-semibold">{p.surplus >= 0 ? "Surplus to date (actual)" : "Deficit to date (actual)"}</span>
              <span className={`text-lg font-semibold ${p.surplus >= 0 ? "text-good" : "text-bad"}`}>{money(p.surplus)}</span>
            </div>
            <div className="flex items-center justify-between rounded-md bg-neutral-100 px-4 py-3">
              <span className="text-sm font-semibold">If all dues are collected</span>
              <span className="text-lg font-semibold text-ink">{money(p.surplus_if_collected)}</span>
            </div>
          </div>
        </Card>

        <Card className="lg:col-span-2" title="Cash & balances" actions={<Link className="text-xs font-medium text-ink-soft hover:underline" href="/deposits">FD / RD →</Link>}>
          {!c.has_data ? (
            <p className="text-sm text-neutral-500">Import a bank statement to see the cash position.</p>
          ) : (
            <table className="w-full text-sm">
              <tbody>
                <Walk label={`Opening total funds · ${fmtDate(c.opening_on)}`} v={c.total_opening} strong />
                <Walk label="Savings account" v={c.opening} sub />
                <Walk label="FD / RD" v={c.deposits_opening} sub />
                <Walk label="+ Maintenance received" v={c.receipts_maintenance} />
                <Walk label="+ Other income (interest etc.)" v={c.receipts_other} />
                {c.receipts_unassigned > 0 && <Walk label="+ Received, not yet assigned" v={c.receipts_unassigned} warn />}
                <Walk label="− Payments to vendors / staff" v={-c.payments_operating} />
                {c.booked_to_deposits !== 0 && (
                  <Walk label="− Moved to FD/RD (booked, not yet on statement)" v={-c.booked_to_deposits} />
                )}
                {c.payments_unassigned > 0 && <Walk label="of which not yet assigned" v={-c.payments_unassigned} sub warn />}
                {(c.to_deposits > 0 || c.from_deposits > 0) && (
                  <Walk label={`Savings ⇄ FD/RD (net ${money(c.to_deposits - c.from_deposits)} moved, total unchanged)`} v={0} sub />
                )}
                <Walk label={`Closing total funds · ${fmtDate(c.closing_on)}`} v={c.total_closing} strong />
                <Walk label="Savings account" v={c.closing} sub />
                <Walk label="FD / RD" v={c.deposits_closing} sub />
                <tr><td colSpan={2} className="pt-3 text-xs font-semibold uppercase tracking-wide text-neutral-500">Held for</td></tr>
                <Walk label="Sinking fund (books)" v={sf.projected} />
                <Walk label="Advances from members" v={d.advances.total} />
                <Walk label="Bills payable (recorded, not yet paid)" v={d.payables.total - d.payables.prepaid_total} />
                <Walk
                  label="Unreconciled difference"
                  v={c.total_closing - sf.projected - d.advances.total - (d.payables.total - d.payables.prepaid_total)}
                  sub
                  warn={Math.abs(c.total_closing - sf.projected - d.advances.total - (d.payables.total - d.payables.prepaid_total)) >= 100}
                />
              </tbody>
            </table>
          )}
          {c.deposits.length > 0 && (
            <div className="mt-3 border-t border-line pt-2 text-xs text-neutral-600">
              {c.deposits.map((x) => (
                <div key={x.id} className="flex justify-between">
                  <span>{x.label}{x.maturity_date && ` · matures ${fmtDate(x.maturity_date)}`}</span>
                  <span className="num">{money(x.value)}</span>
                </div>
              ))}
            </div>
          )}
        </Card>
      </div>

      {/* sinking fund trend */}
      <Card
        className="mt-4"
        title="Sinking fund growth"
        actions={<span className="flex items-center gap-3 text-xs text-neutral-500"><Swatch color="#2a78d6" /> surplus <Swatch color="#e34948" /> deficit <span>· hollow / hatched = running quarter</span></span>}
      >
        <div className="grid gap-6 lg:grid-cols-3">
          <div className="lg:col-span-2">
            <div className="mb-1 text-xs font-medium text-neutral-500">Fund balance at quarter end</div>
            <FundBalanceChart quarters={sf.quarters} />
            <div className="mb-1 mt-4 text-xs font-medium text-neutral-500">Added each quarter (actual income − expenses)</div>
            <SurplusBars quarters={sf.quarters} />
          </div>
          <div>
            <TrendTable sf={sf} />
          </div>
        </div>
      </Card>

      {/* receivables & payables */}
      <div className="mt-4 grid gap-4 lg:grid-cols-2">
        <Card
          title={`Receivables from (${d.receivables.flats.length} flat${d.receivables.flats.length === 1 ? "" : "s"} · ${money(d.receivables.total)})`}
          actions={<Link className="text-xs font-medium text-ink-soft hover:underline" href="/reminders">Send reminder →</Link>}
        >
          <div className="mb-3 flex flex-wrap gap-4 text-xs text-neutral-600">
            <span>{d.current_period}: <b className="num">{money(d.receivables.current)}</b></span>
            <span>Earlier quarters: <b className="num">{money(d.receivables.arrears)}</b></span>
            <span>Late fees: <b className="num">{money(d.receivables.late_fees)}</b></span>
          </div>
          <Scroll>
            <table className="w-full text-sm">
              <thead className="sticky top-0 bg-white">
                <tr className="text-xs uppercase tracking-wide text-neutral-500">
                  <th className="py-1 text-left font-semibold">Flat</th>
                  <th className="py-1 text-right font-semibold">{d.current_period}</th>
                  <th className="py-1 text-right font-semibold">Arrears</th>
                  <th className="py-1 text-right font-semibold">Late fees</th>
                  <th className="py-1 text-right font-semibold">Total</th>
                </tr>
              </thead>
              <tbody>
                {d.receivables.flats.map((r) => (
                  <tr key={r.flat} className="border-b border-line/60 hover:bg-neutral-50">
                    <td className="py-1.5">
                      <Link className="font-semibold text-ink hover:underline" href={`/flats/view?code=${r.flat}`}>{r.flat}</Link>
                      <span className="ml-1.5 text-xs text-neutral-500">{r.owner}</span>
                      {r.days_overdue > 0 && <span className="ml-1.5"><Badge tone="bad">{r.days_overdue}d overdue</Badge></span>}
                    </td>
                    <td className="num py-1.5 text-right">{r.current ? money(r.current) : ""}</td>
                    <td className="num py-1.5 text-right text-bad">{r.arrears ? money(r.arrears) : ""}</td>
                    <td className="num py-1.5 text-right text-bad">{r.late_fees ? money(r.late_fees) : ""}</td>
                    <td className="num py-1.5 text-right font-semibold">{money(r.total)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </Scroll>
        </Card>

        <Card title={`Payables to (${money(d.payables.total)})`} actions={<Link className="text-xs font-medium text-ink-soft hover:underline" href="/expenses">Bills →</Link>}>
          <p className="mb-3 text-xs text-neutral-600">
            Bills recorded since {fmtDate(d.payables.since)} less bank payments to each payee. A negative balance means the payee holds money in
            advance (e.g. the caretaker&apos;s petty-cash float).
          </p>
          {d.payables.payees.length === 0 ? (
            <p className="text-sm text-neutral-500">Nothing payable.</p>
          ) : (
            <table className="w-full text-sm">
              <thead>
                <tr className="text-xs uppercase tracking-wide text-neutral-500">
                  <th className="py-1 text-left font-semibold">Payee</th>
                  <th className="py-1 text-right font-semibold">Bills</th>
                  <th className="py-1 text-right font-semibold">Paid</th>
                  <th className="py-1 text-right font-semibold">Balance</th>
                </tr>
              </thead>
              <tbody>
                {d.payables.payees.map((r) => (
                  <tr key={r.payee} className="border-b border-line/60">
                    <td className="py-1.5">
                      <Link className="hover:underline" href={`/expenses`}>{r.payee}</Link>
                      {r.last_bill && <span className="ml-1.5 text-xs text-neutral-500">last bill {fmtDate(r.last_bill)}</span>}
                    </td>
                    <td className="num py-1.5 text-right">{money(r.billed)}</td>
                    <td className="num py-1.5 text-right">{money(r.paid)}</td>
                    <td className={`num py-1.5 text-right font-semibold ${r.balance > 0 ? "text-warn" : "text-good"}`}>
                      {r.balance < 0 ? `${money(-r.balance)} adv` : money(r.balance)}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </Card>
      </div>

      {/* collections & advances */}
      <div className="mt-4 grid gap-4 lg:grid-cols-3">
        <Card
          className="lg:col-span-2"
          title={`Payments received in ${d.current_period} (${d.collections.count} · ${money(d.collections.amount)})`}
          actions={<Link className="text-xs font-medium text-ink-soft hover:underline" href="/receipts">Receipts register →</Link>}
        >
          {d.collections.recent.length === 0 ? (
            <p className="text-sm text-neutral-500">No payments received yet this quarter.</p>
          ) : (
            <table className="w-full text-sm">
              <tbody>
                {d.collections.recent.map((r) => (
                  <tr key={r.id} className="border-b border-line/60">
                    <td className="whitespace-nowrap py-1.5 pr-2 text-neutral-600">{fmtDate(r.date)}</td>
                    <td className="py-1.5 pr-2"><Link className="font-semibold text-ink hover:underline" href={`/flats/view?code=${r.flat}`}>{r.flat}</Link></td>
                    <td className="py-1.5 pr-2 text-xs text-neutral-600">
                      {PAYMENT_MODES[r.mode] ?? r.mode} → {r.applied.map((a) => a.label).join(", ") || "advance"}
                    </td>
                    <td className="num py-1.5 text-right">{money(-r.amount)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
          {d.collections.off_bank > 0 && <p className="mt-2 text-xs text-neutral-500">{money(d.collections.off_bank)} received outside the bank (GPay/cash).</p>}
        </Card>
        <Card title={`Advances held (${money(d.advances.total)})`}>
          {d.advances.flats.length === 0 ? (
            <p className="text-sm text-neutral-500">No flat has paid ahead.</p>
          ) : (
            <table className="w-full text-sm">
              <tbody>
                {d.advances.flats.map((a) => (
                  <tr key={a.flat} className="border-b border-line/60">
                    <td className="py-1.5"><Link className="font-semibold text-ink hover:underline" href={`/flats/view?code=${a.flat}`}>{a.flat}</Link> <span className="text-xs text-neutral-500">{a.owner}</span></td>
                    <td className="num py-1.5 text-right text-good">{money(a.amount)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
          {d.draft_reminder && (
            <p className="mt-4 rounded-md bg-amber-50 px-3 py-2 text-xs text-warn">
              Reminder #{d.draft_reminder.seq} drafted for {d.draft_reminder.flats.length} flats. <Link className="underline" href="/reminders">Review &amp; send →</Link>
            </p>
          )}
        </Card>
      </div>
    </>
  );
}

function Walk({ label, v, strong, sub, warn }: { label: string; v: number; strong?: boolean; sub?: boolean; warn?: boolean }) {
  return (
    <tr className={strong ? "border-y border-line font-semibold" : ""}>
      <td className={`py-1.5 pr-2 ${sub ? "pl-4 text-xs text-neutral-500" : ""} ${warn ? "!text-warn" : ""}`}>{label}</td>
      <td className={`num py-1.5 text-right ${sub ? "text-xs text-neutral-500" : ""} ${warn ? "!text-warn" : ""}`}>{money(v)}</td>
    </tr>
  );
}

function Swatch({ color }: { color: string }) {
  return <span className="inline-block h-2.5 w-2.5 rounded-sm align-middle" style={{ background: color }} />;
}

function Scroll({ children }: { children: React.ReactNode }) {
  return <div className="max-h-80 overflow-y-auto">{children}</div>;
}

function TrendTable({ sf }: { sf: Dashboard["sinking"] }) {
  const [showQ, setShowQ] = useState(false);
  return (
    <div>
      <div className="mb-1 text-xs font-medium text-neutral-500">Share of income added to the fund</div>
      <table className="w-full text-sm">
        <thead>
          <tr className="text-[11px] uppercase tracking-wide text-neutral-500">
            <th className="pb-1 text-left font-semibold">FY</th>
            <th className="pb-1 text-right font-semibold" title="Quarters that have ended">Closed qtrs</th>
            <th className="pb-1 pl-3 text-right font-semibold" title="Including the running quarter">To date</th>
          </tr>
        </thead>
        <tbody>
          {sf.years.map((y) => (
            <tr key={y.fy} className="border-b border-line/60 align-top">
              <td className="whitespace-nowrap py-1.5 pr-2">
                {y.fy}
                <div className="text-[11px] text-neutral-500">{y.complete ? "full year" : `${y.completed_quarters} of 4 closed`}</div>
              </td>
              <td className="num py-1.5 text-right">
                {y.completed_quarters > 0 ? (
                  <>
                    <span className="font-semibold">{y.completed_percent.toFixed(1)}%</span>
                    <div className="text-[11px] text-neutral-500">{money(y.completed_surplus)} of {money(y.completed_income)}</div>
                  </>
                ) : "—"}
              </td>
              <td className="num py-1.5 pl-3 text-right">
                {y.complete ? <span className="text-neutral-400">same</span> : (
                  <>
                    <span className="font-semibold">{y.percent.toFixed(1)}%</span>
                    <div className="text-[11px] text-neutral-500">{money(y.surplus)} of {money(y.income)}</div>
                  </>
                )}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      <p className="mt-2 text-[11px] leading-snug text-neutral-500">
        Surplus ÷ actual income. “To date” includes the running quarter, which starts with bills in but maintenance not yet due, so it dips early in each quarter.
      </p>
      <dl className="mt-4 space-y-1 text-sm">
        <div className="flex justify-between"><dt className="text-neutral-600">Opening (before {sf.quarters[0]?.period_id ?? "first quarter"})</dt><dd className="num">{money(sf.opening)}</dd></div>
        <div className="flex justify-between"><dt className="text-neutral-600">After closed quarters</dt><dd className="num">{money(sf.balance)}</dd></div>
        {sf.projected !== sf.balance && (
          <div className="flex justify-between font-semibold"><dt>Including running quarter</dt><dd className="num">{money(sf.projected)}</dd></div>
        )}
      </dl>
      <button className="mt-3 text-xs text-ink-soft underline" onClick={() => setShowQ(!showQ)}>{showQ ? "Hide" : "Show"} quarter table</button>
      {showQ && (
        <table className="mt-2 w-full text-xs">
          <thead><tr className="text-neutral-500"><th className="text-left">Qtr</th><th className="text-right">Added</th><th className="text-right">Balance</th></tr></thead>
          <tbody>
            {sf.quarters.map((q) => (
              <tr key={q.period_id}><td>{q.period_id}</td><td className="num text-right">{money(q.surplus)}</td><td className="num text-right">{money(q.balance)}</td></tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  );
}
