"use client";

import Link from "next/link";
import { useEffect, useState } from "react";
import { api, type Position } from "@/lib/api";
import { cents } from "@/lib/format";
import { Card } from "./ui";

/** Held GPU-hours per market. Positive positions can be resold on the same
 *  book (SELL up to net_quantity); negative ones are supply from your nodes. */
export function Holdings({ refreshKey }: { refreshKey?: unknown }) {
  const [positions, setPositions] = useState<Position[] | null>(null);
  useEffect(() => {
    api<Position[]>("/v1/positions").then((p) => setPositions(p ?? [])).catch(() => setPositions([]));
  }, [refreshKey]);

  const held = (positions ?? []).filter((p) => p.contract_id === 0);
  return (
    <Card title="Holdings (GPU-hours)">
      {!positions ? (
        <p className="text-sm text-zinc-500">Loading…</p>
      ) : held.length === 0 ? (
        <p className="text-sm text-zinc-500">No held hours. Buy on a market, then resell here.</p>
      ) : (
        <table className="w-full text-sm">
          <thead className="text-left text-xs text-zinc-500">
            <tr><th>Market</th><th className="text-right">Net hours</th><th className="text-right">Avg entry</th><th></th></tr>
          </thead>
          <tbody>
            {held.map((p) => (
              <tr key={p.symbol} className="border-t border-zinc-800/60">
                <td className="font-mono">{p.symbol}</td>
                <td className={`text-right ${p.net_quantity > 0 ? "text-emerald-300" : "text-red-300"}`}>{p.net_quantity}</td>
                <td className="text-right text-zinc-400">{cents(p.avg_entry_price_cents)}</td>
                <td className="text-right">
                  <Link href={`/trade/${encodeURIComponent(p.symbol)}`} className="text-xs text-emerald-400 hover:underline">
                    {p.net_quantity > 0 ? "Resell" : "Trade"}
                  </Link>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </Card>
  );
}
