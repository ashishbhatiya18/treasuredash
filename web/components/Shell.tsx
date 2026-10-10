"use client";
import Link from "next/link";
import { usePathname } from "next/navigation";
import { ReactNode, useEffect, useState } from "react";
import { api } from "@/lib/api";
import { cx } from "./ui";
import { Logo } from "./Logo";

const NAV = [
  { href: "/", label: "Dashboard" },
  { href: "/billing", label: "Billing & invoices" },
  { href: "/bank", label: "Bank reconciliation" },
  { href: "/receipts", label: "Receipts register" },
  { href: "/deposits", label: "FD / RD deposits" },
  { href: "/flats", label: "Flats & dues" },
  { href: "/expenses", label: "Expenses & income" },
  { href: "/reminders", label: "Reminders" },
  { href: "/reports", label: "P&L & sinking fund" },
  { href: "/data", label: "Bulk edit (Excel)" },
  { href: "/settings", label: "Settings & import" },
];

// No login here: access is guarded in front of the app (webauthn-proxy).
export default function Shell({ children }: { children: ReactNode }) {
  const path = usePathname();
  const [open, setOpen] = useState(false);
  const [org, setOrg] = useState("");

  useEffect(() => {
    api
      .get<{ settings: Record<string, string> }>("/settings")
      .then((r) => setOrg(r.settings.association_name ?? ""))
      .catch(() => {});
  }, []);

  return (
    <div className="min-h-screen md:flex">
      <nav className="border-b border-line bg-ink text-white md:fixed md:inset-y-0 md:flex md:w-60 md:flex-col md:border-b-0">
        <div className="flex items-center justify-between px-4 py-4">
          <Link href="/" className="flex items-center gap-2.5">
            <Logo className="h-9 w-9 shrink-0" />
            <div className="min-w-0">
              <div className="text-[16px] font-bold leading-tight tracking-tight">
                Treasure <span className="text-gold">Dash</span>
              </div>
              {org && <div className="truncate text-[11px] text-white/55">{org}</div>}
            </div>
          </Link>
          <button className="text-sm text-white/80 md:hidden" onClick={() => setOpen(!open)}>
            Menu
          </button>
        </div>
        <ul className={cx("flex-1 px-2 pb-4 md:block", open ? "block" : "hidden")}>
          {NAV.map((n) => {
            const active = n.href === "/" ? path === "/" : path.startsWith(n.href);
            return (
              <li key={n.href}>
                <Link
                  href={n.href}
                  onClick={() => setOpen(false)}
                  className={cx(
                    "block rounded-md border-l-2 px-3 py-2 text-sm",
                    active ? "border-gold bg-white/15 font-medium text-white" : "border-transparent text-white/75 hover:bg-white/10 hover:text-white",
                  )}
                >
                  {n.label}
                </Link>
              </li>
            );
          })}
        </ul>
        <div className="hidden px-5 pb-4 text-[10px] uppercase tracking-widest text-white/30 md:block">Treasure Dash</div>
      </nav>
      <main className="min-w-0 flex-1 px-4 py-6 md:ml-60 md:px-8">{children}</main>
    </div>
  );
}
