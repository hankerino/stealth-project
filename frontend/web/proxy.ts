import { createServerClient } from "@supabase/ssr";
import { NextResponse, type NextRequest } from "next/server";

const PUBLIC = new Set(["/", "/login", "/auth/callback"]); // "/" renders the landing page for visitors

/** Refreshes the Supabase session cookie on every request and redirects
 *  anonymous users to /login. The /api/gw proxy is excluded: it carries its
 *  own Bearer token and the gateway does the authentication. */
export async function proxy(req: NextRequest) {
  let res = NextResponse.next({ request: req });
  const supabase = createServerClient(
    process.env.NEXT_PUBLIC_SUPABASE_URL!,
    process.env.NEXT_PUBLIC_SUPABASE_ANON_KEY!,
    {
      cookies: {
        getAll: () => req.cookies.getAll(),
        setAll: (toSet) => {
          toSet.forEach(({ name, value }) => req.cookies.set(name, value));
          res = NextResponse.next({ request: req });
          toSet.forEach(({ name, value, options }) => res.cookies.set(name, value, options));
        },
      },
    },
  );

  const path = req.nextUrl.pathname;
  // Magic-link / PKCE landing on any path (Supabase may redirect to the site
  // root when the requested callback URL is not in its allow-list): hand the
  // code to /auth/callback so the session cookie gets set.
  const code = req.nextUrl.searchParams.get("code");
  if (code && path !== "/auth/callback") {
    const url = req.nextUrl.clone();
    url.pathname = "/auth/callback";
    url.search = "";
    url.searchParams.set("code", code);
    url.searchParams.set("next", path === "/login" ? "/" : path);
    return NextResponse.redirect(url);
  }

  const { data } = await supabase.auth.getUser();
  if (!data.user && !PUBLIC.has(path)) {
    const url = req.nextUrl.clone();
    url.pathname = "/login";
    url.searchParams.set("next", path);
    return NextResponse.redirect(url);
  }
  if (data.user && path === "/login") {
    const url = req.nextUrl.clone();
    url.pathname = "/";
    url.search = "";
    return NextResponse.redirect(url);
  }
  return res;
}

export const config = {
  matcher: ["/((?!api/gw|_next/static|_next/image|favicon.ico|.*\\.(?:svg|png|ico)$).*)"],
};
