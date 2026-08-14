package main

import (
	"context"
	"testing"
)

// Only the deterministic paths are tested: with WG_CONFIG unset (or tooling
// absent), every WireGuard operation must be a nil-error no-op — the agent
// must never crash because of WireGuard.
func TestWireGuardNoConfigNoOps(t *testing.T) {
	wg := NewWireGuard("")
	if wg.available() {
		t.Fatal("available() = true with no config")
	}
	if err := wg.Up(context.Background()); err != nil {
		t.Fatalf("Up: %v", err)
	}
	if err := wg.Down(context.Background()); err != nil {
		t.Fatalf("Down: %v", err)
	}
	status, err := wg.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status != "unavailable" {
		t.Fatalf("Status = %q, want unavailable", status)
	}
}

func TestWireGuardIfaceNameDerivation(t *testing.T) {
	wg := NewWireGuard("/etc/wireguard/exchange0.conf")
	if wg.iface != "exchange0" {
		t.Fatalf("iface = %q, want exchange0", wg.iface)
	}
	// No wg-quick on this test host (or any host without the tooling) must
	// still no-op cleanly even with a config set.
	if wg.wgQuick == "" {
		if err := wg.Up(context.Background()); err != nil {
			t.Fatalf("Up without wg-quick: %v", err)
		}
		if err := wg.Down(context.Background()); err != nil {
			t.Fatalf("Down without wg-quick: %v", err)
		}
	}
}
