package main

// Workload-job lifecycle (Phase 4). One job per trade: settlement holds the
// buyer's escrow (trade_ledger PENDING) and queues a job for a seller node;
// the node polls the job control plane (this service's HTTP) and reports
// status transitions with an Ed25519-signed request. On COMPLETED the escrow
// is released to the seller; on FAILED the buyer is refunded and a
// SettlementFailed event goes out (same pattern as insufficient funds).
//
// Scheduling ownership: settlement (not order) because it already consumes
// TradeExecuted, owns escrow, and can create ledger+job in one transaction.
//
// Job control plane (agent-facing; the node-agent is stdlib-only, so no
// Kafka consumer there — events also land on the `node-jobs` topic for
// audit/future Kafka-native consumers):
//
//	GET  /v1/jobs/poll?node_id=&limit=     queued jobs for a node
//	POST /v1/jobs/{id}/status              signed status transition
//
// Status signature (v1): base64 Ed25519 over
// sha256(node_id || job_id || status || decimal(sent_at_unix_ms)) — the
// heartbeat scheme extended with job_id+status, verified against
// seller_nodes.public_key.

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"
)

const jobsTopic = "node-jobs"

// JobEvent matches libs/schemas/JobEvent.avsc (topic: node-jobs, key node_id).
type JobEvent struct {
	EventID          string `json:"event_id"`
	JobID            string `json:"job_id"`
	TradeID          string `json:"trade_id"`
	NodeID           string `json:"node_id"`
	Symbol           string `json:"symbol"`
	BuyerID          string `json:"buyer_id"`
	SellerID         string `json:"seller_id"`
	Status           string `json:"status"` // ASSIGNED | STARTED | COMPLETED | FAILED
	Reason           string `json:"reason,omitempty"`
	OccurredAtUnixMs int64  `json:"occurred_at_unix_ms"`
}

// job is a jobs table row as returned to the polling agent.
type job struct {
	JobID    string          `json:"job_id"`
	TradeID  string          `json:"trade_id"`
	NodeID   string          `json:"node_id"`
	Symbol   string `json:"symbol"`
	BuyerID  string `json:"buyer_id"`
	SellerID string `json:"seller_id"`
	Workload json.RawMessage `json:"workload"`
	Status   string          `json:"-"`
}

// gpuPart extracts the GPU type from a symbol (H100:us-east-1 -> H100).
func gpuPart(symbol string) string { return strings.SplitN(symbol, ":", 2)[0] }

// findExecutorNode picks the most recently registered active node offering
// the symbol's GPU type. Newest-first: dev loops register a fresh node per
// run, and the newest registration is the likeliest to be alive.
func (s *settlementService) findExecutorNode(ctx context.Context, symbol string) (string, error) {
	var nodeID string
	err := s.db.QueryRowContext(ctx, `
		SELECT sn.node_id::text
		FROM seller_nodes sn
		JOIN gpu_types g ON g.id = sn.gpu_type_id
		WHERE g.name = $1 AND sn.status = 'active'
		ORDER BY sn.created_at DESC
		LIMIT 1`, gpuPart(symbol),
	).Scan(&nodeID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return nodeID, err
}

// findBackingNode handles resale routing. If the seller operates an active
// node for the symbol's GPU type this is primary supply: ("", false, nil) and
// the caller picks an executor. Otherwise the seller is reselling hours they
// hold, and the job belongs on the node that executed their most recent
// purchase of this symbol: (node, true, nil). ("", true, nil) means a resale
// with no traceable backing node; the caller falls back to any live node.
func (s *settlementService) findBackingNode(ctx context.Context, sellerID, symbol string) (string, bool, error) {
	var operates int
	if err := s.db.QueryRowContext(ctx, `
		SELECT count(*) FROM seller_nodes sn
		JOIN gpu_types g ON g.id = sn.gpu_type_id
		WHERE sn.seller_id::text = $1 AND g.name = $2 AND sn.status = 'active'`,
		sellerID, gpuPart(symbol)).Scan(&operates); err != nil {
		return "", false, err
	}
	if operates > 0 {
		return "", false, nil
	}
	var nodeID sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT j.node_id::text FROM jobs j
		JOIN seller_nodes sn ON sn.node_id = j.node_id AND sn.status = 'active'
		WHERE j.buyer_id = $1 AND j.symbol = $2
		ORDER BY j.created_at DESC LIMIT 1`, sellerID, symbol).Scan(&nodeID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", true, err
	}
	return nodeID.String, true, nil
}

// createJobForTrade holds the buyer's escrow and creates the queued job +
// PENDING ledger row in one transaction, then emits JobAssigned.
// Business outcomes return nil (consumer commits): job created, FAILED with
// SettlementFailed (insufficient funds or no capacity). Transient errors
// bubble up for the consumer's bounded-retry loop.
func (s *settlementService) createJobForTrade(ctx context.Context, t *TradeExecuted) error {
	totalCost := t.PriceCents * t.Quantity

	var buyerID, sellerID string
	if t.AggressorSide == AggressorBuy {
		buyerID, sellerID = t.TakerUserID, t.MakerUserID
	} else {
		buyerID, sellerID = t.MakerUserID, t.TakerUserID
	}

	// Resale: a seller with no node of their own is passing on hours they
	// bought, so the job goes to the node that backed their holding (the
	// allocation transfers with the sale). Primary supply picks a live node.
	nodeID, resale, err := s.findBackingNode(ctx, sellerID, t.Symbol)
	if err != nil {
		return fmt.Errorf("find backing node: %w", err)
	}
	if nodeID == "" {
		if nodeID, err = s.findExecutorNode(ctx, t.Symbol); err != nil {
			return fmt.Errorf("find executor node: %w", err)
		}
	}

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	// Idempotency: already processed (Kafka redelivery).
	var existing string
	err = tx.QueryRowContext(ctx, `SELECT status FROM trade_ledger WHERE trade_id = $1`, t.TradeID).Scan(&existing)
	if err == nil {
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("check existing trade: %w", err)
	}

	var buyerBalance int64
	err = tx.QueryRowContext(ctx,
		`SELECT balance FROM escrow_accounts WHERE user_id = $1 FOR UPDATE`, buyerID,
	).Scan(&buyerBalance)
	if errors.Is(err, sql.ErrNoRows) {
		buyerBalance = 0
	} else if err != nil {
		return fmt.Errorf("select buyer balance: %w", err)
	}

	fail := func(reason string) error {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO trade_ledger (trade_id, symbol, buyer_id, seller_id, price_cents,
			                           quantity, total_cost, status, failure_reason)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
			t.TradeID, t.Symbol, buyerID, sellerID, t.PriceCents, t.Quantity, totalCost,
			StatusFailed, reason,
		); err != nil {
			return fmt.Errorf("insert failed trade: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit failed trade: %w", err)
		}
		s.emitSettlementFailed(t.TradeID, t.Symbol, buyerID, sellerID, totalCost, reason)
		log.Printf("trade %s FAILED (%s): no escrow moved", t.TradeID, reason)
		return nil
	}

	if buyerBalance < totalCost {
		return fail("INSUFFICIENT_FUNDS")
	}
	if nodeID == "" {
		return fail("NO_CAPACITY")
	}

	// Hold: debit the buyer now (funds locked), credit the seller only on
	// JobCompleted.
	if _, err := tx.ExecContext(ctx,
		`UPDATE escrow_accounts SET balance = balance - $1, updated_at = now() WHERE user_id = $2`,
		totalCost, buyerID,
	); err != nil {
		return fmt.Errorf("hold buyer escrow: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO trade_ledger (trade_id, symbol, buyer_id, seller_id, price_cents,
		                           quantity, total_cost, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		t.TradeID, t.Symbol, buyerID, sellerID, t.PriceCents, t.Quantity, totalCost, StatusPending,
	); err != nil {
		return fmt.Errorf("insert pending trade: %w", err)
	}

	j := job{
		JobID:    newUUID(),
		TradeID:  t.TradeID,
		NodeID:   nodeID,
		Symbol:   t.Symbol,
		BuyerID:  buyerID,
		SellerID: sellerID,
		Workload: json.RawMessage(fmt.Sprintf(`{"mock_duration_seconds": %d}`, defaultMockJobSeconds)),
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO jobs (job_id, trade_id, node_id, symbol, buyer_id, seller_id, workload)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		j.JobID, j.TradeID, j.NodeID, j.Symbol, j.BuyerID, j.SellerID, j.Workload,
	); err != nil {
		return fmt.Errorf("insert job: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	kind := "primary"
	if resale {
		kind = "resale"
	}
	log.Printf("trade %s HELD (%s): %d cents from %s; job %s queued for node %s",
		t.TradeID, kind, totalCost, buyerID, j.JobID, nodeID)
	s.emitJobEvent("ASSIGNED", j, "")
	return nil
}

// reportJobStarted transitions queued -> running and emits JobStarted.
func (s *settlementService) reportJobStarted(ctx context.Context, jobID, nodeID string) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE jobs SET status = 'running', updated_at = now()
		WHERE job_id = $1 AND node_id = $2 AND status = 'queued'`, jobID, nodeID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errJobConflict
	}
	log.Printf("job %s STARTED on node %s", jobID, nodeID)
	s.emitJobEventByID("STARTED", jobID, "")
	return nil
}

// reportJobCompleted transitions running -> completed, releases the held
// escrow to the seller, and settles the ledger — all in one transaction —
// then emits JobCompleted.
func (s *settlementService) reportJobCompleted(ctx context.Context, jobID, nodeID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck

	var j job
	err = tx.QueryRowContext(ctx, `
		UPDATE jobs SET status = 'completed', updated_at = now()
		WHERE job_id = $1 AND node_id = $2 AND status = 'running'
		RETURNING trade_id, symbol, buyer_id, seller_id`, jobID, nodeID,
	).Scan(&j.TradeID, &j.Symbol, &j.BuyerID, &j.SellerID)
	if errors.Is(err, sql.ErrNoRows) {
		return errJobConflict
	}
	if err != nil {
		return err
	}

	var total int64
	if err := tx.QueryRowContext(ctx, `
		UPDATE trade_ledger SET status = 'SETTLED', settled_at = now()
		WHERE trade_id = $1 AND status = 'PENDING'
		RETURNING total_cost`, j.TradeID,
	).Scan(&total); err != nil {
		return fmt.Errorf("settle ledger: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO escrow_accounts (user_id, balance, updated_at)
		VALUES ($1, $2, now())
		ON CONFLICT (user_id) DO UPDATE
			SET balance = escrow_accounts.balance + $2, updated_at = now()`,
		j.SellerID, total,
	); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	log.Printf("job %s COMPLETED: released %d cents to seller %s (trade %s SETTLED)",
		jobID, total, j.SellerID, j.TradeID)
	s.emitJobEventByID("COMPLETED", jobID, "")
	return nil
}

// reportJobFailed transitions queued|running -> failed, refunds the held
// escrow to the buyer, marks the ledger FAILED, then emits JobFailed +
// SettlementFailed.
func (s *settlementService) reportJobFailed(ctx context.Context, jobID, nodeID, reason string) error {
	if reason == "" {
		reason = "EXECUTION_ERROR"
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck

	var j job
	err = tx.QueryRowContext(ctx, `
		UPDATE jobs SET status = 'failed', status_reason = $3, updated_at = now()
		WHERE job_id = $1 AND node_id = $2 AND status IN ('queued', 'running')
		RETURNING trade_id, symbol, buyer_id, seller_id`, jobID, nodeID, reason,
	).Scan(&j.TradeID, &j.Symbol, &j.BuyerID, &j.SellerID)
	if errors.Is(err, sql.ErrNoRows) {
		return errJobConflict
	}
	if err != nil {
		return err
	}

	var total int64
	if err := tx.QueryRowContext(ctx, `
		UPDATE trade_ledger SET status = 'FAILED', failure_reason = $2
		WHERE trade_id = $1 AND status = 'PENDING'
		RETURNING total_cost`, j.TradeID, "JOB_FAILED: "+reason,
	).Scan(&total); err != nil {
		return fmt.Errorf("fail ledger: %w", err)
	}

	// Refund the hold.
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO escrow_accounts (user_id, balance, updated_at)
		VALUES ($1, $2, now())
		ON CONFLICT (user_id) DO UPDATE
			SET balance = escrow_accounts.balance + $2, updated_at = now()`,
		j.BuyerID, total,
	); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	log.Printf("job %s FAILED (%s): refunded %d cents to buyer %s", jobID, reason, total, j.BuyerID)
	s.emitJobEventByID("FAILED", jobID, reason)
	s.emitSettlementFailed(j.TradeID, j.Symbol, j.BuyerID, j.SellerID, total, "JOB_FAILED: "+reason)
	return nil
}

var errJobConflict = errors.New("job not found or not in the expected state")

// ---- job event emission (best-effort, after commit) -------------------------

func (s *settlementService) emitJobEventByID(status, jobID, reason string) {
	var j job
	err := s.db.QueryRowContext(context.Background(), `
		SELECT job_id::text, trade_id::text, node_id::text, symbol, buyer_id, seller_id
		FROM jobs WHERE job_id = $1`, jobID,
	).Scan(&j.JobID, &j.TradeID, &j.NodeID, &j.Symbol, &j.BuyerID, &j.SellerID)
	if err != nil {
		log.Printf("emit %s: load job %s: %v", status, jobID, err)
		return
	}
	s.emitJobEvent(status, j, reason)
}

func (s *settlementService) emitJobEvent(status string, j job, reason string) {
	if s.jobEventProducer == nil {
		return
	}
	ev := JobEvent{
		EventID:          newUUID(),
		JobID:            j.JobID,
		TradeID:          j.TradeID,
		NodeID:           j.NodeID,
		Symbol:           j.Symbol,
		BuyerID:          j.BuyerID,
		SellerID:         j.SellerID,
		Status:           status,
		Reason:           reason,
		OccurredAtUnixMs: time.Now().UnixMilli(),
	}
	payload, err := json.Marshal(ev)
	if err != nil {
		log.Printf("marshal JobEvent: %v", err)
		return
	}
	if err := s.jobEventProducer.publish(j.NodeID, payload); err != nil {
		log.Printf("publish JobEvent %s for job %s: %v", status, ev.JobID, err)
	}
}

// ---- job status signature verification --------------------------------------

// jobStatusMessage is the signed byte string (must match the agent's).
func jobStatusMessage(nodeID, jobID, status string, sentAtUnixMs int64) []byte {
	return []byte(nodeID + jobID + status + strconv.FormatInt(sentAtUnixMs, 10))
}

// verifyJobStatusSignature checks the request signature against the node's
// registered public key (seller_nodes.public_key, base64 raw 32-byte key).
func (s *settlementService) verifyJobStatusSignature(ctx context.Context, nodeID, jobID, status string, sentAtUnixMs int64, sigB64 string) bool {
	var keyB64 string
	err := s.db.QueryRowContext(ctx,
		`SELECT public_key FROM seller_nodes WHERE node_id = $1::uuid`, nodeID,
	).Scan(&keyB64)
	if err != nil {
		log.Printf("job status: unknown node %s (%v)", nodeID, err)
		return false
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(keyB64))
	if err != nil || len(raw) != 32 {
		log.Printf("job status: bad public key for node %s", nodeID)
		return false
	}
	sig, err := base64.StdEncoding.DecodeString(sigB64)
	if err != nil {
		return false
	}
	digest := sha256.Sum256(jobStatusMessage(nodeID, jobID, status, sentAtUnixMs))
	return ed25519.Verify(ed25519.PublicKey(raw), digest[:], sig)
}
