"use client";

import Link from "next/link";
import { useEffect, useState } from "react";
import { api, symbolFor, type FuturesContract, type GpuType, type Region } from "@/lib/api";
import { Card, ErrorBanner } from "./ui";
import { useMarketData } from "@/lib/use-market-data";
import { cents } from "@/lib/format";
import { IndexChart } from "./price-chart";

function FuturesTile({ c }: { c: FuturesContract }) {
  const { quote, status } = useMarketData(c.symbol);
  return (
    <Link href={`/trade/${encodeURIComponent(c.symbol)}`} className="block rounded-lg border border-white/10 bg-white/[0.03] p-4 hover:border-brand">
      <div className="flex items-baseline justify-between">
        <span className="font-mono text-sm">{c.symbol}</span>
        <span className={`text-[10px] uppercase ${status === "live" ? "text-emerald-400" : "text-zinc-500"}`}>{status}</span>
      </div>
      <div className="mt-1 text-xs text-zinc-400">Delivery {c.delivery_date} · {c.contract_size} GPU-h/contract · tick {cents(c.tick_size)}</div>
      <div className="mt-3 grid grid-cols-3 gap-2 text-xs">
        <div><div className="text-zinc-500">Bid</div><div className="text-emerald-300">{cents(quote?.best_bid_price_cents)}</div></div>
        <div><div className="text-zinc-500">Ask</div><div className="text-red-300">{cents(quote?.best_ask_price_cents)}</div></div>
        <div><div className="text-zinc-500">Last</div><div>{cents(quote?.last_trade_price_cents)}</div></div>
      </div>
    </Link>
  );
}

function Tile({ gpu, region }: { gpu: GpuType; region: Region }) {
  const sym = symbolFor(gpu.name, region.code);
  const { quote, status } = useMarketData(sym);
  return (
    <Link href={`/trade/${encodeURIComponent(sym)}`} className="block rounded-lg border border-white/10 bg-white/[0.03] p-4 hover:border-brand">
      <div className="flex items-baseline justify-between">
        <span className="font-mono text-sm">{sym}</span>
        <span className={`text-[10px] uppercase ${status === "live" ? "text-emerald-400" : "text-zinc-500"}`}>{status}</span>
      </div>
      <div className="mt-1 text-xs text-zinc-400">{gpu.vram_gb} GB · {region.name}</div>
      <div className="mt-3 grid grid-cols-3 gap-2 text-xs">
        <div><div className="text-zinc-500">Bid</div><div className="text-emerald-300">{cents(quote?.best_bid_price_cents)}</div></div>
        <div><div className="text-zinc-500">Ask</div><div className="text-red-300">{cents(quote?.best_ask_price_cents)}</div></div>
        <div><div className="text-zinc-500">Last</div><div>{cents(quote?.last_trade_price_cents)}</div></div>
      </div>
    </Link>
  );
}

export function Markets() {
  const [gpus, setGpus] = useState<GpuType[]>([]);
  const [regions, setRegions] = useState<Region[]>([]);
  const [futures, setFutures] = useState<FuturesContract[]>([]);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    Promise.all([api<GpuType[]>("/v1/gpu-types"), api<Region[]>("/v1/regions")])
      .then(([g, r]) => {
        setGpus(g ?? []);
        setRegions(r ?? []);
      })
      .catch((e) => setError(e.message));
    api<FuturesContract[]>("/v1/futures-contracts").then((f) => setFutures(f ?? [])).catch(() => setFutures([]));
  }, []);

  return (
    <div className="space-y-4">
      <div>
        <h1 className="text-xl font-semibold">Spot markets</h1>
        <p className="text-sm text-zinc-400">GPU-hours, priced per hour. Pick a market to trade.</p>
      </div>
      <ErrorBanner error={error} />
      {gpus.length === 0 && !error && <Card><p className="text-sm text-zinc-400">Loading catalog…</p></Card>}
      <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
        {gpus.flatMap((g) => regions.map((r) => <Tile key={`${g.id}-${r.code}`} gpu={g} region={r} />))}
      </div>
      {futures.length > 0 && (
        <div>
          <h2 className="text-lg font-semibold">Forward contracts</h2>
          <p className="text-sm text-zinc-400">Fixed-delivery GPU-hour contracts. Price per GPU-hour; margin held from escrow (10% initial by default).</p>
          <div className="mt-3 grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
            {futures.map((c) => <FuturesTile key={c.id} c={c} />)}
          </div>
        </div>
      )}
      {gpus.length > 0 && <IndexChart gpuTypes={gpus.map((g) => g.name)} />}
    </div>
  );
}
