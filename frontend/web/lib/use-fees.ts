"use client";

import { useEffect, useState } from "react";
import { api, type FeeSchedule } from "./api";

const FALLBACK: FeeSchedule = { buyer_bps: 100, seller_bps: 250, deposit_bps: 290, deposit_fixed_cents: 30 };
let cached: FeeSchedule | null = null;

/** The live fee schedule, fetched once per page load. Math mirrors
 *  services/settlement/fees.go so the ticket shows exactly what settlement
 *  will charge. */
export function useFees(): FeeSchedule {
  const [fees, setFees] = useState<FeeSchedule>(cached ?? FALLBACK);
  useEffect(() => {
    if (cached) return;
    api<FeeSchedule>("/v1/fees").then((f) => { cached = f; setFees(f); }).catch(() => {});
  }, []);
  return fees;
}

export const bpsOf = (cents: number, bps: number) => (cents <= 0 || bps <= 0 ? 0 : Math.floor((cents * bps + 5_000) / 10_000));
export const pct = (bps: number) => `${(bps / 100).toFixed(bps % 100 === 0 ? 0 : bps % 10 === 0 ? 1 : 2)}%`;

/** Grossed-up card fee: smallest fee such that the processor's cut of
 *  (amount + fee) still leaves ≥ amount for escrow. */
export function depositFee(cents: number, f: FeeSchedule): number {
  if (cents <= 0 || (f.deposit_bps <= 0 && f.deposit_fixed_cents <= 0)) return 0;
  const den = 10_000 - f.deposit_bps;
  if (den <= 0) return 0;
  const gross = Math.ceil(((cents + f.deposit_fixed_cents) * 10_000) / den);
  return gross - cents;
}
