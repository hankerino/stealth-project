"use client";

import { useCallback, useEffect, useState } from "react";
import { supabaseBrowser } from "@/lib/supabase-browser";
import { Card, ErrorBanner, btnCls, inputCls } from "./ui";

type Factor = { id: string; friendly_name?: string; status: "verified" | "unverified" };

/** Two-factor authentication (TOTP via Supabase MFA). Enrol once with an
 *  authenticator app; then every session that needs to move money or use
 *  admin tools is elevated to aal2 with a 6-digit code. */
export function MfaSettings() {
  const sb = supabaseBrowser();
  const [factors, setFactors] = useState<Factor[] | null>(null);
  const [aal, setAal] = useState<string>("");
  const [enrol, setEnrol] = useState<{ id: string; qr: string; secret: string } | null>(null);
  const [code, setCode] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);

  const load = useCallback(async () => {
    const [{ data: f }, { data: a }] = await Promise.all([sb.auth.mfa.listFactors(), sb.auth.mfa.getAuthenticatorAssuranceLevel()]);
    setFactors((f?.totp ?? []) as Factor[]);
    setAal(a?.currentLevel ?? "");
  }, [sb]);
  useEffect(() => { void load(); }, [load]);

  const startEnrol = async () => {
    setBusy(true); setError(null);
    const { data, error } = await sb.auth.mfa.enroll({ factorType: "totp", friendlyName: "Authenticator app", issuer: "hQube Exchange" });
    setBusy(false);
    if (error) return setError(error.message);
    setEnrol({ id: data.id, qr: data.totp.qr_code, secret: data.totp.secret });
  };

  const confirmEnrol = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!enrol) return;
    setBusy(true); setError(null);
    const { data: ch, error: e1 } = await sb.auth.mfa.challenge({ factorId: enrol.id });
    if (e1) { setBusy(false); return setError(e1.message); }
    const { error: e2 } = await sb.auth.mfa.verify({ factorId: enrol.id, challengeId: ch.id, code });
    setBusy(false);
    if (e2) return setError(e2.message);
    setEnrol(null); setCode(""); setNotice("Two-factor authentication is on. This session is now elevated.");
    await load();
  };

  const remove = async (id: string) => {
    if (!confirm("Turn off two-factor authentication? Withdrawals and admin actions will be blocked until you enrol again.")) return;
    setBusy(true); setError(null);
    const { error } = await sb.auth.mfa.unenroll({ factorId: id });
    setBusy(false);
    if (error) return setError(error.message);
    await load();
  };

  const verified = (factors ?? []).filter((f) => f.status === "verified");
  return (
    <Card title="Two-factor authentication">
      <ErrorBanner error={error} />
      {notice && <p className="mb-3 text-sm text-emerald-300">{notice}</p>}
      {factors === null ? (
        <p className="text-sm text-zinc-500">Loading…</p>
      ) : verified.length > 0 ? (
        <div className="space-y-3 text-sm">
          <p className="text-emerald-300">Enabled — {verified[0].friendly_name ?? "authenticator app"}.</p>
          <p className="text-zinc-400">This session is at <span className="font-mono">{aal}</span>. Withdrawals and admin actions require <span className="font-mono">aal2</span>; you'll be asked for a code when needed.</p>
          {aal !== "aal2" && <MfaChallenge factorId={verified[0].id} onDone={load} />}
          <button className={`${btnCls} border border-white/15 hover:bg-white/10`} disabled={busy} onClick={() => remove(verified[0].id)}>Turn off</button>
        </div>
      ) : enrol ? (
        <form onSubmit={confirmEnrol} className="space-y-3 text-sm">
          <p className="text-zinc-300">Scan this with Google Authenticator, 1Password, Authy or any TOTP app, then enter the 6-digit code.</p>
          {/* Supabase returns an SVG data URL; CSP allows img-src data: */}
          {/* eslint-disable-next-line @next/next/no-img-element */}
          <img src={enrol.qr} alt="TOTP enrolment QR code" className="h-44 w-44 rounded bg-white p-2" />
          <p className="text-xs text-zinc-500">Can't scan? Secret: <span className="select-all font-mono">{enrol.secret}</span></p>
          <input className={inputCls} inputMode="numeric" pattern="[0-9]{6}" placeholder="123456" value={code} onChange={(e) => setCode(e.target.value)} required />
          <div className="flex gap-2">
            <button className={`${btnCls} bg-brand text-navy-deep hover:bg-brand-dark hover:text-white`} disabled={busy}>Confirm</button>
            <button type="button" className={`${btnCls} border border-white/15`} onClick={() => setEnrol(null)}>Cancel</button>
          </div>
        </form>
      ) : (
        <div className="space-y-3 text-sm">
          <p className="text-zinc-400">Not enabled. Required for withdrawals and for admin tools.</p>
          <button className={`${btnCls} bg-brand text-navy-deep hover:bg-brand-dark hover:text-white`} disabled={busy} onClick={startEnrol}>Set up authenticator app</button>
        </div>
      )}
    </Card>
  );
}

/** Elevate the current session to aal2 with a TOTP code. */
export function MfaChallenge({ factorId, onDone }: { factorId?: string; onDone: () => void }) {
  const sb = supabaseBrowser();
  const [code, setCode] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true); setError(null);
    let fid = factorId;
    if (!fid) {
      const { data } = await sb.auth.mfa.listFactors();
      fid = (data?.totp as Factor[] | undefined)?.find((f) => f.status === "verified")?.id;
      if (!fid) { setBusy(false); return setError("No authenticator enrolled — set one up under Settings → Security."); }
    }
    const { data: ch, error: e1 } = await sb.auth.mfa.challenge({ factorId: fid });
    if (e1) { setBusy(false); return setError(e1.message); }
    const { error: e2 } = await sb.auth.mfa.verify({ factorId: fid, challengeId: ch.id, code });
    setBusy(false);
    if (e2) return setError(e2.message);
    setCode("");
    onDone();
  };

  return (
    <form onSubmit={submit} className="flex flex-wrap items-center gap-2">
      <input className={`${inputCls} w-32`} inputMode="numeric" pattern="[0-9]{6}" placeholder="6-digit code" value={code} onChange={(e) => setCode(e.target.value)} required />
      <button className={`${btnCls} bg-brand text-navy-deep hover:bg-brand-dark hover:text-white`} disabled={busy}>{busy ? "…" : "Verify"}</button>
      {error && <span className="text-xs text-red-300">{error}</span>}
    </form>
  );
}
