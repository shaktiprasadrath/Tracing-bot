# W9 store continuation — parquet fix + tiered implementation

## Summary

1. **Fixed `internal/store/parquet/parquet.go`** (2 build errors): `pq.NewGenericReader[T]` in
   parquet-go v0.32.0 takes `io.ReaderAt` + `...ReaderOption` only — no size option exists
   (confirmed via `go doc`). Both call sites (`ReadTrace`'s trace/span readers, L487/L521) dropped
   the `tfi.Size()`/`sfi.Size()` argument; `*os.File` already satisfies `io.ReaderAt`.
2. Added `parquet.Store.DueBlocks` (list open blocks past the flush interval, without sealing) and
   `parquet.Store.Close` (release open WAL handles) — both additive, used by tests and by
   `store/tiered`'s caller-supplied seal-cycle discovery.
3. Wrote `internal/store/parquet/parquet_test.go` (14 test funcs incl. 4 subtests, all passing):
   Append-before-seal readability, Seal manifest/file correctness, read-back round-trip after seal,
   SealDue age gating, DueBlocks non-sealing, manifest reload on reopen, retention boundary
   (one-second-before survives / exactly-at and one-second-after deleted / other tier untouched),
   Tombstone unreadability, and a ReplayWAL crash-recovery approximation (fresh `Store` over the
   same dir recovers an unsealed trace).
4. Implemented `internal/store/tiered/tiered.go`. **Correction to the task's premise**: `doc.go`'s
   allowed-imports line was right and `archtest.TestPackageAdjacency` enforces it — `store/tiered`
   may import `internal/store` but **not** `store/sqlite`/`store/parquet` directly (first attempt,
   which called `sqlite.Open`/`parquet.Open` from a `tiered.Open(cfg)` constructor, failed
   `TestPackageAdjacency`). Redesigned to depend only on `store.HotIndex`/`store.ColdStore`:
   `tiered.New(hot, cold, clock) *Store` embeds `*store.TieredStore` (all its methods — `Append`,
   `GetTrace`, `SealAndBind`, `EmitCostSignal`, `Signals`, `SweepRetention` — already implemented
   `store/store.go`) and adds `RunSealCycle` (seal-then-bind one block at a time over a
   caller-supplied `[]DueBlock`, DR-7 ordering) and `SampleCostSignal` (measures
   `Hot.IngestedBytes`/`Hot.DiskUsage`, feeds `EmitCostSignal`, DR-12), plus `Close`/`Health`.
5. Wrote `internal/store/tiered/tiered_test.go` (6 test funcs, all passing): mock-based ordering
   invariant tests (`cold.Append` before `hot.WriteBatch`; `cold.Seal` before `hot.BindColdBlock`;
   `hot.BindColdBlock` never runs if `Seal` fails; partial-cycle failure returns progress so far)
   plus two real-driver (sqlite+parquet) round-trip tests: write+`GetTrace` via WAL, and
   `SealAndBind` switching the read path from WAL to the sealed block.

## Status

STATUS: complete — both build errors fixed, `parquet` and `tiered` fully implemented and tested.
Tests: 21/21 pass (`go test ./internal/store/... -v`, includes parquet 14, sqlite 6, tiered 6 test
funcs/subtests — sqlite untouched, pre-existing). `go build ./...`, `go vet ./...`,
`go test ./...` all green, no regressions elsewhere.
Remaining / out of scope for this pass: DuckDB-CLI cross-validation of Parquet output
(AC-F03-8), true process-kill/power-loss crash tests (AC-F03-1, integration-level), orphan
reconciliation (FR-F03-14), Compact/compaction (`parquet.Store.Compact` still a documented no-op),
and wiring `tiered.Store` into `cmd/traceiq` (its `Open`-style constructor was deliberately dropped
per the adjacency-table correction above — `cmd/traceiq` must construct `sqlite.Store`/
`parquet.Store` itself and inject them via `tiered.New`).
