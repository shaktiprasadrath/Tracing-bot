# W2 scaffold report B — anomaly / rca / correlate / memory / llm

Populated exported interfaces/structs/consts (types only, no business logic)
in `internal/anomaly`, `internal/rca`, `internal/correlate`, `internal/memory`,
`internal/llm`, per DR-14 through DR-21, DR-31, DR-34, DR-35 (read-only for
DR-21/DR-35 — out of this session's package scope). `gofmt`, `go build ./...`,
`go vet ./...` and `go test ./...` all pass at the end of this session.

## Per package

- **`internal/anomaly/anomaly.go`** (new) — DR-14 verbatim: `Kind`,
  `Detector`, `EvalInput`, `TopologyReader`, `BaselineReader`, `Baseline`,
  `BaselineStore`, `QuantileEstimator`, `Grouper`, `GrouperStats`,
  `DeployIndex` (§14.6), `EdgeMetaReader` (§14.7). Also `P2Estimator`,
  `TDigest`, `SeasonalBaseline` (named in DR-14 §14.1 prose as the two
  `QuantileEstimator` implementations plus the seasonal container, but never
  given a field list) as minimal TODO-flagged structs.
- **`internal/rca/rca.go`** (new) — DR-15 verbatim: `Engine`, `Reasoner`,
  `Proposal`, `Tool`, `ToolRegistry`, `Journal`, `SchemaValidator`,
  `Sanitizer`, `Budget`, `TopologyReader`; DR-16 verbatim: `ToolArgs`,
  `TraceQueryArgs`, `AttrEqual`, `LogQueryArgs`, `MetricQueryArgs`,
  `REDQuery`, `TopologyQueryArgs`, `MemoryQueryArgs`, plus the closed
  5-tool-set constants `ToolTraceQuery`/`ToolLogQuery`/`ToolMetricQuery`/
  `ToolTopologyQuery`/`ToolMemoryQuery` (DR-16 §16.1), aliased to
  `model.ToolName`'s existing canonical constants rather than re-declaring
  the literals. `ReplayMode`/`ReplayRecorded`/`ReplayLiveDiff` alias
  `model`'s DR-18 values. `LLMReasoner` (doc.go's promised `llm.Client`
  holder) added minimally.
- **`internal/correlate/correlate.go`** (new) — DR-20 §20.1 verbatim:
  `LogAdapter`, `MetricAdapter`, `Correlator`, `LogCapabilities`.
- **`internal/memory/memory.go`** (new) — DR-19 verbatim: `Fingerprint` +
  `Compute` signature (§19.1), `SimilarityScorer`, `Embedder`, `Store`,
  `RunbookImporter` (§19.3), `ExportFormat` closed enum (§19.6:
  `json`/`markdown`). `EmbedderLLM` (doc.go's promised `llm.Client` holder)
  added minimally.
- **`internal/llm/llm.go`** — pre-existing from the prior session (DR-34
  §34.1 reconstructed from prose, since the register gives no explicit
  `package llm` Go block); verified it already matches DR-34 §34.1/§34.3
  exactly and needed no changes.

## Under-specified helper types (flagged `// TODO(DR-n): under-specified`)

Referenced by an interface signature in the register but never given a field
list there: `anomaly.CheckpointReport`, `anomaly.BaselineStats`,
`correlate.MetricCapabilities`, `correlate.Stats`, `memory.Query`,
`memory.Candidate`, `memory.Scored`, `memory.ImportReport`,
`memory.ConsolidateReport`, `memory.Stats`, `rca.State`, `rca.ArgSchema`,
`rca.ToolCost`, `rca.Stats`, `rca.Charge` (this one has a documented shape —
DR-17 §17.5's doc-changes row spells out `Charge(tokensIn, cachedIn,
tokensOut, cost)`).

## Scope-lock conflicts found and how they were resolved

Three verbatim types the register places in packages **outside** this
session's SCOPE LOCK (`internal/{anomaly,rca,correlate,memory,llm}` only —
`internal/model`, `internal/store`, `internal/topology` untouched):

- **`model.ToolResult`** — DR-15's `Tool.Invoke`/`ToolRegistry.Dispatch`,
  DR-16 §16.3, and DR-37 all reference `model.ToolResult`, but unlike DR-4's
  other model DTOs it is never given a field list anywhere in the register,
  and `internal/model` is out of scope this session. Declared locally as
  `rca.ToolResult` instead, flagged `TODO(DR-15/DR-16)` to move into
  `internal/model` once that package is back in scope.
- **`topology.Edge`** (DR-13, used by `anomaly.EdgeMetaReader`) and
  **`topology.Direction`** (DR-13, used by `rca.TopologyQueryArgs.Direction`)
  — `internal/topology` has `doc.go` only. Stood in with minimal local
  placeholders (`anomaly.Edge`, `rca.Direction`), each flagged
  `TODO(DR-13): replace ... once internal/topology is populated`.
- **`store.ErrorSignature`** (DR-6, used by `anomaly.EvalInput.ErrorSigs`) —
  `internal/store` has `doc.go` only. Stood in with a minimal local
  `anomaly.ErrorSignature`, flagged `TODO(DR-6)`.

None of these three packages needed to import `topology` or `store` as a
result — the placeholders keep every file inside DR-2's adjacency ceiling
(a subset of the allowed-imports table is always legal; only exceeding it
fails `archtest`).

One design-level tension noted rather than silently resolved: DR-19 §19.1
gives `memory.Compute`'s signature directly in `package memory`, but DR-14
§14.5 says the fingerprint construction is "identical to
`memory.Fingerprint.Compute` — one implementation, in `anomaly`, called by
`memory`" — which would require `memory` to import `anomaly`, not permitted
by DR-2's adjacency table (`internal/memory` may import `model, config,
tenant, store, llm` only). `memory.Compute` is left as a `panic("not
implemented")` stub with a comment pointing at this tension for the
implementing session to resolve.

## Verification

`gofmt -l` clean on all five touched files. `go build ./...`,
`go vet ./...`, `go test -count=1 ./...` all pass, including
`internal/archtest`'s `TestPackageAdjacency` (confirms `rca` does not import
`remediate` and `memory` does not import `rca`, per the task's explicit
gate). Note: mid-session, `go test ./...` transiently failed on
`store/sqlite`/`store/parquet`/`store/tiered` missing archtest adjacency
entries — traced to a concurrent session actively editing
`internal/store/*` (files appeared/disappeared between runs) and not caused
by, or fixable within, this session's scope; it had resolved itself
(directories empty again) by the final verification run above.

## Remaining

`internal/topology` and `internal/store` still have `doc.go` only (DR-13,
DR-6/7/8/9 — someone else's session appears to be actively working on
`store/*`). Once populated, `rca.ToolResult`/`Direction` and
`anomaly.Edge`/`ErrorSignature` placeholders should be replaced by the real
`model.ToolResult`/`topology.Direction`/`topology.Edge`/`store.ErrorSignature`
types and the local stand-ins deleted. `internal/sampler` (DR-10/11/12,
referenced by `rca`'s allowed-import row but not otherwise touched here) and
`internal/nl`/`internal/eval` remain out of this session's task scope.
