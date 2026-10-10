"use client";
import { useEffect, useState } from "react";
import { api, fmtDate, money, Period, PnL, SinkingFund } from "@/lib/api";
import { useLoad } from "@/lib/hooks";
import { Badge, Button, Card, ErrorBox, LinkButton, Loading, PageHeader, Select, Stat, Td, Th, useAction } from "@/components/ui";

export default function ReportsPage() {
  const periods = useLoad(() => api.get<{ periods: Period[]; current: string }>("/periods"));
  const [period, setPeriod] = useState("");
  useEffect(() => {
    if (periods.data && !period) setPeriod(periods.data.current);
  }, [periods.data, period]);
  const pnl = useLoad(() => (period ? api.get<PnL>(`/reports/pnl?period=${period}`) : Promise.resolve(null)), [period]);
  const sf = useLoad(() => api.get<SinkingFund>("/reports/sinking-fund"));

  if (periods.error) return <ErrorBox error={periods.error} />;
  if (!periods.data) return <Loading />;
  const ids = Array.from(new Set([periods.data.current, ...periods.data.periods.map((p) => p.id)]));

  return (
    <>
      <PageHeader
        title="P&L & sinking fund"
        sub="Income & expenditure per quarter (accrual basis). Every quarter's surplus is transferred to the sinking fund."
        actions={
          <>
            <Select value={period} onChange={(e) => setPeriod(e.target.value)} className="!w-auto">
              {ids.map((id) => <option key={id}>{id}</option>)}
            </Select>
            <LinkButton href={`/api/reports/pnl.csv?period=${period}`}>CSV</LinkButton>
            <LinkButton href={`/api/reports/pnl.pdf?period=${period}`} variant="primary">Download PDF</LinkButton>
            <Button onClick={() => window.print()}>Print</Button>
          </>
        }
      />
      <ErrorBox error={pnl.error} />
      {pnl.data && <Statement p={pnl.data} />}

      <h2 className="mb-3 mt-8 text-lg font-semibold text-ink">Sinking fund</h2>
      <ErrorBox error={sf.error} />
      {sf.data && (
        <Calibrate
          s={sf.data}
          periods={periods.data.periods.filter((p) => p.end_date < new Date().toISOString().slice(0, 10)).map((p) => p.id)}
          onDone={() => { sf.reload(); pnl.reload(); }}
        />
      )}
      {sf.data && <Sinking s={sf.data} />}
    </>
  );
}

function Statement({ p }: { p: PnL }) {
  const surplusLabel = p.surplus < 0 ? "Deficit" : p.running ? "Surplus to date → sinking fund" : "Surplus transferred to sinking fund";
  return (
    <Card title={<>Income &amp; expenditure · {p.period_id} {p.running && <Badge tone="warn">running</Badge>}</>}>
      <p className="mb-4 text-sm text-neutral-600">
        {fmtDate(p.from)} – {fmtDate(p.to)}. Receivable is what was charged for the period; actual is money received in it (applied to the oldest dues
        first). The surplus and the sinking fund use actuals.
      </p>
      <div className="grid gap-8 lg:grid-cols-2">
        <div>
          <div className="mb-2 text-xs font-semibold uppercase tracking-wide text-neutral-500">Income</div>
          <table className="w-full text-sm">
            <thead>
              <tr className="text-xs text-neutral-500">
                <th className="pb-1 text-left font-medium" />
                <th className="pb-1 text-right font-medium">Receivable</th>
                <th className="pb-1 text-right font-medium">Actual</th>
              </tr>
            </thead>
            <tbody>
              {p.income.map((l) => (
                <tr key={l.label} className="border-b border-line/60">
                  <td className="py-1.5 pr-2">{l.label}</td>
                  <td className="num py-1.5 text-right text-neutral-500">{l.no_receivable ? "—" : money(l.receivable)}</td>
                  <td className="num py-1.5 text-right">{money(l.actual)}</td>
                </tr>
              ))}
              <tr className="font-semibold">
                <td className="pt-2">Total income</td>
                <td className="num pt-2 text-right text-neutral-500">{money(p.total_receivable)}</td>
                <td className="num pt-2 text-right">{money(p.total_income)}</td>
              </tr>
            </tbody>
          </table>
        </div>
        <Section title="Expenditure" lines={p.expenses} total={p.total_expenses} />
      </div>

      <div className={`mt-6 flex items-center justify-between rounded-md px-4 py-3 ${p.surplus >= 0 ? "bg-green-50" : "bg-red-50"}`}>
        <span className="font-semibold">{surplusLabel}</span>
        <span className={`num text-xl font-semibold ${p.surplus >= 0 ? "text-good" : "text-bad"}`}>{money(p.surplus)}</span>
      </div>

      <div className="mt-6 grid gap-8 lg:grid-cols-2">
        <div>
          <div className="mb-2 text-xs font-semibold uppercase tracking-wide text-neutral-500">Sinking fund</div>
          <table className="w-full text-sm">
            <tbody>
              <tr className="border-b border-line/60"><td className="py-1.5">Balance brought forward</td><td className="num py-1.5 text-right">{money(p.fund_opening)}</td></tr>
              <tr className="border-b border-line/60"><td className="py-1.5">Add: surplus for {p.period_id}</td><td className="num py-1.5 text-right">{money(p.surplus)}</td></tr>
              <tr className="font-semibold"><td className="py-1.5">{p.running ? "Balance to date" : "Balance carried forward"}</td><td className="num py-1.5 text-right">{money(p.fund_closing)}</td></tr>
            </tbody>
          </table>
          <div className="mt-3 flex items-center justify-between rounded-md bg-blue-50 px-4 py-3">
            <span className="text-sm font-semibold text-ink">Current sinking fund balance · {fmtDate(p.fund_as_of)}</span>
            <span className="num text-xl font-semibold text-ink">{money(p.fund_current)}</span>
          </div>
        </div>
        <div>
          <div className="mb-2 text-xs font-semibold uppercase tracking-wide text-neutral-500">Bank account vs sinking fund</div>
          {p.has_bank_balance ? (
            <>
              <table className="w-full text-sm">
                <tbody>
                  <tr className="border-b border-line/60"><td className="py-1.5">Savings balance as of {fmtDate(p.bank_balance_on)}</td><td className="num py-1.5 text-right">{money(p.bank_balance)}</td></tr>
                  {p.pending_transfers !== 0 && (
                    <tr className="border-b border-line/60 text-xs text-neutral-500"><td className="py-1.5 pl-3">incl. {money(-p.pending_transfers)} moved to FD/RD, not yet on a statement</td><td /></tr>
                  )}
                  <tr className="border-b border-line/60"><td className="py-1.5">Add: FD / RD deposits</td><td className="num py-1.5 text-right">{money(p.deposits_balance)}</td></tr>
                  <tr className="border-b border-line/60"><td className="py-1.5">Less: sinking fund (recorded surpluses)</td><td className="num py-1.5 text-right">{money(-p.fund_closing)}</td></tr>
                  <tr className="border-b border-line/60"><td className="py-1.5">Less: advances held for members</td><td className="num py-1.5 text-right">{money(-p.advances_held)}</td></tr>
                  <tr className="font-semibold"><td className="py-1.5">Reserves not yet allocated to a fund</td><td className="num py-1.5 text-right text-warn">{money(p.unallocated_reserve)}</td></tr>
                </tbody>
              </table>
              <p className="mt-2 text-xs text-neutral-500">
                Money held before the recorded history (corpus / handover deposits, surpluses before the first recorded quarter) plus timing differences. Set the sinking
                fund opening balance in Settings to allocate it.
              </p>
            </>
          ) : (
            <p className="text-sm text-neutral-500">No bank statement covers {fmtDate(p.to)}. Import one to reconcile.</p>
          )}
          {p.bank_current_on && (
            <div className="mt-3 flex items-center justify-between rounded-md bg-neutral-100 px-4 py-3">
              <span className="text-sm font-semibold text-ink">Current account balance · {fmtDate(p.bank_current_on)}</span>
              <span className="num text-xl font-semibold text-ink">{money(p.bank_current)}</span>
            </div>
          )}
        </div>
      </div>

      <div className="mt-6 grid grid-cols-1 gap-3 sm:grid-cols-3">
        <div className="contents">
          <Stat label="Surplus if all receivables collected" value={money(p.surplus_if_collected)} />
          <Stat label="Member dues outstanding" value={money(p.receivables)} tone={p.receivables > 0 ? "warn" : "good"} hint={`as of ${p.running ? "today" : fmtDate(p.to)}`} />
          <Stat label="Collected in period" value={money(p.collections)} hint="all payments received, incl. advances" />
        </div>
      </div>

      <div className="mt-6">
        <div className="mb-2 text-xs font-semibold uppercase tracking-wide text-neutral-500">Non P&amp;L items (advances, reimbursements, corrections)</div>
        {p.non_pnl_items.length === 0 ? (
          <p className="text-sm text-neutral-500">None in this period.</p>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full">
              <thead><tr><Th>Date</Th><Th>Particulars</Th><Th>Payee</Th><Th right>Amount</Th></tr></thead>
              <tbody>
                {p.non_pnl_items.map((it) => (
                  <tr key={it.id}>
                    <Td className="whitespace-nowrap">{fmtDate(it.date)}</Td>
                    <Td>{it.description}</Td>
                    <Td>{it.payee}</Td>
                    <Td right className={it.amount < 0 ? "text-good" : ""}>{money(it.amount)}</Td>
                  </tr>
                ))}
                <tr className="font-semibold">
                  <Td>Total</Td><Td /><Td />
                  <Td right>{money(p.non_pnl_outflow)}</Td>
                </tr>
              </tbody>
            </table>
          </div>
        )}
      </div>
    </Card>
  );
}

function Section({ title, lines, total }: { title: string; lines: { label: string; amount: number; count?: number }[]; total: number }) {
  return (
    <div>
      <div className="mb-2 text-xs font-semibold uppercase tracking-wide text-neutral-500">{title}</div>
      <table className="w-full text-sm">
        <tbody>
          {lines.length === 0 && <tr><td className="py-1 text-neutral-500">Nothing recorded</td></tr>}
          {lines.map((l) => (
            <tr key={l.label} className="border-b border-line/60">
              <td className="py-1.5 pr-2">{l.label}{l.count ? <span className="text-xs text-neutral-400"> ({l.count})</span> : null}</td>
              <td className="num py-1.5 text-right">{money(l.amount)}</td>
            </tr>
          ))}
          <tr className="font-semibold">
            <td className="pt-2">Total {title.toLowerCase()}</td>
            <td className="num pt-2 text-right">{money(total)}</td>
          </tr>
        </tbody>
      </table>
    </div>
  );
}

function Calibrate({ s, periods, onDone }: { s: SinkingFund; periods: string[]; onDone: () => void }) {
  const [q, setQ] = useState(periods[0] ?? "");
  const act = useAction();
  return (
    <Card className="mb-4" title={`Opening balance (reserves before ${s.quarters[0]?.period_id ?? "the first quarter"})`}>
      <ErrorBox error={act.error} />
      <div className="flex flex-wrap items-end gap-3">
        <div>
          <div className="num text-2xl font-semibold text-ink">{money(s.opening)}</div>
          <div className="text-xs text-neutral-500">{s.opening_note || "Not calibrated yet. Set it from the bank balance so the fund matches the account."}</div>
          <label className="mt-2 flex items-center gap-2 text-xs text-neutral-600">
            <input
              type="checkbox"
              checked={s.auto_opening}
              disabled={act.busy}
              onChange={async (e) => {
                const on = e.target.checked;
                if (await act.run(() => api.put("/settings", { sinking_fund_auto: on ? "1" : "0" }))) onDone();
              }}
            />
            Recalculate automatically when an uploaded statement first covers a quarter end
          </label>
        </div>
        <div className="ml-auto flex items-end gap-2">
          <Select value={q} onChange={(e) => setQ(e.target.value)} className="!w-auto">
            {periods.map((id) => <option key={id}>{id}</option>)}
          </Select>
          <Button
            variant="primary"
            disabled={act.busy || !q}
            onClick={async () => {
              if (await act.run(() => api.post("/reports/sinking-fund/calibrate", { period: q }))) onDone();
            }}
          >
            Recalculate from bank balance at end of {q}
          </Button>
        </div>
      </div>
    </Card>
  );
}

function Sinking({ s }: { s: SinkingFund }) {
  const maxAbs = Math.max(1, ...s.quarters.map((q) => Math.abs(q.surplus)));
  return (
    <>
      <div className="mb-4 grid grid-cols-2 gap-3 lg:grid-cols-4">
        <Stat label={`${s.current.fy} share to sinking fund`} value={`${s.current.percent.toFixed(1)}%`} hint="surplus ÷ total income, year to date" tone="good" />
        <Stat label={`${s.current.fy} income`} value={money(s.current.income)} />
        <Stat label="Fund balance" value={money(s.balance)} hint={`after closed quarters (opening ${money(s.opening)})`} />
        <Stat label="Incl. running quarter" value={money(s.projected)} />
      </div>
      <div className="grid gap-4 lg:grid-cols-2">
        <Card title="By financial year">
          <div className="-mx-4 -my-4 overflow-x-auto">
            <table className="w-full">
              <thead><tr><Th>FY</Th><Th right>Income</Th><Th right>Surplus → fund</Th><Th right>% of income</Th></tr></thead>
              <tbody>
                {s.years.map((y) => (
                  <tr key={y.fy}>
                    <Td className="whitespace-nowrap">{y.fy} {!y.complete && <Badge tone="warn">in progress</Badge>}</Td>
                    <Td right>{money(y.income)}</Td>
                    <Td right>{money(y.surplus)}</Td>
                    <Td right className="font-semibold">{y.percent.toFixed(1)}%</Td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </Card>
        <Card title="By quarter">
          <div className="-mx-4 -my-4 overflow-x-auto">
            <table className="w-full">
              <thead><tr><Th>Quarter</Th><Th right>Income</Th><Th right>Expenses</Th><Th>Surplus</Th><Th right>Fund after</Th></tr></thead>
              <tbody>
                {[...s.quarters].reverse().map((q) => (
                  <tr key={q.period_id}>
                    <Td>{q.period_id} {q.running && <Badge tone="warn">running</Badge>}</Td>
                    <Td right>{money(q.income)}</Td>
                    <Td right>{money(q.expenses)}</Td>
                    <Td className="w-40">
                      <div className="flex items-center gap-2">
                        <div className="h-2 flex-1 rounded-full bg-neutral-100">
                          <div className={`h-2 rounded-full ${q.surplus >= 0 ? "bg-good" : "bg-bad"}`} style={{ width: `${(Math.abs(q.surplus) / maxAbs) * 100}%` }} />
                        </div>
                        <span className={`num text-xs ${q.surplus < 0 ? "text-bad" : ""}`}>{money(q.surplus)}</span>
                      </div>
                    </Td>
                    <Td right>{money(q.balance)}</Td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </Card>
      </div>
    </>
  );
}
