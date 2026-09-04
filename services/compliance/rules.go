package main

// Surveillance rules (Phase 4). Pure functions over event streams so they are
// unit-testable without Kafka or Postgres; the consumer feeds them and
// persists whatever they return.
//
//   WASH_TRADE  a fill where the same account is on both sides.
//   SPOOFING    layering/spoofing proxy: within a sliding window one account
//               cancels most of what it places on a book, with few fills.
//               (Cancels from IOC/FOK remainders and engine expiry are not
//               the user's doing and are ignored.)

import (
	"encoding/json"
	"strings"
	"sync"
	"time"
)

// TradeExecuted mirrors libs/schemas/TradeExecuted.avsc (topic: trades).
type TradeExecuted struct {
	EventID          string `json:"event_id"`
	TradeID          string `json:"trade_id"`
	Symbol           string `json:"symbol"`
	PriceCents       int64  `json:"price_cents"`
	Quantity         int64  `json:"quantity"`
	AggressorSide    string `json:"aggressor_side"`
	MakerOrderID     string `json:"maker_order_id"`
	TakerOrderID     string `json:"taker_order_id"`
	MakerUserID      string `json:"maker_user_id"`
	TakerUserID      string `json:"taker_user_id"`
	OccurredAtUnixMs int64  `json:"occurred_at_unix_ms"`
}

// OrderUpdated mirrors libs/schemas/OrderUpdated.avsc (topic: order-updates).
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

// Alert is a triggered rule, ready to persist/emit.
type Alert struct {
	Rule     string
	Severity string
	UserID   string
	Symbol   string
	RefID    string
	Details  map[string]any
}

func (a Alert) detailsJSON() []byte {
	b, _ := json.Marshal(a.Details)
	return b
}

// --- WASH_TRADE ----------------------------------------------------------------

// checkWashTrade flags a fill whose maker and taker are the same account.
func checkWashTrade(t *TradeExecuted) *Alert {
	if t.MakerUserID == "" || t.MakerUserID != t.TakerUserID {
		return nil
	}
	return &Alert{
		Rule: "WASH_TRADE", Severity: "high", UserID: t.MakerUserID, Symbol: t.Symbol, RefID: t.TradeID,
		Details: map[string]any{
			"maker_order_id": t.MakerOrderID, "taker_order_id": t.TakerOrderID,
			"price_cents": t.PriceCents, "quantity": t.Quantity, "aggressor_side": t.AggressorSide,
		},
	}
}

// --- SPOOFING -------------------------------------------------------------------

// spoofParams tunes the cancel-ratio detector.
type spoofParams struct {
	Window      time.Duration // sliding window
	MinCancels  int           // at least this many user cancels in the window
	CancelRatio float64       // cancels / (cancels + fills) >= this
	Cooldown    time.Duration // one alert per (user, symbol) per cooldown
}

var defaultSpoofParams = spoofParams{
	Window:      10 * time.Minute,
	MinCancels:  5,
	CancelRatio: 0.8,
	Cooldown:    10 * time.Minute,
}

type orderEvent struct {
	at     time.Time
	cancel bool // true = user cancel, false = fill
}

// spoofDetector keeps per-(user, symbol) event windows in memory. State is
// bounded by the window (old events are pruned on every touch) and rebuilds
// naturally after a restart from the live stream.
type spoofDetector struct {
	mu      sync.Mutex
	params  spoofParams
	events  map[string][]orderEvent
	alerted map[string]time.Time
}

func newSpoofDetector(p spoofParams) *spoofDetector {
	return &spoofDetector{params: p, events: map[string][]orderEvent{}, alerted: map[string]time.Time{}}
}

// engineCancelReasons are cancellations the user did not initiate.
var engineCancelReasons = map[string]bool{"IOC_REMAINDER": true, "FOK_UNFILLABLE": true, "EXPIRED": true}

// classify maps an order update to (relevant, isCancel).
func classify(u *OrderUpdated) (bool, bool) {
	switch strings.ToUpper(u.NewStatus) {
	case "FILLED":
		return true, false
	case "CANCELLED", "CANCELED":
		if u.Reason != nil && engineCancelReasons[strings.ToUpper(*u.Reason)] {
			return false, false
		}
		// A cancel after a partial fill still counts, but pure fills do not.
		return true, true
	}
	return false, false
}

// observe feeds one order update; returns an alert when the rule trips.
func (d *spoofDetector) observe(u *OrderUpdated, now time.Time) *Alert {
	relevant, isCancel := classify(u)
	if !relevant || u.UserID == "" {
		return nil
	}
	key := u.UserID + "|" + u.Symbol
	d.mu.Lock()
	defer d.mu.Unlock()

	cutoff := now.Add(-d.params.Window)
	evs := d.events[key]
	kept := evs[:0]
	for _, e := range evs {
		if e.at.After(cutoff) {
			kept = append(kept, e)
		}
	}
	kept = append(kept, orderEvent{at: now, cancel: isCancel})
	d.events[key] = kept

	cancels, fills := 0, 0
	for _, e := range kept {
		if e.cancel {
			cancels++
		} else {
			fills++
		}
	}
	if cancels < d.params.MinCancels {
		return nil
	}
	ratio := float64(cancels) / float64(cancels+fills)
	if ratio < d.params.CancelRatio {
		return nil
	}
	if last, ok := d.alerted[key]; ok && now.Sub(last) < d.params.Cooldown {
		return nil
	}
	d.alerted[key] = now
	return &Alert{
		Rule: "SPOOFING", Severity: "medium", UserID: u.UserID, Symbol: u.Symbol, RefID: u.OrderID,
		Details: map[string]any{
			"window_seconds": int(d.params.Window.Seconds()), "cancels": cancels, "fills": fills,
			"cancel_ratio": ratio, "last_order_id": u.OrderID,
		},
	}
}
