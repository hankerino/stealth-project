package main

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strconv"
)

// REST front for order entry. Routes use the Go 1.22 method+wildcard mux.
//
// M1: identity is NO LONGER client-supplied. The API gateway verifies the
// caller's Supabase JWT and forwards two trusted headers:
//   - X-Gateway-Secret : proves the request came through the gateway (guards
//     against this service's public URL being hit directly). Checked against
//     GATEWAY_SHARED_SECRET; when that env is unset the check is skipped (dev).
//   - X-Account-Id      : the authenticated account UUID. Replaces the old
//     client-supplied user_id in the body / ?user_id= query.
// Any user_id still present in a request body is ignored (backward-compat).

const (
	headerGatewaySecret = "X-Gateway-Secret"
	headerAccountID     = "X-Account-Id"
)

type httpAPI struct {
	svc *orderService
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// accountID enforces the gateway trust boundary and returns the authenticated
// account id. It writes the appropriate error and returns ("", false) if the
// request is not a valid gateway-forwarded, authenticated call.
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

// handleOrders serves POST /v1/orders and GET /v1/orders.
// A POST with contract_id > 0 is a FUTURES order (margin-checked); otherwise
// it is a SPOT order on <gpu_type>:<region>. Identity comes from X-Account-Id.
func (a *httpAPI) handleOrders(w http.ResponseWriter, r *http.Request) {
	userID, ok := accountID(w, r)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodPost:
		var req struct {
			// UserID is accepted but IGNORED (identity comes from the gateway);
			// kept in the struct for backward compatibility with old clients.
			UserID      string `json:"user_id"`
			GPUType     string `json:"gpu_type"`
			Region      string `json:"region"`
			Side        string `json:"side"`
			PriceCents  int64  `json:"price_cents"`
			Quantity    int64  `json:"quantity"`
			TimeInForce string `json:"time_in_force"`
			ContractID  int64  `json:"contract_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		o, err := a.svc.placeOrder(r.Context(), &OrderInput{
			UserID:      userID,
			GPUType:     req.GPUType,
			Region:      req.Region,
			Side:        req.Side,
			PriceCents:  req.PriceCents,
			Quantity:    req.Quantity,
			TimeInForce: req.TimeInForce,
			ContractID:  req.ContractID,
		})
		if o == nil && err != nil {
			var me *marginError
			if errors.As(err, &me) {
				writeError(w, http.StatusPaymentRequired, me.Error())
				return
			}
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		// 201 even when only the publish failed: the order is durable.
		writeJSON(w, http.StatusCreated, o)
	case http.MethodGet:
		orders, err := a.svc.listOrders(r.Context(), userID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "query failed")
			return
		}
		writeJSON(w, http.StatusOK, orders)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// handleCancel serves DELETE /v1/orders/{id}. Identity comes from X-Account-Id.
func (a *httpAPI) handleCancel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	userID, ok := accountID(w, r)
	if !ok {
		return
	}
	orderID := r.PathValue("id")
	o, err := a.svc.cancelOrder(r.Context(), userID, orderID)
	if errors.Is(err, errNotFound) {
		writeError(w, http.StatusNotFound, "order not found or not cancellable")
		return
	}
	if err != nil && o == nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, o)
}

// handleAdminOrders serves GET /v1/admin/orders?limit=N: newest orders across
// all accounts, for market surveillance. Read-only. Role (admin or auditor)
// is enforced by the gateway; the gateway secret + identity are checked here.
func (a *httpAPI) handleAdminOrders(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if _, ok := accountID(w, r); !ok {
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	orders, err := a.svc.listAllOrders(r.Context(), limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	writeJSON(w, http.StatusOK, orders)
}
