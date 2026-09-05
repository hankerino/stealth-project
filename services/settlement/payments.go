package main

// M3: money in (Stripe Checkout, webhook-confirmed) and money out (manual
// payouts) for escrow. See db/migrations/0004_payments.up.sql.
//
// Invariants:
//   - Escrow is credited exactly once per Stripe session: the credit is tied to
//     the escrow_deposits row's PENDING -> COMPLETED transition under a row
//     lock, and provider_ref (session id) is UNIQUE.
//   - Amounts credited come from Stripe's amount_total, never from the client.
//   - A payout debits escrow immediately (funds are no longer tradable); an
//     admin REJECT refunds it. PAID is recorded after the out-of-band transfer.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	depositPending   = "PENDING"
	depositCompleted = "COMPLETED"
	depositExpired   = "EXPIRED"

	payoutRequested = "REQUESTED"
	payoutPaid      = "PAID"
	payoutRejected  = "REJECTED"

	// Guard rails for the closed beta.
	minDepositCents = 100       // $1
	maxDepositCents = 10_000_00 // $10,000 per checkout
	// Withdrawals: per request and per rolling 24 h (closed-beta limits;
	// raise deliberately, with the admin 4-eyes step, before real volume).
	maxPayoutCents      = 5_000_00  // $5,000 per request
	dailyPayoutCapCents = 10_000_00 // $10,000 per 24 h
	webhookMaxBody  = 64 << 10
)

type escrowDeposit struct {
	ID          string     `json:"id"`
	UserID      string     `json:"user_id"`
	AmountCents int64      `json:"amount_cents"`
	FeeCents    int64      `json:"fee_cents"`
	Provider    string     `json:"provider"`
	ProviderRef string     `json:"provider_ref"`
	Status      string     `json:"status"`
	CreatedAt   time.Time  `json:"created_at"`
	CompletedAt *time.Time `json:"completed_at"`
}

type payoutRequest struct {
	ID          string     `json:"id"`
	UserID      string     `json:"user_id"`
	AmountCents int64      `json:"amount_cents"`
	Status      string     `json:"status"`
	Note        *string    `json:"note"`
	CreatedAt   time.Time  `json:"created_at"`
	ResolvedAt  *time.Time `json:"resolved_at"`
	ResolvedBy  *string    `json:"resolved_by"`
}

// --- service ---

// createCheckout opens a Stripe Checkout session for userID and records a
// PENDING deposit keyed by the session id.
func (s *settlementService) createCheckout(ctx context.Context, userID string, amountCents int64) (*checkoutSession, error) {
	if amountCents < minDepositCents || amountCents > maxDepositCents {
		return nil, fmt.Errorf("amount_cents must be between %d and %d", minDepositCents, maxDepositCents)
	}
	depositID := uuidv4()
	feeCents := s.fees.depositFee(amountCents)
	base := strings.TrimRight(s.publicWebURL, "/")
	sess, err := s.stripe.createCheckoutSession(ctx, userID, depositID, amountCents, feeCents,
		base+"/portfolio?deposit=success", base+"/portfolio?deposit=cancelled")
	if err != nil {
		return nil, err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO escrow_deposits (id, user_id, amount_cents, provider, provider_ref, status, fee_cents)
		VALUES ($1, $2, $3, 'stripe', $4, $5, $6)`,
		depositID, userID, amountCents, sess.ID, depositPending, feeCents)
	if err != nil {
		// The webhook can still reconcile from session metadata (see
		// completeCheckout), so log rather than fail the user's checkout.
		log.Printf("payments: record pending deposit %s for session %s: %v", depositID, sess.ID, err)
	}
	return sess, nil
}

// completeCheckout credits escrow for a paid Checkout session, exactly once.
// Safe under webhook redelivery and out-of-order delivery.
func (s *settlementService) completeCheckout(ctx context.Context, sess *checkoutSession) error {
	if sess.PaymentStatus != "paid" {
		return nil // async payment methods: wait for async_payment_succeeded
	}
	userID := sess.Metadata["user_id"]
	if userID == "" {
		return fmt.Errorf("session %s has no user_id metadata", sess.ID)
	}
	if sess.AmountTotal <= 0 {
		return fmt.Errorf("session %s has non-positive amount_total %d", sess.ID, sess.AmountTotal)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck

	// The processing fee was a separate line item: escrow is credited
	// amount_total − fee. The fee comes from our pending row, falling back to
	// the session metadata Stripe echoes back (both were set at checkout).
	feeCents, _ := strconv.ParseInt(sess.Metadata["fee_cents"], 10, 64)

	var status string
	var depositID string
	var rowFee sql.NullInt64
	err = tx.QueryRowContext(ctx,
		`SELECT id::text, status, fee_cents FROM escrow_deposits WHERE provider_ref = $1 FOR UPDATE`, sess.ID,
	).Scan(&depositID, &status, &rowFee)
	if err == nil && rowFee.Valid {
		feeCents = rowFee.Int64
	}
	if feeCents < 0 || feeCents >= sess.AmountTotal {
		return fmt.Errorf("session %s has implausible fee %d for amount_total %d", sess.ID, feeCents, sess.AmountTotal)
	}
	credit := sess.AmountTotal - feeCents

	switch {
	case errors.Is(err, sql.ErrNoRows):
		// Pending row was never written (createCheckout insert failed) —
		// reconcile from Stripe's copy of the facts.
		depositID = sess.Metadata["deposit_id"]
		if depositID == "" {
			depositID = uuidv4()
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO escrow_deposits (id, user_id, amount_cents, provider, provider_ref, status, completed_at, fee_cents)
			VALUES ($1, $2, $3, 'stripe', $4, $5, now(), $6)`,
			depositID, userID, credit, sess.ID, depositCompleted, feeCents); err != nil {
			return err
		}
	case err != nil:
		return err
	case status == depositCompleted:
		return nil // duplicate delivery
	default:
		if _, err := tx.ExecContext(ctx, `
			UPDATE escrow_deposits SET status = $2, amount_cents = $3, fee_cents = $4, completed_at = now()
			WHERE provider_ref = $1`, sess.ID, depositCompleted, credit, feeCents); err != nil {
			return err
		}
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO escrow_accounts (user_id, balance, updated_at)
		VALUES ($1, $2, now())
		ON CONFLICT (user_id) DO UPDATE
			SET balance = escrow_accounts.balance + $2, updated_at = now()`,
		userID, credit); err != nil {
		return err
	}
	// Pass-through to the processor: recorded, not credited to the platform.
	did := depositID
	if err := s.recordFee(ctx, tx, feeKindDeposit, userID, nil, &did, credit, s.fees.DepositBps, feeCents, false); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	log.Printf("payments: credited %d cents to %s from stripe session %s (processing fee %d)", credit, userID, sess.ID, feeCents)
	return nil
}

// expireCheckout marks an abandoned session so it drops out of "pending".
func (s *settlementService) expireCheckout(ctx context.Context, sessionID string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE escrow_deposits SET status = $2 WHERE provider_ref = $1 AND status = $3`,
		sessionID, depositExpired, depositPending)
	return err
}

// adminCredit is the closed-beta / operator path: credit escrow directly and
// leave an audit row. Authorisation (admin role) is enforced by the gateway.
func (s *settlementService) adminCredit(ctx context.Context, adminID, userID string, amountCents int64) (int64, error) {
	if userID == "" {
		return 0, errors.New("user_id is required")
	}
	if amountCents <= 0 {
		return 0, errors.New("amount_cents must be greater than 0")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback() //nolint:errcheck
	id := uuidv4()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO escrow_deposits (id, user_id, amount_cents, provider, provider_ref, status, completed_at)
		VALUES ($1, $2, $3, 'admin', $4, $5, now())`,
		id, userID, amountCents, "admin:"+adminID+":"+id, depositCompleted); err != nil {
		return 0, err
	}
	var balance int64
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO escrow_accounts (user_id, balance, updated_at)
		VALUES ($1, $2, now())
		ON CONFLICT (user_id) DO UPDATE
			SET balance = escrow_accounts.balance + $2, updated_at = now()
		RETURNING balance`, userID, amountCents).Scan(&balance); err != nil {
		return 0, err
	}
	return balance, tx.Commit()
}

// requestPayout debits escrow and opens a REQUESTED payout.
func (s *settlementService) requestPayout(ctx context.Context, userID string, amountCents int64, note string) (*payoutRequest, error) {
	if amountCents <= 0 {
		return nil, errors.New("amount_cents must be greater than 0")
	}
	if amountCents > maxPayoutCents {
		return nil, fmt.Errorf("amount_cents exceeds the per-request maximum of %d", maxPayoutCents)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck

	// Withdrawal controls (security pass 2): one open request at a time and a
	// rolling 24 h cap, so a hijacked session cannot drain an account in one go.
	var open int
	if err := tx.QueryRowContext(ctx,
		`SELECT count(*) FROM payout_requests WHERE user_id = $1 AND status = 'REQUESTED'`, userID).Scan(&open); err != nil {
		return nil, err
	}
	if open > 0 {
		return nil, errPayoutPending
	}
	var last24h int64
	if err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(amount_cents), 0) FROM payout_requests
		WHERE user_id = $1 AND status <> 'REJECTED' AND created_at > now() - interval '24 hours'`, userID).Scan(&last24h); err != nil {
		return nil, err
	}
	if last24h+amountCents > dailyPayoutCapCents {
		return nil, errPayoutDailyCap
	}

	var balance int64
	err = tx.QueryRowContext(ctx,
		`SELECT balance FROM escrow_accounts WHERE user_id = $1 FOR UPDATE`, userID,
	).Scan(&balance)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && balance < amountCents) {
		return nil, errInsufficientFunds
	}
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE escrow_accounts SET balance = balance - $2, updated_at = now() WHERE user_id = $1`,
		userID, amountCents); err != nil {
		return nil, err
	}
	p := &payoutRequest{ID: uuidv4(), UserID: userID, AmountCents: amountCents, Status: payoutRequested}
	if n := strings.TrimSpace(note); n != "" {
		p.Note = &n
	}
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO payout_requests (id, user_id, amount_cents, status, note)
		VALUES ($1, $2, $3, $4, $5) RETURNING created_at`,
		p.ID, p.UserID, p.AmountCents, p.Status, p.Note).Scan(&p.CreatedAt); err != nil {
		return nil, err
	}
	return p, tx.Commit()
}

// resolvePayout moves a REQUESTED payout to PAID (money sent out-of-band) or
// REJECTED (escrow refunded). Idempotent: resolving twice is a no-op error.
func (s *settlementService) resolvePayout(ctx context.Context, adminID, payoutID, status string) (*payoutRequest, error) {
	if status != payoutPaid && status != payoutRejected {
		return nil, fmt.Errorf("status must be %s or %s", payoutPaid, payoutRejected)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck

	p := &payoutRequest{}
	err = tx.QueryRowContext(ctx, `
		SELECT id::text, user_id, amount_cents, status, note, created_at, resolved_at, resolved_by
		FROM payout_requests WHERE id = $1::uuid FOR UPDATE`, payoutID,
	).Scan(&p.ID, &p.UserID, &p.AmountCents, &p.Status, &p.Note, &p.CreatedAt, &p.ResolvedAt, &p.ResolvedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errPayoutNotFound
	}
	if err != nil {
		return nil, err
	}
	if p.Status != payoutRequested {
		return nil, fmt.Errorf("payout already %s", p.Status)
	}
	if status == payoutRejected {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO escrow_accounts (user_id, balance, updated_at)
			VALUES ($1, $2, now())
			ON CONFLICT (user_id) DO UPDATE
				SET balance = escrow_accounts.balance + $2, updated_at = now()`,
			p.UserID, p.AmountCents); err != nil {
			return nil, err
		}
	}
	if err := tx.QueryRowContext(ctx, `
		UPDATE payout_requests SET status = $2, resolved_at = now(), resolved_by = $3
		WHERE id = $1::uuid RETURNING status, resolved_at, resolved_by`,
		p.ID, status, adminID).Scan(&p.Status, &p.ResolvedAt, &p.ResolvedBy); err != nil {
		return nil, err
	}
	return p, tx.Commit()
}

var errPayoutNotFound = errors.New("payout not found")

func (s *settlementService) listDeposits(ctx context.Context, userID string, limit int) ([]escrowDeposit, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id::text, user_id, amount_cents, fee_cents, provider, provider_ref, status, created_at, completed_at
		FROM escrow_deposits WHERE user_id = $1 ORDER BY created_at DESC LIMIT $2`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []escrowDeposit{}
	for rows.Next() {
		var d escrowDeposit
		if err := rows.Scan(&d.ID, &d.UserID, &d.AmountCents, &d.FeeCents, &d.Provider, &d.ProviderRef, &d.Status, &d.CreatedAt, &d.CompletedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// listPayouts returns payouts for one user (userID != "") or, for admins,
// all payouts optionally filtered by status.
func (s *settlementService) listPayouts(ctx context.Context, userID, status string, limit int) ([]payoutRequest, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id::text, user_id, amount_cents, status, note, created_at, resolved_at, resolved_by
		FROM payout_requests
		WHERE ($1 = '' OR user_id = $1) AND ($2 = '' OR status = $2)
		ORDER BY created_at DESC LIMIT $3`, userID, status, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []payoutRequest{}
	for rows.Next() {
		var p payoutRequest
		if err := rows.Scan(&p.ID, &p.UserID, &p.AmountCents, &p.Status, &p.Note, &p.CreatedAt, &p.ResolvedAt, &p.ResolvedBy); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// --- HTTP ---

// handleCheckout serves POST /v1/escrow/checkout {amount_cents} -> {url, session_id}.
func (a *httpAPI) handleCheckout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	userID, ok := accountID(w, r)
	if !ok {
		return
	}
	if !a.svc.stripe.enabled() {
		writeError(w, http.StatusServiceUnavailable, errStripeDisabled.Error())
		return
	}
	var req struct {
		AmountCents int64 `json:"amount_cents"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	sess, err := a.svc.createCheckout(r.Context(), userID, req.AmountCents)
	if err != nil {
		status := http.StatusBadRequest
		if strings.HasPrefix(err.Error(), "stripe:") {
			status = http.StatusBadGateway
			log.Printf("payments: checkout for %s: %v", userID, err)
		}
		writeError(w, status, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"url": sess.URL, "session_id": sess.ID, "amount_cents": req.AmountCents})
}

// handleStripeWebhook serves POST /v1/stripe/webhook. Public endpoint: trust
// comes from the Stripe-Signature header, not the gateway.
func (a *httpAPI) handleStripeWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, webhookMaxBody))
	if err != nil {
		writeError(w, http.StatusBadRequest, "read body")
		return
	}
	if err := verifyWebhook(a.svc.stripe.webhookSecret, r.Header.Get("Stripe-Signature"), body, time.Now()); err != nil {
		log.Printf("payments: webhook rejected: %v", err)
		writeError(w, http.StatusBadRequest, "invalid signature")
		return
	}
	var ev stripeEvent
	if err := json.Unmarshal(body, &ev); err != nil {
		writeError(w, http.StatusBadRequest, "invalid event")
		return
	}
	switch ev.Type {
	case "checkout.session.completed", "checkout.session.async_payment_succeeded":
		var sess checkoutSession
		if err := json.Unmarshal(ev.Data.Object, &sess); err != nil {
			writeError(w, http.StatusBadRequest, "invalid session object")
			return
		}
		if err := a.svc.completeCheckout(r.Context(), &sess); err != nil {
			// 5xx => Stripe retries with backoff; the credit is idempotent.
			log.Printf("payments: webhook %s (%s): %v", ev.ID, ev.Type, err)
			writeError(w, http.StatusInternalServerError, "processing failed")
			return
		}
	case "checkout.session.expired", "checkout.session.async_payment_failed":
		var sess checkoutSession
		if err := json.Unmarshal(ev.Data.Object, &sess); err == nil {
			if err := a.svc.expireCheckout(r.Context(), sess.ID); err != nil {
				log.Printf("payments: expire session %s: %v", sess.ID, err)
			}
		}
	default:
		// Unsubscribed event types are acknowledged and ignored.
	}
	writeJSON(w, http.StatusOK, map[string]string{"received": ev.ID})
}

// handleWithdraw serves POST /v1/escrow/withdraw {amount_cents, note?}.
func (a *httpAPI) handleWithdraw(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	userID, ok := accountID(w, r)
	if !ok {
		return
	}
	var req struct {
		AmountCents int64  `json:"amount_cents"`
		Note        string `json:"note"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	p, err := a.svc.requestPayout(r.Context(), userID, req.AmountCents, req.Note)
	if err != nil {
		writePayoutError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// writePayoutError maps requestPayout failures: business rejections are 409,
// bad input is 400.
func writePayoutError(w http.ResponseWriter, err error) {
	if errors.Is(err, errInsufficientFunds) || errors.Is(err, errPayoutPending) || errors.Is(err, errPayoutDailyCap) {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeError(w, http.StatusBadRequest, err.Error())
}

// handleHistory serves GET /v1/escrow/history -> {deposits, payouts}.
func (a *httpAPI) handleHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	userID, ok := accountID(w, r)
	if !ok {
		return
	}
	deposits, err := a.svc.listDeposits(r.Context(), userID, 50)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	payouts, err := a.svc.listPayouts(r.Context(), userID, "", 50)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deposits": deposits, "payouts": payouts})
}

// handleAdminPayouts serves GET /v1/admin/payouts?status=REQUESTED.
// Admin role is enforced by the gateway.
func (a *httpAPI) handleAdminPayouts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if _, ok := accountID(w, r); !ok {
		return
	}
	payouts, err := a.svc.listPayouts(r.Context(), "", r.URL.Query().Get("status"), 200)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	writeJSON(w, http.StatusOK, payouts)
}

// handleAdminPayoutResolve serves POST /v1/admin/payouts/{id} {status: PAID|REJECTED}.
func (a *httpAPI) handleAdminPayoutResolve(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	adminID, ok := accountID(w, r)
	if !ok {
		return
	}
	var req struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	p, err := a.svc.resolvePayout(r.Context(), adminID, r.PathValue("id"), strings.ToUpper(req.Status))
	if errors.Is(err, errPayoutNotFound) {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, p)
}
