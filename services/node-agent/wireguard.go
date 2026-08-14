package main

// WireGuard management via wg-quick, driven by the config file path in
// WG_CONFIG (e.g. /etc/wireguard/exchange0.conf; interface name = basename
// minus .conf).
//
// HARD RULE: WireGuard is a best-effort sidecar. If WG_CONFIG is unset,
// wg-quick/wg are not installed, or any command fails, the failure is
// logged (by the caller) and swallowed — WireGuard must never crash the
// agent.

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type WireGuard struct {
	configPath string
	iface      string
	wgQuick    string // resolved path, "" if not installed
	wg         string // resolved path, "" if not installed
}

func NewWireGuard(configPath string) *WireGuard {
	w := &WireGuard{configPath: configPath}
	if configPath == "" {
		return w
	}
	w.iface = strings.TrimSuffix(filepath.Base(configPath), ".conf")
	if p, err := exec.LookPath("wg-quick"); err == nil {
		w.wgQuick = p
	}
	if p, err := exec.LookPath("wg"); err == nil {
		w.wg = p
	}
	return w
}

// available reports whether up/down can run. Status degrades more
// gracefully (it only needs `wg`).
func (w *WireGuard) available() bool {
	return w.configPath != "" && w.wgQuick != ""
}

// Up brings the configured interface up. No-op (nil error) when wg-quick or
// WG_CONFIG is unavailable.
func (w *WireGuard) Up(ctx context.Context) error {
	if !w.available() {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, w.wgQuick, "up", w.configPath).CombinedOutput(); err != nil {
		return fmt.Errorf("wg-quick up %s: %v: %s", w.configPath, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Down tears the configured interface down. No-op (nil error) when wg-quick
// or WG_CONFIG is unavailable. Called on agent shutdown; the agent only
// downs the interface it was configured to manage.
func (w *WireGuard) Down(ctx context.Context) error {
	if !w.available() {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, w.wgQuick, "down", w.configPath).CombinedOutput(); err != nil {
		return fmt.Errorf("wg-quick down %s: %v: %s", w.configPath, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Status reports "up"/"down"/"unavailable" for the configured interface.
// "unavailable" means WG_CONFIG is unset or the wg tooling is missing —
// never an error.
func (w *WireGuard) Status(ctx context.Context) (string, error) {
	if w.configPath == "" || w.wg == "" {
		return "unavailable", nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, w.wg, "show", "interfaces").Output()
	if err != nil {
		return "", fmt.Errorf("wg show interfaces: %w", err)
	}
	for _, iface := range strings.Fields(string(out)) {
		if iface == w.iface {
			return "up", nil
		}
	}
	return "down", nil
}
