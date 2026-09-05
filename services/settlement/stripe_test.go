package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const whSecret = "whsec_test_123"

func TestVerifyWebhook_RoundTrip(t *testing.T) {
	body := []byte(`{"id":"evt_1","type":"checkout.session.completed"}`)
	now := time.Now()
	hdr := signWebhook(whSecret, body, now)
	if err := verifyWebhook(whSecret, hdr, body, now.Add(30*time.Second)); err != nil {
		t.Fatalf("valid signature rejected: %v", err)
	}
}

func TestVerifyWebhook_Rejects(t *testing.T) {
	body := []byte(`{"id":"evt_1"}`)
	now := time.Now()
	good := signWebhook(whSecret, body, now)
	cases := []struct {
		name   string
		secret string
		header string
		body   []byte
		at     time.Time
	}{
		{"wrong secret", "whsec_other", good, body, now},
		{"tampered body", whSecret, good, []byte(`{"id":"evt_2"}`), now},
		{"stale timestamp", whSecret, good, body, now.Add(webhookTolerance + time.Minute)},
		{"future timestamp", whSecret, good, body, now.Add(-webhookTolerance - time.Minute)},
		{"missing header", whSecret, "", body, now},
		{"no v1", whSecret, "t=" + strings.SplitN(good, ",", 2)[0][2:], body, now},
		{"no secret configured", "", good, body, now},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := verifyWebhook(tc.secret, tc.header, tc.body, tc.at); err == nil {
				t.Fatal("expected rejection, got nil")
			}
		})
	}
}

func TestVerifyWebhook_MultipleV1(t *testing.T) {
	body := []byte(`{}`)
	now := time.Now()
	good := signWebhook(whSecret, body, now)
	// Stripe may send several v1 entries during secret rotation; any match passes.
	hdr := strings.Replace(good, ",v1=", ",v1=deadbeef,v1=", 1)
	if err := verifyWebhook(whSecret, hdr, body, now); err != nil {
		t.Fatalf("rotation header rejected: %v", err)
	}
}

func TestStripeWebhookHandler_BadSignatureIs400(t *testing.T) {
	a := &httpAPI{svc: &settlementService{stripe: newStripeClient("", whSecret)}}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/stripe/webhook", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Stripe-Signature", "t=1,v1=00")
	a.handleStripeWebhook(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestStripeWebhookHandler_IgnoresUnknownEvent(t *testing.T) {
	a := &httpAPI{svc: &settlementService{stripe: newStripeClient("", whSecret)}}
	body := []byte(`{"id":"evt_x","type":"customer.created","data":{"object":{}}}`)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/stripe/webhook", bytes.NewReader(body))
	req.Header.Set("Stripe-Signature", signWebhook(whSecret, body, time.Now()))
	a.handleStripeWebhook(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestCheckoutDisabledIs503(t *testing.T) {
	t.Setenv("GATEWAY_SHARED_SECRET", "")
	a := &httpAPI{svc: &settlementService{stripe: newStripeClient("", "")}}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/escrow/checkout", strings.NewReader(`{"amount_cents":10000}`))
	req.Header.Set(headerAccountID, "acct-1")
	a.handleCheckout(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("want 503, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestCreateCheckoutSession_Disabled(t *testing.T) {
	c := newStripeClient("", "")
	if _, err := c.createCheckoutSession(context.Background(), "u", "d", 100, 33, "https://x/s", "https://x/c"); err != errStripeDisabled {
		t.Fatalf("want errStripeDisabled, got %v", err)
	}
}
