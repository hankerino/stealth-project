-- Workload jobs (Phase 4): the execution half of a trade. Settlement creates
-- one job per TradeExecuted (escrow is held in PENDING), the assigned node
-- executes it, and on COMPLETED the escrow is released to the seller
-- (trade_ledger -> SETTLED); on FAILED the buyer is refunded
-- (trade_ledger -> FAILED + SettlementFailed event).
CREATE TABLE IF NOT EXISTS jobs (
    job_id        UUID PRIMARY KEY,
    trade_id      UUID NOT NULL REFERENCES trade_ledger(trade_id),
    node_id       UUID,                       -- seller_nodes.node_id (the executor)
    symbol        TEXT NOT NULL,              -- <GPU_TYPE>:<REGION>
    buyer_id      TEXT NOT NULL,
    seller_id     TEXT NOT NULL,
    workload      JSONB NOT NULL DEFAULT '{}', -- spec: image/command/mock duration (draft: scheduler fills a default)
    status        TEXT NOT NULL DEFAULT 'queued'
                  CHECK (status IN ('queued', 'running', 'completed', 'failed')),
    status_reason TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Agent poll: queued jobs for one node.
CREATE INDEX IF NOT EXISTS jobs_node_status_idx ON jobs (node_id, status);
CREATE INDEX IF NOT EXISTS jobs_trade_idx ON jobs (trade_id);
