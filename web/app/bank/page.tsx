"use client";
import Link from "next/link";
import { useEffect, useRef, useState } from "react";
import { api, BankTxn, Category, fmtDate, Flat, money, Statement } from "@/lib/api";
import { useLoad } from "@/lib/hooks";
import {
  Badge, Button, Card, Empty, ErrorBox, Field, Input, Loading, Modal, Notice, PageHeader, Select, Td, Th, useAction,
} from "@/components/ui";

const STATUSES = [
  { id: "review", label: "Needs review" },
  { id: "matched", label: "Matched" },
  { id: "historical", label: "Before cut-over" },
  { id: "ignored", label: "Ignored" },
  { id: "", label: "All" },
] as const;

type Ref = { flats: Flat[]; categories: Category[]; payees: string[]; incomeCategories: string[] };

export default function BankPage() {
  const [status, setStatus] = useState<string>("review");
  const [dir, setDir] = useState("");
  const [q, setQ] = useState("");
  const [qInput, setQInput] = useState("");
  const [flatF, setFlatF] = useState("");
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const [assigning, setAssigning] = useState<BankTxn | null>(null);
  const [msg, setMsg] = useState<string | null>(null);
  const fileRef = useRef<HTMLInputElement>(null);
  const act = useAction();

  useEffect(() => {
    const u = new URLSearchParams(window.location.search);
    if (u.get("status") !== null) setStatus(u.get("status")!);
    if (u.get("q")) { setQ(u.get("q")!); setQInput(u.get("q")!); }
    if (u.get("flat")) setFlatF(u.get("flat")!);
  }, []);
  useEffect(() => {
    const t = setTimeout(() => setQ(qInput), 300);
    return () => clearTimeout(t);
  }, [qInput]);

  const statements = useLoad(() => api.get<Statement[]>("/bank/statements"));
  const txns = useLoad(
    () => api.get<BankTxn[]>(`/bank/txns?status=${status}&dir=${dir}&q=${encodeURIComponent(q)}&flat=${flatF}&from=${from}&to=${to}`),
    [status, dir, q, flatF, from, to],
  );
  const ref = useLoad<Ref>(async () => {
    const [flats, categories, exp, settings] = await Promise.all([
      api.get<Flat[]>("/flats"),
      api.get<Category[]>("/categories"),
      api.get<{ payees: string[] }>("/expenses?from=2099-01-01"),
      api.get<{ income_categories: string[] }>("/settings"),
    ]);
    return { flats, categories, payees: exp.payees, incomeCategories: settings.income_categories };
  });

  async function onUpload(f: File) {
    setMsg(null);
    const r = await act.run(() => api.upload<{ statement: Statement; warning?: string }>("/bank/statements", f));
    if (r) {
      setMsg(
        `Imported ${r.statement.filename}: ${r.statement.new} new transaction(s) (${fmtDate(r.statement.from_date)} – ${fmtDate(r.statement.to_date)}).` +
          (r.statement.calibration ? ` ${r.statement.calibration}` : "") +
          (r.warning ? ` Warning: ${r.warning}` : ""),
      );
      statements.reload();
      txns.reload();
    }
    if (fileRef.current) fileRef.current.value = "";
  }

  return (
    <>
      <PageHeader
        title="Bank reconciliation"
        sub="Upload the HDFC statement PDF. Payments are matched to flats using the flat number in the remarks, payers you confirmed before, owner names and the amount due."
        actions={
          <>
            <Button onClick={() => act.run(async () => { const r = await api.post<{ matched: number }>("/bank/rematch"); setMsg(`Re-matched ${r.matched} transaction(s).`); txns.reload(); })}>
              Re-run matching
            </Button>
            <Button variant="primary" onClick={() => fileRef.current?.click()} disabled={act.busy}>
              {act.busy ? "Importing…" : "Upload statement PDF"}
            </Button>
            <input ref={fileRef} type="file" accept="application/pdf" hidden onChange={(e) => e.target.files?.[0] && onUpload(e.target.files[0])} />
          </>
        }
      />
      <ErrorBox error={act.error} />
      {msg && <Notice tone="good">{msg}</Notice>}

      <div className="mb-3 flex flex-wrap items-center gap-2">
        {STATUSES.map((s) => (
          <button
            key={s.id}
            onClick={() => setStatus(s.id)}
            className={`rounded-md border px-3 py-1.5 text-sm ${status === s.id ? "border-ink bg-ink text-white" : "border-line bg-white hover:bg-neutral-50"}`}
          >
            {s.label}
          </button>
        ))}
        <Select value={dir} onChange={(e) => setDir(e.target.value)} className="!w-auto">
          <option value="">In &amp; out</option>
          <option value="in">Money in</option>
          <option value="out">Money out</option>
        </Select>
      </div>
      <div className="mb-3 flex flex-wrap items-center gap-2">
        <Input placeholder="Search name, ref no, UPI, amount…" value={qInput} onChange={(e) => setQInput(e.target.value)} className="!w-72" />
        <Select value={flatF} onChange={(e) => setFlatF(e.target.value)} className="!w-auto">
          <option value="">Any flat</option>
          {ref.data?.flats.map((f) => <option key={f.code} value={f.code}>{f.code} — {f.owner}</option>)}
        </Select>
        <Input type="date" value={from} onChange={(e) => setFrom(e.target.value)} className="!w-auto" />
        <span className="text-xs text-neutral-500">to</span>
        <Input type="date" value={to} onChange={(e) => setTo(e.target.value)} className="!w-auto" />
        {(qInput || flatF || from || to) && (
          <Button size="sm" variant="ghost" onClick={() => { setQInput(""); setFlatF(""); setFrom(""); setTo(""); }}>Clear</Button>
        )}
        {txns.data && (
          <span className="ml-auto text-xs text-neutral-500">
            {txns.data.length} txns · in {money(txns.data.reduce((s, t) => s + t.credit, 0))} · out {money(txns.data.reduce((s, t) => s + t.debit, 0))}
          </span>
        )}
      </div>

      <Card>
        {txns.error && <ErrorBox error={txns.error} />}
        {!txns.data ? (
          <Loading />
        ) : txns.data.length === 0 ? (
          <Empty>{status === "review" ? "Nothing to review. ✓" : "No transactions."}</Empty>
        ) : (
          <div className="-mx-4 -my-4 overflow-x-auto">
            <table className="w-full">
              <thead>
                <tr>
                  <Th>Date</Th>
                  <Th>Narration</Th>
                  <Th right>In</Th>
                  <Th right>Out</Th>
                  <Th>Mapped to</Th>
                  <Th>Why</Th>
                  <Th />
                </tr>
              </thead>
              <tbody>
                {txns.data.map((t) => (
                  <tr key={t.id} className="hover:bg-neutral-50">
                    <Td className="whitespace-nowrap">{fmtDate(t.date)}</Td>
                    <Td mono className="max-w-md break-all">{t.narration}</Td>
                    <Td right className="text-good">{t.credit ? money(t.credit) : ""}</Td>
                    <Td right>{t.debit ? money(t.debit) : ""}</Td>
                    <Td className="whitespace-nowrap">
                      <StatusBadge t={t} />
                      <div className="mt-1 font-medium">
                        {t.kind === "maintenance" && t.postings.length === 0 && t.flat && (
                          <Link className="hover:underline" href={`/flats/view?code=${t.flat}`}>Flat {t.flat}</Link>
                        )}
                        {t.postings.map((p) => (
                          <div key={p.flat} className="mb-1">
                            <Link className="hover:underline" href={`/flats/view?code=${p.flat}`}>Flat {p.flat}</Link>
                            {t.postings.length > 1 && <span className="num ml-1 text-xs font-normal text-neutral-500">{money(p.amount)}</span>}
                            <div className="text-xs font-normal text-neutral-600">
                              {p.applied.map((ap) => <div key={ap.charge_id}>→ {ap.label} {money(ap.amount)}</div>)}
                              {p.unapplied > 0 && <div className="text-good">→ advance {money(p.unapplied)}</div>}
                            </div>
                          </div>
                        ))}
                        {t.kind === "expense" && t.payee}
                        {t.kind === "other_income" && t.category}
                        {t.kind === "transfer" && "Transfer"}
                      </div>
                      {t.status === "review" && t.flat && <div className="text-xs text-neutral-500">suggested ({t.confidence}%)</div>}
                    </Td>
                    <Td className="max-w-xs text-xs text-neutral-600">
                      <ul className="list-disc space-y-0.5 pl-4">
                        {t.reasons.map((r, i) => <li key={i}>{r}</li>)}
                      </ul>
                    </Td>
                    <Td>
                      <Button size="sm" variant={t.status === "review" ? "primary" : "secondary"} onClick={() => setAssigning(t)}>
                        {t.status === "review" ? "Assign" : "Change"}
                      </Button>
                    </Td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Card>

      <Card className="mt-4" title="Statements & reconciliation checks">
        {!statements.data ? (
          <Loading />
        ) : statements.data.length === 0 ? (
          <Empty>No statements yet.</Empty>
        ) : (
          <div className="-mx-4 -my-4 overflow-x-auto">
            <table className="w-full">
              <thead>
                <tr><Th>File</Th><Th>Period</Th><Th right>Opening</Th><Th right>+ Credits</Th><Th right>− Debits</Th><Th right>Closing</Th><Th>Check</Th><Th right>Reconciled</Th><Th right>To review</Th></tr>
              </thead>
              <tbody>
                {statements.data.map((s) => (
                  <tr key={s.id}>
                    <Td>
                      {s.filename}
                      <div className="text-xs text-neutral-500">{s.txns} txns · uploaded {fmtDate(s.uploaded_at)}</div>
                      {s.issues.map((i, k) => <div key={k} className="text-xs text-bad">⚠ {i}</div>)}
                    </Td>
                    <Td className="whitespace-nowrap">{fmtDate(s.from_date)} – {fmtDate(s.to_date)}</Td>
                    <Td right>{money(s.opening)}</Td>
                    <Td right className="text-good">{money(s.credits)}</Td>
                    <Td right>{money(s.debits)}</Td>
                    <Td right>{money(s.closing)}</Td>
                    <Td>{s.balanced ? <Badge tone="good">balanced</Badge> : <Badge tone="bad">mismatch</Badge>}</Td>
                    <Td right className="text-xs">
                      in {Math.round((s.credits_resolved / Math.max(1, s.credits)) * 100)}% · out {Math.round((s.debits_resolved / Math.max(1, s.debits)) * 100)}%
                    </Td>
                    <Td right>{s.review > 0 ? <Badge tone="warn">{s.review}</Badge> : "0"}</Td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Card>

      {assigning && ref.data && (
        <AssignModal
          t={assigning}
          refData={ref.data}
          onClose={() => setAssigning(null)}
          onDone={() => {
            setAssigning(null);
            txns.reload();
            statements.reload();
          }}
        />
      )}
    </>
  );
}

function StatusBadge({ t }: { t: BankTxn }) {
  const tone = t.status === "matched" ? "good" : t.status === "review" ? "warn" : "neutral";
  const label = t.status === "historical" ? "pre cut-over" : t.status;
  return <Badge tone={tone}>{label}</Badge>;
}

function AssignModal({ t, refData, onClose, onDone }: { t: BankTxn; refData: Ref; onClose: () => void; onDone: () => void }) {
  const incoming = t.credit > 0;
  const [kind, setKind] = useState<string>(t.kind ? t.kind : incoming ? "maintenance" : "expense");
  const [flat, setFlat] = useState(t.flat ?? "");
  const [payee, setPayee] = useState(t.payee);
  const [category, setCategory] = useState(t.category);
  const [incomeCat, setIncomeCat] = useState(t.category || "Bank interest");
  const [createExpense, setCreateExpense] = useState(t.expense_ids.length === 0 && t.payee === "Bescom");
  const [description, setDescription] = useState("");
  const [remember, setRemember] = useState(true);
  const [splitting, setSplitting] = useState(t.postings.length > 1);
  const [splits, setSplits] = useState<{ flat: string; amount: string }[]>(
    t.postings.length > 1 ? t.postings.map((p) => ({ flat: p.flat, amount: String(p.amount / 100) })) : [{ flat: t.flat ?? "", amount: String(t.credit / 100) }, { flat: "", amount: "" }],
  );
  const splitTotal = splits.reduce((s, x) => s + (Number(x.amount) || 0), 0);
  const splitOk = !splitting || (Math.round(splitTotal * 100) === t.credit && splits.every((x) => x.flat && Number(x.amount) > 0));
  const act = useAction();

  const kinds = incoming
    ? [["maintenance", "Maintenance payment"], ["other_income", "Other income"], ["transfer", "Transfer / not income"], ["ignore", "Ignore"]]
    : [["expense", "Payment to vendor / staff"], ["transfer", "Transfer (e.g. FD, sinking fund a/c)"], ["ignore", "Ignore"]];

  return (
    <Modal open onClose={onClose} title={`${incoming ? "Money in" : "Money out"} · ${money(t.credit || t.debit)} · ${fmtDate(t.date)}`} wide>
      <p className="mb-4 rounded bg-neutral-50 p-2 font-mono text-xs break-all">{t.narration}</p>
      {t.status === "historical" && (
        <Notice>This is before the cut-over date, so it is already counted in the balances imported from the sheet. Assigning it only teaches the matcher.</Notice>
      )}
      <ErrorBox error={act.error} />
      <div className="grid gap-3 sm:grid-cols-2">
        <Field label="What is this?">
          <Select value={kind} onChange={(e) => setKind(e.target.value)}>
            {kinds.map(([k, l]) => <option key={k} value={k}>{l}</option>)}
          </Select>
        </Field>

        {kind === "maintenance" && !splitting && (
          <Field label="Flat">
            <Select value={flat} onChange={(e) => setFlat(e.target.value)}>
              <option value="">Choose flat…</option>
              {refData.flats.map((f) => (
                <option key={f.code} value={f.code}>
                  {f.code} — {f.owner} {f.balance > 0 ? `(due ${money(f.balance)})` : ""}
                </option>
              ))}
            </Select>
          </Field>
        )}

        {kind === "maintenance" && (
          <div className="sm:col-span-2">
            <label className="flex items-center gap-2 text-sm">
              <input type="checkbox" checked={splitting} onChange={(e) => setSplitting(e.target.checked)} />
              Split across several flats (one transfer paying for more than one flat)
            </label>
            {splitting && (
              <div className="mt-2 space-y-2">
                {splits.map((sp, i) => (
                  <div key={i} className="flex gap-2">
                    <Select value={sp.flat} onChange={(e) => setSplits(splits.map((x, j) => (j === i ? { ...x, flat: e.target.value } : x)))}>
                      <option value="">Choose flat…</option>
                      {refData.flats.map((f) => <option key={f.code} value={f.code}>{f.code} — {f.owner} {f.balance > 0 ? `(due ${money(f.balance)})` : ""}</option>)}
                    </Select>
                    <Input type="number" className="!w-36" value={sp.amount} placeholder="₹" onChange={(e) => setSplits(splits.map((x, j) => (j === i ? { ...x, amount: e.target.value } : x)))} />
                    {splits.length > 2 && <Button size="sm" variant="ghost" onClick={() => setSplits(splits.filter((_, j) => j !== i))}>✕</Button>}
                  </div>
                ))}
                <div className="flex items-center justify-between text-xs">
                  <Button size="sm" onClick={() => setSplits([...splits, { flat: "", amount: "" }])}>+ Add flat</Button>
                  <span className={Math.round(splitTotal * 100) === t.credit ? "text-good" : "text-bad"}>
                    {money(Math.round(splitTotal * 100))} of {money(t.credit)}
                  </span>
                </div>
              </div>
            )}
          </div>
        )}

        {kind === "other_income" && (
          <>
            <Field label="Income type">
              <Select value={incomeCat} onChange={(e) => setIncomeCat(e.target.value)}>
                {refData.incomeCategories.map((c) => <option key={c}>{c}</option>)}
              </Select>
            </Field>
            <Field label="Flat (optional, e.g. move-in charges)">
              <Select value={flat} onChange={(e) => setFlat(e.target.value)}>
                <option value="">—</option>
                {refData.flats.map((f) => <option key={f.code} value={f.code}>{f.code} — {f.owner}</option>)}
              </Select>
            </Field>
          </>
        )}

        {kind === "expense" && (
          <>
            <Field label="Paid to">
              <Input list="payees" value={payee} onChange={(e) => setPayee(e.target.value)} placeholder="e.g. Bescom, caretaker" />
              <datalist id="payees">{refData.payees.map((p) => <option key={p} value={p} />)}</datalist>
            </Field>
            <Field label="Category (for a new bill)">
              <Select value={category} onChange={(e) => setCategory(e.target.value)}>
                <option value="">Auto</option>
                {refData.categories.map((c) => <option key={c.name}>{c.name}</option>)}
              </Select>
            </Field>
            <div className="sm:col-span-2">
              {t.expense_ids.length > 0 ? (
                <p className="text-sm text-good">Linked to recorded bill #{t.expense_ids.join(", #")}.</p>
              ) : (
                <label className="flex items-start gap-2 text-sm">
                  <input type="checkbox" className="mt-1" checked={createExpense} onChange={(e) => setCreateExpense(e.target.checked)} />
                  <span>
                    Also record this payment as an expense bill
                    <span className="block text-xs text-neutral-500">
                      Tick this for direct payments with no bill entered (e.g. Bescom bill-pay). Leave it unticked for reimbursements to the caretaker, whose individual bills are already entered.
                    </span>
                  </span>
                </label>
              )}
              {createExpense && (
                <Input className="mt-2" placeholder="Particulars, e.g. Bescom bill Sep 2026" value={description} onChange={(e) => setDescription(e.target.value)} />
              )}
            </div>
          </>
        )}
      </div>

      {(kind === "maintenance" || kind === "expense") && t.payer_key && (
        <label className="mt-4 flex items-center gap-2 text-sm">
          <input type="checkbox" checked={remember} onChange={(e) => setRemember(e.target.checked)} />
          Remember <code className="rounded bg-neutral-100 px-1 text-xs">{t.payer_key}</code> for future statements
        </label>
      )}

      <div className="mt-5 flex justify-end gap-2">
        {t.status !== "review" && (
          <Button variant="ghost" onClick={async () => { if (await act.run(() => api.post(`/bank/txns/${t.id}/assign`, { kind: "review" }))) onDone(); }}>
            Move back to review
          </Button>
        )}
        <Button onClick={onClose}>Cancel</Button>
        <Button
          variant="primary"
          disabled={act.busy || (kind === "maintenance" && (splitting ? !splitOk : !flat)) || (kind === "expense" && !payee)}
          onClick={async () => {
            const body = {
              kind, flat, payee, category, income_category: incomeCat, create_expense: createExpense, description, remember,
              splits: kind === "maintenance" && splitting ? splits.map((x) => ({ flat: x.flat, amount: Number(x.amount) })) : [],
            };
            if (await act.run(() => api.post(`/bank/txns/${t.id}/assign`, body))) onDone();
          }}
        >
          Save
        </Button>
      </div>
    </Modal>
  );
}
