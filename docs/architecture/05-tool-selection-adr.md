# ADR-05 — Implementation language, frameworks, and core dependency set

> Revision 2 — 2026-09-15 — applies DR-0, DR-1, DR-6, DR-9, DR-24, DR-31, DR-32, DR-34 (round-1 fixes)

| | |
|---|---|
| **Status** | Accepted (with one blocking follow-up, FU-1) |
| **Date** | 2026-09-14 |
| **Deciders** | TraceIQ architecture board |
| **Supersedes** | The indicative "Brain: Python/Go services" row in `Tracing-Bot-PRD.md` § Technology stack summary |
| **Scope** | `Tracing-Bot-PRD.md` § Detailed Low Level Architecture (tiers 1–8); `docs/architecture/00-feature-catalog.md` F01–F12, X-SEC, X-OPS |
| **Evidence date** | All versions, advisories and build results in this ADR were verified against the Go module proxy, the Go vulnerability database (OSV + `govulncheck`), and a real compile on the build machine on 2026-09-14 |

---

## 1. Context

TraceIQ is a single deliverable that must behave as two very different kinds of program at once:

1. **A telemetry data plane.** OTLP gRPC (4317) and OTLP HTTP (4318) receivers, per-trace assembly in a consistent-hash ring, tail-sampling decisions within 30 s of trace completion, RED-metric extraction, Parquet block writing, and a hot index — all under sustained concurrent ingest, with backpressure that degrades gracefully instead of OOM-ing. This is the F01/F02/F03 path.
2. **An agentic reasoning control plane.** An LLM hypothesis→test→validate loop issuing structured tool calls against the stores, plus memory, remediation guards, NL interfaces and a web UI. This is the F06/F08/F09/F10/F12 path.

The product owner's requirement is explicit and is the primary driver:

> "the language should be capable enough to handle the security, processing speed etc **natively**."

"Natively" is the operative word. It rules out architectures where TLS, memory safety, or parallelism are delegated to a C library, a JIT warm-up, or an external process supervisor. It pushes toward a language whose standard library *is* the security and concurrency substrate.

Three hard deployment constraints follow from the PRD's § 8 Deployment Model and the feature catalog's X-OPS row:

- **C1 — Single static binary.** Must run on an engineer's laptop with no runtime installed (`./traceiq` and it works, embedded hot index, local disk), and the same artifact must run in Kubernetes.
- **C2 — Native TLS/mTLS, memory safety, high-throughput concurrent ingest, structured concurrency with backpressure.**
- **C3 — Toolchain available on the build machine** (verified by direct invocation on 2026-09-14):

  | Tool | Version present | Assessment |
  |---|---|---|
  | Go | `go1.23.1 windows/amd64` | Usable; **but see § 7.1 — badly outdated even within its own line** |
  | Node | `v22.15.0` | Usable |
  | Python | `3.11.5` | Usable, but **not** the 3.12 named in the brief |
  | Docker | `29.1.3` | Usable |
  | Rust | `rustc 1.47.0 (2020-10-07)` / `cargo 1.47.0` | **Disqualifying** — see § 4.2 |
  | Java | `11.0.12 LTS` | **Disqualifying for a Java 21 design** — see § 4.3 |

A fourth constraint is structural rather than technical: the feature catalog already fixes Go package paths (`internal/ingest`, `internal/sampler`, `store/sqlite`, …) and Go interface signatures (`ingest.Receiver`, `sampler.Sampler`, `store.Store`, `rca.Engine`, …). This ADR does **not** treat that as settling the question — an ADR that rubber-stamps a prior assumption is worthless — but it does mean a non-Go decision carries a documented rewrite cost for the entire architecture doc set.

---

## 2. Decision drivers (weighted)

Weights sum to 100. They were fixed **before** scoring, and derive from the product owner's requirement (security and native processing speed dominate) plus the PRD's deployment model.

| # | Driver | Weight | What it actually measures |
|---|---|---:|---|
| D-A | **Security** | 20 | Memory safety by construction; TLS/mTLS in the standard library rather than a C dependency; size and auditability of the transitive dependency graph; availability of first-party vulnerability tooling; patch cadence |
| D-B | **Ecosystem fit for OTel / tracing** | 18 | Maturity of the OTel SDK and OTLP wire types; whether the reference implementations in this exact problem space are written in the language; availability of Parquet, ClickHouse and Kafka clients that do not require C |
| D-C | **Concurrency & backpressure** | 15 | Cheap concurrency primitives for per-trace fan-in; first-class cancellation propagation; bounded worker pools and semaphores; structured concurrency that makes backpressure expressible rather than bolted on |
| D-D | **Toolchain availability (C3)** | 15 | Can the team compile, test and release *today* on the build machine, without procuring a new toolchain first |
| D-E | **Performance** | 12 | Sustained span throughput, tail latency of the sampling decision, memory footprint per buffered trace, cold-start time (matters for a laptop-mode binary and for K8s pod churn) |
| D-F | **Single-binary ops (C1)** | 12 | Static linking with no libc; cross-compilation without a cross C toolchain; embedded assets; distroless/scratch container viability |
| D-G | **Team velocity** | 8 | Time to a working F01–F05 slice; readability for an SRE-adjacent audience; test tooling quality |

---

## 3. Options considered

### 3.1 Go 1.23

**Ecosystem fit (D-B) — decisive.** Every reference implementation in TraceIQ's problem space is Go: the OpenTelemetry Collector, Jaeger v2 (which *is* an OTel Collector distribution), Grafana Tempo, and Prometheus. The OTel Go SDK reports **Traces: Stable, Metrics: Stable, Logs: Release candidate** on the OpenTelemetry status page as of this ADR's date — traces and metrics, the two signals TraceIQ's data plane depends on, are stable. `go.opentelemetry.io/proto/otlp` gives the OTLP wire types directly, so F01 can accept OTLP without a translation layer, satisfying D-D3 ("lossy OTel semantic-convention translation") by construction.

**Security (D-A).** `crypto/tls` is pure Go — no OpenSSL, no libssl CVE surface, no dynamic linking. mTLS is `tls.Config{ClientAuth: tls.RequireAndVerifyClientCert}`; no third-party dependency. Memory-safe (GC), with the caveat that data races are possible but detectable — the race detector is first-party and runs in CI (§ 6, D-11). `govulncheck` performs **symbol-level reachability analysis**, not naive version matching, which materially reduces false-positive triage cost. Counterweight: see § 7.1 — the *available* Go toolchain is EOL, and this is the single largest risk this ADR carries.

**Concurrency (D-C).** Goroutines make the per-trace fan-in in F02 natural: one goroutine per shard, channels as the bounded queue. Backpressure is expressible with the standard library plus `golang.org/x/sync`: `errgroup.Group.SetLimit(n)` bounds concurrent hypothesis evaluation in F06 and propagates the first error with cancellation; `semaphore.Weighted` bounds memory-weighted work such as Parquet block flushes; `context.Context` gives uniform deadline and cancellation propagation across the whole request tree, which is exactly what the PRD's "per-investigation token/time/query caps" requires. A bounded channel *is* the backpressure signal — a full channel blocks the producer, which is the correct behaviour for an ingest tier that must shed load rather than buffer without limit.

**Single-binary ops (D-F) — verified empirically, not assumed.** See § 5.

**Performance (D-E).** AOT-compiled, no JIT warm-up, sub-10 ms process start. GC pauses are sub-millisecond at TraceIQ's heap sizes. Slower than Rust on raw compute, which matters for Parquet encode and zstd but is not the bottleneck — object-storage I/O is.

**Velocity (D-G).** Strongest of the five. Small language, one formatter, `go test` in the box, and the existing architecture docs already speak Go.

### 3.2 Rust

On merit, Rust is the only candidate that beats Go on D-A (ownership eliminates data races at compile time, not just detects them) and D-E. `rustls` is a memory-safe TLS stack. This option was taken seriously.

It fails on two independent grounds, either of which alone is disqualifying:

- **D-D, hard fail.** The build machine has `rustc 1.47.0`, released **2020-10-07**. Tokio — unavoidable for an async network service — requires **Rust ≥ 1.71**. The gap is roughly three years of language evolution (`async fn` in traits, let-else, GATs, the 2021/2024 editions). Nothing in the modern async ecosystem compiles. *Note for reviewers: do not confuse `rustc 1.47` with tokio's own `1.47.x` LTS crate line — the version-number collision is coincidental and has misled this evaluation once already.*
- **D-B, soft fail.** The OTel Rust SDK is **Beta for traces, metrics, and logs** on the OpenTelemetry status page. TraceIQ's entire value proposition rests on OTLP fidelity; building the data plane on a beta SDK inverts the risk profile. There is also no mature pure-Rust Parquet writer with the same production mileage as the Go/Java implementations, and no Rust reference implementation in this problem space to borrow operational patterns from.

Rust would be the right answer for a rewrite of the sampler hot loop in 2028 if profiling demands it. It is not the right answer for the whole system in 2026 on this machine.

### 3.3 Java 21 / Kotlin (JVM)

The JVM is genuinely strong here and the brief is right to list it. OTel Java is the **most mature SDK of any language — Traces, Metrics and Logs all Stable** — and the OTel Java agent's zero-code instrumentation is best-in-class. Java 21's virtual threads and `StructuredTaskScope` are, on paper, the best structured-concurrency-with-backpressure story of any candidate, and Parquet's reference implementation is Java.

It fails on:

- **D-D, hard fail.** The build machine has **Java 11.0.12**. Virtual threads (JEP 444, Java 21) and structured concurrency (preview from 21) — the two features that make the JVM attractive for this workload — simply do not exist in 11. Evaluating "Java 21" on this machine means evaluating a toolchain that must first be procured. Java 11 without virtual threads is a platform-thread-per-connection model, which is the wrong shape for high-fan-in trace assembly.
- **D-F, structural fail.** "Single static binary runnable on a laptop" is not the JVM's native shape. `jlink` produces a runtime image (a directory tree, tens of MB) and GraalVM `native-image` produces a real static binary but imposes a closed-world assumption that fights reflection-heavy libraries, adds minutes to every build, and requires its own toolchain — which is also not on the machine.
- **D-E, partial.** JIT warm-up conflicts with laptop-mode start and with K8s pod churn; heap footprint per buffered trace is materially higher than Go's.

### 3.4 Python 3.12

- **D-D, fails as specified.** The machine has **Python 3.11.5**, not 3.12. Minor, but the brief's option is literally unavailable.
- **D-C / D-E, hard fail — and this is the real reason.** The GIL makes a single Python process incapable of the concurrent ingest in C2. Free-threaded CPython (PEP 703) exists but is not a basis on which to build a data plane in 2026, and 3.11 does not offer it at all. The workaround — multiprocessing plus a shared buffer — reintroduces exactly the cross-process trace-assembly problem that the PRD identifies as what makes tail sampling "operationally tricky" for everyone else. `asyncio` gives concurrency but not parallelism.
- **D-F, hard fail.** PyInstaller-style bundles are not static binaries; they are archives that unpack a interpreter at runtime. They are not a distroless/scratch container story.
- **D-A, partial.** `ssl` wraps OpenSSL — a C dependency, contradicting "natively".
- **D-B / D-G, strong.** OTel Python is Stable for traces and metrics, and Python is the fastest language here for the *agent* half of the system.

This shape — strong for the brain, unusable for the data plane — is exactly what the PRD's provisional "Python/Go services" row anticipated. § 6, D-2 addresses it directly.

### 3.5 TypeScript / Node 22

- **D-D, passes.** Node 22.15 is present.
- **D-C / D-E, hard fail.** A single-threaded event loop cannot saturate multiple cores for span decode and Parquet encode. `worker_threads` helps but turns shared trace-assembly state into a message-passing problem with structured-clone costs on the hot path. There is no `errgroup`/`SetLimit` equivalent in the standard library; backpressure must be hand-rolled around streams.
- **D-F, partial fail.** Node's Single Executable Application support has improved but still produces a large binary that embeds the full runtime, and it is not the ecosystem's well-trodden path.
- **D-A, partial.** TLS via OpenSSL (C), plus the largest and least-auditable transitive dependency surface of any candidate — a poor fit for a security-forward brief.
- **D-B / D-G, good.** OTel JS is Stable for traces and metrics; velocity is high.

Node earns a real role in this architecture, but at build time only — see § 6, D-10.

---

## 4. Scoring matrix

Scores 1–5 (5 best), multiplied by the § 2 weights. Total shown as weighted score out of 500 and normalised out of 5.

| Driver (weight) | Go 1.23 | Rust | Java 21 / Kotlin | Python 3.12 | TS / Node 22 |
|---|:--:|:--:|:--:|:--:|:--:|
| D-A Security (20) | 4 | **5** | 4 | 3 | 3 |
| D-B OTel/tracing ecosystem (18) | **5** | 2 | 4 | 4 | 4 |
| D-C Concurrency & backpressure (15) | **5** | 4 | 4 | 2 | 2 |
| D-D Toolchain availability (15) | **5** | 1 | 1 | 2 | **5** |
| D-E Performance (12) | 4 | **5** | 4 | 1 | 2 |
| D-F Single-binary ops (12) | **5** | 4 | 2 | 1 | 2 |
| D-G Team velocity (8) | **5** | 2 | 4 | **5** | **5** |
| **Weighted total (/500)** | **468** | 335 | 331 | 256 | 325 |
| **Normalised (/5)** | **4.68** | 3.35 | 3.31 | 2.56 | 3.25 |

Sensitivity check: Go still wins if D-D (toolchain availability) is zeroed out entirely — Go 393/425 vs Rust 320/425 vs Java 316/425. The decision is therefore **not** an artefact of the build machine's contents; it survives the counterfactual where the team may procure any toolchain it likes. Rust's and Java's deficits are in ecosystem fit and single-binary ops respectively, which no amount of toolchain procurement fixes.

---

## 5. Verification: the decision was compiled, not asserted

Before accepting Go, the full candidate dependency set was resolved and built on the actual build machine under the actual constraints. This is reproducible.

```
GOTOOLCHAIN=local   # forbids Go from silently downloading a newer toolchain
CGO_ENABLED=0       # forbids any C dependency
go build -trimpath -ldflags="-s -w"
```

A probe program importing `go.opentelemetry.io/proto/otlp/{trace,collector/trace}/v1`, `google.golang.org/grpc` + `credentials` (TLS), `google.golang.org/protobuf`, `modernc.org/sqlite`, `github.com/parquet-go/parquet-go`, `github.com/klauspost/compress/zstd`, `golang.org/x/sync/errgroup`, `golang.org/x/time/rate`, and `net/http`'s Go 1.22+ method-pattern mux was compiled with the § 8 pin set.

| Result | Value |
|---|---|
| Resolution under `GOTOOLCHAIN=local` on go1.23.1 | **Succeeded** — no toolchain switch, entire graph declares `go 1.23.0` or lower |
| Total modules in graph (`go list -m all`) | **73** |
| Build `windows/amd64`, `CGO_ENABLED=0` | **Succeeded** |
| Cross-compile `linux/amd64` | **Succeeded**, 16.6 MB stripped |
| Cross-compile `linux/arm64` | **Succeeded**, 15.9 MB stripped |
| Cross-compile `darwin/arm64` | **Succeeded**, 16.3 MB stripped |
| Cross C toolchain required | **None** |

Cross-compiling a Linux arm64 static binary *from a Windows laptop with no cross toolchain installed* is the concrete demonstration of D-F that no other candidate can match.

**One finding worth recording.** `go.opentelemetry.io/proto/otlp/collector/trace/v1` ships generated grpc-gateway stubs (`trace_service.pb.gw.go`) in the same package as the service types, so importing the OTLP collector service pulls `github.com/grpc-ecosystem/grpc-gateway/v2` into the graph as a required indirect dependency. The build fails without it. It is pinned in § 8 accordingly. This was not predicted from the go.mod graph and only surfaced on a real compile.

---

## 6. Decision

### D-1 — Implementation language: **Go**, one language for the whole system

TraceIQ is written in Go. There is no second implementation language in the runtime artefact. The data plane and the brain share one process, one type system (`model.Span` is the same struct everywhere), one build, and one binary.

Rationale: Go wins the weighted matrix by a wide margin (4.68 vs 3.35 next-best) and wins the toolchain-independent counterfactual too. It is the only candidate that satisfies C1, C2 and C3 simultaneously. On the product owner's "natively" test it scores strongest of any candidate that is actually buildable: TLS is standard-library pure Go with no C dependency; memory safety is by construction; concurrency and cancellation are language-level, not library-level.

### D-2 — Explicitly rejecting the PRD's "Python/Go services" split for the brain

The PRD's technology-stack summary proposed "Python/Go services" for the intelligence layer. **Rejected.** The RCA engine (F06) is Go.

Rationale: the loop's actual work is issuing structured tool calls and marshalling their results — HTTP, JSON, and query execution against stores that live in the same process. Python's advantage (the ML/data library ecosystem) is not exercised, because per the PRD's own model strategy "the model never receives raw span dumps, only query results". Splitting languages would force the RCA engine to reach its evidence over an IPC boundary rather than a function call, would break C1 (single binary), and would double the security surface for zero capability gain. The cost is that contributors who know Python but not Go cannot work on the brain; accepted.

### D-3 — Hot index: **embedded pure-Go SQLite (`modernc.org/sqlite`)** as default; **ClickHouse pluggable** for scale-out; **DuckDB rejected**

| Candidate | Verdict | Evidence |
|---|---|---|
| `modernc.org/sqlite` (version pinned in `§ 8.2`) | **Default** — `store/sqlite` | Pure Go (SQLite transpiled to Go), **no CGO**, compiled and cross-compiled cleanly in § 5. The only option that satisfies C1. |
| ClickHouse (`clickhouse-go/v2`, version pinned in `§ 8.2`) | **Pluggable backend**, Phase 2 [P2] | Validated by Jaeger v2.18's ClickHouse move (8.6× compression on 10M spans, cited in the PRD). Pure-Go client. |
| DuckDB | **Rejected** | `github.com/marcboeker/go-duckdb/v2` is **deprecated** (moved to `github.com/duckdb/duckdb-go`), declares `go 1.24`, and binds prebuilt per-platform native libraries (`duckdb-go-bindings/{linux,darwin,windows}-{amd64,arm64}`) **via CGO**. That breaks `CGO_ENABLED=0`, breaks toolchain-free cross-compilation, and forces a glibc-linked container base. Disqualified on C1 alone. |

Explicitly **not** rejecting `mattn/go-sqlite3` on quality — it is the faster binding — but it requires CGO and therefore fails C1. The pure-Go driver is measurably slower than the cgo binding on write-heavy workloads.

**Rationale corrected (DR-6 §6.1/§6.2):** the hot index **does** perform a per-kept-span write — one `span` row, up to 3 `attr_index` rows, and (for anomalous traces) one `span_text_fts` row per kept span — it is the *transaction*, not the row count, that is batched: `HotIndex.WriteBatch` commits exactly one batch call per flush, never one transaction per row. Two SQLite files with two dedicated single-writer goroutines (`traceiq.db` for telemetry, `control.db` for the audit/control state) keep a fail-closed audit append off the telemetry writer's queue. The published, benchmarked writer budget (`01 §5.1`, DR-6 §6.2), gated by FU-3, is **≥ 2 000 committed rows/s, ≥ 20 tx/s, p99 commit ≤ 120 ms** for the telemetry writer, and **≥ 200 tx/s, p99 audit append ≤ 15 ms independent of the telemetry batch interval** for the control writer. Design consequence, binding on `internal/store/sqlite`: a **single writer goroutine per file**, WAL journal mode, `busy_timeout` set, and per-span rows committed in batched transactions on a timer — never one transaction per span. An acceptance benchmark gates this rather than an assumed number: see § 10, SEC/PERF gate, and FU-3.

### D-4 — Cold store: **Parquet** via `github.com/parquet-go/parquet-go`, zstd via `klauspost/compress`

Confirms the PRD. Version pinned in `§ 8.2`. Apache-2.0 + MIT, pure Go, no CGO, originally developed at Twilio Segment and now community-maintained. Alternative `apache/arrow-go` parquet was rejected as a much heavier graph for no gain — TraceIQ writes its own row layout and does not need Arrow in-memory representation on the write path. The open-format requirement (D-D3, D-Y4: "exportable, Athena/DuckDB-queryable") is a property of the *file format*, not of the writer, so rejecting the DuckDB Go driver in D-3 does not compromise it.

### D-5 — Buffering: **in-process bounded ring + disk WAL spill** by default; **Kafka/Redpanda only at scale-out**

The PRD names Kafka/Redpanda as the buffering substrate. That is correct at scale and wrong as a default — requiring a Kafka cluster to run `./traceiq` on a laptop directly contradicts C1 and the "same single-binary onboarding simplicity as Zipkin" claim the PRD makes against Zipkin.

Decision: one interface, two implementations.

- **`buffer/ring` (default).** Bounded in-process ring per sampler shard, with spill-to-disk WAL so a restart does not lose in-flight traces (this is what closes D-Z3, "volatile in-memory default storage"). A full ring is the backpressure signal: it blocks the receiver, which sheds load deterministically rather than growing the heap without bound. **Precise guarantee (DR-9 §9, replaces the earlier unquantified claim):** on `Consume`, raw span bytes append to the shard's WAL segment with a group fsync every `sampler.wal.flush_interval` (**1s**, `sampler.wal.dir`, `sampler.wal.max_segment_bytes` 64 MiB); on start, `sampler.ReplayWAL` re-assembles every undecided trace before receivers bind. **On graceful shutdown, in-flight trace loss is 0. On crash, loss is bounded by one WAL flush interval (default 1s) and is counted exactly in `traceiq_sampler_traces_lost_total`** (FR-F02-9, rewritten).
- **`buffer/kafka` (scale-out, Phase 2 [P2]).** `github.com/twmb/franz-go` (version pinned in `§ 8.2`) — pure Go, no CGO, BSD-3-Clause; chosen over `confluent-kafka-go`, which wraps librdkafka via CGO and fails C1.

**Switch to Kafka when any of:** (a) the sampler must be horizontally sharded across more than one node, so trace assembly needs a durable shared partition keyed by trace-ID; (b) replay across restarts or reprocessing of historical spans is required; (c) ingest exceeds what one node's ring can hold within the PRD's 30 s decision budget. Below all three, the ring is strictly better: fewer moving parts, no network hop, no cluster to operate.

### D-6 — HTTP: **`net/http` standard library only.** No chi, no gin, no echo

Go 1.22+ `http.ServeMux` supports method and wildcard patterns (`mux.HandleFunc("POST /v1/traces", …)`, `"GET /api/investigations/{id}"`), which covers every route in F12. This was verified to compile in § 5.

Rationale: routing is the *only* thing chi would add, and the standard library now does it. gin is additionally rejected on D-A grounds — reflection-heavy binding and a larger attack surface. Zero HTTP-framework dependencies means zero HTTP-framework CVEs, which is the cheapest possible win against the product owner's security requirement. Middleware (auth, request-ID, recovery, rate-limiting via `golang.org/x/time/rate`) is written as plain `func(http.Handler) http.Handler` — roughly 100 lines, fully auditable, no dependency.

Revisit only if routing needs regex constraints or per-route middleware trees that the standard mux genuinely cannot express. It currently can.

### D-7 — gRPC: **`google.golang.org/grpc`** for the OTLP/gRPC receiver

Required — OTLP/gRPC on 4317 is a first-class ingest path (F01) and there is no realistic alternative. Version pinned in `§ 8.2`. TLS/mTLS via `credentials.NewTLS(*tls.Config)`, i.e. the standard-library TLS stack, not a bundled one.

**The Set-A pin of this dependency carried a known reachable advisory (GO-2026-6061); that risk is resolved under the Set B pin (`§ 8.2`) — see § 7.1's historical record and § 12's verified landing.**

### D-8 — Protobuf: **`google.golang.org/protobuf`** + **`go.opentelemetry.io/proto/otlp`**

OTLP wire types are consumed directly rather than regenerated, so OTel semantic conventions are preserved verbatim — this is the mechanism by which F01 addresses D-D3. Both modules' versions are pinned in `§ 8.2`.

### D-9 — LLM client: **direct HTTPS to the Anthropic Messages API. No vendor SDK**

**Normative (DR-34 §34.1) — this section, not `01 §7`, is the source of truth for the model
choice**; `01 §7`'s prior `rca.llm.model: claude-sonnet-4-5` was a transcription error against
this ADR and is corrected to match it. The client now lives in `internal/llm` (DR-2), shared
by `rca` and `memory` so neither imports the other, but the decision recorded here is unchanged.

The RCA engine talks to the model over `net/http` + `encoding/json` against `POST https://api.anthropic.com/v1/messages`, with headers `x-api-key`, `anthropic-version: 2023-06-01`, `content-type: application/json`. Default model **`claude-opus-5`**. Confined to one package, `internal/llm` (`internal/rca/llm/anthropic` is renamed per DR-2's new package), behind the `rca.Reasoner` interface, so the deterministic rule-based fallback reasoner (F06) and a local-model backend (the PRD's air-gapped option) are drop-in. `temperature` is **not sent** — it is incompatible with adaptive thinking — so `01 §7`'s `temperature: 0` is deleted and replaced by `rca.llm.effort` (DR-34 §34.1).

Rationale — three independent reasons, in order of weight:

1. **Supply chain (D-A).** `github.com/anthropics/anthropic-sdk-go` pulls `aws-sdk-go-v2` (+ config, credentials, sso, ssooidc, sts, imds — the Bedrock path), `google.golang.org/api` and `cloud.google.com/go/auth` (the Vertex path), the MCP Go SDK, `creack/pty`, `tidwall/gjson`/`sjson`, and `invopop/jsonschema`. TraceIQ uses **none** of those transports. Adding roughly a hundred modules and two cloud-provider auth stacks to a security-forward telemetry binary, in order to make one JSON POST, is the wrong trade.
2. **Toolchain (D-D, historical).** Under the withdrawn Go 1.23 constraint (§ 7.1, § 8.1) the SDK's declared toolchain requirement (`go 1.24`, `toolchain go1.25.8`) did not build at all, and the newest Go-1.23-compatible release was already well behind. Go 1.27.1 (`§ 8.2`) moots this specific blocker, but it does not change the decision: Reason 1's supply-chain weight — roughly a hundred modules and two cloud-provider auth stacks to make one JSON POST — is dispositive on its own.
3. **Explicit product-owner direction** for a direct-HTTPS, no-heavy-SDK approach.

**Accepted cost, stated plainly:** we hand-maintain the request/response structs and own the API-drift risk that an SDK would absorb. Mitigations, binding: the API version header is pinned and changed only deliberately; the wire shape is covered by golden-file tests (D-11); and the client implements the current API contract rather than a recalled one — specifically `thinking: {"type":"adaptive"}` (Opus 5 rejects `budget_tokens` with a 400 and has no assistant prefill), `output_config.effort` for cost/depth control, `strict: true` with `additionalProperties: false` on every tool schema, all `tool_result` blocks for a parallel batch returned in a **single** user message, `cache_control: {"type":"ephemeral"}` breakpoints ordered tools → system → messages with the volatile per-investigation content placed last, and a **`stop_reason == "refusal"` check before reading `content`** on every response. Per-investigation budgets (PRD § 4) map onto `max_tokens` plus `output_config.effort` plus our own wall-clock and query-count caps; `usage.cache_read_input_tokens` is exported as a self-observability metric so a silent cache regression is visible.

### D-10 — UI: **embedded static SPA, vanilla ES modules.** No React, no Vite, in the release build

`web/` ships hand-written ES modules, served from `embed.FS` by the F12 API server. `go build` remains the single build step for the release artefact; no `node_modules` enters it.

Rationale: C1 is the binding constraint. A React/Vite pipeline would put Node 22.15 and a lockfile with hundreds of transitive packages on the critical path of producing `traceiq`, and would put that dependency surface inside a security-forward artefact. Modern browsers support ES modules, `import`, `fetch` and web components natively — the "natively" principle applies to the front end too. The UI's job per the PRD is chat-first with rendered mini-waterfalls and evidence links; the heavy lifting (evidence assembly, trace layout) belongs server-side anyway, where it is testable in Go.

Node's legitimate role: **dev and test only** — Playwright for browser E2E, formatting/linting of `web/`. Never a release-build input.

Revisit if the UI grows a genuinely stateful multi-panel surface. At that point a build step may be justified; it is not justified for Phase 1–2.

### D-11 — Tests: **`go test` + stdlib `testing`**, race detector always, golden files, `go-cmp`. No testify

| Concern | Decision |
|---|---|
| Framework | Standard library `testing`. **testify rejected** — an assertion DSL and an extra dependency for something table-driven tests already do well |
| Diffing | `github.com/google/go-cmp` v0.7.0 — the one testing dependency, and only for readable struct diffs |
| Race detection | `go test -race ./...` on **every** CI run, not a nightly job. Non-negotiable: D-1's case rests on goroutine-based trace assembly, and the race detector is the control that makes that safe |
| Golden files | `testdata/` golden fixtures with a `-update` flag, for OTLP decode, Parquet block layout, the Anthropic request wire shape (D-9), and investigation-report rendering |
| Fuzzing | `go test -fuzz` on every parser that touches untrusted bytes: OTLP protobuf, Zipkin JSON, Jaeger Thrift. These are the true attack surface of F01 |
| Benchmarks | `testing.B` + `benchstat` for the sampler hot path and the D-3 hot-index write gate |

### D-12 — Lint & security tooling

| Tool | Pin | Role |
|---|---|---|
| `go vet` | bundled with toolchain | Baseline correctness; blocking |
| `staticcheck` (`honnef.co/go/tools`) | pinned in `§ 8.2` (latest release declaring Go ≥ 1.27 support) | Full SA/S/ST checks; blocking |
| `govulncheck` (`golang.org/x/vuln`) | pinned in `§ 8.2` | Symbol-reachability vulnerability scan; **blocking on any reachable finding — no allowlist file of any kind (DR-1; FU-2 CLOSED — not needed)** |
| `gosec` (`github.com/securego/gosec/v2`) | pinned in `§ 8.2` (latest release declaring Go ≥ 1.27 support) | Security-pattern scan (hardcoded creds, weak crypto, path traversal, unhandled errors); blocking |
| `go mod verify` + `GOFLAGS=-mod=readonly` | — | Checksum integrity; no implicit graph mutation in CI |
| `GOTOOLCHAIN=go1.27.1` | — | **Enforced in CI. Never `auto`, never `local`** (DR-1) — `auto` permits silent substitution; `local` breaks the self-provisioning property that lets a fresh clone build without a pre-installed matching toolchain |

### D-13 — Container base: **`gcr.io/distroless/static-debian12:nonroot`**

The `CGO_ENABLED=0` static binary needs no libc, so the smallest viable base applies. The distroless static image provides a CA-certificate bundle, tzdata, `/tmp`, and an `/etc/passwd` entry for UID **65532** — and nothing else: no shell, no package manager, no libc, no `curl`. There is no interactive attack surface inside the container.

Chosen over `scratch` because the CA bundle and tzdata are genuinely needed (outbound TLS to the Anthropic API, to object storage, and to Loki/Prometheus; seasonal baselines in F05 need timezone data). Chosen over Alpine because musl gains nothing when the binary is static and Alpine adds a shell and a package manager.

**Amendment (DR-24) — one additional file, `kubectl`.** F09's remediation executor needs a
Kubernetes CLI, and the board **rejected** `k8s.io/client-go` for that purpose (Appendix B):
client-go pulls roughly forty modules and two cloud auth stacks into the 73-module graph this
ADR treats as a security control. The image therefore becomes a two-stage build:
`gcr.io/distroless/static-debian12:nonroot` **plus exactly one additional file**,
`/usr/local/bin/kubectl`, copied from a pinned, digest-verified upstream release — never fetched
at runtime, so `readOnlyRootFilesystem: true` is unaffected.

| Field | Value |
|---|---|
| Pinned `kubectl` version | **v1.32.3** — latest stable 1.32 patch at pin time. Re-verify against the upstream Kubernetes release changelog in the same commit that lands the two-stage Dockerfile, and bump to a newer 1.32.x (or later minor, with a follow-up ADR note) patch if one has shipped since |
| `sha256` digest | `sha256:<TO BE COMPUTED AT IMAGE BUILD — CI step must record and pin this>` — the image-build CI step downloads the pinned version, computes its digest, records it here (and in the SBOM), and the Dockerfile fails closed if a subsequent fetch of that version does not match the recorded digest. This is the reproducible-build pattern (FU-9), not an unpinned placeholder: the version is fixed now, the digest is fixed at first build and never recomputed silently thereafter |
| Upstream URL | `https://dl.k8s.io/release/v1.32.3/bin/linux/<arch>/kubectl` |
| SBOM entry | Required — the binary is a discrete, non-Go supply-chain component and must appear in the release SBOM (`05 §9` FU-9 reproducible-build check covers the Go binary only; `kubectl` is tracked separately) |
| CVE-tracking owner | **Platform/SRE team** — via `govulncheck` (which does not cover this non-Go binary) plus the Kubernetes `security-announce` mailing list, with each `kubectl` patch release reviewed for advisories before the pin is bumped |

`kubectl` is a statically linked Go binary, so the image still contains **no shell and no
package manager**, and the `no-shell-interpolation` CI gate (§ 10) remains meaningful rather than
vacuous. `k8s.io/client-go` and every `k8s.io/*` module stay on § 8's deliberately-absent list,
mechanized by FU-8. `istioctl` is explicitly **not** added (DR-24, DR-36 §36.7) — F11's direct
`istioctl` fault-injection path is deleted.

Pod hardening, binding on `deploy/`: `runAsNonRoot: true`, `runAsUser: 65532`, `readOnlyRootFilesystem: true`, `allowPrivilegeEscalation: false`, `capabilities.drop: [ALL]`, `seccompProfile: RuntimeDefault`.

---

## 7. Consequences

### 7.1 Historical record — the Go 1.23 toolchain risk, now resolved (DR-1)

**Status: RESOLVED, 2026-09-15.** This section is retained as a historical record of the finding
that drove FU-1; it is no longer the state of the shipped toolchain. `05 §8.2` (Set B) is now the
normative pin set, `go.mod` declares `go 1.27` / `toolchain go1.27.1`, and `govulncheck` against
that toolchain reports **zero reachable vulnerabilities**. See the verified landing record in
§ 12.

**What the finding was.** Go's support policy covers the two most recent major releases. As of
the original evaluation date (2026-09-14) the current releases were **go1.27.1** and
**go1.26.8**; **Go 1.23 received no security patches at all**, and its final patch was
**go1.23.12** — so the build machine's then-current **go1.23.1** was nine patch releases behind
even within its own dead line.

This was not theoretical. `govulncheck` v1.1.4 run against the then-current § 8.1 (Set A) pin set
on go1.23.1 reported **25 vulnerabilities with a reachable call path** — not 25 advisories in the
graph, 25 that the symbol-level analysis proved the code could reach:

| Where | Count | Examples |
|---|---:|---|
| Go standard library | 24 | `crypto/tls` ×5 (incl. GO-2026-4870, unauthenticated TLS 1.3 KeyUpdate → persistent connection retention / DoS), `crypto/x509` ×8 (chain-building and policy-validation DoS), `encoding/asn1` ×2 (DER memory exhaustion), `net/http/internal` (GO-2025-3563, **request smuggling via invalid chunked data**), `net/url`, `net/textproto`, `html/template` (XSS), `net`, `os` |
| `google.golang.org/grpc` v1.75.1 | 1 | **GO-2026-6061** — xDS RBAC authorization engine **and the HTTP/2 transport server implementation**. Fixed in v1.82.1, which declares `go 1.25.0` |

Three points sharpened it at the time, retained for the record:

1. **GO-2026-6061 was not only an xDS issue.** The advisory covered the HTTP/2 transport server, and `govulncheck` traced reachable paths through `transport.NewHTTP2Client` / `transport.http2Client.Close` — code every gRPC user executes. It could not be waved away as "we don't use xDS", and it could not be fixed while the toolchain was Go 1.23, because the fix shipped only in grpc v1.82.1+, which requires Go 1.25.
2. **A TLS/x509/HTTP-smuggling exposure was the exact opposite of the requirement this ADR exists to satisfy.** An OTLP ingest endpoint terminating mTLS from an untrusted mesh is precisely where `crypto/tls`, `crypto/x509` and `net/http` correctness matters most.
3. Four of the 25 were free to fix on the 1.23 line alone (GO-2025-3373, GO-2025-3447, GO-2025-3563, GO-2025-3750, fixed in go1.23.5/.6/.8/.10); the remaining 21 required go1.24.8+ or go1.25.8+ and were unreachable from the 1.23 line at any patch level — which is why the board went straight to the 1.27 line rather than patching within 1.23.

**Resolution.** FU-1 landed on 2026-09-15: `go.mod` moved to `go 1.27` / `toolchain go1.27.1`, Set B (§ 8.2) is now normative, and `govulncheck` against the landed graph reports **0 reachable vulnerabilities** (§ 12). FU-2's expiring allowlist is **closed as not needed** — there is nothing left to allow, and CI is red on any new finding with no suppression file (DR-1).

Landing Go 1.27 additionally unlocked two things this security-forward product should want: **`X25519MLKEM768`** hybrid post-quantum key exchange (standardised FIPS 203 ML-KEM, default from Go 1.24; Go 1.23 had only the older `X25519Kyber768Draft00` draft), and **`GOFIPS140=v1.0.0`**, the CMVP-certified (Certificate #5247) pure-Go FIPS 140-3 cryptographic module introduced in Go 1.24 — a hard requirement for regulated customers, achieved with no cgo and no OpenSSL (tracked for a release variant under FU-6, still open, P2).

**Other negative consequences, current:**

- **Hand-maintained Anthropic client (D-9).** We own API-drift risk. Mitigated by golden-file tests and a pinned API version, not eliminated. FU-4 remains open — see § 9.
- **Vanilla-JS UI (D-10).** Slower feature velocity for complex UI than React would give. Accepted while the UI stays chat-first.
- **Single-writer hot index (D-3).** Pure-Go SQLite gives up write throughput relative to the cgo binding. Gated by the benchmark in FU-3 (re-scoped by DR-6 to the real schema — span rows + `attr_index` + FTS), with ClickHouse as the documented escape hatch, **required** above `store.hot.max_kept_spans_per_sec` (DR-6 §6.4).
- **No Python in the runtime (D-2).** Contributors fluent in Python but not Go cannot work on the RCA engine.
- **Go data races are possible.** Unlike Rust, Go does not prevent them at compile time. Mitigated structurally by `-race` on every CI run (D-11) — a control, not a guarantee.

### 7.2 Positive

- **C1 satisfied and demonstrated, not claimed.** A ~16 MB static binary, cross-compiled to linux/amd64, linux/arm64 and darwin/arm64 from a Windows laptop with zero cross toolchains (§ 5).
- **C2 satisfied natively.** TLS/mTLS from the standard library with no C dependency; memory safety by construction; goroutines + bounded channels for concurrent ingest; `context` + `errgroup.SetLimit` + `semaphore.Weighted` for structured concurrency with real backpressure.
- **Small, auditable supply chain.** 73 modules total for the entire data plane — after deliberately rejecting the Anthropic SDK (~100 modules for one POST), gin/chi, testify, DuckDB, and confluent-kafka-go.
- **Ecosystem alignment is strategic, not incidental.** Being Go in a Go-dominated space (OTel Collector, Jaeger v2, Tempo, Prometheus) means the PRD's "can run *on top of* Tempo as a backend" and "consume Tempo's MCP server" claims are library calls, not integration projects.
- **Single language, single binary, single build.** One `model.Span`; no serialisation boundary between the sampler and the RCA engine that reasons over its output.
- **Tooling is first-party.** Race detector, fuzzer, benchmarks, `govulncheck`'s reachability analysis, `GOFIPS140` — none are third-party bolt-ons.
- **Startup in milliseconds.** No JIT warm-up; laptop mode and K8s pod churn both benefit.

### 7.3 Neutral

- The feature catalog's Go package paths and interface signatures are now ratified rather than assumed.
- Node 22.15 and Python 3.11 remain useful for dev tooling and the eval harness's scenario scripting (F11); neither is a runtime dependency.

---

## 8. Follow-up decisions: pinned dependency list

### 8.1 Set A — WITHDRAWN 2026-09-14

**Withdrawn in full (DR-1).** Set A existed only to describe the Go 1.23 toolchain constraint
recorded in § 7.1, and that constraint is gone: `go.mod` now declares `go 1.27` / `toolchain
go1.27.1` (§ 8.2). **No document may pin from Set A.** It is retained below, unedited, purely as
the historical record § 7.1 refers to.

`go.mod` declared `go 1.23.0`. **Every version below was verified on 2026-09-14** against the Go module proxy for (a) its `go` directive being ≤ 1.23, (b) resolving under `GOTOOLCHAIN=local` on go1.23.1, (c) building and cross-compiling under `CGO_ENABLED=0`, and (d) its license via deps.dev. None requires CGO.

**Direct dependencies**

| Module | Version | `go` directive | License | Purpose |
|---|---|---|---|---|
| `go.opentelemetry.io/proto/otlp` | `v1.9.0` | 1.23.0 | Apache-2.0 | OTLP wire types (F01) — v1.11.0 requires Go 1.25 |
| `google.golang.org/grpc` | `v1.75.1` | 1.23.0 | Apache-2.0 | OTLP/gRPC receiver, 4317 (F01) — **last Go 1.23 release**; v1.76.0+ requires Go 1.24. See § 7.1 |
| `google.golang.org/protobuf` | `v1.36.12` | 1.23 | BSD-3-Clause | Protobuf runtime — latest release, no downgrade needed |
| `modernc.org/sqlite` | `v1.39.0` | 1.23.0 | BSD-3-Clause | Pure-Go embedded hot index (F03) — v1.58.0 requires Go 1.25 |
| `github.com/parquet-go/parquet-go` | `v0.25.1` | 1.22 | Apache-2.0, MIT | Parquet cold store (F03) — v0.32.0 requires Go 1.24.9 |
| `github.com/klauspost/compress` | `v1.18.4` | 1.23 | BSD-3-Clause, Apache-2.0, MIT | zstd for Parquet blocks — v1.20.0 requires Go 1.25 |
| `golang.org/x/sync` | `v0.16.0` | 1.23.0 | BSD-3-Clause | `errgroup` (bounded via `SetLimit`), `semaphore` — structured concurrency & backpressure |
| `golang.org/x/time` | `v0.12.0` | 1.23.0 | BSD-3-Clause | `rate.Limiter` for ingest and LLM-call rate limiting |
| `golang.org/x/crypto` | `v0.41.0` | 1.23.0 | BSD-3-Clause | `argon2` for API-token/password hashing (X-SEC). **Not** `x/crypto/ssh` |
| `github.com/google/go-cmp` | `v0.7.0` | 1.21 | BSD-3-Clause | Test-only struct diffing (D-11) |
| `github.com/prometheus/client_golang` | `v1.23.2` | 1.23.0 | Apache-2.0, BSD-3-Clause | `/metrics` self-observability (X-OPS) |
| `go.opentelemetry.io/otel` + `/sdk` | `v1.38.0` | 1.23.0 | Apache-2.0, BSD-3-Clause | Self-tracing — TraceIQ instruments itself (X-OPS) |

**Phase-2 / optional backends** (behind build tags or interface implementations; not in the default binary's default path)

| Module | Version | `go` directive | License | Purpose |
|---|---|---|---|---|
| `github.com/ClickHouse/clickhouse-go/v2` | `v2.40.1` | 1.23.0 | Apache-2.0 | Scale-out hot index (D-3) — v2.48.0 requires Go 1.25 |
| `github.com/twmb/franz-go` | `v1.19.5` | 1.23.8 | BSD-3-Clause | Kafka/Redpanda buffer (D-5) — pure Go; **not** confluent-kafka-go (CGO) |

**Required indirect dependencies** (pinned explicitly because they gate the Go 1.23 floor)

| Module | Version | `go` directive | License | Why pinned |
|---|---|---|---|---|
| `github.com/grpc-ecosystem/grpc-gateway/v2` | `v2.27.2` | 1.23.0 | BSD-3-Clause | **Build-required** — the OTLP collector service package ships generated gateway stubs (§ 5) |
| `google.golang.org/genproto/googleapis/rpc` | `v0.0.0-20250825161204-c5933d9347a5` | 1.23.0 | Apache-2.0 | gRPC status types |
| `google.golang.org/genproto/googleapis/api` | `v0.0.0-20250825161204-c5933d9347a5` | 1.23.0 | Apache-2.0 | gRPC annotations |
| `golang.org/x/net` | `v0.43.0` | 1.23.0 | BSD-3-Clause | **Indirect only** — pulled by grpc/otlp. We do **not** import `x/net/html` (the GO-2026-5028 DoS surface) and do **not** need h2c |
| `golang.org/x/sys` | `v0.35.0` | 1.23.0 | BSD-3-Clause | Platform syscalls |
| `golang.org/x/text` | `v0.28.0` | 1.23.0 | BSD-3-Clause | Encoding |
| `modernc.org/libc` | `v1.66.3` | 1.23.0 | BSD-3-Clause, MIT | Pure-Go libc shim under `modernc.org/sqlite` |
| `modernc.org/memory`, `mathutil`, `fileutil` | `v1.11.0`, `v1.7.1`, `v1.3.8` | ≤1.23.0 | BSD-3-Clause | `modernc.org/sqlite` support |

**Deliberately absent** — each an explicit rejection, recorded so it is not re-added by drift:
`github.com/anthropics/anthropic-sdk-go` (D-9), `github.com/go-chi/chi/v5` / `gin-gonic/gin` (D-6), `github.com/stretchr/testify` (D-11), `github.com/marcboeker/go-duckdb` / `duckdb/duckdb-go` (D-3, CGO), `github.com/mattn/go-sqlite3` (D-3, CGO), `confluentinc/confluent-kafka-go` (D-5, CGO), `go.opentelemetry.io/collector/*` (the collector module set requires Go 1.25; TraceIQ implements OTLP receivers directly and stays wire-compatible — collector-distro packaging is revisited after FU-1).

**License posture.** Every dependency is Apache-2.0, BSD-3-Clause, or MIT. **No copyleft, no AGPL** anywhere in the graph — deliberate, since Grafana Tempo (AGPLv3) is a supported *backend* but must never become a linked dependency of TraceIQ's open core.

**Toolchain pins**

| Tool | Version | License | Invocation |
|---|---|---|---|
| Go toolchain | `go1.23.1` today → **`go1.27.x` required, FU-1** | BSD-3-Clause | `GOTOOLCHAIN=local`, `CGO_ENABLED=0` |
| `honnef.co/go/tools` (staticcheck) | `v0.6.1` | MIT, BSD-3-Clause | `staticcheck ./...` |
| `golang.org/x/vuln` (govulncheck) | `v1.1.4` | BSD-3-Clause | `govulncheck ./...` |
| `github.com/securego/gosec/v2` | `v2.22.7` | Apache-2.0 | `gosec ./...` |
| Container base | `gcr.io/distroless/static-debian12:nonroot` | — | UID 65532 (D-13) |

### 8.2 Set B — normative pins (DR-1; Go 1.27.1 toolchain, landed 2026-09-15)

**This is now the normative dependency set, and `05 §8` is its sole owner (DR-0):** every other
document — including `01 §11`, which is a pointer only — cites a module/version pair from here
rather than declaring one of its own. Set A (§ 8.1) is withdrawn; every other document in this
set pins from Set B, never from Set A. `go.mod`, verbatim (DR-1):

```
module traceiq

go 1.27

toolchain go1.27.1
```

CI environment, verbatim: `GOTOOLCHAIN=go1.27.1`, `CGO_ENABLED=0`, `GOFLAGS=-mod=readonly`, build
flags `-trimpath`. **`GOTOOLCHAIN=auto` and `GOTOOLCHAIN=local` are both forbidden** — `auto`
permits silent substitution, `local` breaks the self-provisioning property that lets a fresh
clone build without a pre-installed matching toolchain. The builder image pre-seeds the
`go1.27.1` toolchain module so offline/air-gapped builds do not regress, and the toolchain
module hash is recorded in the SBOM.

**Direct dependencies.** Rows marked ✅ were compiled and `govulncheck`-verified on the build
machine on 2026-09-15 at `CGO_ENABLED=0` under `GOTOOLCHAIN=go1.27.1`, with **0 reachable
vulnerabilities** (§ 12 records the landing).

| Module | Set A (withdrawn) | **Set B (normative)** | Verified | What it buys |
|---|---|---|:--:|---|
| `go.opentelemetry.io/proto/otlp` | v1.9.0 | **v1.11.0** | ✅ | Current OTLP definitions (F01) |
| `google.golang.org/grpc` | v1.75.1 | **v1.83.2** | ✅ | **Closes GO-2026-6061** (fix landed in v1.82.1); OTLP/gRPC + Jaeger gRPC receivers |
| `google.golang.org/protobuf` | v1.36.12 | **v1.36.12** | ✅ | Protobuf runtime — unchanged, already at head |
| `modernc.org/sqlite` | v1.39.0 | **v1.58.0** | ✅ | Pure-Go hot index (F03), 19 minor releases of fixes |
| `github.com/parquet-go/parquet-go` | v0.25.1 | **v0.32.0** | ✅ | Current writer, cold store (F03) |
| `golang.org/x/sync` | v0.16.0 | **v0.23.0** | ✅ | `errgroup`, `semaphore` |
| `github.com/klauspost/compress` | v1.18.4 | **v1.20.0** | ✅ | Current zstd for Parquet |
| `golang.org/x/time` | v0.12.0 | **v0.16.0** | ✅ | `rate.Limiter` |
| `golang.org/x/crypto` | v0.41.0 | **v0.57.0** | ✅ | Closes GO-2026-5014; `argon2id` (X-SEC) |
| `github.com/google/go-cmp` | v0.7.0 | **v0.7.0** | ✅ | Test-only struct diffing — unchanged |
| `github.com/prometheus/client_golang` | v1.23.2 | **v1.24.1** | ✅ | `/metrics` self-observability |
| `go.opentelemetry.io/otel` + `/sdk` | v1.38.0 | **v1.46.0** | ✅ | Self-tracing |

**Required indirect dependencies**, re-pinned in the same atomic change:

| Module | Set A | **Set B** | Why pinned |
|---|---|---|---|
| `golang.org/x/net` | v0.43.0 | **v0.59.0** | Closes GO-2026-5028; indirect only, pulled by grpc/otlp — we do **not** import `x/net/html` and do **not** need h2c |
| `github.com/grpc-ecosystem/grpc-gateway/v2` | v2.27.2 | **current release declaring `go` ≤ 1.27** | Build-required — the OTLP collector service package ships generated gateway stubs (§ 5) |
| `golang.org/x/sys`, `x/text` | v0.35.0, v0.28.0 | **current releases declaring `go` ≤ 1.27** | Platform syscalls, encoding |
| `modernc.org/libc`, `memory`, `mathutil`, `fileutil` | v1.66.3, v1.11.0, v1.7.1, v1.3.8 | **current releases declaring `go` ≤ 1.27** | `modernc.org/sqlite` support |

**[P2] optional backends** (build-tagged, absent from the default module graph):
`github.com/ClickHouse/clickhouse-go/v2 v2.48.0`, `github.com/twmb/franz-go v1.19.5`.

**Analyser pins, re-pinned in the same atomic change (DR-1)** — an analyser built against an
older Go silently under-analyses:

| Tool | Pin | Gate |
|---|---|---|
| `honnef.co/go/tools` (staticcheck) | latest release declaring Go ≥ 1.27 support | blocking |
| `github.com/securego/gosec/v2` | latest release declaring Go ≥ 1.27 support | blocking |
| `golang.org/x/vuln` (govulncheck) | ≥ `v1.1.4`, rebuilt under go1.27.1 | blocking, **0 reachable** |

**Toolchain pins**

| Tool | Version | License | Invocation |
|---|---|---|---|
| Go toolchain | **`go1.27.1`** (landed 2026-09-15, closing FU-1) | BSD-3-Clause | `GOTOOLCHAIN=go1.27.1`, `CGO_ENABLED=0` — never `auto` or `local` |
| `honnef.co/go/tools` (staticcheck) | latest declaring Go ≥ 1.27 | MIT, BSD-3-Clause | `staticcheck ./...`, blocking |
| `golang.org/x/vuln` (govulncheck) | ≥ `v1.1.4`, rebuilt under go1.27.1 | BSD-3-Clause | `govulncheck ./...`, blocking, 0 reachable |
| `github.com/securego/gosec/v2` | latest declaring Go ≥ 1.27 | Apache-2.0 | `gosec ./...`, blocking |
| Container base | `gcr.io/distroless/static-debian12:nonroot` + pinned `kubectl` (D-13, DR-24) | — | UID 65532 |

**Deliberately absent, unchanged plus two new entries (DR-24, DR-32):** every Set A rejection
carries forward —
`github.com/anthropics/anthropic-sdk-go` (D-9), `github.com/go-chi/chi/v5` / `gin-gonic/gin` (D-6), `github.com/stretchr/testify` (D-11), `github.com/marcboeker/go-duckdb` / `duckdb/duckdb-go` (D-3, CGO), `github.com/mattn/go-sqlite3` (D-3, CGO), `confluentinc/confluent-kafka-go` (D-5, CGO), `go.opentelemetry.io/collector/*` (the collector module set requires Go 1.25+; TraceIQ implements OTLP receivers directly and stays wire-compatible — collector-distro packaging is FU-5)
— **plus, new:**
- **`k8s.io/client-go` and all `k8s.io/*` modules** (DR-24 — see Appendix-B-equivalent rationale in § D-13/§ D-9 pattern: a pinned `kubectl` binary in the image plus `internal/k8s`'s argv builder replaces the client library entirely; mechanized by FU-8).
- **`github.com/lib/pq`, `github.com/jackc/pgx`, and any `pgvector` client** (DR-32 §32.1) — superseded, recorded as a supersession row: *"PRD § Technology stack summary: Memory = Postgres + pgvector → superseded by `store`-backed `memory.HybridStore` (D-J5)."* No external vector database is required by default; `01 §1` ("no required external database"), `02` (`memory.HybridStore → store.Store`) and `F08 §3.2` already agree. An external vector store, if ever wanted at scale, arrives as a `memory.Backend` implementation with its own pins and an ADR amendment — not as a box in a topology diagram.

**License posture.** Every dependency is Apache-2.0, BSD-3-Clause, or MIT. **No copyleft, no AGPL** anywhere in the graph — deliberate, since Grafana Tempo (AGPLv3) is a supported *backend* but must never become a linked dependency of TraceIQ's open core.

---

## 9. Follow-up actions

Rewritten per DR-1's follow-up table; FU-1 and FU-2 are now closed.

| ID | State | Action | Priority | Owner | Gate |
|---|---|---|---|---|---|
| **FU-1** | **CLOSED** | Go 1.27.1 landed 2026-09-15; Set B (§ 8.2) is normative; 0 reachable vulnerabilities | P0 — was a release blocker | Dev lead | `govulncheck ./...` reports **zero** reachable vulnerabilities — met |
| **FU-2** | **CLOSED — not needed** | The expiring `govulncheck` allowlist envisioned in Revision 1 is deleted; there is nothing to allow. CI is red on any finding, with no suppression file | — | Dev lead | N/A |
| FU-3 | **OPEN, re-scoped by DR-6** | Benchmark the pure-Go SQLite hot index against **DR-6's writer budget** (≥ 2 000 committed rows/s, ≥ 20 tx/s, p99 commit ≤ 120 ms) with the real schema — span rows + `attr_index` + FTS, not a synthetic rollup-only load; publish sustained committed rows/sec. If the gate fails, promote ClickHouse from pluggable to **required** above `store.hot.max_kept_spans_per_sec` (DR-6 §6.4; laptop mode stays SQLite regardless) | P1 | Dev lead | § 10 PERF gate |
| FU-4 | OPEN | Land golden-file tests for the Anthropic Messages wire shape — the exact request body and the exact parsing of `usage`, `stop_reason`, `refusal` and tool-use blocks — before the first F06 merge (DR-34 §34.2 drift mitigation) | P1 | Test lead | Golden tests in CI |
| FU-5 | OPEN [P2] | Reassess packaging TraceIQ as an OTel Collector distribution (per the PRD's Jaeger v2 precedent), now that Go 1.27 removes the Go 1.25 barrier from `go.opentelemetry.io/collector` | P2 | Architecture board | ADR addendum |
| FU-6 | OPEN [P2] | Evaluate `GOFIPS140=v1.0.0` builds as a distributed release variant for regulated customers | P2 | Architecture board | ADR addendum |
| **FU-7** | **NEW, P0** | CI asserts each analyser actually executed (non-empty analysis manifest per tool); a skipped gate must fail, not pass | P0 | Dev lead | Analysis manifest present and non-empty per tool, per CI run |
| **FU-8** | **NEW, P0** | CI check over `go list -m all` fails on any module in the § 8.2 "deliberately absent" list (`k8s.io/*`, `lib/pq`, `jackc/pgx`, any `pgvector` client, plus the pre-existing rejections) | P0 | Dev lead | CI job fails the build on a match |
| **FU-9** | **NEW, P1** | Reproducible-build check: two clean builds produce identical digests under the pinned `go1.27.1` toolchain | P1 | Dev lead | Digest comparison in CI |
| **FU-10** | **NEW (DR-31)** | Land `internal/archtest`'s time/rand ban (forbidding `time.Now`/`time.After`/`time.NewTicker`/package-level `math/rand` outside `internal/model` and `cmd/traceiq`) **before the first feature merge** — retrofitting it later is far more expensive | P0 | Dev lead | `archtest` gate present and blocking before the first `internal/*` feature PR merges |
| **FU-11** | **NEW (DR-38 §38.4)** | Generate `docs/architecture/defaults.md` (from `01 §7`, diffed against every feature doc's `Config keys` block) and `docs/architecture/traceability.md` (FR ↔ AC ↔ test file, failing on any FR with zero ACs); diff both in CI | P1 | Architecture board | Both generated files exist and their CI diff/coverage jobs are blocking |

---

## 10. Security posture checklist for the chosen stack

Bindings on `internal/auth` (X-SEC) and all feature packages. Each item is verifiable in CI or by inspection.

**Transport**
- [ ] TLS 1.3 minimum on every listener: `tls.Config{MinVersion: tls.VersionTLS13}`. TLS 1.2 only by explicit per-listener config with a documented reason
- [ ] mTLS on OTLP ingest: `ClientAuth: tls.RequireAndVerifyClientCert` with a configured `ClientCAs` pool — mesh-sourced spans are untrusted input
- [ ] Standard-library `crypto/tls` only. **No OpenSSL, no cgo TLS, no bundled crypto** — the "natively" requirement, enforced by `CGO_ENABLED=0`
- [ ] Certificate hot-reload via `tls.Config.GetCertificate` so rotation needs no restart
- [ ] Outbound TLS (Anthropic API, object storage, Loki/Prometheus) verifies the server certificate. `InsecureSkipVerify` is banned; `gosec` G402 enforces this
- [ ] `X25519MLKEM768` hybrid PQ key exchange confirmed active on ingest listeners (FU-1 closed, Go 1.27.1 landed)

**Memory & concurrency safety**
- [ ] `go test -race ./...` blocking on every CI run — the structural control behind D-1
- [ ] `go test -fuzz` corpora for every untrusted-input parser: OTLP protobuf, Zipkin JSON, Jaeger Thrift
- [ ] Hard caps before allocation on every decode path: max span batch bytes, max spans per trace, max attribute count and value length. An unbounded OTLP payload must not be able to allocate an unbounded heap
- [ ] Every bounded queue has an explicit overflow policy (block, or drop-with-metric) — never unbounded growth
- [ ] `context.Context` threaded through every I/O path; no goroutine started without a cancellation path (leak test via `goleak`-style check in package teardown)

**Supply chain**
- [ ] `go.sum` committed; `go mod verify` and `GOFLAGS=-mod=readonly` in CI
- [ ] `GOTOOLCHAIN=go1.27.1` in CI — **never `auto`, never `local`** — no silent toolchain substitution and no self-provisioning break (DR-1)
- [ ] `govulncheck` blocking, **zero reachable vulnerabilities, no allowlist file of any kind** (FU-2 closed — there is nothing to allow)
- [ ] `gosec` and `staticcheck` blocking, both pinned to releases declaring Go ≥ 1.27 support (§ 8.2 analyser pins)
- [ ] FU-7: CI asserts each analyser actually produced a non-empty analysis manifest — a skipped gate fails, not passes
- [ ] FU-8: CI fails on any `k8s.io/*`, `lib/pq`, `jackc/pgx` or `pgvector` client appearing in `go list -m all`
- [ ] FU-9: two clean builds under `go1.27.1` produce identical digests (reproducible build)
- [ ] SBOM generated per release (`go version -m` at minimum, plus the pinned `kubectl` binary's version/digest, D-13) and published with the artefact
- [ ] Reproducible builds: `-trimpath`, pinned toolchain version, no timestamps in `ldflags`
- [ ] Dependency additions require ADR amendment — the § 8 "deliberately absent" list exists to be enforced, not admired
- [ ] License scan gate: Apache-2.0 / BSD / MIT only; **any copyleft or AGPL dependency fails the build**

**Application security (X-SEC)**
- [ ] API authentication on every non-health endpoint; no unauthenticated mutation path
- [ ] RBAC enforced server-side, per tenant, on every store query — never in the UI
- [ ] Tenant ID derived from the authenticated principal, **never** from a client-supplied header or body field
- [ ] Secrets (Anthropic API key, object-storage credentials, DB DSNs) from environment or mounted files only. Never in config committed to git; `gosec` G101 blocking
- [ ] Secrets redacted from logs, from investigation logs, and from error strings returned over the API
- [ ] All SQL parameterised. String-concatenated SQL fails review; `gosec` G201/G202 blocking
- [ ] NL→query (F10) compiles to a **constrained query AST**, never to raw SQL or raw TraceQL text — the injection boundary for the agentic surface
- [ ] LLM output is never executed, never interpolated into a query, and never trusted as an authorisation decision. Model output selects from a fixed tool set with validated arguments (`strict: true`, `additionalProperties: false`)
- [ ] `stop_reason == "refusal"` checked before reading `content` on every Anthropic response (D-9)
- [ ] Per-investigation budgets enforced server-side (tokens, wall-clock, query count) — a runaway agent is a denial-of-service against our own stores
- [ ] Remediation (F09): read-only default, tenant allowlist, human approval gate, per-incident action budget, immutable audit log. Kubernetes access via a **scoped ServiceAccount with least-privilege RBAC**, never cluster-admin
- [ ] Embedded UI served with `Content-Security-Policy` (no inline script, no remote origins), `X-Content-Type-Options: nosniff`, `Referrer-Policy: no-referrer`
- [ ] Rate limiting (`golang.org/x/time/rate`) on ingest, API, and outbound LLM calls

**Runtime**
- [ ] Container runs as non-root UID 65532 on `distroless/static-debian12:nonroot`
- [ ] `readOnlyRootFilesystem: true`, `allowPrivilegeEscalation: false`, `capabilities.drop: [ALL]`, `seccompProfile: RuntimeDefault`
- [ ] No shell, no package manager, no debug tooling in the release image
- [ ] Health/readiness endpoints expose no internal state and require no auth; **everything else requires auth**
- [ ] Self-observability (metrics, self-traces) exposed on a **separate port** from ingest and API, bindable to localhost

---

## 11. References

- `Tracing-Bot-PRD.md` — § Detailed Low Level Architecture, § Technology stack summary
- `docs/architecture/00-feature-catalog.md` — F01–F12, X-SEC, X-OPS
- [OpenTelemetry — Status / per-language SDK maturity](https://opentelemetry.io/status/)
- [OpenTelemetry graduates in the CNCF (2026-05-21)](https://www.cncf.io/announcements/2026/05/21/cloud-native-computing-foundation-announces-opentelemetrys-graduation-solidifying-status-as-the-de-facto-observability-standard/)
- [Jaeger v2 released: OpenTelemetry in the core (CNCF)](https://www.cncf.io/blog/2024/11/12/jaeger-v2-released-opentelemetry-in-the-core/)
- [ClickHouse as a core storage backend in Jaeger](https://medium.com/jaegertracing/making-design-decisions-for-clickhouse-as-a-core-storage-backend-in-jaeger-62bf90a979d)
- [Go — Release History and support policy](https://go.dev/doc/devel/release)
- [The FIPS 140-3 Go Cryptographic Module](https://go.dev/blog/fips140) · [Go FIPS 140-3 compliance docs](https://go.dev/doc/security/fips140)
- [Tokio — MSRV policy (Rust ≥ 1.71)](https://github.com/tokio-rs/tokio)
- [parquet-go/parquet-go](https://github.com/parquet-go/parquet-go)
- [GoogleContainerTools/distroless — base images](https://github.com/GoogleContainerTools/distroless/blob/master/base/README.md)
- Go module proxy (`proxy.golang.org`), deps.dev license API, OSV API, and `govulncheck` v1.1.4 — all queried 2026-09-14

---

## 12. Verified: the Go 1.27.1 landing (DR-1, closes FU-1/FU-2)

Recorded separately from § 8.2 so the landing event itself — not just the resulting pin table —
is independently auditable. All facts below were verified on the build machine on **2026-09-15**.

| Fact | Value |
|---|---|
| `go.mod` header | `module traceiq` / `go 1.27` / `toolchain go1.27.1` |
| `go build ./...` | **Succeeded** under `CGO_ENABLED=0` |
| `govulncheck ./...` | **0 reachable vulnerabilities** (down from the 25 recorded in § 7.1's historical finding) |
| Direct pins confirmed at their Set B (§ 8.2) versions | `go.opentelemetry.io/proto/otlp v1.11.0`, `google.golang.org/grpc v1.83.2`, `google.golang.org/protobuf v1.36.12`, `modernc.org/sqlite v1.58.0`, `github.com/parquet-go/parquet-go v0.32.0`, `golang.org/x/sync v0.23.0`, `github.com/klauspost/compress v1.20.0`, `golang.org/x/time v0.16.0`, `golang.org/x/crypto v0.57.0`, `github.com/google/go-cmp v0.7.0`, `github.com/prometheus/client_golang v1.24.1`, `go.opentelemetry.io/otel` + `/sdk v1.46.0` |

This closes **FU-1** (P0, release blocker — met) and **FU-2** (closed as not needed — the
`govulncheck` allowlist envisioned against the 25-finding baseline has nothing left to hold).
§ 7.1 and § 8.1 are retained as the historical record of the finding that made this landing
necessary; § 8.2 is the live, normative pin table going forward. Remaining open follow-ups are
FU-3 through FU-11 (§ 9).
