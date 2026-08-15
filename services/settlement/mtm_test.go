package main

import (
	"testing"
	"time"
)

func TestComputePnL(t *testing.T) {
	cases := []struct {
		name                          string
		net, settlement, prior, want int64
	}{
		{"long gains on rise", 10, 550, 500, 500},
		{"long loses on fall", 10, 450, 500, -500},
		{"short loses on rise", -10, 550, 500, -500},
		{"short gains on fall", -10, 450, 500, 500},
		{"flat position", 0, 600, 500, 0},
		{"no move", 10, 500, 500, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := computePnL(c.net, c.settlement, c.prior); got != c.want {
				t.Fatalf("computePnL(%d,%d,%d)=%d want %d", c.net, c.settlement, c.prior, got, c.want)
			}
		})
	}
}

func TestNextMidnightUTC(t *testing.T) {
	in := time.Date(2026, 8, 15, 13, 30, 45, 0, time.UTC)
	got := nextMidnightUTC(in)
	want := time.Date(2026, 8, 16, 0, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("nextMidnightUTC=%s want %s", got, want)
	}
	// Exactly midnight rolls to the next day (strictly after).
	mid := time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)
	if got := nextMidnightUTC(mid); !got.Equal(time.Date(2026, 8, 16, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("nextMidnightUTC(midnight)=%s", got)
	}
}
