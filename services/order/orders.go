// Package main implements the Order Service for the Compute Trading
// Exchange: order entry over REST and gRPC, persisted to Postgres, with
// order lifecycle events published to Kafka via a transactional outbox.
//
// Phase 2 adds FUTURES order entry: an order referencing a futures contract
// (contract_id) is resolved to the contract's book symbol, margin-checked
// against the Risk service (gRPC) before acceptance, and tagged FUTURES.
//
// Config via env:
//   - LISTEN_ADDR        REST listen address (default ":8080")
//   - GRPC_ADDR          gRPC listen address (default ":9090")
//   - DATABASE_URL       Postgres DSN (required)
//   - KAFKA_BROKERS      comma-separated brokers (log-only publisher if unset)
//   - KAFKA_TLS_ENABLED  "true" to dial brokers over TLS/mTLS
//   - RISK_ADDR          Risk service gRPC address; if unset, futures margin
//                        checks are skipped (nop allow-all, dev only)
package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	SideBuy  = "BUY"
	SideSell = "SELL"

	TIFGTC = "GTC"
	TIFIOC = "IOC"
	TIFFOK = "FOK"

	StatusOpen      = "OPEN"
	StatusCancelled = "CANCELLED"

	OrderTypeLimit = "LIMIT"

	KindSpot    = "SPOT"
	KindFutures = "FUTURES"
)

// Order is the orders table row.
type Order struct {
	ID             string    `json:"id"`
	UserID         string    `json:"user_id"`
	Symbol         string    `json:"symbol"`
	GPUType        string    `json:"gpu_type"`
	Region         string    `json:"region"`
	Side           string    `json:"side"`
	PriceCents     int64     `json:"price_cents"`
	Quantity       int64     `json:"quantity"`
	FilledQuantity int64     `json:"filled_quantity"`
	Status         string    `json:"status"`
	TimeInForce    string    `json:"time_in_force"`
	OrderKind      string    `json:"order_kind"`
	ContractID     int64     `json:"contract_id"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// OrderInput is a validated order-placement request (REST or gRPC).
// ContractID > 0 makes this a FUTURES order on that contract; GPUType/Region
// are then resolved from the contract and may be left empty by the caller.
type OrderInput struct {
	UserID      string
	GPUType     string
	Region      string
	Side        string
	PriceCents  int64
	Quantity    int64
	TimeInForce string
	ContractID  int64
}

// marginError is returned when the Risk service rejects an order.
type marginError struct{ reason string }

func (e *marginError) Error() string { return "margin check failed: " + e.reason }

func symbol(gpuType, region string) string { return gpuType + ":" + region }

// validateCore checks the fields common to spot and futures orders.
func validateCore(in *OrderInput) error {
	if strings.TrimSpace(in.UserID) == "" {
		return errors.New("user_id is required")
	}
	switch in.Side {
	case SideBuy, SideSell:
	default:
		return fmt.Errorf("side must be BUY or SELL, got %q", in.Side)
	}
	if in.PriceCents <= 0 {
		return errors.New("price_cents must be greater than 0")
	}
	if in.Quantity <= 0 {
		return errors.New("quantity must be greater than 0")
	}
	switch in.TimeInForce {
	case TIFGTC, TIFIOC, TIFFOK:
	default:
		return fmt.Errorf("time_in_force must be GTC, IOC or FOK, got %q", in.TimeInForce)
	}
	return nil
}

// validateOrder enforces spot order-entry rules (gpu_type/region required).
func validateOrder(in *OrderInput) error {
	if strings.TrimSpace(in.GPUType) == "" {
		return errors.New("gpu_type is required")
	}
	if strings.TrimSpace(in.Region) == "" {
		return errors.New("region is required")
	}
	return validateCore(in)
}

func newUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// OrderPlaced matches libs/schemas/OrderPlaced.avsc (topic: orders).
type OrderPlaced struct {
	EventID          string `json:"event_id"`
	OrderID          string `json:"order_id"`
	UserID           string `json:"user_id"`
	Symbol           string `json:"symbol"`
	GPUType          string `json:"gpu_type"`
	Region           string `json:"region"`
	Side             string `json:"side"`
	OrderType        string `json:"order_type"`
	TimeInForce      string `json:"time_in_force"`
	PriceCents       int64  `json:"price_cents"`
	Quantity         int64  `json:"quantity"`
	OrderKind        string `json:"order_kind"`
	ContractID       int64  `json:"contract_id"`
	OccurredAtUnixMs int64  `json:"occurred_at_unix_ms"`
}

// OrderCancelled matches libs/schemas/OrderCancelled.avsc (topic: orders).
type OrderCancelled struct {
	EventID          string `json:"event_id"`
	OrderID          string `json:"order_id"`
	UserID           string `json:"user_id"`
	Symbol           string `json:"symbol"`
	OccurredAtUnixMs int64  `json:"occurred_at_unix_ms"`
}

const ordersTopic = "orders"

type Publisher interface {
	Publish(ctx context.Context, key string, event any) error
}

var errNotFound = errors.New("order not found")

// orderService holds the shared core used by the REST and gRPC fronts.
type orderService struct {
	db   *sql.DB
	pub  Publisher
	risk RiskChecker // pre-trade margin check for futures (nop if RISK_ADDR unset)
}

// placeOrder validates, (for futures) resolves the contract and runs a Risk
// margin check, then persists (status OPEN) and enqueues an OrderPlaced event
// in the outbox — persistence + outbox in one DB transaction.
func (s *orderService) placeOrder(ctx context.Context, in *OrderInput) (*Order, error) {
	kind := KindSpot
	var sym, gpuType, region string

	if in.ContractID > 0 {
		kind = KindFutures
		var status string
		err := s.db.QueryRowContext(ctx, `
			SELECT fc.symbol, g.name, fc.region_code, fc.status
			FROM futures_contracts fc
			JOIN gpu_types g ON g.id = fc.gpu_type_id
			WHERE fc.id = $1`, in.ContractID,
		).Scan(&sym, &gpuType, &region, &status)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("unknown contract_id %d", in.ContractID)
		}
		if err != nil {
			return nil, fmt.Errorf("resolve contract: %w", err)
		}
		if status != "LISTED" {
			return nil, fmt.Errorf("contract %d is not tradeable (status=%s)", in.ContractID, status)
		}
		if err := validateCore(in); err != nil {
			return nil, err
		}
		// Pre-trade margin check (Risk service).
		allowed, reason, err := s.risk.CheckMargin(ctx, MarginReq{
			UserID:     strings.TrimSpace(in.UserID),
			ContractID: in.ContractID,
			Symbol:     sym,
			Kind:       KindFutures,
			Side:       in.Side,
			PriceCents: in.PriceCents,
			Quantity:   in.Quantity,
		})
		if err != nil {
			return nil, fmt.Errorf("risk check: %w", err)
		}
		if !allowed {
			return nil, &marginError{reason: reason}
		}
	} else {
		if err := validateOrder(in); err != nil {
			return nil, err
		}
		gpuType = strings.TrimSpace(in.GPUType)
		region = strings.TrimSpace(in.Region)
		sym = symbol(gpuType, region)
		// Spot pre-trade check (Risk): buys need escrow for the notional;
		// sells must be backed by an active node (primary supply) or by
		// held hours (resale) — no naked capacity sells.
		allowed, reason, err := s.risk.CheckMargin(ctx, MarginReq{
			UserID:     strings.TrimSpace(in.UserID),
			Symbol:     sym,
			Kind:       KindSpot,
			Side:       in.Side,
			PriceCents: in.PriceCents,
			Quantity:   in.Quantity,
		})
		if err != nil {
			return nil, fmt.Errorf("risk check: %w", err)
		}
		if !allowed {
			return nil, &marginError{reason: reason}
		}
	}

	o := &Order{
		ID:          newUUID(),
		UserID:      strings.TrimSpace(in.UserID),
		Symbol:      sym,
		GPUType:     gpuType,
		Region:      region,
		Side:        in.Side,
		PriceCents:  in.PriceCents,
		Quantity:    in.Quantity,
		Status:      StatusOpen,
		TimeInForce: in.TimeInForce,
		OrderKind:   kind,
		ContractID:  in.ContractID,
	}
	ev := OrderPlaced{
		EventID:          newUUID(),
		OrderID:          o.ID,
		UserID:           o.UserID,
		Symbol:           o.Symbol,
		GPUType:          o.GPUType,
		Region:           o.Region,
		Side:             o.Side,
		OrderType:        OrderTypeLimit,
		TimeInForce:      o.TimeInForce,
		PriceCents:       o.PriceCents,
		Quantity:         o.Quantity,
		OrderKind:        o.OrderKind,
		ContractID:       o.ContractID,
		OccurredAtUnixMs: time.Now().UnixMilli(),
	}
	evPayload, _ := json.Marshal(ev)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	err = tx.QueryRowContext(ctx, `
		INSERT INTO orders (id, user_id, symbol, gpu_type, region, side,
		                    price_cents, quantity, time_in_force, order_kind, contract_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		RETURNING filled_quantity, status, created_at, updated_at`,
		o.ID, o.UserID, o.Symbol, o.GPUType, o.Region, o.Side,
		o.PriceCents, o.Quantity, o.TimeInForce, o.OrderKind, o.ContractID,
	).Scan(&o.FilledQuantity, &o.Status, &o.CreatedAt, &o.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("insert order: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO outbox_events (id, aggregate_type, aggregate_id, event_type, payload)
		VALUES ($1, 'order', $2, 'OrderPlaced', $3)`,
		ev.EventID, o.ID, evPayload,
	); err != nil {
		return nil, fmt.Errorf("insert outbox: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return o, nil
}

// cancelOrder marks an open order CANCELLED and enqueues an OrderCancelled
// event in the outbox — all within a single database transaction.
func (s *orderService) cancelOrder(ctx context.Context, userID, orderID string) (*Order, error) {
	if strings.TrimSpace(orderID) == "" {
		return nil, errors.New("order_id is required")
	}
	var o Order
	ev := OrderCancelled{EventID: newUUID()}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	err = tx.QueryRowContext(ctx, `
		UPDATE orders SET status = 'CANCELLED', updated_at = now()
		WHERE id = $1 AND user_id = $2 AND status IN ('OPEN', 'PARTIALLY_FILLED')
		RETURNING id, user_id, symbol, gpu_type, region, side, price_cents,
		          quantity, filled_quantity, status, time_in_force, order_kind, contract_id, created_at, updated_at`,
		orderID, userID,
	).Scan(&o.ID, &o.UserID, &o.Symbol, &o.GPUType, &o.Region, &o.Side,
		&o.PriceCents, &o.Quantity, &o.FilledQuantity, &o.Status,
		&o.TimeInForce, &o.OrderKind, &o.ContractID, &o.CreatedAt, &o.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("cancel order: %w", err)
	}

	ev.OrderID = o.ID
	ev.UserID = o.UserID
	ev.Symbol = o.Symbol
	ev.OccurredAtUnixMs = time.Now().UnixMilli()
	evPayload, _ := json.Marshal(ev)

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO outbox_events (id, aggregate_type, aggregate_id, event_type, payload)
		VALUES ($1, 'order', $2, 'OrderCancelled', $3)`,
		ev.EventID, o.ID, evPayload,
	); err != nil {
		return nil, fmt.Errorf("insert outbox: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return &o, nil
}

// getOrder fetches one order by id.
func (s *orderService) getOrder(ctx context.Context, orderID string) (*Order, error) {
	var o Order
	err := s.db.QueryRowContext(ctx, `
		SELECT id, user_id, symbol, gpu_type, region, side, price_cents,
		       quantity, filled_quantity, status, time_in_force, order_kind, contract_id, created_at, updated_at
		FROM orders WHERE id = $1`, orderID,
	).Scan(&o.ID, &o.UserID, &o.Symbol, &o.GPUType, &o.Region, &o.Side,
		&o.PriceCents, &o.Quantity, &o.FilledQuantity, &o.Status,
		&o.TimeInForce, &o.OrderKind, &o.ContractID, &o.CreatedAt, &o.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errNotFound
	}
	if err != nil {
		return nil, err
	}
	return &o, nil
}

// listOrders returns a user's orders, newest first.
func (s *orderService) listOrders(ctx context.Context, userID string) ([]Order, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, user_id, symbol, gpu_type, region, side, price_cents,
		       quantity, filled_quantity, status, time_in_force, order_kind, contract_id, created_at, updated_at
		FROM orders WHERE user_id = $1 ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Order{}
	for rows.Next() {
		var o Order
		if err := rows.Scan(&o.ID, &o.UserID, &o.Symbol, &o.GPUType, &o.Region,
			&o.Side, &o.PriceCents, &o.Quantity, &o.FilledQuantity, &o.Status,
			&o.TimeInForce, &o.OrderKind, &o.ContractID, &o.CreatedAt, &o.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// marshalEvent is a small helper so publishers share JSON encoding.
func marshalEvent(event any) ([]byte, error) { return json.Marshal(event) }
