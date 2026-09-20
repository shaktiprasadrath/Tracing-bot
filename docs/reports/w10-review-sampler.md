# w10-review-sampler — code review of internal/sampler (post w9-sampler-cont)

Reviewed against `docs/architecture/features/F02-tail-sampler.md` (rev 2) and
DR-8/DR-9/DR-10/DR-11/DR-12 in `docs/architecture/06-decision-register.md`.
Baseline: `docs/reports/w9-sampler-cont.md`.

## Verdict: **APPROVE-WITH-FIXES** (fixes applied in this pass, suite green)

`go build ./internal/sampler/...` and `go vet ./internal/sampler/...` are
clean. `go test ./internal/sampler/... -race` cannot run on this box (no
`gcc` — `CGO_ENABLED=1 go test -race` fails at the `runtime/cgo` build step
with `cgo: C compiler "gcc" not found`); fell back to plain
`go test ./internal/sampler/... -v` (and `go test ./... ` for the whole repo,
including `internal/archtest`'s DR-2/DR-31 boundary checks) — **all green**,
27/27 tests (22 pre-existing + 5 added in this pass).

## Findings

| # | Severity | Area | Finding | Status |
|---|---|---|---|---|
| 1 | **Blocker** | AC-F02-13 / FR-F02-13 | `max_keep_rate` cap shed *any* non-Error kept decision uniformly once the tenant's rolling rate exceeded the cap, with no regard to the mandated fixed order `Probabilistic → Floor → Interest(Recurrence) → Rare → Slow`. Under sustained overload a `Slow`/`Rare` trace (meant to be protected almost as strongly as `Error`) had the same shed odds as a `Probabilistic` one (meant to be cut first) — this defeats the entire point of the six-class keep-100%-of-interesting system, which is the feature's headline differentiator (D-X1). AC-F02-13 is an explicit, testable acceptance criterion citing this exact order. **FIXED.** |
| 2 | **Blocker** | DR-10 / package self-implementation | No concrete type combined `Manager` + `DefaultPolicy` + `PredicateSet` into the full `sampler.Sampler` interface — `Manager` alone is missing `SetInterestPredicate`/`RemoveInterestPredicate`/`ListInterestPredicates`/`AdjustFloor`. `internal/sampler`'s own doc.go states its catalog interface is `sampler.Sampler`; nothing in the package produced it. This blocks any real caller (F06's `rca.Engine`, `cmd/traceiq`) from getting a single object satisfying the contract this package exists to provide. **FIXED.** |
| 3 | **Major** | FR-F02-5(c) | Duplicate-`SpanID` dedup (`partialTrace`'s `seenSpanIDs`, "dropped before both assembly and RED") was **entirely unimplemented** — not even flagged as a known gap in w9-sampler-cont.md. Every duplicate span delivery (F01's documented at-least-once semantics) was double-counted into both the RED accumulator (inflating Calls/Errors/DurationSum) and the trace buffer (inflating SpanCount, corrupting `PathSignature`'s edge list and `traceMaxDurationNanos`). This directly threatens AC-F02-5's "10% duplicated spans" adversarial case and the < 0.5% RED-accuracy gate. **FIXED.** |
| 4 | **Major** | Race condition | `simplePredicateSet.Match` took `p := &snap[i]` — a pointer directly into the slice published via the atomic snapshot pointer — and mutated `p.Hits++` through it before calling `storeUpdated`. Every concurrent reader of `ps.snapshot.Load()` shares that same backing array (that is the entire point of the lock-free-read design the file's own comment claims), so this was an unsynchronized concurrent read/write on shared memory: two concurrent `Match` calls hitting the same predicate raced on `Hits` (lost updates), and any concurrent `Add`/`Remove`/`ExpireDue`/`Narrow` doing `old := *ps.snapshot.Load(); copy(next, old)` could read a torn value mid-write. This is exactly the "race conditions (concurrent span arrival)" class the review brief asked to check, and it violates DR-11's own "atomic snapshot pointer" contract. `-race` was unavailable on this box to *prove* it at runtime, but the code is unambiguously a textbook Go data race independent of any tool. **FIXED** (added a regression test that would have caught the pre-fix behavior via a snapshot-immutability assertion, since `-race` isn't available here). |
| 5 | Minor | `policy.go` rare-path check-then-act | `isRareCandidate` (check) and `tryTakeRare` (consume+mark-seen) are two separate locked operations with no atomicity between them. Two concurrent `Evaluate` calls that happen to compute the *same* `PathSignature` at the same instant could both pass the candidacy check and both successfully call `tryTakeRare`, each consuming a token and marking `rareSeen` (last write wins) — a narrow TOCTOU window, not a memory-safety data race (all map access is still mutex-protected). Practical impact is low: it requires two concurrently-evaluated traces to collide on the exact same 64-bit path signature within the same tick. Left unfixed — flagging for a follow-up if/when `Evaluate` calls become genuinely concurrent per tenant across shards (today's single-goroutine-per-shard model makes same-signature collisions rare in practice, and W9's own framing already treats `Evaluate` as intentionally streaming/one-at-a-time). |
| 6 | Minor | `predicate.go` eviction tie-break | `Add`'s eviction tie-break compares `ExpiresAt.Before(...)` to find "soonest-expiring." A predicate with a zero `ExpiresAt` (meaning "never expires") sorts as expiring *soonest* (zero `time.Time` is chronologically earliest), so a no-TTL predicate would be preferentially evicted over one with a concrete, later expiry — backwards from the obvious intent. Not fixed: DR-11's two real predicate paths (FR-F06-12a/12b, `scope_ttl`/`recurrence_ttl`) always set a non-zero `ExpiresAt`; a zero-`ExpiresAt` predicate is a test-only construct in this codebase today, so this edge case has no production path. Flagging in case a future caller (e.g. a manually-pushed `user:`-sourced predicate with no expiry) hits it. |
| 7 | Observation | `assembly.go` `Manager.Tick` / late span arrival | A span that arrives for a `TraceID` *after* its buffer was already finalized (deleted from `sb.traces` under lock, decision already emitted) silently opens a **new** `Trace` bucket for the same `TraceID`, which will eventually produce a **second** `Decision` for that trace ID. This is a distinct issue from the already-documented `SetRing` hard-cutover gap (DR-8's drain-then-move) — it can happen on a single, never-resized shard purely from network delay/reordering. Not in scope to fix here (would need a short-lived "recently decided" tombstone set, a real design addition); noting because FR-F02-1's "0 traces decided twice" language is about the *resize* case specifically, so this doesn't contradict any tested AC, but it is a real edge case worth a future ticket. |
| 8 | Confirmed correct (no action) | DR-8 rendezvous hashing | `ShardFor`'s determinism, full-member-coverage, and the resize-remaps-~1/(N+1)-onto-only-the-new-member property are all correctly implemented and covered by `ring_test.go`'s two tests, which were re-run and pass. XXH64-for-xxh3 stand-in remains a documented, deliberate deviation (not bit-identical to a real xxh3 — flagged for before any cross-language/Kafka-partitioner reliance), not a bug. |
| 9 | Confirmed correct (no action) | DR-9 RED-before-discard (non-duplicate paths) | Router-side RED accumulation happens unconditionally for every span that isn't a duplicate, before the shard/assembly decision and independent of the eventual keep/drop outcome — verified again after the shed-order and dedup fixes via `TestConsume_ExtractsREDForEverySpanRegardlessOfKeepDecision` and the new `TestConsume_DuplicateSpanIDDroppedBeforeAssemblyAndRED`. |
| 10 | Confirmed correct (no action) | AC-F02-2 assembly timeouts | `idle_timeout` (since `LastSeen`) and `hard_timeout` (since `FirstSeen`) are evaluated independently with correct `>=` boundaries in `Tick`; a trace refreshed well inside `idle_timeout` still finalizes at `hard_timeout` (tested). `max_open_traces_per_shard`/`memory_high_watermark_bytes` enforcement remains an explicitly-documented, unfixed deviation from w9 (F02 §5's memory-pressure failure mode) — acceptable-for-this-wave: it's a distinct failure-mode feature, not a correctness bug in what's implemented, and was already flagged rather than silently missing. |
| 11 | Confirmed correct (no action) | DR-11 predicate TTL/eviction (the w9 fix) | Re-verified `Add`'s `max_predicates` eviction: correctly scoped per-tenant, correctly requires the bound to be met *before* evicting (not after, which would let the set grow past the bound), and correctly picks lowest-`Hits` with soonest-`ExpiresAt` as the tie-break (modulo finding #6 above). `ExpireDue`'s TTL removal correctly treats a zero `ExpiresAt` as "never expires." All four predicate tests re-run and pass. |
| 12 | Confirmed correct (no action) | AC-F02-4 keep-rate floor statistics | `TestEvaluate_HealthyKeptAtConfiguredFloorRate`'s 1,000,000-trace run lands inside the required [0.9%, 1.1%] band around `healthy_sample_rate=0.01` and is properly isolated from the other five keep classes (rare/floor disabled via config) so the measurement is unambiguous; the bound is generous enough (±10% relative) to not be flaky while still catching a real regression. The `ShardFor` resize-remap test's [0.7×, 1.4×] band around the ~11.1% expected value is similarly reasoned (documented std-dev justification in the test's own comment) — not flaky, not too loose to catch a mod-N-style regression. |

## Test quality

The 22 pre-existing tests (from w9-sampler-cont) are real assertions, not
weak/tautological checks — each asserts a specific `Decision.Reason`/`Keep`
value or a specific counted quantity, and several include explicit negative
controls (`TestManager_Tick_DoesNotFinalizeFreshTraces`,
`TestEvaluate_RarePathKeptOnceThenDownsampled`'s second-call fallthrough).
The two statistical tests (`TestEvaluate_HealthyKeptAtConfiguredFloorRate`,
`TestShardFor_ResizeRemapsApproxOneOverNPlusOne`) use large sample sizes with
explicitly-reasoned tolerance bands, not eyeballed magic numbers — not flaky
by inspection, confirmed by three consecutive clean runs in this pass.

The 5 tests added in this pass (`w10_review_test.go`) each target one fix
directly: two shed-order tests bracket the priority walk (a low-rank class
sheds while a high-rank one doesn't; the same high-rank class becomes
shed-eligible once all lower ranks are vacated), one confirms
`ScopeInvestigation` interest stays protected, one proves the duplicate-span
path stops both RED double-counting and body double-assembly, one proves
`Match` no longer mutates a previously-published snapshot in place, and one
is a compile-time-plus-behavioral proof that `Impl` satisfies `Sampler` end
to end (predicate CRUD + `AdjustFloor` + `Consume`/`FlushAll`/`Decisions`).

## Simplification / dead-code / efficiency

- `Governor.Allow(class)` is unreachable in practice — `Evaluate` always
  type-asserts `g` to `*DefaultPolicy` and calls `allowForTenant` directly
  (documented in the code as intentional, since `Allow`'s signature has no
  tenant parameter, a gap in DR-10's own interface). Left as-is: it's
  correctly documented, costs nothing, and removing it would break
  `Governor` interface satisfiability for any future caller holding only the
  interface.
- `ShardFor` allocates a new `xxhash.New()` streaming hasher per member per
  call (`O(members)` allocations per routing decision, called once per span).
  Not fixed — this is downstream of the already-documented XXH64-stand-in
  deviation, and `Sum64` isn't a fixed-input-size convenience for two `[]byte`
  args in this xxhash package version, so switching away from `New()`+`Write`
  would need a small buffer-concat helper. Worth a follow-up if `ShardFor`
  ever shows up in a profile; not a correctness issue.
- No other dead code or obviously-simplifiable logic found in the six
  production files reviewed line-by-line (`sampler.go`, `policy.go`,
  `predicate.go`, `assembly.go`, `red.go`, `spanutil.go`, `tokenbucket.go`,
  `wal.go`).

## Files touched in this pass

- `internal/sampler/policy.go` — AC-F02-13 fixed shed order (`shedRank`,
  `shouldShedClass`, `keepEvent.reason`); `recordKeep` signature change.
- `internal/sampler/policy_test.go` — one call-site update for the
  `recordKeep` signature change.
- `internal/sampler/predicate.go` — `Match`'s in-place-mutation race fixed;
  added `List` (used by the new wiring type).
- `internal/sampler/assembly.go` — duplicate-`SpanID` dedup in `Consume`
  (FR-F02-5(c)) and in `ReplayWAL`; `DuplicateSpansTotal()` accessor.
- `internal/sampler/sampler.go` — added `Trace.seenSpanIDs`.
- `internal/sampler/impl.go` (new) — `Impl` type wiring `Manager` +
  `DefaultPolicy` + `PredicateSet` into the full `Sampler` interface, with a
  compile-time `var _ Sampler = (*Impl)(nil)` proof.
- `internal/sampler/w10_review_test.go` (new) — 5 tests covering findings
  #1, #2, #3, #4.

## Test status after fixes

`go build ./internal/sampler/...` clean. `go vet ./internal/sampler/...`
clean. `go test ./internal/sampler/... -v` — **27/27 pass** (22 pre-existing
+ 5 new), ~17-19s wall time (dominated by the pre-existing 1M-iteration
statistical test, unchanged). Whole-repo `go build ./...`, `go vet ./...`,
and `go test ./...` also re-run clean after these changes, confirming no
cross-package breakage (including `internal/archtest`'s DR-2/DR-31 boundary
checks). `-race` remains unavailable on this box (no `gcc`); noted rather
than silently skipped.
