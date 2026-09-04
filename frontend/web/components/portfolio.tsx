"use client";

import { useCallback, useEffect, useState } from "react";
import { accountRole, api, ApiError, type Balance, type EscrowHistory, type Order } from "@/lib/api";
import { cents } from "@/lib/format";
import { Card, ErrorBanner, VerifyBanner } from "./ui";
import { OrdersTable } from "./orders-table";
import { AddFunds, AdminFunds, FundsHistory, Withdraw } from "./funds";
import { Holdings } from "./holdings";

export function Portfolio() {
  const [balance, setBalance] = useState<Balance | null>(null);
  const [orders, setOrders] = useState<Order[]>([]);
  const [history, setHistory] = useState<EscrowHistory | null>(null);
  const [role, setRole] = useState("");
  const [unverified, setUnverified] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);

  const refresh = useCallback(async () => {
    try {
      const [b, o, h] = await Promise.all([
        api<Balance>("/v1/escrow/balance"),
        api<Order[]>("/v1/orders"),
        api<EscrowHistory>("/v1/escrow/history"),
      ]);
      setBalance(b);
      setOrders(o ?? []);
      setHistory(h);
      setUnverified(false);
      setError(null);
    } catch (e) {
      if (e instanceof ApiError && e.status === 403) setUnverified(true);
      else setError((e as Error).message);
    }
  }, []);

  useEffect(() => {
    void refresh();
    void accountRole().then(setRole);
    // Stripe Checkout returns here with ?deposit=success|cancelled. The credit
    // arrives via webhook, usually before the redirect lands; poll briefly.
    const q = new URLSearchParams(window.location.search).get("deposit");
    if (q === "success") {
      setNotice("Payment received — your escrow balance will update in a moment.");
      const timers = [2000, 5000, 10000].map((ms) => setTimeout(() => void refresh(), ms));
      window.history.replaceState(null, "", "/portfolio");
      return () => timers.forEach(clearTimeout);
    }
    if (q === "cancelled") {
      setNotice("Checkout cancelled — no charge was made.");
      window.history.replaceState(null, "", "/portfolio");
    }
  }, [refresh]);

  const open = orders.filter((o) => !["FILLED", "CANCELLED", "CANCELED", "REJECTED", "EXPIRED"].includes(o.status));
  const pending = history?.deposits.filter((d) => d.status === "PENDING").length ?? 0;

  return (
    <div className="space-y-4">
      <h1 className="text-xl font-semibold">Portfolio</h1>
      {unverified && <VerifyBanner />}
      <ErrorBanner error={error} />
      {notice && (
        <p className="rounded border border-emerald-900 bg-emerald-950/40 px-3 py-2 text-sm text-emerald-200">{notice}</p>
      )}
      <div className="grid gap-4 md:grid-cols-3">
        <Card title="Escrow balance">
          <p className="text-3xl font-semibold">{balance ? cents(balance.balance) : "—"}</p>
          <p className="mt-1 text-xs text-zinc-500">
            Available to fund buy orders. Sell proceeds settle here.
            {pending > 0 && <span className="ml-1 text-amber-300">{pending} deposit{pending > 1 ? "s" : ""} awaiting confirmation.</span>}
          </p>
        </Card>
        <AddFunds disabled={unverified} />
        <Withdraw disabled={unverified || !balance || balance.balance <= 0} balance={balance?.balance ?? 0} onChanged={refresh} />
      </div>
      {role === "admin" && <AdminFunds onChanged={refresh} />}
      <Holdings refreshKey={orders} />
      <Card title="Deposits & payouts"><FundsHistory history={history} /></Card>
      <Card title={`Open orders (${open.length})`}><OrdersTable orders={open} onChanged={refresh} showSymbol /></Card>
      <Card title="Order history"><OrdersTable orders={orders} onChanged={refresh} showSymbol /></Card>
    </div>
  );
}
