package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// Parquet files start and end with the 4-byte magic "PAR1". This exercises the
// generic writer for both row types (compiles + runs the parquet integration).
func TestWriteParquetTrades(t *testing.T) {
	data, err := writeParquet([]tradeRow{{
		TradeID: "t1", Symbol: "H100:us-east-1", BuyerID: "b", SellerID: "s",
		PriceCents: 500, Quantity: 10, TotalCost: 5000, ExecutedAtUnixMs: 1,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(data) < 8 || string(data[:4]) != "PAR1" || string(data[len(data)-4:]) != "PAR1" {
		t.Fatalf("not a parquet file (len=%d)", len(data))
	}
}

func TestWriteParquetCandles(t *testing.T) {
	data, err := writeParquet([]candleRow{{
		Symbol: "H100:us-east-1", BucketUnixMs: 1, Open: 1, High: 2, Low: 1, Close: 2, Volume: 10, Trades: 2,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if string(data[:4]) != "PAR1" {
		t.Fatalf("bad parquet magic: %q", data[:4])
	}
}

// The admin endpoint is closed without ADMIN_TOKEN and rejects wrong/missing
// tokens; a correct token passes the gate (proven with an invalid time range,
// which returns 400 — after the gate, before any DB access).
func TestOffloadAdminTokenGate(t *testing.T) {
	o := &offloader{}
	newReq := func(token, rawURL string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, rawURL, nil)
		if token != "" {
			r.Header.Set("X-Admin-Token", token)
		}
		w := httptest.NewRecorder()
		o.handleOffload(w, r)
		return w
	}
	const url = "/v1/admin/offload"

	t.Setenv("ADMIN_TOKEN", "")
	if w := newReq("anything", url); w.Code != http.StatusUnauthorized {
		t.Fatalf("unset ADMIN_TOKEN: got %d, want 401", w.Code)
	}
	t.Setenv("ADMIN_TOKEN", "s3cret")
	if w := newReq("", url); w.Code != http.StatusUnauthorized {
		t.Fatalf("missing header: got %d, want 401", w.Code)
	}
	if w := newReq("wrong", url); w.Code != http.StatusUnauthorized {
		t.Fatalf("wrong token: got %d, want 401", w.Code)
	}
	if w := newReq("s3cret", url+"?from=bogus"); w.Code != http.StatusBadRequest {
		t.Fatalf("correct token + bad range: got %d, want 400 (gate passed)", w.Code)
	}
}
