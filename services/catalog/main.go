// Package main implements the Catalog Service for the Compute Trading
// Exchange: the reference-data API for GPU types, regions, and SLA
// templates (Phase 1A).
//
// Config via env:
//   - LISTEN_ADDR   HTTP listen address (default ":8080")
//   - DATABASE_URL  Aurora Postgres DSN (required)
//
// Migrations are golang-migrate style SQL files under db/migrations/;
// reference data is seeded with db/seed.sql.
package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	_ "github.com/lib/pq"
)

// GPUType is a tradeable GPU SKU (e.g. H100).
type GPUType struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	VRAMGB    int       `json:"vram_gb"`
	CreatedAt time.Time `json:"created_at"`
}

// Region is a deployable region (e.g. us-east-1).
type Region struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

// validateGPUType checks the POST /v1/gpu-types request body.
func validateGPUType(name string, vramGB int) error {
	if strings.TrimSpace(name) == "" {
		return errors.New("name is required")
	}
	if vramGB <= 0 {
		return errors.New("vram_gb must be greater than 0")
	}
	return nil
}

// validateRegion checks the POST /v1/regions request body.
func validateRegion(code, name string) error {
	if strings.TrimSpace(code) == "" {
		return errors.New("code is required")
	}
	if strings.TrimSpace(name) == "" {
		return errors.New("name is required")
	}
	return nil
}

type server struct {
	db *sql.DB
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func (s *server) handleGPUTypes(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		var req struct {
			Name   string `json:"name"`
			VRAMGB int    `json:"vram_gb"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		if err := validateGPUType(req.Name, req.VRAMGB); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		var g GPUType
		err := s.db.QueryRowContext(r.Context(),
			`INSERT INTO gpu_types (name, vram_gb) VALUES ($1, $2)
			 RETURNING id, name, vram_gb, created_at`,
			strings.TrimSpace(req.Name), req.VRAMGB,
		).Scan(&g.ID, &g.Name, &g.VRAMGB, &g.CreatedAt)
		if err != nil {
			writeError(w, http.StatusConflict, "gpu type already exists or insert failed")
			return
		}
		writeJSON(w, http.StatusCreated, g)
	case http.MethodGet:
		rows, err := s.db.QueryContext(r.Context(),
			`SELECT id, name, vram_gb, created_at FROM gpu_types ORDER BY name`)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "query failed")
			return
		}
		defer rows.Close()
		out := []GPUType{}
		for rows.Next() {
			var g GPUType
			if err := rows.Scan(&g.ID, &g.Name, &g.VRAMGB, &g.CreatedAt); err != nil {
				writeError(w, http.StatusInternalServerError, "scan failed")
				return
			}
			out = append(out, g)
		}
		writeJSON(w, http.StatusOK, out)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *server) handleRegions(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		var req Region
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		if err := validateRegion(req.Code, req.Name); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		_, err := s.db.ExecContext(r.Context(),
			`INSERT INTO regions (code, name) VALUES ($1, $2)`,
			strings.TrimSpace(req.Code), strings.TrimSpace(req.Name))
		if err != nil {
			writeError(w, http.StatusConflict, "region already exists or insert failed")
			return
		}
		writeJSON(w, http.StatusCreated, req)
	case http.MethodGet:
		rows, err := s.db.QueryContext(r.Context(),
			`SELECT code, name FROM regions ORDER BY code`)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "query failed")
			return
		}
		defer rows.Close()
		out := []Region{}
		for rows.Next() {
			var rg Region
			if err := rows.Scan(&rg.Code, &rg.Name); err != nil {
				writeError(w, http.StatusInternalServerError, "scan failed")
				return
			}
			out = append(out, rg)
		}
		writeJSON(w, http.StatusOK, out)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func main() {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		log.Fatal("DATABASE_URL is required")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		log.Fatalf("ping db: %v", err)
	}

	s := &server{db: db}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("/v1/gpu-types", s.handleGPUTypes)
	mux.HandleFunc("/v1/regions", s.handleRegions)

	addr := envOr("LISTEN_ADDR", ":8080")
	log.Printf("catalog service listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}
