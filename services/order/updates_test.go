package main

import (
	"encoding/json"
	"testing"
)

// Payload as emitted by services/matching-engine (events.rs OrderUpdated).
const engineOrderUpdated = `{"event_id":"e-1","order_id":"d36778b0-5756-4cd6-8a3f-f1d4cf89f0a3",
"user_id":"u-1","symbol":"H100:us-east-1","new_status":"FILLED","filled_quantity":1,
"remaining_quantity":0,"reason":null,"occurred_at_unix_ms":1788448411855}`

func TestOrderUpdatedDecodesEnginePayload(t *testing.T) {
	var u OrderUpdated
	if err := json.Unmarshal([]byte(engineOrderUpdated), &u); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if u.OrderID != "d36778b0-5756-4cd6-8a3f-f1d4cf89f0a3" || u.NewStatus != "FILLED" ||
		u.FilledQuantity != 1 || u.RemainingQuantity != 0 || u.Reason != nil {
		t.Fatalf("unexpected decode: %+v", u)
	}
	if err := validateOrderUpdate(&u); err != nil {
		t.Fatalf("engine payload rejected: %v", err)
	}
}

func TestValidateOrderUpdate(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*OrderUpdated)
		wantOK bool
	}{
		{"valid partial", func(u *OrderUpdated) { u.NewStatus = "PARTIALLY_FILLED" }, true},
		{"valid cancelled", func(u *OrderUpdated) { u.NewStatus = "CANCELLED" }, true},
		{"missing order_id", func(u *OrderUpdated) { u.OrderID = " " }, false},
		{"unknown status", func(u *OrderUpdated) { u.NewStatus = "DONE" }, false},
		{"lowercase status", func(u *OrderUpdated) { u.NewStatus = "filled" }, false},
		{"negative fill", func(u *OrderUpdated) { u.FilledQuantity = -1 }, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u := OrderUpdated{OrderID: "o-1", NewStatus: "FILLED", FilledQuantity: 1}
			tc.mutate(&u)
			err := validateOrderUpdate(&u)
			if tc.wantOK && err != nil {
				t.Fatalf("expected ok, got %v", err)
			}
			if !tc.wantOK && err == nil {
				t.Fatal("expected error, got nil")
			}
		})
	}
}

// Every status the engine can emit must be accepted by the orders.status
// CHECK constraint (0001_orders.up.sql); keep the two lists in lock-step.
func TestEngineStatusesMatchDBConstraint(t *testing.T) {
	dbCheck := []string{"OPEN", "PARTIALLY_FILLED", "FILLED", "CANCELLED", "REJECTED", "EXPIRED"}
	if len(dbCheck) != len(engineStatuses) {
		t.Fatalf("engineStatuses has %d entries, DB CHECK has %d", len(engineStatuses), len(dbCheck))
	}
	for _, s := range dbCheck {
		if !engineStatuses[s] {
			t.Errorf("DB status %q missing from engineStatuses", s)
		}
	}
	for s := range terminalStatuses {
		if !engineStatuses[s] {
			t.Errorf("terminal status %q not an engine status", s)
		}
	}
}
