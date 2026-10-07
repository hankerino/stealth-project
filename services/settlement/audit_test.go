package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandleAdminAudit_ReadOnlyAndGated(t *testing.T) {
	t.Setenv("GATEWAY_SHARED_SECRET", "s3cret")
	api := &httpAPI{}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/audit", nil)
	api.handleAdminAudit(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST: want 405, got %d", rr.Code)
	}

	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/v1/admin/audit", nil)
	req.Header.Set(headerAccountID, "auditor-1")
	req.Header.Set(headerGatewaySecret, "wrong")
	api.handleAdminAudit(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("direct call without gateway secret: want 401, got %d", rr.Code)
	}
}
