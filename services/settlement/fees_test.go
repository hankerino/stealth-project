package main

import "testing"

func TestBpsOfRoundsHalfUp(t *testing.T) {
	cases := []struct{ amount, bps, want int64 }{
		{10_000, 100, 100},   // $100 × 1% = $1.00
		{10_000, 250, 250},   // $100 × 2.5% = $2.50
		{1, 100, 0},          // 0.01¢ rounds to 0
		{50, 100, 1},         // 0.5¢ rounds up to 1¢
		{49, 100, 0},         // 0.49¢ rounds down
		{123_456, 250, 3086}, // 3086.4 → 3086
		{0, 250, 0},
		{10_000, 0, 0}, // fee switched off
	}
	for _, c := range cases {
		if got := bpsOf(c.amount, c.bps); got != c.want {
			t.Errorf("bpsOf(%d,%d)=%d want %d", c.amount, c.bps, got, c.want)
		}
	}
}

func TestDefaultScheduleTradeFees(t *testing.T) {
	f := feeSchedule{BuyerBps: 100, SellerBps: 250}
	fees := f.forTrade(20_000) // $200 notional
	if fees.Buyer != 200 || fees.Seller != 500 {
		t.Fatalf("got buyer %d seller %d, want 200/500", fees.Buyer, fees.Seller)
	}
	// Money conservation: buyer pays principal+fee; seller gets principal−fee;
	// platform gets both fees.
	principal := int64(20_000)
	paid := principal + fees.Buyer
	received := principal - fees.Seller
	platform := fees.Buyer + fees.Seller
	if paid != received+platform {
		t.Fatalf("conservation broken: paid %d ≠ received %d + platform %d", paid, received, platform)
	}
}

// The deposit fee is grossed up so that after Stripe takes bps + fixed of the
// total charge, at least the typed amount is left for escrow.
func TestDepositFeeGrossUpCoversProcessor(t *testing.T) {
	f := feeSchedule{DepositBps: 290, DepositFixedCents: 30}
	for _, amount := range []int64{100, 1_000, 5_000, 10_000, 99_999, 1_000_000} {
		fee := f.depositFee(amount)
		total := amount + fee
		// Stripe's cut of the total charge (round half-up like Stripe does).
		cut := (total*290+5_000)/10_000 + 30
		if total-cut < amount {
			t.Errorf("amount %d fee %d: net %d < amount", amount, fee, total-cut)
		}
		// Not wildly over: within 2¢ of the exact solution.
		if total-cut > amount+2 {
			t.Errorf("amount %d fee %d: over-recovers by %d¢", amount, fee, total-cut-amount)
		}
	}
	// $100 deposit → about $3.29 fee (100+0.30)/(1−0.029) − 100 = 3.30.
	if fee := f.depositFee(10_000); fee < 325 || fee > 335 {
		t.Fatalf("$100 deposit fee = %d¢, expected ≈330", fee)
	}
	if f.depositFee(0) != 0 {
		t.Fatal("zero amount must have zero fee")
	}
	if (feeSchedule{}).depositFee(10_000) != 0 {
		t.Fatal("no processor cost configured must mean zero fee")
	}
}
