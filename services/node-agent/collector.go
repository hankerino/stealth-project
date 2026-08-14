package main

// GPU + host metric collection.
//
// Real collector: shells out to nvidia-smi via os/exec (no CGO/NVML binding,
// keeping the daemon stdlib-only and trivially portable).
//
// Field sourcing notes (approximations, per PRD):
//   - The PRD query field list is `uuid,name,utilization.gpu,memory.used,
//     memory.total,power.draw,temperature.gpu,pcie.link.gen.current`. We
//     prepend `index` to that query so dmon output (which is indexed, not
//     UUID-addressed) can be correlated back to GPUs. That is the only
//     deviation from the PRD field list.
//   - active_processes: `nvidia-smi --query-compute-apps=gpu_uuid` emits one
//     line per compute application per GPU; we count lines per UUID.
//     Approximation: counts COMPUTE apps only (graphics contexts are not
//     visible), and a process using the same GPU via multiple contexts can
//     appear once per context on some drivers.
//   - pcie_tx/rx_mbps: query-gpu exposes link generation/width but NOT live
//     throughput on current drivers, so we sample `nvidia-smi dmon -s p -c 1`
//     (columns: gpu, rx, tx in MB/s) and convert MB/s -> Mb/s (x8) into the
//     schema's pcie_*_mbps fields. Single instantaneous sample per tick.
//   - pcie.link.gen.current is collected per PRD but is NOT in
//     NodeTelemetry.avsc's GpuMetrics, so it is logged at inventory time only
//     and never sent in the payload.
//   - GPU marketing names containing commas would break the CSV parse; no
//     current datacenter SKU does, and malformed lines are skipped+logged.
//
// Fake collector: when nvidia-smi is absent OR AGENT_FAKE_GPU=1, emits
// deterministic synthetic metrics (pure function of the tick counter) so
// tests and the e2e script get reproducible output.

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// GpuSample is one node's metrics for one GPU. The field set mirrors
// GpuMetrics in libs/schemas/NodeTelemetry.avsc; PCIeGen is collected per
// PRD but is not part of the schema payload (log/inventory only).
type GpuSample struct {
	Index           int
	Model           string
	UUID            string
	UtilizationPct  float64
	VramUsedMB      int64
	VramTotalMB     int64
	PowerWatts      float64
	TemperatureC    float64
	ActiveProcesses int
	PCIeTxMbps      float64
	PCIeRxMbps      float64
	PCIeGen         int
}

// HostStats carries host-level context from /proc into the payload as
// additive host_* fields (see README "Schema extensions").
type HostStats struct {
	Load1          float64
	Load5          float64
	Load15         float64
	MemTotalMB     int64
	MemAvailableMB int64
}

// Collector produces one GpuSample per GPU per tick. tick is a monotonic
// counter starting at 0; the fake collector derives metrics from it.
type Collector interface {
	Collect(tick uint64) ([]GpuSample, error)
}

// newCollector picks the real nvidia-smi collector when possible and falls
// back to the deterministic fake collector otherwise.
func newCollector(fake bool) Collector {
	if fake {
		log.Print("AGENT_FAKE_GPU=1; using deterministic fake GPU collector")
		return fakeCollector{}
	}
	path, err := exec.LookPath("nvidia-smi")
	if err != nil {
		log.Print("nvidia-smi not found; using deterministic fake GPU collector")
		return fakeCollector{}
	}
	log.Printf("using nvidia-smi collector (%s)", path)
	return &nvidiaCollector{smiPath: path}
}

// --- nvidia-smi collector ---------------------------------------------------

type nvidiaCollector struct {
	smiPath     string
	warnedProcs bool
	warnedPCIe  bool
}

// gpuQueryFields is the PRD field list plus a leading `index` for dmon
// correlation (documented at the top of this file).
const gpuQueryFields = "index,uuid,name,utilization.gpu,memory.used,memory.total,power.draw,temperature.gpu,pcie.link.gen.current"

func (c *nvidiaCollector) Collect(_ uint64) ([]GpuSample, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, c.smiPath,
		"--query-gpu="+gpuQueryFields,
		"--format=csv,noheader,nounits").Output()
	if err != nil {
		return nil, fmt.Errorf("nvidia-smi query-gpu: %w", err)
	}
	samples, err := parseGpuQueryCSV(bytes.NewReader(out))
	if err != nil {
		return nil, err
	}

	// active_processes (best-available): count compute apps per GPU UUID.
	procCtx, procCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer procCancel()
	if out, err := exec.CommandContext(procCtx, c.smiPath,
		"--query-compute-apps=gpu_uuid", "--format=csv,noheader").Output(); err == nil {
		counts := countComputeApps(bytes.NewReader(out))
		for i := range samples {
			samples[i].ActiveProcesses = counts[samples[i].UUID]
		}
	} else if !c.warnedProcs {
		c.warnedProcs = true
		log.Printf("nvidia-smi query-compute-apps failed (%v); active_processes pinned to 0", err)
	}

	// PCIe throughput (best-available): dmon -s p, one sample, MB/s -> Mb/s.
	pcieCtx, pcieCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer pcieCancel()
	if out, err := exec.CommandContext(pcieCtx, c.smiPath,
		"dmon", "-s", "p", "-c", "1").Output(); err == nil {
		throughput := parseDmonPCIe(bytes.NewReader(out))
		for i := range samples {
			if rxtx, ok := throughput[samples[i].Index]; ok {
				samples[i].PCIeRxMbps = rxtx[0]
				samples[i].PCIeTxMbps = rxtx[1]
			}
		}
	} else if !c.warnedPCIe {
		c.warnedPCIe = true
		log.Printf("nvidia-smi dmon failed (%v); pcie_*_mbps pinned to 0", err)
	}

	return samples, nil
}

// parseGpuQueryCSV parses `nvidia-smi --query-gpu=<gpuQueryFields>
// --format=csv,noheader,nounits` output. Malformed lines are skipped; a line
// that yields no samples at all surfaces as an error. Individual fields that
// are unparseable (e.g. "[N/A]" on older GPUs) degrade to 0.
func parseGpuQueryCSV(r io.Reader) ([]GpuSample, error) {
	raw, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	var samples []GpuSample
	for lineno, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Split(line, ",")
		if len(fields) != 9 {
			log.Printf("gpu query line %d: want 9 fields, got %d; skipping", lineno+1, len(fields))
			continue
		}
		for i := range fields {
			fields[i] = strings.TrimSpace(fields[i])
		}
		samples = append(samples, GpuSample{
			Index:          int(parseIntOr(fields[0], 0)),
			UUID:           fields[1],
			Model:          fields[2],
			UtilizationPct: parseFloatOr(fields[3], 0),
			VramUsedMB:     parseIntOr(fields[4], 0),
			VramTotalMB:    parseIntOr(fields[5], 0),
			PowerWatts:     parseFloatOr(fields[6], 0),
			TemperatureC:   parseFloatOr(fields[7], 0),
			PCIeGen:        int(parseIntOr(fields[8], 0)),
		})
	}
	if len(samples) == 0 {
		return nil, fmt.Errorf("no GPUs parsed from nvidia-smi output")
	}
	return samples, nil
}

// parseDmonPCIe parses `nvidia-smi dmon -s p -c 1` output:
//
//	# gpu    rx    tx
//	    0   512   128
//
// rx/tx are MB/s; returned values are converted to Mb/s (x8) keyed by GPU
// index.
func parseDmonPCIe(r io.Reader) map[int][2]float64 {
	out := make(map[int][2]float64)
	raw, err := io.ReadAll(r)
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		idx, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		// MB/s -> Mb/s.
		out[idx] = [2]float64{parseFloatOr(fields[1], 0) * 8, parseFloatOr(fields[2], 0) * 8}
	}
	return out
}

// countComputeApps counts lines (one per compute application) per GPU UUID
// from `nvidia-smi --query-compute-apps=gpu_uuid --format=csv,noheader`.
func countComputeApps(r io.Reader) map[string]int {
	counts := make(map[string]int)
	raw, err := io.ReadAll(r)
	if err != nil {
		return counts
	}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		counts[line]++
	}
	return counts
}

func parseFloatOr(s string, fallback float64) float64 {
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return fallback
	}
	return v
}

func parseIntOr(s string, fallback int64) int64 {
	v, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return fallback
	}
	return v
}

// --- fake collector ---------------------------------------------------------

// fakeCollector emits deterministic synthetic metrics: output is a pure
// function of the tick counter, so tests and the e2e script are reproducible.
type fakeCollector struct{}

func (fakeCollector) Collect(tick uint64) ([]GpuSample, error) {
	t := int(tick % 1000)
	out := make([]GpuSample, 2)
	for i := range out {
		out[i] = GpuSample{
			Index:           i,
			Model:           "FakeGPU-H100",
			UUID:            fmt.Sprintf("GPU-fake-%012d", i),
			UtilizationPct:  float64((t*7 + i*13) % 100),
			VramUsedMB:      int64(1024 * ((t+i)%8 + 1)),
			VramTotalMB:     81920,
			PowerWatts:      float64(100 + (t*3+i*11)%600),
			TemperatureC:    float64(30 + (t+i)%50),
			ActiveProcesses: (t + i) % 5,
			PCIeTxMbps:      float64((t*5+i*3)%2000) * 8,
			PCIeRxMbps:      float64((t*2+i*7)%2000) * 8,
			PCIeGen:         5,
		}
	}
	return out, nil
}

// --- host context (/proc) ---------------------------------------------------

// readHostStats reads /proc/loadavg and /proc/meminfo. Paths are parameters
// so tests can point at fixtures.
func readHostStats(loadavgPath, meminfoPath string) (HostStats, error) {
	var h HostStats

	load, err := os.ReadFile(loadavgPath)
	if err != nil {
		return h, fmt.Errorf("read %s: %w", loadavgPath, err)
	}
	fields := strings.Fields(string(load))
	if len(fields) < 3 {
		return h, fmt.Errorf("%s: want >= 3 fields, got %d", loadavgPath, len(fields))
	}
	h.Load1 = parseFloatOr(fields[0], 0)
	h.Load5 = parseFloatOr(fields[1], 0)
	h.Load15 = parseFloatOr(fields[2], 0)

	mem, err := os.ReadFile(meminfoPath)
	if err != nil {
		return h, fmt.Errorf("read %s: %w", meminfoPath, err)
	}
	for _, line := range strings.Split(string(mem), "\n") {
		key, rest, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		var kb int64
		if _, err := fmt.Sscanf(strings.TrimSpace(rest), "%d", &kb); err != nil {
			continue
		}
		switch key {
		case "MemTotal":
			h.MemTotalMB = kb / 1024
		case "MemAvailable":
			h.MemAvailableMB = kb / 1024
		}
	}
	return h, nil
}
