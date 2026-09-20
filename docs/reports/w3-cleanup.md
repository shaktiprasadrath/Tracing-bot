# W3 cleanup report — archtest, store doc.go, dedupe, Resolution, ToolArgs, api.Deps

Scope: the six numbered fixes below, in order, each built/vetted/tested green
before the next started. No changes to go.mod. No changes under docs/ other
than this report.

## 1. archtest Windows path-separator bug

**Finding:** `internal/archtest/archtest_test.go`'s `path2pkg` already
wrapped `filepath.Dir(relPath)` in `filepath.ToSlash(...)` before this
session started — i.e. the fix `docs/reports/w2-scaffold-a.md` flagged
(`filepath.Dir` on Windows re-emits `\` even when fed an already-`/`-joined
path, desyncing a two-level package key like `store/sqlite` from the
adjacency table's `"store/sqlite"` entry) was already present in the
checked-out code. Verified empirically with a throwaway program:
`filepath.Dir("store/sqlite/doc.go")` → `"store\\sqlite"`;
`filepath.ToSlash(filepath.Dir(...))` → `"store/sqlite"`. Confirmed no other
`filepath.Dir` call in the file was left unwrapped (`TestCmdImports` wraps
its own `filepath.Dir(rel)` the same way). No source change was needed for
the normalization itself.

**What was actually missing, and added:** a coverage guard, per the fix
list's explicit ask ("confirm the test actually exercises every internal
package ... so a silently-empty scan can't pass"). `TestPackageAdjacency`
now tracks every package key its main walk visits (`checked map[string]int`)
and runs a `ScopeCoverage` subtest that (a) fails outright if zero packages
were visited, (b) logs the count and the list, and (c) cross-checks that set
against an independently-computed one — built with plain `"/"`-splitting
rather than `path2pkg`/`filepath.Dir`, so a regression in the exact
normalization this test guards can't also blind the guard itself.

**DR:** DR-2 (adjacency table, and "`internal/archtest` asserts it exactly").

**Verification:**
```
GOTOOLCHAIN=go1.27.1 CGO_ENABLED=0 go build ./...   # PASS
GOTOOLCHAIN=go1.27.1 CGO_ENABLED=0 go vet ./...     # PASS
GOTOOLCHAIN=go1.27.1 CGO_ENABLED=0 go test ./...    # PASS
```
(At this point `store/sqlite`, `store/parquet`, `store/tiered` were still
empty, so `ScopeCoverage` reported 22 packages / 53 files — the two-level
case wasn't exercised yet. Re-verified after fix 2, below.)

## 2. store subpackage doc.go

Added `internal/store/sqlite/doc.go`, `internal/store/parquet/doc.go`,
`internal/store/tiered/doc.go` — each states purpose, binding DR, feature
IDs and allowed imports, matching the style of the existing `internal/store/
doc.go` and `internal/anomaly/doc.go`:

- `store/sqlite` — DR-6's `HotIndex` driver (two SQLite files, two writer
  goroutines) plus DR-13's `topology.EdgeSink`/`EdgeSource` (F04 persists/
  warm-starts through it) and DR-10/DR-2's `sampler.BaselineSource`. Feature
  IDs F01, F03, F04.
- `store/parquet` — DR-7's `ColdStore` driver (per-trace rows into
  time-bucketed blocks, WAL, tombstone erasure, compaction). Feature ID F03.
- `store/tiered` — composes the sqlite/parquet drivers into
  `store.TieredStore` (DR-6 §6.2's two-file routing, DR-7's seal/bind
  ordering, DR-12's `CostSignal` emission). Feature IDs F01, F03.

Allowed imports listed as `model, config, tenant, topology, store`, matching
`internal/archtest`'s adjacency map (which documents, as a register
ambiguity carried over from `docs/reports/w1-scaffold.md`, that each store
driver subpackage may additionally import its own parent `store` package —
otherwise a driver could never return `store`'s own exported row/interface
types).

**DR:** DR-2 (adjacency), DR-6, DR-7, DR-12, DR-13.

**Verification:** with the three `doc.go` files in place, `TestPackageAdjacency`'s
new `ScopeCoverage` subtest reports 25 packages / 56 files, explicitly
including `store/parquet`, `store/sqlite`, `store/tiered` — confirming fix 1's
normalization now correctly matches these two-level package keys against the
adjacency table.
```
go build ./...   # PASS
go vet ./...     # PASS
go test ./...    # PASS
```

## 3. Deduplicated placeholder types

Three scope-lock placeholders from `docs/reports/w2-scaffold-b.md` deleted,
replaced by imports of the now-populated owning packages:

- **`anomaly.ErrorSignature`** deleted; `EvalInput.ErrorSigs` now
  `[]store.ErrorSignature` (`internal/store` is populated; DR-2 permits
  `anomaly → store`).
- **`anomaly.Edge`** deleted; `EdgeMetaReader.NewEdgesSince` now returns
  `[]topology.Edge` (`internal/topology` is populated; DR-2 permits
  `anomaly → topology`).
- **`rca.Direction`** deleted; `TopologyQueryArgs.Direction` now
  `topology.Direction` (DR-2 permits `rca → topology`).
- **`rca.ToolResult`** deleted. Every register reference to it is qualified
  `model.ToolResult` (DR-15's `Tool.Invoke`/`ToolRegistry.Dispatch`, DR-16
  §16.3's `ToolResult.Clamped`, DR-37's "only `model.ToolResult` projections
  reach a prompt") — never `rca.ToolResult` — so per the fix list's fallback
  rule it belongs in `internal/model`, not `rca`. Added `model.ToolResult`
  to `internal/model/rca.go` (same file that already holds DR-15's other
  type additions) with the same field set the old placeholder had
  (`Rows []byte`, `Clamped`, `Truncated`, `FromCache bool` — the register
  gives no field list anywhere, so this remains a `TODO(DR-15/DR-16):
  under-specified` reconstruction, just relocated to its correct package).
  `rca.Tool.Invoke` and `rca.ToolRegistry.Dispatch` now return
  `model.ToolResult`.

No external package referenced any of the four deleted placeholders (grepped
`rca.ToolResult`, `rca.Direction`, `anomaly.Edge`, `anomaly.ErrorSignature`
repo-wide before deleting).

**DR:** DR-2 (adjacency, now that store/topology are populated), DR-4 (model
owns cross-package DTOs), DR-13 (`topology.Edge`/`topology.Direction`), DR-15
(`Tool.Invoke`, `ToolRegistry.Dispatch`), DR-16 (`ToolResult.Clamped`,
`TopologyQueryArgs.Direction`).

**Verification:**
```
go build ./...   # PASS
go vet ./...     # PASS
go test ./...    # PASS
```

## 4. `Resolution` enum triplication

`model.Resolution`, `topology.Resolution` and `store.Resolution` were three
independent `{Res10s=1, Res5m=2, Res1h=3}` copies. Kept `model.Resolution`
as the sole definition; deleted the `topology` and `store` copies.

**Owner rationale:** DR-39 ("One RED record, one quantile set") is the
decision that actually resolves this exact triplication class — it prints
`package model` explicitly for `Resolution`, and its entire purpose is
collapsing per-package RED-shaped duplicates (`sampler.REDSample`,
`anomaly.REDSample`, `store.SpanRollup`, `store.REDResult`,
`store.REDBucket`, `topology.EdgeRED` all deleted in favor of
`model.REDSample`). `topology.Edge.Resolution` and DR-6's
`HotIndex.CascadeRED(..., from, to Resolution, ...)` both read as bare,
unqualified `Resolution` inside their own `package X` blocks — which is
exactly the ambiguity `w2-scaffold-a.md` had read as license for each
package to keep a local copy — but DR-39 is later in the register, is
explicitly the "one RED record" decision, and both `topology` and `store`
already import `model`, so there is no adjacency reason to keep a local
copy once DR-39 is read as the tie-breaker.

- `internal/topology/topology.go`: deleted local `Resolution` type + consts;
  `Edge.Resolution` field retyped `model.Resolution`.
- `internal/store/store.go`: deleted local `Resolution` type + consts;
  `HotIndex.CascadeRED`'s `from, to` parameters retyped `model.Resolution`.
- `internal/model/red.go`: updated the doc comment on `model.Resolution` to
  record the resolution (no code change to the type itself).

**DR:** DR-39 (owner), DR-4 (model as the shared-DTO package), DR-13, DR-6
(the two call sites).

**Verification:**
```
go build ./...   # PASS
go vet ./...     # PASS
go test ./...    # PASS
```

## 5. `nl.ToolArgs` → `rca.ToolArgs`

`internal/nl` had declared `type ToolArgs = map[string]any` as a scope-lock
stand-in (`docs/reports/w2-scaffold-c.md`), pending `internal/rca` being
populated. `internal/rca` is now populated and DR-2 permits `nl → rca`.
Deleted the alias; `nl.Intent.Args` is now `rca.ToolArgs` directly, matching
DR-35 §35.2's statement that `Intent.Args` is "the SAME typed args the RCA
loop uses" (DR-16) — the point being that an NL answer and an RCA step over
the same window produce evidence with identical `Query`/`ToolResultHash`
(D-X3, `AC-F10-10`), which a `map[string]any` copy could never guarantee.
No other file referenced `nl.ToolArgs`.

**DR:** DR-35 §35.2, DR-16 (canonical `ToolArgs` shape), DR-2 (`nl → rca`
adjacency).

**Verification:**
```
go build ./...   # PASS
go vet ./...     # PASS
go test ./...    # PASS
```

## 6. `api.Deps` completion

The register never prints an explicit `type Deps struct` block for DR-30's
"`api.Server` interface + `api.HTTPServer` impl + `api.Deps` wiring,
matching `02 §4`'s shape" — `02 §4` (the class diagram doc) is outside this
pass's binding-read scope (DR-0..DR-39 only), so `Deps`'s exact field list is
necessarily a reconstruction, same as the pre-existing fields already in the
struct. Filled it out from what DR-29 §29.1's route table, DR-29 §29.2's MCP
backing-call table and DR-30's per-screen endpoint list actually require
`api.HTTPServer` to hold, one field per interface, following the same
pattern the existing `Auth*`/`Tenants`/`Policies` fields already use:

| New field | Type | Backed by (register citation) |
|---|---|---|
| `Investigations` | `rca.Engine` | `traceiq_get_investigation`/`traceiq_start_investigation` (DR-29 §29.2); `/v1/investigations/{id}/replay`, `/v1/investigations/{id}/steps/{stepID}/correct` (DR-29 §29.1) |
| `Memory` | `memory.Store` | `traceiq_search_memory` (DR-29 §29.2); screen 9 (DR-30) |
| `Correlate` | `correlate.Correlator` | `traceiq_correlate_logs`/`traceiq_correlate_metrics` (DR-29 §29.2) |
| `Anomaly` | `anomaly.Grouper` | `traceiq_list_incidents` (DR-29 §29.2); incidents/anomalies panel, screen 1 (DR-30) |
| `Baselines` | `anomaly.BaselineReader` | `GET /v1/baselines` (DR-29 §29.1, already in this file's `Routes`) |
| `Deploys` | `anomaly.DeployIndex` | `GET /v1/deploys` (DR-29 §29.1, already in `Routes`) |
| `Sampler` | `sampler.Sampler` | `GET/DELETE /v1/sampler/interest`, `GET /v1/sampler/stats` (DR-29 §29.1, already in `Routes`); Sampler panel, screen 8 (DR-30) |
| `Store` | `store.HotIndex` | `traceiq_search_traces`/`traceiq_get_trace`/`traceiq_query_red` (DR-29 §29.2); `GET /v1/store/budget` (DR-29 §29.1) |
| `Cold` | `store.ColdStore` | `GET /v1/tenants/{id}/erasure` (DR-29 §29.1, already in `Routes`; DR-7's tombstone SLA) |

**`internal/ingest` deliberately NOT added**, departing from the prior
session's TODO comment (`w2-scaffold-c.md`: "add Investigations/Memory/
Correlate/Anomaly/Ingest/Store"). Checked `internal/ingest/ingest.go`:
its only exported top-level interface, `Receiver`, is a per-protocol
process-lifecycle handle (`Start`/`Stop`/`Protocol`), not a query surface;
DR-29's route table and MCP tool table name no ingest-backed endpoint or
tool. Wiring it into `Deps` would be an unjustified field with no backing
citation, so it was left out and flagged here instead of silently carried
forward.

Types/fields only — no method bodies changed, `HTTPServer.Start/Shutdown/
Addr` are untouched `panic("not implemented")` stubs.

**DR:** DR-29 (§29.1 route table, §29.2 MCP tool table), DR-30 (screen table,
the `api.Deps` naming decision itself), DR-2 (`api` may import every feature
package, which is what makes each field a direct interface rather than a
further indirection).

**Verification:**
```
go build ./...   # PASS
go vet ./...     # PASS
go test ./...    # PASS
```

## Final verification (whole module)

```
GOTOOLCHAIN=go1.27.1 CGO_ENABLED=0 go build ./...   # PASS
GOTOOLCHAIN=go1.27.1 CGO_ENABLED=0 go vet ./...     # PASS
GOTOOLCHAIN=go1.27.1 CGO_ENABLED=0 go test ./...    # PASS (internal/archtest: TestPackageAdjacency + ScopeCoverage + TestCmdImports)
gofmt -l .                                          # clean
```

`internal/archtest`'s `ScopeCoverage` subtest now reports 25 internal
packages / 56 non-test `.go` files scanned, up from 22/53 before fix 2,
confirming both the doc.go additions and the path-normalization are working
together correctly.

## Register ambiguities hit

1. **`api.Deps`'s exact shape is not printed anywhere in DR-0..DR-39.**
   DR-30 points at "`02 §4`'s shape", which is outside this pass's binding
   read set. Reconstructed field-by-field from DR-29's route/tool tables and
   DR-30's screen table instead of guessing at `02 §4`'s content; see the
   citation table in fix 6 above. A future pass with `02 §4` in scope should
   confirm field names/types against it.
2. **`model.ToolResult`'s field list is never printed anywhere in the
   register** (DR-15, DR-16, DR-37 all reference it by name only). Carried
   forward as `TODO(DR-15/DR-16): under-specified` on the relocated type
   rather than invented wholesale.
3. **The `Resolution` triplication's "correct" owner is a judgment call
   between DR-4/DR-13/DR-39**, none of which explicitly says "delete the
   other two". DR-39's title and stated purpose ("One RED record, one
   quantile set", collapsing exactly this class of duplicate) was read as
   the tie-breaker over DR-13's/DR-6's bare unqualified `Resolution`
   mentions. Flagged here in case a future pass reading `01 §5.1`/`01 §7` in
   full finds a more explicit statement.
