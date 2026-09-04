"use client";

import Link from "next/link";
import { usePathname, useRouter } from "next/navigation";
import { supabaseBrowser } from "@/lib/supabase-browser";
import { Brand } from "./brand";

const links = [
  { href: "/", label: "Markets" },
  { href: "/portfolio", label: "Portfolio" },
  { href: "/settings/security", label: "Security" },
];

export function Nav({ email }: { email?: string | null }) {
  const path = usePathname();
  const router = useRouter();
  const signOut = async () => {
    await supabaseBrowser().auth.signOut();
    router.replace("/login");
    router.refresh();
  };
  return (
    <header className="sticky top-0 z-20 border-b border-white/10 bg-navy/90 backdrop-blur">
      <div className="mx-auto flex max-w-6xl items-center gap-4 px-4 py-3 sm:gap-6">
        <Brand compact />
        <nav className="flex gap-4 text-sm">
          {links.map((l) => {
            const active = l.href === "/" ? path === "/" || path.startsWith("/trade") : path.startsWith(l.href);
            return (
              <Link key={l.href} href={l.href} className={active ? "text-white" : "text-zinc-400 hover:text-white"}>
                {l.label}
              </Link>
            );
          })}
        </nav>
        <div className="ml-auto flex items-center gap-3 text-sm text-zinc-400">
          {email && <span className="hidden md:inline">{email}</span>}
          <button onClick={signOut} className="rounded border border-white/15 px-2 py-1 hover:bg-white/10">
            Sign out
          </button>
        </div>
      </div>
    </header>
  );
}
