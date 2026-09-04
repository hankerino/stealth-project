"use client";

import Link from "next/link";
import { useCallback, useEffect, useState } from "react";
import { api, type Allocation } from "@/lib/api";
import { when } from "@/lib/format";
import { Card, btnCls } from "./ui";

const STATUS_CLS: Record<Allocation["status"], string> = {
  held: "text-emerald-300",
  queued: "text-amber-300",
  running: "text-amber-300",
  completed: "text-zinc-400",
  failed: "text-red-300",
};

/** Purchased GPU-hours. Held allocations can be run (queued on the node that
 *  backs them) or resold on the same market; the rest is history. */
export function Holdings({ refreshKey }: { refreshKey?: unknown }) {
  const [rows, setRows] = useState<Allocation[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState<string | null>(null);

  const load = useCallback(() => {
    api<Allocation[]>("/v1/allocations").then((a) => setRows(a ?? [])).catch((e) => setError((e as Error).message));
  }, []);
  useEffect(load, [load, refreshKey]);

  // Poll while something is in flight so the buyer sees it complete.
  useEffect(() => {
    if (!rows?.some((r) => r.status === "queued" || r.status === "running")) return;
    const t = setInterval(load, 4000);
    return () => clearInterval(t);
  }, [rows, load]);

  const run = async (id: string) => {
    setBusy(id);
    setError(null);
    try {
      await api(`/v1/jobs/${id}/run`, { method: "POST", body: "{}" });
      load();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(null);
    }
  };

  const held = (rows ?? []).filter((r) => r.status === "held");
  const heldHours = held.reduce((n, r) => n + r.quantity, 0);

  return (
    <Card title={`Allocations${rows ? ` · ${heldHours} GPU-hour${heldHours === 1 ? "" : "s"} held` : ""}`}>
      {error && <p className="mb-2 text-sm text-red-300">{error}</p>}
      {!rows ? (
        <p className="text-sm text-zinc-500">Loading…</p>
      ) : rows.length === 0 ? (
        <p className="text-sm text-zinc-500">No allocations yet. Buy on a market; the hours are yours to run or resell.</p>
      ) : (
        <div className="overflow-x-auto"><table className="w-full text-sm">
          <thead className="text-left text-xs text-zinc-500">
            <tr><th>Market</th><th className="text-right">Hours</th><th>Status</th><th>Updated</th><th></th></tr>
          </thead>
          <tbody>
            {rows.map((r) => (
              <tr key={r.job_id} className="border-t border-zinc-800/60">
                <td className="font-mono">{r.symbol}</td>
                <td className="text-right">{r.quantity}</td>
                <td className={STATUS_CLS[r.status]}>
                  {r.status}{r.status_reason ? <span className="text-zinc-500"> · {r.status_reason}</span> : null}
                </td>
                <td className="text-zinc-500">{when(r.updated_at)}</td>
                <td className="whitespace-nowrap text-right text-xs">
                  {r.status === "held" && (
                    <>
                      <button className={`${btnCls} mr-2 bg-emerald-700 hover:bg-emerald-600`} disabled={busy === r.job_id} onClick={() => run(r.job_id)}>
                        {busy === r.job_id ? "Starting…" : "Run"}
                      </button>
                      <Link href={`/trade/${encodeURIComponent(r.symbol)}`} className="text-emerald-400 hover:underline">Resell</Link>
                    </>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table></div>
      )}
    </Card>
  );
}
