# W13 — `internal/nl` (F10 natural-language interface)

Implements `nl.RulesInterpreter` (mandatory offline path, DR-35 §35.4) and `nl.RulesAnswerer`
against the F10 spec and the pre-existing `internal/nl` skeleton (`nl.go`). TDD: entity-extraction
and rule-classification tests were written and run to failure (undefined symbols) before each
implementation file; the answerer tests were written alongside `answerer.go` in the same pass given
the time budget (see Remaining/gaps).

## Files

- `internal/nl/entities.go` — Levenshtein, `matchService`, `extractTimeRange`, `extractTraceID`,
  `extractErrorString`.
- `internal/nl/patterns.go` — `DefaultPatterns()`, the golden pattern table (5 surface forms per
  intent, all ten kinds).
- `internal/nl/rules.go` — `RulesInterpreter`, `Interpret`, `extractToolArgs`, `mergeFollowUpArgs`.
- `internal/nl/context.go` — `AddTurn` (ring cap 5), `IsExpired` (30 min TTL).
- `internal/nl/answerer.go` — `RulesAnswerer`, `RemediateProposer`/`Authorizer` consumer-declared
  interfaces, evidence/citation rendering.
- `internal/nl/nl.go` — additive-only: `Intent.Candidates`/`Source`, `Answer.FollowUps`/
  `IntentKind`/`Clarifying` fields added; no existing field renamed or removed (api.go's use of
  `nl.Interpreter`/`nl.Answerer` untouched).
- Tests: `entities_test.go`, `rules_test.go`, `context_test.go`, `answerer_test.go`.

## Intent coverage vs spec (FR-F10-9, all ten `IntentKind` values)

All ten intents resolve via `RulesInterpreter` alone (no network), each with 4-5 representative
surface forms per intent (`TestIntentClassification_AllTenIntents`, 40 phrasings, all pass):
TraceSearch, ServiceHealth, TopologyQuestion, IncidentStatus, InvestigationAsk, MemoryLookup,
StartInvestigation, CompareWindows, ExplainTrace, Remediate.

Unrecognized/gibberish input (`""`, "good morning", nonsense tokens) returns `IntentUnknown`
without a panic (`TestIntentClassification_UnrecognizedFallsBackGracefully`). Ambiguous near-tied
matches return `IntentUnknown` + top-2 `Candidates` per FR-F10-11
(`TestIntentClassification_AmbiguousReturnsUnknownWithCandidates`).

Not yet built: an automated ≥85%-accuracy measurement against an F11 scenario corpus (AC-F10-9) —
F11's eval corpus doesn't exist in-repo yet; the 40-phrasing golden table is the offline substitute
for this wave.

## Entity extraction coverage

- **Service name** (FR-F10-2): case-insensitive, Levenshtein ≤ 2 fuzzy match against an injected
  `ServiceCatalogFunc` (stands in for `topology.Graph.Snapshot`); distance 0/1/2 match, distance 3
  correctly fails (`TestMatchService`).
- **Time range** (FR-F10-3): `last <n> <unit>`, `since <ISO8601>`, `between <t1> and <t2>`,
  `yesterday`, `today`, standalone ISO-8601 — all covered (`TestExtractTimeRange`,
  `TestExtractTimeRange_LastNMinutesWindow` asserts the exact window). **Deploy-marker time
  references** ("since the checkout-svc deploy", requiring `anomaly.DeployIndex`, FR-F10-3) are
  **not implemented** — documented as a gap below rather than silently guessed.
- **Trace ID**: literal 32-hex-char shape parsed into `model.TraceID`; too-short/non-hex/absent all
  correctly rejected (`TestExtractTraceID`).
- **Error string**: quoted literal or `error:`/`errors:`-prefixed phrase, always populated into a
  typed field (`ErrorSigID`/`Contains`), never interpolated into a query (`TestExtractErrorString`).
- Every extracted entity lands in the correct typed `rca.ToolArgs` sub-field (`Trace.Service`,
  `Metric.RED.Service`, `Topology.Service`, trace-ID via `Trace.AttrEquals["trace_id"]`) — never a
  free-form string or map (`TestEntityExtraction_PopulatesTypedToolArgs`).

## Tenant isolation test result

`TestAnswer_TenantIsolation`: PASS. `RulesAnswerer.AnswerForSubject`/`Answer` always dispatch using
the caller-supplied, server-resolved `tid` parameter (i.e. `auth.Subject.Tenant`, DR-5) and
overwrite `Args.TenantID` with it before every `rca.ToolRegistry.Dispatch` call — a crafted/stale
`Intent.Args.TenantID` naming a different tenant is provably never used for the actual dispatch, and
the returned `Answer.Evidence` is asserted to contain none of the other tenant's row bytes.
`TestAnswer_CitationsAreReal` independently confirms `Answer.Evidence[].PayloadJSON` matches the
exact bytes the (fake) registry returned — no fabricated citation text.

## FR-F10-6 (remediate) coverage

`TestAnswer_RemediateRequiresCapability` / `TestAnswer_RemediateNeverExecutesOnlyProposes`: PASS.
`answerRemediate` never touches `rca.ToolRegistry`; it checks `Authorizer.Can(...,
"remediation_action:propose", ...)` first and only then calls `RemediateProposer.Propose` — there is
no `Execute` method reachable from `nl` at all.

## Verification

```
go build ./...   -> clean
go vet ./...     -> clean
go test ./internal/nl/... -v   -> 19 top-level tests, all PASS, 0 FAIL
go test ./...    -> all packages PASS except internal/archtest's TestPackageAdjacency, which is
                     PRE-EXISTING and unrelated to this wave (flags internal/eval -> internal/rca/rules,
                     a package this task never touched).
```

## Remaining / gaps (NEEDS_CONTEXT)

1. **`nl.Answerer` interface has no `auth.Subject` parameter** (`nl.go`, matches `api.go`'s existing
   usage), but F10 FR-F10-6/FR-F10-7 require the resolved chat `Subject` to gate `IntentRemediate`
   and `IntentStartInvestigation`. Resolved by adding `RulesAnswerer.AnswerForSubject(ctx, tid, in,
   cc, subject)` as the capability-aware entry point; `Answer()` (the interface method) delegates to
   it with a zero-privilege `Subject{Tenant: tid}`, so both gated intents always safely refuse
   through the plain interface path. The wiring layer (`internal/api`, out of this task's scope)
   should call `AnswerForSubject` directly once it resolves `auth.Subject` per FR-F10-7 — otherwise
   remediation/investigation will always report "you don't have permission" even for legitimately
   authorized users.
2. **`internal/remediate` is not in `doc.go`'s allowed-import list** for `nl`, so `RulesAnswerer`
   cannot call `remediate.Guard.Propose` directly. A `RemediateProposer` interface (Propose-only, no
   Execute) is declared in `nl` per DR-2's consumer-declared-backend pattern; it must be satisfied by
   `remediate.Guard` at the `cmd/traceiq` wiring layer.
3. **`ExplainTrace`'s single-trace lookup** uses `TraceQueryArgs.AttrEquals[{Key:"trace_id"}]`
   because `TraceQueryArgs` has no native trace-ID field — this is a best-effort mapping, not
   verified against `store`'s actual indexed-attribute-key allowlist (`store.hot.indexed_attribute_keys`).
4. **Deploy-marker time phrases** ("since the checkout-svc deploy") are not resolved via
   `anomaly.DeployIndex` — such phrases currently fall through to `extractTimeRange`'s `ok=false`
   path (defaults to a trailing 30-minute window rather than the FR-F10-3-mandated `IntentUnknown`
   clarifying fallback for unmatched time grammar).
5. **`LLMInterpreter` and `FallbackDispatch`** (F10 §4.4, `nl.interpreter: auto`/`llm`) are not
   implemented — task scope named the LLM interpreter "stubbed only"; no code for it was added this
   wave beyond the `Interpreter`/`Kind()` interface already in `nl.go`.
6. **AC-F10-9's ≥85%-accuracy gate** has no automated measurement in this wave (no F11 scenario
   corpus in-repo); the 40-case golden table stands in as the offline regression suite.
