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

func TestNakedSpotSell(t *testing.T) {
	cases := []struct {
		kind, side string
		projected  int64
		want       bool
	}{
		{KindSpot, SideSell, -1, true},   // selling more than held
		{KindSpot, SideSell, 0, false},   // exact resale of held hours
		{KindSpot, SideSell, 3, false},   // partial resale
		{KindSpot, SideBuy, -5, false},   // buys never naked
		{KindFutures, SideSell, -1, false}, // futures short is margin-governed
	}
	for _, c := range cases {
		if got := nakedSpotSell(c.kind, c.side, c.projected); got != c.want {
			t.Errorf("nakedSpotSell(%s,%s,%d)=%v want %v", c.kind, c.side, c.projected, got, c.want)
		}
	}
}
