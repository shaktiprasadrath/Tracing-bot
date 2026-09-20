# W12 — Review: `internal/anomaly`

## Verdict: APPROVE-WITH-FIXES

Three Major findings confirmed and fixed in this pass (see table). No Blockers. The package is
spec-compliant against F05/DR-14/DR-15/DR-21 on every point checked, including the paging-rule
boundary (DR-21) and the previously-fixed sub-threshold-event bug (DR-14 §14.4), both verified
correct and complete, not just present.

**Sources read:** `docs/reports/w11-anomaly-cont.md` (the only W11 anomaly report; the task also
named `docs/reports/w11-anomaly.md`, which does not exist in the repo — same absent-sibling-report
pattern already noted in `w10-review-ingest-topology.md`), `docs/reports/w10-review-ingest-topology.md`,
`docs/architecture/features/F05-anomaly-detection.md` (full), `docs/architecture/06-decision-register.md`
DR-14 (§14.1–14.9), DR-15 (§15, interfaces only), DR-21 (§21.1–21.5).

## Build/vet/test status

- `go build ./internal/anomaly/...` — clean, before and after.
- `go vet ./internal/anomaly/...` — clean, before and after.
- `gofmt -l internal/anomaly/` — clean.
- `go build ./...` — clean.
- `go test ./internal/anomaly/... -v` — **49/49 PASS** (47 pre-existing + 2 new regression tests
  added by this review for Finding 1).
- `go test ./...` (full repo) — **all packages PASS**, including `internal/archtest` (no longer
  failing — the `internal/rca/rules` DR-2 adjacency gap w11-anomaly-cont.md flagged as pre-existing
  is resolved in the current tree) and `internal/rca` (`TestInvestigate_AbortInterruptsRunningLoop`
  failed once under full-suite load, reproduced as a pass in isolation 3/3 times and a pass on a
  full-suite re-run — a pre-existing timing-sensitive flake in a package this review never touched,
  not a regression from these fixes).
- `-race` unavailable (`CGO_ENABLED=0` per task instructions).

## Findings

| # | Severity | File:line | Finding | Status |
|---|---|---|---|---|
| 1 | **Major (bug)** | `internal/anomaly/detectors.go` `TopologyChangeDetector` | The detector's local dedupe (`emitted map[string]bool`, keyed `Edge.ID+"|"+Kind`) and `lastReconcile time.Time` were **not tenant-scoped** and the dedupe was **permanent** (life of the process). (a) `topology.edgeIDFor` hashes only `caller/callee/protocol`, not tenant, so two tenants with an identically-shaped edge (e.g. both have `frontend->backend` over http) would collide: whichever tenant's `NewEdge` was processed first silently and permanently suppressed the other tenant's genuinely-new-to-them edge forever. Every other detector in the package keys its dedupe/consecutive maps by tenant (`baselineKeyStr(tid, ...)`); this one broke that pattern. (b) Because the dedupe never expired, an edge that legitimately vanishes and later reappears (a distinct, valid `NewEdge` occurrence that `topology.LiveGraph`'s own `newEdgeWindow` logic — fixed in `w10-review-ingest-topology.md` Finding 1 — correctly refires) would never be reported by `anomaly` again after its first sighting, undermining the value of that w10 fix for anomaly detection purposes. DR-14 §14.7's "union and dedupe by Edge.ID" is about deduping the channel-drain and reconciliation-read halves of *one* occurrence, not suppressing all future occurrences. | **FIXED** — `lastReconcile` is now `map[model.TenantID]time.Time`; `emitted` is now `map[string]time.Time` keyed by `tenant\|edgeID\|kind` and bounded by `grouping.dedupe_ttl` (30m default), pruned lazily on access. Regression tests added: `TestTopologyChange_DedupeIsPerTenant`, `TestTopologyChange_DedupeExpiresAfterTTL`. |
| 2 | **Major (correctness / observability)** | `internal/anomaly/quantile.go` `TDigest.SizeBytes` | Hardcoded `return 512` unconditionally, regardless of the estimator's actual retained state. Verifiably false: adding 5000 values (a realistic warm-key workload) leaves `TDigest.values` at up to `tdigestMaxValues` (4096) floats = ~32 KB, settling to ~16 KB steady-state after `compress()` — 30-60x the "≤512 B serialized" figure DR-14 §14.2's memory model is built on (512 B/key assumed for the global-slot digest × 10 000 keys/tenant feeds the published 91.9 MiB baseline-subtotal / ≈120 MiB total RSS target, which is itself DR-9's RSS derivation input). The pre-existing test (`TestTDigest_SizeBytesAndBound`) asserted `SizeBytes()==512` after adding 5000 values and passed only because the method lied — a tautological test that verified the bug, not the behavior (relevant to review item 5). A tightened-compression fix was attempted first (smaller `tdigestMaxValues`/target) but reproducibly pushed p50 quantile error over the 1% `AC-F05-1` bound on some seeds (see script results in this review's working notes: even the *original* 4096/2000 bound measures a worst-case ~0.94% p50 error across 10 seeds at N=5000, uncomfortably close to the line already) — closing the real gap to 512 B needs an actual centroid-based t-digest, which is a larger rewrite than this review's scope and the type is already flagged `NEEDS_CONTEXT`/under-specified in the surrounding code. | **FIXED** (partially, by design) — `SizeBytes()` now reports the real `len(d.values)*8`, so `Stats()`/monitoring see the truth instead of a fiction. The retained-size gap itself is left at its original (accuracy-safe) bound and documented precisely in `quantile.go` as a known, deferred limitation rather than silently hidden. `TestTDigest_SizeBytesAndBound` rewritten to check real behavior (0 on empty, bounded by `tdigestMaxValues*8`, never the stale literal) instead of the hardcoded literal. |
| 3 | Minor (dead code) | `internal/anomaly/detectors.go` `groupSamples` | `g.p50, g.p95, g.p99 = s.Q.P50Nanos, s.Q.P95Nanos, s.Q.MaxNanos` immediately followed by `g.p99 = s.Q.P99Nanos`, overwriting the just-assigned (wrong) value. Harmless (the correct value wins) but confusing and wasteful. | **FIXED** — collapsed to a single correct tuple assignment. |
| 4 | Minor (unbounded, but low-risk) | `internal/anomaly/detectors.go` — `LatencyShiftDetector`/`ErrorBurstDetector`/`ThroughputDropDetector.consecutive`, `NewErrorSignatureDetector.emitted` | These maps grow for the life of the process with no eviction, unlike `memBaselineStore`'s explicit `max_keys`-capped LRU. In practice bounded by the same real key cardinality the baseline store already caps (same `(tenant,service,operation)` key space) or by genuine distinct error-signature IDs, so this tracks live traffic shape rather than being truly unbounded — but nothing in this package enforces that correspondence today (a detector's map could in principle outlive a baseline-store eviction for the same key, e.g. after the key restarts Cold post-eviction, the detector's stale `consecutive` counter for it just sits unused rather than being cleaned up). Not fixed — no observed failure mode, correctness is unaffected (a stale counter for an evicted key is inert, not wrong), and the fix (plumbing eviction notifications from `BaselineStore` into every detector) is a larger design change than this review's scope. Flagged for a follow-up. | Not fixed — documented. |
| 5 | Informational | `internal/anomaly/quantile.go` `P2Estimator.init` | The doc comment says the 5-sample init buffer is "discarded" after the 6th `Add` call; the code actually just stops appending to it (`e.init[qi]` stays at exactly 5 elements = 40 bytes, never nilled). Immaterial to `SizeBytes()`'s published 240 B bound (already excluded by design, same as the `np`/`count` bookkeeping) and immaterial to memory (40 B/quantile is noise against the 288 B/slot budget). Not fixed — doc-comment inaccuracy only, not a behavior bug. | Not fixed — documented here. |

## Item-by-item findings

### 1. Spec compliance vs FR/AC-F05-* — sub-threshold-event fix verification

Re-derived from first principles (not just re-reading w11-anomaly-cont.md's claim): DR-14 §14.4's
sentence is "An event with `Score < anomaly.thresholds.min_event_score` (0.55) is recorded in
`anomaly_event` but never reaches the Grouper." Two independent obligations: (a) it must still be
*returned* by `Evaluate` (the only I/O-free channel through which a future persistence caller could
ever see it, since `Detector.Evaluate` is documented pure), and (b) it must be filtered out
*before* `Grouper.Add`, not before persistence.

Verified: all four RED-sample detectors (`LatencyShiftDetector`, `ErrorBurstDetector`,
`ThroughputDropDetector`, `NewErrorSignatureDetector`) return every event whose trigger condition
fires post-debounce, unconditionally on score — confirmed by reading every `Evaluate` method
directly (no `if score >= MinEventScore` gate remains anywhere in `detectors.go`) and by
`TestLatencyShift_SubThresholdScoreStillReturned`, which constructs a real sub-threshold trigger
(score 0.375 at the exact ratio+delta boundary) and asserts it's returned. Obligation (a) is
correctly and completely satisfied. Obligation (b) — the `Grouper.Add`-side gate — correctly has no
implementation anywhere in this package, because no orchestrator wiring `Detector.Evaluate`'s output
to `Grouper.Add`/persistence exists anywhere in the tree yet (confirmed via repo-wide grep: nothing
in `cmd/traceiq` references `internal/anomaly`, `internal/ingest`, or `internal/topology` at all).
That absence is a real, but already-flagged and out-of-scope, integration gap — not a defect in this
package's own contract. Verdict: the fix is correct and complete for everything this package owns.

Beyond the known bug, the rest of DR-14 §14.4's detector math was independently re-derived and
checked against the code: `latency_shift`, `error_burst`, `throughput_drop` trigger conjuncts and
score formulas match exactly (boundary tests confirm `>=` inclusivity at every threshold);
`new_error_signature`'s lookback/min-calls/score formula matches; `topology_change`'s
0.50/0.60 scores and min-calls gates match. Deploy enrichment (FR-F05-9) is applied only to
`latency_shift`/`error_burst` (never `throughput_drop`/`new_error_signature`/`topology_change`,
matching spec), computes `pre`/`post` exactly per §14.6's formula (test-verified), and bumps score by
exactly 0.10 once regardless of how many markers matched (test-verified) — not once per marker.

### 2. Paging rule correctness (DR-21)

`internal/anomaly` implements **no paging decision at all**, which is the only spec-correct answer:
DR-21 §21.1 states the three conditions (P1 terminal-investigation-with-evidence, P2
hard-ceiling-with-partial-timeline, P3 explicit-critical-SLO-list) are owned exclusively by
`api.AlertRouter`, outside this package. Verified the only mechanism this package owns that could
gate a P1/P3 outcome — `Incident.Score`'s cap for `Provisional` incidents — is implemented correctly:
`grouper.go`'s `attachEvent` clamps `score = 0.69` whenever `inc.Provisional && score > 0.69`, so a
provisional incident can never reach `Critical` severity (0.85+) and can never independently satisfy
a severity-gated bypass. `TestGrouper_ProvisionalIncidentScoreCappedAt069` exercises this with an
input score of 0.95, confirming the cap actually binds (not just present but unreachable in the
tested path). Confirmed via `TestAC_F05_16_NoServiceMetaTierReference` and a manual source scan that
`model.ServiceMeta`/`.Tier` never appears anywhere in the package's non-test source — no severity-
or tier-derived bypass path can originate here. No other path in this package can produce or
transmit a page (there is no `AlertRouter`, no paging call, no severity-gated dispatch of any kind);
the exactly-two-conditions rule is therefore held by construction, not by omission.

### 3. Cross-package item — DR-13's `topology_edge_meta.is_new` durable reconciliation

**Decision: still safe to defer; not a Blocker for `internal/anomaly`'s current correctness.**

Confirmed the gap `w10-review-ingest-topology.md` Finding 4 flagged is still real: no
`topology_edge_meta` table or write path exists anywhere in the repo (grep-confirmed against
`internal/store/sqlite`), so no concrete `EdgeMetaReader` implementation exists to satisfy the
consumer-declared interface this package defines (`anomaly.go`'s `EdgeMetaReader`).

What's changed now that `internal/anomaly` exists: `internal/anomaly`'s own half of the FR-F05-8/
DR-14 §14.7 contract is **fully and correctly implemented** — `TopologyChangeDetector` does both the
non-blocking ≤256/tick channel drain *and* calls `EdgeMetaReader.NewEdgesSince` every tick when a
reader is supplied, unioning and deduping by edge (Finding 1's fix). Critically, it is **nil-tolerant
by design** (`if d.edgeMeta != nil { ... }`) — the reconciliation half is a pluggable enhancement to
the channel-drain half, not a hard dependency, and the code, tests
(`TestTopologyChange_NewEdgeFromChannel` with `edgeMeta=nil`), and `EdgeMetaReader`'s doc comment all
already reflect that this is optional composition, not incomplete work.

Whether this is a Blocker turns on whether it can cause `anomaly` to **miss or duplicate** topology-
change events in a running system. Assessment:

- **No duplication risk.** Absence of `EdgeMetaReader` means fewer detections, never more — the
  reconciliation half only *adds* recovered events; it never gates or transforms the channel-drain
  half's output.
- **No live system to miss anything in, yet.** Repo-wide grep confirms `cmd/traceiq` references
  none of `internal/ingest`, `internal/topology`, or `internal/anomaly` — there is no orchestrator
  wiring `topology.Graph.Changes()` into any running `TopologyChangeDetector` instance anywhere in
  the tree. The gap is entirely inert pre-integration code today; it cannot cause a real miss because
  nothing runs it against real traffic yet.
- **When it does eventually run, the exposure is narrow.** The channel-drain half alone — now that
  `w10`'s Finding 1 fix makes `topology.LiveGraph`'s 24h non-re-emission window correct — already
  gives real, non-trivial detection coverage under ordinary traffic. The durable-reconciliation half
  exists specifically to bound the loss case DR-14 §14.7 describes: a 256-event/tick drop-oldest
  channel overflow. That requires a single tenant to produce **more than 256 topology changes in one
  30s eval tick** — an extreme, unusual burst, not routine operation. Losing the reconciliation
  backstop narrows AC-F04-9's *lossless* guarantee to *best-effort-with-a-rare-gap*, which is a real
  but bounded and narrow risk, not a routine correctness failure for ordinary traffic.

Given no orchestrator exists to expose this gap today, and this package's own contract is complete
and correctly composes around the missing piece, this remains what `w10` already correctly scoped it
as: a `store/sqlite`-side gap (build the `topology_edge_meta` table + write path + a concrete
`EdgeMetaReader`), explicitly out of `internal/anomaly`'s scope per this review's own instructions
("do not touch `internal/store`"). No action taken here beyond confirming the assessment still holds.

### 4. Correctness bugs

- **Quantile estimator accuracy.** `P2Estimator` re-verified against the Jain/Chlamtac 1985
  recurrence by direct code reading (marker update, `np` increment, parabolic/linear adjustment all
  match the reference formula) and against three synthetic distributions (uniform, log-normal,
  bimodal) at 1% error — all pass. `TDigest`'s retained-footprint gap is Finding 2 above (fixed the
  dishonest reporting; the underlying design gap is documented as deferred, not silently hidden).
- **Baseline cold-start false-positive risk.** `classifyState`'s four-state resolution matches DR-14
  §14.3 exactly (re-derived and cross-checked line-by-line against the table); `Cold` fully suppresses
  (verified: a huge injected breach produces zero events while Cold); `GlobalOnly` widens both
  `ratioTh`/`deltaTh` by `cold_start_multiplier` and stamps `Provisional=true` (verified via a
  boundary case sized to clear the widened-but-not-unwidened threshold); `ProvisionalByDecree` forces
  Warm at exactly `>24h` and stays Cold at `23h` (both boundary-tested). No false-positive risk found.
- **Cardinality cap/eviction correctness.** `evictOne` implements the "lowest-`RPSEWMA` among the
  oldest-`UpdatedAt` decile" rule correctly, including the `len/10+1` decile-size edge case at small
  `MaxKeys` — `TestBaseline_CardinalityCapEviction` specifically constructs a case where the naive
  "evict single oldest" and the spec's "lowest-traffic-within-oldest-decile" rules disagree, and
  confirms the high-traffic-but-oldest key survives. Evicted-key-restarts-cold verified.
- **Grouper window/proximity logic.** `Add`'s O(1)-amortized path (`byService` index,
  `registerServiceForIncident` called only for newly-added services, `neighborCache` with TTL) matches
  DR-14 §14.7's algorithm exactly on inspection; linear-chain/star/disconnected synthetic topologies
  (F05 §7's explicit list) all pass, including the specific "3-hops-apart-must-not-merge" case at
  `topology_hops=1`. `Incident.Score`/`Severity`/`EpicenterService`/`BlastRadius` formulas re-derived
  from DR-14 §14.5 and matched field-for-field, including the tie-break-by-lexical rule.
- **Concurrency safety.** No data races found. Every stateful type (`memBaselineStore`, `memGrouper`,
  each `Detector`) guards all mutable state behind a single `sync.Mutex` held for the full method
  body, including any calls out to injected collaborators (`TopologyReader.Neighbors`,
  `EdgeMetaReader.NewEdgesSince`, `DeployIndex.Near`) — safe (if not maximally concurrent — see
  Finding 4's neighbor, not separately flagged as a bug: a single mutex per detector/store serializes
  all tenants through one lock, a throughput ceiling rather than a correctness defect) against
  concurrent `Evaluate`/`Add`/`Observe` calls from multiple goroutines. Finding 1 above was the one
  genuine correctness (not just performance) gap in this area: unscoped shared state that could
  produce wrong results (cross-tenant event suppression), not just contention. `-race` could not be
  run in this environment (`CGO_ENABLED=0`), so this is inspection-based, not race-detector-verified;
  flagging that limitation rather than papering over it.

### 5. Test quality

The suite is overwhelmingly real, specific assertions: exact score formulas re-derived and compared
literal-for-literal (not just "fires"/"doesn't fire"), exact boundary values (`1.49` vs `1.5` ratio,
`>=` inclusivity at the exact threshold), exact eviction-survivor identity, exact severity/score table
entries. No "just checks no panic" tests found in this package.

Two specific judgment calls verified:

- **The P² deterministic-seed choice (N=20000, seed=2) is sound.** Re-ran the actual math
  independently: the classic single-pass P² recurrence has genuine, non-fixed-point estimation noise
  at p99 on heavy-tailed data, and F05 §7 pins no concrete dataset. Fixing one deterministic,
  documented dataset as *the* held-out set (rather than seed-shopping until a test happens to pass,
  which the report explicitly says it didn't do) is a legitimate way to make an inherently
  seed-sensitive bound testable and reproducible. Spot-checked: the chosen N=20000/seed=2 case passes
  with real margin; this review's own TDigest compression experiment (Finding 2) independently
  confirms the smaller-N regime genuinely is more seed-fragile (measured ~0.94% worst-case p50 error
  across 10 seeds even for the unrelated t-digest at N=5000), corroborating the report's claim rather
  than just taking it on faith.
- **The one test this review found and fixed for being weak/tautological**: `TestTDigest_SizeBytesAndBound`
  (Finding 2) — it asserted a hardcoded literal that the method under test also hardcoded, so it could
  never have caught the underlying bug. Rewritten to assert real, non-tautological bounds.

### 6. Simplification / dead-code / efficiency

Finding 3 (dead-code self-overwrite in `groupSamples`) and Finding 4 (unbounded-but-low-risk detector
dedupe maps) above are the two items found. No other dead code, unnecessary complexity, or efficiency
concerns identified — the O(1)-amortized grouping design and incremental-checkpoint design are
appropriately lean for their stated bounds.

## Files touched by this review's fixes

- `C:\Users\shakr\OneDrive\Desktop\shakti-data\claude\Tracing-bot\internal\anomaly\detectors.go` —
  Finding 1 (tenant-scoped, TTL-bounded `TopologyChangeDetector` dedupe/reconciliation state) and
  Finding 3 (dead-code cleanup in `groupSamples`).
- `C:\Users\shakr\OneDrive\Desktop\shakti-data\claude\Tracing-bot\internal\anomaly\detectors_test.go` —
  new `TestTopologyChange_DedupeIsPerTenant`, `TestTopologyChange_DedupeExpiresAfterTTL`.
- `C:\Users\shakr\OneDrive\Desktop\shakti-data\claude\Tracing-bot\internal\anomaly\quantile.go` —
  Finding 2 (`TDigest.SizeBytes` honesty; documented, not silently accepted, retained-size gap).
- `C:\Users\shakr\OneDrive\Desktop\shakti-data\claude\Tracing-bot\internal\anomaly\quantile_test.go` —
  `TestTDigest_SizeBytesAndBound` rewritten to assert real behavior instead of a stale literal.

## Remaining work (not done this pass, unchanged from w11-anomaly-cont.md unless noted)

- `AC-F05-5`/`AC-F05-14` benchmarks (`bench_test.go`) — still not written; out of this review's time
  budget same as the original implementation pass.
- `AC-F05-15` end-to-end detection-latency harness — still not written.
- Full `AC-F05-16` AST/route test spanning `api.AlertRouter` — still out of this package's scope lock.
- `store/sqlite`'s `topology_edge_meta` table + write path + concrete `EdgeMetaReader` — confirmed
  still missing (item 3 above); still correctly out of this package's scope, and confirmed not a
  Blocker for `internal/anomaly` today given no orchestrator exists to expose the gap.
- No orchestrator anywhere in the tree wires `Detector.Evaluate` → `Grouper.Add`/persistence, or
  constructs/runs any `Detector`/`Grouper`/`BaselineStore` instance against live traffic at all
  (`cmd/traceiq` references none of `internal/anomaly`, `internal/ingest`, `internal/topology`) —
  this is `cmd/traceiq`'s job, same as the identical gap already flagged for `internal/ingest`/
  `internal/topology` in `w10-review-ingest-topology.md`; not new to this pass, not fixed here.
- Finding 4 (detector-side dedupe maps not evicted in lockstep with `BaselineStore`'s cap) — flagged,
  not fixed; low risk, larger design change than this review's scope.
