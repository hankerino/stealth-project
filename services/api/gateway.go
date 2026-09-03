// M1 gateway wiring: mounts authenticated, rate-limited, RBAC-gated proxy routes
// onto the API mux, fanning out to the downstream trading services.
//
// Env:
//
//	SUPABASE_JWT_SECRET   HS256 secret for verifying Supabase access tokens.
//	                      Unset => protected routes fail closed (503), unless
//	                      AUTH_DEV_BYPASS=true (local dev only).
//	AUTH_DEV_BYPASS       "true" to run with a synthetic admin identity (dev).
//	GATEWAY_SHARED_SECRET stamped on internal calls as X-Gateway-Secret so
//	                      downstreams can confirm the request came via us.
//	ORDER_ADDR, SETTLEMENT_ADDR, CATALOG_ADDR, MARKETDATA_ADDR
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

func registerGateway(mux *http.ServeMux) {
	secret := os.Getenv("SUPABASE_JWT_SECRET")
	devBypass := os.Getenv("AUTH_DEV_BYPASS") == "true"
	gwSecret := os.Getenv("GATEWAY_SHARED_SECRET")

	if secret == "" && !devBypass {
		log.Print("SUPABASE_JWT_SECRET unset; gateway routes fail closed (503)")
	}
	if gwSecret == "" {
		log.Print("GATEWAY_SHARED_SECRET unset; downstream calls carry no gateway secret")
	}

	auth := newAuthenticator(secret, devBypass)
	rl := newRateLimiter(
		envFloat("RATE_LIMIT_RPS", 20),
		envFloat("RATE_LIMIT_BURST", 40),
	)

	order := proxyHandler(os.Getenv("ORDER_ADDR"), gwSecret)
	settlement := proxyHandler(os.Getenv("SETTLEMENT_ADDR"), gwSecret)
	catalog := proxyHandler(os.Getenv("CATALOG_ADDR"), gwSecret)
	marketData := proxyHandler(os.Getenv("MARKETDATA_ADDR"), gwSecret)

	// protected wraps a downstream handler with auth -> rate-limit -> role/verified.
	protected := func(h http.Handler, mws ...func(http.Handler) http.Handler) http.Handler {
		base := []func(http.Handler) http.Handler{auth.middleware, rl.middleware}
		return chain(h, append(base, mws...)...)
	}

	// Trading: any KYB-verified trading role (buyer/seller/trader). admin passes too.
	trading := func(h http.Handler) http.Handler {
		return protected(h, requireVerified(), requireRole("buyer", "seller", "trader"))
	}

	// Orders (Go 1.22 method+wildcard patterns; downstream keeps its own paths).
	mux.Handle("POST /v1/orders", trading(order))
	mux.Handle("GET /v1/orders", trading(order))
	mux.Handle("DELETE /v1/orders/{id}", trading(order))

	// Escrow (settlement).
	mux.Handle("POST /v1/escrow/deposit", trading(settlement))
	mux.Handle("GET /v1/escrow/balance", trading(settlement))

	// Catalog: reference data. Reads are public (no auth); writes are admin-only.
	mux.Handle("GET /v1/gpu-types", catalog)
	mux.Handle("GET /v1/regions", catalog)
	mux.Handle("GET /v1/futures-contracts", catalog)
	mux.Handle("POST /v1/gpu-types", protected(catalog, requireRole("admin")))
	mux.Handle("POST /v1/regions", protected(catalog, requireRole("admin")))
	mux.Handle("POST /v1/futures-contracts", protected(catalog, requireRole("admin")))

	// Market data: public quotes (WS). Left unauthenticated for M1.
	mux.Handle("/v1/marketdata/", http.StripPrefix("/v1/marketdata", marketData))
}
