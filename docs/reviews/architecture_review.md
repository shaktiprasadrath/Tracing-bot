# TraceIQ — Architecture Review Log

**Project:** TraceIQ — AI-native distributed tracing (Go 1.27, single binary, embedded SQLite + Parquet)
**Artefacts under review:** `Tracing-Bot-PRD.md`; `docs/architecture/00`–`05`; `docs/architecture/features/F01`–`F12`, `X-SEC`, `X-OPS`; `docs/ledger.md`
**Decision register (binding):** [`../architecture/06-decision-register.md`](../architecture/06-decision-register.md)

---

## Round 1 — 2026-09-14

### Board and verdicts

| Reviewer lens | Reviewer artefact | Verdict | Counts |
|---|---|---|---|
| **Security & Reliability** | [`round1/security-reliability.md`](round1/security-reliability.md) | **APPROVE-WITH-CHANGES** — blocking on SR-1 … SR-7 | 30 findings: 7 Blocker, 16 Major, 7 Minor |
| **Performance, Scalability & Data Architecture** | [`round1/performance-data.md`](round1/performance-data.md) | **REJECT** | 35 findings: 16 Blocker, 14 Major, 5 Minor |
| **Completeness, Traceability & Design Coherence** | [`round1/completeness-coherence.md`](round1/completeness-coherence.md) | **REJECT** | 33 findings: 8 Blocker, 15 Major, 10 Minor |

**Consolidated round-1 verdict: REJECT — revise and re-review.**
**Total findings: 98** (31 Blocker, 45 Major, 22 Minor).

> **Count note.** The fix-wave brief quoted "79 findings". The three review artefacts as filed contain **98** distinct IDs (SR-1…30, PD-1…35, CC-1…33). Every one of the 98 is carried in the table below and mapped to a decision; none was dropped to reach a smaller number.

### Chief Architect's reading of round 1

The three reviews disagree on verdict but converge on one diagnosis, and it is worth stating plainly because it determines the shape of the fix wave:

> **The system documents (`00`–`05`) describe one system; the fourteen feature documents describe a different, partially incompatible one.** Where `01 §8.6` says the Guard re-resolves targets against the topology graph and `F09 §4.4` does not, the code will be written from `F09`. Almost every Blocker is of that form.

Two findings are of a different and more serious kind, and are not reconciliation defects at all:

- **SR-1** establishes a live path from attacker-controlled telemetry content to a cluster mutation payload. It does not require the model to escape its schema — only to fill a legitimately-schema'd free-form field (`Action.Params["patch"]`) that nothing downstream semantically validates. Fixed by deleting free-form payloads from the data model (DR-22), not by validating them.
- **PD-1/PD-2/PD-3** show the storage tier cannot meet its own headline gate: the hot-index sizing is ~4× low and exceeds its own disk budget before any index amplification, while the feature-level hot index has no span rows at all. Fixed by a re-derived, published sizing model and a real `HotIndex` contract (DR-6).

The response is **`06-decision-register.md`**: forty binding decisions (DR-0 … DR-39) that own every contested fact, with exact Go signatures, DDL, config keys and defaults, state machines and worked arithmetic. Per DR-0, the register **wins** over `00`–`05`, any feature doc and the PRD until those documents are rewritten to match it.

### Disposition legend

- **ACCEPT** — the finding is upheld as written; the register implements the reviewer's required change.
- **ACCEPT-MODIFIED** — the defect is upheld, the remedy differs. Every instance is justified in the row and in the register's **Appendix B**.
- **REJECT** — the finding is not upheld. *(Zero findings were rejected in round 1.)*

---

## Consolidated findings table

### Security & Reliability (SR-1 … SR-30)

| ID | Sev | Doc § | Issue (one line) | Disposition | DR | Owner docs |
|---|---|---|---|---|---|---|
| SR-1 | Blocker | `F09 §4.2`/`§4.4`, `01 §4.1` | Telemetry content reaches a cluster mutation payload via syntax-only-validated `Params["patch"]` | ACCEPT | DR-22 | `01 §4.1`/`§4.5`/`§8.6`, `02 §3`, `03 §5`, `F06 §4.2`, `F09 §3.1`/`§4.2`/`§4.4`/`§6`/`§7` |
| SR-2 | Blocker | `F10 §4.3`, `F12 §4.3`, `F09 §4.2`, `02 §3` | Approval identity undefined; chat surface authorizes on an HMAC signature | ACCEPT | DR-25 (§25.4), DR-23 (§23.7) | `X-SEC §4.6` (new), `01 §5.1`/`§6.1`, `02 §3`, `F09 §4.3`, `F10 §4.3`, `F12 §4.3` |
| SR-3 | Blocker | `01 §7`, `X-SEC`, `F12 §4.3`, `F01 §4.3` | Plaintext non-loopback is the shipped default; ingest auth not expressible in the config schema | ACCEPT | DR-26 | `01 §7`/`§8.1`/`§8.4`, `F01 §4.3`/`§6`, `F12 §4.3`, `X-SEC §3.1` |
| SR-4 | Blocker | `F09 §4.2`/`§4.3`/`§4.4` | No `Expired` state, no separation of duty, `auto_execute_on_approve: true` | ACCEPT | DR-23 (§23.2–§23.5) | `01 §4.5`/`§8.2`, `03 §5`, `F09 §3.1`/`§4.2`/`§4.4`/`§7` |
| SR-5 | Blocker | `F09 §3.1`/`§4.4`/`§8` | Per-incident action budget bypassable: `failed` frees a slot; override is a bare string with no ceiling | ACCEPT | DR-23 (§23.6) | `01 §4.5`, `F09 §3.1`/`§4.3`/`§4.4`/`§6`/`§7`/`§8` |
| SR-6 | Blocker | `F03`/`F06`/`F07`/`F08`/`F10`/`X-OPS §4.4` | Multi-tenant isolation asserted everywhere, enforceable nowhere — tenant absent from signatures and from F06's tables | ACCEPT | DR-5 | `00`, `01 §3.2`/`§4`/`§5.1`/`§6.1`/`§7`, `02`, all `F0x §4.3`, `X-SEC`, `X-OPS` |
| SR-7 | Blocker | `F03 §3.1`/`§3.2`/`§4.4`/`§7` vs `01 §3.1`/`§5.2` | Two mutually exclusive cold write paths; the implementable one inverts F03's crash invariant; per-trace Parquet delete is impossible | ACCEPT-MODIFIED — the `block_id` *pointer* is written post-seal, but the `trace` row exists at ack, or SR-7's own AC and the 12 s queryable NFR are unreachable | DR-7 | `01 §3.1`/`§5.2`/`§5.3`/`§7`, `03 §1`, `04 §1 L4`, `F03 §3.1`/`§4.2`/`§4.3`/`§4.4`/`§5`/`§7`/`§8` |
| SR-8 | Major | `F01 §3.1`/`§4.3`/`§4.4` vs `01 §3.1` | `queue.capacity: 100000` batches with unbounded `blockingPush`; ~400 GB ceiling against a 1.5 GiB RSS target | ACCEPT | DR-28 (§28.1) | `01 §3.1`/`§7`, `04 §4`/`§6`, `F01 §3.1`/`§4.3`/`§4.4`/`§7` |
| SR-9 | Major | `F02 §3.1`/`§5` vs `05 D-5`, `01 §10.3` | The spill-to-disk WAL that closes D-Z3 is absent from the feature spec | ACCEPT | DR-9 | `01 §10.3`, `05 §D-5`, `F02 §3.1`/`§4.3`/`§4.4`/`§7`, `X-OPS §3.2` |
| SR-10 | Major | `F02 §4.4`, `F03 §4.3` | Retention unbounded: ~39 % keep on healthy traffic, no `max_keep_rate`, free rare-path amplification, unbounded tenant byte budget | ACCEPT | DR-10, DR-11, DR-12 | `01 §7`, `03 §6`, `F02 §3.1`/`§4.3`/`§4.4`/`§7`, `F03 §3.1`/`§4.3`, `X-SEC §4.4` |
| SR-11 | Major | `01 §5.1`, `F09 §4.2`, `X-SEC §4.2` | Three hash definitions; unkeyed chain in the same file as the data; no multi-writer story; audit role disputed | ACCEPT | DR-27 | `X-SEC §4.2`, `01 §5.1`/`§6.1`/`§7`/`§8.5`, `02 §4`, `04 §5.2`, `F09 §3.1`/`§4.2`, `F12 §4.3` |
| SR-12 | Major | `F06 §3.1`/`§4.3` vs `01 §4.4`/`§6.3` | Budgets inconsistent, no cost or step ceiling, and `ToolArgs.Query`/`Extra` hand the model a free-form query string | ACCEPT | DR-16, DR-15, DR-17 | `01 §4.4`/`§6.3`/`§7`/`§8.6`, `02 §3`, `F06 §3.1`/`§4.3`/`§4.4`/`§6`/`§7` |
| SR-13 | Major | `F08 §6`, `01 §8.6(2)`, `03 §3`/`§7` | Memory is an unmanaged persistence channel for injected content, re-entering future prompts for up to a year | ACCEPT | DR-19 (§19.5), DR-37, DR-5 | `01 §4.6`/`§8.6`, `F06 §4.4`, `F08 §3.1`/`§4.2`/`§6`/`§7`, `X-SEC §4.4` |
| SR-14 | Major | `02 §4` vs `F12 FR-F12-8`, `01 §10.3` | Paging silently depends on the brain, specified two contradictory ways, with no deadman | ACCEPT-MODIFIED — one rule with three conditions (terminal-with-evidence, 5 min ceiling with partial, configured critical SLO), plus an `api`-owned deadman | DR-21 | `01 §7`/`§9`/`§10.3`, `02 §4`, `04 §5.2`, `F05 §3.1`, `F12 §3.1`/`§4.3`/`§7`, `F11 §3.1` |
| SR-15 | Major | `F09 §3.1`/`§4.4`, `02 §3`, `05 D-13` | Executor specified two ways and neither is buildable in the distroless image | ACCEPT-MODIFIED — **`client-go` rejected**; pinned digest-verified `kubectl` baked into the image, `internal/k8s` argv builder, no shell (Appendix B) | DR-24 | `05 §D-13`/`§8.1`/`§8.2`, `01 §1.2`, `02 §3`, `F09 §3.1`/`§4.4`/`§6`/`§7`, `X-OPS §6` |
| SR-16 | Major | `F11 §3.1`/`§4.3`/`§6` vs `F09 §1` | The eval harness is a second, unguarded cluster-mutation path reachable from the API | ACCEPT | DR-36 (§36.7), DR-24 | `01 §6.1`/`§7`, `F09 §1`, `F11 §3.1`/`§4.3`/`§4.4`/`§6`/`§7` |
| SR-17 | Major | `X-OPS FR-XOPS-4` vs `04 §3`/`§5.2` | `/readyz` probes object storage — a correlated readiness failure that stops ingest cluster-wide | ACCEPT | DR-33 (§33.1) | `04 §3`/`§5.2`, `X-OPS §3.1`/`§5`/`§7` |
| SR-18 | Major | `01 §7`, `X-OPS §4.2`, `F01 §6`, `X-SEC FR-XSEC-1` | Safety gates keyed on `deploy.mode`/`ops.mode`/`mode=dev`, none of which exist in the config schema | ACCEPT | DR-26 (§26.1) | `01 §7`, `04 §2`, `F01 §6`, `X-SEC FR-XSEC-1`, `X-OPS §4.2` |
| SR-19 | Major | `01 §6.1`/`§6.2` vs `F06`/`F09`/`F10`/`F12` | API surface disagrees on prefix, paths and MCP tool set; MCP privilege model inverted | ACCEPT-MODIFIED — one `/v1` table, per-tool MCP gating, and **12 tools**: `traceiq_propose_action` is not exposed over MCP in v1 | DR-29 | `01 §6.1`/`§6.2`/`§7`, `02 §4`, `F04`–`F12 §4.3`, `X-SEC §5` |
| SR-20 | Major | `01 §7`/`§8.4`, `X-SEC §3.1`, `F01 §6` | Three documents, three values for body size, attribute cap and rate limit — and an injection mitigation sized against the losing number | ACCEPT-MODIFIED — attribute cap settles at **4 KiB** with truncate-and-flag default plus a per-tenant strict-reject option | DR-26 (§26.4) | `01 §7`/`§8.4`, `F01 §6`, `F12 §4.3`, `X-SEC §3.1` |
| SR-21 | Major | `F07 §4.3`/`§6`, `01 §7` | Cross-tenant log retrieval by design; `EgressGuard` absent from the doc that owns egress; private networks allowed by default | ACCEPT | DR-20, DR-5 | `01 §7`/`§8.7`, `02 §3`, `F07 §3.1`/`§4.3`/`§4.4`/`§6`/`§7`, `X-SEC §4.4` |
| SR-22 | Major | `05 D-9` vs `01 §7`/`§10.3` | Two different default models with incompatible request shapes; no aggregate spend cap | ACCEPT | DR-34, DR-17 (§17.4) | `01 §7`/`§10.3`, `02 §3`, `05 §D-9`/`§9`, `PRD` ROI row, `F06 §3.2`/`§4.3`/`§4.4` |
| SR-23 | Major | `05 §7.1`/`§8`/`§9`/`§12` | FU-1 mis-sequenced; `GOTOOLCHAIN` discipline and analyser re-pinning unspecified | ACCEPT | DR-1 | `05 §7.1`/`§8.1`/`§8.2`/`§9`/`§10`/`§12`, `01` header/`§10`/`§11`, `docs/ledger.md` |
| SR-24 | Minor | `F01 §4.2`, `F06 §4.2`, `F02 §4.2` | Foundational types defined twice with incompatible shapes | ACCEPT | DR-4, DR-0 | `01 §4`/`§5.1`, `F01 §4.2`, `F02 §4.2`, `F06 §4.2` |
| SR-25 | Minor | `01 §6.1`, `F12 FR-F12-11`, `F09` | Idempotency covers creation only; `X-Slack-Request-Id` does not exist; `hash(body)` collides | ACCEPT | DR-23 (§23.8) | `01 §5.1`/`§6.1`, `F09 §3.1`/`§5`, `F12 §4.4` |
| SR-26 | Minor | `03 §5`, `F09 §4.4`, `02 §3` | Pre-approval server-side dry-run runs the admission chain on an unapproved model-authored payload | ACCEPT-MODIFIED — dry-run stays at propose, but only on the Guard-reconstructed payload, with the admission-chain consequence stated and the SA scoped | DR-22 (§22.4) | `02 §3`, `03 §5`, `F09 §4.4`/`§7` |
| SR-27 | Minor | `F09 §3.1`/`§8`, `03 §5`, `01 §7` | Auto-rollback specified three ways, exempt from budget, and unsafe against concurrent change | ACCEPT | DR-23 (§23.9) | `01 §7`, `03 §5`, `F09 §3.1`/`§4.2`/`§5`/`§7`/`§8` |
| SR-28 | Minor | `01 §5.1`/`§7` vs `X-SEC §3.2`/`§4.3`/`§8` | Two token models (opaque+revocable vs JWT with no revocation); audit retention 2555 d vs 365 d | ACCEPT | DR-25 (§25.3) | `01 §8`, `X-SEC §3.2`/`§4.3`/`§8` |
| SR-29 | Minor | `01 §7`/`§6.1`, `X-OPS §4.2`, `F12 §4.3` | Three metrics-port answers; the default binds `0.0.0.0` unauthenticated with tenant-labelled series | ACCEPT | DR-26 (§26.5) | `01 §6.1`/`§7`, `F12 §4.3`, `X-OPS §4.2`/`§6`/`§7` |
| SR-30 | Minor | `X-SEC §4.4`/`§6`/`§8` | STRIDE table missing eight rows; the document's own process control already violated | ACCEPT | DR-37 (§37.3) | `X-SEC §4.4`/`§6`/`§8` |

### Performance, Scalability & Data Architecture (PD-1 … PD-35)

| ID | Sev | Doc § | Issue (one line) | Disposition | DR | Owner docs |
|---|---|---|---|---|---|---|
| PD-1 | Blocker | `01 §5.1`/`§7`/`§10.2` | Hot-index sizing arithmetically wrong (~4× low) and over budget before any index amplification | ACCEPT | DR-6 (§6.4) | `01 §5.1`/`§7`/`§10.1`/`§10.2`, `F03 §3.2`/`§7` |
| PD-2 | Blocker | `01 §5.1`, `05 §D-3` | The ADR's sole justification for pure-Go SQLite ("not per-span writes") is falsified by the schema | ACCEPT-MODIFIED — all three reviewer options combined: 8-key allowlist, FTS for anomalous traces only, and ClickHouse **required** above a measured line | DR-6 (§6.2, §6.4) | `01 §5.1`/`§7`, `05 §D-3`/`§9`, `F03 §4.2`/`§4.3` |
| PD-3 | Blocker | `F03 §4.2`/`§4.3` vs `01 §10.1`/`§9` | The feature-level hot index has no span rows, so it cannot answer `SearchSpans` — the D-T1 gate | ACCEPT | DR-6 (§6.1, §6.3) | `01 §5.1`/`§9`/`§10.1`, `F03 §3.1`/`§3.2`/`§4.2`/`§4.3`/`§7` |
| PD-4 | Blocker | `F03 §4.3`/`§4.4` vs `01 §3.1`/`§5.2` | Per-trace Parquet row groups defeat encoding and make the ≥ 8× compression target unreachable | ACCEPT | DR-7 | `01 §3.1`/`§5.2`/`§10.2`, `F03 §4.3`/`§4.4`, `X-OPS §4.4` |
| PD-5 | Blocker | `01 §5.1`, `04 §1 L4`, `F03 §4.4`/`§5` | Hot/cold crash consistency broken both ways; the FK on `trace.block_id` forbids the documented write order | ACCEPT | DR-7 | `01 §5.1`/`§5.2`, `04 §1 L4`, `F03 §4.4`/`§5`/`§7` |
| PD-6 | Blocker | `F02`, `01 §3.2`, `04 §1 L2`, `X-OPS FR-XOPS-2` | Four conflicting sharding schemes; rebalance-loss bound wrong by two orders of magnitude; split traces double-count RED | ACCEPT | DR-8 | `01 §3.2`, `02 §1`, `04 §1 L2`, `F02 §3.1`/`§3.2`/`§4.3`/`§4.4`/`§7`/`§8`, `X-OPS §3.1` |
| PD-7 | Blocker | `F02 §3.2`/`§4.3` vs `01 §7`/`§10.2` | Assembly memory bound specified two ways; F02's version alone breaks the RSS ceiling | ACCEPT | DR-9 | `01 §7`/`§10.2`, `F02 §3.2`/`§4.3` |
| PD-8 | Blocker | `F02 §4.4`, `02 §1`, `01 §10.1` | Per-span SQLite baseline lookups plus a per-trace write from the shard goroutine | ACCEPT | DR-10, DR-9 | `01 §7`/`§10.1`, `02 §1`, `04 §6`, `F02 §4.3`/`§4.4`/`§7` |
| PD-9 | Blocker | `F02 §3.1`/`§4.2`/`§4.4`, `01 §7` | Rare-path rule unbounded, `max_keep_rate` unimplemented, and `PathSignature` computed from an unordered set | ACCEPT | DR-10 | `01 §4.1`/`§7`, `F02 §3.1`/`§4.2`/`§4.4`/`§7` |
| PD-10 | Blocker | `F03 §4.2`/`§4.3`/`§4.4`, `02 §5` | Cost-control loop is both an import-cycle violation and an unstable, authority-less controller | ACCEPT | DR-12, DR-2 | `01 §5.3`/`§7`/`§9`, `02 §5`, `04 §5.2`, `F02 §4.3`, `F03 §3.1`/`§4.2`/`§4.3`/`§4.4`/`§7` |
| PD-11 | Blocker | `F05 §3.2`/`§4.2`/`§4.4`, `01 §10.2` | 168-bucket t-digest baselines are ~3.4 GiB and their 60 s full checkpoint is unschedulable | ACCEPT-MODIFIED — 31 slots × a 240 B P² estimator (**119 MiB published**), global-slot t-digest only, incremental dirty-key checkpointing | DR-14 (§14.2) | `01 §7`/`§10.2`, `02 §2`, `F05 §3.2`/`§4.2`/`§4.3`/`§4.4`/`§7` |
| PD-12 | Blocker | `01 §4.7`/`§5.1`/`§7`, `F04 §3.2`/`§4.2` | Per-callee-operation edges at 10 s for 30 d are 5.2 × 10¹⁰ rows; per-bucket quantiles have no estimator | ACCEPT | DR-13 | `01 §4.7`/`§5.1`/`§7`, `F04 §3.2`/`§4.2`/`§4.3`/`§7` |
| PD-13 | Blocker | `F05 §4.4`, `F04 §4.3`, `01 §10.1` | Grouper merge is O(open incidents × services) topology queries per event, calling a method that does not exist | ACCEPT | DR-14 (§14.7), DR-13 | `01 §10.1`, `02 §2`, `F04 §4.3`, `F05 §4.3`/`§4.4`/`§7` |
| PD-14 | Blocker | `F06 §4.4`, `01 §4.4` | The token budget and the tool-result cap are mutually unsatisfiable; `max_tool_calls` is unreachable by 4–8× | ACCEPT-MODIFIED — compaction + a sliding verbatim window, and **cache reads get their own dimension** (charging them against `max_tokens_in` would make 40 calls unreachable); worked arithmetic published | DR-17 (§17.1) | `01 §4.4`/`§7`/`§10.3`, `03 §3`, `F06 §3.1`/`§3.2`/`§4.3`/`§4.4`/`§7` |
| PD-15 | Blocker | `02 §5`, `F03 §4.2`, `F08 §4.3`, `F04 §4.1`, `F07 §4.4`, `02 §3` | Six forbidden import edges, three of them cycles the class diagram declares broken | ACCEPT-MODIFIED — for PD-15(5) the fix is the consumer-declared-signature rule, not moving `Sink` into `model` (Go interfaces are structural, so no edge existed) | DR-2 | `02 §3`/`§5`, `01 §1.2`, `F01 §4.3`, `F03 §4.2`, `F04 §4.1`/`§4.4`, `F07 §4.4`, `F08 §4.3`, `X-OPS §4.3` |
| PD-16 | Blocker | `F11 §3.1`/`§3.2`/`§4.1`/`§4.4`, `01 §7` | The eval harness bypasses the system under test, cannot be deterministic, and cannot meet its CI budget | ACCEPT | DR-36, DR-31 | `01 §4.1`/`§5.1`/`§7`/`§10.3`, `02 §4`, `04 §1`, `F11 §3.1`/`§3.2`/`§4.1`/`§4.3`/`§4.4`/`§7` |
| PD-17 | Major | `04 §4`/`§6`, `01 §8.5`, `F06 FR-F06-2` | Every write funnels through one SQLite writer with no published budget — including fail-closed audit behind a 250 ms trace batch | ACCEPT | DR-6 (§6.2) | `01 §5.1`/`§8.5`/`§10.1`, `04 §4`/`§6`, `F03 §3.2`, `X-SEC §7` |
| PD-18 | Major | `01 §3.1`/`§4.1`, `F02`/`F03`/`F05 §4.2` | Four incompatible RED representations at two volumes differing by 2 400× | ACCEPT | DR-39, DR-0 | `01 §3.1`/`§4.1`/`§5.1`/`§10.1`, `F02 §4.2`, `F03 §4.2`, `F04 §4.2`, `F05 §3.1`/`§4.2` |
| PD-19 | Major | `F01 §4.2`/`§3.2`, `01 §4.1` | `F01` redefines `model.Span` incompatibly and claims zero allocations against a per-span map | ACCEPT | DR-4, DR-28 (§28.2) | `01 §4.1`/`§10.1`, `F01 §3.2`/`§4.2`/`§7` |
| PD-20 | Major | `F01 §4.3`, `01 §3.1`/`§7` | Queue sizing, overflow policy and config key shapes contradict `01`; `block` default inverts the design | ACCEPT | DR-28 (§28.1) | `01 §3.1`/`§7`, `F01 §4.3`/`§4.4`/`§7` |
| PD-21 | Major | `01 §10.1`, `F01`–`F04 §3.2`, `X-OPS §4.4` | Six mutually inconsistent throughput numbers, none derived from another | ACCEPT | DR-32 (§32.4), DR-0 | `01 §10.1`, all `F0x §3.2`, `X-OPS §4.4` |
| PD-22 | Major | `F02 §3.1`/`§4.3`, `01 §7`/`§10.1` | Assembly timeouts differ 25–100 % and break a published latency gate; `FinalizeTick` full-scans every buffer every second | ACCEPT | DR-9 | `01 §7`/`§10.1`, `02 §1`, `04 §4`, `F02 §3.1`/`§4.3`/`§4.4`/`§7` |
| PD-23 | Major | `F03 §4.4`, `01 §5.3` | Per-trace delete from immutable Parquet is infeasible; orphan GC lists the entire cold store hourly; no compaction | ACCEPT | DR-7 | `01 §5.3`, `F03 §4.4`/`§7`, `X-OPS §4.4` |
| PD-24 | Major | `F03 §4.3`, `05 §D-3` | `HotIndex` leaks SQLite semantics, so the ClickHouse conformance claim cannot hold; methods used in §4.4 are undeclared | ACCEPT | DR-6 (§6.1) | `F03 §3.1`/`§4.3`/`§4.4`/`§7`, `01 §10.1` |
| PD-25 | Major | `F02 FR-F02-5`/`§4.4`, `04 §1 L2`, `F01 §3.2` | "RED before any discard" violated on three paths, and duplicate delivery corrupts the aggregates | ACCEPT | DR-9 | `01 §3.1`, `04 §1 L2`, `F01 §3.2`, `F02 §3.1`/`§4.4`/`§7` |
| PD-26 | Major | `F02 §4.2`/`§4.3`, `01 §7` | Predicate matching is unbounded on the hot path and `Hits` is structurally always ~0 | ACCEPT | DR-11 | `01 §4.2`/`§7`, `03 §6`, `F02 §4.2`/`§4.3`/`§4.4`/`§7` |
| PD-27 | Major | `F08 §4.4`/`§3.1`/`§8`, `01 §5.1` | `Similar()`'s p99 is unsupported by the schema; no token index; consolidation is O(n²) | ACCEPT | DR-19 (§19.2) | `01 §5.1`/`§7`, `F08 §3.1`/`§4.3`/`§4.4`/`§7`/`§8` |
| PD-28 | Major | `F06 §3.2`/`§8`, `01 §7`, `F05 §4.4` | Concurrency specified 10× apart; duplicate incidents investigated N times; `dedupe_ttl`/`SuppressedBy` unimplemented | ACCEPT | DR-17 (§17.3), DR-14 (§14.7) | `01 §4.3`/`§7`/`§10.2`, `04 §4`, `F05 §4.4`, `F06 §3.2`/`§8` |
| PD-29 | Major | `F03 §3.2`, `01 §5.1`, `X-OPS FR-XOPS-5` | Durability claim does not match the configured PRAGMAs; the backup design conflicts with the checkpoint policy | ACCEPT | DR-6 (§6.5), DR-33 (§33.3) | `01 §5.1`, `F03 §3.2`/`§7`, `X-OPS §3.1`/`§3.2` |
| PD-30 | Major | `X-OPS §4.1`/`§4.4`, `F08 §3.2` | Capacity table 25–90× off and not derivable; prod topology contradicts two features with Postgres+pgvector | ACCEPT | DR-32 | `01 §10.1`, `05 §8`, `X-OPS §4.1`/`§4.2`/`§4.4`/`§7`, `F08 §3.2` |
| PD-31 | Minor | `X-OPS §4.2`/`§4.3`, `F09 §4.2`, `02 §5` | `TenantPolicy` declared twice; the `ops` package exists in no package map or graph | ACCEPT-MODIFIED — a dedicated `internal/tenant` package, not `model`+`auth` (Appendix B) | DR-3, DR-2 | `01 §1.2`/`§4`/`§5.1`, `02 §4`/`§5`, `F09 §4.2`, `X-OPS §4.2`/`§4.3` |
| PD-32 | Minor | `F05`, `01 §4.3`/`§7`/`§10.1` | Detector math, severity model and schema specified twice and differently; detection latency floor exceeds its gate | ACCEPT-MODIFIED — F05's ratio+absolute-delta **trigger** is adopted into `01` and the score is derived from it; window and debounce re-derived to meet ≤ 90 s | DR-14 (§14.4, §14.5, §14.8) | `01 §4.3`/`§5.1`/`§7`/`§10.1`, `F05 §3.1`/`§4.2`/`§4.4`/`§7` |
| PD-33 | Minor | `F04 §5`/`§4.3`, `02 §2` | `Changes()` drop-oldest permanently loses a `NewEdge` detection; two designs coexist | ACCEPT | DR-13, DR-14 (§14.7) | `02 §2`, `F04 §4.3`/`§5`/`§7` |
| PD-34 | Minor | `01 §4.7`/`§5.1`, `F03`/`F04`/`F05` | Stored quantile sets disagree (p90 vs p95) — a schema change, not a naming nit | ACCEPT | DR-39 (§39.2), DR-4 | `01 §4.7`/`§5.1`/`§7`, `F03 §4.2`, `F04 §4.2`, `F05 §3.1` |
| PD-35 | Minor | `F08 §4.4`, `01 §4.6`/`§5.3` | Retrieval-weight semantics inverted between docs; retention differs; no `tenant_id` | ACCEPT | DR-19 (§19.4), DR-5 | `01 §4.6`/`§5.3`/`§7`, `F08 §3.2`/`§4.2`/`§4.4`/`§7`/`§8` |

### Completeness, Traceability & Design Coherence (CC-1 … CC-33)

| ID | Sev | Doc § | Issue (one line) | Disposition | DR | Owner docs |
|---|---|---|---|---|---|---|
| CC-1 | Blocker | `F03 §4.2`, `F08 §4.3`, `F04 §4.1`/`§4.4` vs `02 §5` | Three feature docs declare types creating the exact import cycles `02 §5` forbids and CI is specified to fail on | ACCEPT | DR-2 | `02 §3`/`§5`, `01 §1.2`, `F03 §4.2`, `F04 §4.1`/`§4.4`, `F08 §4.3` |
| CC-2 | Blocker | `01 §1.1 P6`/`§9`, `03 §6`, `04 L7` vs `F06 FR-F06-12` | The wedge differentiator (D-X5) is specified two incompatible ways with opposite retention profiles | ACCEPT — **both phases**, each with its own FR (12a scope, 12b recurrence) | DR-11 | `01 §4.2`/`§7`, `02 §1`, `03 §6`, `04 §1 L7`, `F02 §4.2`/`§4.3`/`§4.4`/`§6`/`§7`, `F06 §3.1`/`§4.4`/`§7` |
| CC-3 | Blocker | `01 §7`/`§9`, `02 §4`, `F05 FR-F05-11`, `F12 FR-F12-8` | "When does a human get paged" has three answers; two break the PRD contract | ACCEPT-MODIFIED — one rule, three conditions; the severity/tier bypass is deleted outright rather than converted to a budget override | DR-21 | `01 §7`/`§9`/`§10.3`, `02 §4`, `04 §5.2`, `F05 §3.1`, `F12 §3.1`/`§4.3`/`§7` |
| CC-4 | Blocker | `01 §4.4`/`§6.1`, `02 §3`, `03 §7` vs `F06` | D-Y1's replay is specified everywhere except the owning feature doc, which denies it is possible | ACCEPT-MODIFIED — replay never re-invokes the model, so nondeterminism is out of the path; `ReplayReReason` is a Phase 3 deferral | DR-18, DR-15 | `01 §4.4`/`§5.1`/`§6.1`, `02 §3`, `03 §7`, `F06 §3.1`/`§4.2`/`§4.3`/`§4.4`/`§5`/`§7`, `F12 §3.1`/`§4.3`/`§7` |
| CC-5 | Blocker | `F05 §4.2` vs `01 §4.3`/`§7` | `anomaly.Event`/`Incident` lack `Score`, `Severity`, `Fingerprint`, `EpicenterService`, `BlastRadius` — every downstream gate consumes exactly those | ACCEPT | DR-14 (§14.5) | `01 §4.3`/`§5.1`, `02 §2`, `03 §2`, `F05 §4.2`/`§4.3`/`§7` |
| CC-6 | Blocker | `F09 FR-F09-9`/`§4.4` vs `05 D-13`/`§8`, `02 §3` | The only write path into customer infrastructure is unbuildable under the stated deployment constraints | ACCEPT-MODIFIED — **`client-go` rejected**; the image is fixed instead (Appendix B) | DR-24, DR-22 | `05 §D-13`/`§8.1`/`§8.2`, `02 §3`, `F09 §3.1`/`§4.4`/`§6`/`§7`, `X-OPS §6` |
| CC-7 | Blocker | `X-OPS §4.1` vs `01 §5.1`, `02 §3`, `F08 §3.2`, `05 §8` | An external Postgres+pgvector appears in the prod topology, contradicting four documents and D-J5 | ACCEPT | DR-32 (§32.1) | `05 §8`, `X-OPS §4.1`, `F08 §3.2` |
| CC-8 | Blocker | `X-OPS §4.3` vs `01 §1.2`, `02 §5`, `F01 §4.3` | A `package ops` exists in no package map or dependency graph, yet owns `TenantPolicy`; two `TenantResolver`s | ACCEPT-MODIFIED — a dedicated `internal/tenant` package rather than `model`+`auth` (Appendix B) | DR-3, DR-2 | `01 §1.2`/`§4`/`§5.1`, `02 §4`/`§5`, `00`, `F01 §4.3`, `X-OPS §4.2`/`§4.3` |
| CC-9 | Major | `01 §9`/`§5.1`/`§7`/`§10.1` vs `F03` | D-T1 is the Tempo-killer claim and the owning feature doc implements none of it | ACCEPT | DR-6 (§6.1–§6.3) | `01 §5.1`/`§7`/`§9`/`§10.1`, `F03 §3.1`/`§3.2`/`§4.2`/`§4.3`/`§7` |
| CC-10 | Major | `F05 §3.1`/`§4.2` vs `01 §4.3`/`§7`, `02 §2` | Detector sets disagree: no throughput-drop detector, and a `deploy_regression` kind that exists in no enum or schema | ACCEPT — throughput-drop gets an FR; `deploy_regression` is demoted to an enrichment | DR-14 (§14.4, §14.6) | `01 §4.3`/`§7`, `02 §2`, `03 §2`, `F05 §3.1`/`§4.2`/`§4.4`/`§7` |
| CC-11 | Major | `01 §7`/`§10` vs nine documents; `05 D-9` | Twenty-plus numeric contradictions, each stated somewhere as an acceptance gate | ACCEPT — `01 §7` owns every default, `01 §10` every gate, enforced by a generated-and-diffed `defaults.md` | DR-0, DR-10, DR-34, DR-39, DR-38 (§38.4) | `01 §7`/`§10`, every `F0x §3.2`/`§4.3`, `05 §D-9`, `docs/architecture/defaults.md` (new) |
| CC-12 | Major | `01 §4.6`, `03 §7` vs `F08 FR-F08-4`/`§4.4` | The learning flywheel is inverted: F08 *boosts* the record an engineer just said was wrong | ACCEPT | DR-19 (§19.4) | `01 §4.6`/`§7`, `03 §7`, `F08 §3.1`/`§4.2`/`§4.4`/`§7` |
| CC-13 | Major | `01 §6.1`/`§6.2` vs seven feature docs | The REST surface is defined twice and never the same way; MCP is 13 tools vs 5 | ACCEPT-MODIFIED — one `/v1` table, and **12** MCP tools (propose-action withheld from MCP in v1) | DR-29, DR-0 | `01 §6.1`/`§6.2`, `F04`–`F12 §4.3`, `X-SEC §5` |
| CC-14 | Major | `01 §9`/`§4.5`/`§8.2`, `03 §5` vs `F09` | F09 drops approval TTL, separation of duty and target re-resolution, and inverts the approve/execute split | ACCEPT | DR-23, DR-22 (§22.3) | `01 §4.5`/`§7`/`§8.2`, `03 §5`, `04 §5.2`, `F09 §3.1`/`§4.2`/`§4.3`/`§4.4`/`§5`/`§7` |
| CC-15 | Major | `F04 §1`/`§4.3` vs `01 §2`, `03 §1`, `02 §2` | Topology has two different inputs, and its interface drops `Distance`, `Flush` and `Neighbors`' parameters | ACCEPT — F04's ingest-fan-out input wins; `01 §2` and `03 §1` are corrected | DR-13 | `01 §2`/`§4.7`, `02 §2`, `03 §1`, `04 §X6`, `F04 §1`/`§3.2`/`§4.1`/`§4.3`/`§7`, `F05 §4.3` |
| CC-16 | Major | `F06 §4.3` vs `01 §6.3`/`§8.6`, `05 §10` | `ToolArgs.Query` re-opens the injection boundary the ADR declares binding | ACCEPT | DR-16 | `01 §6.3`/`§8.6`, `02 §3`, `F06 §4.3`/`§4.4`/`§6`/`§7` |
| CC-17 | Major | `F10 §4.2`/`§4.4` vs `02 §4`, `03 §4` | Two disjoint intent taxonomies, and F10 bypasses `rca.ToolRegistry`, breaking D-X3's single-evidence-path property | ACCEPT — the union of ten intents, closed; all NL data access via the registry | DR-35 | `02 §4`, `03 §4`, `F05 §4.3`, `F10 §3.1`/`§4.2`/`§4.3`/`§4.4`/`§7`/`§8` |
| CC-18 | Major | `F11 §4.4` vs `01 §4.4`/`§5.1`/`§10.3`, `02 §4`, `F12 §4.4` | The eval scorer cannot compile against the types it scores, and its metrics do not round-trip | ACCEPT | DR-36 (§36.5), DR-15 | `01 §4.4`/`§5.1`/`§10.3`, `02 §4`, `F11 §3.1`/`§4.4`/`§7`, `F12 §4.4` |
| CC-19 | Major | `X-SEC §3.1`/`§4.2`/`§4.3` vs `01 §8.1`–`§8.5`, `02 §4` | Two identity models, and a linear role hierarchy that silently lets every approver propose | ACCEPT — capability matrix, not a hierarchy | DR-25 (§25.2, §25.3) | `01 §8`, `02 §4`, `X-SEC §3.1`/`§3.2`/`§4.2`/`§4.3`/`§4.6`/`§8`, `F12 §4.2` |
| CC-20 | Major | `PRD §2`/`§4`/`§5`/`§6`/`§7`/`§8` vs feature FR sets | Six PRD promises have no owning FR anywhere | ACCEPT — three owned with new FRs, three deferred to Phase 3 in writing; the D-T3 secondary clause is struck from `01 §9` | DR-38 (§38.1) | `00`, `01 §7`/`§9`, `F04 §3.1`, `F07 §3.1`, `F08 §4.2`, `05 §8` |
| CC-21 | Major | `F12 §4.3` vs six feature docs | The six screens do not cover eval, cost/retention, memory, baselines, sampler state or audit verification — three of them the only UI for a `D-*` claim | ACCEPT — nine screens, with a docs-CI rule binding each `D-*` row to a screen | DR-30 | `01 §9`, `F12 §3.1`/`§4.2`/`§4.3`/`§7`/`§8` |
| CC-22 | Major | `05 D-5`, `01 §9` D-Z3 vs `F02 FR-F02-9`/`§5` | D-Z3's fix is stated as durability; the owning feature doc accepts losing every in-assembly trace | ACCEPT — option (a): the spill WAL is specified and the claim is kept | DR-9 | `01 §9`/`§10.3`, `05 §D-5`, `F02 §3.1`/`§4.3`/`§4.4`/`§7`, `X-OPS §3.2` |
| CC-23 | Major | `01 §9` D-D1, `§5.3`, `§7` vs `F03`, `F06` | Both halves of the cost-linearity mechanism are missing from their owning docs | ACCEPT | DR-12, DR-17 | `01 §5.3`/`§7`/`§9`, `F03 §3.1`/`§4.2`/`§4.3`/`§7`, `F06 §3.1`/`§4.3`/`§7` |
| CC-24 | Minor | `F12 §4.2` vs `§4.3` | `type Server struct` and `type Server interface` in the same package — a compile error | ACCEPT | DR-30, DR-0 | `02 §4`, `F12 §4.2` |
| CC-25 | Minor | `01 §10.2` vs `F03 FR-F03-7` | `store_hot_index_ratio` has two different denominators, differing by an order of magnitude | ACCEPT — denominator is **raw ingested** span bytes, defined once | DR-6 (§6.4) | `01 §10.2`, `F03 §3.1`/`§7` |
| CC-26 | Minor | `02 §1` vs `F02 §4.3`, `04 §4`/`§X4` | Method-set mismatch on the catalog's own `Sampler` interface; F02 has no channels for `04`'s backpressure contract | ACCEPT | DR-10 | `00`, `02 §1`, `04 §4`/`§X4`, `F02 §4.3` |
| CC-27 | Minor | `F01 §4.2`/`§4.3` vs `01 §4.1`, `02 §1` | `internal/model` — the bottom of the graph — is defined twice, and four load-bearing fields are missing | ACCEPT | DR-4 | `01 §4.1`, `02 §1`, `F01 §4.2`/`§4.3` |
| CC-28 | Minor | `F05 §4.4`/`§8`, `F06 §8`, `01 §6.3` | The deploy-window abstraction is duplicated between F05 and F06; both flag canary delivery as unsolved | ACCEPT — one owner, `anomaly.DeployIndex.PrePostSplit`; canary is a written Phase 3 deferral | DR-14 (§14.6) | `00`, `01 §6.3`, `02 §2`, `F05 §4.4`/`§8`, `F06 §4.4`/`§8` |
| CC-29 | Minor | nine docs' `§7` | 33 FRs with no AC, and AC ID sequences that skip numbers | ACCEPT — a generated traceability matrix, CI-failing on any FR with zero ACs | DR-38 (§38.4) | every feature doc `§7`, `docs/architecture/traceability.md` (new) |
| CC-30 | Minor | `F05 FR-F05-11`, `F06 §3.2`, `F08 FR-F08-4`, `F11 FR-F11-6` | Four requirements are not testable as written ("tier-0", "a dollar ceiling in config", "fingerprint family", "expected evidence categories") | ACCEPT — one deleted with its requirement, three given named fields or config keys | DR-38 (§38.3), with DR-17, DR-19, DR-21, DR-36 | `01 §4.1`/`§7`, `F05 §3.1`, `F06 §3.2`, `F08 §3.1`, `F11 §3.1`/`§4.3` |
| CC-31 | Minor | `X-OPS §4.1`/`§4.2` vs `01 §3.2`, `04 §2` | Two deployment vocabularies, and DaemonSet vs HPA'd Deployment is a real topology difference | ACCEPT — one `server.mode` axis plus `server.profile`; `collector` → `gateway`, Deployment+HPA | DR-26 (§26.1), DR-32, DR-33 | `01 §3.2`/`§7`, `04 §2`, `X-OPS §4.1`/`§4.2`/`§4.4` |
| CC-32 | Minor | `F02 §3.2`/`§4.3`/`§4.4` vs `01 §3.2` | HRW routing and Kafka's murmur2 partitioner are different assignment functions, breaking the Determinism NFR | ACCEPT — a custom partitioner computes `HRW → partition index` | DR-8 | `01 §3.2`, `F02 §4.3`/`§4.4`/`§7`/`§8` |
| CC-33 (1) | Minor | `F02 §8` | Kafka vs in-process default unresolved | ACCEPT — in-process ring is the default in **every** mode, including Kubernetes | DR-38 (§38.2), DR-32 | `F02 §8`, `01 §7`, `05 §D-5` |
| CC-33 (2) | Minor | `F08 §8` | `tenant_id` in the memory fingerprint unresolved | ACCEPT — mandatory, and **inside the hash preimage**, so cross-tenant matching is structurally impossible | DR-5, DR-19 (§19.1) | `F08 §8`/`§4.2`, `01 §5.1` |
| CC-33 (3) | Minor | `F03 §8` | Two-phase commit vs orphan reconciliation unresolved | ACCEPT — reconciliation, bounded by `FR-F03-14` and an AC | DR-7 | `F03 §8`/`§3.1`/`§7` |
| CC-33 (4) | Minor | `F04 §8` | Edge-sharding key unresolved | ACCEPT — `hash(caller, callee)` with the callee reverse index | DR-13 | `F04 §8` |
| CC-33 (5) | Minor | `F03 §8` | Exemplar selection unresolved | ACCEPT — per `(service, operation, bucket)` first-wins reservoir of 4; ties by lowest `TraceID` so replay is deterministic | DR-38 (§38.2) | `F03 §8`, `01 §5.1` |
| CC-33 (6) | Minor | `X-OPS §8` | `PolicyStore` backend unresolved | ACCEPT — `control.db` `tenant.policy_json`; no new datastore | DR-3 | `X-OPS §8`, `01 §5.1` |

---

## Coverage and outcome

| Measure | Result |
|---|---|
| Findings filed | **98** (SR 30 · PD 35 · CC 33) |
| Mapped to a binding decision | **98 / 98 (100 %)** |
| ACCEPT | 84 |
| ACCEPT-MODIFIED | 14 |
| REJECT | **0** |
| Decisions issued | **40** (DR-0 … DR-39) |
| Unmapped / deferred without a decision | **none** |

Every ACCEPT-MODIFIED carries its rationale in its row and in the register's **Appendix B** ("Decisions that overrule a reviewer"), so a fix agent does not "correct" a deliberate departure back to the reviewer's original wording.

### The six Weak differentiators, closed

CC's drawback-traceability matrix rated 8 of 29 drawbacks **Weak**, and observed that the four the PRD leans on hardest were all Weak for the same reason. Each now has a single decision:

| Drawback | Was | Decision |
|---|---|---|
| D-Z3 (durable from the first write) | Weak | DR-9 — the spill WAL is specified; the claim is kept and testable |
| D-T1 (fast indexed attribute search) | Weak | DR-6 — span rows, `attr_index`, an 8-key allowlist, a covering index per listed query |
| D-D1 (cost visible and capped) | Weak | DR-12 (disk) + DR-17 (LLM spend, with startup reconciliation of the cap) |
| D-D4 (investigation-gated paging) | Weak | DR-21 — one rule, three conditions, plus a deadman |
| D-Y1 (replayable RCA) | Weak | DR-18 — step persistence contract and two replay modes |
| D-Y4 (exportable learning) | Weak | DR-13 (topology export) + DR-18/DR-29 (investigation export) + DR-19 (byte-reproducible memory export) |
| D-X4 (guardrailed remediation) | Weak | DR-22 + DR-23 + DR-24 |
| D-X5 (sampler/agent loop) | Weak | DR-11 — both phases, with the `max_keep_rate` circuit breaker and narrowing |

### What round 2 must verify

1. `00`, `01`, `02` updated to the register **first**, as one atomic change; then the feature docs rewritten against them.
2. `internal/archtest` green on all four enforced rules: DR-2 adjacency, DR-5 tenant parameter, DR-31 time/rand ban, DR-20 egress-client ban.
3. The eight SR re-review items (SR-1, -2, -3, -4, -5, -6, -7, -23) confirmed by reading `F09 §4.2`/`§4.4`, `X-SEC §4.6`, `01 §7` and `F03 §4.4` directly.
4. `defaults.md` and `traceability.md` generated and diffed in CI (DR-38 §38.4); no FR without an AC.
5. Every `D-*` row in `01 §9` names an FR ID and, where user-visible, an `F12` screen.
6. `govulncheck ./...` reports **zero** reachable vulnerabilities with the analyser-ran assertions present (DR-1).

---

## Round 1 status

> **Round 1 status: fix wave dispatched — 0/98 verified.**
>
> *(The brief's "79" is superseded by the filed count of 98; see the Count note above.)*
>
> Verification is a **round-2** activity. A finding moves from *dispatched* to *verified* only when a reviewer confirms the changed text in the owner documents named in its row — not when the decision is written. Round 2 opens when items 1–6 above are all green.

| Round | Date | Verdict | Findings | Verified |
|---|---|---|---|---|
| 1 | 2026-09-14 | REJECT (2 of 3 lenses); APPROVE-WITH-CHANGES (Security & Reliability) | 98 | **0 / 98** |
| 2 | *pending* | *pending* | — | — |

