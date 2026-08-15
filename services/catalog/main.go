// Package main implements the Catalog Service for the Compute Trading
// Exchange: the reference-data API for GPU types, regions, SLA templates
// (Phase 1A), and standardized futures contracts (Phase 2).
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

// FuturesContract is a standardized forward on (gpu_type, region) with a fixed
// delivery date. One matching-engine order book exists per Symbol.
type FuturesContract struct {
	ID           int64     `json:"id"`
	GPUTypeID    int64     `json:"gpu_type_id"`
	RegionCode   string    `json:"region_code"`
	DeliveryDate string    `json:"delivery_date"` // ISO date YYYY-MM-DD
	TickSize     int64     `json:"tick_size"`     // min price increment, cents
	ContractSize int64     `json:"contract_size"` // GPU-hours per contract
	Status       string    `json:"status"`
	Symbol       string    `json:"symbol"`
	CreatedAt    time.Time `json:"created_at"`
}

// futuresInput is the validated POST /v1/futures-contracts body.
type futuresInput struct {
	GPUTypeID    int64  `json:"gpu_type_id"`
	RegionCode   string `json:"region_code"`
	DeliveryDate string `json:"delivery_date"`
	TickSize     int64  `json:"tick_size"`
	ContractSize int64  `json:"contract_size"`
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

// validateFutures checks a futures-contract create request. It enforces value
// rules only; referential integrity (gpu_type_id, region_code) is enforced by
// the DB foreign keys at insert time.
func validateFutures(in *futuresInput) error {
	if in.GPUTypeID <= 0 {
		return errors.New("gpu_type_id is required")
	}
	if strings.TrimSpace(in.RegionCode) == "" {
		return errors.New("region_code is required")
	}
	d, err := time.Parse("2006-01-02", strings.TrimSpace(in.DeliveryDate))
	if err != nil {
		return errors.New("delivery_date must be an ISO date (YYYY-MM-DD)")
	}
	if !d.After(time.Now().UTC().Truncate(24 * time.Hour)) {
		return errors.New("delivery_date must be in the future")
	}
	if in.TickSize <= 0 {
		return errors.New("tick_size must be greater than 0")
	}
	if in.ContractSize <= 0 {
		return errors.New("contract_size must be greater than 0")
	}
	return nil
}

// futuresSymbol derives the order-book symbol: <GPU>:<REGION>:FUT:<YYYY-MM>.
func futuresSymbol(gpuName, regionCode, deliveryDate string) string {
	month := deliveryDate
	if len(deliveryDate) >= 7 {
		month = deliveryDate[:7] // YYYY-MM
	}
	return gpuName + ":" + regionCode + ":FUT:" + month
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

// handleFuturesContracts serves the Phase 2 futures reference data.
//
//	POST /v1/futures-contracts  create a contract
//	GET  /v1/futures-contracts  list contracts (active only unless ?all=true)
func (s *server) handleFuturesContracts(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		var in futuresInput
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		if err := validateFutures(&in); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		// Resolve the GPU name to build the order-book symbol.
		var gpuName string
		if err := s.db.QueryRowContext(r.Context(),
			`SELECT name FROM gpu_types WHERE id = $1`, in.GPUTypeID,
		).Scan(&gpuName); err != nil {
			writeError(w, http.StatusBadRequest, "unknown gpu_type_id")
			return
		}
		sym := futuresSymbol(gpuName, strings.TrimSpace(in.RegionCode), strings.TrimSpace(in.DeliveryDate))

		var fc FuturesContract
		var delivery time.Time
		err := s.db.QueryRowContext(r.Context(), `
			INSERT INTO futures_contracts
			    (gpu_type_id, region_code, delivery_date, tick_size, contract_size, symbol)
			VALUES ($1, $2, $3, $4, $5, $6)
			RETURNING id, gpu_type_id, region_code, delivery_date, tick_size,
			          contract_size, status, symbol, created_at`,
			in.GPUTypeID, strings.TrimSpace(in.RegionCode), strings.TrimSpace(in.DeliveryDate),
			in.TickSize, in.ContractSize, sym,
		).Scan(&fc.ID, &fc.GPUTypeID, &fc.RegionCode, &delivery, &fc.TickSize,
			&fc.ContractSize, &fc.Status, &fc.Symbol, &fc.CreatedAt)
		if err != nil {
			// FK violation (bad region_code) or unique violation (dup symbol).
			writeError(w, http.StatusConflict, "contract exists or references unknown region")
			return
		}
		fc.DeliveryDate = delivery.Format("2006-01-02")
		writeJSON(w, http.StatusCreated, fc)

	case http.MethodGet:
		query := `SELECT id, gpu_type_id, region_code, delivery_date, tick_size,
		                 contract_size, status, symbol, created_at
		          FROM futures_contracts`
		if r.URL.Query().Get("all") != "true" {
			query += ` WHERE status = 'LISTED' AND delivery_date >= CURRENT_DATE`
		}
		query += ` ORDER BY delivery_date, symbol`
		rows, err := s.db.QueryContext(r.Context(), query)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "query failed")
			return
		}
		defer rows.Close()
		out := []FuturesContract{}
		for rows.Next() {
			var fc FuturesContract
			var delivery time.Time
			if err := rows.Scan(&fc.ID, &fc.GPUTypeID, &fc.RegionCode, &delivery,
				&fc.TickSize, &fc.ContractSize, &fc.Status, &fc.Symbol, &fc.CreatedAt); err != nil {
				writeError(w, http.StatusInternalServerError, "scan failed")
				return
			}
			fc.DeliveryDate = delivery.Format("2006-01-02")
			out = append(out, fc)
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
	mux.HandleFunc("/v1/futures-contracts", s.handleFuturesContracts)

	addr := envOr("LISTEN_ADDR", ":8080")
	log.Printf("catalog service listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}
