package main

import "testing"

// Limits are policy; pin them so a change is a deliberate diff.
func TestWithdrawalLimits(t *testing.T) {
	if maxPayoutCents != 5_000_00 {
		t.Fatalf("maxPayoutCents = %d", maxPayoutCents)
	}
	if dailyPayoutCapCents != 10_000_00 {
		t.Fatalf("dailyPayoutCapCents = %d", dailyPayoutCapCents)
	}
	if maxPayoutCents > dailyPayoutCapCents {
		t.Fatal("per-request max must not exceed the daily cap")
	}
}
