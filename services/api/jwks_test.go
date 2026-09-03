package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// mintES256Token builds an ES256 JWT signed with priv, carrying kid in the
// header. Mirrors mintToken in auth_test.go but for the asymmetric path.
func mintES256Token(t *testing.T, priv *ecdsa.PrivateKey, kid string, claims map[string]any) string {
	t.Helper()
	header := map[string]any{"alg": "ES256", "typ": "JWT", "kid": kid}
	hb, _ := json.Marshal(header)
	cb, _ := json.Marshal(claims)
	seg := func(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }
	signingInput := seg(hb) + "." + seg(cb)

	digest := sha256.Sum256([]byte(signingInput))
	r, s, err := ecdsa.Sign(rand.Reader, priv, digest[:])
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	return signingInput + "." + seg(sig)
}

func jwkFromPublicKey(kid string, pub *ecdsa.PublicKey) jwk {
	enc := base64.RawURLEncoding.EncodeToString
	xb := make([]byte, 32)
	yb := make([]byte, 32)
	pub.X.FillBytes(xb)
	pub.Y.FillBytes(yb)
	return jwk{Kty: "EC", Crv: "P-256", Kid: kid, Alg: "ES256", X: enc(xb), Y: enc(yb)}
}

func newTestJWKSServer(t *testing.T, keys ...jwk) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(jwksResponse{Keys: keys})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestES256Verifier_Happy(t *testing.T) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	srv := newTestJWKSServer(t, jwkFromPublicKey("key-1", &priv.PublicKey))

	v := newES256Verifier(srv.URL, "anon-key")
	now := time.Now()
	tok := mintES256Token(t, priv, "key-1", baseClaims(now))

	c, err := verifySupabaseJWT(tok, v, now)
	if err != nil {
		t.Fatalf("expected valid token, got error: %v", err)
	}
	if c.Subject == "" {
		t.Errorf("expected subject to be populated")
	}
}

func TestES256Verifier_WrongKey(t *testing.T) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	other, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	// The JWKS endpoint serves a different public key under the same kid the
	// token claims — signature must fail to verify.
	srv := newTestJWKSServer(t, jwkFromPublicKey("key-1", &other.PublicKey))

	v := newES256Verifier(srv.URL, "anon-key")
	now := time.Now()
	tok := mintES256Token(t, priv, "key-1", baseClaims(now))

	if _, err := verifySupabaseJWT(tok, v, now); err != errBadSignature {
		t.Fatalf("want errBadSignature, got: %v", err)
	}
}

func TestES256Verifier_UnknownKidThrottlesRefetch(t *testing.T) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		_ = json.NewEncoder(w).Encode(jwksResponse{Keys: []jwk{jwkFromPublicKey("key-1", &priv.PublicKey)}})
	}))
	t.Cleanup(srv.Close)

	v := newES256Verifier(srv.URL, "anon-key")
	now := time.Now()

	tok := mintES256Token(t, priv, "key-does-not-exist", baseClaims(now))
	if _, err := verifySupabaseJWT(tok, v, now); err != errBadSignature {
		t.Fatalf("want errBadSignature for unknown kid, got: %v", err)
	}
	if hits != 1 {
		t.Fatalf("expected exactly one JWKS fetch, got %d", hits)
	}

	// A second, different unknown kid shortly after must not trigger another
	// fetch — this is the throttle that stops a flood of garbage kids from
	// hammering the Supabase endpoint.
	tok2 := mintES256Token(t, priv, "still-unknown", baseClaims(now))
	if _, err := verifySupabaseJWT(tok2, v, now); err != errBadSignature {
		t.Fatalf("want errBadSignature, got: %v", err)
	}
	if hits != 1 {
		t.Fatalf("expected throttled refetch to be skipped, got %d fetches", hits)
	}
}

func TestES256Verifier_RejectsHS256Token(t *testing.T) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	srv := newTestJWKSServer(t, jwkFromPublicKey("key-1", &priv.PublicKey))
	v := newES256Verifier(srv.URL, "anon-key")

	now := time.Now()
	tok := mintToken(t, "some-secret", "HS256", baseClaims(now))
	if _, err := verifySupabaseJWT(tok, v, now); err != errBadSignature {
		t.Fatalf("want errBadSignature, got: %v", err)
	}
}
