import type { NextConfig } from "next";

// The browser talks to the trading API gateway through this same-origin
// prefix so no CORS changes are needed on the backend. The Authorization
// header (Supabase access token) is forwarded as-is by the rewrite.
const gateway = (process.env.GATEWAY_URL ?? "http://localhost:8443").replace(/\/$/, "");

// Origins the browser is allowed to reach. Everything else is blocked by CSP.
const supabase = process.env.NEXT_PUBLIC_SUPABASE_URL ?? "https://szaxkuxpcasugvprapsq.supabase.co";
const marketData = process.env.NEXT_PUBLIC_MARKETDATA_WS_URL ?? "wss://cte-market-data.onrender.com";

const csp = [
  "default-src 'self'",
  // Next.js hydration needs inline scripts; no third-party scripts are loaded at all.
  "script-src 'self' 'unsafe-inline'",
  "style-src 'self' 'unsafe-inline' https://fonts.googleapis.com",
  "font-src 'self' https://fonts.gstatic.com",
  "img-src 'self' data:",
  `connect-src 'self' ${supabase} ${marketData}`,
  "frame-ancestors 'none'", // never embeddable (clickjacking)
  "form-action 'self' https://checkout.stripe.com",
  "base-uri 'self'",
  "object-src 'none'",
  "upgrade-insecure-requests",
].join("; ");

const securityHeaders = [
  { key: "Content-Security-Policy", value: csp },
  { key: "Strict-Transport-Security", value: "max-age=63072000; includeSubDomains; preload" },
  { key: "X-Content-Type-Options", value: "nosniff" },
  { key: "X-Frame-Options", value: "DENY" },
  { key: "Referrer-Policy", value: "strict-origin-when-cross-origin" },
  { key: "Permissions-Policy", value: "camera=(), microphone=(), geolocation=(), payment=(), usb=()" },
  { key: "Cross-Origin-Opener-Policy", value: "same-origin" },
];

const nextConfig: NextConfig = {
  output: "standalone",
  poweredByHeader: false,
  async rewrites() {
    return [{ source: "/api/gw/:path*", destination: `${gateway}/:path*` }];
  },
  async headers() {
    return [{ source: "/(.*)", headers: securityHeaders }];
  },
};

export default nextConfig;
