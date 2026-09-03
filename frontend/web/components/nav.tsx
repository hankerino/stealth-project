"use client";

import Link from "next/link";
import { usePathname, useRouter } from "next/navigation";
import { supabaseBrowser } from "@/lib/supabase-browser";

const links = [
  { href: "/", label: "Markets" },
  { href: "/portfolio", label: "Portfolio" },
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
    <header className="border-b border-zinc-800">
      <div className="mx-auto flex max-w-6xl items-center gap-6 px-4 py-3">
        <Link href="/" className="font-semibold tracking-tight">
          <span className="text-emerald-400">▲</span> Compute Exchange
        </Link>
        <nav className="flex gap-4 text-sm">
          {links.map((l) => (
            <Link
              key={l.href}
              href={l.href}
              className={path === l.href ? "text-white" : "text-zinc-400 hover:text-white"}
            >
              {l.label}
            </Link>
          ))}
        </nav>
        <div className="ml-auto flex items-center gap-3 text-sm text-zinc-400">
          {email && <span className="hidden sm:inline">{email}</span>}
          <button onClick={signOut} className="rounded border border-zinc-700 px-2 py-1 hover:bg-zinc-800">
            Sign out
          </button>
        </div>
      </div>
    </header>
  );
}
