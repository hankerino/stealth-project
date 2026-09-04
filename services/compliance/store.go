package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"time"
)

// complianceService persists alerts and publishes AlertTriggered.
type complianceService struct {
	db    *sql.DB
	pub   *kafkaProducer // nil in dev (no Kafka)
	spoof *spoofDetector
}

// AlertTriggered matches libs/schemas/AlertTriggered.avsc (topic: compliance-alerts, key user_id).
type AlertTriggered struct {
	EventID          string  `json:"event_id"`
	AlertID          string  `json:"alert_id"`
	Rule             string  `json:"rule"`
	Severity         string  `json:"severity"`
	UserID           string  `json:"user_id"`
	Symbol           string  `json:"symbol"`
	RefID            *string `json:"ref_id"`
	Details          string  `json:"details"`
	OccurredAtUnixMs int64   `json:"occurred_at_unix_ms"`
}

// record inserts the alert (idempotent on (rule, ref_id): a Kafka redelivery
// of the same trade/order is a no-op) and emits AlertTriggered on first insert.
func (s *complianceService) record(ctx context.Context, a *Alert) error {
	if a == nil {
		return nil
	}
	alertID := uuidv4()
	var ref sql.NullString
	if a.RefID != "" {
		ref = sql.NullString{String: a.RefID, Valid: true}
	}
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO surveillance_alerts (alert_id, rule, severity, user_id, symbol, ref_id, details)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT DO NOTHING`,
		alertID, a.Rule, a.Severity, a.UserID, a.Symbol, ref, a.detailsJSON())
	if err != nil {
		return fmt.Errorf("insert alert: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil // duplicate delivery
	}
	log.Printf("ALERT %s (%s) user=%s symbol=%s ref=%s %s", a.Rule, a.Severity, a.UserID, a.Symbol, a.RefID, a.detailsJSON())
	if s.pub != nil {
		ev := AlertTriggered{
			EventID: uuidv4(), AlertID: alertID, Rule: a.Rule, Severity: a.Severity,
			UserID: a.UserID, Symbol: a.Symbol, Details: string(a.detailsJSON()),
			OccurredAtUnixMs: time.Now().UnixMilli(),
		}
		if ref.Valid {
			ev.RefID = &ref.String
		}
		if b, err := json.Marshal(ev); err == nil {
			if err := s.pub.publish(a.UserID, b); err != nil {
				log.Printf("compliance: publish AlertTriggered: %v", err)
			}
		}
	}
	return nil
}

// alertRow is the admin-facing view of surveillance_alerts.
type alertRow struct {
	AlertID   string          `json:"alert_id"`
	Rule      string          `json:"rule"`
	Severity  string          `json:"severity"`
	UserID    string          `json:"user_id"`
	Symbol    string          `json:"symbol"`
	RefID     *string         `json:"ref_id"`
	Details   json.RawMessage `json:"details"`
	Status    string          `json:"status"`
	CreatedAt time.Time       `json:"created_at"`
}

// listAlerts returns the newest alerts, optionally filtered by status.
func (s *complianceService) listAlerts(ctx context.Context, status string, limit int) ([]alertRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT alert_id, rule, severity, user_id, symbol, ref_id, details, status, created_at
		FROM surveillance_alerts
		WHERE ($1 = '' OR status = $1)
		ORDER BY created_at DESC
		LIMIT $2`, status, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []alertRow{}
	for rows.Next() {
		var r alertRow
		var ref sql.NullString
		if err := rows.Scan(&r.AlertID, &r.Rule, &r.Severity, &r.UserID, &r.Symbol, &ref, &r.Details, &r.Status, &r.CreatedAt); err != nil {
			return nil, err
		}
		if ref.Valid {
			r.RefID = &ref.String
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// setAlertStatus moves an alert to reviewed/dismissed. Returns sql.ErrNoRows if unknown.
func (s *complianceService) setAlertStatus(ctx context.Context, alertID, status string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE surveillance_alerts SET status = $2 WHERE alert_id = $1::uuid`, alertID, status)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}
