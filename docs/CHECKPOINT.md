# TraceIQ — RESUME CHECKPOINT (read this file completely before doing anything)

**Purpose:** any session — this one after a weekly usage reset, or a brand-new session with zero
memory of prior conversation — resumes the build from exactly here. This file plus
`docs/ledger.md` (chronological narrative log, append-only, read the tail) plus files already on
disk are the ONLY source of truth. Do not trust anything else, including your own assumed
recollection of "what we were doing."

## HOLD 2026-09-19 14:24 — waiting for user's explicit "start wave 15" command

Batch 3 (remediate, nl, eval) is now fully COMPLETE: implemented (wave 13) AND reviewed+fixed
(wave 14, see ledger 2026-09-19 14:24 entry — remediate's security-critical fail-open allowlist
bug and nl's fail-open AuthZ bypass were both real, both fixed). Cost ≈ $11.39 of $30.

**Standing instruction confirmed twice now: the user wants to approve each wave individually,
not run fully autonomous.** Wave 15 (TDD batch 4: rca LLM-reasoner wiring, api+web UI, cmd
wiring — see NEXT ACTION step 5 below) is drafted and ready but must NOT be dispatched until the
user explicitly says so, whether that's a live "resume"/"start wave 15" message or another
pre-authorized cron like the one used for wave 14 (`CronCreate` one-shot, prompt spelling out
exactly what to verify and dispatch — that pattern works, reuse it if the user asks for another
scheduled resume).

## HOLD STATE (why we're paused) — earlier hold, superseded by the one above, kept for history

**Paused 2026-09-16 13:38 EDT on explicit user request: weekly usage limit approaching.**
No cron/wakeup is scheduled — session-only crons die when a session ends, and a weekly gap will
outlive this session. **Resume only when the user says "resume" (or equivalent) in a new message.**
On resume: read this whole file, read the tail of `docs/ledger.md` (last ~15 entries) to confirm
nothing else changed, verify the repo builds (`go build ./... && go vet ./... && go test ./...`
with `GOTOOLCHAIN=go1.27.1 CGO_ENABLED=0`), THEN continue at "NEXT ACTION" below. Do not re-derive
the plan from scratch — it is fully specified below.

## What TraceIQ is

An AI-native distributed-tracing bot (Go 1.27, single binary) that closes the gaps found in Jaeger,
Zipkin, Grafana Tempo, Datadog APM, and Dynatrace (see `Tracing-Bot-PRD.md` for the full product
case). Anomaly-aware tail sampling, tiered open storage, agentic root-cause analysis with a
deterministic rules reasoner and an optional LLM reasoner, guarded remediation, natural-language
interface. Repo: `C:\Users\shakr\OneDrive\Desktop\shakti-data\claude\Tracing-bot` (git repo,
remote `github.com/shaktiprasadrath/Tracing-bot`, branch `main`, nothing pushed yet — all work is
local commits-not-even-made; `git status --short` will show everything as untracked/new).

## Budget

**User cap: $30 total, hard stop at projected $28.** Spent so far: **≈ $9.15** (measured from
subagent `<usage>` token reports × blended list-price rate: sonnet $1.25/M, opus $3.15/M, haiku
$0.65/M — see `docs/cost-ledger.md` for the row-by-row log; a few early killed-agent partial runs
are unmeasured but immaterial). **~$19–21 of runway left.** Policy, unchanged: sonnet for
coding/fixes/reviews, opus ONLY for architecture-board-style gates (round-2 verify, final
whole-codebase review) and truly hard synthesis tasks. Log every completed agent's tokens × rate
in `docs/cost-ledger.md` immediately. Stop dispatching new agents if projected total would exceed
$28 — checkpoint and ask the user before going further.

## Session-limit handling (this has happened ~6 times already, expect it again)

Subagents die mid-run with a 429 `session limit` or `monthly spend limit` error — this is normal,
not a bug. When it happens: **do not panic or re-derive from scratch.** Check what's actually on
disk (`find internal -name '*.go' | xargs wc -l`, `go build ./...`, `git status`) — prior agents
save incrementally, so partial work survives. Re-dispatch a **continuation** agent scoped to
exactly what's missing/broken, telling it precisely what already exists so it doesn't redo
finished work. This has worked cleanly every time so far (see ledger entries for waves 5, 9, 11).

## Skills/tools used this build (for a fresh session to know what's available)

`superpowers:subagent-driven-development` (the process this whole build follows: fresh subagent
per task, task review, fix loop, ledger discipline), `superpowers:dispatching-parallel-agents`
(parallel wave dispatch pattern), `artifact-design` (the published PRD page), `prompt-master`
(dispatch-prompt optimization). The `Agent` tool with explicit `model:` per dispatch is how all
subagents are launched — always specify model, never let it inherit session default.

---

## PROGRESS SO FAR (all phases through this point are DONE, do not redo)

### Phase 1 — Product research + PRD — DONE
- `Tracing-Bot-PRD.md` + published artifact `traceiq-prd.html`: full competitive gap analysis vs
  Jaeger/Zipkin/Tempo/Datadog/Dynatrace, architecture, ROI.

### Phase 2 — Architecture docs — DONE, APPROVED
- `docs/architecture/00-feature-catalog.md` — F01–F12 + X-SEC + X-OPS feature IDs, drawback IDs
  (D-J1..D-X5), canonical package list.
- `docs/architecture/01-system-architecture.md` (3261 lines) — system design, all DR-0..DR-39
  applied.
- `docs/architecture/02-class-diagram.md`, `03-sequence-diagrams.md`, `04-execution-flow.md` —
  mermaid diagrams, DR-0..DR-39 applied.
- `docs/architecture/05-tool-selection-adr.md` — Go 1.27 chosen, dependency ADR, Set B pins.
- `docs/architecture/features/F01..F12-*.md`, `X-SEC-security.md`, `X-OPS-deployment.md` — one
  design doc per feature, DR-0..DR-39 applied, FR/AC-numbered requirements.
- `docs/architecture/06-decision-register.md` (3887 lines) — **THE BINDING SPEC.** 40 decisions
  (DR-0..DR-39) resolving all 98 round-1 review findings. Appendix A (finding→DR map), Appendix B
  (14 accept-modified rulings), Appendix C (exact new FR/AC IDs), Appendix D (one known open gap:
  `model.ToolResult`'s canonical field list was never printed in the register — resolved
  pragmatically during rca implementation, see below).
- Review trail: `docs/reviews/round1/{security-reliability,performance-data,completeness-coherence}.md`
  (98 findings, 31 Blocker/45 Major/22 Minor) → `docs/reviews/architecture_review.md` (round-1
  dispositions) → `docs/reviews/round2/verification.md` (round-2: 89/9/0) →
  `docs/reviews/round2/recheck.md` (round-2 scoped recheck: NOT APPROVED, 3 items) →
  `docs/reports/w7-fix.md` (closed all 3) → **`docs/signoffs/architecture-signoff.md` — APPROVED
  FOR IMPLEMENTATION.**

### Phase 3 — Go scaffold — DONE
- `go.mod`: `module traceiq`, `go 1.27`, `toolchain go1.27.1`. **Always run Go commands with
  `GOTOOLCHAIN=go1.27.1 CGO_ENABLED=0`** (pure-Go build, no CGO anywhere, Windows-compatible).
- Every package under `internal/` has canonical interfaces per the decision register:
  `model, tenant, config, auth, llm, k8s, selfobs, bus, cluster` (foundational/cross-cutting) +
  `ingest, sampler, store(+sqlite,parquet,tiered), topology, anomaly, correlate, memory, rca(+rules),
  remediate, nl, eval, api`.
- `internal/archtest/archtest_test.go` — parses import blocks and FAILS the build if any package
  imports one not permitted by the DR-2 adjacency table. This is load-bearing — every implementer
  must keep it green. It currently encodes the full adjacency including `rca/rules` (fixed during
  wave 11).
- `cmd/traceiq/main.go`, `Makefile`, `scripts/ci.sh`/`ci.ps1`, `README.md` — all present.

### Phase 4 — Implementation (TDD, in progress)

**Batch 1 — COMPLETE, reviewed, fixed, green:** `internal/store` (sqlite hot-index + parquet
cold-store + tiered composite), `internal/ingest` (OTLP-gRPC + Zipkin receivers), `internal/sampler`
(rendezvous sharding, tail sampling, keep policy, interest predicates), `internal/topology` (live
graph). Reports: `docs/reports/w9-*.md` (implementation), `docs/reports/w10-review-*.md` (review +
fixes — reviewers found and fixed real bugs in every single package: a disk leak, a spurious
anomaly-trigger bug, missing dedup, a data race, unbounded index growth, an unverified checksum).

**Batch 2 — implementation COMPLETE, review NOT YET DONE:** `internal/anomaly` (47 tests),
`internal/correlate` (tests pass, untouched since first pass), `internal/memory` (26 tests),
`internal/rca` + `internal/rca/rules` (23/41 tests, rules reasoner only — 4 of 6 rules implemented:
error-signature-after-deploy, downstream-latency-propagation, N+1-span-pattern,
connection-pool-exhaustion; retry-storm and timeout-mismatch deliberately deferred). Reports:
`docs/reports/w11-anomaly.md` + `w11-anomaly-cont.md`, `w11-correlate-memory.md`,
`w11-memory-cont.md`, `w11-rca-rules.md` + `w11-rca-cont.md`.

Known bugs already fixed during wave 11 (do not re-flag, they're closed): sub-threshold anomaly
events were silently dropped instead of recorded (DR-14 §14.4) — fixed; memory fingerprint
matching was diluted by prose tokens, causing exact matches to fail — fixed; missing
`RunbookImporter` — implemented; missing `mathPow`/`decodeToolArgs` build breaks — fixed; interest
predicate `Remove` used the wrong ID (DR-11 violation) — fixed; `archtest` had no adjacency entry
for `rca/rules` — fixed.

**Deliberately deferred (not bugs, tracked, address later at the stated point):**
- `internal/rca`: LLM reasoner wiring (planned for a later wave alongside `api`+`web`, per the
  original sequencing — do NOT do this before batch 3).
- `internal/rca/rules`: retry-storm and timeout-mismatch rules (2 of the 6-rule catalog) — pick up
  whenever rca gets touched again; not blocking.
- `internal/ingest`: OTLP-HTTP and Jaeger-format receivers not built (only OTLP-gRPC + Zipkin);
  auth/TLS/metrics wiring deferred pending `internal/auth`/`internal/selfobs` having real logic
  (currently interface-only from scaffold) — legitimately out of order, address when those
  packages get implemented.
- `internal/store`: DuckDB cross-validation (AC-F03-8), orphan reconciliation (FR-F03-14),
  `Compact()` still a no-op, `cmd/traceiq` wiring.
- `internal/topology`↔`internal/store`/`internal/anomaly`: the durable `topology_edge_meta.is_new`
  reconciliation path (DR-13, required for `Changes()` losslessness / AC-F04-9) spans a schema
  change in `store` and a not-yet-built reconciler in `anomaly` — flagged by the wave-10 reviewer,
  address when `anomaly`'s topology-change detector gets its review pass (wave 12).
- `internal/memory`: AC-F08-7 (100k-row p99/candidate-bound perf target) not meaningfully testable
  against the current in-memory O(n)-scan store; MinHash-LSH consolidation still pairwise —
  acceptable for now, revisit if `memory` becomes a bottleneck later.
- Appendix D gap (`model.ToolResult` canonical fields): resolved pragmatically — the rca-rules
  implementer defined additive fields in `internal/model/rca.go` (347 lines, includes the
  `ToolResult`-related types) rather than leaving it fully open. Verify this is adequate when
  `api`/`web` (which also touch tool results for the UI) get built — if the rca implementer's
  choice doesn't fit api's needs, extend rather than redefine.

### Current repo state (verified 2026-09-16 13:38 EDT, last thing done before this pause)

- 123 `.go` files, 24 `_test.go` files, 12 tested packages.
- `go build ./... && go vet ./... && go test ./...` — **ALL GREEN, zero failures, zero known
  Blockers open.**
- `gofmt -l .` — clean (17 files were reformatted by the orchestrator directly, no agent, right
  before this checkpoint was written).
- `internal/archtest` passes (import-boundary enforcement working, including the `rca/rules` fix).

---

## NEXT ACTION (do this first on resume, in order)

1. **Sanity check:** re-run `go build ./... && go vet ./... && go test ./...`
   (`GOTOOLCHAIN=go1.27.1 CGO_ENABLED=0`) yourself to confirm the state above still holds (nothing
   should have changed while paused, but verify, don't assume).

2. **Wave 12 — review batch 2** (same pattern as wave 10, which worked well: 3 parallel sonnet
   reviewers, each ALSO authorized to fix Blocker/Major findings directly and re-verify, no
   separate fix-dispatch round needed). Dispatch:
   - **Reviewer A — anomaly**: spec vs `docs/architecture/features/F05-anomaly-detection.md` +
     DR-14/15/21. Also resolve the topology↔store↔anomaly `is_new` reconciliation item flagged
     above if in scope; otherwise re-confirm it's still correctly deferred.
   - **Reviewer B — correlate + memory**: spec vs F07/F08 + DR-16/19/20. Pay attention to the
     tenant-isolation tests (already 5 dedicated tests passing per wave 11 — verify they're
     actually rigorous, not just present).
   - **Reviewer C — rca + rules**: spec vs F06 + DR-17/18/11. Check the `model.ToolResult` field
     choice is sound (Appendix D gap). Confirm the 4 implemented rules are correct and the 2
     deferred ones are cleanly stubbed, not silently broken.

   Use the exact dispatch-prompt shape from wave 10 (see `docs/ledger.md` 2026-09-16 09:17 entry,
   or just re-derive it — the pattern is: read the wave-11 reports first, read the binding spec
   sections by line range, review implementation+tests, run build/vet/test yourself to confirm,
   fix Blocker/Major directly, write `docs/reports/w12-review-*.md`, return ≤8 lines).

3. **After wave 12 is clean:** Batch 2 done. Proceed to **Batch 3 — TDD implementation**:
   `internal/remediate`, `internal/nl`, `internal/eval` (3 parallel sonnet coders, same TDD
   pattern: tests first, implement, `go test` green, report to `docs/reports/w13-*.md`). Specs:
   `F09-guarded-remediation.md`, `F10-natural-language.md`, `F11-eval-harness.md` +
   corresponding DRs (grep `06-decision-register.md` "Docs to change" for F09/F10/F11).

4. **Wave 14 — review batch 3** (same pattern as wave 10/12).

5. **Batch 4 — TDD implementation**: `internal/rca`'s LLM reasoner (wire `internal/llm` into the
   existing rules-reasoner-only engine — `Reasoner` interface should already support this per DR-17
   design, this is additive not a rewrite), `internal/api` (+ embedded `web/` SPA — check
   `docs/architecture/features/F12-ui-api-integrations.md` for the UI screen list and REST/MCP
   endpoint table), `cmd/traceiq` wiring (assemble every package into the real binary — this is
   where all the "cmd wiring" deferred items from earlier phases finally connect).

6. **Wave 16 — review batch 4**, then **final whole-codebase review** (ONE opus agent, most
   capable model — this is a gate, not a routine review) per
   `superpowers:requesting-code-review`'s pattern, covering the full diff since the architecture
   sign-off commit (there is no commit yet — cover the whole `internal/`+`cmd/` tree). One fix
   wave if findings, one scoped re-check, done.

7. **Testing phase (Wave 18) — ⚠️ HARD GATE, DO NOT SKIP:**

   **STOP before dispatching Wave 18 and ask the user to confirm the Istio cluster is up.**
   User decided (2026-09-19) on **live mode**, not offline-replay-only, for the S01–S23 run — this
   needs a real cluster, not just `internal/eval`'s fixture replay. Before dispatching any Wave 18
   agent:
   1. Ask the user: "Is the Istio Transaction Lab cluster up (Docker Desktop Kubernetes, sidecar
      injection active, both ingress gateways reachable)?" — per prior session memory, the lab
      lives in the sibling project `C:\Users\shakr\OneDrive\Desktop\shakti-data\claude\Istio_testing`,
      runs on Docker Desktop's Kubernetes (not Minikube), and is reached via NodePort (not
      localhost:80 — that returns 404; the correct ingress gateway depends on which bank config is
      used, there are two).
   2. If the user says it's not up yet, **stop and wait** — do not proceed with a "we'll just use
      offline mode instead" fallback without asking; the user specifically wants live validation.
   3. Only once confirmed up: dispatch the testing agent(s) — build/run an end-to-end integration
      scenario (synthetic OTLP trace generator → ingest → sampler → store → anomaly → rca) plus
      drive the S01–S23 catalog through TraceIQ's `internal/eval` in **live mode** (inject a real
      fault via the lab's existing `run.sh`-style scripts, let TraceIQ ingest real traces from the
      cluster, score the RCA verdict). Known constraint: only ~4 of 6 rca rules are implemented
      (see Batch 2 rca report) — scenarios outside what those rules can explain are expected
      misses, log them as scoped gaps, not defects, unless the user wants the remaining 2 rules
      built first.
   4. Log every real defect found to `Testing_defect.md` (repo root, create if absent) with
      severity, repro steps, fix status. Loop fix→retest until the defect log is clean or only has
      accepted/deferred items with explicit rationale.

8. **Sign-off swarm:** four short agents (haiku or sonnet, cheap — this is a formality gate, not
   deep analysis) each writing one file in `docs/signoffs/`: PM sign-off (scope delivered vs
   PRD), dev-lead sign-off (code quality, review trail clean), test-lead sign-off (defect log
   clean), architecture sign-off already exists — just needs a final "implementation matches
   design" confirmation appended or a new `implementation-signoff.md`.

9. **Final summary to user:** what was built, what's deferred and why, total cost vs $30 cap, how
   to build/run it (`cmd/traceiq`), where every doc/report lives.

---

## Files that matter (quick index)

| Purpose | Path |
|---|---|
| Chronological narrative log (READ THE TAIL FIRST) | `docs/ledger.md` |
| This file | `docs/CHECKPOINT.md` |
| Cost tracking | `docs/cost-ledger.md` |
| Binding spec, all decisions | `docs/architecture/06-decision-register.md` |
| Feature specs | `docs/architecture/features/F01..F12-*.md`, `X-SEC-*.md`, `X-OPS-*.md` |
| Architecture sign-off | `docs/signoffs/architecture-signoff.md` |
| Implementation reports | `docs/reports/w9-*.md` (batch 1), `w10-review-*.md` (batch 1 review), `w11-*.md` (batch 2) |
| Import-boundary enforcement | `internal/archtest/archtest_test.go` |
| Go module root | `go.mod` (module traceiq, go 1.27, toolchain go1.27.1) |

**Do not re-read every architecture doc from scratch on resume** — this checkpoint plus the
ledger tail is sufficient. Read a spec doc only when a specific wave's dispatch prompt needs its
exact FR/AC/DR text (same discipline used throughout: cite line ranges, don't re-absorb whole
files).
