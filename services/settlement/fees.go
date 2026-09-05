package main

// Exchange fees (M4). Two-sided marketplace model, the way Coinbase
// Advanced / Airbnb price a matched transaction:
//
//   - buyer fee  : FEE_BUYER_BPS of the trade notional, charged on top of the
//                  price and held with the trade; collected when the trade
//                  settles, refunded with the principal if delivery fails.
//   - seller fee : FEE_SELLER_BPS of the notional, deducted from the seller's
//                  proceeds at release. The order book still shows the clean
//                  matched price; fees are line items on the ledger.
//   - deposits   : Stripe's card cost (FEE_DEPOSIT_BPS + FEE_DEPOSIT_FIXED_CENTS)
//                  is added to the Checkout as a visible "Card processing"
//                  line, grossed up so escrow is credited exactly the amount
//                  the user typed. That fee goes to Stripe, not to hQube, so
//                  it is recorded but never credited to the platform account.
//
// Trade fees accrue to a platform escrow account (PLATFORM_ACCOUNT_ID) and
// leave the system through the same payout path as any other balance
// (admin role + MFA at the gateway). Rates are frozen per trade at hold time
// (stored on trade_ledger), so changing the env never rewrites history.
// Every fee leaves a fee_ledger row.

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"
)

const (
	feeKindBuyerTrade  = "BUYER_TRADE"
	feeKindSellerTrade = "SELLER_TRADE"
	feeKindDeposit     = "DEPOSIT_PROCESSING"

	defaultPlatformAccount = "platform:hqube"
)

type feeSchedule struct {
	BuyerBps          int64  `json:"buyer_bps"`
	SellerBps         int64  `json:"seller_bps"`
	DepositBps        int64  `json:"deposit_bps"`
	DepositFixedCents int64  `json:"deposit_fixed_cents"`
	PlatformAccount   string `json:"-"`
}

// loadFeeSchedule reads the schedule from the environment. Defaults are the
// closed-beta rates; set any to 0 to switch that fee off.
func loadFeeSchedule() feeSchedule {
	return feeSchedule{
		BuyerBps:          envInt64("FEE_BUYER_BPS", 100),
		SellerBps:         envInt64("FEE_SELLER_BPS", 250),
		DepositBps:        envInt64("FEE_DEPOSIT_BPS", 290),
		DepositFixedCents: envInt64("FEE_DEPOSIT_FIXED_CENTS", 30),
		PlatformAccount:   envOr("PLATFORM_ACCOUNT_ID", defaultPlatformAccount),
	}
}

func envInt64(key string, fallback int64) int64 {
	v := envOr(key, "")
	if v == "" {
		return fallback
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n < 0 {
		log.Printf("fees: invalid %s=%q, using %d", key, v, fallback)
		return fallback
	}
	return n
}

// bpsOf returns amount × bps / 10_000, rounded half-up. Never negative.
func bpsOf(amountCents, bps int64) int64 {
	if amountCents <= 0 || bps <= 0 {
		return 0
	}
	return (amountCents*bps + 5_000) / 10_000
}

func (f feeSchedule) buyerFee(notionalCents int64) int64  { return bpsOf(notionalCents, f.BuyerBps) }
func (f feeSchedule) sellerFee(notionalCents int64) int64 { return bpsOf(notionalCents, f.SellerBps) }

// depositFee is the grossed-up card fee: the smallest fee such that the
// processor's cut of (amount + fee) still leaves ≥ amount for escrow.
//   gross = (amount + fixed) / (1 − bps/10_000)
func (f feeSchedule) depositFee(amountCents int64) int64 {
	if amountCents <= 0 || (f.DepositBps <= 0 && f.DepositFixedCents <= 0) {
		return 0
	}
	den := 10_000 - f.DepositBps
	if den <= 0 {
		return 0
	}
	num := (amountCents + f.DepositFixedCents) * 10_000
	gross := (num + den - 1) / den // ceil
	return gross - amountCents
}

// tradeFees is what a trade carries from hold to settlement.
type tradeFees struct {
	Buyer  int64
	Seller int64
}

func (f feeSchedule) forTrade(notionalCents int64) tradeFees {
	return tradeFees{Buyer: f.buyerFee(notionalCents), Seller: f.sellerFee(notionalCents)}
}

// recordFee writes one fee_ledger row inside tx. toPlatform also credits the
// platform escrow account (trade fees yes, processor pass-through no).
func (s *settlementService) recordFee(ctx context.Context, tx *sql.Tx, kind, payerID string, tradeID, depositID *string, basisCents, bps, feeCents int64, toPlatform bool) error {
	if feeCents <= 0 {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO fee_ledger (id, kind, payer_id, trade_id, deposit_id, basis_cents, bps, fee_cents, to_platform)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		uuidv4(), kind, payerID, tradeID, depositID, basisCents, bps, feeCents, toPlatform); err != nil {
		return fmt.Errorf("fee_ledger %s: %w", kind, err)
	}
	if !toPlatform {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO escrow_accounts (user_id, balance, updated_at) VALUES ($1, $2, now())
		ON CONFLICT (user_id) DO UPDATE SET balance = escrow_accounts.balance + $2, updated_at = now()`,
		s.fees.PlatformAccount, feeCents); err != nil {
		return fmt.Errorf("credit platform: %w", err)
	}
	return nil
}

// collectTradeFees is called when a trade settles: the buyer fee was held
// with the principal, the seller fee is netted from the release. Both are
// booked to the platform in the same transaction as the release.
func (s *settlementService) collectTradeFees(ctx context.Context, tx *sql.Tx, tradeID, buyerID, sellerID string, notional int64, fees tradeFees) error {
	tid := tradeID
	if err := s.recordFee(ctx, tx, feeKindBuyerTrade, buyerID, &tid, nil, notional, s.fees.BuyerBps, fees.Buyer, true); err != nil {
		return err
	}
	return s.recordFee(ctx, tx, feeKindSellerTrade, sellerID, &tid, nil, notional, s.fees.SellerBps, fees.Seller, true)
}

// --- revenue reporting ------------------------------------------------------

type revenueReport struct {
	PlatformAccount string          `json:"platform_account"`
	BalanceCents    int64           `json:"balance_cents"`
	Schedule        feeSchedule     `json:"schedule"`
	Totals          []revenueBucket `json:"totals"`
	Last30d         []revenueBucket `json:"last_30d"`
	Recent          []feeRow        `json:"recent"`
}

type revenueBucket struct {
	Kind      string `json:"kind"`
	Count     int64  `json:"count"`
	FeeCents  int64  `json:"fee_cents"`
	ToHQube   bool   `json:"to_platform"`
}

type feeRow struct {
	ID         string    `json:"id"`
	Kind       string    `json:"kind"`
	PayerID    string    `json:"payer_id"`
	TradeID    *string   `json:"trade_id"`
	DepositID  *string   `json:"deposit_id"`
	BasisCents int64     `json:"basis_cents"`
	Bps        int64     `json:"bps"`
	FeeCents   int64     `json:"fee_cents"`
	ToPlatform bool      `json:"to_platform"`
	CreatedAt  time.Time `json:"created_at"`
}

func (s *settlementService) revenue(ctx context.Context) (*revenueReport, error) {
	r := &revenueReport{PlatformAccount: s.fees.PlatformAccount, Schedule: s.fees}
	bal, err := s.getBalance(ctx, s.fees.PlatformAccount)
	if err != nil {
		return nil, err
	}
	r.BalanceCents = bal
	bucket := func(where string) ([]revenueBucket, error) {
		rows, err := s.db.QueryContext(ctx, `
			SELECT kind, count(*), COALESCE(SUM(fee_cents),0), to_platform
			FROM fee_ledger `+where+` GROUP BY kind, to_platform ORDER BY kind`)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var out []revenueBucket
		for rows.Next() {
			var b revenueBucket
			if err := rows.Scan(&b.Kind, &b.Count, &b.FeeCents, &b.ToHQube); err != nil {
				return nil, err
			}
			out = append(out, b)
		}
		if out == nil {
			out = []revenueBucket{}
		}
		return out, rows.Err()
	}
	if r.Totals, err = bucket(""); err != nil {
		return nil, err
	}
	if r.Last30d, err = bucket("WHERE created_at > now() - interval '30 days'"); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id::text, kind, payer_id, trade_id::text, deposit_id::text, basis_cents, bps, fee_cents, to_platform, created_at
		FROM fee_ledger ORDER BY created_at DESC LIMIT 50`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	r.Recent = []feeRow{}
	for rows.Next() {
		var f feeRow
		if err := rows.Scan(&f.ID, &f.Kind, &f.PayerID, &f.TradeID, &f.DepositID, &f.BasisCents, &f.Bps, &f.FeeCents, &f.ToPlatform, &f.CreatedAt); err != nil {
			return nil, err
		}
		r.Recent = append(r.Recent, f)
	}
	return r, rows.Err()
}

// --- HTTP -------------------------------------------------------------------

// handleFees serves GET /v1/fees — the public schedule, so the web ticket and
// deposit form can show the exact fee before the user commits.
func (a *httpAPI) handleFees(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	writeJSON(w, http.StatusOK, a.svc.fees)
}

// handleAdminRevenue serves GET /v1/admin/revenue. Admin role via gateway.
func (a *httpAPI) handleAdminRevenue(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if _, ok := accountID(w, r); !ok {
		return
	}
	rep, err := a.svc.revenue(r.Context())
	if err != nil {
		log.Printf("revenue: %v", err)
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

// handleAdminRevenuePayout serves POST /v1/admin/revenue/payout
// {amount_cents, note}: moves platform fee revenue into the normal payout
// queue (same limits and 4-eyes resolve step as any user payout). Admin +
// MFA enforced by the gateway.
func (a *httpAPI) handleAdminRevenuePayout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	adminID, ok := accountID(w, r)
	if !ok {
		return
	}
	var in struct {
		AmountCents int64  `json:"amount_cents"`
		Note        string `json:"note"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	note := "platform revenue payout requested by " + adminID
	if in.Note != "" {
		note += ": " + in.Note
	}
	p, err := a.svc.requestPayout(r.Context(), a.svc.fees.PlatformAccount, in.AmountCents, note)
	if err != nil {
		writePayoutError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, p)
}
