package main

import "testing"

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
