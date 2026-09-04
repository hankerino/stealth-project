package main

// Allocations: the buyer-facing half of deferred execution.
//
//	GET  /v1/allocations          my jobs (held/queued/running/completed/failed)
//	POST /v1/jobs/{id}/run        held -> queued on the allocation's node
//	                              body (optional): {"workload": {...}}
//
// Both go through the API gateway (X-Account-Id). Resale transfer lives here
// too: transferAllocation moves held jobs from the seller to the buyer, splitting
// the last one if needed, and settles the resale trade immediately (the
// reseller is paid now; the ORIGINAL seller is still paid when the job runs
// to completion, from the original hold).

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"
)

// heldQuantity is the GPU-hours the user holds unrun for a symbol.
func (s *settlementService) heldQuantity(ctx context.Context, userID, symbol string) (int64, error) {
	var q sql.NullInt64
	err := s.db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(quantity), 0) FROM jobs
		WHERE buyer_id = $1 AND symbol = $2 AND status = 'held'`, userID, symbol).Scan(&q)
	return q.Int64, err
}

// transferAllocation executes a resale fill: buyer's funds move to the
// reseller (trade SETTLED at once) and `t.Quantity` hours of the reseller's
// held jobs become the buyer's, oldest first, splitting the last job.
// Business outcomes return nil; transient errors bubble up for retry.
func (s *settlementService) transferAllocation(ctx context.Context, t *TradeExecuted, buyerID, sellerID string, totalCost int64) error {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	var existing string
	err = tx.QueryRowContext(ctx, `SELECT status FROM trade_ledger WHERE trade_id = $1`, t.TradeID).Scan(&existing)
	if err == nil {
		return nil // already processed
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("check existing trade: %w", err)
	}

	var buyerBalance int64
	err = tx.QueryRowContext(ctx, `SELECT balance FROM escrow_accounts WHERE user_id = $1 FOR UPDATE`, buyerID).Scan(&buyerBalance)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("select buyer balance: %w", err)
	}
	if buyerBalance < totalCost {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO trade_ledger (trade_id, symbol, buyer_id, seller_id, price_cents, quantity, total_cost, status, failure_reason)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'INSUFFICIENT_FUNDS')`,
			t.TradeID, t.Symbol, buyerID, sellerID, t.PriceCents, t.Quantity, totalCost, StatusFailed); err != nil {
			return fmt.Errorf("insert failed trade: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		s.emitSettlementFailed(t.TradeID, t.Symbol, buyerID, sellerID, totalCost, "INSUFFICIENT_FUNDS")
		log.Printf("resale trade %s FAILED (INSUFFICIENT_FUNDS)", t.TradeID)
		return nil
	}

	// Move the hours. Lock the seller's held jobs for this symbol.
	rows, err := tx.QueryContext(ctx, `
		SELECT job_id::text, quantity FROM jobs
		WHERE buyer_id = $1 AND symbol = $2 AND status = 'held'
		ORDER BY created_at FOR UPDATE`, sellerID, t.Symbol)
	if err != nil {
		return fmt.Errorf("lock held jobs: %w", err)
	}
	type held struct {
		id  string
		qty int64
	}
	var jobs []held
	for rows.Next() {
		var h held
		if err := rows.Scan(&h.id, &h.qty); err != nil {
			rows.Close()
			return err
		}
		jobs = append(jobs, h)
	}
	rows.Close()

	remaining := t.Quantity
	moved := []string{}
	for _, h := range jobs {
		if remaining == 0 {
			break
		}
		if h.qty <= remaining {
			if _, err := tx.ExecContext(ctx, `UPDATE jobs SET buyer_id = $2, updated_at = now() WHERE job_id = $1::uuid`, h.id, buyerID); err != nil {
				return err
			}
			remaining -= h.qty
			moved = append(moved, h.id)
			continue
		}
		// Split: shrink the seller's job, give the buyer a new one on the same node/trade.
		if _, err := tx.ExecContext(ctx, `UPDATE jobs SET quantity = quantity - $2, updated_at = now() WHERE job_id = $1::uuid`, h.id, remaining); err != nil {
			return err
		}
		newID := newUUID()
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO jobs (job_id, trade_id, node_id, symbol, buyer_id, seller_id, workload, quantity, status)
			SELECT $1::uuid, trade_id, node_id, symbol, $2, seller_id, workload, $3, 'held'
			FROM jobs WHERE job_id = $4::uuid`, newID, buyerID, remaining, h.id); err != nil {
			return err
		}
		moved = append(moved, newID)
		remaining = 0
	}
	if remaining > 0 {
		// Raced: the holding shrank between the risk check and the fill.
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO trade_ledger (trade_id, symbol, buyer_id, seller_id, price_cents, quantity, total_cost, status, failure_reason)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'NO_ALLOCATION')`,
			t.TradeID, t.Symbol, buyerID, sellerID, t.PriceCents, t.Quantity, totalCost, StatusFailed); err != nil {
			return fmt.Errorf("insert failed trade: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		s.emitSettlementFailed(t.TradeID, t.Symbol, buyerID, sellerID, totalCost, "NO_ALLOCATION")
		log.Printf("resale trade %s FAILED (NO_ALLOCATION): seller %s no longer holds %d h", t.TradeID, sellerID, t.Quantity)
		return nil
	}

	// Funds: buyer -> reseller, settled now.
	if _, err := tx.ExecContext(ctx, `UPDATE escrow_accounts SET balance = balance - $1, updated_at = now() WHERE user_id = $2`, totalCost, buyerID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO escrow_accounts (user_id, balance, updated_at) VALUES ($1, $2, now())
		ON CONFLICT (user_id) DO UPDATE SET balance = escrow_accounts.balance + $2, updated_at = now()`, sellerID, totalCost); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO trade_ledger (trade_id, symbol, buyer_id, seller_id, price_cents, quantity, total_cost, status, settled_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, now())`,
		t.TradeID, t.Symbol, buyerID, sellerID, t.PriceCents, t.Quantity, totalCost, StatusSettled); err != nil {
		return fmt.Errorf("insert settled trade: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	log.Printf("resale trade %s SETTLED: %d cents %s -> %s; %d h transferred (jobs %v)", t.TradeID, totalCost, buyerID, sellerID, t.Quantity, moved)
	return nil
}

// runAllocation queues a held job owned by userID. If the allocation's node
// is no longer active, it is reassigned to a live node of the GPU type.
func (s *settlementService) runAllocation(ctx context.Context, userID, jobID string, workload json.RawMessage) (job, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return job{}, err
	}
	defer tx.Rollback() //nolint:errcheck

	var j job
	var active bool
	err = tx.QueryRowContext(ctx, `
		SELECT j.job_id::text, j.trade_id::text, COALESCE(j.node_id::text, ''), j.symbol, j.buyer_id, j.seller_id, j.workload, j.quantity,
		       EXISTS (SELECT 1 FROM seller_nodes sn WHERE sn.node_id = j.node_id AND sn.status = 'active')
		FROM jobs j WHERE j.job_id = $1::uuid AND j.buyer_id = $2 AND j.status = 'held' FOR UPDATE`, jobID, userID,
	).Scan(&j.JobID, &j.TradeID, &j.NodeID, &j.Symbol, &j.BuyerID, &j.SellerID, &j.Workload, &j.Quantity, &active)
	if errors.Is(err, sql.ErrNoRows) {
		return job{}, errJobConflict
	}
	if err != nil {
		return job{}, err
	}
	if !active || j.NodeID == "" {
		nodeID, err := s.findExecutorNode(ctx, j.Symbol, j.SellerID)
		if err != nil {
			return job{}, err
		}
		if nodeID == "" {
			return job{}, errNoCapacity
		}
		j.NodeID = nodeID
	}
	if len(workload) > 0 {
		j.Workload = workload
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE jobs SET status = 'queued', node_id = $2::uuid, workload = $3, updated_at = now()
		WHERE job_id = $1::uuid`, j.JobID, j.NodeID, j.Workload); err != nil {
		return job{}, err
	}
	if err := tx.Commit(); err != nil {
		return job{}, err
	}
	log.Printf("allocation %s RUN by %s: queued on node %s", j.JobID, userID, j.NodeID)
	s.emitJobEvent("ASSIGNED", j, "")
	return j, nil
}

var errNoCapacity = errors.New("no active node for this GPU type")

// allocationRow is the buyer-facing view of a job.
type allocationRow struct {
	JobID     string          `json:"job_id"`
	TradeID   string          `json:"trade_id"`
	Symbol    string          `json:"symbol"`
	NodeID    string          `json:"node_id"`
	Quantity  int64           `json:"quantity"`
	Status    string          `json:"status"`
	Reason    *string         `json:"status_reason"`
	Workload  json.RawMessage `json:"workload"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

// handleAllocations serves GET /v1/allocations for the authenticated buyer.
func (a *httpAPI) handleAllocations(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	userID, ok := accountID(w, r)
	if !ok {
		return
	}
	rows, err := a.svc.db.QueryContext(r.Context(), `
		SELECT job_id::text, trade_id::text, symbol, COALESCE(node_id::text, ''), quantity, status, status_reason, workload, created_at, updated_at
		FROM jobs WHERE buyer_id = $1 ORDER BY created_at DESC LIMIT 200`, userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	defer rows.Close()
	out := []allocationRow{}
	for rows.Next() {
		var x allocationRow
		if err := rows.Scan(&x.JobID, &x.TradeID, &x.Symbol, &x.NodeID, &x.Quantity, &x.Status, &x.Reason, &x.Workload, &x.CreatedAt, &x.UpdatedAt); err != nil {
			writeError(w, http.StatusInternalServerError, "scan failed")
			return
		}
		out = append(out, x)
	}
	writeJSON(w, http.StatusOK, out)
}

// handleJobRun serves POST /v1/jobs/{id}/run.
func (a *httpAPI) handleJobRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	userID, ok := accountID(w, r)
	if !ok {
		return
	}
	var req struct {
		Workload json.RawMessage `json:"workload"`
	}
	if r.Body != nil && r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
	}
	j, err := a.svc.runAllocation(r.Context(), userID, r.PathValue("id"), req.Workload)
	switch {
	case errors.Is(err, errJobConflict):
		writeError(w, http.StatusNotFound, "no held allocation with that id")
	case errors.Is(err, errNoCapacity):
		writeError(w, http.StatusConflict, "no active node for this GPU type right now")
	case err != nil:
		writeError(w, http.StatusInternalServerError, "run failed")
	default:
		writeJSON(w, http.StatusOK, map[string]any{"job_id": j.JobID, "node_id": j.NodeID, "status": "queued"})
	}
}
