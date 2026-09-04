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
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	_ "github.com/lib/pq"
)

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// defaultMockJobSeconds is the mock-run duration the scheduler writes into
// job specs (DEFAULT_MOCK_JOB_SECONDS; the node-agent may override per job).
var defaultMockJobSeconds = func() int64 {
	if v, err := strconv.Atoi(envOr("DEFAULT_MOCK_JOB_SECONDS", "6")); err == nil && v > 0 {
		return int64(v)
	}
	return 6
}()

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

	svc := &settlementService{
		db:           db,
		stripe:       newStripeClient(os.Getenv("STRIPE_SECRET_KEY"), os.Getenv("STRIPE_WEBHOOK_SECRET")),
		publicWebURL: envOr("PUBLIC_WEB_URL", "https://cte-web.onrender.com"),
	}
	if svc.stripe.enabled() {
		log.Print("stripe: card deposits enabled (Checkout + webhook)")
	} else {
		log.Print("STRIPE_SECRET_KEY unset; card deposits disabled (admin credit only)")
	}
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
		jobsProd, err := newKafkaProducer(brokers, tls, jobsTopic)
		if err != nil {
			log.Fatalf("create jobs producer: %v", err)
		}
		svc.jobEventProducer = jobsProd

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

	// Shutdown handling is wired up below, once srv exists.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	// REST API.
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	api := &httpAPI{svc: svc}
	mux.HandleFunc("/v1/escrow/deposit", api.handleDeposit) // admin credit (gateway: admin only)
	mux.HandleFunc("/v1/escrow/balance", api.handleBalance)
	mux.HandleFunc("/v1/escrow/checkout", api.handleCheckout)
	mux.HandleFunc("/v1/escrow/withdraw", api.handleWithdraw)
	mux.HandleFunc("/v1/escrow/history", api.handleHistory)
	mux.HandleFunc("/v1/admin/payouts", api.handleAdminPayouts)
	mux.HandleFunc("/v1/admin/payouts/{id}", api.handleAdminPayoutResolve)
	mux.HandleFunc("/v1/stripe/webhook", api.handleStripeWebhook) // public; Stripe-Signature verified
	mux.HandleFunc("/v1/mtm/run", api.handleMTMRun)
	mux.HandleFunc("/v1/jobs/poll", api.handleJobsPoll)
	mux.HandleFunc("/v1/jobs/{id}/status", api.handleJobStatus)
	mux.HandleFunc("/v1/jobs/{id}/run", api.handleJobRun)   // buyer (gateway)
	mux.HandleFunc("/v1/allocations", api.handleAllocations) // buyer (gateway)

	addr := envOr("LISTEN_ADDR", ":8083")
	srv := &http.Server{Addr: addr, Handler: mux}

	// Graceful shutdown on SIGTERM/SIGINT: cancel the MTM scheduler, then stop
	// the HTTP server so main returns and the process exits. Without the
	// Shutdown the process hung forever in ListenAndServe — signal.Notify
	// suppresses the default SIGTERM termination (same pattern as
	// services/order's gs.GracefulStop). The Kafka consumer is not joined on
	// purpose: its offset is committed only after a trade is settled, so
	// abandoning an in-flight fetch is safe (at-least-once redelivery), and
	// kafka-go's Reader.Close would block on that in-flight fetch instead.
	go func() {
		<-sigCh
		log.Println("shutdown signal received")
		cancel()
		shutdownCtx, release := context.WithTimeout(context.Background(), 5*time.Second)
		defer release()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Printf("http shutdown: %v", err)
		}
	}()

	log.Printf("settlement REST listening on %s", addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("http server: %v", err)
	}
	log.Println("settlement stopped")
}
