package main

// node-agent: seller-side daemon for the compute trading exchange (Phase
// 1B). Collects GPU + host metrics every 5s, signs them into
// NodeTelemetry envelopes (libs/schemas/NodeTelemetry.avsc), and ships them
// via the direct edge POST and/or a Prometheus exporter. Separately:
// registration + heartbeats against the verifier, and best-effort WireGuard
// tunnel management.
//
// Stdlib-only by design: the daemon must stay trivially portable.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

type config struct {
	Mode              string // direct | prometheus | both
	EdgeURL           string
	VerifierURL       string
	KeyPath           string
	StatePath         string
	SellerID          string
	RegistrationToken string
	NodeID            string // optional override; skips registration
	ActiveContractID  string // optional; null in payload when empty
	FakeGPU           bool
	WGConfig          string
	MetricsAddr       string
	LoadavgPath       string
	MeminfoPath       string
	CollectInterval   time.Duration
	HeartbeatInterval time.Duration
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envDuration(key string, fallback time.Duration) (time.Duration, error) {
	v := os.Getenv(key)
	if v == "" {
		return fallback, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	return d, nil
}

func loadConfig() (config, error) {
	var cfg config
	var err error
	cfg.Mode = envOr("MODE", "direct")
	cfg.EdgeURL = envOr("EDGE_URL", "https://telemetry.compute-exchange.com/v1/telemetry")
	cfg.VerifierURL = envOr("VERIFIER_URL", "https://verifier.compute-exchange.com")
	cfg.KeyPath = envOr("KEY_PATH", "/etc/exchange-agent/key.pem")
	cfg.StatePath = envOr("STATE_PATH", "/etc/exchange-agent/state.json")
	cfg.SellerID = os.Getenv("SELLER_ID")
	cfg.RegistrationToken = os.Getenv("REGISTRATION_TOKEN")
	cfg.NodeID = os.Getenv("NODE_ID")
	cfg.ActiveContractID = os.Getenv("ACTIVE_CONTRACT_ID")
	cfg.FakeGPU = os.Getenv("AGENT_FAKE_GPU") == "1"
	cfg.WGConfig = os.Getenv("WG_CONFIG")
	cfg.MetricsAddr = envOr("METRICS_ADDR", ":9100")
	cfg.LoadavgPath = envOr("LOADAVG_PATH", "/proc/loadavg")
	cfg.MeminfoPath = envOr("MEMINFO_PATH", "/proc/meminfo")
	if cfg.CollectInterval, err = envDuration("COLLECT_INTERVAL", 5*time.Second); err != nil {
		return cfg, err
	}
	if cfg.HeartbeatInterval, err = envDuration("HEARTBEAT_INTERVAL", 15*time.Second); err != nil {
		return cfg, err
	}
	switch cfg.Mode {
	case "direct", "prometheus", "both":
	default:
		return cfg, fmt.Errorf("MODE %q invalid: want direct|prometheus|both", cfg.Mode)
	}
	if cfg.SellerID == "" {
		return cfg, errors.New("SELLER_ID is required")
	}
	return cfg, nil
}

// nodeState persists the assigned node_id so registration happens once
// (first boot), not on every restart.
type nodeState struct {
	NodeID string `json:"node_id"`
}

func readState(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var st nodeState
	if err := json.Unmarshal(b, &st); err != nil {
		log.Printf("state file %s unreadable (%v); ignoring", path, err)
		return ""
	}
	return st.NodeID
}

func writeState(path, nodeID string) error {
	b, err := json.Marshal(nodeState{NodeID: nodeID})
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func main() {
	log.SetFlags(log.LstdFlags | log.LUTC)

	cfg, err := loadConfig()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	signer, err := LoadOrGenerateSigner(cfg.KeyPath)
	if err != nil {
		log.Fatalf("signing key: %v", err)
	}
	if pub, err := signer.PublicKeyPEM(); err == nil {
		log.Printf("agent Ed25519 public key (registered at first boot):\n%s", pub)
	}

	collector := newCollector(cfg.FakeGPU)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// WireGuard sidecar: best-effort, never fatal.
	wg := NewWireGuard(cfg.WGConfig)
	if cfg.WGConfig != "" {
		if err := wg.Up(ctx); err != nil {
			log.Printf("wireguard up: %v (continuing)", err)
		}
		defer func() {
			if err := wg.Down(context.Background()); err != nil {
				log.Printf("wireguard down: %v", err)
			}
		}()
	} else {
		log.Print("WG_CONFIG unset; WireGuard management disabled")
	}

	// GPU inventory (also used for registration). A boot-time failure is not
	// fatal: the collect loop will keep retrying, and registration with an
	// empty GPU list is the platform's call to reject.
	inventory, err := collector.Collect(0)
	if err != nil {
		log.Printf("initial GPU inventory failed: %v (continuing)", err)
		inventory = nil
	}
	for _, s := range inventory {
		log.Printf("gpu[%d] %s %s vram=%dMB pcie_gen=%d", s.Index, s.UUID, s.Model, s.VramTotalMB, s.PCIeGen)
	}

	// node_id resolution: NODE_ID env > persisted state > registration.
	nodeID := cfg.NodeID
	if nodeID == "" {
		nodeID = readState(cfg.StatePath)
	}
	if nodeID == "" {
		if cfg.VerifierURL == "" {
			log.Fatal("no NODE_ID/state and VERIFIER_URL unset; cannot register")
		}
		if cfg.RegistrationToken == "" {
			log.Fatal("no NODE_ID/state and REGISTRATION_TOKEN unset; cannot register")
		}
		cp := NewControlPlane(cfg.VerifierURL, signer)
		id, err := cp.Register(ctx, cfg.SellerID, cfg.RegistrationToken, descriptorsFrom(inventory))
		if err != nil {
			log.Fatalf("register node: %v", err)
		}
		nodeID = id
		if err := writeState(cfg.StatePath, nodeID); err != nil {
			log.Printf("persist state %s: %v (will re-register next boot)", cfg.StatePath, err)
		}
	}
	log.Printf("node_id=%s seller_id=%s mode=%s gpus=%d", nodeID, cfg.SellerID, cfg.Mode, len(inventory))

	// Control plane: heartbeats (registration above). Disabled only when
	// VERIFIER_URL is explicitly empty.
	if cfg.VerifierURL != "" {
		cp := NewControlPlane(cfg.VerifierURL, signer)
		go cp.HeartbeatLoop(ctx, nodeID, cfg.SellerID, cfg.HeartbeatInterval)
	} else {
		log.Print("VERIFIER_URL unset; heartbeat disabled")
	}

	// Telemetry transports.
	var registry *MetricsRegistry
	if cfg.Mode == "prometheus" || cfg.Mode == "both" {
		registry = NewMetricsRegistry(nodeID)
		srv := &http.Server{
			Addr:              cfg.MetricsAddr,
			Handler:           registry.Handler(),
			ReadHeaderTimeout: 5 * time.Second,
		}
		go func() {
			log.Printf("prometheus metrics listening on %s/metrics", cfg.MetricsAddr)
			if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Fatalf("metrics server: %v", err)
			}
		}()
		defer srv.Close()
	}
	var direct *DirectTransport
	if cfg.Mode == "direct" || cfg.Mode == "both" {
		direct = NewDirectTransport(cfg.EdgeURL)
		log.Printf("direct transport: posting signed envelopes to %s", cfg.EdgeURL)
	}

	var contractPtr *string
	if cfg.ActiveContractID != "" {
		contractPtr = &cfg.ActiveContractID
	}

	// Telemetry loop: collect -> sign -> ship, every COLLECT_INTERVAL.
	log.Printf("telemetry loop every %s (heartbeat every %s)", cfg.CollectInterval, cfg.HeartbeatInterval)
	ticker := time.NewTicker(cfg.CollectInterval)
	defer ticker.Stop()
	tick := uint64(1) // tick 0 was the boot inventory
	for {
		select {
		case <-ctx.Done():
			log.Print("shutting down")
			return
		case <-ticker.C:
		}

		samples, err := collector.Collect(tick)
		tick++
		if err != nil {
			log.Printf("collect: %v (skipping tick)", err)
			continue
		}
		host, err := readHostStats(cfg.LoadavgPath, cfg.MeminfoPath)
		if err != nil {
			log.Printf("host stats: %v (host fields zeroed this tick)", err)
			host = HostStats{}
		}

		if registry != nil {
			registry.Update(samples, host)
		}
		if direct != nil {
			ts := nowUnixMs()
			// One envelope per GPU: the schema's payload carries a single
			// GpuMetrics record (Kafka key: node_id).
			for _, s := range samples {
				envBytes, err := signer.SignPayload(buildPayload(nodeID, cfg.SellerID, contractPtr, ts, s, host))
				if err != nil {
					log.Printf("sign payload gpu=%s: %v (skipping)", s.UUID, err)
					continue
				}
				if err := direct.Send(ctx, envBytes); err != nil {
					log.Printf("send telemetry gpu=%s: %v (continuing)", s.UUID, err)
				}
			}
		}
	}
}
