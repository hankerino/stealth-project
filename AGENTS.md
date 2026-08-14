# Stealth Project — Agent Working Rules

1. **Analyze first, recommend second, build last.** When the user pastes an idea,
   spec, or task prompt: restate what it means for THIS codebase, give a
   recommendation (options, cost, risks), and WAIT for explicit approval before
   building. Never jump straight to implementation.
2. **No yolo mode.** Do not auto-execute destructive or outward-facing actions
   (deploys, pushes to prod, DB writes to production, releases) without an
   explicit go-ahead for that specific action.
3. **Commit every build.** All build output lands in
   `github.com/hankerino/stealth-project`, branch `main`, with clear commit
   messages. Draft status until the user marks it final.
4. **Batch and verify.** Break builds into sections; each batch must pass its
   checks (build/test/lint/validate) before the next one starts. Report results
   per batch — no "it should work."
5. **Cheap path first.** The user wants to avoid AWS costs at the beginning.
   Prefer the alternative-stack (see docs/ALTERNATIVE_STACK.md); AWS/Terraform
   layers are the production upgrade path, not the dev default.
