package main

// Core risk logic for the Compute Trading Exchange (Phase 2): position
// tracking from trades and pre-trade margin checks. Shared by the REST and
// gRPC fronts. All money is integer cents; quantities are GPU-hours (signed
// for positions: positive = long, negative = short).

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"strings"
	"time"
)

// Order kinds and sides (string form used across REST/gRPC/DB).
const (
	KindSpot    = "SPOT"
	KindFutures = "FUTURES"

	SideBuy  = "BUY"
	SideSell = "SELL"
)

// Default risk parameters used when a contract has no explicit row.
const (
	defaultInitialMarginPct     = 10
	defaultMaintenanceMarginPct = 5
	defaultMaxPositionSize      = 0 // 0 = unlimited
)

// riskParams is the margin schedule + limit for one contract.
type riskParams struct {
	InitialMarginPct     int
	MaintenanceMarginPct int
	MaxPositionSize      int64
}

// MarginCheck is a normalized pre-trade check request (REST/gRPC agnostic).
type MarginCheck struct {
	UserID     string
	ContractID int64
	Symbol     string
	Kind       string // SPOT | FUTURES
	Side       string // BUY | SELL
	PriceCents int64
	Quantity   int64
}

// MarginResult is the decision plus the numbers behind it.
type MarginResult struct {
	Allowed             bool
	RequiredMarginCents int64
	AvailableCents      int64
	ProjectedPosition   int64
	Reason              string
}

// Position is a user's net position in one contract.
type Position struct {
	UserID             string
	ContractID         int64
	Symbol             string
	NetQuantity        int64
	AvgEntryPriceCents int64
	MarginPostedCents  int64
}

// TradeExecuted mirrors libs/schemas/TradeExecuted.avsc (topic: trades).
type TradeExecuted struct {
	EventID          string `json:"event_id"`
	TradeID          string `json:"trade_id"`
	Symbol           string `json:"symbol"`
	PriceCents       int64  `json:"price_cents"`
	Quantity         int64  `json:"quantity"`
	AggressorSide    string `json:"aggressor_side"`
	MakerOrderID     string `json:"maker_order_id"`
	TakerOrderID     string `json:"taker_order_id"`
	MakerUserID      string `json:"maker_user_id"`
	TakerUserID      string `json:"taker_user_id"`
	OccurredAtUnixMs int64  `json:"occurred_at_unix_ms"`
}

// OrderUpdated mirrors libs/schemas/OrderUpdated.avsc (topic: order-updates).
type OrderUpdated struct {
	EventID           string  `json:"event_id"`
	OrderID           string  `json:"order_id"`
	UserID            string  `json:"user_id"`
	Symbol            string  `json:"symbol"`
	NewStatus         string  `json:"new_status"`
	FilledQuantity    int64   `json:"filled_quantity"`
	RemainingQuantity int64   `json:"remaining_quantity"`
	Reason            *string `json:"reason"`
	OccurredAtUnixMs  int64   `json:"occurred_at_unix_ms"`
}

// PositionUpdated mirrors libs/schemas/PositionUpdated.avsc (topic: risk-events).
type PositionUpdated struct {
	EventID            string `json:"event_id"`
	UserID             string `json:"user_id"`
	ContractID         int64  `json:"contract_id"`
	Symbol             string `json:"symbol"`
	NetQuantity        int64  `json:"net_quantity"`
	AvgEntryPriceCents int64  `json:"avg_entry_price_cents"`
	MarginPostedCents  int64  `json:"margin_posted_cents"`
	OccurredAtUnixMs   int64  `json:"occurred_at_unix_ms"`
}

// riskService is the shared core.
type riskService struct {
	db  *sql.DB
	pub *kafkaProducer // nil in dev (no Kafka): PositionUpdated is skipped
}

// isFuturesSymbol reports whether a symbol is a futures book (encodes :FUT:).
func isFuturesSymbol(symbol string) bool { return strings.Contains(symbol, ":FUT:") }

// resolveContract maps a symbol to (contractID, kind). Spot => contractID 0.
func (s *riskService) resolveContract(ctx context.Context, symbol string) (int64, string) {
	if !isFuturesSymbol(symbol) {
		return 0, KindSpot
	}
	var id int64
	err := s.db.QueryRowContext(ctx,
		`SELECT id FROM futures_contracts WHERE symbol = $1`, symbol).Scan(&id)
	if err != nil {
		// Unknown futures symbol: treat as contract 0 so we still track it.
		return 0, KindFutures
	}
	return id, KindFutures
}

// loadRiskParams returns the contract's params, or in-code defaults.
func (s *riskService) loadRiskParams(ctx context.Context, contractID int64) riskParams {
	p := riskParams{
		InitialMarginPct:     defaultInitialMarginPct,
		MaintenanceMarginPct: defaultMaintenanceMarginPct,
		MaxPositionSize:      defaultMaxPositionSize,
	}
	if contractID == 0 {
		return p
	}
	_ = s.db.QueryRowContext(ctx,
		`SELECT initial_margin_pct, maintenance_margin_pct, max_position_size
		 FROM risk_parameters WHERE contract_id = $1`, contractID,
	).Scan(&p.InitialMarginPct, &p.MaintenanceMarginPct, &p.MaxPositionSize)
	return p
}

// loadPosition returns the user's current position (zero value if none).
func (s *riskService) loadPosition(ctx context.Context, q sqlQueryer, userID string, contractID int64) (Position, error) {
	var p Position
	err := q.QueryRowContext(ctx, `
		SELECT user_id, contract_id, symbol, net_quantity, avg_entry_price_cents, margin_posted_cents
		FROM positions WHERE user_id = $1 AND contract_id = $2`,
		userID, contractID,
	).Scan(&p.UserID, &p.ContractID, &p.Symbol, &p.NetQuantity, &p.AvgEntryPriceCents, &p.MarginPostedCents)
	if errors.Is(err, sql.ErrNoRows) {
		return Position{UserID: userID, ContractID: contractID}, nil
	}
	if err != nil {
		return Position{}, err
	}
	return p, nil
}

// sqlQueryer lets loadPosition run against *sql.DB or *sql.Tx.
type sqlQueryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// totalMarginPosted sums margin the user has locked across all positions.
func (s *riskService) totalMarginPosted(ctx context.Context, userID string) (int64, error) {
	var total sql.NullInt64
	err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(margin_posted_cents), 0) FROM positions WHERE user_id = $1`,
		userID,
	).Scan(&total)
	if err != nil {
		return 0, err
	}
	return total.Int64, nil
}

// escrowBalance reads the user's free escrow from the shared settlement table.
// If the table/row is absent (dev without settlement migrated), returns 0.
func (s *riskService) escrowBalance(ctx context.Context, userID string) int64 {
	var bal int64
	if err := s.db.QueryRowContext(ctx,
		`SELECT balance FROM escrow_accounts WHERE user_id = $1`, userID,
	).Scan(&bal); err != nil {
		return 0
	}
	return bal
}

func abs64(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}

// CheckMargin runs the pre-trade check. Futures require initial margin
// (a percentage of notional); spot buys require the full notional; spot sells
// require no cash (capacity, enforced elsewhere). Also enforces position limits.
func (s *riskService) CheckMargin(ctx context.Context, in MarginCheck) (MarginResult, error) {
	contractID := in.ContractID
	kind := in.Kind
	if contractID == 0 && in.Symbol != "" {
		contractID, kind = s.resolveContract(ctx, in.Symbol)
	}
	if kind == "" {
		kind = KindSpot
	}
	params := s.loadRiskParams(ctx, contractID)

	pos, err := s.loadPosition(ctx, s.db, in.UserID, contractID)
	if err != nil {
		return MarginResult{}, err
	}
	delta := in.Quantity
	if in.Side == SideSell {
		delta = -in.Quantity
	}
	projected := pos.NetQuantity + delta

	// Position limit.
	if params.MaxPositionSize > 0 && abs64(projected) > params.MaxPositionSize {
		return MarginResult{
			Allowed:           false,
			ProjectedPosition: projected,
			Reason:            "POSITION_LIMIT_EXCEEDED",
		}, nil
	}

	notional := in.PriceCents * in.Quantity
	var required int64
	switch kind {
	case KindFutures:
		required = notional * int64(params.InitialMarginPct) / 100
	default: // SPOT
		if in.Side == SideBuy {
			required = notional
		}
	}

	posted, err := s.totalMarginPosted(ctx, in.UserID)
	if err != nil {
		return MarginResult{}, err
	}
	available := s.escrowBalance(ctx, in.UserID) - posted

	res := MarginResult{
		RequiredMarginCents: required,
		AvailableCents:      available,
		ProjectedPosition:   projected,
	}
	if required <= available {
		res.Allowed = true
	} else {
		res.Reason = "INSUFFICIENT_MARGIN"
	}
	return res, nil
}

// GetPosition returns a user's position in a contract.
func (s *riskService) GetPosition(ctx context.Context, userID string, contractID int64) (Position, error) {
	return s.loadPosition(ctx, s.db, userID, contractID)
}

// applyTrade updates both counterparties' positions from a fill and emits
// PositionUpdated for each. Buyer goes long (+qty), seller short (-qty).
func (s *riskService) applyTrade(ctx context.Context, t *TradeExecuted) error {
	contractID, _ := s.resolveContract(ctx, t.Symbol)
	params := s.loadRiskParams(ctx, contractID)

	var buyerID, sellerID string
	if t.AggressorSide == SideBuy {
		buyerID, sellerID = t.TakerUserID, t.MakerUserID
	} else {
		buyerID, sellerID = t.MakerUserID, t.TakerUserID
	}

	if err := s.updateOne(ctx, buyerID, contractID, t.Symbol, +t.Quantity, t.PriceCents, params); err != nil {
		return err
	}
	if err := s.updateOne(ctx, sellerID, contractID, t.Symbol, -t.Quantity, t.PriceCents, params); err != nil {
		return err
	}
	return nil
}

// updateOne applies a signed fill to one user's position (weighted-average
// entry accounting) inside a transaction, then emits PositionUpdated.
func (s *riskService) updateOne(ctx context.Context, userID string, contractID int64, symbol string, signedQty, priceCents int64, params riskParams) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck

	pos, err := s.loadPosition(ctx, tx, userID, contractID)
	if err != nil {
		return err
	}
	newNet := pos.NetQuantity + signedQty
	newAvg := nextAvgEntry(pos.NetQuantity, pos.AvgEntryPriceCents, signedQty, newNet, priceCents)

	var margin int64
	if contractID != 0 { // futures posts margin; spot posts none
		margin = abs64(newNet) * newAvg * int64(params.InitialMarginPct) / 100
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO positions (user_id, contract_id, symbol, net_quantity, avg_entry_price_cents, margin_posted_cents, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, now())
		ON CONFLICT (user_id, contract_id) DO UPDATE
			SET symbol = EXCLUDED.symbol,
			    net_quantity = EXCLUDED.net_quantity,
			    avg_entry_price_cents = EXCLUDED.avg_entry_price_cents,
			    margin_posted_cents = EXCLUDED.margin_posted_cents,
			    updated_at = now()`,
		userID, contractID, symbol, newNet, newAvg, margin,
	); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}

	s.emitPositionUpdated(userID, contractID, symbol, newNet, newAvg, margin)
	return nil
}

// nextAvgEntry computes the new average entry price after a signed fill.
func nextAvgEntry(oldNet, oldAvg, signedQty, newNet, price int64) int64 {
	switch {
	case oldNet == 0:
		return price
	case (oldNet > 0) == (signedQty > 0): // increasing same direction
		if newNet == 0 {
			return 0
		}
		return (abs64(oldNet)*oldAvg + abs64(signedQty)*price) / abs64(newNet)
	default: // reducing / closing / flipping
		if newNet == 0 {
			return 0
		}
		if (newNet > 0) == (oldNet > 0) { // same side, reduced
			return oldAvg
		}
		return price // flipped to the other side
	}
}

// emitPositionUpdated publishes a PositionUpdated event (best-effort).
func (s *riskService) emitPositionUpdated(userID string, contractID int64, symbol string, netQty, avgEntry, margin int64) {
	if s.pub == nil {
		return
	}
	ev := PositionUpdated{
		EventID:            uuidv4(),
		UserID:             userID,
		ContractID:         contractID,
		Symbol:             symbol,
		NetQuantity:        netQty,
		AvgEntryPriceCents: avgEntry,
		MarginPostedCents:  margin,
		OccurredAtUnixMs:   time.Now().UnixMilli(),
	}
	payload, err := json.Marshal(ev)
	if err != nil {
		log.Printf("risk: marshal PositionUpdated: %v", err)
		return
	}
	if err := s.pub.publish(userID, payload); err != nil {
		log.Printf("risk: publish PositionUpdated for %s: %v", userID, err)
	}
}
