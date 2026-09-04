package main

// REST front for the risk service: pre-trade margin check + position lookup.

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"os"
	"strconv"
)

type httpAPI struct {
	svc *riskService
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// handleCheckMargin serves POST /v1/risk/check-margin.
// Body: {user_id, contract_id, symbol, order_kind (SPOT|FUTURES),
//        side (BUY|SELL), price_cents, quantity}
func (a *httpAPI) handleCheckMargin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req struct {
		UserID     string `json:"user_id"`
		ContractID int64  `json:"contract_id"`
		Symbol     string `json:"symbol"`
		OrderKind  string `json:"order_kind"`
		Side       string `json:"side"`
		PriceCents int64  `json:"price_cents"`
		Quantity   int64  `json:"quantity"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.UserID == "" || req.Quantity <= 0 || req.PriceCents <= 0 {
		writeError(w, http.StatusBadRequest, "user_id, positive price_cents and quantity are required")
		return
	}
	res, err := a.svc.CheckMargin(r.Context(), MarginCheck{
		UserID:     req.UserID,
		ContractID: req.ContractID,
		Symbol:     req.Symbol,
		Kind:       req.OrderKind,
		Side:       req.Side,
		PriceCents: req.PriceCents,
		Quantity:   req.Quantity,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "margin check failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"allowed":               res.Allowed,
		"required_margin_cents": res.RequiredMarginCents,
		"available_cents":       res.AvailableCents,
		"projected_position":    res.ProjectedPosition,
		"reason":                res.Reason,
	})
}

// handlePosition serves GET /v1/risk/position?user_id=&contract_id=.
func (a *httpAPI) handlePosition(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	userID := r.URL.Query().Get("user_id")
	if userID == "" {
		writeError(w, http.StatusBadRequest, "user_id is required")
		return
	}
	contractID, _ := strconv.ParseInt(r.URL.Query().Get("contract_id"), 10, 64)
	pos, err := a.svc.GetPosition(r.Context(), userID, contractID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"user_id":               pos.UserID,
		"contract_id":           pos.ContractID,
		"symbol":                pos.Symbol,
		"net_quantity":          pos.NetQuantity,
		"avg_entry_price_cents": pos.AvgEntryPriceCents,
		"margin_posted_cents":   pos.MarginPostedCents,
	})
}

// handleMyPositions serves GET /v1/positions for the authenticated account.
// Identity comes from the API gateway (X-Account-Id), never from the query
// string; X-Gateway-Secret is enforced when GATEWAY_SHARED_SECRET is set.
func (a *httpAPI) handleMyPositions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if secret := os.Getenv("GATEWAY_SHARED_SECRET"); secret != "" {
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Gateway-Secret")), []byte(secret)) != 1 {
			writeError(w, http.StatusUnauthorized, "request must come through the API gateway")
			return
		}
	}
	userID := r.Header.Get("X-Account-Id")
	if userID == "" {
		writeError(w, http.StatusUnauthorized, "missing account identity")
		return
	}
	positions, err := a.svc.ListPositions(r.Context(), userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	out := make([]map[string]any, 0, len(positions))
	for _, p := range positions {
		out = append(out, map[string]any{
			"contract_id":           p.ContractID,
			"symbol":                p.Symbol,
			"net_quantity":          p.NetQuantity,
			"avg_entry_price_cents": p.AvgEntryPriceCents,
			"margin_posted_cents":   p.MarginPostedCents,
		})
	}
	writeJSON(w, http.StatusOK, out)
}
