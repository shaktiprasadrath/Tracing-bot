# W12 — Review: internal/correlate and internal/memory

**Verdict: APPROVE-WITH-FIXES**

Reviewed `internal/correlate/**/*.go` and `internal/memory/**/*.go` against F07/F08's FR/AC list, DR-16,
DR-19, DR-20. Read `docs/reports/w11-memory-cont.md` first (no `w11-correlate-memory.md` exists —
`internal/correlate` was last substantively discussed in `docs/reports/w1-F07-F08.md`, a docs-only pass).
Found and fixed 3 Major bugs (1 in `correlate`, 2 in `memory`); found 1 further Major spec gap in
`correlate` that is **not** fixed here because closing it correctly requires an interface-level decision
outside a review's blast radius. Tenant isolation in `memory` is verified solid — no bypass found after
independently trying to break it. `mathPow` and the fingerprint-dilution scoring bug from the prior pass
are both confirmed fixed and complete.

## Findings table

| # | Severity | Area | Finding | Status |
|---|---|---|---|---|
| 1 | Major | `correlate/correlator.go` | FR-F07-7's circuit breaker was entirely unimplemented: `ErrAdapterUnavailable` was declared but never returned anywhere, no consecutive-failure counter existed, and `Stats().BreakerOpen` was hardcoded `false`. An unhealthy adapter would be called on every single request forever — no fail-fast, contradicting AC-F07-3 and the round-1 doc's own claim ("breaker (5 failures → 30s)" in `w1-F07-F08.md`). | **FIXED** |
| 2 | Major | `memory/store.go` `Similar()` | Candidate generation capped at `maxCandidates` by breaking out of a **Go map `range` loop**, whose iteration order is randomized per-process. Once a tenant holds more than 500 records, this silently, non-deterministically dropped the true best (or only) match in favor of unrelated records that happened to be visited first — a correctness bug, not just a latency shortfall, and a violation of FR-F08-2/DR-19 §19.2's "rarest-first keeps the selective candidates." The existing `TestSimilar_BoundedByMaxCandidates` test couldn't catch this because all its seeded records share identical tokens (any subset scores equally). | **FIXED** |
| 3 | Major | `memory/store.go` `Similar()` | Data race: after releasing `s.mu`, the method re-read the **live** tenant map (`tenantRecsSnapshot(tenantRecs)`, a bare pass-through of the map reference, not a copy) to look up each candidate's effective weight — concurrently with `Record`/`Correct`/`Confirm`/`Delete`, all of which mutate that same map under `s.mu`. Textbook concurrent map read/write; would very likely be caught by `go test -race` (not runnable in this sandbox — no `gcc` on `PATH` for cgo). | **FIXED** |
| 4 | Major | `correlate/correlator.go` `LogsForTrace` | FR-F07-5's heuristic `QueryByServiceWindow` fallback is unimplemented (explicit `TODO` in the code). Root cause is architectural: the DR-20-canon `Correlator.LogsForTrace(ctx, tid, traceID, w, limit)` signature carries no service/`RootService`, which the fallback needs. AC-F07-5 is therefore not met when no adapter has `trace_id`-tagged data — `LogsForTrace` just returns an empty bundle instead of a labeled heuristic one. Zero test coverage of this path exists. | **NOT fixed** — needs a DR-level interface decision (extend the signature, or have the caller thread `RootService` through), which is a wider blast-radius change than a review fix should make unilaterally. Flagging for a follow-up pass. |
| 5 | — | `memory/store.go` `pow()` | `mathPow` build-break fix from `w11-memory-cont.md` verified: `pow()` correctly delegates to `math.Pow` for the fractional-exponent decay formula. | Verified, no issue |
| 6 | — | `memory/scorer.go` `lexicalScorer.Score` | Fingerprint-token-dilution fix verified **complete**: fingerprint Jaccard and free-text Jaccard are scored as two independent components in the one and only scoring path that exists in this codebase (no `hashed`/`anthropic` scorer is implemented yet, so there is nothing else to check). | Verified, no issue |
| 7 | — | Tenant isolation, `memory` | Read all 5 dedicated tests plus every `Store` method's implementation. Tests perform genuine cross-tenant attempts (identical-shape query from tenant B, tenant B querying with **tenant A's own `Fingerprint` object** as a forged/replayed fingerprint, cross-tenant `Get`/`Search`/`Correct`/`Confirm`, `Export` content + count check) and assert real denial (`len==0`, non-nil error, victim record provably unchanged after a rejected `Correct`/`Confirm`) — not merely "no error". Every `Store` method scopes strictly via `s.tenantMap(tid)` (the `tid` **argument**), never via any tenant value embedded in caller-supplied data (`Fingerprint.TenantID` is caller-settable and deliberately ignored for scoping). Tried to construct a bypass myself (forged fingerprint, colliding record IDs across tenant maps, `Consolidate()`'s cross-tenant loop) — found none. | Verified — solid |
| 8 | — | Tenant isolation, `correlate` | `FileLogAdapter`/`FileMetricAdapter` scope by per-tenant subdirectory (`filepath.Join(root, string(tid))`); `TestFileLogAdapter_TenantIsolation` passes. | Verified, no issue |
| 9 | Minor | `correlate/dev_adapter.go` | Tenant ID is interpolated unsanitized into a filesystem path (`filepath.Join(a.root, string(tid))`) for the dev/file adapter. Not exploitable today (tenant IDs come from the authenticated context upstream, not directly from request bodies at this layer), but there's no defense-in-depth validation of the tenant-ID charset at this boundary. | Not fixed — flagged for follow-up |
| 10 | Minor | `memory/runbook.go` `RunbookImporter` | Not yet wired to any API/CLI (`grep` across the repo found zero callers besides tests), so there is currently no live path-traversal or injection surface — `Import` takes `[]byte` markdown directly, no file-path handling. However, DR-19 §19's security section requires runbook bodies to pass the shared secret scrubber at import time (they originate outside the RCA pipeline); this is explicitly not implemented, a documented deviation (scrubber package isn't in `memory`'s DR-2 allowed-import list). Must be resolved before `RunbookImporter` is wired to any endpoint accepting untrusted Markdown. | Not fixed — pre-existing documented gap, correctly flagged by the prior pass |
| 11 | Minor | `memory/store.go` `Similar()` | The post-scoring step that swaps two slice elements to force superseder-before-superseded ordering does a raw index swap, which (in rare 3+-way tie scenarios) can leave the slice no longer globally monotonic by `rank`. Low practical impact — `AC-F08-3`'s actual requirement (the pair's relative order) is preserved — but worth a cleanup note. | Not fixed — cosmetic/edge-case only |
| 12 | — | `correlate/correlator.go` bounded fan-out | Verified real and tested: per-tenant + global `semaphore.Weighted`, `TryAcquire` (non-blocking), `ErrAdapterBusy` returned immediately, never queued (`TestCorrelator_BoundedFanOut_DoesNotExceedCap`). | Verified, no issue |
| 13 | — | `correlate/correlator.go` cache | TTL checked on read; key includes tenant + adapter-args hash equivalent; per-tenant partitioned eviction (`CacheMaxEntries / len(tenantOrder)`), insertion-order (not true LRU-by-access) — a reasonable, already-documented stand-in. No staleness or key-collision bug found. | Verified, no issue |
| 14 | — | `memory` AC-F08-7 assessment | Agree with `w11-memory-cont.md`: load-testing the current in-memory O(n)-scan store against the 100k-row/200ms target would validate the wrong architecture. Finding #2 above was a real, cheap-to-fix correctness bug hiding inside that documented deviation; now fixed. No further safeguard needed beyond it — a real SQLite + `memory_fp_token` inverted index remains the correct fix for the latency claim itself. | Assessed, partial fix applied (finding #2) |
| 15 | — | Test quality | Both packages' tests assert concrete values/behavior (exact counts, exact weight deltas, ranked order, denial + unchanged-victim-state), not weak "no error" checks. Good quality throughout; added 3 new tests for the fixes above (`TestCorrelator_BreakerOpensAfterConsecutiveFailures`, `TestSimilar_CandidateCutKeepsBestMatch_NotArbitrarySubset`, plus the race fix is covered structurally by not touching the map post-unlock). | — |

## Fixes applied

1. **`internal/correlate/correlator.go`** — added `breakerState` (per-adapter, keyed by `Name()`):
   opens after `Config.BreakerFailures` (default 5) consecutive failures for `Config.BreakerOpenDuration`
   (default 30s); while open, calls fail fast with `ErrAdapterUnavailable` without reaching the adapter.
   Wired into both `LogsForTrace` and `MetricsForSpan` fan-out loops; `Stats().BreakerOpen` now reflects
   real state via `anyBreakerOpen()`. New config fields default via `DefaultConfig()`/`New()`'s existing
   zero-value-fallback pattern.
2. **`internal/memory/store.go` `Similar()`** — candidate generation now scores every tenant record by
   raw fingerprint-token overlap against the query before cutting at `maxCandidates`, instead of cutting
   at an arbitrary Go-map-iteration-order point; this guarantees a record that actually shares tokens
   with the query is never evicted in favor of one that shares none. The effective-weight lookup used
   later in the function is now snapshotted into a plain `map[string]float64` while `s.mu` is still held,
   removing the post-unlock live-map read (the race). Removed the now-dead `tenantRecsSnapshot` helper.

## Commands run (all green)

```
go build ./internal/correlate/... ./internal/memory/...   # clean
go vet ./internal/correlate/... ./internal/memory/...     # clean
go test ./internal/correlate/... ./internal/memory/... -v # 36/36 pass (34 pre-existing + 2 new)
go build ./...                                             # clean
go test ./...                                               # all packages pass, including internal/archtest
                                                              # (its rca/rules adjacency gap noted in
                                                              # w11-memory-cont.md is no longer failing —
                                                              # resolved elsewhere, unrelated to this pass)
```

`go test -race` could not be run in this sandbox (`gcc` not on `PATH`, required for cgo/-race on this
Windows box); finding #3's race was confirmed by code inspection (a live map reference read outside the
lock, concurrently with lock-held writers in four other methods), not by a race-detector run.

## Remaining follow-up (not fixed this pass)

- **Correlate FR-F07-5 heuristic fallback** (finding #4) — needs a DR-level decision on how
  `LogsForTrace` obtains a service to fall back on, since the canonical DR-20 signature doesn't carry one.
- **RunbookImporter secret scrubbing** (finding #10) — block wiring it to any real endpoint until this is
  resolved; currently no live exploit path since nothing calls it outside tests.
- **Dev-adapter tenant-ID path hygiene** (finding #9) — cheap defense-in-depth validation, not urgent.
- Everything else already flagged as open in `w11-memory-cont.md` (SQLite/inverted-index/MinHash-LSH
  deviations, `textWeight` judgment call, `rca.Sanitizer` wrapping living outside this package) still
  stands; not re-litigated here since nothing new was found on those fronts.
