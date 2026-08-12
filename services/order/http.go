package main

import (
	"encoding/json"
	"errors"
	"net/http"
)

// REST front for order entry. Routes use the Go 1.22 method+wildcard mux.

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

// handleOrders serves POST /v1/orders and GET /v1/orders?user_id=.
func (a *httpAPI) handleOrders(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		var req struct {
			UserID      string `json:"user_id"`
			GPUType     string `json:"gpu_type"`
			Region      string `json:"region"`
			Side        string `json:"side"`
			PriceCents  int64  `json:"price_cents"`
			Quantity    int64  `json:"quantity"`
			TimeInForce string `json:"time_in_force"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		o, err := a.svc.placeOrder(r.Context(), &OrderInput{
			UserID:      req.UserID,
			GPUType:     req.GPUType,
			Region:      req.Region,
			Side:        req.Side,
			PriceCents:  req.PriceCents,
			Quantity:    req.Quantity,
			TimeInForce: req.TimeInForce,
		})
		if o == nil && err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		// 201 even when only the publish failed: the order is durable.
		writeJSON(w, http.StatusCreated, o)
	case http.MethodGet:
		userID := r.URL.Query().Get("user_id")
		if userID == "" {
			writeError(w, http.StatusBadRequest, "user_id query parameter is required")
			return
		}
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

// handleCancel serves DELETE /v1/orders/{id}?user_id=.
func (a *httpAPI) handleCancel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	orderID := r.PathValue("id")
	userID := r.URL.Query().Get("user_id")
	if userID == "" {
		writeError(w, http.StatusBadRequest, "user_id query parameter is required")
		return
	}
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
