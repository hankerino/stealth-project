package main

import (
	"encoding/json"
	"testing"
	"time"
)

// The signed byte string must match settlement's verification
// byte-for-byte: node_id || job_id || status || decimal(sent_at_unix_ms).
func TestJobStatusMessage(t *testing.T) {
	got := string(jobStatusMessage("node-1", "job-9", "completed", 1755000000000))
	want := "node-1job-9completed1755000000000"
	if got != want {
		t.Fatalf("jobStatusMessage = %q, want %q", got, want)
	}
}

// A signature produced for the job-status scheme verifies with the same
// public key (the platform does the equivalent against
// seller_nodes.public_key).
func TestJobStatusSignatureRoundtrip(t *testing.T) {
	s, err := LoadOrGenerateSigner(t.TempDir() + "/key.pem")
	if err != nil {
		t.Fatal(err)
	}
	msg := jobStatusMessage("n", "j", "started", 42)
	sig := s.SignSha256(msg)
	if !VerifySha256(s.PublicKey(), msg, sig) {
		t.Fatal("valid job-status signature rejected")
	}
	if VerifySha256(s.PublicKey(), jobStatusMessage("n", "j", "completed", 42), sig) {
		t.Fatal("signature accepted for a different message")
	}
}

func TestAgentJobSpecParsing(t *testing.T) {
	j := agentJob{Workload: json.RawMessage(`{"image":"ubuntu:24.04","command":["echo","hi"],"mock_duration_seconds":3}`)}
	s := j.spec()
	if s.Image != "ubuntu:24.04" || len(s.Command) != 2 || s.MockDurationSeconds != 3 {
		t.Fatalf("spec = %+v", s)
	}
	// Missing/garbage spec degrades to zero-value defaults, no panic.
	j2 := agentJob{Workload: json.RawMessage(`{"bogus"`)}
	_ = j2.spec()
	j3 := agentJob{}
	if s := j3.spec(); s.Image != "" || s.MockDurationSeconds != 0 {
		t.Fatalf("empty spec = %+v", s)
	}
}

func TestExecutorClaimDedupes(t *testing.T) {
	e := newExecutor("http://x", "n", nil, "mock", time.Second, time.Second)
	if !e.claim("j1") {
		t.Fatal("first claim rejected")
	}
	if e.claim("j1") {
		t.Fatal("duplicate claim accepted")
	}
	e.release("j1")
	if !e.claim("j1") {
		t.Fatal("claim after release rejected")
	}
}
