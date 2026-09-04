package main

// Admin REST front. Reached only through the API gateway (admin role):
//   GET  /v1/admin/alerts?status=open&limit=100
//   POST /v1/admin/alerts/{id}   {"status":"reviewed"|"dismissed"}
// Trust boundary mirrors settlement/identity.go: X-Gateway-Secret must match
// GATEWAY_SHARED_SECRET when set; X-Account-Id carries the admin's account.

import (
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strconv"
)

type httpAPI struct {
	svc *complianceService
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func gateway(w http.ResponseWriter, r *http.Request) bool {
	if secret := os.Getenv("GATEWAY_SHARED_SECRET"); secret != "" {
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Gateway-Secret")), []byte(secret)) != 1 {
			writeError(w, http.StatusUnauthorized, "request must come through the API gateway")
			return false
		}
	}
	if r.Header.Get("X-Account-Id") == "" {
		writeError(w, http.StatusUnauthorized, "missing account identity")
		return false
	}
	return true
}

func (a *httpAPI) handleAlerts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !gateway(w, r) {
		return
	}
	limit := 100
	if v, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && v > 0 && v <= 500 {
		limit = v
	}
	alerts, err := a.svc.listAlerts(r.Context(), r.URL.Query().Get("status"), limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	writeJSON(w, http.StatusOK, alerts)
}

func (a *httpAPI) handleAlertStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !gateway(w, r) {
		return
	}
	var req struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || (req.Status != "reviewed" && req.Status != "dismissed") {
		writeError(w, http.StatusBadRequest, `status must be "reviewed" or "dismissed"`)
		return
	}
	err := a.svc.setAlertStatus(r.Context(), r.PathValue("id"), req.Status)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "unknown alert")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "update failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"alert_id": r.PathValue("id"), "status": req.Status})
}
