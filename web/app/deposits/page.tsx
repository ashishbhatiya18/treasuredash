"use client";
import { useState } from "react";
import { api, Deposit, fmtDate, money, today } from "@/lib/api";
import { useLoad } from "@/lib/hooks";
import { Badge, Button, Card, Empty, ErrorBox, Field, Input, Loading, Modal, PageHeader, Select, Stat, Td, Th, useAction } from "@/components/ui";

export default function DepositsPage() {
  const list = useLoad(() => api.get<Deposit[]>("/deposits"));
  const [editing, setEditing] = useState<Partial<Deposit> | null>(null);
  if (list.error) return <ErrorBox error={list.error} />;
  if (!list.data) return <Loading />;
  const open = list.data.filter((d) => d.active);
  const total = open.reduce((s, d) => s + d.value, 0);
  const nextMaturity = open.filter((d) => d.maturity_date).sort((a, b) => a.maturity_date.localeCompare(b.maturity_date))[0];

  return (
    <>
      <PageHeader
        title="Fixed & recurring deposits"
        sub="Association money held in FDs / RDs. It counts toward total funds alongside the savings balance. Opening an FD/RD from savings reduces the savings balance immediately; the bank debit is matched when the statement arrives. Transfers are neither income nor expense; interest received on closing is income."
        actions={<Button variant="primary" onClick={() => setEditing({ kind: "FD", opened_on: today(), tenure_months: 12, from_savings: true, to_savings: true })}>Add deposit</Button>}
      />
      <div className="mb-4 grid grid-cols-2 gap-3 lg:grid-cols-3">
        <Stat label="Held in deposits today" value={money(total)} tone="good" hint={`${open.length} open`} />
        <Stat label="Next maturity" value={nextMaturity ? fmtDate(nextMaturity.maturity_date) : "—"} hint={nextMaturity?.label} />
        <Stat label="Expected at maturity" value={money(open.reduce((s, d) => s + d.maturity_amount, 0))} hint="as entered" />
      </div>
      <Card>
        {list.data.length === 0 ? <Empty>No deposits recorded. Add FDs/RDs so total funds and the sinking fund reconcile with the bank.</Empty> : (
          <div className="-mx-4 -my-4 overflow-x-auto">
            <table className="w-full">
              <thead><tr><Th>Deposit</Th><Th>Opened</Th><Th right>Amount</Th><Th right>Rate</Th><Th>Matures</Th><Th right>Book value today</Th><Th>Savings account</Th><Th>Status</Th></tr></thead>
              <tbody>
                {list.data.map((d) => (
                  <tr key={d.id} className="cursor-pointer hover:bg-neutral-50" onClick={() => setEditing(d)}>
                    <Td className="font-medium">{d.label}{d.notes && <div className="text-xs font-normal text-neutral-500">{d.notes}</div>}</Td>
                    <Td>{fmtDate(d.opened_on)}</Td>
                    <Td right>{d.kind === "FD" ? money(d.principal) : `${money(d.installment)}/mo × ${d.tenure_months}`}</Td>
                    <Td right>{d.rate ? `${d.rate}%` : ""}</Td>
                    <Td>{fmtDate(d.maturity_date)}{d.maturity_amount ? <div className="text-xs text-neutral-500">{money(d.maturity_amount)}</div> : null}</Td>
                    <Td right className="font-semibold">{d.active ? money(d.value) : "—"}</Td>
                    <Td className="text-xs"><FundingStatus d={d} /></Td>
                    <Td>{d.active ? <Badge tone="good">open</Badge> : <Badge>closed {fmtDate(d.closed_on)}</Badge>}</Td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Card>
      {editing && <DepositModal d={editing} onClose={() => setEditing(null)} onDone={() => { setEditing(null); list.reload(); }} />}
    </>
  );
}

function FundingStatus({ d }: { d: Deposit }) {
  if (d.fundings.length === 0) {
    return <span className="text-neutral-500">{d.from_savings ? "—" : "not linked to savings"}</span>;
  }
  const t = today();
  const due = d.fundings.filter((f) => f.date <= t);
  const pending = due.filter((f) => !f.bank_txn_id);
  const matched = due.filter((f) => f.bank_txn_id);
  return (
    <div className="space-y-0.5">
      {matched.length > 0 && <div className="text-good">{matched.length} matched to statement</div>}
      {pending.length > 0 && (
        <div className="text-warn">
          {pending.length} awaiting statement ({money(pending.reduce((s, f) => s + Math.abs(f.amount), 0))})
        </div>
      )}
      {d.fundings.length > due.length && <div className="text-neutral-500">{d.fundings.length - due.length} future instalments</div>}
    </div>
  );
}

function DepositModal({ d, onClose, onDone }: { d: Partial<Deposit>; onClose: () => void; onDone: () => void }) {
  const [f, setF] = useState({
    kind: d.kind ?? "FD", bank: d.bank ?? "HDFC Bank", number: d.number ?? "", opened_on: d.opened_on ?? today(),
    principal: d.principal ? String(d.principal / 100) : "", installment: d.installment ? String(d.installment / 100) : "",
    tenure_months: String(d.tenure_months ?? 12), rate: d.rate ? String(d.rate) : "", maturity_date: d.maturity_date ?? "",
    maturity_amount: d.maturity_amount ? String(d.maturity_amount / 100) : "", closed_on: d.closed_on ?? "", notes: d.notes ?? "",
    from_savings: d.from_savings ?? false, to_savings: d.to_savings ?? true,
    closing_amount: d.closing_amount ? String(d.closing_amount / 100) : "",
  });
  const act = useAction();
  const set = (k: keyof typeof f) => (e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement>) => setF({ ...f, [k]: e.target.value });
  return (
    <Modal open onClose={onClose} title={d.id ? `Edit ${d.label}` : "Add deposit"} wide>
      <ErrorBox error={act.error} />
      <div className="grid gap-3 sm:grid-cols-3">
        <Field label="Type"><Select value={f.kind} onChange={set("kind")}><option value="FD">Fixed deposit</option><option value="RD">Recurring deposit</option></Select></Field>
        <Field label="Bank"><Input value={f.bank} onChange={set("bank")} /></Field>
        <Field label="Deposit number"><Input value={f.number} onChange={set("number")} /></Field>
        <Field label="Opened on"><Input type="date" value={f.opened_on} onChange={set("opened_on")} /></Field>
        {f.kind === "FD"
          ? <Field label="Principal (₹)"><Input type="number" value={f.principal} onChange={set("principal")} /></Field>
          : <Field label="Monthly instalment (₹)"><Input type="number" value={f.installment} onChange={set("installment")} /></Field>}
        <Field label="Tenure (months)"><Input type="number" value={f.tenure_months} onChange={set("tenure_months")} /></Field>
        <Field label="Interest rate (% p.a.)"><Input type="number" step="0.01" value={f.rate} onChange={set("rate")} /></Field>
        <Field label="Maturity date" hint="Blank = opened + tenure"><Input type="date" value={f.maturity_date} onChange={set("maturity_date")} /></Field>
        <Field label="Maturity amount (₹)"><Input type="number" value={f.maturity_amount} onChange={set("maturity_amount")} /></Field>
        <Field label="Closed / matured on" hint="Set when the money returns to savings"><Input type="date" value={f.closed_on} onChange={set("closed_on")} /></Field>
        <div className="sm:col-span-2"><Field label="Notes"><Input value={f.notes} onChange={set("notes")} /></Field></div>
        <div className="space-y-2 rounded-md border border-line p-3 sm:col-span-3">
          <label className="flex items-start gap-2 text-sm">
            <input type="checkbox" className="mt-1" checked={f.from_savings} onChange={(e) => setF({ ...f, from_savings: e.target.checked })} />
            <span>
              {f.kind === "FD" ? "Paid from the association savings account" : "Instalments debited from the association savings account"}
              <span className="block text-xs text-neutral-500">
                The savings balance drops by {f.kind === "FD" ? "the principal on the opening date" : "each instalment on its date"} right away. When
                the bank statement shows the debit, it is matched automatically and not counted twice. Total funds stay the same.
              </span>
            </span>
          </label>
          {f.closed_on && (
            <div className="flex flex-wrap items-end gap-3">
              <label className="flex items-center gap-2 text-sm">
                <input type="checkbox" checked={f.to_savings} onChange={(e) => setF({ ...f, to_savings: e.target.checked })} />
                Proceeds credited back to savings on {fmtDate(f.closed_on)}
              </label>
              {f.to_savings && (
                <Field label="Amount received (₹)" hint="Blank = amount deposited. Anything above it is booked as FD/RD interest income.">
                  <Input type="number" value={f.closing_amount} onChange={set("closing_amount")} placeholder={f.maturity_amount || ""} />
                </Field>
              )}
            </div>
          )}
        </div>
      </div>
      <div className="mt-5 flex justify-between">
        <div>{d.id && <Button variant="danger" onClick={async () => { if (confirm("Delete this deposit?") && (await act.run(() => api.del(`/deposits/${d.id}`)))) onDone(); }}>Delete</Button>}</div>
        <div className="flex gap-2">
          <Button onClick={onClose}>Cancel</Button>
          <Button variant="primary" disabled={act.busy} onClick={async () => {
            const body = {
              kind: f.kind, bank: f.bank, number: f.number, opened_on: f.opened_on, tenure_months: Number(f.tenure_months), rate: Number(f.rate) || 0,
              maturity_date: f.maturity_date, closed_on: f.closed_on || null, notes: f.notes,
              principal_rupees: Number(f.principal) || 0, installment_rupees: Number(f.installment) || 0, maturity_amount_rupees: Number(f.maturity_amount) || 0,
              from_savings: f.from_savings, to_savings: f.to_savings, closing_amount_rupees: Number(f.closing_amount) || 0,
            };
            if (await act.run(() => (d.id ? api.put(`/deposits/${d.id}`, body) : api.post("/deposits", body)))) onDone();
          }}>Save</Button>
        </div>
      </div>
    </Modal>
  );
}
