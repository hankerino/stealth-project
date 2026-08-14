package main

// Transactional Outbox dispatcher.
//
// The order service writes to `orders` and `outbox_events` in the same DB
// transaction (see orders.go). This background worker polls the outbox every
// 500ms, publishes unpublished events to the MSK `orders` topic, and marks
// them published.
//
// Delivery semantics: at-least-once. The dispatcher publishes before marking
// published_at; if it crashes after the Kafka write but before the UPDATE,
// the event is re-published on restart. Consumers (matching-engine) must be
// idempotent — they already are: duplicate OrderPlaced with the same
// order_id is rejected (DUPLICATE_ORDER_ID); duplicate OrderCancelled on an
// already-cancelled order is a no-op.

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"database/sql"

	"github.com/segmentio/kafka-go"
)

const (
	outboxPollInterval = 500 * time.Millisecond
	outboxBatchSize    = 100
)

// outboxEvent is one row in outbox_events.
type outboxEvent struct {
	ID            string          `json:"id"`
	AggregateType string          `json:"aggregate_type"`
	AggregateID   string          `json:"aggregate_id"`
	EventType     string          `json:"event_type"`
	Payload       json.RawMessage `json:"payload"`
	CreatedAt     time.Time       `json:"created_at"`
}

// outboxDispatcher polls outbox_events and publishes to Kafka.
type outboxDispatcher struct {
	db  *sql.DB
	pub *kafkaPublisher // nil if Kafka is unavailable (nop mode)
}

func newOutboxDispatcher(db *sql.DB, pub *kafkaPublisher) *outboxDispatcher {
	return &outboxDispatcher{db: db, pub: pub}
}

// Run blocks until ctx is cancelled, polling every 500ms.
func (d *outboxDispatcher) Run(ctx context.Context) {
	if d.pub == nil {
		log.Println("outbox: Kafka publisher is nil (nop mode); dispatcher sleeping")
		<-ctx.Done()
		return
	}

	ticker := time.NewTicker(outboxPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("outbox: dispatcher shutting down")
			return
		case <-ticker.C:
			if err := d.dispatchBatch(ctx); err != nil {
				log.Printf("outbox: dispatch error: %v", err)
			}
		}
	}
}

// dispatchBatch reads up to outboxBatchSize unpublished events, publishes each,
// and marks it published. A single failure in the batch logs and continues;
// the next tick retries any still-unpublished rows.
func (d *outboxDispatcher) dispatchBatch(ctx context.Context) error {
	rows, err := d.db.QueryContext(ctx, `
		SELECT id, aggregate_type, aggregate_id, event_type, payload, created_at
		FROM outbox_events
		WHERE published_at IS NULL
		ORDER BY created_at
		LIMIT $1`, outboxBatchSize)
	if err != nil {
		return err
	}

	var events []outboxEvent
	for rows.Next() {
		var e outboxEvent
		if err := rows.Scan(&e.ID, &e.AggregateType, &e.AggregateID, &e.EventType, &e.Payload, &e.CreatedAt); err != nil {
			rows.Close()
			return err
		}
		events = append(events, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	for _, e := range events {
		// The Kafka key is the symbol, which is the aggregate_id for orders.
		// But the payload already contains the symbol field — extract it.
		key := extractSymbol(e.Payload)
		if key == "" {
			key = e.AggregateID
		}

		if err := d.pub.w.WriteMessages(ctx, kafka.Message{
			Key:   []byte(key),
			Value: e.Payload,
		}); err != nil {
			log.Printf("outbox: publish failed for event %s: %v", e.ID, err)
			continue // leave unpublished; retry next tick
		}

		if _, err := d.db.ExecContext(ctx, `
			UPDATE outbox_events SET published_at = now() WHERE id = $1`, e.ID); err != nil {
			log.Printf("outbox: mark published failed for event %s: %v", e.ID, err)
			// The event WAS published; marking failed. It will be re-published
			// next tick (at-least-once). Log and continue.
		}
	}

	if len(events) > 0 {
		log.Printf("outbox: dispatched %d event(s)", len(events))
	}
	return nil
}

// extractSymbol pulls the "symbol" field from the JSON payload to use as the
// Kafka key (matching the existing partitioning scheme: key=symbol).
func extractSymbol(payload json.RawMessage) string {
	var m struct {
		Symbol string `json:"symbol"`
	}
	if err := json.Unmarshal(payload, &m); err != nil {
		return ""
	}
	return m.Symbol
}
