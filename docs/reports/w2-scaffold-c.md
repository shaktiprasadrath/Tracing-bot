# W2 scaffold report C — remediate / nl / eval / api

Scope: `internal/{remediate,nl,eval,api,config,selfobs,cluster}`. `config`,
`selfobs` and `cluster` were already fully populated (by an earlier/parallel
session) and needed no changes — verified against 01 §7 and their existing
doc comments, left untouched.

## New files

- **`internal/remediate/remediate.go`** — DR-23 §23.1 verbatim: `Guard`
  (9-method interface), `ApprovalRequest`, `RecoverySignal` (declared here
  so `remediate` never imports `anomaly`, per DR-2's cycle-break table),
  `Verifier`. The closed action enum and 9-state machine (incl.
  `ActionExpired`) already live in `internal/model` (`model.ActionType`,
  `model.ActionState`) and `internal/k8s` (`k8s.Executor`) from a prior
  session — referenced, not redefined, per DR-4. Added `ActionFilter` and
  `Stats` (Guard.List/Stats return shapes) as minimal reconstructions —
  the register names them but never prints their fields; flagged
  `// TODO(DR-23)`.
- **`internal/nl/nl.go`** — DR-35 §35.1/§35.2 verbatim: `IntentKind` (closed
  11-value enum), `Interpreter`, `Answerer`, `Question`, `ConversationKey`,
  `ConversationContext`, `Intent`. `Intent.Args` is spec'd as `rca.ToolArgs`,
  but `internal/rca` is out of this task's scope lock and was unpopulated
  when this file was written; kept as a local `nl.ToolArgs` alias with a
  `// TODO(DR-16)` to swap once `rca.ToolArgs` exists, rather than import a
  package that might not compile. `ChatContext`, `Turn`, `Focus`, `Answer`
  are named by the register but not field-defined — minimal
  reconstructions, each flagged `// TODO(DR-35)`.
- **`internal/eval/eval.go`** — DR-36 §36.1/§36.4 + DR-31 verbatim: `Mode`,
  `Isolation`, `Runner`, `RunOptions`, `ScenarioSpec`, `Expectation`
  (`EvidenceCategory` referenced as `model.EvidenceCategory`, not
  redefined — DR-36's own header note says it's declared once, in `model`).
  `VirtualClock` struct with DR-31's three method signatures
  (`Advance`/`AwaitQuiescence`/`Register`), stubbed `panic("not
  implemented")` — no business logic. `FixtureRef`, `ClockSpec`,
  `FaultSpec`, `Result`, `Report`, `Stats` are named by the register without
  full field lists — minimal reconstructions, each flagged `// TODO(DR-36)`.
- **`internal/api/api.go`** — DR-29 §29.2 verbatim: `MCPTool` struct
  (`MinRole` is `auth.Role`, not redefined) and the closed **12-tool**
  `MCPTools` array (13 minus `traceiq_propose_action`, per the register's
  explicit exclusion). DR-29 §29.1's "rows added to 01 §6.1" reproduced as
  `Route`/`Routes` (the delta table only — the full endpoint table lives in
  01 §6.1, out of this task's read allowlist). DR-30's `Server` interface +
  `HTTPServer` impl + `Deps` wiring (the CC-24 rename): `Deps` wires the
  interfaces of every already-populated feature package
  (`auth`, `tenant`, `topology`, `remediate`, `nl`, `eval`, `k8s`,
  `cluster`, `selfobs`, `config`); `rca`/`memory`/`correlate`/`anomaly`/
  `ingest`/`store` handler deps are deferred with a `// TODO(DR-29/DR-30)`
  since those packages' exported surfaces were still moving under a
  concurrent session while this file was written.

## Build/vet/test

```
GOTOOLCHAIN=go1.27.1 CGO_ENABLED=0 go build ./...   # PASS
GOTOOLCHAIN=go1.27.1 CGO_ENABLED=0 go vet ./...     # PASS
GOTOOLCHAIN=go1.27.1 CGO_ENABLED=0 go test ./...    # FAIL — internal/archtest only
gofmt -l .                                          # clean
```

`go test ./...` fails in `internal/archtest.TestPackageAdjacency`:
`store\parquet`, `store\sqlite`, `store\tiered` have no DR-2 adjacency-table
entry. This is pre-existing/concurrent — `internal/archtest` and
`internal/store/*` are outside this task's scope lock, and their `doc.go`
files were added by another session while this one ran. Every package this
session touched builds, vets and gofmts clean in isolation and repo-wide.

## Remaining

- `internal/archtest`'s adjacency table needs `store/parquet`,
  `store/sqlite`, `store/tiered` rows (out of scope here).
- `nl.ToolArgs`, `eval`'s several reconstructed structs, and
  `remediate.ActionFilter`/`Stats` are flagged `TODO` pending fuller reads
  of `rca`/DR sections not in this task's allowlist.
- `api.Deps` needs `rca`/`memory`/`correlate`/`anomaly`/`ingest`/`store`
  fields once those packages' interfaces settle.
- `api.Routes` covers only DR-29 §29.1's delta rows, not the full 01 §6.1
  table (not in this task's read allowlist).
