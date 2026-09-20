# W9 — internal/ingest continuation report

## Status: COMPLETE (with documented scope reductions)

Fixed the broken build (unused `fmt`, undefined `sinkQueue`/`metrics`/`otlpGRPCReceiver`) by
completing the prior agent's `Server` design rather than replacing it — F01 §4.4's HandleInbound
pseudocode genuinely needs a per-sink bounded queue (FR-F01-6/DR-28), a tenant/limits pipeline
(FR-F01-7/8/10), and a registered OTLP gRPC receiver (FR-F01-1), so a `Server` aggregate owning
queues+receivers+metrics was the right shape, not overcomplication.

## Files (all under `internal/ingest/`)

- `ingest.go` — `Server`, `New()`, `Start`/`Stop`, `Server.Ingest` (§4.4's HandleInbound: normalize →
  limits → tenant resolve/stamp → fan-out).
- `otlp_normalize.go` — `OTLPNormalizer`: `*collector/trace/v1.ExportTraceServiceRequest` →
  `model.Batch`, verbatim attribute preservation (FR-F01-7), rejects all-zero/wrong-length trace/span IDs.
- `zipkin_normalize.go` — `ZipkinNormalizer` (Zipkin v2 JSON, FR-F01-5) + `B3HeadersToTraceParent`
  (B3 single/multi-header → W3C traceparent string).
- `limits.go` — `applyLimits`: batch-level `MaxSpansPerBatch` reject; per-span attribute
  count/key/value caps, truncate-by-default with `OversizeAttr: reject` as the per-tenant strict opt-in (§6).
- `queue.go` — `sinkQueue`: bounded batches+bytes (DR-28), `shed` / `block_up_to_timeout` via
  `model.Clock` (no `time.After`).
- `otlpgrpc.go` — `otlpGRPCReceiver`: real `grpc.Server` implementing `TraceService.Export`, wired to `Server.Ingest`.
- `errors.go` — `IngestError` with `GRPCStatus()`/`HTTPStatus()` (INVALID_ARGUMENT/401/429 mapping).
- `metrics.go` — in-process counters matching FR-F01-9's label shape (see Deferred).
- `ingest_test.go` — 22 top-level table-driven tests (27 incl. subtests).

## Tests: 22/22 top-level (27/27 incl. subtests) PASS

Covers: OTLP field/ID/attribute-type-preservation round-trip (incl. nested array/map AnyValue),
malformed-ID and wrong-raw-type rejection, batch/attribute limit enforcement (truncate vs.
per-tenant reject), Zipkin v2 JSON conversion + malformed rejection, B3→traceparent single/multi/
64-bit-pad cases, Server.Ingest tenant stamping + FromDevDefault + fan-out to multiple sinks,
gRPC status-code mapping (InvalidArgument/Unauthenticated/ResourceExhausted), and queue overflow
(`shed` immediate, `block_up_to_timeout` via a fake clock/timer).

## Build/vet: green

`go build ./...`, `go vet ./...`, `gofmt -l internal/ingest/` all clean.
`go test ./...`: only pre-existing, unrelated failure is
`internal/store/tiered.TestStore_AppendAndGetTrace_RoundTrip` (Windows temp-dir file-lock on
cleanup) — outside SCOPE LOCK, not touched. `internal/archtest` passes, confirming no direct
`time.Now`/`time.After` calls (DR-31 compliance).

## Deferred / documented simplifications

- **Auth/principal extraction** (`otlpgrpc.go:subjectFromContext`): only the `auth.mode: none` dev-default
  branch is implemented; token/mTLS-SAN extraction belongs to X-SEC's (unbuilt) auth package. Every
  other mode fails closed (empty subject → resolver rejects UNAUTHENTICATED).
- **TLS** on the gRPC listener: not wired (§6 requires TLS 1.3 minimum).
- **Metrics**: in-process counters with FR-F01-9's label shape, not real Prometheus exposition —
  `New()`'s signature is fixed by F01 §4.3 with no `selfobs.Recorder` parameter, so wiring one is a
  pure addition later, not a rewrite.
- **OTLP HTTP, Jaeger receivers**: `ProtocolOTLPHTTP` normalizer is registered (reuses `OTLPNormalizer`)
  but no HTTP listener/handler exists; Jaeger Thrift/proto conversion is not implemented (out of
  30-minute budget — Zipkin was chosen as the representative non-OTLP conversion path since it's the
  simpler of the two migration protocols).
- **`Span.SizeBytes` per-span** and full `MaxSpanBytes`/`MaxRequestBytes` enforcement: batch-level
  `SizeBytes` is set (`proto.Size`/`len(body)`), but per-span byte accounting is not implemented.
- Full integration tests (real otel-go SDK / real Jaeger-Zipkin clients, load tests) from F01 §7 are
  out of scope for this unit-test pass.
