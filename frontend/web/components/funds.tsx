"use client";

import { useEffect, useState } from "react";
import { api, ApiError, type EscrowHistory, type Payout } from "@/lib/api";
import { cents, when } from "@/lib/format";
import { Card, btnCls, inputCls } from "./ui";
import { MfaChallenge } from "./mfa";
import Link from "next/link";
import { useFees, depositFee, pct } from "@/lib/use-fees";
import type { RevenueReport, ProviderApplication } from "@/lib/api";

/** Money in: Stripe Checkout (hosted page). The server creates the session
 *  and the escrow credit only happens on the webhook, so this button never
 *  touches the balance directly. */
export function AddFunds({ disabled }: { disabled: boolean }) {
  const [amount, setAmount] = useState("100");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const fees = useFees();
  const amountCents = Math.round((parseFloat(amount) || 0) * 100);
  const fee = depositFee(amountCents, fees);

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
      {amountCents > 0 && fee > 0 && (
        <div className="mt-2 flex justify-between text-xs text-zinc-400">
          <span>Card processing ({pct(fees.deposit_bps)} + {cents(fees.deposit_fixed_cents)}, shown on the Stripe page)</span>
          <span className="text-zinc-200">+{cents(fee)} · charged {cents(amountCents + fee)}, {cents(amountCents)} to escrow</span>
        </div>
      )}
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
      kind: d.provider === "admin" ? "Credit (admin)" : d.fee_cents > 0 ? `Deposit (card, +${cents(d.fee_cents)} processing)` : "Deposit (card)",
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

/** Admin: platform fee revenue (fees.go). Balance lives in the platform
 *  escrow account and leaves through the normal payout queue. */
export function AdminRevenue() {
  const [rep, setRep] = useState<RevenueReport | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [amount, setAmount] = useState("");
  const [msg, setMsg] = useState<string | null>(null);
  const [mfa, setMfa] = useState(false);
  const load = () => api<RevenueReport>("/v1/admin/revenue").then(setRep).catch((e) => setErr((e as Error).message));
  useEffect(() => { void load(); }, []);

  const payout = async (e: React.FormEvent) => {
    e.preventDefault();
    setMsg(null);
    try {
      await api("/v1/admin/revenue/payout", { method: "POST", body: JSON.stringify({ amount_cents: Math.round(parseFloat(amount) * 100) }) });
      setMsg("Revenue payout queued — resolve it in the payout queue below.");
      setAmount("");
      void load();
    } catch (e) {
      if (e instanceof ApiError && e.mfaRequired) setMfa(true);
      else setMsg((e as Error).message);
    }
  };

  const sum = (rows: RevenueReport["totals"] | undefined, platformOnly: boolean) =>
    (rows ?? []).filter((r) => !platformOnly || r.to_platform).reduce((n, r) => n + r.fee_cents, 0);
  const label: Record<string, string> = { BUYER_TRADE: "Buyer fees", SELLER_TRADE: "Seller fees", DEPOSIT_PROCESSING: "Card processing (pass-through to Stripe)" };

  return (
    <Card title="Platform revenue">
      {err && <p className="text-xs text-red-300">{err}</p>}
      {rep && (
        <>
          <div className="grid grid-cols-3 gap-3 text-sm">
            <div><div className="text-xs text-zinc-500">Revenue balance</div><div className="font-mono text-lg text-emerald-300">{cents(rep.balance_cents)}</div></div>
            <div><div className="text-xs text-zinc-500">Fees earned, last 30 d</div><div className="font-mono text-lg">{cents(sum(rep.last_30d, true))}</div></div>
            <div><div className="text-xs text-zinc-500">Fees earned, all time</div><div className="font-mono text-lg">{cents(sum(rep.totals, true))}</div></div>
          </div>
          <p className="mt-1 text-xs text-zinc-500">
            Schedule: buyers {pct(rep.schedule.buyer_bps)} · sellers {pct(rep.schedule.seller_bps)} · deposits {pct(rep.schedule.deposit_bps)} + {cents(rep.schedule.deposit_fixed_cents)} (pass-through).
          </p>
          <table className="mt-3 w-full text-xs">
            <tbody>
              {rep.totals.map((b) => (
                <tr key={b.kind} className="border-t border-zinc-800">
                  <td className="py-1 pr-3 text-zinc-400">{label[b.kind] ?? b.kind}</td>
                  <td className="py-1 pr-3 text-right text-zinc-500">{b.count}×</td>
                  <td className={`py-1 text-right font-mono ${b.to_platform ? "" : "text-zinc-500"}`}>{cents(b.fee_cents)}</td>
                </tr>
              ))}
            </tbody>
          </table>
          <form onSubmit={payout} className="mt-3 flex gap-2">
            <input className={inputCls} type="number" step="0.01" min="1" max={rep.balance_cents / 100} placeholder="Withdraw revenue (USD)" value={amount} onChange={(e) => setAmount(e.target.value)} />
            <button className={`${btnCls} shrink-0 bg-zinc-700 hover:bg-zinc-600`} disabled={!amount}>Request payout</button>
          </form>
          {mfa && <div className="mt-2"><MfaChallenge onDone={() => { setMfa(false); }} /></div>}
          {msg && <p className="mt-2 text-xs text-zinc-300">{msg}</p>}
        </>
      )}
    </Card>
  );
}

/** Admin: founding-provider applications from /sell. */
export function AdminProviders() {
  const [apps, setApps] = useState<ProviderApplication[] | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const load = () => api<ProviderApplication[]>("/v1/admin/providers").then(setApps).catch((e) => setErr((e as Error).message));
  useEffect(() => { void load(); }, []);
  const setStatus = async (id: string, status: ProviderApplication["status"]) => {
    await api(`/v1/admin/providers/${id}`, { method: "POST", body: JSON.stringify({ status }) });
    void load();
  };
  const open = apps?.filter((a) => a.status === "NEW").length ?? 0;
  return (
    <Card title={`Provider applications${apps ? ` (${open} new)` : ""}`}>
      {err && <p className="text-xs text-red-300">{err}</p>}
      {apps && apps.length === 0 && <p className="text-sm text-zinc-500">No applications yet. They arrive from <a className="text-brand hover:underline" href="/sell">/sell</a>.</p>}
      {apps && apps.length > 0 && (
        <table className="w-full text-xs">
          <thead><tr className="text-left text-zinc-500"><th className="py-1 pr-3">When</th><th className="py-1 pr-3">Who</th><th className="py-1 pr-3">GPUs</th><th className="py-1 pr-3">Status</th></tr></thead>
          <tbody>
            {apps.map((a) => (
              <tr key={a.id} className="border-t border-zinc-800 align-top">
                <td className="py-1 pr-3 whitespace-nowrap text-zinc-400">{when(a.created_at)}</td>
                <td className="py-1 pr-3">
                  <div>{a.name}{a.company ? ` · ${a.company}` : ""}</div>
                  <a className="text-brand hover:underline" href={`mailto:${a.email}`}>{a.email}</a>
                  {a.location && <div className="text-zinc-500">{a.location}</div>}
                </td>
                <td className="py-1 pr-3">{a.gpus}{a.notes && <div className="mt-1 text-zinc-500">{a.notes}</div>}</td>
                <td className="py-1 pr-3">
                  <select className={`${inputCls} py-1`} value={a.status} onChange={(e) => setStatus(a.id, e.target.value as ProviderApplication["status"])}>
                    {(["NEW", "CONTACTED", "ONBOARDED", "DECLINED"] as const).map((s) => <option key={s}>{s}</option>)}
                  </select>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </Card>
  );
}

