package main

import (
	"database/sql"
	"log"
	"net"
	"net/http"
	"os"

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
	if brokers := os.Getenv("KAFKA_BROKERS"); brokers != "" {
		kp := newKafkaPublisher(brokers, os.Getenv("KAFKA_TLS_ENABLED") == "true")
		defer kp.Close()
		pub = kp
		log.Printf("publishing order events to Kafka brokers %q topic %q", brokers, ordersTopic)
	} else {
		log.Print("KAFKA_BROKERS unset; using log-only publisher")
	}

	svc := &orderService{db: db, pub: pub}

	// REST front.
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	api := &httpAPI{svc: svc}
	mux.HandleFunc("/v1/orders", api.handleOrders)
	mux.HandleFunc("/v1/orders/{id}", api.handleCancel)

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
	log.Fatal(gs.Serve(lis))
}
