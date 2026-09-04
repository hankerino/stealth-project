// Supabase JWT authentication for the trading-engine API gateway (M1).
//
// The gateway is the ONLY place identity is derived. It verifies the caller's
// Supabase access token, extracts the account UUID (the "sub" claim) and the
// role/verified flags, and stashes them on the request context. Downstream
// services never see the raw token — the proxy layer (proxy.go) forwards
// only the trusted X-Account-* headers.
//
// Signature verification is pluggable (see signatureVerifier): the dedicated
// stealth-project-auth Supabase project signs access tokens with an
// asymmetric ES256 key (see jwks.go), verified against the project's public
// JWKS with no secret to store. A legacy HS256 shared-secret verifier is kept
// for a project configured to sign symmetrically. Both are stdlib-only
// (crypto/ecdsa / crypto/hmac + crypto/sha256) — no external JWT dependency.
package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
)

// clockSkew tolerates small clock differences between the token issuer and us.
const clockSkew = 60 * time.Second

var (
	errNoToken       = errors.New("missing bearer token")
	errMalformed     = errors.New("malformed token")
	errBadSignature  = errors.New("bad token signature")
	errExpired       = errors.New("token expired")
	errNotYetValid   = errors.New("token not yet valid")
	errWrongAudience = errors.New("wrong token audience")
	errNoSubject     = errors.New("token has no subject")
)

// Claims is the subset of a Supabase access token the gateway cares about.
// Supabase signs standard registered claims plus custom ones; account_role and
// account_verified are injected by our custom_access_token_hook (see the
// Supabase migration). They may be absent if the hook is not yet enabled, in
// which case the caller is treated as an unverified buyer.
type Claims struct {
	Subject   string `json:"sub"`
	Audience  Aud    `json:"aud"`
	Expiry    int64  `json:"exp"`
	IssuedAt  int64  `json:"iat"`
	NotBefore int64  `json:"nbf"`
	Role      string `json:"account_role"`
	Verified  bool   `json:"account_verified"`
	// Supabase authenticator assurance level: "aal1" (password / magic link)
	// or "aal2" (a second factor was verified in this session).
	AAL string `json:"aal"`
	// Supabase's built-in "role" is always "authenticated" for a signed-in
	// user; it is NOT our account role. Kept only for completeness.
	SupabaseRole string `json:"role"`
}

// Aud handles the Supabase "aud" claim, which may be a single string or an
// array of strings depending on configuration.
type Aud []string

func (a *Aud) UnmarshalJSON(b []byte) error {
	var single string
	if err := json.Unmarshal(b, &single); err == nil {
		*a = Aud{single}
		return nil
	}
	var many []string
	if err := json.Unmarshal(b, &many); err != nil {
		return err
	}
	*a = Aud(many)
	return nil
}

func (a Aud) contains(want string) bool {
	for _, v := range a {
		if v == want {
			return true
		}
	}
	return false
}

// effectiveRole returns the account role, defaulting to "buyer" when the hook
// has not populated the claim yet.
func (c *Claims) effectiveRole() string {
	if c.Role == "" {
		return "buyer"
	}
	return c.Role
}

// jwtHeader is the decoded JOSE header fields the gateway cares about.
type jwtHeader struct {
	Alg string `json:"alg"`
	Kid string `json:"kid"`
	Typ string `json:"typ"`
}

// signatureVerifier checks a token's signature for one JWT algorithm.
// Different Supabase projects sign access tokens differently: modern
// projects use an asymmetric key (ES256, verified against the public JWKS —
// see jwks.go) and legacy projects use a symmetric HS256 shared secret. The
// gateway picks a verifier at startup (see gateway.go).
type signatureVerifier interface {
	// alg is the JWT "alg" header value this verifier accepts. A token whose
	// header names a different algorithm is rejected before verify is ever
	// called — this is what stops an attacker downgrading to "none" or a
	// weaker algorithm the gateway isn't configured for.
	alg() string
	// verify checks the signature over signingInput ("header.payload") given
	// the raw decoded signature bytes and the token's JOSE header.
	verify(signingInput string, sig []byte, hdr jwtHeader) error
}

// hs256Verifier is the legacy symmetric verifier, used only when the gateway
// is explicitly configured with SUPABASE_JWT_SECRET.
type hs256Verifier struct {
	secret []byte
}

func (h hs256Verifier) alg() string { return "HS256" }

func (h hs256Verifier) verify(signingInput string, sig []byte, _ jwtHeader) error {
	mac := hmac.New(sha256.New, h.secret)
	mac.Write([]byte(signingInput))
	expected := mac.Sum(nil)
	if subtle.ConstantTimeCompare(sig, expected) != 1 {
		return errBadSignature
	}
	return nil
}

// verifySupabaseJWT validates a JWT with the given verifier and returns its
// claims. It checks the header algorithm against the verifier, the
// signature, expiry/nbf (with skew), and that the audience is
// "authenticated" (Supabase's audience for signed-in users).
func verifySupabaseJWT(token string, verifier signatureVerifier, now time.Time) (*Claims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, errMalformed
	}

	// Verify the header alg matches the configured verifier before trusting
	// anything else — this rejects "none" and any algorithm the gateway
	// isn't set up to check, explicitly.
	headerJSON, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, errMalformed
	}
	var header jwtHeader
	if err := json.Unmarshal(headerJSON, &header); err != nil {
		return nil, errMalformed
	}
	if header.Alg != verifier.alg() {
		return nil, errBadSignature
	}

	signingInput := parts[0] + "." + parts[1]
	gotSig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, errMalformed
	}
	if err := verifier.verify(signingInput, gotSig, header); err != nil {
		return nil, errBadSignature
	}

	// Signature is valid; now decode and validate claims.
	payloadJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, errMalformed
	}
	var claims Claims
	if err := json.Unmarshal(payloadJSON, &claims); err != nil {
		return nil, errMalformed
	}

	if claims.Subject == "" {
		return nil, errNoSubject
	}
	if claims.Expiry != 0 && now.After(time.Unix(claims.Expiry, 0).Add(clockSkew)) {
		return nil, errExpired
	}
	if claims.NotBefore != 0 && now.Before(time.Unix(claims.NotBefore, 0).Add(-clockSkew)) {
		return nil, errNotYetValid
	}
	// Supabase issues access tokens with aud "authenticated". If an aud is
	// present it must match; a token with no aud is rejected (defense-in-depth).
	if len(claims.Audience) == 0 || !claims.Audience.contains("authenticated") {
		return nil, errWrongAudience
	}

	return &claims, nil
}

// --- request-context plumbing ---

type ctxKey int

const claimsKey ctxKey = iota

func withClaims(ctx context.Context, c *Claims) context.Context {
	return context.WithValue(ctx, claimsKey, c)
}

func claimsFrom(ctx context.Context) (*Claims, bool) {
	c, ok := ctx.Value(claimsKey).(*Claims)
	return c, ok
}

// bearerToken extracts the token from an "Authorization: Bearer <token>" header.
func bearerToken(r *http.Request) (string, error) {
	h := r.Header.Get("Authorization")
	if h == "" {
		return "", errNoToken
	}
	const prefix = "Bearer "
	if len(h) <= len(prefix) || !strings.EqualFold(h[:len(prefix)], prefix) {
		return "", errNoToken
	}
	return strings.TrimSpace(h[len(prefix):]), nil
}

// authenticator verifies incoming JWTs. It fails CLOSED: if no verifier is
// configured, every protected route returns 503 — this is the security
// milestone, so we deliberately do NOT fall back to allow-all the way other
// optional deps in this codebase degrade to no-ops. A single explicit dev
// escape hatch (AUTH_DEV_BYPASS) is honored only when no verifier is set.
type authenticator struct {
	verifier  signatureVerifier
	devBypass bool
	now       func() time.Time
}

func newAuthenticator(verifier signatureVerifier, devBypass bool) *authenticator {
	return &authenticator{
		verifier:  verifier,
		devBypass: devBypass,
		now:       func() time.Time { return time.Now() },
	}
}

// middleware verifies the bearer token and injects claims into the context.
func (a *authenticator) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.verifier == nil {
			if a.devBypass {
				// Dev only: synthesize an admin/verified identity so local
				// work can proceed without a real Supabase project.
				next.ServeHTTP(w, r.WithContext(withClaims(r.Context(), &Claims{
					Subject:  "00000000-0000-0000-0000-000000000000",
					Role:     "admin",
					Verified: true,
				})))
				return
			}
			writeError(w, http.StatusServiceUnavailable, "auth not configured")
			return
		}

		tok, err := bearerToken(r)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "missing or malformed Authorization header")
			return
		}
		claims, err := verifySupabaseJWT(tok, a.verifier, a.now())
		if err != nil {
			writeError(w, http.StatusUnauthorized, "invalid token")
			return
		}
		next.ServeHTTP(w, r.WithContext(withClaims(r.Context(), claims)))
	})
}
