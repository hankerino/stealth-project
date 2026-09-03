// ES256/JWKS verification for Supabase access tokens.
//
// The dedicated stealth-project-auth Supabase project signs access tokens
// with an asymmetric ES256 key, published at the project's public JWKS
// endpoint. There is no shared secret to store: the gateway fetches and
// caches the JWKS and verifies signatures against it with crypto/ecdsa
// (stdlib only), refetching on an unknown kid to pick up key rotation.
package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"
)

// jwk is a single JSON Web Key from Supabase's JWKS endpoint. Only the EC
// (ES256) fields are decoded; any other key type on the same endpoint is
// skipped.
type jwk struct {
	Kty string `json:"kty"`
	Crv string `json:"crv"`
	Kid string `json:"kid"`
	Alg string `json:"alg"`
	X   string `json:"x"`
	Y   string `json:"y"`
}

type jwksResponse struct {
	Keys []jwk `json:"keys"`
}

// minRefetchInterval throttles JWKS refetches triggered by an unknown kid,
// so a flood of tokens carrying garbage kids can't be used to hammer the
// Supabase endpoint. A var (not const) so tests can inspect/override it.
var minRefetchInterval = 10 * time.Second

// jwksCache fetches and caches a Supabase project's public JWKS, refetching
// (rate-limited) on an unknown kid so key rotation is picked up without a
// restart.
type jwksCache struct {
	url    string
	apiKey string
	client *http.Client

	mu        sync.Mutex
	keys      map[string]*ecdsa.PublicKey
	lastFetch time.Time
}

func newJWKSCache(supabaseURL, anonKey string) *jwksCache {
	return &jwksCache{
		url:    strings.TrimRight(supabaseURL, "/") + "/auth/v1/.well-known/jwks.json",
		apiKey: anonKey,
		client: &http.Client{Timeout: 5 * time.Second},
		keys:   map[string]*ecdsa.PublicKey{},
	}
}

// get returns the public key for kid, fetching (or refetching) the JWKS if
// the kid isn't already cached and the refetch cooldown has elapsed.
func (c *jwksCache) get(kid string) (*ecdsa.PublicKey, error) {
	c.mu.Lock()
	key, ok := c.keys[kid]
	dueForRefetch := time.Since(c.lastFetch) >= minRefetchInterval
	c.mu.Unlock()
	if ok {
		return key, nil
	}
	if !dueForRefetch {
		return nil, fmt.Errorf("unknown kid %q", kid)
	}

	if err := c.refresh(); err != nil {
		return nil, err
	}

	c.mu.Lock()
	key, ok = c.keys[kid]
	c.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("unknown kid %q", kid)
	}
	return key, nil
}

func (c *jwksCache) refresh() error {
	req, err := http.NewRequest(http.MethodGet, c.url, nil)
	if err != nil {
		return err
	}
	if c.apiKey != "" {
		req.Header.Set("apikey", c.apiKey)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("jwks fetch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("jwks fetch: unexpected status %d", resp.StatusCode)
	}

	var parsed jwksResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return fmt.Errorf("jwks fetch: decode: %w", err)
	}

	keys := make(map[string]*ecdsa.PublicKey, len(parsed.Keys))
	for _, k := range parsed.Keys {
		if k.Kty != "EC" || k.Crv != "P-256" || k.Kid == "" {
			continue
		}
		pub, err := decodeECPublicKey(k.X, k.Y)
		if err != nil {
			continue
		}
		keys[k.Kid] = pub
	}

	c.mu.Lock()
	c.keys = keys
	c.lastFetch = time.Now()
	c.mu.Unlock()
	return nil
}

func decodeECPublicKey(xB64, yB64 string) (*ecdsa.PublicKey, error) {
	xb, err := base64.RawURLEncoding.DecodeString(xB64)
	if err != nil {
		return nil, fmt.Errorf("decode x: %w", err)
	}
	yb, err := base64.RawURLEncoding.DecodeString(yB64)
	if err != nil {
		return nil, fmt.Errorf("decode y: %w", err)
	}
	return &ecdsa.PublicKey{
		Curve: elliptic.P256(),
		X:     new(big.Int).SetBytes(xb),
		Y:     new(big.Int).SetBytes(yb),
	}, nil
}

// es256Verifier verifies ES256-signed access tokens against a Supabase
// project's public JWKS. It is the default verifier: modern Supabase
// projects sign with an asymmetric key, so there is no secret to configure.
type es256Verifier struct {
	keys *jwksCache
}

func newES256Verifier(supabaseURL, anonKey string) *es256Verifier {
	return &es256Verifier{keys: newJWKSCache(supabaseURL, anonKey)}
}

func (v *es256Verifier) alg() string { return "ES256" }

func (v *es256Verifier) verify(signingInput string, sig []byte, hdr jwtHeader) error {
	if hdr.Kid == "" {
		return errBadSignature
	}
	// ES256 signatures are the concatenation of two 32-byte big-endian
	// integers (r, s) per RFC 7518 §3.4 — not ASN.1 DER.
	if len(sig) != 64 {
		return errBadSignature
	}
	pub, err := v.keys.get(hdr.Kid)
	if err != nil {
		return errBadSignature
	}

	r := new(big.Int).SetBytes(sig[:32])
	s := new(big.Int).SetBytes(sig[32:])
	digest := sha256.Sum256([]byte(signingInput))
	if !ecdsa.Verify(pub, digest[:], r, s) {
		return errBadSignature
	}
	return nil
}
