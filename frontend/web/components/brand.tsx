import Link from "next/link";

/** hQube cube mark — three faces, brand green on navy. */
export function CubeMark({ className = "h-6 w-6" }: { className?: string }) {
  return (
    <svg viewBox="0 0 32 32" className={className} aria-hidden="true">
      <path d="M16 3 29 10.5v11L16 29 3 21.5v-11z" fill="#1dbf74" />
      <path d="M16 3v13L3 10.5z" fill="#4fd696" />
      <path d="M16 16v13L3 21.5v-11z" fill="#159a5d" />
      <path d="M16 16 29 10.5v11L16 29z" fill="#12804e" />
    </svg>
  );
}

/** Wordmark: hQube Exchange. `compact` hides the product word on small screens. */
export function Brand({ href = "/", compact = false }: { href?: string; compact?: boolean }) {
  return (
    <Link href={href} className="flex items-center gap-2 font-semibold tracking-tight">
      <CubeMark />
      <span className="text-lg leading-none">
        h<span className="text-brand">Q</span>ube
        <span className={`font-normal text-zinc-400 ${compact ? "hidden sm:inline" : ""}`}> Exchange</span>
      </span>
    </Link>
  );
}

export function Footer() {
  return (
    <footer className="mt-12 border-t border-zinc-800">
      <div className="mx-auto flex max-w-6xl flex-col gap-3 px-4 py-6 text-xs text-zinc-500 sm:flex-row sm:items-center sm:justify-between">
        <div className="flex items-center gap-2">
          <CubeMark className="h-4 w-4" />
          <span>© {new Date().getFullYear()} hQube Automation LLC · hQube Exchange (closed beta)</span>
        </div>
        <nav className="flex flex-wrap gap-4">
          <a href="https://hqube.co" className="hover:text-zinc-300">hqube.co</a>
          <a href="https://hqube.co/contact-us" className="hover:text-zinc-300">Contact</a>
          <a href="/sell" className="hover:text-zinc-300">Sell capacity</a>
          <a href="https://hqube.co/privacy-policy" className="hover:text-zinc-300">Privacy</a>
          <a href="https://hqube.co/terms" className="hover:text-zinc-300">Terms</a>
        </nav>
      </div>
    </footer>
  );
}
