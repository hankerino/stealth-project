package main

// Historical market-data REST endpoints (Phase 3), backed by the shared
// Postgres `trade_ledger` (settled trades). All are read-only, support
// date-range filtering (?from,?to as RFC3339 or YYYY-MM-DD) and pagination
// (?limit,?offset):
//
//   GET /v1/trades/historical?symbol=&from=&to=&limit=&offset=
//   GET /v1/prices/historical?symbol=&interval=1h&from=&to=&limit=&offset=   (OHLCV candles)
//   GET /v1/prices/index?gpu_type=&interval=1h&from=&to=&limit=&offset=      (Compute Price Index history)

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	defaultLimit    = 100
	maxLimit        = 1000
	defaultInterval = 3600 // 1h, seconds
	defaultLookback = 24 * time.Hour
)

type historyAPI struct {
	db *sql.DB
}

// --- pure request-parsing helpers (unit-tested) ---

// parsePagination reads ?limit (1..maxLimit, default 100) and ?offset (>=0).
func parsePagination(q map[string][]string) (limit, offset int, err error) {
	limit, offset = defaultLimit, 0
	if v := first(q, "limit"); v != "" {
		limit, err = strconv.Atoi(v)
		if err != nil || limit < 1 {
			return 0, 0, errors.New("limit must be a positive integer")
		}
		if limit > maxLimit {
			limit = maxLimit
		}
	}
	if v := first(q, "offset"); v != "" {
		offset, err = strconv.Atoi(v)
		if err != nil || offset < 0 {
			return 0, 0, errors.New("offset must be a non-negative integer")
		}
	}
	return limit, offset, nil
}

// parseTimeRange reads ?from and ?to (RFC3339 or YYYY-MM-DD). Defaults:
// to = now (UTC), from = to - 24h. Enforces from < to.
func parseTimeRange(q map[string][]string) (from, to time.Time, err error) {
	to = time.Now().UTC()
	if v := first(q, "to"); v != "" {
		to, err = parseTime(v)
		if err != nil {
			return time.Time{}, time.Time{}, errors.New("invalid 'to' time")
		}
	}
	from = to.Add(-defaultLookback)
	if v := first(q, "from"); v != "" {
		from, err = parseTime(v)
		if err != nil {
			return time.Time{}, time.Time{}, errors.New("invalid 'from' time")
		}
	}
	if !from.Before(to) {
		return time.Time{}, time.Time{}, errors.New("'from' must be before 'to'")
	}
	return from, to, nil
}

func parseTime(s string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC(), nil
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t.UTC(), nil
	}
	return time.Time{}, errors.New("unrecognized time format")
}

// parseIntervalSeconds reads ?interval like 30s, 1m, 5m, 1h, 1d (default 1h).
func parseIntervalSeconds(q map[string][]string) (int, error) {
	v := first(q, "interval")
	if v == "" {
		return defaultInterval, nil
	}
	units := map[byte]int{'s': 1, 'm': 60, 'h': 3600, 'd': 86400}
	mult, ok := units[v[len(v)-1]]
	if !ok {
		return 0, errors.New("interval must end in s, m, h or d (e.g. 5m, 1h)")
	}
	n, err := strconv.Atoi(v[:len(v)-1])
	if err != nil || n <= 0 {
		return 0, errors.New("interval must be a positive number with unit (e.g. 5m)")
	}
	return n * mult, nil
}

func first(q map[string][]string, key string) string {
	if vs := q[key]; len(vs) > 0 {
		return strings.TrimSpace(vs[0])
	}
	return ""
}

// --- handlers ---

func (h *historyAPI) requireDB(w http.ResponseWriter) bool {
	if h.db == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "historical API not configured (no DATABASE_URL)"})
		return false
	}
	return true
}

func (h *historyAPI) handleTradesHistorical(w http.ResponseWriter, r *http.Request) {
	if !h.requireDB(w) {
		return
	}
	q := r.URL.Query()
	symbol := first(q, "symbol")
	if symbol == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "symbol is required"})
		return
	}
	from, to, err := parseTimeRange(q)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	limit, offset, err := parsePagination(q)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	rows, err := h.db.QueryContext(r.Context(), `
		SELECT trade_id, symbol, price_cents, quantity, total_cost, created_at
		FROM trade_ledger
		WHERE symbol = $1 AND status = 'SETTLED' AND created_at >= $2 AND created_at < $3
		ORDER BY created_at DESC
		LIMIT $4 OFFSET $5`, symbol, from, to, limit, offset)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var tradeID, sym string
		var price, qty, total int64
		var createdAt time.Time
		if err := rows.Scan(&tradeID, &sym, &price, &qty, &total, &createdAt); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "scan failed"})
			return
		}
		// Public tape: no counterparty identities (buyer_id/seller_id stay
		// in trade_ledger for the parties' own order history and admin).
		out = append(out, map[string]any{
			"trade_id": tradeID, "symbol": sym,
			"price_cents": price, "quantity": qty, "total_cost": total,
			"executed_at": createdAt.UTC().Format(time.RFC3339),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"symbol": symbol, "from": from.Format(time.RFC3339), "to": to.Format(time.RFC3339),
		"limit": limit, "offset": offset, "trades": out,
	})
}

func (h *historyAPI) handlePricesHistorical(w http.ResponseWriter, r *http.Request) {
	if !h.requireDB(w) {
		return
	}
	q := r.URL.Query()
	symbol := first(q, "symbol")
	if symbol == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "symbol is required"})
		return
	}
	interval, err := parseIntervalSeconds(q)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	from, to, err := parseTimeRange(q)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	limit, offset, err := parsePagination(q)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	// OHLCV candles by time bucket. open/close via ordered array_agg.
	rows, err := h.db.QueryContext(r.Context(), `
		WITH b AS (
			SELECT to_timestamp(floor(extract(epoch FROM created_at) / $2) * $2) AS bucket,
			       price_cents, quantity, created_at
			FROM trade_ledger
			WHERE symbol = $1 AND status = 'SETTLED' AND created_at >= $3 AND created_at < $4
		)
		SELECT bucket,
		       (array_agg(price_cents ORDER BY created_at ASC))[1]  AS open,
		       max(price_cents)                                     AS high,
		       min(price_cents)                                     AS low,
		       (array_agg(price_cents ORDER BY created_at DESC))[1] AS close,
		       sum(quantity)                                        AS volume,
		       count(*)                                             AS trades
		FROM b GROUP BY bucket ORDER BY bucket DESC
		LIMIT $5 OFFSET $6`, symbol, interval, from, to, limit, offset)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var bucket time.Time
		var open, high, low, close_, volume, trades int64
		if err := rows.Scan(&bucket, &open, &high, &low, &close_, &volume, &trades); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "scan failed"})
			return
		}
		out = append(out, map[string]any{
			"bucket": bucket.UTC().Format(time.RFC3339),
			"open":   open, "high": high, "low": low, "close": close_,
			"volume": volume, "trades": trades,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"symbol": symbol, "interval_seconds": interval,
		"from": from.Format(time.RFC3339), "to": to.Format(time.RFC3339),
		"limit": limit, "offset": offset, "candles": out,
	})
}

func (h *historyAPI) handlePricesIndex(w http.ResponseWriter, r *http.Request) {
	if !h.requireDB(w) {
		return
	}
	q := r.URL.Query()
	gpuType := first(q, "gpu_type") // empty => composite across all GPU types
	interval, err := parseIntervalSeconds(q)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	from, to, err := parseTimeRange(q)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	limit, offset, err := parsePagination(q)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	// VWAP per time bucket; optional GPU-type filter (split_part on the symbol).
	rows, err := h.db.QueryContext(r.Context(), `
		WITH b AS (
			SELECT to_timestamp(floor(extract(epoch FROM created_at) / $1) * $1) AS bucket,
			       split_part(symbol, ':', 1) AS gpu_type, price_cents, quantity
			FROM trade_ledger
			WHERE status = 'SETTLED' AND created_at >= $2 AND created_at < $3
			  AND ($4 = '' OR split_part(symbol, ':', 1) = $4)
		)
		SELECT bucket,
		       (sum(price_cents * quantity) / NULLIF(sum(quantity), 0))::bigint AS vwap_cents,
		       sum(quantity) AS volume,
		       count(*)      AS trades
		FROM b GROUP BY bucket ORDER BY bucket DESC
		LIMIT $5 OFFSET $6`, interval, from, to, gpuType, limit, offset)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var bucket time.Time
		var vwap, volume, trades int64
		if err := rows.Scan(&bucket, &vwap, &volume, &trades); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "scan failed"})
			return
		}
		out = append(out, map[string]any{
			"bucket": bucket.UTC().Format(time.RFC3339),
			"vwap_cents": vwap, "volume": volume, "trades": trades,
		})
	}
	scope := "COMPOSITE"
	if gpuType != "" {
		scope = gpuType
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"scope": scope, "interval_seconds": interval,
		"from": from.Format(time.RFC3339), "to": to.Format(time.RFC3339),
		"limit": limit, "offset": offset, "index": out,
	})
}
