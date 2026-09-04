package main

// Audit trail: every authenticated mutating request (and every admin
// request) is recorded in audit_log with who/what/result. Writes are
// asynchronous and best-effort so they never slow or fail the request; the
// table is append-only (no UPDATE/DELETE grants).

import (
	"context"
	"database/sql"
	"log"
	"net"
	"net/http"
	"strings"
	"time"
)

type auditor struct {
	db *sql.DB
	ch chan auditEntry
}

type auditEntry struct {
	account, role, aal, method, path, ip, ua string
	status                                  int
	duration                                time.Duration
}

func newAuditor(db *sql.DB) *auditor {
	if db == nil {
		return nil
	}
	a := &auditor{db: db, ch: make(chan auditEntry, 1024)}
	go a.run()
	return a
}

func (a *auditor) run() {
	for e := range a.ch {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, err := a.db.ExecContext(ctx, `
			INSERT INTO audit_log (account_id, role, aal, method, path, status, ip, user_agent, duration_ms)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
			e.account, e.role, e.aal, e.method, e.path, e.status, e.ip, e.ua, int(e.duration.Milliseconds()))
		cancel()
		if err != nil {
			log.Printf("audit: insert failed: %v", err)
		}
	}
}

// statusRecorder captures the downstream status code.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// clientIP prefers the proxy-supplied address (Render sets X-Forwarded-For).
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return strings.TrimSpace(strings.Split(xff, ",")[0])
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// middleware records mutating requests (non-GET) and every request by an
// admin. Runs after auth so Claims are on the context.
func (a *auditor) middleware(next http.Handler) http.Handler {
	if a == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, ok := claimsFrom(r.Context())
		if !ok || (r.Method == http.MethodGet && c.effectiveRole() != "admin") {
			next.ServeHTTP(w, r)
			return
		}
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()
		next.ServeHTTP(rec, r)
		e := auditEntry{
			account: c.Subject, role: c.effectiveRole(), aal: c.AAL, method: r.Method, path: r.URL.Path,
			ip: clientIP(r), ua: r.UserAgent(), status: rec.status, duration: time.Since(start),
		}
		select {
		case a.ch <- e:
		default:
			log.Printf("audit: queue full, dropped %s %s by %s", e.method, e.path, e.account)
		}
	})
}
