# W18-D1 Fix Report — Span-level `service` attribution collapses to the trace's root service

Fixes the defect logged in `Testing_defect.md` under W18-D1, found during
the wave 18 live end-to-end test against a real Istio mesh
(`docs/reports/w18-live-test.md`).

## Root cause (confirmed)

`internal/store/store.go`, `TieredStore.Append`, span-conversion loop
(pre-fix around line 664):

```go
spans = append(spans, SpanIndex{
    Tenant:        tid,
    TraceID:       t.TraceID,
    SpanID:        sp.SpanID,
    Service:       t.RootService,   // BUG
    Operation:     sp.Name,
    StartUnixNano: int64(sp.StartUnixNano),
    DurationNanos: sp.EndUnixNano - sp.StartUnixNano,
})
```

Every `SpanIndex.Service` was stamped with the trace's overall
`t.RootService` instead of the individual span's own originating service.
This is invisible in single-hop synthetic traces (root service and span
service are trivially identical) and only surfaces on a real multi-hop
trace — confirmed live in the W18 test with 276 real span rows all
mis-stamped as the trace's root service regardless of which service
actually produced them.

The per-span service identity was never missing from the model: the OTLP
normalizer (`internal/ingest/otlp_normalize.go`'s `convertOTLPSpan`) already
attaches each span's own `Resource` (populated from that span's own OTLP
`ExportTraceServiceRequest.ResourceSpans.Resource`, `service.name` →
`model.Resource.ServiceName`) before the sampler assembles the trace.
`model.Span.Service()` (`internal/model/telemetry.go:135`) is meant to
expose this but is still a `panic("not implemented")` stub, so it can't be
called directly — `internal/sampler/spanutil.go` had already hit this same
constraint and worked around it with a local `spanService` helper reading
`sp.Resource.ServiceName` (documented there as a scope-lock workaround,
since `internal/sampler` may not edit `internal/model` either).

## Fix applied

`internal/store/store.go`:

- Added a package-local `spanService(sp *model.Span) string` helper
  (just above `Append`) that returns `sp.Resource.ServiceName` (empty
  string if `sp.Resource` is nil) — mirroring `internal/sampler/spanutil.go`'s
  helper of the same name, for the same "can't touch `internal/model`"
  reason.
- `TieredStore.Append`'s span-conversion loop now sets
  `Service: spanService(&sp)` instead of `Service: t.RootService`.
- `TraceIndex.RootService` (the trace-level summary field, and
  `model.Trace.RootService` itself) are untouched — they are correct as a
  trace-level summary; only the per-span `SpanIndex.Service` column was
  wrong.

No changes were made to `internal/ingest`, `internal/model`, or any package
outside `internal/store` — the per-span service field (`sp.Resource.ServiceName`)
was already present and correctly populated; only its use at the
`store.Append` write site was wrong.

## Test added

`internal/store/store_test.go` (new file, external `store_test` package so
it can use the real `sqlite.HotIndex` implementation as the hot index
without an import cycle):

- `TestAppend_SpanServicePerSpan_NotRootService` — builds a synthetic
  3-hop trace sharing one `trace_id`: a root span from
  `mesh-client.bank`, a child span from `transaction-orchestrator.bank`,
  and a grandchild span from `ledger-service.bank` (mirroring the live
  W18 repro's real service names). Appends it via `store.NewTieredStore(...).Append`
  against a real temp-file `sqlite.Store` hot index and a minimal fake
  `ColdStore`, then:
  - queries the hot index back (`SearchSpans` over the trace's time
    window) and asserts each returned span's `Service` equals its own
    originating service, not the trace root;
  - additionally asserts `SearchSpans` filtered by the non-root service
    `transaction-orchestrator.bank` finds exactly that span — directly
    exercising the impact called out in `Testing_defect.md`
    ("`SearchSpans` filtered by `Service` cannot find spans from a
    non-root service").

### Before / after

**Before the fix** (test run against the unmodified code):

```
--- FAIL: TestAppend_SpanServicePerSpan_NotRootService (0.06s)
    store_test.go:173: span 0300000000000000 (operation "POST /ledger"): Service = "mesh-client.bank", want "ledger-service.bank" ...
    store_test.go:173: span 0200000000000000 (operation "POST /payment"): Service = "mesh-client.bank", want "transaction-orchestrator.bank" ...
    store_test.go:185: SearchSpans(Service=transaction-orchestrator.bank): got [], want exactly the child span
FAIL
```

**After the fix:**

```
--- PASS: TestAppend_SpanServicePerSpan_NotRootService (0.06s)
PASS
ok  	traceiq/internal/store	1.976s
```

## Verification

- `internal/store/...` full suite (top-level `store`, `store/parquet`,
  `store/sqlite`, `store/tiered`): all green, no existing test's
  assertions needed adjustment (every pre-existing test used single-hop or
  root-equals-span-service traces, so none was relying on the bug).
- Full repo: `go build ./...` — OK. `go vet ./...` — OK.
  `go test ./... -count=1` — all packages `ok` (including
  `internal/archtest`, which enforces package-boundary/scope invariants).
- `gofmt -l .` — clean, no files need formatting.

## Scope

Touched only: `internal/store/store.go`, `internal/store/store_test.go`
(new), `Testing_defect.md`, and this report — per the assigned scope lock.
`internal/ingest`, `internal/model`, and all other packages are unchanged.
