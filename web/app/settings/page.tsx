"use client";
import { useEffect, useRef, useState } from "react";
import { api, Category, ImportReport } from "@/lib/api";
import { useLoad } from "@/lib/hooks";
import { Button, Card, ErrorBox, Field, Input, LinkButton, Loading, Notice, PageHeader, Select, Td, Textarea, Th, useAction } from "@/components/ui";

type SettingsResp = { settings: Record<string, string>; scan_enabled: boolean };

const GROUPS: { title: string; fields: [string, string, string?][] }[] = [
  {
    title: "Association",
    fields: [
      ["association_name", "Name"],
      ["association_address", "Address"],
      ["signatory", "Signatory (title)"],
      ["treasurer_name", "Treasurer name (signs the payment request)"],
      ["treasurer_phone", "Treasurer WhatsApp number (for queries)"],
    ],
  },
  {
    title: "Bank details printed on invoices",
    fields: [
      ["bank_account_name", "Account name"],
      ["bank_account_no", "Account number"],
      ["bank_ifsc", "IFSC"],
      ["bank_name", "Bank & branch"],
    ],
  },
  {
    title: "Billing defaults (for newly opened quarters)",
    fields: [
      ["rate_per_sqft", "Rate ₹/sqft/month", "number"],
      ["late_fee", "Late fee ₹ per month of delay", "number"],
      ["cutover_date", "Cut-over date", "date"],
      ["sinking_fund_opening", "Sinking fund opening balance ₹", "number"],
    ],
  },
];

export default function SettingsPage() {
  const s = useLoad(() => api.get<SettingsResp>("/settings"));
  const [form, setForm] = useState<Record<string, string>>({});
  const [saved, setSaved] = useState(false);
  const act = useAction();
  useEffect(() => {
    if (s.data) setForm(s.data.settings);
  }, [s.data]);

  if (s.error) return <ErrorBox error={s.error} />;
  if (!s.data) return <Loading />;
  const set = (k: string) => (e: React.ChangeEvent<HTMLInputElement | HTMLTextAreaElement | HTMLSelectElement>) => {
    setForm({ ...form, [k]: e.target.value });
    setSaved(false);
  };

  async function save() {
    if (await act.run(() => api.put("/settings", form))) {
      setSaved(true);
      s.reload();
    }
  }

  return (
    <>
      <PageHeader title="Settings & import" actions={<Button variant="primary" onClick={save} disabled={act.busy}>Save settings</Button>} />
      <ErrorBox error={act.error} />
      {saved && <Notice tone="good">Settings saved.</Notice>}

      <ImportCard />

      <div className="mt-4 grid gap-4 lg:grid-cols-2">
        {GROUPS.map((g) => (
          <Card key={g.title} title={g.title}>
            <div className="grid gap-3">
              {g.fields.map(([k, label, type]) => (
                <Field
                  key={k}
                  label={label}
                  hint={k === "cutover_date" ? "Bank transactions before this date are treated as already counted in the imported sheet balances." : undefined}
                >
                  <Input type={type ?? "text"} value={form[k] ?? ""} onChange={set(k)} />
                </Field>
              ))}
            </div>
          </Card>
        ))}
        <Card
          title="Day 0 payment request email"
          actions={
            <Button
              size="sm"
              onClick={async () => {
                if (!confirm("Replace the subject and email text with the built-in default?")) return;
                await act.run(async () => {
                  await api.post("/settings/reset", { key: "payment_request_subject" });
                  await api.post("/settings/reset", { key: "payment_request_template" });
                });
                s.reload();
              }}
            >
              Restore default
            </Button>
          }
        >
          <div className="grid gap-3">
            <Field label="Subject">
              <Input value={form.payment_request_subject ?? ""} onChange={set("payment_request_subject")} />
            </Field>
            <Field
              label="Email text"
              hint={<>Placeholders: {"{{quarter_label}} {{quarter}} {{due_date}} {{due_column}} {{next_quarter_label}} {{next_due_date}} {{late_fee}} {{treasurer_name}} {{treasurer_phone}} {{signatory}} {{account_name}} {{bank_name}} {{account_no}} {{ifsc}}"}</>}
            >
              <Textarea rows={16} value={form.payment_request_template ?? ""} onChange={set("payment_request_template")} />
            </Field>
          </div>
        </Card>
        <Card title="Reminders">
          <div className="grid gap-3">
            <div className="grid grid-cols-2 gap-3">
              <Field label="Prepare a draft automatically">
                <Select value={form.reminder_auto ?? "1"} onChange={set("reminder_auto")}>
                  <option value="1">Yes</option>
                  <option value="0">No</option>
                </Select>
              </Field>
              <Field label="Every N days">
                <Input type="number" min="1" value={form.reminder_interval_days ?? ""} onChange={set("reminder_interval_days")} />
              </Field>
            </div>
            <Field label="Template" hint={<>Placeholders: {"{{seq}} {{status_line}} {{count}} {{flats}} {{due_line}} {{due_date}} {{period}}"}</>}>
              <Textarea rows={11} value={form.reminder_template ?? ""} onChange={set("reminder_template")} />
            </Field>
          </div>
        </Card>
      </div>

      <Categories />

      <Card className="mt-4" title="Backup & bill scanning">
        <div className="flex flex-wrap items-center gap-3 text-sm">
          <LinkButton href="/api/export/backup.db">Download database backup</LinkButton>
          <span className="text-neutral-600">Nightly snapshots are also kept in the data volume (last 14 days).</span>
        </div>
        <p className="mt-3 text-sm text-neutral-600">
          Bill scanning is <strong>{s.data.scan_enabled ? "enabled" : "disabled"}</strong>.{" "}
          {!s.data.scan_enabled && "Set ANTHROPIC_API_KEY on the container to read bill photos automatically."}
        </p>
      </Card>
    </>
  );
}

function ImportCard() {
  const fileRef = useRef<HTMLInputElement>(null);
  const [rep, setRep] = useState<ImportReport | null>(null);
  const act = useAction();
  return (
    <Card title="Import treasury workbook">
      <p className="mb-3 text-sm text-neutral-600">
        One-time import of the treasury workbook (.xlsx): flats from the Master List, quarter-wise maintenance history (from Apr 2025; the sheet’s “Q1FY25” is stored as Q1FY26 under Indian FY naming) from the
        Maintenance sheet and every row of the Expenses sheet (dates Excel read as month/day are corrected). Only allowed into an empty database.
      </p>
      <ErrorBox error={act.error} />
      <Button variant="primary" onClick={() => fileRef.current?.click()} disabled={act.busy}>{act.busy ? "Importing…" : "Choose .xlsx"}</Button>
      <input
        ref={fileRef}
        type="file"
        accept=".xlsx"
        hidden
        onChange={async (e) => {
          const f = e.target.files?.[0];
          if (f) {
            const r = await act.run(() => api.upload<ImportReport>("/import/workbook", f));
            if (r) setRep(r);
          }
        }}
      />
      {rep && (
        <div className="mt-4 text-sm">
          <Notice tone="good">
            Imported {rep.flats} flats, {rep.periods} quarters, {rep.ledger_entries} ledger entries and {rep.expenses} expenses ({rep.dates_corrected} dates corrected).
          </Notice>
          {rep.outstanding_flats && rep.outstanding_flats.length > 0 && <p className="mb-2">Opening dues: {rep.outstanding_flats.join(", ")}</p>}
          {rep.warnings && rep.warnings.length > 0 && (
            <ul className="list-disc space-y-1 pl-5 text-warn">{rep.warnings.map((w, i) => <li key={i}>{w}</li>)}</ul>
          )}
        </div>
      )}
    </Card>
  );
}

function Categories() {
  const cats = useLoad(() => api.get<Category[]>("/categories"));
  const [rows, setRows] = useState<Category[]>([]);
  const act = useAction();
  useEffect(() => {
    if (cats.data) setRows(cats.data);
  }, [cats.data]);
  const upd = (i: number, patch: Partial<Category>) => setRows(rows.map((r, j) => (j === i ? { ...r, ...patch } : r)));
  return (
    <Card
      className="mt-4"
      title="Expense categories & auto-categorisation rules"
      actions={<Button size="sm" onClick={() => setRows([...rows, { name: "", in_pnl: true, keywords: "", priority: 100 }])}>Add category</Button>}
    >
      <p className="mb-3 text-sm text-neutral-600">New bills are categorised by the first rule (lowest priority number) whose keyword appears in the particulars or payee.</p>
      <ErrorBox error={act.error} />
      <div className="-mx-4 -mb-4 overflow-x-auto">
        <table className="w-full">
          <thead><tr><Th>Category</Th><Th>Keywords (comma separated)</Th><Th>Priority</Th><Th>In P&amp;L</Th><Th /></tr></thead>
          <tbody>
            {rows.map((c, i) => {
              const orig = cats.data?.find((x) => x.name === c.name);
              const dirty = !orig || orig.keywords !== c.keywords || orig.priority !== c.priority || orig.in_pnl !== c.in_pnl;
              return (
                <tr key={i}>
                  <Td>{orig ? <span className="font-medium">{c.name}</span> : <Input value={c.name} onChange={(e) => upd(i, { name: e.target.value })} />}</Td>
                  <Td className="w-1/2"><Input value={c.keywords} onChange={(e) => upd(i, { keywords: e.target.value })} /></Td>
                  <Td><Input type="number" className="!w-20" value={c.priority} onChange={(e) => upd(i, { priority: Number(e.target.value) })} /></Td>
                  <Td><input type="checkbox" checked={c.in_pnl} onChange={(e) => upd(i, { in_pnl: e.target.checked })} /></Td>
                  <Td>
                    {dirty && (
                      <Button size="sm" variant="primary" disabled={act.busy || !c.name} onClick={async () => { if (await act.run(() => api.put("/categories", c))) cats.reload(); }}>
                        Save
                      </Button>
                    )}
                  </Td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
    </Card>
  );
}
