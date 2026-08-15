// trading-engine API skeleton — Compute Trading Exchange.
//
// HTTPS on :8443 (matches the k8s Service targetPort and the edge target
// group's HTTPS:443 health check on /healthz). TLS is a self-signed cert
// generated at boot: the edge ALB does not validate target certificates, so
// this is sufficient for east-west traffic inside the org perimeter.
package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"encoding/json"
	"encoding/pem"
	"log"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "github.com/lib/pq"
)

var startedAt = time.Now().UTC()

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func version() string { return envOr("APP_VERSION", "dev") }

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func main() {
	addr := envOr("LISTEN_ADDR", ":8443")

	mux := http.NewServeMux()

	// Edge target group health check.
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	mux.HandleFunc("/v1/status", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"service":   "trading-engine-api",
			"version":   version(),
			"startedAt": startedAt,
			"uptimeSec": int(time.Since(startedAt).Seconds()),
		})
	})

	// Historical market-data API (Phase 3). Reads the shared trade_ledger.
	var db *sql.DB
	if dsn := os.Getenv("DATABASE_URL"); dsn != "" {
		var err error
		db, err = sql.Open("postgres", dsn)
		if err != nil {
			log.Fatalf("open db: %v", err)
		}
		if err := db.Ping(); err != nil {
			log.Fatalf("ping db: %v", err)
		}
		log.Print("historical API: database connected")
	} else {
		log.Print("DATABASE_URL unset; historical endpoints return 503")
	}
	hist := &historyAPI{db: db}
	mux.HandleFunc("/v1/trades/historical", hist.handleTradesHistorical)
	mux.HandleFunc("/v1/prices/historical", hist.handlePricesHistorical)
	mux.HandleFunc("/v1/prices/index", hist.handlePricesIndex)

	// Parquet offload to S3-compatible storage (R2/MinIO); enabled via S3_BUCKET.
	if off, err := newOffloader(context.Background(), db); err != nil {
		log.Fatalf("offloader: %v", err)
	} else if off != nil {
		mux.HandleFunc("/v1/admin/offload", off.handleOffload)
		go off.Run(context.Background())
	}

	cert, err := selfSignedCert()
	if err != nil {
		log.Fatalf("tls cert: %v", err)
	}

	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		TLSConfig: &tls.Config{
			MinVersion:   tls.VersionTLS12,
			Certificates: []tls.Certificate{cert},
		},
	}

	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		<-sig
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}()

	log.Printf("trading-engine-api %s listening on %s", version(), addr)
	if err := srv.ListenAndServeTLS("", ""); err != http.ErrServerClosed {
		log.Fatalf("serve: %v", err)
	}
}

// selfSignedCert creates an in-memory ECDSA certificate valid for 30 days.
func selfSignedCert() (tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return tls.Certificate{}, err
	}

	tmpl := x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "trading-engine-api"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(30 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:              []string{"localhost", "trading-engine.exchange.svc"},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
		BasicConstraintsValid: true,
	}

	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, err
	}

	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return tls.Certificate{}, err
	}

	return tls.X509KeyPair(
		pemEncode("CERTIFICATE", der),
		pemEncode("EC PRIVATE KEY", keyDER),
	)
}

func pemEncode(blockType string, der []byte) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: blockType, Bytes: der})
}
