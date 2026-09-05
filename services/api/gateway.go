// M1 gateway wiring: mounts authenticated, rate-limited, RBAC-gated proxy routes
// onto the API mux, fanning out to the downstream trading services.
//
// Env:
//
//	SUPABASE_URL          Project URL; drives ES256/JWKS verification (the
//	                      default — no secret needed). Unset, with no HS256
//	                      secret either, => protected routes fail closed
//	                      (503), unless AUTH_DEV_BYPASS=true (local dev only).
//	SUPABASE_ANON_KEY     Sent as the `apikey` header when fetching the JWKS.
//	SUPABASE_JWT_SECRET   Optional legacy HS256 secret. If set, HS256 is used
//	                      instead of ES256/JWKS.
//	AUTH_DEV_BYPASS       "true" to run with a synthetic admin identity (dev).
//	GATEWAY_SHARED_SECRET stamped on internal calls as X-Gateway-Secret so
//	                      downstreams can confirm the request came via us.
//	ORDER_ADDR, SETTLEMENT_ADDR, CATALOG_ADDR, MARKETDATA_ADDR, RISK_ADDR, COMPLIANCE_ADDR
//	                      base URLs of the downstream services, e.g.
//	                      http://cte-order:8090 . Unset => that route 503s.
//	RATE_LIMIT_RPS, RATE_LIMIT_BURST  per-account token-bucket tuning.
package main

import (
	"log"
	"net/http"
	"os"
	"strconv"
)

func envFloat(key string, fallback float64) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return fallback
}

// selectVerifier picks the JWT verifier at startup: an explicit HS256 secret
// takes precedence (legacy-configured project); otherwise ES256/JWKS backed
// by SUPABASE_URL, the default for a modern Supabase project; nil (fail
// closed, subject to AUTH_DEV_BYPASS) if neither is set.
func selectVerifier(hs256Secret, supabaseURL, anonKey string) signatureVerifier {
	if hs256Secret != "" {
		return hs256Verifier{secret: []byte(hs256Secret)}
	}
	if supabaseURL != "" {
		return newES256Verifier(supabaseURL, anonKey)
	}
	return nil
}

func registerGateway(mux *http.ServeMux, audit *auditor) {
	hs256Secret := os.Getenv("SUPABASE_JWT_SECRET")
	supabaseURL := os.Getenv("SUPABASE_URL")
	anonKey := os.Getenv("SUPABASE_ANON_KEY")
	devBypass := os.Getenv("AUTH_DEV_BYPASS") == "true"
	gwSecret := os.Getenv("GATEWAY_SHARED_SECRET")

	verifier := selectVerifier(hs256Secret, supabaseURL, anonKey)
	if verifier == nil && !devBypass {
		log.Print("no JWT verifier configured (SUPABASE_JWT_SECRET/SUPABASE_URL unset); gateway routes fail closed (503)")
	}
	if gwSecret == "" {
		log.Print("GATEWAY_SHARED_SECRET unset; downstream calls carry no gateway secret")
	}

	auth := newAuthenticator(verifier, devBypass)
	rl := newRateLimiter(
		envFloat("RATE_LIMIT_RPS", 20),
		envFloat("RATE_LIMIT_BURST", 40),
	)

	order := proxyHandler(os.Getenv("ORDER_ADDR"), gwSecret)
	settlement := proxyHandler(os.Getenv("SETTLEMENT_ADDR"), gwSecret)
	catalog := proxyHandler(os.Getenv("CATALOG_ADDR"), gwSecret)
	marketData := proxyHandler(os.Getenv("MARKETDATA_ADDR"), gwSecret)
	risk := proxyHandler(os.Getenv("RISK_ADDR"), gwSecret)
	compliance := proxyHandler(os.Getenv("COMPLIANCE_ADDR"), gwSecret)

	// protected wraps a downstream handler with auth -> audit -> rate-limit -> role/verified.
	protected := func(h http.Handler, mws ...func(http.Handler) http.Handler) http.Handler {
		base := []func(http.Handler) http.Handler{auth.middleware, audit.middleware, rl.middleware}
		return chain(h, append(base, mws...)...)
	}

	// Money out and every admin action need a second factor (Supabase aal2).
	mfa := requireMFA()

	// Trading: any KYB-verified trading role (buyer/seller/trader). admin passes too.
	trading := func(h http.Handler, extra ...func(http.Handler) http.Handler) http.Handler {
		return protected(h, append([]func(http.Handler) http.Handler{requireVerified(), requireRole("buyer", "seller", "trader")}, extra...)...)
	}

	// Orders (Go 1.22 method+wildcard patterns; downstream keeps its own paths).
	mux.Handle("POST /v1/orders", trading(order))
	mux.Handle("GET /v1/orders", trading(order))
	mux.Handle("DELETE /v1/orders/{id}", trading(order))

	// Positions (risk): held GPU-hours per market, for resale.
	mux.Handle("GET /v1/positions", trading(risk))

	// Surveillance alerts (compliance): admin review queue.
	mux.Handle("GET /v1/admin/alerts", protected(compliance, requireRole("admin")))
	mux.Handle("POST /v1/admin/alerts/{id}", protected(compliance, requireRole("admin"), mfa))

	// Escrow (settlement). Card deposits go through Stripe Checkout
	// (/checkout -> hosted page -> webhook credits escrow); the direct credit
	// endpoint is an operator tool, admin only. The Stripe webhook itself hits
	// settlement's public URL directly (signature-verified), not the gateway.
	mux.Handle("POST /v1/escrow/deposit", protected(settlement, requireRole("admin"), mfa))
	mux.Handle("POST /v1/escrow/checkout", trading(settlement))
	mux.Handle("POST /v1/escrow/withdraw", trading(settlement, mfa))
	mux.Handle("GET /v1/escrow/history", trading(settlement))
	mux.Handle("GET /v1/escrow/balance", trading(settlement))
	// Allocations (settlement): held GPU-hours; run them or resell on the book.
	mux.Handle("GET /v1/allocations", trading(settlement))
	mux.Handle("POST /v1/jobs/{id}/run", trading(settlement))
	mux.Handle("GET /v1/admin/payouts", protected(settlement, requireRole("admin")))
	mux.Handle("POST /v1/admin/payouts/{id}", protected(settlement, requireRole("admin"), mfa))
	// Fees (settlement): public schedule for the UI; platform revenue for admins.
	mux.Handle("GET /v1/fees", trading(settlement))
	mux.Handle("GET /v1/admin/revenue", protected(settlement, requireRole("admin")))
	mux.Handle("POST /v1/admin/revenue/payout", protected(settlement, requireRole("admin"), mfa))

	// Catalog: reference data. Reads are public (no auth); writes are admin-only.
	mux.Handle("GET /v1/gpu-types", catalog)
	mux.Handle("GET /v1/regions", catalog)
	mux.Handle("GET /v1/futures-contracts", catalog)
	mux.Handle("POST /v1/gpu-types", protected(catalog, requireRole("admin"), mfa))
	mux.Handle("POST /v1/regions", protected(catalog, requireRole("admin"), mfa))
	mux.Handle("POST /v1/futures-contracts", protected(catalog, requireRole("admin"), mfa))

	// Market data: public quotes (WS). Left unauthenticated for M1.
	mux.Handle("/v1/marketdata/", http.StripPrefix("/v1/marketdata", marketData))
}
