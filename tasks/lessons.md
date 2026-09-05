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

## 2026-09-04 — Migrations re-run on every deploy; every statement must be a no-op the 2nd time
- Pattern: `ALTER COLUMN ... TYPE ... USING NULL` in a "make it idempotent"
  migration silently nulled a live column on each deploy. Nobody noticed
  because the symptom (NO_CAPACITY) looked like a missing seller.
- Rule: scripts/migrate has no applied-version table, so guard any
  data-touching DDL with an information_schema / pg_constraint check, and
  after a deploy re-query the rows you depend on, not just the schema.

## 2026-09-04 — At-least-once consumers need an idempotency key in the same tx
- Pattern: risk applied one leg of a trade, crashed, replayed and applied it
  again. Settlement already did this right (trade_ledger PK); risk did not.
- Rule: every Kafka consumer that mutates state writes a marker keyed by the
  event id in the same transaction as the mutation (see risk_applied_trades,
  surveillance_alerts (rule, ref_id)). Check this for any new consumer.

## 2026-09-04 — Pushing render.yaml IS a deploy action
- Pattern: the Blueprint created two services from a render.yaml push before
  I had "decided" to create them; a later revert did not delete them.
- Rule: treat render.yaml on main as live infra. Add services deliberately;
  removal needs a dashboard delete, and secrets (sync: false) via the Render
  API right after the push. Render API can't read secrets or DB connection
  strings — private services without a public URL may skip the gateway
  secret, public ones may not.

## 2026-09-04 — kafka-go Writer: never assign a nil *Transport
- Pattern: the settlement lesson from 09-03 was applied to settlement only;
  risk had the same constructor and panicked on its first prod publish.
- Rule: when fixing a bug in copied boilerplate, grep the other services
  for the same lines before closing it (`grep -rn "Transport:" services/`).

## 2026-09-04 — A full host disk corrupts the sandbox git clone
- Pattern: empty .git objects after the Mac hit 276 MB free. Remote was fine.
- Rule: if git errors mention empty/corrupt objects, save the working-tree
  diff, re-clone, re-apply; don't try to repair the object store.

## 2026-09-04 — Supabase redirect allow-list globs and proxied redirects
- Pattern: `https://host/*` does NOT match `/auth/callback?next=…` (single `*`
  stops at `/` and `?`); Supabase then silently redirects to the site root and
  the PKCE code is never exchanged. And `NextResponse.redirect(new URL(x, req.url))`
  in a route handler behind Render's proxy sends users to localhost:10000.
- Rule: allow-list entries end in `/**`; build post-auth redirects from
  `x-forwarded-host`/`x-forwarded-proto`; have middleware forward any stray
  `?code=` to the callback. Test the whole magic-link loop yourself (Gmail MCP
  can read the email) before calling auth done.

## 2026-09-04 — Base64 through the browser tool is lossy; draw or fetch instead
- Pattern: a PNG pasted as base64 into javascript_tool arrived corrupted (same
  length, different hash) and rendered as a thin line.
- Rule: generate assets in the page (canvas) or upload through a form; verify
  with a hash before wiring the URL anywhere.

## 2026-09-04 — Supabase dashboard renders blank on direct URLs
- Pattern: direct navigation to dashboard pages often stays blank (innerText 0)
  for 30-60 s or forever; a fresh tab + waiting ~40 s + client-side navigation
  (clicking sidebar links / JS `a.click()`) works.
- Rule: land on one page, wait, then navigate inside the SPA. Supabase MCP
  covers SQL/migrations/keys; only hooks, URL config and the JWT secret need
  the dashboard.

## 2026-09-05 — fees
- Any fee on a buyer-side hold must ALSO be added to risk's pre-trade margin
  check, or orders pass risk and then FAIL at settlement with
  INSUFFICIENT_FUNDS. Keep FEE_BUYER_BPS identical on cte-settlement and cte-risk.
- Freeze rates on the ledger row at hold time; never recompute from env at
  release (schedule changes would break hold/release symmetry).
- Processor pass-through fees are not revenue: record them (fee_ledger
  to_platform=false) but never credit the platform account.
- Supabase `mfa.enroll` defaults the TOTP issuer to the project's Site URL
  (jizoni.com on the shared pool) — always pass `issuer`.

## 2026-09-05 — Stripe webhook first live deposit
- The Stripe webhook Endpoint URL for cte-settlement had a stray trailing
  period (`.../v1/stripe/webhook.`) since it was first configured — every
  delivery 404'd (Go's ServeMux treats the trailing dot as a different path).
  This meant a paying customer's card would be charged but escrow would
  never credit. Only found because the $1 live deposit sanity check was
  done deliberately before opening the site to real users. Fixed the URL in
  the Stripe dashboard, resent the failed event (200 OK), escrow credited
  correctly ($328.25 -> $329.25). Lesson: always click the actual saved
  Endpoint URL in Stripe's dashboard character-by-character when wiring a
  webhook, and do one real end-to-end deposit as a release gate before
  going live with real users, not just Stripe test mode.

