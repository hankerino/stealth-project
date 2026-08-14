// Package main implements the Trade Settlement Service for the Compute
// Trading Exchange (Phase 1B). It consumes matched trades from the MSK
// `trades` topic, verifies buyer escrow balance, atomically transfers
// funds from buyer to seller, and records the settlement in the
// trade_ledger. Failed settlements are emitted as SettlementFailed events
// to the `sla-breach-events` topic.
//
// Config via env:
//   - LISTEN_ADDR        REST listen address (default ":8083")
//   - DATABASE_URL       Aurora Postgres DSN (required)
//   - KAFKA_BROKERS      comma-separated MSK bootstrap brokers
//   - KAFKA_TLS_ENABLED  "true" to dial brokers over TLS (MSK)
//   - KAFKA_CLIENT_CERT  path to mTLS client certificate PEM (optional)
//   - KAFKA_CLIENT_KEY   path to mTLS client private key PEM (optional)
//   - KAFKA_CA_CERT      path to CA certificate PEM (optional)
package main

import (
	"database/sql"
	"log"
	"net/http"
	"os"

	_ "github.com/lib/pq"
)

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
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

	svc := &settlementService{db: db}

	// Start Kafka consumer for the trades topic (non-blocking).
	brokers := os.Getenv("KAFKA_BROKERS")
	if brokers != "" {
		consumer, err := newKafkaConsumer(brokers, os.Getenv("KAFKA_TLS_ENABLED") == "true")
		if err != nil {
			log.Fatalf("create kafka consumer: %v", err)
		}
		producer, err := newKafkaProducer(brokers, os.Getenv("KAFKA_TLS_ENABLED") == "true")
		if err != nil {
			log.Fatalf("create kafka producer: %v", err)
		}
		svc.failedEventProducer = producer
		go func() {
			log.Printf("settlement consumer starting on trades topic, brokers %q", brokers)
			if err := consumer.run(svc); err != nil {
				log.Printf("kafka consumer exited: %v", err)
			}
		}()
	} else {
		log.Print("KAFKA_BROKERS unset; settlement consumer not started (dev mode)")
	}

	// REST API: health + escrow deposit.
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	api := &httpAPI{svc: svc}
	mux.HandleFunc("/v1/escrow/deposit", api.handleDeposit)
	mux.HandleFunc("/v1/escrow/balance", api.handleBalance)

	addr := envOr("LISTEN_ADDR", ":8083")
	log.Printf("settlement REST listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}
