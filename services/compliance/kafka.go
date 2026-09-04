package main

// Kafka wiring: one reader per topic (trades, order-updates) in its own
// consumer group, at-least-once with synchronous commits after the alert is
// durable (same discipline as settlement/kafka.go), plus a producer for
// AlertTriggered on `compliance-alerts`.

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"log"
	"os"
	"strings"
	"time"

	"github.com/segmentio/kafka-go"
)

const (
	tradesTopic       = "trades"
	orderUpdatesTopic = "order-updates"
	alertsTopic       = "compliance-alerts"
	consumerGroup     = "compliance"
	maxBackoff        = 30 * time.Second
)

func buildTLSConfig() *tls.Config {
	cfg := &tls.Config{MinVersion: tls.VersionTLS12}
	certPath, keyPath, caPath := os.Getenv("KAFKA_CLIENT_CERT"), os.Getenv("KAFKA_CLIENT_KEY"), os.Getenv("KAFKA_CA_CERT")
	if certPath != "" && keyPath != "" {
		cert, err := tls.LoadX509KeyPair(certPath, keyPath)
		if err != nil {
			log.Fatalf("load mTLS client cert/key: %v", err)
		}
		cfg.Certificates = []tls.Certificate{cert}
	}
	if caPath != "" {
		caBytes, err := os.ReadFile(caPath)
		if err != nil {
			log.Fatalf("read CA cert: %v", err)
		}
		pool := x509.NewCertPool()
		pool.AppendCertsFromPEM(caBytes)
		cfg.RootCAs = pool
	}
	return cfg
}

func newReader(brokers, topic string, tlsEnabled bool) *kafka.Reader {
	dialer := &kafka.Dialer{Timeout: 10 * time.Second} // no absolute Deadline (wedges reconnects)
	if tlsEnabled {
		dialer.TLS = buildTLSConfig()
	}
	return kafka.NewReader(kafka.ReaderConfig{
		Brokers:  strings.Split(brokers, ","),
		Topic:    topic,
		GroupID:  consumerGroup + "-" + topic,
		MinBytes: 1,
		MaxBytes: 10e6,
		Dialer:   dialer,
		ErrorLogger: kafka.LoggerFunc(func(msg string, args ...interface{}) {
			log.Printf("compliance kafka["+topic+"]: "+msg, args...)
		}),
	})
}

// consume runs one topic loop: parse -> handle (with bounded retry on
// transient errors) -> commit. Poison messages are committed and skipped.
func consume(r *kafka.Reader, handle func(context.Context, kafka.Message) error) error {
	for {
		fetchCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		msg, err := r.FetchMessage(fetchCtx)
		cancel()
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				continue
			}
			return err
		}
		backoff := 500 * time.Millisecond
		for {
			if err := handle(context.Background(), msg); err == nil {
				break
			} else {
				log.Printf("compliance: offset %d transient error: %v — retrying in %s", msg.Offset, err, backoff)
				time.Sleep(backoff)
				if backoff < maxBackoff {
					backoff *= 2
				}
			}
		}
		if err := r.CommitMessages(context.Background(), msg); err != nil {
			log.Printf("compliance: commit offset %d: %v", msg.Offset, err)
		}
	}
}

func (s *complianceService) handleTrade(ctx context.Context, msg kafka.Message) error {
	var t TradeExecuted
	if err := json.Unmarshal(msg.Value, &t); err != nil {
		log.Printf("compliance: poison trade @%d: %v — skipping", msg.Offset, err)
		return nil
	}
	return s.record(ctx, checkWashTrade(&t))
}

func (s *complianceService) handleOrderUpdate(ctx context.Context, msg kafka.Message) error {
	var u OrderUpdated
	if err := json.Unmarshal(msg.Value, &u); err != nil {
		log.Printf("compliance: poison order-update @%d: %v — skipping", msg.Offset, err)
		return nil
	}
	at := time.Now()
	if u.OccurredAtUnixMs > 0 {
		at = time.UnixMilli(u.OccurredAtUnixMs)
	}
	return s.record(ctx, s.spoof.observe(&u, at))
}

type kafkaProducer struct{ w *kafka.Writer }

func newKafkaProducer(brokers string, tlsEnabled bool) *kafkaProducer {
	w := &kafka.Writer{
		Addr:         kafka.TCP(strings.Split(brokers, ",")...),
		Topic:        alertsTopic,
		Balancer:     &kafka.Hash{},
		RequiredAcks: kafka.RequireAll,
	}
	if tlsEnabled { // only when TLS: a nil *Transport in the interface nil-derefs on publish
		w.Transport = &kafka.Transport{TLS: buildTLSConfig(), DialTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second}
	}
	return &kafkaProducer{w: w}
}

func (p *kafkaProducer) publish(key string, payload []byte) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return p.w.WriteMessages(ctx, kafka.Message{Key: []byte(key), Value: payload})
}
