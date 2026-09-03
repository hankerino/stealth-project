"use client";

import Link from "next/link";
import { useState } from "react";
import { api, type Order } from "@/lib/api";
import { cents, when } from "@/lib/format";

const OPEN = new Set(["NEW", "OPEN", "PARTIALLY_FILLED", "ACCEPTED"]);

export function OrdersTable({ orders, onChanged, showSymbol }: { orders: Order[]; onChanged: () => void; showSymbol?: boolean }) {
  const [busy, setBusy] = useState<string | null>(null);
  const [err, setErr] = useState<string | null>(null);

  const cancel = async (id: string) => {
    setBusy(id);
    setErr(null);
    try {
      await api(`/v1/orders/${id}`, { method: "DELETE" });
      onChanged();
    } catch (e) {
      setErr((e as Error).message);
    } finally {
      setBusy(null);
    }
  };

  if (orders.length === 0) return <p className="text-sm text-zinc-500">No orders.</p>;
  return (
    <div className="overflow-x-auto">
      {err && <p className="mb-2 text-xs text-red-300">{err}</p>}
      <table className="w-full text-xs">
        <thead className="text-left text-zinc-500">
          <tr>
            {showSymbol && <th className="py-1">Market</th>}
            <th className="py-1">Side</th><th>Price</th><th>Qty</th><th>Filled</th><th>TIF</th><th>Status</th><th>Placed</th><th />
          </tr>
        </thead>
        <tbody>
          {orders.map((o) => (
            <tr key={o.id} className="border-t border-zinc-800/60">
              {showSymbol && <td className="py-1 font-mono"><Link className="hover:underline" href={`/trade/${encodeURIComponent(o.symbol)}`}>{o.symbol}</Link></td>}
              <td className={`py-1 ${o.side === "BUY" ? "text-emerald-300" : "text-red-300"}`}>{o.side}</td>
              <td>{cents(o.price_cents)}</td>
              <td>{o.quantity}</td>
              <td>{o.filled_quantity}</td>
              <td className="text-zinc-400">{o.time_in_force}</td>
              <td>{o.status}</td>
              <td className="text-zinc-500">{when(o.created_at)}</td>
              <td className="text-right">
                {OPEN.has(o.status) && (
                  <button onClick={() => cancel(o.id)} disabled={busy === o.id} className="text-zinc-400 hover:text-red-300 disabled:opacity-50">
                    {busy === o.id ? "…" : "Cancel"}
                  </button>
                )}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
