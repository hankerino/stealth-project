"use client";

import { useState } from "react";
import Link from "next/link";
import { Brand, CubeMark, Footer } from "./brand";
import { btnCls, inputCls } from "./ui";

const perks = [
  ["You set the opening prices", "The first asks in each GPU class define the market. Founding providers list before buyers arrive."],
  ["Paid on settlement, from escrow", "Buyer funds are held before a trade matches and released to you the moment the job completes. No invoicing, no chasing."],
  ["One-line install, outbound only", "A small agent registers your node, heartbeats, and runs buyer jobs in containers. No inbound ports. Uninstall any time."],
  ["Preferred placement + a direct line", "Founding nodes are matched first at equal price, and you talk to the people building the market."],
];

const steps = [
  "Apply below with what you have (even one box counts).",
  "We reply within two business days with a per-provider registration token.",
  "Run the install script on your Ubuntu/Debian machine with an NVIDIA driver.",
  "Your hours appear on the book. Sell at market or name your price.",
];

export function SellPage() {
  return (
    <div className="flex min-h-screen flex-col">
      <header className="border-b border-white/10">
        <div className="mx-auto flex max-w-6xl items-center justify-between px-4 py-3">
          <Brand />
          <div className="flex items-center gap-3 text-sm">
            <Link href="/" className="hidden text-zinc-400 hover:text-white sm:inline">Markets</Link>
            <Link href="/login" className="rounded border border-white/15 px-3 py-1.5 hover:bg-white/10">Sign in</Link>
          </div>
        </div>
      </header>

      <main className="flex-1">
        <section className="bg-gradient-to-b from-navy to-navy-deep">
          <div className="mx-auto max-w-6xl px-4 py-16 sm:py-20">
            <p className="mb-3 text-xs font-medium uppercase tracking-widest text-brand">Founding providers</p>
            <h1 className="max-w-3xl text-4xl font-semibold leading-tight sm:text-5xl">
              Your GPUs are earning $0 while they sleep.
            </h1>
            <p className="mt-5 max-w-2xl text-lg text-zinc-300">
              A single H100 rents for $1.50–3.30 an hour on today&apos;s market. An 8-GPU box idle 12 hours a day leaves $150–300 on the table. Every day.
              hQube Exchange puts those hours on a live order book where buyers bid for them.
            </p>
            <a href="#apply" className="mt-8 inline-block rounded bg-brand px-5 py-2.5 font-medium text-navy-deep hover:bg-brand-dark hover:text-white">Apply as a founding provider</a>
          </div>
        </section>

        <section className="mx-auto max-w-6xl px-4 py-14">
          <div className="grid gap-6 sm:grid-cols-2">
            {perks.map(([t, b]) => (
              <div key={t} className="rounded-lg border border-white/10 bg-white/[0.03] p-6">
                <CubeMark className="mb-4 h-5 w-5" />
                <h2 className="text-base font-semibold">{t}</h2>
                <p className="mt-2 text-sm leading-relaxed text-zinc-400">{b}</p>
              </div>
            ))}
          </div>

          <div className="mt-14 grid gap-10 lg:grid-cols-5">
            <div className="lg:col-span-2">
              <h2 className="text-xl font-semibold">How onboarding works</h2>
              <ol className="mt-4 space-y-3 text-sm text-zinc-300">
                {steps.map((s, i) => (
                  <li key={s} className="flex gap-3"><span className="font-mono text-brand">{i + 1}.</span><span>{s}</span></li>
                ))}
              </ol>
              <p className="mt-6 text-xs text-zinc-500">
                Fees: 2.5% of each settled trade, deducted from proceeds. No listing fee, no minimum commitment. Details in the <a className="text-brand hover:underline" href="https://github.com/hankerino/stealth-project/blob/main/docs/FEES.md">fee schedule</a>; technical setup in the <a className="text-brand hover:underline" href="https://github.com/hankerino/stealth-project/blob/main/docs/SELLER_ONBOARDING.md">onboarding guide</a>.
              </p>
            </div>
            <div className="lg:col-span-3"><ApplyForm /></div>
          </div>
        </section>
      </main>
      <Footer />
    </div>
  );
}

function ApplyForm() {
  const [f, setF] = useState({ name: "", email: "", company: "", gpus: "", location: "", notes: "", website: "" });
  const [busy, setBusy] = useState(false);
  const [done, setDone] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const set = (k: keyof typeof f) => (e: React.ChangeEvent<HTMLInputElement | HTMLTextAreaElement>) => setF({ ...f, [k]: e.target.value });

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true); setErr(null);
    try {
      const r = await fetch("/api/gw/v1/providers/apply", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(f) });
      if (!r.ok) {
        const j = (await r.json().catch(() => ({}))) as { message?: string; error?: string };
        throw new Error(j.message ?? j.error ?? r.statusText);
      }
      setDone(true);
    } catch (e) {
      setErr((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  if (done) {
    return (
      <div id="apply" className="rounded-lg border border-brand/40 bg-brand/10 p-6">
        <h2 className="text-lg font-semibold">Application received.</h2>
        <p className="mt-2 text-sm text-zinc-300">We&apos;ll reply to <span className="font-mono">{f.email}</span> within two business days with your registration token and next steps.</p>
        <p className="mt-4 text-sm text-zinc-400">Meanwhile you can <Link href="/login?mode=signup" className="text-brand hover:underline">create your account</Link> so it&apos;s ready when the token arrives.</p>
      </div>
    );
  }

  return (
    <form id="apply" onSubmit={submit} className="rounded-lg border border-white/10 bg-white/[0.03] p-6">
      <h2 className="text-lg font-semibold">Apply as a founding provider</h2>
      <p className="mt-1 text-sm text-zinc-400">Takes a minute. No account needed yet.</p>
      <div className="mt-5 grid gap-4 sm:grid-cols-2">
        <label className="block text-xs text-zinc-400">Name
          <input className={`${inputCls} mt-1`} required maxLength={200} value={f.name} onChange={set("name")} autoComplete="name" />
        </label>
        <label className="block text-xs text-zinc-400">Email
          <input className={`${inputCls} mt-1`} required type="email" maxLength={254} value={f.email} onChange={set("email")} autoComplete="email" />
        </label>
        <label className="block text-xs text-zinc-400">Company (optional)
          <input className={`${inputCls} mt-1`} maxLength={200} value={f.company} onChange={set("company")} autoComplete="organization" />
        </label>
        <label className="block text-xs text-zinc-400">Location / data center (optional)
          <input className={`${inputCls} mt-1`} maxLength={200} placeholder="e.g. Frankfurt, colo" value={f.location} onChange={set("location")} />
        </label>
        <label className="block text-xs text-zinc-400 sm:col-span-2">GPUs you can list
          <input className={`${inputCls} mt-1`} required maxLength={1000} placeholder="e.g. 8× H100 SXM 80GB, 16× A100 80GB — idle nights and weekends" value={f.gpus} onChange={set("gpus")} />
        </label>
        <label className="block text-xs text-zinc-400 sm:col-span-2">Anything else (optional)
          <textarea className={`${inputCls} mt-1 min-h-24`} maxLength={2000} placeholder="Bandwidth, availability windows, questions…" value={f.notes} onChange={set("notes")} />
        </label>
        {/* Honeypot: hidden from humans, filled by bots. */}
        <input tabIndex={-1} autoComplete="off" className="hidden" name="website" value={f.website} onChange={set("website")} />
      </div>
      {err && <p className="mt-3 text-sm text-red-300">{err}</p>}
      <button className={`${btnCls} mt-5 bg-brand text-navy-deep hover:bg-brand-dark hover:text-white`} disabled={busy}>
        {busy ? "Sending…" : "Send application"}
      </button>
      <p className="mt-3 text-xs text-zinc-500">We use this only to onboard you as a provider. hQube Automation LLC · <a className="hover:text-zinc-300" href="https://hqube.co/privacy-policy">Privacy</a></p>
    </form>
  );
}
