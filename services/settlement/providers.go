package main

// Founding-provider applications (public /sell page). The endpoint is
// unauthenticated — anyone with GPUs can apply before creating an account —
// so it is deliberately narrow: strict field limits, a per-IP rate limit,
// and one row per (email, 24 h) so a stuck retry button can't flood the list.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
)

type providerApplication struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Email     string    `json:"email"`
	Company   *string   `json:"company"`
	GPUs      string    `json:"gpus"`
	Location  *string   `json:"location"`
	Notes     *string   `json:"notes"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

var emailRe = regexp.MustCompile(`^[^\s@]+@[^\s@]+\.[^\s@]{2,}$`)

var errApplyDuplicate = errors.New("we already have an application from this email in the last 24 hours")

// ipLimiter: fixed window, N requests per IP per window. Small, in-memory,
// good enough for a form behind a single instance.
type ipLimiter struct {
	mu     sync.Mutex
	hits   map[string][]time.Time
	limit  int
	window time.Duration
}

func newIPLimiter(limit int, window time.Duration) *ipLimiter {
	return &ipLimiter{hits: map[string][]time.Time{}, limit: limit, window: window}
}

func (l *ipLimiter) allow(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	keep := l.hits[ip][:0]
	for _, t := range l.hits[ip] {
		if now.Sub(t) < l.window {
			keep = append(keep, t)
		}
	}
	if len(keep) >= l.limit {
		l.hits[ip] = keep
		return false
	}
	l.hits[ip] = append(keep, now)
	if len(l.hits) > 10_000 { // crude memory bound
		l.hits = map[string][]time.Time{}
	}
	return true
}

var applyLimiter = newIPLimiter(5, time.Hour)

func requestIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return strings.TrimSpace(strings.Split(xff, ",")[0])
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func optional(s string, max int) *string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	if len(s) > max {
		s = s[:max]
	}
	return &s
}

func (s *settlementService) createProviderApplication(ctx context.Context, a *providerApplication, ip string) error {
	var recent int
	if err := s.db.QueryRowContext(ctx, `
		SELECT count(*) FROM provider_applications
		WHERE lower(email) = lower($1) AND created_at > now() - interval '24 hours'`, a.Email).Scan(&recent); err != nil {
		return err
	}
	if recent > 0 {
		return errApplyDuplicate
	}
	a.ID = uuidv4()
	a.Status = "NEW"
	return s.db.QueryRowContext(ctx, `
		INSERT INTO provider_applications (id, name, email, company, gpus, location, notes, source_ip, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING created_at`,
		a.ID, a.Name, a.Email, a.Company, a.GPUs, a.Location, a.Notes, ip, a.Status).Scan(&a.CreatedAt)
}

func (s *settlementService) listProviderApplications(ctx context.Context, limit int) ([]providerApplication, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id::text, name, email, company, gpus, location, notes, status, created_at
		FROM provider_applications ORDER BY created_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []providerApplication{}
	for rows.Next() {
		var a providerApplication
		if err := rows.Scan(&a.ID, &a.Name, &a.Email, &a.Company, &a.GPUs, &a.Location, &a.Notes, &a.Status, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// providerApplicationEmail looks up the email for a provider_applications
// row, used to mirror a status change onto the matching HubSpot Contact.
func (s *settlementService) providerApplicationEmail(ctx context.Context, id string) (string, error) {
	var email string
	err := s.db.QueryRowContext(ctx, `SELECT email FROM provider_applications WHERE id = $1::uuid`, id).Scan(&email)
	return email, err
}

func (s *settlementService) setProviderApplicationStatus(ctx context.Context, id, status string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE provider_applications SET status = $2 WHERE id = $1::uuid`, id, status)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// --- HTTP -------------------------------------------------------------------

// handleProviderApply serves POST /v1/providers/apply (public; gateway does
// not attach an identity). Body: {name, email, company?, gpus, location?, notes?}.
func (a *httpAPI) handleProviderApply(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	ip := requestIP(r)
	if !applyLimiter.allow(ip) {
		writeError(w, http.StatusTooManyRequests, "too many applications from this address; try again later")
		return
	}
	var in struct {
		Name, Email, Company, GPUs, Location, Notes string
		Website                                    string `json:"website"` // honeypot: bots fill it, humans never see it
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if in.Website != "" {
		writeJSON(w, http.StatusCreated, map[string]any{"ok": true}) // silently drop bots
		return
	}
	app := providerApplication{
		Name:     strings.TrimSpace(in.Name),
		Email:    strings.TrimSpace(strings.ToLower(in.Email)),
		Company:  optional(in.Company, 200),
		GPUs:     strings.TrimSpace(in.GPUs),
		Location: optional(in.Location, 200),
		Notes:    optional(in.Notes, 2000),
	}
	switch {
	case app.Name == "" || len(app.Name) > 200:
		writeError(w, http.StatusBadRequest, "name is required")
		return
	case !emailRe.MatchString(app.Email) || len(app.Email) > 254:
		writeError(w, http.StatusBadRequest, "a valid email is required")
		return
	case app.GPUs == "" || len(app.GPUs) > 1000:
		writeError(w, http.StatusBadRequest, "tell us what GPUs you have")
		return
	}
	if err := a.svc.createProviderApplication(r.Context(), &app, ip); errors.Is(err, errApplyDuplicate) {
		writeError(w, http.StatusConflict, err.Error())
		return
	} else if err != nil {
		log.Printf("provider apply: %v", err)
		writeError(w, http.StatusInternalServerError, "could not save application")
		return
	}
	log.Printf("provider application %s from %s (%s)", app.ID, app.Email, ip)
	a.svc.hubspot.syncProviderApplication(app)
	writeJSON(w, http.StatusCreated, map[string]any{"ok": true, "id": app.ID})
}

// handleAdminProviders serves GET /v1/admin/providers (admin via gateway).
func (a *httpAPI) handleAdminProviders(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if _, ok := accountID(w, r); !ok {
		return
	}
	apps, err := a.svc.listProviderApplications(r.Context(), 200)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	writeJSON(w, http.StatusOK, apps)
}

// handleAdminProviderStatus serves POST /v1/admin/providers/{id} {status}.
func (a *httpAPI) handleAdminProviderStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if _, ok := accountID(w, r); !ok {
		return
	}
	var in struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	switch in.Status {
	case "NEW", "CONTACTED", "ONBOARDED", "DECLINED":
	default:
		writeError(w, http.StatusBadRequest, "status must be NEW, CONTACTED, ONBOARDED or DECLINED")
		return
	}
	id := r.PathValue("id")
	if err := a.svc.setProviderApplicationStatus(r.Context(), id, in.Status); errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "not found")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "update failed")
		return
	}
	if email, err := a.svc.providerApplicationEmail(r.Context(), id); err == nil {
		a.svc.hubspot.syncProviderStatus(email, in.Status)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
