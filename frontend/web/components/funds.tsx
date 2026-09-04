"use client";

import { useState } from "react";
import { api, ApiError, type EscrowHistory, type Payout } from "@/lib/api";
import { cents, when } from "@/lib/format";
import { Card, btnCls, inputCls } from "./ui";
import { MfaChallenge } from "./mfa";
import Link from "next/link";

/** Money in: Stripe Checkout (hosted page). The server creates the session
 *  and the escrow credit only happens on the webhook, so this button never
 *  touches the balance directly. */
export function AddFunds({ disabled }: { disabled: boolean }) {
  const [amount, setAmount] = useState("100");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  const go = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setErr(null);
    try {
      const r = await api<{ url: string }>("/v1/escrow/checkout", {
        method: "POST",
        body: JSON.stringify({ amount_cents: Math.round(parseFloat(amount) * 100) }),
      });
      window.location.assign(r.url);
    } catch (e) {
      if (e instanceof ApiError && e.status === 503) setErr("Card payments aren't enabled on this environment yet.");
      else setErr((e as Error).message);
      setBusy(false);
    }
  };

  return (
    <Card title="Add funds">
      <form onSubmit={go} className="flex gap-2">
        <input className={inputCls} type="number" step="0.01" min="1" max="10000" value={amount} onChange={(e) => setAmount(e.target.value)} disabled={disabled} />
        <button className={`${btnCls} shrink-0 bg-emerald-600 hover:bg-emerald-500`} disabled={busy || disabled}>
          {busy ? "Redirecting…" : "Pay with card"}
        </button>
      </form>
      {err && <p className="mt-2 text-xs text-red-300">{err}</p>}
      <p className="mt-2 text-xs text-zinc-500">Secure checkout by Stripe. Funds appear in escrow as soon as the payment is confirmed.</p>
    </Card>
  );
}

/** Money out: request a payout. Escrow is debited immediately; the team pays
 *  out and marks it paid (or rejects, which refunds). */
export function Withdraw({ disabled, balance, onChanged }: { disabled: boolean; balance: number; onChanged: () => void }) {
  const [amount, setAmount] = useState("");
  const [note, setNote] = useState("");
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<{ ok: boolean; text: string } | null>(null);
  const [needMfa, setNeedMfa] = useState(false);

  const go = async (e?: React.FormEvent) => {
    e?.preventDefault();
    setBusy(true);
    setMsg(null);
    try {
      const p = await api<Payout>("/v1/escrow/withdraw", {
        method: "POST",
        body: JSON.stringify({ amount_cents: Math.round(parseFloat(amount) * 100), note }),
      });
      setNeedMfa(false);
      setMsg({ ok: true, text: `Payout of ${cents(p.amount_cents)} requested.` });
      setAmount("");
      setNote("");
      onChanged();
    } catch (e) {
      if (e instanceof ApiError && e.mfaRequired) setNeedMfa(true);
      else setMsg({ ok: false, text: (e as Error).message });
    } finally {
      setBusy(false);
    }
  };

  return (
    <Card title="Withdraw">
      <form onSubmit={go} className="space-y-2">
        <div className="flex gap-2">
          <input className={inputCls} type="number" step="0.01" min="1" max={balance / 100} placeholder="Amount (USD)" value={amount} onChange={(e) => setAmount(e.target.value)} disabled={disabled} />
          <button className={`${btnCls} shrink-0 bg-zinc-700 hover:bg-zinc-600`} disabled={busy || disabled || !amount}>Request payout</button>
        </div>
        <input className={inputCls} placeholder="Payout details (bank / reference) — optional" value={note} onChange={(e) => setNote(e.target.value)} disabled={disabled} />
      </form>
      {needMfa && (
        <div className="mt-2 space-y-1 rounded border border-amber-900 bg-amber-950/40 p-2 text-xs text-amber-200">
          <p>Withdrawals need two-factor authentication. Enter your authenticator code to continue, or <Link href="/settings/security" className="underline">set one up</Link>.</p>
          <MfaChallenge onDone={() => void go()} />
        </div>
      )}
      {msg && <p className={`mt-2 text-xs ${msg.ok ? "text-emerald-300" : "text-red-300"}`}>{msg.text}</p>}
      <p className="mt-2 text-xs text-zinc-500">Closed beta: payouts are processed manually within 2 business days. Limits: $5,000 per request, $10,000 per 24 h, one open request at a time.</p>
    </Card>
  );
}

const depositStatusCls: Record<string, string> = { COMPLETED: "text-emerald-300", PENDING: "text-amber-300", EXPIRED: "text-zinc-500" };
const payoutStatusCls: Record<string, string> = { PAID: "text-emerald-300", REQUESTED: "text-amber-300", REJECTED: "text-red-300" };

export function FundsHistory({ history }: { history: EscrowHistory | null }) {
  if (!history) return null;
  const rows = [
    ...history.deposits.map((d) => ({
      key: `d-${d.id}`,
      at: d.created_at,
      kind: d.provider === "admin" ? "Credit (admin)" : "Deposit (card)",
      amount: d.amount_cents,
      status: d.status,
      cls: depositStatusCls[d.status] ?? "",
    })),
    ...history.payouts.map((p) => ({
      key: `p-${p.id}`,
      at: p.created_at,
      kind: "Payout",
      amount: -p.amount_cents,
      status: p.status,
      cls: payoutStatusCls[p.status] ?? "",
    })),
  ].sort((a, b) => (a.at < b.at ? 1 : -1));
  if (rows.length === 0) return <p className="text-sm text-zinc-500">No deposits or payouts yet.</p>;
  return (
    <div className="overflow-x-auto">
      <table className="w-full text-left text-xs">
        <thead className="text-zinc-500">
          <tr><th className="py-1 pr-3">When</th><th className="py-1 pr-3">Type</th><th className="py-1 pr-3">Amount</th><th className="py-1">Status</th></tr>
        </thead>
        <tbody>
          {rows.map((r) => (
            <tr key={r.key} className="border-t border-zinc-800">
              <td className="py-1 pr-3 text-zinc-400">{when(r.at)}</td>
              <td className="py-1 pr-3">{r.kind}</td>
              <td className={`py-1 pr-3 font-mono ${r.amount < 0 ? "text-red-300" : ""}`}>{cents(r.amount)}</td>
              <td className={`py-1 ${r.cls}`}>{r.status}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

/** Operator tools: direct escrow credit + payout queue. Rendered only when
 *  the token says admin; the gateway enforces it regardless. */
export function AdminFunds({ onChanged }: { onChanged: () => void }) {
  const [userId, setUserId] = useState("");
  const [amount, setAmount] = useState("");
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<string | null>(null);
  const [queue, setQueue] = useState<Payout[] | null>(null);

  const loadQueue = async () => {
    try {
      setQueue(await api<Payout[]>("/v1/admin/payouts?status=REQUESTED"));
    } catch (e) {
      setMsg((e as Error).message);
    }
  };

  const credit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setMsg(null);
    try {
      const r = await api<{ user_id: string; new_balance: number }>("/v1/escrow/deposit", {
        method: "POST",
        body: JSON.stringify({ user_id: userId.trim() || undefined, amount_cents: Math.round(parseFloat(amount) * 100) }),
      });
      setMsg(`Credited. ${r.user_id.slice(0, 8)}… balance is now ${cents(r.new_balance)}.`);
      setAmount("");
      onChanged();
    } catch (e) {
      setMsg((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  const resolve = async (id: string, status: "PAID" | "REJECTED") => {
    setBusy(true);
    setMsg(null);
    try {
      await api(`/v1/admin/payouts/${id}`, { method: "POST", body: JSON.stringify({ status }) });
      await loadQueue();
      onChanged();
    } catch (e) {
      setMsg((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  return (
    <Card
      title="Admin · funds"
      right={<button className="text-xs text-zinc-400 hover:text-white" onClick={loadQueue} type="button">Load payout queue</button>}
    >
      <form onSubmit={credit} className="flex flex-wrap gap-2">
        <input className={`${inputCls} md:max-w-xs`} placeholder="Target account id (blank = me)" value={userId} onChange={(e) => setUserId(e.target.value)} />
        <input className={`${inputCls} md:max-w-[10rem]`} type="number" step="0.01" min="1" placeholder="USD" value={amount} onChange={(e) => setAmount(e.target.value)} />
        <button className={`${btnCls} bg-zinc-700 hover:bg-zinc-600`} disabled={busy || !amount}>Credit escrow</button>
      </form>
      {msg && <p className="mt-2 text-xs text-zinc-300">{msg}</p>}
      {/mfa_required|two-factor/i.test(msg ?? "") && (
        <div className="mt-2 space-y-1 rounded border border-amber-900 bg-amber-950/40 p-2 text-xs text-amber-200">
          <p>Admin actions need two-factor authentication. Enter your code (or <Link href="/settings/security" className="underline">enrol</Link>), then retry.</p>
          <MfaChallenge onDone={() => setMsg("Session elevated — retry the action.")} />
        </div>
      )}
      {queue && (
        <div className="mt-3 overflow-x-auto">
          {queue.length === 0 ? (
            <p className="text-xs text-zinc-500">No payouts waiting.</p>
          ) : (
            <table className="w-full text-left text-xs">
              <thead className="text-zinc-500">
                <tr><th className="py-1 pr-3">When</th><th className="py-1 pr-3">Account</th><th className="py-1 pr-3">Amount</th><th className="py-1 pr-3">Note</th><th className="py-1"></th></tr>
              </thead>
              <tbody>
                {queue.map((p) => (
                  <tr key={p.id} className="border-t border-zinc-800">
                    <td className="py-1 pr-3 text-zinc-400">{when(p.created_at)}</td>
                    <td className="py-1 pr-3 font-mono">{p.user_id.slice(0, 8)}…</td>
                    <td className="py-1 pr-3 font-mono">{cents(p.amount_cents)}</td>
                    <td className="py-1 pr-3 text-zinc-400">{p.note ?? "—"}</td>
                    <td className="py-1 text-right">
                      <button className="mr-2 text-emerald-300 hover:underline" disabled={busy} onClick={() => resolve(p.id, "PAID")} type="button">Mark paid</button>
                      <button className="text-red-300 hover:underline" disabled={busy} onClick={() => resolve(p.id, "REJECTED")} type="button">Reject</button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
      )}
    </Card>
  );
}
