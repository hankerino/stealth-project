package main

import (
	"testing"
	"time"
)

func TestCheckWashTrade(t *testing.T) {
	same := &TradeExecuted{TradeID: "t1", Symbol: "H100:us-east-1", MakerUserID: "u1", TakerUserID: "u1"}
	if a := checkWashTrade(same); a == nil || a.Rule != "WASH_TRADE" || a.UserID != "u1" || a.RefID != "t1" {
		t.Fatalf("expected WASH_TRADE alert, got %+v", a)
	}
	diff := &TradeExecuted{TradeID: "t2", MakerUserID: "u1", TakerUserID: "u2"}
	if a := checkWashTrade(diff); a != nil {
		t.Fatalf("expected no alert, got %+v", a)
	}
	empty := &TradeExecuted{TradeID: "t3"}
	if a := checkWashTrade(empty); a != nil {
		t.Fatalf("empty ids must not alert, got %+v", a)
	}
}

func strp(s string) *string { return &s }

func TestSpoofDetector(t *testing.T) {
	d := newSpoofDetector(spoofParams{Window: 10 * time.Minute, MinCancels: 3, CancelRatio: 0.75, Cooldown: 5 * time.Minute})
	base := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	upd := func(id, status string, reason *string) *OrderUpdated {
		return &OrderUpdated{OrderID: id, UserID: "u1", Symbol: "H100:us-east-1", NewStatus: status, Reason: reason}
	}

	// Engine-side cancels never count.
	if a := d.observe(upd("o0", "CANCELLED", strp("IOC_REMAINDER")), base); a != nil {
		t.Fatalf("IOC remainder must not count: %+v", a)
	}
	// Two user cancels: below MinCancels.
	if a := d.observe(upd("o1", "CANCELLED", strp("USER_CANCEL")), base.Add(time.Second)); a != nil {
		t.Fatal("too early")
	}
	if a := d.observe(upd("o2", "CANCELLED", nil), base.Add(2*time.Second)); a != nil {
		t.Fatal("too early")
	}
	// A fill keeps the ratio at 2/3 < 0.75 even after the 3rd cancel? 3 cancels/1 fill = 0.75 -> trips.
	if a := d.observe(upd("o3", "FILLED", nil), base.Add(3*time.Second)); a != nil {
		t.Fatal("fill must not alert")
	}
	a := d.observe(upd("o4", "CANCELLED", strp("USER_CANCEL")), base.Add(4*time.Second))
	if a == nil || a.Rule != "SPOOFING" || a.Details["cancels"] != 3 || a.Details["fills"] != 1 {
		t.Fatalf("expected SPOOFING alert with 3 cancels / 1 fill, got %+v", a)
	}
	// Cooldown: another cancel right away does not re-alert.
	if a := d.observe(upd("o5", "CANCELLED", nil), base.Add(5*time.Second)); a != nil {
		t.Fatalf("cooldown violated: %+v", a)
	}
	// After the window slides past everything, state resets.
	if a := d.observe(upd("o6", "CANCELLED", nil), base.Add(30*time.Minute)); a != nil {
		t.Fatalf("stale window must not alert: %+v", a)
	}
	if n := len(d.events["u1|H100:us-east-1"]); n != 1 {
		t.Fatalf("expected pruned window of 1 event, got %d", n)
	}
}

func TestClassify(t *testing.T) {
	cases := []struct {
		status   string
		reason   *string
		relevant bool
		cancel   bool
	}{
		{"FILLED", nil, true, false},
		{"CANCELLED", strp("USER_CANCEL"), true, true},
		{"CANCELED", nil, true, true},
		{"CANCELLED", strp("FOK_UNFILLABLE"), false, false},
		{"EXPIRED", nil, false, false},
		{"OPEN", nil, false, false},
		{"PARTIALLY_FILLED", nil, false, false},
	}
	for _, c := range cases {
		r, k := classify(&OrderUpdated{NewStatus: c.status, Reason: c.reason})
		if r != c.relevant || k != c.cancel {
			t.Errorf("classify(%s,%v) = (%v,%v) want (%v,%v)", c.status, c.reason, r, k, c.relevant, c.cancel)
		}
	}
}
