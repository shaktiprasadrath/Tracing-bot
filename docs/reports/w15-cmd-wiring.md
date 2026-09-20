# w15 — cmd/traceiq wiring: two bugs found by the smoke test

This wave's task was to fix two real bugs surfaced by
`TestSystem_EndToEnd_OTLPSpanReachesStore`
(`cmd/traceiq/system_smoke_test.go`), the headline integration test that
assembles the real `System` (store, topology, sampler, ingest) and drives a
synthetic OTLP span through it end to end. The wiring itself (`cmd/traceiq`
config loading, `System.Start`/`Shutdown` ordering, the smoke test) was
otherwise complete and compiling clean going into this wave — see the wave's
task brief for that context; this file covers only the two bugs and their
fixes.

## Bug 1 — `path_signature` uint64/int64 round-trip (internal/store/sqlite)

**Symptom:**

```
trace never reached the store within the deadline; last GetTrace error: sql: Scan error on
column index 4, name "path_signature": converting driver.Value type int64
("-1205034819632174695") to a uint64: invalid syntax
```

**Root cause.** `model.Trace.PathSignature` (`internal/model/telemetry.go`)
is a `uint64` — an xxh3 hash of the ordered (service, operation) edge list
(DR-10 §10.4) — and SQLite has no native unsigned integer type. The write
path in `internal/store/sqlite/hotindex.go`'s `WriteBatch` already handles
this correctly: it stores the hash as `int64(t.PathSignature)`, a plain bit
reinterpretation, into the `path_signature INTEGER NOT NULL` column
(`schema.go`), consistent with DR-6's schema (`path_signature` is a signed
`INTEGER` column, not `TEXT`; DR-6 does not call for a storage-format
change). Any hash with the sign bit set (roughly half of all hashes) is
therefore stored as a negative `int64`.

The bug was on the **read** side: `GetTrace` and `SearchTraces` scanned that
same column straight into `*uint64` destinations (`&ti.PathSignature`,
where `ti store.TraceIndex` and `PathSignature uint64`). `database/sql`'s
generic scan converts an `int64` driver value into a `*uint64` destination
via `strconv.ParseUint` on the value's string form — which rejects a
leading `-` with exactly the observed "invalid syntax" error. So any trace
whose path-signature hash happened to be negative as an `int64` (about half
of all traces, and the specific hash the smoke test's `checkout-service` /
`POST /checkout` edge produced) could be written but never read back.

**Fix.** `internal/store/sqlite/hotindex.go`, both read sites
(`Store.GetTrace` and `Store.SearchTraces`): scan the column into a local
`int64` instead of `*uint64`, then bit-reinterpret with `uint64(pathSig)`
before assigning to `store.TraceIndex.PathSignature`. No schema change, no
change to the hash algorithm, no change to the write path — `WriteBatch`
was already correct. `PathSeen`'s existing scan (`internal/store/sqlite/hotindex.go`,
the `path_signature` lookup table's `last_seen` column) was unaffected;
it scans a timestamp, not the signature itself, into `int64` already.

## Bug 2 — double-seal on shutdown (cmd/traceiq/system.go)

**Symptom:**

```
shutdown: seal blk-smoke-tenant-1-1789855200-1: parquet: unknown open block "blk-smoke-tenant-1-1789855200-1"
```

**Root cause.** This was a misuse of `store/parquet`'s API by
`System.Shutdown`'s X9 step, not a bug inside `store/parquet` itself.
`parquet.Store` exposes two different ways to get at due-for-seal blocks:

- `SealDue(ctx, now)` — seals every due block **itself** (removes each from
  the internal `open` map, writes its Parquet files/manifest, adds it to
  `sealed`) and returns the list of block IDs it just sealed.
- `DueBlocks(now)` — a side-effect-free **listing** of open blocks past the
  flush interval, meant for a caller (like `store.TieredStore.SealAndBind`,
  see `internal/store/tiered/tiered.go`) that wants to seal one block at a
  time itself and do something with each manifest as it's produced (here:
  bind it into the hot index via `hot.BindColdBlock`).

`System.Shutdown` needed the second pattern — its own comment says as much
("Seal + BindColdBlock is used directly here... requires the caller to
already know the tenant up front") — but called the first: it treated
`SealDue`'s return value (block IDs *already sealed*) as a still-open list,
then called `s.cold.Seal(shutdownCtx, blockID)` on each one again. The
second `Seal` call always failed with "unknown open block", because
`SealDue`'s own call to `Seal` had already deleted the block from `s.open`
on the first pass. `BindColdBlock` was consequently never reached for any
block sealed this way, and the shutdown log carried one spurious error per
open block — benign for the smoke test's single-span trace (the trace was
already readable from the hot index before shutdown ran) but a real defect:
any block sealed only at shutdown would never get bound into the hot
index's `block_id`/`row_group`/`row_offset` columns, leaving cold-tier rows
that `GetTrace`'s cold-fallback path could never locate.

**Fix.** `cmd/traceiq/system.go`, `Shutdown`'s X9 step: replaced the
`SealDue` call with `s.cold.DueBlocks(time.Now().Add(24*time.Hour))`
(list-only) and kept the existing per-block `Seal` + `BindColdBlock` loop
unchanged, now operating on blocks that are genuinely still open. This
matches the same seal-then-bind-one-at-a-time pattern
`store.TieredStore.SealAndBind`/`tiered.go`'s own seal cycle already uses
for the identical situation, so the fix is idiomatic to the codebase rather
than a one-off workaround.

## Verification

```
GOTOOLCHAIN=go1.27.1 CGO_ENABLED=0 go build ./...                             # clean
GOTOOLCHAIN=go1.27.1 CGO_ENABLED=0 go vet ./...                               # clean
GOTOOLCHAIN=go1.27.1 CGO_ENABLED=0 go test ./cmd/traceiq/... \
    -run TestSystem_EndToEnd_OTLPSpanReachesStore -v                          # PASS
GOTOOLCHAIN=go1.27.1 CGO_ENABLED=0 go test ./cmd/... ./internal/store/... -v  # all PASS
GOTOOLCHAIN=go1.27.1 CGO_ENABLED=0 go test ./...                              # only traceiq/internal/api FAILs
```

The lone `go test ./...` failure is `TestMCP_SearchTraces_RealStore` in
`traceiq/internal/api` — pre-existing, part of the sibling agent's
concurrent `w15-api-web` work (see `docs/reports/w15-api-web.md`), and
explicitly out of this wave's scope lock.

Every package touched this wave (`cmd/traceiq`, `internal/store/sqlite`,
`internal/store/parquet`, `internal/store/tiered`) is green. The one
full-repo test failure (`traceiq/internal/api`) is unrelated to this wave's
scope lock and was already known-broken per the task brief.

## Files changed

- `internal/store/sqlite/hotindex.go` — `GetTrace`, `SearchTraces`: scan
  `path_signature` into `int64` and bit-reinterpret to `uint64`.
- `cmd/traceiq/system.go` — `Shutdown`: use `DueBlocks` (list) instead of
  `SealDue` (seals internally) before the manual `Seal`/`BindColdBlock`
  loop, eliminating the double-seal.
