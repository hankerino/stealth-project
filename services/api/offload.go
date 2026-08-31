package main

// Batch offload of trades and OHLCV candles to S3-compatible object storage
// (Cloudflare R2 / MinIO on the cheap stack; AWS S3 on the upgrade path) in
// Apache Parquet format (Phase 3).
//
// Enabled when S3_BUCKET is set. Config via env:
//   - S3_BUCKET                (required to enable)
//   - S3_ENDPOINT              R2/MinIO endpoint URL; empty => real AWS S3
//   - S3_REGION                default "auto" (R2) — set your region for AWS
//   - S3_PREFIX                key prefix, default "market-data"
//   - OFFLOAD_INTERVAL_SECONDS ticker period, default 3600
// AWS credentials come from the standard SDK chain (env vars, etc.).

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/parquet-go/parquet-go"
)

type tradeRow struct {
	TradeID          string `parquet:"trade_id"`
	Symbol           string `parquet:"symbol"`
	BuyerID          string `parquet:"buyer_id"`
	SellerID         string `parquet:"seller_id"`
	PriceCents       int64  `parquet:"price_cents"`
	Quantity         int64  `parquet:"quantity"`
	TotalCost        int64  `parquet:"total_cost"`
	ExecutedAtUnixMs int64  `parquet:"executed_at_unix_ms"`
}

type candleRow struct {
	Symbol       string `parquet:"symbol"`
	BucketUnixMs int64  `parquet:"bucket_unix_ms"`
	Open         int64  `parquet:"open"`
	High         int64  `parquet:"high"`
	Low          int64  `parquet:"low"`
	Close        int64  `parquet:"close"`
	Volume       int64  `parquet:"volume"`
	Trades       int64  `parquet:"trades"`
}

// writeParquet serializes rows into an in-memory Parquet file.
func writeParquet[T any](rows []T) ([]byte, error) {
	buf := new(bytes.Buffer)
	w := parquet.NewGenericWriter[T](buf)
	if _, err := w.Write(rows); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

type offloader struct {
	db     *sql.DB
	s3     *s3.Client
	bucket string
	prefix string
}

// newOffloader returns nil (disabled) when S3_BUCKET is unset.
func newOffloader(ctx context.Context, db *sql.DB) (*offloader, error) {
	bucket := os.Getenv("S3_BUCKET")
	if bucket == "" || db == nil {
		return nil, nil
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(envOr("S3_REGION", "auto")))
	if err != nil {
		return nil, err
	}
	endpoint := os.Getenv("S3_ENDPOINT")
	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		if endpoint != "" {
			o.BaseEndpoint = aws.String(endpoint)
			o.UsePathStyle = true // MinIO wants path-style
		}
	})
	return &offloader{db: db, s3: client, bucket: bucket, prefix: envOr("S3_PREFIX", "market-data")}, nil
}

func (o *offloader) put(ctx context.Context, key string, data []byte) error {
	_, err := o.s3.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(o.bucket),
		Key:         aws.String(key),
		Body:        bytes.NewReader(data),
		ContentType: aws.String("application/vnd.apache.parquet"),
	})
	return err
}

func (o *offloader) offloadTrades(ctx context.Context, from, to time.Time) (int, error) {
	rows, err := o.db.QueryContext(ctx, `
		SELECT trade_id, symbol, buyer_id, seller_id, price_cents, quantity, total_cost, created_at
		FROM trade_ledger
		WHERE status = 'SETTLED' AND created_at >= $1 AND created_at < $2
		ORDER BY created_at`, from, to)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var out []tradeRow
	for rows.Next() {
		var t tradeRow
		var createdAt time.Time
		if err := rows.Scan(&t.TradeID, &t.Symbol, &t.BuyerID, &t.SellerID,
			&t.PriceCents, &t.Quantity, &t.TotalCost, &createdAt); err != nil {
			return 0, err
		}
		t.ExecutedAtUnixMs = createdAt.UnixMilli()
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if len(out) == 0 {
		return 0, nil
	}
	data, err := writeParquet(out)
	if err != nil {
		return 0, err
	}
	key := fmt.Sprintf("%s/trades/date=%s/trades-%d.parquet", o.prefix, from.UTC().Format("2006-01-02"), to.UnixMilli())
	return len(out), o.put(ctx, key, data)
}

func (o *offloader) offloadCandles(ctx context.Context, intervalSeconds int, from, to time.Time) (int, error) {
	rows, err := o.db.QueryContext(ctx, `
		WITH b AS (
			SELECT symbol,
			       to_timestamp(floor(extract(epoch FROM created_at) / $1) * $1) AS bucket,
			       price_cents, quantity, created_at
			FROM trade_ledger
			WHERE status = 'SETTLED' AND created_at >= $2 AND created_at < $3
		)
		SELECT symbol, bucket,
		       (array_agg(price_cents ORDER BY created_at ASC))[1]  AS open,
		       max(price_cents)                                     AS high,
		       min(price_cents)                                     AS low,
		       (array_agg(price_cents ORDER BY created_at DESC))[1] AS close,
		       sum(quantity)                                        AS volume,
		       count(*)                                             AS trades
		FROM b GROUP BY symbol, bucket ORDER BY symbol, bucket`, intervalSeconds, from, to)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var out []candleRow
	for rows.Next() {
		var c candleRow
		var bucket time.Time
		if err := rows.Scan(&c.Symbol, &bucket, &c.Open, &c.High, &c.Low, &c.Close, &c.Volume, &c.Trades); err != nil {
			return 0, err
		}
		c.BucketUnixMs = bucket.UnixMilli()
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if len(out) == 0 {
		return 0, nil
	}
	data, err := writeParquet(out)
	if err != nil {
		return 0, err
	}
	key := fmt.Sprintf("%s/candles/interval=%ds/date=%s/candles-%d.parquet", o.prefix, intervalSeconds, from.UTC().Format("2006-01-02"), to.UnixMilli())
	return len(out), o.put(ctx, key, data)
}

// Run offloads the trailing window every OFFLOAD_INTERVAL_SECONDS.
func (o *offloader) Run(ctx context.Context) {
	interval := time.Duration(envInt("OFFLOAD_INTERVAL_SECONDS", 3600)) * time.Second
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	log.Printf("offload: enabled, every %s -> s3://%s/%s", interval, o.bucket, o.prefix)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			to := time.Now().UTC()
			from := to.Add(-interval)
			nt, err := o.offloadTrades(ctx, from, to)
			if err != nil {
				log.Printf("offload trades: %v", err)
			}
			nc, err := o.offloadCandles(ctx, defaultInterval, from, to)
			if err != nil {
				log.Printf("offload candles: %v", err)
			}
			if nt > 0 || nc > 0 {
				log.Printf("offload: %d trades, %d candles for %s..%s", nt, nc,
					from.Format(time.RFC3339), to.Format(time.RFC3339))
			}
		}
	}
}

// handleOffload serves POST /v1/admin/offload — manual trigger.
// Body (optional): {"from":"...","to":"..."} (RFC3339 or YYYY-MM-DD).
func (o *offloader) handleOffload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	q := r.URL.Query()
	from, to, err := parseTimeRange(q)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	nt, err := o.offloadTrades(r.Context(), from, to)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "offload trades: " + err.Error()})
		return
	}
	nc, err := o.offloadCandles(r.Context(), defaultInterval, from, to)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "offload candles: " + err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "trades": nt, "candles": nc,
		"from": from.Format(time.RFC3339), "to": to.Format(time.RFC3339)})
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}
