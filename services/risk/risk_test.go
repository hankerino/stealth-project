package main

import "testing"

func TestIsFuturesSymbol(t *testing.T) {
	cases := map[string]bool{
		"H100:us-east-1":               false,
		"H100:us-east-1:FUT:2026-11":   true,
		"A100:eu-west-1:FUT:2027-01":   true,
		"B200:us-west-2":               false,
	}
	for sym, want := range cases {
		if got := isFuturesSymbol(sym); got != want {
			t.Errorf("isFuturesSymbol(%q)=%v want %v", sym, got, want)
		}
	}
}

func TestNextAvgEntry(t *testing.T) {
	cases := []struct {
		name                              string
		oldNet, oldAvg, signed, newNet, p int64
		want                              int64
	}{
		{"open long", 0, 0, 10, 10, 500, 500},
		{"add to long (avg)", 10, 500, 10, 20, 700, 600},
		{"reduce long keeps avg", 20, 600, -5, 15, 900, 600},
		{"close flat", 10, 500, -10, 0, 900, 0},
		{"flip to short", 10, 500, -15, -5, 900, 900},
		{"open short", 0, 0, -10, -10, 400, 400},
		{"add to short (avg)", -10, 400, -10, -20, 600, 500},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := nextAvgEntry(c.oldNet, c.oldAvg, c.signed, c.newNet, c.p); got != c.want {
				t.Errorf("nextAvgEntry=%d want %d", got, c.want)
			}
		})
	}
}

func TestAbs64(t *testing.T) {
	if abs64(-7) != 7 || abs64(7) != 7 || abs64(0) != 0 {
		t.Fatal("abs64 wrong")
	}
}

func TestSpotSellAllowed(t *testing.T) {
	cases := []struct {
		operator  bool
		held, qty int64
		want      bool
	}{
		{true, 0, 5, true},   // node operator: primary supply
		{false, 5, 5, true},  // exact resale of held hours
		{false, 8, 3, true},  // partial resale
		{false, 2, 3, false}, // selling more than held
		{false, 0, 1, false}, // naked
	}
	for _, c := range cases {
		if got := spotSellAllowed(c.operator, c.held, c.qty); got != c.want {
			t.Errorf("spotSellAllowed(%v,%d,%d)=%v want %v", c.operator, c.held, c.qty, got, c.want)
		}
	}
}
