# PM Sign-Off — TraceIQ v1

**Reviewer:** shakrath@gmail.com, Product Manager
**Date:** 2026-09-20
**Basis:** `Tracing-Bot-PRD.md` (2026-08-31, Draft v1.0), `docs/architecture/00-feature-catalog.md` (Rev 2), `docs/reports/w18-live-test.md`, `docs/ledger.md` (through 2026-09-20 08:57, Wave 18 close)

---

## Scope of this review

This is a scope sign-off, not a code-quality re-audit. Waves 1–17 (design, implementation, three review passes) and Wave 18 (live Istio mesh test) are treated as closed and correct on their own terms. The question answered here is narrower: **does what got built, and what got proven, match what the PRD promised — and does the PRD's "better than X" table now have real evidence behind it, or is it still a pitch?**

---

## Feature-by-feature delivery table

| ID | Feature | Status | Evidence |
|----|---------|--------|----------|
| F01 | Multi-format OTel-native ingest | **Delivered** | Live test: 47/47 real OTLP export batches from a real Istio sidecar reached TraceIQ's ingest listener (`w18-live-test.md` step 7/9). |
| F02 | Anomaly-aware tail sampler | **Delivered** | Live test: 89 real error traces from the S13 incident kept with `KeepReason=KeepError` against real mesh traffic — the `keep_errors:true` policy confirmed working end-to-end, not just in unit tests. |
| F03 | Open tiered storage | **Delivered with gap** | Hot index (SQLite) proven live and queryable post-incident. **Gap:** live test uncovered W18-D1 — every span in a multi-hop trace was stamped with the trace's root service instead of its own service (`internal/store/store.go`); fixed same-day with a TDD regression test (`docs/ledger.md` 2026-09-20 08:57). Cold-store Parquet durability was configured but never round-tripped (written-and-read-back) in the live test — only the hot index was verified. |
| F04 | Live topology graph | **Delivered with gap** | Code implemented; Wave 17 final review found and fixed 3 topology-eviction bugs (tie-break nondeterminism, two unbounded maps, WarmStart bypassing max_edges). **Gap:** not exercised against real mesh spans in the live test — no confirmation the graph actually populated from the S13 traffic. The Envoy-access-log topology adapter (`FR-F04-10`) ships behind a default-`false` flag and was not turned on for the live test. |
| F05 | Streaming anomaly detection | **Delivered** | Live test: real `ErrorBurstDetector` fired on real observed data (`observed=0.989 baseline=0.000 score=0.994`) — the anomaly→incident trigger chain worked end-to-end. |
| F06 | Agentic RCA engine | **Delivered with gap** | The rules-reasoner path is genuinely proven live: walked the priority-ordered rule catalog, correctly refuted 3 unrelated hypotheses for lack of evidence, and confirmed `connection-pool-exhaustion` at 0.80 confidence with real captured evidence. **Gap, and it's the important one:** the PRD's headline is an *LLM-driven* hypothesis loop — that path is security-reviewed (Wave 16, prompt-injection defenses fixed) but was never exercised live; only the deterministic rules fallback ran. Further, in the actual shipped binary 4 of the 5 tool backends (`TraceStore`, `LogStore`, `MetricStore`, `TopologyStore`) are no-op stubs (`cmd/traceiq/stubs.go`) — the live test's RCA conclusion depended on a `MetricStore` substituted by hand in a standalone verification harness outside the deployed pod, not on the real binary's own evidence-gathering. |
| F07 | Trace–log–metric correlation | **Deferred / not delivered** | `internal/correlate` exists and was reviewed (Wave 12), but its backends (Loki/ES/Prometheus adapters) are unimplemented no-op stubs in the running binary. Never demonstrated joining a trace to a real log line or metric. `FR-F07-8`'s startup capability check is a documented deployment prerequisite, not evidence correlation runs. |
| F08 | Investigation & runbook memory | **Delivered with gap** | `internal/memory` implemented and reviewed (Wave 12). No live-test evidence — the symptom-fingerprint→root-cause retrieval loop and engineer-correction feedback were never exercised against real incident data. |
| F09 | Guarded remediation | **Deferred / not delivered** | `internal/remediate` and the pinned `k8s.Executor` are implemented and reviewed, but the feature catalog itself records "Execution via existing operators (ArgoCD rollback, …)" as a **Phase 3 deferral** — the five closed `model.ActionType` values are v1, unwired to any live approval flow. Zero live proof; no remediation action was taken or tested in Wave 18. |
| F10 | Natural-language interface | **Deferred / not usable today** | `internal/nl` (10-intent taxonomy) is implemented and reviewed, but `cmd/traceiq` does not wire `internal/api` at all — a deliberate Wave 16 decision because `internal/auth` has zero concrete implementations, and wiring now would silently 401 every route. There is currently no way to actually talk to TraceIQ in natural language against the shipped binary. |
| F11 | Eval harness | **Delivered with gap** | `internal/eval` (real `ingest.Receiver`-driven runner, deterministic `VirtualClock`) is implemented and reviewed (Wave 14). **Gap:** the PRD promises "published, re-runnable accuracy metrics" across the S01–S23 Istio catalog; only S13 was run, and it was run manually outside the eval harness (a bespoke Wave 18 test), not as an automated harness sweep. No accuracy numbers have been published for any scenario. |
| F12 | Web UI + API + integrations | **Deferred / not usable today** | Same root cause as F10: API server exists and was reviewed (16/34 REST endpoints implemented ≈47%, 3/12 MCP tools real, SPA serves clean HTML with no XSS but a deferred CSP gap) but is never wired into the running binary. Grafana datasource contract and PagerDuty/OpsGenie routing have no implementation evidence at all. |
| X-SEC | Security (TLS/mTLS, auth, RBAC, secrets, audit) | **Delivered with gap** | RBAC fail-closed behavior, hash-chained audit sink, and prompt-injection/schema-validation defenses were specifically checked and hardened across Waves 16–17. **Gap:** `internal/auth` has **zero concrete implementations** — the live test itself only worked by running with `auth.mode: none` (the explicit dev-only bypass path). No real deployment can currently turn auth on. |
| X-OPS | Deployment (single binary, Helm/K8s, multi-tenancy, self-obs) | **Delivered with gap** | Single-binary dev mode is the one deployment path proven live: built, deployed, clean startup, healthy `/readyz`, clean shutdown, clean teardown. Wave 17 found and fixed 3 Blocker-severity cross-tenant data-isolation bugs (trace-ID collision across tenants, empty-tenant fallback, tenant-blind WAL truncation) — serious, but caught and closed before ship. **Gap:** no Helm chart or production multi-replica deployment was built or tested; the live test used a hand-written raw Deployment/ConfigMap that was deleted afterward. |

**Tally: 3 fully delivered (F01, F02, F05) · 7 delivered with a documented gap (F03, F04, F06, F08, F11, X-SEC, X-OPS) · 4 deferred/not usable today (F07, F09, F10, F12).**

---

## Cross-check: PRD's "How This Is Better Than Other Compared Tools" table

| Vs. | PRD claims fixed | Real evidence today | Verdict |
|---|---|---|---|
| **Jaeger** | Adds alerting, agentic RCA, NL chat, correlation | Alerting/anomaly detection and anomaly-aware sampling: **proven live**. RCA: proven live only for the rules-fallback path with a hand-substituted metric backend, not the shipped binary's own evidence pipeline. NL chat: **not reachable at all** (F10 unwired). Correlation: **not implemented** (F07 stubs). | **Partially backed.** The core "watch + sample + detect" loop is real; "RCA + NL + correlation" — the actual intelligence layer Jaeger lacks — is either unproven or unusable in the current binary. |
| **Zipkin** | OTel-first ingest, live topology, adaptive/durable storage, full intelligence layer | Ingest and sampling: **proven live**. Topology graph: implemented, reviewed, bugs fixed, but **not exercised against live spans**. Durable storage: hot index proven; cold-store durability **unverified**. "Full intelligence layer": same gaps as above. | **Partially backed**, weighted toward the ingest/storage claims rather than the intelligence claims. |
| **Grafana Tempo** | Semantic hot index fixes query speed; ships the agent Tempo only hooks; self-contained UI | Hot index exists and was used live to query 89 real traces successfully — but there is **no query-speed benchmark** against Tempo's Parquet-scan baseline, so the "fixes query speed" claim is still asserted, not measured. "Ships the agent": true only for the rules reasoner; the LLM agent path is unexercised. "Self-contained chat-first UI": **not true today** — the UI/API is built but unwired, so TraceIQ is currently *more* Grafana-adjacent-dependent than the PRD implies, not less. Tempo-as-backend interop is explicitly a Phase 3 deferral per the feature catalog. | **Weakest-backed of the five.** Several claims here are still design intent. |
| **Datadog APM** | Predictable infra-linear cost; budget-capped LLM spend; retains the anomalous tail; native/portable OTLP; investigation-gated alerting (page only after evidence); transparent replayable logs vs. silent regressions | Retention-of-the-anomalous-tail: **proven live** (89/90 error traces kept). Investigation-gated alerting: **proven live** — the detector→incident→RCA chain actually ran before anything would have paged. Native OTLP ingest: **proven live**. Cost figures: still a **projection** (the ROI section's own footnote says the $0.08–$0.50/investigation figures are computed from assumed token counts, not measured spend). Replayable investigation logs: code-reviewed for injection-safety but replay itself was not exercised in the live test. | **Best-backed of the five** — the two claims most central to Datadog's specific pain points (bill shock via retention-that-matters, and alert-gating) both have live evidence, not just design. |
| **Dynatrace** | Transparent/correctable RCA vs. black-box Davis; accessible single-node cost; NL-first UX; exportable intelligence | Transparent RCA: **partially proven** — the live test's step-by-step rule refutation (3 hypotheses tried, refuted, one confirmed with cited evidence) is a real, inspectable trace of reasoning, which is the actual substance of this claim. Single-node accessibility: **proven live** (one pod, embedded SQLite, no external dependencies). NL-first UX (the PRD's own example: "why is checkout slow since the 2pm deploy?"): **not deliverable today** — no wired API/chat path exists. Exportable intelligence (JSON/Markdown reports, memory): implemented but **not demonstrated**. | **Mixed** — the transparency and cost-accessibility claims are real; the signature NL-UX claim, ironically the PRD's own headline example, is currently unusable. |

---

## Honest assessment

The engineering process behind this build was unusually rigorous — three independent review waves, a Wave 17 pass that caught three genuine cross-tenant data-isolation Blockers before ship, and a Wave 18 live-cluster test that is real evidence, not a simulation: real Istio mesh, real fault injection, real spans, a real defect found and fixed same-day. That is not nothing, and it is more validation than most v1s get.

But measured strictly against the PRD's own "How This Is Better" table, the product as it stands today proves the **collection and detection** half of the pitch (ingest, anomaly-aware sampling, anomaly detection, and a deterministic rules-based RCA path) and has **not** proven the **intelligence and access** half that the PRD leads with. Specifically:

- The PRD's own worked example — a natural-language question answered by TraceIQ — cannot be run today. `internal/api` and `internal/nl` are implemented and reviewed but deliberately left unwired because `internal/auth` has no concrete implementation. This is the right engineering call (don't wire a fake-secure auth layer), but it means the "NL-first, no query language required" claim versus Dynatrace, and the "chat-first UI" claim versus Tempo, are currently **not demonstrable**, not "delivered with a gap" in the cosmetic sense — a user cannot talk to TraceIQ.
- Trace–log–metric correlation (F07) — one of the five cross-cutting gaps the PRD says "no compared tool solves" — has zero working backend today. This is a specific, repeated PRD claim (vs. Jaeger, vs. Zipkin, in the cross-cutting list) that remains entirely aspirational.
- The RCA engine's live proof used the deterministic rules fallback with a hand-substituted metric source, not the LLM hypothesis loop the PRD centers, and not the binary's own (currently stubbed) evidence tools. The reasoning *logic* is proven; the reasoning *pipeline as shipped* is not.
- Guarded remediation (F09) is explicitly scoped to Phase 3 in the feature catalog itself — good that it's honestly labeled, but it means one of the PRD's five cross-cutting differentiators ("remediation stops at suggestion — TraceIQ: guarded remediation") is currently in the same state as every competitor it criticizes: suggestion-only, because there's no suggestion path either yet.

None of this is being oversold in the project's own internal docs — the feature catalog's "PRD promises → owning FR or explicit deferral" table (DR-38) and the ledger are candid about every gap above. The risk is entirely in how this gets represented *externally*: the PRD's comparison table, read on its own, claims more than is currently true. Anyone citing "How This Is Better Than Other Compared Tools" as a customer-facing document today would be overstating the product's current state on at least the Tempo and Dynatrace rows.

---

## Sign-off statement

**APPROVED WITH NOTED GAPS.**

The core, hardest-to-fake claim of this PRD — that TraceIQ co-designs the sampler and the agent so the traces that matter are the traces that exist — is genuinely proven end-to-end against a real Istio mesh. Engineering process (three review waves, live testing, honest internal gap-tracking) meets a high bar. I am comfortable signing off on this as a v1 foundation.

This is not a sign-off on the PRD's comparison table as customer-facing collateral in its current form. Before any external use of that table: (1) the NL/API layer needs to be wired (blocked on `internal/auth` getting a real implementation — this should be the next wave's top priority, not Wave 19's sign-off swarm), (2) F07 correlation needs at least one working backend, and (3) the eval harness needs an actual automated run across the S01–S23 catalog with published numbers before "published, re-runnable accuracy metrics" is said out loud to anyone outside this repo.

— shakrath@gmail.com, Product Manager
2026-09-20
