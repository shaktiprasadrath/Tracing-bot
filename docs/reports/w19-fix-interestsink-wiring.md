# w19 — Fix: wire DR-11/D-X5's `InterestSink` into the running binary

**Date:** 2026-09-20
**Scope:** `cmd/traceiq` only (adapter + wiring + test), plus the D-X5 section of
`docs/signoffs/architecture-final-signoff.md`. No change to `internal/sampler` or `internal/rca`.
**Trigger:** `docs/signoffs/architecture-final-signoff.md` §1.3 — the chief architect's final
sign-off found that DR-11/D-X5's agent-to-sampler interest-predicate feedback loop (TraceIQ's
PRD-level differentiating wedge) was fully implemented and unit-tested on both sides but never
connected in the running binary: `cmd/traceiq/system.go:153` passed `nil` for `rca.Engine`'s
`InterestSink` parameter.

---

## 1. What was broken

- `internal/sampler.Impl.SetInterestPredicate`/`RemoveInterestPredicate` (DR-10/DR-11): fully
  implemented, exercised by `internal/sampler/w10_review_test.go`.
- `internal/rca/interest.go`: `InterestSink` interface, `InterestPredicate` type, `scopePredicate`
  (phase A) and `recurrencePredicate` (phase B) construction: fully implemented, exercised by
  `internal/rca/engine_test.go`'s `TestInvestigate_InterestPredicateLifecycle` and siblings, using a
  `fakeInterestSink`.
- The two sides were never connected: `cmd/traceiq/system.go` constructed `rca.NewEngine(...)` with
  a literal `nil` for the `InterestSink` argument. `rca.InterestSink`'s doc comment documents `nil`
  as a valid, best-effort-skip value, so nothing crashed — the loop just silently never closed. No
  running instance of TraceIQ ever let an RCA investigation steer what the sampler kept.
- `internal/rca/interest.go`'s own doc comment named the fix exactly: *"cmd/traceiq ... adapts
  between the two one-for-one when it wires sampler.Sampler into an rca.InterestSink"* — the design
  had already anticipated this step; it simply hadn't been written.

The two `InterestPredicate` structs (`rca.InterestPredicate` in `internal/rca/interest.go`,
`sampler.InterestPredicate` in `internal/sampler/sampler.go`) are declared independently but
field-for-field identical, specifically so a one-for-one adapter would need no business logic beyond
a copy. `PredicateScope` is likewise `uint8`-backed on both sides with matching constants (`1` =
investigation scope, `2` = recurrence scope).

## 2. Why it couldn't be fixed inside `rca` or `sampler`

DR-2's package-adjacency table (enforced by `internal/archtest`, `TestPackageAdjacency`) does not
grant `internal/rca` a reverse import of `internal/sampler`. `rca` therefore declares its own local
`InterestPredicate`/`InterestSink` pair rather than importing `sampler`'s. `cmd/traceiq` is on the
import path of both packages already (`system.go` imports both `internal/rca` and
`internal/sampler`), so it is the only place in the module allowed to mention both
`InterestPredicate` types in one file — which is exactly where DR-2 places the adjacency boundary
and exactly where the design doc comment said the adapter belonged.

## 3. The fix

### 3.1 Adapter — `cmd/traceiq/interest_adapter.go` (new file)

```go
type samplerInterestSink struct {
    s sampler.Sampler
}

func newSamplerInterestSink(s sampler.Sampler) rca.InterestSink {
    return &samplerInterestSink{s: s}
}

func (a *samplerInterestSink) SetInterestPredicate(ctx context.Context, tid model.TenantID, p rca.InterestPredicate) (string, error) {
    return a.s.SetInterestPredicate(ctx, tid, toSamplerInterestPredicate(p))
}

func (a *samplerInterestSink) RemoveInterestPredicate(ctx context.Context, tid model.TenantID, id string) error {
    return a.s.RemoveInterestPredicate(ctx, tid, id)
}
```

`toSamplerInterestPredicate` is a pure, one-for-one field copy from `rca.InterestPredicate` to
`sampler.InterestPredicate` (`Tenant`, `Scope` via an explicit `sampler.PredicateScope(p.Scope)`
conversion, `Source`, `Services`, `Operations`, `AttrEquals`, `ErrorSigIDs`, `PathSigs`, `TraceIDs`,
`MinDuration`, `ErrorsOnly`, `NarrowLevel`, `Hits`, `CreatedAt`, `ExpiresAt`). `ID` is intentionally
left unset on the way in — both `rca.scopePredicate`/`recurrencePredicate` and `sampler.Impl.Add`
already follow the convention that the sink/store assigns the real ID and hands it back via the
return value, which `rca.engine.Investigate` then uses for the matching `Remove` call. `Remove`
needs no translation at all: the ID is an opaque string on both sides.

`newSamplerInterestSink` takes `sampler.Sampler` (the interface), not `*sampler.Impl`, so the
adapter depends only on the package's canonical interface, matching how `system.go` already treats
`samp` everywhere else it's passed around (e.g. as an `ingest.SpanSink`).

### 3.2 Wiring point — `cmd/traceiq/system.go`

Before (line 153):

```go
rcaEng := rca.NewEngine(rca.NewMemJournal(), registry, reasoner, rca.NewMemObjectStore(), nil, clock)
```

After:

```go
rcaEng := rca.NewEngine(rca.NewMemJournal(), registry, reasoner, rca.NewMemObjectStore(), newSamplerInterestSink(samp), clock)
```

`samp` is the same `*sampler.Impl` constructed a few lines earlier in `newSystem` and already wired
into `ingest.New(...)`'s `SpanSink` list — the identical, single running sampler instance the rest of
the process uses, not a second/parallel one.

`cmd/traceiq/stubs.go`'s trailing comment (which explained the `nil` choice) was updated to point at
the adapter instead of describing a deferral.

## 4. Test proving the wiring is real

**File:** `cmd/traceiq/interest_adapter_test.go`
**Test:** `TestInterestSinkAdapter_RCAPredicateReachesRunningSampler`

This does not merely assert the adapter compiles or type-satisfies `rca.InterestSink` — it assembles
the same two concrete components `system.go` wires together in production (a real `sampler.Impl`, a
real `rca.Engine`), connects them through `newSamplerInterestSink` exactly as `system.go` now does,
and drives a real `rca.Engine.Investigate` call end to end:

1. **Setup:** a `sampler.Impl` is built with every keep-class *other than* `KeepInterest` zeroed out
   (`HealthySampleRate=0`, `FloorTracesPerMinPerService=0`, `RarePathKeepsPerMin=0`,
   `SlowMinDuration` raised to 1h, no error spans used) — so a kept trace for the incident's
   epicenter service can only be explained by an active interest predicate.
2. **Drive:** a `blockingReasoner` (same pattern as `internal/rca/engine_test.go`'s
   `TestInvestigate_AbortInterruptsRunningLoop`) holds `rca.Engine.Investigate` open mid-flight after
   its first `NextStep` call, i.e. strictly after `Investigate`'s Contextualize phase has already
   called `SetInterestPredicate` (FR-F06-12a).
3. **Assert (loop closed, Set path):** while the investigation is still open, a synthetic span for
   the incident's epicenter service (`svc-interest`) is pushed through `sampler.Impl.Consume` and
   force-finalized via `Tick`. The resulting `Decision` has `Keep == true` and
   `Reason == model.KeepInterest` — proof the predicate `rca.Engine` pushed through the adapter is
   live inside the real, running sampler's `PredicateSet`, not just recorded in a fake.
4. **Conclude:** the reasoner gate is released; `Investigate` runs to completion with a
   low-confidence conclusion, so per `engine.go` the scope predicate is removed on the terminal
   status path and no phase-B recurrence predicate is pushed.
5. **Assert (loop closed, Remove path):** the identical span shape, same service, is pushed again
   after the investigation has concluded. With every other keep path still zeroed, the resulting
   `Decision.Reason` is **not** `KeepInterest` — proof `RemoveInterestPredicate`'s delegation through
   the adapter reached the real sampler too, and the loop's teardown half is real, not just its
   setup half.

```
=== RUN   TestInterestSinkAdapter_RCAPredicateReachesRunningSampler
--- PASS: TestInterestSinkAdapter_RCAPredicateReachesRunningSampler (0.09s)
PASS
```

## 5. Before / after

| | Before | After |
|---|---|---|
| `cmd/traceiq/system.go` `rca.NewEngine(...)` `InterestSink` arg | `nil` | `newSamplerInterestSink(samp)` |
| A trace matching an open investigation's epicenter service, with no other keep reason | Dropped like any other unremarkable trace | Kept with `Reason == KeepInterest` while the investigation is open |
| Predicate cleanup on investigation conclusion | N/A (nothing was ever pushed) | Verified: same trace shape reverts to being dropped once the investigation concludes |
| D-X5 / "AI agents steer what gets sampled" | Aspirational — both halves tested in isolation, not connected | Real in the shipped binary — proven by a cross-component test, not just unit tests on each side |

## 6. Verification

```
GOTOOLCHAIN=go1.27.1 CGO_ENABLED=0 go build ./...      # clean
GOTOOLCHAIN=go1.27.1 CGO_ENABLED=0 go vet ./...         # clean
GOTOOLCHAIN=go1.27.1 CGO_ENABLED=0 go test ./... -count=1   # all packages ok (cmd/traceiq, internal/rca,
                                                             # internal/sampler, internal/archtest included)
gofmt -l .                                               # no output — fully formatted
```

`internal/archtest`'s `TestCmdImports`/`TestPackageAdjacency` also pass unchanged: the new adapter
file lives in `cmd/traceiq`, which already has the widest import scope in the module (DR-2), so no
new adjacency entry was needed.

## 7. Docs updated

- `docs/signoffs/architecture-final-signoff.md`: the D-X5 section (§1.3), the top-level Verdict
  paragraph, the consolidated gap list's item 3, and the Authorization section's "next action" note
  were each annotated with a w19 resolution note (the sign-off's own factual findings as of
  2026-09-20 were left intact as the historical record; only the current-status annotations were
  added).
- `docs/CHECKPOINT.md` and `Tracing-Bot-PRD.md`: checked, not changed. Neither document previously
  named this gap (the sign-off's own cross-check table confirmed `InterestSink`/`nil` wiring was
  "not mentioned anywhere" in either), and both describe design intent / historical wave notes rather
  than a live implementation-status ledger for this specific item, so there was nothing stale in
  either to correct.
- `cmd/traceiq/stubs.go`: its trailing comment explaining the `nil` deferral was updated to describe
  the adapter instead.

## 8. Scope discipline

No change was made to `internal/sampler` or `internal/rca` — their existing interfaces, structs, and
logic (`SetInterestPredicate`/`RemoveInterestPredicate`, `InterestSink`, `InterestPredicate`,
`scopePredicate`/`recurrencePredicate`) are used exactly as they already existed. This was a pure
wiring task: one new adapter file, a one-line change at the `rca.NewEngine` call site, a comment
update, one new test file, and the signed-off document's own gap entry.
