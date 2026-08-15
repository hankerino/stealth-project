package main

// Kafka consumer for the trades topic + producer for SettlementFailed events.
// Uses segmentio/kafka-go with mTLS support.

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
	tradesTopic           = "trades"
	settlementFailedTopic = "sla-breach-events"
	consumerGroup         = "settlement"

	maxSettleBackoff = 30 * time.Second
)

// buildTLSConfig builds a *tls.Config for mTLS if KAFKA_TLS_ENABLED is true.
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
		Timeout:  10 * time.Second,
		Deadline: time.Now().Add(30 * time.Second),
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
		// CommitInterval left at 0: offsets are committed synchronously via
		// CommitMessages only after a trade is durably settled (below).
	})
	return &kafkaConsumer{r: r}, nil
}

// run consumes the trades topic with at-least-once, in-order settlement.
//
// Offset handling: we FetchMessage (which does NOT auto-commit) and only
// CommitMessages after settleTrade succeeds. settleTrade returns nil for both
// business outcomes (SETTLED and FAILED) — those commit immediately. It
// returns an error only for transient infrastructure failures (e.g. the DB is
// unreachable); in that case we retry the SAME trade with bounded backoff
// instead of committing, so no trade is ever skipped or lost. Because the
// partition key is the symbol, this preserves per-book ordering. Malformed
// ("poison") messages that can never be parsed are committed and skipped so
// they don't wedge the partition.
func (c *kafkaConsumer) run(svc *settlementService) error {
	for {
		fetchCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		msg, err := c.r.FetchMessage(fetchCtx)
		cancel()
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				continue // no message this interval; poll again
			}
			return err
		}

		var trade TradeExecuted
		if err := json.Unmarshal(msg.Value, &trade); err != nil {
			log.Printf("settlement: poison message at offset %d: %v — skipping", msg.Offset, err)
			c.commit(msg)
			continue
		}

		// Settle with bounded exponential backoff on transient errors. Business
		// outcomes (SETTLED/FAILED) return nil and break immediately.
		backoff := 500 * time.Millisecond
		for {
			if err := svc.settleTrade(context.Background(), &trade); err == nil {
				break
			} else {
				log.Printf("settlement: trade %s transient error: %v — retrying in %s (offset %d not committed)",
					trade.TradeID, err, backoff, msg.Offset)
				time.Sleep(backoff)
				if backoff < maxSettleBackoff {
					backoff *= 2
				}
			}
		}
		c.commit(msg)
	}
}

// commit synchronously commits the message's offset for the consumer group.
func (c *kafkaConsumer) commit(msg kafka.Message) {
	if err := c.r.CommitMessages(context.Background(), msg); err != nil {
		log.Printf("settlement: commit offset %d: %v", msg.Offset, err)
	}
}

// kafkaProducer publishes SettlementFailed events.
type kafkaProducer struct {
	w *kafka.Writer
}

func newKafkaProducer(brokers string, tlsEnabled bool, topic string) (*kafkaProducer, error) {
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
		Topic:        topic,
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
