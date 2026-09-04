"use client";

import { useEffect, useMemo, useState } from "react";
import { api } from "@/lib/api";
import { cents } from "@/lib/format";
import { Card } from "./ui";

// Dependency-free SVG charts over the Phase 3 historical API
// (GET /v1/prices/historical, /v1/prices/index). Data is fetched with the
// user's token via the gateway; the SVG scales to its container.

export type Candle = { bucket: string; open: number; high: number; low: number; close: number; volume: number; trades: number };
export type IndexPoint = { bucket: string; vwap_cents: number; volume: number; trades: number };

const RANGES = [
  { label: "24h", hours: 24, interval: "5m" },
  { label: "7d", hours: 24 * 7, interval: "1h" },
  { label: "30d", hours: 24 * 30, interval: "1d" },
] as const;
type Range = (typeof RANGES)[number];

const fromFor = (r: Range) => new Date(Date.now() - r.hours * 3600_000).toISOString();

function RangeTabs({ value, onChange }: { value: Range; onChange: (r: Range) => void }) {
  return (
    <div className="flex gap-1 text-xs">
      {RANGES.map((r) => (
        <button
          key={r.label}
          type="button"
          onClick={() => onChange(r)}
          className={`rounded px-2 py-0.5 ${r.label === value.label ? "bg-zinc-700 text-zinc-100" : "text-zinc-400 hover:text-zinc-200"}`}
        >
          {r.label}
        </button>
      ))}
    </div>
  );
}

// --- scales -----------------------------------------------------------------

const W = 800;
const H = 260;
const PAD = { l: 56, r: 12, t: 10, b: 24 };
const VOL_H = 40; // volume strip at the bottom of the plot area

function yScale(min: number, max: number, top: number, bottom: number) {
  const span = max - min || 1;
  return (v: number) => bottom - ((v - min) / span) * (bottom - top);
}

function niceTicks(min: number, max: number, n = 4): number[] {
  if (max <= min) return [min];
  const raw = (max - min) / n;
  const mag = 10 ** Math.floor(Math.log10(raw));
  const step = [1, 2, 5, 10].map((m) => m * mag).find((s) => s >= raw) ?? raw;
  const out: number[] = [];
  for (let v = Math.ceil(min / step) * step; v <= max; v += step) out.push(v);
  return out;
}

function timeLabel(iso: string, hours: number) {
  const d = new Date(iso);
  return hours <= 24
    ? d.toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit", hour12: false })
    : d.toLocaleDateString(undefined, { month: "short", day: "numeric" });
}

// --- candlestick chart (trade view) ------------------------------------------

export function CandleChart({ symbol, refreshKey }: { symbol: string; refreshKey?: number }) {
  const [range, setRange] = useState<Range>(RANGES[0]);
  const [candles, setCandles] = useState<Candle[] | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let live = true;
    api<{ candles: Candle[] }>(
      `/v1/prices/historical?symbol=${encodeURIComponent(symbol)}&interval=${range.interval}&from=${fromFor(range)}&limit=500`,
    )
      .then((r) => live && setCandles([...(r.candles ?? [])].reverse())) // API is newest-first
      .catch((e) => live && setError((e as Error).message));
    return () => {
      live = false;
    };
  }, [symbol, range, refreshKey]);

  const view = useMemo(() => {
    if (!candles?.length) return null;
    const lo = Math.min(...candles.map((c) => c.low));
    const hi = Math.max(...candles.map((c) => c.high));
    const padV = (hi - lo || hi * 0.02 || 100) * 0.1;
    const pTop = PAD.t;
    const pBot = H - PAD.b - VOL_H - 6;
    const y = yScale(lo - padV, hi + padV, pTop, pBot);
    const vMax = Math.max(...candles.map((c) => c.volume), 1);
    const yv = yScale(0, vMax, H - PAD.b - VOL_H, H - PAD.b);
    const slot = (W - PAD.l - PAD.r) / candles.length;
    const bw = Math.max(1, Math.min(14, slot * 0.7));
    const x = (i: number) => PAD.l + slot * i + slot / 2;
    return { y, yv, x, bw, ticks: niceTicks(lo - padV, hi + padV) };
  }, [candles]);

  const last = candles?.at(-1);
  const first = candles?.[0];
  const change = last && first ? last.close - first.open : null;

  return (
    <Card
      title="Price"
      right={
        <div className="flex items-center gap-3">
          {change != null && (
            <span className={`text-xs ${change >= 0 ? "text-emerald-300" : "text-red-300"}`}>
              {change >= 0 ? "+" : ""}{cents(change)} over {range.label}
            </span>
          )}
          <RangeTabs value={range} onChange={setRange} />
        </div>
      }
    >
      {error ? (
        <p className="text-sm text-red-300">{error}</p>
      ) : !candles ? (
        <p className="text-sm text-zinc-500">Loading…</p>
      ) : !view ? (
        <p className="text-sm text-zinc-500">No trades in the last {range.label}.</p>
      ) : (
        <svg viewBox={`0 0 ${W} ${H}`} className="h-64 w-full" role="img" aria-label={`${symbol} price chart`}>
          {view.ticks.map((t) => (
            <g key={t}>
              <line x1={PAD.l} x2={W - PAD.r} y1={view.y(t)} y2={view.y(t)} stroke="#27272a" strokeDasharray="2 4" />
              <text x={PAD.l - 6} y={view.y(t) + 3} textAnchor="end" fontSize="10" fill="#71717a">{cents(t)}</text>
            </g>
          ))}
          {candles.map((c, i) => {
            const up = c.close >= c.open;
            const color = up ? "#6ee7b7" : "#fca5a5";
            const top = view.y(Math.max(c.open, c.close));
            const bot = view.y(Math.min(c.open, c.close));
            return (
              <g key={c.bucket}>
                <line x1={view.x(i)} x2={view.x(i)} y1={view.y(c.high)} y2={view.y(c.low)} stroke={color} />
                <rect x={view.x(i) - view.bw / 2} y={top} width={view.bw} height={Math.max(1, bot - top)} fill={color} />
                <rect x={view.x(i) - view.bw / 2} y={view.yv(c.volume)} width={view.bw} height={H - PAD.b - view.yv(c.volume)} fill={color} opacity="0.35" />
                <title>{`${new Date(c.bucket).toLocaleString(undefined, { hour12: false })}\nO ${cents(c.open)} H ${cents(c.high)} L ${cents(c.low)} C ${cents(c.close)}\nvol ${c.volume} · ${c.trades} trades`}</title>
              </g>
            );
          })}
          {candles.map((c, i) =>
            i % Math.max(1, Math.floor(candles.length / 6)) === 0 ? (
              <text key={c.bucket} x={view.x(i)} y={H - 8} textAnchor="middle" fontSize="10" fill="#71717a">{timeLabel(c.bucket, range.hours)}</text>
            ) : null,
          )}
        </svg>
      )}
    </Card>
  );
}

// --- Compute Price Index (markets page) ---------------------------------------

const LINE_COLORS = ["#6ee7b7", "#93c5fd", "#fcd34d", "#f9a8d4", "#c4b5fd"];

export function IndexChart({ gpuTypes }: { gpuTypes: string[] }) {
  const [range, setRange] = useState<Range>(RANGES[1]);
  const [series, setSeries] = useState<Record<string, IndexPoint[]> | null>(null);
  const key = gpuTypes.join(",");

  useEffect(() => {
    if (!key) return;
    let live = true;
    Promise.all(
      key.split(",").map((g) =>
        api<{ index: IndexPoint[] }>(`/v1/prices/index?gpu_type=${encodeURIComponent(g)}&interval=${range.interval}&from=${fromFor(range)}&limit=500`)
          .then((r) => [g, [...(r.index ?? [])].reverse()] as const)
          .catch(() => [g, []] as const),
      ),
    ).then((rows) => live && setSeries(Object.fromEntries(rows)));
    return () => {
      live = false;
    };
  }, [key, range]);

  const view = useMemo(() => {
    if (!series) return null;
    const pts = Object.values(series).flat();
    if (!pts.length) return null;
    const lo = Math.min(...pts.map((p) => p.vwap_cents));
    const hi = Math.max(...pts.map((p) => p.vwap_cents));
    const padV = (hi - lo || hi * 0.02 || 100) * 0.1;
    const t0 = Math.min(...pts.map((p) => Date.parse(p.bucket)));
    const t1 = Math.max(...pts.map((p) => Date.parse(p.bucket)));
    const y = yScale(lo - padV, hi + padV, PAD.t, H - PAD.b);
    const x = (iso: string) => PAD.l + ((Date.parse(iso) - t0) / (t1 - t0 || 1)) * (W - PAD.l - PAD.r);
    return { y, x, ticks: niceTicks(lo - padV, hi + padV), t0, t1 };
  }, [series]);

  const latest = (g: string) => series?.[g]?.at(-1)?.vwap_cents;

  return (
    <Card title="Compute Price Index (VWAP)" right={<RangeTabs value={range} onChange={setRange} />}>
      {!series ? (
        <p className="text-sm text-zinc-500">Loading…</p>
      ) : !view ? (
        <p className="text-sm text-zinc-500">No settled trades in the last {range.label}.</p>
      ) : (
        <div className="space-y-2">
          <div className="flex flex-wrap gap-4 text-xs">
            {gpuTypes.map((g, i) => (
              <span key={g} className="flex items-center gap-1.5">
                <span className="inline-block h-2 w-2 rounded-full" style={{ background: LINE_COLORS[i % LINE_COLORS.length] }} />
                <span className="font-mono">{g}</span>
                <span className="text-zinc-400">{cents(latest(g))}</span>
              </span>
            ))}
          </div>
          <svg viewBox={`0 0 ${W} ${H}`} className="h-56 w-full" role="img" aria-label="Compute price index">
            {view.ticks.map((t) => (
              <g key={t}>
                <line x1={PAD.l} x2={W - PAD.r} y1={view.y(t)} y2={view.y(t)} stroke="#27272a" strokeDasharray="2 4" />
                <text x={PAD.l - 6} y={view.y(t) + 3} textAnchor="end" fontSize="10" fill="#71717a">{cents(t)}</text>
              </g>
            ))}
            {gpuTypes.map((g, i) => {
              const pts = series[g] ?? [];
              if (!pts.length) return null;
              const d = pts.map((p, j) => `${j ? "L" : "M"}${view.x(p.bucket).toFixed(1)},${view.y(p.vwap_cents).toFixed(1)}`).join(" ");
              const color = LINE_COLORS[i % LINE_COLORS.length];
              return (
                <g key={g}>
                  <path d={d} fill="none" stroke={color} strokeWidth="1.5" />
                  {pts.map((p) => (
                    <circle key={p.bucket} cx={view.x(p.bucket)} cy={view.y(p.vwap_cents)} r="2.5" fill={color}>
                      <title>{`${g} · ${new Date(p.bucket).toLocaleString(undefined, { hour12: false })}\nVWAP ${cents(p.vwap_cents)} · vol ${p.volume}`}</title>
                    </circle>
                  ))}
                </g>
              );
            })}
            <text x={PAD.l} y={H - 8} fontSize="10" fill="#71717a">{timeLabel(new Date(view.t0).toISOString(), range.hours)}</text>
            <text x={W - PAD.r} y={H - 8} textAnchor="end" fontSize="10" fill="#71717a">{timeLabel(new Date(view.t1).toISOString(), range.hours)}</text>
          </svg>
        </div>
      )}
    </Card>
  );
}
