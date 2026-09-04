# Seller onboarding (closed beta)

A seller is an account that operates one or more GPU nodes. Their node runs
`services/node-agent`, registers with the telemetry-verifier, heartbeats,
and executes the workload jobs that buyers run against the seller's
allocations. Once a node is registered and active, the seller's SELL
orders on that GPU type are accepted as primary supply.

## 1. Account (exchange side, ~5 min)

1. Seller signs up at https://exchange.hqube.co/login.
2. In Supabase (`stealth-project-auth`) → `accounts`: set `role = 'seller'`
   (or `trader`) and `kyb_verified = true` after checks. The JWT hook picks
   this up on the next login.
3. Seller reads their **Account id** at the top of Portfolio (also the
   `sub` of their token). That is `SELLER_ID`.
4. Mint a **per-seller registration token** (bound to that account id; only
   its hash is stored) and share it over a private channel:

   ```sh
   curl -s -X POST https://cte-telemetry-verifier-w3t7.onrender.com/v1/admin/registration-tokens \
     -H "X-Admin-Token: $VERIFIER_ADMIN_TOKEN" -H "Content-Type: application/json" \
     -d '{"seller_id":"<account uuid>","label":"acme-gpu-rack-1"}'
   # -> {"seller_id":"…","token":"srt_…"}   (shown once)
   ```
   Revoke all of a seller's tokens: `POST …/v1/admin/registration-tokens/revoke`
   with `{"seller_id":"…"}`. A token only registers nodes for its own
   seller_id; the old platform-wide `REGISTRATION_TOKEN` is a break-glass
   fallback and should be unset once real sellers are on per-seller tokens.

## 2. Node install (seller side, ~5 min)

Ubuntu 22.04+/Debian, systemd, NVIDIA driver + `nvidia-smi` present, and
`docker` if real workloads should run (otherwise the executor mocks them):

```sh
curl -fsSL https://raw.githubusercontent.com/hankerino/stealth-project/main/scripts/install-node-agent.sh \
  | sudo SELLER_ID=<account uuid> REGISTRATION_TOKEN=<token> bash
```

The script builds the agent from source (installs Go if needed), writes
`/etc/exchange-agent/agent.env`, installs a systemd unit and waits for
`node_id=…` in the log. Identity (`key.pem`, `state.json`) persists across
upgrades; re-run the script to upgrade.

Outbound only: HTTPS to the verifier and settlement URLs. No inbound ports
(metrics bind to 127.0.0.1:9100).

## 3. Verify (both sides)

- Seller: `journalctl -fu exchange-agent` shows `gpu[0] … H100`, `node_id=`,
  then heartbeats. `curl 127.0.0.1:9100/metrics` works.
- Exchange: `seller_nodes` has the row with `gpu_type_id` resolved (the
  model string must contain a catalog name — `H100`, `A100`, `B200`);
  verifier log shows `NodeOnline`. Then a SELL from that account on
  `<GPU>:<region>` is accepted, a BUY crosses it, the buyer runs the
  allocation, and the job completes on the seller's node.

## 4. Remove / rotate

`systemctl disable --now exchange-agent && rm -rf /etc/exchange-agent
/usr/local/bin/exchange-agent`. Rotate `REGISTRATION_TOKEN` on the verifier
(Render env) — existing nodes keep working (they already hold a node_id).

## Open items before real money

- Region: nodes register without a region; scheduling matches GPU type
  only. Add `REGION` to the agent + `seller_nodes.region_id`.
- Held allocations never expire (see docs/RESALE.md).
