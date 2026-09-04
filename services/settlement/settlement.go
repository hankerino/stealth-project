package main

// Models and core settlement logic for the Trade Settlement Service.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
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
	db                  *sql.DB
	failedEventProducer *kafkaProducer // nil in dev mode (no Kafka)
	jobEventProducer    *kafkaProducer // nil in dev mode (no Kafka)
	mtm                 *mtmRunner     // daily mark-to-market runner
	stripe              *stripeClient  // never nil; disabled when no secret key
	publicWebURL        string         // where Checkout returns the user (cte-web)
}

// Trade settlement flow (Phase 4): the consumer's trades handler is
// createJobForTrade (jobs.go) — escrow is HELD at trade time and released
// when the executing node reports completion. The previous settle-now
// implementation was replaced; see jobs.go for the full lifecycle.

// Escrow credits live in payments.go (Stripe checkout webhook / admin credit)
// so every credit leaves an escrow_deposits audit row.

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
