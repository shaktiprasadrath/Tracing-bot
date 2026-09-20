# TraceIQ — Test Lead Sign-off

**Project:** TraceIQ — distributed-tracing RCA bot (Go 1.27, `GOTOOLCHAIN=go1.27.1`, `CGO_ENABLED=0`)
**Date:** 2026-09-20
**Issued by:** Test Lead
**Document:** `docs/signoffs/test-lead-signoff.md`

---

## Verdict

> ## **APPROVED WITH NOTED COVERAGE GAPS**

The unit-test suite is large, green, and stable across repeated runs, and the one live-cluster
scenario that was run (S13) is a genuine, well-documented, defect-finding end-to-end test with a
verified fix. That is real evidence the core ingest→sampler→anomaly→RCA pipeline works against a
real Istio mesh. It is not evidence that TraceIQ is production-load-ready: outside S13, there is
zero live/load/soak/chaos evidence, the `api`/`web` layer has never seen real HTTP traffic because
it isn't wired into the shipped binary, and 22 of the 23 istio-tx-lab scenarios have never been run
against this codebase. Sign-off is for **testing-phase completion as scoped**, not for a
production-load guarantee.

---

## 1. Independently verified numbers (run directly, not taken from the ledger)

Environment: `GOTOOLCHAIN=go1.27.1 CGO_ENABLED=0`, repo root `Tracing-bot`.

| Check | Command | Result |
|---|---|---|
| Test files | `find internal cmd -name '*_test.go' \| wc -l` | **51** `_test.go` files |
| Passing subtests/tests | `go test ./... -count=1 -v 2>&1 \| grep -c '^--- PASS'` | **375** `--- PASS` lines |
| Failures (single run) | `grep -c '^--- FAIL'` on the same run | **0** |
| Build | exit code of the `-v` run | **0** (all packages `ok`) |
| Flakiness | `go test ./... -count=3 2>&1 \| grep -E 'FAIL'` | **0 matches** — every package `ok` on all 3 repeated runs, including the slowest package (`internal/sampler`, ~155s for 3 runs) |

Package-level test-file distribution (`internal/sampler` 9, `internal/rca` 6, `internal/anomaly` 5,
`internal/nl` 4, `internal/eval` 4, `internal/remediate` 3, `internal/ingest` 3, `internal/api` 3,
`cmd/traceiq` 3, `internal/topology` 2, one each for `store`, `store/tiered`, `store/sqlite`,
`store/parquet`, `rca/rules`, `memory`, `correlate`, `config`, `archtest`). No test files exist for
`internal/auth`, `internal/bus`, `internal/cluster`, `internal/k8s`, `internal/llm`,
`internal/model`, `internal/selfobs`, `internal/tenant`, `web` (several of these are thin
type-only or stub packages; `web` and `internal/llm` are the ones worth flagging as genuinely
untested surface).

**No flaky test surfaced.** Three consecutive full-suite runs (`-count=3`, i.e. every test executed
three times in sequence) produced no `FAIL` anywhere — the W18/W18-D1 fix wave did not leave
behind, and did not introduce, any intermittent failure.

## 2. Defect log status

`Testing_defect.md` (repo root): one logged defect, **W18-D1** (span-level `service` collapsing to
`t.RootService` in `TieredStore.Append`), **Status: Fixed**. Verified against `docs/reports/w18-fix-d1.md`:
root cause confirmed, fix applied (`spanService` helper reading `sp.Resource.ServiceName`,
mirroring the existing `internal/sampler/spanutil.go` pattern), regression test
`TestAppend_SpanServicePerSpan_NotRootService` added in `internal/store/store_test.go`, shown
failing pre-fix / passing post-fix in the report, and now included in the 375 passing tests
observed above. **Zero open defects.** The log's "known, already-documented gaps" section (api not
wired, rca tool backends stubbed) is explicitly *not* logged as a defect — it's a scope gap, which
is exactly where the coverage assessment below picks it up.

## 3. Coverage-balance assessment — be honest about what kind of evidence this is

**Well-tested (unit level, high confidence in correctness of logic):**
`sampler`, `rca` + `rca/rules`, `anomaly`, `nl`, `eval`, `remediate`, `ingest`, `store` (+ `tiered`/
`sqlite`/`parquet`), `topology`, `memory`, `correlate`, `config`, and `archtest` (which enforces
package-boundary/scope invariants at test time — a nice belt-and-suspenders control against scope
creep). This is genuine TDD coverage: 51 test files, 375 passing assertions, deterministic across
3 repeated runs.

**Proven under one real live scenario (S13, connection-pool-exhaustion), not the other 22:**
Wave 18 deployed the real binary into a real `docker-desktop` cluster, wired it into a real Istio
mesh, drove real OTLP traffic through a 100-request S13 fault-injection load burst, and confirmed
the real ingest → sampler (`KeepError`) → `anomaly.ErrorBurstDetector` → `rca.Engine` chain
produced the correct conclusion (`connection-pool-exhaustion`, confidence 0.80) — and it caught a
real defect (W18-D1) that no unit test had caught, because every existing unit test used
single-hop or root-equals-span-service traces. That is real, valuable, defect-finding evidence that
package-level unit tests alone did not provide. But it is **one scenario out of the istio-tx-lab
lab's full set** (`S13` only; the report itself frames this as "the first time" a real multi-hop
trace was exercised). The other ~22 documented lab scenarios (different fault classes — e.g.
latency injection, cascading timeouts, N+1 patterns, error-signature-after-deploy, whatever
SCENARIOS.md enumerates) have **never been run against this codebase**. The `rca.Engine`'s other
rule paths (`error-signature-new-after-deploy`, `downstream-latency-propagation`,
`n-plus-one-span-pattern`) were only *attempted and refuted* in S13 for lack of evidence — none of
them has ever been *proven correct* end-to-end the way `connection-pool-exhaustion` now has.

**Genuinely untested / structurally untestable-as-shipped:**
- `internal/api` has 3 unit-test files but the package **is not wired into `cmd/traceiq`'s binary
  at all** (`cmd/traceiq/system.go:30-31,190-193`, `rca.NewRegistry` in `cmd/traceiq/system.go:152`
  wires all five tool backends — `TraceStore`/`LogStore`/`MetricStore`/`TopologyStore`/
  `MemoryStore` — to no-op stubs in `cmd/traceiq/stubs.go`). This means there is **zero test
  evidence of the api/web layer's actual HTTP/MCP behavior against real traffic**, because the
  shipped binary has no HTTP/MCP query surface to test against. Unit tests exercising `internal/api`
  in isolation are not the same claim as "the deployed pod answers real queries correctly" — they
  aren't, today, because there's no live surface for it to answer on.
- Because the rca tool backends are stubbed, **the real deployed pod can never confirm any
  `MetricQuery`-based rule on its own** (confirmed directly in `Testing_defect.md`'s gap section)
  — S13's RCA conclusion only worked because the verification harness substituted a real
  evidence-backed `MetricStore` standalone, outside the running pod.
- `internal/llm` and `web` have no test files at all.
- **Zero load, soak, or chaos testing beyond the single S13 run.** No sustained-duration run, no
  concurrent-scenario run, no resource-exhaustion/backpressure test on TraceIQ's own ingest path
  (as opposed to the mesh's), no test of behavior under sampler/store pressure over time. The S13
  load was a single ~100-request burst over roughly a minute, not a soak.

**Bottom line:** "unit-tested" (375 green assertions) and "production-load-tested" are different
claims, and this codebase currently supports the first strongly and the second only for one narrow
slice (ingest→sampler→anomaly→RCA's `connection-pool-exhaustion` path, single burst, single
scenario). Treating one live pass as general production-readiness evidence would be overconfident.

## 4. Rationale for verdict

- Zero open defects, 375/375 passing, zero flakiness across 3 repeated full-suite runs: the
  **testing-phase work that was scoped and executed is complete and clean**. Nothing here blocks
  sign-off on its own terms.
- The gap is not a defect to fix before sign-off — it's a **coverage-scope gap**, explicitly
  documented in `Testing_defect.md` itself as pre-existing and not new. Blocking sign-off on it
  would conflate "testing phase done" with "production load-tested," which are different gates.
- **APPROVED WITH NOTED COVERAGE GAPS** rather than a plain APPROVED, so the gap is on record
  going into any later production-readiness or load-testing gate: (1) `internal/api`/`web` need a
  real wiring + live-traffic test once `cmd/traceiq` actually exposes them, (2) the remaining ~22
  istio-tx-lab scenarios remain unrun against this codebase, (3) no load/soak/chaos test exists
  beyond the single S13 burst.

---

## 5. Recommended follow-up (non-blocking for this sign-off)

1. Wire `internal/api` into `cmd/traceiq` (tracked as a pre-existing TODO, `cmd/traceiq/system.go:190-193`)
   and add a live-HTTP-traffic test against the running binary.
2. Run the remaining istio-tx-lab scenarios (beyond S13) against the deployed binary, at minimum
   the ones targeting the `rca.Engine` rules that S13 could only refute for lack of evidence.
3. Add a load/soak test (sustained multi-minute traffic, not a single burst) and a basic
   chaos/backpressure test against TraceIQ's own ingest path before any production-load claim is
   made.
4. Implement real `MetricStore`/`TraceStore`/`LogStore`/`TopologyStore`/`MemoryStore` backends (or
   at least one) in `cmd/traceiq` so the deployed pod — not just a standalone verification
   harness — can confirm rca findings end-to-end.
