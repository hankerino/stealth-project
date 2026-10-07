package main

// Read-only audit snapshot for operators and the hQube OS Exchange pod.
//
// GET /v1/admin/audit returns, in one consistent read, everything a
// reconciliation or surveillance check needs: escrow balances, the trade
// ledger, deposits, payouts, fees and MTM settlements (newest first, capped).
// Role (admin or auditor) is enforced by the gateway; this handler only reads.

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
)

const auditDefaultLimit = 2000

// auditSnapshot builds the snapshot in a single read-only transaction so every
// section reflects the same moment.
func (s *settlementService) auditSnapshot(ctx context.Context, limit int) (json.RawMessage, error) {
	if limit < 1 || limit > 10000 {
		limit = auditDefaultLimit
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck // read-only
	var out []byte
	err = tx.QueryRowContext(ctx, `
		SELECT json_build_object(
		  'as_of',    now(),
		  'limit',    $1::int,
		  'accounts', (SELECT coalesce(json_agg(a ORDER BY a.user_id), '[]') FROM (
		                 SELECT user_id, balance, updated_at FROM escrow_accounts) a),
		  'trades',   (SELECT coalesce(json_agg(t), '[]') FROM (
		                 SELECT trade_id, symbol, buyer_id, seller_id, price_cents, quantity, total_cost,
		                        buyer_fee_cents, seller_fee_cents, status, failure_reason, settled_at, created_at
		                 FROM trade_ledger ORDER BY created_at DESC LIMIT $1) t),
		  'deposits', (SELECT coalesce(json_agg(d), '[]') FROM (
		                 SELECT id, user_id, amount_cents, fee_cents, provider, provider_ref, status, created_at, completed_at
		                 FROM escrow_deposits ORDER BY created_at DESC LIMIT $1) d),
		  'payouts',  (SELECT coalesce(json_agg(p), '[]') FROM (
		                 SELECT id, user_id, amount_cents, status, note, created_at, resolved_at, resolved_by
		                 FROM payout_requests ORDER BY created_at DESC LIMIT $1) p),
		  'fees',     (SELECT coalesce(json_agg(f), '[]') FROM (
		                 SELECT id, kind, payer_id, trade_id, deposit_id, basis_cents, bps, fee_cents, to_platform, created_at
		                 FROM fee_ledger ORDER BY created_at DESC LIMIT $1) f),
		  'mtm',      (SELECT coalesce(json_agg(m), '[]') FROM (
		                 SELECT settlement_date, contract_id, symbol, user_id, net_quantity, pnl_cents, created_at
		                 FROM mtm_settlements ORDER BY created_at DESC LIMIT $1) m)
		)`, limit).Scan(&out)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(out), nil
}

// handleAdminAudit serves GET /v1/admin/audit?limit=N.
func (a *httpAPI) handleAdminAudit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if _, ok := accountID(w, r); !ok {
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	snap, err := a.svc.auditSnapshot(r.Context(), limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(snap)
}
