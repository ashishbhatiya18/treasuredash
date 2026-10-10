"use client";
import { ReactNode, useEffect, useState } from "react";

export function cx(...c: (string | false | null | undefined)[]) {
  return c.filter(Boolean).join(" ");
}

export function PageHeader({ title, sub, actions }: { title: string; sub?: ReactNode; actions?: ReactNode }) {
  return (
    <div className="mb-6 flex flex-wrap items-end justify-between gap-3">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight text-ink">{title}</h1>
        {sub && <p className="mt-1 text-sm text-neutral-600">{sub}</p>}
      </div>
      {actions && <div className="no-print flex flex-wrap gap-2">{actions}</div>}
    </div>
  );
}

export function Card({ title, actions, children, className }: { title?: ReactNode; actions?: ReactNode; children: ReactNode; className?: string }) {
  return (
    <section className={cx("rounded-lg border border-line bg-white", className)}>
      {(title || actions) && (
        <header className="flex items-center justify-between gap-2 border-b border-line px-4 py-3">
          <h2 className="text-sm font-semibold text-ink">{title}</h2>
          {actions && <div className="flex gap-2">{actions}</div>}
        </header>
      )}
      <div className="p-4">{children}</div>
    </section>
  );
}

export function Stat({ label, value, hint, tone }: { label: string; value: ReactNode; hint?: ReactNode; tone?: "good" | "warn" | "bad" }) {
  return (
    <div className="rounded-lg border border-line bg-white px-4 py-3">
      <div className="text-xs font-medium uppercase tracking-wide text-neutral-500">{label}</div>
      <div className={cx("num mt-1 text-2xl font-semibold", tone === "good" && "text-good", tone === "warn" && "text-warn", tone === "bad" && "text-bad", !tone && "text-ink")}>
        {value}
      </div>
      {hint && <div className="mt-0.5 text-xs text-neutral-500">{hint}</div>}
    </div>
  );
}

type BtnProps = React.ButtonHTMLAttributes<HTMLButtonElement> & { variant?: "primary" | "secondary" | "ghost" | "danger"; size?: "sm" | "md" };
export function Button({ variant = "secondary", size = "md", className, ...p }: BtnProps) {
  return (
    <button
      {...p}
      className={cx(
        "inline-flex items-center justify-center gap-1.5 rounded-md font-medium transition disabled:cursor-not-allowed disabled:opacity-50",
        size === "sm" ? "px-2.5 py-1 text-xs" : "px-3.5 py-2 text-sm",
        variant === "primary" && "bg-ink text-white hover:bg-ink-soft",
        variant === "secondary" && "border border-line bg-white text-ink hover:bg-neutral-50",
        variant === "ghost" && "text-ink-soft hover:bg-neutral-100",
        variant === "danger" && "border border-red-200 bg-white text-bad hover:bg-red-50",
        className,
      )}
    />
  );
}

export function LinkButton({ href, children, variant = "secondary", download }: { href: string; children: ReactNode; variant?: "primary" | "secondary"; download?: boolean }) {
  return (
    <a
      href={href}
      download={download}
      className={cx(
        "inline-flex items-center gap-1.5 rounded-md px-3.5 py-2 text-sm font-medium",
        variant === "primary" ? "bg-ink text-white hover:bg-ink-soft" : "border border-line bg-white text-ink hover:bg-neutral-50",
      )}
    >
      {children}
    </a>
  );
}

export function Badge({ children, tone = "neutral" }: { children: ReactNode; tone?: "neutral" | "good" | "warn" | "bad" | "info" }) {
  return (
    <span
      className={cx(
        "inline-flex items-center rounded px-1.5 py-0.5 text-[11px] font-semibold uppercase tracking-wide",
        tone === "neutral" && "bg-neutral-100 text-neutral-600",
        tone === "good" && "bg-green-50 text-good",
        tone === "warn" && "bg-amber-50 text-warn",
        tone === "bad" && "bg-red-50 text-bad",
        tone === "info" && "bg-blue-50 text-ink-soft",
      )}
    >
      {children}
    </span>
  );
}

export function Field({ label, children, hint }: { label: string; children: ReactNode; hint?: ReactNode }) {
  return (
    <label className="block">
      <span className="mb-1 block text-xs font-medium text-neutral-600">{label}</span>
      {children}
      {hint && <span className="mt-1 block text-xs text-neutral-500">{hint}</span>}
    </label>
  );
}

export const inputCls =
  "w-full rounded-md border border-line bg-white px-2.5 py-1.5 text-sm outline-none focus:border-ink-soft focus:ring-2 focus:ring-ink/10";

export function Input(p: React.ComponentProps<"input">) {
  return <input {...p} className={cx(inputCls, p.className)} />;
}

export function Select(p: React.SelectHTMLAttributes<HTMLSelectElement>) {
  return <select {...p} className={cx(inputCls, p.className)} />;
}

export function Textarea(p: React.TextareaHTMLAttributes<HTMLTextAreaElement>) {
  return <textarea {...p} className={cx(inputCls, "font-mono", p.className)} />;
}

export function Modal({ open, onClose, title, children, wide }: { open: boolean; onClose: () => void; title: string; children: ReactNode; wide?: boolean }) {
  useEffect(() => {
    const k = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    window.addEventListener("keydown", k);
    return () => window.removeEventListener("keydown", k);
  }, [onClose]);
  if (!open) return null;
  return (
    <div className="fixed inset-0 z-50 flex items-start justify-center overflow-y-auto bg-black/30 p-4 pt-[8vh]" onMouseDown={onClose}>
      <div className={cx("w-full rounded-lg bg-white shadow-xl", wide ? "max-w-3xl" : "max-w-lg")} onMouseDown={(e) => e.stopPropagation()}>
        <div className="flex items-center justify-between border-b border-line px-5 py-3">
          <h3 className="font-semibold text-ink">{title}</h3>
          <button onClick={onClose} className="text-xl leading-none text-neutral-400 hover:text-neutral-700" aria-label="Close">
            ×
          </button>
        </div>
        <div className="p-5">{children}</div>
      </div>
    </div>
  );
}

export function ErrorBox({ error }: { error: string | null | undefined }) {
  if (!error) return null;
  return <div className="mb-4 rounded-md border border-red-200 bg-red-50 px-3 py-2 text-sm text-bad">{error}</div>;
}

export function Notice({ children, tone = "info" }: { children: ReactNode; tone?: "info" | "good" | "warn" }) {
  return (
    <div
      className={cx(
        "mb-4 rounded-md border px-3 py-2 text-sm",
        tone === "info" && "border-blue-100 bg-blue-50 text-ink-soft",
        tone === "good" && "border-green-200 bg-green-50 text-good",
        tone === "warn" && "border-amber-200 bg-amber-50 text-warn",
      )}
    >
      {children}
    </div>
  );
}

export function Loading() {
  return <div className="py-10 text-center text-sm text-neutral-500">Loading…</div>;
}

export function Empty({ children }: { children: ReactNode }) {
  return <div className="py-8 text-center text-sm text-neutral-500">{children}</div>;
}

export function Th({ children, right, className }: { children?: ReactNode; right?: boolean; className?: string }) {
  return (
    <th className={cx("border-b border-line px-3 py-2 text-xs font-semibold uppercase tracking-wide text-neutral-500", right ? "text-right" : "text-left", className)}>
      {children}
    </th>
  );
}

export function Td({ children, right, className, mono }: { children?: ReactNode; right?: boolean; className?: string; mono?: boolean }) {
  return <td className={cx("border-b border-line/70 px-3 py-2 align-top text-sm", right && "num text-right", mono && "font-mono text-xs", className)}>{children}</td>;
}

/** Runs an async action with a busy flag and error capture. */
export function useAction() {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  async function run<T>(fn: () => Promise<T>): Promise<T | undefined> {
    setBusy(true);
    setError(null);
    try {
      return await fn();
    } catch (e) {
      setError((e as Error).message);
      return undefined;
    } finally {
      setBusy(false);
    }
  }
  return { busy, error, setError, run };
}

export function CopyButton({ text, label = "Copy" }: { text: string; label?: string }) {
  const [done, setDone] = useState(false);
  return (
    <Button
      variant="primary"
      onClick={async () => {
        await navigator.clipboard.writeText(text);
        setDone(true);
        setTimeout(() => setDone(false), 1500);
      }}
    >
      {done ? "Copied ✓" : label}
    </Button>
  );
}
