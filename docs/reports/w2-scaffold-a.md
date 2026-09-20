# W2 scaffold report A — store / ingest / sampler / topology / bus

Scope: `internal/{store,ingest,sampler,topology,bus}` (+ `store/sqlite`,
`store/parquet`, `store/tiered` doc-only, deferred — see Ambiguities).
`internal/bus` was already fully populated (`bus.go`: `Message`, `Bus`) by
an earlier/parallel session against DR-32 §32.2/§32.3 — verified against
this session's DR-32 read (drivers table, "none" default in every mode)
and left untouched, no changes needed.

## New files

- **`internal/topology/topology.go`** — DR-13, verbatim: `Resolution`
  (package-local, matching the `internal/model/red.go` precedent of each
  DR-13/DR-39 block keeping its own copy rather than cross-importing),
  `Edge`, `Direction`, `Graph` (the full 9-method interface F04 had
  dropped), `Neighborhood`, `EdgeSink`, `EdgeSource`. `EdgeOp` reconstructed
  1:1 from the `topology_edge_op` DDL columns DR-13 does print. `Snapshot`,
  `ChangeEvent`, `Stats`, `ExportFormat` are named by `Graph`'s method set
  but never given field lists/enums by the register — each is a minimal
  reconstruction flagged `// TODO(DR-13)`.
- **`internal/sampler/sampler.go`** — DR-8 + DR-10 + DR-11, verbatim.
  DR-8's `MemberID`, `RingState`, `Ring`, `ShardFor` (its Go block is
  itself `package sampler`, despite the DR's "Sharding" title — routing
  lives with the package that owns `Consume`). DR-10's `Sampler` (9-method
  interface), `Stats`, `ShardStats`, `PolicyEvaluator`, `BaselineSource`,
  `PredicateSet`, `InterestMatch`, `ServiceOp`, `KeyBaseline`,
  `BaselineSnapshot`. DR-11's `PredicateScope`, `InterestPredicate`
  (verbatim 15-field struct). Four types the register names only in a
  signature and never gives a field list/method set for: `Decision`
  (`PolicyEvaluator.Evaluate`'s return / `Sampler.Decisions()`'s element —
  reconstructed from prose: `Reason`, `MatchedPredicateID`,
  `SecondaryReasons uint16` bitmask), `Governor` (`Evaluate`'s rate-cap
  collaborator — reconstructed as a one-method `Allow(class
  model.KeepReason) bool`), `ReplayReport` (`Sampler.ReplayWAL`'s return),
  `TimerWheel` (DR-9's normative finalize mechanism, named by exported
  identifier with no printed struct). All four flagged
  `// TODO(DR-9/DR-10/DR-11)` inline.
- **`internal/ingest/ingest.go`** — `docs/architecture/features/F01-ingest.md`
  §4.3 only, verbatim: `Protocol` (4-value enum), `Receiver`, `SpanSink`,
  `Normalizer`, `New(cfg config.IngestConfig, clock model.Clock, resolver
  tenant.Resolver, sinks []SpanSink) (*Server, error)`. F01 §4.3 prints
  `New`'s first parameter as unqualified `Config`; resolved to
  `config.IngestConfig` (already fully populated in `internal/config` by
  an earlier session, owns every `ingest.*` key F01 §4.3 itself cites) per
  DR-2's adjacency table (`ingest -> config` is allowed) rather than
  declaring a second, competing `ingest.Config` type. `Server` (the
  constructor's return type) is named but never field-defined — minimal
  reconstruction, `// TODO(F01 §4.3)`.
- **`internal/store/store.go`** — DR-6 (hot index), DR-7 (cold store),
  DR-12 (cost control), all verbatim where the register prints a Go block:
  `HotIndex` (13-method interface), `HotBatch`, `BatchReceipt`,
  `HotCapabilities`, `ObjectStore` (renamed from `BlobStore`), `ColdTier`,
  `ColdStore` (9-method interface), `WALRef`, `TraceLoc`, `Watermark`,
  `CostSignal`, `RetentionPolicy`. `TieredStore` struct + `Signals()
  <-chan CostSignal` method added with a `panic("not implemented")` body
  (types-only per the task, but DR-12 prints this as a concrete method on a
  named receiver, not just an interface — matches the existing
  `model.NewRealClock` stub pattern already in the codebase). A
  package-local `HealthReport` (used unqualified by both `HotIndex.Health`
  and `ColdStore.Health`) follows the precedent `internal/model/health.go`
  already documents for this exact ambiguity (store keeps its own rather
  than importing `model.HealthReport`). A package-local `Resolution`
  (`HotIndex.CascadeRED`'s `from, to Resolution` parameter) — same
  three-way-duplication pattern as `model.Resolution`/`topology.Resolution`,
  DR-6's own block never defines it.

  Fourteen further types are referenced by `HotIndex`/`ColdStore`'s method
  signatures but never given field lists in the read range (`01 §5.1`'s
  full DDL is cited, not reproduced, for most of them): `TraceIndex`,
  `SpanIndex`, `FTSRow`, `PathSigRow`, `ResourceRow`, `ErrorSignature`,
  `Exemplar`, `PendingCold`, `TableID`, `Expired`, `DiskReport`,
  `SpanQuery`/`SpanPage`, `TraceQuery`/`TracePage`, `REDSeries`,
  `BlockManifest`, `ReplayReport` (store's own, `ColdStore.ReplayWAL`'s
  return — separate from `sampler.ReplayReport`, different package),
  `CompactBudget`, `CompactReport`, `ObjectInfo`. Each carries an inline
  `// TODO(DR-6)` / `// TODO(DR-7)` comment. Two of these, `AttrIndexRow`
  and `AttrDictRow`, are **not** TODO'd — DR-6 §6.2 prints their full DDL
  verbatim and the struct fields are a direct 1:1 transcription; likewise
  `EdgeOpRow` against `topology_edge_op`'s DDL.

## Remaining — `PARTIAL`

- **`store/sqlite`, `store/parquet`, `store/tiered`** — no `doc.go` added
  this session. See Ambiguities: adding a first `.go` file to any of these
  (previously-empty) directories trips a latent Windows-only bug in
  `internal/archtest`, which is out of this task's scope lock to fix.
  Flagged as a background task (`task_9bbc9740`) rather than fixed inline.
- Row/query types listed above as `TODO`'d are minimal reconstructions,
  not verified against `01 §5.1`'s full (unread) DDL. A future pass that
  reads `01 §5.1` in full should reconcile column names/types against the
  real schema before any driver package starts implementing `HotIndex`.
- `sampler.TimerWheel`, `sampler.Governor`, `sampler.Decision` have no
  method bodies or construction logic — DR-9's finalize mechanism and
  DR-10's shed-order enforcement remain business logic for a future pass,
  intentionally (types-only scope).
- `topology.ExportFormat`'s value set (json/dot/other) is unconfirmed —
  D-Y4 is referenced but not read this session.

## Register ambiguities found at the code level

- **`internal/archtest`'s `path2pkg` has a Windows path-separator bug**,
  exposed for the first time by this session's attempt to add `doc.go` to
  `internal/store/sqlite`/`parquet`/`tiered`. `TestPackageAdjacency`
  (`internal/archtest/archtest_test.go`) computes each file's package key
  via `filepath.ToSlash(filepath.Rel(...))` then `path2pkg` →
  `filepath.Dir(relPath)` — but `filepath.Dir` on Windows re-normalizes its
  *result* back to `\`, undoing the prior `ToSlash`. A one-level package
  (e.g. `"store/doc.go"` → key `"store"`) never manifests the bug (no
  separator in the key), but any two-level package (e.g.
  `"store/sqlite/doc.go"` → key `"store\\sqlite"`) fails to match the
  adjacency map's `"store/sqlite"` entry, which is already present and
  correct. This was latent because those three directories had zero `.go`
  files before this session (`filepath.Walk` never visited them). Verified
  directly: `filepath.Dir("store/sqlite/doc.go")` returns `"store\\sqlite"`
  on this Windows host. Fix is a one-line `filepath.ToSlash` added to
  `path2pkg`'s return — flagged as background task `task_9bbc9740` rather
  than fixed here, since `internal/archtest` is outside this task's scope
  lock (`internal/{store,ingest,sampler,topology,bus}/**` only). The three
  `doc.go` files were written, confirmed to trigger the failure, then
  removed again to leave `go test ./...` green per the task's hard
  requirement; the directories are still empty.
- **`store.Resolution` is a third independent copy** of the same
  `{Res10s=1, Res5m=2, Res1h=3}` enum, alongside `model.Resolution` (DR-39)
  and `topology.Resolution` (DR-13). DR-6's `HotIndex.CascadeRED` block
  uses a bare `Resolution` with no import and no local definition in the
  excerpt read this session — the register never states which package's
  copy it means. Resolved the same way the prior session resolved
  `model`/`topology`'s duplication (see `internal/model/red.go`'s note):
  each verbatim block that says `Resolution` unqualified inside its own
  `package X` gets its own type, since DR-2 forbids `store` designating
  which package "owns" a cross-cutting enum this deep into three already-
  separately-scaffolded packages. Should be reconciled (probably down to
  one canonical type, likely `model.Resolution`, with the others deleted)
  in a follow-up pass that reads `01 §5.1`/`01 §7` in full.
- **`ingest.New`'s `Config` parameter** is resolved to `config.IngestConfig`
  rather than a new `ingest.Config` type. F01 §4.3 prints the parameter
  unqualified, but `internal/config` (populated by an earlier/parallel
  session, confirmed present and complete before this file was written)
  already owns every config key F01 §4.3 itself cites
  (`ingest.otlp_grpc.endpoint`, `ingest.queue.*`, `ingest.limits.*`,
  `ingest.auth.*`) as `config.IngestConfig`'s fields. Declaring a second,
  competing `ingest.Config` would duplicate `01 §7` and immediately
  diverge from it. High confidence this is correct, but noted since F01
  §4.3's literal text does not spell out the package.
- **`sampler.Decision`, `sampler.Governor`, `sampler.ReplayReport`,
  `sampler.TimerWheel`** — four types DR-9/DR-10/DR-11 name only inside
  prose or a method signature, never as a printed `type ... struct`.
  Reconstructed minimally with inline `// TODO` comments per the task's
  instruction; a future pass should cross-check `Decision`'s exact field
  set against `AC-F02-16`'s assertions (`Reason`, `MatchedPredicateID`,
  `Hits`, `SecondaryReasons`) and `sampler_shed_total{class=...}`'s label
  values before treating it as final.
- **`store`'s fourteen under-specified row/query types** (see New files,
  last paragraph) are reconstructed from DR-6 §6.3's query table and the
  method signatures alone, not from `01 §5.1`'s full DDL (only the ALTER
  and two full `CREATE TABLE`s for `attr_index`/`attr_dict` were in the
  read range). Flagged individually inline; a future pass that reads
  `01 §5.1` in full should reconcile.

## Build/test status

```
GOTOOLCHAIN=go1.27.1 CGO_ENABLED=0 go build ./...   # PASS
GOTOOLCHAIN=go1.27.1 CGO_ENABLED=0 go vet ./...     # PASS
GOTOOLCHAIN=go1.27.1 CGO_ENABLED=0 go test ./...    # PASS (archtest included)
gofmt -l internal/store internal/ingest internal/sampler internal/topology internal/bus   # clean
```

Note: `internal/rca`, `internal/anomaly`, `internal/eval` were being
populated concurrently by parallel W2 sessions during this run (observed
mid-session file timestamps and a transient `model.ToolResult` undefined
error in `internal/rca` that resolved itself between this session's build
attempts) — outside this task's scope either way, mentioned only because
`go build ./...`/`go test ./...` are whole-module commands and their
green status briefly depended on that other session's progress, not this
one's.
