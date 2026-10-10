"use client";
import { useEffect, useState } from "react";
import { api, fmtDate, FlatPeriodStatus, money, Period } from "@/lib/api";
import { useLoad } from "@/lib/hooks";
import {
  Badge, Button, Card, ErrorBox, Field, Input, Loading, Modal, Notice, PageHeader, Select, Stat, Td, Th, useAction,
} from "@/components/ui";

type PeriodsResp = { periods: Period[]; current: string; suggested_next: string };

export default function BillingPage() {
  const periods = useLoad(() => api.get<PeriodsResp>("/periods"));
  const [sel, setSel] = useState<string | null>(null);
  const act = useAction();

  useEffect(() => {
    if (!periods.data || sel) return;
    const q = new URLSearchParams(window.location.search).get("id");
    const cur = periods.data.periods.find((p) => p.id === periods.data!.current);
    setSel(q ?? cur?.id ?? periods.data.periods[0]?.id ?? null);
  }, [periods.data, sel]);

  if (periods.error) return <ErrorBox error={periods.error} />;
  if (!periods.data) return <Loading />;
  const { periods: ps, suggested_next } = periods.data;

  async function openQuarter() {
    const r = await act.run(() => api.post<Period>("/periods", { id: suggested_next }));
    if (r) {
      await periods.reload();
      setSel(r.id);
    }
  }

  return (
    <>
      <PageHeader
        title="Billing & invoices"
        sub="Quarterly maintenance at area × rate × 3 months. Due on the last day of the quarter's second month; late fee per month of delay."
        actions={
          <Button variant="primary" onClick={openQuarter} disabled={act.busy}>
            Open {suggested_next}
          </Button>
        }
      />
      <ErrorBox error={act.error} />
      <div className="mb-4 flex flex-wrap gap-1.5">
        {ps.map((p) => (
          <button
            key={p.id}
            onClick={() => {
              setSel(p.id);
              history.replaceState(null, "", `?id=${p.id}`);
            }}
            className={`rounded-md border px-3 py-1.5 text-sm ${sel === p.id ? "border-ink bg-ink text-white" : "border-line bg-white hover:bg-neutral-50"}`}
          >
            {p.id}
            {p.status === "open" && p.id !== sel && <span className="ml-1.5 inline-block h-1.5 w-1.5 rounded-full bg-good align-middle" />}
          </button>
        ))}
      </div>
      {sel && <PeriodView key={sel} id={sel} onChanged={periods.reload} />}
    </>
  );
}

function PeriodView({ id, onChanged }: { id: string; onChanged: () => void }) {
  const st = useLoad(() => api.get<{ period: Period; flats: FlatPeriodStatus[] }>(`/periods/${id}/status`), [id]);
  const [edit, setEdit] = useState(false);
  const [filter, setFilter] = useState<"all" | "unpaid">("all");
  const act = useAction();

  if (st.error) return <ErrorBox error={st.error} />;
  if (!st.data) return <Loading />;
  const { period: p, flats } = st.data;
  const shown = filter === "unpaid" ? flats.filter((f) => f.outstanding > 0 || f.balance > 0) : flats;

  async function raise() {
    const r = await act.run(() => api.post<{ created: number }>(`/periods/${id}/invoices`));
    if (r) {
      await st.reload();
      onChanged();
    }
  }

  return (
    <>
      <ErrorBox error={act.error} />
      {!p.invoiced_at ? (
        <Notice tone="warn">
          Invoices for {p.id} have not been raised. Check the due date, rate and late fee first.{" "}
          <Button size="sm" variant="primary" className="ml-2" onClick={raise} disabled={act.busy}>
            Raise invoices for all {flats.length} flats
          </Button>
        </Notice>
      ) : (
        !p.auto_late_fee && <Notice>Imported from the treasury sheet. Penalties come from the sheet; automatic late fees are off for this quarter.</Notice>
      )}

      <div className="grid grid-cols-2 gap-3 lg:grid-cols-5">
        <Stat label="Billed" value={money(p.billed)} />
        <Stat label="Outstanding" value={money(p.outstanding)} tone={p.outstanding > 0 ? "warn" : "good"} hint={`${p.unpaid_flats} flats unpaid`} />
        <Stat label="Collected in quarter" value={money(p.collected)} hint="all payments dated in quarter" />
        <Stat label="Due date" value={<span className="text-lg">{fmtDate(p.due_date)}</span>} hint={`late fee ${money(p.late_fee)}/month`} />
        <Stat label="Rate" value={<span className="text-lg">₹{p.rate_per_sqft}/sqft/mo</span>} hint={<Badge tone={p.status === "open" ? "good" : "neutral"}>{p.status}</Badge>} />
      </div>

      <Card
        className="mt-4"
        title={`Flat-wise status · ${p.id}`}
        actions={
          <>
            <Select value={filter} onChange={(e) => setFilter(e.target.value as "all" | "unpaid")} className="!w-auto !py-1 text-xs">
              <option value="all">All flats</option>
              <option value="unpaid">Only with dues</option>
            </Select>
            <Button size="sm" onClick={() => setEdit(true)}>Edit quarter</Button>
            {p.invoiced_at && (
              <a className="rounded-md bg-ink px-2.5 py-1 text-xs font-medium text-white hover:bg-ink-soft" href={`/api/periods/${p.id}/invoices.zip`}>
                Download all invoices (.zip)
              </a>
            )}
          </>
        }
      >
        <div className="-mx-4 -my-4 overflow-x-auto">
          <table className="w-full">
            <thead>
              <tr>
                <Th>Flat</Th>
                <Th>Owner</Th>
                <Th right>Quarter charge</Th>
                <Th right>Unpaid (this qtr)</Th>
                <Th right>Late fees</Th>
                <Th right>Account balance</Th>
                <Th>Invoice</Th>
              </tr>
            </thead>
            <tbody>
              {shown.map((f) => (
                <tr key={f.flat} className="hover:bg-neutral-50">
                  <Td><a className="font-semibold text-ink hover:underline" href={`/flats/view?code=${f.flat}`}>{f.flat}</a></Td>
                  <Td>{f.owner}</Td>
                  <Td right>{f.invoiced ? money(f.invoice) : "—"}</Td>
                  <Td right className={f.outstanding > 0 ? "text-warn font-medium" : "text-good"}>{f.invoiced ? (f.outstanding > 0 ? money(f.outstanding) : "Paid") : "—"}</Td>
                  <Td right>{f.late_fees ? money(f.late_fees) : ""}</Td>
                  <Td right className={f.balance > 0 ? "text-bad font-medium" : f.balance < 0 ? "text-good" : ""}>{money(f.balance)}</Td>
                  <Td>
                    {f.invoiced && (
                      <span className="flex gap-2 text-xs">
                        <a className="text-ink-soft hover:underline" target="_blank" href={`/api/periods/${p.id}/invoices/${f.flat}.pdf`}>View</a>
                        <a className="text-ink-soft hover:underline" href={`/api/periods/${p.id}/invoices/${f.flat}.pdf?download=1`}>Download</a>
                      </span>
                    )}
                  </Td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </Card>

      <EditPeriod open={edit} onClose={() => setEdit(false)} p={p} onSaved={() => { st.reload(); onChanged(); }} />
    </>
  );
}

function EditPeriod({ open, onClose, p, onSaved }: { open: boolean; onClose: () => void; p: Period; onSaved: () => void }) {
  const [due, setDue] = useState(p.due_date);
  const [rate, setRate] = useState(String(p.rate_per_sqft));
  const [fee, setFee] = useState(String(p.late_fee / 100));
  const [status, setStatus] = useState(p.status);
  const act = useAction();
  return (
    <Modal open={open} onClose={onClose} title={`Edit ${p.id}`}>
      <ErrorBox error={act.error} />
      <div className="grid gap-3 sm:grid-cols-2">
        <Field label="Due date" hint="Move it to announce a revised last date; late fees recalculate.">
          <Input type="date" value={due} onChange={(e) => setDue(e.target.value)} />
        </Field>
        <Field label="Late fee per month (₹)">
          <Input type="number" value={fee} onChange={(e) => setFee(e.target.value)} />
        </Field>
        <Field label="Rate (₹/sqft/month)" hint={p.invoiced_at ? "Locked once invoices are raised." : undefined}>
          <Input type="number" step="0.01" value={rate} disabled={!!p.invoiced_at} onChange={(e) => setRate(e.target.value)} />
        </Field>
        <Field label="Status">
          <Select value={status} onChange={(e) => setStatus(e.target.value as "open" | "closed")}>
            <option value="open">Open</option>
            <option value="closed">Closed</option>
          </Select>
        </Field>
      </div>
      <div className="mt-5 flex justify-end gap-2">
        <Button onClick={onClose}>Cancel</Button>
        <Button
          variant="primary"
          disabled={act.busy}
          onClick={async () => {
            const r = await act.run(() => api.put(`/periods/${p.id}`, { due_date: due, rate_per_sqft: Number(rate), late_fee: Number(fee), status }));
            if (r) {
              onSaved();
              onClose();
            }
          }}
        >
          Save
        </Button>
      </div>
    </Modal>
  );
}

