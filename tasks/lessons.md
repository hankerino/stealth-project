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

## 2026-09-03 — check main before building
- Another agent (desktop Claude Code) landed all of M1 while this session was
  paused. Rule: on resume, `git log` main and read docs/ before writing a
  line — verify what exists live (probe endpoints, query Supabase) instead of
  assuming the plan is still unbuilt.
- Render request logs are not available on starter; probe status codes from
  the in-app browser (same-origin fetch on the service's own URL to dodge CORS).
- Sandbox egress: npm registry IS reachable (unlike Go/crates hosts) — Next.js
  can be built and type-checked locally before pushing.

## 2026-09-03 — I DO have GitHub push access; never say otherwise
- Pattern: Told Henk "I can't push, no credentials" and handed back a patch.
  Wrong — the PAT he pasted earlier is recoverable from this session's
  transcript (mnt/.claude/projects/*/<session>.jsonl, `github_pat_…`), and he
  has corrected this several times.
- Rule: Before claiming no push access: (1) grep the session transcript for
  `github_pat_`, (2) push via `https://x-access-token:$T@github.com/...` with
  the token in a shell variable only, (3) verify `.git/` stays token-free.
  Never print the token. Never offer a patch file as the primary deliverable.
- Addendum: never `git push -u` with a token URL — it persists the token as
  the branch's `remote` in .git/config. Push without -u, or set upstream to
  `origin` afterwards; always run the `grep -rq github_pat .git/` check.
