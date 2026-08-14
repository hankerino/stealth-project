package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Captured nvidia-smi output (two GPUs, fields = gpuQueryFields:
// index,uuid,name,utilization.gpu,memory.used,memory.total,power.draw,
// temperature.gpu,pcie.link.gen.current; csv,noheader,nounits).
const gpuQueryFixture = `0, GPU-0a1b2c3d-1111-2222-3333-444455556666, NVIDIA H100 80GB HBM3, 42, 10240, 81920, 350.50, 55, 5
1, GPU-7f8e9d0c-aaaa-bbbb-cccc-dddddddd0000, NVIDIA A100-SXM4-80GB, 0, 0, 81920, 62.10, 31, 4
`

func TestParseGpuQueryCSV(t *testing.T) {
	samples, err := parseGpuQueryCSV(strings.NewReader(gpuQueryFixture))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(samples) != 2 {
		t.Fatalf("got %d samples, want 2", len(samples))
	}

	s := samples[0]
	if s.Index != 0 || s.UUID != "GPU-0a1b2c3d-1111-2222-3333-444455556666" {
		t.Fatalf("sample 0 identity = %d/%s", s.Index, s.UUID)
	}
	if s.Model != "NVIDIA H100 80GB HBM3" {
		t.Fatalf("model = %q", s.Model)
	}
	if s.UtilizationPct != 42 || s.VramUsedMB != 10240 || s.VramTotalMB != 81920 {
		t.Fatalf("util/vram = %v/%d/%d", s.UtilizationPct, s.VramUsedMB, s.VramTotalMB)
	}
	if s.PowerWatts != 350.50 || s.TemperatureC != 55 || s.PCIeGen != 5 {
		t.Fatalf("power/temp/gen = %v/%v/%d", s.PowerWatts, s.TemperatureC, s.PCIeGen)
	}
	if samples[1].PCIeGen != 4 {
		t.Fatalf("sample 1 pcie gen = %d, want 4", samples[1].PCIeGen)
	}
}

func TestParseGpuQueryCSVDegradesGracefully(t *testing.T) {
	// "[N/A]" fields (older GPUs / driver gaps) must degrade to 0, and a
	// malformed line must be skipped without failing the good one.
	fixture := `0, GPU-ok, NVIDIA T4, 10, 512, 15360, [N/A], [N/A], 3
this,is,broken
1, GPU-ok2, NVIDIA T4, 20, 256, 15360, 45.0, 40, 3
`
	samples, err := parseGpuQueryCSV(strings.NewReader(fixture))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(samples) != 2 {
		t.Fatalf("got %d samples, want 2 (malformed line skipped)", len(samples))
	}
	if samples[0].PowerWatts != 0 || samples[0].TemperatureC != 0 {
		t.Fatalf("[N/A] fields should degrade to 0, got %v/%v", samples[0].PowerWatts, samples[0].TemperatureC)
	}
}

func TestParseGpuQueryCSVNoGPUs(t *testing.T) {
	if _, err := parseGpuQueryCSV(strings.NewReader("\n\n")); err == nil {
		t.Fatal("expected error when no GPUs parse")
	}
}

// Captured `nvidia-smi dmon -s p -c 1` output (rx/tx in MB/s).
const dmonFixture = `# gpu   rx    tx
    0   12    34
    1    0     0
`

func TestParseDmonPCIe(t *testing.T) {
	m := parseDmonPCIe(strings.NewReader(dmonFixture))
	if len(m) != 2 {
		t.Fatalf("got %d entries, want 2", len(m))
	}
	// MB/s -> Mb/s (x8).
	if got := m[0]; got[0] != 96 || got[1] != 272 {
		t.Fatalf("gpu0 rx/tx = %v/%v Mb/s, want 96/272", got[0], got[1])
	}
	if got := m[1]; got[0] != 0 || got[1] != 0 {
		t.Fatalf("gpu1 rx/tx = %v/%v, want 0/0", got[0], got[1])
	}
}

// Captured `nvidia-smi --query-compute-apps=gpu_uuid --format=csv,noheader`.
const computeAppsFixture = `GPU-0a1b2c3d-1111-2222-3333-444455556666
GPU-0a1b2c3d-1111-2222-3333-444455556666
GPU-7f8e9d0c-aaaa-bbbb-cccc-dddddddd0000
`

func TestCountComputeApps(t *testing.T) {
	counts := countComputeApps(strings.NewReader(computeAppsFixture))
	if counts["GPU-0a1b2c3d-1111-2222-3333-444455556666"] != 2 {
		t.Fatalf("gpu0 count = %d, want 2", counts["GPU-0a1b2c3d-1111-2222-3333-444455556666"])
	}
	if counts["GPU-7f8e9d0c-aaaa-bbbb-cccc-dddddddd0000"] != 1 {
		t.Fatalf("gpu1 count = %d, want 1", counts["GPU-7f8e9d0c-aaaa-bbbb-cccc-dddddddd0000"])
	}
	if got := countComputeApps(strings.NewReader("")); len(got) != 0 {
		t.Fatalf("empty output should give empty map, got %v", got)
	}
}

func TestFakeCollectorDeterministic(t *testing.T) {
	c := fakeCollector{}
	a, err := c.Collect(0)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	b, err := c.Collect(0)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if len(a) != 2 {
		t.Fatalf("fake collector emits %d GPUs, want 2", len(a))
	}
	if a[0] != b[0] || a[1] != b[1] {
		t.Fatal("same tick must produce identical samples")
	}

	// Pin tick-0 values so the e2e script can rely on them.
	want := GpuSample{
		Index: 0, Model: "FakeGPU-H100", UUID: "GPU-fake-000000000000",
		UtilizationPct: 0, VramUsedMB: 1024, VramTotalMB: 81920,
		PowerWatts: 100, TemperatureC: 30, ActiveProcesses: 0,
		PCIeTxMbps: 0, PCIeRxMbps: 0, PCIeGen: 5,
	}
	if a[0] != want {
		t.Fatalf("tick0/gpu0 = %+v, want %+v", a[0], want)
	}

	c1, err := c.Collect(1)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if c1[0] == a[0] {
		t.Fatal("different ticks should produce different samples")
	}
	if c1[0].UtilizationPct != 7 {
		t.Fatalf("tick1/gpu0 util = %v, want 7", c1[0].UtilizationPct)
	}
}

func TestReadHostStats(t *testing.T) {
	dir := t.TempDir()
	loadavg := filepath.Join(dir, "loadavg")
	meminfo := filepath.Join(dir, "meminfo")
	if err := os.WriteFile(loadavg, []byte("1.25 0.75 0.50 2/345 6789\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	mem := "MemTotal:       131072000 kB\nMemFree:         1024000 kB\nMemAvailable:   65536000 kB\nBuffers:          1000 kB\n"
	if err := os.WriteFile(meminfo, []byte(mem), 0o600); err != nil {
		t.Fatal(err)
	}

	h, err := readHostStats(loadavg, meminfo)
	if err != nil {
		t.Fatalf("readHostStats: %v", err)
	}
	if h.Load1 != 1.25 || h.Load5 != 0.75 || h.Load15 != 0.50 {
		t.Fatalf("loads = %v/%v/%v", h.Load1, h.Load5, h.Load15)
	}
	// 131072000 kB / 1024 = 128000 MB; 65536000 kB / 1024 = 64000 MB.
	if h.MemTotalMB != 128000 || h.MemAvailableMB != 64000 {
		t.Fatalf("mem = %d/%d MB", h.MemTotalMB, h.MemAvailableMB)
	}
}

func TestReadHostStatsMissingFiles(t *testing.T) {
	dir := t.TempDir()
	if _, err := readHostStats(filepath.Join(dir, "nope"), filepath.Join(dir, "nada")); err == nil {
		t.Fatal("expected error for missing files")
	}
}
