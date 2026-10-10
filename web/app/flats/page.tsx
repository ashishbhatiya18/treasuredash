"use client";
import Link from "next/link";
import { useState } from "react";
import { api, Flat, money } from "@/lib/api";
import { useLoad } from "@/lib/hooks";
import { Card, ErrorBox, Input, Loading, PageHeader, Stat, Td, Th } from "@/components/ui";

export default function FlatsPage() {
  const { data, error } = useLoad(() => api.get<Flat[]>("/flats"));
  const [q, setQ] = useState("");
  if (error) return <ErrorBox error={error} />;
  if (!data) return <Loading />;
  const needle = q.toLowerCase();
  const shown = data.filter((f) => !q || f.code.toLowerCase().includes(needle) || f.owner.toLowerCase().includes(needle));
  const dues = data.filter((f) => f.balance > 0);
  const advance = data.filter((f) => f.balance < 0);

  return (
    <>
      <PageHeader title="Flats & dues" sub="Each flat's running account: invoices and late fees add to the balance, payments reduce it." />
      <div className="mb-4 grid grid-cols-2 gap-3 lg:grid-cols-4">
        <Stat label="Flats" value={data.length} hint={`${data.reduce((s, f) => s + f.area_sqft, 0).toLocaleString("en-IN")} sq ft`} />
        <Stat label="With dues" value={dues.length} tone={dues.length ? "warn" : "good"} />
        <Stat label="Total dues" value={money(dues.reduce((s, f) => s + f.balance, 0))} tone="warn" />
        <Stat label="Advances held" value={money(-advance.reduce((s, f) => s + f.balance, 0))} hint={`${advance.length} flats`} />
      </div>
      <Card title="All flats" actions={<Input placeholder="Search flat or owner" value={q} onChange={(e) => setQ(e.target.value)} className="!w-56 !py-1" />}>
        <div className="-mx-4 -my-4 overflow-x-auto">
          <table className="w-full">
            <thead>
              <tr><Th>Flat</Th><Th>Owner</Th><Th right>Area</Th><Th>Contact</Th><Th>Notes</Th><Th right>Balance</Th></tr>
            </thead>
            <tbody>
              {shown.map((f) => (
                <tr key={f.code} className="hover:bg-neutral-50">
                  <Td><Link className="font-semibold text-ink hover:underline" href={`/flats/view?code=${f.code}`}>{f.code}</Link></Td>
                  <Td>{f.owner}</Td>
                  <Td right>{f.area_sqft}</Td>
                  <Td className="text-xs text-neutral-600">{[f.phone1, f.email1].filter(Boolean).join(" · ")}</Td>
                  <Td className="max-w-xs text-xs text-neutral-600">{f.notes}</Td>
                  <Td right className={f.balance > 0 ? "font-semibold text-bad" : f.balance < 0 ? "text-good" : "text-neutral-500"}>
                    {f.balance === 0 ? "Nil" : f.balance < 0 ? `${money(-f.balance)} adv` : money(f.balance)}
                  </Td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </Card>
    </>
  );
}
