package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// captureServer records the headers a downstream actually receives.
func captureServer(t *testing.T, gotHeaders *http.Header) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*gotHeaders = r.Header.Clone()
		w.WriteHeader(http.StatusOK)
	}))
}

func TestProxy_InjectsVerifiedIdentity(t *testing.T) {
	var got http.Header
	backend := captureServer(t, &got)
	defer backend.Close()

	target, _ := url.Parse(backend.URL)
	rp := newReverseProxy(target, "gw-secret")

	req := httptest.NewRequest(http.MethodGet, "/v1/orders", nil)
	req = req.WithContext(withClaims(req.Context(), &Claims{
		Subject: "acct-uuid", Role: "trader", Verified: true,
	}))
	rr := httptest.NewRecorder()
	rp.ServeHTTP(rr, req)

	if got.Get(headerAccountID) != "acct-uuid" {
		t.Errorf("X-Account-Id = %q", got.Get(headerAccountID))
	}
	if got.Get(headerAccountRole) != "trader" {
		t.Errorf("X-Account-Role = %q", got.Get(headerAccountRole))
	}
	if got.Get(headerAccountVerified) != "true" {
		t.Errorf("X-Account-Verified = %q", got.Get(headerAccountVerified))
	}
	if got.Get(headerGatewaySecret) != "gw-secret" {
		t.Errorf("X-Gateway-Secret = %q", got.Get(headerGatewaySecret))
	}
}

// The critical security test: a client trying to smuggle its own identity
// headers must have them overwritten, never trusted.
func TestProxy_StripsSpoofedIdentity(t *testing.T) {
	var got http.Header
	backend := captureServer(t, &got)
	defer backend.Close()

	target, _ := url.Parse(backend.URL)
	rp := newReverseProxy(target, "gw-secret")

	req := httptest.NewRequest(http.MethodGet, "/v1/orders", nil)
	// Attacker-supplied headers:
	req.Header.Set(headerAccountID, "victim-account")
	req.Header.Set(headerAccountRole, "admin")
	req.Header.Set(headerGatewaySecret, "guessed-secret")
	// Real verified identity from the JWT:
	req = req.WithContext(withClaims(req.Context(), &Claims{
		Subject: "real-account", Role: "buyer", Verified: false,
	}))

	rr := httptest.NewRecorder()
	rp.ServeHTTP(rr, req)

	if got.Get(headerAccountID) != "real-account" {
		t.Errorf("spoofed account id leaked: %q", got.Get(headerAccountID))
	}
	if got.Get(headerAccountRole) != "buyer" {
		t.Errorf("spoofed role leaked: %q", got.Get(headerAccountRole))
	}
	if got.Get(headerAccountVerified) != "false" {
		t.Errorf("spoofed verified leaked: %q", got.Get(headerAccountVerified))
	}
	if got.Get(headerGatewaySecret) != "gw-secret" {
		t.Errorf("gateway secret should be the real one, got %q", got.Get(headerGatewaySecret))
	}
}

func TestProxy_StripsIdentityWhenNoClaims(t *testing.T) {
	var got http.Header
	backend := captureServer(t, &got)
	defer backend.Close()

	target, _ := url.Parse(backend.URL)
	rp := newReverseProxy(target, "")

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set(headerAccountID, "smuggled")
	rr := httptest.NewRecorder()
	rp.ServeHTTP(rr, req)

	if got.Get(headerAccountID) != "" {
		t.Errorf("identity should be stripped when no claims, got %q", got.Get(headerAccountID))
	}
}

func TestProxyHandler_UnconfiguredReturns503(t *testing.T) {
	h := proxyHandler("", "secret")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/x", nil))
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("want 503, got %d", rr.Code)
	}
}
