package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// These tests cover the M1 gateway trust boundary in http.go. They exercise the
// accountID gate and the 401 paths, which return before touching the DB-backed
// orderService, so no live service is needed.

func TestAccountID_MissingIdentity(t *testing.T) {
	t.Setenv("GATEWAY_SHARED_SECRET", "")
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/orders", nil)
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
	req := httptest.NewRequest(http.MethodGet, "/v1/orders", nil)
	req.Header.Set(headerAccountID, "acct-uuid-123")
	id, ok := accountID(rr, req)
	if !ok || id != "acct-uuid-123" {
		t.Fatalf("expected acct-uuid-123, got id=%q ok=%v", id, ok)
	}
}

func TestAccountID_GatewaySecretEnforced(t *testing.T) {
	t.Setenv("GATEWAY_SHARED_SECRET", "correct-secret")

	// Missing/incorrect secret -> 401 even with an account id present.
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/orders", nil)
	req.Header.Set(headerAccountID, "acct-uuid-123")
	req.Header.Set(headerGatewaySecret, "guessed")
	if _, ok := accountID(rr, req); ok {
		t.Fatal("expected failure with wrong gateway secret")
	}
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rr.Code)
	}

	// Correct secret + account id -> success.
	rr2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/v1/orders", nil)
	req2.Header.Set(headerAccountID, "acct-uuid-123")
	req2.Header.Set(headerGatewaySecret, "correct-secret")
	id, ok := accountID(rr2, req2)
	if !ok || id != "acct-uuid-123" {
		t.Fatalf("expected success, got id=%q ok=%v", id, ok)
	}
}

// handleCancel must reject before touching the service when identity is absent.
func TestHandleCancel_RejectsWithoutIdentity(t *testing.T) {
	t.Setenv("GATEWAY_SHARED_SECRET", "")
	a := &httpAPI{} // svc nil is fine: the gate returns first.
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/v1/orders/abc", nil)
	a.handleCancel(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rr.Code)
	}
}

// handleOrders must reject before touching the service when identity is absent.
func TestHandleOrders_RejectsWithoutIdentity(t *testing.T) {
	t.Setenv("GATEWAY_SHARED_SECRET", "")
	a := &httpAPI{}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/orders", nil)
	a.handleOrders(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rr.Code)
	}
}
