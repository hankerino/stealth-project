// Supabase JWT authentication for the trading-engine API gateway (M1).
//
// The gateway is the ONLY place identity is derived. It verifies the caller's
// Supabase access token (HS256, signed with the project's JWT secret), extracts
// the account UUID (the "sub" claim) and the role/verified flags, and stashes
// them on the request context. Downstream services never see the raw token —
// the proxy layer (proxy.go) forwards only the trusted X-Account-* headers.
//
// HS256 verification is a symmetric HMAC-SHA256 check implemented with the
// standard library only (crypto/hmac + crypto/sha256), matching Supabase's
// default access-token signing and the repo's stdlib-first style — no external
// JWT dependency, and CI `go mod tidy` fetches nothing new.
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

// verifySupabaseJWT validates an HS256 JWT against the given secret and returns
// its claims. It checks the signature, expiry/nbf (with skew), and that the
// audience is "authenticated" (Supabase's audience for signed-in users).
func verifySupabaseJWT(token string, secret []byte, now time.Time) (*Claims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, errMalformed
	}

	// Verify header alg is HS256 before trusting anything else.
	headerJSON, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, errMalformed
	}
	var header struct {
		Alg string `json:"alg"`
		Typ string `json:"typ"`
	}
	if err := json.Unmarshal(headerJSON, &header); err != nil {
		return nil, errMalformed
	}
	if header.Alg != "HS256" {
		// Reject anything else, including "none", explicitly.
		return nil, errBadSignature
	}

	// Recompute the signature over "header.payload" and constant-time compare.
	signingInput := parts[0] + "." + parts[1]
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(signingInput))
	expectedSig := mac.Sum(nil)

	gotSig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, errMalformed
	}
	if subtle.ConstantTimeCompare(gotSig, expectedSig) != 1 {
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

// authenticator verifies incoming JWTs. It fails CLOSED: if the JWT secret is
// unset, every protected route returns 503 — this is the security milestone, so
// we deliberately do NOT fall back to allow-all the way other optional deps in
// this codebase degrade to no-ops. A single explicit dev escape hatch
// (AUTH_DEV_BYPASS) is honored only when the secret is unset.
type authenticator struct {
	secret    []byte
	devBypass bool
	now       func() time.Time
}

func newAuthenticator(secret string, devBypass bool) *authenticator {
	return &authenticator{
		secret:    []byte(secret),
		devBypass: devBypass,
		now:       func() time.Time { return time.Now() },
	}
}

// middleware verifies the bearer token and injects claims into the context.
func (a *authenticator) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(a.secret) == 0 {
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
		claims, err := verifySupabaseJWT(tok, a.secret, a.now())
		if err != nil {
			writeError(w, http.StatusUnauthorized, "invalid token")
			return
		}
		next.ServeHTTP(w, r.WithContext(withClaims(r.Context(), claims)))
	})
}
