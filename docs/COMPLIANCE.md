# Compliance — market surveillance

`services/compliance` (Go, `cte-compliance` on Render) consumes `trades` and
`order-updates` and writes `surveillance_alerts` (migration
`compliance/0001`, applied by `scripts/migrate`). Each alert is also emitted
as `AlertTriggered` on `compliance-alerts` (`libs/schemas/AlertTriggered.avsc`;
create the topic on Redpanda like the others — publishing is best-effort and
the DB row is the source of truth).

## Rules (`rules.go`, unit-tested)

| Rule | Trigger | Severity |
|---|---|---|
| `WASH_TRADE` | a fill where `maker_user_id == taker_user_id` | high |
| `SPOOFING` | per (account, market) sliding window (default 10 min): ≥ 5 user-initiated cancels and cancels ⁄ (cancels + fills) ≥ 0.8. IOC/FOK remainders and expiry are engine cancels and do not count. One alert per window (cooldown). | medium |

Tuning: `SPOOF_WINDOW_SECONDS`, `SPOOF_MIN_CANCELS`, `SPOOF_CANCEL_RATIO`.
Spoofing state is in-memory and bounded by the window; it rebuilds from the
live stream after a restart.

## Review

Gateway (admin role only):

- `GET  /v1/admin/alerts?status=open&limit=100`
- `POST /v1/admin/alerts/{id}` `{"status":"reviewed"|"dismissed"}`

Portfolio shows the open queue to admins (Surveillance alerts card).

Note: the founder account trading with itself during the closed beta will
raise `WASH_TRADE` on every self-fill — that is the rule working, not a bug.
Dismiss them from the queue.
