# W14 — Review of `internal/eval` (F11 eval harness, w13)

Reviewer pass over `internal/eval/**/*.go` (spec loader, driver, scorer, runner, report) against
F11 (`docs/architecture/features/F11-eval-harness.md`) and DR-36
(`docs/architecture/06-decision-register.md`, §36.1–§36.7). Cross-checked against
`docs/reports/w13-eval.md`'s own account of what was built, deferred, and bug-fixed.

## Verdict: **APPROVE-WITH-FIXES**

Two Major findings (spec-compliance gap + misleading-report risk) were found and **fixed in this
pass**, re-verified green. No Blockers. The harness's core claim — that the scorer genuinely
discriminates and that its gates genuinely gate — holds up under adversarial scrutiny.

## Findings

| # | Severity | Area | Finding | Status |
|---|---|---|---|---|
| 1 | **Major** | Spec compliance (FR-F11-11 / DR-36 §36.6) | `checkGates` implemented the Top-1-accuracy floor and mean-time-to-RCA ceiling but **not** the cost gate DR-36 §36.6 lists in the same table ("Cost per investigation \| median ≤ $0.08, max ≤ $0.50"). `Report` had no `MedianCostMicroUSD`/`MaxCostMicroUSD` fields at all, so a reasoner (esp. an `llm` one, once wired) that blew the published cost budget would still report `GatesPassed=true`. Not listed in w13-eval.md's "Scope boundaries / deferred" section — an unflagged gap, not a conscious deferral. | **FIXED** |
| 2 | **Major** | Report integrity / the empty-evidence risk | `scoreEvidence` returns vacuous `1.0/1.0` when `Expect.ExpectedEvidence` is empty (a documented, correctly-tested convention — see #3 below). But because `rca.Engine` never populates `Step.EvidenceIDs`, **every** real end-to-end fixture sets `expected_evidence: []`, so `Report.MeanEvidencePrecision`/`MeanEvidenceRecall` are *always* `1.0/1.0` in practice with zero visibility into whether that's "verified perfect" or "never measured." A reader of `report.md`/`report.json` could not tell the difference — the exact "empty-expected/empty-actual trivially matching masks as a pass" risk named in the task. Root cause (`rca.Engine` not populating evidence) is out of `internal/eval`'s scope per w13, but the report-level opacity was in scope and fixable. | **FIXED** |
| 3 | Informational | Scorer discrimination | Verified genuinely non-trivial: `TestRunner_S03_DoesNotMatch` drives the **real** `rules.Reasoner` to a confirmed `CatResourceExhaustion` hypothesis, then asserts the scorer reports `Top1=false, Top3=false` against a deliberately mismatched `Expect.RootCauseCategory=network` — a true adversarial negative control, not a hand-wavy one (`Category`+`Component` both required, no fuzzy matching). `TestScoreRootCause`'s table (4th-place-outside-top3, category-only match, no hypotheses) and `TestScoreEvidence`'s "no evidence surfaced against non-empty expectation scores 0/0" / "dangling EvidenceID ignored, not a panic" cases independently confirm the scorer fails when it should fail, not just passes when it should pass. No further adversarial test needed — the existing suite already proves this. |
| 4 | Informational | Regression gate | `TestRunner_RegressionGate` is a genuine end-to-end check: writes a real `report.json` baseline, runs a scenario through the full `Runner.Run` (not just `checkGates` in isolation), and asserts both a non-nil `error` (`errors.Is(err, ErrRegressionTop1Accuracy \|\| ErrBelowAbsoluteFloor)`) and `Report.GatesPassed == false`. Boundary arithmetic is table-driven with an `epsilon` correctly absorbing float64 noise. Confirmed by direct execution: the gate genuinely fails the run, it doesn't silently report success. |
| 5 | Minor / assessed, not fixed | Determinism vs. `eval.VirtualClock` gap | `TimeToRCAMillis` uses real wall-clock `EndedAt.Sub(StartedAt)` (documented in `runner.go` as this wave's simplification). Assessed for CI-load flakiness risk: `investigate()`'s only I/O is one `os.ReadFile` *before* timing starts; the timed region (`rca.Engine.Investigate` against `fixtureRegistry`, which returns canned bytes with no goroutines/network/disk) is pure in-memory CPU work, sub-millisecond even under load. Against a 180s absolute ceiling and a 25% regression ratio, the practical risk of this flipping a gate under CI load is negligible today. It does NOT satisfy FR-F11-5's actual definition (fault-observable-in-store → final report) — that's the already-documented, correctly-scoped gap (real `VirtualClock`/`ingest.Receiver` wiring, a much larger wave). FR-F11-12 explicitly excludes `TimeToRCAMillis` from the bit-identical determinism requirement, so no compliance violation here — just a metric that doesn't yet measure what its name promises. Not fixed: implementing `eval.VirtualClock` correctly requires the `ingest.Receiver`/barrier wiring that doesn't exist yet, which is a feature wave, not a review-scope fix. |
| 6 | Informational | YAML parsing / injection | `LoadSpec` decodes into private typed `specYAML` structs (`go.yaml.in/yaml/v3`) — no dynamic field access, no `!!` tag abuse surface reachable from typed decode. `TestLoadSpec_Malformed` covers missing-required-field, unknown-enum, bad-duration, and syntactically-invalid-YAML cases, each asserting a specific, non-leaky error string. Scenario files are repo-local per F11's design (not attacker-supplied input in the current architecture — `ModeLive`/customer-authored scenarios are explicitly out of this wave's scope), so YAML-bomb (billion-laughs/alias-expansion) risk is theoretical, not applicable today; flagging only in case `ScenarioSpec` loading is ever exposed to untrusted uploads in a later wave. |
| 7 | Informational | Correctness / test quality | No other correctness bugs found. `sort.SliceStable` in `scoreRootCause` correctly preserves catalog priority order on `PostScore` ties. `scoreEvidence`'s dangling-`EvidenceID` and duplicate-ID dedup (`seen` map) paths are both tested. `checkGates`'s `fail()`/`rep.FailedGates` accumulator correctly reports *all* failing gates rather than short-circuiting on the first (verified: `TestRunner_RegressionGate` asserts `len(rep.FailedGates) > 0` generically because either gate could fire first). |

## Fixes applied (this pass)

**Finding #1 — cost gate (FR-F11-11):**
- `internal/eval/eval.go`: added `Report.MedianCostMicroUSD` / `Report.MaxCostMicroUSD`.
- `internal/eval/report.go`: `buildReport` now computes both via a new `medianAndMax` helper; `ToMarkdown` prints both.
- `internal/eval/score.go`: `checkGates` now fails `absolute_ceiling_median_cost` / `absolute_ceiling_max_cost` (both `ErrBelowAbsoluteFloor`) at the DR-36 §36.6 thresholds (80,000 / 500,000 micro-USD), independent of accuracy or baseline, mirroring the existing floor/ceiling pattern exactly.
- Tests added: `TestCheckGates_AbsoluteFloor` gained 4 boundary sub-tests (exactly-at-$0.08 passes / one-µUSD-over fails; exactly-at-$0.50 passes / one-µUSD-over fails, including a case where a cheap median doesn't mask a blown max). `TestMedianAndMax` covers empty/single/odd/even directly. `TestBuildReport_CostAggregation` covers the wiring end-to-end through `buildReport`.

**Finding #2 — evidence-scoring transparency:**
- `internal/eval/eval.go`: added `Result.EvidenceExpected` (true iff the scenario's `Expect.ExpectedEvidence` was non-empty) and `Report.EvidenceScenariosCount`, with a doc comment explaining exactly why this matters (the vacuous-convention risk).
- `internal/eval/runner.go`: `runOne` sets `EvidenceExpected` from the spec.
- `internal/eval/report.go`: `buildReport` tallies `EvidenceScenariosCount`; `ToMarkdown` now prints an explicit note — "0 of N scenarios had a non-empty `expected_evidence` ... they do not reflect a measured evidence quality" when the count is 0, or "reflect M of N scenarios" otherwise — so a published report can never be misread as validating evidence quality it never measured.
- This is additive/transparency-only: it does not change `scoreEvidence`'s existing (correctly tested) 1.0/1.0 convention or gate on it, since neither the spec nor DR-36 defines an evidence-precision/recall CI gate — it makes the existing vacuous result impossible to mistake for a real one.
- Tests added: `TestBuildReport_EvidenceScenariosCount` (both the all-vacuous and mixed cases, asserting the correct markdown note appears/doesn't appear).

## Verification

```
go build ./internal/eval/...   # clean
go vet ./internal/eval/...     # clean
go test ./internal/eval/... -v -count=1   # 38 tests pass, 0 fail (26 original + 12 new)
go build ./...                 # clean, all packages
go vet ./...                   # clean, all packages
go test ./... -count=1         # all packages green
```

One unrelated pre-existing flake was observed and independently confirmed not caused by this
change: `traceiq/internal/topology`'s `TestEvictionAtCap` failed once (`want EdgesEvicted=1, got
2`) on a full-suite run, then passed 3/3 on immediate reruns (`go test ./internal/topology/...
-run TestEvictionAtCap -count=3`) and passed again on a second full `go test ./...`. `internal/eval`
does not import or touch `internal/topology`; not investigated further as out of this review's
scope, but flagged here rather than silently ignored.

## Spec-compliance summary vs. F11/DR-36

| Requirement | Status |
|---|---|
| FR-F11-1 (YAML spec, required fields) | Met |
| FR-F11-2 (real `ingest.Receiver` replay) | **Not met** — known, flagged deferral (w13), unchanged this pass; largest real gap vs. full spec |
| FR-F11-4 (Top1/Top3/PartialCredit over real types) | Met |
| FR-F11-5 (TimeToRCA from fault-observable to final report) | **Not met precisely** — measures engine wall-clock instead (see Finding #5); documented, low practical risk |
| FR-F11-6 (EvidencePrecision/Recall, closed enum) | Met at the unit level; vacuous end-to-end pending rca.Engine (Finding #2, now transparent) |
| FR-F11-7 (23-scenario Istio catalog) | **Not met** — 3 of 23, explicitly deferred (w13), unchanged |
| FR-F11-8 (JSON+Markdown report, no re-run) | Met |
| FR-F11-9 (regression gate, non-nil error) | Met, verified end-to-end |
| FR-F11-11 (absolute floors incl. cost) | **Was partially met (cost missing) — now fully met (Finding #1 fixed)** |
| FR-F11-12 (determinism) | Met for the 4 named fields |
| FR-F11-13 (receiver-entry test hook) | **Not met** — no receiver to hook into yet (depends on FR-F11-2), correctly flagged as deferred |

FR-F11-2/-7/-13 are pre-existing, correctly-documented scope deferrals from w13 (not silent), out
of this review's remit to fix — they require ingest-pipeline wiring that doesn't exist yet, a
separate feature wave, not a review-scope patch.
