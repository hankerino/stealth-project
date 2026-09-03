package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// mintToken builds an HS256 JWT for tests. alg lets us forge a bad header.
func mintToken(t *testing.T, secret string, alg string, claims map[string]any) string {
	t.Helper()
	header := map[string]any{"alg": alg, "typ": "JWT"}
	hb, _ := json.Marshal(header)
	cb, _ := json.Marshal(claims)
	seg := func(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }
	signingInput := seg(hb) + "." + seg(cb)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(signingInput))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return signingInput + "." + sig
}

func baseClaims(now time.Time) map[string]any {
	return map[string]any{
		"sub":              "11111111-1111-1111-1111-111111111111",
		"aud":              "authenticated",
		"exp":              now.Add(time.Hour).Unix(),
		"iat":              now.Add(-time.Minute).Unix(),
		"account_role":     "trader",
		"account_verified": true,
	}
}

const testSecret = "super-secret-supabase-jwt-signing-key"

var testVerifier = hs256Verifier{secret: []byte(testSecret)}

func TestVerifySupabaseJWT_Happy(t *testing.T) {
	now := time.Now()
	tok := mintToken(t, testSecret, "HS256", baseClaims(now))

	c, err := verifySupabaseJWT(tok, testVerifier, now)
	if err != nil {
		t.Fatalf("expected valid token, got error: %v", err)
	}
	if c.Subject != "11111111-1111-1111-1111-111111111111" {
		t.Errorf("subject = %q", c.Subject)
	}
	if c.effectiveRole() != "trader" {
		t.Errorf("role = %q, want trader", c.effectiveRole())
	}
	if !c.Verified {
		t.Errorf("expected verified=true")
	}
}

func TestVerifySupabaseJWT_AudArray(t *testing.T) {
	now := time.Now()
	cl := baseClaims(now)
	cl["aud"] = []string{"authenticated", "somethingelse"}
	tok := mintToken(t, testSecret, "HS256", cl)

	if _, err := verifySupabaseJWT(tok, testVerifier, now); err != nil {
		t.Fatalf("aud array should be accepted, got: %v", err)
	}
}

func TestVerifySupabaseJWT_Expired(t *testing.T) {
	now := time.Now()
	cl := baseClaims(now)
	cl["exp"] = now.Add(-2 * time.Hour).Unix()
	tok := mintToken(t, testSecret, "HS256", cl)

	if _, err := verifySupabaseJWT(tok, testVerifier, now); err != errExpired {
		t.Fatalf("want errExpired, got: %v", err)
	}
}

func TestVerifySupabaseJWT_BadSignature(t *testing.T) {
	now := time.Now()
	tok := mintToken(t, "the-wrong-secret", "HS256", baseClaims(now))

	if _, err := verifySupabaseJWT(tok, testVerifier, now); err != errBadSignature {
		t.Fatalf("want errBadSignature, got: %v", err)
	}
}

func TestVerifySupabaseJWT_WrongAlg(t *testing.T) {
	now := time.Now()
	// "none" and RS256 must both be rejected — we only trust HS256.
	for _, alg := range []string{"none", "RS256", "HS512"} {
		tok := mintToken(t, testSecret, alg, baseClaims(now))
		if _, err := verifySupabaseJWT(tok, testVerifier, now); err != errBadSignature {
			t.Errorf("alg %q: want errBadSignature, got: %v", alg, err)
		}
	}
}

func TestVerifySupabaseJWT_WrongAudience(t *testing.T) {
	now := time.Now()
	cl := baseClaims(now)
	cl["aud"] = "anon"
	tok := mintToken(t, testSecret, "HS256", cl)

	if _, err := verifySupabaseJWT(tok, testVerifier, now); err != errWrongAudience {
		t.Fatalf("want errWrongAudience, got: %v", err)
	}
}

func TestVerifySupabaseJWT_NoSubject(t *testing.T) {
	now := time.Now()
	cl := baseClaims(now)
	delete(cl, "sub")
	tok := mintToken(t, testSecret, "HS256", cl)

	if _, err := verifySupabaseJWT(tok, testVerifier, now); err != errNoSubject {
		t.Fatalf("want errNoSubject, got: %v", err)
	}
}

func TestVerifySupabaseJWT_Malformed(t *testing.T) {
	now := time.Now()
	for _, bad := range []string{"", "a.b", "a.b.c.d", "not-a-token"} {
		if _, err := verifySupabaseJWT(bad, testVerifier, now); err == nil {
			t.Errorf("expected error for malformed token %q", bad)
		}
	}
}

func TestVerifySupabaseJWT_MissingRoleDefaultsBuyer(t *testing.T) {
	now := time.Now()
	cl := baseClaims(now)
	delete(cl, "account_role")
	delete(cl, "account_verified")
	tok := mintToken(t, testSecret, "HS256", cl)

	c, err := verifySupabaseJWT(tok, testVerifier, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.effectiveRole() != "buyer" {
		t.Errorf("role = %q, want buyer default", c.effectiveRole())
	}
	if c.Verified {
		t.Errorf("expected verified=false default")
	}
}

// --- middleware behavior ---

func TestAuthMiddleware_FailsClosedWhenUnset(t *testing.T) {
	a := newAuthenticator(nil, false)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/orders", nil)
	a.middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not run when auth is unconfigured")
	})).ServeHTTP(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("want 503, got %d", rr.Code)
	}
}

func TestAuthMiddleware_DevBypass(t *testing.T) {
	a := newAuthenticator(nil, true)
	var gotRole string
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/orders", nil)
	a.middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, _ := claimsFrom(r.Context())
		gotRole = c.effectiveRole()
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK || gotRole != "admin" {
		t.Fatalf("dev bypass: code=%d role=%q", rr.Code, gotRole)
	}
}

func TestAuthMiddleware_RejectsMissingHeader(t *testing.T) {
	a := newAuthenticator(testVerifier, false)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/orders", nil)
	a.middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not run without a token")
	})).ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rr.Code)
	}
}

func TestAuthMiddleware_AcceptsValidToken(t *testing.T) {
	now := time.Now()
	a := newAuthenticator(testVerifier, false)
	a.now = func() time.Time { return now }
	tok := mintToken(t, testSecret, "HS256", baseClaims(now))

	var seen *Claims
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/orders", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	a.middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen, _ = claimsFrom(r.Context())
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rr.Code)
	}
	if seen == nil || seen.Subject == "" {
		t.Fatalf("claims not propagated to handler")
	}
}
