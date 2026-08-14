package main

// Models and core settlement logic for the Trade Settlement Service.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"
)

// --- Kafka event payload (matches libs/schemas/TradeExecuted.avsc) ---

type AggressorSide string

const (
	AggressorBuy  AggressorSide = "BUY"
	AggressorSell AggressorSide = "SELL"
)

type TradeExecuted struct {
	EventID          string        `json:"event_id"`
	TradeID          string        `json:"trade_id"`
	Symbol           string        `json:"symbol"`
	PriceCents       int64         `json:"price_cents"`
	Quantity         int64         `json:"quantity"`
	AggressorSide    AggressorSide `json:"aggressor_side"`
	MakerOrderID     string        `json:"maker_order_id"`
	TakerOrderID     string        `json:"taker_order_id"`
	MakerUserID      string        `json:"maker_user_id"`
	TakerUserID      string        `json:"taker_user_id"`
	OccurredAtUnixMs int64         `json:"occurred_at_unix_ms"`
}

// SettlementFailed event payload (published to sla-breach-events topic).
// Matches libs/schemas/SettlementFailed.avsc.
type SettlementFailed struct {
	EventID          string `json:"event_id"`
	TradeID          string `json:"trade_id"`
	Symbol           string `json:"symbol"`
	BuyerID          string `json:"buyer_id"`
	SellerID         string `json:"seller_id"`
	TotalCost        int64  `json:"total_cost"`
	Reason           string `json:"reason"`
	OccurredAtUnixMs int64  `json:"occurred_at_unix_ms"`
}

// --- Settlement statuses ---

const (
	StatusPending = "PENDING"
	StatusSettled = "SETTLED"
	StatusFailed  = "FAILED"
)

var errInsufficientFunds = errors.New("insufficient escrow balance")

// settlementService holds the shared state used by the consumer and REST API.
type settlementService struct {
	db                 *sql.DB
	failedEventProducer *kafkaProducer // nil in dev mode (no Kafka)
}

// settleTrade processes one TradeExecuted event atomically:
// 1. Determine buyer/seller from aggressor side.
// 2. Check buyer escrow balance >= total_cost.
// 3. If sufficient: debit buyer, credit seller, insert SETTLED ledger row.
// 4. If insufficient: insert FAILED ledger row, emit SettlementFailed.
//
// All DB writes are in a single transaction. Idempotent: if trade_id already
// exists in trade_ledger, the trade is skipped (already processed).
func (s *settlementService) settleTrade(ctx context.Context, t *TradeExecuted) error {
	totalCost := t.PriceCents * t.Quantity

	// Determine buyer and seller from aggressor side.
	// Aggressor = the one crossing the spread (taker). If aggressor is BUY,
	// the taker is the buyer and maker is the seller. Vice versa for SELL.
	var buyerID, sellerID string
	if t.AggressorSide == AggressorBuy {
		buyerID = t.TakerUserID
		sellerID = t.MakerUserID
	} else {
		buyerID = t.MakerUserID
		sellerID = t.TakerUserID
	}

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	// Idempotency check: skip if already processed.
	var existingStatus string
	err = tx.QueryRowContext(ctx,
		`SELECT status FROM trade_ledger WHERE trade_id = $1`, t.TradeID,
	).Scan(&existingStatus)
	if err == nil {
		// Already processed — skip.
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("check existing trade: %w", err)
	}

	// Check buyer balance.
	var buyerBalance int64
	err = tx.QueryRowContext(ctx,
		`SELECT balance FROM escrow_accounts WHERE user_id = $1 FOR UPDATE`,
		buyerID,
	).Scan(&buyerBalance)
	if errors.Is(err, sql.ErrNoRows) {
		buyerBalance = 0
	} else if err != nil {
		return fmt.Errorf("select buyer balance: %w", err)
	}

	if buyerBalance < totalCost {
		// Insufficient funds — record FAILED and emit event.
		_, err = tx.ExecContext(ctx, `
			INSERT INTO trade_ledger (trade_id, symbol, buyer_id, seller_id, price_cents,
			                           quantity, total_cost, status, failure_reason)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
			t.TradeID, t.Symbol, buyerID, sellerID, t.PriceCents,
			t.Quantity, totalCost, StatusFailed, "INSUFFICIENT_FUNDS",
		)
		if err != nil {
			return fmt.Errorf("insert failed trade: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit failed trade: %w", err)
		}
		// Emit SettlementFailed event (best-effort, outside the tx).
		s.emitSettlementFailed(t.TradeID, t.Symbol, buyerID, sellerID, totalCost, "INSUFFICIENT_FUNDS")
		log.Printf("trade %s FAILED: buyer %s has %d cents, needs %d", t.TradeID, buyerID, buyerBalance, totalCost)
		return nil
	}

	// Sufficient funds — debit buyer, credit seller.
	_, err = tx.ExecContext(ctx,
		`UPDATE escrow_accounts SET balance = balance - $1, updated_at = now()
		 WHERE user_id = $2`,
		totalCost, buyerID,
	)
	if err != nil {
		return fmt.Errorf("debit buyer: %w", err)
	}

	// Upsert seller escrow account (credit, creating if missing).
	_, err = tx.ExecContext(ctx, `
		INSERT INTO escrow_accounts (user_id, balance, updated_at)
		VALUES ($1, $2, now())
		ON CONFLICT (user_id) DO UPDATE
			SET balance = escrow_accounts.balance + $2, updated_at = now()`,
		sellerID, totalCost,
	)
	if err != nil {
		return fmt.Errorf("credit seller: %w", err)
	}

	// Record SETTLED in trade_ledger.
	_, err = tx.ExecContext(ctx, `
		INSERT INTO trade_ledger (trade_id, symbol, buyer_id, seller_id, price_cents,
		                           quantity, total_cost, status, settled_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, now())`,
		t.TradeID, t.Symbol, buyerID, sellerID, t.PriceCents,
		t.Quantity, totalCost, StatusSettled,
	)
	if err != nil {
		return fmt.Errorf("insert settled trade: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	log.Printf("trade %s SETTLED: buyer %s -> seller %s, %d cents for %d units",
		t.TradeID, buyerID, sellerID, totalCost, t.Quantity)
	return nil
}

// depositFunds adds to a buyer's escrow balance (idempotent upsert).
func (s *settlementService) depositFunds(ctx context.Context, userID string, amountCents int64) (int64, error) {
	if userID == "" {
		return 0, errors.New("user_id is required")
	}
	if amountCents <= 0 {
		return 0, errors.New("amount_cents must be greater than 0")
	}
	var newBalance int64
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO escrow_accounts (user_id, balance, updated_at)
		VALUES ($1, $2, now())
		ON CONFLICT (user_id) DO UPDATE
			SET balance = escrow_accounts.balance + $2, updated_at = now()
		RETURNING balance`,
		userID, amountCents,
	).Scan(&newBalance)
	if err != nil {
		return 0, fmt.Errorf("deposit: %w", err)
	}
	return newBalance, nil
}

// getBalance returns a buyer's escrow balance (0 if no account exists).
func (s *settlementService) getBalance(ctx context.Context, userID string) (int64, error) {
	var balance int64
	err := s.db.QueryRowContext(ctx,
		`SELECT balance FROM escrow_accounts WHERE user_id = $1`, userID,
	).Scan(&balance)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return balance, nil
}

// emitSettlementFailed publishes a SettlementFailed event (best-effort).
func (s *settlementService) emitSettlementFailed(tradeID, symbol, buyerID, sellerID string, totalCost int64, reason string) {
	if s.failedEventProducer == nil {
		return
	}
	ev := SettlementFailed{
		EventID:          newUUID(),
		TradeID:          tradeID,
		Symbol:           symbol,
		BuyerID:          buyerID,
		SellerID:         sellerID,
		TotalCost:        totalCost,
		Reason:           reason,
		OccurredAtUnixMs: time.Now().UnixMilli(),
	}
	payload, err := json.Marshal(ev)
	if err != nil {
		log.Printf("marshal SettlementFailed: %v", err)
		return
	}
	if err := s.failedEventProducer.publish(tradeID, payload); err != nil {
		log.Printf("publish SettlementFailed for trade %s: %v", tradeID, err)
	}
}

// newUUID returns a random UUIDv4 string (avoids a third-party dependency).
func newUUID() string {
	return uuidv4()
}
