package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestDirectTransportDeliversVerifiableEnvelope(t *testing.T) {
	s, err := LoadOrGenerateSigner(t.TempDir() + "/key.pem")
	if err != nil {
		t.Fatal(err)
	}

	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q", ct)
		}
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	payload := buildPayload("n-1", "s-1", nil, 1755000000000,
		GpuSample{UUID: "GPU-x", UtilizationPct: 42}, HostStats{})
	envBytes, err := s.SignPayload(payload)
	if err != nil {
		t.Fatal(err)
	}

	tr := NewDirectTransport(srv.URL)
	if err := tr.Send(context.Background(), envBytes); err != nil {
		t.Fatalf("send: %v", err)
	}

	var env Envelope
	if err := json.Unmarshal(gotBody, &env); err != nil {
		t.Fatalf("edge received undecodable envelope: %v", err)
	}
	if !VerifySha256(s.PublicKey(), env.Payload, env.Signature) {
		t.Fatal("edge-received envelope failed signature verification")
	}
}

func TestDirectTransportRetries(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) < 3 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	tr := NewDirectTransport(srv.URL)
	if err := tr.Send(context.Background(), []byte(`{}`)); err != nil {
		t.Fatalf("send: %v", err)
	}
	if calls.Load() != 3 {
		t.Fatalf("calls = %d, want 3 (2 failures + success)", calls.Load())
	}
}

func TestDirectTransportGivesUpAfterThreeTries(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	tr := NewDirectTransport(srv.URL)
	if err := tr.Send(context.Background(), []byte(`{}`)); err == nil {
		t.Fatal("expected error after retries exhausted")
	}
	if calls.Load() != 3 {
		t.Fatalf("calls = %d, want 3", calls.Load())
	}
}

func TestDirectTransportDoesNotRetry4xx(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	tr := NewDirectTransport(srv.URL)
	if err := tr.Send(context.Background(), []byte(`{}`)); err == nil {
		t.Fatal("expected error on 400")
	}
	if calls.Load() != 1 {
		t.Fatalf("calls = %d, want 1 (4xx not retried)", calls.Load())
	}
}

func TestDirectTransportHonorsContextCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	tr := NewDirectTransport(srv.URL)
	if err := tr.Send(ctx, []byte(`{}`)); err == nil {
		t.Fatal("expected error on context cancel")
	}
}

func TestPrometheusExposition(t *testing.T) {
	reg := NewMetricsRegistry("n-1")
	reg.Update([]GpuSample{
		{UUID: "GPU-b", UtilizationPct: 7, VramUsedMB: 2048, VramTotalMB: 81920, PowerWatts: 111, TemperatureC: 31, ActiveProcesses: 1, PCIeTxMbps: 24, PCIeRxMbps: 56},
		{UUID: "GPU-a", UtilizationPct: 0, VramUsedMB: 1024, VramTotalMB: 81920, PowerWatts: 100, TemperatureC: 30, ActiveProcesses: 0, PCIeTxMbps: 0, PCIeRxMbps: 0},
	}, HostStats{Load1: 1.25, Load5: 0.75, Load15: 0.5, MemTotalMB: 128125, MemAvailableMB: 64000})

	srv := httptest.NewServer(reg.Handler())
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	out := string(body)

	for _, want := range []string{
		"# TYPE cte_node_gpu_utilization_pct gauge",
		`cte_node_gpu_utilization_pct{node_id="n-1",gpu_uuid="GPU-a"} 0`,
		`cte_node_gpu_utilization_pct{node_id="n-1",gpu_uuid="GPU-b"} 7`,
		`cte_node_gpu_vram_used_mb{node_id="n-1",gpu_uuid="GPU-b"} 2048`,
		`cte_node_gpu_power_watts{node_id="n-1",gpu_uuid="GPU-b"} 111`,
		`cte_node_gpu_active_processes{node_id="n-1",gpu_uuid="GPU-b"} 1`,
		`cte_node_gpu_pcie_tx_mbps{node_id="n-1",gpu_uuid="GPU-b"} 24`,
		`cte_node_gpu_pcie_rx_mbps{node_id="n-1",gpu_uuid="GPU-b"} 56`,
		`cte_node_host_load1{node_id="n-1"} 1.25`,
		`cte_node_host_mem_total_mb{node_id="n-1"} 128125`,
		`cte_node_host_mem_available_mb{node_id="n-1"} 64000`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("exposition missing %q\n---\n%s", want, out)
		}
	}

	// Deterministic ordering: GPU-a before GPU-b.
	if strings.Index(out, `gpu_uuid="GPU-a"`) > strings.Index(out, `gpu_uuid="GPU-b"`) {
		t.Error("GPU series not sorted by uuid")
	}
}
