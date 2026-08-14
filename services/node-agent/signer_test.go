package main

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadOrGenerateSignerRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "exchange-agent", "key.pem")

	s1, err := LoadOrGenerateSigner(path)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("key not persisted: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("key perms = %o, want 600", perm)
	}

	s2, err := LoadOrGenerateSigner(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !s1.PublicKey().Equal(s2.PublicKey()) {
		t.Fatal("reloaded key does not match generated key")
	}

	// The PEM must be parseable as PKIX public key material (registration
	// payload).
	if _, err := s1.PublicKeyPEM(); err != nil {
		t.Fatalf("public key PEM: %v", err)
	}
}

func TestLoadOrGenerateSignerRejectsGarbage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "key.pem")
	if err := os.WriteFile(path, []byte("not a pem"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrGenerateSigner(path); err == nil {
		t.Fatal("expected error for unparseable key file")
	}
}

// TestCanonicalJSONStability pins the canonical form: keys sorted
// (recursively), compact separators, no HTML escaping, no trailing newline —
// independent of Go map insertion order.
func TestCanonicalJSONStability(t *testing.T) {
	a := map[string]any{
		"z": 1,
		"a": "x<y", // must NOT be HTML-escaped
		"m": map[string]any{"b": 2.5, "a": true, "n": nil},
	}
	b := map[string]any{
		"m": map[string]any{"n": nil, "a": true, "b": 2.5},
		"a": "x<y",
		"z": 1,
	}
	ba, err := canonicalJSON(a)
	if err != nil {
		t.Fatal(err)
	}
	bb, err := canonicalJSON(b)
	if err != nil {
		t.Fatal(err)
	}
	if string(ba) != string(bb) {
		t.Fatalf("canonical form differs by insertion order:\n%s\n%s", ba, bb)
	}
	want := `{"a":"x<y","m":{"a":true,"b":2.5,"n":null},"z":1}`
	if string(ba) != want {
		t.Fatalf("canonical JSON = %s, want %s", ba, want)
	}
}

// TestSignPayloadRoundTrip signs a payload, unpacks the envelope, and
// verifies the signature with crypto/ed25519 against the exact payload bytes
// carried in the envelope (what the platform verifier does).
func TestSignPayloadRoundTrip(t *testing.T) {
	s, err := LoadOrGenerateSigner(filepath.Join(t.TempDir(), "key.pem"))
	if err != nil {
		t.Fatal(err)
	}
	payload := buildPayload("n-1", "s-1", nil, 1755000000000,
		GpuSample{Model: "NVIDIA H100 80GB HBM3", UUID: "GPU-x", UtilizationPct: 42, VramUsedMB: 1024, VramTotalMB: 81920, PowerWatts: 350.5, TemperatureC: 55, ActiveProcesses: 2, PCIeTxMbps: 272, PCIeRxMbps: 96},
		HostStats{Load1: 1, Load5: 2, Load15: 3, MemTotalMB: 128125, MemAvailableMB: 64000})

	envBytes, err := s.SignPayload(payload)
	if err != nil {
		t.Fatal(err)
	}
	var env Envelope
	if err := json.Unmarshal(envBytes, &env); err != nil {
		t.Fatalf("envelope unmarshal: %v", err)
	}

	// The embedded payload must already be canonical (sorted keys).
	var reordered map[string]any
	if err := json.Unmarshal(env.Payload, &reordered); err != nil {
		t.Fatal(err)
	}
	recanonical, err := canonicalJSON(reordered)
	if err != nil {
		t.Fatal(err)
	}
	if string(recanonical) != string(env.Payload) {
		t.Fatalf("envelope payload is not canonical:\n%s\n%s", env.Payload, recanonical)
	}

	// Verify signature over sha256(canonical payload).
	sig, err := base64.StdEncoding.DecodeString(env.Signature)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(env.Payload)
	if !ed25519.Verify(s.PublicKey(), digest[:], sig) {
		t.Fatal("ed25519 verification failed")
	}
	if !VerifySha256(s.PublicKey(), env.Payload, env.Signature) {
		t.Fatal("VerifySha256 helper failed")
	}

	// Tampering must fail verification.
	if VerifySha256(s.PublicKey(), append(env.Payload, ' '), env.Signature) {
		t.Fatal("tampered payload verified")
	}
}

// TestTelemetryEnvelopeContract pins payload/metrics field names to
// libs/schemas/NodeTelemetry.avsc, plus the additive host_* context fields
// documented in README. The envelope itself is {payload, signature}.
func TestTelemetryEnvelopeContract(t *testing.T) {
	contract := "c-9"
	payload := buildPayload("n-1", "s-1", &contract, 1755000000000, GpuSample{}, HostStats{})
	raw, err := canonicalJSON(payload)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}

	wantPayload := []string{
		"node_id", "seller_id", "active_contract_id", "timestamp_unix_ms", "metrics",
		"host_load1", "host_load5", "host_load15", "host_mem_total_mb", "host_mem_available_mb",
	}
	for _, k := range wantPayload {
		if _, ok := got[k]; !ok {
			t.Errorf("payload missing field %q (schema: NodeTelemetry.avsc)", k)
		}
	}
	if got["active_contract_id"] != "c-9" {
		t.Errorf("active_contract_id = %v, want c-9", got["active_contract_id"])
	}

	metrics, ok := got["metrics"].(map[string]any)
	if !ok {
		t.Fatal("metrics is not an object")
	}
	wantMetrics := []string{
		"gpu_model", "gpu_uuid", "utilization_pct", "vram_used_mb", "vram_total_mb",
		"power_watts", "temperature_c", "active_processes", "pcie_tx_mbps", "pcie_rx_mbps",
	}
	for _, k := range wantMetrics {
		if _, ok := metrics[k]; !ok {
			t.Errorf("metrics missing field %q (schema: GpuMetrics)", k)
		}
	}
	if len(metrics) != len(wantMetrics) {
		t.Errorf("metrics has %d fields, want exactly %d", len(metrics), len(wantMetrics))
	}

	// Nullable contract id must serialize as JSON null when unset.
	rawNull, err := canonicalJSON(buildPayload("n-1", "s-1", nil, 0, GpuSample{}, HostStats{}))
	if err != nil {
		t.Fatal(err)
	}
	var gotNull map[string]any
	if err := json.Unmarshal(rawNull, &gotNull); err != nil {
		t.Fatal(err)
	}
	v, present := gotNull["active_contract_id"]
	if !present || v != nil {
		t.Errorf("active_contract_id = %v (present=%v), want explicit null", v, present)
	}
}
