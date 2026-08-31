# trading-engine API (skeleton)

Minimal HTTPS service that proves the whole platform path:
CI runner → ECR → EKS (vpc-core, sealed) → edge ALB → public domain.

- `GET /healthz` — edge target group health check
- `GET /v1/status` — version + uptime
- Listens on `:8443` with a self-signed cert generated at boot (the ALB does
  not validate target certs; east-west only inside the org perimeter).
  Set `API_TLS_ENABLED=false` to serve plain HTTP instead — for platforms
  whose proxy terminates TLS (e.g. Render).
- `LISTEN_ADDR`, `APP_VERSION`, `API_TLS_ENABLED` env overrides.

## Local

```bash
go run .            # serves https://localhost:8443
curl -k https://localhost:8443/healthz
```

## Image

```bash
docker build -t api:dev .
```

Distroless, non-root, ~10 MB.
