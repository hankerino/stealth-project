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

## 2026-08-15 — Rust serde(default) != struct-literal default
- Pattern: adding fields to a Rust struct with #[serde(default)] fixes JSON
  parsing but every struct *literal* (incl. test helpers) must still set them;
  CI caught a missing-fields E0063.
- Rule: after adding struct fields, grep for `StructName {` literals across the
  crate (tests, examples) and update them too.

## 2026-08-15 — rdkafka CI needs libcurl4-openssl-dev
- Pattern: librdkafka 2.x (cmake-build) #includes <curl/curl.h> under an OIDC
  ifdef; CI build fails without the curl dev header even though no curl links.
- Rule: Rust services using rdkafka need `cmake build-essential pkg-config
  libssl-dev libcurl4-openssl-dev` + CMAKE_POLICY_VERSION_MINIMUM=3.5 in CI
  (mirror the service Dockerfile's build deps).
