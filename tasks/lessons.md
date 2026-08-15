# Lessons (self-improvement log)

## 2026-08-15 — Ship changes push-ready, not as patches
- Pattern: When taking over a repo to "deliver," the user expects changes landed
  on the remote, not handed back as a local patch. Defaulting to a patch created
  an extra round-trip.
- Rule: For any repo build task, establish write access up front (confirm a push
  path — connector or scoped token), commit to a feature branch, and push so CI
  verifies. Only fall back to a patch if push is explicitly declined.

## 2026-08-15 — No local Go/Rust toolchain in this sandbox
- Pattern: go.dev / proxy.golang.org / crates hosts are proxy-blocked; only
  github.com + pypi are reachable. Cannot `go build`/`cargo build` locally.
- Rule: Treat CI as the compiler. Add a CI workflow for every service touched so
  pushes actually verify; never claim "production-ready" from static review alone
  — say what was and wasn't verified.
