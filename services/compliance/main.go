// Package main implements the market-surveillance (compliance) service for
// the Compute Trading Exchange (Phase 4). It consumes `trades` and
// `order-updates`, runs the wash-trade and spoofing rules (rules.go), stores
// alerts in surveillance_alerts, emits AlertTriggered, and serves the admin
// review API through the gateway.
//
// Config via env:
//   - LISTEN_ADDR        REST listen address (default ":8085")
//   - DATABASE_URL       Postgres DSN (required; shared exchange DB)
//   - KAFKA_BROKERS      comma-separated brokers; unset => REST only (dev)
//   - KAFKA_TLS_ENABLED  "true" to dial brokers over TLS/mTLS
//   - GATEWAY_SHARED_SECRET  gateway trust boundary for the admin API
//   - SPOOF_WINDOW_SECONDS / SPOOF_MIN_CANCELS / SPOOF_CANCEL_RATIO  tuning
package main

import (
	"database/sql"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	_ "github.com/lib/pq"
)

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func spoofParamsFromEnv() spoofParams {
	p := defaultSpoofParams
	if v, err := strconv.Atoi(os.Getenv("SPOOF_WINDOW_SECONDS")); err == nil && v > 0 {
		p.Window = time.Duration(v) * time.Second
		p.Cooldown = p.Window
	}
	if v, err := strconv.Atoi(os.Getenv("SPOOF_MIN_CANCELS")); err == nil && v > 0 {
		p.MinCancels = v
	}
	if v, err := strconv.ParseFloat(os.Getenv("SPOOF_CANCEL_RATIO"), 64); err == nil && v > 0 && v <= 1 {
		p.CancelRatio = v
	}
	return p
}

func main() {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		log.Fatal("DATABASE_URL is required")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		log.Fatalf("ping db: %v", err)
	}

	svc := &complianceService{db: db, spoof: newSpoofDetector(spoofParamsFromEnv())}

	brokers := os.Getenv("KAFKA_BROKERS")
	if brokers != "" {
		tlsOn := os.Getenv("KAFKA_TLS_ENABLED") == "true"
		svc.pub = newKafkaProducer(brokers, tlsOn)
		go func() {
			log.Fatalf("trades consumer: %v", consume(newReader(brokers, tradesTopic, tlsOn), svc.handleTrade))
		}()
		go func() {
			log.Fatalf("order-updates consumer: %v", consume(newReader(brokers, orderUpdatesTopic, tlsOn), svc.handleOrderUpdate))
		}()
		log.Printf("compliance: consuming %s + %s from %s", tradesTopic, orderUpdatesTopic, brokers)
	} else {
		log.Print("KAFKA_BROKERS unset; surveillance consumers disabled (REST only)")
	}

	api := &httpAPI{svc: svc}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("/v1/admin/alerts", api.handleAlerts)
	mux.HandleFunc("/v1/admin/alerts/{id}", api.handleAlertStatus)

	addr := envOr("LISTEN_ADDR", ":8085")
	log.Printf("compliance: REST listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}
