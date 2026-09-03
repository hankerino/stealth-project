import { createServerClient } from "@supabase/ssr";
import { cookies } from "next/headers";

/** Server-component client. Read-only cookie access is enough for reading
 *  the session; token refresh is handled by proxy.ts. */
export async function supabaseServer() {
  const store = await cookies();
  return createServerClient(
    process.env.NEXT_PUBLIC_SUPABASE_URL!,
    process.env.NEXT_PUBLIC_SUPABASE_ANON_KEY!,
    {
      cookies: {
        getAll: () => store.getAll(),
        setAll: () => {
          /* server components cannot set cookies; proxy.ts refreshes */
        },
      },
    },
  );
}
