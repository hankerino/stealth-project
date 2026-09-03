"use client";

import { useCallback, useEffect, useState } from "react";
import { api, ApiError, type Balance, type Order } from "@/lib/api";
import { cents } from "@/lib/format";
import { Card, ErrorBanner, VerifyBanner, btnCls, inputCls } from "./ui";
import { OrdersTable } from "./orders-table";

export function Portfolio() {
  const [balance, setBalance] = useState<Balance | null>(null);
  const [orders, setOrders] = useState<Order[]>([]);
  const [unverified, setUnverified] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [amount, setAmount] = useState("100");
  const [busy, setBusy] = useState(false);

  const refresh = useCallback(async () => {
    try {
      const [b, o] = await Promise.all([api<Balance>("/v1/escrow/balance"), api<Order[]>("/v1/orders")]);
      setBalance(b);
      setOrders(o ?? []);
      setUnverified(false);
      setError(null);
    } catch (e) {
      if (e instanceof ApiError && e.status === 403) setUnverified(true);
      else setError((e as Error).message);
    }
  }, []);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  const deposit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      await api("/v1/escrow/deposit", {
        method: "POST",
        body: JSON.stringify({ amount_cents: Math.round(parseFloat(amount) * 100) }),
      });
      await refresh();
    } catch (err) {
      setError((err as Error).message);
    } finally {
      setBusy(false);
    }
  };

  const open = orders.filter((o) => !["FILLED", "CANCELLED", "CANCELED", "REJECTED", "EXPIRED"].includes(o.status));

  return (
    <div className="space-y-4">
      <h1 className="text-xl font-semibold">Portfolio</h1>
      {unverified && <VerifyBanner />}
      <ErrorBanner error={error} />
      <div className="grid gap-4 md:grid-cols-2">
        <Card title="Escrow balance">
          <p className="text-3xl font-semibold">{balance ? cents(balance.balance) : "—"}</p>
          <p className="mt-1 text-xs text-zinc-500">Available to fund buy orders. Sell proceeds settle here.</p>
        </Card>
        <Card title="Deposit funds">
          <form onSubmit={deposit} className="flex gap-2">
            <input className={inputCls} type="number" step="0.01" min="1" value={amount} onChange={(e) => setAmount(e.target.value)} disabled={unverified} />
            <button className={`${btnCls} shrink-0 bg-emerald-600 hover:bg-emerald-500`} disabled={busy || unverified}>Deposit</button>
          </form>
          <p className="mt-2 text-xs text-zinc-500">Closed beta: deposits are credited directly. Card/bank funding via Stripe is coming next.</p>
        </Card>
      </div>
      <Card title={`Open orders (${open.length})`}><OrdersTable orders={open} onChanged={refresh} showSymbol /></Card>
      <Card title="Order history"><OrdersTable orders={orders} onChanged={refresh} showSymbol /></Card>
    </div>
  );
}
