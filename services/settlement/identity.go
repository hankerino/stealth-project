package main

// M1 gateway trust boundary for user-facing settlement endpoints.
//
// The API gateway verifies the caller's Supabase JWT and forwards:
//   - X-Gateway-Secret : proves the request came through the gateway (guards
//     against this service's public URL being hit directly). Checked against
//     GATEWAY_SHARED_SECRET; when that env is unset the check is skipped (dev).
//   - X-Account-Id      : the authenticated account UUID, replacing the old
//     client-supplied user_id in the deposit body / balance query.
//
// This applies ONLY to the user-facing escrow endpoints. The agent job control
// plane (jobs poll/status) uses node ids + Ed25519 signatures, and the MTM run
// trigger is an admin/operational endpoint — neither is user identity, so they
// do not use this gate.

import (
	"crypto/subtle"
	"net/http"
	"os"
)

const (
	headerGatewaySecret = "X-Gateway-Secret"
	headerAccountID     = "X-Account-Id"
)

// accountID enforces the gateway trust boundary and returns the authenticated
// account id, or writes an error and returns ("", false).
func accountID(w http.ResponseWriter, r *http.Request) (string, bool) {
	if secret := os.Getenv("GATEWAY_SHARED_SECRET"); secret != "" {
		if subtle.ConstantTimeCompare([]byte(r.Header.Get(headerGatewaySecret)), []byte(secret)) != 1 {
			writeError(w, http.StatusUnauthorized, "request must come through the API gateway")
			return "", false
		}
	}
	id := r.Header.Get(headerAccountID)
	if id == "" {
		writeError(w, http.StatusUnauthorized, "missing account identity")
		return "", false
	}
	return id, true
}
