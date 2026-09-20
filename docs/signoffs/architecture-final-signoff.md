# TraceIQ — Architecture Final Sign-off (Implementation vs. Approved Design)

**Project:** TraceIQ — distributed-tracing RCA bot (Go 1.27, `GOTOOLCHAIN=go1.27.1`, `CGO_ENABLED=0`)
**Date:** 2026-09-20
**Issued by:** Chief Architect — final sign-off
**Predecessor document:** `docs/signoffs/architecture-signoff.md` (2026-09-16, architecture-docs-only,
predates all implementation)
**Primary evidence source:** `docs/reports/w17-final-review.md` (whole-codebase review, read in full),
`docs/CHECKPOINT.md`, `docs/reports/w18-live-test.md`, `Testing_defect.md`, live code and test runs
against the repo on disk.

---

## Verdict

> ## **APPROVED WITH DEVIATIONS**

**UPDATE (w19 follow-up, post-sign-off):** the deviation named below — the unwired DR-11/D-X5
`InterestSink` — has been fixed. `cmd/traceiq/interest_adapter.go` now implements
`rca.InterestSink` by adapting `sampler.Sampler`, and `cmd/traceiq/system.go`'s `rca.NewEngine` call
passes `newSamplerInterestSink(samp)` in place of the `nil` this document flagged. See
`docs/reports/w19-fix-interestsink-wiring.md` for the adapter design and a test
(`cmd/traceiq/interest_adapter_test.go`'s `TestInterestSinkAdapter_RCAPredicateReachesRunningSampler`)
proving the loop is closed end-to-end in the real, running components: a predicate pushed by a live
`rca.Engine.Investigate` call reaches a real `sampler.Impl` and changes a subsequent keep decision,
and removal on conclusion is likewise verified. The rest of this document is left as originally
issued, as the historical record of what this sign-off found on 2026-09-20.

The built system matches the approved architecture on package boundaries, tenancy discipline, and
the mechanical shape of every spot-checked decision. One deviation is material enough to name
explicitly rather than fold into routine "deferred" bookkeeping: **the DR-11/D-X5 agent-to-sampler
feedback loop — TraceIQ's PRD-level differentiating wedge — is implemented and unit-tested on both
sides but is not connected in the running binary** (`cmd/traceiq/system.go:153` passes `nil` for
`rca.Engine`'s `InterestSink`). This specific gap is not recorded in any of the three docs this
sign-off was asked to cross-check for gap consistency (`docs/CHECKPOINT.md`,
`docs/reports/w17-final-review.md`, `Testing_defect.md`/`docs/reports/w18-live-test.md`) — it is
discoverable only from source comments. Every other known-deferred item (API not wired, RCA tool
backends stubbed) is consistently and accurately documented across all three.
**(RESOLVED w19 — see update above.)**

---

## 1. Spot-check results

### 1.1 DR-2 package adjacency — **PASS**

Ran directly:

```
GOTOOLCHAIN=go1.27.1 CGO_ENABLED=0 go test ./internal/archtest/... -v
=== RUN   TestPackageAdjacency
=== RUN   TestPackageAdjacency/ScopeCoverage
    checked 26 internal package(s), 131 non-test .go file(s)
--- PASS: TestPackageAdjacency (0.62s)
    --- PASS: TestPackageAdjacency/ScopeCoverage (0.05s)
=== RUN   TestWebPackageEdge
--- PASS: TestWebPackageEdge (0.54s)
=== RUN   TestCmdImports
--- PASS: TestCmdImports (0.01s)
PASS
```

Matches w17's check 1 finding (M4's `traceiq/web`-edge gap was already fixed and is now covered by
`TestWebPackageEdge`). The import-boundary contract from DR-2 still holds against the live tree, not
just against the w17 report's prior run.

### 1.2 DR-5 tenant-first-param convention — **PASS**

Grepped exported tenant-scoped methods across `topology`, `anomaly`, `correlate`, `store`, `api`
test adapters. Sample of confirmed `(ctx context.Context, tid model.TenantID, ...)` ordering, zero
exceptions found:

- `internal/topology/livegraph.go`: `Consume`, `Has`, `Neighbors`, `Distance`, `Edges`, `EdgeOps`,
  `Snapshot`, `Export`, `WarmStart` — all `(ctx, tid, ...)`.
- `internal/anomaly/{detectors,grouper,baseline,deploy}.go`: `Evaluate`, `Add`, `ActiveIncidents`,
  `Suppress`, `Observe`, `Load`, `Record`, `Near`, `PrePostSplit`, `ListWindow` — all `(ctx, tid, ...)`.
- `internal/correlate/{correlator,dev_adapter}.go`: `LogsForTrace`, `QueryByServiceWindow`,
  `ExemplarsFor`, `Range` — all `(ctx, tid, ...)`.
- `internal/api/topology_adapter.go`: `Has`, `Neighbors` — `(ctx, tid, ...)`.

Consistent with w17 check 2's own finding: the one exported exception,
`ingest.Server.Ingest(ctx, protocol, raw, subjectTenant, useDevDefault)`, is correct by design
(`subjectTenant` is an unresolved input to resolution, not a resolved scope). No new exception found.

### 1.3 D-X5 — agent-to-sampler feedback loop — **implemented, unit-tested, NOT wired in the running binary at time of sign-off; WIRED as of w19 (see update at top of document)**

This is the PRD's core differentiating wedge (`00-feature-catalog.md`: D-X5 = "AI agents bounded by
telemetry quality," addressed by F02 via the "agent feedback channel"; `06-decision-register.md`
DR-11 = "Interest predicate (D-X5)").

**What exists and is correct, independently, on each side:**

- `internal/sampler/sampler.go` + `impl.go`: `Sampler.SetInterestPredicate` /
  `RemoveInterestPredicate` fully implemented, exercised by
  `internal/sampler/w10_review_test.go` (push, narrow, remove).
- `internal/rca/interest.go`: `scopePredicate` (phase A, FR-F06-12a, pushed at Contextualize) and
  `recurrencePredicate` (phase B, FR-F06-12b, pushed only on `Concluded` +
  `Confidence >= threshold`) are both implemented field-for-field against DR-11's struct. rca
  declares its own local `InterestPredicate`/`InterestSink` types rather than importing
  `sampler`, by design — DR-2's adjacency table does not grant rca a reverse import of sampler, so
  the doc comment in `interest.go` states explicitly: *"cmd/traceiq (out of this wave's scope)
  adapts between the two one-for-one when it wires sampler.Sampler into an rca.InterestSink."*
- `internal/rca/engine.go`: `interest InterestSink` is a constructor parameter, called at the right
  investigation lifecycle points; `engine_test.go`'s `fakeInterestSink` confirms both phase A and
  phase B push/remove fire correctly in isolation.

**What is missing:** the adapter the `interest.go` doc comment says cmd/traceiq owes. It was never
written.

```go
// cmd/traceiq/system.go:153
rcaEng := rca.NewEngine(rca.NewMemJournal(), registry, reasoner, rca.NewMemObjectStore(), nil, clock)
```

`nil` is a documented-valid value for `InterestSink` (`interest.go`: *"A nil InterestSink is valid —
Engine.Investigate treats predicate push/remove as best-effort and skips it"*), and
`cmd/traceiq/stubs.go:75-79` explains the choice: *"the DR-11 phase-A/B predicate push back into
sampler.Impl is deferred ... by passing nil for it at construction."* So this is not a bug — it fails
safe and was a conscious call — but it means **in the deployed binary today, the sampler never
learns what the RCA agent is investigating, and never narrows or widens retention in response.**
The mechanism that lets TraceIQ (per the PRD) avoid "reasoning over sampled-away traces" by having
the agent steer sampling exists as tested code on both ends and is not connected.

**Verdict on D-X5 at time of sign-off: ASPIRATIONAL in the current build, not real.** The design was
sound, both halves were independently correct and tested, and the remaining work was a small,
well-scoped adapter (one-for-one field mapping, both structs already identical) rather than new
design — but as shipped on 2026-09-20, no running instance of TraceIQ exercised the feedback loop. A
trace kept because an active investigation asked for it did not happen then.

**w19 update: no longer aspirational.** The adapter described above as missing now exists
(`cmd/traceiq/interest_adapter.go`, `samplerInterestSink`) and is wired at construction in
`cmd/traceiq/system.go`. `TestInterestSinkAdapter_RCAPredicateReachesRunningSampler`
(`cmd/traceiq/interest_adapter_test.go`) exercises a real `rca.Engine.Investigate` call against a
real `sampler.Impl` with every other keep-class zeroed out, and asserts: (1) while the investigation
is open, a trace for the incident's epicenter service is kept with `Reason == KeepInterest`, proving
the phase-A scope predicate (FR-F06-12a) reached the sampler through the adapter; and (2) after the
investigation concludes, the same trace shape is no longer kept, proving
`RemoveInterestPredicate` also reached the sampler. Full details:
`docs/reports/w19-fix-interestsink-wiring.md`.

### 1.4 Unwired-gaps documentation consistency — **PASS with one exception (see above)**

Checked whether the known-deferred/unwired items are stated the same way, without contradiction,
across `docs/CHECKPOINT.md`, `docs/reports/w17-final-review.md`, and
`Testing_defect.md`/`docs/reports/w18-live-test.md`:

| Gap | CHECKPOINT.md | w17-final-review.md | Testing_defect.md / w18-live-test.md |
|---|---|---|---|
| `internal/api` not wired into `cmd/traceiq` | Implied by phase 4 batch-4 sequencing (api+web slated, `internal/auth` interface-only) | **M5**, explicit, root-caused to `internal/auth` having zero implementations; shapes the whole verdict qualifier | Explicit, under "Known, already-documented gaps confirmed still present" — verification had to route around the pod entirely |
| rca's 5 tool backends (`TraceStore`/`LogStore`/`MetricStore`/`TopologyStore`/`MemoryStore`) are no-op stubs in `cmd/traceiq` | Not named directly, but batch-4 wiring note flags `cmd/traceiq` wiring as outstanding | Named in check 6 (TODO triage) and Minor-1 (downstream MCP tool coverage) | Explicit — `noopMetricStore.MetricQuery` always empty; live-verified that `connection-pool-exhaustion` only confirmed because the test harness substituted a real `MetricStore` |
| DR-11/D-X5 `InterestSink` wired to `nil` in `cmd/traceiq` | **Not mentioned anywhere** | **Not mentioned anywhere** — w17's M5 discusses `internal/api`/`internal/auth` wiring but never touches the sampler-feedback wire | **Not mentioned anywhere** — the S13 live-test walked the full ingest→sampler→store→anomaly→rca path and did not surface (or wasn't asked to check) that the loop closing back into the sampler is absent |

The first two rows are consistent, accurate, and cross-referenced correctly — no document contradicts
another. The third row is the finding of this sign-off: it is a real gap in the built system, it is
not silently *contradicted* anywhere, but it is also not *recorded* anywhere outside a source-code
comment (`interest.go`, `stubs.go`). Given D-X5 is called out by name in the architecture's own
decision register as the thing DR-11 exists to resolve, its non-wiring belongs in the same tier of
visibility as M5, not left to be found by reading `cmd/traceiq/system.go`.

---

## 2. Consolidated list of all known deferred / unwired gaps

Gathered from `docs/CHECKPOINT.md`, `docs/reports/w17-final-review.md`, `Testing_defect.md`,
`docs/reports/w18-live-test.md`, and this sign-off's own source read, in one place:

1. **`internal/api` is not wired into `cmd/traceiq`.** Root cause: `internal/auth` has zero
   concrete `Authenticator`/`Authorizer` implementations; `api/middleware.go` fails closed on nil,
   so wiring it today would 401 every route. (w17 M5)
2. **rca's five tool backends are no-op stubs** (`TraceStore`, `LogStore`, `MetricStore`,
   `TopologyStore`, `MemoryStore` in `cmd/traceiq/stubs.go`) — the rules reasoner runs end-to-end
   but against empty evidence in the real binary; live-confirmed on the S13 drill (w18).
3. ~~**DR-11/D-X5 `InterestSink` is wired to `nil` in `cmd/traceiq`** — the sampler-feedback loop is
   implemented and tested on both sides but not connected.~~ **RESOLVED (w19):** wired via
   `cmd/traceiq/interest_adapter.go`'s `samplerInterestSink`, passed into `rca.NewEngine` in
   `cmd/traceiq/system.go` in place of `nil`; proven with a real cross-component test (see the w19
   update at the top of this document and `docs/reports/w19-fix-interestsink-wiring.md`). Was newly
   surfaced by this sign-off; not previously recorded outside source comments.
4. **9 of 12 MCP tools return `rpcNotImplemented`** (Minor-1, w17) — downstream of gap 1.
5. **`internal/memory`'s admin-only import/export check is deferred to the API layer** and is
   currently unreachable only because gap 1 exists (Minor-5, w17) — **must** close in the same wave
   gap 1 closes, or it becomes a live authorization hole.
6. **`MemWAL` is in-memory, not disk-backed** (Minor-8, w17) — crash-durability (AC-F02-11) not
   testable until a disk implementation lands.
7. **Only 4 of 6 RCA rules implemented** (retry-storm, timeout-mismatch deferred) — flagged in
   `docs/CHECKPOINT.md` phase 4 and confirmed still true by the w18 live run.
8. **OTLP-HTTP and Jaeger-format receivers not built** (only OTLP-gRPC + Zipkin) — CHECKPOINT.md,
   legitimately sequenced behind `internal/auth`/`internal/selfobs`.
9. **`store.Compact()` is a no-op; DuckDB cross-validation and orphan reconciliation not built**
   (CHECKPOINT.md, F03 deferred items).
10. **Topology↔store `is_new` reconciliation for lossless `Changes()`** spans a schema change not
    yet made (CHECKPOINT.md, flagged at wave 10, still open).
11. **`internal/depstub` retains pins for 6 modules with no production importer**
    (`argon2`→auth hashing, `zstd`→Parquet compression, `otel`+`sdk/trace`→self-tracing,
    `errgroup`/`rate`→concurrency/rate-limiting) — each maps 1:1 to an unbuilt feature; accurate
    inventory, not drift (w17 check 7, Minor-4).
12. **W18-D1 (High, FIXED during the live-test wave):** `TieredStore.Append` was stamping every span
    in a multi-hop trace with the trace's root service instead of its own originating service —
    found live against real Istio mesh traffic, fixed with a regression test the same wave. No
    longer open, listed here only for completeness of the gap inventory's history.

None of items 1–2, 4–11 are newly found by this sign-off — they were already correctly documented.
Item 3 is this sign-off's addition to the record.

---

## 3. Authorization

The implementation of TraceIQ is **APPROVED WITH DEVIATIONS** as of 2026-09-20, on the basis of:
DR-2's import-boundary contract verified green against the live tree; DR-5's tenant-first-parameter
convention holding with zero exceptions across every package sampled; a clean, tenant-isolated,
fail-closed build per w17's independent whole-codebase pass; and one live-cluster defect (W18-D1)
found and fixed. The deviation is scoped and named, not open-ended: **DR-11/D-X5's agent-to-sampler
feedback loop must be wired in `cmd/traceiq` — a bounded adapter task, not a redesign — before
TraceIQ's PRD-level differentiating claim ("AI agents steer what gets sampled") is true of the
shipped binary**, and this item should be added to `docs/CHECKPOINT.md`'s gap list and w17-class
review tracking alongside M5 so it carries the same visibility.

*Chief Architect — final sign-off. Next action: file the DR-11/`InterestSink` wiring as a tracked
item (same tier as M5) and schedule the adapter in the wave that next touches `cmd/traceiq`.*

---

**w19 follow-up note:** the next action above has been completed. `cmd/traceiq/interest_adapter.go`
wires the real `sampler.Impl` into `rca.Engine`'s `InterestSink`, `cmd/traceiq/system.go` no longer
passes `nil`, and `cmd/traceiq/interest_adapter_test.go` proves the effect is real end-to-end (a
predicate pushed by `rca.Engine.Investigate` changes a live `sampler.Impl` keep decision, and its
removal on conclusion is likewise observed). `go build ./...`, `go vet ./...`, and
`go test ./... -count=1` are all green against the full repo after this change. Full report:
`docs/reports/w19-fix-interestsink-wiring.md`. TraceIQ's PRD-level differentiating claim ("AI agents
steer what gets sampled") is now true of the shipped binary; the deviation named above is closed.
