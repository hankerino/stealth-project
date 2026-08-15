// Package main implements the Risk Service for the Compute Trading Exchange
// (Phase 2): real-time position tracking and pre-trade margin/position-limit
// checks for futures (and spot) orders.
//
// Config via env:
//   - LISTEN_ADDR       REST listen address (default ":8084")
//   - GRPC_ADDR         gRPC listen address (default ":9094")
//   - DATABASE_URL      Postgres DSN (required; shared exchange DB)
//   - KAFKA_BROKERS     comma-separated brokers; if unset, the consumer/
//                       producer are disabled (dev mode)
//   - KAFKA_TLS_ENABLED "true" to dial brokers over TLS/mTLS
package main

import (
	"database/sql"
	"log"
	"net"
	"net/http"
	"os"

	riskv1 "github.com/hankerino/stealth-project/libs/proto/gen/risk/v1"
	_ "github.com/lib/pq"
	"google.golang.org/grpc"
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

	svc := &riskService{db: db}

	brokers := os.Getenv("KAFKA_BROKERS")
	if brokers != "" {
		tlsEnabled := os.Getenv("KAFKA_TLS_ENABLED") == "true"
		svc.pub = newKafkaProducer(brokers, tlsEnabled)
		consumer := newKafkaConsumer(brokers, tlsEnabled)
		go func() {
			log.Printf("risk consumer starting on trades + order-updates, brokers %q", brokers)
			if err := consumer.run(svc); err != nil {
				log.Printf("risk consumer exited: %v", err)
			}
		}()
	} else {
		log.Print("KAFKA_BROKERS unset; risk consumer/producer disabled (dev mode)")
	}

	// REST front.
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	api := &httpAPI{svc: svc}
	mux.HandleFunc("/v1/risk/check-margin", api.handleCheckMargin)
	mux.HandleFunc("/v1/risk/position", api.handlePosition)

	httpAddr := envOr("LISTEN_ADDR", ":8084")
	go func() {
		log.Printf("risk REST listening on %s", httpAddr)
		if err := http.ListenAndServe(httpAddr, mux); err != nil {
			log.Fatalf("http server: %v", err)
		}
	}()

	// gRPC front (RiskService — order service calls CheckMargin pre-trade).
	grpcAddr := envOr("GRPC_ADDR", ":9094")
	lis, err := net.Listen("tcp", grpcAddr)
	if err != nil {
		log.Fatalf("listen grpc: %v", err)
	}
	gs := grpc.NewServer()
	riskv1.RegisterRiskServiceServer(gs, &grpcServer{svc: svc})
	log.Printf("risk gRPC listening on %s", grpcAddr)
	log.Fatal(gs.Serve(lis))
}
