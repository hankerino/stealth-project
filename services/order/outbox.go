package main

// Transactional Outbox dispatcher.
//
// The order service writes to `orders` and `outbox_events` in the same DB
// transaction (see orders.go). This background worker polls the outbox every
// 500ms, publishes unpublished events to the MSK `orders` topic, and marks
// them published.
//
// Concurrency: the order service runs multiple replicas (Deployment
// replicas: 2, HPA up to 6), so every replica runs a dispatcher. Each poll
// claims its batch with SELECT ... FOR UPDATE SKIP LOCKED inside a
// transaction, so replicas take DISJOINT rows and never publish the same
// event twice in the happy path. The rows stay locked until the tx commits
// (after publish + marking published_at), so no other replica can grab them.
//
// Delivery semantics: at-least-once. Publish happens before the tx commits;
// if the process crashes after the Kafka write but before COMMIT, the tx
// rolls back, published_at is not set, and the event is re-published on the
// next poll (by this or another replica). Consumers (matching-engine) are
// idempotent — duplicate OrderPlaced with the same order_id is rejected
// (DUPLICATE_ORDER_ID); duplicate OrderCancelled on an already-cancelled
// order is a no-op.

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

// dispatchBatch claims up to outboxBatchSize unpublished events with
// FOR UPDATE SKIP LOCKED (so concurrent dispatchers take disjoint rows),
// publishes each to Kafka, and marks it published — all inside one
// transaction. On the first publish failure the batch stops and commits the
// rows already published; the still-locked-then-rolled-back rows are retried
// next tick. A publish that succeeds but whose COMMIT later fails is
// re-published next tick (at-least-once).
func (d *outboxDispatcher) dispatchBatch(ctx context.Context) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() // no-op after a successful Commit

	rows, err := tx.QueryContext(ctx, `
		SELECT id, aggregate_type, aggregate_id, event_type, payload, created_at
		FROM outbox_events
		WHERE published_at IS NULL
		ORDER BY created_at
		LIMIT $1
		FOR UPDATE SKIP LOCKED`, outboxBatchSize)
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

	if len(events) == 0 {
		return tx.Commit() // release the (empty) transaction promptly
	}

	published := 0
	for _, e := range events {
		// The Kafka key is the symbol so all events for one book land on the
		// same partition (in-order). The payload already carries "symbol".
		key := extractSymbol(e.Payload)
		if key == "" {
			key = e.AggregateID
		}

		if err := d.pub.w.WriteMessages(ctx, kafka.Message{
			Key:   []byte(key),
			Value: e.Payload,
		}); err != nil {
			log.Printf("outbox: publish failed for event %s: %v (committing %d already-published; retrying rest next tick)", e.ID, err, published)
			break // commit what we've published; the rest stay unpublished
		}

		if _, err := tx.ExecContext(ctx, `
			UPDATE outbox_events SET published_at = now() WHERE id = $1`, e.ID); err != nil {
			// The event WAS published but marking failed inside the tx; abort
			// the whole tx so we don't commit a partial/inconsistent batch.
			// Everything rolls back and is re-published next tick.
			return err
		}
		published++
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	if published > 0 {
		log.Printf("outbox: dispatched %d event(s)", published)
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
