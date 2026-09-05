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
	// buyerFeeBps mirrors settlement's FEE_BUYER_BPS so a spot BUY is only
	// admitted when escrow covers principal + the fee settlement will hold;
	// otherwise the order would fill and then FAIL at settlement.
	buyerFeeBps int64
}

// buyerFee is settlement's fee math (round half-up) for the pre-trade check.
func (s *riskService) buyerFee(notionalCents int64) int64 {
	if notionalCents <= 0 || s.buyerFeeBps <= 0 {
		return 0
	}
	return (notionalCents*s.buyerFeeBps + 5_000) / 10_000
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
// Positions are keyed (user, contract, symbol); an empty symbol matches the
// contract's single row (futures) or the first spot row (legacy callers).
func (s *riskService) loadPosition(ctx context.Context, q sqlQueryer, userID string, contractID int64, symbol string) (Position, error) {
	var p Position
	err := q.QueryRowContext(ctx, `
		SELECT user_id, contract_id, symbol, net_quantity, avg_entry_price_cents, margin_posted_cents
		FROM positions WHERE user_id = $1 AND contract_id = $2 AND ($3 = '' OR symbol = $3)
		ORDER BY symbol LIMIT 1`,
		userID, contractID, symbol,
	).Scan(&p.UserID, &p.ContractID, &p.Symbol, &p.NetQuantity, &p.AvgEntryPriceCents, &p.MarginPostedCents)
	if errors.Is(err, sql.ErrNoRows) {
		return Position{UserID: userID, ContractID: contractID, Symbol: symbol}, nil
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

	pos, err := s.loadPosition(ctx, s.db, in.UserID, contractID, in.Symbol)
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

	// Spot SELL is either primary supply (the user operates an active node
	// for this GPU type) or a resale of allocations they hold unrun
	// (settlement's jobs, status held). Anything else is a naked sell.
	if kind == KindSpot && in.Side == SideSell {
		operator, err := s.isNodeOperator(ctx, in.UserID, in.Symbol)
		if err != nil {
			return MarginResult{}, err
		}
		held, err := s.heldAllocation(ctx, in.UserID, in.Symbol)
		if err != nil {
			return MarginResult{}, err
		}
		if !spotSellAllowed(operator, held, in.Quantity) {
			return MarginResult{
				Allowed:           false,
				ProjectedPosition: projected,
				Reason:            "INSUFFICIENT_HELD_QUANTITY",
			}, nil
		}
	}

	notional := in.PriceCents * in.Quantity
	var required int64
	switch kind {
	case KindFutures:
		required = notional * int64(params.InitialMarginPct) / 100
	default: // SPOT
		if in.Side == SideBuy {
			required = notional + s.buyerFee(notional)
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

// spotSellAllowed: node operators sell freely (primary supply); everyone
// else may sell only hours they hold unrun. Pure for testing.
func spotSellAllowed(operator bool, held, qty int64) bool {
	return operator || held >= qty
}

// heldAllocation is the user's unrun GPU-hours for a symbol (settlement's
// jobs table, shared DB). Missing table => 0.
func (s *riskService) heldAllocation(ctx context.Context, userID, symbol string) (int64, error) {
	var q sql.NullInt64
	err := s.db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(quantity), 0) FROM jobs
		WHERE buyer_id = $1 AND symbol = $2 AND status = 'held'`, userID, symbol).Scan(&q)
	if err != nil {
		if strings.Contains(err.Error(), "does not exist") {
			return 0, nil
		}
		return 0, err
	}
	return q.Int64, nil
}

// GetPosition returns a user's position in a contract.
func (s *riskService) GetPosition(ctx context.Context, userID string, contractID int64) (Position, error) {
	return s.loadPosition(ctx, s.db, userID, contractID, "")
}

// ListPositions returns every non-flat position the user holds.
func (s *riskService) ListPositions(ctx context.Context, userID string) ([]Position, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT user_id, contract_id, symbol, net_quantity, avg_entry_price_cents, margin_posted_cents
		FROM positions WHERE user_id = $1 AND net_quantity <> 0
		ORDER BY symbol`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Position{}
	for rows.Next() {
		var p Position
		if err := rows.Scan(&p.UserID, &p.ContractID, &p.Symbol, &p.NetQuantity, &p.AvgEntryPriceCents, &p.MarginPostedCents); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// isNodeOperator reports whether the user has an active registered node for
// the symbol's GPU type (shared seller_nodes table, written by the
// telemetry-verifier on registration). Missing table => false.
func (s *riskService) isNodeOperator(ctx context.Context, userID, symbol string) (bool, error) {
	gpu := strings.SplitN(symbol, ":", 2)[0]
	var n int
	err := s.db.QueryRowContext(ctx, `
		SELECT count(*) FROM seller_nodes sn
		JOIN gpu_types g ON g.id = sn.gpu_type_id
		WHERE sn.seller_id::text = $1 AND g.name = $2 AND sn.status = 'active'`,
		userID, gpu).Scan(&n)
	if err != nil {
		if strings.Contains(err.Error(), "does not exist") {
			return false, nil
		}
		return false, err
	}
	return n > 0, nil
}

// applyTrade updates both counterparties' positions from a fill in ONE
// transaction, idempotently (risk_applied_trades keyed by trade_id: a Kafka
// redelivery is a no-op), then emits PositionUpdated for each leg. Buyer
// goes long (+qty), seller short (-qty).
func (s *riskService) applyTrade(ctx context.Context, t *TradeExecuted) error {
	contractID, _ := s.resolveContract(ctx, t.Symbol)
	params := s.loadRiskParams(ctx, contractID)

	var buyerID, sellerID string
	if t.AggressorSide == SideBuy {
		buyerID, sellerID = t.TakerUserID, t.MakerUserID
	} else {
		buyerID, sellerID = t.MakerUserID, t.TakerUserID
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck

	res, err := tx.ExecContext(ctx,
		`INSERT INTO risk_applied_trades (trade_id) VALUES ($1::uuid) ON CONFLICT DO NOTHING`, t.TradeID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		log.Printf("risk: trade %s already applied — skipping (redelivery)", t.TradeID)
		return nil
	}

	buyer, err := s.updateOne(ctx, tx, buyerID, contractID, t.Symbol, +t.Quantity, t.PriceCents, params)
	if err != nil {
		return err
	}
	seller, err := s.updateOne(ctx, tx, sellerID, contractID, t.Symbol, -t.Quantity, t.PriceCents, params)
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.emitPositionUpdated(buyerID, contractID, t.Symbol, buyer.NetQuantity, buyer.AvgEntryPriceCents, buyer.MarginPostedCents)
	s.emitPositionUpdated(sellerID, contractID, t.Symbol, seller.NetQuantity, seller.AvgEntryPriceCents, seller.MarginPostedCents)
	return nil
}

// sqlExecQueryer is what updateOne needs from a *sql.Tx.
type sqlExecQueryer interface {
	sqlQueryer
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// updateOne applies a signed fill to one user's position (weighted-average
// entry accounting) within the caller's transaction and returns the new row.
func (s *riskService) updateOne(ctx context.Context, tx sqlExecQueryer, userID string, contractID int64, symbol string, signedQty, priceCents int64, params riskParams) (Position, error) {
	pos, err := s.loadPosition(ctx, tx, userID, contractID, symbol)
	if err != nil {
		return Position{}, err
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
		ON CONFLICT (user_id, contract_id, symbol) DO UPDATE
			SET net_quantity = EXCLUDED.net_quantity,
			    avg_entry_price_cents = EXCLUDED.avg_entry_price_cents,
			    margin_posted_cents = EXCLUDED.margin_posted_cents,
			    updated_at = now()`,
		userID, contractID, symbol, newNet, newAvg, margin,
	); err != nil {
		return Position{}, err
	}
	return Position{UserID: userID, ContractID: contractID, Symbol: symbol, NetQuantity: newNet, AvgEntryPriceCents: newAvg, MarginPostedCents: margin}, nil
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
