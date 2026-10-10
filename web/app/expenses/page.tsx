"use client";
import { useMemo, useRef, useState } from "react";
import { api, BillTemplate, Category, Expense, Flat, fmtDate, money, OtherIncome, today } from "@/lib/api";
import { useLoad } from "@/lib/hooks";
import {
  Badge, Button, Card, Empty, ErrorBox, Field, Input, LinkButton, Loading, Modal, Notice, PageHeader, Select, Td, Th, useAction,
} from "@/components/ui";

type Scan = { date: string; vendor: string; description: string; total: number; category: string; line_items: { item: string; amount: number }[]; notes: string };

// FY quarters (Apr–Jun, Jul–Sep, Oct–Dec, Jan–Mar) line up with calendar quarters.
function quarterRange(offset = 0): [string, string] {
  const d = new Date();
  const start = new Date(d.getFullYear(), Math.floor(d.getMonth() / 3) * 3 + offset * 3, 1);
  const end = new Date(start.getFullYear(), start.getMonth() + 3, 0);
  const iso = (x: Date) => `${x.getFullYear()}-${String(x.getMonth() + 1).padStart(2, "0")}-${String(x.getDate()).padStart(2, "0")}`;
  return [iso(start), iso(end)];
}

export default function ExpensesPage() {
  const [tab, setTab] = useState<"expenses" | "income">("expenses");
  return (
    <>
      <PageHeader title="Expenses & income" sub="Bills paid by the association. Hard-copy bills can be photographed and scanned." />
      <div className="mb-4 flex gap-1.5">
        {(["expenses", "income"] as const).map((t) => (
          <button key={t} onClick={() => setTab(t)} className={`rounded-md border px-3 py-1.5 text-sm ${tab === t ? "border-ink bg-ink text-white" : "border-line bg-white"}`}>
            {t === "expenses" ? "Expenses" : "Other income"}
          </button>
        ))}
      </div>
      {tab === "expenses" ? <Expenses /> : <Income />}
    </>
  );
}

function Expenses() {
  const [range, setRange] = useState<[string, string]>(() => quarterRange(0));
  const [category, setCategory] = useState("");
  const [q, setQ] = useState("");
  const [editing, setEditing] = useState<Partial<Expense> | null>(null);
  const cats = useLoad(() => api.get<Category[]>("/categories"));
  const me = useLoad(() => api.get<{ scan_enabled: boolean }>("/me"));
  const list = useLoad(
    () => api.get<{ expenses: Expense[]; payees: string[] }>(`/expenses?from=${range[0]}&to=${range[1]}&category=${encodeURIComponent(category)}&q=${encodeURIComponent(q)}`),
    [range[0], range[1], category, q],
  );
  const byCat = useMemo(() => {
    const m = new Map<string, number>();
    for (const e of list.data?.expenses ?? []) m.set(e.category, (m.get(e.category) ?? 0) + e.amount);
    return [...m.entries()].sort((a, b) => b[1] - a[1]);
  }, [list.data]);
  const total = byCat.reduce((s, [, v]) => s + v, 0);

  return (
    <>
      <div className="mb-3 flex flex-wrap items-end gap-2">
        <Field label="From"><Input type="date" value={range[0]} onChange={(e) => setRange([e.target.value, range[1]])} /></Field>
        <Field label="To"><Input type="date" value={range[1]} onChange={(e) => setRange([range[0], e.target.value])} /></Field>
        <div className="flex gap-1 pb-0.5">
          <Button size="sm" onClick={() => setRange(quarterRange(0))}>This quarter</Button>
          <Button size="sm" onClick={() => setRange(quarterRange(-1))}>Last quarter</Button>
          <Button size="sm" onClick={() => setRange(["2000-01-01", "2099-12-31"])}>All</Button>
        </div>
        <Field label="Category">
          <Select value={category} onChange={(e) => setCategory(e.target.value)}>
            <option value="">All categories</option>
            {cats.data?.map((c) => <option key={c.name}>{c.name}</option>)}
          </Select>
        </Field>
        <Field label="Search"><Input value={q} onChange={(e) => setQ(e.target.value)} placeholder="diesel, Bescom…" /></Field>
        <div className="ml-auto flex gap-2 pb-0.5">
          <LinkButton href={`/api/export/expenses.csv?from=${range[0]}&to=${range[1]}&category=${encodeURIComponent(category)}`}>Export CSV</LinkButton>
          <Button variant="primary" onClick={() => setEditing({ date: today() })}>Add bill</Button>
        </div>
      </div>

      <div className="grid gap-4 lg:grid-cols-4">
        <Card className="lg:col-span-3" title={`${list.data?.expenses.length ?? 0} bills · ${money(total)}`}>
          <ErrorBox error={list.error} />
          {!list.data ? <Loading /> : list.data.expenses.length === 0 ? <Empty>No bills in this range.</Empty> : (
            <div className="-mx-4 -my-4 overflow-x-auto">
              <table className="w-full">
                <thead><tr><Th>Date</Th><Th>Particulars</Th><Th>Category</Th><Th>Payee</Th><Th right>Amount</Th><Th /></tr></thead>
                <tbody>
                  {list.data.expenses.map((e) => (
                    <tr key={e.id} className="cursor-pointer hover:bg-neutral-50" onClick={() => setEditing(e)}>
                      <Td className="whitespace-nowrap">{fmtDate(e.date)}</Td>
                      <Td>
                        {e.description}
                        {e.notes && <div className="text-xs text-neutral-500">{e.notes}</div>}
                      </Td>
                      <Td><Badge tone={e.category.startsWith("Adjustment") ? "neutral" : "info"}>{e.category}</Badge></Td>
                      <Td>{e.payee}</Td>
                      <Td right className={e.amount < 0 ? "text-good" : ""}>{money(e.amount)}</Td>
                      <Td className="whitespace-nowrap text-xs">
                        {e.bill_file && <a onClick={(x) => x.stopPropagation()} target="_blank" className="text-ink-soft hover:underline" href={`/api/bills/${e.bill_file}`}>bill</a>}
                        {e.bank_txn_id && <span className="ml-2 text-good" title="Matched to a bank debit">bank ✓</span>}
                      </Td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </Card>
        <Card title="By category">
          <table className="w-full text-sm">
            <tbody>
              {byCat.map(([c, v]) => (
                <tr key={c}><td className="py-1 pr-2">{c}</td><td className="num py-1 text-right">{money(v)}</td></tr>
              ))}
            </tbody>
          </table>
        </Card>
      </div>

      {editing && cats.data && (
        <ExpenseModal
          initial={editing}
          categories={cats.data}
          payees={list.data?.payees ?? []}
          scanEnabled={!!me.data?.scan_enabled}
          onClose={() => setEditing(null)}
          onDone={() => { setEditing(null); list.reload(); }}
        />
      )}
    </>
  );
}

function ExpenseModal({ initial, categories, payees, scanEnabled, onClose, onDone }: {
  initial: Partial<Expense>; categories: Category[]; payees: string[]; scanEnabled: boolean; onClose: () => void; onDone: () => void;
}) {
  const [e, setE] = useState({
    id: initial.id ?? 0,
    date: initial.date ?? today(),
    description: initial.description ?? "",
    category: initial.category ?? "",
    payee: initial.payee ?? "",
    amount: initial.amount != null ? String(initial.amount / 100) : "",
    notes: initial.notes ?? "",
    bill_file: initial.bill_file ?? "",
    bank_txn_id: initial.bank_txn_id ?? null,
  });
  const [scan, setScan] = useState<Scan | null>(null);
  const [scanMsg, setScanMsg] = useState<string | null>(null);
  const fileRef = useRef<HTMLInputElement>(null);
  const amountRef = useRef<HTMLInputElement>(null);
  const act = useAction();
  const set = (k: keyof typeof e) => (x: React.ChangeEvent<HTMLInputElement | HTMLSelectElement>) => setE({ ...e, [k]: x.target.value });

  // autofill from bills entered before
  const tpl = useLoad(() => api.get<BillTemplate[]>("/expenses/templates"));
  const templates = tpl.data ?? [];
  const [lastAmount, setLastAmount] = useState<number | null>(null);
  const isNew = !e.id;
  const payeeTemplates = templates.filter((t) => t.payee.toLowerCase() === e.payee.trim().toLowerCase());

  function applyTemplate(t: BillTemplate) {
    setE({ ...e, description: t.description, payee: t.payee, category: t.category, amount: t.typical ? String(t.last_amount / 100) : e.amount });
    setLastAmount(t.typical ? null : t.last_amount);
    setTimeout(() => {
      amountRef.current?.focus();
      amountRef.current?.select();
    }, 0);
  }

  function onPayee(value: string) {
    const next = { ...e, payee: value };
    const known = templates.find((t) => t.payee.toLowerCase() === value.trim().toLowerCase());
    if (known && !e.category) next.category = known.category; // the payee's most frequent category
    setE(next);
  }

  async function onFile(f: File) {
    setScanMsg(scanEnabled ? "Reading bill…" : "Uploading…");
    const r = await act.run(() => api.upload<{ bill_file: string; scan?: Scan; scan_error?: string }>("/expenses/scan", f));
    if (!r) return setScanMsg(null);
    const next = { ...e, bill_file: r.bill_file };
    if (r.scan) {
      setScan(r.scan);
      if (r.scan.date) next.date = r.scan.date;
      if (r.scan.description) next.description = r.scan.description;
      if (r.scan.vendor && !next.payee) next.payee = r.scan.vendor;
      if (r.scan.total) next.amount = String(r.scan.total);
      if (r.scan.category && categories.some((c) => c.name === r.scan!.category)) next.category = r.scan.category;
      setScanMsg("Filled from the bill. Please check every field before saving.");
    } else {
      setScanMsg(r.scan_error ? `Bill attached; could not read it automatically (${r.scan_error}).` : "Bill attached.");
    }
    setE(next);
  }

  return (
    <Modal open onClose={onClose} title={e.id ? `Edit bill #${e.id}` : "Add bill"} wide>
      <ErrorBox error={act.error} />
      <div className="mb-4 flex flex-wrap items-center gap-3 rounded-md border border-dashed border-line p-3">
        <Button onClick={() => fileRef.current?.click()} disabled={act.busy}>
          {scanEnabled ? "📷 Scan bill photo / PDF" : "📎 Attach bill photo / PDF"}
        </Button>
        <input ref={fileRef} type="file" accept="image/*,application/pdf" capture="environment" hidden onChange={(x) => x.target.files?.[0] && onFile(x.target.files[0])} />
        {e.bill_file && <a target="_blank" className="text-sm text-ink-soft underline" href={`/api/bills/${e.bill_file}`}>View attached bill</a>}
        <span className="text-xs text-neutral-500">{scanMsg ?? (scanEnabled ? "Date, vendor, total and category are read for you." : "Scanning is off (no API key); fields are entered manually.")}</span>
      </div>
      {scan && scan.line_items.length > 0 && (
        <Notice>
          <div className="mb-1 font-medium">Line items on the bill</div>
          <ul className="text-xs">
            {scan.line_items.map((l, i) => <li key={i} className="flex justify-between gap-4"><span>{l.item}</span><span className="num">₹{l.amount}</span></li>)}
          </ul>
          {scan.notes && <div className="mt-1 text-xs">Note: {scan.notes}</div>}
        </Notice>
      )}
      {isNew && templates.length > 0 && !e.description && (
        <div className="mb-4">
          <div className="mb-1.5 text-xs font-medium text-neutral-500">Frequent bills – one click fills the form</div>
          <div className="flex flex-wrap gap-1.5">
            {templates.slice(0, 8).map((t) => (
              <button
                key={t.description + t.payee}
                type="button"
                onClick={() => applyTemplate(t)}
                className="rounded-full border border-line bg-white px-3 py-1 text-xs hover:border-ink-soft hover:bg-blue-50"
              >
                {t.description} <span className="text-neutral-500">· {t.payee}{t.typical ? ` · ${money(t.last_amount)}` : ""}</span>
              </button>
            ))}
          </div>
        </div>
      )}
      <div className="grid gap-3 sm:grid-cols-2">
        <Field label="Particulars" hint="Start typing to pick a bill you’ve entered before.">
          <ParticularsInput value={e.description} templates={templates} onChange={(v) => setE({ ...e, description: v })} onPick={applyTemplate} />
        </Field>
        <Field label="Paid to / via">
          <Input list="exp-payees" value={e.payee} onChange={(x) => onPayee(x.target.value)} placeholder="Caretaker, Bescom, security agency…" />
          <datalist id="exp-payees">{payees.map((p) => <option key={p} value={p} />)}</datalist>
          {payeeTemplates.length > 0 && !e.description && (
            <div className="mt-1.5 flex flex-wrap gap-1">
              {payeeTemplates.slice(0, 5).map((t) => (
                <button key={t.description} type="button" onClick={() => applyTemplate(t)} className="rounded border border-line px-2 py-0.5 text-[11px] hover:bg-neutral-50">
                  {t.description}
                </button>
              ))}
            </div>
          )}
        </Field>
        <Field
          label="Amount (₹)"
          hint={
            lastAmount != null ? (
              <>
                Varies – last time {money(lastAmount)}.{" "}
                <button type="button" className="text-ink-soft underline" onClick={() => { setE({ ...e, amount: String(lastAmount / 100) }); setLastAmount(null); }}>
                  Use it
                </button>
              </>
            ) : "Negative for advances returned / corrections."
          }
        >
          <Input ref={amountRef} type="number" step="0.01" value={e.amount} onChange={set("amount")} placeholder={lastAmount != null ? String(lastAmount / 100) : undefined} />
        </Field>
        <Field label="Bill date"><Input type="date" value={e.date} onChange={set("date")} /></Field>
        <Field label="Category" hint="Leave on Auto to use the keyword rules.">
          <Select value={e.category} onChange={set("category")}>
            <option value="">Auto</option>
            {categories.map((c) => <option key={c.name}>{c.name}</option>)}
          </Select>
        </Field>
        <Field label="Notes"><Input value={e.notes} onChange={set("notes")} /></Field>
      </div>
      <div className="mt-5 flex justify-between gap-2">
        <div>
          {!!e.id && (
            <Button variant="danger" onClick={async () => { if (confirm("Delete this bill?") && (await act.run(() => api.del(`/expenses/${e.id}`)))) onDone(); }}>
              Delete
            </Button>
          )}
        </div>
        <div className="flex gap-2">
          <Button onClick={onClose}>Cancel</Button>
          <Button
            variant="primary"
            disabled={act.busy || !e.description || !e.amount}
            onClick={async () => {
              const body = { ...e, amount_rupees: Number(e.amount) };
              const r = await act.run(() => (e.id ? api.put(`/expenses/${e.id}`, body) : api.post("/expenses", body)));
              if (r) onDone();
            }}
          >
            Save
          </Button>
        </div>
      </div>
    </Modal>
  );
}

// Type-ahead over past bills: matches particulars or payee, fills the whole form on pick.
function ParticularsInput({ value, templates, onChange, onPick }: {
  value: string; templates: BillTemplate[]; onChange: (v: string) => void; onPick: (t: BillTemplate) => void;
}) {
  const [open, setOpen] = useState(false);
  const [hi, setHi] = useState(0);
  const q = value.trim().toLowerCase();
  const matches = (q
    ? templates.filter((t) => t.description.toLowerCase().includes(q) || t.payee.toLowerCase().includes(q))
    : templates
  ).slice(0, 8);
  const exact = matches.length === 1 && matches[0].description.toLowerCase() === q;
  const show = open && matches.length > 0 && !exact;
  const pick = (t: BillTemplate) => {
    onPick(t);
    setOpen(false);
  };
  return (
    <div className="relative">
      <Input
        value={value}
        placeholder="Diesel for DG"
        autoComplete="off"
        onChange={(x) => { onChange(x.target.value); setOpen(true); setHi(0); }}
        onFocus={() => setOpen(true)}
        onBlur={() => setTimeout(() => setOpen(false), 150)}
        onKeyDown={(x) => {
          if (!show) return;
          if (x.key === "ArrowDown") { x.preventDefault(); setHi((hi + 1) % matches.length); }
          if (x.key === "ArrowUp") { x.preventDefault(); setHi((hi - 1 + matches.length) % matches.length); }
          if (x.key === "Enter") { x.preventDefault(); pick(matches[hi]); }
          if (x.key === "Escape") setOpen(false);
        }}
      />
      {show && (
        <ul className="absolute z-20 mt-1 max-h-72 w-full overflow-y-auto rounded-md border border-line bg-white py-1 shadow-lg">
          {matches.map((t, i) => (
            <li key={t.description + t.payee}>
              <button
                type="button"
                onMouseDown={(x) => { x.preventDefault(); pick(t); }}
                onMouseEnter={() => setHi(i)}
                className={`flex w-full items-baseline justify-between gap-3 px-3 py-1.5 text-left text-sm ${i === hi ? "bg-blue-50" : ""}`}
              >
                <span>
                  {t.description}
                  <span className="ml-1.5 text-xs text-neutral-500">{t.payee} · {t.category}</span>
                </span>
                <span className="num shrink-0 text-xs text-neutral-500">
                  {t.typical ? money(t.last_amount) : `last ${money(t.last_amount)}`} · {t.count}×
                </span>
              </button>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

function Income() {
  const list = useLoad(() => api.get<OtherIncome[]>("/income"));
  const ref = useLoad(async () => {
    const [s, flats] = await Promise.all([api.get<{ income_categories: string[] }>("/settings"), api.get<Flat[]>("/flats")]);
    return { cats: s.income_categories, flats };
  });
  const [open, setOpen] = useState(false);
  const act = useAction();
  return (
    <>
      <Card title="Other income" actions={<Button size="sm" variant="primary" onClick={() => setOpen(true)}>Add income</Button>}>
        <p className="mb-3 text-sm text-neutral-600">Bank interest is picked up automatically from statements. Add move-in charges, hall bookings and similar here.</p>
        <ErrorBox error={list.error || act.error} />
        {!list.data ? <Loading /> : list.data.length === 0 ? <Empty>No other income recorded.</Empty> : (
          <div className="-mx-4 -mb-4 overflow-x-auto">
            <table className="w-full">
              <thead><tr><Th>Date</Th><Th>Type</Th><Th>Description</Th><Th>Flat</Th><Th right>Amount</Th><Th /></tr></thead>
              <tbody>
                {list.data.map((o) => (
                  <tr key={o.id}>
                    <Td>{fmtDate(o.date)}</Td><Td>{o.category}</Td><Td className="text-xs">{o.description}</Td><Td>{o.flat ?? ""}</Td>
                    <Td right>{money(o.amount)}</Td>
                    <Td>
                      {o.bank_txn_id ? <span className="text-xs text-good">bank ✓</span> : (
                        <button className="text-xs text-bad hover:underline" onClick={async () => { if (await act.run(() => api.del(`/income/${o.id}`))) list.reload(); }}>Delete</button>
                      )}
                    </Td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Card>
      {open && ref.data && <IncomeModal cats={ref.data.cats} flats={ref.data.flats} onClose={() => setOpen(false)} onDone={() => { setOpen(false); list.reload(); }} />}
    </>
  );
}

function IncomeModal({ cats, flats, onClose, onDone }: { cats: string[]; flats: Flat[]; onClose: () => void; onDone: () => void }) {
  const [o, setO] = useState({ date: today(), category: cats[1] ?? "Other", description: "", amount: "", flat: "" });
  const act = useAction();
  return (
    <Modal open onClose={onClose} title="Add other income">
      <ErrorBox error={act.error} />
      <div className="grid gap-3 sm:grid-cols-2">
        <Field label="Date"><Input type="date" value={o.date} onChange={(e) => setO({ ...o, date: e.target.value })} /></Field>
        <Field label="Amount (₹)"><Input type="number" value={o.amount} onChange={(e) => setO({ ...o, amount: e.target.value })} /></Field>
        <Field label="Type"><Select value={o.category} onChange={(e) => setO({ ...o, category: e.target.value })}>{cats.map((c) => <option key={c}>{c}</option>)}</Select></Field>
        <Field label="Flat (optional)">
          <Select value={o.flat} onChange={(e) => setO({ ...o, flat: e.target.value })}>
            <option value="">—</option>
            {flats.map((f) => <option key={f.code} value={f.code}>{f.code} — {f.owner}</option>)}
          </Select>
        </Field>
        <div className="sm:col-span-2"><Field label="Description"><Input value={o.description} onChange={(e) => setO({ ...o, description: e.target.value })} /></Field></div>
      </div>
      <div className="mt-5 flex justify-end gap-2">
        <Button onClick={onClose}>Cancel</Button>
        <Button variant="primary" disabled={act.busy || !o.amount} onClick={async () => {
          if (await act.run(() => api.post("/income", { ...o, amount_rupees: Number(o.amount), flat: o.flat || null }))) onDone();
        }}>Save</Button>
      </div>
    </Modal>
  );
}
