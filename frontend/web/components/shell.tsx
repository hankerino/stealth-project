import { Nav } from "./nav";
import { supabaseServer } from "@/lib/supabase-server";

/** Authenticated page chrome. Middleware guarantees a user exists here. */
export async function Shell({ children }: { children: React.ReactNode }) {
  const supabase = await supabaseServer();
  const { data } = await supabase.auth.getUser();
  return (
    <>
      <Nav email={data.user?.email} />
      <main className="mx-auto max-w-6xl px-4 py-6">{children}</main>
    </>
  );
}
