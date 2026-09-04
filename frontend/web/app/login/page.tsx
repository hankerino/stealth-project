"use client";

import { useState, Suspense } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { supabaseBrowser } from "@/lib/supabase-browser";
import { ErrorBanner, btnCls, inputCls } from "@/components/ui";
import { Brand, CubeMark } from "@/components/brand";
import Link from "next/link";

function LoginForm() {
  const router = useRouter();
  const params = useSearchParams();
  // "link" = passwordless magic link (the same flow as hqube.co/account, so one
  // hQube account works everywhere); "signin"/"signup" = email + password.
  const [mode, setMode] = useState<"link" | "signin" | "signup">(params.get("mode") === "signup" ? "signup" : params.get("mode") === "password" ? "signin" : "link");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(null);
    setNotice(null);
    const sb = supabaseBrowser();
    try {
      if (mode === "link") {
        const { error } = await sb.auth.signInWithOtp({
          email,
          options: { emailRedirectTo: `${location.origin}/auth/callback?next=${encodeURIComponent(params.get("next") || "/")}` },
        });
        if (error) throw error;
        setNotice("Check your email — the link signs you in (and creates your hQube account if you're new).");
        return;
      }
      if (mode === "signup") {
        const { data, error } = await sb.auth.signUp({
          email,
          password,
          options: { emailRedirectTo: `${location.origin}/auth/callback` },
        });
        if (error) throw error;
        if (!data.session) {
          setNotice("Check your email to confirm your account, then sign in.");
          return;
        }
      } else {
        const { error } = await sb.auth.signInWithPassword({ email, password });
        if (error) throw error;
      }
      router.replace(params.get("next") || "/");
      router.refresh();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <form onSubmit={submit} className="w-full max-w-sm space-y-4 rounded-lg border border-white/10 bg-white/[0.03] p-6">
      <div>
        <h1 className="text-lg font-semibold">{mode === "link" ? "Sign in or create your account" : mode === "signin" ? "Sign in with a password" : "Create your account"}</h1>
        <p className="text-sm text-zinc-400">
          {mode === "link"
            ? "Your hQube account works here too. We'll email you a secure link — no password needed."
            : "Verification (KYB) is completed by our team before trading unlocks."}
        </p>
      </div>
      <input className={inputCls} type="email" placeholder="you@company.com" value={email} onChange={(e) => setEmail(e.target.value)} required autoComplete="email" />
      {mode !== "link" && (
        <input className={inputCls} type="password" placeholder="Password" value={password} onChange={(e) => setPassword(e.target.value)} required minLength={8} autoComplete={mode === "signin" ? "current-password" : "new-password"} />
      )}
      <ErrorBanner error={error} />
      {notice && <p className="text-sm text-emerald-300">{notice}</p>}
      <button className={`${btnCls} w-full bg-brand text-navy-deep hover:bg-brand-dark hover:text-white`} disabled={busy}>
        {busy ? "…" : mode === "link" ? "Email me a sign-in link" : mode === "signin" ? "Sign in" : "Sign up"}
      </button>
      <div className="flex justify-between text-xs text-zinc-400">
        {mode === "link" ? (
          <button type="button" className="hover:text-white" onClick={() => setMode("signin")}>Use a password instead</button>
        ) : (
          <button type="button" className="hover:text-white" onClick={() => setMode("link")}>Email me a link instead</button>
        )}
        {mode !== "link" && (
          <button type="button" className="hover:text-white" onClick={() => setMode(mode === "signin" ? "signup" : "signin")}>
            {mode === "signin" ? "Create a password account" : "Have a password? Sign in"}
          </button>
        )}
      </div>
      <p className="text-center text-xs text-zinc-500">Same account as <a href="https://api.hqube.co/account" className="underline hover:text-zinc-300">hqube.co/account</a>.</p>
    </form>
  );
}

export default function LoginPage() {
  return (
    <div className="grid min-h-screen lg:grid-cols-2">
      <aside className="hidden flex-col justify-between bg-gradient-to-br from-navy to-navy-deep p-10 lg:flex">
        <Brand />
        <div>
          <CubeMark className="mb-6 h-10 w-10" />
          <h2 className="max-w-md text-3xl font-semibold leading-tight">GPU compute hours, traded like a commodity.</h2>
          <p className="mt-4 max-w-md text-zinc-300">Spot and forward markets, escrow settlement, verified hardware. Closed beta by hQube.</p>
        </div>
        <a href="https://hqube.co" className="text-sm text-zinc-400 hover:text-white">← hqube.co</a>
      </aside>
      <div className="flex flex-col items-center justify-center px-4 py-10">
        <div className="mb-6 lg:hidden"><Brand /></div>
        <Suspense>
          <LoginForm />
        </Suspense>
        <Link href="/" className="mt-6 text-xs text-zinc-500 hover:text-zinc-300">About hQube Exchange</Link>
      </div>
    </div>
  );
}
