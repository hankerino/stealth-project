package main

// REST API for the settlement service: escrow deposit + balance + the
// agent-facing job control plane (jobs poll / status).

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"
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

// handleDeposit serves POST /v1/escrow/deposit — admin credit (closed beta /
// operator top-up). The gateway restricts this route to role=admin; card
// deposits for everyone else go through /v1/escrow/checkout (Stripe).
// Body: { "amount_cents": 10000, "user_id": "<target, optional>" }
// user_id defaults to the caller (X-Account-Id).
func (a *httpAPI) handleDeposit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	adminID, ok := accountID(w, r)
	if !ok {
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
	target := req.UserID
	if target == "" {
		target = adminID
	}
	balance, err := a.svc.adminCredit(r.Context(), adminID, target, req.AmountCents)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"user_id":      target,
		"new_balance":  balance,
		"amount_cents": req.AmountCents,
	})
}

// handleBalance serves GET /v1/escrow/balance.
// Identity comes from the gateway (X-Account-Id), not a ?user_id= query.
func (a *httpAPI) handleBalance(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	userID, ok := accountID(w, r)
	if !ok {
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

// handleJobsPoll serves GET /v1/jobs/poll?node_id=&limit= — the agent's
// job pickup. Unsigned read: job specs are not secret, and every state
// transition requires the node's Ed25519 signature, so polling alone
// cannot move money.
func (a *httpAPI) handleJobsPoll(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	nodeID := r.URL.Query().Get("node_id")
	if nodeID == "" {
		writeError(w, http.StatusBadRequest, "node_id query parameter is required")
		return
	}
	limit := 1
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 10 {
			limit = n
		}
	}
	rows, err := a.svc.db.QueryContext(r.Context(), `
		SELECT job_id::text, trade_id::text, node_id::text, symbol, buyer_id, seller_id, workload
		FROM jobs WHERE node_id = $1::uuid AND status = 'queued'
		ORDER BY created_at LIMIT $2`, nodeID, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	defer rows.Close()
	out := []job{}
	for rows.Next() {
		var j job
		if err := rows.Scan(&j.JobID, &j.TradeID, &j.NodeID, &j.Symbol, &j.BuyerID, &j.SellerID, &j.Workload); err != nil {
			writeError(w, http.StatusInternalServerError, "scan failed")
			return
		}
		out = append(out, j)
	}
	writeJSON(w, http.StatusOK, map[string]any{"jobs": out})
}

// jobStatusRequest is the agent's signed status report.
type jobStatusRequest struct {
	NodeID       string `json:"node_id"`
	Status       string `json:"status"` // started | completed | failed
	Reason       string `json:"reason,omitempty"`
	SentAtUnixMs int64  `json:"sent_at_unix_ms"`
	Signature    string `json:"signature"`
}

// handleJobStatus serves POST /v1/jobs/{id}/status. Transitions are atomic
// and idempotent-safe: a duplicate or out-of-order report gets 409, which
// the agent treats as "already handled".
func (a *httpAPI) handleJobStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	jobID := r.PathValue("id")
	var req jobStatusRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	switch req.Status {
	case "started", "completed", "failed":
	default:
		writeError(w, http.StatusBadRequest, "status must be started|completed|failed")
		return
	}
	if !a.svc.verifyJobStatusSignature(r.Context(), req.NodeID, jobID, req.Status, req.SentAtUnixMs, req.Signature) {
		writeError(w, http.StatusUnauthorized, "invalid signature")
		return
	}

	var err error
	switch req.Status {
	case "started":
		err = a.svc.reportJobStarted(r.Context(), jobID, req.NodeID)
	case "completed":
		err = a.svc.reportJobCompleted(r.Context(), jobID, req.NodeID)
	case "failed":
		err = a.svc.reportJobFailed(r.Context(), jobID, req.NodeID, req.Reason)
	}
	if errors.Is(err, errJobConflict) {
		writeError(w, http.StatusConflict, "job not found or not in the expected state")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "status update failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "job_id": jobID, "status": req.Status})
}

// handleMTMRun serves POST /v1/mtm/run — trigger a mark-to-market run.
// Body (optional): {"date":"YYYY-MM-DD"}; defaults to today (UTC).
func (a *httpAPI) handleMTMRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if a.svc.mtm == nil {
		writeError(w, http.StatusServiceUnavailable, "mtm runner not configured")
		return
	}
	var req struct {
		Date string `json:"date"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req) // body optional
	date := req.Date
	if date == "" {
		date = time.Now().UTC().Format("2006-01-02")
	}
	if err := a.svc.mtm.runOnce(r.Context(), date); err != nil {
		writeError(w, http.StatusInternalServerError, "mtm run failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "settlement_date": date})
}
