package main

// Daily Mark-to-Market (MTM) settlement for open futures positions (Phase 2).
//
// Once per day (00:00 UTC by default) the worker:
//   1. Finds each futures contract with open positions (risk-owned `positions`
//      table, shared DB).
//   2. Computes the settlement price = VWAP of the last 10 minutes of settled
//      trades for that symbol (from trade_ledger). This is the settlement mark;
//      the market-data service is the alternative source (MARKET_DATA source is
//      pluggable — see priceSource).
//   3. For each open position, computes daily PnL against the prior mark
//      (previous day's settlement price, or the entry price on the first MTM),
//      transfers PnL to/from the user's escrow, records an mtm_settlements row,
//      and emits an MTMSettled event.
//
// The daily run is idempotent: mtm_settlements has a UNIQUE
// (settlement_date, contract_id, user_id) key, so re-running a day is a no-op.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"time"
)

const mtmTopic = "settlement-events"

// MTMSettled matches libs/schemas/MTMSettled.avsc (topic: settlement-events).
type MTMSettled struct {
	EventID              string `json:"event_id"`
	SettlementDate       string `json:"settlement_date"`
	ContractID           int64  `json:"contract_id"`
	Symbol               string `json:"symbol"`
	UserID               string `json:"user_id"`
	NetQuantity          int64  `json:"net_quantity"`
	PriorMarkCents       int64  `json:"prior_mark_cents"`
	SettlementPriceCents int64  `json:"settlement_price_cents"`
	PnLCents             int64  `json:"pnl_cents"`
	OccurredAtUnixMs     int64  `json:"occurred_at_unix_ms"`
}

// openPosition is a risk `positions` row for an open futures position.
type openPosition struct {
	ContractID  int64
	Symbol      string
	UserID      string
	NetQuantity int64
	AvgEntry    int64
}

// computePnL is the signed daily PnL for a position marked from prior to
// settlement price: long (net>0) gains when price rises, short loses.
func computePnL(netQuantity, settlementPrice, priorMark int64) int64 {
	return netQuantity * (settlementPrice - priorMark)
}

// mtmRunner owns the daily MTM job.
type mtmRunner struct {
	db  *sql.DB
	pub *kafkaProducer // MTMSettled -> settlement-events; nil in dev
}

// settlementPriceVWAP returns the VWAP of the last 10 minutes of settled trades
// for a symbol, and whether any trades were found in the window.
func (m *mtmRunner) settlementPriceVWAP(ctx context.Context, symbol string) (int64, bool, error) {
	var vwap sql.NullInt64
	err := m.db.QueryRowContext(ctx, `
		SELECT (SUM(price_cents * quantity) / NULLIF(SUM(quantity), 0))::bigint
		FROM trade_ledger
		WHERE symbol = $1 AND status = 'SETTLED'
		  AND created_at >= now() - interval '10 minutes'`, symbol,
	).Scan(&vwap)
	if err != nil {
		return 0, false, err
	}
	if !vwap.Valid {
		return 0, false, nil
	}
	return vwap.Int64, true, nil
}

// priorMark returns the previous settlement price for (contract,user) before
// settlementDate, or the position's entry price if this is the first MTM.
func (m *mtmRunner) priorMark(ctx context.Context, contractID int64, userID string, settlementDate string, entry int64) (int64, error) {
	var prev sql.NullInt64
	err := m.db.QueryRowContext(ctx, `
		SELECT settlement_price_cents FROM mtm_settlements
		WHERE contract_id = $1 AND user_id = $2 AND settlement_date < $3::date
		ORDER BY settlement_date DESC LIMIT 1`, contractID, userID, settlementDate,
	).Scan(&prev)
	if errors.Is(err, sql.ErrNoRows) || !prev.Valid {
		return entry, nil
	}
	if err != nil {
		return 0, err
	}
	return prev.Int64, nil
}

// openFuturesPositions lists open (non-zero) futures positions.
func (m *mtmRunner) openFuturesPositions(ctx context.Context) ([]openPosition, error) {
	rows, err := m.db.QueryContext(ctx, `
		SELECT contract_id, symbol, user_id, net_quantity, avg_entry_price_cents
		FROM positions
		WHERE contract_id <> 0 AND net_quantity <> 0
		ORDER BY contract_id, user_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []openPosition
	for rows.Next() {
		var p openPosition
		if err := rows.Scan(&p.ContractID, &p.Symbol, &p.UserID, &p.NetQuantity, &p.AvgEntry); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// runOnce executes the MTM for the given settlement date (YYYY-MM-DD, UTC).
func (m *mtmRunner) runOnce(ctx context.Context, settlementDate string) error {
	positions, err := m.openFuturesPositions(ctx)
	if err != nil {
		return err
	}
	// Cache settlement price per symbol so we price each contract once.
	priceBySymbol := map[string]int64{}
	priced := map[string]bool{}

	var settled, skipped int
	for _, p := range positions {
		price, ok := priceBySymbol[p.Symbol]
		if !priced[p.Symbol] {
			pr, found, err := m.settlementPriceVWAP(ctx, p.Symbol)
			if err != nil {
				return err
			}
			priced[p.Symbol] = true
			if found {
				priceBySymbol[p.Symbol] = pr
				price, ok = pr, true
			} else {
				ok = false
			}
		}
		if !ok {
			skipped++
			log.Printf("mtm: no trades in window for %s; skipping mark", p.Symbol)
			continue
		}

		prior, err := m.priorMark(ctx, p.ContractID, p.UserID, settlementDate, p.AvgEntry)
		if err != nil {
			return err
		}
		pnl := computePnL(p.NetQuantity, price, prior)

		applied, err := m.settleOne(ctx, settlementDate, p, prior, price, pnl)
		if err != nil {
			return err
		}
		if applied {
			settled++
			m.emitMTMSettled(settlementDate, p, prior, price, pnl)
		}
	}
	log.Printf("mtm: run %s complete: %d settled, %d skipped (of %d open positions)",
		settlementDate, settled, skipped, len(positions))
	return nil
}

// settleOne applies one position's PnL to escrow and records the MTM row, all
// in one transaction. Returns false if the position was already settled today
// (idempotent). Escrow is floored at 0 (a shortfall is a margin breach — the
// negative PnL is still recorded for audit).
func (m *mtmRunner) settleOne(ctx context.Context, settlementDate string, p openPosition, prior, price, pnl int64) (bool, error) {
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback() //nolint:errcheck

	res, err := tx.ExecContext(ctx, `
		INSERT INTO mtm_settlements
		    (id, settlement_date, contract_id, symbol, user_id, net_quantity,
		     prior_mark_cents, settlement_price_cents, pnl_cents)
		VALUES ($1, $2::date, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (settlement_date, contract_id, user_id) DO NOTHING`,
		uuidv4(), settlementDate, p.ContractID, p.Symbol, p.UserID, p.NetQuantity,
		prior, price, pnl,
	)
	if err != nil {
		return false, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return false, nil // already settled for this day
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO escrow_accounts (user_id, balance, updated_at)
		VALUES ($1, GREATEST(0, $2), now())
		ON CONFLICT (user_id) DO UPDATE
			SET balance = GREATEST(0, escrow_accounts.balance + $2), updated_at = now()`,
		p.UserID, pnl,
	); err != nil {
		return false, err
	}

	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

// emitMTMSettled publishes an MTMSettled event (best-effort).
func (m *mtmRunner) emitMTMSettled(settlementDate string, p openPosition, prior, price, pnl int64) {
	if m.pub == nil {
		return
	}
	ev := MTMSettled{
		EventID:              uuidv4(),
		SettlementDate:       settlementDate,
		ContractID:           p.ContractID,
		Symbol:               p.Symbol,
		UserID:               p.UserID,
		NetQuantity:          p.NetQuantity,
		PriorMarkCents:       prior,
		SettlementPriceCents: price,
		PnLCents:             pnl,
		OccurredAtUnixMs:     time.Now().UnixMilli(),
	}
	payload, err := json.Marshal(ev)
	if err != nil {
		log.Printf("mtm: marshal MTMSettled: %v", err)
		return
	}
	if err := m.pub.publish(p.UserID, payload); err != nil {
		log.Printf("mtm: publish MTMSettled for %s: %v", p.UserID, err)
	}
}

// scheduleDaily runs the MTM at each 00:00 UTC until ctx is cancelled.
func (m *mtmRunner) scheduleDaily(ctx context.Context) {
	for {
		next := nextMidnightUTC(time.Now().UTC())
		wait := time.Until(next)
		log.Printf("mtm: next daily run at %s (in %s)", next.Format(time.RFC3339), wait.Truncate(time.Second))
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
			date := next.Format("2006-01-02")
			if err := m.runOnce(ctx, date); err != nil {
				log.Printf("mtm: daily run %s error: %v", date, err)
			}
		}
	}
}

// nextMidnightUTC returns the next 00:00:00 UTC strictly after t.
func nextMidnightUTC(t time.Time) time.Time {
	t = t.UTC()
	midnight := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	return midnight.Add(24 * time.Hour)
}
