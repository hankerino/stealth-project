package main

// Control plane: node registration and liveness heartbeats against the
// verifier. gRPC is the contract of record
// (libs/proto/nodeagent/v1/node_agent.proto); these HTTP POSTs are the v1
// transport shim, with snake_case JSON bodies mirroring the proto field
// names 1:1:
//
//	POST {VERIFIER_URL}/v1/nodes/register  <- RegisterNodeRequest
//	POST {VERIFIER_URL}/v1/heartbeat       <- HeartbeatRequest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

type ControlPlane struct {
	baseURL string
	client  *http.Client
	signer  *Signer
}

func NewControlPlane(verifierURL string, signer *Signer) *ControlPlane {
	return &ControlPlane{
		baseURL: strings.TrimRight(verifierURL, "/"),
		client:  &http.Client{Timeout: 5 * time.Second},
		signer:  signer,
	}
}

// gpuDescriptor mirrors GpuDescriptor in the proto contract.
type gpuDescriptor struct {
	Model  string `json:"model"`
	UUID   string `json:"uuid"`
	VramMB int64  `json:"vram_mb"`
}

func descriptorsFrom(samples []GpuSample) []gpuDescriptor {
	out := make([]gpuDescriptor, 0, len(samples))
	for _, s := range samples {
		out = append(out, gpuDescriptor{Model: s.Model, UUID: s.UUID, VramMB: s.VramTotalMB})
	}
	return out
}

// registerRequest mirrors RegisterNodeRequest.
type registerRequest struct {
	SellerID          string          `json:"seller_id"`
	RegistrationToken string          `json:"registration_token"`
	GPUs              []gpuDescriptor `json:"gpus"`
	PublicKeyPEM      string          `json:"public_key_pem"`
}

// registerResponse mirrors RegisterNodeResponse.
type registerResponse struct {
	NodeID       string `json:"node_id"`
	Accepted     bool   `json:"accepted"`
	RejectReason string `json:"reject_reason"`
}

// Register exchanges the seller credential for a node_id (first boot; the
// result is persisted in the state file by the caller). 3 tries with
// backoff; a terminal error means the agent cannot operate and main exits.
func (c *ControlPlane) Register(ctx context.Context, sellerID, token string, gpus []gpuDescriptor) (string, error) {
	pubPEM, err := c.signer.PublicKeyPEM()
	if err != nil {
		return "", err
	}
	body, err := json.Marshal(registerRequest{
		SellerID:          sellerID,
		RegistrationToken: token,
		GPUs:              gpus,
		PublicKeyPEM:      pubPEM,
	})
	if err != nil {
		return "", err
	}

	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(time.Duration(250<<(attempt-1)) * time.Millisecond):
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/nodes/register", bytes.NewReader(body))
		if err != nil {
			return "", fmt.Errorf("register: build request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := c.client.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("register post: %w", err)
			continue
		}
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		resp.Body.Close()
		if resp.StatusCode/100 != 2 {
			lastErr = fmt.Errorf("register: status %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
			if resp.StatusCode >= 400 && resp.StatusCode < 500 {
				break // 4xx will not heal by retrying
			}
			continue
		}
		var rr registerResponse
		if err := json.Unmarshal(raw, &rr); err != nil {
			return "", fmt.Errorf("register: decode response: %w", err)
		}
		if !rr.Accepted {
			return "", fmt.Errorf("register: rejected: %s", rr.RejectReason)
		}
		if rr.NodeID == "" {
			return "", fmt.Errorf("register: accepted but empty node_id")
		}
		return rr.NodeID, nil
	}
	return "", lastErr
}

// heartbeatRequest mirrors HeartbeatRequest. signature is base64 Ed25519
// over sha256(node_id || sent_at_unix_ms); see heartbeatMessage in
// signer.go for the v1 encoding of that concatenation.
type heartbeatRequest struct {
	NodeID       string `json:"node_id"`
	SellerID     string `json:"seller_id"`
	SentAtUnixMs int64  `json:"sent_at_unix_ms"`
	Signature    string `json:"signature"`
}

// HeartbeatOnce sends a single signed heartbeat.
func (c *ControlPlane) HeartbeatOnce(ctx context.Context, nodeID, sellerID string) error {
	sentAt := nowUnixMs()
	body, err := json.Marshal(heartbeatRequest{
		NodeID:       nodeID,
		SellerID:     sellerID,
		SentAtUnixMs: sentAt,
		Signature:    c.signer.SignSha256(heartbeatMessage(nodeID, sentAt)),
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/heartbeat", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("heartbeat post: %w", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("heartbeat: status %d", resp.StatusCode)
	}
	return nil
}

// HeartbeatLoop sends a heartbeat every interval until ctx is done.
// Failures are logged and the loop continues — liveness must not crash the
// telemetry path.
func (c *ControlPlane) HeartbeatLoop(ctx context.Context, nodeID, sellerID string, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		if err := c.HeartbeatOnce(ctx, nodeID, sellerID); err != nil {
			log.Printf("heartbeat: %v (continuing)", err)
		}
	}
}
