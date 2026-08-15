// Package main implements the Trade Settlement Service for the Compute
// Trading Exchange. It consumes matched trades from the `trades` topic,
// verifies buyer escrow, atomically transfers funds and records the
// trade_ledger (Phase 1B), and runs daily mark-to-market (MTM) settlement
// for open futures positions (Phase 2).
//
// Config via env:
//   - LISTEN_ADDR        REST listen address (default ":8083")
//   - DATABASE_URL       Postgres DSN (required; shared exchange DB)
//   - KAFKA_BROKERS      comma-separated brokers; unset => dev mode
//   - KAFKA_TLS_ENABLED  "true" to dial brokers over TLS/mTLS
//   - KAFKA_CLIENT_CERT / KAFKA_CLIENT_KEY / KAFKA_CA_CERT  mTLS paths
package main

import (
	"context"
	"database/sql"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

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

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	svc := &settlementService{db: db}
	// MTM runner is available in all modes so POST /v1/mtm/run works in dev.
	mtm := &mtmRunner{db: db}
	svc.mtm = mtm

	brokers := os.Getenv("KAFKA_BROKERS")
	if brokers != "" {
		tls := os.Getenv("KAFKA_TLS_ENABLED") == "true"
		consumer, err := newKafkaConsumer(brokers, tls)
		if err != nil {
			log.Fatalf("create kafka consumer: %v", err)
		}
		failedProd, err := newKafkaProducer(brokers, tls, settlementFailedTopic)
		if err != nil {
			log.Fatalf("create kafka producer: %v", err)
		}
		svc.failedEventProducer = failedProd
		mtmProd, err := newKafkaProducer(brokers, tls, mtmTopic)
		if err != nil {
			log.Fatalf("create mtm producer: %v", err)
		}
		mtm.pub = mtmProd

		go func() {
			log.Printf("settlement consumer starting on trades topic, brokers %q", brokers)
			if err := consumer.run(svc); err != nil {
				log.Printf("kafka consumer exited: %v", err)
			}
		}()
		go mtm.scheduleDaily(ctx)
		log.Print("mtm daily scheduler started (00:00 UTC)")
	} else {
		log.Print("KAFKA_BROKERS unset; consumer + MTM scheduler off (dev mode); POST /v1/mtm/run still works")
	}

	// Graceful cancel of the scheduler on SIGTERM/SIGINT.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sigCh
		log.Println("shutdown signal received")
		cancel()
	}()

	// REST API.
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	api := &httpAPI{svc: svc}
	mux.HandleFunc("/v1/escrow/deposit", api.handleDeposit)
	mux.HandleFunc("/v1/escrow/balance", api.handleBalance)
	mux.HandleFunc("/v1/mtm/run", api.handleMTMRun)

	addr := envOr("LISTEN_ADDR", ":8083")
	log.Printf("settlement REST listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}
