# TraceIQ — Round 2 Verification (fix-wave audit)

**Round:** 2 — verification gate
**Date:** 2026-09-15
**Board:** Architecture Review Board
**Scope:** all 98 round-1 findings (SR-1…30, PD-1…35, CC-1…33), each traced to its owning DR in
[`../../architecture/06-decision-register.md`](../../architecture/06-decision-register.md) Appendix A, and verified
against the *content* of the documents the DR's "Docs to change" line names — not against revision headers.
Go scaffold verified by build/vet/test plus canonical-name grep.

**Method.** Appendix A + Appendix B read in full as the checklist. For each finding, the decided artefact
(exact interface name, config key, numeric value, DDL column, state table, FR/AC ID) was grepped in the
owning document. Every Blocker and Major was content-verified. Minors were content-verified where the DR
names a concrete artefact and grep-sampled otherwise. Where a deleted term still appears, the hit was read
in context to confirm it is a *deletion statement* rather than a survival.

**Scaffold gate.**

```
GOTOOLCHAIN=go1.27.1 CGO_ENABLED=0 go build ./...   # PASS (exit 0)
GOTOOLCHAIN=go1.27.1 CGO_ENABLED=0 go vet ./...     # PASS (exit 0)
GOTOOLCHAIN=go1.27.1 CGO_ENABLED=0 go test ./...    # PASS (archtest: TestPackageAdjacency + ScopeCoverage + TestCmdImports)
```

Canonical names confirmed present, one per owning package: `store.HotIndex`, `store.ColdStore`,
`store.CostSignal`, `sampler.Sampler`, `sampler.ShardFor`, `sampler.InterestPredicate`, `topology.Graph`,
`anomaly.QuantileEstimator`, `rca.Journal`, `rca.ToolArgs`, `remediate.Guard`, `k8s.Executor`,
`k8s.BuildArgv`, `tenant.Resolver`, `memory.Fingerprint`, `nl.IntentKind`, `eval.Runner`, `model.Clock`,
`model.REDSample`, `model.ActionProposal`. `internal/ops` does not exist (DR-2/DR-3). `internal/depstub`
and `internal/archtest` are excluded from the adjacency graph with an in-code citation — acceptable tooling.

---

## Verdict

> ## **NEEDS-FIX-ROUND**
>
> Zero findings are NOT-ADDRESSED and nothing regressed. The register's decisions landed, largely verbatim,
> across all 21 documents and the scaffold. But **nine findings are only PARTIAL**, and the fix wave itself
> introduced or left **five Major-severity contradictions** (N2-1 … N2-5), four of them inside documents the
> register declares the sole owner of the fact in question. These are small, surgical, and all in one
> direction: *sections the register's "Docs to change" lines did not name were never swept, even when they
> restate the same fact.*

The residue is concentrated in four places: `05 §6` (the `D-*` decision records), `F01 §3.2`/`§7`,
`05 §D-13`'s pin table, and the PRD. None of it is a design defect; all of it is a document that now
contradicts the register or itself.

---

## Summary counts

| Status | SR | PD | CC | **Total** |
|---|---:|---:|---:|---:|
| **ADDRESSED** | 26 | 33 | 30 | **89** |
| **PARTIAL** | 4 | 2 | 3 | **9** |
| **NOT-ADDRESSED** | 0 | 0 | 0 | **0** |
| Total | 30 | 35 | 33 | **98** |

PARTIAL findings by round-1 severity: **1 Blocker** (CC-6), **7 Major** (SR-8, SR-15, SR-22, SR-23, PD-20,
PD-21, CC-11), **1 Minor** (CC-29).

New round-2 findings: **5 Major, 6 Minor, 0 Blocker** (N2-1 … N2-11).

---

## 1. Security & Reliability (SR-1 … SR-30)

| ID | DR | Status | Evidence | Note |
|---|---|---|---|---|
| SR-1 | DR-22 | ADDRESSED | `01 §4.5` `ActionRollbackDeployment`…`RemoveIstioFaultSpec`, `TargetKind`, `ResolvedUID`; `F09 §6` "No model-authored string is ever passed to the cluster"; `F09` FR-F09-15/-16/-17 + AC-F09-14/-15/-16 | `Params map[string]string` survives in `01` only as three explicit deletion statements (`§4.1` l.632, `§5.1` l.1842, `§8.6(6)` l.3075) |
| SR-2 | DR-25 §25.4 + DR-23 §23.7 | ADDRESSED | `X-SEC §4.6` (new) + `identity_binding` DDL in `01 §5.1`; `403 identity_not_bound`; AC-XSEC-14/-15; `02 §4` `auth_IdentityStore` | "signature-verified, no RBAC role" purged from `X-SEC`; `F10`/`F12` retain the string only inside the sentence recording its deletion |
| SR-3 | DR-26 | ADDRESSED | `01 §7` `api.endpoint: 127.0.0.1:8443`, `ingest.otlp_grpc.endpoint: 127.0.0.1:4317`, `ingest.auth` block, `selfobs.metrics_endpoint: 127.0.0.1:9464`; ten validation rules with `exit 2` (×13) | |
| SR-4 | DR-23 §23.2–§23.5 | ADDRESSED | `F09 §4.2` nine states incl. `Expired`; 9×9 matrix; AC-F09-17 (81 pairs), AC-F09-18; `01 §7` `auto_execute_on_approve: false` | |
| SR-5 | DR-23 §23.6 | ADDRESSED | `F09 §6` budget-counts-attempted invariant; `OverrideJustification` (×6); AC-F09-19/-20/-21/-22 | |
| SR-6 | DR-5 | ADDRESSED | `01` 21× `tid model.TenantID` in signatures; `archtest` AST rule cited; `tenancy.header` present only as "does not exist as a config key" (`01 §3.2` l.386) | |
| SR-7 | DR-7 | ADDRESSED | `01 §5.1` `cold_state`/`wal_segment`, FK dropped; `01 §5.2` invariant + 6-row crash matrix; `F03 §4.3` `ReadFromWAL`; `F03 §8` marked resolved | Appendix B's ACCEPT-MODIFIED (pointer post-seal, row at ack) is reproduced in `F03 §8` |
| SR-8 | DR-28 §28.1 | **PARTIAL** | FR-F01-6 rewritten (`capacity: 1024`, `max_bytes: 268435456`, `overflow_policy: shed`); `§4.4` `blockingPush` deleted — **but** `F01 §7` AC-F01-9 still reads "Load test sustains 50k spans/sec, p99 < 50ms, < 200MB receiver RSS" | DR-28 mandates AC-F01-9 = *2× ingest capacity ⇒ 429s rise, RSS under `01 §10.2`, bounded goroutines, zero handlers blocked > `enqueue_timeout`*. → **N2-2** |
| SR-9 | DR-9 | ADDRESSED | `01 §7` `sampler.wal` block; `F02` FR-F02-9 rewritten, AC-F02-11, `traces_lost_total`; `05 §D-5` D-Z3 retained | |
| SR-10 | DR-10 + DR-11 + DR-12 | ADDRESSED | `01 §7` `max_keep_rate: 0.25`, `slow_min_duration`, `rare_path_keeps_per_min`, `max_path_signature_cardinality`; `F03` `shed_sampled` ladder | |
| SR-11 | DR-27 | ADDRESSED | `X-SEC §4.2` `traceiq.audit.v1` + `LP()` + RFC 8785; `audit_anchor` DDL in `01 §5.1`; Ed25519 anchor; AC-XSEC-16…19 | `F09 FR-F09-8` cites rather than restates |
| SR-12 | DR-16 + DR-15 + DR-17 | ADDRESSED | `01 §6.3` typed `ToolArgs` + 10-template allowlist; `F06 §4.3` interface block replaced; `ToolArgs.Query` appears only in its deletion sentence | |
| SR-13 | DR-19 §19.5 + DR-37 + DR-5 | ADDRESSED | `01 §4.6` `Provenance`/`TrustTier`; `01 §8.6(2)` widened to "not authored by TraceIQ"; AC-F06-21; `X-SEC §4.4` memory-poisoning row | |
| SR-14 | DR-21 | ADDRESSED | `01 §7 alerting` P1/P2/P3 + `deadman`; `02 §4` AlertRouter note corrected; AC-F12-14/-15 | |
| SR-15 | DR-24 | **PARTIAL** | `internal/k8s` in `01 §1.2`/`02 §3`; `BuildArgv`, `MinimalEnv`, `ErrForbiddenFlag`, `--patch-file=/dev/stdin`, denylist all in `F09 §4.4` — **but** `05 §D-13`'s pinned version, `sha256` digest and CVE-tracking owner are all **TBD**, and `X-OPS §6` states no image contents | DR-24 requires all three recorded in D-13; `X-OPS §6` is named in its Docs-to-change line. → **N2-3**, **N2-11** |
| SR-16 | DR-36 §36.7 + DR-24 | ADDRESSED | `F11 §6` faults through `remediate.Guard`, `EvalNamespaceAllowlist`, `i-know-this-mutates-a-cluster`, AC-F11-14; FR-F11-3 deleted | |
| SR-17 | DR-33 §33.1 | ADDRESSED | `04 §3` six-condition `/readyz`, never probes object storage; AC-XOPS-11/-12 | |
| SR-18 | DR-26 §26.1 | ADDRESSED | `X-OPS §4.2` l.242 "`ops.mode`, `ops.role` … are **deleted**"; `F01 §6` l.318 "`deploy.mode` does not exist in `01 §7`'s schema" | |
| SR-19 | DR-29 + DR-25 | ADDRESSED | `01 §6.1` single `/v1` table incl. 10 new rows; `01 §6.2` twelve tools; `F12` `MinRole` per tool; no `/api/v1/` anywhere in `F12` | |
| SR-20 | DR-26 §26.4 | ADDRESSED | `01 §8.4` limits table, `max_attribute_value_bytes: 4096`, `oversize_attr: truncate\|reject`; 8 KiB only in its deletion note | |
| SR-21 | DR-20 + DR-5 | ADDRESSED | `01 §7 correlate` `tenant_mode`, `allow_private_networks: false`, `max_inflight`, `breaker_*`; `F07` FR-F07-6/-7/-8 + AC-F07-6/-7/-8 + `EgressDialer` + `169.254.169.254` | |
| SR-22 | DR-34 + DR-17 §17.4 | **PARTIAL** | `01 §7` `model: claude-opus-5`, `effort`, `pricing` block; `01 §10.3` cost row carries the DR-34 §34.3 formula verbatim; daily caps present — **but** the PRD ROI row was never recomputed | `Tracing-Bot-PRD.md` mtime 2026-08-31 22:20: untouched by the fix wave. DR-34's Docs-to-change names "`PRD` ROI row (recomputed)". → **N2-5** |
| SR-23 | DR-1 | **PARTIAL** | `05 §7.1` historical, `§8.1` WITHDRAWN, `§8.2` normative Set B, `§9` FU-1/-2 CLOSED + FU-7/-8/-9, `§10` "never `auto`, never `local`", `§12`, `01` header, `docs/ledger.md` — **but** `05 §6 D-12` still asserts `GOTOOLCHAIN=local` "**Enforced in CI**" and govulncheck "blocking with a documented, expiring allowlist", with Set-A analyser pins | Direct self-contradiction inside the document DR-0 makes the sole owner of toolchain facts. → **N2-1**, **N2-6** |
| SR-24 | DR-4 + DR-0 | ADDRESSED | `01 §4.1` canonical block; `F01 §4.2`/`F02 §4.2`/`F06 §4.2` duplicate blocks deleted (no `type Span struct`/`type Event struct` survives in a feature doc) | |
| SR-25 | DR-23 §23.8 | ADDRESSED | `01 §5.1` `idempotency` DDL; `01 §6.1` `Idempotency-Key` required (×8); `F12 §4.4` records `X-Slack-Request-Id` as non-existent; AC-F09-23 | |
| SR-26 | DR-22 §22.4 | ADDRESSED | `F09 §4.4` RiskTier table + three dry-run conditions; AC-F09-16 asserts count and reconstructed-payload property | |
| SR-27 | DR-23 §23.9 | ADDRESSED | `F09 §5` `concurrent_modification`; `01 §7` `auto_rollback: true`; `03 §5` rollback conditional; `F09 §8` Decided | |
| SR-28 | DR-25 §25.3 | ADDRESSED | `X-SEC §3.2` "opaque `tiq_<tokenID>_<secret>` … argon2id … there is no JWT model (DR-25 §25.3)"; audit retention 2555 d | |
| SR-29 | DR-26 §26.5 | ADDRESSED | `01 §7` one metrics default `127.0.0.1:9464`; `X-OPS §6` tenant-sensitive-labels note; AC-XOPS-10 | |
| SR-30 | DR-37 §37.3 | ADDRESSED | `X-SEC §4.4` ten rows incl. `store`/`sampler`/`memory`/`correlate`/`eval`/deploy-webhook/`topology`/chat-identity/`k8s`/`rca`; `§6` mechanical docs-CI gate | |

## 2. Performance, Scalability & Data Architecture (PD-1 … PD-35)

| ID | DR | Status | Evidence | Note |
|---|---|---|---|---|
| PD-1 | DR-6 §6.4 | ADDRESSED | `01 §5.1` derived bytes/row table, 750 B/kept span, 9.34 GiB hot total, `max_disk_bytes: 26843545600` | |
| PD-2 | DR-6 §6.2,§6.4 | ADDRESSED | `01 §7` 8-key `indexed_attribute_keys` allowlist; FTS anomalous-only; `max_kept_spans_per_sec` exit-2 rule; `05 §D-3` rationale rewritten ("the hot index **does** perform a per-kept-span write") | |
| PD-3 | DR-6 §6.1,§6.3 | ADDRESSED | `01 §5.1` `span` rows + `attr_index`/`attr_dict` DDL; `§6.3` covering-index-per-query table; `F03 §4.3` `SearchSpans` | |
| PD-4 | DR-7 | ADDRESSED | `01 §5.2` time-bucketed blocks, no per-trace row group; `F03 §4.3` `Seal`/`SealDue` | |
| PD-5 | DR-7 | ADDRESSED | `01 §5.1` `block_id TEXT` with FK dropped; crash matrix; `04 §1 L4` + `BindColdBlock` | |
| PD-6 | DR-8 | ADDRESSED | `01 §3.2` `ShardFor` HRW; `04 §1 L2` — `fnv64a` fully removed; `F02 §3.2` corrected `1/(N+1)` NFR; `X-OPS` FR-XOPS-2 cites `ShardFor` | |
| PD-7 | DR-9 | ADDRESSED | `01 §7` `memory_high_watermark_bytes: 536870912` global; `01 §10.2` 1 500 MiB component derivation; per-shard key present only as its deletion note | |
| PD-8 | DR-10 + DR-9 | ADDRESSED | `F02 §4.3` `BaselineSnapshot` cached, zero SQLite reads in `Evaluate`; AC-F02-12 | |
| PD-9 | DR-10 | ADDRESSED | `F02 §4.4` `PathSignature` = pre-order DFS over ordered edges; AC-F02-14; rare-path token bucket | |
| PD-10 | DR-12 + DR-2 | ADDRESSED | `01 §5.3` `CostSignal` + deadband/max_step/recovery_ramp/settling_target; `02 §5` `store` never imports `sampler` | |
| PD-11 | DR-14 §14.2 | ADDRESSED | `F05 §3.2` "≈ 120 MiB … 288 B/key/season-slot × 31 slots … ≈ 9 640 B/key"; `anomaly.P2Estimator`; incremental `checkpoint_max_rows: 2000` | |
| PD-12 | DR-13 | ADDRESSED | `01 §4.7` `Edge` drops `CalleeOperation`; `model.LatencyHist`; `topology_edge_op` hourly top-N table | |
| PD-13 | DR-14 §14.7 + DR-13 | ADDRESSED | `F05 §4.4` `byService` index + `neighborCache` LRU; AC-F05-14 | |
| PD-14 | DR-17 §17.1 | ADDRESSED | `01 §4.4` token table verbatim: 97 920 uncached, 541 432 cache reads, `max_tokens_out: 64000`, `verbatim_digest_window: 3`, `compacted_verdict_tokens: 60`, `effective_max_tool_calls` | |
| PD-15 | DR-2 | ADDRESSED | `02 §5` adjacency replaced; `02 §3` consumer-declared `SpanSink` note; the `memory_AnthropicEmbedder --> rca_AnthropicClient` edge survives only in the prose recording its removal | Appendix B's PD-15(5) ACCEPT-MODIFIED is reproduced in `02 §5` |
| PD-16 | DR-36 + DR-31 | ADDRESSED | `F11 §4.1` bypass edge deleted, `ModeOffline`/`IsolationProcess`; `eval.VirtualClock`/`AwaitQuiescence`; AC-F11-13 | |
| PD-17 | DR-6 §6.2 | ADDRESSED | `01 §5.1` two files / two writers with per-writer PRAGMAs; `F03 §3.2` control writer ≥ 200 tx/s, p99 audit append ≤ 15 ms independent of the telemetry batch | |
| PD-18 | DR-39 + DR-0 | ADDRESSED | `01 §4.1` sole `model.REDSample`; `01 §3.1` `redCh` row cites the per-bucket rate (~20/s vs 120 000/s) | |
| PD-19 | DR-4 + DR-28 §28.2 | ADDRESSED | `F01 §3.2` Cost row: ≤ 6 allocs / ≤ 512 B per span (OTLP), `KeyInterner`, `AttrSorted` from `sync.Pool`; "zero allocation" only in its deletion sentence | |
| PD-20 | DR-28 §28.1 | **PARTIAL** | `F01 §4.3` adopts `01 §7`'s key paths; `overflow_policy: shed \| block_up_to_timeout` — **but** `F01 §7` AC-F01-4 still tests "`block` policy … `reject` policy", and the unit-test bullet reads "Overflow policy: `block` vs `reject`" | Both policy names were deleted by DR-28; the AC can never pass as written. → **N2-2** |
| PD-21 | DR-32 §32.4 + DR-0 | **PARTIAL** | `01 §10.1` five formulas + four-band capacity table; `X-OPS §4.4` cites it and deletes its own rows; `F02`–`F09`/`X-*` §3.2 carry owning-gate citations — **but** `F01 §3.2` Throughput ("≥ 50,000 spans/sec … < 200MB RSS") / Latency ("p99 < 50ms") and FR-F01-1 ("100ms p99 at 10,000 spans/sec") assert uncited local values | `01 §10.1`'s headline is ≥ 120 000 spans/sec at ≥ 45 000/core; F01's 50 000 is not derivable from it. → **N2-7** |
| PD-22 | DR-9 | ADDRESSED | `F02 §4.4` `TimerWheel` normative, `FinalizeTick` deleted; canonical `idle_timeout: 8s`/`hard_timeout: 30s` | |
| PD-23 | DR-7 | ADDRESSED | `F03 §4.4` block-scoped `ExpireBlocks`, `cold_tombstone` DDL, L0/L1 compaction, bounded manifest-driven reconciliation; FR-F03-14 + AC-F03-14 | |
| PD-24 | DR-6 §6.1 | ADDRESSED | `F03 §4.3` `HotCapabilities` (×8) with per-backend semantics; deleted methods listed as deleted | |
| PD-25 | DR-9 | ADDRESSED | `F02 §4.4` RED extracted at the router; `KeepTruncated`; duplicate-`SpanID` set; AC-F02-5 three adversarial cases | |
| PD-26 | DR-11 | ADDRESSED | `F02 §4.3` inverted indexes + published ≤ 150 µs p99; AC-F02-15; `Hits` semantics + AC-F02-16 | |
| PD-27 | DR-19 §19.2 | ADDRESSED | `01 §5.1` `memory_fp_token` DDL; `max_candidates: 500` rarest-first; AC-F08-7 asserts p99 **and** the ≤ 500 count | |
| PD-28 | DR-17 §17.3 + DR-14 §14.7 | ADDRESSED | `F06 §3.2` `max_concurrent_investigations: 2`; `rca.Dispatcher` dedupe + AC-F06-22; `F05 §4.4` `SuppressedBy` | |
| PD-29 | DR-6 §6.5 + DR-33 §33.3 | ADDRESSED | `F03 §3.2` Durability row rewritten to the two-PRAGMA statement; `X-OPS` `VACUUM INTO` with `control_interval: 5m` / `telemetry_interval: 6h`, per-file RPO | |
| PD-30 | DR-32 | ADDRESSED | `X-OPS §4.1` Postgres+pgvector box deleted; capacity table regenerated; `50–150 GB/day` row deleted | |
| PD-31 | DR-3 + DR-2 | ADDRESSED | `internal/tenant` in `01 §1.2`/`02 §4`/`02 §5`; `X-OPS` `package ops` gone entirely | Appendix B's ACCEPT-MODIFIED honoured |
| PD-32 | DR-14 §14.4,§14.5,§14.8 | ADDRESSED | `01 §7 anomaly.thresholds` ratio+abs-delta; `latency_z`/`error_burst_z` only in their deletion comment; `eval_window: 90s`, `debounce_ticks: 2` | |
| PD-33 | DR-13 + DR-14 §14.7 | ADDRESSED | `F04 §4.3` `topology_edge_meta.is_new` durable record + `EdgeMetaReader` reconcile; AC-F04-9 | |
| PD-34 | DR-39 §39.2 + DR-4 | ADDRESSED | `01 §4.7`/`§5.1` quantile set `{p50,p95,p99,max}`; `F05` FR-F05-1 "no p90"; `slow_quantile` closed enum | |
| PD-35 | DR-19 §19.4 + DR-5 | ADDRESSED | `F08 §4.4` `Weight += 0.15` / `−= 0.35`, read-time decay; retention 400 d; `tenant_id` first PK column | |

## 3. Completeness, Traceability & Design Coherence (CC-1 … CC-33)

| ID | DR | Status | Evidence | Note |
|---|---|---|---|---|
| CC-1 | DR-2 | ADDRESSED | `02 §5` adjacency table replaced; `F03 §4.2`/`F04 §4.1`/`F08 §4.3` cycle-creating declarations removed | |
| CC-2 | DR-11 | ADDRESSED | `F06 §3.1` FR-F06-12a (scope) and -12b (recurrence), each with its own TTL and removal rule | |
| CC-3 | DR-21 | ADDRESSED | `01 §9` D-D4 row = three conditions; `alerting.gate: immediate` is exit 2; severity/tier bypass deleted | Appendix B ACCEPT-MODIFIED reproduced |
| CC-4 | DR-18 + DR-15 | ADDRESSED | `F06 §4.3` `Replay(ctx, tid, id, mode)`; `§5` nondeterminism claim deleted; FR-F06-18/-19/-20/-24; AC-F06-15/-16/-17 | |
| CC-5 | DR-14 §14.5 | ADDRESSED | `01 §5.1` `score`, `severity`, `fingerprint`, `epicenter_service`, `blast_radius_json`, `deploy_marker_ids_json`, `suppressed_by`, `provisional` columns | |
| CC-6 | DR-24 + DR-22 | **PARTIAL** | `05 §D-13` amended with the two-stage build and `readOnlyRootFilesystem` reasoning; `02 §3` `k8s_Executor`; `F09 §4.4` argv rules — **but** the version, digest and CVE owner are TBD and `X-OPS §6` never states the image contents | The executor is now buildable in design; the supply-chain controls DR-24 attached to it are unrecorded. → **N2-3**, **N2-11** |
| CC-7 | DR-32 §32.1 | ADDRESSED | `X-OPS §4.1` l.159 "the prior `Postgres+pgvector` box is deleted"; `05 §8` absent-list + supersession row | |
| CC-8 | DR-3 + DR-2 | ADDRESSED | `01 §1.2` l.72 "There is no `internal/ops` package and none may be created"; one `tenant.Resolver`; `tenant.policy_json` in `control.db` | Appendix B ACCEPT-MODIFIED reproduced |
| CC-9 | DR-6 §6.1–§6.3 | ADDRESSED | `F03 §3.1`/`§4.3` now implement span rows, `attr_index`, the covering-index set and `SearchSpans` | |
| CC-10 | DR-14 §14.4,§14.6 | ADDRESSED | `F05` FR-F05-13 throughput-drop detector; `deploy_regression` demoted to an enrichment (no sixth `Kind`) | |
| CC-11 | DR-0 + DR-10/34/39/38 §38.4 | **PARTIAL** | `01 §0` precedence table; `01 §7` owns every default; `01 §10` owns every gate; spot-checked cross-doc agreement on `max_keep_rate`, `confidence_threshold`, `approval_ttl`, `healthy_sample_rate`, `min_event_score`, `max_edges`, `parallelism` — all consistent — **but** `F01 §3.2`/FR-F01-1 still declare their own performance numbers | `defaults.md` is not generated; that half is an explicit, tracked deferral (FU-11, `05 §9`, P1 OPEN) and is not counted against this finding. → **N2-7** |
| CC-12 | DR-19 §19.4 | ADDRESSED | `F08` FR-F08-4 rewritten; correction inserts a `Kind = Correction` record with `SupersedesID`; AC-F08-3 rewritten | |
| CC-13 | DR-29 + DR-0 | ADDRESSED | `01 §6.1` single table; `01 §6.2` twelve tools; `F04`–`F12 §4.3` are filtered views | Appendix B: 12 not 13 — `traceiq_propose_action` withheld, recorded at `01 §6.2` l.2321 |
| CC-14 | DR-23 + DR-22 §22.3 | ADDRESSED | `F09 §4.4` target re-resolution before the allowlist check; approval TTL; separation of duty; approve/execute split | |
| CC-15 | DR-13 | ADDRESSED | `01 §2` `LIM --> TG`; `03` diagram 1 corrected; `F04 §4.3` full `Graph` with `Distance`, `Flush`, `EdgeOps`, `Neighbors(hops, dir)` | |
| CC-16 | DR-16 | ADDRESSED | `F06 §4.3` typed args; `01 §6.3` template allowlist; `ToolArgs.Query` deleted | |
| CC-17 | DR-35 | ADDRESSED | `F10 §4.2` ten-value closed `IntentKind` incl. `IntentRemediate`; `02 §4` carries `nl_IntentKind`, `nl_ChatContext` **and** `nl_ConversationContext`; FR-F10-9/-10 + AC-F10-9/-10 | `02 §4` does not enumerate the ten constants — consistent with `02`'s convention of not rendering enum members for any package; not counted as a gap |
| CC-18 | DR-36 §36.5 + DR-15 | ADDRESSED | `F11 §4.4` scores on `Hypothesis.Category`/`.Component`/`.PostScore`; shadow structs deleted; `01 §5.1` `eval_result` gains the seven columns | |
| CC-19 | DR-25 §25.2,§25.3 | ADDRESSED | `01 §8.2` capability matrix replaces the hierarchy; approver has no `remediation_action:propose`; AC-XSEC-3 asserts zero false-allows | |
| CC-20 | DR-38 §38.1 | ADDRESSED | `00` PRD-promise table + three Phase 3 deferrals; FR-F04-10 + AC-F04-10 (Envoy); FR-F07-8 (< 50 % `trace_id` ⇒ degraded); `model.ServiceMeta`; D-T3 secondary clause struck | |
| CC-21 | DR-30 | ADDRESSED | `F12 §4.3` nine screens; FR-F12-16/-17/-18/-19; `01 §9` D-* → screen map | |
| CC-22 | DR-9 | ADDRESSED | Spill WAL specified; `05 §D-5` D-Z3 claim retained and now implemented | |
| CC-23 | DR-12 + DR-17 | ADDRESSED | `01 §9` D-D1 row names FR-F03-15 **and** FR-F06-17; `F03` disk budget; `F06` cost ceiling | |
| CC-24 | DR-30 + DR-0 | ADDRESSED | `F12 §4.2` `api.HTTPServer` + `api.Deps`; no `type Server struct` survives | See **N2-4**: the *scaffold*'s `Deps` does not match `02 §4`'s printed shape |
| CC-25 | DR-6 §6.4 | ADDRESSED | `01 §10.2` defines `store_hot_index_ratio` once as `hot_bytes_on_disk / raw_ingested_span_bytes`; FR-F03-7 cites it | |
| CC-26 | DR-10 | ADDRESSED | `02 §1` method set matches `F02 §4.3`; `Decisions()`/`Traces()`/`REDSamples()` channels in `04 §4`/`§X4` | |
| CC-27 | DR-4 | ADDRESSED | `F01 §4.2` `model` block deleted and replaced by a cross-reference; the four dropped fields are in `01 §4.1` | |
| CC-28 | DR-14 §14.6 | ADDRESSED | `anomaly.DeployIndex.PrePostSplit` in `F05 §4.4` and `F06 §4.4`; canary a written Phase 3 deferral | |
| CC-29 | DR-38 §38.4 | **PARTIAL** | The CI rule ("fail on any FR with zero ACs") is stated and `traceability.md` generation is tracked as FU-11 — **but** the mandated contiguous AC renumbering was **not** performed | `F09`: 1,2,3,4,7,14–23 · `F11`: 1,4,7,9,11–14 · `F12`: 1,5,6,8,10,12–17 · `X-SEC`: 1,3,5,7a–d,9,12,14–16…19,20. Gaps are wider than in round 1. → **N2-8** |
| CC-30 | DR-38 §38.3 | ADDRESSED | All four terms resolved: tier-0 deleted with FR-F05-11; `max_cost_micro_usd`; Jaccard ≥ 0.6; `ExpectedEvidence` closed enum | |
| CC-31 | DR-26 §26.1 + DR-32 + DR-33 | ADDRESSED | One `server.mode`/`server.profile` axis; `collector` → `gateway` as a Deployment+HPA in `X-OPS §4.1` | |
| CC-32 | DR-8 | ADDRESSED | `F02 §4.3` custom partitioner `partition = ShardFor(ring, traceID) mod partitions`; `partitions` fixed at 32 | |
| CC-33 (1) bus default | DR-38 §38.2 + DR-32 | ADDRESSED | `F02 §8` "**Decided (round 1, DR-8 §8 / DR-32 §32.3)** — `cluster.bus.driver: none` … in **every** mode" | |
| CC-33 (2) tenant in fingerprint | DR-5 + DR-19 §19.1 | ADDRESSED | `F08 §4.2` hash preimage `tenantID ‖ "\x00" ‖ join(Tokens,"\x01")`; `01 §5.1` `tenant_id` first PK column | |
| CC-33 (3) cold reconciliation | DR-7 | ADDRESSED | `F03 §8` marked resolved; FR-F03-14 + AC-F03-14; `List(prefix="")` deleted | |
| CC-33 (4) edge-sharding key | DR-13 | ADDRESSED | `F04 §8` `hash(caller, callee)` with the callee reverse index (×6) | |
| CC-33 (5) exemplar selection | DR-38 §38.2 | ADDRESSED | `F03 §8` first-wins reservoir of 4, ties by lowest `TraceID` | |
| CC-33 (6) `PolicyStore` backend | DR-3 | ADDRESSED | `01 §5.1` `tenant(… policy_json, version …)` in `control.db`; no new datastore | |

---

## New findings (round 2)

### Major

**N2-1 — `05 §6 D-12` still mandates `GOTOOLCHAIN=local` and a `govulncheck` allowlist that DR-1 deleted.**
*Doc §:* `docs/architecture/05-tool-selection-adr.md §6 D-12` (l. 268–277).
*Issue:* The D-12 tooling table reads `` `GOTOOLCHAIN=local` | — | **Enforced in CI.** `` and
`` `govulncheck` … blocking with a documented, expiring allowlist (§ 7.1) ``, and pins the three analysers at
Set-A versions (`staticcheck v0.6.1`, `govulncheck v1.1.4`, `gosec v2.22.7`). Every one of these is reversed
by DR-1 and by `§8.2` ("**`GOTOOLCHAIN=auto` and `GOTOOLCHAIN=local` are both forbidden**"), `§9`
(FU-2 **CLOSED — not needed**, "no suppression file") and `§10` ("never `auto`, never `local`", "no allowlist
file of any kind", "both pinned to releases declaring Go ≥ 1.27 support"). `§6` is the *Decision* section — a
reader who stops there gets the CI configuration exactly backwards, which is SR-23's original defect.
*Required change:* Rewrite the D-12 table to `GOTOOLCHAIN=go1.27.1`, drop the expiring-allowlist clause, and
replace the three version pins with a pointer to `§8.2`'s analyser-pin table (DR-0: `05 §8` is the sole owner).

**N2-2 — `F01 §7` was never swept: two acceptance criteria test deleted configuration.**
*Doc §:* `docs/architecture/features/F01-ingest.md §7`.
*Issue:* (a) `AC-F01-4` reads "`block` policy shows zero dropped batches …; `reject` policy shows zero
unbounded queue growth" and the unit-test bullet reads "Overflow policy: `block` vs `reject` behavior" —
DR-28 §28.1 **deleted** `overflow_policy: block` and the policy set is now `shed | block_up_to_timeout`, so
neither branch of the AC is expressible. (b) `AC-F01-9` — which **Appendix C mandates as a new AC** — carries
the round-1 load-test criterion ("Load test sustains 50k spans/sec, p99 < 50ms, < 200MB receiver RSS")
instead of DR-28's text: *load at 2× ingest capacity ⇒ 429s rise, RSS stays under `01 §10.2`'s ceiling,
goroutine count stays bounded, and a goroutine-dump assertion shows zero handlers blocked longer than
`enqueue_timeout`*. The ID exists; the requirement it was created to verify does not.
*Required change:* Replace `AC-F01-4` with a `shed` / `block_up_to_timeout` pair, replace `AC-F01-9` with
DR-28 §28.1's sentence verbatim, and fix the unit-test bullet.

**N2-3 — `05 §D-13`'s `kubectl` pin, digest and CVE owner are `TBD`.**
*Doc §:* `docs/architecture/05-tool-selection-adr.md §6 D-13`.
*Issue:* DR-24 requires D-13 to be "amended with the pinned version, the `sha256` digest, the upstream URL,
an SBOM entry, **and a named CVE-tracking owner**". Three of the five are literal `**TBD**`. Without a
version and digest, "pinned, digest-verified" is a claim with nothing behind it, and FU-8's absent-list check
cannot cover a binary that is not in the module graph. The CVE-tracking owner is the control that makes a
non-Go binary inside a distroless image acceptable at all, and it needs no download to assign.
*Required change:* Pin an exact `kubectl` version and its `sha256`, and name the CVE-tracking owner. If the
digest genuinely cannot be fixed before the Dockerfile lands, record that as a dated, owned follow-up in
`05 §9` rather than as an inline TBD.

**N2-4 — the scaffold's `api.Deps` contradicts `02 §4`'s printed `api_Deps`, which DR-30 binds it to.**
*Doc §:* `internal/api/api.go` vs `docs/architecture/02-class-diagram.md §4` (l. 1430).
*Issue:* `docs/reports/w3-cleanup.md §6` states that "the register never prints an explicit `type Deps struct`
block" and that "`02 §4` … is outside this pass's binding-read scope", and reconstructed the struct from
DR-29/DR-30's tables. But `02 §4` **does** print `api_Deps` with a fifteen-field list, and DR-30 says the
rename is "matching `02 §4`'s shape". The reconstruction diverges: `RCA rca_Engine` → `Investigations`;
`Tenant tenant_Resolver` → `Tenants`; `Authz` → `Authorizer`; `Interpreter` + `Answerer` → a single `NL`;
`Store store_TieredStore` → `Store store.HotIndex` + `Cold store.ColdStore`; `Anomaly anomaly_Engine` →
`Anomaly anomaly.Grouper` + `Baselines` + `Deploys`; plus four fields (`Config`, `SelfObs`, `Cluster`, `K8s`)
absent from `02 §4`. It also surfaces that `02 §2`'s `anomaly_Engine` is declared in `02` but in no DR and in
no Go package.
*Required change:* Reconcile in one direction and record it. Either amend `02 §4`'s `api_Deps` to the
scaffold's shape (and resolve `anomaly_Engine` — either declare it in `anomaly` or replace it with `Grouper` +
`BaselineReader` + `DeployIndex`), or rename the scaffold's fields to `02 §4`'s. Note the outcome in the
register so the next pass does not re-derive it.

**N2-5 — the PRD's ROI row was never recomputed.**
*Doc §:* `Tracing-Bot-PRD.md § ROI` ("LLM API spend (capped, ~8 deep investigations + daily use) | ~$6–12k").
*Issue:* DR-34 §34.3 states that `01 §10.3`'s cost targets **and the PRD's ROI row** "were computed on the
Sonnet assumption and are **recomputed**", and DR-34's Docs-to-change line names "`PRD` ROI row (recomputed)".
`01 §10.3` was done correctly and now carries the derivation formula. The PRD was not touched at all — its
mtime is `2026-08-31 22:20`, predating the entire fix wave — so the customer-facing cost claim still rests on
the withdrawn model assumption, with no formula and no DR reference.
*Required change:* Recompute the ROI row's LLM line from `rca.llm.pricing` and DR-17 §17.1's token model,
print the formula beside it as `01 §10.3` does, and cite DR-34 §34.3.

### Minor

**N2-6 — `05 §6`'s `D-3`/`D-4`/`D-5`/`D-7`/`D-8`/`D-9` carry Set-A pins and Go-1.23 rationale.**
*Doc §:* `05 §6`. `modernc.org/sqlite v1.39.0` (vs `§8.2`'s **v1.58.0**), parquet-go `v0.25.1` (vs **v0.32.0**),
grpc `v1.75.1` "the last release supporting Go 1.23" (vs **v1.83.2**), otlp `v1.9.0` "(v1.11.0 declares
`go 1.25.0`)" (vs **v1.11.0**), and ClickHouse "**Pin v2.40.1** — v2.48.0 declares `go 1.25.0` and will not
build here" (vs `§8.2`'s **v2.48.0**). Each statement was true under Go 1.23 and is now false. DR-0 makes
`05 §8` the sole owner of dependency pins, so `§6` should cite it rather than restate it.
*Required change:* Strike the version numbers from `§6`'s decision tables, leaving the *choice* and its
rationale, and point each row at `§8.2`.

**N2-7 — `F01 §3.2` and `FR-F01-1` declare their own performance numbers.**
*Doc §:* `F01 §3.2` Throughput/Latency rows; `F01 §3.1` FR-F01-1.
*Issue:* "≥ 50,000 spans/sec … < 200MB RSS attributable to receivers" and "p99 receive-to-fan-out < 50ms" and
"100ms p99 at 10,000 spans/sec" are stated with no owning-gate citation, while `01 §10.1` owns ≥ 45 000
spans/sec/core decode and a ≥ 120 000 spans/sec single-binary headline. Every other feature doc's §3.2 now
cites its gate (`F02`–`F09`, `X-OPS`, `X-SEC`). This is PD-21/CC-11's residue in the one doc that kept its
numbers. *(`F04 §3.2` states 50 000 too but explicitly cites `01 §10.1` and explains the derivation — that one
is fine.)*
*Required change:* Convert the three values to references to `01 §10.1`/`§10.2`, or derive them there.

**N2-8 — AC ID sequences in `F09`, `F11`, `F12` and `X-SEC` were not renumbered contiguously.**
*Doc §:* `F09 §7`, `F11 §7`, `F12 §7`, `X-SEC §7`.
*Issue:* DR-38 §38.4(2) requires "AC ID sequences are renumbered contiguously in `F09`, `F11`, `F12` and
`X-SEC`, where the gaps currently read as sampled rather than complete coverage". The new ACs were appended
at 14+ and the round-1 gaps were left: `F09` 1,2,3,4,7,14–23 (5,6,8–13 absent); `F11` 1,4,7,9,11–14;
`F12` 1,5,6,8,10,12–17; `X-SEC` 1,3,5,7a–d,9,12,14,15,16…19,20. The sequences are now *more* discontinuous
than when CC-29 was filed.
*Required change:* Renumber each doc's AC table contiguously and update every inbound cross-reference
(`AC-F06-16` ← `AC-F12-13`, `AC-F12-16` ← `F11 §3.1`, etc.). Best done together with FU-11's generated
`traceability.md` so the two cannot drift again.

**N2-9 — the `Resolution` tie-break was applied to the scaffold but not to the docs.**
*Doc §:* `01 §4.1` (l. 594) and `01 §4.7` (l. 1334) both declare `type Resolution uint8`; `F04 §4.2` (l. 108)
declares a third under `package topology`; `01 §5.1` (l. 1594) comments the `topology_edge.resolution` column
as `(topology.Resolution)`.
*Issue:* The register itself prints `Resolution` twice — once as `package topology` (DR-13) and once as
`package model` (DR-39 §39.1) — and never says which wins. `docs/reports/w3-cleanup.md §4` resolved it in code
in favour of `model.Resolution` (verified: `internal/model/red.go` is the only declaration; `topology.Edge.Resolution`
and `HotIndex.CascadeRED` both take `model.Resolution`). The docs still carry the `topology`-local copy, so an
engineer reading `F04 §4.2` writes a type that does not exist. DR-0's ownership table forbids a feature doc
re-declaring a `model.*` type.
*Required change:* Delete the `Resolution` declaration from `01 §4.7` and `F04 §4.2`, leaving the one in
`01 §4.1`; correct the `01 §5.1` column comment to `model.Resolution`; add a one-line tie-break note to
DR-39 §39.1 so the ambiguity cannot be re-derived.

**N2-10 — `model.ToolResult`'s field list is printed nowhere.**
*Doc §:* DR-15 (`Tool.Invoke`), DR-16 §16.3(4) (`ToolResult.Clamped`), DR-37 §37.1(1); `02 §3` signatures;
`01 §8.6(1)`.
*Issue:* Every reference is by name. The type is load-bearing — DR-18's `Step` persists
`ToolResultHash`/`ToolResultRef`/`ToolResultBytes` over "the canonical result bytes", and DR-16 requires a
`Clamped` flag — but no document states what those canonical bytes contain.
`internal/model/rca.go:293` carries a `TODO(DR-15/DR-16): under-specified` reconstruction
(`Rows []byte; Clamped, Truncated, FromCache bool`), correctly flagged rather than invented.
*Required change:* Print the `model.ToolResult` field list in `01 §4.4` (its DR-0 owner) and reconcile the
scaffold to it — including how `Rows` relates to DR-16's `Project` field allowlist and to the RFC 8785
canonicalisation the hash is taken over.

**N2-11 — `X-OPS §6` states no container-image contents.**
*Doc §:* `docs/architecture/features/X-OPS-deployment.md §6`.
*Issue:* DR-24's Docs-to-change names `X-OPS §6` (image contents). `§6` covers ServiceAccounts, RBAC and the
metrics port but never mentions the image: the words "distroless", "image" and "kubectl" do not appear in the
document. The operator-facing deployment doc therefore does not record that the release image is distroless
plus exactly one extra binary, nor that `readOnlyRootFilesystem: true` survives because that binary is baked
in and never fetched.
*Required change:* Add a short image-contents paragraph to `X-OPS §6` citing `05 §D-13`, stating the
two-stage build, the single `/usr/local/bin/kubectl`, no shell / no package manager, and the pod-hardening set.

---

## Adjudication of the three items `docs/reports/w3-cleanup.md` flagged

| Flagged item | Ruling |
|---|---|
| `api.Deps`'s field list "is not printed anywhere in DR-0…DR-39" | **Not acceptable as-is.** The premise is wrong: `02 §4` prints a fifteen-field `api_Deps`, and DR-30 explicitly binds the rename to "`02 §4`'s shape". Raised as **N2-4** (Major). |
| `model.ToolResult`'s field list "is never printed anywhere in the register" | **Correct, and a genuine register gap.** The `TODO` reconstruction was the right call for the scaffold pass; the register/`01 §4.4` must now print the type. Raised as **N2-10** (Minor). |
| `Resolution` tie-break resolved in favour of DR-39 | **Ruling upheld** — `model.Resolution` is canonical, and the scaffold applies it consistently (single declaration in `internal/model/red.go`; `topology` and `store` call sites retyped). But the decision was **not** propagated to `01 §4.7`, `F04 §4.2` or `01 §5.1`'s column comment. Raised as **N2-9** (Minor). |

## Contradictions introduced by the fix wave itself

Checked for: two documents now stating different values for the same DR-owned fact; citations to
non-existent DR numbers; mermaid diagrams that no longer match adjacent prose.

- **Numeric agreement across docs: clean.** `max_keep_rate` 0.25, `confidence_threshold` 0.75,
  `approval_ttl` 15m, `healthy_sample_rate` 0.01, `min_event_score` 0.55, `max_edges` 20 000,
  `parallelism` 4 all agree everywhere they appear; every residual old value (`parallel: 1`, `168` buckets,
  `8 KiB`, `p90`, `30m`, `claude-sonnet-4-5`, `temperature: 0`, `latency_z`) appears **only** inside an
  explicit deletion or supersession sentence.
- **No dangling DR references.** Every `DR-nn` citation found resolves to DR-0…DR-39.
- **Diagram/prose agreement: clean.** `02 §3` no longer draws `memory_AnthropicEmbedder --> rca_AnthropicClient`
  (it survives only in the prose recording its removal) and now draws `llm_Client`; `03` diagram 1's
  `SH->>TOPO` edge is gone and `04 §1 L5`/`L2` show topology consuming the pre-sampling fan-out per DR-13;
  `04`'s shutdown ladder renders DR-33 §33.2's twelve steps in order (X5 `topology.Flush`, X6
  `anomaly.BaselineStore.Checkpoint`), superseding DR-13's stale "`04 §X6`" pointer.
- **Intra-document contradictions found: two,** both in `05` — `§6 D-12` vs `§8.2`/`§9`/`§10` (**N2-1**) and
  `§6 D-3/D-4/D-5/D-7/D-8/D-9` vs `§8.2` (**N2-6**).
- **Docs-vs-scaffold divergences found: two** — `api.Deps` (**N2-4**) and `Resolution` (**N2-9**).

## Deferrals accepted without a finding

- `docs/architecture/defaults.md` and `docs/architecture/traceability.md` do not exist. DR-38 §38.4 makes both
  *generated* artefacts and `05 §9` FU-11 records the work as P1 OPEN with a blocking CI gate. That is an
  explicit, owned deferral, not a gap.
- `F03 §8` and several other §8 sections mark their open questions "resolved (DR-n)" rather than the register's
  phrasing "**Decided (round 1)**". Substance matches; wording is not binding.
- `02 §4` does not enumerate `nl.IntentKind`'s ten constants. `02` renders no package's enum members; the union
  is complete in `F10 §4.2`.

---

*End of round-2 verification. 98 findings checked · 89 ADDRESSED · 9 PARTIAL · 0 NOT-ADDRESSED ·
11 new findings (5 Major, 6 Minor) · verdict **NEEDS-FIX-ROUND**.*
