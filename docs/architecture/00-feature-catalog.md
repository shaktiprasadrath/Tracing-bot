# TraceIQ Feature Catalog (single source of truth)

> Revision 2 — 2026-09-15 — applies DR-0, DR-3, DR-4, DR-5, DR-10, DR-15, DR-25, DR-38 (round-1 fixes)

Source: Tracing-Bot-PRD.md. Every feature maps to one or more gaps found in the compared tools
(Jaeger, Zipkin, Grafana Tempo, Datadog APM, Dynatrace). Feature IDs are stable; docs, code packages,
tests, and reviews reference them.

**Fact ownership (DR-0).** This catalog is a map, not a source of truth for any fact it points
at. Shared Go types live in `01 §4`; per-package interfaces are decided in
`06-decision-register.md` and mirrored into `02` — this catalog lists interface and type **names
only**, never a restated method signature; package adjacency lives in `02 §5`; every config key
and default lives in `01 §7`; every performance/accuracy gate lives in `01 §10`. Where this file
and any of those disagree, this file is wrong and should be fixed to cite, not restate.

## Compared-tool drawbacks (must ALL be addressed by the design)

| ID | Tool | Drawback |
|----|------|----------|
| D-J1 | Jaeger | No built-in alerting / anomaly detection |
| D-J2 | Jaeger | No automated RCA (trace diff only) |
| D-J3 | Jaeger | Traces only — no log/metric correlation |
| D-J4 | Jaeger | Head-sampling blind spots; tail sampling operationally hard |
| D-J5 | Jaeger | Self-operated Cassandra/ES storage burden; basic trace-centric UI |
| D-Z1 | Zipkin | Feature-frozen; OTel deprecated Zipkin exporters (Dec 2025) |
| D-Z2 | Zipkin | Dependency graph needs external Spark batch job (not real-time) |
| D-Z3 | Zipkin | Client-side head sampling only; volatile in-memory default storage |
| D-Z4 | Zipkin | No alerting, analytics, RCA, AI; basic search UI |
| D-T1 | Tempo | Slow attribute searches at scale (no index; Parquet scans) |
| D-T2 | Tempo | Grafana-dependent; no standalone UI; no built-in alerting/RCA |
| D-T3 | Tempo | AI is hooks-only (MCP server, LLM API) — no reasoning shipped |
| D-T4 | Tempo | "Store everything" shifts cost to query-time compute; high-cardinality pain |
| D-D1 | Datadog | Cost unpredictability / bill shock (per-host + per-span + per-SKU) |
| D-D2 | Datadog | Retention limits: 15-min live search / 15-day indexed |
| D-D3 | Datadog | Vendor lock-in; lossy OTel semantic-convention translation |
| D-D4 | Datadog | Alert noise out of the box |
| D-D5 | Datadog | Agentic AI tied to paid platform; silent eval-quality regressions |
| D-Y1 | Dynatrace | Black-box AI (Davis decisions not inspectable/tunable) |
| D-Y2 | Dynatrace | Cost (~$58/host/mo), metered queries, enterprise-only |
| D-Y3 | Dynatrace | Steep learning curve (DQL, Smartscape, Gen-3 UI) |
| D-Y4 | Dynatrace | Analytical value (topology/RCA/memory) not exportable |
| D-X1 | All | Sampling loses the traces that matter |
| D-X2 | All | RCA manual (OSS) or black-box/expensive (commercial) |
| D-X3 | All | No natural-language tracing UX on the OSS stack |
| D-X4 | All | Remediation stops at suggestion; no guardrail standards |
| D-X5 | All | AI agents bounded by telemetry quality (cannot reason over sampled-away traces) |

## Features

| ID | Feature | Go package | Drawbacks addressed |
|----|---------|-----------|---------------------|
| F01 | Multi-format OTel-native ingest (OTLP gRPC/HTTP first-class; Jaeger + Zipkin formats for migration; OTel semantic conventions preserved verbatim) | `internal/ingest` | D-Z1, D-D3 |
| F02 | Anomaly-aware tail sampler (trace assembly, keep-100% policy for error/slow/rare/investigation-predicate, RED extraction before discard, agent feedback channel) | `internal/sampler` | D-J4, D-Z3, D-D2, D-T4, D-X1, D-X5 |
| F03 | Open tiered storage: Parquet cold store on local/object storage + hot index (SQLite embedded / ClickHouse pluggable) + retention tiers + cost controls | `internal/store` | D-J5, D-T1, D-T4, D-D1, D-D2, D-D3, D-Y2, D-Y4 |
| F04 | Live topology graph built incrementally from streaming spans (real-time, no batch job) | `internal/topology` | D-Z2, D-J5 |
| F05 | Streaming anomaly detection (per service/operation RED baselines, seasonal, deploy-marker aware) + investigation-gated alerting (anomaly events grouped by topology proximity) | `internal/anomaly` | D-J1, D-Z4, D-T2, D-D4 |
| F06 | Agentic RCA engine: hypothesis→test→validate loop, evidence-grounded, transparent + replayable investigation log, pluggable LLM (Claude API) with deterministic rule-based reasoner fallback, per-investigation budgets | `internal/rca` | D-J2, D-Z4, D-T3, D-D5, D-Y1, D-X2 |
| F07 | Trace–log–metric correlation (trace_id join to logs, exemplar join to metrics, pluggable Loki/ES/Prometheus adapters) | `internal/correlate` | D-J3, D-Z4 |
| F08 | Investigation & runbook memory (symptom fingerprint → root cause, engineer corrections as feedback, exportable JSON/Markdown) | `internal/memory` | D-Y4, D-D5 |
| F09 | Guarded remediation (read-only default, tenant allowlist, human approval gate, per-incident action budget, pre-snapshot/post-verify, immutable audit log) | `internal/remediate` | D-X4 |
| F10 | Natural-language interface (intent parsing, NL→query, chat over Slack/Teams/web; answers with evidence links) | `internal/nl` | D-Y3, D-X3 |
| F11 | Eval harness (SREBench-style fault-injection scenarios incl. Istio lab S01–S23; published, re-runnable accuracy metrics) | `internal/eval` | D-D5 |
| F12 | Web UI + API + integrations (standalone SPA embedded in binary; REST API; MCP server; Grafana datasource contract; PagerDuty/OpsGenie routing with RCA attached) | `internal/api`, `web/` | D-J5, D-T2, D-Z4 |
| X-SEC | Cross-cutting: security (TLS/mTLS, API auth, RBAC, secrets, input validation, audit) | `internal/auth`, all | — |
| X-OPS | Cross-cutting: deployment (single binary dev mode, Helm/K8s prod, multi-tenancy, self-observability) | `cmd/traceiq`, `deploy/` | — |

## Foundational / cross-cutting packages (DR-2)

Four packages exist purely to break import cycles or hold state no feature package should own
directly; none was in the original catalog. **`internal/ops` never existed and must not be
created** (CC-8, PD-31, DR-3 §3) — its would-be responsibilities are split across the packages
below and `cmd/traceiq` subcommands (`backup snapshot`, `restore`, `version --check-skew`).

| Package | Owns | May import | Used by |
|---|---|---|---|
| `internal/tenant` | `tenant.Policy`, `tenant.Resolver`, `tenant.PolicyStore` — the **only** tenant-resolution path (DR-3) | `model` | every feature package |
| `internal/auth` | Identity, RBAC, audit sink, rate limiter, secrets, egress dialer (DR-25) | `model`, `config`, `tenant` | `api`, `remediate`, `nl`, `rca`, `correlate` |
| `internal/llm` | The Anthropic HTTP client (DR-34) — the one thing `rca` and `memory` share, so neither imports the other (DR-2) | `model`, `config` | `rca`, `memory` |
| `internal/k8s` | The pinned-`kubectl` executor and argv builder (DR-24) — replaces `k8s.io/client-go`, which is deliberately absent (`05 §8.2`) | `model` | `remediate`, `eval` (ModeLive) |
| `internal/config` | Typed config load/validation (`01 §7`) | `model` | every feature package, `cmd/traceiq` |
| `internal/selfobs` | Self-tracing/metrics (X-OPS) | `model` | `ingest`, `cmd/traceiq` |
| `internal/bus` | The in-process ring / Kafka seam (DR-32 §32.3) | `model`, `config` | `cmd/traceiq`, `sampler` wiring |
| `internal/cluster` | Leader election for the audit appender and multi-replica coordination (DR-27 §27.2) | `config` | `cmd/traceiq`, `auth` |

## Core interfaces (names fixed so docs and code agree)

**Names only (DR-0).** Full method signatures, parameter order (every tenant-scoped method takes
`ctx context.Context, tid model.TenantID` first — DR-5) and return types are decided in
`06-decision-register.md` and mirrored into `02`; this list exists so a reader can find the right
package without re-deriving the signature.

- `tenant.Resolver`, `tenant.PolicyStore` — the sole tenant-resolution and per-tenant-policy path (DR-3).
- `ingest.Receiver`, `ingest.SpanSink` — accepts spans in any supported format, emits `model.Span`/`model.Batch`; `SpanSink` is consumer-declared and satisfied structurally by `sampler` and `topology` (DR-2).
- `sampler.Sampler`, `sampler.PolicyEvaluator`, `sampler.BaselineSource`, `sampler.PredicateSet` — trace assembly, keep-rate policy, interest predicates (phase A/B), baseline-cached decisions (DR-10, DR-11).
- `store.HotIndex`, `store.ColdStore`, `store.ObjectStore` (renamed from `BlobStore`) — batch-oriented hot index, WAL-journaled cold store with block manifests, and the object-store seam (DR-6, DR-7).
- `topology.Graph`, `topology.EdgeSink`, `topology.EdgeSource` — live topology graph plus the consumer-declared persistence seam `store/sqlite` satisfies (DR-13).
- `anomaly.Detector`, `anomaly.Grouper`, `anomaly.BaselineStore`, `anomaly.QuantileEstimator`, `anomaly.DeployIndex` — detector set, incident grouping, seasonal baselines, deploy-window correlation (DR-14).
- `rca.Engine`, `rca.Reasoner`, `rca.Tool`, `rca.ToolRegistry`, `rca.Journal` (renamed from `StepStore`), `rca.SchemaValidator`, `rca.Sanitizer`, `rca.Budget` — the investigation loop, the closed five-tool set, step persistence, output validation, prompt-injection wrapping, and cost/step/wall-clock accounting (DR-15, DR-16, DR-17, DR-37).
- `correlate.Correlator` — `LogsForTrace`, `MetricsForSpan`, tenant-scoped adapters (DR-20).
- `memory.Store`, `memory.SimilarityScorer`, `memory.Embedder`, `memory.RunbookImporter` — fingerprint similarity search, scoring, embedding, and runbook import (DR-19).
- `remediate.Guard`, `remediate.Verifier` — the sole write path into customer infrastructure; `Guard.Approve` takes an `auth.Subject`, never a bare string (DR-23).
- `k8s.Executor` — the pinned-`kubectl` argv executor `remediate` and `eval` (ModeLive) both call; `k8s.BuildArgv` is the only argv constructor in the system (DR-24).
- `auth.Authenticator`, `auth.Authorizer`, `auth.RateLimiter`, `auth.AuditSink` (aliased `AuditLog`), `auth.SecretSource`, `auth.EgressDialer`, `auth.IdentityStore` — identity, the capability-matrix RBAC check (not a role hierarchy), the hash-chained externally-anchored audit sink, secrets, the one egress-client factory, and chat identity binding (DR-25, DR-27, DR-20).
- `llm.Client` — the one Anthropic HTTP client shared by `rca` and `memory` (DR-34, DR-2).
- `nl.Interpreter`, `nl.Answerer` — closed ten-intent taxonomy; every data access dispatches through `rca.ToolRegistry`, never a second query path (DR-35).
- `eval.Runner` — drives fixtures through the real `ingest.Receiver`, never a bypass path; `eval.VirtualClock` for deterministic offline runs (DR-31, DR-36).
- `api.Server` (renamed `api.HTTPServer` + `api.Deps`) — HTTP/JSON REST + static UI + the twelve-tool MCP endpoint (DR-29, DR-30).
- `model.Clock` (+ `Ticker`, `Timer`, `Barrier`) — the one time source; `time.Now`/`time.After`/package-level `math/rand` are forbidden outside `internal/model` and `cmd/traceiq`, enforced by `internal/archtest` (DR-31).

## PRD promises → owning FR or explicit deferral (DR-38)

A PRD claim with neither an owning FR nor a dated deferral below is not part of this design; it
must not appear as a bare assertion in `01 §9`'s drawback table.

| PRD promise | Disposition |
|---|---|
| "TraceIQ can consume Tempo/Grafana MCP servers as backends" / "can run on top of Tempo" | **Phase 3 deferral.** Struck from `01 §9`'s D-T3 row until an FR exists. D-T3's primary claim — TraceIQ ships the reasoning loop — is unaffected. |
| "Local model option for air-gapped" | **Phase 3 deferral** (DR-34). `rca.llm.provider` keeps the seam; `01 §7` notes it. |
| "Works out-of-the-box on Istio / service-mesh telemetry (Envoy access logs enrich topology)" | **Owned.** `FR-F04-10` — an Envoy access-log → `topology.Edge` adapter, behind `topology.envoy_access_logs.enabled: false` (DR-38). |
| "Correlation contract: `trace_id` injected into logs" | **Owned as a deployment prerequisite.** `FR-F07-8` — a startup capability check; below 50% trace-correlated log lines, every affected report is labelled, never silently degraded. |
| "Service metadata (owners, SLOs, escalation) in memory" | **Owned.** `model.ServiceMeta` (DR-4), sourced from resource attributes or `tenant.Policy`; `GET /v1/services`; `memory.Record.Kind = ServiceMeta`. |
| "Execution via existing operators (ArgoCD rollback, …)" | **Phase 3 deferral.** The five closed `model.ActionType` values (DR-22) are v1. |
| "Replay a past investigation with a different model / re-reasoning" | **Phase 3 deferral** (`ReplayReReason`, DR-18 §18.2) — v1 ships `ReplayRecorded` and `ReplayLiveDiff` only. |
| Canary / progressive-delivery rollout awareness | **Phase 3 deferral** (DR-14 §14.6). `model.DeployMarker.RolloutFraction` exists in the type but is unused in v1. |
