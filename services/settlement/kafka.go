package main

// Kafka consumer for the trades topic + producer for SettlementFailed events.
// Uses segmentio/kafka-go with mTLS support.

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"log"
	"os"
	"strings"
	"time"

	"github.com/segmentio/kafka-go"
)

const (
	tradesTopic          = "trades"
	settlementFailedTopic = "sla-breach-events"
	consumerGroup        = "settlement"
)

// tlsConfig builds a *tls.Config for mTLS if KAFKA_TLS_ENABLED is true.
// Client cert/key/CA are loaded from env-specified paths (KAFKA_CLIENT_CERT,
// KAFKA_CLIENT_KEY, KAFKA_CA_CERT). If those are unset, falls back to TLS
// without client auth (for dev environments without mTLS).
func buildTLSConfig() *tls.Config {
	cfg := &tls.Config{MinVersion: tls.VersionTLS12}

	certPath := os.Getenv("KAFKA_CLIENT_CERT")
	keyPath := os.Getenv("KAFKA_CLIENT_KEY")
	caPath := os.Getenv("KAFKA_CA_CERT")

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
		caPool := x509.NewCertPool()
		caPool.AppendCertsFromPEM(caBytes)
		cfg.RootCAs = caPool
	}

	return cfg
}

// kafkaConsumer reads TradeExecuted events from the trades topic.
type kafkaConsumer struct {
	r *kafka.Reader
}

func newKafkaConsumer(brokers string, tlsEnabled bool) (*kafkaConsumer, error) {
	dialer := &kafka.Dialer{
		Timeout:   10 * time.Second,
		Deadline:  time.Now().Add(30 * time.Second),
	}
	if tlsEnabled {
		dialer.TLS = buildTLSConfig()
	}
	r := kafka.NewReader(kafka.ReaderConfig{
		Brokers:  strings.Split(brokers, ","),
		Topic:    tradesTopic,
		GroupID:  consumerGroup,
		MinBytes: 1,
		MaxBytes: 10e6,
		Dialer:   dialer,
	})
	return &kafkaConsumer{r: r}, nil
}

func (c *kafkaConsumer) run(svc *settlementService) error {
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		msg, err := c.r.ReadMessage(ctx)
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				continue // timeout, retry
			}
			return err
		}

		var trade TradeExecuted
		if err := json.Unmarshal(msg.Value, &trade); err != nil {
			log.Printf("unmarshal trade: %v (offset %d)", err, msg.Offset)
			continue
		}
		if err := svc.settleTrade(context.Background(), &trade); err != nil {
			log.Printf("settle trade %s: %v", trade.TradeID, err)
		}
	}
}

// kafkaProducer publishes SettlementFailed events.
type kafkaProducer struct {
	w *kafka.Writer
}

func newKafkaProducer(brokers string, tlsEnabled bool) (*kafkaProducer, error) {
	var transport *kafka.Transport
	if tlsEnabled {
		transport = &kafka.Transport{
			TLS:         buildTLSConfig(),
			DialTimeout: 10 * time.Second,
			IdleTimeout: 30 * time.Second,
		}
	}
	w := &kafka.Writer{
		Addr:         kafka.TCP(strings.Split(brokers, ",")...),
		Topic:        settlementFailedTopic,
		Balancer:     &kafka.Hash{},
		RequiredAcks: kafka.RequireAll,
		Transport:    transport,
	}
	return &kafkaProducer{w: w}, nil
}

func (p *kafkaProducer) publish(key string, payload []byte) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return p.w.WriteMessages(ctx, kafka.Message{Key: []byte(key), Value: payload})
}

func (p *kafkaProducer) close() error { return p.w.Close() }
