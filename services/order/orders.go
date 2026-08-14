// Package main implements the Order Service for the Compute Trading
// Exchange (Phase 1A): order entry over REST and gRPC, persisted to Aurora
// Postgres, with order lifecycle events published to MSK Kafka.
//
// Config via env:
//   - LISTEN_ADDR        REST listen address (default ":8080")
//   - GRPC_ADDR          gRPC listen address (default ":9090")
//   - DATABASE_URL       Aurora Postgres DSN (required)
//   - KAFKA_BROKERS      comma-separated MSK bootstrap brokers. If unset the
//     service runs with a log-only publisher (local dev without Kafka).
//   - KAFKA_TLS_ENABLED  "true" to dial brokers over TLS (MSK).
//
// Kafka payloads are plain JSON documents whose field names exactly match
// the Avro schemas in libs/schemas/*.avsc (no schema registry in dev).
// NOTE: MSK TLS client-certificate provisioning (mutual TLS) is a
// follow-up; for now we use the MSK TLS listener without client certs.
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

// OrderSide / TimeInForce / OrderStatus string values mirror the enum
// symbols in libs/schemas/*.avsc and the proto enums in
// libs/proto/order/v1/order.proto.
const (
	SideBuy  = "BUY"
	SideSell = "SELL"

	TIFGTC = "GTC"
	TIFIOC = "IOC"
	TIFFOK = "FOK"

	StatusOpen      = "OPEN"
	StatusCancelled = "CANCELLED"

	OrderTypeLimit = "LIMIT"
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
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// OrderInput is a validated order-placement request (REST or gRPC).
type OrderInput struct {
	UserID      string
	GPUType     string
	Region      string
	Side        string
	PriceCents  int64
	Quantity    int64
	TimeInForce string
}

// symbol derives the Kafka key / book symbol: <GPU_TYPE>:<REGION>.
func symbol(gpuType, region string) string {
	return gpuType + ":" + region
}

// validateOrder enforces the Phase 1A order-entry rules.
func validateOrder(in *OrderInput) error {
	if strings.TrimSpace(in.UserID) == "" {
		return errors.New("user_id is required")
	}
	if strings.TrimSpace(in.GPUType) == "" {
		return errors.New("gpu_type is required")
	}
	if strings.TrimSpace(in.Region) == "" {
		return errors.New("region is required")
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

// newUUID returns a random UUIDv4 string (avoids a third-party dependency).
func newUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant RFC 4122
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// --- Kafka event payloads. JSON field names are the contract: they must
// match libs/schemas/OrderPlaced.avsc and OrderCancelled.avsc exactly. ---

// OrderPlaced matches libs/schemas/OrderPlaced.avsc (topic: orders).
type OrderPlaced struct {
	EventID          string `json:"event_id"` // UUIDv4, idempotency key
	OrderID          string `json:"order_id"`
	UserID           string `json:"user_id"`
	Symbol           string `json:"symbol"`
	GPUType          string `json:"gpu_type"`
	Region           string `json:"region"`
	Side             string `json:"side"`      // BUY | SELL
	OrderType        string `json:"order_type"` // always LIMIT in Phase 1A
	TimeInForce      string `json:"time_in_force"`
	PriceCents       int64  `json:"price_cents"`
	Quantity         int64  `json:"quantity"`
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

// ordersTopic is the single topic for order-entry events in Phase 1A.
const ordersTopic = "orders"

// Publisher publishes a JSON event to the orders topic keyed by symbol.
type Publisher interface {
	Publish(ctx context.Context, key string, event any) error
}

var errNotFound = errors.New("order not found")

// orderService holds the shared core used by the REST and gRPC fronts.
type orderService struct {
	db  *sql.DB
	pub Publisher
}

// placeOrder validates, persists (status OPEN), and enqueues an OrderPlaced
// event in the outbox — all within a single database transaction. The outbox
// dispatcher (outbox.go) publishes the event to Kafka asynchronously.
func (s *orderService) placeOrder(ctx context.Context, in *OrderInput) (*Order, error) {
	if err := validateOrder(in); err != nil {
		return nil, err
	}
	o := &Order{
		ID:          newUUID(),
		UserID:      strings.TrimSpace(in.UserID),
		Symbol:      symbol(strings.TrimSpace(in.GPUType), strings.TrimSpace(in.Region)),
		GPUType:     strings.TrimSpace(in.GPUType),
		Region:      strings.TrimSpace(in.Region),
		Side:        in.Side,
		PriceCents:  in.PriceCents,
		Quantity:    in.Quantity,
		Status:      StatusOpen,
		TimeInForce: in.TimeInForce,
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
		OccurredAtUnixMs: time.Now().UnixMilli(),
	}
	evPayload, _ := json.Marshal(ev)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback() // safe to call after commit

	err = tx.QueryRowContext(ctx, `
		INSERT INTO orders (id, user_id, symbol, gpu_type, region, side,
		                    price_cents, quantity, time_in_force)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING filled_quantity, status, created_at, updated_at`,
		o.ID, o.UserID, o.Symbol, o.GPUType, o.Region, o.Side,
		o.PriceCents, o.Quantity, o.TimeInForce,
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
	ev := OrderCancelled{
		EventID: newUUID(),
	}
	evPayload, _ := json.Marshal(ev)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	err = tx.QueryRowContext(ctx, `
		UPDATE orders SET status = 'CANCELLED', updated_at = now()
		WHERE id = $1 AND user_id = $2 AND status IN ('OPEN', 'PARTIALLY_FILLED')
		RETURNING id, user_id, symbol, gpu_type, region, side, price_cents,
		          quantity, filled_quantity, status, time_in_force, created_at, updated_at`,
		orderID, userID,
	).Scan(&o.ID, &o.UserID, &o.Symbol, &o.GPUType, &o.Region, &o.Side,
		&o.PriceCents, &o.Quantity, &o.FilledQuantity, &o.Status,
		&o.TimeInForce, &o.CreatedAt, &o.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("cancel order: %w", err)
	}

	// Fill in the remaining event fields now that we have the order.
	ev.OrderID = o.ID
	ev.UserID = o.UserID
	ev.Symbol = o.Symbol
	ev.OccurredAtUnixMs = time.Now().UnixMilli()
	evPayload, _ = json.Marshal(ev)

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
		       quantity, filled_quantity, status, time_in_force, created_at, updated_at
		FROM orders WHERE id = $1`, orderID,
	).Scan(&o.ID, &o.UserID, &o.Symbol, &o.GPUType, &o.Region, &o.Side,
		&o.PriceCents, &o.Quantity, &o.FilledQuantity, &o.Status,
		&o.TimeInForce, &o.CreatedAt, &o.UpdatedAt)
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
		       quantity, filled_quantity, status, time_in_force, created_at, updated_at
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
			&o.TimeInForce, &o.CreatedAt, &o.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// marshalEvent is a small helper so publishers share JSON encoding.
func marshalEvent(event any) ([]byte, error) { return json.Marshal(event) }
