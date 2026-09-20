# TraceIQ

TraceIQ is a multi-tenant tracing and remediation bot: it ingests OTLP
telemetry, samples and stores traces, builds a live service topology,
detects anomalies, correlates signals, runs LLM-driven root-cause
investigations with a bounded tool set and budget, proposes and executes
guarded Kubernetes remediation through a pinned `kubectl` (no client-go),
and answers natural-language questions — all behind one identity/RBAC/audit
surface. The system's binding design lives entirely in
`docs/architecture/06-decision-register.md` (the "decision register"):
every exported Go type and interface in this repository is meant to be
copied from it verbatim, per package, rather than invented independently —
`00`/`01`/`02` and the `F0x`/`X-*` feature docs describe intent and
rationale, but the register is the sole owner of Go shapes (DR-0) and of
the package import graph (DR-2), the latter mechanically enforced by
`internal/archtest`.

This repository is currently a **compiling skeleton**: every package below
holds its canonical types and interfaces with `panic("not implemented")`
bodies, not business logic. It builds, vets and passes `internal/archtest`
today; filling in behavior, wiring the composition root in
`cmd/traceiq/main.go`, and adding real tests are tracked as follow-on work
(see `docs/reports/w1-scaffold.md` for exactly what is and is not done).

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
  `internal/remediate/`, `internal/rca/`, `internal/nl/`, `internal/eval/`
  — feature packages, one per `F0x` capability.
  Several of these still need their interfaces populated from the
  register; see `docs/reports/w1-scaffold.md`.
- `internal/api/` — the one HTTP/MCP surface; may import every feature
  package (DR-2).
  Not yet populated.
  `internal/archtest/` — `go/parser`-based CI gate that fails the build on
  any import edge not in DR-2's adjacency table, or any non-stdlib import
  in `internal/model`.
- `internal/depstub/` — pins the Set B dependency graph (DR-1) until real
  packages import those modules; delete once they do.
- `docs/architecture/` — the binding specification (read-only for this
  scaffold's scope). `docs/reports/` — scaffold progress reports.

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

## Documentation

The binding architecture is `docs/architecture/06-decision-register.md`
(the decision register), read alongside `docs/architecture/01`–`05` and
the `F0x`/`X-*` feature docs it amends. Scaffold-specific progress notes,
ambiguities resolved at the code level, and remaining work are recorded in
`docs/reports/`.
