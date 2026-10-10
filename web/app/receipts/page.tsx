"use client";
import Link from "next/link";
import { useEffect, useState } from "react";
import { api, Flat, fmtDate, money, PAYMENT_MODES, Period, Receipt } from "@/lib/api";
import { useLoad } from "@/lib/hooks";
import { Card, Empty, ErrorBox, Input, LinkButton, Loading, PageHeader, Select, Stat, Td, Th } from "@/components/ui";

export default function ReceiptsPage() {
  const [flat, setFlat] = useState("");
  const [mode, setMode] = useState("");
  const [period, setPeriod] = useState("");
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  useEffect(() => {
    const u = new URLSearchParams(window.location.search);
    if (u.get("flat")) setFlat(u.get("flat")!);
    if (u.get("period")) setPeriod(u.get("period")!);
  }, []);
  const qs = `flat=${flat}&mode=${mode}&period=${period}&from=${from}&to=${to}`;
  const list = useLoad(() => api.get<Receipt[]>(`/receipts?${qs}`), [qs]);
  const ref = useLoad(async () => {
    const [flats, ps] = await Promise.all([api.get<Flat[]>("/flats"), api.get<{ periods: Period[] }>("/periods")]);
    return { flats, periods: ps.periods };
  });

  const rows = list.data ?? [];
  const total = rows.reduce((s, r) => s - r.amount, 0);
  const adv = rows.reduce((s, r) => s + r.unapplied, 0);
  const offBank = rows.filter((r) => r.mode !== "bank").reduce((s, r) => s - r.amount, 0);

  return (
    <>
      <PageHeader
        title="Receipts register"
        sub="Every maintenance payment received, with the bank reference and the invoices it cleared (oldest dues first)."
        actions={<LinkButton href={`/api/receipts.csv?${qs}`}>Export CSV</LinkButton>}
      />
      <div className="mb-3 flex flex-wrap items-center gap-2">
        <Select value={flat} onChange={(e) => setFlat(e.target.value)} className="!w-auto">
          <option value="">All flats</option>
          {ref.data?.flats.map((f) => <option key={f.code} value={f.code}>{f.code} — {f.owner}</option>)}
        </Select>
        <Select value={period} onChange={(e) => setPeriod(e.target.value)} className="!w-auto">
          <option value="">Any quarter</option>
          {ref.data?.periods.map((p) => <option key={p.id} value={p.id}>Paid towards {p.id}</option>)}
        </Select>
        <Select value={mode} onChange={(e) => setMode(e.target.value)} className="!w-auto">
          <option value="">Any mode</option>
          {Object.entries(PAYMENT_MODES).map(([k, v]) => <option key={k} value={k}>{v}</option>)}
        </Select>
        <Input type="date" value={from} onChange={(e) => setFrom(e.target.value)} className="!w-auto" />
        <span className="text-xs text-neutral-500">to</span>
        <Input type="date" value={to} onChange={(e) => setTo(e.target.value)} className="!w-auto" />
      </div>
      <div className="mb-4 grid grid-cols-2 gap-3 lg:grid-cols-4">
        <Stat label="Receipts" value={rows.length} />
        <Stat label="Total received" value={money(total)} tone="good" />
        <Stat label="Outside the bank" value={money(offBank)} hint="GPay to treasurer, cash, caretaker" />
        <Stat label="Held as advance" value={money(adv)} />
      </div>
      <Card>
        <ErrorBox error={list.error} />
        {!list.data ? <Loading /> : rows.length === 0 ? <Empty>No receipts match.</Empty> : (
          <div className="-mx-4 -my-4 overflow-x-auto">
            <table className="w-full">
              <thead><tr><Th>Date</Th><Th>Flat</Th><Th>Mode / reference</Th><Th right>Amount</Th><Th>Applied to</Th><Th right>Advance</Th></tr></thead>
              <tbody>
                {rows.map((r) => (
                  <tr key={r.id} className="hover:bg-neutral-50">
                    <Td className="whitespace-nowrap">{fmtDate(r.date)}</Td>
                    <Td>
                      <Link className="font-semibold text-ink hover:underline" href={`/flats/view?code=${r.flat}`}>{r.flat}</Link>
                      <div className="text-xs text-neutral-500">{r.owner}</div>
                    </Td>
                    <Td className="max-w-sm text-xs">
                      <div>{PAYMENT_MODES[r.mode] ?? r.mode}{r.source === "import" && " (as per sheet)"}</div>
                      {r.bank_ref ? (
                        <Link className="font-mono text-ink-soft underline" href={`/bank?q=${encodeURIComponent(r.bank_ref)}&status=`}>{r.bank_ref}</Link>
                      ) : r.mode !== "bank" ? <span className="text-neutral-500">{r.description}</span> : null}
                    </Td>
                    <Td right>{money(-r.amount)}</Td>
                    <Td>
                      <div className="flex flex-wrap gap-1">
                        {r.applied.map((ap) => (
                          <span key={ap.charge_id} className="rounded border border-line px-1.5 py-0.5 text-xs">{ap.label} <span className="num text-neutral-500">{money(ap.amount)}</span></span>
                        ))}
                      </div>
                    </Td>
                    <Td right className={r.unapplied ? "font-medium text-good" : "text-neutral-400"}>{r.unapplied ? money(r.unapplied) : "—"}</Td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Card>
    </>
  );
}
