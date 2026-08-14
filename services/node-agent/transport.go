package main

// Telemetry transports.
//
// MODE=direct:     POST each signed envelope to EDGE_URL (5s timeout,
//                  3 tries with exponential backoff; failures are logged by
//                  the caller and the collect loop continues).
// MODE=prometheus: serve the latest samples on :9100/metrics in Prometheus
//                  text exposition format (one gauge per GpuMetrics field,
//                  labeled by node_id/gpu_uuid).
// MODE=both:       run both.

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DirectTransport posts signed envelopes to the telemetry edge.
type DirectTransport struct {
	url    string
	client *http.Client
	tries  int
}

func NewDirectTransport(url string) *DirectTransport {
	return &DirectTransport{
		url:    url,
		client: &http.Client{Timeout: 5 * time.Second},
		tries:  3,
	}
}

// Send posts one envelope with retry + backoff (250ms, 500ms). 4xx responses
// are not retried (they will not heal). The returned error is for logging;
// the telemetry loop must continue regardless.
func (t *DirectTransport) Send(ctx context.Context, envelope []byte) error {
	var lastErr error
	for attempt := 0; attempt < t.tries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(250<<(attempt-1)) * time.Millisecond):
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.url, bytes.NewReader(envelope))
		if err != nil {
			return fmt.Errorf("build request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := t.client.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("post %s: %w", t.url, err)
			continue
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		if resp.StatusCode/100 == 2 {
			return nil
		}
		lastErr = fmt.Errorf("post %s: status %d: %s", t.url, resp.StatusCode, strings.TrimSpace(string(body)))
		if resp.StatusCode >= 400 && resp.StatusCode < 500 {
			return lastErr
		}
	}
	return lastErr
}

// --- Prometheus exposition --------------------------------------------------

// MetricsRegistry holds the latest samples and renders them in Prometheus
// text exposition format. Stdlib-only: no client_golang dependency.
type MetricsRegistry struct {
	mu      sync.RWMutex
	nodeID  string
	samples map[string]GpuSample // keyed by gpu_uuid
	host    HostStats
	hasHost bool
}

func NewMetricsRegistry(nodeID string) *MetricsRegistry {
	return &MetricsRegistry{nodeID: nodeID, samples: make(map[string]GpuSample)}
}

// Update replaces the current sample set (called once per collect tick).
func (r *MetricsRegistry) Update(samples []GpuSample, h HostStats) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, s := range samples {
		r.samples[s.UUID] = s
	}
	r.host = h
	r.hasHost = true
}

var gpuMetricDefs = []struct {
	name string
	help string
	get  func(GpuSample) float64
}{
	{"cte_node_gpu_utilization_pct", "GPU utilization percent.", func(s GpuSample) float64 { return s.UtilizationPct }},
	{"cte_node_gpu_vram_used_mb", "GPU VRAM used (MiB as reported by nvidia-smi).", func(s GpuSample) float64 { return float64(s.VramUsedMB) }},
	{"cte_node_gpu_vram_total_mb", "GPU VRAM total (MiB as reported by nvidia-smi).", func(s GpuSample) float64 { return float64(s.VramTotalMB) }},
	{"cte_node_gpu_power_watts", "GPU power draw in watts.", func(s GpuSample) float64 { return s.PowerWatts }},
	{"cte_node_gpu_temperature_c", "GPU temperature in Celsius.", func(s GpuSample) float64 { return s.TemperatureC }},
	{"cte_node_gpu_active_processes", "Compute applications resident on the GPU (approximation, see collector.go).", func(s GpuSample) float64 { return float64(s.ActiveProcesses) }},
	{"cte_node_gpu_pcie_tx_mbps", "PCIe transmit throughput in Mb/s (nvidia-smi dmon MB/s x8, see collector.go).", func(s GpuSample) float64 { return s.PCIeTxMbps }},
	{"cte_node_gpu_pcie_rx_mbps", "PCIe receive throughput in Mb/s (nvidia-smi dmon MB/s x8, see collector.go).", func(s GpuSample) float64 { return s.PCIeRxMbps }},
}

var hostMetricDefs = []struct {
	name string
	help string
	get  func(HostStats) float64
}{
	{"cte_node_host_load1", "Host 1-minute load average from /proc/loadavg.", func(h HostStats) float64 { return h.Load1 }},
	{"cte_node_host_load5", "Host 5-minute load average from /proc/loadavg.", func(h HostStats) float64 { return h.Load5 }},
	{"cte_node_host_load15", "Host 15-minute load average from /proc/loadavg.", func(h HostStats) float64 { return h.Load15 }},
	{"cte_node_host_mem_total_mb", "Host MemTotal from /proc/meminfo in MiB.", func(h HostStats) float64 { return float64(h.MemTotalMB) }},
	{"cte_node_host_mem_available_mb", "Host MemAvailable from /proc/meminfo in MiB.", func(h HostStats) float64 { return float64(h.MemAvailableMB) }},
}

// Render writes the text exposition for all current samples. GPU ordering is
// sorted by UUID for deterministic output.
func (r *MetricsRegistry) Render(w io.Writer) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	uuids := make([]string, 0, len(r.samples))
	for u := range r.samples {
		uuids = append(uuids, u)
	}
	sort.Strings(uuids)

	for _, def := range gpuMetricDefs {
		fmt.Fprintf(w, "# HELP %s %s\n", def.name, def.help)
		fmt.Fprintf(w, "# TYPE %s gauge\n", def.name)
		for _, u := range uuids {
			s := r.samples[u]
			fmt.Fprintf(w, "%s{node_id=%q,gpu_uuid=%q} %s\n",
				def.name, r.nodeID, s.UUID, strconv.FormatFloat(def.get(s), 'f', -1, 64))
		}
	}
	if r.hasHost {
		for _, def := range hostMetricDefs {
			fmt.Fprintf(w, "# HELP %s %s\n", def.name, def.help)
			fmt.Fprintf(w, "# TYPE %s gauge\n", def.name)
			fmt.Fprintf(w, "%s{node_id=%q} %s\n",
				def.name, r.nodeID, strconv.FormatFloat(def.get(r.host), 'f', -1, 64))
		}
	}
}

// Handler serves /metrics (text exposition) and /healthz.
func (r *MetricsRegistry) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		r.Render(w)
	})
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		io.WriteString(w, "ok\n")
	})
	return mux
}
