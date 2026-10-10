"use client";
import { useEffect, useMemo, useRef, useState } from "react";
import { api, ApplyResult, Changeset, DataChange, InRow, PlanResult, SheetColumn, SheetData } from "@/lib/api";
import { Badge, Button, Card, Empty, ErrorBox, Input, LinkButton, Loading, Modal, Notice, PageHeader, cx, inputCls, useAction } from "@/components/ui";

const SHEETS: { name: string; label: string }[] = [
  { name: "ledger", label: "Dues & payments" },
  { name: "bank", label: "Bank links" },
  { name: "expenses", label: "Expenses" },
  { name: "income", label: "Other income" },
  { name: "flats", label: "Flats" },
  { name: "quarters", label: "Quarters" },
];

const PAGE = 100;
const NEW_ROW_BASE = 100000; // grid row numbers for added rows, so errors map back

/** Pending edits for one sheet: changed values by row index, added rows, deleted row indexes. */
type Draft = { edits: Record<number, Record<string, string>>; added: Record<string, string>[]; deleted: Set<number> };
const emptyDraft = (): Draft => ({ edits: {}, added: [], deleted: new Set() });

function draftCount(d?: Draft) {
  if (!d) return 0;
  return Object.keys(d.edits).length + d.added.length + d.deleted.size;
}

function toChangeset(sheets: Record<string, SheetData>, drafts: Record<string, Draft>): Changeset {
  const cs: Changeset = {};
  for (const [name, d] of Object.entries(drafts)) {
    const sh = sheets[name];
    if (!sh || draftCount(d) === 0) continue;
    const rows: InRow[] = [];
    for (const [i, vals] of Object.entries(d.edits)) {
      const r = sh.rows[+i];
      if (!d.deleted.has(+i)) rows.push({ row: +i + 1, values: { ...r.values, ...vals }, _v: r._v });
    }
    d.deleted.forEach((i) => rows.push({ row: i + 1, values: sh.rows[i].values, _v: sh.rows[i]._v, delete: true }));
    d.added.forEach((vals, i) => rows.push({ row: NEW_ROW_BASE + i, values: vals }));
    cs[name] = rows;
  }
  return cs;
}

export default function BulkEditPage() {
  const [tab, setTab] = useState("ledger");
  const [sheets, setSheets] = useState<Record<string, SheetData>>({});
  const [drafts, setDrafts] = useState<Record<string, Draft>>({});
  const [loadErr, setLoadErr] = useState<string | null>(null);
  const [plan, setPlan] = useState<{ result: PlanResult; source: "grid" | File } | null>(null);
  const [done, setDone] = useState<ApplyResult | null>(null);
  const act = useAction();
  const fileRef = useRef<HTMLInputElement>(null);

  async function load(name: string) {
    try {
      const d = await api.get<SheetData>(`/data/sheets/${name}`);
      setSheets((s) => ({ ...s, [name]: d }));
      setLoadErr(null);
    } catch (e) {
      setLoadErr((e as Error).message);
    }
  }
  useEffect(() => {
    if (!sheets[tab]) load(tab);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [tab]);

  const pending = Object.values(drafts).reduce((n, d) => n + draftCount(d), 0);
  const draft = drafts[tab] ?? emptyDraft();
  const setDraft = (d: Draft) => setDrafts({ ...drafts, [tab]: d });

  // errors from the last grid preview, by sheet and grid row
  const rowErrors = useMemo(() => {
    const m: Record<string, Record<number, DataChange>> = {};
    if (plan && plan.source === "grid") for (const c of plan.result.changes) if (c.error) (m[c.sheet] ??= {})[c.row] = c;
    return m;
  }, [plan]);

  async function previewGrid() {
    const r = await act.run(() => api.post<PlanResult>("/data/preview", toChangeset(sheets, drafts)));
    if (r) setPlan({ result: r, source: "grid" });
  }

  async function previewFile(f: File) {
    setDone(null);
    const r = await act.run(() => api.upload<PlanResult>("/data/preview", f));
    if (r) setPlan({ result: r, source: f });
  }

  async function apply() {
    if (!plan) return;
    const src = plan.source;
    const r = await act.run(() =>
      src === "grid" ? api.post<ApplyResult>("/data/apply", toChangeset(sheets, drafts)) : api.upload<ApplyResult>("/data/apply", src),
    );
    if (!r) return;
    setDone(r);
    setPlan(null);
    if (src === "grid") setDrafts({});
    setSheets({});
    load(tab);
  }

  const sh = sheets[tab];

  return (
    <div className="pb-20">
      <PageHeader
        title="Bulk edit"
        sub="Add, change or delete flats, quarters, dues & payments, expenses, other income and bank links – right here, or offline in Excel. Every save shows you the changes first and takes a backup."
        actions={
          <>
            <LinkButton href="/api/data/workbook.xlsx" download>
              Download Excel
            </LinkButton>
            <Button
              variant="primary"
              onClick={() => fileRef.current?.click()}
              disabled={act.busy || pending > 0}
              title={pending > 0 ? "Save or discard the edits made here first" : undefined}
            >
              Upload edited Excel
            </Button>
            <input
              ref={fileRef}
              type="file"
              accept=".xlsx"
              className="hidden"
              onChange={(e) => {
                const f = e.target.files?.[0];
                e.target.value = "";
                if (f) previewFile(f);
              }}
            />
          </>
        }
      />

      {done && (
        <Notice tone="good">
          Saved {done.applied} change{done.applied === 1 ? "" : "s"}. Backup taken first: <span className="font-mono text-xs">{done.backup}</span>
          {done.gone > 0 && ` · ${done.gone} unedited rows had already been deleted in the portal and were skipped.`}
        </Notice>
      )}
      {!plan && <ErrorBox error={act.error} />}
      <ErrorBox error={loadErr} />

      <div className="mb-3 flex flex-wrap gap-1 border-b border-line">
        {SHEETS.map((s) => {
          const n = draftCount(drafts[s.name]);
          return (
            <button
              key={s.name}
              onClick={() => setTab(s.name)}
              className={cx(
                "-mb-px rounded-t-md border px-3 py-2 text-sm",
                tab === s.name ? "border-line border-b-white bg-white font-medium text-ink" : "border-transparent text-neutral-600 hover:text-ink",
              )}
            >
              {s.label}
              {n > 0 && <span className="ml-1.5 rounded-full bg-amber-100 px-1.5 text-[11px] font-semibold text-warn">{n}</span>}
            </button>
          );
        })}
      </div>

      {!sh ? <Loading /> : <Grid key={tab} sheet={sh} draft={draft} setDraft={setDraft} errors={rowErrors[tab] ?? {}} />}

      {pending > 0 && (
        <div className="fixed inset-x-0 bottom-0 z-40 border-t border-line bg-white/95 px-4 py-3 backdrop-blur md:left-60">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <span className="text-sm text-neutral-700">
              {pending} unsaved edit{pending === 1 ? "" : "s"}
              {Object.entries(drafts).filter(([, d]) => draftCount(d) > 0).length > 1 && " across sheets"}
            </span>
            <div className="flex gap-2">
              <Button variant="ghost" onClick={() => { setDrafts({}); setPlan(null); }}>
                Discard
              </Button>
              <Button variant="primary" onClick={previewGrid} disabled={act.busy}>
                Review & save
              </Button>
            </div>
          </div>
        </div>
      )}

      <Modal open={!!plan} onClose={() => setPlan(null)} title={plan?.source === "grid" ? "Review changes" : `Review ${(plan?.source as File)?.name ?? ""}`} wide>
        {plan && <Review result={plan.result} fromFile={plan.source !== "grid"} />}
        <ErrorBox error={act.error} />
        <div className="mt-4 flex justify-end gap-2">
          <Button onClick={() => setPlan(null)}>{plan?.result.errors ? "Back to fix" : "Cancel"}</Button>
          <Button variant="primary" disabled={act.busy || !plan || plan.result.errors > 0 || plan.result.changes.length === 0} onClick={apply}>
            {plan && plan.result.changes.length > 0 ? `Save ${plan.result.changes.length} change${plan.result.changes.length === 1 ? "" : "s"}` : "Nothing to save"}
          </Button>
        </div>
      </Modal>
    </div>
  );
}

// ---- grid -------------------------------------------------------------------------

function Grid({ sheet, draft, setDraft, errors }: { sheet: SheetData; draft: Draft; setDraft: (d: Draft) => void; errors: Record<number, DataChange> }) {
  const [q, setQ] = useState("");
  const [onlyEdited, setOnlyEdited] = useState(false);
  const [limit, setLimit] = useState(PAGE);
  const cols = sheet.columns;

  const visible = useMemo(() => {
    const needle = q.trim().toLowerCase();
    const out: number[] = [];
    sheet.rows.forEach((r, i) => {
      if (onlyEdited && !draft.edits[i] && !draft.deleted.has(i) && !errors[i + 1]) return;
      if (needle) {
        const vals = { ...r.values, ...draft.edits[i] };
        if (!Object.values(vals).some((v) => v.toLowerCase().includes(needle))) return;
      }
      out.push(i);
    });
    return out;
  }, [sheet, q, onlyEdited, draft, errors]);

  function setCell(i: number, key: string, v: string) {
    const orig = sheet.rows[i].values[key] ?? "";
    const row = { ...draft.edits[i], [key]: v };
    if (v === orig) delete row[key];
    const edits = { ...draft.edits };
    if (Object.keys(row).length) edits[i] = row;
    else delete edits[i];
    setDraft({ ...draft, edits });
  }
  function toggleDelete(i: number) {
    const deleted = new Set(draft.deleted);
    if (deleted.has(i)) deleted.delete(i);
    else deleted.add(i);
    setDraft({ ...draft, deleted });
  }
  function addRow() {
    setDraft({ ...draft, added: [{}, ...draft.added] });
  }
  function setNew(j: number, key: string, v: string) {
    const added = draft.added.slice();
    added[j] = { ...added[j], [key]: v };
    setDraft({ ...draft, added });
  }
  function dropNew(j: number) {
    setDraft({ ...draft, added: draft.added.filter((_, k) => k !== j) });
  }

  return (
    <Card>
      <p className="mb-3 text-sm text-neutral-600">{sheet.help}</p>
      <div className="mb-3 flex flex-wrap items-center gap-2">
        <Input className="max-w-xs" placeholder="Search this sheet…" value={q} onChange={(e) => { setQ(e.target.value); setLimit(PAGE); }} />
        <label className="flex items-center gap-1.5 text-sm text-neutral-600">
          <input type="checkbox" checked={onlyEdited} onChange={(e) => setOnlyEdited(e.target.checked)} /> Edited rows only
        </label>
        <span className="text-xs text-neutral-500">{visible.length} of {sheet.rows.length} rows</span>
        <div className="flex-1" />
        {sheet.can_add && (
          <Button size="sm" onClick={addRow}>
            + Add row
          </Button>
        )}
      </div>
      <div className="overflow-x-auto">
        <table className="min-w-full border-separate border-spacing-0 text-sm">
          <thead>
            <tr>
              {cols.map((c) => (
                <th
                  key={c.key}
                  title={c.help}
                  className={cx("sticky top-0 border-b border-line bg-white px-1.5 py-2 text-left text-xs font-semibold text-neutral-500", c.help && "cursor-help underline decoration-dotted")}
                >
                  {c.header}
                  {c.required && !c.read_only && <span className="text-bad">*</span>}
                </th>
              ))}
              {sheet.can_delete && <th className="border-b border-line px-1.5 py-2 text-left text-xs font-semibold text-neutral-500">Delete</th>}
            </tr>
          </thead>
          <tbody>
            {draft.added.map((vals, j) => (
              <RowView key={`n${j}`} cols={cols} keyCol={sheet.key} values={vals} isNew error={errors[NEW_ROW_BASE + j]?.error} onChange={(k, v) => setNew(j, k, v)}
                trailing={<Button size="sm" variant="ghost" onClick={() => dropNew(j)}>Remove</Button>} showTrailing={sheet.can_delete} />
            ))}
            {visible.slice(0, limit).map((i) => {
              const r = sheet.rows[i];
              const del = draft.deleted.has(i);
              return (
                <RowView
                  key={i}
                  cols={cols}
                  keyCol={sheet.key}
                  values={{ ...r.values, ...draft.edits[i] }}
                  original={r.values}
                  locked={r.locked}
                  deleted={del}
                  error={errors[i + 1]?.error}
                  onChange={(k, v) => setCell(i, k, v)}
                  showTrailing={sheet.can_delete}
                  trailing={
                    !r.locked && (
                      <input type="checkbox" className="ml-3 accent-red-700" checked={del} onChange={() => toggleDelete(i)} aria-label="Delete row" />
                    )
                  }
                />
              );
            })}
          </tbody>
        </table>
        {visible.length === 0 && draft.added.length === 0 && <Empty>No rows.</Empty>}
      </div>
      {visible.length > limit && (
        <div className="mt-3 text-center">
          <Button size="sm" onClick={() => setLimit(limit + PAGE)}>
            Show {Math.min(PAGE, visible.length - limit)} more
          </Button>
        </div>
      )}
    </Card>
  );
}

function RowView({
  cols, keyCol, values, original, locked, deleted, isNew, error, onChange, trailing, showTrailing,
}: {
  cols: SheetColumn[]; keyCol: string; values: Record<string, string>; original?: Record<string, string>; locked?: string; deleted?: boolean; isNew?: boolean;
  error?: string; onChange: (k: string, v: string) => void; trailing?: React.ReactNode; showTrailing: boolean;
}) {
  return (
    <>
      <tr className={cx(deleted && "bg-red-50 line-through decoration-red-400", isNew && "bg-green-50/60", locked && "text-neutral-500")} title={locked}>
        {cols.map((c) => {
          const v = values[c.key] ?? "";
          const changed = original && !c.read_only && v !== (original[c.key] ?? "");
          const ro = c.read_only || !!locked || deleted || (!isNew && c.key === keyCol); // keys can't be renamed
          const w = colWidth(c, ro);
          return (
            <td key={c.key} className="border-b border-line/60 px-1 py-1 align-top">
              {ro ? (
                <div className={cx("truncate px-1.5 py-1", c.type === "money" && "num text-right")} style={{ width: w }} title={v}>
                  {c.key === "id" && locked ? "🔒 " : ""}
                  {v || <span className="text-neutral-300">—</span>}
                </div>
              ) : (
                <Cell col={c} value={v} width={w} changed={!!changed} onChange={(x) => onChange(c.key, x)} />
              )}
            </td>
          );
        })}
        {showTrailing && <td className="border-b border-line/60 px-1 py-1 align-middle no-underline">{trailing}</td>}
      </tr>
      {error && (
        <tr>
          <td colSpan={cols.length + 1} className="border-b border-red-200 bg-red-50 px-2 py-1 text-xs text-bad">
            {error}
          </td>
        </tr>
      )}
    </>
  );
}

/** Pixel width of a column; editable dates and dropdowns need room for the browser's controls. */
function colWidth(c: SheetColumn, ro: boolean) {
  const w = Math.max(56, (c.width ?? 12) * 8);
  if (c.type === "date") return ro ? Math.max(w, 92) : Math.max(w, 140);
  if ((c.type === "enum" || c.type === "bool") && !ro) return Math.max(w, 120);
  return w;
}

function Cell({ col, value, width, changed, onChange }: { col: SheetColumn; value: string; width: number; changed: boolean; onChange: (v: string) => void }) {
  const cls = cx(inputCls, "px-1.5 py-1", changed && "border-amber-400 bg-amber-50", col.type === "money" && "text-right");
  if (col.type === "enum" || col.type === "bool") {
    const opts = col.type === "bool" ? ["yes", "no"] : col.options ?? [];
    return (
      <select className={cls} style={{ width }} value={value} onChange={(e) => onChange(e.target.value)}>
        <option value="">{col.required ? "Choose…" : "—"}</option>
        {opts.map((o) => (
          <option key={o} value={o}>
            {o.replace(/_/g, " ")}
          </option>
        ))}
        {value && !opts.includes(value) && <option value={value}>{value}</option>}
      </select>
    );
  }
  return (
    <input
      className={cls}
      style={{ width }}
      value={value}
      type={col.type === "date" ? "date" : "text"}
      inputMode={col.type === "money" || col.type === "number" ? "decimal" : undefined}
      onChange={(e) => onChange(col.type === "code" ? e.target.value.toUpperCase() : e.target.value)}
    />
  );
}

// ---- review -----------------------------------------------------------------------

const ACTION_TONE = { add: "good", update: "info", delete: "bad" } as const;

function Review({ result, fromFile }: { result: PlanResult; fromFile: boolean }) {
  const bySheet = useMemo(() => {
    const m: Record<string, DataChange[]> = {};
    for (const c of result.changes) (m[c.sheet] ??= []).push(c);
    return m;
  }, [result]);
  const count = (a: string) => result.changes.filter((c) => c.action === a && !c.error).length;
  return (
    <div>
      {result.errors > 0 ? (
        <Notice tone="warn">
          {result.errors} row{result.errors === 1 ? " has a problem" : "s have problems"}. Nothing will be saved until {result.errors === 1 ? "it is" : "they are"} fixed.
        </Notice>
      ) : result.changes.length === 0 ? (
        <Notice>No changes found{result.unchanged ? ` – ${result.unchanged} rows match what's already saved` : ""}.</Notice>
      ) : (
        <Notice>
          {count("add")} to add, {count("update")} to change, {count("delete")} to delete
          {result.unchanged > 0 && ` · ${result.unchanged} unchanged rows ignored`}
          {result.gone > 0 && ` · ${result.gone} rows already deleted in the portal`}.
        </Notice>
      )}
      <div className="max-h-[55vh] space-y-4 overflow-y-auto pr-1">
        {SHEETS.filter((s) => bySheet[s.name]).map((s) => (
          <div key={s.name}>
            <h4 className="mb-1.5 text-xs font-semibold uppercase tracking-wide text-neutral-500">{s.label}</h4>
            <ul className="space-y-1.5">
              {bySheet[s.name].map((c, i) => (
                <li key={i} className={cx("rounded-md border px-3 py-2 text-sm", c.error ? "border-red-200 bg-red-50" : "border-line")}>
                  <div className="flex flex-wrap items-center gap-2">
                    <Badge tone={c.error ? "bad" : ACTION_TONE[c.action]}>{c.action}</Badge>
                    <span className="font-medium text-ink">{c.label}</span>
                    {fromFile && c.row > 0 && <span className="text-xs text-neutral-400">row {c.row}</span>}
                  </div>
                  {c.fields && c.fields.length > 0 && (
                    <ul className="mt-1 space-y-0.5 text-xs text-neutral-600">
                      {c.fields.map((f) => (
                        <li key={f.column}>
                          {f.column}: <span className="text-neutral-400 line-through">{f.from || "blank"}</span> → <span className="font-medium text-ink">{f.to || "blank"}</span>
                        </li>
                      ))}
                    </ul>
                  )}
                  {c.error && <p className="mt-1 text-xs text-bad">{c.error}</p>}
                  {c.warnings?.map((w) => (
                    <p key={w} className="mt-1 text-xs text-warn">
                      {w}
                    </p>
                  ))}
                </li>
              ))}
            </ul>
          </div>
        ))}
      </div>
    </div>
  );
}
