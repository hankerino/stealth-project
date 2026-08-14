package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"log"
	"os"
	"strings"
	"time"

	"github.com/segmentio/kafka-go"
)

// kafkaPublisher publishes JSON events to MSK via segmentio/kafka-go.
//
// TLS modes:
//   - KAFKA_TLS_ENABLED=false (default): plaintext, no TLS (local dev).
//   - KAFKA_TLS_ENABLED=true, no client cert: TLS to MSK's TLS listener (9094)
//     without mutual auth — for brokers that allow unauthenticated TLS.
//   - KAFKA_TLS_ENABLED=true + KAFKA_CLIENT_CERT_FILE + KAFKA_CLIENT_KEY_FILE
//     + KAFKA_CA_CERT_FILE: mutual TLS (mTLS) — MSK requires a client
//     certificate signed by the AWS Private CA. This is the production path.
type kafkaPublisher struct {
	w *kafka.Writer
}

func newKafkaPublisher(brokers string, tlsEnabled bool) *kafkaPublisher {
	var tlsCfg *tls.Config
	if tlsEnabled {
		tlsCfg = buildTLSConfig()
	}
	return &kafkaPublisher{w: &kafka.Writer{
		Addr:         kafka.TCP(strings.Split(brokers, ",")...),
		Topic:        ordersTopic,
		Balancer:     &kafka.Hash{}, // route by message key (symbol)
		RequiredAcks: kafka.RequireAll,
		Transport: &kafka.Transport{
			TLS:         tlsCfg,
			DialTimeout: 10 * time.Second,
			IdleTimeout: 30 * time.Second,
		},
	}}
}

// buildTLSConfig assembles a *tls.Config from the KAFKA_* env vars.
// If client cert/key files are set, mTLS is configured; otherwise it's
// server-only TLS.
func buildTLSConfig() *tls.Config {
	cfg := &tls.Config{MinVersion: tls.VersionTLS12}

	// Load CA certificate (for verifying the broker's cert).
	if caFile := os.Getenv("KAFKA_CA_CERT_FILE"); caFile != "" {
		caPEM, err := os.ReadFile(caFile)
		if err != nil {
			log.Fatalf("read KAFKA_CA_CERT_FILE %q: %v", caFile, err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caPEM) {
			log.Fatalf("no valid certs in KAFKA_CA_CERT_FILE %q", caFile)
		}
		cfg.RootCAs = pool
	}

	// Load client certificate + key for mTLS.
	certFile := os.Getenv("KAFKA_CLIENT_CERT_FILE")
	keyFile := os.Getenv("KAFKA_CLIENT_KEY_FILE")
	if certFile != "" && keyFile != "" {
		cert, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			log.Fatalf("load client cert/key: %v", err)
		}
		cfg.Certificates = []tls.Certificate{cert}
		log.Printf("kafka mTLS: loaded client cert from %s", certFile)
	}

	return cfg
}

func (p *kafkaPublisher) Publish(ctx context.Context, key string, event any) error {
	payload, err := marshalEvent(event)
	if err != nil {
		return err
	}
	return p.w.WriteMessages(ctx, kafka.Message{Key: []byte(key), Value: payload})
}

func (p *kafkaPublisher) Close() error { return p.w.Close() }

// nopPublisher logs events instead of publishing; used when KAFKA_BROKERS
// is unset so the service stays runnable in local dev without Kafka.
type nopPublisher struct{}

func (nopPublisher) Publish(_ context.Context, key string, event any) error {
	payload, err := marshalEvent(event)
	if err != nil {
		return err
	}
	log.Printf("KAFKA_BROKERS unset; dropping event key=%s payload=%s", key, payload)
	return nil
}
