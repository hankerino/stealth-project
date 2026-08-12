package main

import (
	"context"
	"crypto/tls"
	"log"
	"strings"
	"time"

	"github.com/segmentio/kafka-go"
)

// kafkaPublisher publishes JSON events to MSK via segmentio/kafka-go.
//
// NOTE: MSK TLS client-certificate provisioning (mutual TLS) is a
// follow-up. When KAFKA_TLS_ENABLED=true we dial the MSK TLS listener
// (port 9094) with TLS but without a client certificate; until cert
// provisioning lands, brokers must allow unauthenticated TLS clients
// (or run within the VPC on the plaintext listener with TLS disabled).
type kafkaPublisher struct {
	w *kafka.Writer
}

func newKafkaPublisher(brokers string, tlsEnabled bool) *kafkaPublisher {
	var tlsCfg *tls.Config
	if tlsEnabled {
		tlsCfg = &tls.Config{MinVersion: tls.VersionTLS12}
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
