package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestRegisterSendsProtoContractFields(t *testing.T) {
	s, err := LoadOrGenerateSigner(filepath.Join(t.TempDir(), "key.pem"))
	if err != nil {
		t.Fatal(err)
	}

	var got registerRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/nodes/register" {
			t.Errorf("path = %s, want /v1/nodes/register", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode register body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(registerResponse{NodeID: "n-123", Accepted: true})
	}))
	defer srv.Close()

	cp := NewControlPlane(srv.URL, s)
	gpus := []gpuDescriptor{{Model: "NVIDIA H100 80GB HBM3", UUID: "GPU-x", VramMB: 81920}}
	nodeID, err := cp.Register(context.Background(), "s-1", "tok-abc", gpus)
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if nodeID != "n-123" {
		t.Fatalf("node_id = %q, want n-123", nodeID)
	}

	// Contract: RegisterNodeRequest {seller_id, registration_token, gpus[],
	// public_key_pem}.
	if got.SellerID != "s-1" || got.RegistrationToken != "tok-abc" {
		t.Fatalf("seller_id/token = %q/%q", got.SellerID, got.RegistrationToken)
	}
	if len(got.GPUs) != 1 || got.GPUs[0].UUID != "GPU-x" || got.GPUs[0].VramMB != 81920 {
		t.Fatalf("gpus = %+v", got.GPUs)
	}
	if !strings.Contains(got.PublicKeyPEM, "BEGIN PUBLIC KEY") {
		t.Fatalf("public_key_pem = %q", got.PublicKeyPEM)
	}
}

func TestRegisterRejected(t *testing.T) {
	s, err := LoadOrGenerateSigner(filepath.Join(t.TempDir(), "key.pem"))
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(registerResponse{Accepted: false, RejectReason: "bad token"})
	}))
	defer srv.Close()

	cp := NewControlPlane(srv.URL, s)
	if _, err := cp.Register(context.Background(), "s-1", "bad", nil); err == nil {
		t.Fatal("expected rejection error")
	} else if !strings.Contains(err.Error(), "bad token") {
		t.Fatalf("error = %v, want reject reason", err)
	}
}

func TestHeartbeatSignatureVerifies(t *testing.T) {
	s, err := LoadOrGenerateSigner(filepath.Join(t.TempDir(), "key.pem"))
	if err != nil {
		t.Fatal(err)
	}

	var got heartbeatRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/heartbeat" {
			t.Errorf("path = %s, want /v1/heartbeat", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode heartbeat body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cp := NewControlPlane(srv.URL, s)
	if err := cp.HeartbeatOnce(context.Background(), "n-1", "s-1"); err != nil {
		t.Fatalf("heartbeat: %v", err)
	}

	// Contract: HeartbeatRequest {node_id, seller_id, sent_at_unix_ms,
	// signature}; signature = base64 Ed25519 over sha256(node_id || sent_at).
	if got.NodeID != "n-1" || got.SellerID != "s-1" {
		t.Fatalf("node_id/seller_id = %q/%q", got.NodeID, got.SellerID)
	}
	if got.SentAtUnixMs <= 0 {
		t.Fatalf("sent_at_unix_ms = %d", got.SentAtUnixMs)
	}
	if !VerifySha256(s.PublicKey(), heartbeatMessage(got.NodeID, got.SentAtUnixMs), got.Signature) {
		t.Fatal("heartbeat signature does not verify over sha256(node_id||sent_at)")
	}
}

func TestStateRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if got := readState(path); got != "" {
		t.Fatalf("readState on missing file = %q, want empty", got)
	}
	if err := writeState(path, "n-123"); err != nil {
		t.Fatal(err)
	}
	if got := readState(path); got != "n-123" {
		t.Fatalf("readState = %q, want n-123", got)
	}
}
