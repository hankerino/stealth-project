// Reverse-proxy fan-out for the trading-engine API gateway (M1).
//
// After auth + RBAC + rate-limiting, the gateway forwards the request to the
// owning downstream service (order, settlement, catalog, market-data). The
// proxy is the trust boundary: it STRIPS any client-supplied identity headers
// and INJECTS the verified ones derived from the JWT, plus a shared secret that
// lets the downstream confirm the request actually came through the gateway
// (defense against a downstream's public URL being hit directly).
//
// Uses net/http/httputil.ReverseProxy from the standard library — no deps.
package main

import (
	"net/http"
	"net/http/httputil"
	"net/url"
)

// Trusted internal headers. The gateway is the only writer; the proxy removes
// any inbound copy before setting its own.
const (
	headerAccountID       = "X-Account-Id"
	headerAccountRole     = "X-Account-Role"
	headerAccountVerified = "X-Account-Verified"
	headerGatewaySecret   = "X-Gateway-Secret"
)

// newReverseProxy builds a ReverseProxy to target, rewriting each request to
// carry verified identity + the gateway secret. gatewaySecret may be empty
// (dev), in which case no secret header is added.
func newReverseProxy(target *url.URL, gatewaySecret string) *httputil.ReverseProxy {
	rp := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.SetXForwarded()

			// Always strip any client-supplied identity headers first, whether
			// or not we have claims — a caller must never be able to smuggle
			// these in.
			pr.Out.Header.Del(headerAccountID)
			pr.Out.Header.Del(headerAccountRole)
			pr.Out.Header.Del(headerAccountVerified)
			pr.Out.Header.Del(headerGatewaySecret)

			if c, ok := claimsFrom(pr.In.Context()); ok {
				pr.Out.Header.Set(headerAccountID, c.Subject)
				pr.Out.Header.Set(headerAccountRole, c.effectiveRole())
				if c.Verified {
					pr.Out.Header.Set(headerAccountVerified, "true")
				} else {
					pr.Out.Header.Set(headerAccountVerified, "false")
				}
			}

			if gatewaySecret != "" {
				pr.Out.Header.Set(headerGatewaySecret, gatewaySecret)
			}
		},
	}
	return rp
}

// mustProxy builds a proxy handler from an env URL. If the env var is unset it
// returns a handler that 503s, matching the codebase's "unconfigured dep is
// unavailable, not fatal" convention (but per-route rather than process-wide).
func proxyHandler(rawURL, gatewaySecret string) http.Handler {
	if rawURL == "" {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			writeError(w, http.StatusServiceUnavailable, "downstream not configured")
		})
	}
	target, err := url.Parse(rawURL)
	if err != nil {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			writeError(w, http.StatusServiceUnavailable, "downstream misconfigured")
		})
	}
	return newReverseProxy(target, gatewaySecret)
}
