package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestValidateOrder(t *testing.T) {
	valid := OrderInput{
		UserID:      "u-1",
		GPUType:     "H100",
		Region:      "us-east-1",
		Side:        SideBuy,
		PriceCents:  250,
		Quantity:    4,
		TimeInForce: TIFGTC,
	}
	if err := validateOrder(&valid); err != nil {
		t.Fatalf("valid order rejected: %v", err)
	}

	cases := []struct {
		name  string
		mutate func(*OrderInput)
	}{
		{"missing user_id", func(o *OrderInput) { o.UserID = "" }},
		{"missing gpu_type", func(o *OrderInput) { o.GPUType = " " }},
		{"missing region", func(o *OrderInput) { o.Region = "" }},
		{"bad side", func(o *OrderInput) { o.Side = "HOLD" }},
		{"zero price", func(o *OrderInput) { o.PriceCents = 0 }},
		{"negative price", func(o *OrderInput) { o.PriceCents = -1 }},
		{"zero quantity", func(o *OrderInput) { o.Quantity = 0 }},
		{"bad time_in_force", func(o *OrderInput) { o.TimeInForce = "DAY" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o := valid
			tc.mutate(&o)
			if err := validateOrder(&o); err == nil {
				t.Fatalf("expected validation error for %s", tc.name)
			}
		})
	}
}

func TestSymbol(t *testing.T) {
	if got := symbol("H100", "us-east-1"); got != "H100:us-east-1" {
		t.Fatalf("symbol = %q, want H100:us-east-1", got)
	}
}

// TestOrderPlacedEventContract pins the Kafka payload field names to
// libs/schemas/OrderPlaced.avsc (the wire contract has no registry in dev).
func TestOrderPlacedEventContract(t *testing.T) {
	ev := OrderPlaced{
		EventID:          "e-1",
		OrderID:          "o-1",
		UserID:           "u-1",
		Symbol:           "H100:us-east-1",
		GPUType:          "H100",
		Region:           "us-east-1",
		Side:             SideBuy,
		OrderType:        OrderTypeLimit,
		TimeInForce:      TIFGTC,
		PriceCents:       250,
		Quantity:         4,
		OccurredAtUnixMs: 1720000000000,
	}
	payload, err := marshalEvent(ev)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(payload, &m); err != nil {
		t.Fatal(err)
	}
	wantFields := []string{
		"event_id", "order_id", "user_id", "symbol", "gpu_type", "region",
		"side", "order_type", "time_in_force", "price_cents", "quantity",
		"occurred_at_unix_ms",
	}
	for _, f := range wantFields {
		if _, ok := m[f]; !ok {
			t.Errorf("OrderPlaced payload missing contract field %q: %s", f, payload)
		}
	}
	if len(m) != len(wantFields) {
		t.Errorf("OrderPlaced payload has unexpected fields: %s", payload)
	}
}

// TestOrderCancelledEventContract pins OrderCancelled.avsc field names.
func TestOrderCancelledEventContract(t *testing.T) {
	payload, err := marshalEvent(OrderCancelled{
		EventID: "e-1", OrderID: "o-1", UserID: "u-1",
		Symbol: "H100:us-east-1", OccurredAtUnixMs: 1720000000000,
	})
	if err != nil {
		t.Fatal(err)
	}
	s := string(payload)
	for _, f := range []string{"event_id", "order_id", "user_id", "symbol", "occurred_at_unix_ms"} {
		if !strings.Contains(s, `"`+f+`"`) {
			t.Errorf("OrderCancelled payload missing contract field %q: %s", f, s)
		}
	}
}

// nopPublisher satisfies Publisher for compile-time interface checks.
var _ Publisher = nopPublisher{}

func TestNopPublisher(t *testing.T) {
	if err := (nopPublisher{}).Publish(context.Background(), "k", OrderCancelled{}); err != nil {
		t.Fatal(err)
	}
}
