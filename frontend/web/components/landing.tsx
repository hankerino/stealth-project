import Link from "next/link";
import { Brand, CubeMark, Footer } from "./brand";

const features = [
  {
    title: "Buy GPU-hours like a commodity",
    body: "H100, A100 and B200 hours by region, on a real order book with live bid/ask. Pay from escrow; the hours are yours to run when you want.",
  },
  {
    title: "Run it or resell it",
    body: "A purchase is an allocation you hold. Launch a workload on the backing node, or sell unused hours back into the same market.",
  },
  {
    title: "Verified supply, escrow-backed settlement",
    body: "Sellers' nodes register with signed telemetry and heartbeats. Buyer funds are held in escrow and released only when the job completes.",
  },
  {
    title: "Forwards and institutional access",
    body: "Fixed-delivery forward contracts with margin and daily mark-to-market. FIX 4.4 for desks; a historical prices API for everyone.",
  },
];

/** Logged-out home page. */
export function Landing() {
  return (
    <div className="flex min-h-screen flex-col">
      <header className="border-b border-white/10">
        <div className="mx-auto flex max-w-6xl items-center justify-between px-4 py-3">
          <Brand />
          <div className="flex items-center gap-3 text-sm">
            <a href="https://hqube.co" className="hidden text-zinc-400 hover:text-white sm:inline">hqube.co</a>
            <Link href="/login" className="rounded border border-white/15 px-3 py-1.5 hover:bg-white/10">Sign in</Link>
            <Link href="/login?mode=signup" className="rounded bg-brand px-3 py-1.5 font-medium text-navy-deep hover:bg-brand-dark hover:text-white">Create account</Link>
          </div>
        </div>
      </header>

      <main className="flex-1">
        <section className="bg-gradient-to-b from-navy to-navy-deep">
          <div className="mx-auto max-w-6xl px-4 py-16 sm:py-24">
            <p className="mb-3 text-xs font-medium uppercase tracking-widest text-brand">Closed beta</p>
            <h1 className="max-w-3xl text-4xl font-semibold leading-tight sm:text-5xl">
              The exchange for GPU compute hours.
            </h1>
            <p className="mt-5 max-w-2xl text-lg text-zinc-300">
              Spot and forward markets for H100-class capacity, priced per GPU-hour, settled from escrow on verified hardware. Buy what you need, sell what you don&apos;t.
            </p>
            <div className="mt-8 flex flex-wrap gap-3">
              <Link href="/login?mode=signup" className="rounded bg-brand px-5 py-2.5 font-medium text-navy-deep hover:bg-brand-dark hover:text-white">Create an account</Link>
              <Link href="/sell" className="rounded border border-white/20 px-5 py-2.5 hover:bg-white/10">Sell capacity</Link>
            </div>
            <dl className="mt-12 grid max-w-3xl grid-cols-2 gap-6 text-sm sm:grid-cols-4">
              {[["H100 · A100 · B200", "GPU types"], ["Spot + forwards", "Markets"], ["Escrow", "Settlement"], ["FIX 4.4 · REST", "Access"]].map(([v, k]) => (
                <div key={k}><dt className="text-zinc-500">{k}</dt><dd className="mt-1 font-medium">{v}</dd></div>
              ))}
            </dl>
          </div>
        </section>

        <section className="mx-auto max-w-6xl px-4 py-14">
          <div className="grid gap-6 sm:grid-cols-2">
            {features.map((f) => (
              <div key={f.title} className="rounded-lg border border-white/10 bg-white/[0.03] p-6">
                <CubeMark className="mb-4 h-5 w-5" />
                <h2 className="text-base font-semibold">{f.title}</h2>
                <p className="mt-2 text-sm leading-relaxed text-zinc-400">{f.body}</p>
              </div>
            ))}
          </div>
          <p className="mt-10 text-sm text-zinc-500">
            Accounts are verified (KYB) before trading is enabled. hQube Exchange is operated by hQube Automation LLC and is in closed beta; contact us at <a href="https://hqube.co/contact-us" className="text-brand hover:underline">hqube.co</a> for access.
          </p>
        </section>
      </main>
      <Footer />
    </div>
  );
}
