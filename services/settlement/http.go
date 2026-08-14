package main

// REST API for the settlement service: escrow deposit + balance.

import (
	"encoding/json"
	"net/http"
)

type httpAPI struct {
	svc *settlementService
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// handleDeposit serves POST /v1/escrow/deposit.
// Body: { "user_id": "...", "amount_cents": 10000 }
func (a *httpAPI) handleDeposit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req struct {
		UserID      string `json:"user_id"`
		AmountCents int64  `json:"amount_cents"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	balance, err := a.svc.depositFunds(r.Context(), req.UserID, req.AmountCents)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"user_id":       req.UserID,
		"new_balance":   balance,
		"amount_cents":  req.AmountCents,
	})
}

// handleBalance serves GET /v1/escrow/balance?user_id=.
func (a *httpAPI) handleBalance(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	userID := r.URL.Query().Get("user_id")
	if userID == "" {
		writeError(w, http.StatusBadRequest, "user_id query parameter is required")
		return
	}
	balance, err := a.svc.getBalance(r.Context(), userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"user_id": userID,
		"balance": balance,
	})
}
