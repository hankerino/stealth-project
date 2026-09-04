import { Nav } from "./nav";
import { Footer } from "./brand";
import { supabaseServer } from "@/lib/supabase-server";

/** Authenticated page chrome. Middleware guarantees a user exists here. */
export async function Shell({ children }: { children: React.ReactNode }) {
  const supabase = await supabaseServer();
  const { data } = await supabase.auth.getUser();
  return (
    <div className="flex min-h-screen flex-col">
      <Nav email={data.user?.email} />
      <main className="mx-auto w-full max-w-6xl flex-1 px-4 py-6">{children}</main>
      <Footer />
    </div>
  );
}
