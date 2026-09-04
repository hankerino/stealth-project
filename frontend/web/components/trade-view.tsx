"use client";

import { useCallback, useEffect, useState } from "react";
import { api, ApiError, splitSymbol, type Order, type Position } from "@/lib/api";
import { useMarketData } from "@/lib/use-market-data";
import { cents, when } from "@/lib/format";
import { Card, ErrorBanner, VerifyBanner, btnCls, inputCls } from "./ui";
import { OrdersTable } from "./orders-table";
import { CandleChart } from "./price-chart";

export function TradeView({ symbol }: { symbol: string }) {
  const { gpu, region } = splitSymbol(symbol);
  const { quote, trades, status } = useMarketData(symbol);
  const [orders, setOrders] = useState<Order[]>([]);
  const [held, setHeld] = useState<number | null>(null);
  const [unverified, setUnverified] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const refresh = useCallback(async () => {
    try {
      const all = await api<Order[]>("/v1/orders");
      setOrders((all ?? []).filter((o) => o.symbol === symbol));
      setUnverified(false);
      api<Position[]>("/v1/positions")
        .then((ps) => setHeld((ps ?? []).find((p) => p.symbol === symbol && p.contract_id === 0)?.net_quantity ?? 0))
        .catch(() => setHeld(null));
    } catch (e) {
      if (e instanceof ApiError && e.status === 403) setUnverified(true);
      else setError((e as Error).message);
    }
  }, [symbol]);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  // Re-pull my orders whenever the tape prints; fills change status.
  useEffect(() => {
    if (trades.length) void refresh();
  }, [trades.length, refresh]);

  return (
    <div className="space-y-4">
      <div className="flex items-baseline gap-3">
        <h1 className="font-mono text-xl font-semibold">{symbol}</h1>
        <span className="text-sm text-zinc-400">{gpu} · {region} · per GPU-hour</span>
        <span className={`ml-auto text-xs uppercase ${status === "live" ? "text-emerald-400" : "text-zinc-500"}`}>● {status}</span>
      </div>
      {unverified && <VerifyBanner />}
      <ErrorBanner error={error} />

      <div className="grid gap-4 lg:grid-cols-3">
        <Card title="Quote">
          <dl className="grid grid-cols-2 gap-y-2 text-sm">
            <dt className="text-zinc-400">Best bid</dt><dd className="text-right text-emerald-300">{cents(quote?.best_bid_price_cents)} <span className="text-zinc-500">× {quote?.bid_depth ?? 0}</span></dd>
            <dt className="text-zinc-400">Best ask</dt><dd className="text-right text-red-300">{cents(quote?.best_ask_price_cents)} <span className="text-zinc-500">× {quote?.ask_depth ?? 0}</span></dd>
            <dt className="text-zinc-400">Last</dt><dd className="text-right">{cents(quote?.last_trade_price_cents)}</dd>
            <dt className="text-zinc-400">Spread</dt>
            <dd className="text-right">{quote?.best_bid_price_cents != null && quote?.best_ask_price_cents != null ? cents(quote.best_ask_price_cents - quote.best_bid_price_cents) : "—"}</dd>
          </dl>
        </Card>

        <Ticket symbol={symbol} gpu={gpu} region={region} disabled={unverified} onPlaced={refresh} held={held} defaultPrice={quote?.last_trade_price_cents ?? quote?.best_ask_price_cents ?? quote?.best_bid_price_cents ?? null} />

        <Card title="Recent trades">
          {trades.length === 0 ? (
            <p className="text-sm text-zinc-500">No trades yet this session.</p>
          ) : (
            <table className="w-full text-xs">
              <tbody>
                {trades.map((t) => (
                  <tr key={t.trade_id} className="border-b border-zinc-800/60">
                    <td className={t.aggressor_side === "BUY" ? "text-emerald-300" : "text-red-300"}>{cents(t.price_cents)}</td>
                    <td className="text-right">{t.quantity}</td>
                    <td className="text-right text-zinc-500">{new Date(t.occurred_at_unix_ms).toLocaleTimeString(undefined, { hour12: false })}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </Card>
      </div>

      <CandleChart symbol={symbol} refreshKey={trades.length} />

      <Card title="My orders in this market">
        <OrdersTable orders={orders} onChanged={refresh} />
      </Card>
    </div>
  );
}

function Ticket({ symbol, gpu, region, disabled, onPlaced, held, defaultPrice }: {
  symbol: string; gpu: string; region: string; disabled: boolean; onPlaced: () => void; held: number | null; defaultPrice: number | null;
}) {
  const [side, setSide] = useState<"BUY" | "SELL">("BUY");
  const [price, setPrice] = useState("");
  const [qty, setQty] = useState("1");
  const [tif, setTif] = useState("GTC");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [ok, setOk] = useState<string | null>(null);

  useEffect(() => {
    if (!price && defaultPrice != null) setPrice((defaultPrice / 100).toFixed(2));
  }, [defaultPrice, price]);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(null);
    setOk(null);
    try {
      const o = await api<Order>("/v1/orders", {
        method: "POST",
        body: JSON.stringify({
          gpu_type: gpu,
          region,
          side,
          price_cents: Math.round(parseFloat(price) * 100),
          quantity: parseInt(qty, 10),
          time_in_force: tif,
        }),
      });
      setOk(`${o.side} ${o.quantity} @ ${cents(o.price_cents)} — ${o.status}`);
      onPlaced();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };

  const total = Math.round((parseFloat(price) || 0) * 100) * (parseInt(qty, 10) || 0);

  return (
    <Card title={`Order ticket · ${symbol}`}>
      <form onSubmit={submit} className="space-y-3">
        <div className="grid grid-cols-2 gap-2">
          {(["BUY", "SELL"] as const).map((s) => (
            <button type="button" key={s} onClick={() => setSide(s)}
              className={`${btnCls} border ${side === s ? (s === "BUY" ? "border-emerald-500 bg-emerald-600/30 text-emerald-200" : "border-red-500 bg-red-600/30 text-red-200") : "border-zinc-700 text-zinc-400"}`}>
              {s}
            </button>
          ))}
        </div>
        <label className="block text-xs text-zinc-400">Price (USD / GPU-hour)
          <input className={`${inputCls} mt-1`} type="number" step="0.01" min="0.01" value={price} onChange={(e) => setPrice(e.target.value)} required />
        </label>
        <label className="block text-xs text-zinc-400">Quantity (GPU-hours)
          <input className={`${inputCls} mt-1`} type="number" step="1" min="1" value={qty} onChange={(e) => setQty(e.target.value)} required />
        </label>
        <label className="block text-xs text-zinc-400">Time in force
          <select className={`${inputCls} mt-1`} value={tif} onChange={(e) => setTif(e.target.value)}>
            <option value="GTC">GTC — good till cancelled</option>
            <option value="IOC">IOC — immediate or cancel</option>
            <option value="FOK">FOK — fill or kill</option>
          </select>
        </label>
        <div className="flex justify-between text-xs text-zinc-400"><span>Notional</span><span className="text-zinc-200">{cents(total)}</span></div>
        {side === "SELL" && held != null && (
          <p className="text-xs text-zinc-500">
            {held > 0
              ? `You hold ${held} GPU-hour${held === 1 ? "" : "s"} here — sells up to that resell your allocation.`
              : "Sells must be backed by hours you hold or by a registered node."}
          </p>
        )}
        <ErrorBanner error={error} />
        {ok && <p className="text-xs text-emerald-300">{ok}</p>}
        <button className={`${btnCls} w-full ${side === "BUY" ? "bg-emerald-600 hover:bg-emerald-500" : "bg-red-600 hover:bg-red-500"}`} disabled={busy || disabled}>
          {disabled ? "Verification required" : busy ? "Placing…" : `Place ${side.toLowerCase()} order`}
        </button>
      </form>
    </Card>
  );
}
