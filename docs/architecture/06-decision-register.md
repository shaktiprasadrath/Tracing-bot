# 06 — TraceIQ Decision Register (BINDING)

Status: **Binding, round 1** · Date: 2026-09-14 · Owner: Chief Architect / Architecture Review Board
Source findings: `docs/reviews/round1/security-reliability.md` (SR-*), `performance-data.md` (PD-*), `completeness-coherence.md` (CC-*)
Review log: [`../reviews/architecture_review.md`](../reviews/architecture_review.md)

---

## How to use this document

This register is the **reconciliation source of truth**. Where it contradicts `00`–`05`, any feature doc, or the PRD, **this document wins** until those documents are rewritten to match it.

Fix agents apply decisions **mechanically**:

1. Find your document in the "Docs to change" row of each decision.
2. Copy signatures, DDL, config keys, numbers and state tables **verbatim**. Do not re-derive, do not improve, do not rename.
3. Where a decision says *delete*, delete. Where it says *cite*, replace the local restatement with a one-line cross-reference.
4. Every new FR/AC ID this register mandates is listed in **Appendix C**. Create exactly those, with exactly those IDs.
5. If a decision is ambiguous for your document, stop and raise it — do not choose.

Numbers in this register are **defaults for the `dev` profile** (MVP: one binary, one developer laptop, Windows/macOS/Linux, embedded SQLite + local Parquet) unless a `prod` value is given alongside. Phase 2 (Kafka / ClickHouse / StatefulSet ring) values are marked **[P2]** — they are design-binding now, implementation-deferred.

---

## DR-0 — Document precedence and ownership

**Resolves:** CC-11, CC-13, CC-24, SR-24, PD-18, PD-21 (the general form of "the same number appears twice with different values").

**Decision.** Exactly one document owns each class of fact. Every other document **cites** it and states no value of its own.

| Class of fact | Sole owner | Everyone else |
|---|---|---|
| Shared Go types (`model.*`) | `01 §4` | cite `01 §4.x`; may not re-declare |
| Per-package interfaces | **this register**, then mirrored into `02` | `00` lists names only; feature docs cite `02` |
| Package adjacency | `02 §5` (updated per DR-2) | `internal/archtest` enforces it |
| SQLite DDL and Parquet layout | `01 §5` | feature docs cite table names only |
| REST/MCP endpoint table | `01 §6.1`/`§6.2` | feature docs render a **filtered view** with the header "defined in `01 §6.1`" |
| RCA tool argument schemas | `01 §6.3` (replaced per DR-16) | `F06` cites |
| Every config key and default | `01 §7` | feature docs cite key paths, never values |
| Every performance/accuracy gate | `01 §10` | feature NFR tables become references |
| Security limits table | `01 §8.4` (replaces `X-SEC §3.1`) | `X-SEC`, `F01`, `F12` cite |
| Dependency pins and toolchain | `05 §8` | `01 §11` becomes a pointer |
| Drawback → mechanism mapping | `01 §9` | each row must name an FR ID |

**Docs to change:** `00` (add a "fact ownership" note), `01` (add `§0 Precedence` restating this table), `02 §5`, all `F0x`/`X-*` §3.2 NFR tables and §4.3 config blocks, `05 §8`.

---

## DR-1 — Toolchain and dependency set: Set A withdrawn, Set B on Go 1.27.1

**Resolves:** SR-23. **Also closes:** ADR FU-1, FU-2.

**Decision.**

`go.mod` header, verbatim:

```
module traceiq

go 1.27

toolchain go1.27.1
```

CI environment, verbatim: `GOTOOLCHAIN=go1.27.1`, `CGO_ENABLED=0`, `GOFLAGS=-mod=readonly`, build flags `-trimpath`. **`GOTOOLCHAIN=auto` and `GOTOOLCHAIN=local` are both forbidden** — `auto` permits silent substitution, `local` breaks the self-provisioning property. The builder image pre-seeds the `go1.27.1` toolchain module so offline/air-gapped builds do not regress, and the toolchain module hash is recorded in the SBOM.

**ADR §8.1 "Set A" is withdrawn in full.** It is retained under a heading `8.1 Set A — WITHDRAWN 2026-09-14` with a two-sentence note explaining that it existed only to describe the Go 1.23 constraint, and that the constraint is gone. No document may pin from Set A. `01 §11` is replaced by a pointer to `05 §8.2`.

**Set B (normative direct dependencies).** Rows marked ✅ were compiled and `govulncheck`-verified on the build machine on 2026-09-14 at `CGO_ENABLED=0` under `GOTOOLCHAIN=go1.27.1`, with **0 reachable vulnerabilities**.

| Module | Version | Verified | Purpose |
|---|---|---|---|
| `go.opentelemetry.io/proto/otlp` | `v1.11.0` | ✅ | OTLP wire types (F01) |
| `google.golang.org/grpc` | `v1.83.2` | ✅ | OTLP/gRPC + Jaeger gRPC receivers |
| `google.golang.org/protobuf` | `v1.36.12` | ✅ | Protobuf runtime |
| `modernc.org/sqlite` | `v1.58.0` | ✅ | Hot index (F03), pure Go |
| `github.com/parquet-go/parquet-go` | `v0.32.0` | ✅ | Cold store (F03) |
| `golang.org/x/sync` | `v0.23.0` | ✅ | `errgroup`, `semaphore` |
| `github.com/klauspost/compress` | `v1.20.0` | VERIFY-ON-LAND | zstd for Parquet |
| `golang.org/x/time` | `v0.16.0` | VERIFY-ON-LAND | `rate.Limiter` |
| `golang.org/x/crypto` | `v0.57.0` | VERIFY-ON-LAND | `argon2id` (X-SEC) |
| `golang.org/x/net` | `v0.59.0` | VERIFY-ON-LAND | indirect via grpc/otlp |
| `github.com/google/go-cmp` | `v0.7.0` | VERIFY-ON-LAND | test-only |
| `github.com/prometheus/client_golang` | `v1.23.2`+ | VERIFY-ON-LAND | `/metrics` |
| `go.opentelemetry.io/otel` + `/sdk` | `v1.38.0`+ | VERIFY-ON-LAND | self-tracing |

`VERIFY-ON-LAND` rows must be resolved to a concrete version, compiled, and `govulncheck`-verified in the same commit that lands `go.mod`; a row that cannot reach 0 reachable vulnerabilities blocks the commit.

**[P2] optional backends** (build-tagged, absent from the default module graph): `github.com/ClickHouse/clickhouse-go/v2 v2.48.0`, `github.com/twmb/franz-go v1.19.5`.

**Analyser pins, re-pinned in the same atomic change** (an analyser built against an older Go silently under-analyses):

| Tool | Pin | Gate |
|---|---|---|
| `honnef.co/go/tools` (staticcheck) | latest release declaring Go ≥ 1.27 support | blocking |
| `github.com/securego/gosec/v2` | latest release declaring Go ≥ 1.27 support | blocking |
| `golang.org/x/vuln` (govulncheck) | ≥ `v1.1.4`, rebuilt under go1.27.1 | blocking, **0 reachable** |

**Deliberately absent list is unchanged and gains one entry: `k8s.io/client-go` and all `k8s.io/*` modules** (see DR-24).

**Follow-up actions, rewritten in `05 §9`:**

| ID | State |
|---|---|
| FU-1 | **CLOSED** — Go 1.27.1 landed; 0 reachable vulnerabilities |
| FU-2 | **CLOSED — not needed.** The expiring `govulncheck` allowlist is deleted; there is nothing to allow. CI is red on any finding, with no suppression file |
| FU-3 | **OPEN, re-scoped by DR-6** — benchmark the pure-Go SQLite hot index against DR-6's writer budget with the real schema (span rows + `attr_index` + FTS), publish sustained committed rows/sec |
| FU-4 | OPEN — Anthropic golden-file wire tests before the first F06 merge (DR-34) |
| FU-5 | OPEN [P2] — OTel Collector distro packaging |
| FU-6 | OPEN [P2] — `GOFIPS140=v1.0.0` release variant |
| **FU-7** | **NEW, P0** — CI asserts each analyser actually executed (non-empty analysis manifest per tool); a skipped gate must fail, not pass |
| **FU-8** | **NEW, P0** — CI check over `go list -m all` fails on any module in the "deliberately absent" list |
| **FU-9** | **NEW, P1** — reproducible-build check: two clean builds produce identical digests under the pinned toolchain |

**Docs to change:** `05 §7.1` (rewrite: the 25-vuln finding becomes a historical record, the conclusion becomes "resolved"), `05 §8.1` (withdraw), `05 §8.2` (promote to normative, add analyser pins + the absent-list entry), `05 §9` (table above), `05 §10` (replace `GOTOOLCHAIN=local` with `GOTOOLCHAIN=go1.27.1`), `05 §12`, `01` header line ("Implementation target: **Go 1.27**"), `01 §10` preamble (reference box is Go 1.27.1), `01 §11` (pointer only), `docs/ledger.md`.

---

## DR-2 — Final package adjacency: no cycles, and the rule that keeps it that way

**Resolves:** CC-1, PD-15, PD-10(a), PD-31 (graph half), CC-8 (graph half).

**Decision — the structural rule.** Every fan-out, sink, source or callback interface is **declared in the consumer package**, and **its method signatures may reference only `model`, `tenant`, and stdlib types**. Because Go interface satisfaction is structural, an implementor then satisfies it *without importing the declaring package*, so no import edge is created.

> Correction to PD-15(5): `topology` implementing `ingest.SpanSink` does **not** create `topology → ingest`, provided `SpanSink`'s signature mentions only `model` types. The finding's defect is real (the docs imply an import); the remedy is the signature rule above, not moving `Sink` into `model`.

**Decision — the adjacency list.** This replaces `02 §5`'s graph in full. `internal/archtest` asserts it exactly; any edge not listed fails CI.

| Package | May import (internal only) |
|---|---|
| `internal/model` | — (leaf; stdlib only) |
| `internal/config` | `model` |
| `internal/tenant` | `model` |
| `internal/selfobs` | `model` |
| `internal/bus` | `model`, `config` |
| `internal/cluster` | `config` |
| `internal/llm` | `model`, `config` |
| `internal/k8s` | `model` |
| `internal/auth` | `model`, `config`, `tenant` |
| `internal/topology` | `model`, `config`, `tenant` |
| `internal/store` (+ `store/sqlite`, `store/parquet`, `store/tiered`, `store/blob`, `store/clickhouse`) | `model`, `config`, `tenant`, `topology` |
| `internal/ingest` | `model`, `config`, `tenant`, `selfobs` |
| `internal/sampler` | `model`, `config`, `tenant` |
| `internal/anomaly` | `model`, `config`, `tenant`, `topology`, `store` |
| `internal/correlate` | `model`, `config`, `tenant`, `auth` |
| `internal/memory` | `model`, `config`, `tenant`, `store`, `llm` |
| `internal/remediate` | `model`, `config`, `tenant`, `store`, `topology`, `auth`, `k8s` |
| `internal/rca` | `model`, `config`, `tenant`, `store`, `topology`, `anomaly`, `correlate`, `memory`, `sampler`, `auth`, `llm` |
| `internal/nl` | `model`, `config`, `tenant`, `store`, `topology`, `anomaly`, `rca`, `memory`, `auth` |
| `internal/eval` | `model`, `config`, `tenant`, `ingest`, `store`, `anomaly`, `rca` |
| `internal/api` | all feature packages, `auth`, `tenant`, `config`, `selfobs`, `web` |
| `cmd/traceiq` | `api`, `cluster`, `bus`, `selfobs`, `config`, `tenant`, `auth`, all feature constructors |

**Changes versus `02 §5` as published:**

| Change | Why |
|---|---|
| **`sampler` no longer imports `store`** | The sampler emits through channels (DR-10) and reads baselines through the consumer-declared `sampler.BaselineSource`. `cmd/traceiq` wires `store/sqlite` in. This removes the `store ↔ sampler` pressure at the root (PD-10a, CC-1a) |
| **`correlate` no longer imports `store`** | The caller passes `model.Window` and the trace; `correlate` never calls `store.GetTrace` (PD-15(6)) |
| **`remediate` does not import `anomaly`** | `remediate.Verifier` takes the locally declared `remediate.RecoverySignal` (DR-23) |
| **`memory` does not import `rca`** | `memory.Store.Record` takes `model.InvestigationRecord` (CC-1b) |
| **new `internal/llm`** | Houses the Anthropic HTTP client. `rca` and `memory` both import `llm`; neither imports the other. Kills `memory.AnthropicEmbedder{-client rca.AnthropicClient}` (PD-15(3)) |
| **new `internal/tenant`** | See DR-3 |
| **new `internal/k8s`** | See DR-24 |
| **`internal/ops` never existed and must not be created** | CC-8, PD-31 |
| `store → topology` retained | Single direction, for the `topology.Edge` value type in `EdgeSink`/`EdgeSource` implementations |

**Cycle-break table (replaces `02 §5`'s):**

| Would-be cycle | Break |
|---|---|
| `rca ↔ memory` | `memory.Store.Record(ctx, tid, model.InvestigationRecord)`. `rca.Investigation` has `Summary() model.InvestigationRecord` |
| `rca ↔ memory` via the embedder | The Anthropic client lives in `internal/llm`; `memory.EmbedderLLM` and `rca.LLMReasoner` both hold `llm.Client` |
| `rca ↔ remediate` | `rca.Investigation.SuggestedActions []model.ActionProposal`; `api` bridges to `remediate.Guard.Propose` |
| `store ↔ sampler` | `store.Trace` carries `model.StorageTier` + `model.KeepReason`. `sampler.Reason` is **deleted** (DR-4) |
| `sampler ↔ store` (baselines) | `sampler.BaselineSource`, declared in `sampler`, satisfied by `store/sqlite` |
| `topology ↔ store` | `topology.EdgeSink` / `topology.EdgeSource`, declared in `topology`, satisfied by `store/sqlite` and `store/clickhouse`; injected by `cmd/traceiq`. **`topology` never imports `store`** |
| `topology ↔ ingest` | `ingest.SpanSink.Consume(ctx, model.TenantID, []model.Span) error` mentions only `model`; `topology.LiveGraph` satisfies it structurally, no import |
| `remediate ↔ anomaly` | `remediate.RecoverySignal`, declared in `remediate` |
| `correlate ↔ store` | Caller supplies `model.Window`; `correlate` returns lines/series only |

**Docs to change:** `02 §5` (graph + both tables, replace wholesale), `01 §1.2` (add `tenant`, `llm`, `k8s`; delete any `ops` reference), `F01 §4.3`, `F03 §4.2`, `F04 §4.1`/`§4.4`, `F07 §4.4`, `F08 §4.3`, `02 §3` (delete `memory_AnthropicEmbedder --> rca_AnthropicClient`; add `llm_Client`), `X-OPS §4.3`.

---

## DR-3 — Tenancy: `internal/tenant` is the canonical home; one `TenantResolver`

**Resolves:** CC-8, PD-31. **Supersedes both reviewers' placements** — CC-8 proposed `model` + `auth`, PD-31 proposed `model`; the board adopts a dedicated leaf-adjacent package instead, because `auth` must be able to *use* a tenant policy (RBAC bindings) while `store`, `sampler` and `remediate` must be able to read it without importing `auth`.

**Decision.** New package `internal/tenant`, importing only `model`.

```go
package tenant

// Policy is the single per-tenant record consumed by F02 (floor), F03 (retention,
// byte budget), F06 (LLM spend), F09 (remediation allowlist) and X-SEC (RBAC).
type Policy struct {
    TenantID                model.TenantID
    DisplayName             string

    // F03 retention and cost
    RetentionAnomalousDays  int     // default 30
    RetentionHealthyDays    int     // default 7
    RetentionHotSpanHours   int     // default 24 (dev) / 72 (prod)
    ByteBudgetBytes         int64   // 0 = inherit store.budget.max_disk_bytes share
    // F02 sampling
    SamplingFloor           float64 // default 0.01
    MaxKeepRate             float64 // default 0.25
    // F06 cost
    LLMCostMicroUSDPerDay   int64   // default 20_000_000 ($20)
    // F09 remediation
    RemediationAllowlist    []model.ActionType
    NamespaceAllowlist      []string
    TargetAllowlist         []string // glob rules, e.g. "prod/Deployment/checkout-*"
    ActionBudgetPerIncident int      // default 2
    AutoExecuteAllowed      bool     // default false
    FeatureFlags            map[string][]string // key -> allowed string values ("true"/"false" for bools)
    // X-SEC
    RBACBindings            map[string][]string // subjectID -> role names
    ChatBindings            []ChatBinding
    UpdatedAt               time.Time
    Version                 int64    // optimistic concurrency
}

type ChatBinding struct {
    Platform    string // "slack" | "teams"
    WorkspaceID string
    ChannelIDs  []string // approval-capable channels; empty = none
}

// Resolver is the ONLY tenant resolution path in the system.
type Resolver interface {
    // FromSubject is the sole production path: the tenant is a property of the
    // authenticated principal. No header, no body field, no query parameter.
    FromSubject(ctx context.Context, subjectTenant model.TenantID) (model.TenantID, error)
    // FromDevDefault is legal ONLY when server.profile == "dev" AND the listener is
    // loopback AND auth.mode == "none". It returns tenancy.default_tenant.
    FromDevDefault(ctx context.Context) (model.TenantID, error)
}

type PolicyStore interface {
    Get(ctx context.Context, tid model.TenantID) (Policy, error)
    Set(ctx context.Context, p Policy, by string) (Policy, error) // admin only, audited
    List(ctx context.Context) ([]Policy, error)
    Watch(ctx context.Context, tid model.TenantID) (<-chan Policy, error)
}

// Context carriage is a convenience for logging and defence in depth. It is NEVER
// the enforcement path: tenancy is enforced by the explicit model.TenantID parameter
// on every store/memory/correlate/topology/rca/remediate call.
func WithTenant(ctx context.Context, tid model.TenantID) context.Context
func FromContext(ctx context.Context) (model.TenantID, bool)
```

**Deletions, binding:**
- `ingest.TenantResolver` — **deleted**. `F01` consumes `tenant.Resolver`.
- `ops.TenantResolver` — **deleted** with the whole `ops` package.
- `ops.TenantPolicy` and `remediate.TenantPolicy` — **deleted**. One `tenant.Policy`.
- `ops.PolicyStore` — renamed `tenant.PolicyStore`.
- `ops.BackupManager`, `ops.VersionChecker` — **rejected as interfaces** (CC-8); implemented as `cmd/traceiq` subcommands `traceiq backup snapshot`, `traceiq restore --from=<id>`, `traceiq version --check-skew`.

**Backing store:** `control.db` table `tenant(id, display_name, policy_json, version, created_at, updated_at)` (DR-6). No new datastore (closes CC-33(6)).

**Docs to change:** `01 §1.2` (add `internal/tenant`), `01 §4` (reference, not re-declaration), `01 §5.1` (`tenant` table gains `version`), `02 §4`/`§5`, `00` (core interfaces list), `F01 §4.3`, `F02`, `F03`, `F09 §4.2`, `X-SEC §4.2`/`§4.3`, `X-OPS §4.2`/`§4.3` (delete `package ops` entirely).

---

## DR-4 — `internal/model`: one definition of every shared type

**Resolves:** SR-24, PD-19, CC-27, PD-18 (type half), PD-34 (type half).

**Decision.** `01 §4` is normative and complete. Every `model` block in a feature doc is **deleted** and replaced with a cross-reference. The following additions/changes to `01 §4.1` are binding:

```go
package model

type TenantID string          // NEW — the tenancy parameter type used in every signature
type KeepReason uint8         // NEW — replaces sampler.Reason as the persisted value
const (
    KeepError        KeepReason = 1
    KeepSlow         KeepReason = 2
    KeepRare         KeepReason = 3
    KeepInterest     KeepReason = 4
    KeepFloor        KeepReason = 5
    KeepProbabilistic KeepReason = 6
    KeepDropped      KeepReason = 7
    KeepShed         KeepReason = 8
    KeepTruncated    KeepReason = 9
)

type Window struct { Start, End time.Time }   // NEW — replaces store.TimeWindow,
// store.Window, topology.Window, correlate.TimeWindow, anomaly window pairs. One type.

type Quantiles struct {                        // NEW — see DR-39
    P50Nanos, P95Nanos, P99Nanos, MaxNanos uint64
}

type Clock interface {                         // NEW — see DR-31
    Now() time.Time
    Since(time.Time) time.Duration
    NewTicker(d time.Duration) Ticker
    NewTimer(d time.Duration) Timer
    Sleep(ctx context.Context, d time.Duration) error
}

type Batch struct {                            // NEW — the ingest unit; F01/F02/F04 clock convention
    Tenant           TenantID
    Spans            []Span
    SourceFormat     SourceFormat
    ReceivedUnixNano uint64
    SourceAddr       string
    SizeBytes        uint32
}

type ServiceMeta struct {                       // NEW — gives CC-30's "tier-0" and PRD §5 a home
    Tenant    TenantID
    Service   string
    Tier      uint8     // 0 = unknown; 1 = most critical. Display/grouping only — NEVER a paging input (DR-21)
    Owners    []string
    SLOTargetMillis uint32
    Escalation string
    Source    string    // "resource_attribute" | "tenant_policy" | "api"
}

type DeployMarker struct { ID string; Tenant TenantID; Service, Version, PreviousVersion, Source string; At time.Time; RolloutFraction float64 /* [P2], unused in v1 */ }
```

Fields already in `01 §4.1` that feature docs dropped and **must** carry: `Span.Scope`, `Span.Flags`, `Span.TraceState`, `Span.SourceFormat`, `Span.ReceivedUnixNano`, `Span.SizeBytes`, `Span.DroppedAttrsCount/EventsCount/LinksCount`, and the helpers `Service()`, `DurationNanos()`, `IsError()`, `IsRoot()`.

**Attribute representation (PD-19).** `Span.Attrs` stays `AttrMap` on the control plane, but the OTLP decode hot path **must** build a sorted `[]KV` with interned keys and convert lazily:

```go
type KV struct { Key string; Val AttrValue }   // Key is interned via ingest.KeyInterner
func (s *Span) AttrSorted() []KV               // allocation-free view for the limiter and indexer
```

The NFR "zero allocation per span" is **deleted** and replaced by a measured budget (DR-28).

**Deletions (single definition survives):**

| Duplicate | Delete from | Canonical |
|---|---|---|
| `model.Span`, `model.Resource`, `AttributeValue` | `F01 §4.2` | `01 §4.1` |
| `sampler.Reason` (string enum) | `F02 §4.2`; map `ReasonForcedFlush → KeepShed` | `model.KeepReason` |
| `sampler.REDSample`, `anomaly.REDSample`, `store.SpanRollup` | `F02 §4.2`, `F05 §4.2`, `F03 §4.2` | `model.REDSample` (DR-39) |
| `anomaly.Event` / `anomaly.Incident` | `F05 §4.2` | `01 §4.3` |
| `memory.Record` | `F08 §4.2` | `01 §4.6` |
| `rca.Investigation` / `rca.Step` / `rca.Hypothesis` | `F06 §4.2` | `01 §4.4` (+ DR-15, DR-17 additions) |
| `remediate.Action` 6-state enum, `remediate.ActionType`, `ActionTarget`, `Params map[string]string` | `F09 §4.2` | `01 §4.5` + DR-22 |
| `store.TimeWindow`, `store.Window`, `topology.Window`, `correlate.TimeWindow` | everywhere | `model.Window` |
| `store.TopologyEdgeRow` | `F03 §4.2` | `topology.Edge` (DR-13) |
| `rca_investigations` / `rca_steps` tables | `F06 §4.2` | `01 §5.1` `investigation`/`investigation_step`/`evidence` |
| `anomaly_baselines` / `anomaly_events` / `anomaly_incidents` / `anomaly_incident_events` tables | `F05 §4.2` | `01 §5.1` `anomaly_event`/`incident` + `anomaly_baseline` (DR-14) |

**Docs to change:** `01 §4.1`, `F01 §4.2`, `F02 §4.2`, `F03 §4.2`, `F05 §4.2`, `F06 §4.2`, `F08 §4.2`, `F09 §4.2`, `02 §1`–`§4`, `00`.

---

## DR-5 — Tenant propagation: `model.TenantID` is a compile-time-required parameter

**Resolves:** SR-6, SR-21(a), PD-35 (tenant half), CC-33(2), SR-13(c).

**Decision.**

1. **Signature rule (binding).** Every exported method on `store`, `store/sqlite`, `store/parquet`, `store/blob`, `memory`, `correlate`, `topology`, `anomaly`, `rca` (including every `rca.Tool`), `remediate`, `sampler` (registry operations), `nl` and `eval` that touches tenant-scoped data takes, in this exact order:
   ```go
   func (x *T) Method(ctx context.Context, tid model.TenantID, ...) (..., error)
   ```
   `ctx` first, `model.TenantID` second. No exceptions. `internal/archtest` fails the build on any exported method in those packages whose second parameter is not `model.TenantID` (allowlist file for the genuinely global ones: `Health`, `Close`, `Start`, `Stop`, `Kind`, `Name`, `Schema`, `Stats`).

2. **Resolution rule (binding).** The tenant is derived from the authenticated principal, and from nothing else:
   - `auth.Subject.Tenant` → `tenant.Resolver.FromSubject` → the `model.TenantID` parameter.
   - **Delete** the `tenant` field from `F10`'s `POST /v1/nl/query` body. A body or query tenant is a `400 tenant_not_accepted`, never an override.
   - **Delete** the `X-TraceIQ-Tenant` header fallback from `X-OPS §4.4`. Absent an authenticated principal the answer is `401`, in every mode except `server.profile: dev` **and** loopback **and** `auth.mode: none`, where `tenant.Resolver.FromDevDefault` returns `tenancy.default_tenant`.
   - `tenancy.header` is **removed from `01 §7`**.

3. **Schema rule (binding).** Every tenant-scoped table carries `tenant_id` as the **first** primary-key column. `F06`'s investigation tables are deleted (DR-4); `F08`'s tables gain `tenant_id`. Memory fingerprints are **never** matched cross-tenant.

4. **Verification (new ACs in Appendix C):** an `internal/archtest` AST check; a cross-tenant contract test covering **100%** of the methods above; a test that a tenant-A token supplying `{"tenant":"B"}` is answered as A; a test that a webhook with `X-TraceIQ-Tenant: B` and no principal returns 401.

**Docs to change:** `00` (core interface list), `01 §3.2`, `01 §4`, `01 §5.1`, `01 §6.1`, `01 §7` (remove `tenancy.header`), `02 §1`–`§4`, `F03 §4.3`, `F04 §4.3`, `F05 §4.3`, `F06 §4.3`, `F07 §4.3`, `F08 §4.2`/`§4.3`, `F09 §4.3`, `F10 §4.3`, `F11 §4.3`, `X-SEC §4.3`, `X-OPS §4.3`/`§4.4`.

---

## DR-6 — Hot index: span rows, an attribute index, corrected sizing, two SQLite files

**Resolves:** PD-1, PD-2, PD-3, PD-17, PD-24, PD-29, CC-9, CC-25. **Closes the D-T1 gate.**

### 6.1 `HotIndex` — canonical interface (replaces `F03 §4.3`)

Batch-oriented, insert-only, append-only rollups with read-time aggregation, so the same contract is implementable on SQLite now and ClickHouse in Phase 2.

```go
package store

type HotIndex interface {
    // --- writes: exactly one batch call per flush, never per row ---
    WriteBatch(ctx context.Context, tid model.TenantID, b HotBatch) (BatchReceipt, error)
    BindColdBlock(ctx context.Context, tid model.TenantID, walSegment string, m BlockManifest) (rowsBound int, err error)

    // --- reads ---
    GetTrace(ctx context.Context, tid model.TenantID, id model.TraceID) (TraceIndex, error)
    SearchSpans(ctx context.Context, tid model.TenantID, q SpanQuery) (SpanPage, error)
    SearchTraces(ctx context.Context, tid model.TenantID, q TraceQuery) (TracePage, error)
    QueryRED(ctx context.Context, tid model.TenantID, service, operation string, w model.Window) (REDSeries, error)
    QueryEdges(ctx context.Context, tid model.TenantID, w model.Window) ([]topology.Edge, error)
    QueryErrorSignatures(ctx context.Context, tid model.TenantID, service string, w model.Window) ([]ErrorSignature, error)
    ExemplarsFor(ctx context.Context, tid model.TenantID, service, operation string, w model.Window, n int) ([]Exemplar, error)
    PathSeen(ctx context.Context, tid model.TenantID, sig uint64, lookback time.Duration) (time.Time, bool, error)
    IngestedBytes(ctx context.Context, tid model.TenantID, w model.Window) (int64, error)
    PendingColdRows(ctx context.Context, tid model.TenantID, olderThan time.Time) ([]PendingCold, error)

    // --- retention ---
    ExpireBefore(ctx context.Context, tid model.TenantID, tbl TableID, cutoff time.Time) (Expired, error)
    CascadeRED(ctx context.Context, tid model.TenantID, from, to Resolution, before time.Time) (int, error)
    DiskUsage(ctx context.Context) (DiskReport, error)

    Capabilities() HotCapabilities
    Health(ctx context.Context) HealthReport
    Close() error
}

type HotBatch struct {
    Traces    []TraceIndex
    Spans     []SpanIndex
    AttrRows  []AttrIndexRow
    AttrDict  []AttrDictRow
    FTSRows   []FTSRow        // only for traces whose KeepReason is Error/Slow/Rare/Interest
    RED       []model.REDSample
    Edges     []topology.Edge
    EdgeOps   []EdgeOpRow
    ErrorSigs []ErrorSignature
    PathSigs  []PathSigRow
    Exemplars []Exemplar
    Resources []ResourceRow
}
type BatchReceipt struct { Rows int; Bytes int64; CommitMillis int64; WALSegments []string }

// HotCapabilities lets store/tiered state per-backend semantics instead of assuming SQLite.
type HotCapabilities struct {
    ReadYourWrites   bool   // sqlite: true; clickhouse ReplacingMergeTree: false
    RowLevelDelete   bool   // sqlite: true; clickhouse: false (partition drop only)
    FullTextSearch   bool   // sqlite fts5: true
    MaxKeptSpansPerSec int  // sqlite (dev box): 1200; clickhouse [P2]: 60000
}
```

`UpsertRollup`, `UpsertErrorSignature`, `UpsertTopologyEdge`, `RecordExemplar`, `PutColdPointer`, `SearchAttributes`, `LookupColdPointer`, `IngestedBytes24h`, `PointersExpiredBefore`, `HasPointerFor` are **all deleted** from `F03 §4.3`. `store.ColdPointer` is deleted (DR-7).

`store.BlobStore` is **renamed `store.ObjectStore`** (02's name, F03's method set):

```go
type ObjectStore interface {
    Put(ctx context.Context, key string, r io.Reader, size int64) (ObjectInfo, error)
    Get(ctx context.Context, key string) (io.ReadCloser, error)
    GetRange(ctx context.Context, key string, offset, length int64) (io.ReadCloser, error)
    Delete(ctx context.Context, keys []string) error
    List(ctx context.Context, prefix string, after string, limit int) ([]ObjectInfo, string, error)
    Kind() string
}
```

### 6.2 Schema changes (amend `01 §5.1`)

**Two SQLite files, two writer goroutines** (PD-17). This is the fix for "a fail-closed audit write queued behind a 250 ms trace batch".

| File | Tables | Writer | PRAGMAs |
|---|---|---|---|
| `${data_dir}/hot/traceiq.db` | `trace`, `span`, `attr_index`, `attr_dict`, `span_text_fts`, `resource`, `red_rollup`, `red_rollup_5m`, `red_rollup_1h`, `error_signature`, `topology_edge`, `topology_edge_op`, `topology_edge_meta`, `path_signature`, `block_manifest`, `cold_tombstone`, `anomaly_baseline` | **1** telemetry writer | `journal_mode=WAL`, `synchronous=NORMAL`, `foreign_keys=ON`, `busy_timeout=5000`, `page_size=8192`, `cache_size=-262144`, `mmap_size=268435456`, `temp_store=MEMORY`, `wal_autocheckpoint=4000` |
| `${data_dir}/control/control.db` | `tenant`, `api_token`, `identity_binding`, `audit_log`, `audit_anchor`, `action`, `investigation`, `investigation_step`, `evidence`, `memory_record`, `memory_fp_token`, `memory_fts`, `anomaly_event`, `incident`, `deploy_marker`, `sampler_interest`, `eval_run`, `eval_result`, `idempotency` | **1** control writer | as above but **`synchronous=FULL`** and `wal_autocheckpoint=1000` |

Both files are opened by the same process; `store.TieredStore` routes by table. Cross-file references (`incident.investigation_id`, `trace.block_id`) are plain columns with application-level integrity, **not** SQL foreign keys.

**Writer budget (published, PD-17):**

| Writer | Target | Gate |
|---|---|---|
| Telemetry | ≥ 2 000 committed rows/s, ≥ 20 tx/s, p99 commit ≤ 120 ms at the dev reference load | FU-3 |
| Control | ≥ 200 tx/s, **p99 audit append ≤ 15 ms, independent of `store.hot.sqlite.batch_interval`** | AC-XSEC-13 |

**New / changed DDL:**

```sql
-- trace: FK on block_id DROPPED (DR-7); cold_state + wal_segment added.
ALTER ... trace:
  block_id       TEXT,                         -- no REFERENCES; NULL until seal
  row_group      INTEGER,
  row_offset     INTEGER,
  cold_state     INTEGER NOT NULL DEFAULT 0,   -- 0 pending, 1 sealed, 2 rollup_only, 3 lost
  wal_segment    TEXT,
  keep_reason    INTEGER NOT NULL              -- model.KeepReason
CREATE INDEX trace_by_coldstate ON trace(tenant_id, cold_state, wal_segment) WHERE cold_state = 0;

-- attr_index: key dictionary + value hash, one index (the PK). ~55 bytes/row.
DROP TABLE attr_index;  -- the 01 §5.1 shape is replaced
CREATE TABLE attr_index (
  tenant_id   INTEGER NOT NULL,   -- tenant dictionary id
  key_id      INTEGER NOT NULL,   -- index into store.hot.indexed_attribute_keys (0..7)
  value_hash  INTEGER NOT NULL,   -- xxh3(normalized value) as int64
  bucket      INTEGER NOT NULL,   -- start_unix_nano / 10e9, 10 s bucket
  trace_id    BLOB    NOT NULL,
  span_id     BLOB    NOT NULL,
  PRIMARY KEY (tenant_id, key_id, value_hash, bucket, trace_id, span_id)
) WITHOUT ROWID;

CREATE TABLE attr_dict (
  tenant_id  INTEGER NOT NULL,
  key_id     INTEGER NOT NULL,
  value_hash INTEGER NOT NULL,
  value      TEXT    NOT NULL,     -- normalized, <= 128 bytes; for exact verification + UI
  first_seen INTEGER NOT NULL,
  last_seen  INTEGER NOT NULL,
  PRIMARY KEY (tenant_id, key_id, value_hash)
) WITHOUT ROWID;

-- span_text_fts: written ONLY for spans of anomalous traces (keep_reason in 1..4).
-- The always-on path does not write FTS rows.

CREATE TABLE anomaly_baseline (      -- DR-14; replaces F05's anomaly_baselines
  tenant_id TEXT NOT NULL, service TEXT NOT NULL, operation TEXT NOT NULL,
  bucket    INTEGER NOT NULL,        -- 0..23 hour-of-day; 24..30 weekday multiplier slots
  p50_nanos INTEGER NOT NULL, p95_nanos INTEGER NOT NULL, p99_nanos INTEGER NOT NULL,
  error_ewma REAL NOT NULL, rps_ewma REAL NOT NULL, samples INTEGER NOT NULL,
  digest BLOB,                       -- only for bucket = 255 (the global digest), <= 512 bytes
  updated_at INTEGER NOT NULL,
  PRIMARY KEY (tenant_id, service, operation, bucket)
) WITHOUT ROWID;
```

**`store.hot.indexed_attribute_keys` is reduced from 14 to 8** (the ones an incident search actually uses), and is a **closed allowlist** — a query on a key outside it is answered from the cold tier and is explicitly outside the 250 ms gate:

```yaml
store:
  hot:
    indexed_attribute_keys:          # exactly 8; key_id is the list index, order is schema
      - http.response.status_code    # 0
      - http.route                   # 1
      - error.type                   # 2
      - rpc.method                   # 3
      - db.system.name               # 4
      - peer.service                 # 5
      - k8s.pod.name                 # 6
      - service.version              # 7
    index_attrs_for_kinds: [2, 5]    # Server, Consumer — plus every span with IsError()
    max_indexed_attr_value_bytes: 128
    max_path_signatures: 200000      # LRU evict, counter on evict
    max_kept_spans_per_sec: 1200     # startup error if the sampler's derived rate exceeds it
                                     # on driver: sqlite (see DR-32)
```

### 6.3 Covering index per listed query (CC-9, PD-3)

| Query | SQL predicate | Index used |
|---|---|---|
| `SearchSpans` by indexed attribute + time | `attr_index` on `(tenant_id, key_id, value_hash, bucket)` then join `span` on PK | `attr_index` PK (covering: returns `trace_id, span_id` with no table access) |
| `SearchSpans` by service+operation+time | `span(tenant_id, service, operation, start_unix_nano DESC)` | `span_by_svc_op_time` |
| `SearchSpans` by error signature | `span(tenant_id, error_sig_id, start_unix_nano DESC) WHERE error_sig_id IS NOT NULL` | `span_by_errsig` |
| `SearchSpans` free text | `span_text_fts MATCH ?` → `(trace_id, span_id)` → `span` PK | FTS5 (anomalous traces only) |
| `SearchTraces` by time | `trace(tenant_id, start_unix_nano DESC)` | `trace_by_time` |
| `SearchTraces` by service + duration | `trace(tenant_id, root_service, duration_nanos DESC)` | `trace_by_dur` |
| `SearchTraces` errors only | partial index `WHERE error_count > 0` | `trace_by_err` |
| `SearchTraces` by path signature | `trace(tenant_id, path_signature, start_unix_nano DESC)` | `trace_by_path` |
| `GetTrace` | `trace` PK, then `span(tenant_id, trace_id, *)` prefix | PK scans only |
| `QueryRED` | `red_rollup` PK prefix `(tenant_id, service, operation, bucket_start)` | PK (covering) |
| `QueryEdges(window)` | `topology_edge(tenant_id, bucket_start DESC)` | `edge_by_time` |
| Cold pointer back-fill | `trace(tenant_id, cold_state, wal_segment) WHERE cold_state = 0` | `trace_by_coldstate` |
| Retention sweep | per-table `(tenant_id, <time col>)` ranged DELETE in 5 000-row batches | existing time indexes |

### 6.4 Published sizing model (replaces `01 §5.1`'s "Expected hot-index size")

**Derived bytes per row**, from the DDL above (base record + every index entry that row creates):

| Table | Base | Index overhead | **All-in bytes/row** |
|---|---:|---:|---:|
| `span` | 330 | 180 (3 indexes) | **510** |
| `attr_index` | 55 | 0 (PK only) | **55** |
| `attr_dict` | 190 | 0 | 190 (bounded by distinct values, not spans) |
| `span_text_fts` | 120 | — | **120** |
| `trace` | 180 | 240 (5 indexes) | **420** |
| `red_rollup` (10 s) | 95 | 15 | **110** |
| `red_rollup_1h` (with ≤ 512 B digest) | 600 | 15 | **615** |
| `topology_edge` (10 s / 5 m) | 80 | 10 | **90** |
| `topology_edge` (1 h, with digest) | 290 | 10 | **300** |

**Per-kept-span multipliers**, measured assumptions stated explicitly:
- `attr_index` rows per kept span = **3.0** (8-key allowlist × ~37 % presence on Server/Consumer/error spans only).
- `span_text_fts` rows per kept span = **0.4** (only traces with `KeepReason ∈ {Error, Slow, Rare, Interest}`).
- ⇒ **cost per kept span = 510 + 3.0×55 + 0.4×120 = 723 B.** Round to **750 B/kept span**.

**Profile `dev` (the MVP reference, and the only profile `driver: sqlite` supports):**

| Assumption | Value |
|---|---|
| Ingest | 2 000 spans/s |
| Effective keep rate | 4 % (`healthy_sample_rate: 0.01` + error/slow/rare/interest) |
| Kept spans/s | 80 |
| Spans per trace | 10 → 8 kept traces/s |

| Table | Rows | Retention | **GiB** |
|---|---:|---|---:|
| `span` + `attr_index` + `span_text_fts` | 6.91 M kept spans/day | 24 h (`hot_span_rows`) | **4.83** |
| `trace` | 691 k/day | 7 d (`hot_trace_rows`) | **1.89** |
| `red_rollup` 10 s (200 svc-op pairs) | 1.73 M/day | 48 h | 0.35 |
| `red_rollup_5m` | 57.6 k/day | 14 d | 0.08 |
| `red_rollup_1h` | 4.8 k/day | 400 d | 1.10 |
| `topology_edge` 10 s (600 edges) | 5.18 M/day | 6 h | 0.11 |
| `topology_edge` 5 m | 173 k/day | 7 d | 0.10 |
| `topology_edge` 1 h + `topology_edge_op` | 14 k/day | 30 d | 0.13 |
| `error_signature`, `path_signature`, `resource`, `attr_dict` | cardinality-capped | 30 d | 0.35 |
| `control.db` (incidents, investigations, memory, audit, actions) | — | 400 d / 2555 d | 0.40 |
| **Hot total** | | | **≈ 9.34 GiB** |
| Cold Parquet (anomalous 30 d + sampled 7 d, ≥ 8× zstd) | 442 MB/day raw-equivalent | T2/T3 | **≈ 6.7** |
| **Grand total** | | | **≈ 16.1 GiB** |

`store.budget.max_disk_bytes` default is therefore **26 843 545 600 (25 GiB)** with `high_watermark: 0.85` (21.25 GiB) — 32 % headroom over the model. The published total and `max_disk_bytes` must be re-derived whenever a retention default changes; the derivation lives in `01 §5.1` and nowhere else.

**`store_hot_index_ratio` (CC-25):** defined **once**, in `01 §10.2`, as `hot_bytes_on_disk / raw_ingested_span_bytes` over the same window — the denominator is **raw ingested**, not kept. At the dev profile: 9.34 GiB hot against 2 000 × 512 B × 86 400 = 82.4 GB/day raw, i.e. **~2.4 % over a 24 h window** — inside the published 2–5 % band. `F03 FR-F03-7` is rewritten to cite this definition; the metric's Prometheus help text states the denominator verbatim.

**Profile `prod` [P2]:** 20 000 spans/s at 4 % keep and 72 h span retention = 207 M span rows × 750 B = **155 GB**, which **exceeds what a single pure-Go SQLite writer can serve**. `01 §7` therefore gains a startup validation rule: *if the derived kept-span rate exceeds `store.hot.max_kept_spans_per_sec` and `store.hot.driver == "sqlite"`, exit 2 naming both keys.* This is the honest answer to PD-2's option (c): ClickHouse is not "promoted to default", it is **required above a stated, measured line**.

### 6.5 Durability (PD-29)

The claim is stated precisely and matched to the test: *"Durable across process crash (`kill -9`). Up to one WAL group-commit may be lost on OS crash or power loss on `traceiq.db` (`synchronous=NORMAL`); `control.db` is durable across power loss (`synchronous=FULL`)."* `F03 §3.2`'s "WAL mode with fsync" wording is deleted. `AC-F03-1` is split into a process-kill case and a power-loss case with the two different guarantees.

Backup (`X-OPS FR-XOPS-5`) is re-specified as **`VACUUM INTO` snapshots at `ops.backup.interval` plus continuous cold-WAL shipping**, with the RPO **recomputed from the actual database size** (dev profile: a 9 GiB `VACUUM INTO` every 5 min is not feasible; the dev default becomes `interval: 6h` for `traceiq.db` and `5m` for `control.db`, which is the file that actually holds irreplaceable state). The "< 10 % storage overhead via incremental WAL shipping" claim is **deleted** — `wal_autocheckpoint` makes naive WAL shipping unsound, and the doc must say so.

**Docs to change:** `01 §5.1` (DDL, PRAGMAs, two files, sizing model), `01 §7` (`store.hot.*` keys above, `store.budget.max_disk_bytes`, `store.retention.hot_trace_rows`), `01 §10.1`/`§10.2` (`store_hot_index_ratio`, SearchSpans gate), `05 §D-3` (rewrite the rationale: the hot index **does** do per-span writes, at a stated and benchmarked rate), `05 §9` FU-3, `F03 §3.1`/`§3.2`/`§4.2`/`§4.3`/`§7`, `X-OPS §3.1` FR-XOPS-5, `02 §1`.

---

## DR-7 — Cold store: one write model, one crash matrix, tombstones for per-trace erasure

**Resolves:** SR-7, PD-4, PD-5, PD-23, CC-33(3).

**Decision — the model.** Per-trace Parquet **rows** are appended to **time-bucketed blocks** that seal on size or time. There is no per-trace row group and no `ColdPointer`.

```go
package store

type ColdTier uint8
const ( ColdAnomalous ColdTier = 1; ColdSampled ColdTier = 2 )

type ColdStore interface {
    // Append writes the trace's spans into the open block for (tenant, tier, hour bucket).
    // It returns only after the write-ahead journal record is group-fsynced.
    Append(ctx context.Context, tid model.TenantID, t model.Trace, tier ColdTier) (WALRef, error)
    // Seal closes a block, writes spans.parquet/traces.parquet/meta.json, verifies the
    // checksum and returns the manifest. The caller commits the manifest row and then
    // calls HotIndex.BindColdBlock with the same WAL segment id.
    Seal(ctx context.Context, blockID string) (BlockManifest, error)
    SealDue(ctx context.Context, now time.Time) ([]string, error)
    ReadTrace(ctx context.Context, tid model.TenantID, id model.TraceID, loc TraceLoc) (model.Trace, error)
    ReadFromWAL(ctx context.Context, tid model.TenantID, id model.TraceID, ref WALRef) (model.Trace, error)
    ReplayWAL(ctx context.Context) (ReplayReport, error)
    ExpireBlocks(ctx context.Context, before time.Time, tier ColdTier) ([]string, error)
    Tombstone(ctx context.Context, tid model.TenantID, ids []model.TraceID, reason string) error
    Compact(ctx context.Context, level int, b CompactBudget) (CompactReport, error)
    Health(ctx context.Context) HealthReport
}

type WALRef struct { Segment string; Offset int64; Length int32; CRC32C uint32 }

// TraceLoc is resolved from the hot index; it is never returned synchronously by Append.
type TraceLoc struct { BlockID string; RowGroup int32; RowOffset int64; ColdState uint8; WAL WALRef }
```

**Decision — the ordering, and the invariant.**

1. `ColdStore.Append` writes the span bytes to the **cold write-ahead journal** (`store.cold.wal_dir`) and returns only after a group fsync (`cold.wal_fsync_interval: 250ms` or `cold.wal_fsync_bytes: 4MiB`, whichever first) with a CRC32C per record.
2. The hot-index `trace` row is committed **in the same batch transaction** with `cold_state = 0 (pending)`, `wal_segment` set, `block_id = NULL`. **This is what makes the trace searchable immediately** — an appended trace resolves through `ReadFromWAL`, never a 404.
3. The receiver acks only after (1) and (2).
4. The block seals at `row_group_bytes` / `target_block_bytes` / `flush_interval` (dev: 32 MiB / 128 MiB / 5 m). `Seal` writes the objects, verifies the checksum, then the **manifest row is committed**, and only then does `HotIndex.BindColdBlock` run a single ranged `UPDATE trace SET block_id=?, row_group=?, row_offset=?, cold_state=1 WHERE tenant_id=? AND cold_state=0 AND wal_segment=?`, served by `trace_by_coldstate`.
5. WAL segments are retained until `seal + store.cold.wal_retain` (default 10 m).

> **Refinement of the brief.** The instruction "hot rows written only after block seal commit" is adopted as: *the `block_id` **pointer** is written only after the manifest row commits.* The `trace` row itself must exist at ack, or SR-7's own acceptance criterion ("a trace queried immediately after ack resolves via WAL/open block, not a 404") cannot hold and the 12 s ingest-to-queryable NFR is unreachable.

**The invariant, stated once in `01 §5.2` and cited by `F03 §5`:**

> A hot-index trace row may be `pending` and reference only a WAL segment. It may **never** carry a `block_id` for a manifest row that does not exist, and a manifest row may never exist for an object that is absent or checksum-mismatched.

**Crash matrix (both directions), which `AC-F03-1` must exercise:**

| Kill point | On restart |
|---|---|
| After WAL fsync, before hot commit | `ReplayWAL` finds records with no `trace` row, re-appends them to a new open block; the trace becomes searchable. `traceiq_cold_wal_replayed_total` |
| After hot commit, before seal | `trace` rows are `pending` with a live WAL segment; `ReplayWAL` re-appends, re-seals, `BindColdBlock` back-fills. **Zero** rows point at a missing block |
| Mid-block (partial object) | Object has no manifest row, deleted as an orphan by manifest-driven reconciliation. WAL segment still present, replayed |
| After seal object write, before manifest commit | Orphan object, no pointer. Reconciled within one `reaper_interval` |
| After manifest commit, before `BindColdBlock` | Manifest exists, rows still `pending` with a live WAL segment; `BindColdBlock` is idempotent and re-runs on startup |
| Manifest row exists, object missing or corrupt | Manifest marked `state='orphaned'`; dependent rows revert to `pending` if the WAL segment survives, else `cold_state=3 (lost)` + `traceiq_cold_bodies_lost_total` + a `Critical` incident |

**Achievable durability statement (corrects `AC-F03-1`):** *"Span bodies are durable once `Append` returns: loss is bounded by one cold-WAL group commit (`≤ cold.wal_fsync_interval`, default 250 ms) on power loss, and is **zero** on process kill."*

**Decision — deletion and retention.**

- Per-trace deletion from an immutable Parquet block is impossible and is **deleted from `F03 §4.4`**. Expiry is **block-scoped**, by manifest: `ExpireBlocks` deletes whole blocks whose `expires_at` has passed. `expires_at = sealed_at + retention(tier)`; blocks are written per `(tenant, tier, hour)` so a block never mixes tiers and the "mixed expiry" case cannot arise.
- **Targeted single-trace erasure** (right-to-erasure — the case `coldStore.Delete(ptr)` was trying to serve) uses tombstones:

```sql
CREATE TABLE cold_tombstone (
  tenant_id TEXT NOT NULL, trace_id BLOB NOT NULL, block_id TEXT NOT NULL,
  requested_at INTEGER NOT NULL, requested_by TEXT NOT NULL, reason TEXT NOT NULL,
  purged_at INTEGER,
  PRIMARY KEY (tenant_id, trace_id)
) WITHOUT ROWID;
CREATE INDEX tombstone_by_block ON cold_tombstone(block_id) WHERE purged_at IS NULL;
```

  `Tombstone()` writes the row, deletes the hot `trace`/`span`/`attr_index`/FTS rows immediately, and `ReadTrace` filters tombstoned IDs. The compactor rewrites any block where `unpurged_tombstones / trace_count >= store.cold.compaction.tombstone_ratio` (0.02) **or** whose oldest unpurged tombstone exceeds `store.cold.compaction.max_tombstone_age` (24 h). **Erasure SLA ≤ 24 h**, reported by `GET /v1/tenants/{id}/erasure`.
- **Compaction** (absent from `F03` entirely): `L0` = sealed blocks; `L1` = hourly merge of same-`(tenant, tier, hour)` L0 blocks to a 512 MiB target; `L2` = daily merge **[P2]**. Triggers: ≥ 8 L0 blocks in one bucket, or a tombstone trigger. Write-amplification budget **≤ 1.5×** the bytes ingest writes per day, bounded by `store.cold.compaction.max_bytes_per_hour`, reported as `traceiq_cold_compaction_write_amplification`.
- **Orphan reconciliation** is manifest-driven over a bounded window, never a full prefix listing. Each `reaper_interval` lists only objects sealed within `store.cold.orphan_grace` (2 h) and diffs against `block_manifest`. `ReconcileOrphans`'s `List(prefix="")` is **deleted**. `FR-F03-14` (new, closes CC-33(3)): *orphaned cold objects < 0.1 % of sealed blocks; every orphan is deleted within one `reaper_interval`*, with `AC-F03-14`.

**Retention tiers (replaces `01 §5.3`; dev defaults):**

| Tier | What | Where | dev | prod [P2] | Key |
|---|---|---|---|---|---|
| T0 | Span rows + `attr_index` + FTS | `traceiq.db` | **24 h** | 72 h | `store.retention.hot_span_rows` |
| T0b | Trace index rows | `traceiq.db` | **7 d** | 30 d | `store.retention.hot_trace_rows` *(new)* |
| T1 | Error/path signatures, edge metadata | `traceiq.db` | 30 d | 30 d | `store.retention.hot_rollups` |
| T2 | Anomalous trace bodies | Parquet `anomalous` | 30 d | 30 d | `store.retention.cold_anomalous` |
| T3 | Healthy sampled bodies | Parquet `sampled` | 7 d | 7 d | `store.retention.cold_sampled` |
| T4 | RED 10 s / 5 m / 1 h | `traceiq.db` | **48 h / 14 d / 400 d** | same | `store.retention.red_10s`, `red_5m`, `red_rollups` *(two new)* |
| T4b | Topology 10 s / 5 m / 1 h | `traceiq.db` | **6 h / 7 d / 30 d** | same | `store.retention.topology_10s`, `topology_5m`, `topology_1h` *(new)* |
| T5 | Incidents, investigations, steps, evidence, memory | `control.db` | 400 d | 400 d | `store.retention.investigations` |
| T6 | Audit | `control.db` + NDJSON | 2555 d | 2555 d | `store.retention.audit` |

**Docs to change:** `01 §3.1` (Cold-write row), `01 §5.2` (invariant + crash matrix), `01 §5.3` (table above), `01 §7` (`store.cold.*`, new `store.retention.*` keys), `03` diagram 1, `04 §1 L4`, `04 §5.2`, `F03 §3.1` FR-F03-1, `§3.2`, `§4.2` (delete `ColdPointer`), `§4.3`, `§4.4`, `§5`, `§7` (AC-F03-1 split into process-kill and power-loss cases; AC-F03-5 rewritten to block granularity; AC-F03-14 new), `§8` (mark open question 3 **Decided (round 1)**).

---

## DR-8 — Sharding: rendezvous hashing, drain-then-move, RED counted exactly once

**Resolves:** PD-6, CC-32. **Corrects** the `< 0.1 %` rebalance NFR.

**Decision — one routing contract, stated once in `01 §3.2`, cited everywhere else.**

```go
package sampler

type MemberID string
type RingState uint8
const ( RingStable RingState = 1; RingDraining RingState = 2 )

type Ring struct {
    Epoch   uint64
    Members []MemberID // sorted, immutable; len == shard count
    State   RingState
}

// ShardFor is the ONLY routing function in the system. Rendezvous (HRW) hashing.
func ShardFor(r Ring, id model.TraceID) int   // argmax_i xxh3(Members[i] || id[:])
```

- `fnv64a(TraceID) mod N` (`04 §1 L2`, `01 §3.2`) is **deleted**.
- Kafka **[P2]**: a **custom partitioner** computes `partition = ShardFor(ring, traceID) mod partitions`. `01 §3.2`'s "Kafka partition key = TraceID" becomes *"partition = HRW(TraceID) over a fixed partition set"*. `cluster.bus.partitions` is fixed at 32; `sampler.shards <= cluster.bus.partitions`; a resize reassigns partitions between consumers and never changes the partition count. `ShardFor` and the partitioner therefore agree on 100 % of assignments at equal counts (closes CC-32 and F02's Determinism NFR).
- `X-OPS FR-XOPS-2`'s "consistent-hash ring" is reworded to cite `ShardFor`; the StatefulSet's stable pod identity **is** the `MemberID`.

**Decision — the resize protocol (drain, do not re-route).**

| Step | Rule |
|---|---|
| 1 | The coordinator publishes `Ring{Epoch: e+1, State: RingDraining}`. Both rings are live |
| 2 | A span whose `TraceID` is **already open** routes to its epoch-`e` owner, regardless of `ShardFor(e+1, id)`. Ownership of an open trace never moves |
| 3 | A span whose `TraceID` is **not open anywhere** routes under epoch `e+1` |
| 4 | Epoch `e` retires when every member reports `openUnderEpoch(e) == 0`, or after `assembly.hard_timeout` (30 s), whichever is first; then `State: RingStable` |
| 5 | A trace surviving step 4 is force-finalized by its epoch-`e` owner with `KeepReason = KeepShed`, counted in `traceiq_sampler_rebalance_splits_total` |

**Decision — RED is never double-counted.** `model.REDSample` gains `ShardEpoch uint64` and `TraceID model.TraceID` (DR-39). The RED write path deduplicates on `(tenant, epoch, trace_id, service, operation, bucket_start)` through an in-memory ring of `sampler.red.dedupe_window` (200 000) entries with a `2 × assembly.hard_timeout` TTL; the `red_rollup` merge is a no-op on a duplicate key. A split trace contributes to RED **exactly once**.

**Corrected NFR (replaces `F02 §3.2 Rebalance`):**

> During a membership change the fraction of the trace-ID key space whose owner changes is `1/(N+1)` — at N = 10, ~9 %, **not** 0.1 %. The drain protocol makes the fraction of traces **decided twice** exactly **0**, and the fraction force-finalized across the transition (`rebalance_splits`) ≤ **0.01 %** of traces over the transition window. Aggregate RED after a resize is within **0.5 %** of a single-shard reference run.

**Docs to change:** `01 §3.2`, `02 §1` (`route` → `ShardFor`), `04 §1 L2`, `F02 §3.1` FR-F02-1, `§3.2`, `§4.3`, `§4.4`, `§5`, `§7` (AC-F02-1, AC-F02-5 extended to a mid-flight resize), `§8` (**Decided (round 1)**), `X-OPS §3.1` FR-XOPS-2.

---

## DR-9 — Assembly: one memory budget, one timeout pair, a spill WAL, RED before every discard

**Resolves:** PD-7, PD-22, PD-25, SR-9, CC-22, PD-8 (write-path half).

**Decision — memory.** The assembly byte budget is **global and centrally enforced** as one atomic counter. `sampler.buffer.max_bytes_per_shard` is **deleted**.

```yaml
sampler:
  shards: 0                          # 0 = GOMAXPROCS
  shard_queue_size: 4096
  assembly:
    idle_timeout: 8s                 # canonical; F02's 10s is deleted
    hard_timeout: 30s                # canonical; F02's 60s is deleted
    max_open_traces_per_shard: 50000
    memory_high_watermark_bytes: 536870912   # 512 MiB GLOBAL across all shards
    wheel_tick: 250ms
  wal:
    enabled: true
    dir: ${data_dir}/sampler/wal
    flush_interval: 1s
    max_segment_bytes: 67108864      # 64 MiB
```

**Published RSS derivation (replaces the bare "≤ 1.5 GiB" assertion, PD-7):**

| Component | dev profile |
|---|---:|
| Assembly buffers (global watermark) | 512 MiB |
| SQLite page cache (`traceiq.db` 256 + `control.db` 32) | 288 MiB |
| Parquet writer buffers (`max_open_blocks: 2` × 32 MiB row group) | 64 MiB |
| Anomaly baselines (DR-14 model) | 120 MiB |
| RCA heap (2 concurrent × 96 MiB) | 192 MiB |
| Topology graph at `max_edges: 20000` (DR-13) | 80 MiB |
| Memory index: `corpusIDF` + posting lists (DR-19) | 64 MiB |
| Go runtime, channels, HTTP pool, mmap slack | 180 MiB |
| **Total** | **1 500 MiB = 1.46 GiB** |

`01 §10.2`'s `≤ 1.5 GiB` stands **only because** these are the values; changing any one requires re-publishing the sum. `store.cold.parquet.max_open_blocks` drops 4 → **2** and `row_group_bytes` to **33554432 (32 MiB)** on the dev profile — that is what makes the sum close.

**Decision — the spill WAL (closes D-Z3; SR-9 / CC-22 option (a)).**

- On `Consume`, raw span bytes are appended to the shard's WAL segment; a group fsync runs every `sampler.wal.flush_interval` (1 s).
- On decision emit, the trace's records are logically truncated; a segment is deleted when fully consumed.
- On start, `sampler.ReplayWAL` re-assembles every undecided trace **before receivers bind**.
- **FR-F02-9 (rewritten):** *"On graceful shutdown, in-flight trace loss is **0**. On crash, loss is bounded by one WAL flush interval (default 1 s) and is counted exactly in `traceiq_sampler_traces_lost_total`."* `01 §10.3`'s "span loss = 0 under normal operation" is retained and now true; `X-OPS §3.2`'s rolling-upgrade claim is retained; `ADR D-5`'s D-Z3 claim is retained and is now implemented.
- Shard panic (`04 §5.2`) no longer loses the shard's traces: the restarted shard replays its segment.
- `AC-F02-11` (new): `kill -9` mid-assembly, restart, assert every span acked more than one flush interval before the kill appears in a decision, and that `traceiq_sampler_traces_lost_total` equals the measured loss exactly.

**Decision — RED before every discard (PD-25). Three holes closed.**

1. **Router drops.** RED is extracted **per span at the router**, before `shardCh`, into a per-`(tenant, service, operation, 10 s bucket)` accumulator owned by the decode worker. A span dropped on `shard_full` has already contributed calls, errors and duration. The shard later stamps `Kept` and exemplar trace IDs as a separate additive update. `04 §1 L2`'s claim now has a mechanism.
2. **Span cap.** Spans beyond `max_spans_per_trace` are **still RED-extracted at the router**; only their bodies are dropped. `Trace.Truncated = true`, `KeepReason = KeepTruncated`.
3. **Duplicates.** `partialTrace` holds `map[model.SpanID]struct{}`; a duplicate `SpanID` is dropped before both assembly and RED, counted in `traceiq_ingest_spans_duplicate_total`. Cost: 8 B per span ≈ 80 B per 10-span trace, ≤ 4 MiB at 50 000 open traces per shard — inside the global watermark. This is what makes `F01 §3.2`'s at-least-once claim safe.

`AC-F02-5` gains three adversarial cases (forced `shard_full` drops, an over-cap trace, 10 % duplicated spans); all must stay inside 0.5 % on calls/errors and 2 % on p99.

**Decision — finalize (PD-22).** `FinalizeTick`'s full scan is **deleted**. `sampler.TimerWheel` is normative: 256 slots × 250 ms = 64 s span, two levels to cover `hard_timeout`; memory 8 B per scheduled trace ≈ 3.2 MiB at 400 000 open traces. The wheel tick is the only periodic work on the shard goroutine.

**Docs to change:** `01 §3.1` (Route row), `01 §7`, `01 §10.2`, `01 §10.3`, `02 §1`, `03` diagram 1, `04 §4`, `04 §5.2`, `05 §D-5`, `F01 §3.2`, `F02 §3.1` FR-F02-2/-5/-9, `§3.2`, `§4.2`, `§4.3`, `§4.4`, `§5`, `§7`.

---

## DR-10 — Sampler: canonical interface, hard keep-rate cap, bounded rare-path, cached baselines

**Resolves:** SR-10, PD-8, PD-9, CC-26, CC-11 (sampler half), D-X1.

**Decision — the canonical `sampler` interface set.**

```go
package sampler

type Sampler interface {
    // Consume satisfies ingest.SpanSink structurally (DR-2).
    Consume(ctx context.Context, tid model.TenantID, spans []model.Span) error

    SetInterestPredicate(ctx context.Context, tid model.TenantID, p InterestPredicate) (string, error)
    RemoveInterestPredicate(ctx context.Context, tid model.TenantID, id string) error
    ListInterestPredicates(ctx context.Context, tid model.TenantID) ([]InterestPredicate, error)

    AdjustFloor(ctx context.Context, tid model.TenantID, floor float64) error

    Decisions() <-chan Decision            // cap 512, blocking send
    Traces() <-chan model.Trace            // cap 512, blocking send
    REDSamples() <-chan model.REDSample    // cap 8192, drop-oldest + counter

    FlushAll(ctx context.Context) error    // shutdown step 04 §X4
    ReplayWAL(ctx context.Context) (ReplayReport, error)
    Stats() Stats
}
```

**Binding renames:** F02's `ClearInterestPredicate` → `RemoveInterestPredicate`; F02's `Snapshot()` → `Stats()`; F02's `BaselineLookup` → `BaselineSource` (02's name, F02's method set).

```go
type Stats struct {
    Shards           []ShardStats
    BytesInFlight    int64
    OpenTraces       int64
    KeepRate60s      float64
    KeepRateByReason map[model.KeepReason]float64
    PredicatesActive int
    TracesLostTotal  int64
    RebalanceSplits  int64
    RingEpoch        uint64
}
type ShardStats struct { ShardID int; OpenTraces int; BytesInFlight int64; WALSegment string }

type PolicyEvaluator interface {
    Evaluate(ctx context.Context, tid model.TenantID, t *model.Trace, b KeyBaseline, m InterestMatch, g Governor) Decision
}

// BaselineSource is the consumer-declared read interface satisfied by store/sqlite.
// It is NEVER called from the decision path — see the cache decision below.
type BaselineSource interface {
    Quantile(ctx context.Context, tid model.TenantID, service, operation string, q float64) (uint64, bool, error)
    LoadSnapshot(ctx context.Context, tid model.TenantID) (BaselineSnapshot, error)
    PathSeen(ctx context.Context, tid model.TenantID, sig uint64, lookback time.Duration) (time.Time, bool, error)
    CallRate(ctx context.Context, tid model.TenantID, service string) (float64, error)
}

type PredicateSet interface {
    Match(t *model.Trace) InterestMatch
    Add(p InterestPredicate) (string, error)
    Remove(id string) error
    ExpireDue(now time.Time) int
    Narrow(level int) int
}
type InterestMatch struct { Matched bool; PredicateID string; Scope PredicateScope }
```

**Decision — baselines are never read from SQLite on the decision path (PD-8).**

```go
type ServiceOp       struct { Service, Operation string }
type KeyBaseline     struct { P95Nanos, P99Nanos uint64; Warmed bool; Samples uint32 }
type BaselineSnapshot struct { At time.Time; ByKey map[ServiceOp]KeyBaseline } // immutable, swapped atomically
```

- Built at startup from `red_rollup` via `LoadSnapshot`; refreshed on `sampler.baseline.refresh_interval` (60 s); **stated staleness tolerance ≤ 120 s** — the slow-trace rule tolerates a stale p99 far better than a synchronous read.
- `Evaluate` computes `max(duration)` per **distinct** `(service, operation)` in the trace and performs **one map lookup per distinct key**, never one per span. A 10 000-span trace over 20 distinct keys does 20 lookups.
- `RecordPathSignature` is **removed from the decision path**; observations go out on `pathSigCh` (cap 4096, drop-oldest + counter), drained by the single telemetry writer, dedup-on-write. `04 §6` invariant 1 is preserved.
- **`AC-F02-12`:** `Evaluate` benchmarked on a 10-span and a 10 000-span trace, both meeting `01 §10.1` (p50 ≤ 2 ms, p99 ≤ 25 ms), the benchmark asserting **zero SQLite reads inside `Evaluate`**.

**Decision — the keep rules, in this exact order, all evaluated.**

```yaml
sampler:
  policy:
    keep_errors: true
    slow_quantile: 0.99                     # closed enum: 0.95 | 0.99 (DR-39)
    slow_min_duration: 250ms                # MANDATORY floor; F02 omits it today
    rare_path_lookback_days: 7
    rare_path_keeps_per_min: 60             # NEW per-tenant token bucket
    max_path_signature_cardinality: 200000  # NEW; over cap, new signatures count as healthy
    baseline_min_samples: 200
    healthy_sample_rate: 0.01
    floor_traces_per_min_per_service: 6
    max_keep_rate: 0.25                     # HARD CAP applied after every keep class
```

1. **Error** — any span with `Status.Code == Error` or `error.type` present.
2. **Slow** — `duration > baseline.P99` **and** `duration > slow_min_duration` **and** `baseline.Warmed`. The `slow_min_duration` conjunct is what turns a ~39 % keep rate on healthy 50-span traces into the intended ~1 %. It is mandatory.
3. **Rare** — signature unseen in `rare_path_lookback_days`, **and** the per-tenant `rare_path_keeps_per_min` token bucket has a token, **and** `path_signature` cardinality is under `max_path_signature_cardinality`. Overflow is a distinct outcome: not kept, `KeepDropped`, `traceiq_sampler_rare_overflow_total`. This closes the randomised-operation-name amplification attack.
4. **Interest** — DR-11.
5. **Floor** — `floor_traces_per_min_per_service`.
6. **Probabilistic** — `healthy_sample_rate`, adjusted by `AdjustFloor` (DR-12).

**`max_keep_rate` is a hard cap in `Policy.Evaluate`, applied after all six classes.** Over the rolling-60 s cap, keeps shed in this fixed order and no other: `Probabilistic → Floor → Interest(Recurrence) → Rare → Slow`. **`Error` is never shed.** Each shed increments `traceiq_sampler_shed_total{class=...}`; the trace still records RED. New `FR-F02-13` / `AC-F02-13`.

**Decision — `PathSignature` is an ordered edge list (PD-9c).** `TraceBuffer.ServiceOps map[ServiceOp]struct{}` is **deleted**.

> `PathSignature = xxh3( concat over edges of ( caller_service "\x00" caller_operation "\x01" callee_service "\x00" callee_operation "\x02" ) )`, edges enumerated by **pre-order DFS from the root, children sorted by `(StartUnixNano, SpanID)`**. Orphan subtrees are appended after the rooted tree, ordered by their own root's `(StartUnixNano, SpanID)`.

`AC-F02-14`: two traces over the same service set with different call orders produce different signatures.

**Docs to change:** `00`, `01 §4.2`, `01 §7` (`sampler.policy.*`, new `sampler.baseline.*`), `02 §1`, `04 §4`, `04 §X4`, `F02 §3.1`, `§3.2`, `§4.2`, `§4.3`, `§4.4`, `§7`.

---

## DR-11 — Interest predicate (D-X5): two phases, exact struct, bounded matching

**Resolves:** CC-2, PD-26, SR-10(iii).

**Decision — both phases exist, each with its own FR.** `F06 FR-F06-12` is split into 12a and 12b.

```go
package sampler

type PredicateScope uint8
const (
    ScopeInvestigation PredicateScope = 1 // phase A: data for THIS investigation's next step
    ScopeRecurrence    PredicateScope = 2 // phase B: data for the NEXT occurrence
)

type InterestPredicate struct {
    ID          string
    Tenant      model.TenantID
    Scope       PredicateScope
    Source      string                     // "rca:<investigationID>" | "user:<subjectID>"
    Services    []string                   // <= 32
    Operations  []string                   // <= 32
    AttrEquals  map[string]string          // <= 8; every key MUST be in store.hot.indexed_attribute_keys
    ErrorSigIDs []string                   // <= 8
    PathSigs    []uint64                   // <= 8
    TraceIDs    map[model.TraceID]struct{} // <= 1024, a SET (never a slice)
    MinDuration time.Duration
    ErrorsOnly  bool
    NarrowLevel uint8                      // 0 = as pushed; 1..4 progressively narrowed
    Hits        uint64
    CreatedAt   time.Time
    ExpiresAt   time.Time
}
```

**FR-F06-12a — scope predicate (phase A).** Pushed at **Contextualize**, before the first Hypothesize step.
`Scope = ScopeInvestigation`; `Services = {Incident.EpicenterService} ∪ Incident.BlastRadius` truncated to 32; `MinDuration = baseline.P95(epicenter, rootOperation)`; `ErrorsOnly = false`; `ExpiresAt = now + sampler.interest.scope_ttl`.
**Removed on any terminal `Investigation.Status`** — `Concluded`, `Inconclusive`, `BudgetExhausted`, `Failed`, `Aborted` — by `RemoveInterestPredicate`, in the same code path that writes the terminal status; or on `ExpiresAt`, whichever is first.

**FR-F06-12b — recurrence predicate (phase B).** Pushed **only** on `Status == Concluded && Confidence >= rca.confidence_threshold`.
`Scope = ScopeRecurrence`; scoped by `ErrorSigIDs` ∪ `PathSigs` **only** — never by service alone, because a service-scoped 24 h predicate is a store-everything switch; `ExpiresAt = now + sampler.interest.recurrence_ttl`.
Removed on TTL, on `DELETE /v1/sampler/interest/{id}` (operator), or when a later investigation for the same `Incident.Fingerprint` concludes and supersedes it.

**Config (replaces `01 §7`'s `sampler.interest`):**

```yaml
sampler:
  interest:
    enabled: true
    max_predicates: 32                  # per tenant, both scopes combined
    max_recurrence_predicates: 8        # per tenant, subset of the above
    max_trace_ids_per_predicate: 1024
    scope_ttl: 30m                      # phase A
    recurrence_ttl: 24h                 # phase B
    narrow_at_keep_rate: 0.25           # == sampler.policy.max_keep_rate
    persist: true                       # control.db sampler_interest
```

Eviction at `max_predicates`: evict the **lowest-`Hits`, soonest-expiring** predicate; increment `traceiq_sampler_predicates_evicted_total`; **refuse** with `429 predicate_limit` if that would evict a `ScopeInvestigation` predicate belonging to a running investigation.

**Decision — matching is bounded (PD-26).** `PredicateSet` maintains, rebuilt on add/remove and read through an atomic snapshot pointer: `byService`, `byErrorSig`, `byPathSig`, `byAttrKey` inverted indexes, plus a per-predicate `TraceIDs` **hash set**. `Match(t)` unions the candidate sets for the trace's ≤ 20 distinct services, its error signatures and its path signature, and evaluates only those.

> **Published worst case: ≤ 32 candidate predicates × O(1) map lookups + one `MinDuration`/`ErrorsOnly` comparison each ≈ 150 µs p99** — inside the 2 ms p50 decision budget. `AC-F02-15` benchmarks 32 predicates × 1 024 trace IDs × a 1 000-span trace against `01 §10.1`.

**Decision — `Hits` is accurate.** All six keep classes are evaluated. `Decision.Reason` follows DR-10's precedence, **and independently**: `Decision.MatchedPredicateID` is set and `predicate.Hits++` fires **whenever a predicate matched**, regardless of the winning reason; `Decision.SecondaryReasons` (a `uint16` bitmask of `model.KeepReason`) records every other class that fired. A trace that has an error *and* matches an open investigation's predicate records `Reason = KeepError`, `MatchedPredicateID = <id>`, `Hits++`. `AC-F02-16` asserts exactly this.

**Decision — narrowing (the `03` diagram-6 circuit breaker, absent from F02).** When the rolling-60 s keep rate exceeds `narrow_at_keep_rate`, `PredicateSet.Narrow(level)` escalates:

| Level | Action |
|---|---|
| 1 | `MinDuration` raised to the epicenter's p99 |
| 2 | `ErrorsOnly = true` |
| 3 | `Services` reduced to the epicenter only |
| 4 | Expire the lowest-`Hits` `ScopeRecurrence` predicate |

Each level sets `NarrowLevel`, increments `traceiq_sampler_predicate_narrowed_total`, and is visible in `GET /v1/sampler/interest`.

**Docs to change:** `01 §4.2`, `01 §7`, `02 §1`, `03 §6` (becomes normative), `04 §1 L7`, `F02 §4.2`, `§4.3`, `§4.4`, `§6`, `§7`, `F06 §3.1`, `§4.4`, `§7`.

---

## DR-12 — Cost control: a stable controller, a real disk budget, an escalation ladder

**Resolves:** PD-10, CC-23, SR-10(iv).

**Decision — the loop's shape.** `store` never imports `sampler`. `store.Signals()` emits `store.CostSignal`; `cmd/traceiq` wires it to `sampler.AdjustFloor`. `store.Trace.KeptReason sampler.Reason` is **deleted** (DR-4: `model.KeepReason`).

```go
package store

type Watermark uint8
const ( WMNormal Watermark = 0; WMWarn Watermark = 1; WMHigh Watermark = 2; WMCritical Watermark = 3 )

type CostSignal struct {
    Tenant              model.TenantID
    ObservedBytesPerSec float64   // 5-minute RATE, not a 24 h cumulative
    BudgetBytesPerSec   float64   // tenant.Policy.ByteBudgetBytes / retention horizon
    CurrentFloor        float64
    RecommendedFloor    float64
    DiskUsedRatio       float64
    Watermark           Watermark
    EmittedAt           time.Time
}
func (s *TieredStore) Signals() <-chan CostSignal
```

**Decision — the controller.** Proportional with a deadband, a rate limit and a symmetric recovery ramp; the measurement window matches the actuation interval.

```yaml
store:
  cost:
    signal_interval: 60s            # was 5m
    measure_window: 5m              # a RATE; IngestedBytes24h is deleted
    deadband: 0.15                  # |observed/budget - 1| < 0.15 -> no change
    max_step: 0.5                   # floor may change by at most 0.5x / 2x per interval
    recovery_ramp: 1.25             # 3 consecutive under-budget intervals -> floor *= 1.25
    adaptive_floor_min: 0.0001
    adaptive_floor_max: 1.0
    settling_target: 30m            # published gate: the controller must settle within this
```

**Decision — the escalation ladder.** Driving `healthy_sample_rate` to 0.0001 cannot bound bytes when error/slow/rare keeps dominate, which is the design's own premise. Each rung names its lever and its watermark:

| Watermark | Lever | Effect |
|---|---|---|
| `Warn` 0.80 | Controller | Floor lowered within `max_step`; `traceiq_store_cost_pressure = 1` |
| `High` 0.85 | Floor at `adaptive_floor_min` and still over budget ⇒ **`max_keep_rate` lowered** by 0.05 per interval to a hard floor of 0.05 | The only lever with authority over error/slow/rare keeps; DR-10's shed order protects `Error` to the last |
| `High` 0.85 | `action_on_full: shed_sampled` (default) | Delete T3 `sampled` blocks oldest-first, then T0 span rows oldest-first. **Anomalous blocks are the last thing deleted, ever** |
| `Critical` 0.95 | `action_on_full: stop_ingest` if configured; else keep shedding and raise a `Critical` incident `disk_budget_critical` | Receivers 429; `/readyz` fails **only** under `stop_ingest` |

**Decision — the disk budget belongs to F03 (CC-23).**

```go
type RetentionPolicy struct {
    Tenant          model.TenantID
    HotSpanRows     time.Duration
    HotTraceRows    time.Duration
    HotRollups      time.Duration
    ColdAnomalous   time.Duration
    ColdSampled     time.Duration
    REDRollups      time.Duration
    Investigations  time.Duration
    Audit           time.Duration
    MaxDiskBytes    int64      // NEW
    HighWatermark   float64    // NEW, default 0.85
    ActionOnFull    string     // NEW, "shed_sampled" | "stop_ingest"
    ByteBudgetBytes int64      // per-tenant, from tenant.Policy
}
```

New `FR-F03-15` (disk budget + shed policy with anomalous-last ordering) and `AC-F03-15`.

**Per-tenant byte budget.** `store.cost.byte_budget_per_tenant_gb: 0` (unbounded) survives **only** when `tenancy.enabled: false`. Startup validation: *`tenancy.enabled: true` with `byte_budget_per_tenant_gb: 0` and more than one tenant is exit 2* — an unbounded per-tenant budget in a multi-tenant deployment is a cross-tenant denial of service.

**Docs to change:** `01 §5.3`, `01 §7`, `01 §9` D-D1 row (must name FR-F03-15 and FR-F06-17), `02 §1`, `04 §5.2`, `F02 §4.3`, `F03 §3.1`, `§4.2`, `§4.3`, `§4.4`, `§7`.

---

## DR-13 — Topology: one input, one edge key, a bounded graph, the full `Graph` interface

**Resolves:** PD-12, PD-13 (interface half), PD-33, CC-15, CC-33(4).

**Decision — the input is the pre-sampling ingest fan-out.** F04 is right: topology must see undownsampled traffic for `FR-F04-1`'s 1 s freshness, which the post-assembly path (bounded below by `idle_timeout` 8 s) cannot reach. `01 §2`'s `RED --> TG` edge is **deleted** and replaced by `LIM --> TG` alongside `LIM --> ROUTE`; `03` diagram 1's `SH->>TOPO: Observe each span` is **deleted**. `topology.LiveGraph` satisfies `ingest.SpanSink` structurally (DR-2) and does **not** import `ingest`.

**Decision — the edge key drops `calleeOperation`** (PD-12): per-callee-operation edges are combinatorial and do not fit.

```go
package topology

// model.Resolution (DR-39 §39.1) — the ONLY RED type, declared once in internal/model; cited
// here, not redeclared (DR-0).
type Edge struct {
    ID          string            // xxh3(caller "\x00" callee "\x00" protocol)
    Tenant      model.TenantID
    Caller      string            // "" for ingress
    Callee      string
    Protocol    string            // http | grpc | db | messaging | internal | unknown
    BucketStart time.Time
    Resolution  model.Resolution
    Calls       uint64
    Errors      uint64
    DurationSumNanos uint64
    Hist        model.LatencyHist // fixed-boundary, mergeable, 16 buckets, 64 bytes
    ExemplarTraceIDs []model.TraceID // <= 2
    FirstSeen, LastSeen time.Time
    IsNew, IsVanished   bool
}
```

Per-operation visibility survives at **bounded** cardinality in a separate hourly top-N table:

```sql
CREATE TABLE topology_edge_op (
  tenant_id TEXT NOT NULL, edge_id TEXT NOT NULL, callee_operation TEXT NOT NULL,
  bucket_start INTEGER NOT NULL,          -- 1 h buckets only
  calls INTEGER NOT NULL, errors INTEGER NOT NULL, p99_nanos INTEGER NOT NULL,
  PRIMARY KEY (tenant_id, edge_id, bucket_start, callee_operation)
) WITHOUT ROWID;
-- top topology.max_operations_per_edge (default 20) operations by calls, per edge per hour
```

**Decision — per-bucket quantiles become a fixed-boundary histogram.** `P50/P95/P99` per edge per 10 s bucket needs a live estimator per edge per bucket (4.5 M at F04's own target) and is deleted.

```go
// model.LatencyHist: 16 fixed log-spaced boundaries from 1 ms to 32 s, uint32 counts.
// 64 bytes. Mergeable by addition. Quantiles interpolated at READ time over the window.
type LatencyHist [16]uint32
```

Per-edge byte cost is therefore **90 B at 10 s / 5 m** and **300 B at 1 h** (DR-6's table).

**Decision — cardinality and cascade.** `topology.max_edges` drops 200 000 → **20 000** (dev; 600 edges expected, 33× headroom), LRU eviction by `Calls` with `traceiq_topology_edges_evicted_total`. Retention cascades 10 s → 5 m → 1 h per DR-7's T4b row. `topology_edge` is now inside DR-6's sizing model; it was absent from `01 §5.1`'s sizing paragraph entirely.

**Decision — the full `Graph` interface** (restores what F04 dropped and what `02`, `03 §2`, `04 §X6` and `F06.TopologyQuery` all call):

```go
type Direction uint8 // 1 Upstream, 2 Downstream, 3 Both

type Graph interface {
    Consume(ctx context.Context, tid model.TenantID, spans []model.Span) error   // ingest.SpanSink
    Neighbors(ctx context.Context, tid model.TenantID, service string, hops int, dir Direction) (Neighborhood, error)
    Distance(ctx context.Context, tid model.TenantID, a, b string, maxHops int) (int, bool, error)
    Edges(ctx context.Context, tid model.TenantID, w model.Window) ([]Edge, error)
    EdgeOps(ctx context.Context, tid model.TenantID, edgeID string, w model.Window, topN int) ([]EdgeOp, error)
    Snapshot(ctx context.Context, tid model.TenantID) (Snapshot, error)
    Export(ctx context.Context, tid model.TenantID, w io.Writer, f ExportFormat) error  // D-Y4
    Changes() <-chan ChangeEvent
    Flush(ctx context.Context) error        // shutdown step 04 §X6
    Stats() Stats
}

type Neighborhood struct {
    Root string
    Upstream, Downstream []string
    Edges  []Edge
    HopOf  map[string]int
}

type EdgeSink   interface { WriteEdges(ctx context.Context, tid model.TenantID, edges []Edge) error }
type EdgeSource interface { LoadEdges(ctx context.Context, tid model.TenantID, w model.Window) ([]Edge, error) }
```

**F04 persists through `EdgeSink` and warm-starts through `EdgeSource`, never by calling `store.HotIndex`** (CC-1c, PD-15(4)). `store.HotIndex.UpsertTopologyEdge` and `QueryTopologyEdges` are deleted; `store/sqlite` satisfies both interfaces; `cmd/traceiq` injects it. `Export` closes the topology half of D-Y4.

**Decision — `Changes()` is lossless for `NewEdge` (PD-33).** The channel stays (cap 256, drop-oldest), **but** every new edge is durably recorded as `topology_edge_meta.is_new = 1` with `first_seen`, and `anomaly.TopologyChangeDetector` reconciles from that table on each 30 s tick in addition to draining the channel. Drop-oldest therefore delays a `NewEdge` detection by at most one tick and can never lose it; `VanishedEdge` stays channel-only and best-effort (re-derivable from `last_seen`). `02 §2`'s alternative design (detector holds the graph and diffs `knownEdges`) is deleted. `AC-F04-9` (new): a deliberately stalled consumer misses **zero** `NewEdge` detections.

**Memory budget:** the live graph holds one `LatencyHist` per `(edge, open bucket)` with at most 2 open buckets: 20 000 × 2 × 154 B ≈ 6 MiB, plus adjacency, meta and the pending-client-span join table ≈ **80 MiB** at the cap — the figure used in DR-9's RSS derivation.

**Docs to change:** `01 §2`, `01 §4.7`, `01 §5.1`, `01 §7`, `01 §10.2`, `02 §2`, `03 §1`, `04 §X6`, `F03 §4.2`/`§4.3`, `F04 §1`, `§3.2`, `§4.1`, `§4.2`, `§4.3`, `§4.4`, `§5`, `§7`, `§8` (**Decided (round 1)**: shard by `hash(caller, callee)`, keep the callee reverse index), `F05 §4.3`.

---

## DR-14 — Anomaly detection: canonical interfaces, a baseline model that fits, bounded cardinality

**Resolves:** PD-11, PD-32, PD-13 (grouper half), CC-5, CC-10, CC-28.

### 14.1 Canonical `anomaly` interfaces

```go
package anomaly

type Kind uint8
const (
    KindLatencyShift      Kind = 1
    KindErrorBurst        Kind = 2
    KindNewErrorSignature Kind = 3
    KindThroughputDrop    Kind = 4
    KindTopologyChange    Kind = 5
)
// CLOSED. deploy_regression is NOT a sixth kind — see §14.6.

type Detector interface {
    Kind() Kind
    // Evaluate runs once per anomaly.eval_interval per tenant over the samples
    // accumulated since the previous tick. It is pure with respect to (in, clock):
    // no I/O, no store call, so the eval harness can drive it deterministically.
    Evaluate(ctx context.Context, tid model.TenantID, in EvalInput) ([]model.AnomalyEvent, error)
}

type EvalInput struct {
    Now       time.Time
    Window    model.Window
    Samples   []model.REDSample      // Res10s buckets only (DR-39)
    ErrorSigs []store.ErrorSignature
    Baselines BaselineReader
    Topology  TopologyReader         // consumer-declared; topology.LiveGraph satisfies it
    Deploys   DeployIndex
}

type TopologyReader interface {
    Neighbors(ctx context.Context, tid model.TenantID, service string, hops int, dir uint8) ([]string, error)
    Has(ctx context.Context, tid model.TenantID, service string) bool
}

type BaselineReader interface {
    Get(tid model.TenantID, service, operation string, at time.Time) (Baseline, bool)
}

type Baseline struct {
    Service, Operation string
    Bucket      uint8            // 0..23 hour-of-day; 24..30 weekday slots; 255 global
    Q           model.Quantiles
    ErrorEWMA   float64
    RPSEWMA     float64
    Samples     uint32
    Warmed      bool
    Provisional bool             // warmed by decree at max_cold_start; stamped onto every derived event
    UpdatedAt   time.Time
}

type BaselineStore interface {
    Observe(ctx context.Context, tid model.TenantID, s model.REDSample) error
    Reader() BaselineReader
    Checkpoint(ctx context.Context) (CheckpointReport, error)   // incremental, dirty keys only
    Load(ctx context.Context, tid model.TenantID) (int, error)  // warm start from anomaly_baseline
    Stats() BaselineStats
}

// QuantileEstimator is the seam between P2 (default) and t-digest (global slot only).
type QuantileEstimator interface {
    Add(v float64)
    Quantile(q float64) float64
    Merge(other QuantileEstimator) error
    SizeBytes() int
    MarshalBinary() ([]byte, error)
    UnmarshalBinary([]byte) error
}

type Grouper interface {
    Add(ctx context.Context, tid model.TenantID, e model.AnomalyEvent) (model.Incident, bool, error)
    ActiveIncidents(ctx context.Context, tid model.TenantID) ([]model.Incident, error)
    Suppress(ctx context.Context, tid model.TenantID, id, reason string, ttl time.Duration) error
    Tick(ctx context.Context, now time.Time) ([]model.Incident, error)
    Stats() GrouperStats
}

type GrouperStats struct {
    OpenIncidents         int
    LastTickAt            time.Time   // read by the deadman (DR-21)
    NeighborCacheHitRatio float64
}
```

**Two `QuantileEstimator` implementations, and only two.** `anomaly.P2Estimator` — the P² algorithm, three tracked quantiles (0.50 / 0.95 / 0.99), 5 markers each, **fixed 240 B, zero allocation after construction** — is the per-season-slot estimator. `anomaly.TDigest` (compression 100, ≤ 512 B serialized) is used **only** for the global slot (`bucket = 255`) and for `red_rollup_1h.digest`. `02 §2`'s `anomaly.SeasonalTDigest` is renamed `anomaly.SeasonalBaseline` and holds 31 `P2Estimator` triples plus one `TDigest`.

### 14.2 The memory model, derived (PD-11)

Seasonality drops from **168 hour-of-week buckets to 31 slots**: 24 hour-of-day (`bucket 0..23`) plus 7 weekday multipliers (`bucket 24..30`) — exactly the shape DR-6's `anomaly_baseline` DDL already carries.

| Component | Bytes | Note |
|---|---:|---|
| `P2Estimator` × 3 quantiles | 240 | 5 markers × (position float64 + height float64) × 3 |
| `ErrorEWMA`, `RPSEWMA`, `Samples`, `UpdatedAt`, flags | 32 | |
| Slot struct + map overhead | 16 | |
| **Per (tenant, service, operation, season slot)** | **288 B** | **the published per-key-per-bucket bound** |

| Aggregate | Value |
|---|---:|
| Slots per key | 31 |
| Bytes per key (31 × 288) | 8 928 B ≈ 8.7 KiB |
| Global slot (`bucket 255`) t-digest per key | 512 B |
| Key header (interned service/operation strings, map entry) | 200 B |
| **Bytes per key, all-in** | **9 640 B** |
| `anomaly.baseline.max_keys` (dev) | **10 000** |
| Baseline subtotal | **91.9 MiB** |
| Error-signature state (`max_error_signatures: 20000` × 120 B) | 2.3 MiB |
| Throughput/EWMA side state, dirty-key set, neighbour cache | 25 MiB |
| **Total** | **≈ 119 MiB → published 120 MiB** |

This is the **120 MiB** line in DR-9's RSS derivation. Changing `max_keys`, the slot count or the estimator requires re-publishing both tables.

**Cardinality cap and eviction.** At `max_keys`, evict the key with the lowest `RPSEWMA` among the oldest-`UpdatedAt` decile (LRU-by-traffic), increment `traceiq_anomaly_baseline_keys_evicted_total`, and delete its `anomaly_baseline` rows on the next checkpoint. A key evicted and later re-seen restarts cold. `max_keys` is per **tenant**; startup validation refuses a configuration whose `max_keys × tenants` product exceeds `anomaly.baseline.max_keys_global` (default 20 000).

**Checkpointing is incremental, never a full sweep.** The 60 s full checkpoint (1.68 M blobs) is deleted. Each `anomaly.baseline.checkpoint_interval` (60 s) the store writes at most `anomaly.baseline.checkpoint_max_rows` (**2 000**) dirty `(key, bucket)` rows, oldest-dirty first, through the **telemetry** writer. Published cost: 2 000 rows / 60 s = **33 rows/s against DR-6's 2 000 rows/s telemetry budget — 1.7 %**. A dirty backlog above 20 000 rows raises `traceiq_anomaly_checkpoint_backlog` and coarsens the interval to 300 s rather than growing the batch.

### 14.3 Seasonal cold start (binding)

| State | Condition | Behaviour |
|---|---|---|
| **Cold** | key's global slot `Samples < anomaly.baseline.warmup_samples` (200) | **all detectors suppressed** for the key; `traceiq_anomaly_suppressed_cold_total{reason="global"}` |
| **Global-only** | global warmed, season slot `Samples < warmup_samples_per_bucket` (**30**) | detectors run against the **global** slot with thresholds widened by `cold_start_multiplier` (**1.5**); events carry `Provisional = true` |
| **Warm** | both warmed | normal thresholds |
| **Provisional-by-decree** | `now − first_seen > anomaly.baseline.max_cold_start` (**24 h**) and still Cold | the key is forced Warm on whatever samples exist; every derived event and incident carries `Provisional = true`, which the UI renders and which **caps `Incident.Score` at 0.69** — a provisional incident can never reach `Critical` and can never satisfy DR-21's P3 |

`warmup_samples: 200` keeps its name and meaning (global slot); `warmup_samples_per_bucket: 30` is new. `F05 FR-F05-4`'s blanket suppression is replaced by this four-state table.

### 14.4 Detector math (replaces both `01 §7`'s z-scores and `F05`'s bare ratios)

The board adopts F05's ratio-plus-absolute-delta **trigger** (genuinely better on skewed latency, per CC-11's own guidance) and derives `01`'s **score** from it. `anomaly.thresholds.latency_z` and `error_burst_z` are **deleted from `01 §7`**.

| Detector | Trigger (all conjuncts required) | `Event.Score` |
|---|---|---|
| `latency_shift` | `obs.P95 >= base.P95 × latency_ratio` (1.5) **and** `obs.P95 − base.P95 >= latency_abs_delta` (50 ms) **and** `obs.Calls >= min_calls` (20) | `clamp01(0.5·min(1,(ratio−1)/1.0) + 0.5·min(1, delta/(4·latency_abs_delta)))` |
| `error_burst` | `obs.ErrorRate − base.ErrorEWMA >= error_rate_delta` (0.05) **and** `obs.ErrorRate >= base.ErrorEWMA × error_burst_ratio` (3.0) **and** `obs.Calls >= min_calls` | `clamp01(0.5·min(1,(obs.ErrorRate−base.ErrorEWMA)/0.20) + 0.5·min(1, obs.ErrorRate))` |
| `new_error_signature` | signature unseen in `new_error_signature_lookback` (7 d) **and** occurrences `>= new_error_sig_min_calls` (5) in the window | `clamp01(0.4 + 0.6·min(1, occurrences/50))` |
| `throughput_drop` | `obs.RPS <= base.RPSEWMA × throughput_drop_ratio` (0.5) **and** `base.RPSEWMA >= min_rps` (0.1) | `clamp01(1 − obs.RPS/base.RPSEWMA)` |
| `topology_change` | `NewEdge` with `Calls >= topology.new_edge_min_calls` (5), or `VanishedEdge` on an edge with ≥ 1 000 calls in the prior 24 h | 0.50 (new) / 0.60 (vanished) |

An event with `Score < anomaly.thresholds.min_event_score` (0.55) is recorded in `anomaly_event` but never reaches the Grouper.

**`throughput_drop` now has a detector.** `F05 FR-F05-3`'s orphan throughput EWMA gains a consumer; new **FR-F05-13** states the rule above with `01 §7`'s numbers (CC-10).

### 14.5 Incident score, severity, fingerprint, epicenter

```
Incident.Score        = clamp01( max(event.Score) × (1 + 0.05 × (distinctServices − 1)) )
Incident.Fingerprint  = "fp1:" + hex(xxh3( tenantID ‖ "\x00" ‖ join(sorted(tokens), "\x01") ))
   tokens ∈ { "svc:<service>", "op:<operation>", "kind:<Kind>", "errsig:<id>", "dep:<callee>", "ns:<namespace>" }, ≤ 24
Incident.EpicenterService = rankEpicenter(events, topology)  // 02 §2: highest (score × inbound-edge count);
                                                             // ties by earliest FirstSeen, then lexical
Incident.BlastRadius      = services within grouping.topology_hops of the epicenter that carry an event
```

The fingerprint construction is **identical to `memory.Fingerprint.Compute`** (DR-19) — one implementation, in `anomaly`, called by `memory`. `F05 §4.2`'s `Event`/`Incident` are replaced field-for-field by `01 §4.3`'s, which carry `Score`, `Severity`, `Fingerprint`, `EpicenterService`, `BlastRadius`, `DeployMarkerIDs`, `InvestigationID`, `SuppressedBy` (CC-5).

**Severity** (`model.Severity uint8`, 1..5 — `F05`'s three-value string is deleted):

| `Incident.Score` | Distinct services | Severity |
|---|---|---|
| < 0.55 | any | not an incident (event only) |
| 0.55–0.69 | 1 | `Low` (2) |
| 0.55–0.69 | ≥ 2 | `Medium` (3) |
| 0.70–0.84 | any | `High` (4) |
| ≥ 0.85 | any | `Critical` (5) |

`model.ServiceMeta.Tier` is **not** an input at any row (DR-21).

### 14.6 Deploy windows: one owner (CC-28)

The deploy-window abstraction lives in **`internal/anomaly`** as `anomaly.DeployIndex`. F05's `EvalDeployCorrelation` and F06's private pre/post split are both **deleted**; both call this.

```go
package anomaly

type DeployIndex interface {
    Near(ctx context.Context, tid model.TenantID, service string, at time.Time, window time.Duration) ([]model.DeployMarker, error)
    PrePostSplit(ctx context.Context, tid model.TenantID, m model.DeployMarker) (pre, post model.Window, err error)
    Record(ctx context.Context, tid model.TenantID, m model.DeployMarker) error
    ListWindow(ctx context.Context, tid model.TenantID, w model.Window) ([]model.DeployMarker, error)
}
```

`PrePostSplit` is defined exactly once:

```
w      = anomaly.deploy_markers.correlation_window   // 30m, canonical; F05's 15m is DELETED
settle = anomaly.deploy_markers.settle               // 2m, NEW
pre    = { m.At − w,        m.At }
post   = { m.At + settle,   m.At + settle + w }
```

`rca` imports `anomaly` (DR-2 permits it), so F06's `error-signature-new-after-deploy` rule calls the same method. `01 §6.3`'s rule stands: deploy markers are pulled deterministically at Contextualize, **never as a tool**.

**`deploy_regression` is demoted to an enrichment**, per CC-10's recommendation: a `latency_shift` or `error_burst` event whose window intersects `Near(service, at, w)` is tagged in place with `Event.DeployMarkerIDs` and `Event.Score += 0.10` (clamped). No sixth `Kind`, no new config value, no new schema column. Canary/progressive delivery is an explicit **Phase 3 deferral** recorded in `00`; `model.DeployMarker.RolloutFraction` exists and is unused in v1.

### 14.7 `Changes()` consumption, and O(1) grouping

**Topology-change intake.** `anomaly.TopologyChangeDetector` does both halves, every `eval_interval` tick:

1. drains `topology.Graph.Changes()` non-blockingly, at most 256 events per tick;
2. reconciles from the durable table through a consumer-declared reader:

```go
type EdgeMetaReader interface {
    NewEdgesSince(ctx context.Context, tid model.TenantID, since time.Time) ([]topology.Edge, error)
}
```

satisfied by `store/sqlite` over `topology_edge_meta(is_new = 1, first_seen > ?)` (DR-13). Union and dedupe by `Edge.ID`. Drop-oldest on the channel therefore delays a `NewEdge` detection by at most one tick and can never lose it (PD-33, AC-F04-9).

**Grouping is O(1) amortized per event (PD-13).** The per-event scan over open incidents × services × `Neighbors` is deleted.

- `byService map[string][]incidentID` — service → open incidents whose K-hop neighbourhood contains it.
- The K-hop set is computed **once per newly-added service in an incident**, via one `TopologyReader.Neighbors(service, grouping.topology_hops, Both)` call, memoised in `neighborCache` (LRU 4 096 entries, TTL 60 s).
- `Add(event)` does one map lookup, one fingerprint-dedupe check, then attaches or creates.

> **Published worst case: ≤ 1 `Neighbors` call per new service per incident and 0 per event; `Add` p99 ≤ 5 ms at 200 open incidents and 5 000 events/min** — inside `01 §10.1`'s "incident grouping ≤ 5 s after the triggering event". `AC-F05-14` benchmarks exactly that shape.

`anomaly.grouping.max_open_incidents: 200` per tenant. Over cap, the lowest-`Score` open incident is force-closed with `Status = Expired` and `traceiq_anomaly_incidents_force_closed_total`.

**Dedupe (`dedupe_ttl` gains an implementation).** If an incident with the same `Fingerprint` is open, or was closed within `anomaly.grouping.dedupe_ttl` (30 m), the event attaches to it and the would-be new incident is written with `SuppressedBy = <existingIncidentID>` rather than dispatched. This is the F05 half of PD-28; the RCA half is DR-17.

### 14.8 Detection latency, re-derived

`F05`'s "3 consecutive 30 s windows over a 5-minute rolling p95" has a floor of ~90 s **plus** the rolling window's own lag, which cannot meet `01 §10.1`'s p95 ≤ 90 s.

| Parameter | Value |
|---|---|
| `anomaly.eval_interval` | 30 s |
| Rolling evaluation window | **90 s** (was 5 m) |
| Debounce | **2** consecutive triggering ticks (was 3) |
| Derived detection latency | 30 s (first trigger) + 30 s (confirm) + ≤ 15 s tick phase = **≤ 75 s p95** |

`01 §10.1`'s ≤ 90 s p95 gate is reachable with 15 s of margin. `AC-F05-15` measures deviation-start → `anomaly_event` against it.

### 14.9 Config (replaces `01 §7 anomaly`)

```yaml
anomaly:
  detectors: [latency_shift, error_burst, new_error_signature, throughput_drop, topology_change]
  eval_interval: 30s
  eval_window: 90s                    # was 5m
  debounce_ticks: 2                   # was 3
  baseline:
    estimator: p2                     # p2 | tdigest  (tdigest is global-slot only)
    tdigest_compression: 100
    ewma_alpha: 0.2
    seasonal: true
    season_slots: 31                  # 24 hour-of-day + 7 weekday; was season_buckets: 168
    warmup_samples: 200               # global slot
    warmup_samples_per_bucket: 30     # NEW
    cold_start_multiplier: 1.5        # NEW
    max_cold_start: 24h               # NEW
    max_keys: 10000                   # NEW, per tenant
    max_keys_global: 20000            # NEW, process-wide
    max_error_signatures: 20000       # NEW
    checkpoint_interval: 60s          # NEW
    checkpoint_max_rows: 2000         # NEW
    min_rps: 0.1
  thresholds:
    latency_ratio: 1.5                # NEW, replaces latency_z
    latency_abs_delta: 50ms           # NEW
    error_rate_delta: 0.05
    error_burst_ratio: 3.0            # NEW, replaces error_burst_z
    throughput_drop_ratio: 0.5
    new_error_signature_lookback: 7d  # NEW
    new_error_sig_min_calls: 5        # NEW
    min_calls: 20                     # NEW
    min_event_score: 0.55
  grouping:
    window: 5m
    topology_hops: 2
    max_events_per_incident: 200
    max_open_incidents: 200           # NEW
    dedupe_ttl: 30m
    neighbor_cache_entries: 4096      # NEW
    neighbor_cache_ttl: 60s           # NEW
  deploy_markers:
    enabled: true
    correlation_window: 30m
    settle: 2m                        # NEW
```

A `detectors` list naming an unknown detector is a startup error (`01 §7`'s existing rule).

**Docs to change:** `01 §4.3` (Event/Incident normative, `Provisional` added), `01 §5.1` (`anomaly_event`/`incident` gain `score`, `severity`, `fingerprint`, `epicenter_service`, `blast_radius_json`, `deploy_marker_ids_json`, `suppressed_by`, `provisional`), `01 §7` (block above), `01 §10.1`, `02 §2` (`SeasonalTDigest` → `SeasonalBaseline`; add `QuantileEstimator`, `DeployIndex`, `Grouper.ActiveIncidents`, `EdgeMetaReader`), `03 §2`, `04 §1 L6`, `F05 §3.1` (FR-F05-3 gains a consumer; FR-F05-4 replaced by the cold-start table; **FR-F05-11 deleted** per DR-21; new FR-F05-13/-14/-15), `§3.2`, `§4.2` (delete the four duplicate tables and both duplicate types), `§4.3`, `§4.4`, `§7`, `§8` (**Decided (round 1)**), `F06 §4.4`.

---

## DR-15 — RCA: the canonical `rca` interface set and the types the loop persists

**Resolves:** CC-4 (type half), CC-18 (hypothesis half), SR-12 (interface half), CC-33 residue on `rca.Journal`.

**Decision — the interfaces. `F06 §4.3`'s set is replaced wholesale.**

```go
package rca

type Engine interface {
    Investigate(ctx context.Context, tid model.TenantID, inc model.Incident) (model.Investigation, error)
    Get(ctx context.Context, tid model.TenantID, id string) (model.Investigation, error)
    Replay(ctx context.Context, tid model.TenantID, investigationID string, mode ReplayMode) (model.Investigation, error)  // DR-18
    Correct(ctx context.Context, tid model.TenantID, investigationID, stepID string, c model.Correction) (model.Investigation, error)
    Abort(ctx context.Context, tid model.TenantID, investigationID, reason string) error
    Stats() Stats
}

type Reasoner interface {
    Kind() model.ReasonerKind                      // "llm" | "rules"
    NextStep(ctx context.Context, tid model.TenantID, s State) (Proposal, error)
    Conclude(ctx context.Context, tid model.TenantID, s State) (model.Conclusion, error)
}

type Proposal struct {
    Phase      model.Phase        // Contextualize | Hypothesize | Test | Validate | Report
    Hypotheses []model.Hypothesis // additions or score updates only
    Call       *ToolArgs          // nil when the step is pure reasoning
    Done       bool
    Rationale  string             // <= 2000 bytes, DISPLAY ONLY, never reaches a tool or the cluster
}

type Tool interface {
    Name() model.ToolName
    Schema() ArgSchema                              // rendered into the model's tool list; also the validator's source
    Invoke(ctx context.Context, tid model.TenantID, a ToolArgs) (model.ToolResult, error)
    Cost() ToolCost                                 // declared row/byte/time ceilings, clamped server-side
}

type ToolRegistry interface {
    Get(n model.ToolName) (Tool, bool)
    Names() []model.ToolName                        // exactly the five of DR-16
    Dispatch(ctx context.Context, tid model.TenantID, a ToolArgs) (model.ToolResult, error)
}

// Journal is 02's name for what F06 called StepStore. One component, one name.
type Journal interface {
    Create(ctx context.Context, tid model.TenantID, inv model.Investigation) error
    AppendStep(ctx context.Context, tid model.TenantID, invID string, st model.Step) error   // fsynced before the loop advances
    AppendEvidence(ctx context.Context, tid model.TenantID, invID string, ev []model.Evidence) error
    Steps(ctx context.Context, tid model.TenantID, invID string) ([]model.Step, error)
    SetStatus(ctx context.Context, tid model.TenantID, invID string, st model.InvestigationStatus, at time.Time) error
    ListRunning(ctx context.Context) ([]model.Investigation, error)   // startup reconciliation (04 §S9)
}

type SchemaValidator interface {
    ValidateReasonerOutput(raw []byte) (Proposal, error)              // strict: an unknown field is an error
    ValidateToolArgs(a ToolArgs, inc model.Incident, topo TopologyReader) error   // DR-16 §16.3
}

type Sanitizer interface {
    Wrap(k model.UntrustedKind, s string) string
    Canary() string
    CheckEcho(modelOutput string) error
}

type Budget interface {
    Charge(ctx context.Context, tid model.TenantID, invID string, d Charge) error
    Remaining(invID string) model.Spend
    Terminated(invID string) (model.TerminationReason, bool)
}

type TopologyReader interface {   // consumer-declared; topology.LiveGraph satisfies it
    Has(ctx context.Context, tid model.TenantID, service string) bool
}
```

**Binding renames and deletions:** `F06`'s `StepStore` → `Journal`; `F06`'s single-method `Engine` gains `Get`/`Replay`/`Correct`/`Abort`/`Stats`; `F06 §4.2`'s local `Investigation`/`Step`/`Hypothesis` are **deleted** in favour of `01 §4.4` plus the additions below; `rca_investigations`/`rca_steps` tables are deleted (DR-4) in favour of `investigation`/`investigation_step`/`evidence`.

**Decision — the type additions to `01 §4.4`** (this is the "DR-15 additions" DR-4 refers to):

```go
package model

type Phase uint8
const ( PhaseContextualize Phase = 1; PhaseHypothesize Phase = 2; PhaseTest Phase = 3; PhaseValidate Phase = 4; PhaseReport Phase = 5 )

type HypothesisCategory uint8   // CLOSED — this is what makes a hypothesis machine-scorable (CC-18)
const (
    CatUnknown            HypothesisCategory = 0
    CatSaturation         HypothesisCategory = 1
    CatDependencyFailure  HypothesisCategory = 2
    CatDeployRegression   HypothesisCategory = 3
    CatConfigChange       HypothesisCategory = 4
    CatResourceExhaustion HypothesisCategory = 5
    CatNetwork            HypothesisCategory = 6
    CatDataSkew           HypothesisCategory = 7
    CatExternalProvider   HypothesisCategory = 8
)

type Hypothesis struct {
    ID, Statement string
    Category  HypothesisCategory
    Component string            // MUST resolve in topology at validation time, or be ""
    PriorScore, PostScore float64
    Status    HypothesisStatus  // proposed | testing | supported | refuted | inconclusive
    Source    HypothesisSource  // detector | memory | rule | llm
}

type TerminationReason uint8
const (
    TermConcluded TerminationReason = 1; TermSteps = 2; TermToolCalls = 3; TermTokens = 4
    TermCachedTokens = 5; TermCost = 6; TermWallClock = 7; TermAborted = 8; TermFailed = 9
)

type ReasonerSwap struct { AtStep int; From, To ReasonerKind; Reason string; At time.Time }
```

`model.Investigation` gains `Phase`, `ReasonerKind`, `ReasonerSwaps []ReasonerSwap`, `TerminationReason`, `IncidentIDs []string` (dedupe attaches extra incidents — DR-17), plus the replay fields in DR-18. `F06`'s rule catalogue populates `Category` and `Component` deterministically for every rule it fires, so the rules reasoner is scorable by the same metric as the LLM reasoner.

**Docs to change:** `00` (core interface list: `Journal` not `StepStore`; add `SchemaValidator`, `ToolRegistry`, `Budget`), `01 §4.4`, `02 §3` (`rca.Journal`, full `Engine` method set, `SchemaValidator` alongside `Sanitizer`), `F06 §4.2` (delete the duplicate types), `§4.3` (replace the interface block), `§4.4`, `§7`, `F11 §4.4` (scorer compiles against these types, no shadow structs).

---

## DR-16 — The closed tool set: typed arguments, a template allowlist, semantic validation

**Resolves:** SR-12 (argument half), CC-16, and the `01 §6.3` replacement DR-0 points at.

### 16.1 The five tools, and only five

`trace_query`, `log_query`, `metric_query`, `topology_query`, `memory_query`. **Adding a sixth is an architecture change — a new DR — never a config value.** `01 §6.3` is replaced by §16.2 below and `F06 §4.3` cites it.

### 16.2 Typed argument structs (`ToolArgs.Query` and `ToolArgs.Extra` are deleted)

```go
package rca

// Exactly one pointer field is non-nil, and it MUST match Tool. The validator
// enforces this before dispatch. There is no free-form string and no map[string]any
// anywhere in this type.
type ToolArgs struct {
    TenantID model.TenantID        // DR-5: mandatory, checked again inside every Tool.Invoke
    Tool     model.ToolName
    Trace    *TraceQueryArgs
    Log      *LogQueryArgs
    Metric   *MetricQueryArgs
    Topology *TopologyQueryArgs
    Memory   *MemoryQueryArgs
}

type TraceQueryArgs struct {
    Service, Operation string
    MinDurationMillis  uint32
    Status             model.StatusFilter   // any | ok | error
    AttrEquals         []AttrEqual          // <= 4; every Key MUST be in store.hot.indexed_attribute_keys
    ErrorSigID         string
    PathSignature      uint64
    Start, End         time.Time
    Limit              int                  // clamped to rca.budget.max_rows_per_tool_call (500)
    Project            []string             // closed field allowlist, <= 12 names
}
type AttrEqual struct { Key, Value string }  // Value <= 128 bytes, matched as a LITERAL, never a pattern

type LogQueryArgs struct {
    TraceID    model.TraceID   // either this ...
    Service    string          // ... or (Service, Start, End)
    Start, End time.Time
    Contains   string          // <= 64 bytes; matched as a LITERAL substring SERVER-SIDE after retrieval.
                               // It is never interpolated into LogQL, ES DSL or any backend query language.
    Limit      int             // clamped to 200 lines
}

type MetricQueryArgs struct {
    TemplateID  string              // MUST be a registered template id — free PromQL is not representable
    Params      map[string]string   // keys fixed by the template; each value validated by its declared param type
    RED         *REDQuery           // alternative shape: red(service, operation, window)
    Start, End  time.Time
    StepSeconds int                 // clamped to [10, 300]; points clamped to 1000
}
type REDQuery struct { Service, Operation string }

type TopologyQueryArgs struct {
    Service    string
    Hops       int                  // 1..3
    Direction  topology.Direction
    Start, End time.Time
    Limit      int                  // clamped to 500 edges
}

type MemoryQueryArgs struct {
    Fingerprint string   // supplied from the incident by the Engine; the reasoner may NOT author one
    Text        string   // <= 256 bytes
    TopK        int      // clamped to memory.retrieval.top_k (8)
}
```

`Params` is the single surviving `map[string]string` in the design and it is safe precisely because both its keys and its value grammar are fixed by the template it names — see §16.4.

### 16.3 Semantic validation, before every dispatch (`01 §8.6(4)`, absent from `F06` today)

1. **Existence.** `Service` (and `Hypothesis.Component`) must be present in `topology.Graph.Snapshot(tenant)`. A name the model invented that no telemetry ever produced fails with `ErrToolArgsUnresolvable`.
2. **Window.** `[Start, End] ⊆ [Incident.StartedAt − 2h, Incident.LastSeenAt + 2h]`, and `End − Start <= rca.tools.max_window` (**6 h**). Out-of-range values are **clamped**, not silently accepted.
3. **Allowlists.** Every `AttrEquals.Key` in `store.hot.indexed_attribute_keys` (DR-6's closed 8); every `Project` name in the per-tool field allowlist; `TemplateID` registered.
4. **Limits.** Every limit clamped server-side; `ToolResult.Clamped = true` when a clamp occurred.
5. **Tenant.** `ToolArgs.TenantID` must equal the investigation's tenant, re-checked **inside** `Tool.Invoke`, not only at the registry.

A validation failure is recorded as a real step with `Verdict = invalid_args`, **no tool is called**, and it **counts against `max_tool_calls`** — so an injected loop of malformed calls is not free and terminates the investigation on budget rather than spinning.

Three and only three tool-call outcomes exist: `ok`, `invalid_args`, `unavailable`. `ErrAdapterUnavailable` produces `unavailable` and is surfaced in the report as a **named missing-evidence class**, never as a silent gap (this preserves the design's degradation property).

### 16.4 The PromQL template allowlist (v1, complete, owned by `01 §6.3`)

| Template ID | Query | Params |
|---|---|---|
| `cpu_throttle` | `rate(container_cpu_cfs_throttled_seconds_total{namespace="$ns",pod=~"$pod"}[$window])` | `ns`, `pod`, `window` |
| `cpu_usage` | `rate(container_cpu_usage_seconds_total{namespace="$ns",pod=~"$pod"}[$window])` | `ns`, `pod`, `window` |
| `mem_working_set` | `container_memory_working_set_bytes{namespace="$ns",pod=~"$pod"}` | `ns`, `pod` |
| `pod_restarts` | `increase(kube_pod_container_status_restarts_total{namespace="$ns",pod=~"$pod"}[$window])` | `ns`, `pod`, `window` |
| `pod_ready` | `kube_pod_status_ready{namespace="$ns",pod=~"$pod",condition="true"}` | `ns`, `pod` |
| `http_server_rate` | `sum(rate(http_server_request_duration_seconds_count{service_name="$service"}[$window]))` | `service`, `window` |
| `http_server_error_ratio` | `sum(rate(http_server_request_duration_seconds_count{service_name="$service",http_response_status_code=~"5.."}[$window])) / sum(rate(http_server_request_duration_seconds_count{service_name="$service"}[$window]))` | `service`, `window` |
| `db_pool_saturation` | `db_client_connection_count{service_name="$service",state="used"} / db_client_connection_max{service_name="$service"}` | `service` |
| `queue_depth` | `messaging_client_consumed_messages_lag{service_name="$service"}` | `service` |
| `gc_pause` | `rate(process_runtime_gc_duration_seconds_sum{service_name="$service"}[$window])` | `service`, `window` |

**Param types (validated before rendering, rendering by `text/template` with an escaping function — never string concatenation):**

| Param | Grammar | Extra rule |
|---|---|---|
| `ns` | `^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$` | MUST be in `tenant.Policy.NamespaceAllowlist` |
| `pod` | same, with **one optional trailing `*`** expanded to `.*` and nothing else | no other regex metacharacter survives validation |
| `service` | same | MUST resolve in topology (§16.3.1) |
| `window` | closed enum `{5m, 15m, 1h, 6h, 24h}` | — |

An unregistered `TemplateID`, an unknown param key, a missing required param, or a value failing its grammar is `invalid_args` **before** any egress.

**Docs to change:** `01 §6.3` (replaced by §16.2–§16.4), `01 §8.6(4)` (cite §16.3), `02 §3` (`ToolArgs` shape), `F06 §3.1` (new FR-F06-21 "semantic validation" with AC), `§4.3`, `§4.4`, `§6`, `§7` (AC-F06-3 extended: no corpus input produces a non-template query and none produces a tool call whose arguments differ from the sanitized-copy run — DR-37), `F07 §4.3` (`Contains` is literal, matched after retrieval), `F10 §4.4` (NL compiles to the same typed args — DR-35).

---

## DR-17 — RCA budget: real token accounting, a reachable `max_tool_calls`, a wall clock, and dedupe

**Resolves:** PD-14, PD-28, SR-12 (budget half), SR-22 (cap half), CC-23 (F06 half), CC-30 (cost-key half), CC-11 (RCA numbers).

### 17.1 What is sent to the model, and what is charged

The quadratic blow-up PD-14 identifies is real and is fixed by **compaction plus a sliding verbatim window**, not by wishing the numbers were different.

At step *n* the prompt is: a **cached prefix** (system + tool schemas + incident context + memory seeds + every earlier step in compacted form) plus **new material** (the previous step's digest and the reasoner's previous output). Concretely:

| Symbol | Meaning | Value |
|---|---|---|
| `S` | system prompt + five tool schemas | 3 200 tok, cached |
| `C` | incident context (events, blast radius, deploy markers) | 2 000 tok, cached |
| `M` | memory seeds, ≤ 8 records | 1 500 tok, cached |
| `D` | **per-step digest cap** = `rca.budget.max_evidence_bytes` **8192 B** ÷ 4 B/tok | **2 048 tok** |
| `K` | **verbatim window** — the last K digests are sent in full | **3** |
| `V` | compacted verdict for a step older than K (one line: tool, args hash, verdict, one-sentence finding) | 60 tok |
| `O` | reasoner output per step, `rca.llm.max_output_tokens` 1600 hard, 400 typical | ≤ 400 tok |
| `N` | `rca.budget.max_tool_calls` | **40** |

**Uncached input** = `N × (D + O)` = 40 × 2 448 = **97 920 tokens ≤ `max_tokens_in` 120 000**, with 22 080 tokens of headroom.
**Cache reads** = `Σ_{n=1..40} [ S+C+M + min(n−1,K)·D + max(0,n−1−K)·V ]` = 268 000 + 2 048×114 + 60×666 = **541 432 tokens ≤ `max_cached_tokens_in` 600 000**.
**Output** = 40 × 400 typical, 40 × 1 600 worst = 64 000 — so `max_tokens_out` rises from 16 000 to **64 000** *or* the loop terminates on output tokens at step 10; the board sets **`max_tokens_out: 64000`** and keeps `max_output_tokens: 1600` per response.

> **`max_tool_calls: 40` is therefore reachable.** This is the worked example PD-14 demanded, and it is binding: any change to `D`, `K`, `V`, `S`, `C`, `M` or `N` requires re-publishing this arithmetic in `01 §4.4`.

**Cache reads are charged.** `max_tokens_in` counts **uncached** input only; cache reads have their own dimension (`max_cached_tokens_in`) and their own price. The **cost** cap is the honest one, because it charges every dimension at its real rate from the API's `usage` block.

**Startup reconciliation of the cost cap.** Worst-case cost is computed at boot from `rca.llm.pricing` (DR-34) as
`(97 920·in + 541 432·cached_read + 64 000·out) / 1e6` µUSD.
If that exceeds `rca.budget.max_cost_micro_usd` (500 000 = $0.50), **`max_tool_calls` is reduced at startup** to the largest `N` that fits, the effective value is logged at WARN and exposed on `GET /v1/config` as `rca.budget.effective_max_tool_calls`. The cap is never silently violated and never silently vacuous.

### 17.2 Wall clock

| Key | Value | Note |
|---|---|---|
| `rca.budget.wall_clock` | 5m | whole investigation |
| `rca.budget.max_step_wall_clock` | **45s** (new) | per-step `context.WithTimeout`; a step that exceeds it is `Verdict = unavailable` |
| `rca.llm.timeout` | **25s** (was 60s) | per HTTP attempt |
| `rca.llm.max_retries` | **2** (was 3) | ⇒ LLM worst case per step 75 s, clamped by `max_step_wall_clock` to 45 s |
| per-tool timeout | 15s | `04 §4`, retained |

> **Published:** the reachable step count is `min(max_steps, max_tool_calls, floor(wall_clock / observed_step_latency))`. At the p50 step-latency target (3.5 s) that is 40; at p99 (12 s) it is 25. **Wall clock, not tokens, is the binding dimension in the common case** — which is the correct design, and it is now stated rather than discovered at runtime.

`Investigation.TerminationReason` records which dimension ended the loop (`model.TerminationReason`, DR-15). `Spend` carries `TokensIn`, `CachedTokensIn`, `TokensOut`, `CostMicroUSD`, `ToolCalls`, `Steps`, `WallClockMillis`.

### 17.3 Concurrency and duplicate-incident dedupe (PD-28)

- **`rca.max_concurrent_investigations: 2` is canonical.** `F06 §3.2`'s "≥ 20 concurrent" is deleted: 20 × 96 MiB = 1.9 GiB exceeds the whole-process ceiling. 2 × 96 MiB = **192 MiB** is the figure in DR-9's RSS derivation.
- Excess incidents queue at `Status = Candidate`; queue depth > 32 raises `traceiq_rca_queue_saturated` and downgrades new investigations to the `rules` reasoner (`04 §4`, retained).
- **Dedupe before dispatch.** `rca.Dispatcher` holds `inflight map[fingerprint]investigationID` and additionally queries `investigation` for a row with the same `Incident.Fingerprint` whose `ended_at > now − anomaly.grouping.dedupe_ttl`. On a hit: **no second investigation starts**; the incident is written with `SuppressedBy = <investigationID>` and appended to `Investigation.IncidentIDs`, and the existing investigation's report lists every attached incident. This is the RCA half of PD-28; the Grouper half is DR-14 §14.7.
- **AC-F06-22:** one injected fault tripping five correlated detectors ⇒ exactly **one** investigation, four incidents carrying `SuppressedBy`, and total LLM spend equal to one investigation's budget.

### 17.4 The per-tenant daily cap (SR-22)

`rca.budget.max_cost_micro_usd_per_tenant_per_day` (default = `tenant.Policy.LLMCostMicroUSDPerDay` = 20 000 000 = **$20/day**) and `rca.budget.max_cost_micro_usd_global_per_day` (default 100 000 000). On exhaustion the engine **downgrades to `rules`** — it never stops investigating, because a cost cap that silences the product during a cost spike is a worse failure than the spend. `Investigation.ReasonerKind = rules`, `ReasonerSwaps` records it (DR-34), `traceiq_rca_daily_cap_downgrades_total` increments, and the report states the downgrade. **AC-F06-23:** a burst of 100 incidents cannot exceed the daily cap and the downgrade is visible on `ReasonerKind`.

### 17.5 Config (replaces `01 §7 rca.budget`; `F06 §4.3`'s four values are deleted)

```yaml
rca:
  max_concurrent_investigations: 2
  confidence_threshold: 0.75            # canonical; F06's 0.8 deleted
  min_incident_score: 0.6
  budget:
    wall_clock: 5m
    max_step_wall_clock: 45s            # NEW
    max_steps: 24
    max_tool_calls: 40
    max_tokens_in: 120000               # UNCACHED input tokens
    max_cached_tokens_in: 600000        # NEW — cache reads, charged separately
    max_tokens_out: 64000               # was 16000; see §17.1
    max_cost_micro_usd: 500000
    max_cost_micro_usd_per_tenant_per_day: 20000000     # NEW
    max_cost_micro_usd_global_per_day: 100000000        # NEW
    max_rows_per_tool_call: 500
    max_evidence_bytes: 8192            # was 32768; this is D in §17.1
    verbatim_digest_window: 3           # NEW — K
    compacted_verdict_tokens: 60        # NEW — V
  tools:
    max_window: 6h                      # NEW (DR-16 §16.3)
```

**Docs to change:** `01 §4.4` (`Budget`/`Spend` fields, the §17.1 table verbatim), `01 §7`, `01 §10.3` (cost targets re-derived — DR-34), `02 §3` (`rca.Budget` interface, `Dispatcher`), `03 §3` (`Charge(tokensIn, cachedIn, tokensOut, cost)`), `04 §4`, `F05 §4.4` (Grouper dedupe half), `F06 §3.1` (FR-F06-7/-8 replaced; new FR-F06-22 dedupe, FR-F06-23 daily cap), `§3.2`, `§4.3`, `§4.4`, `§7`, `§8` (**Decided (round 1)**).

---

## DR-18 — The replay contract (D-Y1): what is persisted, and what `Replay` means

**Resolves:** CC-4, D-Y1, and `F12`'s orphan `/replay` endpoint.

### 18.1 Step persistence — the contract that makes replay possible

Every step is written through `rca.Journal.AppendStep` and **fsynced before the loop may advance** (`FR-F06-2`, retained). The persisted shape:

```go
package model

type Step struct {
    ID, InvestigationID string
    Tenant   TenantID
    Seq      int
    Phase    Phase
    Tool     ToolName          // "" for a pure-reasoning step

    // --- tool inputs, verbatim and re-executable ---
    ToolArgsJSON       string  // canonical (RFC 8785) JSON of the typed ToolArgs, TenantID elided
    ToolArgsHash       string  // "sha256:" + hex

    // --- tool result: hash AND ref, never the body inline ---
    ToolResultHash     string  // "sha256:" + hex over the canonical result bytes
    ToolResultRef      string  // "evidence/<tenant>/<invID>/<seq>.json.zst" in store.ObjectStore
    ToolResultBytes    int64
    Truncated, Clamped, FromCache bool

    // --- reasoner output, raw ---
    ReasonerOutputRef  string  // "reasoner/<tenant>/<invID>/<seq>.json.zst"
    ReasonerOutputHash string
    PromptHash         string  // sha256 of the exact rendered prompt

    Verdict   StepVerdict      // ok | invalid_args | unavailable | refused | schema_error
    StartedAt, EndedAt time.Time
    LatencyMillis int64
    TokensIn, CachedTokensIn, TokensOut int64
    CostMicroUSD  int64
}
```

Bodies live in `store.ObjectStore` under `evidence/` and `reasoner/` (dev: `${data_dir}/evidence`), **never inline in SQLite** — `control.db.investigation_step` carries refs and hashes only, which is what keeps the control writer inside DR-6's 200 tx/s budget. Retention T5 (400 d), expiring with the investigation.

`model.Investigation` gains: `ReplaySeed int64` (from `crypto/rand` at creation; seeds every tie-break in the rules reasoner and the memory scorer), `PromptVersion string` (build constant `rca.PromptVersion`; a prompt edit without a bump fails a CI golden test — DR-34), `ModelID string`, `ParentID string`, `ReplayOf string`, `ReplayMode`.

### 18.2 `Replay` — one signature, two modes

```go
type ReplayMode uint8
const (
    ReplayRecorded ReplayMode = 1   // zero tool calls, zero LLM calls, zero spend
    ReplayLiveDiff ReplayMode = 2   // re-dispatch each tool with the STORED args; diff hashes
)

func (e *engine) Replay(ctx context.Context, tid model.TenantID, investigationID string, mode ReplayMode) (model.Investigation, error)
```

**`ReplayRecorded` (binding semantics).**
1. Load steps in `Seq` order; for each, fetch `ToolResultRef` and verify `sha256 == ToolResultHash`. A mismatch **aborts** with `ErrEvidenceCorrupt` and raises a `Critical` incident — it is never silently re-fetched, because a silent re-fetch would turn the transparency claim into a lie.
2. Re-run `SchemaValidator` → `Validator` → `Reporter` over the cached results. The reasoner is **not** called; the stored `ReasonerOutputRef` supplies each step's decision.
3. The result is a **new** `Investigation` with a new ID, `ReplayOf` and `ParentID` set to the original, `Spend = {0,0,0,0}`. **The original is immutable.**
4. **Determinism:** two `ReplayRecorded` runs produce byte-identical investigation JSON after erasing `ID`, `StartedAt`, `EndedAt`, and each `Steps[].ID/StartedAt/EndedAt/LatencyMillis`.

**`ReplayLiveDiff` (binding semantics).**
1. Each tool step is re-dispatched with the **exact stored `ToolArgsJSON`**, parsed back into the typed struct (a parse failure is `Drift = schema_changed`).
2. `sha256(newResult)` vs `ToolResultHash`: equal ⇒ `Drift = none`; different ⇒ `Drift = data_drifted`, and `NewToolResultRef` is written so both bodies are inspectable side by side. **This is the mechanism that distinguishes a reasoning error from changed data** — the D-Y1 differentiator.
3. The reasoner is still **not** called. Re-reasoning would confound drift with nondeterminism, which is precisely the confusion `F06 §5` fell into. A third mode `ReplayReReason` is an explicit **Phase 3 deferral** recorded in `00`.
4. Spend: tool cost only; `TokensIn/CachedTokensIn/TokensOut = 0`.

`F06 §5`'s claim that "exact replay is defeated by LLM nondeterminism" is **deleted**: replay never re-invokes the model, so nondeterminism is not in the path.

### 18.3 FR/AC text to add

**To `F06 §3.1`:**

- **FR-F06-18** — *Step persistence.* Every step persists `ToolArgsJSON`, `ToolArgsHash`, `ToolResultHash`, `ToolResultRef`, `ReasonerOutputRef`, `ReasonerOutputHash`, `PromptHash`, `Verdict` and the four spend counters, fsynced before the loop advances. Bodies are stored in `store.ObjectStore`, never inline.
- **FR-F06-19** — *Deterministic recorded replay.* `Replay(ctx, tenant, id, ReplayRecorded)` re-derives the report from cached results, issues **zero** tool calls and **zero** LLM tokens, and is byte-reproducible.
- **FR-F06-20** — *Live-diff replay.* `Replay(ctx, tenant, id, ReplayLiveDiff)` re-dispatches every tool step with the stored arguments and annotates each step `none | data_drifted | schema_changed`.
- **FR-F06-24** — *Evidence integrity.* A `ToolResultHash` mismatch aborts the replay and raises a `Critical` incident.

**To `F06 §7`:** **AC-F06-15** (two `ReplayRecorded` runs byte-identical modulo IDs/timestamps; zero tool calls; zero tokens), **AC-F06-16** (against a store mutated at exactly one step, `ReplayLiveDiff` flags `data_drifted` on exactly that step and `none` elsewhere), **AC-F06-17** (a corrupted evidence blob aborts with `ErrEvidenceCorrupt` and raises the incident).

**To `F12 §3.1`:**

- **FR-F12-12** — `POST /v1/investigations/{id}/replay?mode=recorded|live-diff` returns `202` with the new investigation ID; role **operator**; `mode` defaults to `recorded`; an unknown `mode` is `400`.
- **FR-F12-13** — The Investigations screen renders a replay diff view: per step, the tool, the stored arguments, the result hash, a drift badge, and the original verdict beside the replay verdict.

**To `F12 §7`:** **AC-F12-6** (endpoint role enforcement and the 202 contract), **AC-F12-7** (the diff view renders `data_drifted` for the mutated step in AC-F06-16's fixture).

**Docs to change:** `01 §4.4` (Step/Investigation fields above), `01 §5.1` (`investigation_step` columns; `evidence` refs), `01 §6.1` (`/replay` gains `?mode=`), `02 §3` (`Engine.Replay(ctx, tid, id, mode)`), `03 §7` (already correct — now cited rather than restated), `F06 §3.1`, `§4.2`, `§4.3`, `§4.4`, `§5` (delete the nondeterminism claim), `§7`, `F12 §3.1`, `§4.3`, `§4.4`, `§7`.

---

## DR-19 — Memory: tenant-bound fingerprints, a bounded scan, one correction semantics

**Resolves:** PD-27, PD-35, CC-12, SR-13, CC-30 (fingerprint-family half), CC-33(2) (enforcement).

### 19.1 The fingerprint includes the tenant, inside the hash

```go
package memory

type Fingerprint struct {
    TenantID model.TenantID  // ALWAYS present, ALWAYS first
    Tokens   []string        // sorted, deduped, <= 24
    Hash     string          // "fp1:" + hex(xxh3( tenantID ‖ "\x00" ‖ join(Tokens, "\x01") ))
}

// Compute delegates to the ONE implementation, in anomaly (DR-14 §14.5).
func Compute(tid model.TenantID, inc model.Incident) Fingerprint
```

Token classes are closed: `svc:`, `op:`, `kind:`, `errsig:`, `dep:`, `ns:`. Because the tenant is **inside the preimage**, two tenants cannot produce the same hash and a cross-tenant fingerprint match is impossible by construction — not merely by a `WHERE` clause that someone might forget. This is the enforcement half of CC-33(2). `memory.Record` carries `TenantID` and every table carries `tenant_id` as the first primary-key column (DR-5).

**"Fingerprint family"** (CC-30's untestable term) is now defined: two fingerprints are in the same family when their token Jaccard ≥ `memory.consolidation.dedupe_threshold` (**0.6**).

### 19.2 Scan complexity is bounded by an inverted index, not by hope (PD-27)

```sql
CREATE TABLE memory_fp_token (
  tenant_id TEXT NOT NULL, token TEXT NOT NULL, record_id TEXT NOT NULL,
  df INTEGER NOT NULL,                       -- document frequency, maintained on write
  PRIMARY KEY (tenant_id, token, record_id)
) WITHOUT ROWID;
CREATE INDEX memory_fp_token_by_record ON memory_fp_token(tenant_id, record_id);
```

`Similar()` is a three-step, constant-bounded algorithm:

1. **Candidate generation.** Union the posting lists for the query's ≤ 24 tokens, **rarest first** (ascending `df`), stopping at `memory.retrieval.max_candidates` (**500**). Rarest-first is what makes the cap keep the *selective* candidates rather than an arbitrary 500.
2. **Scoring.** Score exactly those ≤ 500 with `SimilarityScorer`.
3. **Selection.** Return the top `memory.retrieval.top_k` (8) above `min_similarity` (0.35).

> **Published bound: ≤ 500 records scored per `Similar()` call, independent of corpus size.** `FR-F08-8`'s "p99 < 200 ms at 100 k records" is therefore a function of a constant rather than of *n*. **AC-F08-7** asserts *both* the p99 **and** the ≤ 500 candidate count at 100 k records — a p99 that passes with an unbounded candidate count does not satisfy this AC.

The FTS index survives for free-text search over root-cause prose; it is **not** the prefilter for fingerprint similarity (which is what PD-27 correctly identified as degrading to O(n)).

**`corpusIDF` is bounded:** `memory.index.max_vocabulary: 50000` terms, LRU by document frequency, ≈ 2 MiB; posting lists live on disk, not in memory. Together with a per-tenant record-header cache (10 000 × ≈ 1.5 KiB ≈ 15 MiB) and slack this is the **64 MiB** line in DR-9's RSS derivation.

**Consolidation is not O(n²).** The nightly pairwise Jaccard (5 × 10⁹ comparisons at 100 k, self-acknowledged in `F08 §8`) is deleted and replaced by **banded MinHash LSH** over fingerprint tokens: 128 permutations, **32 bands × 4 rows**, candidate pairs only within a shared band bucket. Published: nightly consolidation at 100 k records completes within `memory.consolidation.max_duration` (**15 m**) and examines ≤ 2 % of pairs. `FR-F08-7` is rewritten to state the algorithm; `F08 §8`'s open question is **Decided (round 1)**.

### 19.3 Scorer and embedder both exist

```go
type SimilarityScorer interface {
    Name() string    // "lexical" | "hashed" | "anthropic"
    Score(ctx context.Context, tid model.TenantID, q Query, cand []Candidate) ([]Scored, error)
}
type Embedder interface {
    Embed(ctx context.Context, tid model.TenantID, texts []string) ([][]float32, error)
    Dim() int
    Name() string
}
type Store interface {
    Record(ctx context.Context, tid model.TenantID, rec model.InvestigationRecord) error   // DR-2: NOT rca.Investigation
    Similar(ctx context.Context, tid model.TenantID, fp Fingerprint, topK int) ([]model.Record, error)
    Search(ctx context.Context, tid model.TenantID, text string, topK int) ([]model.Record, error)
    Get(ctx context.Context, tid model.TenantID, id string) (model.Record, error)
    Correct(ctx context.Context, tid model.TenantID, c model.Correction) (model.Record, error)
    Confirm(ctx context.Context, tid model.TenantID, id, by string) error
    Delete(ctx context.Context, tid model.TenantID, id, by string) error
    Import(ctx context.Context, tid model.TenantID, r io.Reader, f ExportFormat, by string) (ImportReport, error)
    Export(ctx context.Context, tid model.TenantID, w io.Writer, f ExportFormat) error
    Consolidate(ctx context.Context, now time.Time) (ConsolidateReport, error)
    Stats() Stats
}
type RunbookImporter interface {
    Import(ctx context.Context, tid model.TenantID, markdown []byte, by string) ([]model.Record, error)
}
```

They are different components and both survive (per CC-30's reconciliation list): the **Embedder** produces vectors, the **Scorer** ranks. `01 §7 memory.embeddings.driver` gains **`lexical`** and `lexical` becomes the **default** — it is the better zero-config offline default and needs no API key. `hashed` remains for the vector component when `memory.retrieval.hybrid: true`. `memory_record` gains the `embedding BLOB` column `01 §5.1` already declared, so `FR-F08-3`'s embedding scorer does not re-embed candidates per query.

### 19.4 Correction semantics — `01 §4.6` wins, `F08`'s `+0.2` is deleted

| Event | Effect |
|---|---|
| **Confirmation** (an engineer marks a conclusion right) | original: `Weight += 0.15`, `Confirmations++`, `Weight` capped at 2.0 |
| **Correction** | original: `Weight −= 0.35` (floor 0.0), `Corrections++`. **A new record** is inserted with `Kind = Correction`, `Weight = 1.0`, `SupersedesID = <original>`, carrying the corrected conclusion |
| **Decay** | `effectiveWeight = Weight × 0.5^(age(LastUsedAt)/90d)`, applied at **read** time, never written |
| **Retrieval ordering** | a superseded record is returned **only together with** its superseder and always ranked below it |

`F08 FR-F08-4` is rewritten to exactly this. **AC-F08-3** is rewritten: after a correction, `Similar()` on the same fingerprint ranks the **Correction** record above the superseded one, and the superseded record's weight is strictly lower than before. Retention aligns to **400 d** (`store.retention.investigations`); `F08`'s 180 d and `memory.consolidation.prune_after_days: 365` both become **400**.

### 19.5 Provenance and the injection-persistence bound (SR-13)

```go
type Provenance uint8
const ( ProvHumanCurated Provenance = 1; ProvLLMAuthored Provenance = 2; ProvImported Provenance = 3 )
```

`model.Record` gains `Provenance` and `TrustTier uint8` (1 = human-curated, 2 = imported, 3 = llm-authored). Binding rules, stated in `F06 §4.4` and `F08 §6`:

1. Every `ProvLLMAuthored` and `ProvImported` record is wrapped by `rca.Sanitizer.Wrap(model.UntrustedMemory, …)` **on retrieval**, with the same canary and escaping as telemetry. `01 §8.6(2)`'s scope is widened from "strings originating from telemetry" to **"every string not authored by TraceIQ's own code"** (DR-37).
2. A retrieved memory record may seed a hypothesis (`Hypothesis.Source = memory`) but **may never be cited as `Evidence`**, and **may never raise `Confidence` to or above `rca.confidence_threshold` without at least one `ok` tool step in the same investigation**. Enforced in `rca.Validator`; **AC-F06-21** asserts it.
3. `Similar()` is tenant-scoped structurally (§19.1).
4. `X-SEC §4.4` gains a memory row (Tampering: memory poisoning) — DR-37.

### 19.6 Export format (D-Y4), byte-reproducible

`Export(ctx, tid, w, f)` with `f ∈ {json, markdown}`. Reproducibility rules, binding: records sorted by `(Kind, Fingerprint.Hash, CreatedAt, ID)`; timestamps RFC 3339 in **UTC** with second precision; LF line endings; no map iteration anywhere in the output path (every map is emitted through a sorted key slice); `effectiveWeight` is **not** exported (it is time-dependent) — `Weight`, `LastUsedAt`, `Confirmations`, `Corrections` are. The JSON envelope carries `{schema: "traceiq.memory.v1", tenant, exported_at, count, records}`; `exported_at` is excluded from the reproducibility comparison. **AC-F08-5** (byte-reproducible JSON + Markdown) is retained and now has a specification behind it.

Import is **admin-only**, stamps `ProvImported`, never overwrites an existing ID (a colliding ID becomes a new record), and rejects a bundle whose `schema` is unknown.

### 19.7 Config (replaces `01 §7 memory`)

```yaml
memory:
  enabled: true
  embeddings: { driver: lexical, dim: 256 }        # lexical | hashed | anthropic | none  (default CHANGED)
  retrieval:
    top_k: 8
    min_similarity: 0.35
    max_candidates: 500                            # NEW — the published scan bound
    hybrid: false                                  # NEW — lexical + vector
    weight_decay_half_life: 90d
  index:
    max_vocabulary: 50000                          # NEW
  consolidation:
    interval: 24h
    hour_of_day: 3
    prune_after_days: 400                          # was 365; aligns with T5
    dedupe_threshold: 0.6                          # NEW — defines "fingerprint family"
    minhash_permutations: 128                      # NEW
    minhash_bands: 32                              # NEW
    max_duration: 15m                              # NEW
```

**Docs to change:** `01 §4.6` (Provenance/TrustTier; correction arithmetic is already correct and becomes the sole statement), `01 §5.1` (`memory_fp_token` DDL, `embedding BLOB`, `provenance`, `trust_tier`), `01 §7`, `01 §8.6(2)` (widened scope), `02 §3` (`SimilarityScorer` + `Embedder` + `RunbookImporter`), `03 §7`, `F06 §4.4` (rules 2 above), `F08 §3.1` (FR-F08-4 rewritten, FR-F08-7 rewritten, new FR-F08-9 provenance, FR-F08-10 export determinism), `§3.2`, `§4.2` (delete the duplicate `Record`; add `Kind`, `Weight`, `SupersedesID`, `Embedding`, `Confirmations`, `TenantID`, `Provenance`), `§4.3`, `§4.4`, `§6`, `§7`, `§8` (**Decided (round 1)**).

---

## DR-20 — Correlation: tenant-scoped adapters, one egress path, bounded fan-out, a dev adapter

**Resolves:** SR-21(b), SR-21(c), CC-20(d), and the F07 half of SR-6.

### 20.1 Interfaces, with the tenant in every signature

```go
package correlate

type LogAdapter interface {
    Name() string
    LogsForTrace(ctx context.Context, tid model.TenantID, traceID model.TraceID, w model.Window, limit int) ([]model.LogLine, error)
    QueryByServiceWindow(ctx context.Context, tid model.TenantID, service string, w model.Window, contains string, limit int) ([]model.LogLine, error)
    Capabilities() LogCapabilities
    Health(ctx context.Context) model.HealthReport
    Close() error
}

type MetricAdapter interface {
    Name() string
    Range(ctx context.Context, tid model.TenantID, templateID string, params map[string]string, w model.Window, stepSeconds int) (model.Series, error)
    ExemplarsFor(ctx context.Context, tid model.TenantID, service, operation string, w model.Window) ([]model.Exemplar, error)
    Capabilities() MetricCapabilities
    Health(ctx context.Context) model.HealthReport
    Close() error
}

type Correlator interface {
    LogsForTrace(ctx context.Context, tid model.TenantID, traceID model.TraceID, w model.Window, limit int) (model.LogBundle, error)
    MetricsForSpan(ctx context.Context, tid model.TenantID, s model.Span, w model.Window) (model.MetricBundle, error)
    Stats() Stats
}

type LogCapabilities struct {
    TenantScoped   bool          // false => refuses construction when tenancy.enabled
    TraceIDIndexed bool          // false => QueryByServiceWindow heuristic is the only path; always labelled
    MaxLookback    time.Duration
}
```

`correlate` never calls `store.GetTrace`; the caller supplies `model.Window` and the trace (DR-2). `Contains` is applied as a **literal substring filter server-side after retrieval** and is never interpolated into LogQL/ES DSL/PromQL (DR-16).

### 20.2 Tenant scoping is a startup requirement, not a convention

```yaml
correlate:
  logs:
    driver: file                 # none | loki | elasticsearch | file   (dev default: file)
    url: ""
    path: ${data_dir}/devlogs    # driver: file
    timeout: 10s
    max_lines: 200
    tenant_mode: none            # NEW: none | header | label | per_tenant_credential
    tenant_header: ""            # used only when tenant_mode: header, e.g. X-Scope-OrgID
    tenant_label: ""             # used only when tenant_mode: label
  metrics:
    driver: file                 # none | prometheus | file
    url: ""
    path: ${data_dir}/devmetrics
    timeout: 10s
    step: 30s
    tenant_mode: none
  egress_allowlist: []           # exact host:port list; empty means only the configured URLs
  allow_private_networks: false  # DEFAULT FLIPPED (was true)
  max_inflight: 4                # NEW, per tenant
  max_inflight_global: 16        # NEW
  breaker_failures: 5            # NEW
  breaker_open: 30s              # NEW
  cache:
    enabled: true                # NEW
    max_entries: 2048
    ttl: 60s
```

**Startup validation (exit 2, naming both keys):** `tenancy.enabled: true` **and** a correlate driver other than `none`/`file` **and** `tenant_mode: none`. A shared backend credential with no tenant selector means a tenant-A investigation can retrieve tenant-B log lines, which is exactly SR-21's finding; the configuration is refused rather than documented. An adapter whose `Capabilities().TenantScoped` is false likewise **refuses construction** when tenancy is enabled.

`tenant_mode: per_tenant_credential` resolves a credential through `auth.SecretSource` keyed by tenant (DR-25). `tenant.Policy` gains `CorrelationTenantID string` (defaults to the `TenantID`) so a customer whose Loki org-id differs from their TraceIQ tenant id can map it.

### 20.3 One egress path (SR-21b)

**No adapter constructs its own `http.Client`.** Every outbound call is made with a client obtained from `auth.EgressDialer.HTTPClient(timeout)` (DR-25), which enforces `01 §8.7` in full: host allowlist, DNS pinned per connection against rebinding, redirects **not** followed, and `169.254.169.254` refused **unconditionally in every mode**. `F07 §6` states this rather than omitting it.

`internal/archtest` fails the build on `http.DefaultClient`, `http.Get`, `http.Post`, `http.Head`, `net.Dial`, `net.DialTimeout` or an `http.Client{…}` composite literal anywhere under `internal/correlate`, `internal/llm`, `internal/k8s` or `internal/api/chat`.

`allow_private_networks` defaults **false**; an in-cluster backend opts in explicitly, the opt-in is logged at startup and surfaced on `GET /v1/config` as `egress_private_networks: true`.

### 20.4 Bounded fan-out, breaker, cache

- **Fan-out** is bounded by `golang.org/x/sync/semaphore`: `max_inflight` (4) per tenant and `max_inflight_global` (16). Over the limit the call returns `ErrAdapterBusy` immediately — recorded as a named missing-evidence class — and is **never queued unboundedly**. This is what stops 2 concurrent investigations × 40 tool calls from becoming an outbound stampede.
- **Breaker:** `breaker_failures` (5) consecutive failures opens the adapter for `breaker_open` (30 s); while open, calls fail fast with `ErrAdapterUnavailable`, `traceiq_component_degraded{component="log_adapter"} = 1`, and **`/readyz` is unaffected** (DR-33).
- **Cache:** keyed `(tenant, adapter, sha256(canonical args))`, LRU, `max_entries` (2048) partitioned per tenant so one tenant cannot evict another's entries beyond its `max_entries / ntenants` share. A hit sets `Step.FromCache = true` and does **not** weaken `ToolResultHash` — the hashed bytes are the same bytes.

### 20.5 The dev/offline adapter

`driver: file` is a first-class adapter, not a stub, and is the **dev-profile default** so correlation is exercised with zero egress and the eval harness is deterministic (DR-36):

- **Logs:** NDJSON under `correlate.logs.path`, one object per line: `{ts, trace_id, span_id, service, level, body, attrs}`. `LogsForTrace` filters on `trace_id`; `QueryByServiceWindow` filters on `service` + window + literal `contains`. `Capabilities{TenantScoped: true, TraceIDIndexed: true}` — files are per-tenant subdirectories.
- **Metrics:** a directory of Prometheus text-format snapshots named `<unix>.prom` under `correlate.metrics.path`; `Range` interpolates across snapshots at `stepSeconds`.

**Docs to change:** `01 §7` (block above), `01 §8.7` (cited, not restated, by F07), `02 §3` (adapter signatures gain the tenant; add `LogCapabilities`/`MetricCapabilities`), `F07 §3.1` (new FR-F07-6 tenant scoping, FR-F07-7 bounded fan-out + breaker, FR-F07-8 trace-correlation capability check — DR-38), `§3.2`, `§4.3`, `§4.4`, `§6` (egress clause), `§7` (AC-F07-6 cross-tenant returns zero lines; AC-F07-7 SSRF suite: rebinding, redirect, metadata IP, private ranges all refused; AC-F07-8 startup exit 2), `X-SEC §4.4` (correlate STRIDE row).

---

## DR-21 — Paging (D-D4): one rule, three conditions, no severity bypass

**Resolves:** CC-3, SR-14, CC-30 (tier-0 half), and the D-D4 "Weak" rating.

### 21.1 The rule

Stated once, in `01 §7` and `01 §9`'s D-D4 row, and **cited** by `F05`, `F12` and `02 §4`:

> **A page is emitted for an incident when, and only when, one of exactly three conditions holds.**
>
> **P1 — terminal investigation with validated evidence.** The incident's investigation reached a terminal `Status` (`Concluded`, `Inconclusive`, `BudgetExhausted`, `Failed`, `Aborted`) **and** its report carries at least one `model.Evidence` record produced by a step whose `Verdict == ok`. The page carries the RCA.
>
> **P2 — hard ceiling.** `alerting.max_wait_for_rca` (**default 5 m**) elapsed since the incident first reached `rca.min_incident_score`. The page fires **with the partial investigation attached**: `Status = Running`, the hypotheses tested so far, the persisted step timeline, and `custom_details.rca_state = "partial"`.
>
> **P3 — configured critical SLO breach.** The incident matches an entry in `alerting.critical_slo_breaches`, an **explicitly configured, per-tenant list that is empty by default**. The page fires immediately with `custom_details.rca_state = "none"` and the matched rule ID.
>
> **There is no severity-derived bypass, and `model.ServiceMeta.Tier` is never a paging input.** `F05 FR-F05-11`'s "tier-0-labeled service" hard-page fast path is **deleted** — the label it keys on was never defined anywhere in the document set (CC-30), and a severity bypass is exactly the alert-fatigue behaviour D-D4 claims to fix.

**A page never carries an empty RCA section.** Under P2 the partial timeline *is* the RCA section — `04 §5.2` already guarantees a partial exists because steps are persisted before the loop advances (DR-18). Under P3 the section reads `"no investigation — configured critical SLO breach <ruleID>"`. `F12 FR-F12-8`'s "fire with `custom_details.rca` omitted" is deleted; `02 §4`'s "will not deliver until terminal" note is corrected to the three conditions above.

A **`Provisional`** incident (DR-14 §14.3) can satisfy P1 and P2 but **never P3**, and its page is labelled `baseline_provisional = true`.

### 21.2 `critical_slo_breaches` — typed, validated, never model-authored

```go
package model

type SLOObjective uint8
const ( SLOAvailability SLOObjective = 1; SLOLatency SLOObjective = 2 )

type CriticalSLOBreach struct {
    ID          string        // required, unique per tenant; appears verbatim in the page
    Service     string        // must resolve in topology at evaluation time, else the rule is inert + a WARN metric
    Objective   SLOObjective
    Threshold   float64       // availability: error ratio 0..1 ; latency: p99 milliseconds
    Window      time.Duration // 1m..60m
    MinDuration time.Duration // sustained for at least this long; default 2m
}
```

Rules live in config (and in `tenant.Policy` for per-tenant overrides), are evaluated by the **`api` role** against `model.REDSample` at `Res10s`, and are the only bypass in the system. A rule naming an unresolvable service is inert and raises `traceiq_alerting_slo_rule_inert{id=...}` — it never silently matches everything.

### 21.3 Deadman (SR-14b)

Owned by the **`api` role**, never by `brain`, so a wedged, crashed, saturated or leaderless brain cannot silence it:

- A goroutine checks `anomaly.Grouper.Stats().LastTickAt` and the store write probe.
- If no detection tick completed within `alerting.deadman.interval` (**10 m**), it pages **directly** through the configured sinks with `traceiq_deadman_fired_total` and `custom_details.reason = "no detection tick"`.
- It depends on no LLM, no reasoner and no leader lease.

`04 §5.2` gains a row: **"brain unavailable / RCA saturated → detection and paging continue; every page in the window carries `rca_state = partial|none`; `traceiq_component_degraded{component="rca"} = 1`; `/readyz` is unaffected."**

### 21.4 Config (replaces `01 §7 alerting`)

```yaml
alerting:
  gate: investigation           # `immediate` is DELETED and is exit 2 at startup (it existed only to express the bypass)
  max_wait_for_rca: 5m          # NEW — the P2 hard ceiling
  min_severity: high            # applies to P1 and P2 only; P3 ignores it
  critical_slo_breaches: []     # NEW — P3; [{id, service, objective, threshold, window, min_duration}]
  deadman:
    enabled: true               # NEW
    interval: 10m               # NEW
    target: pagerduty           # NEW
  pagerduty: { enabled: false, routing_key_env: TRACEIQ_PAGERDUTY_KEY }
  opsgenie:  { enabled: false, api_key_env: TRACEIQ_OPSGENIE_KEY }
  slack:     { enabled: false, channel: "#incidents" }
  routes: []
```

### 21.5 New FRs and ACs

- **FR-F12-14** — the three paging conditions, implemented in `api.AlertRouter`; no other path may emit a page.
- **FR-F12-15** — the deadman, owned by the `api` role.
- **AC-F12-8** — kill the brain, inject a `Critical` incident → a page arrives within `max_wait_for_rca` with `rca_state = "partial"` and a non-empty step timeline.
- **AC-F12-9** — stop the detection loop → a deadman page within `alerting.deadman.interval`.
- **AC-F12-10** — **alert precision ≥ 80 % actionable and ≤ 1 page per genuine incident**, measured on the F11 suite. This closes `01 §10.3`'s previously unowned D-D4 gate; owners are **F11 (measurement) and F12 (mechanism)**.
- **AC-F05-16** — an AST/route test asserting no code path from `ServiceMeta.Tier` to the alert router.

**Docs to change:** `01 §7`, `01 §9` (D-D4 row rewritten to the three conditions and to name FR-F12-14/-15), `01 §10.3` (alert-precision gate gains owners), `02 §4` (the `AlertRouter` note), `03 §2`, `04 §5.2`, `F05 §3.1` (**delete FR-F05-11**), `§4.4`, `F12 §3.1` (rewrite FR-F12-8; add FR-F12-14/-15), `§4.3`, `§4.4`, `§7`, `F11 §3.1` (AC-F12-10's measurement).

---

## DR-22 — Remediation payloads: a closed action enum with typed fields, and no free-form patch

**Resolves:** SR-1, CC-6 (payload half), CC-14(c), SR-26.

### 22.1 The data model — `Params map[string]string` is deleted, not validated

```go
package model

type ActionType uint8
const (
    ActionRollbackDeployment ActionType = 1
    ActionScaleReplicas      ActionType = 2
    ActionRestartPod         ActionType = 3
    ActionToggleFeatureFlag  ActionType = 4
    ActionRemoveIstioFault   ActionType = 5
)
// CLOSED. A sixth action type is an architecture change (a new DR), never a config value.

type TargetKind uint8
const ( KindDeployment TargetKind = 1; KindStatefulSet TargetKind = 2; KindPod TargetKind = 3
        KindConfigMap TargetKind = 4; KindVirtualService TargetKind = 5 )

type ActionTarget struct {
    Cluster         string      // "" = the configured kubeconfig context; NEVER model-authored
    Namespace       string      // DNS-1123 label; MUST be in tenant.Policy.NamespaceAllowlist
    Kind            TargetKind
    Name            string      // DNS-1123 subdomain. NO "/" — the "deployment/checkout" form is DELETED,
                                // which also resolves the validate()/argv contradiction SR names in its §3.
    ResolvedUID     string      // stamped by the Guard from the live cluster; never by the proposer
    ResolvedVersion string      // resourceVersion at resolve time
}

// Exactly one spec pointer is non-nil and it MUST match Type.
type ActionProposal struct {
    ID              string
    Tenant          TenantID
    IncidentID      string
    InvestigationID string
    Type            ActionType
    Target          ActionTarget
    RiskTier        uint8       // 1..3, from the fixed table in §22.4 — NOT proposer-supplied
    Rationale       string      // <= 2000 bytes. DISPLAY ONLY. Never reaches a tool, an argv or the cluster.
    ProposedBy      string      // auth.Subject.ID, or "rca:<investigationID>"
    ProposedAt      time.Time

    Rollback    *RollbackDeploymentSpec
    Scale       *ScaleReplicasSpec
    Restart     *RestartPodSpec
    FeatureFlag *ToggleFeatureFlagSpec
    IstioFault  *RemoveIstioFaultSpec
}

type RollbackDeploymentSpec struct { ToRevision int64 }         // 0 = previous; must exist in rollout history
type ScaleReplicasSpec     struct { Replicas int32 }            // 1..tenant.Policy.MaxReplicas (default 50);
                                                                // and <= 3x the current replica count
type RestartPodSpec        struct { GracePeriodSeconds int32 }  // 0..300
type ToggleFeatureFlagSpec struct { Key string; Value bool }    // Key MUST be in tenant.Policy.FeatureFlags;
                                                                // Value MUST be one of that key's allowed values
type RemoveIstioFaultSpec  struct { }                           // NO FIELDS. The Guard computes the patch.
```

**The statement for `F09 §6`, verbatim:**

> **No model-authored string is ever passed to the cluster.** The Guard reconstructs every mutation payload from the typed spec above plus the pre-snapshot it took itself. `Rationale`, the report narrative and the Slack message body are display-only and are rendered with an explicit *untrusted, model-authored* marker beside the Guard-reconstructed payload. `map[string]string` params, merge-patch strings, JSON-patch strings and every other free-form body are **absent from the data model**, not merely validated.

`01 §8.6(6)`'s claim that "the reasoner can emit only a `model.ActionProposal`" is now true, because the proposal no longer contains a payload.

### 22.2 Payload construction — by the Guard, from typed fields plus the snapshot

| Type | Payload the Guard builds | Source of every byte |
|---|---|---|
| `RollbackDeployment` | `rollout undo --to-revision=<n>` | `int64` → `strconv` |
| `ScaleReplicas` | `scale --replicas=<n>` | `int32` → `strconv`, range-checked |
| `RestartPod` | `delete pod <name> --grace-period=<n>` | validated name + `int32` |
| `ToggleFeatureFlag` | merge patch **computed** as `{"data":{"<Key>":"<true\|false>"}}` | `Key` from `tenant.Policy.FeatureFlags`; value from `strconv.FormatBool(bool)` |
| `RemoveIstioFault` | JSON patch **computed** by diffing `PreSnapshot.spec.http[*]` against the same object with `fault` removed: only `{"op":"remove","path":"/spec/http/<i>/fault"}` entries, one per index that actually carries a `fault`. If that set is empty the Guard **refuses** with `422 nothing_to_remove` | the pre-snapshot the Guard fetched |

The Guard then **diffs the computed payload against the pre-snapshot** and refuses if the diff touches any path outside the per-type allowed path set (`/spec/replicas`, `/data/<Key>`, `/spec/http/*/fault`, `/metadata/annotations/kubectl.kubernetes.io/restartedAt`). A payload that fails the diff check is `422 payload_out_of_scope` and the action is `Failed` with an audit row.

Patch bodies are delivered on **stdin**, never on the command line (DR-24).

### 22.3 Target re-resolution against the live topology (CC-14c, `01 §8.6(6)`, `03` diag 5)

This step exists in `01` and in `03` diagram 5 and is **missing from `F09 §4.4`**, which is the doc engineers build from. It is mandatory and runs **before** the allowlist check:

```go
func (g *guard) resolveTarget(ctx context.Context, tid model.TenantID, t *model.ActionTarget) error
```

1. **Topology existence.** `topology.Graph.Snapshot(tid)` must contain a service whose `k8s.namespace.name` and `k8s.deployment.name`/`k8s.statefulset.name`/`k8s.pod.name` resource attributes match `(Namespace, Name)`. A plausible-but-nonexistent name that no telemetry ever produced ⇒ **`422 target_not_resolvable`**. This is `01 §1.1 P4`'s "never a string the LLM invented", given a mechanism.
2. **Live existence.** The object is fetched (`k8s.VerbGet`); `ResolvedUID` and `ResolvedVersion` are stamped. Absent ⇒ `422 target_not_resolvable`.
3. **Re-resolution at execute.** `Execute` re-resolves **inside the same transaction** as the state transition and requires an unchanged `ResolvedUID`; a changed UID ⇒ `409 target_replaced`, action `Failed`, audited. A target deleted and recreated between approval and execution is a different object and is not mutated.
4. **Only then** are `tenant.Policy.RemediationAllowlist`, `NamespaceAllowlist` and `TargetAllowlist` globs evaluated.

### 22.4 Risk tiers and dry-run (SR-26)

| Type | RiskTier |
|---|---|
| `RestartPod` | 1 |
| `ScaleReplicas` | 1 |
| `RollbackDeployment` | 2 |
| `ToggleFeatureFlag` | 2 |
| `RemoveIstioFault` | 3 |

`RiskTier` is set by the Guard from this table; a proposer-supplied value is ignored. `auto_execute_on_approve` is forbidden at RiskTier 3 (DR-23).

**Dry-run stays at propose** — the diff is what the approver reviews, and that is worth keeping — but with three binding conditions `F09 §4.4` must state:
1. It runs **only on the Guard-reconstructed payload**, after §22.2 and §22.3 have both passed.
2. `F09 §4.4` must say plainly that **`--dry-run=server` executes the admission chain**, so mutating and validating webhooks observe the request; the executor ServiceAccount is therefore scoped to exactly the five verbs on the five kinds and nothing more, and that scoping is part of the deploy manifest review.
3. Exactly one dry-run invocation per proposal. **AC-F09-8** asserts both the count and the reconstructed-payload property against a recording admission webhook.

**Docs to change:** `01 §4.1`/`§4.5` (delete `Params map[string]string`; add the typed specs, `TargetKind`, `ResolvedUID`/`ResolvedVersion`), `01 §8.6(6)`, `02 §3` (`ActionProposal` shape; `Executor.DryRun` takes a reconstructed payload), `03 §5` (already correct — becomes the template `F09` is rewritten against), `F06 §4.2` (`Report.Remediation` carries typed proposals), `F09 §3.1` (new FR-F09-15 typed payloads, FR-F09-16 target re-resolution, FR-F09-17 payload-diff scope check), `§4.2`, `§4.4`, `§6`, `§7` (AC-F09-6 adversarial corpus: zero proposals carry operator-supplied patch text; AC-F09-7 `422` on an unresolvable target before `Snapshot()`; AC-F09-8 dry-run), `X-SEC §4.4` (remediation row cites this DR).

---

## DR-23 — Remediation control plane: the `Guard` interface, a 9-state machine, a budget that bounds blast radius

**Resolves:** SR-4, SR-5, SR-25, SR-27, CC-14(a), CC-14(b), CC-14(d), SR-2 (approver half), and `F09 §8`'s two open questions.

### 23.1 The full `Guard` interface

```go
package remediate

type Guard interface {
    Propose(ctx context.Context, tid model.TenantID, p model.ActionProposal, by auth.Subject, idem string) (model.Action, error)
    Approve(ctx context.Context, tid model.TenantID, id string, by auth.Subject, req ApprovalRequest) (model.Action, error)
    Reject(ctx context.Context, tid model.TenantID, id string, by auth.Subject, reason string) (model.Action, error)
    Execute(ctx context.Context, tid model.TenantID, id string, by auth.Subject, idem string) (model.Action, error)
    Verify(ctx context.Context, tid model.TenantID, id string) (model.VerifyResult, error)
    Rollback(ctx context.Context, tid model.TenantID, id string, by auth.Subject, reason string) (model.Action, error)
    Get(ctx context.Context, tid model.TenantID, id string) (model.Action, error)
    List(ctx context.Context, tid model.TenantID, f ActionFilter) ([]model.Action, string, error)
    ExpireDue(ctx context.Context, now time.Time) (int, error)   // CLEANUP ONLY — never the control
    Stats() Stats
}

type ApprovalRequest struct {
    Comment               string // <= 1000 bytes, display only
    OverrideJustification string // >= 40 bytes when RequestBudgetOverride is true; a STRUCTURALLY DISTINCT field
    RequestBudgetOverride bool
}

// Declared HERE. remediate never imports anomaly (DR-2).
type RecoverySignal struct {
    Service, Operation string
    Window             model.Window
    ErrorRateBefore, ErrorRateAfter float64
    P99BeforeNanos, P99AfterNanos   uint64
    OpenIncidents      int
}
type Verifier interface {
    Verify(ctx context.Context, tid model.TenantID, a model.Action, s RecoverySignal) (model.VerifyResult, error)
}
```

`02 §3`'s `Guard.Approve(id string, approver string)` is **deleted**: a bare string carries no role and no tenant, so the Guard structurally could not enforce anything (SR-2d). A CI grep gate fails the build if `Approve(` is declared with a non-`auth.Subject` approver. `F09`'s missing `Verify` and `02`'s missing `Get`/`List` are both restored (the union is canonical, per CC's reconciliation list).

### 23.2 The 9-state machine and its transition matrix

`01 §4.5`'s nine states are adopted **verbatim** in `F09`; the six-state enum is deleted.

`Proposed(1)`, `Approved(2)`, `Rejected(3)`, `Executing(4)`, `Verifying(5)`, `Succeeded(6)`, `Failed(7)`, `RolledBack(8)`, `Expired(9)`.

| from ↓ / to → | Proposed | Approved | Rejected | Executing | Verifying | Succeeded | Failed | RolledBack | Expired |
|---|---|---|---|---|---|---|---|---|---|
| **Proposed** | — | `Approve` | `Reject` | — | — | — | — | — | `proposal_ttl` |
| **Approved** | — | — | `Reject` | `Execute` | — | — | — | — | `approval_ttl` |
| **Executing** | — | — | — | — | apply ok | — | apply error / RBAC denial | — | — |
| **Verifying** | — | — | — | — | — | verified | verify error | auto-rollback fired | — |
| **Succeeded** | — | — | — | — | — | — | — | — | — |
| **Rejected** | — | — | — | — | — | — | — | — | — |
| **Failed** | — | — | — | — | — | — | — | — | — |
| **RolledBack** | — | — | — | — | — | — | — | — | — |
| **Expired** | — | — | — | — | — | — | — | — | — |

`AC-F09-9` exercises **all 81 pairs**, asserting exactly the eleven legal transitions above and rejecting the other seventy.

### 23.3 Expiry is enforced in the transaction, not by a poller

- `Action.ExpiresAt = ApprovedAt + remediate.approval_ttl`, **default 15 m** (was 30 m).
- An unapproved proposal expires at `ProposedAt + remediate.proposal_ttl` (**60 m**).
- **`Execute` re-checks `now < ExpiresAt` inside the same `control.db` transaction that performs the compare-and-set on `(id, state, version)` from `Approved` to `Executing`.** The 30 s poller (`ExpireDue`) is cleanup for display and metrics only. A poller alone is a TOCTOU race, which is exactly SR-4's point.
- Expired ⇒ `409 action_expired`, an audit row, and **zero** executor calls. `AC-F09-10`: approve, advance a fake clock past `approval_ttl`, call `Execute` → `409`, audit row, executor call count 0.

### 23.4 Separation of duty

`Approve` rejects `by.ID == action.ProposedBy` with **`409 approver_must_differ`**. When the proposer is the engine (`ProposedBy = "rca:<investigationID>"`), any human approver satisfies the check. The single implementation is `auth.Authorizer.SeparationOfDuty(proposer, approver string) error`; `F09 FR-F09-3`'s role-only check is replaced. Because DR-25's matrix gives `approver` **no** `remediation_action:propose` capability, separation of duty is structural as well as checked.

### 23.5 `auto_execute_on_approve` defaults to false

`remediate.auto_execute_on_approve: false` is the default (was `true`). When set `true`:

1. it must **also** be enabled per tenant (`tenant.Policy.AutoExecuteAllowed`, default `false`); a global `true` with a tenant `false` is `false`;
2. it is **forbidden at `RiskTier == 3`** — such an action is always created with `AutoExecute = false` regardless of config;
3. enabling it writes an audit event `auto_execute_enabled` naming the subject and tenant.

`Approve()` no longer tail-calls `Execute(id)`; `POST /v1/actions/{id}/execute` remains a separate authenticated call (`01 §6.1`).

**Who may execute: approver or admin.** This settles the three-way split (`01 §8.2` operator, `F09 §4.3` approver-or-admin, `X-SEC FR-XSEC-3` ≥ approver) and `01 §8.2`'s matrix row is corrected. Rationale, recorded: execute is the second gate on an already-approved mutation; granting it to `operator` would let the proposer complete the action alone.

### 23.6 The budget counts attempted mutations

> **`used(incident) = count(actions WHERE incident_id = ? AND state NOT IN (Rejected, Expired))`**

Explicitly counted: `Proposed`, `Approved`, `Executing`, `Verifying`, `Succeeded`, **`Failed`**, **`RolledBack`**, and every auto-rollback (which consumes a slot of its own). The invariant, stated in `F09 §6`:

> *The budget counts **attempted and pending** mutations, not successful ones. It bounds blast radius, which is what it was sold as bounding.*

This closes SR-5(i): an action that fails mid-mutation no longer frees its slot.

- `tenant.Policy.ActionBudgetPerIncident`, default **2**.
- **Override** is a distinct capability, `remediation_action:budget_override`, **admin only** (DR-25). It requires `ApprovalRequest.RequestBudgetOverride == true` **and** `len(OverrideJustification) >= 40` — a structurally separate field, so "type 20 characters in the comment" is not an override (SR-5ii).
- **Ceiling: one override per incident** (`remediate.max_budget_overrides_per_incident: 1`). A second is `409 override_limit` regardless of role (SR-5iii).
- Every override writes a distinct `budget_override` audit event **and** broadcasts to every `tenant.Policy.ChatBinding` approval channel.
- `AC-F09-11`: two actions executed and failed ⇒ a third approval is `409 budget_exceeded`. `AC-F09-12`: an auto-rollback consumes a slot. `AC-F09-13`: a 20-char comment with an empty `OverrideJustification` is rejected. `AC-F09-14`: a second override on the same incident is rejected.

### 23.7 The approver is always an authenticated principal (SR-2)

A chat interaction is authorized only after **all** of:

1. HMAC signature verification (authenticity);
2. `(platform, workspace_id, platform_user_id)` resolves through `auth.IdentityStore` to a provisioned `auth.Subject` (DR-25) — unmapped ⇒ **`403 identity_not_bound`** plus an audit row;
3. that subject holds `remediation_action:approve`;
4. the channel is listed in the tenant's `ChatBinding.ChannelIDs`.

The mapping lives in `internal/auth`, never in `nl` or `api/chat`. `F10`/`F12`'s "signature-verified, no RBAC role" rows are deleted (DR-25, DR-29).

### 23.8 Idempotency (SR-25)

`Idempotency-Key` is **required** on `POST /v1/actions`, `/approve`, `/reject`, `/execute`, `/rollback`; absent ⇒ `400 idempotency_key_required`.

```sql
CREATE TABLE idempotency (
  tenant_id TEXT NOT NULL, key TEXT NOT NULL, endpoint TEXT NOT NULL,
  request_hash TEXT NOT NULL, response_json TEXT NOT NULL, status_code INTEGER NOT NULL,
  created_at INTEGER NOT NULL,
  PRIMARY KEY (tenant_id, key, endpoint)
) WITHOUT ROWID;
```

TTL = `max(remediate.approval_ttl, 24h)`. A replay with the same key **and** the same `request_hash` returns the recorded response with header `Idempotent-Replay: true`; the same key with a different hash is `409 idempotency_key_reused`.

Chat-derived key: `sha256(platform | team_id | message_ts | action_id | verb)`. **`X-Slack-Request-Id` does not exist and is deleted from `F12 §4.4`**; Slack's real retry headers (`X-Slack-Retry-Num`, `X-Slack-Retry-Reason`) are logged only, and `hash(body)` as a dedupe key is deleted because it collides across legitimately identical commands. `AC-F09-15`: the identical approve/execute request replayed 5× produces exactly one state transition, one audit row and an identical response; a Slack double-click produces one transition.

### 23.9 Rollback safety (SR-27) and `scope_violation`

`model.Snapshot` gains `ResourceVersion`, `Generation`, `UID`, `TakenAt`, `SHA256`.

- **Auto-rollback is refused when the live object's `resourceVersion` differs from the snapshot's.** Instead the action becomes `Failed` with `reason = concurrent_modification`, a `Critical` incident `rollback_refused_concurrent_modification` is raised, and the tenant's approval channels are paged immediately. Restoring a spec over a human's mid-incident edit is worse than not rolling back.
- A **failed rollback always pages**, immediately, regardless of `alerting.min_severity`.
- **`F09 §8`'s open question is Decided (round 1):** a rollback **consumes a budget slot** and is **pre-authorized by the original approval** (no second human approval), because requiring an approval to undo a mutation is the wrong failure mode during an incident.
- `remediate.auto_rollback: true` is added to `01 §7`; `03` diagram 5's rollback branch becomes conditional on it.
- A Kubernetes RBAC denial (`403` from the API server) sets `State = Failed`, `reason = scope_violation`, and raises a `Critical` incident — added to `F09 §5` (`01 §9` and `04 §5.2` already require it).

### 23.10 Config (replaces `01 §7 remediate`)

```yaml
remediate:
  enabled: false
  mode: dryrun                       # dryrun | execute
  executor: dryrun                   # dryrun | kubectl
  allowlist: []                      # subset of the five model.ActionType values
  target_allowlist: []               # ["prod/Deployment/checkout-*"]; empty = nothing allowed
  namespace_allowlist: []            # NEW; also per tenant
  require_approval: true             # cannot be false while mode: execute
  approver_roles: [approver, admin]
  auto_execute_on_approve: false     # DEFAULT FLIPPED; forbidden at RiskTier 3
  action_budget_per_incident: 2
  max_budget_overrides_per_incident: 1   # NEW
  approval_ttl: 15m                  # was 30m
  proposal_ttl: 60m                  # NEW
  verify_window: 10m                 # canonical; F09's 5m deleted
  auto_rollback: true                # NEW
  exec_timeout: 30s                  # NEW
  kubeconfig: ""
  snapshot_dir: ${data_dir}/snapshots
```

**Docs to change:** `01 §4.5`, `01 §5.1` (`action` columns: `expires_at`, `resolved_uid`, `resolved_version`, `risk_tier`, `auto_execute`, `override_*`; `idempotency` DDL), `01 §6.1` (add `/rollback`; `Idempotency-Key` required rows), `01 §7`, `01 §8.2` (execute row corrected to approver-or-admin), `02 §3` (`Guard` method set), `03 §5` (rollback made conditional), `04 §5.2`, `04 §S10`, `F09 §3.1` (FR-F09-3 replaced; new FR-F09-18 expiry-in-transaction, -19 separation of duty, -20 budget invariant, -21 override capability, -22 idempotency, -23 rollback safety), `§4.2`, `§4.3`, `§4.4`, `§5`, `§6`, `§7`, `§8` (**Decided (round 1)** ×2), `F12 §4.4` (delete the nonexistent Slack header), `X-SEC FR-XSEC-3`.

---

## DR-24 — Kubernetes access: no client-go, a pinned `kubectl`, `internal/k8s`, argv slices, no shell

**Resolves:** SR-15, CC-6 (executor half), SR-16 (executor half).

**Decision, with the reasoning stated because it departs from both reviewers' recommendation.**

> Both reviewers recommend `k8s.io/client-go`. The board **rejects** it. client-go pulls roughly forty modules (`k8s.io/api`, `apimachinery`, `klog`, `gnostic`, plus two cloud auth stacks) into a module graph the design deliberately holds at 73 as a security control (`05` D-6/D-9/D-10/D-11), and SR's own positive note 9 names that minimalism as a real achievement. **The reviewers' underlying defect — "neither executor is buildable in the shipped container" — is real, and it is fixed by fixing the image, not by fixing the client.**

- The release image becomes a two-stage build: `gcr.io/distroless/static-debian12:nonroot` **plus exactly one additional file**, `/usr/local/bin/kubectl`, copied from a pinned, digest-verified upstream release. `05 D-13` is amended with the pinned version, the `sha256` digest, the upstream URL, an SBOM entry, a named CVE-tracking owner, and the statement that `readOnlyRootFilesystem: true` is unaffected because the binary is baked in and never fetched at runtime.
- The image still contains **no shell and no package manager**, and `kubectl` is a statically linked Go binary, so `05 D-13`'s "no shell" property survives intact and the `no-shell-interpolation` CI gate remains meaningful rather than vacuous.
- `k8s.io/*` stays on `05 §8.1`'s deliberately-absent list, mechanized by FU-8.
- **`istioctl` is not added.** `F11 FR-F11-3`'s direct `istioctl` path is deleted (DR-36).

```go
package k8s   // imports model + stdlib only (DR-2)

type Verb uint8
const ( VerbGet Verb = 1; VerbScale Verb = 2; VerbRolloutUndo Verb = 3; VerbDeletePod Verb = 4; VerbPatch Verb = 5 )

type Request struct {
    Verb      Verb
    Namespace string
    Kind      model.TargetKind
    Name      string
    Argv      []string          // produced by BuildArgv ONLY; never assembled by a caller
    StdinJSON []byte            // patch bodies travel on STDIN, never on the command line
    DryRun    bool
    Timeout   time.Duration
}

type Result struct { ExitCode int; Stdout, Stderr []byte; Duration time.Duration }

type Executor interface {
    Do(ctx context.Context, r Request) (Result, error)
    Kind() string    // "kubectl" | "dryrun" | "fake"
}

// BuildArgv is the ONLY argv constructor in the system. Its output goes straight to
// exec.CommandContext. There is no shell, no "sh -c", no string join, no interpolation.
func BuildArgv(r Request) ([]string, error)
func MinimalEnv() []string
```

**Binding argv rules:**

1. `exec.CommandContext(ctx, "/usr/local/bin/kubectl", argv...)` with `cmd.Env = k8s.MinimalEnv()` — exactly `KUBECONFIG`, `HOME`, `PATH=/usr/local/bin`. The ambient environment is never inherited.
2. Every argv element is either a compile-time constant or a value that passed `validateName` / `validateNamespace` / a `strconv` conversion. `validateName` is `^[a-z0-9]([-a-z0-9.]{0,251}[a-z0-9])?$` — **`/` is rejected**, and the kind is a **separate argv element** (`"scale", "deployment", "checkout"`), which removes the `ActionTarget.Resource` regex-versus-argv contradiction SR names in its §3.
3. **Patch bodies go to stdin** (`--patch-file=/dev/stdin`), never to argv, so no payload can ever be parsed as a flag.
4. `--namespace` is always explicit and always drawn from the tenant's `NamespaceAllowlist`.
5. **Forbidden-flag denylist**, checked after argv construction: `--all-namespaces`, `-A`, `--kubeconfig`, `--server`, `--token`, `--as`, `--as-group`, `--as-uid`, `-f`, `--filename`, `--raw`, `--insecure-skip-tls-verify`, and any bare `--`. A hit fails the request with `ErrForbiddenFlag` and raises a `Critical` incident — it can only mean a construction bug.
6. `Executor.Do` uses `remediate.exec_timeout` (30 s) and kills the **process group** on cancellation.
7. `remediate.executor: dryrun` satisfies the same interface with zero network I/O and is the default.
8. The eval harness uses the **same** `Executor` and the same `BuildArgv` — there is no second cluster-write path anywhere (DR-36).

**Docs to change:** `05 §D-13` (image amendment with digest and owner), `05 §8.1`/`§8.2` (absent-list entry; kubectl provenance row), `01 §1.2` (`internal/k8s`), `02 §3` (`remediate.KubectlExecutor{-client k8sClient}` replaced by `k8s.Executor`), `F09 §3.1` FR-F09-9, `§4.4`, `§6`, `§7` (the 50-case argv fuzz AC is retained and extended with the denylist), `F11 §3.1` FR-F11-3 (deleted), `X-OPS §6` (image contents).

---

## DR-25 — Auth: one identity model, one RBAC matrix, one audit sink, one rate limiter

**Resolves:** SR-2, SR-19 (role half), SR-28, CC-19, PD-31 (auth half).

### 25.1 The `auth` package interfaces

```go
package auth

type Role string
const ( RoleViewer Role = "viewer"; RoleOperator Role = "operator"; RoleApprover Role = "approver"; RoleAdmin Role = "admin" )

type SubjectKind uint8
const ( SubjToken SubjectKind = 1; SubjOIDC = 2; SubjMTLS = 3; SubjChat = 4; SubjInternal = 5 )

type Subject struct {
    ID        string          // "tok_<id>" | "oidc:<sub>" | "mtls:<cn>" | "rca:<investigationID>"
    Tenant    model.TenantID
    Roles     []Role
    Scopes    []string        // "ingest" is a SCOPE, never a role
    Kind      SubjectKind
    ChatRef   *ChatIdentity
    IssuedAt, ExpiresAt time.Time
}
type Principal = Subject       // X-SEC's auth.Principal becomes an alias for one release, then is deleted

type Authenticator interface {
    Authenticate(ctx context.Context, r *http.Request) (Subject, error)
    AuthenticateChat(ctx context.Context, p ChatRequest) (Subject, error)
    Close() error
}

type Authorizer interface {
    Can(ctx context.Context, s Subject, c Capability, res ResourceRef) error
    SeparationOfDuty(proposer, approver string) error
    Roles(s Subject) []Role
}

type RateLimiter interface {
    Allow(ctx context.Context, k Key) (Decision, error)
    Close() error
}
type LimitClass uint8
const ( LimitIngest LimitClass = 1; LimitAPI = 2; LimitNL = 3; LimitWebhook = 4; LimitChat = 5; LimitMCP = 6 )
type Key struct { Tenant model.TenantID; Subject string; Class LimitClass }

type AuditSink interface {
    Append(ctx context.Context, e Event) (Receipt, error)   // FAIL-CLOSED: an error refuses the operation
    Query(ctx context.Context, tid model.TenantID, f Filter) ([]Event, string, error)
    VerifyChain(ctx context.Context, tid model.TenantID, from, to int64) (VerifyReport, error)
    Anchor(ctx context.Context, tid model.TenantID) (Anchor, error)   // DR-27
}
type AuditLog = AuditSink       // 02's name; X-SEC's AuditSink and F09's private chain are the same component

type SecretSource interface {
    Get(ctx context.Context, ref string) ([]byte, error)              // "env:NAME" | "file:/path"
    Watch(ctx context.Context, ref string) (<-chan []byte, error)     // rotation visible within 60s
}

type EgressDialer interface {
    DialContext(ctx context.Context, network, addr string) (net.Conn, error)
    HTTPClient(timeout time.Duration) *http.Client                    // the ONLY http.Client factory (DR-20)
}

type ChatIdentity struct { Platform, WorkspaceID, PlatformUserID string }
type IdentityBinding struct {
    Platform, WorkspaceID, PlatformUserID string
    SubjectID string
    Tenant    model.TenantID
    CreatedBy string
    CreatedAt time.Time
    Disabled  bool
}
type IdentityStore interface {
    Resolve(ctx context.Context, platform, workspaceID, platformUserID string) (Subject, error)
    Put(ctx context.Context, b IdentityBinding, by Subject) error
    List(ctx context.Context, tid model.TenantID) ([]IdentityBinding, error)
    Delete(ctx context.Context, tid model.TenantID, platform, workspaceID, platformUserID string, by Subject) error
}
```

### 25.2 The RBAC matrix — the single table every document cites

Roles are a **capability matrix, not a hierarchy.** `X-SEC`'s `admin ⊇ approver ⊇ operator ⊇ viewer` is **deleted**: a linear order silently gives every approver the propose capability, which defeats separation of duty (CC-19, SR §3).

| Capability | viewer | operator | approver | admin |
|---|:--:|:--:|:--:|:--:|
| `telemetry:read` (traces, spans, RED, topology, services) | ✅ | ✅ | ✅ | ✅ |
| `incident:read` | ✅ | ✅ | ✅ | ✅ |
| `incident:investigate` | — | ✅ | — | ✅ |
| `incident:suppress` | — | ✅ | — | ✅ |
| `investigation:read` (steps, evidence, export) | ✅ | ✅ | ✅ | ✅ |
| `investigation:replay` | — | ✅ | — | ✅ |
| `investigation:correct` | — | ✅ | ✅ | ✅ |
| `investigation:abort` | — | ✅ | — | ✅ |
| `memory:read`, `memory:export` | ✅ | ✅ | ✅ | ✅ |
| `memory:write` (import, delete) | — | — | — | ✅ |
| `sampler:interest:write` | — | ✅ | — | ✅ |
| `remediation_action:propose` | — | ✅ | **—** | ✅ |
| `remediation_action:approve`, `:reject` | — | — | ✅ | ✅ |
| `remediation_action:execute` | — | — | ✅ | ✅ |
| `remediation_action:budget_override` | — | — | — | ✅ |
| `audit:read`, `audit:verify` | — | — | — | ✅ |
| `config:read` | — | — | — | ✅ |
| `tenant:read`, `tenant:write` | — | — | — | ✅ |
| `identity:bind` | — | — | — | ✅ |
| `eval:run` | — | — | — | ✅ |
| `eval:read` | ✅ | ✅ | ✅ | ✅ |
| `nl:ask` | ✅ | ✅ | ✅ | ✅ |
| `mcp:connect` | ✅ | ✅ | ✅ | ✅ |

Consequences recorded explicitly: an **approver cannot propose**, so separation of duty is structural; an **operator cannot approve or execute**; **`audit:read` is admin**, settling `01` (admin) against `F09`/`F12` (viewer). A "viewer-redacted audit projection" may be added later only with a documented need.

### 25.3 Tokens, TLS-adjacent identity, and the deleted JWT model

`01 §8`'s opaque `tiq_<tokenID>_<secret>` with argon2id at rest, `expires_at` and an explicit `revoked_at` is **canonical**. `X-SEC §3.2`'s short-TTL JWT model is deleted, and with it `auth.token_ttl`, the signing-key configuration, and `X-SEC §8`'s "revocation not yet designed" open question — `01` had already solved revocation. OIDC survives as an inbound `auth.mode`; an OIDC subject's roles come from `tenant.Policy.RBACBindings` unless `auth.oidc.role_claim` is explicitly configured **and** the tenant policy permits claim-derived roles. Audit retention is **2555 d**, with a per-tenant override no lower than 365 d (`X-SEC`'s 365 d default is deleted).

**Revocation test:** a revoked token is `401` on the **next** request, not at TTL expiry.

### 25.4 Chat identity binding — `X-SEC §4.6` (new, normative)

> **An HMAC signature is an authenticity control, never an authorization control.**

An inbound chat request is authenticated only after `(platform, workspace_id, platform_user_id)` resolves through `auth.IdentityStore` to a provisioned `Subject`. An unmapped user gets **`403 identity_not_bound`** and an audit row. Approval-capable interactions are further restricted to the channel IDs in `tenant.Policy.ChatBinding`.

```sql
CREATE TABLE identity_binding (
  tenant_id TEXT NOT NULL, platform TEXT NOT NULL, workspace_id TEXT NOT NULL,
  platform_user_id TEXT NOT NULL, subject_id TEXT NOT NULL,
  created_by TEXT NOT NULL, created_at INTEGER NOT NULL, disabled INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (tenant_id, platform, workspace_id, platform_user_id)
) WITHOUT ROWID;
```

Admin endpoints `POST /v1/identities`, `GET /v1/identities`, `DELETE /v1/identities/{id}` (DR-29). Every `F10`/`F12` row reading "signature-verified, **no RBAC role**" becomes **"signature-verified and principal-resolved; RBAC per `25.2`"**.

**ACs:** `AC-XSEC-7` — route enumeration asserts **no** endpoint reaches a handler on signature alone. `AC-XSEC-8` — a signed Slack approve from (i) an unmapped user → `403` + audit, (ii) a mapped `viewer` → `403` + audit, (iii) a mapped `approver` → `200`.

### 25.5 Rate limiting

One `auth.RateLimiter`; `ingest.RateLimiter` (`02 §1`) becomes its ingest-scoped construction, not a second type. Bounded: an LRU of at most `auth.rate_limit.max_keys` (**65 536**) `golang.org/x/time/rate.Limiter` values, with `traceiq_auth_ratelimit_keys_evicted_total`. Per-class values are in DR-26's limits table.

**Docs to change:** `00` (core interface list), `01 §8.1`–`§8.5`, `01 §8.2` (the matrix above replaces the prose), `01 §5.1` (`identity_binding`), `01 §6.1` (identity endpoints; audit role), `01 §7` (`auth.*`), `02 §4` (`Subject`, `AuditLog`, `RateLimiter`, `SecretSource`, `EgressDialer`, `IdentityStore`), `F09 §4.2`/`§4.3`, `F10 §4.3`, `F12 §4.2`/`§4.3`, `X-SEC §3.2`, `§4.2`, `§4.3`, `§4.6` (new), `§8` (delete the revocation question).

---

## DR-26 — Listeners, TLS, profiles, and one limits table

**Resolves:** SR-3, SR-18, SR-20, SR-29, CC-31 (profile half).

### 26.1 Two orthogonal axes, one name each

- **`server.mode: single | gateway | sampler | brain | api`** — what this process runs. `X-OPS`'s `ops.role` is deleted and `collector` is renamed **`gateway`**.
- **`server.profile: dev | prod`** — safety gating only. **`deploy.mode`, `ops.mode` and the bare `mode=dev` are deleted from every document.** A gate keyed on a field that does not exist in `01 §7`'s schema never fires, and since unknown keys are a startup error, a config written against `F01 §6` would not even boot.

### 26.2 Defaults, flipped

```yaml
server:
  mode: single
  profile: dev
api:
  endpoint: "127.0.0.1:8443"          # was 0.0.0.0:8080
auth:
  tls: { enabled: true, cert_file: "", key_file: "", client_ca_file: "", min_version: "1.3" }
ingest:
  otlp_grpc:   { endpoint: "127.0.0.1:4317", tls: { enabled: true, cert_file: "", key_file: "", client_ca_file: "" } }
  otlp_http:   { endpoint: "127.0.0.1:4318", tls: { ... } }
  jaeger_grpc: { endpoint: "", tls: { ... } }
  zipkin_http: { endpoint: "", tls: { ... } }
  auth:                                # NEW — ingest auth was not expressible at all
    mode: token                        # none | token | mtls
    tokens_file: ${data_dir}/ingest_tokens.json
    client_ca_file: ""
selfobs:
  metrics_endpoint: "127.0.0.1:9464"   # was 0.0.0.0:9464
```

On `profile: dev` with `tls.enabled: true` and no cert/key, the server generates a self-signed loopback certificate into `${data_dir}/tls/` at first start and logs its fingerprint. On `profile: prod`, a missing cert/key is **exit 2**.

### 26.3 Startup validation rules (each names both offending keys on exit)

1. **Any listener bound to a non-loopback address requires TLS and requires its `auth.mode != none`** — `api`, every `ingest.*`, and `selfobs.metrics_endpoint`. Violation ⇒ **exit 2**. This is the rule that makes `auth.mode: token` + `tls.enabled: false` + `0.0.0.0` — a *valid* configuration today that ships bearer tokens in cleartext — impossible.
2. `server.profile: prod` with any `auth.mode: none` ⇒ exit 2.
3. `auth.mode: none` requires `server.profile: dev` **and** a loopback listener, and is the only condition under which `tenant.Resolver.FromDevDefault` is legal (DR-3).
4. `alerting.gate: immediate` ⇒ exit 2 (DR-21).
5. `tenancy.enabled: true` + a network correlate driver + `tenant_mode: none` ⇒ exit 2 (DR-20).
6. `tenancy.enabled: true` + `store.cost.byte_budget_per_tenant_gb: 0` + more than one tenant ⇒ exit 2 (DR-12).
7. Derived kept-span rate > `store.hot.max_kept_spans_per_sec` with `store.hot.driver: sqlite` ⇒ exit 2 (DR-6).
8. `rca.reasoner: llm` requires a resolvable API key **and** a non-zero `rca.llm.pricing` block (DR-17, DR-34).
9. `remediate.mode: execute` requires `require_approval: true`, a non-empty `allowlist`, a non-empty `target_allowlist`, `auth.mode != none`, **and `server.profile: prod`** — remediation execute is refused on the dev profile.
10. `anomaly.baseline.max_keys × tenants > max_keys_global` ⇒ exit 2 (DR-14).

### 26.4 The single limits table (owned by `01 §8.4`; `X-SEC §3.1` and `F01 §6` cite it and declare nothing)

| Limit | Value | Applies to | Over-limit behaviour |
|---|---|---|---|
| API request body | **1 MiB** | every `/v1/*` except `/v1/traces` | `413` |
| Ingest request body | **4 MiB** | `/v1/traces`, OTLP HTTP | `413` / gRPC `RESOURCE_EXHAUSTED` |
| Spans per batch | 10 000 | all receivers | reject batch + `traceiq_ingest_batches_rejected_total{reason="too_many_spans"}` |
| Span size (post-normalization) | 512 KiB | normalizer | drop span + counter |
| Attributes per span | 128 | normalizer | drop excess, `DroppedAttrsCount++` |
| **Attribute value** | **4 KiB** | normalizer | **truncate + flag** by default; `ingest.limits.oversize_attr: truncate\|reject` per tenant |
| Attribute key | 256 B | normalizer | drop attribute |
| Events / links per span | 128 / 128 | normalizer | drop excess + dropped counters |
| API rate | **100 rps**, burst 200, per subject | `LimitAPI` | `429` + `Retry-After` |
| Ingest rate | 20 000 spans/s per token, burst 2× | `LimitIngest` | `429` / `RESOURCE_EXHAUSTED` |
| NL rate | **20/min**, burst 5, per subject | `LimitNL` | `429` |
| Webhook rate | 5 rps, burst 10, per tenant | `LimitWebhook` | `429` |
| Chat rate | 1 rps, burst 5, per platform user | `LimitChat` | `429` |
| MCP rate | 10 rps, burst 20, per token | `LimitMCP` | JSON-RPC `-32000` |

**4 KiB is chosen deliberately**: `X-SEC §5` lists "payload capped at 4 KiB per attribute" as a prompt-injection mitigation, and `01 §8.4`'s 8 KiB doubled the injection budget the mitigation was sized against. `01 §8.4`'s 8 KiB is deleted. The rate limits are expressed with explicit units and bursts, per class, so the 100-rps-versus-100-per-minute 60× discrepancy cannot recur; NL keeps its per-minute framing because that is how the limit is reasoned about.

`F01 §6`'s "reject, don't truncate — truncation would silently corrupt evidence" becomes the **per-tenant strict option** (`oversize_attr: reject`). The default is `truncate`, and truncation is **never silent**: `Span.Truncated` and `DroppedAttrsCount` are set, the UI renders it, and D-D3's evidence-fidelity claim is restated as *"flagged, never silent"* rather than *"never truncated"*.

### 26.5 TLS floor and key-name reconciliation

TLS **1.3** minimum everywhere. 1.2 is permitted only by an explicit per-listener `min_version: "1.2"`, logged at WARN at startup and surfaced on `GET /v1/config`. `X-SEC`'s "default 1.2" is deleted.

Canonical key names, cited verbatim by `X-SEC`, `F01` and `F12`: `auth.tls.min_version` (**not** `auth.tls_min_version`), `api.endpoint` (**not** `api.listen_addr`), `selfobs.metrics_endpoint` (**not** `MetricsAddr` / `:9090`), `ingest.otlp_grpc.endpoint` (**not** `ingest.otlp.grpc.port`).

**Metrics endpoint (SR-29):** one port, one default — `127.0.0.1:9464`, unauthenticated on loopback. Binding it to a routable address **requires** auth (rule 1). `X-OPS §6` gains a note that metric labels are tenant-sensitive (service names, keep rates, LLM spend, action states) and must not be exposed cross-tenant; `AC-XOPS-10`: no per-tenant series on a shared or routable endpoint without auth.

**Docs to change:** `01 §7` (defaults, the ten validation rules, `ingest.*.tls`, `ingest.auth`, `ingest.limits`), `01 §8.1` (TLS floor), `01 §8.4` (the limits table above), `01 §6.1` (`/metrics` row), `02 §1` (`ingest.RateLimiter` folded into `auth.RateLimiter`), `04 §2` (mode gating uses `server.mode`), `F01 §3.2`, `§4.3`, `§6`, `F12 §4.3`, `X-SEC §3.1` (becomes a citation), `FR-XSEC-1`, `FR-XSEC-5`, `FR-XSEC-6`, `X-OPS §4.2` (delete `ops.mode`/`ops.role`), `§6`.

---

## DR-27 — Audit chain: one hash definition, an external anchor, one appender

**Resolves:** SR-11.

### 27.1 One hash, domain-separated and length-prefixed

Owned by `X-SEC §4.2`; `01 §5.1` and `F09 §4.2` **cite** it and state no formula of their own.

```
LP(x)  = uint32be(len(x)) ‖ x
H(0)   = SHA256( "traceiq.audit.v1" ‖ LP("genesis") ‖ LP(tenantID) )
H(n)   = SHA256( "traceiq.audit.v1" ‖ LP(H(n−1)) ‖ LP(uint64be(seq)) ‖ LP(uint64be(tsUnixNano)) ‖ LP(canonicalJSON(row_without_hash)) )
canonicalJSON = RFC 8785 (JCS)
```

The actor, event type, subject, tenant, resource and outcome are **inside `row_without_hash`**, so they are covered directly. `F09`/`X-SEC`'s separate `PayloadHash` preimage — whose canonicalisation was undefined — is **deleted**, and length prefixing removes the collision ambiguity of bare `‖` concatenation.

### 27.2 One chain per tenant, one appender

| Deployment | Appender |
|---|---|
| `server.mode: single` | the `control.db` writer goroutine |
| multi-replica | the **leader** (`cluster.leader_election`). A non-leader `api` replica forwards the append over the internal control channel; if that is unavailable it **refuses the operation** (fail-closed, `04 §5.2`) |
| `store.hot.driver: clickhouse` | unchanged — **the audit chain always lives on `control.db` (SQLite)**, in every driver configuration |

Pinning audit to SQLite removes SR-11(iii) entirely rather than designing a ClickHouse sequencer for it: ClickHouse has neither `AUTOINCREMENT` nor the `BEFORE UPDATE/DELETE` triggers the design depends on, and the audit volume never needs a columnar store. DR-6 already places `audit_log` and `audit_anchor` in `control.db` with `synchronous=FULL` and a p99 append ≤ 15 ms independent of the telemetry batcher (PD-17).

### 27.3 The external anchor — this is what makes the chain anti-tamper rather than anti-typo

An unkeyed chain in the same file, written by the same process, is a corruption detector: anything that can write the file can recompute the whole chain. Every `auth.audit.anchor_interval` (**5 m**) and at shutdown, the appender writes a **signed checkpoint** `{tenant, seq, hash, at}` — Ed25519, key from `auth.SecretSource` — to a destination the TraceIQ process cannot rewrite:

```yaml
auth:
  audit:
    enabled: true
    path: ${data_dir}/audit              # NDJSON mirror
    hash_chain: true
    anchor_interval: 5m                  # NEW
    anchor_sink: file                    # NEW: file | object_lock | syslog
    anchor_dir: ${data_dir}/audit/anchors        # dev
    anchor_object_prefix: ""             # prod: an object-lock / WORM bucket prefix
    anchor_key_ref: env:TRACEIQ_AUDIT_ANCHOR_KEY
    anchor_syslog_addr: ""
    retention_days: 2555
```

`object_lock` and `syslog` are write-once destinations. `anchor_sink: file` is **dev-only**; on `server.profile: prod` it is a startup **warning** rather than an error — an air-gapped operator may genuinely have no WORM target — and the weakness is surfaced on `GET /v1/config` as `audit_anchor_weak: true` so it cannot be forgotten.

`VerifyChain` compares the live chain against the **last anchor**, so an offline full-chain recompute is detected. `GET /v1/audit?verify=true` (**admin**) returns `{ok, last_anchor_seq, last_anchor_at, first_divergent_seq}`.

```sql
CREATE TABLE audit_anchor (
  tenant_id TEXT NOT NULL, seq INTEGER NOT NULL, hash TEXT NOT NULL,
  at INTEGER NOT NULL, signature BLOB NOT NULL, sink TEXT NOT NULL,
  PRIMARY KEY (tenant_id, seq)
) WITHOUT ROWID;
```

### 27.4 Tests (`AC-XSEC-9` … `-12`)

1. **Tamper** — edit one row; verification breaks at exactly that `seq`.
2. **Rewrite** — recompute the entire chain offline; detected via anchor mismatch. *Today's specification fails this test; that is the point of §27.3.*
3. **Concurrency** — 4 writers × 1 000 appends ⇒ strictly monotonic `seq`, no forks.
4. **Role** — a `viewer` gets `403` on `/v1/audit`.

**Docs to change:** `X-SEC §4.2` (the formula above, normative), `01 §5.1` (`audit_log` cites it; `audit_anchor` DDL), `01 §6.1` (audit role = admin; `?verify=true`), `01 §7` (`auth.audit.*`), `01 §8.5`, `02 §4` (`AuditLog.Anchor`), `04 §5.2` (non-leader fail-closed), `F09 §3.1` FR-F09-8 (cites, does not restate), `§4.2`, `F12 §4.3` (audit role).

---

## DR-28 — Ingest: bounded queues, one overflow policy, a measured allocation budget

**Resolves:** SR-8, PD-20, PD-19 (allocation half), SR-20 (ingest half).

### 28.1 `01 §3.1` is adopted verbatim in `F01`

```yaml
ingest:
  queue:
    capacity: 1024                 # BATCHES. F01's 100000 is DELETED (100k x 4 MiB is ~400 GB, not a queue)
    max_bytes: 268435456           # 256 MiB — the HARD byte bound, and the binding one
    enqueue_timeout: 100ms
    overflow_policy: shed          # shed | block_up_to_timeout.  "block" (unbounded) is DELETED
```

- The effective bound is `min(capacity batches, max_bytes)`. At 4 MiB per batch, 1 024 batches is 4 GiB nominal, so **`max_bytes` binds at 256 MiB** — the queue is bounded in bytes, as PD-20 requires.
- `overflow_policy: block` is deleted. `block_up_to_timeout` blocks for at most `enqueue_timeout` and then sheds; it exists only so the old spelling has a defined meaning.
- **No receiver goroutine may block unboundedly.** `blockingPush` is deleted. Every receiver uses
  `select { case ch <- b: ; case <-clock.After(enqueue_timeout): ; case <-ctx.Done(): }`
  and on timeout returns gRPC `RESOURCE_EXHAUSTED` or HTTP `429` with `Retry-After: 1`.
- `01 §3.1`'s per-shard rule is restated in `F01 §4.4`: a blocking send with a **50 ms** timeout, then drop with `traceiq_ingest_spans_dropped_total{reason="shard_full"}` — and the span's RED contribution has **already** been recorded at the router (DR-9), so the counter is a body-loss counter, not an accuracy loss.
- **`AC-F01-9`:** load at 2× ingest capacity ⇒ 429s rise, RSS stays under `01 §10.2`'s ceiling, goroutine count stays bounded, and a goroutine-dump assertion shows **zero** handlers blocked longer than `enqueue_timeout`.

### 28.2 The allocation budget replaces "zero allocation per span"

`F01 §3.2`'s "zero allocation-per-span on the OTLP protobuf hot path" is not credible against a per-span map plus a pointer per attribute, and is **deleted**. It is replaced by a measured, CI-enforced budget (`01 §10.1` gains these three rows):

| Path | Budget | Gate |
|---|---|---|
| OTLP protobuf → `model.Span` | **≤ 6 allocs/span, ≤ 512 B/span** | `go test -bench -benchmem`; CI fails at +10 % regression |
| Jaeger gRPC → `model.Span` | ≤ 8 allocs/span, ≤ 640 B/span | same |
| Zipkin JSON → `model.Span` | ≤ 14 allocs/span, ≤ 1 024 B/span | same |

Mechanism (DR-4's attribute decision, given an implementation):

- `ingest.KeyInterner` — a sharded `map[string]string` with `max_keys: 4096` and LRU eviction, so a repeated attribute key allocates once per process, not once per span.
- `Span.AttrSorted() []model.KV` is built into a per-worker slice drawn from a `sync.Pool`; the limiter and the indexer read it **without allocating**.
- `Span.Attrs` (`AttrMap`) is materialised **lazily**, only when a control-plane caller asks for it — so the hot path never builds a map.

CI publishes `allocs/span` and `bytes/span` per protocol on every commit.

### 28.3 Config-schema reconciliation

`F01`'s `ingest.otlp.grpc.port` shape is deleted; `01 §7`'s `ingest.otlp_grpc.endpoint` is canonical. Since `01 §7` makes unknown keys a **startup error**, the two schemas cannot coexist and a config written against `F01` would fail to boot — so this is a build-blocking reconciliation, not a cosmetic one.

**Docs to change:** `01 §3.1` (unchanged — now cited), `01 §7`, `01 §10.1` (allocation rows), `01 §10.2`, `02 §1`, `04 §4`, `04 §6` (invariant 2 cites the byte bound), `F01 §3.1` FR-F01-6, `§3.2` (delete the zero-allocation claim; adopt the three budgets), `§4.2` (delete the duplicate `model` block — DR-4), `§4.3` (adopt `01 §7`'s key paths), `§4.4` (delete `blockingPush`), `§6`, `§7` (AC-F01-9).

---

## DR-29 — One API surface: the endpoint table, roles, the MCP tool set, idempotency

**Resolves:** SR-19, CC-13 (table half), SR-25 (surface half), CC-21 (endpoint half).

### 29.1 One prefix, one table

The prefix is **`/v1`**. Every `/api/v1/...` path in `F04`, `F05`, `F06`, `F08`, `F09`, `F10` and `F12` is deleted; each feature doc renders a **filtered view** of `01 §6.1` under the header "defined in `01 §6.1`" and adds no path of its own. `/api/v1` buys nothing, since the SPA is served from `/ui/*`.

**Rows added to `01 §6.1`:**

| Method | Path | Purpose | Role |
|---|---|---|---|
| POST · GET | `/v1/identities` | chat identity binding (DR-25) | admin |
| DELETE | `/v1/identities/{id}` | remove a binding | admin |
| GET | `/v1/sampler/interest` | active interest predicates with `NarrowLevel` and `Hits` (DR-11) | viewer |
| DELETE | `/v1/sampler/interest/{id}` | remove a predicate | operator |
| GET | `/v1/sampler/stats` | keep rate by `model.KeepReason`, shed counts, ring epoch | viewer |
| GET | `/v1/store/budget` | disk usage, watermark, tier occupancy, `store_hot_index_ratio` | viewer |
| GET | `/v1/baselines` | anomaly baselines and warm state (DR-14) | viewer |
| GET | `/v1/deploys` | deploy markers in a window | viewer |
| GET | `/v1/tenants/{id}/erasure` | tombstone / erasure SLA status (DR-7) | admin |
| POST | `/v1/actions/{id}/rollback` | manual rollback (DR-23) | approver |

**Corrections to existing rows:** `GET /v1/audit` → **admin** (was viewer in `F09`/`F12`), and gains `?verify=true`; `POST /v1/investigations/{id}/replay` → **operator**, gains `?mode=recorded|live-diff` (DR-18); `POST /v1/eval/runs` → **admin**; correction stays scoped to a **step** (`/v1/investigations/{id}/steps/{stepID}/correct`) and `F12`'s investigation-level `/correct` is deleted; every `/v1/chat/*` and `/v1/webhooks/*` Auth cell becomes **"HMAC **and** principal-resolved (DR-25); RBAC per the capability matrix"** — the string "no RBAC role" appears nowhere in the document set.

**`Idempotency-Key` is required** on `POST /v1/actions`, `/approve`, `/reject`, `/execute`, `/rollback`, and on `POST /v1/webhooks/deploy`; optional elsewhere (DR-23 §23.8).

### 29.2 MCP: one path, twelve tools, gated per tool

Path **`/v1/mcp`** (`F12`'s `/mcp` is deleted). The tool set is `01 §6.2`'s thirteen **minus one**:

> **`traceiq_propose_action` is not exposed over MCP in v1.** `01 §6.2`'s own reasoning — an approval must name a person — applies equally to a proposal that lands in an approver's queue with an MCP token as its author. Re-adding it is a future DR.

| Tool | Backing call | MinRole |
|---|---|---|
| `traceiq_search_traces` | `store.SearchSpans` | viewer |
| `traceiq_get_trace` | `store.GetTrace` | viewer |
| `traceiq_query_red` | `store.QueryRED` | viewer |
| `traceiq_topology_neighbors` | `topology.Neighbors` | viewer |
| `traceiq_topology_edges` | `topology.Edges` | viewer |
| `traceiq_list_incidents` | incident query | viewer |
| `traceiq_get_investigation` | investigation + steps + evidence | viewer |
| `traceiq_search_memory` | `memory.Similar` | viewer |
| `traceiq_correlate_logs` | `correlate.LogsForTrace` | viewer |
| `traceiq_correlate_metrics` | `correlate.MetricsForSpan` | viewer |
| `traceiq_ask` | `nl.Answerer` | viewer |
| `traceiq_start_investigation` | `rca.Engine.Investigate` | **operator** |

Authorization is **per tool**, through `api.MCPTool.MinRole` (which `02 §4` already declares and `F12` omitted). `F12`'s endpoint-level `operator` gate is deleted — it would hand any MCP client operator across the board. **An MCP token defaults to `viewer` scope**; `operator` is an explicit property of the token. **No MCP tool approves, executes, rejects, imports memory, writes config or mutates a tenant.**

### 29.3 Verification

- **`AC-XSEC-1` (extended):** the live route set **equals** the `01 §6.1` table exactly — no extra routes, no missing routes — and every route resolves a principal before its handler runs. This is the test `X-SEC`'s `actionRoleTable` could not previously be built from.
- **`AC-XSEC-6`:** a viewer-scoped MCP token calls every read tool successfully and is refused `traceiq_start_investigation` with JSON-RPC `-32000`.
- **`AC-XSEC-14`:** no endpoint is reachable on an HMAC signature alone.

**Docs to change:** `01 §6.1` (rows + role corrections), `01 §6.2` (twelve tools; per-tool roles), `01 §7` (`api.mcp_path: /v1/mcp`), `02 §4` (`MCPTool` shape retained), `F04 §4.3`, `F05 §4.3`, `F06 §4.3`, `F08 §4.3`, `F09 §4.3`, `F10 §4.3`, `F12 §3.1` FR-F12-6, `§4.3` (filtered views only), `X-SEC §5`.

---

## DR-30 — UI: nine screens, and the rule that every `D-*` claim renders somewhere

**Resolves:** CC-21, CC-24 (the `api.Server` rename).

| # | Screen | What it shows | Endpoints | Min role |
|---|---|---|---|---|
| 1 | **Overview** | service health, open incidents, a **"below threshold" candidates panel** (sub-`min_event_score` events and `Status = Candidate` incidents — where alert-fatigue tuning actually happens), deadman status | `/v1/services`, `/v1/incidents`, `/v1/anomalies`, `/v1/baselines` | viewer |
| 2 | **Traces** | search, waterfall, span detail, `AnsweredFrom=hot|cold` badge (the D-T1 surface) | `/v1/traces`, `/v1/search` | viewer |
| 3 | **Topology** | graph, RED per edge, new/vanished badges, **export** (D-Y4) | `/v1/topology`, `/v1/topology/{s}/neighbors`, `/v1/topology/export` | viewer |
| 4 | **Investigations** | step timeline, evidence, hypotheses with `Category`/`Component`, **replay (recorded and live-diff drift view)**, **corrections history**, export | `/v1/investigations*` | viewer (replay/correct: operator) |
| 5 | **Remediation** | proposals showing the **Guard-reconstructed payload diff beside the model's rationale, with the rationale marked untrusted**, approvals, TTL countdown, budget usage and overrides, audit with chain-verification status | `/v1/actions*`, `/v1/audit?verify=true` | viewer (approve/execute: approver; audit: admin) |
| 6 | **Chat** | NL console over `/v1/ask` with evidence links and mini-waterfalls | `/v1/ask` | viewer |
| **7** | **Eval** *(new)* | runs, per-scenario results, top-1/top-3, evidence precision/recall, time-to-RCA, cost, **baseline diff and the regression verdict** | `/v1/eval/runs`, `/v1/eval/runs/{id}` | viewer (run: admin) |
| **8** | **Cost & Retention** *(new)* | disk budget and watermark, tier occupancy T0–T6, **keep rate by `model.KeepReason`**, shed events, `store_hot_index_ratio`, LLM spend per tenant per day, erasure SLA, plus a **Sampler panel**: active interest predicates, `NarrowLevel`, `Hits` | `/v1/store/budget`, `/v1/sampler/stats`, `/v1/sampler/interest`, `/v1/tenants/{id}/erasure` | viewer |
| **9** | **Memory** *(new)* | search, record detail with **provenance and weight**, runbook import, export, correction history | `/v1/memory*` | viewer (import/delete: admin) |

**The traceability rule, added to `F12 §7`:** *every `D-*` row in `01 §9` whose mechanism produces a user-visible artefact names the screen that renders it*, checked by a docs-CI rule (DR-38). The mapping:

| D-ID | Screen |
|---|---|
| D-T1 (fast attribute search) | 2 |
| D-D1 (cost visible and capped) | 8 |
| D-D4 (investigation-gated paging) | 1 + 5 |
| D-D5 (published, re-runnable accuracy) | 7 |
| D-Y1 (replayable RCA) | 4 |
| D-Y4 (exportable learning) | 3 + 4 + 9 |
| D-X4 (guardrailed remediation) | 5 |
| D-X5 (sampler/agent loop) | 8 (Sampler panel) |

Three of the new screens are the **only** UI surface for a `D-*` claim, which is why none may be deferred silently; a deferral must be written into `F12 §8` with a phase tag.

**`api.Server` collision (CC-24).** `F12 §4.2`'s `type Server struct` is renamed **`api.HTTPServer`** and its inline dependency fields are replaced by **`api.Deps`**, matching `02 §4`'s shape (`api.Server` interface + `api.HTTPServer` impl + `api.Deps` wiring). This is what lets `server.mode: api` be wired without brain components.

**Docs to change:** `F12 §3.1` (new FR-F12-16 Eval screen, FR-F12-17 Cost & Retention screen, FR-F12-18 Memory screen, FR-F12-19 untrusted-rationale marker beside the reconstructed payload), `§4.2` (rename), `§4.3` (the table above), `§7`, `§8` (phase tags for anything deferred), `01 §9` (each row names its screen).

---

## DR-31 — `model.Clock`: one time source, enforced

**Resolves:** PD-16 (clock half); prerequisite for DR-36.

`model.Clock` (declared in DR-4) is constructed once in `cmd/traceiq` and **injected into every component that reads time**: `ingest` (receive timestamps, enqueue timeout), `sampler` (idle/hard timeouts, timer wheel, token buckets, WAL flush), `anomaly` (eval ticker, season slot, debounce, grouping window, dedupe TTL, cold-start), `topology` (bucket boundaries, `vanished_after`), `store` (retention sweeps, seal timers, cost-signal interval, backup cadence), `correlate` (cache TTL, breaker), `memory` (decay, consolidation), `rca` (wall clock, step timeout, budget windows), `remediate` (approval TTL, verify window, expiry poller), `auth` (token expiry, rate limiter), `eval`.

```go
package model

type Ticker interface { C() <-chan time.Time; Stop() }
type Timer  interface { C() <-chan time.Time; Stop() bool; Reset(time.Duration) bool }
func NewRealClock() Clock

// Barrier lets a virtual clock know when a component is idle.
type Barrier interface { Name() string; Pending() int }
```

**Binding rule, enforced by `internal/archtest`:** `time.Now`, `time.Since`, `time.After`, `time.Tick`, `time.NewTimer` and `time.NewTicker` are **forbidden in every `internal/*` package** except `internal/model` (which declares `RealClock`) and `cmd/traceiq`. A small allowlist file covers the genuine exceptions (`selfobs` metric exposition timestamps). This is the mechanism PD-16(b) found missing from `01`, `02` and `04` entirely.

**Randomness is seeded, never global.** `math/rand`'s package-level functions are forbidden by the same test. Every non-cryptographic source is an explicit `*rand.Rand` seeded from `Investigation.ReplaySeed` (DR-18) or `eval.RunOptions.Seed` (DR-36). `crypto/rand` is unrestricted.

```go
package eval

// VirtualClock advances only when every registered Barrier reports Pending() == 0,
// so a 30-minute scenario runs in seconds and is bit-reproducible.
type VirtualClock struct{ /* ... */ }
func (c *VirtualClock) Advance(d time.Duration)
func (c *VirtualClock) AwaitQuiescence(ctx context.Context) error
func (c *VirtualClock) Register(b model.Barrier)
```

Quiescence — not a sleep — is what makes the eval suite deterministic instead of racy: the clock jumps to the next scheduled timer only when the sum of `Pending()` across all registered barriers is zero.

**Docs to change:** `01 §4.1` (`Clock`, `Ticker`, `Timer`, `Barrier`), `01 §1.2` (wiring note), `02 §1`–`§4` (constructors take a `Clock`), `04 §1` (`cmd/traceiq` build order includes the clock), `05 §9` (new FU-10: land `archtest`'s time/rand ban before the first feature merge, since retrofitting it is far more expensive), every `F0x §4.3` constructor signature, `F11 §4.1`/`§4.4`.

---

## DR-32 — Profiles, drivers, prod topology, and a capacity table anyone can recompute

**Resolves:** PD-30, CC-7, CC-31 (topology half), PD-21 (capacity half).

### 32.1 No external datastore in the production topology

`X-OPS §4.1`'s `PGV[(Postgres+pgvector — memory.Store)]` box is **deleted**. `01 §1` ("no required external database"), `02` (`memory.HybridStore → store.Store`), `F08 §3.2` ("no external vector database required by default") and `05 §8` all already agree; X-OPS was carrying un-reconciled PRD residue.

- `pgvector`, `lib/pq` and `pgx` join `05 §8.1`'s **deliberately-absent list**, mechanized by FU-8.
- `05 §8` gains an explicit supersession row, mirroring how `05 D-2` superseded the "Python/Go services" row: *"PRD § Technology stack summary: Memory = Postgres + pgvector → superseded by `store`-backed `memory.HybridStore` (D-J5)."*
- If an external vector store is ever wanted at scale it must arrive as a `memory.Backend` implementation with pins and an ADR amendment — not as a box in a diagram.

### 32.2 One workload vocabulary

`server.mode` is the only mode/role axis (DR-26). `collector` → **`gateway`**, deployed as a **Deployment with HPA 2..50** (`01 §3.2`). The DaemonSet is deleted: Envoy access logs reach TraceIQ over OTLP from the mesh's own exporter (DR-38 gives that an FR), so node-local collection is not required. If it is ever wanted, it is a Phase 3 item with its own DR. `X-OPS`'s capacity table, which currently sizes "collector pods (1/node)", is regenerated per §32.4.

### 32.3 Drivers

| Key | Default | Alternatives |
|---|---|---|
| `store.hot.driver` | `sqlite` | `clickhouse` **[P2]** — **required** above `store.hot.max_kept_spans_per_sec` (1 200), per DR-6 §6.4 |
| `store.cold.driver` | `parquet_local` | `parquet_s3` |
| `cluster.bus.driver` | **`none` — in every mode, including Kubernetes** | `kafka`, `redpanda`, only when one of `05 D-5`'s three triggers holds |

The bus default closes **CC-33(1)**: the in-process ring is the default everywhere; Kafka is never mandatory because the deployment is Kubernetes.

### 32.4 The capacity table, derived from five formulas

Published in `01 §10.1` and **cited** by `X-OPS §4.4`, which stops asserting numbers of its own:

```
kept_spans_per_sec = ingest_spans_per_sec × effective_keep_rate        # keep rate per DR-10; 0.04 at the dev profile
hot_bytes_per_day  = kept_spans_per_sec × 86400 × 750 B                # 750 B/kept span, DR-6 §6.4
cold_bytes_per_day = kept_spans_per_sec × 86400 × 512 B ÷ compression  # compression >= 8, 01 §10.2
shards_needed      = ceil(ingest_spans_per_sec ÷ 20000)                # per_shard_assembly_rate, F02 §3.2
gateway_pods       = ceil(ingest_spans_per_sec ÷ (45000 × cores))      # per-core decode rate, 01 §10.1
```

| Band (spans/s) | kept/s | hot GiB/day | cold GB/day | shards | gateway pods (8 cores) |
|---:|---:|---:|---:|---:|---:|
| 1 000 | 40 | 2.4 | 0.22 | 1 | 1 |
| 10 000 | 400 | 24.1 | 2.2 | 1 | 1 |
| 50 000 | 2 000 | 120.7 | 11.1 | 3 | 2 |
| 200 000 | 8 000 | 482.8 | 44.2 | 10 | 6 |

`X-OPS`'s `50–150 GB/day` and `1–4 TB/day` object-storage rows are **deleted** — they were 25–90× above what the system's own keep rate and compression target produce, and no operator could have reconciled them. The methodology line *"no single shard exceeds ~2 000 spans/sec"* is deleted and replaced by `per_shard_assembly_rate = 20 000`, which is `F02 §3.2`'s own number and which reproduces the table's own top row.

**Rule:** every cell must be reproducible with a calculator from the five formulas. **`AC-XOPS-7`** validates at least the 10 000 band's storage-growth column against a real run, not only the ingest rate.

**Docs to change:** `01 §3.2`, `01 §7` (driver keys and defaults), `01 §10.1` (the formula set and table), `05 §8` (absent-list entries and the supersession row), `X-OPS §4.1` (delete the Postgres box; gateway Deployment), `§4.2`, `§4.4` (cite `01 §10.1`), `§7` (AC-XOPS-7), `F02 §8` (**Decided (round 1)**: bus default), `F08 §3.2` (unchanged, now uncontradicted).

---

## DR-33 — Ops: readiness that cannot fail everywhere at once, a complete shutdown order, real backup

**Resolves:** SR-17, CC-31 (ops half), PD-29 (backup half), PD-30 (ops half).

### 33.1 Readiness

**`04 §3` is normative; `X-OPS FR-XOPS-4` is rewritten to match.** The rule, stated once and cited:

> **A probe that can fail on all replicas at once must not gate Service membership.**

`/readyz` returns 200 **iff** all of:

1. every stage required by `server.mode` reached `Ready`;
2. migrations applied;
3. receivers bound (in modes that receive);
4. a write probe on **both** `traceiq.db` and `control.db` succeeded within the last 30 s;
5. disk below `store.budget.high_watermark` — **only when** `action_on_full: stop_ingest`;
6. shutdown has not begun.

`/readyz` **never** probes object storage, Kafka lag, the hot-index cluster, the LLM, or the log/metric adapters. `X-OPS FR-XOPS-4`'s object-storage probe is deleted outright: an S3 partial outage would fail readiness on every gateway pod simultaneously, Kubernetes would remove them all from the Service, and ingest would stop cluster-wide — while `04 §5.2` correctly specifies that the cold store spills to WAL and ingest continues. Those dependencies move to `/readyz?verbose=true` and to `traceiq_component_degraded{component=...}` gauges.

`/healthz` = process up and config loaded. A hot-store write failure fails `/readyz` and keeps `/healthz` at 200, so Kubernetes does not restart a pod that would fail identically (`04 §5.2`, retained).

**ACs:** `AC-XOPS-11` — fault-inject object-storage unavailability: `/readyz` stays 200, ingest continues, `traceiq_component_degraded{component="cold_store"}=1`, WAL spill occurs. `AC-XOPS-12` — fault-inject a hot-store write failure: `/readyz` fails, `/healthz` stays 200.

### 33.2 Graceful shutdown order (reverse dependency order, budget `ops.shutdown_grace: 45s`)

| Step | Action |
|---|---|
| X1 | `/readyz` starts failing; 5 s drain window for load balancers |
| X2 | receivers stop accepting; in-flight requests finish within `api.write_timeout` |
| X3 | close `spanBatchCh`; decode workers drain and exit |
| X4 | `sampler.FlushAll` — force-complete every open trace with `KeepReason = KeepShed`, emit decisions and RED; then truncate the sampler WAL segments |
| X5 | `topology.Flush` — flush open edge buckets through `EdgeSink` |
| X6 | `anomaly.BaselineStore.Checkpoint` — final dirty-key checkpoint |
| X7 | `rca` — abort in-flight investigations with `Status = Aborted`, `reason = shutdown`; every completed step is already journalled (DR-18) |
| X8 | `remediate` — no new transitions; an in-flight `Executing` action is **left for restart reconciliation, never cancelled mid-apply** |
| X9 | cold store — seal open blocks, or spill to `store.cold.wal_dir`; run `HotIndex.BindColdBlock` for anything sealed |
| X10 | control writer drains, then telemetry writer drains; `wal_checkpoint(TRUNCATE)` on both files |
| X11 | audit `process.shutdown` appended; **final anchor written** (DR-27); chain closed |
| X12 | listeners closed; **exit 0** |

Grace exceeded ⇒ force path: spill open Parquet buffers to the cold WAL, one 5 s checkpoint attempt, log `shutdown_forced` naming the stalled stage, **exit 1**. `04 §X`'s orphan steps (`sampler.FlushAll`, `topology.Flush`, "abort orphaned investigations", "expire stale approvals") now all have methods to call (DR-10, DR-13, DR-15, DR-23).

**Startup reconciliation** is the mirror image: `rca.Journal.ListRunning` aborts investigations orphaned by a crash; `Guard.ExpireDue` expires stale approvals; `ColdStore.ReplayWAL` and `HotIndex.BindColdBlock` run before receivers bind; `sampler.ReplayWAL` runs before receivers bind (DR-9).

### 33.3 Backup

```yaml
ops:
  shutdown_grace: 45s
  backup:
    enabled: true
    dir: ${data_dir}/backup
    control_interval: 5m        # control.db — the irreplaceable state
    telemetry_interval: 6h      # traceiq.db — large, and re-derivable from the cold tier
    cold_wal_ship: true
    retain: 7d
    verify_on_write: true       # each VACUUM INTO output is reopened and integrity_check'd
```

Method: `VACUUM INTO` snapshots at the two intervals plus continuous cold-WAL shipping (DR-6 §6.5). **RPO is stated per file**: `control.db` ≤ 5 min; `traceiq.db` ≤ 6 h, with the gap covered by cold-WAL replay. The "< 10 % storage overhead via incremental WAL shipping" claim is **deleted**, and `X-OPS` must say why: `wal_autocheckpoint` continuously truncates the WAL, so naive WAL shipping is unsound. A 9 GiB `VACUUM INTO` every 5 minutes is not feasible and is not attempted.

`traceiq backup snapshot`, `traceiq restore --from=<id>` and `traceiq version --check-skew` are **`cmd/traceiq` subcommands**, not interfaces (DR-3 rejected `ops.BackupManager`/`ops.VersionChecker`).

### 33.4 Dev-mode defaults — `traceiq run` with no config file must work

On `server.profile: dev`, one binary on one laptop, **zero external dependencies**: `api.endpoint 127.0.0.1:8443` with a generated self-signed certificate; `auth.mode: token` with a bootstrap admin token printed once to stderr on first start; `tenancy.enabled: false`; `store.hot.driver: sqlite`; `store.cold.driver: parquet_local`; `cluster.bus.driver: none`; `rca.reasoner: auto` (⇒ `rules` with no API key); `correlate.logs.driver: file`, `correlate.metrics.driver: file`; `memory.embeddings.driver: lexical`; `remediate.enabled: false`; `eval.enabled: false`; `selfobs.metrics_endpoint 127.0.0.1:9464`; alert sinks disabled but **the deadman enabled and logging** rather than paging.

**`AC-XOPS-9` (new):** `traceiq run` with no config file starts, serves the UI, accepts OTLP on loopback, assembles traces, detects an anomaly and produces a `rules`-reasoner investigation — with no network egress and no external service.

**Docs to change:** `01 §7` (`ops.*`), `04 §3` (normative), `04 §X` (the twelve steps), `04 §5.2`, `X-OPS §3.1` FR-XOPS-4 (rewritten), FR-XOPS-5 (rewritten), `§3.2`, `§4.2`, `§5`, `§7` (AC-XOPS-9, -10, -11, -12), `§8` (**Decided (round 1)**), `F03 §3.2` (durability wording per DR-6 §6.5).

---

## DR-34 — The LLM client: one model, one wire contract, golden files, one fallback

**Resolves:** SR-22, CC-11 (model half), CC-20(b partial); **closes FU-4**.

### 34.1 One model, one request shape

**`05 D-9` is normative.** Default model **`claude-opus-5`**; `01 §7`'s `rca.llm.model: claude-sonnet-4-5` is corrected. A hand-rolled client (which `05 D-9` deliberately chose) built for one model's request shape and configured for another fails at runtime or silently drops parameters — which is exactly what the two documents currently set up.

Binding request shape: `thinking: {"type":"adaptive"}`, `output_config.effort`, **no assistant prefill**, tool schemas sent as `tools`, `stop_reason == "refusal"` checked and mapped to `StepVerdict = refused`, strict JSON output validated by `rca.SchemaValidator` (DR-15). **`temperature` is not sent** — it is incompatible with adaptive thinking — so `01 §7`'s `temperature: 0` is **deleted** and replaced by `rca.llm.effort`.

```yaml
rca:
  llm:
    provider: anthropic            # the only v1 value; the seam survives for a Phase 3 local model
    base_url: https://api.anthropic.com
    model: claude-opus-5           # was claude-sonnet-4-5
    effort: medium                 # low | medium | high   (replaces temperature)
    thinking: adaptive
    api_key_env: TRACEIQ_ANTHROPIC_API_KEY
    api_key_file: ""
    timeout: 25s                   # was 60s (DR-17 §17.2)
    max_retries: 2                 # was 3
    max_output_tokens: 1600        # was 4096; this is O in DR-17 §17.1
    prompt_cache: true
    pricing:                       # micro-USD per million tokens; MUST be non-zero when reasoner: llm
      input: 0
      cached_read: 0
      cache_write: 0
      output: 0
```

`internal/llm` (DR-2) holds the client: stdlib `net/http` through `auth.EgressDialer` (DR-20/DR-25), streaming JSON decode, retry on 429/5xx honouring `Retry-After`, and a `usage` parser that feeds the budget governor. `llm.Client` is the only type `rca` and `memory` share, which is what kills the `memory → rca` cycle (DR-2).

### 34.2 FU-4 closes here

Golden-file wire tests assert **the exact request body** and the exact parsing of `usage`, `stop_reason`, `refusal` and tool-use blocks for the configured model. A model change, a shape change, or a prompt edit without bumping `rca.PromptVersion` (DR-18) **fails CI**. `05 §9` marks FU-4 **CLOSED** when these land, which must be before the first F06 merge.

### 34.3 Cost re-derivation

`01 §10.3`'s "median ≤ $0.08" and the PRD's ROI row were computed on the Sonnet assumption and are **recomputed** from `rca.llm.pricing` and DR-17 §17.1's token model, published as *derived* numbers with the formula printed beside them:

```
cost_per_investigation_micro_usd =
    (uncached_in/1e6)·pricing.input + (cached_read/1e6)·pricing.cached_read
  + (cache_write/1e6)·pricing.cache_write + (out/1e6)·pricing.output
```

The **$0.50 per-investigation** hard cap stands (`rca.budget.max_cost_micro_usd`); the per-tenant and global **daily** caps are DR-17 §17.4. Startup reconciliation (DR-17 §17.1) guarantees the per-investigation cap is not silently unreachable or silently vacuous.

### 34.4 `llm → rules` mid-loop fallback — exact semantics

The reasoner may be swapped **only between steps, never inside one**.

| Trigger | Condition |
|---|---|
| transport | 3 consecutive LLM transport failures (after retries) |
| refusal | `stop_reason == "refusal"` twice in one investigation |
| daily cap | `max_cost_micro_usd_per_tenant_per_day` or the global daily cap exhausted (DR-17) |
| saturation | `traceiq_rca_queue_saturated` (queue depth > 32) at dispatch time |
| schema | schema validation fails twice on the same step |
| canary | two canary violations in one investigation (DR-37) |

On a swap, all of the following hold:

1. the investigation keeps its **ID, its steps and its evidence** — it is not restarted;
2. `Investigation.ReasonerKind` becomes `rules`;
3. `Investigation.ReasonerSwaps` appends `{AtStep, From, To, Reason, At}` (DR-15) and the report states the swap and the step it happened at;
4. the rules reasoner is **seeded with the hypotheses already on the board**, so prior work is not discarded;
5. **`Confidence` is capped at `rca.confidence_threshold − 0.01` for the remainder of the investigation** — a swapped investigation can never auto-conclude at high confidence, and therefore can never satisfy DR-21's P1 with a confident-but-half-LLM conclusion;
6. **there is no swap back** within an investigation.

`traceiq_rca_reasoner_swaps_total{reason=...}`. **AC-F06-24:** each of the six triggers produces exactly one swap, preserves prior steps, and caps confidence.

**A local/offline model is an explicit Phase 3 deferral** recorded in `00` (CC-20b); `rca.llm.provider` keeps the seam and has exactly one legal v1 value.

**Docs to change:** `01 §7` (`rca.llm.*`), `01 §10.3` (derived cost targets with the formula), `02 §3` (`llm.Client`; delete `memory_AnthropicEmbedder --> rca_AnthropicClient`), `05 §D-9` (normative; note the `01` correction), `05 §9` (FU-4), `PRD` ROI row (recomputed), `F06 §3.2`, `§4.3`, `§4.4`, `§5`, `§7`, `F08 §4.3` (embedder holds `llm.Client`).

---

## DR-35 — NL: one closed taxonomy, one data path, a mandatory offline interpreter

**Resolves:** CC-17, CC-20(b partial), and the F10 half of SR-6.

### 35.1 One taxonomy — the union, closed

```go
package nl

type IntentKind uint8
const (
    IntentUnknown            IntentKind = 0
    IntentTraceSearch        IntentKind = 1
    IntentServiceHealth      IntentKind = 2
    IntentTopologyQuestion   IntentKind = 3
    IntentIncidentStatus     IntentKind = 4
    IntentInvestigationAsk   IntentKind = 5
    IntentMemoryLookup       IntentKind = 6
    IntentStartInvestigation IntentKind = 7
    IntentCompareWindows     IntentKind = 8
    IntentExplainTrace       IntentKind = 9
    IntentRemediate          IntentKind = 10
)
// CLOSED. A new intent is a DR, not a config value.
```

This is the union of `02`/`03`'s seven and `F10`'s seven: `02` gains `Remediate`, `CompareWindows` and `ExplainTrace`; `F10` gains `MemoryLookup`, `ServiceHealth` and `InvestigationAsk`.

### 35.2 Interfaces

```go
type Interpreter interface {
    Kind() string    // "rules" | "llm"
    Interpret(ctx context.Context, tid model.TenantID, q Question, cc ConversationContext) (Intent, error)
}

type Answerer interface {
    Answer(ctx context.Context, tid model.TenantID, in Intent, cc ConversationContext) (Answer, error)
}

type Question struct {
    Text    string          // <= nl.max_question_bytes (4096)
    Subject auth.Subject
    Chat    *ChatContext    // per-request identity: platform, workspace, channel, thread, user
    AskedAt time.Time
}

// ChatContext (02) and ConversationContext (F10) BOTH exist and are different things:
// ChatContext is request identity; ConversationContext is multi-turn state.
type ConversationKey struct {
    Tenant                                  model.TenantID
    Platform, WorkspaceID, ChannelID, ThreadID string   // per THREAD, per F10 §8's own correction
}

type ConversationContext struct {
    TenantID  model.TenantID   // ALWAYS present (DR-5)
    Key       ConversationKey
    Turns     []Turn           // ring of <= nl.context_turns (5)
    Focus     Focus            // last incident / investigation / service / trace referenced
    UpdatedAt time.Time
}

type Intent struct {
    Kind       IntentKind
    Args       rca.ToolArgs   // the SAME typed args the RCA loop uses (DR-16)
    Raw        string         // the user's text, for the audit row only
    Confidence float64
}
```

**The `tenant` field is deleted from `POST /v1/nl/query`'s body** (DR-5); the tenant comes from the authenticated principal and from nothing else.

### 35.3 One data path

**Binding: every NL data access goes through `rca.ToolRegistry.Dispatch`.** `02 §4`'s design note — *"`nl.Answerer` reuses `rca.ToolRegistry` rather than defining a second query path, so an NL answer and an RCA step cite identical evidence objects"* — is currently violated by `F10 §4.4`, which calls `store`, `topology` and `anomaly` directly. `nl` keeps those imports (DR-2) for **type references only**; answer content comes from the registry.

Consequences, all free: NL inherits DR-16's typed arguments, its semantic validation and its server-side clamping; and an NL `ServiceHealth` answer and an RCA `metric_query` step over the same window produce `model.Evidence` with **identical `Query` and `ToolResultHash`** — that is D-X3, and it is the AC (`AC-F10-10`).

`anomaly.Grouper.ActiveIncidents` (DR-14 §14.1) gives `IntentIncidentStatus` a declared method to call; `F10`'s undeclared `anomaly.Grouper.ActiveIncidents(tenantID)` is now real.

`IntentStartInvestigation` requires `incident:investigate`. **`IntentRemediate` never executes and never approves**: it renders a proposal form, requires `remediation_action:propose`, and routes through `remediate.Guard.Propose` with the chat user's resolved `auth.Subject` (DR-23, DR-25).

### 35.4 The offline rules interpreter is mandatory

`nl.RulesInterpreter` must resolve **all ten intents** from a deterministic pattern table with **no network call**, and `nl.interpreter: rules` is a fully supported configuration, not a degraded one. Startup rule: `nl.enabled: true` with `interpreter: llm` and no resolvable key silently selects **`rules`**, logged at INFO — never an error, never a dead feature.

The pattern table, published in `F10 §4.4`: ≥ 4 surface forms per intent; entity extraction restricted to service names present in the topology snapshot and operations present in `span`; time phrases from a **closed grammar** (`last <n> <unit>`, `since <time>`, `between <t1> and <t2>`, `yesterday`, `today`, ISO-8601); anything unmatched becomes `IntentUnknown` with a clarifying question rather than a guess.

**Published gate — `FR-F10-9` / `AC-F10-9`: ≥ 85 % intent accuracy on the F11 NL scenario set with `interpreter: rules`.** The eval harness drives NL through `rules` only (DR-36), so NL accuracy is reproducible and cost-free in CI.

### 35.5 Injection

An NL question is wrapped by `rca.Sanitizer` exactly as telemetry is (`model.UntrustedUserQuestion`, DR-37), and the LLM interpreter's output is validated by `rca.SchemaValidator` into the closed `Intent` above — so a free-form query string never exists on this path either.

```yaml
nl:
  enabled: true
  interpreter: auto              # auto | llm | rules  (auto -> rules without a key)
  context_turns: 5               # NEW
  max_question_bytes: 4096
  answer_evidence_limit: 10
  slack: { enabled: false, signing_secret_env: TRACEIQ_SLACK_SIGNING_SECRET, bot_token_env: TRACEIQ_SLACK_BOT_TOKEN }
  teams: { enabled: false, secret_env: TRACEIQ_TEAMS_SECRET }
```

**Docs to change:** `01 §7` (`nl.context_turns`), `02 §4` (`IntentKind` union; `ChatContext` + `ConversationContext` both listed), `03 §4`, `F10 §3.1` (new FR-F10-9 rules accuracy, FR-F10-10 single data path), `§4.2`, `§4.3` (delete the body `tenant`; roles per DR-25), `§4.4` (dispatch through the registry; the pattern table), `§7`, `§8` (**Decided (round 1)**: per-thread key), `F05 §4.3` (`ActiveIncidents`).

---

## DR-36 — Eval harness: the real pipeline, a virtual clock, isolation, and CI gates that actually fail

**Resolves:** PD-16, CC-18, SR-16, CC-30 (evidence-recall half), and the D-D5 caveat.

### 36.1 The runner

```go
package eval

type Mode uint8
const ( ModeOffline Mode = 1; ModeLive Mode = 2 )

type Isolation uint8
const ( IsolationProcess Isolation = 1; IsolationShared Isolation = 2 )

type Runner interface {
    Run(ctx context.Context, opts RunOptions) (Report, error)
    List(ctx context.Context, dir string) ([]ScenarioSpec, error)
    Stats() Stats
}

type RunOptions struct {
    Tenant             model.TenantID
    ScenariosDir       string
    Select             []string          // scenario ids; empty = all
    Mode               Mode              // ModeOffline is the default and the only mode CI runs
    Isolation          Isolation         // IsolationProcess default; Shared refuses Parallelism > 1
    Parallelism        int               // default 4 with IsolationProcess
    Seed               int64             // seeds every *rand.Rand and every Investigation.ReplaySeed
    Clock              model.Clock       // eval.VirtualClock in ModeOffline (DR-31)
    Reasoner           model.ReasonerKind // "rules" in CI; "llm" for published accuracy runs
    RegressionBaseline string            // path to a stored Report
    FailOnRegression   bool
    OutputDir          string
    Timeout            time.Duration
}
```

### 36.2 No bypass, ever

**Fixtures are replayed through `ingest.Receiver`** — the real OTLP path, the real normalizer and limiter, the real sampler, the real detectors, the real grouper. `F11 §4.1`'s "inject fixtures directly into store" and its `FIX → RCA` edge are **deleted**. `02 §4`'s claim — *"`eval.HarnessRunner` drives the real pipeline through the real receivers; there is no bypass path, so the published accuracy numbers measure shipped behavior"* — becomes true rather than aspirational. A test hook asserts every span entered through a receiver (**`AC-F11-7`**); without it the published numbers would measure neither the sampler nor the detectors, which is the exact failure mode F11 exists to prevent.

`awaitIncidentCandidate` now has the detector path it needs, because that path is no longer skipped.

### 36.3 Determinism and the CI budget, re-derived

`ModeOffline` installs `eval.VirtualClock` (DR-31), which advances only at quiescence. A 30-minute scenario therefore completes in seconds and is bit-reproducible.

| Input | Value |
|---|---|
| Scenarios | 23 |
| Reasoner in CI | `rules` (no network, no tokens) |
| Isolation | process-per-scenario |
| Parallelism | 4 |
| Per-scenario virtual-clock wall time | ≤ 35 s (dominated by fixture decode, not by simulated time) |
| **Derived suite wall time** | **6 waves × 35 s ≈ 3.5 min, published as ≤ 4 min** |

The old ≥ 12–36 min arithmetic came entirely from wall-clock-driven timers; with a virtual clock the NFR is not merely met, it is met with margin. `01 §7`'s `eval.parallel: 1` becomes **`eval.parallelism: 4`** with `eval.isolation: process`, and `F11`'s default agrees — the 1-versus-4 contradiction is gone.

**Isolation is what makes parallelism safe.** Each scenario gets its own `data_dir`, its own `traceiq.db` and `control.db`, its own topology graph, baseline store, path-signature set, grouper and memory store. `IsolationShared` exists only for a single-scenario debug run and **refuses `Parallelism > 1`** — four scenarios sharing one grouper would merge events across scenarios within 2 topology hops, which is exactly PD-16(d).

**Cold baselines are solved in the fixture, not by lowering thresholds.** Every `ScenarioSpec` carries either a `baseline_warmup` fixture segment or a serialized `BaselineSnapshot`, loaded before the fault window, so `warmup_samples` is satisfied and DR-14 §14.3 does not suppress every detector (PD-16e).

### 36.4 The scenario spec

```go
type ScenarioSpec struct {
    ID, Title, Description string
    Tenant         model.TenantID
    Fixture        FixtureRef      // OTLP/NDJSON span bundle + optional log and metric bundles for the file adapters
    BaselineWarmup *FixtureRef     // or an inline BaselineSnapshot
    Clock          ClockSpec       // {start, fault_at, end} — all virtual
    Faults         []FaultSpec     // ModeLive only
    Expect         Expectation
}

type Expectation struct {
    IncidentWithin     time.Duration
    EpicenterService   string
    RootCauseCategory  model.HypothesisCategory
    RootCauseComponent string
    ExpectedEvidence   []EvidenceCategory   // CLOSED enum — this is what makes EvidenceRecall computable
    MaxCostMicroUSD    int64
    MustNotPage        bool
}

type EvidenceCategory uint8
const (
    EvTraceExemplar EvidenceCategory = 1; EvErrorSignature = 2; EvLogLine = 3; EvMetricSeries = 4
    EvTopologyEdge = 5; EvDeployMarker = 6; EvREDSeries = 7; EvMemoryRecord = 8
)
```

`ExpectedEvidence` over a closed enum resolves CC-30's fourth untestable predicate: `EvidenceRecall` is now computable consistently, rather than "heuristically inferred" as `F11 §8` admits today.

### 36.5 Scoring against the real types (CC-18)

The scorer compiles against `rca` and `model` — **no shadow structs**. `F11 §4.4`'s `hypothesis.ServiceOrComponent` / `.Narrative` and `investigation.Evidence` / `.CompletedAt` are deleted:

- **Top-1 / Top-3:** `Hypothesis.Category == Expect.RootCauseCategory` **and** `Hypothesis.Component == Expect.RootCauseComponent`, over the top 1 / top 3 by `PostScore` (DR-15 gave `Hypothesis` both fields).
- **PartialCredit:** 0.5 for a category match with a wrong component.
- **EvidencePrecision / EvidenceRecall:** over `Investigation.Steps[].EvidenceIDs → Evidence.Category` against `Expect.ExpectedEvidence`.
- Plus `DetectLatencyMillis`, `TimeToRCAMillis`, `CostMicroUSD`, `ReasonerKind`.

`01 §5.1`'s `eval_result` gains `top3`, `evidence_precision`, `evidence_recall`, `partial_credit`, `time_to_rca_millis`, `cost_micro_usd`, `reasoner_kind`; `02`'s `eval.Result`/`eval.Report` gain the same. **No metric exists in the report schema without a defined computation.**

`F12 §4.4`'s `rca.Engine.PreliminaryReport(incident)` / `.FinalReport(incident)` — methods on an interface that has neither — are deleted; the alert router polls `Investigation.Status` per `02 §4` (DR-21).

### 36.6 CI gates — absolute floors *and* relative regression; either failing exits non-zero

| Gate | Threshold | Source |
|---|---|---|
| Top-1 accuracy, `reasoner: llm` | **≥ 70 %** absolute floor | `01 §10.3` |
| Top-1 accuracy, `reasoner: rules` | **≥ 40 %** absolute floor | `01 §10.3` |
| Top-1 regression vs the stored baseline | a drop > **5 pp** fails | `FR-F11-9` |
| Mean time-to-RCA | **≤ 180 s** absolute ceiling, **and** > **+25 %** vs baseline fails | new + `FR-F11-9` |
| Cost per investigation | median ≤ **$0.08**, max ≤ **$0.50** | `01 §10.3`, derived per DR-34 |
| NL intent accuracy, `interpreter: rules` | ≥ **85 %** | DR-35 |
| Alert precision | ≥ **80 %** actionable, ≤ **1** page per genuine incident | `01 §10.3` (DR-21) |
| Determinism | two `ModeOffline` runs with the same `Seed` produce **bit-identical** `Top1`, `Top3`, `EvidencePrecision`, `EvidenceRecall` | new |

`FR-F11-9`'s relative gate was the strongest traceability in the set and is kept; the **absolute** floors are new and are what makes the *published* number enforceable — previously nothing enforced it. New **FR-F11-11** (absolute floors), **FR-F11-12** (determinism), **FR-F11-13** (receiver-entry assertion), each with an AC.

### 36.7 Live mode goes through the Guard (SR-16)

`F09 §1`'s "sole write path into customer infrastructure" is preserved rather than re-scoped:

- Every fault application and teardown goes through **`remediate.Guard`** with `ProposedBy = "eval:<runID>"`, using the existing five `model.ActionType` values and the same `k8s.Executor`/`BuildArgv` (DR-24). **`model.ActionType` is not extended.**
- Scope comes from a new `tenant.Policy.EvalNamespaceAllowlist`, **disjoint from** the production `NamespaceAllowlist`; a namespace in both is a startup error.
- Faults not expressible as one of the five types — `istioctl` fault injection, CPU throttling — are **deleted from v1** and recorded as a Phase 3 deferral. `F11 FR-F11-3`'s direct `istioctl`/`kubectl` path is deleted (DR-24).
- **`ModeLive` requires all of:** `eval.live.enabled: true`; `eval.live.confirm: "i-know-this-mutates-a-cluster"`; an `eval.live.kubeconfig` **distinct from** `remediate.kubeconfig`; a context name matching `eval.live.context_allowlist`; and `server.profile != prod`. Any missing ⇒ **`400 eval_live_refused`**, audited.
- Every application and teardown writes to the same hash-chained audit log (DR-27). Teardown is verified even on scenario panic (`defer` plus a reconciliation pass at run end). **`AC-F11-8`**.

```yaml
eval:
  enabled: false
  scenarios_dir: ./eval/scenarios
  output_dir: ${data_dir}/eval
  mode: offline                  # offline | live
  parallelism: 4                 # was parallel: 1
  isolation: process             # process | shared
  seed: 1
  reasoner: rules
  regression_baseline: ""
  fail_on_regression: true
  live:
    enabled: false
    confirm: ""
    kubeconfig: ""
    context_allowlist: []
```

**Docs to change:** `01 §5.1` (`eval_result` columns), `01 §7` (block above), `01 §10.3` (gates gain owners), `02 §4` (`Runner`, `RunOptions`, `Report`, `Result`; the no-bypass note now true), `F11 §3.1` (FR-F11-2 rewritten, FR-F11-3 deleted, new FR-F11-11/-12/-13), `§3.2` (runtime NFR re-derived), `§4.1` (delete the bypass edge), `§4.3`, `§4.4`, `§6`, `§7`, `§8` (**Decided (round 1)**), `F12 §4.4` (delete the two nonexistent methods).

---

## DR-37 — Prompt-injection defenses as testable requirements, and a complete STRIDE table

**Resolves:** SR-13(a), SR-13(b), SR-13(d), SR-30, SR-12 (injection half), and the SR standing question on defense sufficiency.

### 37.1 One description, in `01 §8.6`

`01 §8.6` (delimiters), `X-SEC §4.4` (message roles) and `F06 §4.4` (typed JSON) currently describe three different mechanisms, so an implementer gets to choose. `01 §8.6` is normative and the other two **cite** it. The mechanism, exactly:

1. **No raw span dumps.** Only `model.ToolResult` projections reach a prompt, and `project` is a closed field allowlist (DR-16).
2. **Delimited untrusted regions.** Every string not authored by TraceIQ's own code is wrapped as
   `<untrusted k="{class}" c="{canary}"> … escaped … </untrusted k="{class}" c="{canary}">`
   where `{canary}` is 16 random hex bytes per investigation and the escaping neutralises `<`, `>` and any literal occurrence of the canary inside the payload.
   ```go
   type UntrustedKind uint8
   const ( UntrustedTelemetry UntrustedKind = 1; UntrustedLog = 2; UntrustedMemory = 3
           UntrustedUserQuestion = 4; UntrustedRunbook = 5; UntrustedDeployMetadata = 6 )
   ```
   **`01 §8.6(2)`'s scope is widened** from "any string originating from telemetry" to **"every string not authored by TraceIQ's own code"** — which is what brings stored memory narratives (SR-13), imported runbooks, NL questions and deploy metadata inside the wrapper.
3. **Canary echo check.** A canary appearing in model output outside a legal position ⇒ `Verdict = schema_error`, the output is discarded, `traceiq_rca_canary_violation_total` increments; **two violations in one investigation swap the reasoner to `rules`** (DR-34).
4. **Strict output schema.** `rca.SchemaValidator` rejects unknown fields, extra keys and any value outside a closed enum. There is no best-effort parse.
5. **Closed tool set, typed arguments, semantic validation** (DR-16).
6. **No model-authored payload reaches the cluster** (DR-22).
7. **Memory is re-wrapped on retrieval and can never be evidence** (DR-19 §19.5).

### 37.2 The requirements, stated so they can fail

The existing corpora (`AC-XSEC-7`, `AC-F06-3`) test only *"did an unauthorized tool call or execution occur"*. The actual failure mode is *"did injected content change the **content** of a tool call or a proposed action"*. These five requirements test that.

| ID | Requirement | AC |
|---|---|---|
| **FR-XSEC-11** | For a corpus of ≥ 40 injected payloads, **zero** tool calls differ in their arguments from the same investigation run over a sanitized copy of the same telemetry | AC-XSEC-4a |
| **FR-XSEC-12** | **Zero** `ActionProposal`s differ in `Type`, `Target` or the typed spec between the injected and sanitized runs | AC-XSEC-4b |
| **FR-XSEC-13** | Zero canary leaks; a deliberately leaking stub reasoner is detected and swaps to `rules` | AC-XSEC-4c |
| **FR-XSEC-14** | An injection persisted into memory in investigation A is provenance-tagged, wrapped on retrieval in investigation B, and cannot by itself move `Confidence` to `rca.confidence_threshold` | AC-XSEC-4d |
| **FR-XSEC-15** | Every model-authored narrative rendered in an approval surface carries an explicit *untrusted, model-authored* marker **and** is displayed beside the Guard-reconstructed payload | AC-F12-11 |

FR-XSEC-11/12 are the load-bearing ones: they make the *content-equivalence* property mechanically checkable, which is what the reviewers asked for and what neither corpus previously asserted.

### 37.3 STRIDE table completion (SR-30)

`X-SEC §4.4` gains **one row per feature package**, each citing the DR that mitigates it:

| Package / surface | Threat | Mitigation |
|---|---|---|
| `store` / F03 | Information disclosure (cross-tenant read), Tampering (retention bypass), DoS (disk exhaustion) | DR-5, DR-6, DR-7, DR-12 |
| `sampler` / F02 | DoS (rare-path retention amplification, predicate abuse) | DR-10, DR-11 |
| `memory` / F08 | Tampering (memory poisoning) | DR-19 §19.5 |
| `correlate` / F07 | Information disclosure (cross-tenant logs), SSRF | DR-20 |
| `eval` / F11 | Elevation (unguarded cluster mutation) | DR-36 §36.7 |
| Deploy webhook | Spoofing (forged deploy markers steering RCA and the deploy rule) | DR-25 (HMAC + principal), DR-29 (idempotency) |
| `topology` / F04 | DoS (edge-table cardinality exhaustion) | DR-13 |
| Chat identity | Spoofing / Elevation (signature treated as authorization) | DR-25 §25.4 |
| `k8s` / F09 executor | Elevation (argv/flag injection) | DR-24 |
| `rca` / F06 | Tampering (prompt injection), DoS (cost) | DR-16, DR-17, this DR |

**`X-SEC §6`'s process control becomes a mechanical gate:** a feature doc with no STRIDE row **fails the docs CI build**. The control has already been violated by the features that landed alongside it, which is why a review-process promise is replaced by a build step (DR-38).

**Docs to change:** `01 §8.6` (normative, widened scope, `UntrustedKind`), `02 §3` (`Sanitizer` + `SchemaValidator` as distinct components), `F06 §4.4`, `§6`, `§7`, `F09 §6`, `F10 §4.4`, `F12 §4.3` (the untrusted marker), `X-SEC §4.4` (ten rows), `§5`, `§6` (mechanical gate), `§8` (delete the incomplete-table open question).

---

## DR-38 — PRD promises, deferrals, and the mechanical gates that stop the drift recurring

**Resolves:** CC-20, CC-29, CC-30, CC-33(1), CC-33(5).

### 38.1 Every PRD promise gets an owner or an explicit, dated deferral

A new table in `00`, "PRD promise → owning FR or explicit deferral". A claim with neither is deleted from `01 §9`, because a drawback-coverage matrix that carries an unimplemented clause is worse than one that admits a gap.

| PRD promise | Disposition |
|---|---|
| "TraceIQ can consume Tempo/Grafana MCP servers as backends" / "can run on top of Tempo" | **Phase 3 deferral.** The clause is **struck from `01 §9`'s D-T3 row** until an FR exists. D-T3's primary claim — TraceIQ ships the reasoning loop — is unaffected and remains Concrete. |
| "Local model option for air-gapped" | **Phase 3 deferral** (DR-34). `rca.llm.provider` keeps the seam; `01 §7` notes it. |
| "Works out-of-the-box on Istio / service-mesh telemetry (Envoy access logs enrich topology)" | **Owned.** New **FR-F04-10**: an Envoy access-log → `topology.Edge` adapter consuming OTLP logs carrying `envoy.*` semantic conventions, behind `topology.envoy_access_logs.enabled: false`. **AC-F04-10** over a recorded Istio bundle. This matters because the eval suite is Istio-based. |
| "Correlation contract: `trace_id` injected into logs" | **Owned as a deployment prerequisite.** New **FR-F07-8**: a startup capability check samples the log adapter; if < 50 % of sampled lines carry `trace_id`, `traceiq_component_degraded{component="log_correlation"} = 1` and every affected report carries the named missing-evidence class *"logs are not trace-correlated"*. The heuristic fallback survives but is **always labelled**. |
| "Service metadata (owners, SLOs, escalation) in memory" | **Owned.** `model.ServiceMeta` (DR-4), sourced from resource attributes or `tenant.Policy`; returned by `GET /v1/services`; `memory.Record.Kind = ServiceMeta` added to `F08`. |
| "Execution via existing operators (ArgoCD rollback, …)" | **Phase 3 deferral**, recorded in `00`. The five closed action types (DR-22) are v1. |

### 38.2 CC-33 residue

- **CC-33(1) — Kafka vs in-process default.** The in-process ring is the default in **every** mode, including Kubernetes. Kafka/Redpanda is enabled only when one of `05 D-5`'s three triggers holds. Kafka is **not** mandatory because the deployment is Kubernetes. (Also DR-32 §32.3.)
- **CC-33(5) — exemplar selection.** Per `(service, operation, bucket)`, **first-wins with a per-window reservoir of 4**, matching `red_rollup.exemplar_trace_ids ≤ 4` (`01 §5.1`). Ties break on the **lowest `TraceID`**, so exemplar selection is deterministic under replay (DR-18) and under the eval harness's fixed seed (DR-36).

Each is written into the owning feature doc's §8 as **Decided (round 1)** with its rationale.

### 38.3 The four untestable predicates, resolved

| Term | Resolution |
|---|---|
| "tier-0-labeled service" (`F05 FR-F05-11`) | **Deleted** with the requirement itself (DR-21). `model.ServiceMeta.Tier` exists for display and grouping and is never a paging input. |
| "a configurable dollar ceiling … set in config" (`F06 §3.2`) | `rca.budget.max_cost_micro_usd`, plus the two daily caps (DR-17). |
| "fingerprint family" (`F08 FR-F08-4`) | fingerprints whose token Jaccard ≥ `memory.consolidation.dedupe_threshold` (0.6) (DR-19 §19.1). |
| "expected evidence categories" (`F11 FR-F11-6`) | `ScenarioSpec.Expect.ExpectedEvidence` over the closed `eval.EvidenceCategory` enum (DR-36 §36.4). |

### 38.4 Mechanical gates, so CC-11 and CC-29 cannot recur

1. **`docs/architecture/defaults.md` is generated** from `01 §7` and diffed in CI against every `Config keys` block in every feature doc. A mismatch **fails the build**. This is what stops the twenty-plus numeric contradictions from reappearing the moment two authors edit in parallel.
2. **`docs/architecture/traceability.md` is generated** from the feature docs (FR ↔ AC ↔ test file). **CI fails on any FR with zero ACs.** The 33 uncovered FRs CC-29 lists must be covered before their feature's acceptance; AC ID sequences are renumbered contiguously in `F09`, `F11`, `F12` and `X-SEC`, where the gaps currently read as sampled rather than complete coverage.
3. A docs-CI rule fails on **any `D-*` row in `01 §9` without an FR ID**, and on **any feature doc without an `X-SEC §4.4` STRIDE row** (DR-37).
4. A docs-CI rule fails on any `D-*` row whose mechanism produces a user-visible artefact but names no `F12` screen (DR-30).
5. `internal/archtest` enforces, in one place: the DR-2 adjacency list, the DR-5 tenant-parameter rule, the DR-31 time/rand ban, and the DR-20 egress-client ban.

**Docs to change:** `00` (the promise table, the three Phase 3 deferrals, the `ops`-package deletion note), `01 §7` (`topology.envoy_access_logs`), `01 §9` (strike the D-T3 secondary clause; every row names an FR), `02 §5`, `05 §9` (FU-10/-11 for the two generated artefacts), `F04 §3.1` (FR-F04-10), `F05 §8`, `F06 §8`, `F07 §3.1` (FR-F07-8), `F08 §4.2` (`Kind`), `F11 §3.1`, every `§7` (AC renumbering), `X-SEC §6`.

---

## DR-39 — One RED record, one quantile set

**Resolves:** PD-18, PD-34, CC-11 (quantile half), PD-21 (RED-volume half).

### 39.1 `model.REDSample` is the only RED type

```go
package model

type Resolution uint8
const ( Res10s Resolution = 1; Res5m Resolution = 2; Res1h Resolution = 3 )

// sampler.REDSample, anomaly.REDSample, store.SpanRollup, store.REDResult,
// store.REDBucket and topology.EdgeRED are ALL DELETED.
type REDSample struct {
    Tenant           TenantID
    Service          string
    Operation        string
    BucketStart      time.Time
    Resolution       Resolution
    Calls            uint64
    Errors           uint64
    DurationSumNanos uint64
    Hist             LatencyHist   // 16 fixed log-spaced buckets, 1 ms .. 32 s, 64 B, mergeable by addition
    Q                Quantiles     // interpolated from Hist at READ time
    ExemplarTraceIDs []TraceID     // <= 4, first-wins reservoir, ties by lowest TraceID (DR-38 §38.2)
    TraceID          TraceID       // dedupe key component (DR-8)
    ShardEpoch       uint64        // dedupe key component (DR-8)
    KeptCount        uint32
}

type Quantiles struct { P50Nanos, P95Nanos, P99Nanos, MaxNanos uint64 }
```

### 39.2 The stored quantile set is `{p50, p95, p99, max}` — everywhere

`p90` is deleted from `F03`'s `SpanRollup`/`REDResult`/`REDBucket`, from `F04`'s `EdgeRED`, and from `F05`'s computation. This is a **schema decision, not a naming nit**: a p90 cannot be derived from a stored p95, so a column-list disagreement across four documents is an unbuildable data model.

`sampler.policy.slow_quantile` becomes a **closed enum `{0.95, 0.99}`**, default `0.99`. Both are stored columns, so changing the config is served without a digest read; any other value is a **startup error** naming the key. This is what makes `slow_quantile` genuinely configurable (PD-34's verification asks for exactly this test).

The serialized t-digest survives in exactly two places and nowhere else: `red_rollup_1h.digest` and `anomaly_baseline` at `bucket = 255`, each ≤ 512 B (DR-6, DR-14), for the ad-hoc-quantile case.

### 39.3 Volume: detectors consume per-bucket records, never per-span (PD-18)

`F05 §4.2`'s per-span `anomaly.REDSample` (`DurationMS`, `IsError`, `StackFingerprint`, `TraceID`) is deleted. Detectors consume **`Res10s` bucket aggregates**.

| Representation | Rate at the `01 §10.1` headline (120 000 spans/s) |
|---|---|
| Per-span (deleted) | 120 000/s — and `redCh`'s drop-oldest would silently corrupt baselines |
| **Per-bucket at 200 service-op pairs (adopted)** | **~20/s** |

`redCh` (cap 8 192, drop-oldest with a counter, `01 §3.1`) is therefore safe by three orders of magnitude, and the drop-oldest policy no longer risks baseline corruption.

The one detector that wanted per-span input — the stack-fingerprint variant of `new_error_signature` — instead reads `store.ErrorSignature` rows written by the single telemetry writer, on its own bounded path with its own counter. **`FR-F05-12`'s "50 000 samples/sec on one core" is deleted** (it was 2.4× short of the headline it was meant to serve) and replaced by: *"≥ 5 000 `model.REDSample` per second per core at `Res10s`"*, which is 250× the reference load and comfortably above the headline.

**Docs to change:** `01 §3.1` (`redCh` row cites the per-bucket rate), `01 §4.1` (the struct above), `01 §4.7` (`topology.Edge` uses `LatencyHist` + `Quantiles`), `01 §5.1` (`red_rollup*` column list; drop any p90), `01 §7` (`slow_quantile` closed enum), `01 §10.1`, `02 §1`/`§2`, `F02 §4.2` (delete `sampler.REDSample`), `F03 §4.2` (delete `SpanRollup`, `REDResult`, `REDBucket`), `F04 §4.2` (delete `EdgeRED`; drop p90), `F05 §3.1` FR-F05-12 (replaced), `§4.2` (delete `anomaly.REDSample`), `§4.4`.

---

## Appendix A — Finding → DR map

Every SR, PD and CC finding from the three round-1 reviews, with the decision that resolves it. A finding spanning two decisions lists both; the **first** DR listed is the primary owner.

### A.1 Security & Reliability (SR-1 … SR-30)

| Finding | Sev | DR |
|---|---|---|
| SR-1 | Blocker | **DR-22** |
| SR-2 | Blocker | **DR-25** (§25.4) + DR-23 (§23.7) |
| SR-3 | Blocker | **DR-26** |
| SR-4 | Blocker | **DR-23** (§23.2–§23.5) |
| SR-5 | Blocker | **DR-23** (§23.6) |
| SR-6 | Blocker | **DR-5** |
| SR-7 | Blocker | **DR-7** |
| SR-8 | Major | **DR-28** |
| SR-9 | Major | **DR-9** |
| SR-10 | Major | **DR-10** + DR-11 + DR-12 |
| SR-11 | Major | **DR-27** |
| SR-12 | Major | **DR-16** + DR-15 + DR-17 |
| SR-13 | Major | **DR-19** (§19.5) + DR-37 + DR-5 |
| SR-14 | Major | **DR-21** |
| SR-15 | Major | **DR-24** |
| SR-16 | Major | **DR-36** (§36.7) + DR-24 |
| SR-17 | Major | **DR-33** (§33.1) |
| SR-18 | Major | **DR-26** (§26.1) |
| SR-19 | Major | **DR-29** + DR-25 |
| SR-20 | Major | **DR-26** (§26.4) |
| SR-21 | Major | **DR-20** + DR-5 |
| SR-22 | Major | **DR-34** + DR-17 (§17.4) |
| SR-23 | Major | **DR-1** |
| SR-24 | Minor | **DR-4** + DR-0 |
| SR-25 | Minor | **DR-23** (§23.8) |
| SR-26 | Minor | **DR-22** (§22.4) |
| SR-27 | Minor | **DR-23** (§23.9) |
| SR-28 | Minor | **DR-25** (§25.3) |
| SR-29 | Minor | **DR-26** (§26.5) |
| SR-30 | Minor | **DR-37** (§37.3) |

### A.2 Performance, Scalability & Data (PD-1 … PD-35)

| Finding | Sev | DR |
|---|---|---|
| PD-1 | Blocker | **DR-6** (§6.4) |
| PD-2 | Blocker | **DR-6** (§6.2, §6.4) |
| PD-3 | Blocker | **DR-6** (§6.1, §6.3) |
| PD-4 | Blocker | **DR-7** |
| PD-5 | Blocker | **DR-7** |
| PD-6 | Blocker | **DR-8** |
| PD-7 | Blocker | **DR-9** |
| PD-8 | Blocker | **DR-10** + DR-9 |
| PD-9 | Blocker | **DR-10** |
| PD-10 | Blocker | **DR-12** + DR-2 |
| PD-11 | Blocker | **DR-14** (§14.2) |
| PD-12 | Blocker | **DR-13** |
| PD-13 | Blocker | **DR-14** (§14.7) + DR-13 (interface half) |
| PD-14 | Blocker | **DR-17** (§17.1) |
| PD-15 | Blocker | **DR-2** |
| PD-16 | Blocker | **DR-36** + DR-31 |
| PD-17 | Major | **DR-6** (§6.2) |
| PD-18 | Major | **DR-39** + DR-0 |
| PD-19 | Major | **DR-4** + DR-28 (§28.2) |
| PD-20 | Major | **DR-28** (§28.1) |
| PD-21 | Major | **DR-32** (§32.4) + DR-0 |
| PD-22 | Major | **DR-9** |
| PD-23 | Major | **DR-7** |
| PD-24 | Major | **DR-6** (§6.1) |
| PD-25 | Major | **DR-9** |
| PD-26 | Major | **DR-11** |
| PD-27 | Major | **DR-19** (§19.2) |
| PD-28 | Major | **DR-17** (§17.3) + DR-14 (§14.7) |
| PD-29 | Major | **DR-6** (§6.5) + DR-33 (§33.3) |
| PD-30 | Major | **DR-32** |
| PD-31 | Minor | **DR-3** + DR-2 |
| PD-32 | Minor | **DR-14** (§14.4, §14.5, §14.8) |
| PD-33 | Minor | **DR-13** + DR-14 (§14.7) |
| PD-34 | Minor | **DR-39** (§39.2) + DR-4 |
| PD-35 | Minor | **DR-19** (§19.4) + DR-5 |

### A.3 Completeness, Traceability & Coherence (CC-1 … CC-33)

| Finding | Sev | DR |
|---|---|---|
| CC-1 | Blocker | **DR-2** |
| CC-2 | Blocker | **DR-11** |
| CC-3 | Blocker | **DR-21** |
| CC-4 | Blocker | **DR-18** + DR-15 |
| CC-5 | Blocker | **DR-14** (§14.5) |
| CC-6 | Blocker | **DR-24** + DR-22 |
| CC-7 | Blocker | **DR-32** (§32.1) |
| CC-8 | Blocker | **DR-3** + DR-2 |
| CC-9 | Major | **DR-6** (§6.1–§6.3) |
| CC-10 | Major | **DR-14** (§14.4, §14.6) |
| CC-11 | Major | **DR-0** + DR-10 + DR-34 + DR-39 + DR-38 (§38.4) |
| CC-12 | Major | **DR-19** (§19.4) |
| CC-13 | Major | **DR-29** + DR-0 |
| CC-14 | Major | **DR-23** + DR-22 (§22.3) |
| CC-15 | Major | **DR-13** |
| CC-16 | Major | **DR-16** |
| CC-17 | Major | **DR-35** |
| CC-18 | Major | **DR-36** (§36.5) + DR-15 |
| CC-19 | Major | **DR-25** (§25.2, §25.3) |
| CC-20 | Major | **DR-38** (§38.1) |
| CC-21 | Major | **DR-30** |
| CC-22 | Major | **DR-9** |
| CC-23 | Major | **DR-12** + DR-17 |
| CC-24 | Minor | **DR-30** + DR-0 |
| CC-25 | Minor | **DR-6** (§6.4) |
| CC-26 | Minor | **DR-10** |
| CC-27 | Minor | **DR-4** |
| CC-28 | Minor | **DR-14** (§14.6) |
| CC-29 | Minor | **DR-38** (§38.4) |
| CC-30 | Minor | **DR-38** (§38.3), with DR-17, DR-19, DR-21, DR-36 |
| CC-31 | Minor | **DR-26** (§26.1) + DR-32 + DR-33 |
| CC-32 | Minor | **DR-8** |
| CC-33 (1) bus default | Minor | **DR-38** (§38.2) + DR-32 |
| CC-33 (2) tenant in fingerprint | Minor | **DR-5** + DR-19 (§19.1) |
| CC-33 (3) cold reconciliation | Minor | **DR-7** |
| CC-33 (4) edge-sharding key | Minor | **DR-13** |
| CC-33 (5) exemplar selection | Minor | **DR-38** (§38.2) |
| CC-33 (6) `PolicyStore` backend | Minor | **DR-3** |

**Coverage: 98 of 98 findings mapped (SR 30, PD 35, CC 33 — CC-33 counted once, its six sub-decisions listed individually). No finding is unmapped and none is deferred without a written decision.**

---

## Appendix B — Decisions that overrule a reviewer

Recorded because a fix agent will otherwise "correct" them back.

| Finding | Reviewer's recommendation | Board decision | Why |
|---|---|---|---|
| SR-15, CC-6 | adopt `k8s.io/client-go` | **Rejected.** Pinned `kubectl` in the image; `internal/k8s` argv builder (DR-24) | client-go adds ~40 modules and two cloud auth stacks to a 73-module graph whose minimalism is itself a security control. The real defect — the binary is absent from the image — is fixed in the image |
| PD-15(5) | move `Sink` into `model`, or have `cmd` adapt | **Modified.** Consumer-declared interface whose signature mentions only `model`/`tenant`/stdlib (DR-2) | Go interfaces are structural: no import edge is created, so the cycle never existed. The documented *implication* of an import was the real defect |
| SR-7 | hot rows written only after block-seal commit | **Modified.** The `block_id` *pointer* is written after the manifest commits; the `trace` row exists at ack (DR-7) | Otherwise SR-7's own AC ("a trace queried immediately after ack resolves, not a 404") and the 12 s ingest-to-queryable NFR are both unreachable |
| CC-8, PD-31 | `TenantPolicy` in `model`; resolver in `auth` | **Modified.** A dedicated `internal/tenant` package (DR-3) | `auth` must *use* a tenant policy (RBAC bindings) while `store`, `sampler` and `remediate` must *read* it without importing `auth` |
| CC-3 | keep `alerting.gate: investigation` as the only gate | **Modified.** Three conditions, including a configured critical-SLO bypass (DR-21) | A pure investigation gate makes a wedged brain a silent monitor (SR-14); the bypass is bounded to an explicitly configured, empty-by-default list, and severity/tier bypasses are still deleted |
| SR-12, PD-14 | one budget document owns all seven numbers | **Adopted, and extended.** Cache reads get their own dimension (DR-17) | Charging cache reads against `max_tokens_in` would make `max_tool_calls: 40` unreachable; the *cost* cap in µUSD is the honest ceiling and charges every dimension |
| CC-13 | reconcile MCP to `01 §6.2`'s 13 tools | **Modified.** 12 tools — `traceiq_propose_action` is not exposed over MCP in v1 (DR-29) | `01 §6.2`'s own reasoning (an approval must name a person) applies equally to a proposal authored by a token |

---

## Appendix C — New FR and AC IDs mandated by this register

Fix agents create exactly these IDs, with exactly these numbers.

| Doc | New FRs | New ACs |
|---|---|---|
| F01 | — | AC-F01-9 |
| F02 | FR-F02-13, -14 | AC-F02-11 … AC-F02-16 |
| F03 | FR-F03-14, -15 | AC-F03-14, AC-F03-15 (AC-F03-1 split; AC-F03-5 rewritten) |
| F04 | FR-F04-10 | AC-F04-9, AC-F04-10 |
| F05 | FR-F05-13, -14, -15 (**FR-F05-11 deleted**) | AC-F05-12 … AC-F05-16 |
| F06 | FR-F06-12a, -12b, -18, -19, -20, -21, -22, -23, -24 | AC-F06-15 … AC-F06-24 |
| F07 | FR-F07-6, -7, -8 | AC-F07-6, -7, -8 |
| F08 | FR-F08-9, -10 (FR-F08-4, -7 rewritten) | AC-F08-3 rewritten, AC-F08-7 extended |
| F09 | FR-F09-15 … FR-F09-23 | AC-F09-6 … AC-F09-15 |
| F10 | FR-F10-9, -10 | AC-F10-9, AC-F10-10 |
| F11 | FR-F11-11, -12, -13 (**FR-F11-3 deleted**) | AC-F11-5 … AC-F11-8 |
| F12 | FR-F12-12 … FR-F12-19 (FR-F12-8 rewritten) | AC-F12-6 … AC-F12-11 |
| X-SEC | FR-XSEC-11 … FR-XSEC-15 | AC-XSEC-4a–d, AC-XSEC-6 … AC-XSEC-14 |
| X-OPS | FR-XOPS-4, -5 rewritten | AC-XOPS-9 … AC-XOPS-12 |

New follow-ups added to `05 §9`: **FU-10** (land `internal/archtest`'s time/rand ban before the first feature merge), **FU-11** (generate `defaults.md` and `traceability.md` and diff both in CI).

---

## Appendix D — Known gaps (round-2 verification, tracked not silently dropped)

| Gap | Where it surfaces | Status |
|---|---|---|
| `model.ToolResult`'s field list is never printed in this register, despite the type being referenced by name in DR-15, DR-16 §16.3, DR-37 and DR-18. `internal/model/rca.go` carries the type with a `TODO(DR-15/DR-16)` comment recording exactly this gap. This is load-bearing because of DR-18: `Step` persists `ToolResultHash`/`ToolResultRef`/`ToolResultBytes` over the canonical result bytes, and DR-18's replay drift-detection compares `sha256(newResult)` against `ToolResultHash` — so replay correctness cannot be verified against a type whose fields no document defines. | F06 implementation (`rca.Tool.Invoke`, `ToolRegistry.Dispatch`) | **Open — architecture board to resolve before F06 coding begins.** Do not invent a field list; extend this appendix with a DR-40 when resolved. |

---

*End of register. DR-0 … DR-39, forty binding decisions, 98 of 98 round-1 findings resolved. One known gap tracked in Appendix D.*

