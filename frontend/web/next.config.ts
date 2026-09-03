import type { NextConfig } from "next";

// The browser talks to the trading API gateway through this same-origin
// prefix so no CORS changes are needed on the backend. The Authorization
// header (Supabase access token) is forwarded as-is by the rewrite.
const gateway = (process.env.GATEWAY_URL ?? "http://localhost:8443").replace(/\/$/, "");

const nextConfig: NextConfig = {
  output: "standalone",
  async rewrites() {
    return [{ source: "/api/gw/:path*", destination: `${gateway}/:path*` }];
  },
};

export default nextConfig;
