# w17 — Final whole-system review before integration testing

**Scope:** TraceIQ as an assembled unit, not package-by-package. Per-package correctness was
reviewed in waves 9–16 (16 prior reports); this pass looked only for defects that live *between*
packages — contract mismatches where each side was individually reasonable, enforcement gaps in the
architecture tests, and isolation/resource invariants that no single package owns.

**Binding sources:** `docs/architecture/06-decision-register.md` (DR-0..DR-39, principally DR-2
adjacency, DR-3 tenancy, DR-5 tenant-scoped signatures, DR-9/DR-10 sampler, DR-13 topology, DR-24
kubectl, DR-25 capabilities), `docs/architecture/01-system-architecture.md`,
`docs/architecture/features/*.md`, `docs/signoffs/architecture-signoff.md`.

**Environment:** `GOTOOLCHAIN=go1.27.1`, `CGO_ENABLED=0`, `go version go1.27.1 windows/amd64`.

---

## Verdict

# APPROVE-WITH-FIXES

Three Blockers and four Majors were found and **all seven are fixed in this pass**, with nine new
regression tests. The codebase builds, vets, formats and tests clean.

The qualifier on the verdict is not a defect — it is scope. `cmd/traceiq` assembles the telemetry
pipeline (ingest → sampler → store → topology → anomaly) and nothing above it: `internal/api`,
`internal/nl`, `internal/remediate`, `internal/memory`, `internal/correlate` and `internal/eval` are
implemented and independently tested but **are not wired into the binary**, because `internal/auth`
contains zero concrete implementations and every API route except `/healthz`, `/readyz` and the
static SPA is gated on `auth.Authenticator`/`auth.Authorizer` (fail-closed → 401/403). Integration
testing can therefore exercise the ingest→storage→detection path end-to-end through the real binary,
and everything above it only through its packages' own test harnesses. That boundary is stated
here so the integration-test plan is written against what actually starts, not against the package
inventory. Detail and rationale in Finding **M5**.

---

## Findings

| # | Severity | Area | Issue | Fix / status |
|---|---|---|---|---|
| **B1** | **Blocker** | `internal/sampler` | `shardBuf.traces` was keyed by `model.TraceID` **alone**. TraceIDs are minted by the instrumented client and arrive verbatim over OTLP, so they are attacker-chosen and carry no cross-tenant uniqueness guarantee. A span from tenant B whose TraceID matched an in-flight trace of tenant A was appended to A's assembly buffer; `finalize()` then evaluated the sampling policy with A's `Tenant` and emitted one `Decision` + `model.Trace` under A — so B's span bodies were persisted into, and readable from, tenant A's store. | **FIXED** — new `sampler.TraceKey{Tenant, TraceID}` keys the buffer, the WAL, and WAL replay. Sharding still hashes the TraceID only, so DR-10's "all spans of one trace on one shard" property is preserved. |
| **B2** | **Blocker** | `cmd/traceiq` → `internal/ingest` (contract mismatch) | `internal/ingest/otlpgrpc.go`'s `subjectFromContext` deliberately returns an **empty** subject tenant for every auth mode it has not implemented, documented verbatim: *"tenant.Resolver.FromSubject is expected to reject [it] as UNAUTHENTICATED -- fail-closed, not fail-open"*. The only production `tenant.Resolver` — `cmd/traceiq`'s `simpleResolver.FromSubject` — was a bare `return subjectTenant, nil`. So with `auth.mode != "none"`, a completely unauthenticated gRPC client's spans were accepted and stamped `TenantID("")`, merging unrelated senders into one shared pseudo-tenant. Each side was individually defensible; only a cross-package read catches it. | **FIXED** at three layers: `FromSubject` rejects an empty subject tenant (`ErrNoSubjectTenant`); `FromDevDefault` rejects a blanked `tenancy.default_tenant` (`ErrNoDefaultTenant`); and `Server.Ingest` re-checks the *resolved* value (`ErrNoTenant` → `ReasonUnauthenticated`) so ingest's isolation does not depend on which Resolver it was injected with. |
| **B3** | **Blocker** | `internal/sampler` | `ReplayWAL` reconstructed recovered traces as `&Trace{TraceID: tid, ...}` with `Tenant` left at its **zero value**, so every trace recovered from the WAL finalized under `TenantID("")` — its Decision, its policy evaluation and its persisted body all lost the tenant they were journalled for. Additionally `WAL.Truncate(shard, traceID)` was tenant-blind: tenant A finalizing its trace deleted tenant B's journalled spans for the same id. | **FIXED** — `TraceKey` carries the tenant through append/truncate/replay; the replayed `Trace` is built with `Tenant: key.Tenant`. |
| **M1** | **Major** | `internal/topology` | `evictIfNeeded` chose its victim with `e.Calls < victim.Calls` evaluated over Go's **randomized map iteration order**, so any `Calls` tie was resolved by whichever key the runtime yielded first. At cap this let a freshly-inserted edge evict *itself* ~50% of the time and then immediately re-trigger eviction on its next sighting. Surfaced as the confirmed `TestEvictionAtCap` flake; in production it is nondeterministic, non-LRU edge loss. | **FIXED** — victim selection now uses a **total order**: fewest `Calls`, then least-recently-seen (`LastSeen`), then earliest insertion (`insertSeq`, a side map, so the exported cross-package `Edge` value type gains no hidden state). `insertSeq` always decides, including under a fake/virtual clock where every edge shares one identical timestamp. |
| **M2** | **Major** | `internal/topology` | `topology.max_edges` bounded `g.edges` only. `g.ops` (per-edge operation rows) and `g.lastNewEmit` were **never pruned** — eviction deleted from `edges` and left both companion maps growing without bound. Under high edge cardinality the configured cap bounded one of three maps, i.e. it was not a real memory bound. | **FIXED** — eviction now drops the victim's `ops` and `lastNewEmit` entries alongside it, and loops until the graph is genuinely at cap rather than evicting exactly one edge per call. |
| **M3** | **Major** | `internal/topology` | `WarmStart` installed whatever `EdgeSource.LoadEdges` returned **wholesale**, bypassing `max_edges` entirely. A restart could re-inflate the graph far past its configured bound and stay there until organic churn happened to upsert a brand-new edge. | **FIXED** — `WarmStart` re-applies the cap after loading, so `max_edges` holds across restarts. |
| **M4** | **Major** | `internal/archtest` | The DR-2 adjacency walk filtered on `strings.HasPrefix(importPath, "traceiq/internal/")` and `continue`d on everything else, so the module-level **`traceiq/web`** edge was invisible: DR-2 grants `web` to `internal/api` alone, but any package could have imported it and archtest would have passed. The contract in check #1 was therefore incomplete, not merely accurate-so-far. | **FIXED** — new `TestWebPackageEdge` asserts both halves of the edge (only `api` may import `web`; `web` itself imports nothing from the module) and fails on a silently-empty scan. |
| **M5** | **Major (scope; documented, not fixed here)** | `cmd/traceiq`, `internal/auth` | `cmd/traceiq` imports only `anomaly, config, ingest, model, rca, rca/rules, sampler, selfobs, store, store/parquet, store/sqlite, tenant, topology`. `internal/api` (and with it `nl`, `remediate`, `memory`, `correlate`, `eval`, `llm`, `k8s`, `bus`, `cluster`) is **not wired into the binary**. Root cause: `internal/auth` is 226 lines of interfaces and types with **zero implementations**, and `api/middleware.go` fails closed on a nil `Authenticator`/`Authorizer` — so wiring the API today yields a process that serves `/healthz`, `/readyz` and the SPA shell but 401s every REST/MCP call. | **NOT FIXED — deliberately.** This is a multi-package wiring job that requires first *writing* a concrete dev-mode Authenticator/Authorizer pair, not a review-pass fix; doing it under a review's time box would produce exactly the "looks configured, is non-functional" state `cmd/traceiq/system.go`'s own TODO argues against. The existing TODO is accurate and remains the right record. **This bounds what integration testing can cover — see the Verdict.** |

### Fixed-file inventory

| File | Change |
|---|---|
| `internal/sampler/wal.go` | `WAL` interface + `MemWAL` re-keyed on new `TraceKey{Tenant, TraceID}` |
| `internal/sampler/assembly.go` | `shardBuf.traces` keyed by `TraceKey`; `Consume` overwrites `span.Tenant` with the batch tenant unconditionally; `ReplayWAL` preserves tenant; `finalize` truncates tenant-scoped |
| `internal/ingest/ingest.go`, `internal/ingest/errors.go` | `ErrNoTenant`; `Server.Ingest` rejects an empty resolved tenant as `ReasonUnauthenticated` |
| `cmd/traceiq/tenant_resolver.go` | `FromSubject`/`FromDevDefault` fail closed on empty (`ErrNoSubjectTenant`, `ErrNoDefaultTenant`) |
| `internal/api/middleware.go` | `authenticate` rejects an authenticated Subject carrying no tenant (401) |
| `internal/topology/livegraph.go` | `insertSeq`/`nextSeq`; total-order `colder()` tie-break; looped eviction pruning `ops`+`lastNewEmit`; `WarmStart` re-applies `max_edges` |
| `internal/archtest/archtest_test.go` | `webImporters` + `TestWebPackageEdge` |

### New regression tests (9)

| File | Tests |
|---|---|
| `internal/sampler/w17_tenant_isolation_test.go` | `TestConsume_SameTraceIDAcrossTenants_StaysIsolated`, `TestConsume_SpanTenantIsAlwaysOverwrittenByBatchTenant`, `TestReplayWAL_PreservesTenant`, `TestWALTruncate_IsTenantScoped` |
| `internal/ingest/w17_empty_tenant_test.go` | `TestServerIngest_EmptyResolvedTenantIsRejected` (drives a deliberate pass-through resolver — the exact pre-fix shape) |
| `internal/topology/w17_eviction_test.go` | `TestEvictionTieBreak_IsDeterministic` (200 iterations — a map-order regression fails ~every run, not 1-in-10), `TestWarmStart_RespectsMaxEdges` |
| `cmd/traceiq/tenant_resolver_test.go` | `TestSimpleResolver_FailsClosedOnEmptyTenant`, `TestSimpleResolver_DevDefaultRefusesEmptyDefaultTenant`, `TestSimpleResolver_DevDefaultGates` |

---

## The seven checks

### 1. archtest import-boundary contract vs. DR-2 — **PASS (after M4 fix)**

- The `adjacency` table in `archtest_test.go` was compared line-by-line against DR-2's published
  table (register lines 129–152). Every row matches, including the additions made since the table
  was first encoded (`auth`, `llm`, `k8s`, `config`, `selfobs`, `bus`, `cluster`, `rca/rules`).
- The **actual** import graph was recomputed with `go list` and checked against the table. **No
  package imports anything it shouldn't.** Every real edge is a subset of its allowed set. The
  `ScopeCoverage` subtest confirms 26 packages / 131 non-test files were genuinely walked.
- **Gap found and fixed (M4):** the `traceiq/web` edge was outside the walk's filter entirely.
- One deliberate widening noted, **not** changed: archtest's `api` row lists `llm, k8s, bus, cluster`
  explicitly, where DR-2's prose says "all feature packages, auth, tenant, config, selfobs, web".
  Those four are arguably infrastructure rather than feature packages. `api` currently imports
  `k8s` and `cluster` (not `llm`/`bus`). This is a register-prose ambiguity, not a violation;
  logged as **Minor-6**.
- `cmd/traceiq` is checked for allowlist membership only, which is correct: DR-2 grants the
  composition root every internal package.

### 2. Tenant isolation end-to-end (DR-5) — **PASS (after B1/B2/B3 fixes); this check found all three Blockers**

Traced `TenantID` across every package boundary rather than within one:

| Boundary | Finding |
|---|---|
| gRPC → `ingest.Ingest` | **B2** — empty subject tenant accepted; fixed, fails closed at both the resolver and ingest. |
| `ingest` → `SpanSink` | Correct: every span stamped with the resolved tenant before any sink sees it. |
| `sampler.Consume` buffer | **B1** — TraceID-only keying merged tenants; fixed. |
| `sampler.ReplayWAL` / `WAL.Truncate` | **B3** — tenant dropped on replay, cross-tenant truncate; fixed. |
| `sampler.finalize` → `Decision`/`model.Trace` | Correct once B1/B3 are fixed: `tr.Tenant` is now always the batch tenant. |
| `store/sqlite` | **Clean.** Every query inspected — all are `WHERE tenant_id=?` and fully parameterized. The two `fmt.Sprintf` query builders interpolate a *table name* from validated `redTable`/`redTableForWindow` helpers, never user input. |
| `store/parquet` | **Clean.** `blockKey(tid, tier, hour)` includes the tenant; tombstones are `map[TenantID]map[TraceID]bool`. |
| `topology.LiveGraph` | **Clean.** `edgeKey` and `pendingKey` both carry `Tenant`. |
| `anomaly` | **Clean.** `incidents map[TenantID]map[string]*Incident`. |
| `memory` | **Clean.** `records map[TenantID]map[string]*Record`, plus explicit `tid == ""` guards. |
| `correlate` | **Clean.** Cache keys are `"logs\|%s\|%x\|..."` with `tid` as the second segment; eviction is per-tenant-partitioned per DR-20 §20.4. |
| `api` handlers | **Clean by construction** — every tenant-scoped call uses `subject.Tenant` from `Authenticate`, never a header, body field or query param (DR-3's "no header, no body field, no query parameter"). Hardened further: `authenticate` now 401s a Subject carrying no tenant, so no handler can query under `TenantID("")`. |

**"Does a missing/zero TenantID silently proceed?"** It did, in three places (B1/B2/B3). It no longer
does anywhere on the ingest→store path or at the API edge.

**DR-5 first-param rule:** every exported tenant-scoped method takes `(ctx, tid, ...)`. The audit
surfaced only unexported helpers and `_ context.Context` variants. The one exported exception,
`ingest.Server.Ingest(ctx, protocol, raw, subjectTenant, useDevDefault)`, is correct by design —
`subjectTenant` is an *unresolved input to* resolution, not a resolved tenant scope.

### 3. Cross-package fail-open sweep — **PASS**

Given the prior pattern (four independent fail-open bugs), swept for the whole class:

| Pattern | Result |
|---|---|
| `if authorizer != nil { check }` | **Only two `!= nil` guards on security deps exist repo-wide.** `remediate/guard.go:279` (`sepOfDuty`) falls back to enforcing `proposer != approver` itself — fail-*closed*. `rca/llm_reasoner.go:16` (`validator()`) falls back to a real `newSchemaValidator()` — fail-closed. Neither is a bypass. |
| Nil-authorizer bypass | `api/middleware.go` (401/403 on nil), `api/mcp.go:162` (nil → `rpcUnauthorized`, checked **before** the not-implemented gate so an unauthorized caller cannot probe which tools exist), `nl/answerer.go:290,346` (nil → deny). All fail closed. Prior fixes intact. |
| Empty/nil allowlist defaulting permissive | `remediate/validate.go` — all three allowlists (`RemediationAllowlist`, `NamespaceAllowlist`, `TargetAllowlist`) deny on empty, with the w14 inversion fix documented in place. `checkTargetAllowlist` treats a malformed glob (`path.Match` error) as non-matching → denied. `guard.policy()` with a nil `PolicyStore` returns a zero `tenant.Policy`, whose empty allowlists deny everything — fail-closed. |
| Error-swallowing that should abort | Every `_ =` / `, _ :=` site triaged. All are deliberate and documented: `queue.consume` ignores a sink error because at-least-once retry lives downstream (DR-9 §9); `memory` `_ = by` defers admin-only enforcement to the API layer (and no such route is registered, so it is unreachable); `api/handlers.go:386` ignores an optional rollback-reason body. None hide a security decision. |
| `panic("not implemented")` reachable in production | 12 stubs audited for **call sites**, not just existence. `model.Span.Service/DurationNanos/IsError/IsRoot/AttrSorted`, `model.NewRealClock`, `tenant.WithTenant/FromContext`, `rca.realClock.NewTicker/NewTimer`: **zero call sites**. `k8s.BuildArgv`/`MinimalEnv`: called only from `remediate/executor.go:214`, which wraps the call in `recover()` and converts the panic to an error — fail-closed. `llm.NewClient`: not reachable, `cmd/traceiq` does not import `llm`. `eval.VirtualClock`: harness-only. **No reachable panic.** |

No new fail-open defect of this class was found. The `authenticate` hardening (B2) is a
defence-in-depth addition rather than a discovered bypass.

### 4a. `internal/ingest/queue.go` shutdown race — **PASS: already fixed, task closed**

The w16 report flagged this via `spawn_task` (task_87d08322). Searching for evidence: **the task was
addressed.** `queue.go:87-139` now carries the fix and its rationale. Verified by reading the code,
not the comment:

- `drain`'s `ctx.Done()` branch calls `drainRemaining()`, which non-blockingly empties `q.ch`
  before returning — closing the window where Go's uniform pseudo-random `select` could pick
  `ctx.Done()` over an already-enqueued batch.
- `consume` uses `context.Background()`, never the loop's cancellable ctx, so `Stop`'s
  `drainCancel` cannot abort a `Consume` already in flight — the same failure mode
  `applyDecision`'s `drainContext()` fix closes in `cmd/traceiq/system.go`.
- The safety precondition is real: `Server.Stop` stops every receiver *before* calling
  `drainCancel`, so nothing can push onto `q.ch` once `ctx.Done()` fires.

**No further action.** This item is closed.

### 4b. `TestEvictionAtCap` flake — **PASS: fixed properly, not suppressed**

Root cause confirmed independently of the w16 diagnosis by reading `upsertEdge` + `evictIfNeeded`:
at `MaxEdges=2` the third edge is inserted and incremented to `Calls=1` *before* `evictIfNeeded`
runs, tying with the second edge; `<` (strictly-less) keeps whichever the map yielded first, so
either may be evicted. When the new edge loses, the next `Consume` re-creates it and evicts again —
`EdgesEvicted=2`. The w16 report's suggested fix (tie-break on `FirstSeen`) would **not** have
worked: the test's `fakeClock.Now()` is static, so `FirstSeen` and `LastSeen` are identical across
all three edges. The fix uses a monotonic insertion sequence as the final, always-unique
discriminator (M1).

**Verification:** `go test ./internal/topology/... -count=50` — green. The new
`TestEvictionTieBreak_IsDeterministic` runs the scenario 200× in one test, so a regression fails
essentially every run rather than one in ten. The test was **not** weakened, retried or skipped.

### 5. End-to-end data-flow sanity — **PASS**

Traced one synthetic span by reading the call chain: `otlpGRPCReceiver.Export` →
`Server.Ingest` (normalize → `applyLimits` → resolve tenant → stamp → fan out) → `sinkQueue.push`
→ per-sink `drain` goroutine → `sampler.Manager.Consume` (shard → assembly buffer → RED → WAL) →
`Manager.Tick`/`finalize` → `decisions`/`tracesCh` → `cmd/traceiq.runStoreBridge` →
`tiered.Append` → `store/sqlite` + `store/parquet`; separately `topology.LiveGraph.Consume`
(peer extraction → `upsertEdge` → change events) and `anomaly` baseline via `runBaselineFeed`.

| Question | Result |
|---|---|
| Unbounded goroutine spawn per span? | **No.** Every `go` statement repo-wide is startup-scoped: one drain goroutine **per sink queue** at `Receiver.Start`, one gRPC `Serve`, one sqlite `writerLoop`, one `http.Server`, and `cmd/traceiq`'s ticker + two bridges. Nothing spawns per span, per batch or per trace. Backpressure is the bounded `sinkQueue` (bounded in *both* batch count and bytes, with `shed` as the default overflow policy and `block` deleted per DR-28) — the correct shape for load. |
| Context deadline propagated? | Yes where it should be, and deliberately **not** where it shouldn't. `push` honours ctx; `consume` uses `context.Background()` on purpose (once dequeued, delivery is committed — cancellation means "stop pulling new work", not "abort an in-flight write"), matching `applyDecision`'s `drainContext()`. `sampler.Consume` ignores ctx because it performs no blocking or cancellable work. Store writes take ctx. No accidental drop found. |
| Silent type-conversion losses? | None material. Every narrowing conversion in the span path was inspected. `uint32(proto.Size(req))` / `uint32(len(body))` for `Batch.SizeBytes` would truncate above 4 GiB, which gRPC's default 4 MB `MaxRecvMsgSize` makes unreachable; logged as **Minor-2**. `path_signature`'s `uint64`↔`int64` round-trip through SQLite was verified exact by the w16 review and re-confirmed here. Hist/status/kind narrowings are into genuinely bounded enums. |
| Memory bounds | **M2/M3 found here** — `topology`'s cap bounded one of three maps and was bypassed entirely on warm start. Both fixed. |

### 6. TODO / FIXME / dead code triage — **PASS**

68 markers across `internal/` and `cmd/`. Triaged; **zero forgotten-and-should-block**:

- **~60 are `TODO(DR-nn): under-specified`** — a consistent, deliberate convention marking a field
  or type reconstructed because the register gives no Go block. Each names its DR and most cite the
  report that recorded the reconstruction. These are *documentation of spec debt*, correctly placed.
  Keep.
- `internal/api/mcp.go:172` — 9 of 12 MCP tools return `rpcNotImplemented`. Honest (never a faked
  result) and gated behind RBAC. **Minor-1**, backlog.
- `internal/correlate/correlator.go:308` (heuristic `QueryByServiceWindow` fallback),
  `internal/eval/eval.go:59` (`VirtualClock` via Barrier quiescence),
  `internal/api/api.go:86`/`:200` (endpoint/method-set confirmation) — all deferred *with* a named
  owner doc. Backlog.
- **Dead code / unused exports:** none found beyond `internal/depstub` (see check 7). The panic
  stubs in `model`, `tenant`, `k8s`, `llm` and `eval` are not dead — they are interface surfaces
  DR-required to exist; their *call sites* were audited in check 3 and none is reachable.
- `traceiq.exe` at the repo root is a local build artefact and is covered by `.gitignore` (`*.exe`).
  Not committed. No action.

### 7. Build / release hygiene — **PASS**

```
go build ./...                          exit 0
go vet   ./...                          exit 0
gofmt -l .                              (empty)
go test  ./... -count=1                 all 18 tested packages ok  (run twice, both green)
go test  ./internal/topology/ -count=50 ok   (the former flake)
go build -trimpath -o traceiq.exe ./cmd/traceiq
                                        exit 0 — 36.9 MB binary, CGO_ENABLED=0
```

**`internal/depstub`: should NOT be deleted yet — deleting it would lose DR-1 pins.** Checked each
of its 13 pinned modules for a real importer outside `depstub`:

| Still stubbed (no production importer) | Now genuinely imported |
|---|---|
| `github.com/google/go-cmp/cmp` (test-only) | `go.opentelemetry.io/proto/otlp` (6) |
| `github.com/klauspost/compress/zstd` | `google.golang.org/grpc` (3) |
| `go.opentelemetry.io/otel` + `/sdk/trace` | `github.com/parquet-go/parquet-go` (1) |
| `golang.org/x/crypto/argon2` | `github.com/prometheus/client_golang/prometheus` (1) |
| `golang.org/x/sync/errgroup` | `google.golang.org/protobuf/proto` (1) |
| `golang.org/x/time/rate` | `modernc.org/sqlite` (1) |

Six of thirteen are still unreferenced, and each maps to unbuilt functionality: `argon2` → X-SEC
credential hashing (the same gap as M5's missing `internal/auth` implementation), `zstd` → Parquet
compression, `otel`+`sdk/trace` → self-tracing, `errgroup`/`rate` → concurrency and rate limiting.
`depstub` is therefore still doing its documented job of keeping DR-1's normative pin set in
`go.mod`. Its own header already states the deletion condition. **Useful side effect: that list is
an accurate inventory of what is specified but not yet built.** Logged as **Minor-4** (trim to the
six).

---

## Minor findings — explicit backlog (not fixed, by design)

| # | Area | Issue | Why it doesn't block testing |
|---|---|---|---|
| **Minor-1** | `internal/api/mcp.go` | 9 of 12 MCP tools return `rpcNotImplemented`. Implemented: `traceiq_search_traces`, `traceiq_get_trace`, `traceiq_topology_neighbors`. | Honest failure, never a faked result; RBAC-gated before the not-implemented check. Also moot until M5 wires the API. |
| **Minor-2** | `internal/ingest` | `Batch.SizeBytes` is `uint32(proto.Size(req))` / `uint32(len(body))` — truncates above 4 GiB. | Unreachable: gRPC's default `MaxRecvMsgSize` is 4 MB. Worth an explicit bound if that default is ever raised. |
| **Minor-3** | `internal/correlate` | Circuit-breaker state is keyed by `Adapter.Name()` and shared across tenants, so one tenant's failures can trip the breaker for all. | Documented as per-adapter by FR-F07-7; the backend genuinely is shared. Revisit if per-tenant backends land. |
| **Minor-4** | `internal/depstub` | Retains pins for 7 modules that now have real importers. | Cosmetic. Trimming to the six genuinely-unused pins would make the file an accurate "not yet built" indicator. |
| **Minor-5** | `internal/memory` | `Store.Import`/`Export` take `by auth.Subject` and `_ = by` it, deferring FR-F08-5's admin-only check to the API layer. | No API route reaches them, so the gap is currently unreachable — but it **must** be closed in the same wave that wires M5, or it becomes live. **Flagged as a dependency of M5, not an independent item.** |
| **Minor-6** | `internal/archtest` | The `api` row lists `llm, k8s, bus, cluster` explicitly; DR-2's prose says "all feature packages, auth, tenant, config, selfobs, web". | A register-prose ambiguity, not a violation — `api` currently imports only `k8s` and `cluster` of the four. Worth one line of clarification in DR-2. |
| **Minor-7** | `internal/nl/rules.go:169` | `extractToolArgs(ctx, text, tid, ...)` puts `tid` third. | Unexported helper; DR-5's first-param rule governs interface methods. Style only. |
| **Minor-8** | `internal/sampler/wal.go` | `MemWAL` is in-memory; AC-F02-11's kill-and-restart durability test needs a disk-backed WAL. | Pre-existing, documented deviation. The interface is pluggable, and `TraceKey` now makes a disk implementation tenant-correct from day one. |

---

## Repo health

| Metric | Value |
|---|---|
| Go files | **190** (was 183; +7 — 5 new test files, 2 pre-existing) |
| Test files | **50** (was 45; +5) |
| Test functions | **374** (+9 this pass) |
| Packages with tests | **18** (was 17; `cmd/traceiq` gained `tenant_resolver_test.go`, `internal/topology` and `internal/ingest` gained w17 files) |
| Untested packages | `auth`, `bus`, `cluster`, `k8s`, `llm`, `model`, `selfobs`, `store`, `tenant`, `depstub`, `web` — all are interface/type/embed-only declarations with no branching logic, **except `auth`**, which has no logic *yet* (M5) |
| `go build ./...` | ✅ exit 0 |
| `go vet ./...` | ✅ exit 0 |
| `gofmt -l .` | ✅ empty |
| `go test ./... -count=1` | ✅ all green, twice |
| Known flaky tests | **None.** The only known flake (`TestEvictionAtCap`) is fixed at the source and pinned by a 200-iteration regression test. |
| Toolchain | go1.27.1, `CGO_ENABLED=0`, matches DR-1 |

### Remaining known issues and why they don't block integration testing

1. **`internal/api` is not wired into `cmd/traceiq` (M5).** This is the one item that genuinely
   shapes the test plan. The binary starts the telemetry pipeline plus `/healthz`, `/readyz` and
   `/metrics`. Integration tests should target that path through the real binary, and cover the
   API/NL/remediation layers through their packages' own harnesses until `internal/auth` grows a
   concrete Authenticator/Authorizer pair. **Minor-5 must be closed in the same wave.**
2. **MCP tool coverage is 3 of 12 (Minor-1).** Downstream of M5 regardless.
3. **`MemWAL` is in-memory (Minor-8).** Crash-durability (AC-F02-11) cannot be integration-tested
   until a disk-backed WAL lands; every other sampler behaviour can.
4. **Six DR-1 dependencies are pinned but unused (check 7).** Each corresponds to a documented
   unbuilt feature (auth hashing, Parquet compression, self-tracing, rate limiting). No runtime
   effect.

None of these is a correctness defect in shipped code. Everything that *is* implemented is now
tenant-isolated, fail-closed, deterministic and bounded.

---

**Reviewer:** final whole-system pass (w17).
**Recommendation:** proceed to integration testing on the pipeline path, with the M5 boundary
written into the test plan and scheduled as the next wave's single objective.
