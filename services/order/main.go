package main

import (
	"context"
	"database/sql"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	orderv1 "github.com/hankerino/stealth-project/libs/proto/gen/order/v1"
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

	var pub Publisher = nopPublisher{}
	var kp *kafkaPublisher
	if brokers := os.Getenv("KAFKA_BROKERS"); brokers != "" {
		kp = newKafkaPublisher(brokers, os.Getenv("KAFKA_TLS_ENABLED") == "true")
		defer kp.Close()
		pub = kp
		log.Printf("publishing order events to Kafka brokers %q topic %q", brokers, ordersTopic)
	} else {
		log.Print("KAFKA_BROKERS unset; using log-only publisher (outbox dispatcher will sleep)")
	}

	// Pre-trade margin checks for futures orders (Risk service, gRPC).
	var risk RiskChecker = nopRiskChecker{}
	if addr := os.Getenv("RISK_ADDR"); addr != "" {
		rc, err := newRiskGRPCClient(addr)
		if err != nil {
			log.Fatalf("risk client: %v", err)
		}
		defer rc.Close()
		risk = rc
		log.Printf("futures margin checks enabled via Risk at %s", addr)
	} else {
		log.Print("RISK_ADDR unset; pre-trade risk checks disabled (nop allow-all)")
	}

	svc := &orderService{db: db, pub: pub, risk: risk}

	// Start the transactional outbox dispatcher (polls every 500ms).
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dispatcher := newOutboxDispatcher(db, kp)
	go dispatcher.Run(ctx)
	log.Println("outbox dispatcher started (500ms poll)")

	// Project engine OrderUpdated events (fills/cancels/rejects) back onto the
	// orders table so reads reflect the matched state.
	if brokers := os.Getenv("KAFKA_BROKERS"); brokers != "" {
		go newOrderUpdatesConsumer(db, brokers, os.Getenv("KAFKA_TLS_ENABLED") == "true").Run(ctx)
	} else {
		log.Print("KAFKA_BROKERS unset; order-updates consumer disabled")
	}

	// REST front.
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	api := &httpAPI{svc: svc}
	mux.HandleFunc("/v1/orders", api.handleOrders)
	mux.HandleFunc("/v1/orders/{id}", api.handleCancel)
	mux.HandleFunc("/v1/admin/orders", api.handleAdminOrders) // admin/auditor read (gateway)

	httpAddr := envOr("LISTEN_ADDR", ":8080")
	go func() {
		log.Printf("order REST listening on %s", httpAddr)
		if err := http.ListenAndServe(httpAddr, mux); err != nil {
			log.Fatalf("http server: %v", err)
		}
	}()

	// gRPC front (OrderService from libs/proto/order/v1/order.proto).
	grpcAddr := envOr("GRPC_ADDR", ":9090")
	lis, err := net.Listen("tcp", grpcAddr)
	if err != nil {
		log.Fatalf("listen grpc: %v", err)
	}
	gs := grpc.NewServer()
	orderv1.RegisterOrderServiceServer(gs, &grpcServer{svc: svc})
	log.Printf("order gRPC listening on %s", grpcAddr)

	// Graceful shutdown on SIGTERM/SIGINT (stops gRPC, cancels outbox).
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sigCh
		log.Println("shutdown signal received")
		cancel()
		gs.GracefulStop()
	}()
	log.Fatal(gs.Serve(lis))
}
