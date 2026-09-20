# 03 — TraceIQ Sequence Diagrams

> Revision 2 — 2026-09-15 — applies decision register DR-5, DR-6, DR-7, DR-8, DR-9, DR-10, DR-11, DR-13, DR-14, DR-15, DR-16, DR-17, DR-18, DR-19, DR-20, DR-21, DR-22, DR-23, DR-24, DR-25, DR-27, DR-34, DR-35, DR-37, DR-39 (round-1 fixes)

Companion to [`01-system-architecture.md`](./01-system-architecture.md) and [`02-class-diagram.md`](./02-class-diagram.md). Participant names are the concrete/canonical types from the class diagrams and the decision register; package prefixes are dropped where unambiguous. This document is bound by [`06-decision-register.md`](./06-decision-register.md) (DR-0): where it disagrees with the register, the register wins.

Timing annotations reference the non-functional targets in `01-system-architecture.md` §10.

---

## 1. Span ingest → assembly → sampling decision → tiered write (F01 → F02 → F03)

```mermaid
sequenceDiagram
    autonumber
    participant SVC as Instrumented service
    participant RCV as ingest.OTLPGRPCReceiver
    participant LIM as auth.RateLimiter (LimitIngest)
    participant NORM as ingest.Normalizer
    participant VAL as ingest.Validator
    participant ROUTE as decode/route worker
    participant TOPO as topology.LiveGraph
    participant SH as sampler.Shard
    participant BASE as sampler.BaselineSnapshot (cached)
    participant POL as sampler.Policy
    participant TIER as store.TieredStore
    participant HOT as store.HotIndex (traceiq.db)
    participant COLD as store.ColdStore (cold WAL + Parquet)

    SVC->>RCV: Export ExportTraceServiceRequest, 512 spans
    RCV->>LIM: Allow(tenant, LimitIngest)
    alt tenant rate limit exceeded (20000 spans/s/token, burst 2x)
        LIM-->>RCV: false
        RCV-->>SVC: RESOURCE_EXHAUSTED with Retry-After 1s
    else within budget
        LIM-->>RCV: true
        RCV->>NORM: FromOTLP(resourceSpans, tenant)
        NORM->>NORM: KeyInterner interns attribute keys, computes ErrorSignature
        NORM->>VAL: ApplyLimits per span
        Note over VAL: span 512KiB; 128 attrs/span; attr value 4KiB truncate+flag<br/>(or reject per tenant oversize_attr); 128 events / 128 links; OTel semconv keys verbatim
        VAL-->>NORM: []model.Span, Truncated / DroppedAttrsCount set — never silent
        NORM-->>RCV: []model.Span
        RCV->>ROUTE: Emit spans on spanBatchCh
        alt spanBatchCh full for ingest.queue.enqueue_timeout 100ms (bound: min(1024 batches, 256MiB))
            ROUTE-->>RCV: ErrBackpressure
            RCV-->>SVC: RESOURCE_EXHAUSTED with Retry-After 1s
        else enqueued
            ROUTE-->>RCV: ok
            RCV-->>SVC: ExportTraceServiceResponse, partial_success counts
        end
    end

    Note over ROUTE: ack already sent — zero added latency to the request path

    loop per span, at the router — BEFORE the shard send (D-X1, D-X5, DR-9)
        ROUTE->>ROUTE: RED-extract into the per-(tenant,service,operation,10s bucket)<br/>accumulator — closes the shard_full RED hole
        ROUTE->>TOPO: Consume(span) — PRE-SAMPLING ingest fan-out (post-normalizer),<br/>independent of the sampling decision (DR-13, freshness < 1s)
        ROUTE->>ROUTE: shard = sampler.ShardFor(ring, TraceID) — rendezvous (HRW) hashing
        ROUTE->>SH: send on shardCh[i] with 50ms timeout
        alt timeout
            ROUTE->>ROUTE: drop span body, traceiq_ingest_spans_dropped_total{reason=shard_full}<br/>— RED contribution was ALREADY recorded above
        end
    end

    TOPO->>TOPO: flush 10s edge buckets to EdgeSink every 10s;<br/>every NewEdge durably recorded (topology_edge_meta.is_new=1)

    SH->>SH: append span bytes to the shard's spill WAL segment,<br/>group fsync every sampler.wal.flush_interval 1s
    SH->>SH: dedupe on model.SpanID before assembly, append to partialTrace
    SH->>SH: TimerWheel.Schedule — 256 slots x 250ms tick, two levels<br/>(idle_timeout 8s, hard_timeout 30s)

    alt GLOBAL memory watermark 512MiB crossed (one atomic counter across ALL shards)
        SH->>SH: shed oldest partial trace, KeepReason = KeepShed
        Note over SH: forced completion, counted, never silent
    end

    SH->>SH: trace expired or root complete -> Assemble model.Trace
    Note over SH: PathSignature = xxh3 of pre-order DFS edge list,<br/>children sorted by (StartUnixNano, SpanID); orphan subtrees appended by their own root

    SH->>SH: stamp Kept + exemplar TraceIDs onto the router's RED accumulator (additive only)
    SH->>SH: PredicateSet.Match — bounded, inverted indexes byService/byErrorSig/byPathSig/byAttrKey
    SH->>BASE: Quantile(service, operation, 0.99); PathSeen(signature, 7d)
    Note over BASE: immutable snapshot built at startup from red_rollup via LoadSnapshot,<br/>refreshed every sampler.baseline.refresh_interval 60s, staleness tolerance <= 120s<br/>ZERO SQLite reads inside Evaluate — AC-F02-12
    SH->>POL: Evaluate(trace, baseline, interestMatch, governor)
    Note over POL: six classes, in this order — Error, Slow, Rare, Interest, Floor, Probabilistic;<br/>Slow requires duration>baseline.P99 AND >slow_min_duration 250ms AND Warmed;<br/>max_keep_rate 0.25 is a HARD CAP applied after all six;<br/>over cap sheds Probabilistic -> Floor -> Interest -> Rare -> Slow — Error is NEVER shed
    POL-->>SH: sampler.Decision{Reason, Tier}

    alt Reason in {Error, Slow, Rare, Interest, Floor} or a Probabilistic keep
        SH->>TIER: WriteTrace(trace, TierFull)
        TIER->>HOT: WriteBatch — ONE transaction per 200 traces or 250ms;<br/>trace row committed cold_state=0 (pending), wal_segment set<br/>— this is what makes the trace searchable at ack, never a 404 (DR-7)
        Note over HOT: span_text_fts written ONLY for KeepReason in {Error, Slow, Rare, Interest}
        TIER->>COLD: Append(trace) — cold WAL group fsync at 250ms or 4MiB, CRC32C per record
        COLD->>COLD: seal at row_group 32MiB / target_block 128MiB / flush_interval 5m (max_open_blocks 2)
        COLD->>COLD: Seal — write spans.parquet/traces.parquet/meta.json, verify checksum,<br/>THEN commit the block_manifest row
        COLD->>HOT: THEN HotIndex.BindColdBlock — one ranged UPDATE cold_state=1 WHERE wal_segment=?
        Note over TIER,COLD: durability: ZERO loss on process kill (kill -9);<br/>on power loss, loss is bounded by one WAL group commit (<= 250ms)
    else Dropped (below floor, below the probabilistic rate, or shed)
        SH->>TIER: WriteTrace(trace, TierRollup)
        Note over TIER,HOT: only red_rollup and path_signature are updated; span bodies discarded;<br/>this trace's RED was ALREADY recorded at the router — no accuracy loss
    end

    SH->>SH: emit Decision on decisionCh for self-observability
    Note over SH: Decision.DecisionLatencyNs p95 at or under 8ms
```

**Guarantees.** All spans of a trace reach one shard via `sampler.ShardFor` rendezvous (HRW) hashing — the same function a Kafka partitioner uses in Phase 2 — so tail sampling needs no collector gymnastics (D-J4); during a resize, an already-open trace's owner never moves (DR-8's drain-then-move protocol). Nothing is discarded before RED extraction, which now happens at the router, before the shard send (D-X1). Every discard path increments a labelled counter, so sampler lossiness is measurable rather than assumed (D-Z3).

---

## 2. RED sample → anomaly event → grouping → incident → investigation start (F05 → F06)

```mermaid
sequenceDiagram
    autonumber
    participant SMP as sampler.Sampler
    participant BS as anomaly.BaselineStore
    participant DET as anomaly detectors (5, DR-14 §14.1)
    participant DEP as anomaly.DeployIndex
    participant GRP as anomaly.Grouper
    participant TOPO as topology.LiveGraph
    participant AE as anomaly.Engine
    participant ST as store.TieredStore
    participant RCA as rca.Engine
    participant API as api.AlertRouter

    SMP->>BS: Observe(model.REDSample) via REDSamples() channel, per Res10s bucket (never per-span — DR-39)
    BS->>BS: SeasonalBaseline — P2Estimator.Add per season slot (24 hour-of-day + 7 weekday = 31 slots)
    BS->>BS: EWMA.Add error rate and throughput (ewma_alpha 0.2)
    Note over BS: Cold until Samples>=warmup_samples 200 (global slot);<br/>Global-only until warmup_samples_per_bucket 30 (season slot), thresholds widened x1.5, Provisional=true;<br/>Provisional-by-decree: forced Warm at max_cold_start 24h, Score capped at 0.69<br/>Checkpoint: incremental, <= checkpoint_max_rows 2000 dirty rows / 60s (never a full sweep)

    loop every anomaly.eval_interval 30s, over eval_window 90s, debounce 2 ticks
        AE->>DET: Evaluate(now, window, samples, errorSigs, baselines, topology, deploys)
        DET->>BS: Get(service, operation, at) per key
        BS-->>DET: Baseline{Q, ErrorEWMA, RPSEWMA, Warmed, Provisional}

        alt latency_shift: P95>=base.P95*1.5 AND P95-base.P95>=50ms AND Calls>=20
            DET->>DET: build KindLatencyShift event
        else error_burst: ErrorRate-base.ErrorEWMA>=0.05 AND ErrorRate>=base.ErrorEWMA*3.0 AND Calls>=20
            DET->>DET: build KindErrorBurst event
        else new_error_signature: unseen in 7d AND occurrences>=5 in the window
            DET->>ST: QueryErrorSignatures / lookup first_seen
            ST-->>DET: not found
            DET->>DET: build KindNewErrorSignature event
        else throughput_drop: RPS<=base.RPSEWMA*0.5 AND base.RPSEWMA>=min_rps 0.1
            DET->>DET: build KindThroughputDrop event
        else topology_change: NewEdge Calls>=5, or a VanishedEdge that carried >=1000 calls in the prior 24h
            DET->>TOPO: Edges(window) / reconciled via EdgeMetaReader from topology_edge_meta
            TOPO-->>DET: edge set with IsNew / IsVanished
            DET->>DET: build KindTopologyChange event
        end

        DET->>DEP: Near(service, at, correlation_window 30m)
        DEP-->>DET: deploy markers in [at-30m, at] / [at+settle 2m, at+settle+30m]
        DET->>DET: deploy-window hit -> tag DeployMarkerIDs, Score += 0.10 (clamped) — NOT a sixth Kind (DR-14 §14.6)
        DET-->>AE: events with Score; events below min_event_score 0.55 never reach the Grouper
        AE->>ST: persist anomaly_event rows
        AE->>GRP: Add(event)
    end

    GRP->>GRP: byService map lookup — O(1) amortized, no per-event topology scan (PD-13)
    GRP->>TOPO: Neighbors(service, topology_hops 2, Both) — ONLY on a newly-added service in an incident,<br/>memoised in neighborCache (LRU 4096, TTL 60s)
    TOPO-->>GRP: neighbourhood
    alt Fingerprint already open, or closed within dedupe_ttl 30m
        GRP->>GRP: attach event; the would-be new incident is written SuppressedBy=<existingIncidentID>, not dispatched
    else events within topology_hops 2 and grouping.window 5m
        GRP->>GRP: merge into one open incident
    else unrelated neighbourhood
        GRP->>GRP: open a new incident; over max_open_incidents 200, force-close the lowest-Score one (Status=Expired)
    end
    GRP->>GRP: EpicenterService = rankEpicenter(events, topology) — max(score x inbound-edge count),<br/>ties by earliest FirstSeen then lexical
    GRP->>GRP: Fingerprint = anomaly.Fingerprint.Compute — xxh3(tenantID, sorted svc:/op:/kind:/errsig:/dep:/ns: tokens, <=24)
    GRP->>TOPO: Neighbors(epicenter, topology_hops 2, Both)
    TOPO-->>GRP: BlastRadius services
    GRP-->>AE: model.Incident{Status: Candidate, Score, Severity, Provisional}
    AE->>ST: persist incident row, link anomaly_event.incident_id

    Note over AE,API: an Incident is NOT a page — D-D4 (DR-21). There is no severity- or tier-derived bypass;<br/>model.ServiceMeta.Tier is display/grouping only and is NEVER a paging input

    alt Score >= rca.min_incident_score 0.6, and the RCA queue is not saturated
        AE->>RCA: Investigate(tid, incident)
        RCA->>ST: update incident.status = Investigating
        Note over RCA: continues in diagram 3; Dispatcher dedupes on an open Fingerprint first (DR-17 §17.3)
    else score below threshold, or traceiq_rca_queue_saturated (depth > 32)
        AE->>ST: incident stays Candidate — or a new investigation downgrades to the rules reasoner; visible in UI only
    end
```

---

## 3. Full RCA loop — contextualize, hypothesize, test, validate, report, memory, alert (F06 + F07 + F08 + F12)

```mermaid
sequenceDiagram
    autonumber
    participant AE as anomaly.Engine
    participant ENG as rca.Engine
    participant JRN as rca.Journal
    participant CTX as rca.Contextualizer
    participant MEM as memory.Store
    participant TOPO as topology.LiveGraph
    participant ST as store.TieredStore
    participant DEP as anomaly.DeployIndex
    participant BUD as rca.Budget
    participant RSN as rca.Reasoner
    participant SAN as rca.Sanitizer
    participant LLM as Anthropic Claude API (llm.Client)
    participant SV as rca.SchemaValidator
    participant REG as rca.ToolRegistry
    participant COR as correlate.Correlator
    participant VAL as rca.Validator
    participant REP as rca.Reporter
    participant SMP as sampler.Sampler
    participant AR as api.AlertRouter

    AE->>ENG: Investigate(tid, incident)
    ENG->>ENG: Dispatcher dedupes on an open Incident.Fingerprint — a hit attaches, no 2nd investigation starts
    ENG->>ENG: acquire semaphore, rca.max_concurrent_investigations 2
    ENG->>BUD: start budget — wall_clock 5m, max_step_wall_clock 45s (NEW), max_steps 24, max_tool_calls 40,<br/>max_tokens_in 120000 (uncached), max_cached_tokens_in 600000 (NEW), max_tokens_out 64000, max_cost_micro_usd 500000
    ENG->>JRN: Create(investigation) — ReplaySeed (crypto/rand), PromptVersion, ModelID
    JRN->>ST: insert investigation row, Status=Running

    rect rgb(238, 244, 255)
    Note over ENG,DEP: Phase 1 — Contextualize (model.Phase=PhaseContextualize), fully deterministic
    ENG->>CTX: Build(incident)
    CTX->>TOPO: Neighbors(epicenter, hops 2)
    TOPO-->>CTX: Neighborhood
    CTX->>ST: QueryRED for each affected service and operation
    ST-->>CTX: REDSeries (p50/p95/p99, error rate)
    CTX->>ST: GetTrace for exemplar trace IDs, max 5
    ST-->>CTX: exemplar traces, Parquet fallback via ReadTrace if evicted from hot
    CTX->>DEP: Near(services, incident window, correlation_window 30m)
    DEP-->>CTX: deploy markers
    CTX->>MEM: Similar(fingerprint, topK 8)
    MEM->>MEM: candidate generation over the inverted memory_fp_token index, rarest-token-first,<br/>stop at max_candidates 500; score with SimilarityScorer (lexical default); min_similarity 0.35
    MEM-->>CTX: []model.Record — a superseded record is returned only WITH its superseder, ranked below it
    CTX-->>ENG: rca.Context
    ENG->>JRN: AppendStep phase=Contextualize
    end

    ENG->>SMP: SetInterestPredicate — Scope=ScopeInvestigation, scope_ttl 30m (phase A, FR-F06-12a)
    Note over ENG,SMP: closed loop — see diagram 6

    rect rgb(240, 248, 240)
    Note over ENG,LLM: Phase 2 — Hypothesize
    ENG->>RSN: NextStep(tid, State) — Kind() = "llm" | "rules"
    alt reasoner = llm
        RSN->>SAN: Wrap(kind, s) — every string not authored by TraceIQ's own code (telemetry, logs, memory,<br/>deploy metadata, the user's own question), delimited with a 16-hex-byte per-investigation canary (DR-37 §37.1)
        SAN-->>RSN: delimited, escaped content
        RSN->>LLM: Messages — model claude-opus-5, thinking:adaptive, effort:medium, NO temperature sent,<br/>tool schemas as tools, prompt_cache:true
        LLM-->>RSN: strict JSON (stop_reason checked; "refusal" maps to StepVerdict=refused)
        RSN->>SV: ValidateReasonerOutput(raw) — an unknown field is an error, no best-effort parse
        SV-->>RSN: rca.Proposal{Phase, Hypotheses, Call, Done, Rationale<=2000B DISPLAY ONLY}
        RSN->>SAN: CheckEcho(output) — canary outside a legal position?
        alt canary echoed, or 2 consecutive schema failures on the same step
            SAN-->>RSN: violation detected
            RSN->>RSN: Verdict=schema_error, traceiq_rca_canary_violation_total++
            Note over RSN,ENG: TWO violations in ONE investigation -> swap to rules mid-loop, and never swap back (DR-34 §34.4)
        end
        RSN->>BUD: Charge(tokensIn, cachedIn, tokensOut, cost)
    else reasoner = rules
        RSN->>RSN: match rca.Rule set against context, populate Category + Component deterministically
        Note over RSN: e.g. deploy within 30m and a latency shift on the same service<br/>=> Hypothesis{Category: CatDeployRegression, Component: service} — scorable like an LLM hypothesis
    end
    RSN-->>ENG: rca.Proposal with ranked []model.Hypothesis (PriorScore)
    ENG->>JRN: AppendStep phase=Hypothesize, fsynced before the loop may advance
    end

    rect rgb(255, 248, 238)
    Note over ENG,COR: Phase 3 and 4 — Test and Validate, looped
    loop until Confidence >= rca.confidence_threshold 0.75, or wall_clock / max_steps / max_tool_calls / tokens / cost exhausted
        ENG->>RSN: NextStep(State) — per-step timeout max_step_wall_clock 45s
        RSN-->>ENG: rca.Proposal{Call: *ToolArgs} — exactly ONE typed pointer non-nil (Trace/Log/Metric/Topology/Memory)
        ENG->>BUD: CountToolCall
        alt budget exhausted
            BUD-->>ENG: Terminated=true, TerminationReason set
            ENG->>ENG: break, Status=BudgetExhausted
        end
        ENG->>SV: ValidateToolArgs(args, incident, topology) — DR-16 §16.3
        Note over SV: (1) Service/Component must exist in topology.Snapshot(tid)<br/>(2) window within incident +/-2h, clamped to rca.tools.max_window 6h<br/>(3) AttrEquals keys in the 8-key store.hot.indexed_attribute_keys allowlist; TemplateID registered<br/>(4) every limit server-clamped, Clamped=true when it fires (5) TenantID re-checked inside Invoke
        alt invalid args
            SV-->>ENG: ErrToolArgsUnresolvable
            ENG->>JRN: AppendStep Verdict=invalid_args — NO tool called, but it COUNTS against max_tool_calls
        else valid
            ENG->>REG: Dispatch(tid, args)
            alt Trace
                REG->>ST: SearchSpans(query), limit clamped to max_rows_per_tool_call 500
                ST-->>REG: SpanPage, hot index then Parquet fallback
            else Log
                REG->>COR: LogsForTrace(tid, traceID, window, limit 200)
                COR->>COR: auth.EgressDialer.HTTPClient — tenant-scoped adapter, max_inflight 4/tenant,<br/>breaker opens after 5 failures for 30s, LRU cache (2048, per-tenant partitioned)
                COR-->>REG: []model.LogLine, Contains matched as a literal substring, server-side, after retrieval
            else Metric
                REG->>COR: Range(templateID, params, window, stepSeconds) — 10-template PromQL allowlist,<br/>rendered by text/template with an escaping function, never string concatenation
                COR-->>REG: model.Series with exemplars
                REG->>ST: QueryRED fallback when the metrics driver is none
            else Topology
                REG->>TOPO: Neighbors / Edges / EdgeOps, limit clamped to 500
                TOPO-->>REG: edges
            else Memory
                REG->>MEM: Similar(fingerprint SUPPLIED BY THE ENGINE, never authored by the reasoner) / Search(text<=256B)
                MEM-->>REG: []model.Record — re-wrapped UntrustedMemory on retrieval, NEVER cited as Evidence (DR-19 §19.5)
            end
            REG-->>ENG: model.ToolResult{Hash, Ref, Clamped}, []model.Evidence
            ENG->>ST: ToolResultRef / ReasonerOutputRef written to store.ObjectStore (evidence/, reasoner/) — NEVER inline
            ENG->>JRN: AppendEvidence
            ENG->>VAL: Judge(hypothesis, evidence)
            VAL-->>ENG: Supports | Contradicts | Inconclusive
            ENG->>ENG: update Hypothesis.PostScore
        end
        ENG->>JRN: AppendStep phase=Test then Validate — ToolArgsJSON+ToolArgsHash, ToolResultHash, PromptHash, fsynced
        alt Contradicts
            ENG->>ENG: discard hypothesis, take next candidate
        end
    end
    end

    rect rgb(248, 240, 255)
    Note over ENG,AR: Phase 5 — Report
    ENG->>RSN: Conclude(State)
    RSN-->>ENG: model.Conclusion{RootCause, Confidence}
    ENG->>REP: Render(investigation)
    REP->>TOPO: BlastRadius from epicenter
    TOPO-->>REP: affected services
    REP->>REP: Proposals -> []model.ActionProposal, typed specs only (no free-form patch);<br/>RiskTier is PROVISIONAL — finalized later by remediate.Guard from the fixed table (DR-22 §22.4)
    REP-->>ENG: ReportMarkdown with cited trace IDs, log lines, metric ranges
    ENG->>JRN: SetStatus Concluded, TerminationReason recorded
    JRN->>ST: update investigation, steps, evidence; incident.status = Reported
    end

    ENG->>MEM: Record(tid, model.InvestigationRecord)
    MEM->>MEM: Fingerprint = anomaly.Fingerprint.Compute(tid, incident) — tenant INSIDE the hash preimage
    MEM->>MEM: embed (memory.embeddings.driver: lexical default), Weight=1.0, Provenance=LLMAuthored
    MEM-->>ENG: memory.Record id
    Note over ENG,MEM: memory.Store takes the portable model.InvestigationRecord DTO,<br/>never rca.Investigation — no import cycle (DR-2)

    ENG->>SMP: RemoveInterestPredicate(id) — phase A predicate
    alt Status=Concluded and Confidence >= rca.confidence_threshold
        ENG->>SMP: SetInterestPredicate — Scope=ScopeRecurrence, scoped by ErrorSigIDs+PathSigs ONLY,<br/>recurrence_ttl 24h (phase B, FR-F06-12b)
    end
    ENG->>AR: Route(incident, investigation)
    Note over AR: pages ONLY under the three conditions P1/P2/P3 (DR-21 §21.1) — never on Concluded alone,<br/>never on severity, and never on model.ServiceMeta.Tier
    alt P1: terminal Status AND >=1 model.Evidence from a step whose Verdict==ok
        AR->>AR: build payload — RootCause, Confidence, BlastRadius, top-3 evidence links, rca_state="full"
        AR->>AR: send to PagerDuty / OpsGenie / Slack per route match
        Note over AR: the page arrives WITH the RCA — D-D4, D-J1
    else terminal with no ok-verdict evidence, or below alerting.min_severity
        AR->>AR: post to Slack channel only, no page (P2's 5m hard ceiling and P3's SLO rule are evaluated independently)
    end

    alt any of six reasoner-swap triggers fire (transport x3, refusal x2, daily cap exhausted, queue saturation, schema x2, canary x2)
        ENG->>RSN: swap to rca.RulesReasoner mid-loop — investigation keeps its ID, steps and evidence, not restarted
        Note over ENG,RSN: ReasonerSwaps appends {AtStep,From,To,Reason,At}; Confidence capped at<br/>confidence_threshold-0.01 for the rest of the run; there is NO swap back (DR-34 §34.4)
    end
```

---

## 4. Natural-language question → intent → query → evidence answer (F10)

```mermaid
sequenceDiagram
    autonumber
    participant U as Engineer in Slack or web UI
    participant CH as chat adapter (nl.SlackAdapter / TeamsAdapter)
    participant SRV as api.HTTPServer
    participant AUT as auth.Authenticator + auth.Authorizer
    participant IDS as auth.IdentityStore
    participant AUD as auth.AuditSink
    participant SAN as rca.Sanitizer
    participant INT as nl.Interpreter
    participant LLM as Anthropic Claude API (llm.Client)
    participant ANS as nl.Answerer
    participant REG as rca.ToolRegistry
    participant ST as store.TieredStore
    participant TOPO as topology.LiveGraph
    participant MEM as memory.Store
    participant GRP as anomaly.Grouper
    participant GRD as remediate.Guard
    participant ENG as rca.Engine

    U->>CH: why is checkout slow since the 2pm deploy
    CH->>CH: verify HMAC signature, timestamp within 5m (authenticity only — NEVER authorization, DR-25 §25.4)
    CH->>SRV: POST /v1/ask with ChatContext{platform, workspaceID, channelID, threadID, userID}
    Note over SRV: NO tenant field in the body (DR-5) — the tenant comes ONLY from the resolved principal
    SRV->>IDS: Resolve(platform, workspaceID, platformUserID)
    alt unmapped chat user
        IDS-->>SRV: 403 identity_not_bound
        SRV->>AUD: Append decision deny, reason identity_not_bound
    else resolved
        IDS-->>SRV: auth.Subject{Tenant, Roles}
        SRV->>AUT: Require capability nl:ask
        AUT-->>SRV: allow
        SRV->>AUD: Append actor, action ask, decision allow
        SRV->>SRV: reject body over nl.max_question_bytes 4096
        SRV->>SAN: Wrap(UntrustedUserQuestion, text)

        SRV->>INT: Interpret(tid, Question{Text, Subject, Chat}, ConversationContext)
        Note over INT: ConversationContext keyed by (tenant, platform, workspace, channel, thread),<br/>Turns ring <= nl.context_turns 5, Focus = last incident / investigation / service / trace referenced
        alt interpreter = rules, or llm unavailable / no key (nl.interpreter: auto)
            INT->>INT: RulesInterpreter — deterministic pattern table, resolves ALL 10 intents, no network call;<br/>closed time-phrase grammar, entities restricted to topology services + span operations
            INT-->>SRV: nl.Intent{Kind, Args: rca.ToolArgs, Confidence}
        else interpreter = llm
            INT->>LLM: classify into IntentKind with a strict JSON schema
            LLM-->>INT: structured output
            INT->>INT: SchemaValidator validates; Service must exist in topology, Window clamped <= max_window 6h
            alt schema violation or unresolvable service
                INT->>INT: fall back to RulesInterpreter
            end
            INT-->>SRV: nl.Intent{Kind, Args: rca.ToolArgs, Confidence}
        end

        Note over INT: IntentKind (CLOSED, 10 values): TraceSearch, ServiceHealth, TopologyQuestion,<br/>IncidentStatus, InvestigationAsk, MemoryLookup, StartInvestigation, CompareWindows, ExplainTrace, Remediate

        SRV->>ANS: Answer(tid, intent, conversationContext)
        Note over ANS,REG: EVERY data access goes through rca.ToolRegistry.Dispatch — no second query path (DR-35 §35.3);<br/>an NL answer and an RCA step over the same window cite an IDENTICAL ToolResultHash (D-X3, AC-F10-10)
        alt IntentKind = ServiceHealth
            ANS->>REG: Dispatch(Metric{RED(checkout, window)})
            REG->>ST: QueryRED(service, operation, window)
            ST-->>REG: REDSeries
            ANS->>REG: Dispatch(Topology{checkout, hops 1})
            REG->>TOPO: Neighbors
            TOPO-->>REG: upstream and downstream RED
            ANS->>REG: Dispatch(Trace{slowest, limit 5})
            REG->>ST: SearchSpans
            ST-->>REG: exemplar traces
        else IntentKind = IncidentStatus or InvestigationAsk
            ANS->>GRP: ActiveIncidents(tid) — anomaly.Grouper.ActiveIncidents (DR-35 §35.3, DR-14 §14.1)
            GRP-->>ANS: []model.Incident
            ANS->>REG: Dispatch(...) resolves the named investigation + steps via the registry
            REG->>ST: load incident / investigation / steps
            ST-->>REG: investigation, evidence
        else IntentKind = MemoryLookup
            ANS->>REG: Dispatch(Memory{text, topK 8})
            REG->>MEM: Similar / Search
            MEM-->>REG: prior root causes, re-wrapped UntrustedMemory on retrieval (DR-19 §19.5)
        else IntentKind = StartInvestigation
            ANS->>SRV: requires capability incident:investigate
            SRV->>AUT: Require capability incident:investigate
            AUT-->>SRV: allow
            ANS->>ENG: Investigate(tid, incident)
            ENG-->>ANS: investigation id, streamed progress
        else IntentKind = Remediate
            ANS->>SRV: requires capability remediation_action:propose
            SRV->>AUT: Require capability remediation_action:propose
            AUT-->>SRV: allow
            ANS->>ANS: render a proposal FORM only — NEVER executes, NEVER approves
            Note over ANS,GRD: on user confirmation, routes through remediate.Guard.Propose<br/>with the chat user's resolved auth.Subject (DR-23, DR-25)
        end
        REG-->>ANS: model.ToolResult + []model.Evidence

        ANS->>ANS: renderWaterfall(exemplar trace) — ASCII in chat, SVG in UI
        ANS->>ANS: cap evidence at nl.answer_evidence_limit 10, scrub secrets
        ANS-->>SRV: nl.Answer{text, markdown, evidence, links, followUps}
        SRV->>AUD: Append action ask.answered with intent kind
        SRV-->>CH: 200 with nl.Answer
        CH->>U: threaded reply with mini-waterfall, RED sparkline,<br/>deep links /ui/trace/{id} and /ui/investigation/{id}
    end

    Note over U,ANS: answers are evidence-linked, never free-form assertions — D-Y3, D-X3
```

---

## 5. Remediation propose → approval → execute → verify → audit (F09)

Nine legal states, adopted verbatim (DR-23 §23.2): `Proposed(1)`, `Approved(2)`, `Rejected(3)`, `Executing(4)`, `Verifying(5)`, `Succeeded(6)`, `Failed(7)`, `RolledBack(8)`, `Expired(9)`.

```mermaid
sequenceDiagram
    autonumber
    participant ENG as rca.Engine
    participant SRV as api.HTTPServer
    participant GRD as remediate.Guard
    participant TOPO as topology.LiveGraph
    participant K8S as k8s.Executor (pinned kubectl, digest-verified)
    participant ST as store.TieredStore
    participant AUD as auth.AuditSink
    participant CH as chat adapter
    participant IDS as auth.IdentityStore
    participant AUT as auth.Authorizer
    participant HUM as Approver, role approver
    participant VER as remediate.Verifier

    ENG->>SRV: investigation concluded with []model.ActionProposal (typed specs only — no free-form patch, DR-22 §22.1)
    SRV->>GRD: Propose(tid, proposal, by=rca:<investigationID>, idem)
    Note over SRV,GRD: Idempotency-Key REQUIRED on propose/approve/reject/execute/rollback (DR-23 §23.8)

    rect rgb(255, 240, 240)
    Note over GRD,K8S: Guard checks — all must pass, none are advisory (State: Proposed)
    GRD->>GRD: remediate.enabled; Proposal.Type in remediate.allowlist
    GRD->>TOPO: resolveTarget — (1) topology existence: does (Namespace,Name) resolve in Snapshot(tid)?
    alt target not found in topology
        TOPO-->>GRD: unknown
        GRD-->>SRV: 422 target_not_resolvable
        GRD->>AUD: Append decision deny, reason target_not_resolvable
        Note over GRD: an LLM-invented target can never reach an executor (01 §1.1 P4)
    end
    TOPO-->>GRD: plausible target
    GRD->>K8S: (2) live existence — VerbGet the object
    K8S-->>GRD: object found, or absent -> 422 target_not_resolvable
    GRD->>GRD: stamp ResolvedUID, ResolvedVersion
    GRD->>GRD: (4) ONLY THEN evaluate NamespaceAllowlist / TargetAllowlist globs / RemediationAllowlist
    alt not matched by tenant.Policy allowlists
        GRD->>AUD: Append decision deny, reason target_not_allowlisted
        GRD-->>SRV: 403 target_not_allowlisted
    end
    GRD->>ST: used(incident) = count(actions WHERE state NOT IN (Rejected, Expired))
    alt used(incident) >= tenant.Policy.ActionBudgetPerIncident 2
        ST-->>GRD: budget exhausted
        GRD->>AUD: Append decision deny, reason action_budget_exhausted
        GRD-->>SRV: 429 budget_exceeded — mandatory human takeover,<br/>unless RequestBudgetOverride + OverrideJustification>=40 bytes (admin only, 1 override per incident)
    end
    end

    GRD->>K8S: Snapshot(target) — resourceVersion, generation, UID, spec, SHA256
    K8S-->>GRD: model.Snapshot (PreSnapshotJSON)
    GRD->>GRD: build the typed payload from the ActionSpec + pre-snapshot (§22.2) — NEVER from a model-authored string
    GRD->>K8S: DryRun — server-side dry-run apply (admission webhooks observe the request)
    K8S-->>GRD: diff
    GRD->>GRD: diff the computed payload against the pre-snapshot; refuse 422 payload_out_of_scope if it touches a path outside the allowed set
    GRD->>ST: insert action row State=Proposed, RiskTier from the fixed table (NEVER proposer-supplied)
    GRD->>AUD: Append action.propose, decision allow
    GRD-->>SRV: model.Action{ID, State=Proposed, ExpiresAt=ProposedAt+proposal_ttl 60m}

    SRV->>CH: PostApproval action with the typed-spec diff, rationale marked UNTRUSTED/model-authored, dry-run diff
    CH->>HUM: interactive message with Approve and Reject buttons

    alt proposal_ttl 60m elapses first
        GRD->>ST: State = Expired
        GRD->>AUD: Append action.expire
        CH->>HUM: expired notice
    else human presses Approve
        HUM->>CH: Approve
        CH->>SRV: POST /v1/actions/{id}/approve, HMAC verified, Idempotency-Key
        SRV->>IDS: Resolve(platform, workspaceID, platformUserID)
        alt unmapped
            IDS-->>SRV: 403 identity_not_bound
            SRV->>AUD: Append decision deny, reason identity_not_bound
        end
        IDS-->>SRV: auth.Subject
        SRV->>AUT: Require capability remediation_action:approve (role approver | admin)
        AUT->>AUT: SeparationOfDuty(proposer, approver) — approver holds NO propose capability, structural (DR-25 §25.2)
        alt by.ID == proposal.ProposedBy
            AUT-->>SRV: 409 approver_must_differ
            SRV->>AUD: Append decision deny
        end
        AUT-->>SRV: allow
        SRV->>GRD: Approve(tid, id, by, req)
        GRD->>ST: CAS Proposed->Approved, ExpiresAt = ApprovedAt + approval_ttl 15m
        GRD->>AUD: Append action.approve with approver identity
    else human presses Reject
        HUM->>CH: Reject with reason
        CH->>SRV: POST /v1/actions/{id}/reject
        SRV->>GRD: Reject(tid, id, by, reason)
        GRD->>ST: State = Rejected
        GRD->>AUD: Append action.reject
    end

    SRV->>GRD: Execute(tid, id, by, idem)
    GRD->>ST: CAS Approved->Executing, re-check now < ExpiresAt IN THE SAME TRANSACTION (DR-23 §23.3 — not the 30s poller)
    alt expired
        GRD-->>SRV: 409 action_expired — ZERO executor calls
        GRD->>AUD: Append decision deny, reason action_expired
    else still valid
        GRD->>TOPO: (3) re-resolve target INSIDE the same transaction, require an unchanged ResolvedUID
        alt UID changed (target replaced)
            GRD-->>SRV: 409 target_replaced
            GRD->>ST: State = Failed
        else unchanged
            alt remediate.mode = dryrun
                GRD->>K8S: dryrun Executor — simulated result, no mutation
                GRD->>ST: State = Succeeded, Verified=false, note dryrun
            else remediate.mode = execute (requires server.profile: prod)
                GRD->>AUD: Append action.execute.start
                GRD->>K8S: BuildArgv(Request) -> exec.CommandContext, MinimalEnv — patch bodies on STDIN, NEVER argv
                Note over K8S: scoped ServiceAccount, exactly 5 verbs on 5 kinds; forbidden-flag denylist checked
                alt API error or RBAC denial (403)
                    K8S-->>GRD: error
                    GRD->>ST: State = Failed, reason=scope_violation if RBAC-denied
                    GRD->>AUD: Append action.execute.fail
                    GRD->>ST: raise a Critical incident if RBAC denied
                else applied
                    K8S-->>GRD: Result{ExitCode 0}
                    GRD->>ST: State = Verifying, VerifyDeadline = now + verify_window 10m
                    GRD->>AUD: Append action.execute.ok
                end
            end
        end
    end

    loop every 30s until VerifyDeadline
        GRD->>VER: Verify(tid, action, RecoverySignal)
        VER->>ST: QueryRED epicenter service, post-execution window; open incident count
        ST-->>VER: RED deltas and open event count
        alt anomaly cleared and no new error signatures
            VER-->>GRD: VerifyResult ok
            GRD->>ST: State = Succeeded, Verified=true, PostVerifyJSON
            GRD->>AUD: Append action.verify.ok
            GRD->>CH: post success summary with before/after RED
        else deadline reached and the anomaly is still firing
            VER-->>GRD: VerifyResult failed
            GRD->>K8S: compare the live resourceVersion against the snapshot
            alt resourceVersion changed since snapshot
                GRD->>ST: State = Failed, reason=concurrent_modification
                GRD->>ST: raise Critical incident rollback_refused_concurrent_modification
                GRD->>CH: page approval channels IMMEDIATELY, regardless of alerting.min_severity
            else unchanged
                GRD->>K8S: Rollback using PreSnapshotJSON — consumes a budget slot, pre-authorized by the original approval, no 2nd approval
                GRD->>ST: State = RolledBack
                GRD->>AUD: Append action.rollback
                GRD->>CH: post rollback notice, escalate to human
            end
        end
    end

    Note over AUD: every row: H(n)=SHA256("traceiq.audit.v1" || LP(H(n-1)) || LP(seq) || LP(ts) || LP(canonicalJSON(row))) (DR-27 §27.1);<br/>a signed checkpoint is anchored every auth.audit.anchor_interval 5m and at shutdown, to a destination TraceIQ cannot rewrite — D-X4
```

---

## 6. RCA feedback predicate → sampler interest update (P6, D-X1, D-X5)

```mermaid
sequenceDiagram
    autonumber
    participant ENG as rca.Engine
    participant SMP as sampler.Sampler
    participant PS as sampler.PredicateSet
    participant ST as store.TieredStore (control.db: sampler_interest)
    participant SH as sampler.Shard
    participant POL as sampler.Policy

    Note over ENG: investigation opens on an incident with epicenter checkout, blast radius payment and inventory

    rect rgb(238, 244, 255)
    Note over ENG,PS: Phase A — pushed at Contextualize, before the first Hypothesize step (FR-F06-12a)
    ENG->>ENG: derive InterestPredicate — Scope=ScopeInvestigation<br/>Services = {epicenter} union BlastRadius, truncated to 32<br/>MinDuration = baseline.P95(epicenter, rootOperation), ErrorsOnly=false<br/>ExpiresAt = now + sampler.interest.scope_ttl 30m
    ENG->>SMP: SetInterestPredicate(tid, predicate)
    SMP->>PS: Add(predicate)
    alt registry at sampler.interest.max_predicates 32 (per tenant, both scopes combined)
        PS->>PS: evict lowest-Hits, soonest-expiring predicate
        alt that would evict a ScopeInvestigation predicate of a RUNNING investigation
            PS-->>SMP: 429 predicate_limit — refused
        end
        PS-->>SMP: traceiq_sampler_predicates_evicted_total++
    end
    PS->>ST: persist sampler_interest row so a restart keeps the predicate
    PS-->>SMP: predicate id
    SMP-->>ENG: id
    end

    Note over PS,SMP: PredicateSet is read via an atomic snapshot pointer — lock-free on the decision path

    loop every completed trace while a predicate is live
        SH->>PS: Match(trace) — union candidate sets over <=20 distinct services,<br/>error signatures and path signature; <=32 candidates x O(1) lookups, p99 ~150us (AC-F02-15)
        alt matched
            PS->>PS: Hits++ (fires whenever a predicate matched — independent of the winning Reason)
            PS-->>SH: InterestMatch{Matched, PredicateID, Scope}
            SH->>POL: Evaluate(trace, baseline, match, governor)
            POL-->>SH: Decision{Reason=KeepInterest (or a higher-precedence class), MatchedPredicateID, Tier=TierFull}
            SH->>ST: WriteTrace(trace, TierFull)
            Note over SH,ST: traces the NEXT investigation step needs are retained at 100%, by construction
        else no match
            PS-->>SH: no match
            SH->>POL: Evaluate with the normal six-class policy
        end
    end

    alt rolling-60s keep rate exceeds narrow_at_keep_rate 0.25 (== sampler.policy.max_keep_rate)
        POL->>PS: Narrow(level)
        Note over PS: level 1: MinDuration -> epicenter's p99<br/>level 2: ErrorsOnly = true<br/>level 3: Services -> epicenter only<br/>level 4: expire the lowest-Hits ScopeRecurrence predicate
        PS->>PS: NarrowLevel set, traceiq_sampler_predicate_narrowed_total++
        Note over POL,PS: a runaway predicate can never turn the sampler into store-everything
    end

    alt investigation concludes, aborts, or budget exhausts (any terminal Status)
        ENG->>SMP: RemoveInterestPredicate(tid, id)
        SMP->>PS: Remove(id)
        PS->>ST: delete sampler_interest row
    else ExpiresAt reached first
        PS->>PS: ExpireDue(now)
        PS->>ST: delete expired rows
    end

    rect rgb(240, 248, 240)
    Note over ENG,PS: Phase B — pushed ONLY on Status=Concluded AND Confidence>=rca.confidence_threshold (FR-F06-12b)
    ENG->>ENG: derive a NEW predicate — Scope=ScopeRecurrence, scoped by ErrorSigIDs union PathSigs ONLY<br/>(never by service alone — that would be a store-everything switch)<br/>ExpiresAt = now + sampler.interest.recurrence_ttl 24h
    ENG->>SMP: SetInterestPredicate(tid, predicate)
    SMP->>PS: Add(predicate) — max_recurrence_predicates 8 per tenant, a subset of max_predicates 32
    Note over PS: removed on TTL, on DELETE /v1/sampler/interest/{id} (operator),<br/>or when a later investigation for the SAME Incident.Fingerprint concludes and supersedes it
    end

    Note over ENG,ST: this loop is the wedge — the agent defines what interesting looks like<br/>and the sampler enforces it upstream of storage (D-X5)
```

---

## 7. Engineer correction → memory update → replay (P3, D-Y1)

```mermaid
sequenceDiagram
    autonumber
    participant HUM as Engineer, role operator or approver
    participant UI as Embedded SPA investigation view
    participant SRV as api.HTTPServer
    participant AUT as auth.Authorizer
    participant AUD as auth.AuditSink
    participant ENG as rca.Engine
    participant JRN as rca.Journal
    participant ST as store.TieredStore (control.db + store.ObjectStore evidence/reasoner refs)
    participant MEM as memory.Store
    participant CONS as memory consolidation (nightly, leader-only)
    participant REG as rca.ToolRegistry

    HUM->>UI: open /ui/investigation/{id}
    UI->>SRV: GET /v1/investigations/{id}/steps
    SRV->>JRN: Steps(tid, id)
    JRN->>ST: select investigation, investigation_step, evidence (refs + hashes only; bodies live in ObjectStore)
    ST-->>JRN: full step log
    JRN-->>SRV: model.Investigation{Steps[], ToolArgsJSON, ToolResultHash, ToolResultRef, Spend}
    SRV-->>UI: every hypothesis, exact query, evidence and cost is visible
    Note over UI: transparency contract — nothing is hidden behind a score

    HUM->>UI: step 7 is wrong, the p99 rise is the cache warm-up, not the DB
    UI->>SRV: POST /v1/investigations/{id}/steps/{stepID}/correct
    SRV->>AUT: Require capability investigation:correct (operator or approver or admin)
    AUT-->>SRV: allow
    SRV->>AUD: Append action investigation.correct, subject stepID
    SRV->>ENG: Correct(tid, investigationID, stepID, model.Correction)

    ENG->>JRN: load investigation, check Version for optimistic concurrency
    JRN-->>ENG: investigation version n
    ENG->>JRN: mark step Corrected, CorrectionNote, CorrectedBy, NewOutcome
    ENG->>ENG: recompute Hypothesis.PostScore and Investigation.Confidence
    ENG->>JRN: write investigation version n+1, RootCause updated if supplied
    JRN->>ST: update rows — original step text preserved, corrections APPEND, never overwrite

    ENG->>MEM: Correct(tid, model.Correction)
    MEM->>ST: locate memory_record by SourceInvestigationID (tenant-scoped, DR-19 §19.1)
    ST-->>MEM: original record
    MEM->>MEM: original.Weight -= 0.35 (floor 0.0), Corrections++
    MEM->>MEM: insert a NEW record: Kind=Correction, SupersedesID=original.ID, Weight=1.0, Provenance=HumanCurated
    MEM->>MEM: embed the corrected symptom+root cause (memory.embeddings.driver: lexical default), index lexically
    MEM->>ST: persist both records
    MEM-->>ENG: correction record id
    Note over MEM: retrieval ordering: a superseded record returns ONLY together with its superseder,<br/>always ranked below it; effectiveWeight = Weight x 0.5^(age/90d), applied at READ time, never written<br/>— the flywheel Dynatrace keeps for itself, D-Y4

    HUM->>UI: Replay this investigation
    UI->>SRV: POST /v1/investigations/{id}/replay?mode=recorded|live-diff (default recorded)
    SRV->>AUT: Require capability investigation:replay (operator or admin)
    SRV->>ENG: Replay(tid, investigationID, mode)

    alt mode = recorded (default)
        loop each step, in Seq order
            ENG->>JRN: fetch ToolResultRef from store.ObjectStore, verify sha256 == ToolResultHash
            alt hash mismatch
                ENG->>ENG: ABORT — ErrEvidenceCorrupt, raise a Critical incident (FR-F06-24)
            end
        end
        ENG->>ENG: re-run SchemaValidator -> Validator -> Reporter over the CACHED results plus the correction
        Note over ENG: the reasoner is NOT called — deterministic, ZERO tool calls, ZERO LLM tokens, Spend={0,0,0,0}
    else mode = live-diff
        loop each tool step, in Seq order
            ENG->>REG: Dispatch with the EXACT stored ToolArgsJSON (parsed back into the typed struct)
            REG-->>ENG: fresh model.ToolResult
            alt parse failure
                ENG->>JRN: Drift = schema_changed
            else sha256(new) == ToolResultHash
                ENG->>JRN: Drift = none
            else differs
                ENG->>JRN: Drift = data_drifted, NewToolResultRef written (both bodies inspectable side by side)
                Note over ENG,JRN: distinguishes a reasoning error from changed underlying data — the D-Y1 differentiator
            end
        end
        Note over ENG: the reasoner is STILL NOT called — re-reasoning would confound drift with LLM nondeterminism;<br/>ReplayReReason is an explicit Phase 3 deferral. Spend: tool cost only, tokens = 0
    end

    ENG->>JRN: write the replay as a NEW investigation, new ID, ParentID=original, ReplayOf=original
    JRN->>ST: insert investigation rows — the ORIGINAL remains immutable
    ENG-->>SRV: replayed investigation (two `recorded` replays are byte-identical modulo IDs/timestamps)
    SRV-->>UI: side-by-side diff, original vs corrected reasoning, per-step drift badges
    UI->>HUM: shows which conclusions changed and why

    loop nightly at memory.consolidation.hour_of_day 3 (leader-only in K8s)
        CONS->>MEM: Consolidate(now)
        MEM->>MEM: banded MinHash LSH (128 permutations, 32 bands x 4 rows) over fingerprint tokens<br/>— candidate pairs only within a shared band bucket, examines <= 2% of all pairs
        MEM->>MEM: decay Weight by a 90d half-life on LastUsedAt (computed at read time, not stored)
        MEM->>MEM: prune Weight below floor AND older than prune_after_days 400 (aligned to T5 retention)
        MEM->>ST: persist consolidation report
        Note over CONS,MEM: published: completes within memory.consolidation.max_duration 15m at 100k records
    end
```

---

## Cross-cutting invariants visible in every flow

| Invariant | Where enforced |
|-----------|----------------|
| RED metrics are extracted before any discard, at the router, before the shard send | Diagram 1 — decode/route worker, before `ShardFor` (DR-9) |
| An anomaly event never pages a human directly; there is no severity- or tier-derived bypass | Diagram 2 — `Grouper`; paging gated by the three P1/P2/P3 conditions (DR-21 §21.1) in diagram 3 and elsewhere |
| The reasoner only ever sees rendered, capped, delimited tool results | Diagram 3, `Sanitizer.Wrap` and `ToolRegistry` row/byte caps (`max_evidence_bytes` 8192) |
| No tool takes a URL, host, or port from the model; every argument is typed and semantically validated | Diagram 3, `SchemaValidator.ValidateToolArgs` (DR-16 §16.3) |
| A target the topology graph does not know can never be mutated, and re-resolution runs again at Execute | Diagram 5, `resolveTarget` (propose) and the transactional re-resolve (execute) — DR-22 §22.3 |
| Approval is always an identity-bound human with the `approver` role, distinct from the proposer | Diagram 5, `IdentityStore.Resolve` + `SeparationOfDuty` (DR-25 §25.4, DR-23 §23.4) |
| Every denial and every state transition writes a hash-chained, externally anchored audit row | Diagrams 4, 5, 7 (DR-27) |
| A runaway interest predicate can never turn the sampler into store-everything | Diagram 6, `PredicateSet.Narrow` |
| Corrections append; the original investigation is immutable; replay never re-invokes the reasoner | Diagram 7 |
| The LLM is never on a critical path — every branch has a deterministic fallback, and a mid-loop swap never reverses | Diagrams 3 and 4 (DR-34 §34.4) |
