"use client";
import { useState } from "react";
import { FundQuarter, money } from "@/lib/api";

// Two single-axis charts sharing the quarter axis: fund balance (level) and
// quarterly surplus/deficit (change). They differ in scale by ~10×, so they are
// never combined on a dual axis.

const BLUE = "#2a78d6"; // series / surplus
const RED = "#e34948"; // deficit (diverging pole)
const W = 640;
const PAD = { l: 64, r: 12, t: 12, b: 26 };

function compact(p: number) {
  const r = p / 100;
  const a = Math.abs(r);
  if (a >= 1e5) return `${r < 0 ? "−" : ""}₹${(a / 1e5).toFixed(a >= 1e6 ? 1 : 2)}L`;
  if (a >= 1e3) return `${r < 0 ? "−" : ""}₹${(a / 1e3).toFixed(0)}k`;
  return `${r < 0 ? "−" : ""}₹${a.toFixed(0)}`;
}

function niceTicks(min: number, max: number, n = 4) {
  const span = max - min || 1;
  const step0 = span / n;
  const mag = Math.pow(10, Math.floor(Math.log10(step0)));
  const step = [1, 2, 2.5, 5, 10].map((m) => m * mag).find((s) => s >= step0) ?? step0;
  const lo = Math.floor(min / step) * step;
  const hi = Math.ceil(max / step) * step;
  const out: number[] = [];
  for (let v = lo; v <= hi + step / 2; v += step) out.push(v);
  return out;
}

function Tooltip({ x, y, h, children }: { x: number; y: number; h: number; children: React.ReactNode }) {
  return (
    <div
      className="pointer-events-none absolute z-10 -translate-x-1/2 -translate-y-full rounded-md border border-line bg-white px-2.5 py-1.5 text-xs shadow-md"
      style={{ left: `${(x / W) * 100}%`, top: `calc(${(y / h) * 100}% - 8px)` }}
    >
      {children}
    </div>
  );
}

export function FundBalanceChart({ quarters }: { quarters: FundQuarter[] }) {
  const [hover, setHover] = useState<number | null>(null);
  const H = 200;
  if (quarters.length === 0) return null;
  const vals = quarters.map((q) => q.balance);
  const ticks = niceTicks(Math.min(...vals) * 0.97, Math.max(...vals) * 1.04);
  const y0 = ticks[0], y1 = ticks[ticks.length - 1];
  const iw = W - PAD.l - PAD.r, ih = H - PAD.t - PAD.b;
  // same quarter slots as the surplus bars below, so the two charts line up
  const slot = iw / quarters.length;
  const x = (i: number) => PAD.l + slot * i + slot / 2;
  const y = (v: number) => PAD.t + ih - ((v - y0) / (y1 - y0)) * ih;
  const path = quarters.map((q, i) => `${i ? "L" : "M"}${x(i)},${y(q.balance)}`).join(" ");
  const last = quarters.length - 1;
  return (
    <div className="relative" onMouseLeave={() => setHover(null)}>
      <svg viewBox={`0 0 ${W} ${H}`} className="w-full" role="img" aria-label="Sinking fund balance by quarter">
        {ticks.map((t) => (
          <g key={t}>
            <line x1={PAD.l} x2={W - PAD.r} y1={y(t)} y2={y(t)} stroke="#e7e5df" strokeWidth={1} />
            <text x={PAD.l - 8} y={y(t)} textAnchor="end" dominantBaseline="middle" className="fill-neutral-500" fontSize={11}>{compact(t)}</text>
          </g>
        ))}
        <path d={path} fill="none" stroke={BLUE} strokeWidth={2} strokeLinejoin="round" />
        {hover !== null && <line x1={x(hover)} x2={x(hover)} y1={PAD.t} y2={PAD.t + ih} stroke="#9a978f" strokeDasharray="3 3" />}
        {quarters.map((q, i) => (
          <g key={q.period_id}>
            <circle
              cx={x(i)} cy={y(q.balance)} r={hover === i || i === last ? 5 : 4}
              fill={q.running ? "#fff" : BLUE} stroke={q.running ? BLUE : "#fff"} strokeWidth={2}
            />
            <text x={x(i)} y={H - 8} textAnchor="middle" fontSize={11} className={i === last ? "fill-neutral-800 font-semibold" : "fill-neutral-500"}>
              {q.period_id}
            </text>
            {/* generous hit target */}
            <rect x={x(i) - slot / 2} y={PAD.t} width={slot} height={ih} fill="transparent" onMouseEnter={() => setHover(i)} />
          </g>
        ))}
        <text x={x(last)} y={y(quarters[last].balance) - 12} textAnchor="middle" fontSize={11} className="fill-neutral-800 font-semibold">
          {compact(quarters[last].balance)}
        </text>
      </svg>
      {hover !== null && (
        <Tooltip x={x(hover)} y={y(quarters[hover].balance)} h={H}>
          <div className="font-semibold">{quarters[hover].period_id}{quarters[hover].running && " (running)"}</div>
          <div>Fund balance <span className="num font-medium">{money(quarters[hover].balance)}</span></div>
        </Tooltip>
      )}
    </div>
  );
}

export function SurplusBars({ quarters }: { quarters: FundQuarter[] }) {
  const [hover, setHover] = useState<number | null>(null);
  const H = 150;
  if (quarters.length === 0) return null;
  const vals = quarters.map((q) => q.surplus);
  const ticks = niceTicks(Math.min(0, ...vals), Math.max(0, ...vals), 3);
  const y0 = ticks[0], y1 = ticks[ticks.length - 1];
  const iw = W - PAD.l - PAD.r, ih = H - PAD.t - PAD.b;
  const slot = iw / quarters.length;
  const bw = Math.min(36, slot * 0.55);
  const y = (v: number) => PAD.t + ih - ((v - y0) / (y1 - y0)) * ih;
  const cx = (i: number) => PAD.l + slot * i + slot / 2;
  return (
    <div className="relative" onMouseLeave={() => setHover(null)}>
      <svg viewBox={`0 0 ${W} ${H}`} className="w-full" role="img" aria-label="Quarterly surplus or deficit">
        {ticks.map((t) => (
          <g key={t}>
            <line x1={PAD.l} x2={W - PAD.r} y1={y(t)} y2={y(t)} stroke={t === 0 ? "#9a978f" : "#e7e5df"} strokeWidth={1} />
            <text x={PAD.l - 8} y={y(t)} textAnchor="end" dominantBaseline="middle" className="fill-neutral-500" fontSize={11}>{compact(t)}</text>
          </g>
        ))}
        {quarters.map((q, i) => {
          const top = y(Math.max(0, q.surplus)), bot = y(Math.min(0, q.surplus));
          const h = Math.max(1, bot - top);
          const pos = q.surplus >= 0;
          const r = Math.min(4, h);
          // rounded only at the data end, square at the zero baseline
          const d = pos
            ? `M${cx(i) - bw / 2},${bot} V${top + r} Q${cx(i) - bw / 2},${top} ${cx(i) - bw / 2 + r},${top} H${cx(i) + bw / 2 - r} Q${cx(i) + bw / 2},${top} ${cx(i) + bw / 2},${top + r} V${bot} Z`
            : `M${cx(i) - bw / 2},${top} V${bot - r} Q${cx(i) - bw / 2},${bot} ${cx(i) - bw / 2 + r},${bot} H${cx(i) + bw / 2 - r} Q${cx(i) + bw / 2},${bot} ${cx(i) + bw / 2},${bot - r} V${top} Z`;
          return (
            <g key={q.period_id}>
              <path d={d} fill={pos ? BLUE : RED} opacity={hover === null || hover === i ? 1 : 0.55} />
              {q.running && <path d={d} fill="url(#hatch)" />}
              <text x={cx(i)} y={H - 8} textAnchor="middle" fontSize={11} className="fill-neutral-500">{q.period_id}</text>
              <rect x={cx(i) - slot / 2} y={PAD.t} width={slot} height={ih} fill="transparent" onMouseEnter={() => setHover(i)} />
            </g>
          );
        })}
        <defs>
          <pattern id="hatch" width="6" height="6" patternUnits="userSpaceOnUse" patternTransform="rotate(45)">
            <line x1="0" y1="0" x2="0" y2="6" stroke="#fff" strokeWidth="2" opacity="0.6" />
          </pattern>
        </defs>
      </svg>
      {hover !== null && (
        <Tooltip x={cx(hover)} y={y(Math.max(0, quarters[hover].surplus))} h={H}>
          <div className="font-semibold">{quarters[hover].period_id}{quarters[hover].running && " (running)"}</div>
          <div>Received <span className="num">{money(quarters[hover].income)}</span></div>
          <div>Spent <span className="num">{money(quarters[hover].expenses)}</span></div>
          <div className="font-medium">{quarters[hover].surplus >= 0 ? "Surplus" : "Deficit"} <span className="num">{money(quarters[hover].surplus)}</span></div>
        </Tooltip>
      )}
    </div>
  );
}
