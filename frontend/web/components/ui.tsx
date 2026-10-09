export function Card({ title, children, right }: { title?: string; children: React.ReactNode; right?: React.ReactNode }) {
  return (
    <section className="rounded-lg border border-white/10 bg-white/[0.03]">
      {(title || right) && (
        <header className="flex items-center justify-between border-b border-white/10 px-4 py-2">
          <h2 className="text-sm font-medium text-zinc-300">{title}</h2>
          {right}
        </header>
      )}
      <div className="p-4">{children}</div>
    </section>
  );
}

export function ErrorBanner({ error }: { error: string | null }) {
  if (!error) return null;
  return <p className="rounded border border-red-900 bg-red-950/50 px-3 py-2 text-sm text-red-300">{error}</p>;
}

export function VerifyBanner() {
  return (
    <div className="rounded border border-amber-900 bg-amber-950/40 px-4 py-3 text-sm text-amber-200">
      <strong>Account pending verification.</strong> You can browse markets; trading and deposits unlock once the
      hQube team completes your KYB review. Questions: <a href="https://share.hsforms.com/11LYvC1dEQRS4cmGHZS5vJA3h8eq" className="underline">contact us</a>.
    </div>
  );
}

export const inputCls =
  "w-full rounded border border-white/15 bg-navy-deep px-3 py-2 text-sm outline-none focus:border-brand";
export const btnCls =
  "rounded px-3 py-2 text-sm font-medium disabled:opacity-50 disabled:cursor-not-allowed";
