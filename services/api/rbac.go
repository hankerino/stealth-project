// Role-based access control for the trading-engine API gateway (M1).
//
// These middlewares run AFTER authenticator.middleware has put verified Claims
// on the request context. They gate routes by account role and by the manual
// KYB "verified" flag. Roles: buyer, seller, trader, admin.
package main

import "net/http"

// requireRole allows the request only if the account's role is in the allowed
// set. admin is always allowed (it is a superset role).
func requireRole(allowed ...string) func(http.Handler) http.Handler {
	allowSet := make(map[string]struct{}, len(allowed))
	for _, r := range allowed {
		allowSet[r] = struct{}{}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			c, ok := claimsFrom(r.Context())
			if !ok {
				writeError(w, http.StatusUnauthorized, "not authenticated")
				return
			}
			role := c.effectiveRole()
			if role == "admin" {
				next.ServeHTTP(w, r)
				return
			}
			if _, allowed := allowSet[role]; !allowed {
				writeError(w, http.StatusForbidden, "insufficient role")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// requireVerified allows the request only if the account has passed manual KYB.
// admin accounts bypass the check (they are operators, not KYB'd customers).
func requireVerified() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			c, ok := claimsFrom(r.Context())
			if !ok {
				writeError(w, http.StatusUnauthorized, "not authenticated")
				return
			}
			if c.effectiveRole() != "admin" && !c.Verified {
				writeError(w, http.StatusForbidden, "account not verified")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// chain applies middlewares in order: chain(h, a, b) runs a, then b, then h.
func chain(h http.Handler, mws ...func(http.Handler) http.Handler) http.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}

// requireMFA allows the request only if the session was elevated with a second
// factor (Supabase aal2). Used for admin routes and money-out. The web app
// turns the 403 + "mfa_required" into a prompt to enrol/challenge.
func requireMFA() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			c, ok := claimsFrom(r.Context())
			if !ok {
				writeError(w, http.StatusUnauthorized, "not authenticated")
				return
			}
			if c.AAL != "aal2" {
				writeJSON(w, http.StatusForbidden, map[string]string{"error": "mfa_required", "message": "this action requires two-factor authentication"})
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
