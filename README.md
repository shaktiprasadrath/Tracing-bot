# TraceIQ

TraceIQ is a multi-tenant tracing and remediation bot: it ingests OTLP
telemetry, samples and stores traces, builds a live service topology,
detects anomalies, correlates signals, runs LLM-driven root-cause
investigations with a bounded tool set and budget, proposes and executes
guarded Kubernetes remediation through a pinned `kubectl` (no client-go),
and answers natural-language questions — all behind one identity/RBAC/audit
surface. The system's binding design lives entirely in
`docs/architecture/06-decision-register.md` (the "decision register"):
every exported Go type and interface in this repository is copied from it
verbatim, per package, rather than invented independently — `00`/`01`/`02`
and the `F0x`/`X-*` feature docs describe intent and rationale, but the
register is the sole owner of Go shapes (DR-0) and of the package import
graph (DR-2), the latter mechanically enforced by `internal/archtest`.

The full product case — what this fixes versus Jaeger, Zipkin, Grafana
Tempo, Datadog APM, and Dynatrace — is in `Tracing-Bot-PRD.md`.

## Status

Implemented, reviewed, and proven end-to-end against a real Istio mesh
(see `docs/reports/w18-live-test.md`): a real fault was injected into a
live cluster, and TraceIQ correctly ingested the resulting traces, kept
the error-bearing ones, detected the anomaly, and diagnosed the root cause
through its rules-based RCA engine. All four formal sign-offs
(`docs/signoffs/`) are on file.

Two things are not yet wired into the running binary, both deliberately:
`internal/api` (the REST/MCP/web-UI surface) waits on `internal/auth`
having a real implementation, and RCA's five tool backends
(`TraceStore`/`LogStore`/`MetricStore`/`TopologyStore`/`MemoryStore`) are
still no-op stubs in `cmd/traceiq`. Everything else in the pipeline —
ingest, sampling, storage, topology, anomaly detection, and rules-based
RCA, including the agent-to-sampler interest-predicate feedback loop — is
implemented, tested, and wired. See `docs/ledger.md` for the full build
history and `docs/CHECKPOINT.md` for exactly what's deferred and why.

## Layout

- `cmd/traceiq/` — the composition root binary (`--version`, `--config`).
- `internal/model/` — every shared Go type (DR-4); stdlib-only, no other
  internal imports.
- `internal/config/`, `internal/tenant/`, `internal/selfobs/`,
  `internal/bus/`, `internal/cluster/`, `internal/llm/`, `internal/k8s/`,
  `internal/auth/` — cross-cutting infrastructure packages.
- `internal/topology/`, `internal/store/` (+ `sqlite`, `parquet`,
  `tiered` drivers), `internal/ingest/`, `internal/sampler/`,
  `internal/anomaly/`, `internal/correlate/`, `internal/memory/`,
  `internal/remediate/`, `internal/rca/` (+ `rules`), `internal/nl/`,
  `internal/eval/` — feature packages, one per `F0x` capability.
- `internal/api/` — the HTTP/MCP surface; may import every feature package
  (DR-2). Implemented and reviewed as a package; not yet wired into
  `cmd/traceiq` (see Status above).
- `web/` — the embedded static SPA `internal/api` serves.
- `internal/archtest/` — `go/parser`-based CI gate that fails the build on
  any import edge not in DR-2's adjacency table, or any non-stdlib import
  in `internal/model`.
- `Dockerfile` — multi-stage build, `gcr.io/distroless/static-debian12:nonroot`
  runtime (DR-13 of the tool-selection ADR); no shell, no package manager
  in the final image.
- `docs/architecture/` — the binding specification. `docs/reports/` — a
  report per implementation/review wave. `docs/signoffs/` — the four
  formal sign-offs. `docs/reviews/` — the architecture review trail.

## Build and test

The toolchain is pinned (DR-1): run every Go command with

```sh
GOTOOLCHAIN=go1.27.1 CGO_ENABLED=0 go build ./...
GOTOOLCHAIN=go1.27.1 CGO_ENABLED=0 go vet ./...
GOTOOLCHAIN=go1.27.1 CGO_ENABLED=0 go test ./...
```

or use the wrapped targets: `make build`, `make vet`, `make test`, and
`make ci` (POSIX) / `scripts/ci.ps1` (Windows PowerShell) for the full
gate — `gofmt` check, `vet`, `build`, `test`, a conditional `-race` pass
(skipped with a warning when no C compiler is on `PATH`, since `-race`
needs `CGO_ENABLED=1`), `govulncheck`, and `staticcheck`.

## Running it locally

```sh
GOTOOLCHAIN=go1.27.1 CGO_ENABLED=0 go build -o traceiq ./cmd/traceiq
./traceiq --config traceiq.yaml
```

A minimal dev config needs `server.profile: dev`, `auth.mode: none`, and
`ingest.otlp_grpc.tls.enabled: false` (TLS is on by default — this is the
one deliberate dev-only override). The process serves `/healthz`,
`/readyz`, and Prometheus metrics on `selfobs.metrics_endpoint`, and
accepts OTLP gRPC traces on `ingest.otlp_grpc.endpoint` (default
`127.0.0.1:4317`). There is no query UI yet (see Status); inspect ingested
data directly via the SQLite hot index under `server.data_dir`, or run the
test suite for the strongest available proof that the pipeline works.

## Documentation

The binding architecture is `docs/architecture/06-decision-register.md`
(the decision register), read alongside `docs/architecture/01`–`05` and
the `F0x`/`X-*` feature docs it amends. Every implementation and review
wave has a report in `docs/reports/`; `docs/ledger.md` is the full
chronological build log; `docs/wave-tracker.md` summarizes time, cost, and
agents per wave.
