package main

// Ed25519 signing of telemetry payloads per libs/schemas/NodeTelemetry.avsc:
//
//	payload = canonical JSON (keys sorted, compact separators)
//	digest  = SHA-256(payload)
//	signature = base64( Ed25519_sign(digest) )
//	envelope = {"payload": <exact signed bytes>, "signature": "..."}
//
// The envelope embeds the exact canonical payload bytes that were signed, so
// the verifier can recompute sha256 over `payload` verbatim without
// re-serializing.

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// Signer holds the agent's Ed25519 private key.
type Signer struct {
	priv ed25519.PrivateKey
}

// LoadOrGenerateSigner loads the PEM private key at path, or generates a new
// Ed25519 keypair and persists it (0600, parent dir 0700) on first boot.
// The matching public key is sent to the platform at registration time
// (RegisterNodeRequest.public_key_pem) and stored in seller_nodes.public_key.
func LoadOrGenerateSigner(path string) (*Signer, error) {
	if b, err := os.ReadFile(path); err == nil {
		s, err := parsePrivateKeyPEM(b)
		if err != nil {
			return nil, fmt.Errorf("parse key %s: %w", path, err)
		}
		return s, nil
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("read key %s: %w", path, err)
	}

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate ed25519 key: %w", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return nil, fmt.Errorf("marshal private key: %w", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create key dir: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, pemBytes, 0o600); err != nil {
		return nil, fmt.Errorf("write key %s: %w", path, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return nil, fmt.Errorf("rename key into place: %w", err)
	}
	return &Signer{priv: priv}, nil
}

func parsePrivateKeyPEM(b []byte) (*Signer, error) {
	block, _ := pem.Decode(b)
	if block == nil {
		return nil, fmt.Errorf("no PEM block found")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse PKCS#8: %w", err)
	}
	priv, ok := key.(ed25519.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("key is %T, want ed25519.PrivateKey", key)
	}
	return &Signer{priv: priv}, nil
}

// PublicKeyPEM returns the PKIX PEM encoding of the public key, sent at
// registration time (RegisterNodeRequest.public_key_pem).
func (s *Signer) PublicKeyPEM() (string, error) {
	pub, ok := s.priv.Public().(ed25519.PublicKey)
	if !ok {
		return "", fmt.Errorf("unexpected public key type %T", s.priv.Public())
	}
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return "", fmt.Errorf("marshal public key: %w", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})), nil
}

// PublicKey returns the raw Ed25519 public key (used by tests/verification).
func (s *Signer) PublicKey() ed25519.PublicKey {
	return s.priv.Public().(ed25519.PublicKey)
}

// SignSha256 signs SHA-256(msg) and returns the base64-encoded signature.
func (s *Signer) SignSha256(msg []byte) string {
	digest := sha256.Sum256(msg)
	return base64.StdEncoding.EncodeToString(ed25519.Sign(s.priv, digest[:]))
}

// VerifySha256 checks a base64 signature produced by SignSha256. Exposed for
// tests and local tooling; the platform-side verifier does the equivalent
// against seller_nodes.public_key.
func VerifySha256(pub ed25519.PublicKey, msg []byte, sigB64 string) bool {
	sig, err := base64.StdEncoding.DecodeString(sigB64)
	if err != nil {
		return false
	}
	digest := sha256.Sum256(msg)
	return ed25519.Verify(pub, digest[:], sig)
}

// canonicalJSON serializes v as canonical JSON: object keys sorted
// lexicographically, compact separators (no whitespace), no HTML escaping.
// encoding/json sorts map keys on marshal, which is the canonicalization
// mechanism; the Encoder path only disables HTML escaping and the result is
// stripped of its trailing newline.
func canonicalJSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// buildPayload assembles the telemetry payload. Field names match
// TelemetryPayload/GpuMetrics in libs/schemas/NodeTelemetry.avsc exactly;
// the host_* keys are additive context fields (see README "Schema
// extensions"). contractID may be nil (Avro null).
func buildPayload(nodeID, sellerID string, contractID *string, tsUnixMs int64, s GpuSample, h HostStats) map[string]any {
	var contract any
	if contractID != nil {
		contract = *contractID
	}
	return map[string]any{
		"node_id":               nodeID,
		"seller_id":             sellerID,
		"active_contract_id":    contract,
		"timestamp_unix_ms":     tsUnixMs,
		"host_load1":            h.Load1,
		"host_load5":            h.Load5,
		"host_load15":           h.Load15,
		"host_mem_total_mb":     h.MemTotalMB,
		"host_mem_available_mb": h.MemAvailableMB,
		"metrics": map[string]any{
			"gpu_model":        s.Model,
			"gpu_uuid":         s.UUID,
			"utilization_pct":  s.UtilizationPct,
			"vram_used_mb":     s.VramUsedMB,
			"vram_total_mb":    s.VramTotalMB,
			"power_watts":      s.PowerWatts,
			"temperature_c":    s.TemperatureC,
			"active_processes": s.ActiveProcesses,
			"pcie_tx_mbps":     s.PCIeTxMbps,
			"pcie_rx_mbps":     s.PCIeRxMbps,
		},
	}
}

// Envelope is the signed wire format, matching libs/schemas/NodeTelemetry.avsc
// {payload, signature}. Payload carries the exact canonical bytes that were
// signed.
type Envelope struct {
	Payload   json.RawMessage `json:"payload"`
	Signature string          `json:"signature"`
}

// SignPayload canonicalizes payload, signs it, and returns the serialized
// envelope ready to POST to the telemetry edge.
func (s *Signer) SignPayload(payload map[string]any) ([]byte, error) {
	pb, err := canonicalJSON(payload)
	if err != nil {
		return nil, fmt.Errorf("canonicalize payload: %w", err)
	}
	return json.Marshal(Envelope{Payload: pb, Signature: s.SignSha256(pb)})
}

// heartbeatMessage is the byte string hashed for heartbeat signatures.
// HeartbeatRequest's contract is "sha256(node_id || sent_at_unix_ms)"; the
// v1 encoding of that concatenation is UTF-8(node_id) followed by the
// decimal ASCII representation of sent_at_unix_ms (documented in README).
func heartbeatMessage(nodeID string, sentAtUnixMs int64) []byte {
	return []byte(nodeID + strconv.FormatInt(sentAtUnixMs, 10))
}

// nowUnixMs exists so tests can pin timestamps if needed.
var nowUnixMs = func() int64 { return time.Now().UnixMilli() }
