package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/segmentio/kafka-go"
)

// The matching engine is the source of truth for an order's lifecycle once it
// has been placed. It emits OrderUpdated (libs/schemas/OrderUpdated.avsc) on
// the order-updates topic for every fill, cancel-ack, reject and expiry. This
// file projects those events back onto the orders table so that reads
// (REST/gRPC, the web UI) reflect fills instead of staying OPEN forever.

const (
	orderUpdatesTopic   = "order-updates"
	orderUpdatesGroup   = "order-service"
	maxUpdateBackoff    = 30 * time.Second
	updateFetchInterval = 30 * time.Second
)

// OrderUpdated mirrors the engine's payload (field names match the Avro schema).
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

// engineStatuses is the set the engine may report; it matches the orders.status
// CHECK constraint, so anything else is a poison message, not a DB error.
var engineStatuses = map[string]bool{
	"OPEN": true, "PARTIALLY_FILLED": true, "FILLED": true,
	"CANCELLED": true, "REJECTED": true, "EXPIRED": true,
}

var terminalStatuses = map[string]bool{
	"FILLED": true, "CANCELLED": true, "REJECTED": true, "EXPIRED": true,
}

// validateOrderUpdate rejects payloads that can never be applied. Returning an
// error here means "skip and commit" (poison), never "retry".
func validateOrderUpdate(u *OrderUpdated) error {
	if strings.TrimSpace(u.OrderID) == "" {
		return errors.New("order_id is required")
	}
	if !engineStatuses[u.NewStatus] {
		return fmt.Errorf("unknown new_status %q", u.NewStatus)
	}
	if u.FilledQuantity < 0 {
		return fmt.Errorf("negative filled_quantity %d", u.FilledQuantity)
	}
	return nil
}

// applyOrderUpdate projects one engine update onto the orders row.
//
// Idempotent and order-tolerant: Kafka guarantees per-key (symbol) ordering
// but redelivery is at-least-once, so a stale/duplicate update must not
// regress the row. Two guards make the UPDATE monotonic:
//   - filled_quantity never decreases;
//   - a terminal status (FILLED/CANCELLED/REJECTED/EXPIRED) is never replaced
//     by a non-terminal one (OPEN/PARTIALLY_FILLED).
//
// Returns (applied, err). applied=false with err=nil means the row was absent
// or the update was stale — both are fine to commit. err != nil is transient
// (DB unreachable) and the caller retries without committing.
func applyOrderUpdate(ctx context.Context, db *sql.DB, u *OrderUpdated) (bool, error) {
	res, err := db.ExecContext(ctx, `
		UPDATE orders
		   SET status = $2, filled_quantity = $3, updated_at = now()
		 WHERE id = $1
		   AND filled_quantity <= $3
		   AND NOT (status IN ('FILLED','CANCELLED','REJECTED','EXPIRED')
		            AND $2 IN ('OPEN','PARTIALLY_FILLED'))`,
		u.OrderID, u.NewStatus, u.FilledQuantity)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

// orderUpdatesConsumer reads OrderUpdated events and applies them to the DB.
type orderUpdatesConsumer struct {
	r  *kafka.Reader
	db *sql.DB
}

func newOrderUpdatesConsumer(db *sql.DB, brokers string, tlsEnabled bool) *orderUpdatesConsumer {
	// Timeout bounds each dial. Never set Dialer.Deadline (absolute instant —
	// it wedged the settlement consumer, see services/settlement/kafka.go).
	dialer := &kafka.Dialer{Timeout: 10 * time.Second}
	if tlsEnabled {
		dialer.TLS = buildTLSConfig()
	}
	r := kafka.NewReader(kafka.ReaderConfig{
		Brokers:     strings.Split(brokers, ","),
		Topic:       orderUpdatesTopic,
		GroupID:     orderUpdatesGroup,
		StartOffset: kafka.FirstOffset,
		MinBytes:    1,
		MaxBytes:    10e6,
		Dialer:      dialer,
		ErrorLogger: kafka.LoggerFunc(func(msg string, args ...interface{}) {
			log.Printf("order-updates kafka: "+msg, args...)
		}),
		// CommitInterval 0: commit synchronously after the row is updated.
	})
	return &orderUpdatesConsumer{r: r, db: db}
}

// Run consumes until ctx is cancelled. At-least-once: the offset is committed
// only after applyOrderUpdate succeeds (or the message is deemed poison).
func (c *orderUpdatesConsumer) Run(ctx context.Context) {
	defer c.r.Close()
	log.Printf("order-updates consumer starting (group %q)", orderUpdatesGroup)
	for {
		fetchCtx, cancel := context.WithTimeout(ctx, updateFetchInterval)
		msg, err := c.r.FetchMessage(fetchCtx)
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				log.Print("order-updates consumer stopped")
				return
			}
			if errors.Is(err, context.DeadlineExceeded) {
				continue
			}
			log.Printf("order-updates: fetch: %v (retrying)", err)
			time.Sleep(time.Second)
			continue
		}

		var u OrderUpdated
		if err := json.Unmarshal(msg.Value, &u); err != nil {
			log.Printf("order-updates: poison message at offset %d: %v — skipping", msg.Offset, err)
			c.commit(ctx, msg)
			continue
		}
		if err := validateOrderUpdate(&u); err != nil {
			log.Printf("order-updates: invalid event %s at offset %d: %v — skipping", u.EventID, msg.Offset, err)
			c.commit(ctx, msg)
			continue
		}

		backoff := 500 * time.Millisecond
		for {
			applied, err := applyOrderUpdate(ctx, c.db, &u)
			if err == nil {
				if !applied {
					log.Printf("order-updates: order %s -> %s (filled %d) ignored: unknown or stale",
						u.OrderID, u.NewStatus, u.FilledQuantity)
				}
				break
			}
			if ctx.Err() != nil {
				return
			}
			log.Printf("order-updates: order %s transient error: %v — retrying in %s (offset %d not committed)",
				u.OrderID, err, backoff, msg.Offset)
			time.Sleep(backoff)
			if backoff < maxUpdateBackoff {
				backoff *= 2
			}
		}
		c.commit(ctx, msg)
	}
}

func (c *orderUpdatesConsumer) commit(ctx context.Context, msg kafka.Message) {
	if err := c.r.CommitMessages(ctx, msg); err != nil && ctx.Err() == nil {
		log.Printf("order-updates: commit offset %d: %v", msg.Offset, err)
	}
}
