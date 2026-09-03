"use client";

import { useEffect, useRef, useState } from "react";

export type Quote = {
  symbol: string;
  best_bid_price_cents: number | null;
  best_ask_price_cents: number | null;
  bid_depth: number;
  ask_depth: number;
  last_trade_price_cents: number | null;
  updated_at_unix_ms: number;
};

export type Trade = {
  trade_id: string;
  symbol: string;
  price_cents: number;
  quantity: number;
  aggressor_side: "BUY" | "SELL";
  occurred_at_unix_ms: number;
};

type Envelope = { channel: string; data: Quote | Trade };

/** Subscribes to quotes.<symbol> and trades.<symbol> on the market-data
 *  WebSocket. Reconnects with backoff; keeps the last 50 trades. */
export function useMarketData(symbol: string) {
  const [quote, setQuote] = useState<Quote | null>(null);
  const [trades, setTrades] = useState<Trade[]>([]);
  const [status, setStatus] = useState<"connecting" | "live" | "offline">("connecting");
  const retry = useRef(0);

  useEffect(() => {
    const url = process.env.NEXT_PUBLIC_MARKETDATA_WS_URL;
    if (!url) {
      setStatus("offline");
      return;
    }
    let ws: WebSocket | null = null;
    let timer: ReturnType<typeof setTimeout> | null = null;
    let closed = false;
    setTrades([]);
    setQuote(null);

    const connect = () => {
      setStatus("connecting");
      ws = new WebSocket(url);
      ws.onopen = () => {
        retry.current = 0;
        setStatus("live");
        for (const ch of [`quotes.${symbol}`, `trades.${symbol}`]) {
          ws?.send(JSON.stringify({ action: "subscribe", channel: ch }));
        }
      };
      ws.onmessage = (ev) => {
        let msg: Partial<Envelope> & { type?: string };
        try {
          msg = JSON.parse(ev.data as string);
        } catch {
          return;
        }
        if (!msg.channel || !msg.data) return; // ack / pong / error
        if (msg.channel === `quotes.${symbol}`) setQuote(msg.data as Quote);
        else if (msg.channel === `trades.${symbol}`)
          setTrades((prev) => [msg.data as Trade, ...prev].slice(0, 50));
      };
      ws.onclose = () => {
        if (closed) return;
        setStatus("offline");
        const delay = Math.min(30_000, 1000 * 2 ** retry.current++);
        timer = setTimeout(connect, delay);
      };
      ws.onerror = () => ws?.close();
    };
    connect();

    return () => {
      closed = true;
      if (timer) clearTimeout(timer);
      ws?.close();
    };
  }, [symbol]);

  return { quote, trades, status };
}
