# W12 — Review: `internal/rca` + `internal/rca/rules`

## Verdict: APPROVE-WITH-FIXES

One Major finding confirmed and fixed in this pass (Abort() was disconnected from a running
investigation — see Finding 1). No Blockers. The `model.ToolResult` shape (Appendix D's flagged gap)
is sound and extensible as chosen. All three "known already-fixed" bugs from `docs/reports/w11-rca-cont.md`
verify correct, not just compiling. One further spec-compliance gap is flagged but not fixed (Finding 2
— out of this review's safe blast radius, needs an architecture-level call, not a code fix).

**Sources read:** `docs/reports/w11-rca-cont.md` (the only w11 rca report on disk; no separate
`w11-rca-rules.md` exists despite the task naming it — same absent-sibling-report pattern other w12
reviews in this repo have already noted), `docs/architecture/features/F06-rca-engine.md` (full),
`docs/architecture/06-decision-register.md` DR-17 (§17.1–17.5), DR-18 (§18.1–18.3), DR-11 (full),
Appendix D.

## Build/vet/test status

- `go build ./internal/rca/...` — clean, before and after.
- `go vet ./internal/rca/...` — clean, before and after.
- `gofmt -l internal/rca/` — clean after `gofmt -w` on the one file touched (`engine.go`).
- `go build ./...` — clean.
- `go test ./internal/rca/... -v` — **25/25 top-level PASS** (23 pre-existing + 2 new: `TestInvestigate_AbortInterruptsRunningLoop`,
  `TestInvestigate_AbortOnAlreadyTerminalInvestigationIsBestEffort`), 45+ including subtests.
- `go test ./...` (full repo) — **all packages PASS**, including `internal/archtest` (the `rca/rules`
  DR-2 adjacency entry from w11 still holds).
- `-race` unavailable in this environment (`CGO_ENABLED=0`, no C compiler on PATH).

## Findings

| # | Severity | File:line | Finding | Status |
|---|---|---|---|---|
| 1 | **Major (bug)** | `internal/rca/engine.go` `Abort` | `Abort` only mutated `e.invs`/the journal directly and never touched a concurrently-running `Investigate` call's own state. Two consequences: (a) `Investigate`'s own finalization code (`storeInv`/`journal.SetStatus` at the bottom of the loop) runs *after* `Abort` and unconditionally overwrites whatever `Abort` wrote with the loop's own eventual, non-aborted outcome — an abort of a still-running investigation was **silently discarded** the moment that investigation finished on its own; (b) `Abort` never called `RemoveInterestPredicate`, so the FR-F06-12a scope predicate for an aborted investigation would linger until its 30m TTL instead of being torn down immediately, unlike every other terminal status. | **FIXED** — `Investigate` now derives a cancellable `ctx` (`context.WithCancel`) and registers its cancel func in `e.cancels[inv.ID]` for the loop's duration; the loop's top-of-iteration checks gained `if ctx.Err() != nil { TerminationReason = TermAborted; break }`, so an abort is observed at the next iteration boundary and falls through the *same* single finalization path every other termination reason already uses (report, `journal.SetStatus`, interest-predicate removal via the existing "removed on any terminal status" code). `Abort` now looks up the running cancel func and signals it instead of racing it; it only falls back to writing status directly when the investigation is not currently running (already terminal). Finalization itself now runs on `context.WithoutCancel(ctx)` so a context-respecting `Journal`/`InterestSink` (unlike today's in-memory ones) doesn't fail its writes precisely when `TermAborted` needs them to succeed. Verified by two new tests: `TestInvestigate_AbortInterruptsRunningLoop` (a blocked-in-flight reasoner call, `Abort` called while it's provably still running, asserts `TermAborted`/`InvestigationAborted`/the recorded reason/a short step count/predicate removal) and `TestInvestigate_AbortOnAlreadyTerminalInvestigationIsBestEffort` (the fallback branch, matching pre-fix behavior for that case). |
| 2 | Major (spec-compliance gap, **not fixed** — architecture-level) | `internal/rca/rules/rules.go` `errorSigAfterDeploy`/`connectionPoolExhaustion`.`BuildCall` | Two of the four implemented rules dispatch a `metric_query` with a `TemplateID` (`"error_signature_deploy_correlation"`, `"pool_wait_and_exhaustion_logs"`) that is **not** in DR-16 §16.4's closed 10-template PromQL allowlist, and neither rule makes the second tool call its own catalog-table entry requires (`error-signature-new-after-deploy`: `memory_query`+`metric_query`, never calls the shared `anomaly.DeployIndex`; `connection-pool-exhaustion`: `metric_query`+`log_query`, never issues a `log_query`). Both instead fold two tools' worth of evidence into one synthetic single-call row shape. This works today only because `validateToolArgs` (engine.go's minimal stand-in for the real `SchemaValidator`) doesn't check `TemplateID` against the closed allowlist — a documented, deliberate deferral. Once `SchemaValidator.ValidateToolArgs`'s real DR-16 §16.3 semantic validation is wired (a later wave), both rules' calls will fail as `invalid_args` on every attempt, since their templates don't exist. Fixing this requires either registering two new templates in DR-16 §16.4 (an architecture-register change) or redesigning both rules around real templates plus the two-tool-call shape the catalog table specifies — either is a spec-level decision, not something a code review should decide unilaterally. `downstream-latency-propagation` has the same one-tool-call simplification (table says `trace_query`+`topology_query`, code only issues `trace_query`) but at least doesn't invent a template ID, since `TraceQueryArgs` has no template concept. | **Not fixed** — flagged for the architecture board / the wave that wires the real `SchemaValidator`. Recorded here rather than silently left implicit, per this task's own "track gaps, don't invent scope" instruction (Appendix D's own precedent). |

## Item-by-item findings

### 1. Spec compliance vs FR/AC-F06-* (rules-reasoner-relevant subset)

- **FR-F06-1/2** (≥1 persisted Step per investigation, fsync-before-advance semantics): met. `Investigate` always appends a Contextualize step before the loop, and `MemJournal.AppendStep` returns only after the step is durably visible to `Steps()` — the documented stand-in for "fsynced before the loop may advance" in a journal with no disk. Confirmed by `TestFullLoop_StepPersistsInputsHashesAndReasonerOutput`.
- **FR-F06-3** (rules reasoner: zero network calls, stamps `Category`/`Component` on every fired rule): met — every `NextStep` proposal and fold-in carries `Category`/`Component` (`TestReasoner_StampsComponent`, all four `TestFullLoop_*Fires` tests).
- **FR-F06-8** (termination on first-of budget dimension): the rules-relevant subset (`WallClock`, `MaxSteps`, `MaxToolCalls`, `MaxCostMicroUSD`) is checked at the top of every loop iteration and each has a dedicated boundary test (`TestInvestigate_MaxStepsBudgetExhausted`, `TestInvestigate_WallClockBudgetExhausted`). `MaxTokensIn`/`MaxCachedTokensIn` gating is correctly absent (llm-only per the pseudocode) and correctly untested here — not a gap for this wave.
- **FR-F06-9** (rule catalog, 4 of 6): see Finding 2 above — two rules' evidence-gathering shape deviates from the catalog table.
- **FR-F06-12a/12b** (two-phase interest predicate): both phases implemented and correctly ordered (scope pushed before the first Hypothesize step; recurrence pushed only on `Concluded && Confidence >= threshold`, scoped by `ErrorSigIDs`/`PathSigs` never `Services`). The recurrence predicate's `ErrorSigIDs`/`PathSigs` are always empty today (`recurrenceSignatures` is a documented stub — `model.Incident` carries no `ErrorSigID` field yet), so the predicate is honestly-scoped-to-nothing rather than wrongly service-scoped; a real implementation needs that field threaded through before phase B is useful. Removal-on-terminal-status previously missed the `Abort` path entirely — Finding 1.
- **FR-F06-21** (invalid tool args recorded, never dispatched, count against `max_tool_calls`): met, verified by `TestInvestigate_InvalidToolArgsRecordedAndCountsAgainstBudget` — asserts the fake registry's `Dispatch` is never called, the step carries `ToolArgsJSON`/`ToolArgsHash`, and `Spend.ToolCalls` increments.

### 2. `model.ToolResult` field shape (Appendix D's flagged gap)

The chosen shape —

```go
type ToolResult struct {
    Rows      []byte // canonical JSON projection, capped by rca.budget.max_evidence_bytes
    Clamped   bool
    Truncated bool
    FromCache bool
    Tool       ToolName
    ObservedAt time.Time
}
```

is **sound and appropriately extensible**. Reasoning:

- `Rows []byte` (opaque canonical JSON) is generic across all five closed tools without needing a
  discriminated union of five row-shape types — exactly the choice the type's own doc comment argues
  for, and it holds up: `internal/rca/rules` already round-trips through it cleanly via a generic
  `decodeRows[T]`, and any future backend can produce it without a `model` change.
- Critically, `Step.ToolResultHash` is computed over `result.Rows` alone (`sha256Hex(resultBytes)` in
  `engine.go`, `resultBytes := result.Rows`), **not** over the whole `ToolResult` struct. This means
  future additive fields on `ToolResult` (this wave already added two: `Tool`, `ObservedAt`) can never
  retroactively change a previously-recorded `ToolResultHash`, which is exactly the property DR-18's
  replay-determinism contract needs going forward — a real extensibility win, whether or not it was
  deliberate.
- `Tool`/`ObservedAt` are well-justified, minimal additions (documented in the type's own comment) that
  close a real gap this wave hit (stamping `Step.Tool` from a single `ToolResult` without threading a
  second parameter through every `Tool.Invoke` call site) without touching `Rows`'s shape.
- One real ambiguity worth flagging for whoever wires the LLM digest formatter (FR-F06-4/§4.4's
  `digest()` pseudocode): it's not documented anywhere in `model` or the register whether `Rows` is
  expected to already be Project-allowlist-filtered by the backend (this wave's `tools.go` doc comment
  implies backends do their own projection/clamping) or whether the digest formatter is expected to do
  that filtering itself from a fuller row set (§4.4's pseudocode reads `rawResult.rows` and applies
  `Project` filtering itself). Not a blocker — resolving it needs only an additive field or a comment,
  never a breaking redefinition — but worth settling explicitly before that wave starts, so projection
  isn't silently applied twice or skipped entirely.

No changes made to `internal/model/rca.go`; the type is fit for purpose as-is.

### 3. Known already-fixed bugs — re-verified correct, not just compiling

- **`decodeToolArgs` (the build break).** Correct: `json.Unmarshal` inverse of `canonicalJSON`
  (currently plain `json.Marshal`), errors on empty input, exercised by a real round-trip test
  (`TestDecodeToolArgs_RoundTrip`) that asserts field-level equality after the trip, not just "no
  error."
- **Interest-predicate `Remove` using the wrong ID (DR-11).** Re-derived DR-11's exact two-phase
  contract from the register (not just re-reading the w11 report's claim) and confirmed the fix against
  it line-by-line: `scopePredID` captures the sink's `Set` return value; `RemoveInterestPredicate` is
  called with that ID (falling back to the `Source`-shaped string only if `Set` never succeeded);
  `TestInvestigate_InterestPredicateLifecycle` uses a fake sink that deliberately assigns IDs unrelated
  to `Source` and asserts `Remove` receives exactly the assigned ID. Correct. (The removal call site
  itself had a real second gap — the `Abort` path didn't reach it at all — see Finding 1, now fixed.)
- **`archtest` DR-2 adjacency gap for `rca/rules`.** `rules.go` imports exactly `traceiq/internal/model`
  and `traceiq/internal/rca`; the adjacency table entry added is `"rca/rules": {"model", "rca"}` —
  matches the actual import set exactly, not a broadened allowlist that happens to make the test pass.
  Principled fix, confirmed by reading both the import list and the table entry side by side.

### 4. Rule catalog correctness (detection logic + false-positive coverage)

| Rule | Matches spec's detection logic | Real false-positive test |
|---|---|---|
| `error-signature-new-after-deploy` | Confirm predicate itself matches (`FirstSeen` within a 30m post-deploy window) — but see Finding 2: evidence-gathering path (template ID, missing `memory_query`/`DeployIndex` call) deviates. | Yes — separate negative cases for "before deploy" and "outside the post-deploy window," a genuinely adjacent-but-different boundary, not just "no data." |
| `downstream-latency-propagation` | Confirm predicate matches (child-span ratio ≥ 80% of total). One-tool-call simplification (no `topology_query`), disclosed in the `Rule` interface's doc comment. | Yes, but weaker — "self-time dominates" (ratio 0.1) is far from the 0.8 boundary; no test at e.g. 0.79 vs 0.80. Div-by-zero guard is tested. |
| `n-plus-one-span-pattern` | Matches exactly (≥N=10 identical sibling spans). | Yes, and the best of the four — explicit 9-vs-10 boundary test plus a below-threshold case. |
| `connection-pool-exhaustion` | Confirm predicate matches (trend-up AND log matches, both required). Evidence-gathering path deviates (Finding 2). | Yes — two independent negative cases isolate each half of the AND (trend without logs, logs without trend), which is exactly what a real false-positive test for a conjunctive predicate should do. |

The `Conclude`-layer "no false positives" guarantee is also independently tested and correct:
`TestReasoner_ConcludeInconclusiveWithoutASupportedHypothesis` confirms an all-refuted/testing
hypothesis set never manufactures a `RootCause`, and the full-loop integration test
`TestFullLoop_NoMatchingEvidenceIsInconclusiveNotAFalsePositive` confirms the same at the engine level
with all four real rules run against refuting evidence.

### 5. Budget enforcement (DR-17) — no unbounded path found

Walked every loop-exit path in `Investigate` looking for a way to run unbounded:

- `WallClock`, `MaxSteps`, `MaxToolCalls`, `MaxCostMicroUSD` are all checked at the top of every
  iteration (now joined by the new `ctx.Err()` abort check, Finding 1).
- The rules reasoner's own progress guarantee (catalog exhaustion via `triedRuleIDs`/`ErrNoMoreRules`)
  independently bounds the loop to at most 4 rule-proposal iterations plus their fold-in iterations,
  regardless of the budget — `TestReasoner_NoMoreRules` covers the exhausted-catalog path directly.
- Every loop branch (`Done`, pure-reasoning, invalid-args, dispatch) calls the per-step `cancel()`
  before its `break`/`continue`; no context leak on any path.
- `MaxToolCalls` can, under `DefaultBudget()`, never be the binding dimension because every branch that
  charges `Spend.ToolCalls` also charges `Spend.Steps` and `MaxSteps(24) < MaxToolCalls(40)` — this is
  the w11 report's own documented observation (`TestInvestigate_MaxToolCallsUnreachableUnderDefaults`),
  re-verified true by inspection: it's a config-value question (which dimension should bind first, per
  DR-17 §17.5), not a bug.
- One real (pre-existing, not newly introduced) gap: `ToolCost.MaxWallClock` (`defaultToolCost`, set to
  the DR-17 §17.2 "per-tool timeout: 15s" value) is declared but **never enforced** anywhere — no
  `Tool.Invoke` implementation applies it, and the engine only wraps the whole step (reasoner call +
  dispatch) in the coarser 45s `MaxStepWallClock`. A single slow tool call could consume the full 45s
  step budget instead of being capped at 15s. Not fixed here: enforcing it would mean wrapping each
  `Tool.Invoke` call in its own `context.WithTimeout(stepCtx, t.Cost().MaxWallClock)`, which touches
  `tools.go`'s five `Invoke` methods — a reasonable, contained fix, but out of this pass's declared
  scope (not one of the three "known already-fixed" bugs, not the ToolResult gap, and not something
  currently causing an observed test failure or an unbounded-loop risk — it only widens one already-
  bounded ceiling). Flagged for a follow-up.

No path found where the loop runs unbounded.

### 6. Replay contract (DR-18)

- **Step persistence** carries real tool-call inputs (`ToolArgsJSON`/`ToolArgsHash`), a real result hash
  and ref (`ToolResultHash`/`ToolResultRef`, backed by `MemObjectStore`), and real reasoner output
  (`ReasonerOutputRef`/`ReasonerOutputHash`) for every tool-dispatching step — verified field-by-field by
  `TestFullLoop_StepPersistsInputsHashesAndReasonerOutput` against the real `rules.Reasoner`, not a fake.
- **`ReplayRecorded` determinism** is verified by an actual test, not claimed: `TestReplayRecorded_Deterministic`
  runs `Replay` twice against the same investigation and asserts step-by-step equality of
  `ToolArgsJSON`/`ToolArgsHash`/`ToolResultHash`/`ToolResultRef`/`Tool`/`Verdict`/`Phase`/`Seq` between
  the two replays *and* against the original recording, plus `Spend == model.Spend{}` and `ReplayOf`
  correctness. This is a real, load-bearing assertion — genuinely exercises `sha256` verification against
  `MemObjectStore`-backed bodies, not a trivial equality on empty structs.
- **`ErrEvidenceCorrupt`** is verified end-to-end: `TestReplayRecorded_CorruptEvidenceAborts` corrupts a
  real stored evidence blob via the object store and asserts `Replay` returns exactly `ErrEvidenceCorrupt`.
- **Known, disclosed gap (pre-existing, not newly found):** `ReplayLiveDiff` reports drift in aggregate
  (`Investigation.ReportMarkdown`) rather than the per-step `Drift` annotation FR-F06-20/AC-F06-16
  require, because `model.Step` has no `Drift` field and this wave's scope lock permitted only additive
  `ToolResult` fields, not new `Step` fields. Honestly documented in `engine.go`'s own doc comment;
  correctly out of this review's fix scope (it's a `model.Step` design question, same class of decision
  as Finding 2, not a code bug).

### 7. Test quality

Real assertions throughout, not weak stubs. Highlights: the false-positive boundary tests noted in §4;
the fake `InterestSink` deliberately assigning IDs unrelated to `Source` (the exact shape needed to
catch the DR-11 bug, not a sink that would pass either way); `TestInvestigate_BudgetExhaustionDoesNotPanicOnPureSteps`
using `recover()` to assert no panic rather than just checking the return value; the new `Abort` test
using a channel-gated fake reasoner specifically so the assertion is "the engine's own loop-top check
stopped it," not "the reasoner noticed cancellation" (the real `rules.Reasoner` ignores `ctx` entirely,
so testing against a ctx-aware fake would have proven the wrong thing). The one non-test:
`TestInvestigate_MaxToolCallsUnreachableUnderDefaults` is a documented invariant-guard (`t.Skip` unless
the invariant it's guarding breaks) rather than a real test — accurately named and commented as such, not
disguised as coverage it doesn't provide.

### 8. Simplification / dead-code / efficiency

Nothing rising to a flag beyond what's captured in Findings 1–2 and the DR-17 §17.2 per-tool-timeout gap
in §5 above. The package is small, and its many `TODO`/doc-comment disclosures of deferred scope
(dedupe, daily-cap downgrade, `AppendEvidence`, per-step `Drift`) are accurate against what the code
actually does — re-verified by reading the code, not just trusting the comments.

## Fix summary

`internal/rca/engine.go`: `Abort` now interrupts a running `Investigate` loop via context cancellation
instead of racing it, and the interest-predicate removal / journal / report finalization that already
existed for every other terminal status now also covers the aborted case, running on a
`context.WithoutCancel` derivative so it isn't itself defeated by the cancellation that triggered it.
`internal/rca/engine_test.go`: two new tests (`TestInvestigate_AbortInterruptsRunningLoop`,
`TestInvestigate_AbortOnAlreadyTerminalInvestigationIsBestEffort`) covering both branches. `go build`,
`go vet`, and `go test ./...` (full repo) all green after the fix.
