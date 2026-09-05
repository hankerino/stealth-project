"use client";

import { supabaseBrowser } from "./supabase-browser";

export class ApiError extends Error {
  constructor(
    public status: number,
    message: string,
    public code?: string,
  ) {
    super(message);
  }
  /** Gateway refused because the session lacks a second factor (aal2). */
  get mfaRequired() {
    return this.status === 403 && this.code === "mfa_required";
  }
}

/** Calls the trading gateway via the same-origin /api/gw proxy, attaching the
 *  current Supabase access token. Throws ApiError with the gateway's message. */
export async function api<T>(path: string, init: RequestInit = {}): Promise<T> {
  const { data } = await supabaseBrowser().auth.getSession();
  const token = data.session?.access_token;
  const headers = new Headers(init.headers);
  headers.set("Accept", "application/json");
  if (init.body && !headers.has("Content-Type")) headers.set("Content-Type", "application/json");
  if (token) headers.set("Authorization", `Bearer ${token}`);

  const res = await fetch(`/api/gw${path}`, { ...init, headers, cache: "no-store" });
  const text = await res.text();
  if (!res.ok) {
    let msg = text;
    let code: string | undefined;
    try {
      const j = JSON.parse(text) as { error?: string; message?: string };
      code = j.error;
      msg = j.message ?? j.error ?? text;
    } catch {
      /* not JSON */
    }
    throw new ApiError(res.status, msg || res.statusText, code);
  }
  return (text ? JSON.parse(text) : null) as T;
}

// ---- Gateway types (mirror the Go services' JSON tags) ----

export type GpuType = { id: number; name: string; vram_gb: number };
export type Region = { code: string; name: string };
export type FuturesContract = {
  id: number;
  gpu_type_id: number;
  region_code: string;
  delivery_date: string;
  tick_size: number;
  contract_size: number;
  status: string;
  symbol: string;
};
export const isFuturesSymbol = (sym: string) => sym.includes(":FUT:");

export type Order = {
  id: string;
  user_id: string;
  symbol: string;
  gpu_type: string;
  region: string;
  side: "BUY" | "SELL";
  price_cents: number;
  quantity: number;
  filled_quantity: number;
  status: string;
  time_in_force: string;
  order_kind: string;
  contract_id: number;
  created_at: string;
  updated_at: string;
};

export type Balance = { user_id: string; balance: number };

/** A purchased allocation (settlement job). held = yours to run or resell. */
export type Allocation = {
  job_id: string;
  trade_id: string;
  symbol: string;
  node_id: string;
  quantity: number;
  status: "held" | "queued" | "running" | "completed" | "failed";
  status_reason: string | null;
  workload: Record<string, unknown>;
  created_at: string;
  updated_at: string;
};

/** Net GPU-hours per market (risk service). Positive = hours held (resellable),
 *  negative = hours supplied as a node operator. contract_id 0 = spot. */
export type Position = {
  contract_id: number;
  symbol: string;
  net_quantity: number;
  avg_entry_price_cents: number;
  margin_posted_cents: number;
};

export type Deposit = {
  id: string;
  user_id: string;
  amount_cents: number;
  fee_cents: number;
  provider: "stripe" | "admin";
  provider_ref: string;
  status: "PENDING" | "COMPLETED" | "EXPIRED";
  created_at: string;
  completed_at: string | null;
};
export type Payout = {
  id: string;
  user_id: string;
  amount_cents: number;
  status: "REQUESTED" | "PAID" | "REJECTED";
  note: string | null;
  created_at: string;
  resolved_at: string | null;
  resolved_by: string | null;
};
export type EscrowHistory = { deposits: Deposit[]; payouts: Payout[] };

/** Account role from the Supabase access token (custom_access_token_hook
 *  claim `account_role`). Display-only: the gateway enforces RBAC. */
export async function accountRole(): Promise<string> {
  const { data } = await supabaseBrowser().auth.getSession();
  const token = data.session?.access_token;
  if (!token) return "";
  try {
    const payload = JSON.parse(atob(token.split(".")[1].replace(/-/g, "+").replace(/_/g, "/")));
    return typeof payload.account_role === "string" ? payload.account_role : "";
  } catch {
    return "";
  }
}

/** Account id (JWT `sub`) — what a seller puts in the node-agent's SELLER_ID. */
export async function accountIdFromToken(): Promise<string> {
  const { data } = await supabaseBrowser().auth.getSession();
  const token = data.session?.access_token;
  if (!token) return "";
  try {
    const payload = JSON.parse(atob(token.split(".")[1].replace(/-/g, "+").replace(/_/g, "/")));
    return typeof payload.sub === "string" ? payload.sub : "";
  } catch {
    return "";
  }
}

export const symbolFor = (gpu: string, region: string) => `${gpu}:${region}`;
export const splitSymbol = (sym: string) => {
  const [gpu, region] = sym.split(":");
  return { gpu, region };
};

/** Exchange fee schedule (basis points) — GET /v1/fees. */
export type FeeSchedule = { buyer_bps: number; seller_bps: number; deposit_bps: number; deposit_fixed_cents: number };
export type RevenueBucket = { kind: string; count: number; fee_cents: number; to_platform: boolean };
export type RevenueReport = {
  platform_account: string;
  balance_cents: number;
  schedule: FeeSchedule;
  totals: RevenueBucket[];
  last_30d: RevenueBucket[];
  recent: { id: string; kind: string; payer_id: string; trade_id: string | null; fee_cents: number; basis_cents: number; created_at: string }[];
};

export type ProviderApplication = {
  id: string; name: string; email: string; company: string | null; gpus: string; location: string | null;
  notes: string | null; status: "NEW" | "CONTACTED" | "ONBOARDED" | "DECLINED"; created_at: string;
};

