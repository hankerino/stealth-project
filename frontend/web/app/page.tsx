import { Shell } from "@/components/shell";
import { Markets } from "@/components/markets";
import { Landing } from "@/components/landing";
import { supabaseServer } from "@/lib/supabase-server";

/** "/" is public: the landing page for visitors, the markets for signed-in users. */
export default async function Home() {
  const supabase = await supabaseServer();
  const { data } = await supabase.auth.getUser();
  if (!data.user) return <Landing />;
  return (
    <Shell>
      <Markets />
    </Shell>
  );
}
