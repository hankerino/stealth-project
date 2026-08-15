package main

// Kafka wiring for the risk service: a single consumer group reading both the
// `trades` and `order-updates` topics to maintain positions, and a producer
// for PositionUpdated events. segmentio/kafka-go with optional mTLS.

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
	positionTopic     = "risk-events"
	consumerGroup     = "risk"

	maxApplyBackoff = 30 * time.Second
)

// buildTLSConfig builds a *tls.Config for mTLS when KAFKA_TLS_ENABLED=true.
// Loads client cert/key/CA from env paths; falls back to TLS without client
// auth if those are unset.
func buildTLSConfig() *tls.Config {
	cfg := &tls.Config{MinVersion: tls.VersionTLS12}
	cert, key, ca := os.Getenv("KAFKA_CLIENT_CERT"), os.Getenv("KAFKA_CLIENT_KEY"), os.Getenv("KAFKA_CA_CERT")
	if cert != "" && key != "" {
		c, err := tls.LoadX509KeyPair(cert, key)
		if err != nil {
			log.Fatalf("load mTLS client cert/key: %v", err)
		}
		cfg.Certificates = []tls.Certificate{c}
	}
	if ca != "" {
		b, err := os.ReadFile(ca)
		if err != nil {
			log.Fatalf("read CA cert: %v", err)
		}
		pool := x509.NewCertPool()
		pool.AppendCertsFromPEM(b)
		cfg.RootCAs = pool
	}
	return cfg
}

// kafkaConsumer reads trades + order-updates for one consumer group.
type kafkaConsumer struct {
	r *kafka.Reader
}

func newKafkaConsumer(brokers string, tlsEnabled bool) *kafkaConsumer {
	dialer := &kafka.Dialer{Timeout: 10 * time.Second, DualStack: true}
	if tlsEnabled {
		dialer.TLS = buildTLSConfig()
	}
	r := kafka.NewReader(kafka.ReaderConfig{
		Brokers:     strings.Split(brokers, ","),
		GroupID:     consumerGroup,
		GroupTopics: []string{tradesTopic, orderUpdatesTopic},
		MinBytes:    1,
		MaxBytes:    10e6,
		Dialer:      dialer,
	})
	return &kafkaConsumer{r: r}
}

// run consumes with at-least-once semantics: FetchMessage + manual commit,
// committing only after the message is applied. Trades update positions;
// order-updates are tracked for open-order margin reservation (see handler).
func (c *kafkaConsumer) run(svc *riskService) error {
	for {
		fctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		msg, err := c.r.FetchMessage(fctx)
		cancel()
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				continue
			}
			return err
		}

		if err := c.handle(svc, msg); err != nil {
			// Transient error (e.g. DB down): retry the same message with
			// bounded backoff rather than committing past it.
			backoff := 500 * time.Millisecond
			for err != nil {
				log.Printf("risk: apply %s@%d failed: %v — retry in %s", msg.Topic, msg.Offset, err, backoff)
				time.Sleep(backoff)
				if backoff < maxApplyBackoff {
					backoff *= 2
				}
				err = c.handle(svc, msg)
			}
		}
		if err := c.r.CommitMessages(context.Background(), msg); err != nil {
			log.Printf("risk: commit %s@%d: %v", msg.Topic, msg.Offset, err)
		}
	}
}

// handle dispatches a message by topic. Poison (unparseable) messages are
// skipped (nil) so they don't wedge the partition.
func (c *kafkaConsumer) handle(svc *riskService, msg kafka.Message) error {
	switch msg.Topic {
	case tradesTopic:
		var t TradeExecuted
		if err := json.Unmarshal(msg.Value, &t); err != nil {
			log.Printf("risk: poison trade @%d: %v — skipping", msg.Offset, err)
			return nil
		}
		return svc.applyTrade(context.Background(), &t)
	case orderUpdatesTopic:
		var u OrderUpdated
		if err := json.Unmarshal(msg.Value, &u); err != nil {
			log.Printf("risk: poison order-update @%d: %v — skipping", msg.Offset, err)
			return nil
		}
		// Open-order margin reservation is a documented follow-up; positions
		// are maintained authoritatively from `trades`. Log for now.
		log.Printf("risk: order-update %s %s -> %s (filled=%d remaining=%d)",
			u.OrderID, u.Symbol, u.NewStatus, u.FilledQuantity, u.RemainingQuantity)
		return nil
	default:
		return nil
	}
}

// kafkaProducer publishes PositionUpdated events.
type kafkaProducer struct {
	w *kafka.Writer
}

func newKafkaProducer(brokers string, tlsEnabled bool) *kafkaProducer {
	var transport *kafka.Transport
	if tlsEnabled {
		transport = &kafka.Transport{TLS: buildTLSConfig(), DialTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second}
	}
	return &kafkaProducer{w: &kafka.Writer{
		Addr:         kafka.TCP(strings.Split(brokers, ",")...),
		Topic:        positionTopic,
		Balancer:     &kafka.Hash{},
		RequiredAcks: kafka.RequireAll,
		Transport:    transport,
	}}
}

func (p *kafkaProducer) publish(key string, payload []byte) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return p.w.WriteMessages(ctx, kafka.Message{Key: []byte(key), Value: payload})
}

func (p *kafkaProducer) close() error { return p.w.Close() }
