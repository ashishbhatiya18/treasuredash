"use client";
import Link from "next/link";
import { Fragment, useEffect, useState } from "react";
import { api, CreditView, Flat, FlatAccount, fmtDate, LedgerEntry, money, Settlement, today } from "@/lib/api";
import { useLoad, useQueryParam } from "@/lib/hooks";
import { Badge, Button, Card, Empty, ErrorBox, Field, Input, Loading, Modal, PageHeader, Select, Stat, Td, Th, Textarea, useAction } from "@/components/ui";

const KIND: Record<LedgerEntry["kind"], { label: string; tone: "neutral" | "good" | "warn" | "bad" | "info" }> = {
  opening: { label: "Opening", tone: "neutral" },
  invoice: { label: "Invoice", tone: "info" },
  late_fee: { label: "Late fee", tone: "bad" },
  payment: { label: "Payment", tone: "good" },
  adjustment: { label: "Adjustment", tone: "warn" },
};

const MODES: Record<string, string> = {
  bank: "Bank transfer", gpay_personal: "GPay/PhonePe to treasurer", cash: "Cash", via_caretaker: "Via caretaker", cheque: "Cheque",
};

export default function FlatView() {
  const code = useQueryParam("code");
  const res = useLoad(async () => (code ? api.get<{ flat: Flat; ledger: LedgerEntry[] }>(`/flats/${code}/ledger`) : null), [code]);
  const acc = useLoad(async () => (code ? api.get<FlatAccount>(`/flats/${code}/account`) : null), [code]);
  const [modal, setModal] = useState<"payment" | "adjustment" | "edit" | null>(null);
  const [tab, setTab] = useState<"invoices" | "payments" | "ledger">("invoices");
  const [editing, setEditing] = useState<LedgerEntry | null>(null);
  const act = useAction();
  const reload = () => { res.reload(); acc.reload(); };

  if (!code) return <Loading />;
  if (res.error || acc.error) return <ErrorBox error={res.error || acc.error} />;
  if (!res.data || !acc.data) return <Loading />;
  const { flat, ledger } = res.data;
  const a = acc.data;
  const s = a.summary;

  return (
    <>
      <PageHeader
        title={`Flat ${flat.code}`}
        sub={`${flat.owner} · ${flat.area_sqft} sq ft${flat.phone1 ? " · " + flat.phone1 : ""}${flat.email1 ? " · " + flat.email1 : ""}`}
        actions={
          <>
            <Link className="inline-flex items-center rounded-md border border-line bg-white px-3.5 py-2 text-sm font-medium text-ink hover:bg-neutral-50" href={`/bank?flat=${flat.code}&status=`}>Bank txns</Link>
            <Button onClick={() => setModal("edit")}>Edit details</Button>
            <Button onClick={() => setModal("adjustment")}>Adjustment / waiver</Button>
            <Button variant="primary" onClick={() => setModal("payment")}>Record off-bank payment</Button>
          </>
        }
      />
      <ErrorBox error={act.error} />
      <div className="mb-4 grid grid-cols-2 gap-3 lg:grid-cols-5">
        <Stat label="Current outstanding" value={money(s.balance > 0 ? s.balance : 0)} tone={s.balance > 0 ? "bad" : "good"}
          hint={s.overdue > 0 ? `${money(s.overdue)} overdue` : s.balance > 0 ? "not yet due" : "nothing due"} />
        <Stat label="Advance held" value={money(s.advance)} tone={s.advance > 0 ? "good" : undefined} hint="applied to the next invoice" />
        <Stat label="Total charged" value={money(s.charged)} hint="invoices, late fees, dues b/f" />
        <Stat label="Total paid" value={money(s.paid)} />
        <Stat label="Waived / credited" value={money(s.waived)} />
      </div>
      {flat.notes && <p className="mb-4 text-sm text-neutral-600">Notes: {flat.notes}</p>}

      <div className="mb-3 flex gap-1.5">
        {([["invoices", `Invoices (${a.charges.length})`], ["payments", `Payments & credits (${a.credits.length})`], ["ledger", "Ledger"]] as const).map(([k, l]) => (
          <button key={k} onClick={() => setTab(k)} className={`rounded-md border px-3 py-1.5 text-sm ${tab === k ? "border-ink bg-ink text-white" : "border-line bg-white"}`}>{l}</button>
        ))}
      </div>

      {tab === "invoices" && <Invoices a={a} />}
      {tab === "payments" && <Payments a={a} onChanged={reload} onEdit={setEditing} />}
      {tab === "ledger" && <Ledger ledger={ledger} onEdit={setEditing} onDelete={async (id) => { if (confirm("Delete this entry?") && (await act.run(() => api.del(`/ledger/${id}`)))) reload(); }} />}

      {modal === "payment" && <MoneyModal flat={flat.code} kind="payment" onClose={() => setModal(null)} onDone={() => { setModal(null); reload(); }} />}
      {modal === "adjustment" && <MoneyModal flat={flat.code} kind="adjustment" onClose={() => setModal(null)} onDone={() => { setModal(null); reload(); }} />}
      {editing && (
        <MoneyModal flat={flat.code} kind={editing.kind as "payment" | "adjustment"} entry={editing} onClose={() => setEditing(null)} onDone={() => { setEditing(null); reload(); }} />
      )}
      {modal === "edit" && <EditFlat flat={flat} onClose={() => setModal(null)} onDone={() => { setModal(null); reload(); }} />}
    </>
  );
}

const STATUS_TONE = { paid: "good", partial: "warn", unpaid: "neutral", overdue: "bad" } as const;

function settledBy(st: Settlement) {
  if (st.credit_kind === "payment") return `${MODES[st.mode] ?? "Payment"} · ${fmtDate(st.credit_date)}`;
  if (st.credit_kind === "opening") return "Advance brought forward";
  return `Waiver · ${st.description}`;
}

function Invoices({ a }: { a: FlatAccount }) {
  const [open, setOpen] = useState<Record<number, boolean>>({});
  return (
    <Card title="Invoices and how they were settled" actions={<span className="text-xs text-neutral-500">Payments clear the oldest dues first</span>}>
      <div className="-mx-4 -my-4 overflow-x-auto">
        <table className="w-full">
          <thead>
            <tr><Th>Charge</Th><Th>Raised</Th><Th>Due</Th><Th right>Amount</Th><Th right>Settled</Th><Th right>Outstanding</Th><Th>Status</Th><Th /></tr>
          </thead>
          <tbody>
            {a.charges.map((c) => (
              <Fragment key={c.id}>
                <tr className="cursor-pointer hover:bg-neutral-50" onClick={() => setOpen({ ...open, [c.id]: !open[c.id] })}>
                  <Td className="font-medium">
                    <span className="mr-1 inline-block w-3 text-neutral-400">{c.settlements.length ? (open[c.id] ? "▾" : "▸") : ""}</span>
                    {c.label}
                    {c.source === "import" && <span className="ml-1 text-xs text-neutral-400">(sheet)</span>}
                  </Td>
                  <Td className="whitespace-nowrap">{fmtDate(c.date)}</Td>
                  <Td className="whitespace-nowrap">{fmtDate(c.due_date)}</Td>
                  <Td right>{money(c.amount)}</Td>
                  <Td right className="text-good">{c.settled ? money(c.settled) : ""}</Td>
                  <Td right className={c.outstanding ? "font-semibold text-bad" : "text-neutral-400"}>{c.outstanding ? money(c.outstanding) : "—"}</Td>
                  <Td><Badge tone={STATUS_TONE[c.status]}>{c.status === "overdue" ? `overdue ${c.days_overdue}d` : c.status}</Badge></Td>
                  <Td>
                    {c.kind === "invoice" && c.period_id && c.source !== "import" && (
                      <a onClick={(e) => e.stopPropagation()} className="text-xs text-ink-soft hover:underline" target="_blank" href={`/api/periods/${c.period_id}/invoices/${a.flat.code}.pdf`}>PDF</a>
                    )}
                  </Td>
                </tr>
                {open[c.id] && c.settlements.map((st, i) => (
                  <tr key={i} className="bg-neutral-50/70 text-xs">
                    <td />
                    <td colSpan={4} className="py-1.5 pl-3 text-neutral-600">
                      ↳ {settledBy(st)}
                      {st.bank_ref && <> · <Link className="text-ink-soft underline" href={`/bank?q=${encodeURIComponent(st.bank_ref)}&status=`}>ref {st.bank_ref}</Link></>}
                      {st.date !== st.credit_date && <span className="text-neutral-400"> (advance, applied {fmtDate(st.date)})</span>}
                    </td>
                    <td className="num py-1.5 pr-3 text-right text-good">{money(st.amount)}</td>
                    <td colSpan={2} />
                  </tr>
                ))}
              </Fragment>
            ))}
          </tbody>
        </table>
      </div>
    </Card>
  );
}

function Payments({ a, onChanged, onEdit }: { a: FlatAccount; onChanged: () => void; onEdit: (e: LedgerEntry) => void }) {
  const [voiding, setVoiding] = useState<CreditView | null>(null);
  const act = useAction();
  return (
    <>
      <ErrorBox error={act.error} />
      <Card title="Payments and credits received">
        {a.credits.length === 0 ? <Empty>No payments recorded.</Empty> : (
          <div className="-mx-4 -my-4 overflow-x-auto">
            <table className="w-full">
              <thead><tr><Th>Date</Th><Th>Type</Th><Th>Mode / reference</Th><Th right>Amount</Th><Th>Applied to</Th><Th right>Advance left</Th><Th /></tr></thead>
              <tbody>
                {a.credits.map((c) => (
                  <tr key={c.id} className="hover:bg-neutral-50">
                    <Td className="whitespace-nowrap">{fmtDate(c.date)}</Td>
                    <Td><Badge tone={c.kind === "payment" ? "good" : "warn"}>{c.label}</Badge></Td>
                    <Td className="max-w-sm text-xs">
                      {c.kind === "payment" && <div>{MODES[c.mode] ?? c.mode}{c.source === "import" && " (as per sheet)"}</div>}
                      {c.bank_ref ? (
                        <Link className="font-mono text-ink-soft underline" href={`/bank?q=${encodeURIComponent(c.bank_ref)}&status=`}>{c.bank_ref}</Link>
                      ) : <span className="text-neutral-500">{c.description}</span>}
                      {c.bank_narration && <div className="break-all font-mono text-[11px] text-neutral-500">{c.bank_narration}</div>}
                    </Td>
                    <Td right>{money(-c.amount)}</Td>
                    <Td>
                      <div className="flex flex-wrap gap-1">
                        {c.applied.map((ap) => (
                          <span key={ap.charge_id} className="rounded border border-line px-1.5 py-0.5 text-xs">{ap.label} <span className="num text-neutral-500">{money(ap.amount)}</span></span>
                        ))}
                      </div>
                    </Td>
                    <Td right className={c.unapplied ? "text-good font-medium" : "text-neutral-400"}>{c.unapplied ? money(c.unapplied) : "—"}</Td>
                    <Td className="space-y-1">
                      {editable(c) && (
                        <button className="block whitespace-nowrap text-xs text-ink-soft hover:underline" onClick={() => onEdit(c)}>
                          Edit
                        </button>
                      )}
                      {c.kind === "payment" && !c.bank_txn_id && (
                        <button className="block whitespace-nowrap text-xs text-bad hover:underline" onClick={() => setVoiding(c)} title="Remove a payment that was recorded but never received">
                          Not received?
                        </button>
                      )}
                    </Td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Card>
      {a.voids.length > 0 && (
        <Card className="mt-4" title="Corrections (payments removed as not received)">
          <div className="-mx-4 -my-4 overflow-x-auto">
            <table className="w-full">
              <thead><tr><Th>Recorded for</Th><Th>Entry</Th><Th right>Amount</Th><Th>Reason</Th><Th>Removed</Th><Th /></tr></thead>
              <tbody>
                {a.voids.map((v) => (
                  <tr key={v.id} className={v.restored_at ? "text-neutral-400" : ""}>
                    <Td>{fmtDate(v.date)}</Td>
                    <Td className="text-xs">{v.description}</Td>
                    <Td right>{money(-v.amount)}</Td>
                    <Td className="text-xs">{v.reason}</Td>
                    <Td className="whitespace-nowrap text-xs">{fmtDate(v.voided_at)}{v.restored_at && ` · restored ${fmtDate(v.restored_at)}`}</Td>
                    <Td>
                      {!v.restored_at && (
                        <button className="text-xs text-ink-soft hover:underline" onClick={async () => { if (await act.run(() => api.post(`/voids/${v.id}/restore`))) onChanged(); }}>
                          Undo
                        </button>
                      )}
                    </Td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </Card>
      )}
      {voiding && (
        <VoidModal
          c={voiding}
          onClose={() => setVoiding(null)}
          onDone={() => { setVoiding(null); onChanged(); }}
        />
      )}
      {a.pre_cutover_receipts.length > 0 && (
        <Card className="mt-4" title={`Bank receipts before cut-over (${fmtDate(a.cutover_date)})`}>
          <p className="mb-3 text-sm text-neutral-600">Matched to this flat for reference. These are already included in the balances imported from the treasury sheet.</p>
          <div className="-mx-4 -mb-4 overflow-x-auto">
            <table className="w-full">
              <thead><tr><Th>Date</Th><Th>Reference</Th><Th>Narration</Th><Th right>Amount</Th></tr></thead>
              <tbody>
                {a.pre_cutover_receipts.map((t) => (
                  <tr key={t.id}>
                    <Td className="whitespace-nowrap">{fmtDate(t.date)}</Td>
                    <Td mono>{t.ref_no}</Td>
                    <Td mono className="max-w-md break-all">{t.narration}</Td>
                    <Td right>{money(t.credit)}</Td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </Card>
      )}
    </>
  );
}

function VoidModal({ c, onClose, onDone }: { c: CreditView; onClose: () => void; onDone: () => void }) {
  const [reason, setReason] = useState("");
  const act = useAction();
  return (
    <Modal open onClose={onClose} title="Payment not actually received">
      <ErrorBox error={act.error} />
      <p className="mb-3 text-sm text-neutral-700">
        Removes the {money(-c.amount)} payment dated {fmtDate(c.date)} ({c.description}). Later payments are then re-applied to the oldest dues first, so the
        dues this entry wrongly cleared become outstanding again or are settled by a later real payment. You can undo this.
      </p>
      {c.applied.length > 0 && (
        <p className="mb-3 text-xs text-neutral-500">Currently clearing: {c.applied.map((ap) => `${ap.label} ${money(ap.amount)}`).join(", ")}</p>
      )}
      <Field label="Reason (kept in the corrections log)">
        <Input value={reason} onChange={(e) => setReason(e.target.value)} placeholder="e.g. Sheet marked Q2 paid; actually paid on 2 Oct" autoFocus />
      </Field>
      <div className="mt-5 flex justify-end gap-2">
        <Button onClick={onClose}>Cancel</Button>
        <Button variant="danger" disabled={act.busy || !reason.trim()} onClick={async () => {
          if (await act.run(() => api.post(`/ledger/${c.id}/void`, { reason }))) onDone();
        }}>Remove payment</Button>
      </div>
    </Modal>
  );
}

/** Payments and adjustments entered by hand or imported from the sheet; bank-posted ones are changed on the Bank page. */
function editable(e: LedgerEntry) {
  return (e.kind === "payment" || e.kind === "adjustment") && !e.bank_txn_id;
}

function Ledger({ ledger, onEdit, onDelete }: { ledger: LedgerEntry[]; onEdit: (e: LedgerEntry) => void; onDelete: (id: number) => void }) {
  return (
    <Card title="Account ledger (running balance)">
      <div className="-mx-4 -my-4 overflow-x-auto">
        <table className="w-full">
          <thead>
            <tr><Th>Date</Th><Th>Type</Th><Th>Description</Th><Th right>Charge</Th><Th right>Credit</Th><Th right>Balance</Th><Th /></tr>
          </thead>
          <tbody>
            {[...ledger].reverse().map((e) => (
              <tr key={e.id} className="hover:bg-neutral-50">
                <Td className="whitespace-nowrap">{fmtDate(e.date)}</Td>
                <Td><Badge tone={KIND[e.kind].tone}>{KIND[e.kind].label}</Badge></Td>
                <Td className="text-xs">
                  {e.description}
                  {e.mode && e.kind === "payment" && <span className="ml-1 text-neutral-500">· {MODES[e.mode] ?? e.mode}</span>}
                  {e.source === "import" && <span className="ml-1 text-neutral-400">(sheet)</span>}
                </Td>
                <Td right>{e.amount > 0 ? money(e.amount) : ""}</Td>
                <Td right className="text-good">{e.amount < 0 ? money(-e.amount) : ""}</Td>
                <Td right className="font-medium">{money(e.balance)}</Td>
                <Td className="whitespace-nowrap">
                  {editable(e) && (
                    <button className="mr-3 text-xs text-ink-soft hover:underline" onClick={() => onEdit(e)}>Edit</button>
                  )}
                  {editable(e) && e.source !== "import" && (
                    <button className="text-xs text-bad hover:underline" onClick={() => onDelete(e.id)}>Delete</button>
                  )}
                </Td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </Card>
  );
}

function MoneyModal({ flat, kind, entry, onClose, onDone }: {
  flat: string; kind: "payment" | "adjustment"; entry?: LedgerEntry; onClose: () => void; onDone: () => void;
}) {
  // payments are stored negative; adjustments are signed (negative = waiver)
  const [date, setDate] = useState(entry?.date ?? today());
  const [amount, setAmount] = useState(entry ? String(Math.abs(entry.amount) / 100) : "");
  const [mode, setMode] = useState(entry?.mode || "gpay_personal");
  const [desc, setDesc] = useState(entry?.description ?? "");
  const [waive, setWaive] = useState(entry ? entry.amount < 0 : true);
  const act = useAction();
  return (
    <Modal open onClose={onClose} title={`${entry ? "Edit " : ""}${kind === "payment" ? (entry ? "payment" : "Record payment") : entry ? "adjustment" : "Adjustment"} · ${flat}`}>
      <ErrorBox error={act.error} />
      <p className="mb-3 text-sm text-neutral-600">
        {kind === "payment"
          ? "Use this for money that did not come through the association bank account (GPay to treasurer, cash, via caretaker). Bank payments come from statement import."
          : "Waive a late fee or correct a balance. Waivers reduce what the flat owes."}
      </p>
      <div className="grid gap-3 sm:grid-cols-2">
        <Field label="Date"><Input type="date" value={date} onChange={(e) => setDate(e.target.value)} /></Field>
        <Field label="Amount (₹)"><Input type="number" min="0" value={amount} onChange={(e) => setAmount(e.target.value)} autoFocus /></Field>
        {kind === "payment" ? (
          <Field label="Mode">
            <Select value={mode} onChange={(e) => setMode(e.target.value)}>
              {Object.entries(MODES).map(([k, v]) => <option key={k} value={k}>{v}</option>)}
            </Select>
          </Field>
        ) : (
          <Field label="Direction">
            <Select value={waive ? "waive" : "charge"} onChange={(e) => setWaive(e.target.value === "waive")}>
              <option value="waive">Reduce dues (waiver / credit)</option>
              <option value="charge">Increase dues (extra charge)</option>
            </Select>
          </Field>
        )}
        <Field label="Note">
          <Input value={desc} onChange={(e) => setDesc(e.target.value)} placeholder={kind === "payment" ? "e.g. GPay ref 1234" : "e.g. Late fee waived by committee"} />
        </Field>
      </div>
      <div className="mt-5 flex justify-end gap-2">
        <Button onClick={onClose}>Cancel</Button>
        <Button
          variant="primary"
          disabled={act.busy || !amount}
          onClick={async () => {
            const amt = Number(amount);
            const signed = kind === "payment" ? amt : waive ? -amt : amt;
            const r = await act.run(() =>
              entry
                ? api.put(`/ledger/${entry.id}`, { date, amount: signed, mode, description: desc || (kind === "adjustment" ? (waive ? "Waiver" : "Charge") : "") })
                : kind === "payment"
                ? api.post(`/flats/${flat}/payments`, { date, amount: amt, mode, description: desc })
                : api.post(`/flats/${flat}/adjustments`, { date, amount: waive ? -amt : amt, description: desc || (waive ? "Waiver" : "Charge") }),
            );
            if (r) onDone();
          }}
        >
          Save
        </Button>
      </div>
    </Modal>
  );
}

function EditFlat({ flat, onClose, onDone }: { flat: Flat; onClose: () => void; onDone: () => void }) {
  const [f, setF] = useState(flat);
  useEffect(() => setF(flat), [flat]);
  const act = useAction();
  const set = (k: keyof Flat) => (e: React.ChangeEvent<HTMLInputElement | HTMLTextAreaElement>) =>
    setF({ ...f, [k]: k === "area_sqft" ? Number(e.target.value) : e.target.value });
  return (
    <Modal open onClose={onClose} title={`Edit ${flat.code}`}>
      <ErrorBox error={act.error} />
      <div className="grid gap-3 sm:grid-cols-2">
        <Field label="Owner"><Input value={f.owner} onChange={set("owner")} /></Field>
        <Field label="Area (sq ft)" hint="Applies to future invoices only."><Input type="number" value={f.area_sqft} onChange={set("area_sqft")} /></Field>
        <Field label="Phone 1"><Input value={f.phone1} onChange={set("phone1")} /></Field>
        <Field label="Phone 2"><Input value={f.phone2} onChange={set("phone2")} /></Field>
        <Field label="Email 1"><Input value={f.email1} onChange={set("email1")} /></Field>
        <Field label="Email 2"><Input value={f.email2} onChange={set("email2")} /></Field>
        <div className="sm:col-span-2">
          <Field label="Notes (tenants, co-owners, who usually pays)"><Textarea rows={3} value={f.notes} onChange={set("notes")} className="!font-sans" /></Field>
        </div>
      </div>
      <div className="mt-5 flex justify-end gap-2">
        <Button onClick={onClose}>Cancel</Button>
        <Button variant="primary" disabled={act.busy} onClick={async () => { if (await act.run(() => api.put(`/flats/${flat.code}`, f))) onDone(); }}>Save</Button>
      </div>
    </Modal>
  );
}
