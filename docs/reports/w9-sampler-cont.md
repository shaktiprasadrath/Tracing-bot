# w9-sampler-cont — TDD continuation for internal/sampler

Continuation of an interrupted pass. All eight files that existed on entry
(`assembly.go`, `policy.go`, `predicate.go`, `red.go`, `sampler.go`,
`spanutil.go`, `tokenbucket.go`, `wal.go`) already compiled cleanly
(`go build`/`go vet` both clean before any change in this pass) — `wal.go`
was complete, not mid-write: `WAL` interface (`Append`/`Truncate`/`Replay`)
and `MemWAL` were fully implemented.

## What changed in internal/sampler

1. **`internal/sampler/predicate.go`** — fixed a real gap against DR-11:
   `simplePredicateSet.Add` had no `max_predicates` bound at all (would grow
   unbounded). Added:
   - `defaultMaxPredicates = 32` (DR-11's config default) and
     `NewPredicateSetWithLimit(max int)` so tests can exercise eviction
     without 32 fixtures; `NewPredicateSet()` now delegates to it.
   - Eviction in `Add`, scoped per `p.Tenant`: when at the bound, evicts the
     lowest-`Hits`, soonest-`ExpiresAt` predicate for that tenant (DR-11
     "Eviction at max_predicates"), before appending the new one.
   - `Evicted() uint64` accessor (stand-in for
     `traceiq_sampler_predicates_evicted_total`).
   - **Documented deviation**: DR-11 also requires refusing the add with
     `429 predicate_limit` if eviction would remove a `ScopeInvestigation`
     predicate belonging to a *running* investigation. This package has no
     visibility into `rca`'s investigation lifecycle (DR-2's import ban), so
     that refusal is left as a future API-layer (`cmd/traceiq` handler)
     concern; `Add` here always evicts the lowest-Hits/soonest-expiring
     candidate unconditionally.

No other production code changed — everything else in the package was
already correct against the spec paths this pass exercised (see "Known gaps
not touched" below for the one thing that is NOT fixed).

## Test files added (22 tests, internal/sampler/*_test.go)

`testhelpers_test.go` (`fakeClock` mirroring `internal/topology/livegraph_test.go`'s
pattern, `mkTraceID`/`mkSpanID`/`mkSpan` builders — no production code
touched), plus:

| File | Tests | AC/FR coverage |
|---|---|---|
| `ring_test.go` | `TestShardFor_Deterministic`, `TestShardFor_ResizeRemapsApproxOneOverNPlusOne` | (a) DR-8: same-input determinism, all members reachable, N→N+1 resize remaps ~1/(N+1) of keys (statistical band [0.7×,1.4×] around 1/9 at 20k keys) and *only* onto the new member (never reshuffled among existing members) — the property that actually distinguishes rendezvous hashing from the deleted mod-N scheme. AC-F02-1. |
| `policy_test.go` | 8 tests | (b) `TestEvaluate_ErrorAlwaysKept` / `TestEvaluate_ErrorNeverShedByCap` — Error kept, never shed even at `max_keep_rate=0`. (c) `TestEvaluate_HealthyKeptAtConfiguredFloorRate` — 1,000,000 random trace IDs at `healthy_sample_rate=0.01`, keep rate asserted in [0.9%,1.1%] (AC-F02-4's exact bound), plus a 20k-ID determinism re-run against a fresh `*DefaultPolicy` instance. (d) `TestEvaluate_RarePathKeptOnceThenDownsampled`, `TestEvaluate_RarePathRespectsTokenBucket` — first occurrence of a path signature kept `KeepRare`, immediate repeat falls through to `KeepDropped`; token bucket capacity respected. (h) `TestAdjustFloor_ChangesEffectiveFloor` — per-tenant override, no cross-tenant leakage, and the adjusted floor actually drives `Evaluate`'s probabilistic branch (not just bookkeeping). |
| `tokenbucket_test.go` | 2 tests | (h) burst-then-refill-per-second math, capacity ceiling never exceeded after a long idle gap. |
| `predicate_test.go` | 7 tests | (e) `Match` sets `Matched`/`PredicateID`/`Scope` and increments `Hits`; non-matching trace doesn't fire; `Evaluate` wires a matched `InterestMatch` to `Reason=KeepInterest`; AC-F02-16 (error+interest → `Reason=KeepError`, `MatchedPredicateID` still set, `SecondaryReasons` carries the Interest bit); TTL expiry via `ExpireDue` (expired removed, non-expired and zero-`ExpiresAt` kept); `max_predicates` bound enforcement + eviction priority (lowest-Hits before soonest-expiring), scoped per tenant. |
| `red_test.go` | 2 tests | (f) RED extracted at `Consume` time for both a trace that ends up kept (error) and one that ends up dropped by a saturated `max_keep_rate` cap — proven by draining `REDSamples()` *before* `Tick` ever runs; plus multi-span/multi-batch accumulation correctness (`Calls`/`Errors`/`DurationSumNanos` sum correctly). |
| `assembly_test.go` | 5 tests | (g) idle_timeout finalize (not before, fires after); hard_timeout finalize despite continuous sub-idle activity (AC-F02-2's two cases); negative control (fresh trace untouched); `FlushAll` force-completes every open trace as `Keep=false/Reason=KeepShed` (FR-F02-9, "loses 0 in-flight traces" = a Decision is recorded for every trace, not that shutdown persists trace bodies — see `Decision.Keep`'s own doc comment in `sampler.go`). |

All 22 tests pass. Full suite: `go build ./...`, `go vet ./...`, and
`go test ./...` are all green (`internal/archtest`'s DR-31/DR-2 boundary
checks included).

`TestEvaluate_HealthyKeptAtConfiguredFloorRate` runs ~30s (1M `Evaluate`
calls at a frozen test clock, so `DefaultPolicy`'s rolling-window history
never prunes and grows unbounded for the duration of the test — an
artifact of holding time still for a million calls, not a production
concern since real traffic bounds the window to whatever arrives in 60
wall-clock seconds). Left as-is to match AC-F02-4's literal "1,000,000
trace-IDs" wording; flagging in case CI time budgets matter.

## Bugs found while writing tests, and how they were resolved

1. **Real implementation gap, fixed**: `simplePredicateSet.Add` had no
   `max_predicates` bound (see above) — fixed in `predicate.go`.
2. **Two test-authoring mistakes, caught by first-run failures, fixed in the
   test, not the implementation** (verified each was a wrong assumption
   against the already-correct implementation, not a real bug):
   - `TestManager_FlushAll_...`: initially asserted `Keep=true` for
     `FlushAll`'s forced decisions. `Decision.Keep`'s own doc comment
     (`sampler.go`) states `KeepShed` is a `Keep=false` class, same as
     `KeepDropped`; DR-9's "loses 0 in-flight traces" means every open trace
     gets a recorded `Decision` (and had its RED already captured at
     `Consume` time), not that it gets persisted. Fixed the assertion to
     `Keep=false`.
   - `TestEvaluate_RarePathRespectsTokenBucket`: initially left
     `MaxKeepRate` at its default (0.25) while testing token-bucket
     admission in isolation. With a frozen clock, the very first kept trace
     saturates the rolling-window rate to 100% (1 kept / 1 decided), so the
     *second* legitimately-token-approved rare keep was correctly converted
     to `KeepShed` by the (unrelated, working-as-designed) `max_keep_rate`
     hard cap — consuming the bucket token but not showing up as a
     `KeepRare` decision. Fixed by setting `MaxKeepRate=1.0` to isolate the
     bucket behavior being tested, per this file's own established pattern
     of disabling unrelated classes.

## Known gaps not touched in this pass (flagged, not fixed — time budget)

- **AC-F02-13's fixed shed order** (`Probabilistic → Floor →
  Interest(Recurrence) → Rare → Slow`, Error never shed) is **not**
  implemented as a priority-ordered admission control. `policy.go`'s current
  `max_keep_rate` cap sheds *any* non-Error kept decision uniformly once the
  tenant's rolling-60s rate exceeds the cap — it does not protect
  higher-priority classes (Slow, Rare) over lower-priority ones
  (Probabilistic, Floor) the way DR-10 specifies. This was **not** in this
  pass's required minimum test list ((a)-(h) in the task brief) and
  implementing correct priority-ordered shedding for a purely streaming,
  one-decision-at-a-time evaluator (no batch/retroactive reordering) is a
  non-trivial design question in its own right — left as a follow-up rather
  than rushed. `TestEvaluate_ErrorNeverShedByCap` in this pass does confirm
  the one part of AC-F02-13 that's already correct (Error is genuinely never
  shed).
- The `Sampler` interface (DR-10) has no concrete type combining `Manager` +
  `DefaultPolicy` + `PredicateSet` into one implementation —
  `SetInterestPredicate`/`RemoveInterestPredicate`/`ListInterestPredicates`/
  `AdjustFloor` (the `Sampler` interface methods) are not wired anywhere.
  Tests in this pass exercise `Manager`, `DefaultPolicy`, and
  `simplePredicateSet` directly against their own concrete APIs, which was
  sufficient for every item in the required minimum list. Building the
  wiring type was judged out of scope for a test-writing pass on already-
  written logic; flagging for whoever wires `cmd/traceiq`.
- `AssemblyConfig`'s documented deviation (already noted in `assembly.go`
  before this pass): `max_open_traces_per_shard` and
  `memory_high_watermark_bytes` are not enforced; `Tick` only finalizes on
  idle/hard timeout. Not touched.
- `AC-F02-14` (`PathSignature` ordering — different call order ⇒ different
  signature) and `AC-F02-12`/`AC-F02-15` (latency benchmarks) were not in
  the required minimum list and were not added given the time budget.

## Files touched

- `internal/sampler/predicate.go` (bug fix: max_predicates bound + eviction)
- `internal/sampler/testhelpers_test.go` (new)
- `internal/sampler/ring_test.go` (new)
- `internal/sampler/policy_test.go` (new)
- `internal/sampler/tokenbucket_test.go` (new)
- `internal/sampler/predicate_test.go` (new)
- `internal/sampler/red_test.go` (new)
- `internal/sampler/assembly_test.go` (new)
- `docs/reports/w9-sampler-cont.md` (this file)
