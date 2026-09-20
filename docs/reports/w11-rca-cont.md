# w11 — RCA engine continuation (rules reasoner only)

Continuation of an interrupted wave. Scope: fix the build break, assess what of `internal/rca` was
already implemented, and add TDD coverage for the **rules reasoner** path only. LLM reasoner
(`internal/llm` wiring) is explicitly out of scope for this wave — `rca.LLMReasoner` exists as a
stub (holds an `llm.Client`) but is not exercised, constructed, or tested here.

## Build break fixed

`internal/rca/engine.go:397` called an undefined `decodeToolArgs`. Implemented it in `engine.go` as
`canonicalJSON`'s inverse (`encoding/json.Unmarshal` into `*ToolArgs`, erroring on an empty string),
used by `replayLiveDiff` to re-dispatch a recorded step's args. `go build ./...` is clean.

## Rules implemented: 4 of 6

`internal/rca/rules/rules.go`'s own doc comment already stated the scope honestly: 4 of the 6 F06
§4.4 catalog rules are implemented (`error-signature-new-after-deploy`,
`downstream-latency-propagation`, `n-plus-one-span-pattern`, `connection-pool-exhaustion`);
`retry-storm` and `timeout-mismatch` are deferred, cut for time. This meets the wave's "at least 4
concrete rules" floor, so no new rules were added — effort went into test coverage and the two bugs
below instead.

## Bugs found and fixed (TDD)

1. **`decodeToolArgs` missing** (the build break) — implemented, see above.
2. **Interest-predicate removal used the wrong identifier (DR-11).** `engine.Investigate` called
   `SetInterestPredicate`, discarded the sink-assigned ID it returned, and later called
   `RemoveInterestPredicate(ctx, tid, "rca:"+inv.ID)` — the predicate's `Source` string, not its
   `ID`. `InterestSink.RemoveInterestPredicate` takes an `id`, and `InterestPredicate.ID` is left
   unset by `scopePredicate`, i.e. it's meant to be assigned by the sink and threaded back for
   removal. This only worked if a sink's ID scheme happened to equal `"rca:"+invID`. Fixed by
   capturing the returned ID (`scopePredID`) and using it for removal, falling back to the
   reconstructed string only if `Set` was never called or failed. Caught by
   `TestInvestigate_InterestPredicateLifecycle`, which uses a fake sink that assigns IDs
   deliberately unrelated to `Source`.
3. **`internal/archtest`'s DR-2 adjacency table had no entry for `rca/rules`.** The prior wave added
   the `rca/rules` subpackage but never registered it, so `go test ./...` failed on
   `TestPackageAdjacency` repo-wide (unrelated to rules logic itself — a pure allowlist gap). Added
   `"rca/rules": {"model", "rca"}`, mirroring the existing `store/sqlite`-under-`store` pattern,
   since `rules.go` imports only those two. This is the one edit outside `internal/rca/**` made this
   wave; it was necessary to get the full suite green and is a one-line manifest addition, not a
   structural change.

## Judgment call documented, not fixed

`TestInvestigate_MaxToolCallsUnreachableUnderDefaults` records an observation: under
`DefaultBudget()` (`MaxSteps=24 < MaxToolCalls=40`), every loop branch that charges
`Spend.ToolCalls` also charges `Spend.Steps`, so `Spend.Steps >= Spend.ToolCalls` always holds and
`MaxToolCalls` can never be the binding termination dimension. That's a DR-17 §17.5 config-value
question (which dimension should bind first), not an `internal/rca` bug — left as-is, flagged for
whoever owns budget tuning.

## Test coverage added

**`internal/rca/rules/rules_test.go`** (per F06 §7's "one fixture-driven test per rule, positive +
negative"): all 4 rules' `Confirm`/`BuildCall`, including edge cases (empty rows, div-by-zero guard,
confidence clamping, default threshold), plus `Reasoner.NextStep`/`Conclude` unit tests (component
stamping, catalog-exhausted `ErrNoMoreRules`, fold-in-then-conclude, and the "no false positive"
guarantee that `Conclude` never manufactures a root cause without a Supported hypothesis).

**`internal/rca/engine_test.go`** (package `rca`, fakes only — no real `Reasoner`): budget
exhaustion (`MaxSteps`, `WallClock`, both via a fully mocked `model.Clock` so no real sleeping),
graceful-no-panic under budget exhaustion, the interest-predicate lifecycle fix above (plus its
negative case — no phase-B predicate on low confidence), invalid-args steps recorded and charged
against `max_tool_calls` without reaching `Dispatch` (AC-F06-19), and `decodeToolArgs` round-trip.

**`internal/rca/engine_rules_test.go`** (package `rca_test`, external — required because
`rca/rules` imports `rca`, so an internal `rca` test file importing `rules` would be a real import
cycle): full-loop integration with the real `rules.Reasoner` — two rules each fired individually
against crafted evidence with earlier catalog rules refuted first, a whole-catalog-exhausted run
producing `Inconclusive`/empty `RootCause` (the "no false positives" integration check), step
persistence asserting every DR-18 field (`ToolArgsJSON`/`Hash`, `ToolResultHash`/`Ref`,
`ReasonerOutputRef`/`Hash`) is populated for tool-dispatching steps, `ReplayRecorded` determinism
(two replays byte-identical modulo ID/timestamps, zero spend), and `ReplayLiveDiff`/`ReplayRecorded`
evidence-corruption aborting with `ErrEvidenceCorrupt`.

**Result:** 23 top-level tests / 41 including subtests, all passing, 0 failing, in
`internal/rca` + `internal/rca/rules`.

## Deferred / explicitly out of scope

- LLM reasoner (`internal/llm` wiring, `rca.LLMReasoner` construction/use) — a later wave, per the
  brief and per DR-2's `rca -> llm` edge being declared but unexercised.
- `retry-storm` and `timeout-mismatch` rules — not implemented (4/6 met the floor).
- `rca.Dispatcher` (fingerprint dedupe before dispatch, FR-F06-22), `memory.Store.Record` at
  conclusion, DR-17 §17.4 daily/global cost-cap downgrade, DR-34 §34.4 reasoner-swap machinery — all
  pre-existing documented gaps in `engine.go`'s own doc comment, not touched this wave (LLM-path or
  cross-package wiring, out of scope).
- `Journal.AppendEvidence` is never called by `engine.Investigate` — the digest-formatter /
  `Evidence` write path F06 §7 describes isn't implemented this wave; not required by this wave's
  (a)-(f) checklist, left as a known gap rather than silently patched.
- Exact `MaxStepWallClock` (45s) boundary test — `DefaultBudget()` isn't injectable into
  `engine.Investigate` this wave (hardcoded), and the per-step timeout is wired through a real
  `context.WithTimeout`, not the injected `Clock`; a true boundary test would need either a 45s
  real sleep or a budget-injection seam that doesn't exist yet. Not added.
- RFC 8785 canonical JSON (`canonicalJSON` is plain `encoding/json.Marshal`) and `Step.Tool`
  TenantID-elision from `ToolArgsJSON` — both pre-existing documented simplifications, unchanged.

## Final status

`go build ./...`, `go vet ./...`, and `go test ./...` are all green. `go test ./internal/rca/... -v`:
41/41 subtests passing.
