package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
}

func doWithClaims(h http.Handler, c *Claims) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	if c != nil {
		req = req.WithContext(withClaims(req.Context(), c))
	}
	h.ServeHTTP(rr, req)
	return rr
}

func TestRequireRole(t *testing.T) {
	h := requireRole("trader", "seller")(okHandler())

	if rr := doWithClaims(h, &Claims{Role: "trader"}); rr.Code != http.StatusOK {
		t.Errorf("trader should pass, got %d", rr.Code)
	}
	if rr := doWithClaims(h, &Claims{Role: "buyer"}); rr.Code != http.StatusForbidden {
		t.Errorf("buyer should be 403, got %d", rr.Code)
	}
	// admin is a superset — always allowed.
	if rr := doWithClaims(h, &Claims{Role: "admin"}); rr.Code != http.StatusOK {
		t.Errorf("admin should pass, got %d", rr.Code)
	}
	// no claims -> 401
	if rr := doWithClaims(h, nil); rr.Code != http.StatusUnauthorized {
		t.Errorf("missing claims should be 401, got %d", rr.Code)
	}
}

func TestRequireVerified(t *testing.T) {
	h := requireVerified()(okHandler())

	if rr := doWithClaims(h, &Claims{Role: "buyer", Verified: true}); rr.Code != http.StatusOK {
		t.Errorf("verified buyer should pass, got %d", rr.Code)
	}
	if rr := doWithClaims(h, &Claims{Role: "buyer", Verified: false}); rr.Code != http.StatusForbidden {
		t.Errorf("unverified should be 403, got %d", rr.Code)
	}
	// admin bypasses verification.
	if rr := doWithClaims(h, &Claims{Role: "admin", Verified: false}); rr.Code != http.StatusOK {
		t.Errorf("admin should bypass verification, got %d", rr.Code)
	}
}

func TestRateLimiter_BurstThenBlock(t *testing.T) {
	rl := newRateLimiter(1, 3) // 1 rps, burst 3
	frozen := time.Now()
	rl.now = func() time.Time { return frozen }

	// First 3 allowed (burst), 4th blocked at the same instant.
	for i := 0; i < 3; i++ {
		if !rl.allow("acct") {
			t.Fatalf("request %d should be allowed within burst", i+1)
		}
	}
	if rl.allow("acct") {
		t.Fatal("4th request should be blocked")
	}

	// Different account has its own bucket.
	if !rl.allow("other") {
		t.Fatal("separate account should have its own bucket")
	}
}

func TestRateLimiter_RefillsOverTime(t *testing.T) {
	rl := newRateLimiter(1, 1) // 1 rps, burst 1
	now := time.Now()
	rl.now = func() time.Time { return now }

	if !rl.allow("a") {
		t.Fatal("first should pass")
	}
	if rl.allow("a") {
		t.Fatal("second immediate should be blocked")
	}
	now = now.Add(time.Second) // one token refills
	if !rl.allow("a") {
		t.Fatal("after 1s a token should be available")
	}
}

func TestRateLimiter_Middleware429(t *testing.T) {
	rl := newRateLimiter(0.0001, 1) // effectively 1 then blocked
	frozen := time.Now()
	rl.now = func() time.Time { return frozen }
	h := rl.middleware(okHandler())

	c := &Claims{Subject: "acct"}
	if rr := doWithClaims(h, c); rr.Code != http.StatusOK {
		t.Fatalf("first should be 200, got %d", rr.Code)
	}
	rr := doWithClaims(h, c)
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("second should be 429, got %d", rr.Code)
	}
	if rr.Header().Get("Retry-After") == "" {
		t.Error("expected Retry-After header on 429")
	}
}

// auditor is read-only: it passes requireRole("auditor") (the GET /v1/admin/*
// reads) but is refused admin writes and trading routes. admin still passes
// auditor routes because admin is a superset.
func TestAuditorRole_ReadOnly(t *testing.T) {
	read := requireRole("auditor")(okHandler())
	write := requireRole("admin")(okHandler())
	trade := chain(okHandler(), requireVerified(), requireRole("buyer", "seller", "trader"))

	if rr := doWithClaims(read, &Claims{Role: "auditor"}); rr.Code != http.StatusOK {
		t.Errorf("auditor should read admin views, got %d", rr.Code)
	}
	if rr := doWithClaims(read, &Claims{Role: "admin"}); rr.Code != http.StatusOK {
		t.Errorf("admin should still read admin views, got %d", rr.Code)
	}
	for _, role := range []string{"buyer", "seller", "trader", ""} {
		if rr := doWithClaims(read, &Claims{Role: role}); rr.Code != http.StatusForbidden {
			t.Errorf("%q must not read admin views, got %d", role, rr.Code)
		}
	}
	if rr := doWithClaims(write, &Claims{Role: "auditor", AAL: "aal2"}); rr.Code != http.StatusForbidden {
		t.Errorf("auditor must be refused admin writes even with MFA, got %d", rr.Code)
	}
	if rr := doWithClaims(trade, &Claims{Role: "auditor", Verified: true}); rr.Code != http.StatusForbidden {
		t.Errorf("auditor must be refused trading routes, got %d", rr.Code)
	}
}
