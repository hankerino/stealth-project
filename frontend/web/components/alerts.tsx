"use client";

import { useCallback, useEffect, useState } from "react";
import { api } from "@/lib/api";
import { when } from "@/lib/format";
import { Card, btnCls } from "./ui";

type Alert = {
  alert_id: string;
  rule: "WASH_TRADE" | "SPOOFING";
  severity: "low" | "medium" | "high";
  user_id: string;
  symbol: string;
  ref_id: string | null;
  details: Record<string, unknown>;
  status: "open" | "reviewed" | "dismissed";
  created_at: string;
};

/** Admin-only surveillance queue (compliance service via the gateway). */
export function SurveillanceAlerts() {
  const [alerts, setAlerts] = useState<Alert[] | null>(null);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(() => {
    api<Alert[]>("/v1/admin/alerts?status=open&limit=50").then((a) => setAlerts(a ?? [])).catch((e) => setError((e as Error).message));
  }, []);
  useEffect(load, [load]);

  const resolve = async (id: string, status: "reviewed" | "dismissed") => {
    try {
      await api(`/v1/admin/alerts/${id}`, { method: "POST", body: JSON.stringify({ status }) });
      load();
    } catch (e) {
      setError((e as Error).message);
    }
  };

  const sev = { high: "text-red-300", medium: "text-amber-300", low: "text-zinc-300" };
  return (
    <Card title={`Surveillance alerts${alerts ? ` (${alerts.length} open)` : ""}`}>
      {error ? (
        <p className="text-sm text-red-300">{error}</p>
      ) : !alerts ? (
        <p className="text-sm text-zinc-500">Loading…</p>
      ) : alerts.length === 0 ? (
        <p className="text-sm text-zinc-500">No open alerts.</p>
      ) : (
        <div className="overflow-x-auto"><table className="w-full text-xs">
          <thead className="text-left text-zinc-500">
            <tr><th>When</th><th>Rule</th><th>Account</th><th>Market</th><th>Evidence</th><th></th></tr>
          </thead>
          <tbody>
            {alerts.map((a) => (
              <tr key={a.alert_id} className="border-t border-zinc-800/60 align-top">
                <td className="whitespace-nowrap text-zinc-400">{when(a.created_at)}</td>
                <td className={sev[a.severity]}>{a.rule}</td>
                <td className="font-mono">{a.user_id.slice(0, 8)}…</td>
                <td className="font-mono">{a.symbol}</td>
                <td className="max-w-xs break-all text-zinc-400">{JSON.stringify(a.details)}</td>
                <td className="whitespace-nowrap text-right">
                  <button className={`${btnCls} mr-1 bg-zinc-800 hover:bg-zinc-700`} onClick={() => resolve(a.alert_id, "reviewed")}>Reviewed</button>
                  <button className={`${btnCls} bg-zinc-800 hover:bg-zinc-700`} onClick={() => resolve(a.alert_id, "dismissed")}>Dismiss</button>
                </td>
              </tr>
            ))}
          </tbody>
        </table></div>
      )}
    </Card>
  );
}
