// Command migrate applies all Compute Trading Exchange SQL migrations
// against DATABASE_URL. Pure Go (database/sql + lib/pq) — no psql, no apt,
// no external tools, so it runs as a Render preDeployCommand on the native
// Go runtime (which denies root).
//
// It mirrors the retired scripts/render-migrate.sh exactly: the four
// migration dirs (catalog, order, settlement, risk — every *.up.sql in
// name order), the catalog seed, and the telemetry DDL. Everything is
// idempotent (CREATE TABLE/INDEX IF NOT EXISTS, seed ON CONFLICT DO
// NOTHING), so it is safe on every deploy.
//
// Paths are resolved from the repo root, located by walking up from the
// working directory until a `services/` directory is found — so it works
// both from the repo root and via `cd scripts/migrate && go run .`
// (Render's preDeployCommand uses the latter; Go module resolution needs
// the module dir as cwd).
package main

import (
	"database/sql"
	"log"
	"os"
	"path/filepath"

	_ "github.com/lib/pq"
)

var migrationDirs = []string{
	"services/catalog/db/migrations",
	"services/order/db/migrations",
	"services/settlement/db/migrations",
	"services/risk/db/migrations",
	"services/compliance/db/migrations",
	"services/api/db/migrations",
	"services/telemetry-verifier/db/migrations",
}

const seedFile = "services/catalog/db/seed.sql"

// telemetryDDL: the tables NOT owned by a formal migration. seller_nodes is
// owned by services/telemetry-verifier/db/migrations (applied above); the
// verifier also creates seller_node_metrics itself at startup.
const telemetryDDL = `
CREATE TABLE IF NOT EXISTS contract_sla_logs (
  contract_id uuid NOT NULL,
  node_id uuid NOT NULL,
  uptime_percentage numeric,
  sla_status varchar(32),
  breach_reason text,
  "timestamp" timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS seller_node_metrics (
  id bigserial PRIMARY KEY,
  node_id uuid NOT NULL,
  contract_id uuid,
  window_start timestamptz NOT NULL,
  uptime_pct numeric,
  avg_utilization_pct numeric,
  health_score numeric,
  created_at timestamptz NOT NULL DEFAULT now()
);
`

func execFile(db *sql.DB, path string) error {
	sql, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	// Whole-file exec: pq's simple-query protocol accepts multiple
	// statements in one call (no placeholders involved).
	_, err = db.Exec(string(sql))
	return err
}

// repoRoot walks up from the working directory to find the repo root
// (the directory containing services/). Fails loudly — a silently wrong
// root would skip migrations and report success.
func repoRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		log.Fatalf("getwd: %v", err)
	}
	for i := 0; i < 6; i++ {
		if st, err := os.Stat(filepath.Join(dir, "services")); err == nil && st.IsDir() {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	log.Fatal("cannot locate repo root (no services/ directory found above the working directory)")
	return ""
}

func main() {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		log.Fatal("DATABASE_URL must be set")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		log.Fatalf("ping db: %v", err)
	}

	root := repoRoot()
	for _, dir := range migrationDirs {
		full := filepath.Join(root, dir)
		files, err := filepath.Glob(filepath.Join(full, "*.up.sql")) // Glob sorts by name
		if err != nil {
			log.Fatalf("glob %s: %v", full, err)
		}
		if len(files) == 0 {
			log.Fatalf("no migrations found in %s (missing dir?)", full)
		}
		for _, f := range files {
			log.Printf("  -> %s", f)
			if err := execFile(db, f); err != nil {
				log.Fatalf("apply %s: %v", f, err)
			}
		}
	}

	seed := filepath.Join(root, seedFile)
	log.Printf("  -> %s", seed)
	if err := execFile(db, seed); err != nil {
		log.Fatalf("apply %s: %v", seed, err)
	}

	if _, err := db.Exec(telemetryDDL); err != nil {
		log.Fatalf("telemetry DDL: %v", err)
	}
	log.Print("migrations applied")
}
