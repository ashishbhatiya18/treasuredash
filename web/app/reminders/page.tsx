"use client";
import { useEffect, useState } from "react";
import { api, fmtDate, money, Payables, Payee, PaymentRequest, Period, Reminder } from "@/lib/api";
import { useLoad } from "@/lib/hooks";
import { Badge, Button, Card, CopyButton, Empty, ErrorBox, Field, Input, LinkButton, Loading, Modal, Notice, PageHeader, Select, Textarea, useAction } from "@/components/ui";
import { FlatsByFloor } from "@/components/FlatsByFloor";

export default function RemindersPage() {
  const [tab, setTab] = useState<"day0" | "followup" | "payables">("day0");
  useEffect(() => {
    const t = new URLSearchParams(window.location.search).get("tab");
    if (t === "followup" || t === "payables") setTab(t);
  }, []);
  return (
    <>
      <div className="mb-4 flex gap-1.5">
        {([["day0", "Day 0 payment request"], ["followup", "Follow-up reminders"], ["payables", "Payables · payment instruction"]] as const).map(([k, l]) => (
          <button key={k} onClick={() => setTab(k)} className={`rounded-md border px-3 py-1.5 text-sm ${tab === k ? "border-ink bg-ink text-white" : "border-line bg-white"}`}>
            {l}
          </button>
        ))}
      </div>
      {tab === "day0" && <PaymentRequestTab />}
      {tab === "followup" && <FollowUpReminders />}
      {tab === "payables" && <PayablesTab />}
    </>
  );
}

function PayablesTab() {
  const data = useLoad(() => api.get<Payables>("/payables"));
  const [picked, setPicked] = useState<Record<string, boolean> | null>(null);
  const [open, setOpen] = useState<Record<string, boolean>>({});
  const [editing, setEditing] = useState<Payee | null>(null);
  const [text, setText] = useState("");
  const act = useAction();

  useEffect(() => {
    if (data.data && picked === null) {
      const p: Record<string, boolean> = {};
      for (const x of data.data.payees) p[x.payee.name] = x.balance > 0;
      setPicked(p);
    }
  }, [data.data, picked]);
  const names = data.data && picked ? data.data.payees.filter((x) => picked[x.payee.name]).map((x) => x.payee.name) : [];
  const key = names.join("|");
  useEffect(() => {
    if (!data.data || picked === null) return;
    api.post<{ text: string }>("/payables/instruction", { payees: names }).then((r) => setText(r.text)).catch(() => {});
  }, [key, data.data]); // eslint-disable-line react-hooks/exhaustive-deps

  if (data.error) return <ErrorBox error={data.error} />;
  if (!data.data || !picked) return <Loading />;
  const d = data.data;
  const chosen = d.payees.filter((x) => picked[x.payee.name]);
  const total = chosen.reduce((s, x) => s + x.balance, 0);
  const incomplete = chosen.filter((x) => !x.complete);
  const qs = `payees=${encodeURIComponent(names.join(","))}`;

  return (
    <>
      <PageHeader
        title="Payables · payment instruction"
        sub={`Bills recorded since ${fmtDate(d.since)} that haven't been paid from the bank, itemised per payee. Send the instruction to whoever operates the bank account; the transfers clear automatically when the next statement is imported.`}
      />
      <ErrorBox error={act.error} />
      {d.payees.length === 0 ? (
        <Notice tone="good">Nothing payable – every recorded bill has been paid from the bank.</Notice>
      ) : (
        <div className="grid gap-4 lg:grid-cols-5">
          <div className="space-y-4 lg:col-span-3">
            <Card title={`1. Choose payments · ${money(total)} selected`}>
              <div className="-mx-4 -my-4 divide-y divide-line">
                {d.payees.map((x) => (
                  <div key={x.payee.name} className="px-4 py-3">
                    <div className="flex items-start gap-3">
                      <input
                        type="checkbox"
                        className="mt-1"
                        checked={!!picked[x.payee.name]}
                        disabled={x.balance <= 0}
                        onChange={(e) => setPicked({ ...picked, [x.payee.name]: e.target.checked })}
                      />
                      <div className="min-w-0 flex-1">
                        <div className="flex flex-wrap items-baseline justify-between gap-2">
                          <span className="font-semibold">{x.payee.name}</span>
                          <span className={`num font-semibold ${x.balance > 0 ? "text-ink" : "text-good"}`}>
                            {x.balance < 0 ? `${money(-x.balance)} advance held` : money(x.balance)}
                          </span>
                        </div>
                        <div className="mt-0.5 flex flex-wrap items-center gap-2 text-xs text-neutral-600">
                          <Badge tone={x.complete ? "good" : "warn"}>{x.payee.method || "NEFT"}</Badge>
                          <span className="font-mono">
                            {x.complete ? beneficiary(x.payee) : "Beneficiary details missing"}
                          </span>
                          <button className="text-ink-soft underline" onClick={() => setEditing(x.payee)}>{x.complete ? "Edit" : "Add details"}</button>
                        </div>
                        <button className="mt-1 text-xs text-ink-soft" onClick={() => setOpen({ ...open, [x.payee.name]: !open[x.payee.name] })}>
                          {open[x.payee.name] ? "▾" : "▸"} {x.items.length} bill{x.items.length === 1 ? "" : "s"}
                          {x.paid > 0 && <span className="text-neutral-500"> · {money(x.paid)} already paid since {fmtDate(d.since)}</span>}
                        </button>
                        {open[x.payee.name] && (
                          <table className="mt-2 w-full text-xs">
                            <tbody>
                              {x.items.map((it) => (
                                <tr key={it.expense_id} className="border-b border-line/60">
                                  <td className="whitespace-nowrap py-1 pr-2 text-neutral-500">{fmtDate(it.date)}</td>
                                  <td className="py-1 pr-2">{it.description}{it.outstanding !== it.amount && it.amount > 0 && <span className="text-neutral-500"> (balance of {money(it.amount)})</span>}</td>
                                  <td className="py-1 pr-2 text-neutral-500">{it.category}</td>
                                  <td className={`num py-1 text-right ${it.outstanding < 0 ? "text-good" : ""}`}>{money(it.outstanding)}</td>
                                </tr>
                              ))}
                            </tbody>
                          </table>
                        )}
                      </div>
                    </div>
                  </div>
                ))}
              </div>
            </Card>
            <Card title="2. Instruction message" actions={<CopyButton text={text} label="Copy message" />}>
              <Textarea rows={18} value={text} onChange={(e) => setText(e.target.value)} className="!text-xs" />
            </Card>
          </div>
          <div className="space-y-4 lg:col-span-2">
            <Card title="3. Send">
              <dl className="mb-3 space-y-1 text-sm">
                <div className="flex justify-between"><dt className="text-neutral-600">Payments</dt><dd>{chosen.length}</dd></div>
                <div className="flex justify-between font-semibold"><dt>Total to transfer</dt><dd className="num">{money(total)}</dd></div>
                <div className="flex justify-between text-xs"><dt className="text-neutral-500">From</dt><dd className="text-right">{d.account.name}<br />A/c {d.account.no}</dd></div>
              </dl>
              {incomplete.length > 0 && (
                <Notice tone="warn">Missing beneficiary details for {incomplete.map((x) => x.payee.name).join(", ")}. Add them before sending.</Notice>
              )}
              <div className="grid gap-2">
                <LinkButton href={`/api/payables/instruction.pdf?${qs}`} variant="primary">Download instruction PDF</LinkButton>
                <a
                  className="inline-flex items-center justify-center rounded-md border border-line bg-white px-3.5 py-2 text-sm font-medium text-ink hover:bg-neutral-50"
                  href={`https://wa.me/?text=${encodeURIComponent(text)}`}
                  target="_blank"
                  rel="noreferrer"
                >
                  Share on WhatsApp
                </a>
                <Button
                  disabled={act.busy || chosen.length === 0}
                  onClick={async () => {
                    if (await act.run(() => api.post("/payables/sent", { text, payees: names }))) data.reload();
                  }}
                >
                  Mark as sent
                </Button>
              </div>
            </Card>
            {d.sent.length > 0 && (
              <Card title="Sent instructions">
                {d.sent.map((x) => (
                  <details key={x.id} className="mb-2 text-xs">
                    <summary className="cursor-pointer">{fmtDate(x.sent_at)} · {x.subject.replace("Payment instruction: ", "")}</summary>
                    <pre className="mt-1 whitespace-pre-wrap font-sans text-neutral-600">{x.text}</pre>
                  </details>
                ))}
              </Card>
            )}
          </div>
        </div>
      )}
      {editing && (
        <PayeeModal
          p={editing}
          onClose={() => setEditing(null)}
          onDone={() => { setEditing(null); setPicked(null); data.reload(); }}
        />
      )}
    </>
  );
}

function beneficiary(p: Payee) {
  switch ((p.method || "NEFT").toUpperCase()) {
    case "UPI": return `UPI ${p.upi}`;
    case "BILLPAY": return `Bill payment · ref ${p.reference}`;
    case "CASH": return "Cash";
    case "CHEQUE": return `Cheque · ${p.account_name || p.name}`;
  }
  return `${p.account_name || p.name} · A/c ${p.account_no} · IFSC ${p.ifsc}`;
}

function PayeeModal({ p, onClose, onDone }: { p: Payee; onClose: () => void; onDone: () => void }) {
  const [f, setF] = useState(p);
  const act = useAction();
  const set = (k: keyof Payee) => (e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement>) => setF({ ...f, [k]: e.target.value });
  const m = (f.method || "NEFT").toUpperCase();
  return (
    <Modal open onClose={onClose} title={`Beneficiary details · ${p.name}`}>
      <ErrorBox error={act.error} />
      <div className="grid gap-3 sm:grid-cols-2">
        <Field label="Method">
          <Select value={f.method || "NEFT"} onChange={set("method")}>
            {["NEFT", "IMPS", "UPI", "BillPay", "Cheque", "Cash"].map((x) => <option key={x}>{x}</option>)}
          </Select>
        </Field>
        <Field label="Account / beneficiary name"><Input value={f.account_name} onChange={set("account_name")} /></Field>
        {(m === "NEFT" || m === "IMPS") && (
          <>
            <Field label="Account number"><Input value={f.account_no} onChange={set("account_no")} inputMode="numeric" /></Field>
            <Field label="IFSC"><Input value={f.ifsc} onChange={set("ifsc")} placeholder="HDFC0005493" className="uppercase" /></Field>
          </>
        )}
        {m === "UPI" && <Field label="UPI ID"><Input value={f.upi} onChange={set("upi")} placeholder="name@okhdfcbank" /></Field>}
        {m === "BILLPAY" && <Field label="Consumer / account no. with the biller"><Input value={f.reference} onChange={set("reference")} /></Field>}
        <div className="sm:col-span-2"><Field label="Notes"><Input value={f.notes} onChange={set("notes")} /></Field></div>
      </div>
      <div className="mt-5 flex justify-end gap-2">
        <Button onClick={onClose}>Cancel</Button>
        <Button variant="primary" disabled={act.busy} onClick={async () => {
          if (await act.run(() => api.put(`/payees/${encodeURIComponent(p.name)}`, f))) onDone();
        }}>Save</Button>
      </div>
    </Modal>
  );
}

function gmailLink(to: string[], subject: string, body: string) {
  const q = new URLSearchParams({ view: "cm", fs: "1", bcc: to.join(","), su: subject, body });
  return `https://mail.google.com/mail/?${q.toString()}`;
}

function PaymentRequestTab() {
  const periods = useLoad(() => api.get<{ periods: Period[]; current: string }>("/periods"));
  const [period, setPeriod] = useState("");
  useEffect(() => {
    if (periods.data && !period) {
      const open = periods.data.periods.find((p) => p.status === "open" && p.invoiced_at);
      setPeriod(open?.id ?? periods.data.current);
    }
  }, [periods.data, period]);
  const pr = useLoad(() => (period ? api.get<PaymentRequest>(`/periods/${period}/payment-request`) : Promise.resolve(null)), [period]);
  const [subject, setSubject] = useState("");
  const [body, setBody] = useState("");
  const act = useAction();
  useEffect(() => {
    if (pr.data) {
      setSubject(pr.data.subject);
      setBody(pr.data.body);
    }
  }, [pr.data]);

  if (periods.error) return <ErrorBox error={periods.error} />;
  if (!periods.data) return <Loading />;
  const d = pr.data;
  const emails = d?.emails.join(", ") ?? "";

  return (
    <>
      <PageHeader
        title="Day 0 payment request"
        sub="The email sent when a quarter's invoices are raised, with the flat-wise dues PDF attached. Copy the addresses, subject and text into Gmail, attach the PDF and send."
        actions={
          <Field label="Quarter">
            <Select value={period} onChange={(e) => setPeriod(e.target.value)}>
              {periods.data.periods.map((p) => <option key={p.id}>{p.id}</option>)}
            </Select>
          </Field>
        }
      />
      <ErrorBox error={pr.error || act.error} />
      {!d ? (pr.error ? null : <Loading />) : (
        <>
          {d.sent.length > 0 && (
            <Notice tone="good">Sent for {d.period_id} on {fmtDate(d.sent[0].sent_at)}. You can send it again if needed; each send is logged below.</Notice>
          )}
          <div className="grid gap-4 lg:grid-cols-3">
            <div className="space-y-4 lg:col-span-2">
              <Card title={`1. Recipients · ${d.emails.length} email addresses`} actions={<CopyButton text={emails} label="Copy all emails" />}>
                <p className="mb-2 text-xs text-neutral-600">Paste into Gmail’s <b>Bcc</b> field so owners don’t see each other’s addresses.</p>
                <Textarea readOnly rows={4} value={emails} onFocus={(e) => e.currentTarget.select()} className="!text-xs" />
                {d.missing_email.length > 0 && (
                  <p className="mt-2 text-xs text-warn">
                    No email on record for {d.missing_email.join(", ")}. Add it on the flat’s page (Edit details) or send to them on WhatsApp.
                  </p>
                )}
              </Card>
              <Card title="2. Subject" actions={<CopyButton text={subject} label="Copy subject" />}>
                <Input value={subject} onChange={(e) => setSubject(e.target.value)} />
              </Card>
              <Card title="3. Email text" actions={<CopyButton text={body} label="Copy text" />}>
                <Textarea rows={22} value={body} onChange={(e) => setBody(e.target.value)} className="!font-sans !text-sm" />
                <p className="mt-2 text-xs text-neutral-500">Edits here apply to this send only. Change the standing wording, phone number or bank details in Settings.</p>
              </Card>
            </div>
            <div className="space-y-4">
              <Card title="4. Attachment">
                <LinkButton href={`/api/periods/${d.period_id}/payment-request.pdf`} variant="primary">Download payment request PDF</LinkButton>
                <p className="mt-2 text-xs text-neutral-600">
                  One page with every flat: quarterly amount, {d.prev_period_id} due, penalties, payment, paid and due as of {fmtDate(d.as_of)}.
                </p>
                <dl className="mt-3 space-y-1 text-sm">
                  <div className="flex justify-between"><dt className="text-neutral-600">Flats</dt><dd>{d.rows.length}</dd></div>
                  <div className="flex justify-between"><dt className="text-neutral-600">Quarterly amount</dt><dd className="num">{money(d.total.quarterly)}</dd></div>
                  <div className="flex justify-between"><dt className="text-neutral-600">{d.prev_period_id} due carried in</dt><dd className="num">{money(d.total.prev_due)}</dd></div>
                  <div className="flex justify-between"><dt className="text-neutral-600">Penalties</dt><dd className="num">{money(d.total.penalties)}</dd></div>
                  <div className="flex justify-between font-semibold"><dt>Total due</dt><dd className="num">{money(d.total.due)}</dd></div>
                </dl>
              </Card>
              <Card title="5. Send">
                <a
                  className="mb-2 inline-flex w-full items-center justify-center rounded-md border border-line bg-white px-3.5 py-2 text-sm font-medium text-ink hover:bg-neutral-50"
                  href={gmailLink(d.emails, subject, body)}
                  target="_blank"
                  rel="noreferrer"
                >
                  Open as Gmail draft
                </a>
                <p className="mb-3 text-xs text-neutral-500">Opens Gmail with Bcc, subject and text filled in. Attach the PDF there before sending.</p>
                <Button
                  variant="primary"
                  className="w-full"
                  disabled={act.busy}
                  onClick={async () => {
                    if (await act.run(() => api.post(`/periods/${d.period_id}/payment-request/sent`, { subject, body }))) pr.reload();
                  }}
                >
                  Mark as sent
                </Button>
              </Card>
              {d.sent.length > 0 && (
                <Card title="Sent">
                  {d.sent.map((x) => (
                    <details key={x.id} className="mb-2 text-xs">
                      <summary className="cursor-pointer">{fmtDate(x.sent_at)} · {x.subject}</summary>
                      <pre className="mt-1 whitespace-pre-wrap font-sans text-neutral-600">{x.text}</pre>
                    </details>
                  ))}
                </Card>
              )}
            </div>
          </div>
        </>
      )}
    </>
  );
}

function FollowUpReminders() {
  const periods = useLoad(() => api.get<{ periods: Period[]; current: string }>("/periods"));
  const [period, setPeriod] = useState<string>("");
  useEffect(() => {
    if (periods.data && !period) {
      const open = periods.data.periods.find((p) => p.status === "open" && p.invoiced_at);
      setPeriod(open?.id ?? periods.data.current);
    }
  }, [periods.data, period]);

  const history = useLoad(() => (period ? api.get<Reminder[]>(`/reminders?period=${period}`) : Promise.resolve([])), [period]);
  const draft = history.data?.find((r) => r.status === "draft") ?? null;
  const [text, setText] = useState("");
  const [preview, setPreview] = useState<Reminder | null>(null);
  const act = useAction();

  useEffect(() => {
    if (draft) {
      setText(draft.text);
      setPreview(draft);
    } else if (period) {
      api.post<Reminder>(`/reminders/preview?period=${period}`).then((r) => {
        setPreview(r);
        setText(r.text);
      });
    }
  }, [draft?.id, period]); // eslint-disable-line react-hooks/exhaustive-deps

  if (periods.error) return <ErrorBox error={periods.error} />;
  if (!periods.data || !preview) return <Loading />;

  const wa = `https://wa.me/?text=${encodeURIComponent(text)}`;

  return (
    <>
      <PageHeader
        title="Payment reminders"
        sub="Lists flats with pending dues in the WhatsApp group format. A fresh draft is prepared automatically every few days (see Settings); nothing is sent without you."
        actions={
          <Field label="Quarter">
            <Select value={period} onChange={(e) => setPeriod(e.target.value)}>
              {periods.data.periods.map((p) => <option key={p.id}>{p.id}</option>)}
            </Select>
          </Field>
        }
      />
      <ErrorBox error={act.error} />
      {preview.flats.length === 0 && <Notice tone="good">No flat has pending dues. Nothing to remind.</Notice>}

      <div className="grid gap-4 lg:grid-cols-3">
        <Card
          className="lg:col-span-2"
          title={draft ? `Draft reminder #${draft.seq}` : `Preview reminder #${preview.seq}`}
          actions={draft ? <Badge tone="warn">draft · {fmtDate(draft.created_at)}</Badge> : <Badge>not saved</Badge>}
        >
          <Textarea rows={18} value={text} onChange={(e) => setText(e.target.value)} />
          <div className="mt-3 flex flex-wrap gap-2">
            <CopyButton text={text} label="Copy for WhatsApp" />
            <a className="inline-flex items-center rounded-md border border-line bg-white px-3.5 py-2 text-sm font-medium text-ink hover:bg-neutral-50" href={wa} target="_blank" rel="noreferrer">
              Open in WhatsApp
            </a>
            <Button
              onClick={async () => {
                const r = await act.run(() => api.post<Reminder>(`/reminders?period=${period}`));
                if (r) history.reload();
              }}
            >
              Refresh from latest dues
            </Button>
            <Button
              variant="primary"
              disabled={act.busy || preview.flats.length === 0}
              onClick={async () => {
                const id = draft?.id ?? (await act.run(() => api.post<Reminder>(`/reminders?period=${period}`)))?.id;
                if (id && (await act.run(() => api.post(`/reminders/${id}/sent`, { text })))) history.reload();
              }}
            >
              Mark as sent
            </Button>
          </div>
        </Card>
        <Card title={`Pending flats (${preview.flats.length})`}>
          <FlatsByFloor codes={preview.flats} />
        </Card>
      </div>

      <Card className="mt-4" title="Sent reminders">
        {!history.data ? <Loading /> : history.data.filter((r) => r.status === "sent").length === 0 ? <Empty>None sent for {period} yet.</Empty> : (
          <div className="space-y-3">
            {history.data.filter((r) => r.status === "sent").map((r) => (
              <details key={r.id} className="rounded border border-line p-3">
                <summary className="cursor-pointer text-sm">
                  <span className="font-medium">Reminder #{r.seq}</span> · sent {fmtDate(r.sent_at)} · {r.flats.length} flats: {r.flats.join(", ")}
                </summary>
                <pre className="mt-2 whitespace-pre-wrap font-mono text-xs text-neutral-700">{r.text}</pre>
              </details>
            ))}
          </div>
        )}
      </Card>
    </>
  );
}
