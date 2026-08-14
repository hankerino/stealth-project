-- Transactional outbox for atomic order + event persistence.
-- The outbox dispatcher (outbox.go) polls this table every 500ms, publishes
-- unpublished rows to the MSK `orders` topic, and stamps published_at.
CREATE TABLE IF NOT EXISTS outbox_events (
    id             UUID         PRIMARY KEY,
    aggregate_type VARCHAR(64)  NOT NULL,          -- e.g. 'order'
    aggregate_id   UUID         NOT NULL,          -- the orders.id
    event_type     VARCHAR(64)  NOT NULL,          -- 'OrderPlaced' | 'OrderCancelled'
    payload        JSONB        NOT NULL,          -- the Avro-conformant JSON event
    created_at     TIMESTAMPTZ  NOT NULL DEFAULT now(),
    published_at   TIMESTAMPTZ
);

-- The dispatcher claims a batch with: ORDER BY created_at WHERE published_at IS NULL.
-- This index makes that scan fast as the table grows.
CREATE INDEX IF NOT EXISTS outbox_events_unpublished_idx
    ON outbox_events (created_at)
    WHERE published_at IS NULL;

-- Optional: prune old published events (run via a cron / scheduled job).
-- DELETE FROM outbox_events WHERE published_at < now() - interval '7 days';
