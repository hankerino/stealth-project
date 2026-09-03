package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAccountID_MissingIdentity(t *testing.T) {
	t.Setenv("GATEWAY_SHARED_SECRET", "")
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/escrow/balance", nil)
	if id, ok := accountID(rr, req); ok || id != "" {
		t.Fatalf("expected failure with no X-Account-Id, got id=%q ok=%v", id, ok)
	}
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rr.Code)
	}
}

func TestAccountID_Valid(t *testing.T) {
	t.Setenv("GATEWAY_SHARED_SECRET", "")
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/escrow/balance", nil)
	req.Header.Set(headerAccountID, "acct-uuid-123")
	id, ok := accountID(rr, req)
	if !ok || id != "acct-uuid-123" {
		t.Fatalf("expected acct-uuid-123, got id=%q ok=%v", id, ok)
	}
}

func TestAccountID_GatewaySecretEnforced(t *testing.T) {
	t.Setenv("GATEWAY_SHARED_SECRET", "correct-secret")

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/escrow/balance", nil)
	req.Header.Set(headerAccountID, "acct-uuid-123")
	req.Header.Set(headerGatewaySecret, "guessed")
	if _, ok := accountID(rr, req); ok {
		t.Fatal("expected failure with wrong gateway secret")
	}
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rr.Code)
	}

	rr2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/v1/escrow/balance", nil)
	req2.Header.Set(headerAccountID, "acct-uuid-123")
	req2.Header.Set(headerGatewaySecret, "correct-secret")
	if id, ok := accountID(rr2, req2); !ok || id != "acct-uuid-123" {
		t.Fatalf("expected success, got id=%q ok=%v", id, ok)
	}
}

func TestHandleDeposit_RejectsWithoutIdentity(t *testing.T) {
	t.Setenv("GATEWAY_SHARED_SECRET", "")
	a := &httpAPI{} // svc nil is fine: the gate returns first.
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/escrow/deposit", nil)
	a.handleDeposit(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rr.Code)
	}
}

func TestHandleBalance_RejectsWithoutIdentity(t *testing.T) {
	t.Setenv("GATEWAY_SHARED_SECRET", "")
	a := &httpAPI{}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/escrow/balance", nil)
	a.handleBalance(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rr.Code)
	}
}
