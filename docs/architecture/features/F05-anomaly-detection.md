# F05 — Streaming Anomaly Detection + Investigation-Gated Alerting

> Revision 2 — 2026-09-15 — applies DR-0, DR-4, DR-5, DR-13, DR-14, DR-17, DR-21, DR-29, DR-31, DR-35, DR-38, DR-39 (round-1 fixes)

## 1. Purpose

`internal/anomaly` watches every span TraceIQ ingests and maintains a live statistical baseline for
each `(service, operation)` pair — latency distribution, error rate, and throughput, each adjusted
for time-of-day/day-of-week seasonality (31 slots: 24 hour-of-day + 7 weekday multipliers — DR-14
§14.2). Five streaming detectors compare live behavior against these baselines and emit
`model.AnomalyEvent`s the instant behavior deviates. A `Grouper` merges events that share a topology
neighborhood and time window into `model.Incident` candidates, which are the unit handed to the RCA
engine (F06). F05 never pages a human directly: an `Event` is a signal, an `Incident` is a candidate,
and a page is emitted **only** under the three conditions owned by `01 §7`/`01 §9`'s D-D4 row and
enforced by `api.AlertRouter` (DR-21) — there is no severity- or tier-derived bypass anywhere in this
package. This inversion — detect broadly, alert narrowly — is TraceIQ's answer to alert fatigue.

## 2. Compared-tool drawbacks addressed

| Drawback ID | Tool | Lagging feature | How TraceIQ fixes it (concrete mechanism in F05) |
|---|---|---|---|
| D-J1 | Jaeger | No built-in alerting or anomaly detection; users must build it externally on spanmetrics + Prometheus/Grafana rules. | F05 computes RED baselines and runs 5 detectors *inside the ingest path* with zero external rule configuration — `anomaly.Detector` is a first-class package, not a bolt-on. |
| D-Z4 | Zipkin | No alerting, analytics, RCA, or AI; basic search UI only. | F05 gives Zipkin-class deployments a full streaming detection layer (latency/error/new-signature/topology/deploy detectors) with no code change to instrumented services. |
| D-T2 | Tempo | No standalone UI; alerting delegated entirely to hand-written Prometheus rules on the metrics-generator output. | F05 replaces manually tuned PromQL alert thresholds with automatically learned, per-key, seasonally-adjusted baselines (`anomaly.Baseline.Q`, `Baseline.ErrorEWMA`) that require no operator-authored rule per service. |
| D-D4 | Datadog | Default monitors generate high volumes of non-actionable alerts without careful tuning. | `anomaly.Grouper` deduplicates/merges correlated events into one `Incident` (O(1) amortized, DR-14 §14.7), and the binding paging rule — a page fires iff P1 (terminal investigation with `ok`-verdict evidence), P2 (hard ceiling with a partial report attached), or P3 (an explicit, per-tenant, empty-by-default critical-SLO list) holds, enforced by `api.AlertRouter` — makes noise structurally impossible to page on, not a tuning exercise. `model.ServiceMeta.Tier` is never a paging input (DR-21 §21.1). |

## 3. Requirements

### 3.1 Functional

| ID | Statement |
|---|---|
| FR-F05-1 | The system SHALL maintain a per-`(tenant, service, operation, season slot)` quantile estimate `{P50, P95, P99}` (`model.Quantiles`, DR-39 — no p90) using `anomaly.P2Estimator` (P² algorithm, 5 markers per quantile, fixed 240 B, zero allocation after construction) for each of the 31 seasonal slots, and `anomaly.TDigest` (compression 100, ≤ 512 B serialized) for the global slot (`bucket = 255`) only (DR-14 §14.1). |
| FR-F05-2 | The system SHALL maintain an EWMA error rate (`Baseline.ErrorEWMA`) per `(tenant, service, operation, season slot)` with smoothing factor `anomaly.baseline.ewma_alpha` (defaults in `01 §7`, DR-14 §14.9). |
| FR-F05-3 | The system SHALL maintain an EWMA throughput (`Baseline.RPSEWMA`, req/s) per `(tenant, service, operation, season slot)`; this signal is consumed by the `throughput_drop` detector (FR-F05-13). |
| FR-F05-4 | Baselines SHALL follow the four-state cold-start table of DR-14 §14.3 — **Cold** (global slot `Samples < warmup_samples`): all detectors suppressed for the key; **Global-only** (global warmed, season slot `Samples < warmup_samples_per_bucket`): detectors run against the global slot with thresholds widened by `cold_start_multiplier`, events carry `Provisional = true`; **Warm** (both warmed): normal thresholds; **Provisional-by-decree** (`now − first_seen > max_cold_start` and still Cold): the key is forced Warm on whatever samples exist, every derived event/incident carries `Provisional = true`, which caps `Incident.Score` at 0.69 (never `Critical`, never satisfies DR-21's P1). Seasonal slots are 31 (24 hour-of-day + 7 weekday multipliers), not 168 hour-of-week buckets. |
| FR-F05-5 | The `latency_shift` detector SHALL emit `model.AnomalyEvent{Kind: KindLatencyShift}` when, over the rolling `anomaly.eval_window` (90 s): `obs.P95 >= base.P95 × latency_ratio` (1.5) **and** `obs.P95 − base.P95 >= latency_abs_delta` (50 ms) **and** `obs.Calls >= min_calls` (20), sustained for `debounce_ticks` (2) consecutive `eval_interval` (30 s) ticks. `Event.Score = clamp01(0.5·min(1,(ratio−1)/1.0) + 0.5·min(1, delta/(4·latency_abs_delta)))` (DR-14 §14.4). |
| FR-F05-6 | The `error_burst` detector SHALL emit `model.AnomalyEvent{Kind: KindErrorBurst}` when `obs.ErrorRate − base.ErrorEWMA >= error_rate_delta` (0.05) **and** `obs.ErrorRate >= base.ErrorEWMA × error_burst_ratio` (3.0) **and** `obs.Calls >= min_calls` (20). `Event.Score = clamp01(0.5·min(1,(obs.ErrorRate−base.ErrorEWMA)/0.20) + 0.5·min(1, obs.ErrorRate))` (DR-14 §14.4). |
| FR-F05-7 | The `new_error_signature` detector SHALL emit `model.AnomalyEvent{Kind: KindNewErrorSignature}` within `eval_interval` of a signature unseen in `new_error_signature_lookback` (7 d) accumulating `>= new_error_sig_min_calls` (5) occurrences in the window. The detector reads `store.ErrorSignature` rows written by the single telemetry writer on their own bounded path (DR-39 §39.3) — it does **not** consume per-span data. `Event.Score = clamp01(0.4 + 0.6·min(1, occurrences/50))`. |
| FR-F05-8 | The `topology_change` detector SHALL emit `model.AnomalyEvent{Kind: KindTopologyChange}` for a `NewEdge` with `Calls >= topology.new_edge_min_calls` (5) (`Score = 0.50`), or for a `VanishedEdge` on an edge with ≥ 1 000 calls in the prior 24 h (`Score = 0.60`). Intake unions `topology.Graph.Changes()` (drained non-blockingly, ≤ 256/tick) with a durable reconciliation read through the consumer-declared `anomaly.EdgeMetaReader` every tick, so a stalled channel consumer delays but never loses a `NewEdge` detection (DR-14 §14.7, DR-13 §13's `Changes()` durability guarantee). |
| FR-F05-9 | `deploy_regression` is **not** a sixth `anomaly.Kind` (DR-14 §14.1, §14.6). It is an **enrichment**: a `latency_shift` or `error_burst` event whose window intersects `anomaly.DeployIndex.Near(service, at, anomaly.deploy_markers.correlation_window)` (30 m) is tagged in place with `Event.DeployMarkerIDs` and `Event.Score += 0.10` (clamped), computed via `DeployIndex.PrePostSplit`, which defines `pre = {m.At−w, m.At}` and `post = {m.At+settle, m.At+settle+w}` with `settle = anomaly.deploy_markers.settle` (2 m). `anomaly.DeployIndex` is the single owner of the deploy-window abstraction; `rca` (F06) calls the same interface for its `error-signature-new-after-deploy` rule — there is no separate F05/F06 implementation. Canary/progressive-delivery deploys (`model.DeployMarker.RolloutFraction`) are an explicit Phase 3 deferral recorded in `00`. |
| FR-F05-10 | `Grouper.Add` SHALL attach an event to an open incident whose topology `anomaly.grouping.topology_hops`-hop neighborhood (default 2, via `TopologyReader.Neighbors`) already contains the event's service and which was updated within `anomaly.grouping.window` (5 m), or open a new incident otherwise — evaluated in O(1) amortized time per event via a `byService` inverted index and a bounded `neighborCache` (LRU 4 096, TTL 60 s), **never** a per-event scan over open incidents (DR-14 §14.7). |
| FR-F05-11 | **Deleted (DR-21).** There is no severity- or tier-derived paging bypass anywhere in this package; `model.ServiceMeta.Tier` is never an input to any grouping, scoring, or paging decision. The binding paging rule (three conditions, no exceptions) is owned by `01 §7`/`01 §9`'s D-D4 row and enforced by `api.AlertRouter`; see DR-21 §21.1. |
| FR-F05-12 | The detector evaluation loop SHALL sustain **≥ 5 000 `model.REDSample` per second per core at `Res10s`** — 250× the `01 §10.1` reference load — with detectors consuming bucket aggregates, never per-span input (DR-39 §39.3; replaces the prior "50 000 samples/sec" target, which was 2.4× short of the headline it was meant to serve). |
| FR-F05-13 | *(New — DR-14 §14.4, Appendix C.)* The `throughput_drop` detector SHALL emit `model.AnomalyEvent{Kind: KindThroughputDrop}` when `obs.RPS <= base.RPSEWMA × throughput_drop_ratio` (0.5) **and** `base.RPSEWMA >= min_rps` (0.1). `Event.Score = clamp01(1 − obs.RPS/base.RPSEWMA)`. This gives FR-F05-3's throughput EWMA its first consumer. |
| FR-F05-14 | *(New — DR-14 §14.7, Appendix C.)* `Grouper.Add` SHALL complete in **p99 ≤ 5 ms at 200 open incidents and 5 000 events/min**, with **≤ 1** `TopologyReader.Neighbors` call per newly-added service per incident and **0** per event — inside `01 §10.1`'s "incident grouping ≤ 5 s after the triggering event" gate. |
| FR-F05-15 | *(New — DR-14 §14.8, Appendix C.)* End-to-end detection latency (deviation onset → `anomaly_event` persisted) SHALL be **≤ 75 s p95**: `eval_interval` (30 s, first trigger) + `eval_interval` (30 s, confirming tick) + ≤ 15 s tick-phase jitter — inside `01 §10.1`'s ≤ 90 s p95 gate with 15 s of margin. |

### 3.2 Non-functional

*Every target below is a citation of the gate `01 §10` owns; the derivations are DR-14's and are reproduced here only because the register requires re-publishing the arithmetic wherever it is depended on (DR-14 §14.2).*

| Category | Target |
|---|---|
| Memory | **≈ 120 MiB** at `anomaly.baseline.max_keys` = 10 000 keys/tenant (288 B/key/season-slot × 31 slots + 512 B global t-digest + 200 B key header ≈ 9 640 B/key, plus ≈ 27 MiB error-signature/EWMA/neighbor-cache side state) — the figure DR-9's RSS derivation uses. Owning gate: `01 §10.1` (derivation: DR-14 §14.2). |
| Availability | `Detector.Evaluate` is pure w.r.t. `(in, clock)` — no I/O, no store call — and runs once per `eval_interval` per tenant over accumulated samples; the ingest hot path is never blocked by detector evaluation (DR-14 §14.1). |
| Durability | Baselines checkpoint **incrementally**: at most `checkpoint_max_rows` (2 000) dirty `(key, bucket)` rows per `checkpoint_interval` (60 s), oldest-dirty first, through the **telemetry** writer — 33 rows/s against DR-6's 2 000 rows/s budget (1.7 %). A dirty backlog above 20 000 rows coarsens the interval to 300 s rather than growing the batch. The full-sweep (1.68 M blobs/60 s) design is deleted (DR-14 §14.2). |
| Cardinality control | `anomaly.baseline.max_keys` = **10 000 per tenant**; LRU eviction by lowest-`RPSEWMA` among the oldest-`UpdatedAt` decile, `traceiq_anomaly_baseline_keys_evicted_total`. Startup validation refuses `max_keys × tenants` exceeding `anomaly.baseline.max_keys_global` (20 000) (DR-14 §14.2). Operation-name templating at the ingest normalizer still applies before keying. |
| Cold start | Four-state table, FR-F05-4 / DR-14 §14.3 — no detector fires above `Provisional` characteristics until the key is Warm or forced Warm-by-decree at 24 h. |
| Detection latency | **≤ 75 s p95**, owning gate `01 §10.1` — see FR-F05-15. |

## 4. Design

### 4.1 Component diagram

```mermaid
flowchart TB
    subgraph Ingest["internal/ingest"]
        SP[Span Stream]
    end

    subgraph AnomalyPkg["internal/anomaly"]
        EX[RED Extractor]
        BL[(BaselineStore<br/>per tenant+service+operation<br/>31 seasonal slots)]
        D1[Latency-Shift Detector]
        D2[Error-Burst Detector]
        D3[New-Error-Signature Detector]
        D4[Throughput-Drop Detector]
        D5[Topology-Change Detector]
        DI[DeployIndex<br/>enrichment, not a 6th Kind]
        GRP[Grouper<br/>O(1) amortized]
    end

    subgraph Deps["Collaborators"]
        TOPO[topology.Graph]
        DEPLOY[Deploy Webhook Source<br/>ArgoCD/Flux/GitHub]
        STORE[store.HotIndex<br/>ErrorSignature, checkpoint]
    end

    RCA[rca.Engine]
    ALERT["api.AlertRouter<br/>(DR-21: P1/P2/P3, no tier bypass)"]

    SP --> EX --> BL
    BL --> D1 & D2 & D3 & D4
    TOPO -->|EdgeMetaReader, Changes| D5
    DEPLOY --> DI
    D1 & D2 --> DI
    D1 & D2 & D3 & D4 & D5 --> GRP
    GRP -->|Incident candidate| RCA
    RCA -->|Investigation, incl. partial| ALERT
    BL <-->|incremental checkpoint| STORE
    STORE -->|ErrorSignature rows| D3
```

### 4.2 Data model

`model.AnomalyEvent` and `model.Incident` are canonical in `01 §4.3` (DR-14 §14.5) and are **not**
re-declared here (DR-0, DR-4). They carry `Score`, `Severity` (`model.Severity uint8`, 1..5 — the
prior three-value string `Severity` type is deleted), `Fingerprint`, `EpicenterService`,
`BlastRadius`, `DeployMarkerIDs`, `InvestigationID`, `SuppressedBy`, `Provisional`. `model.REDSample`
(DR-39 §39.1) is the only RED type; the local `anomaly.REDSample` (`DurationMS`, `IsError`,
`StackFingerprint`, `TraceID`) is deleted — detectors consume `Res10s` bucket aggregates, never
per-span samples.

Types that remain local to `internal/anomaly` (DR-14 §14.1):

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
// CLOSED. deploy_regression is NOT a sixth kind (FR-F05-9).

type Baseline struct {
    Service, Operation string
    Bucket      uint8            // 0..23 hour-of-day; 24..30 weekday slots; 255 global
    Q           model.Quantiles  // P50Nanos, P95Nanos, P99Nanos, MaxNanos — no p90 (DR-39)
    ErrorEWMA   float64
    RPSEWMA     float64
    Samples     uint32
    Warmed      bool
    Provisional bool             // warmed by decree at max_cold_start; stamped onto every derived event
    UpdatedAt   time.Time
}

// SeasonalBaseline (renamed from the prior SeasonalTDigest, 02 §2) holds 31 P2Estimator
// triples (one per season slot, one per tracked quantile) plus one TDigest for bucket 255.
```

Incident scoring, severity, fingerprint and epicenter are computed exactly as in DR-14 §14.5:

```
Incident.Score        = clamp01( max(event.Score) × (1 + 0.05 × (distinctServices − 1)) )
Incident.Fingerprint  = "fp1:" + hex(xxh3( tenantID ‖ "\x00" ‖ join(sorted(tokens), "\x01") ))
   tokens ∈ { "svc:<service>", "op:<operation>", "kind:<Kind>", "errsig:<id>", "dep:<callee>", "ns:<namespace>" }, ≤ 24
Incident.EpicenterService = rankEpicenter(events, topology)  // highest (score × inbound-edge count);
                                                              // ties by earliest FirstSeen, then lexical
Incident.BlastRadius      = services within grouping.topology_hops of the epicenter that carry an event
```

| `Incident.Score` | Distinct services | Severity |
|---|---|---|
| < 0.55 | any | not an incident (event only) |
| 0.55–0.69 | 1 | `Low` (2) |
| 0.55–0.69 | ≥ 2 | `Medium` (3) |
| 0.70–0.84 | any | `High` (4) |
| ≥ 0.85 | any | `Critical` (5) |

This fingerprint construction is identical to `memory.Fingerprint.Compute` (DR-19 §19.1) — one
implementation, in `anomaly`, called by `memory`.

**SQLite (hot index / control) tables** (DR-6 §6.2, DR-14 §14 Docs-to-change; DDL owned by `01 §5.1`
— cited by name only): `anomaly_baseline` (hot: `tenant_id, service, operation, bucket, p50/p95/p99_nanos,
error_ewma, rps_ewma, samples, digest, updated_at`); `anomaly_event` / `incident` (control: gain
`score`, `severity`, `fingerprint`, `epicenter_service`, `blast_radius_json`, `deploy_marker_ids_json`,
`suppressed_by`, `provisional`). The prior locally-declared `anomaly_baselines` / `anomaly_events` /
`anomaly_incidents` / `anomaly_incident_events` tables are deleted (DR-4, DR-14).

### 4.3 Interfaces & APIs

**Canonical interfaces (DR-14 §14.1, replaces this section wholesale):**

```go
package anomaly

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

type BaselineStore interface {
    Observe(ctx context.Context, tid model.TenantID, s model.REDSample) error
    Reader() BaselineReader
    Checkpoint(ctx context.Context) (CheckpointReport, error)   // incremental, dirty keys only
    Load(ctx context.Context, tid model.TenantID) (int, error)  // warm start from anomaly_baseline
    Stats() BaselineStats
}

// QuantileEstimator is the seam between P2 (default, per season slot) and t-digest
// (global slot only). Two implementations, and only two: anomaly.P2Estimator and anomaly.TDigest.
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
    ActiveIncidents(ctx context.Context, tid model.TenantID) ([]model.Incident, error)  // consumed by nl's IntentIncidentStatus (DR-35 §35.3)
    Suppress(ctx context.Context, tid model.TenantID, id, reason string, ttl time.Duration) error
    Tick(ctx context.Context, now time.Time) ([]model.Incident, error)
    Stats() GrouperStats
}

type GrouperStats struct {
    OpenIncidents         int
    LastTickAt            time.Time   // read by the DR-21 deadman
    NeighborCacheHitRatio float64
}

// DeployIndex — the single owner of the deploy-window abstraction (DR-14 §14.6).
// F06's error-signature-new-after-deploy rule calls this same interface.
type DeployIndex interface {
    Near(ctx context.Context, tid model.TenantID, service string, at time.Time, window time.Duration) ([]model.DeployMarker, error)
    PrePostSplit(ctx context.Context, tid model.TenantID, m model.DeployMarker) (pre, post model.Window, err error)
    Record(ctx context.Context, tid model.TenantID, m model.DeployMarker) error
    ListWindow(ctx context.Context, tid model.TenantID, w model.Window) ([]model.DeployMarker, error)
}

// EdgeMetaReader — consumer-declared; satisfied by store/sqlite over topology_edge_meta
// (DR-13). Used by the topology-change detector's durable reconciliation path (FR-F05-8).
type EdgeMetaReader interface {
    NewEdgesSince(ctx context.Context, tid model.TenantID, since time.Time) ([]topology.Edge, error)
}
```

Two `QuantileEstimator` implementations exist, and only two: `anomaly.P2Estimator` (the per-season-slot
default) and `anomaly.TDigest` (global slot and `red_rollup_1h.digest` only). The prior
`anomaly.SeasonalTDigest` type (`02 §2`) is renamed `anomaly.SeasonalBaseline`.

**Every exported method above takes `ctx` first and `model.TenantID` second (DR-5); `internal/archtest`
fails the build on any exception outside the genuine-global allowlist (`Kind`, `Stats`).**

**REST/config surface.** F05 renders no path of its own — every endpoint is a **filtered view** of
`01 §6.1`, headed "defined in `01 §6.1`" (DR-29 §29.1):

| Method | Path | Purpose | Role |
|---|---|---|---|
| GET | `/v1/baselines` | Anomaly baselines and warm state | viewer |
| GET | `/v1/incidents?status=candidate` | List open incident candidates | viewer |
| GET | `/v1/events?service=&since=` | Raw event stream for a service | viewer |
| GET | `/v1/deploys` | Deploy markers in a window | viewer |

Config key **paths** (values and defaults are owned exclusively by `01 §7`, DR-0; this doc cites paths,
never numbers — see DR-14 §14.9 for the authoritative block): `anomaly.detectors`,
`anomaly.eval_interval`, `anomaly.eval_window`, `anomaly.debounce_ticks`, `anomaly.baseline.*`
(`estimator`, `tdigest_compression`, `ewma_alpha`, `seasonal`, `season_slots`, `warmup_samples`,
`warmup_samples_per_bucket`, `cold_start_multiplier`, `max_cold_start`, `max_keys`, `max_keys_global`,
`max_error_signatures`, `checkpoint_interval`, `checkpoint_max_rows`, `min_rps`), `anomaly.thresholds.*`
(`latency_ratio`, `latency_abs_delta`, `error_rate_delta`, `error_burst_ratio`, `throughput_drop_ratio`,
`new_error_signature_lookback`, `new_error_sig_min_calls`, `min_calls`, `min_event_score`),
`anomaly.grouping.*` (`window`, `topology_hops`, `max_events_per_incident`, `max_open_incidents`,
`dedupe_ttl`, `neighbor_cache_entries`, `neighbor_cache_ttl`), `anomaly.deploy_markers.*` (`enabled`,
`correlation_window`, `settle`). A `detectors` list naming an unknown detector is a startup error.

### 4.4 Algorithms / decision logic

**Baseline update (per RED sample, DR-14 §14.1):**
```
function Observe(tid, sample):     // sample: model.REDSample at Res10s
    key = (tid, sample.Service, sample.Operation)
    bucket = seasonSlot(sample.BucketStart)      // 0..23 hour-of-day, or 24..30 weekday multiplier
    b = baselines[key][bucket]                   // lazy-init on first touch; P2Estimator per quantile
    for q in {P50, P95, P99}: b.Q[q].estimator.Add(sample derived value)
    alpha = anomaly.baseline.ewma_alpha
    b.ErrorEWMA = alpha * (sample.Errors / max(1, sample.Calls)) + (1-alpha) * b.ErrorEWMA
    b.RPSEWMA   = alpha * (sample.Calls / bucketSeconds) + (1-alpha) * b.RPSEWMA
    b.Samples++
    b.UpdatedAt = sample.BucketStart
    globalBaselines[key].update(sample)           // bucket 255, TDigest — always also updated
    markDirty(key, bucket)                        // for incremental checkpoint
    evaluateDetectors(tid, key, bucket)            // dispatched on the eval_interval tick, not per-sample
```

**Cold-start state resolution (FR-F05-4, DR-14 §14.3):**
```
function State(key):
    if global.Samples < warmup_samples:                                    return Cold
    if season.Samples < warmup_samples_per_bucket:                         return GlobalOnly  // widen thresholds ×cold_start_multiplier, Provisional=true
    if state was Cold and now - key.firstSeen > max_cold_start:            return ProvisionalByDecree  // force Warm, Provisional=true, Score capped 0.69
    return Warm
```

**Detector evaluation (once per `eval_interval` tick, DR-14 §14.4):**
```
function EvalLatencyShift(tid, key):
    obs  = rollingWindow[key].last(anomaly.eval_window)     // 90s
    base = Baselines.Get(tid, key.service, key.operation, now)
    if base.State == Cold: return nil
    ratioTh, deltaTh = widenIfGlobalOnly(latency_ratio, latency_abs_delta, base.State)
    if obs.P95 >= base.Q.P95Nanos * ratioTh
       and obs.P95 - base.Q.P95Nanos >= deltaTh
       and obs.Calls >= min_calls:
        consecutiveBreach[key]++
        if consecutiveBreach[key] >= debounce_ticks:  // 2
            score = clamp01(0.5*min(1,(ratio-1)/1.0) + 0.5*min(1, delta/(4*latency_abs_delta)))
            emit model.AnomalyEvent{Kind: KindLatencyShift, Score: score, Provisional: base.State != Warm}
            consecutiveBreach[key] = 0
    else:
        consecutiveBreach[key] = 0
    // error_burst, throughput_drop follow the identical shape against their own trigger (§3.1)
```

**`new_error_signature` detector** reads `store.ErrorSignature` rows on its own bounded path (≥ 5 000
rows/s/core, FR-F05-12) rather than per-span samples: a signature unseen in
`new_error_signature_lookback` (7 d) that accumulates `>= new_error_sig_min_calls` (5) occurrences in
the window emits immediately.

**Deploy enrichment (FR-F05-9, DR-14 §14.6) — exact split:**
```
w      = anomaly.deploy_markers.correlation_window   // 30m
settle = anomaly.deploy_markers.settle               // 2m
pre    = { m.At − w,        m.At }
post   = { m.At + settle,   m.At + settle + w }

on latency_shift or error_burst event whose window intersects DeployIndex.Near(service, at, w):
    event.DeployMarkerIDs = append(event.DeployMarkerIDs, marker.ID)
    event.Score = clamp01(event.Score + 0.10)
    // no new Kind, no new Event, no duplicate dispatch
```

**Grouper — O(1) amortized (DR-14 §14.7):**
```
function Add(tid, event):
    if incident open with same Fingerprint, or closed within grouping.dedupe_ttl (30m):
        attach event; write SuppressedBy = <existingIncidentID>; return (incident, false, nil)  // dedupe, not dispatched
    candidates = byService[event.Service]        // O(1) map lookup — never a scan over all open incidents
    if candidates empty and event.Service newly seen in any incident:
        neighbors = neighborCache.get(event.Service) ?? TopologyReader.Neighbors(event.Service, topology_hops, Both)  // ≤ 1 call
        neighborCache.put(event.Service, neighbors, ttl=60s)
    target = pick from candidates updated within grouping.window (5m)
    if target == nil:
        target = newIncident({event.Service}); byService[event.Service] += target.ID
    else:
        target.Events.append(event); target.Services |= {event.Service}; target.UpdatedAt = now()
        recompute Score/Severity/EpicenterService/BlastRadius (§4.2)
    if len(openIncidents) > max_open_incidents (200):
        force-close lowest-Score open incident, Status = Expired, traceiq_anomaly_incidents_force_closed_total++
    return (target, true, nil)
```

### 4.5 Sequence diagram

```mermaid
sequenceDiagram
    participant SP as Span Stream
    participant EX as RED Extractor
    participant DET as Detector
    participant TOPO as topology.Graph
    participant GRP as Grouper
    participant RCA as rca.Engine
    participant ALT as api.AlertRouter

    SP->>EX: span
    EX->>DET: model.REDSample (Res10s bucket)
    DET->>DET: update Baseline (seasonal slot, P2Estimator/TDigest)
    Note over DET: evaluated once per eval_interval tick, not per sample
    alt threshold breached (debounce_ticks consecutive ticks)
        DET->>TOPO: EdgeMetaReader.NewEdgesSince / Changes() [topology_change only]
        DET->>DET: DeployIndex.Near [latency_shift/error_burst enrichment]
        DET->>GRP: model.AnomalyEvent
        GRP->>GRP: Fingerprint dedupe check (O(1))
        alt fingerprint matches an open/recently-closed incident
            GRP->>GRP: attach, SuppressedBy=<id> — no new incident
        else
            GRP->>TOPO: Neighbors(event.Service) [only for a newly-added service; cached]
            GRP->>GRP: attach to existing or open new Incident candidate
        end
        GRP->>RCA: Incident{Status: candidate}
        RCA-->>ALT: Investigation (terminal or partial) — paging decision is DR-21's, not F05's
    end
```

## 5. Failure modes & mitigations

| Failure | Detection | Mitigation |
|---|---|---|
| Cold start false positives (no baseline yet) | Global-slot `Samples < warmup_samples` | Detectors fully suppressed while Cold; widened thresholds while Global-only; forced Warm-by-decree after 24 h caps `Score` at 0.69 (FR-F05-4). |
| Legitimate traffic-pattern shift misread as anomaly (e.g., new feature launch) | Sustained breach with no deploy/topology correlation | Seasonal buckets absorb daily/weekly shifts; RCA F06 requires *evidence*, not just the event, before a page can satisfy DR-21's P1. |
| Operation-name cardinality explosion (unhashed IDs in path) | Key count approaching `max_keys` (10 000/tenant) | Path templating at the ingest normalizer; over cap, LRU-by-traffic eviction with `traceiq_anomaly_baseline_keys_evicted_total` (DR-14 §14.2). |
| Event storm during a large incident (many services breach at once) | Grouper incident event count spikes | Cap events/incident (`max_events_per_incident`, 200); cap open incidents (`max_open_incidents`, 200) with lowest-`Score` force-close. |
| Clock skew between service host clocks | Seasonal bucket misclassification | Bucket on `model.REDSample.BucketStart` (collector-side ingestion clock via `model.Clock`, DR-31), never client-reported span timestamp. |
| Baseline checkpoint loss on crash before the next incremental flush | Detector restarts with a partial dirty backlog | Checkpoint is incremental (≤ 2 000 dirty rows/60 s) rather than a full sweep, so at most one interval of updates is at risk; warm-up fallback to the global slot masks the gap (DR-14 §14.2). |
| `new_error_signature` false negative from `store.ErrorSignature` write lag | N/A (bounded, not silent) | The detector's own counter and bounded read path (≥ 5 000 rows/s/core, FR-F05-12) make lag observable rather than an unbounded per-process Bloom filter with an unstated false-positive budget. |

## 6. Security considerations

- `model.AnomalyEvent` and `model.Incident` carry no raw stack trace or log body — only
  `store.ErrorSignature` hashes and `model.Quantiles`/EWMA snapshots reach persisted state.
- Interest-predicate feedback that follows a validated investigation is pushed by `rca.Engine`, not by
  F05, and is validated and scope-capped by `sampler.InterestPredicate` (DR-11: max 32 predicates/tenant,
  `scope_ttl`/`recurrence_ttl` bounds, narrowing ladder) so a bug or injected content cannot force
  unbounded retention. F05 only originates event/incident data, never predicates.
- `GET /v1/*` endpoints listed in §4.3 are read-only and subject to `internal/auth` RBAC (X-SEC); no
  endpoint accepts write access to baselines or thresholds without admin scope (DR-25, DR-29).
- Deploy webhook ingestion (ArgoCD/Flux/GitHub) is authenticated (HMAC + principal-resolved, DR-25)
  before `model.DeployMarker` data is trusted as `DeployIndex` input.
- `model.ServiceMeta.Tier` exists for display and grouping only and has no code path into
  `api.AlertRouter` (DR-21 §21.1; verified by AC-F05-16's AST/route test).

## 7. Test strategy & acceptance criteria

**Unit tests**
- `P2Estimator`/`TDigest` `QuantileEstimator`: correctness against known distributions (uniform,
  log-normal, bimodal) within 1% error at p99; `P2Estimator` fixed at 240 B, zero allocation after
  construction; `TDigest` ≤ 512 B serialized.
- EWMA convergence: step-function input converges to new value within expected number of samples
  given `ewma_alpha`.
- Cold-start state machine: all four states (Cold, GlobalOnly, Warm, ProvisionalByDecree) individually
  triggered and asserted against their documented behavior, including the 0.69 `Score` cap.
- Each detector: boundary tests at exactly threshold, just below, just above; `debounce_ticks` logic.
- `new_error_signature`: correctness against `store.ErrorSignature` fixtures; rate benchmark at
  ≥ 5 000 rows/s/core.
- Grouper: O(1)-amortized topology-clustering correctness with synthetic graphs (linear chain, star,
  disconnected); dedupe/`SuppressedBy` correctness against `Fingerprint` collisions within `dedupe_ttl`.

**Integration tests**
- End-to-end: synthetic span stream with injected latency regression → `Event` → `Incident` → handed
  to a stub `rca.Engine` → verify `Incident.Status` transitions and that no page is emitted absent a
  DR-21 condition.
- Restart/checkpoint: kill detector mid-stream, restart, verify baseline continuity within the
  incremental-checkpoint tolerance.
- Load test: sustained 5 000 `model.REDSample`/sec/core for 10 min, assert p99 processing latency and
  the ≈120 MiB memory ceiling.

**Acceptance criteria**

| AC | Maps to | Criterion |
|---|---|---|
| AC-F05-1 | FR-F05-1..4 | Baseline quantile error ≤ 1% vs. exact computation on a held-out synthetic dataset (`{P50,P95,P99}`, no p90); cold-start state transitions verified against §4.4's table. |
| AC-F05-2 | FR-F05-5..9 | All 5 detectors plus the deploy-enrichment tag fire on injected fault scenarios from the Istio S01–S23 catalog subset, matching DR-14 §14.4's exact trigger/score formulas, with 0 false positives on the healthy-baseline control run. |
| AC-F05-3 | FR-F05-10 | Grouper merges 2 events on adjacent services (hop distance 1) within `grouping.window` (5m) into 1 incident; does not merge events on services with hop distance > `topology_hops`; a repeat `Fingerprint` within `dedupe_ttl` attaches with `SuppressedBy` set rather than opening a new incident. |
| AC-F05-4 | — | **Deleted (DR-21).** Previously tested the FR-F05-11 hard-page fast path, which is deleted; see AC-F05-16. |
| AC-F05-5 | FR-F05-12 | Benchmark sustains ≥ 5 000 `model.REDSample`/sec/core at `Res10s` (documented in `bench_test.go` header). |
| AC-F05-12 | FR-F05-4 | Each of the four cold-start states (Cold, GlobalOnly, ProvisionalByDecree, Warm) is independently reproduced by a fixture and asserted against its documented suppression/widening/capping behavior. |
| AC-F05-13 | FR-F05-13 | `throughput_drop` fires exactly at `obs.RPS <= base.RPSEWMA × 0.5` with `base.RPSEWMA >= min_rps`, and not below `min_rps`. |
| AC-F05-14 | FR-F05-14 | Benchmark: `Add` p99 ≤ 5 ms at 200 open incidents and 5 000 events/min, with ≤ 1 `Neighbors` call per newly-added service per incident and 0 per event. |
| AC-F05-15 | FR-F05-15 | Measured deviation-start → `anomaly_event` latency ≤ 75 s p95 against a synthetic fault-injection harness. |
| AC-F05-16 | DR-21 §21.1 | An AST/route test asserts no code path from `model.ServiceMeta.Tier` to `api.AlertRouter`. |

## 8. Open questions / risks

- **Decided (round 1), DR-14 §14.1.** P² vs. t-digest is no longer an either/or default: `P2Estimator`
  is the fixed per-season-slot estimator (constant 240 B, zero allocation), `TDigest` is used only for
  the global slot and `red_rollup_1h.digest`. There is no `--low-memory` mode switch to specify.
- **Decided (round 1), DR-21.** The hard-page severity ceiling (formerly FR-F05-11) is deleted outright,
  not tuned per-tenant/per-tier — `model.ServiceMeta.Tier` is never a paging input anywhere in the
  design (§21.1).
- **Decided (round 1), DR-14 §14.6.** The deploy-correlation detector and F06's private pre/post split
  are unified into the single `anomaly.DeployIndex` abstraction, owned by `internal/anomaly` and called
  by both packages (DR-2 permits `rca → anomaly`). Canary/progressive-delivery deploys
  (`model.DeployMarker.RolloutFraction`) remain an explicit Phase 3 deferral recorded in `00` — the
  field exists and is unused in v1.
