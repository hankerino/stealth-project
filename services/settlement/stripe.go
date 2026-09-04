package main

// Minimal Stripe client: Checkout Sessions + webhook signature verification.
// Uses net/http directly (two endpoints, form-encoded) instead of stripe-go
// so the service keeps zero new dependencies.
//
// Env:
//   STRIPE_SECRET_KEY      sk_test_... / sk_live_...  (absent => disabled)
//   STRIPE_WEBHOOK_SECRET  whsec_...  (absent => webhook rejects everything)

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const stripeAPI = "https://api.stripe.com/v1"

// webhookTolerance bounds replay: Stripe recommends 5 minutes.
const webhookTolerance = 5 * time.Minute

var errStripeDisabled = errors.New("card payments not configured")

type stripeClient struct {
	secretKey     string
	webhookSecret string
	http          *http.Client
}

func newStripeClient(secretKey, webhookSecret string) *stripeClient {
	return &stripeClient{
		secretKey:     secretKey,
		webhookSecret: webhookSecret,
		http:          &http.Client{Timeout: 15 * time.Second},
	}
}

func (c *stripeClient) enabled() bool { return c != nil && c.secretKey != "" }

// checkoutSession is the subset of the Checkout Session object we use.
type checkoutSession struct {
	ID            string            `json:"id"`
	URL           string            `json:"url"`
	PaymentStatus string            `json:"payment_status"` // paid | unpaid | no_payment_required
	AmountTotal   int64             `json:"amount_total"`
	Currency      string            `json:"currency"`
	Metadata      map[string]string `json:"metadata"`
}

// createCheckoutSession opens a hosted Stripe Checkout page for a one-off
// escrow top-up. depositID is our escrow_deposits.id and rides along in
// metadata + client_reference_id so the webhook can correlate.
func (c *stripeClient) createCheckoutSession(ctx context.Context, userID, depositID string, amountCents int64, successURL, cancelURL string) (*checkoutSession, error) {
	if !c.enabled() {
		return nil, errStripeDisabled
	}
	form := url.Values{}
	form.Set("mode", "payment")
	form.Set("client_reference_id", depositID)
	form.Set("success_url", successURL)
	form.Set("cancel_url", cancelURL)
	form.Set("metadata[user_id]", userID)
	form.Set("metadata[deposit_id]", depositID)
	form.Set("payment_intent_data[metadata][user_id]", userID)
	form.Set("payment_intent_data[metadata][deposit_id]", depositID)
	form.Set("line_items[0][quantity]", "1")
	form.Set("line_items[0][price_data][currency]", "usd")
	form.Set("line_items[0][price_data][unit_amount]", strconv.FormatInt(amountCents, 10))
	form.Set("line_items[0][price_data][product_data][name]", "Compute Exchange escrow deposit")

	var out checkoutSession
	if err := c.post(ctx, "/checkout/sessions", form, &out); err != nil {
		return nil, err
	}
	if out.URL == "" {
		return nil, errors.New("stripe: checkout session has no url")
	}
	return &out, nil
}

func (c *stripeClient) post(ctx context.Context, path string, form url.Values, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, stripeAPI+path, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.secretKey)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Idempotency-Key", form.Get("client_reference_id"))
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("stripe: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("stripe: read response: %w", err)
	}
	if resp.StatusCode/100 != 2 {
		var e struct {
			Error struct {
				Message string `json:"message"`
				Type    string `json:"type"`
			} `json:"error"`
		}
		_ = json.Unmarshal(body, &e)
		if e.Error.Message == "" {
			e.Error.Message = http.StatusText(resp.StatusCode)
		}
		return fmt.Errorf("stripe: %s (%d)", e.Error.Message, resp.StatusCode)
	}
	return json.Unmarshal(body, out)
}

// stripeEvent is the envelope of a webhook delivery.
type stripeEvent struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	Data struct {
		Object json.RawMessage `json:"object"`
	} `json:"data"`
}

// verifyWebhook validates the Stripe-Signature header against the raw body.
// Scheme (https://stripe.com/docs/webhooks/signatures):
//
//	Stripe-Signature: t=<unix>,v1=<hex hmac>[,v1=<hex hmac>...]
//	signed_payload   = "<t>.<raw body>"
//	expected         = HMAC-SHA256(webhookSecret, signed_payload)
//
// Any v1 signature matching (constant-time) within the tolerance window passes.
func verifyWebhook(secret string, header string, body []byte, now time.Time) error {
	if secret == "" {
		return errors.New("webhook secret not configured")
	}
	var ts string
	var sigs []string
	for _, part := range strings.Split(header, ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		switch k {
		case "t":
			ts = v
		case "v1":
			sigs = append(sigs, v)
		}
	}
	if ts == "" || len(sigs) == 0 {
		return errors.New("malformed Stripe-Signature header")
	}
	tsInt, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return errors.New("malformed timestamp in Stripe-Signature")
	}
	if d := now.Sub(time.Unix(tsInt, 0)); d > webhookTolerance || d < -webhookTolerance {
		return errors.New("webhook timestamp outside tolerance")
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts))
	mac.Write([]byte("."))
	mac.Write(body)
	expected := mac.Sum(nil)
	for _, s := range sigs {
		got, err := hex.DecodeString(s)
		if err != nil {
			continue
		}
		if hmac.Equal(got, expected) {
			return nil
		}
	}
	return errors.New("no matching v1 signature")
}

// signWebhook produces a Stripe-Signature header (used by tests and the local
// dev harness to exercise the webhook without the Stripe CLI).
func signWebhook(secret string, body []byte, at time.Time) string {
	ts := strconv.FormatInt(at.Unix(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts + "."))
	mac.Write(body)
	return "t=" + ts + ",v1=" + hex.EncodeToString(mac.Sum(nil))
}
