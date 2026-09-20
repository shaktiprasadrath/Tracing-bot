# F08 — Investigation & Runbook Memory

> Revision 3 — 2026-09-15 — applies DR-0, DR-2, DR-4, DR-5, DR-19, DR-29, DR-31, DR-32, DR-34, DR-38 (round-1 fixes)

## 1. Purpose

`internal/memory` is TraceIQ's learning flywheel. Every completed investigation is recorded under a
**tenant-bound** symptom fingerprint (DR-19 §19.1) — `memory.Fingerprint{TenantID, Tokens}`, hashed with
the tenant *inside* the preimage, so a cross-tenant fingerprint match is impossible by construction, not
merely by a `WHERE` clause someone might forget. `memory.Store.Record(ctx, tid, rec
model.InvestigationRecord)` takes a plain summary type, **not `rca.Investigation`** — this is the cycle
break that lets `memory` and `rca` both depend on `internal/llm` without depending on each other
(DR-2). Future incidents query `Similar(fingerprint)` at the RCA Contextualize step, through a
**bounded**, inverted-index-backed candidate generation step (≤ 500 candidates scored regardless of
corpus size, DR-19 §19.2) to surface past root causes and imported runbooks before a single new
hypothesis is formed. Engineer corrections are stored as first-class feedback with an exact weight
arithmetic (DR-19 §19.4) that boosts or demotes retrieval ranking. Similarity is pluggable
(`SimilarityScorer`: `lexical` default, `hashed`, or `anthropic`) so the whole system works fully
offline with no embedding-model dependency by default, while exposing a separate `Embedder` hook for
teams that want vector similarity. Everything is exportable to JSON and Markdown, byte-for-byte
reproducibly (DR-19 §19.6) — the customer owns the accumulated intelligence, unlike a commercial
platform's opaque, non-portable learning.

## 2. Compared-tool drawbacks addressed

| Drawback ID | Tool | Lagging feature | How TraceIQ fixes it (concrete mechanism in F08) |
|---|---|---|---|
| D-Y4 | Dynatrace | Analytical value (topology/RCA/Davis's learned knowledge) is not exportable — it stays locked in Grail. | `memory.Store.Export()` produces both full JSON and Markdown, byte-for-byte reproducible from the store under DR-19 §19.6's exact rules (sorted order, fixed timestamp format, no map iteration) — the customer's accumulated root-cause knowledge is a portable artifact, not a platform-trapped asset. |
| D-D5 | Datadog | Agentic AI (Bits AI SRE) is tied to the paid platform; learning captured by the vendor, not the customer, with documented silent eval regressions. | F08 stores every correction locally with the DR-19 §19.4 weight arithmetic (Confirmation +0.15/Correction −0.35, capped/floored, decayed at read time) that is fully inspectable and auditable — corrections improve *this* deployment's memory, not a vendor's shared model, and the effect of each correction on future retrieval is directly observable/testable (AC-F08-3) rather than opaque. |

## 3. Requirements

### 3.1 Functional

| ID | Statement |
|---|---|
| FR-F08-1 | `memory.Store.Record(ctx, tid, rec model.InvestigationRecord)` SHALL persist, for every completed investigation, a `Fingerprint{TenantID, Tokens: sorted/deduped/≤24, Hash: "fp1:"+hex(xxh3(tenantID‖"\x00"‖join(Tokens,"\x01")))}` (DR-19 §19.1) — computed by `memory.Compute`, which **delegates to the one implementation in `internal/anomaly`** (`anomaly.Fingerprint.Compute`, DR-14 §14.5; there is no second, independent fingerprint algorithm in `memory`) — plus the confirmed root-cause text, a reference to the full report, and the record's `Provenance`/`TrustTier`. |
| FR-F08-2 | `memory.Store.Similar(ctx, tid, fp, topK)` SHALL return `[]model.Record` via the bounded three-step algorithm of DR-19 §19.2: (1) union the posting lists for the query's ≤ 24 tokens through the `memory_fp_token` inverted index, rarest-first, stopping at `memory.retrieval.max_candidates` (500); (2) score exactly those ≤ 500 candidates with the configured `SimilarityScorer`; (3) return the top `top_k` above `min_similarity`. The FTS index remains available for free-text search over root-cause prose but is **not** the fingerprint-similarity prefilter. |
| FR-F08-3 | The `SimilarityScorer` and `Embedder` interfaces SHALL both exist as separate components (DR-19 §19.3) — the `Embedder` produces vectors, the `Scorer` ranks — so substituting a vector-similarity path (`hashed` or `anthropic`, the latter sharing `internal/llm.Client` with `rca`, DR-34 §34.3) never changes the `Store.Similar` call contract or any caller. |
| FR-F08-4 | *(Rewritten, DR-19 §19.4.)* `memory.Store.Correct(ctx, tid, c model.Correction)` SHALL apply exactly: **Confirmation** — `Weight += 0.15`, `Confirmations++`, capped at 2.0; **Correction** — `Weight -= 0.35` (floor 0.0), `Corrections++`, **and** a new record inserted with `Kind = Correction`, `Weight = 1.0`, `SupersedesID = <original>`, carrying the corrected conclusion. **Decay** — `effectiveWeight = Weight × 0.5^(age(LastUsedAt)/90d)`, applied at **read** time only, never written. **Retrieval ordering** — a superseded record is returned only together with its superseder, and always ranked below it. The prior "+0.2 additive, capped at 1.0" arithmetic is deleted. |
| FR-F08-5 | Runbook import SHALL parse Markdown files with a front-matter block (`service`, `symptom_tags`, `title`) via `RunbookImporter.Import`, indexing them into the same fingerprint-similarity space so `Similar()` returns runbooks interleaved with past investigations, tagged `Kind: Runbook` (renamed from `Source: "runbook"`, DR-38 §38.1's `Kind` addition). |
| FR-F08-6 | `memory.Store.Export(ctx, tid, w, f)` SHALL support `f ∈ {json, markdown}` (full structured dump / one human-readable file per record) and SHALL be byte-for-byte reproducible given an unchanged store snapshot, per the exact rules of FR-F08-10. |
| FR-F08-7 | *(Rewritten, DR-19 §19.2.)* A nightly consolidation job SHALL merge records within the same **fingerprint family** (token Jaccard ≥ `memory.consolidation.dedupe_threshold`, 0.6) using **banded MinHash LSH** (128 permutations, 32 bands × 4 rows) over fingerprint tokens — candidate pairs only within a shared band bucket, **not** the prior naive O(n²) pairwise comparison — into a canonical entry with an `OccurrenceCount`, and SHALL prune entries older than `memory.consolidation.prune_after_days` (400, aligned to `store.retention.investigations`) except `Pinned` entries (imported runbooks are pinned by default). Published: at 100 000 records the job completes within `max_duration` (15 m) and examines ≤ 2% of pairs. |
| FR-F08-8 | `Similar()` SHALL return within p99 < 200 ms against a corpus of up to 100 000 stored records — a function of the constant `max_candidates` bound (FR-F08-2), not of corpus size *n*, backed by the `memory_fp_token` inverted index. |
| FR-F08-9 | *(New — DR-19 §19.5, Appendix C.)* Every `model.Record` SHALL carry `Provenance` (`ProvHumanCurated` \| `ProvLLMAuthored` \| `ProvImported`) and `TrustTier` (1 = human-curated, 2 = imported, 3 = llm-authored). Every `ProvLLMAuthored`/`ProvImported` record SHALL be wrapped by `rca.Sanitizer.Wrap(model.UntrustedMemory, …)` **on retrieval** with the same canary/escaping as telemetry (`01 §8.6(2)`'s scope widened from "telemetry" to "every string not authored by TraceIQ's own code", DR-37). A retrieved record may seed a `Hypothesis.Source = memory` but **never** be cited as `Evidence`, and **never** raise `Confidence` to or above `rca.confidence_threshold` without at least one same-investigation `ok` tool step (enforced in `rca.Validator`; also stated in F06 §4.4, DR-19 §19 Docs-to-change). |
| FR-F08-10 | *(New — DR-19 §19.6, Appendix C.)* Export reproducibility, binding: records sorted by `(Kind, Fingerprint.Hash, CreatedAt, ID)`; timestamps RFC 3339 in **UTC**, second precision; LF line endings; **no map iteration** anywhere in the output path (every map emitted through a sorted key slice); `effectiveWeight` is **not** exported (time-dependent) — `Weight`, `LastUsedAt`, `Confirmations`, `Corrections` are. The JSON envelope is `{schema: "traceiq.memory.v1", tenant, exported_at, count, records}`; `exported_at` is excluded from the byte-reproducibility comparison. Import is **admin-only**, stamps `ProvImported`, never overwrites an existing ID (a colliding ID becomes a new record), and rejects a bundle whose `schema` is unknown. |

### 3.2 Non-functional

| Category | Target |
|---|---|
| Storage | Embedded SQLite (`modernc.org/sqlite`); **no external vector database required by default** — `X-OPS`'s Postgres+pgvector box is deleted (DR-32 §32.1) so this claim, contradicted in round 0, is now uncontradicted across `01 §1`, `02`, and `05 §8`. |
| Growth control | Retention (`prune_after_days`, 400) + banded-MinHash-LSH consolidation (FR-F08-7) bound corpus growth without the O(n²) nightly-window risk; `Export()` streams rather than buffering the full corpus. |
| Offline capability | Default `SimilarityScorer` (`lexical`) and default `memory.embeddings.driver` (also flipped to `lexical`, DR-19 §19.3) have zero network dependency and zero model-download requirement, matching the single-binary deployment goal. |
| Determinism | Fingerprint construction is order-independent (tokens sorted before hashing) **and** tenant-scoped structurally (tenant inside the hash preimage, DR-19 §19.1) — semantically identical incidents from the same tenant always hash identically; two different tenants can never collide. |
| Scan bound | ≤ 500 candidates scored per `Similar()` call, independent of corpus size (DR-19 §19.2) — owning gate `01 §10`. |
| Memory footprint | `memory.index.max_vocabulary` (50 000 terms, LRU by document frequency) ≈ 2 MiB; posting lists on disk; per-tenant record-header cache (10 000 × ≈ 1.5 KiB) ≈ 15 MiB — together the **64 MiB** line in DR-9's RSS derivation (DR-19 §19.2). |
| Determinism (clock) | Correction decay (`age(LastUsedAt)`) and the nightly consolidation job's schedule both read `model.Clock` (DR-31) — no `time.Now`/`time.After` inside `internal/memory` outside the allowlisted exceptions, so `eval.VirtualClock` can drive memory deterministically. |

## 4. Design

### 4.1 Component diagram

```mermaid
flowchart TB
    RCA[rca.Engine] -->|Record(tid, model.InvestigationRecord)| MEM[memory.Store]
    RCA -->|Correct step, scoped to stepID| MEM
    CTX[RCA Contextualize step] -->|Similar(tid, fingerprint)| MEM

    subgraph MemPkg["internal/memory"]
        MEM --> FP["Fingerprint.Compute<br/>(delegates to anomaly.Fingerprint.Compute)"]
        MEM --> IDX[("memory_fp_token<br/>inverted index<br/>rarest-first, <=500 candidates")]
        MEM --> SCORER{SimilarityScorer}
        SCORER -->|default| LEX[lexical]
        SCORER -->|optional, vector| HASH[hashed]
        SCORER -->|optional, vector| ANTH["anthropic<br/>(shares llm.Client with rca — DR-2/DR-34)"]
        MEM --> EMB[Embedder<br/>separate from Scorer]
        MEM --> DB[(SQLite: memory_record<br/>+embedding BLOB, provenance, trust_tier<br/>+ memory_fts)]
        IMPORT[RunbookImporter] --> DB
        CONS["Nightly Consolidation<br/>banded MinHash LSH<br/>128 perms, 32x4 bands"] --> DB
        EXP[Exporter<br/>byte-reproducible] --> DB
    end

    RUNBOOKS[Markdown runbook files] --> IMPORT
    EXP --> JSONOUT[JSON export]
    EXP --> MDOUT[Markdown export]
    API["api.Server /v1/memory/export<br/>(admin-only import)"] --> EXP
    SAN["rca.Sanitizer<br/>wraps ProvLLMAuthored/ProvImported on retrieval"] -.->|DR-19 §19.5| MEM
```

### 4.2 Data model

`model.Record`, `model.InvestigationRecord`, and `model.Correction` are canonical in `01 §4.6` and are
**not** re-declared here (DR-0, DR-4). The prior locally-declared `memory.Record` and the local `Match`
result type are deleted — `Store.Similar`/`Store.Search` return `[]model.Record` directly, already
carrying `Score`. `01 §4.6` (via DR-19 and DR-38) is normative for: `TenantID` (first PK column, DR-5),
`Kind` (at least `Investigation`, `Runbook`, `Correction`, DR-19 §19.4, and `ServiceMeta`, DR-38 §38.1
— `GET /v1/services` sourced), `Weight`, `SupersedesID`, `Embedding`, `Confirmations`, `Corrections`,
`Provenance`, `TrustTier`, `LastUsedAt`, `CreatedAt`.

**`Fingerprint` stays local to `internal/memory`** (DR-19 §19.1 — this is the one type this package
still declares itself):

```go
package memory

type Fingerprint struct {
    TenantID model.TenantID  // ALWAYS present, ALWAYS first
    Tokens   []string        // sorted, deduped, <= 24 — closed classes: svc:, op:, kind:, errsig:, dep:, ns:
    Hash     string          // "fp1:" + hex(xxh3( tenantID ‖ "\x00" ‖ join(Tokens, "\x01") ))
}

// Compute delegates to the ONE implementation, in anomaly (DR-14 §14.5). There is no
// second, independently-maintained fingerprint algorithm in this package.
func Compute(tid model.TenantID, inc model.Incident) Fingerprint
```

**"Fingerprint family"** (previously an untestable term, CC-30) is now defined: two fingerprints are in
the same family when their token Jaccard ≥ `memory.consolidation.dedupe_threshold` (0.6).

`ExportFormat` (`json`/`markdown`) and `Runbook` (`Title`, `ServiceTags`, `SymptomTags`, `Body`,
`SourceFile`) remain local to `internal/memory` — DR-19 does not move them.

**SQLite (control.db) tables** (DR-6 §6.2, DR-19 §19 Docs-to-change; DDL owned by `01 §5.1` — cited by
name only, DR-0): `memory_record` (gains `embedding BLOB`, `provenance`, `trust_tier`, `tenant_id` as
first PK column); **`memory_fp_token`** (new — the inverted index backing FR-F08-2's bounded candidate
generation: `(tenant_id, token, record_id)` primary key, plus a maintained document-frequency counter
per token, and a secondary index by `(tenant_id, record_id)`); `memory_fts` (unchanged role — free-text
search, not the fingerprint prefilter). The prior standalone `memory_corrections`/`memory_runbooks`
tables collapse into `memory_record` rows distinguished by `Kind`.

### 4.3 Interfaces & APIs

**Canonical interfaces (DR-19 §19.3, replaces this section wholesale):**

```go
package memory

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

// New constructs a Store with an injected clock (DR-31): correction decay
// (age(LastUsedAt)) and the nightly consolidation job's schedule both read
// model.Clock rather than calling time.Now/time.After directly, so
// internal/archtest's time-ban check passes and eval.VirtualClock can drive
// memory deterministically.
func New(cfg Config, clock model.Clock, scorer SimilarityScorer, embedder Embedder, db store.DB) (Store, error)
```

`internal/archtest` fails the build on any exported method above whose second parameter is not
`ctx, model.TenantID` in that order (DR-5). `memory` imports `store`, `config`, `tenant`, and **`llm`**
— never `rca` (DR-2's cycle break; `Store.Record` takes `model.InvestigationRecord`, not
`rca.Investigation`).

**Embedding driver default flip (DR-19 §19.3).** `memory.embeddings.driver` gains `lexical`, which
becomes the **default** — no network dependency, no API key. `hashed` remains for the vector component
when `memory.retrieval.hybrid: true`. The `anthropic` scorer holds `internal/llm.Client`, the same
client `rca` holds — there is no separate `memory.AnthropicEmbedder`/`rca.AnthropicClient` pair to keep
in sync (DR-2, DR-34 §34.3).

**REST/MCP surface.** A **filtered view** of `01 §6.1`, headed "defined in `01 §6.1`" (DR-29 §29.1):

| Method | Path | Purpose | Role |
|---|---|---|---|
| GET | `/v1/memory/similar?fingerprint=...` | `Similar()` query | viewer |
| GET | `/v1/memory/search?text=...` | `Search()` query | viewer |
| POST | `/v1/memory/runbooks/import` | Markdown upload | admin |
| GET | `/v1/memory/export?format=json\|markdown` | Streamed export | viewer |
| POST | `/v1/memory/import` | Bundle re-import (FR-F08-10) | admin |
| POST · DELETE | `/v1/memory/{id}` | Correct / delete a record | operator (correct) / admin (delete) |

MCP: `traceiq_search_memory` (backing `memory.Similar`), owned by `01 §6.2`'s twelve-tool table
(DR-29 §29.2), role **viewer** — no MCP tool imports memory or mutates a tenant.

Config key **paths** (values owned exclusively by `01 §7`, DR-0 — this doc cites paths, never numbers;
see DR-19 §19.7 for the authoritative block): `memory.enabled`, `memory.embeddings.driver`, `.dim`,
`memory.retrieval.top_k`, `.min_similarity`, `.max_candidates`, `.hybrid`, `.weight_decay_half_life`,
`memory.index.max_vocabulary`, `memory.consolidation.interval`, `.hour_of_day`, `.prune_after_days`,
`.dedupe_threshold`, `.minhash_permutations`, `.minhash_bands`, `.max_duration`.

### 4.4 Algorithms / decision logic

**`Similar()` — bounded three-step algorithm (DR-19 §19.2, replaces the prior FTS5-prefilter design):**
```
function Similar(tid, fingerprint, topK):
    candidates = []
    for token in fingerprint.Tokens (rarest-first, ascending document frequency via memory_fp_token.df):
        candidates.union(postingList(tid, token))
        if len(candidates) >= memory.retrieval.max_candidates (500): break   // rarest-first keeps the SELECTIVE ones
    scored = SimilarityScorer.Score(ctx, tid, query, candidates)              // scores exactly these <=500
    results = [c for c in scored if c.effectiveScore >= min_similarity (0.35)]
    return sortDescending(results)[:topK]                                    // superseded records ranked below their superseder
```

**Correction application (DR-19 §19.4, exact arithmetic):**
```
function Correct(tid, c):
    switch c.Verdict:
        case confirmed:
            record.Weight = min(2.0, record.Weight + 0.15); record.Confirmations++
        case refuted:  // "Correction" in the table above
            record.Weight = max(0.0, record.Weight - 0.35); record.Corrections++
            newRecord = model.Record{Kind: Correction, Weight: 1.0, SupersedesID: record.ID, RootCause: c.Note, ...}
            persist(newRecord)
    persist(record)
    // effectiveWeight is NEVER written; it is computed at read time in Similar()/Search():
    //   effectiveWeight = record.Weight * 0.5 ** (age(record.LastUsedAt) / 90d)
    // Retrieval ordering: a superseded record is returned ONLY together with its superseder,
    // and always ranked below it.
```

**Consolidation — banded MinHash LSH (DR-19 §19.2, replaces the naive O(n²) pairwise design):**
```
function ConsolidationJob(now):
    for each record: minhashSig = minhash(record.Fingerprint.Tokens, permutations=128)
    bands = split(minhashSig, into=32, rowsPerBand=4)
    buckets = groupByBandBucketHash(allRecords(), bands)     // candidate pairs ONLY within a shared bucket
    for bucket in buckets where len(bucket) > 1:
        for pair in bucket where jaccard(pair) >= dedupe_threshold (0.6):   // confirm within the fingerprint family
            canonical = pair.mostRecent()
            canonical.OccurrenceCount += other.OccurrenceCount
            deleteAll({other})
    for record in allRecords():
        if not record.Pinned and now - record.CreatedAt > prune_after_days (400):
            delete(record)
    recomputeCorpusIDF()   // only used by the "lexical" SimilarityScorer's text-similarity component
    // Published: at 100,000 records, completes within max_duration (15m), examines <= 2% of pairs.
```

**Export (DR-19 §19.6, byte-reproducible):**
```
function Export(tid, w, f):
    records = allRecords(tid) sortedBy (Kind, Fingerprint.Hash, CreatedAt, ID)   // total order, never map iteration
    for record in records:
        emit record with: Weight, LastUsedAt, Confirmations, Corrections   // effectiveWeight EXCLUDED (time-dependent)
        timestamps rendered RFC3339, UTC, second precision; LF line endings only
    envelope = {schema: "traceiq.memory.v1", tenant: tid, exported_at: now(), count: len(records), records: records}
    // exported_at is excluded from any byte-reproducibility comparison; every other byte is deterministic
    write(w, f == json ? jsonEncode(envelope) : markdownPerRecord(records))

function Import(tid, r, f, by):   // ADMIN ONLY
    bundle = parse(r, f)
    if bundle.schema unknown: reject
    for record in bundle.records:
        record.Provenance = ProvImported
        if record.ID collides with an existing ID: assign a NEW ID (never overwrite)
        persist(record)
```

**Runbook import:**
```
function Import(tid, markdown, sourceFile):
    frontMatter, body = splitFrontMatter(markdown)  // YAML: service, symptom_tags, title
    validate required frontMatter fields; reject with error if missing
    body = secretScrubber.Scrub(body)
    fp = Compute(tid, syntheticIncidentFrom(frontMatter))   // via anomaly.Fingerprint.Compute (FR-F08-1)
    record = model.Record{Kind: Runbook, TenantID: tid, RootCause: body, Pinned: true, Provenance: ProvImported, ...}
    persist(record); index tokens into memory_fp_token; index text in memory_fts
    return record
```

### 4.5 Sequence diagram

```mermaid
sequenceDiagram
    participant RCA as rca.Engine
    participant MEM as memory.Store
    participant IDX as memory_fp_token (inverted index)
    participant SC as SimilarityScorer
    participant SAN as rca.Sanitizer
    participant ENG as Engineer (chat/UI)
    participant CONS as Consolidation Job

    Note over RCA,MEM: Contextualize step of a NEW investigation
    RCA->>MEM: Similar(tid, fingerprint)
    MEM->>IDX: union posting lists, rarest-first, stop at 500
    IDX-->>MEM: <=500 candidates
    MEM->>SC: Score(query, candidates)
    SC-->>MEM: scored []model.Record
    MEM-->>RCA: ranked []model.Record (Investigation/Runbook/Correction)
    RCA->>SAN: Wrap(UntrustedMemory, ...) for every ProvLLMAuthored/ProvImported record
    RCA->>RCA: seed hypothesis only — never cite as Evidence (FR-F08-9)

    Note over RCA,MEM: Investigation completes
    RCA->>MEM: Record(tid, model.InvestigationRecord)
    MEM->>IDX: index fingerprint tokens

    Note over ENG,MEM: Engineer reviews investigation, finds a wrong step
    ENG->>MEM: Correct(tid, model.Correction)
    MEM->>MEM: apply weight arithmetic; insert Kind=Correction record if refuted

    Note over CONS,MEM: Nightly
    CONS->>MEM: banded MinHash LSH — bucket, then confirm within family (Jaccard >= 0.6)
    CONS->>MEM: prune non-pinned records past prune_after_days (400)
    CONS->>MEM: recompute corpus IDF (lexical scorer only)
```

## 5. Failure modes & mitigations

| Failure | Detection | Mitigation |
|---|---|---|
| Fingerprint too coarse — unrelated incidents collide | High `Similar()` result count with low actual relevance | Error signatures and closed token classes (`errsig:`, `dep:`, `ns:`) add specificity beyond service/kind alone; engineer corrections progressively down-rank mismatched suggestions via the DR-19 §19.4 weight arithmetic. |
| Fingerprint too narrow — near-identical incidents never match | Repeated incidents produce no `Similar()` hits despite being the "same" issue | Error signatures are normalized before inclusion; the nightly banded-MinHash-LSH consolidation catches near-duplicates within the same fingerprint family (Jaccard ≥ 0.6) missed at write time. |
| Runbook drift — an imported runbook cites an outdated root cause/fix | Stale `Similar()` suggestion surfaced to a new investigation | Runbook front-matter SHOULD carry a `last_reviewed` field (recommended convention, not enforced); still an open question in §8. |
| SQLite write contention under concurrent investigations | Write latency spikes / lock timeouts | WAL mode, single control writer per DR-6 §6.2; `rca.max_concurrent_investigations: 2` (DR-17 §17.3) bounds concurrent write pressure structurally. |
| A retrieved memory record carries injected content (memory poisoning) | N/A — must be prevented, not detected after the fact | Every `ProvLLMAuthored`/`ProvImported` record is wrapped on retrieval by `rca.Sanitizer`; it can seed a hypothesis but is never `Evidence` and can never alone cross the confidence threshold (FR-F08-9, DR-19 §19.5). |
| Consolidation exceeding its nightly window at large corpus size | `max_duration` (15 m) breach at 100k+ records | The naive O(n²) pairwise design is deleted; banded MinHash LSH examines ≤ 2% of pairs by construction (FR-F08-7). |
| Corpus growth degrading `Similar()` latency past the 200 ms target | Latency metric on `Similar()` calls | `max_candidates` (500) is a hard structural bound, not a tuning target — `Similar()`'s cost is a function of a constant, never of corpus size *n* (DR-19 §19.2). |
| Export interrupted mid-stream (large corpus, client disconnect) | Partial file / broken pipe | `Export()` streams row-by-row to `io.Writer` in the fixed sort order; re-running `Export()` regenerates byte-identical output deterministically (FR-F08-10), so partial failures are safe to retry. |

## 6. Security considerations

- `Export()` and `Import()` endpoints are auth-gated (X-SEC RBAC); `Import()` (both the bundle re-import
  of FR-F08-10 and runbook import of FR-F08-5) is **admin-only** — exported content can include
  incident narratives that may reference internal service names, customer-impact descriptions, or
  sensitive log excerpts cited as evidence.
- Root-cause text persisted via `Record()` has already passed through F06/F07's shared secret scrubber
  before it reaches `Investigation.Report`; `RunbookImporter.Import` additionally scrubs runbook bodies
  at import time since they originate outside the RCA pipeline.
- **Provenance-gated trust (DR-19 §19.5).** Every `ProvLLMAuthored`/`ProvImported` record is wrapped by
  `rca.Sanitizer.Wrap(model.UntrustedMemory, …)` on retrieval — the same canary/escaping mechanism used
  for telemetry (`01 §8.6`, widened scope, DR-37). A retrieved record may seed a hypothesis but is
  never cited as `Evidence` and can never alone raise `Confidence` to `rca.confidence_threshold`.
- **Tenant isolation is structural, not a query clause.** The tenant is inside the `Fingerprint.Hash`
  preimage (DR-19 §19.1); `Similar()` cannot return a cross-tenant match by construction, and every
  `memory` table carries `tenant_id` as the first primary-key column (DR-5).
- `Correction.Author`/`Import`'s `by` parameter require an authenticated identity; correction records
  are append-only, giving an audit trail of who adjusted the memory's retrieval weighting and why.
- SQLite database file permissions restricted to the TraceIQ process user; no network listener is
  opened by the embedded store itself.

**STRIDE (`X-SEC §4.4`, DR-37 §37.3):** `memory` / F08 — Tampering (memory poisoning) → mitigated by
DR-19 §19.5.

## 7. Test strategy & acceptance criteria

**Unit tests**
- Fingerprint determinism: field-order permutation of the same logical incident produces an identical
  `Fingerprint.Hash`; two different tenants over the same tokens never collide.
- `Compute` delegation: `memory.Compute` produces byte-identical output to `anomaly.Fingerprint.Compute`
  for the same input (single-implementation invariant).
- Bounded candidate generation: posting-list union stops at exactly `max_candidates` (500); rarest-first
  ordering verified against a synthetic document-frequency distribution.
- Correction arithmetic: Confirmation (+0.15, cap 2.0) and Correction (−0.35, floor 0.0, new
  `Kind=Correction` record with `SupersedesID`) both verified at the boundary; decay applied only at
  read time, never persisted.
- Consolidation: banded MinHash LSH bucket assignment correctness against known permutation seeds;
  pair-examination-rate assertion (≤ 2% at a seeded 100k-record corpus).
- Runbook import: valid front-matter parses correctly; missing required fields rejected; secret
  scrubbing applied to body; `Kind = Runbook`, `Pinned = true`.
- Export round-trip: `Record()` N synthetic records → `Export(json)` → parse → assert full fidelity,
  sort order, RFC3339/UTC/LF compliance, `effectiveWeight` absence; same for `Export(markdown)`.
- Import: admin-only enforcement; ID collision produces a new record, never an overwrite; unknown
  `schema` rejected.

**Integration tests**
- Contextualize-step integration: seed memory with known past investigations, run `Similar()` from a
  live `rca.Engine` contextualize call, assert relevant matches surface above threshold-irrelevant ones
  and that `ProvLLMAuthored`/`ProvImported` records arrive wrapped.
- Consolidation job: seed near-duplicate fingerprints within a family (Jaccard ≥ 0.6), run job, assert
  merge into one canonical record with correct `OccurrenceCount` within `max_duration`; seed a stale
  non-pinned record past `prune_after_days`, assert pruned; seed a pinned runbook past retention,
  assert retained.
- Load test: 100,000-row corpus, measure `Similar()` p99 latency **and** the candidate count actually
  scored, against the 200 ms / 500-candidate targets simultaneously.

**Acceptance criteria**

| AC | Maps to | Criterion |
|---|---|---|
| AC-F08-1 | FR-F08-1, FR-F08-9 | Every `Record()` call persists a fingerprint whose hash is stable under input-list reordering and distinct per tenant; the persisted record carries the correct `Provenance`/`TrustTier` for its origin (RCA-produced = `ProvLLMAuthored` when authored by the `llm` reasoner, imported = `ProvImported`, engineer-curated = `ProvHumanCurated`). |
| AC-F08-2 | FR-F08-2, FR-F08-3 | `Similar()` produces correct rankings against a hand-verified fixture corpus using the default `lexical` scorer with zero network calls and exactly the bounded three-step algorithm (candidate generation → score → select); swapping in a stub `SimilarityScorer`/`Embedder` requires no `Store` interface change. |
| AC-F08-3 | FR-F08-4 | *(Rewritten, DR-19 §19.4.)* After a correction, `Similar()` on the same fingerprint ranks the **Correction** record above the superseded one, and the superseded record's weight is strictly lower than before. |
| AC-F08-4 | FR-F08-5 | Imported runbooks appear in `Similar()` results tagged `Kind: Runbook` and are excluded from auto-pruning regardless of age. |
| AC-F08-5 | FR-F08-6, FR-F08-10 | `Export(json)` and `Export(markdown)` on an unchanged store snapshot produce byte-identical output (excluding `exported_at`) across two consecutive runs, matching the sort order, timestamp format, and `effectiveWeight`-exclusion rules of FR-F08-10. |
| AC-F08-6 | FR-F08-7 | Consolidation job merges seeded near-duplicates within a fingerprint family and prunes seeded stale non-pinned records in a single run within `max_duration`, examining ≤ 2% of pairs, verified by row-count and pair-examination assertions before/after. |
| AC-F08-7 | FR-F08-8, FR-F08-2 | *(Extended, DR-19 §19.2.)* `Similar()` asserts **both** p99 latency < 200 ms **and** a ≤ 500 candidate-scored count at a 100,000-row seeded corpus — a p99 that passes with an unbounded candidate count does not satisfy this AC. |

## 8. Open questions / risks

- Whether runbook staleness (`last_reviewed` age) should automatically decay a runbook's `Similar()`
  score is unresolved — currently pinned runbooks never lose relevance purely from age, which risks
  surfacing outdated guidance; not addressed by round 1, still a config-gated decay function candidate
  for a follow-up iteration.
- The `Embedder` interface (FR-F08-3) is specified as a seam only — no default embedding implementation
  ships beyond the `lexical`/`hashed` scorers; a team enabling `anthropic` now shares `internal/llm.Client`
  with `rca` (DR-34 §34.3) rather than bringing a wholly separate client, but still supplies its own
  vector backend if it wants one beyond `hashed`.
- **Decided (round 1), DR-19 §19.1 + DR-5.** Cross-tenant memory isolation, open in round 0, is now
  structural: the tenant is inside the `Fingerprint.Hash` preimage and every table carries `tenant_id`
  as the first primary-key column — there is no remaining reconciliation item with X-OPS's multi-tenancy
  design.
- **Decided (round 1), DR-19 §19.2.** Consolidation's O(n²) naive pairwise clustering, flagged as an
  unresolved implementation risk in round 0, is replaced by banded MinHash LSH with a published pair-
  examination bound (≤ 2% at 100k records) and a completion-time gate (15 m).
