# TraceIQ build ledger — plan: docs/architecture/00-feature-catalog.md
Started 2026-09-14. Orchestrator = main session. Roles: Architecture board, PM, Dev lead, Test lead, Coding agents.
Toolchain on box: Go 1.23.1, Node 22.15, Python 3.11, Docker 29, git 2.29. Repo: Tracing-bot (main, remote GitHub). No commits/pushes unless user asks.

## Phase 1 — Architecture docs
- [ ] 01-system-architecture.md (+ class, sequence, execution diagrams)
- [ ] features/F01..F12 + X-SEC + X-OPS
- [ ] 05-tool-selection-adr.md
- [ ] architecture_review.md round 1 → fixes → round N until sign-off

## Dependency pins verified compatible with Go 1.23 (newer releases require Go >= 1.24)
- go.opentelemetry.io/proto/otlp v1.9.0
- google.golang.org/grpc v1.75.1
- google.golang.org/protobuf v1.36.12
- modernc.org/sqlite v1.39.0
- github.com/parquet-go/parquet-go v0.25.1
- golang.org/x/sync v0.16.0

## Toolchain decision (orchestrator, 2026-09-14 16:20)
- GOTOOLCHAIN=auto on build box: go.mod `go 1.27` + `toolchain go1.27.1` auto-downloads Go 1.27.1 (verified). Resolves ADR FU-1 (Go 1.23 EOL, 25 reachable CVEs) with zero system install.
- Set B pins verified compiling CGO_ENABLED=0 on go1.27.1: otlp v1.11.0, grpc v1.83.2, protobuf v1.36.12, modernc.org/sqlite v1.58.0, parquet-go v0.32.0, x/sync v0.23.0.
- govulncheck (GOTOOLCHAIN=go1.27.1): 0 reachable vulnerabilities (1 in imported pkg not called).
- Agent temp dir .tmp-depcheck/ left in repo (rm denied by permissions) — user to delete.

## Phase 1 status
- [x] 01..05 architecture docs written (01:1913 02:1587 03:731 04:312 05:487 lines; 16/16 mermaid verified)
- [x] features/F01..F12, X-SEC, X-OPS written (14 docs, ~5,700 lines)
- [ ] Round 1 board review in progress: security-reliability, performance-data, completeness-coherence (opus)

## Round 1 board review results (2026-09-14 16:38)
- Security-Reliability: APPROVE-WITH-CHANGES — 7 Blocker / 16 Major / 7 Minor (SR-1..30)
- Performance-Data: REJECT — 16 Blocker / 14 Major / 5 Minor (PD-1..35)
- Completeness-Coherence: REJECT — 8 Blocker / 15 Major / 10 Minor (CC-1..33); 8 drawbacks rated Weak
- Total 79 findings. Root cause: 01-system-architecture specifies mechanisms feature docs do not implement; contradictions in cold store, sharding, paging, interest predicate, replay, tenancy, remediation params, RCA budget, hot index schema/sizing.
- Fix wave step 1 dispatched: chief architect -> docs/reviews/architecture_review.md + docs/architecture/06-decision-register.md

## 2026-09-15 06:30 resume (session limit reset)
- Chief architect agent (opus) 3rd attempt: finishing DR-14+ and docs/reviews/architecture_review.md (DR-0..13 survived, 1208 lines).
- go.mod landed (DR-1): module traceiq, go 1.27, toolchain go1.27.1. VERIFY-ON-LAND rows resolved: klauspost/compress v1.20.0, x/time v0.16.0, x/crypto v0.57.0, go-cmp v0.7.0, prometheus/client_golang v1.24.1, otel+sdk v1.46.0. go build ./... OK (CGO_ENABLED=0), govulncheck 0 reachable (1 in required module, not called). internal/depstub pins graph until real imports exist.
- Credit policy: sonnet for doc-fix, coding, per-feature review; opus only for decision register + final gates.

## 2026-09-15 07:05 — Decision register complete
- 06-decision-register.md: 2966 lines, DR-0..DR-39 (40 decisions), Appendix A maps all 98 findings (true count: SR 30 + PD 35 + CC 33 = 98, not 79), Appendix B 14 ACCEPT-MODIFIED rulings, Appendix C new FR/AC IDs. 0 REJECT.
- docs/reviews/architecture_review.md: 182 lines, round-1 log with dispositions.
- Fix wave step 2 dispatched (sonnet x4): system docs 01-04; F01-F04; F05-F08; F09-F12+X-SEC+X-OPS+05+00. Reports -> docs/reports/fix-round1-*.md
- Scaffold agent dispatched (sonnet): internal/model, tenant, canonical interfaces per package, archtest import-boundary test, cmd/traceiq stub, CI scripts.

## 2026-09-15 07:28 — HALT (user: usage 75%+). All 5 in-flight agents killed by orchestrator.
- Killed: doc-fix 01-04 (reading, no edits), F01-F04 (had started editing F01 §4.2 — partial), F05-F08 (reading), F09-F12/X/ADR/00 (reading), scaffold (reading, no files written).
- RESUME: re-dispatch all 5 with identical prompts (see 07:05 entries; prompts in session transcript / CHECKPOINT NEXT step 1). Cron 11:28 AM set. No dispatches before reset.

## 2026-09-15 12:40 — USER BUDGET CAP: $30 remaining. Plan: expected ~$18, hard stop $28. Cost ledger: docs/cost-ledger.md. Implementation batched to 8 coder agents; haiku for mechanical fixes; opus only round-2 verify + final code review.

## 2026-09-15 17:48 — Wave 1 (7/8 done; 01 running). Docs: F01-F12, X-SEC, X-OPS, 00, 02-05 fixed/verified (02-04 structure only, verbatim diff deferred to round-2 gate). Scaffold: auth, k8s, archtest, CI, README done; feature-package interfaces pending → Wave 2a dispatched (3 sonnet: store/ingest/sampler/topology/bus; anomaly/rca/correlate/memory/llm; remediate/nl/eval/api/config/selfobs/cluster). Reports: docs/reports/w1-*.md

## 2026-09-15 22:42 — Wave 3a (scaffold cleanup) complete. 210.8k sonnet tokens ≈ $0.26. Running total ≈ $3.87.
- All 6 fixes done: archtest path-sep + ScopeCoverage subtest; store/{sqlite,parquet,tiered} doc.go; deduped ErrorSignature/Edge/Direction/ToolResult; Resolution triplication resolved to model.Resolution only (DR-39 tie-break); nl.ToolArgs -> rca.ToolArgs; api.Deps completed (Ingest deliberately omitted, documented).
- build/vet/test/gofmt: all green.
- Ambiguities for round-2 review: api.Deps field list not printed verbatim in register (reconstructed from DR-29/30); model.ToolResult fields never specified anywhere (TODO carried on the type).
- Report: docs/reports/w3-cleanup.md
- Still running: 01-doc DR-14..39 finisher (w3-01.md pending).

## 2026-09-15 23:25 — Wave 3 COMPLETE. 501.5k sonnet tokens (01-doc, 4971s) ≈ $0.63. Running total ≈ $4.50.
- 01-system-architecture.md: DR-0..DR-39 fully applied (3261 lines, was 2429). Report: docs/reports/w3-01.md
- Scaffold cleanup (w3-cleanup.md): all 6 fixes verified, build/vet/test/gofmt green.
- ARCHITECTURE DOC PHASE COMPLETE: all docs (00,01,02-05,F01-F12,X-SEC,X-OPS) now cite DR-0..DR-39. Go scaffold: all packages have canonical interfaces, no known defects open.
- USER: session usage 56%, resets Sep 16 3:00 AM ET. User asked (via /prompt-master, redirected): pause here, auto-resume 3:05 AM.
- PAUSING. No new dispatches tonight. Cron set for 3:05 AM 2026-09-16.
NEXT ON RESUME: Wave 4 = round-2 board verification (ONE opus agent, scoped: verify all 98 findings via Appendix A -> DR -> doc text, list new Blocker/Major only) -> docs/reviews/round2/verification.md. Then architecture sign-off. Then implementation waves (batched coders, TDD, per-batch review). See docs/CHECKPOINT.md for full NEXT sequence.

## 2026-09-15 23:56 — Wave 4 (round-2 verify) COMPLETE. 350.6k opus tokens ≈ $1.10. Running total ≈ $5.60.
- Verdict: NEEDS-FIX-ROUND. 89 ADDRESSED / 9 PARTIAL / 0 NOT-ADDRESSED of 98. New: 5 Major (N2-1,2,3,4,5,7 map to open items below) + 6 Minor (N2-6,8,9,10,11 + one more).
- Root cause: DRs fixed their named "Docs to change" targets but sibling sections restating the same fact elsewhere were not swept.
- Open Major (7, one wave): N2-1 05§6 stale GOTOOLCHAIN=local/allowlist/analyser-pin contradiction; N2-2 F01§7 AC-F01-4/9 stale overflow_policy+load-test text (DR-28); N2-3 05 kubectl pin/digest/CVE-owner still TBD (DR-24, was Blocker CC-6); N2-4 api.Deps mismatch vs 02§4 15-field spec + orphan anomaly_Engine in 02§2; N2-5 PRD ROI LLM-spend row not recomputed from DR-17/34; N2-7 F01§3.2/FR-F01-1 still declares own perf numbers instead of citing 01§10.1/10.2.
- Minor (park or bundle): stale Set-A pins 05§6; AC-ID renumbering F09/F11/F12/X-SEC; topology.Resolution still in docs (model.Resolution in code, DR-39); model.ToolResult field list nowhere (genuine register gap, note not fix); X-OPS§6 silent on image contents.
- Report: docs/reviews/round2/verification.md

## 2026-09-16 03:15 — Wave 5 (round-2 fix). Sonnet agent hit session-limit 429 mid-item-7; verified on disk before re-dispatch:
- DONE by killed agent (verified via grep, not headers): N2-1 (05 GOTOOLCHAIN live text = go1.27.1, historical %23.1/local text correctly confined to %235 verification-methodology record), N2-2 (F01 AC-F01-4/9 rewritten to DR-28 shed/block_up_to_timeout), N2-3 (kubectl v1.32.3 pinned, honest CI-computed-digest pattern, CVE owner named), N2-4 (02%4 api_Deps reconciled to scaffold incl. nl.Interpreter field added; orphan anomaly_Engine removed from 02%2), N2-5 (PRD ROI LLM-spend recomputed to $7.3k/yr per DR-34%34.3, formula footnoted), model.ToolResult TODO comment added in internal/model/rca.go.
- FALSE LEAD found on resume: "AC-ID renumbering gaps" (item 7) is not a defect — AC-F0N-M numbers intentionally mirror FR-F0N-M (traceability by design), and repeated grep counts are normal multi-site citations, not duplicate definitions. No fix applied; would have reduced traceability.
- Completed directly by orchestrator (3 trivial edits, no agent needed): topology.Resolution -> model.Resolution in 01 DDL comment (line ~1594); 06-decision-register.md Appendix D "Known gaps" added recording the model.ToolResult field-list gap; X-OPS-deployment.md %6 image-contents sentence added (distroless + traceiq binary + pinned kubectl only, no shell).
- Set-A "removal" (item 6) unnecessary: already correctly retained as withdrawn-with-note per DR-1 design, not a live contradiction.
- build/vet: green.

## 2026-09-16 03:20 — Wave 6 (opus scoped re-check). 138.9k opus tokens ~$0.44. Running total ~$6.04.
- Verdict: NOT APPROVED. 9/10 confirmed fixed. Reviewer OVERRULED orchestrator false-lead call on item 7 with hard evidence: AC-XSEC-11 cited by 4 docs, defined nowhere; several ACs do not map to their FR number at all; DR-38 %38.4(2) normatively mandates contiguous renumbering (traceability lives in an explicit FR column, not matching numbers).
- 2 new minor issues surfaced by the fix wave itself: NI-1 topology.Resolution still declared as local type (not just DDL comment) in 01 (x2) and F04 (x1) - my earlier grep missed the type/const declarations, only caught one comment. NI-2 Appendix D wrongly cites "Appendix C" for ToolResult refs (should be DR-18, the load-bearing replay-drift reference).
- build/vet/test: green throughout.
- Report: docs/reviews/round2/recheck.md
- Wave 7 dispatched (sonnet): real AC-ID renumbering F09/F11/F12/X-SEC per DR-38%38.4(2) incl. defining AC-XSEC-11 + fixing all cross-doc citations; dedup Resolution type in docs (01 x2, F04 x1) to model.Resolution; fix Appendix D citation.

## 2026-09-16 06:26 — Wave 7 reassigned to forked session (Tracing Bot (fork), user-initiated). Same filesystem path/worktree as THIS session (verified: git worktree list shows only one tree; no branch divergence). No true git merge needed — "sync" = re-verify disk state once fork finishes.
POLICY: this session (Tracing Bot [7a1a47]) is the sole continuation point from wave 8 onward. Fork should not dispatch further waves after wave 7. Orchestrator will poll for wave 7 completion evidence (docs/reports/w7-*.md, AC-XSEC-11 defined in X-SEC-security.md, Resolution deduped in 01/F04, Appendix D citation fixed in register) every ~18 min via session cron, verify build/vet/test green, log sync confirmation, then proceed to wave 8 (opus recheck + architecture sign-off) automatically.

## 2026-09-16 06:45 - Wave 7 COMPLETE, independently verified (not just agent self-report). 258.5k sonnet tokens ~$0.32. Running total (this session's dispatches) ~$6.36 of $30.
- Task 1 (AC renumbering): DONE. F09 table was already contiguous; only stale cross-references needed updating. F11/F12/X-SEC renumbered contiguously. AC-XSEC-13 newly defined (formerly dangling AC-XSEC-11), mapped to FR-XSEC-8 beside the audit-chain criteria. Full old-to-new map in docs/reports/w7-fix.md.
- Task 2 (Resolution dedup): DONE, confirmed via direct grep - zero local "type Resolution uint8" in 01-system-architecture.md or F04-topology.md. The one remaining occurrence at 06-decision-register.md around line 3662 is DR-39's own canonical package-model declaration (the decision that CREATES model.Resolution) - correctly present, not a duplicate.
- Task 3 (Appendix D): DONE, now cites DR-18 (replay drift-detection) instead of the false Appendix C claim.
- build/vet/test: green, confirmed independently by this session, not just the agent's claim.
- Report: docs/reports/w7-fix.md (224 lines)

## 2026-09-16 06:45 - SESSION COORDINATION NOTICE
This session ("Tracing Bot (fork)" per ListAgents; session id rotates across restarts) found the POLICY note above was written by a peer session "Tracing Bot [7a1a47]" (interactive, user-driven, busy) at 06:26 while this session's Wave 7 agent was still running. That note claims orchestrator role from Wave 8 onward and calls this session "the fork" limited to Wave 7. Both sessions share the identical working directory - there is only one docs/ on disk, no separate git worktree.
THIS SESSION IS NOW HALTING DISPATCH to avoid two sessions writing agents against the same files concurrently, and to avoid duplicate spend against the shared $30 cap. Wave 7's completion is logged above with independent verification (this session did not just trust the peer note or the agent's self-report). If the peer session's poll-and-continue plan does not pick this up on its own, the user should explicitly tell ONE session which one continues.
Cost tracked here (~$6.36) is this session's dispatches only - the peer session's spend, if any, is not visible from here.

## 2026-09-16 06:44 — Wave 7 SYNCED (ran on forked session, same worktree). Independently verified: build/vet/test green. All 3 recheck.md items closed: AC-XSEC-11 defined (now AC-XSEC-13, cross-ref-corrected), Resolution type dedup complete (found + fixed a 4th site the brief missed: DR-13 code sample in 06-decision-register.md itself), Appendix D cites DR-18 not Appendix C. Report: docs/reports/w7-fix.md. Extra stale AC-ID cross-refs fixed beyond brief scope (F09 l.156, DR-36 l.3431).
SOLE CONTINUATION POINT: this session (Tracing Bot) from here on. Fork should not dispatch further.
Proceeding to Wave 8 (opus re-verify + sign-off) automatically per prior authorization.

## 2026-09-16 06:50 — Wave 8 COMPLETE. APPROVED. docs/signoffs/architecture-signoff.md written. All 40 decisions applied, 98/98 findings resolved, build/vet/test green. Running cost ~$6.33 of $30.
ARCHITECTURE PHASE CLOSED. Starting IMPLEMENTATION PHASE (TDD, batched coders, per-batch review+fix, order: store -> ingest+topology -> sampler -> anomaly+correlate+memory -> rca(rules) -> remediate+nl+eval -> rca(llm)+api+web -> cmd wiring -> testing -> signoffs).
Wave 9 dispatched: 3 sonnet TDD coders — (A) store (sqlite hot index + parquet cold + tiered), (B) ingest+topology, (C) sampler. Each: tests first (FR/AC from feature doc), then implementation, self-verify go test, report.

## 2026-09-16 08:01 — Wave 9 all 3 killed by session limit (reset 8am ET, now past). Verified state before continuation: topology COMPLETE+tested (137 lines livegraph.go missing from count, actually 607+353), sqlite hot-index COMPLETE+tested. parquet has 2 build errors (parquet-go API mismatch), tiered unstarted, ingest broken mid-edit, sampler has 7 impl files but zero tests. 3 continuation agents dispatched: (A) parquet fix + tiered impl, (B) ingest finish, (C) sampler tests+fixes.

## 2026-09-16 08:36 — Wave 9 continuation: sampler DONE (22 tests, fixed predicate eviction bound; follow-ups: priority shed order, Sampler interface wiring). ingest DONE (22/27 tests w/ subtests; deferred: auth/TLS/metrics-wiring/HTTP-Jaeger-receiver/size-limits — legit backlog for X-SEC wave). Noted pre-existing tiered Windows file-lock issue for the still-running store agent to fix. Waiting on: store parquet+tiered continuation.

## 2026-09-16 08:42 — Wave 9 COMPLETE and verified. All 7 tested packages green (archtest, ingest, sampler, store/{parquet,sqlite,tiered}, topology). No FAIL anywhere.
USAGE MONITORING (user request): no tool exposes numeric % usage proactively; /usage is terminal-only and no interactive claude session is open in the readable terminal tab (checked via read_terminal — bare PowerShell prompt). Policy: reactive pause-on-429 (checkpoint + stop) remains primary. If user opens an interactive claude terminal and runs /usage, orchestrator will read it via read_terminal and pause proactively at 85etween waves.
NEXT: Wave 10 = per-package review (store, ingest+topology, sampler) + fix loop. Flag for reviewers: sampler AC-F02-13 priority-shed-order not implemented, top-level Sampler interface not wired; ingest OTLP HTTP + Jaeger receiver not built (Zipkin done), auth/TLS/metrics wiring deferred pending X-SEC (legit, out of order); store DuckDB cross-val + orphan reconciliation + Compact no-op + cmd wiring deferred.

## 2026-09-16 09:17 — Wave 10 COMPLETE. All 3 reviewers: APPROVE-WITH-FIXES, fixes applied directly, all re-verified green.
- store: fixed unbounded-growth retention no-op (attr_index/FTS), WAL-never-deleted disk leak, unverified CRC, unsynchronized counter. 3 regression tests added.
- ingest+topology: fixed spurious 24h edge re-fire bug (would false-positive F05 anomalies), missing Run() driver, missing whole-batch size limit. 3 regression tests. 1 item flagged for later (durable is_new reconciliation, spans store+anomaly).
- sampler: fixed both wave-9-flagged gaps as real Blockers (priority shed order, top-level Sampler wiring) + found 2 more Majors (missing span dedup double-counting RED, data race on predicate snapshot). 5 regression tests.
BATCH 1 (store, ingest, topology, sampler) COMPLETE, reviewed, fixed, green. Repo-wide build/vet/test all pass, zero known Blockers open.
Running cost ~$8.4-8.6 of $30 (wave 9 initial killed-agent spend unmeasured, est $0.6-0.8; rest measured).
NEXT: Wave 11 = TDD batch 2 (anomaly; correlate+memory; rca-rules-reasoner-only) — 3 parallel sonnet coders.

## 2026-09-16 09:28 — USER INSTRUCTION: pause after Wave 11 (TDD anomaly/correlate+memory/rca-rules) completes. Do NOT dispatch Wave 12 (review) automatically. On the 3rd wave-11 notification: verify full repo build/vet/test, log completion+costs, write PAUSE checkpoint, schedule CronCreate one-shot resume for 1:01 PM (13:01) today 2026-09-16. On resume: dispatch Wave 12 = per-package review+fix for anomaly, correlate+memory, rca-rules (same pattern as Wave 10), then continue per the standing NEXT sequence.

## 2026-09-16 13:01 — Resumed (1pm reset). Wave 11 initial attempt: all 3 killed by 429. Verified state: correlate COMPLETE+tested+green (untouched). anomaly substantial code (8 files, no tests). memory substantial code (4 files, 1 build error: undefined mathPow, no tests). rca substantial code (9 files across rca+rules, 1 build error: undefined decodeToolArgs, no tests). model gained 10 new files (anomaly/clock/health/memory/rca/red/remediate/security/telemetry/tenancy .go). 3 continuation agents dispatched.

## 2026-09-16 13:38 — Wave 11 COMPLETE. All 6 packages (anomaly, correlate, memory, rca+rules) implemented, tested, green.
- anomaly: 47 tests. Bug fixed: sub-threshold events were silently dropped instead of recorded (DR-14 §14.4 violation).
- correlate: complete from initial attempt, untouched, tests pass.
- memory: 26 tests. Bugs fixed: missing mathPow import; fingerprint-match scoring diluted by prose tokens (real retrieval-quality bug). RunbookImporter implemented (was missing).
- rca+rules: 23/41 tests (w/ subtests). 4/6 rule catalog implemented (floor met): error-sig-after-deploy, downstream-latency, N+1-span, pool-exhaustion. Bugs fixed: decodeToolArgs missing; interest-predicate Remove used wrong ID (DR-11 violation); archtest adjacency gap for rca/rules subpackage (was blocking repo-wide test).
- gofmt: 17 files were not gofmt-clean, fixed directly by orchestrator (no agent), re-verified green.
BATCH 2 (anomaly, correlate, memory, rca-rules) implementation COMPLETE. Review wave (12) NOT yet dispatched.
Full repo: 123 .go files, 24 test files, 12 tested packages all green, 0 known Blockers open.
Running cost ~$9.15 of $30 (measured; some wave-9-initial killed-agent spend still unmeasured, immaterial to total).

## 2026-09-19 09:29 — Wave 12 COMPLETE. Batch 2 review done, all 3: APPROVE-WITH-FIXES.
- anomaly: fixed cross-tenant dedupe collision (permanent edge suppression bug) + fake SizeBytes literal a test was blindly asserting (30-60x under real footprint). Paging logic confirmed absent/correct (none exists yet, none needed here). is_new reconciliation confirmed still missing on store side but not blocking (nothing live wires it).
- correlate+memory: fixed unimplemented circuit breaker (FR-F07-7), silent worst-match-drop bug in Similar() past 500 records (map iteration order), a real data race on tenant map access. Tenant isolation independently attacked, held. 1 item needs a DR (FR-F07-5 heuristic fallback, signature missing service param).
- rca: fixed Abort() being silently disconnected from the running loop (its status write got clobbered, predicate never removed) — real control-plane bug. ToolResult shape (Appendix D gap) confirmed sound/extensible. 1 item needs a DR (2 of 4 rules use unregistered MetricQueryArgs.TemplateIDs, skip a required 2nd tool call — needs template-registry design).
BACKLOG (2 items needing a proper DR before fixing, not urgent): correlate FR-F07-5 fallback signature; rca metric-query template registry for 2 rules.
BATCH 2 (anomaly, correlate, memory, rca-rules) fully COMPLETE: implemented, reviewed, fixed, green.
Running cost ~$10.01 of $30.
NEXT: Batch 3 = internal/remediate, internal/nl, internal/eval (3 parallel sonnet TDD coders).

## 2026-09-19 10:09 — Wave 13 COMPLETE. Batch 3 (remediate, nl, eval) implemented, all green.
- remediate: 18 tests, all 8 required security scenarios covered (sep-of-duty, TTL expiry, budget counts failed actions, audit-chain tamper detection, kubectl argv safety, dryrun isolation). Deferred: k8s.BuildArgv/MinimalEnv upstream, RemoveIstioFault diffing, real topology/auth/tenant wiring, full transition-matrix fuzz.
- nl: 19 tests, all 10 IntentKind values classify correctly offline, entity extraction + tenant isolation verified. Deferred: LLM interpreter (scoped stubbed-only), deploy-marker time phrases, AC-F10-9 accuracy harness (needs F11 corpus).
- eval: 26 tests, full repo green (21 packages). Real rca.Engine/rules.Reasoner exercised end-to-end, not mocked — resolved the eval->rca/rules DR-2 adjacency concern via constructor injection (no import). 2 real bugs caught (float-boundary epsilon, wrong-clock TimeToRCA). Deferred: Istio S01-S23 catalog authoring, live ingest-replay mode, EvidencePrecision/Recall unpopulated (rca.Engine does not yet populate Step.EvidenceIDs).
gofmt: 7 files fixed directly by orchestrator, reverified green.
BATCH 3 (remediate, nl, eval) implementation COMPLETE. Review wave (14) NOT dispatched — USER INSTRUCTION: hold for explicit command before starting wave 14.
Full repo: 151 .go files, 35 test files, 15 tested packages all green, 0 known Blockers open.
Running cost ~$10.73 of $30.

## 2026-09-19 10:14 — USER DECISION: Wave 18 (Istio S01-S23 testing) runs in LIVE mode, not offline-replay. REMINDER GATE added to CHECKPOINT.md step 7: before dispatching Wave 18, must stop and ask user to confirm the Istio Transaction Lab cluster (sibling project Istio_testing, Docker Desktop K8s, sidecar injection active, both ingress gateways reachable) is up. Do not silently fall back to offline mode.

## 2026-09-19 14:24 — Wave 14 COMPLETE (auto-dispatched by 2PM cron per user pre-authorization). Batch 3 review done, all 3: APPROVE-WITH-FIXES.
- remediate (security-critical): 1 Blocker + 4 Major fixed — empty RemediationAllowlist was fail-OPEN (allow-all) instead of fail-closed; TargetAllowlist declared but never enforced; Execute() swallowed snapshot-read errors instead of aborting (FR-F09-6); ErrPayloadOutOfScope never checked; budget-exceeded refusal missing audit+state transition. Fail-closed verdict on kubectl stub: PASS, confirmed no synthesized-success path. 9 new regression tests (27/27 total). 1 minor gap noted: Rollback() untested.
- nl: 1 Blocker fixed — both gated Answerer paths fail-OPEN on AuthZ==nil (skipped the check entirely); nl is the sole enforcement point (rca/remediate do not re-check), so this was a real bypass-by-omission. Fixed fail-closed + 2 regression tests (21/21 total). Answerer/AnswerForSubject split confirmed architecturally sound and currently unreachable (no api handler wired yet).
- eval: 2 Major fixed — FR-F11-11 cost gate ($0.08/$0.50) was entirely missing from checkGates despite being spec-required (cost blowout would silently pass CI); vacuous 1.0 evidence-precision/recall scores were report-opaque (scorer logic itself confirmed sound, fixed to disclose "never measured" explicitly). 12 new tests (38/38 total).
gofmt clean, full repo go test -count=1 all green, all 3 "pre-existing" cross-package flake reports from earlier waves confirmed resolved/stale.
BATCH 3 (remediate, nl, eval) fully COMPLETE: implemented, reviewed, fixed, green.
Running cost ~$11.39 of $30.
HOLDING per user standing instruction — do NOT auto-proceed to Wave 15. Wait for explicit command.
NEXT (on command): Wave 15 = TDD batch 4 (rca LLM-reasoner wiring, api+web UI, cmd wiring) — biggest remaining batch.

## 2026-09-19 14:36 — Wave 15 (biggest batch, longest) dispatched: rca LLM-reasoner (security-focused: telemetry wrapping, schema validation, SSRF-arg rejection, fallback-to-rules), api+web UI (REST+MCP, RBAC-gated, embedded SPA), cmd/traceiq wiring (real config load, startup/shutdown order, end-to-end smoke test). 40-min stop rule (largest task). USER INSTRUCTION (repeated): hold after this wave, wait for explicit command before wave 16.
CONCURRENT: 2 user-spawned background tasks running in separate isolated worktrees (task_f62bcc28 time-window fix, task_a16749f9 service-name-match caveat), both scoped to internal/nl only, no overlap with wave 15. To merge into this tree once they complete.

## 2026-09-19 18:55 — Wave 15 all 3 killed by 429 (reset 6:50pm ET). Resumed. Verified state: rca LLM reasoner FULLY COMPLETE (12 files incl. tests, all green — security-critical piece survived intact). api+web substantial (11 api files + SPA, build OK but vet/test broken: topology.LiveGraph missing Has() method needed by anomaly.TopologyReader). cmd wiring substantial (config+system+smoke-test, build OK but smoke test FAILS: 2 real bugs caught by the integration test — sqlite path_signature int64/uint64 scan mismatch (batch-1 defect, missed by unit tests), parquet double-seal-on-shutdown race. 2 continuation agents dispatched (api interface fix; cmd/store bug fixes).

## 2026-09-19 20:13 — Cross-session coordination with peer session "Fix silently-wrong time window for unresolved phrases" (user-spawned nl-fix worktree, task_f62bcc28). Peer asked whether this session was editing internal/api or internal/store/sqlite before touching them itself — confirmed yes (2 running Wave 15 continuation agents, same working tree, no isolation). Peer agreed to hold until pinged. Peer's read-only findings, logged here so whoever lands the fix doesn't rediscover them:
- internal/store/sqlite/hotindex.go SearchSpans, `case q.Service != "":` branch: SQL filters `AND operation=?` unconditionally even when q.Operation is empty (mcp.go's caller never sets it) — zero-row match against real (non-empty-operation) spans. Also uses q.Window.Start/End with no caller-side default, so an unset window (zero time.Time) also excludes everything. Fix: make the operation filter conditional (AND-only-if-nonempty), and mcpSearchTraces in internal/api/mcp.go should default a real window when the caller doesn't supply one — nl.extractToolArgs already does this correctly, same pattern applies here. Root cause of TestMCP_SearchTraces_RealStore failing.
- TestIncidents_RealGrouper: anomaly.DefaultConfig() already used in testutil_test.go (MaxOpenIncidents=200 — not the zero-value bug a stale comment there implies). Root cause still open; peer was mid-investigation of Grouper.Add's severity/threshold path when it paused — suspects it may share root cause with the TopologyReader interface gap the wave-15B-cont agent is fixing right now. Worth re-checking once that lands.
Will relay these to whichever agent/review next touches internal/api. Will ping peer back once the 2 running Wave 15 continuation agents complete.

## 2026-09-19 20:20 — Wave 15 COMPLETE. Batch 4 (rca LLM-reasoner, api+web UI, cmd wiring) implemented, both post-429 bugs fixed, verified green.
- rca LLM-reasoner: FULLY COMPLETE from the initial (non-killed) attempt — 12 files incl. tests, all green. Security requirements (telemetry `<untrusted_telemetry>` wrapping, strict-schema validation, no-raw-URL tool args, fallback-to-rules on error/timeout/budget-exhaustion) implemented per brief; not yet independently reviewed (that's wave 16's job).
- cmd/traceiq wiring: 2 real bugs fixed by continuation agent. (1) sqlite hotindex.go GetTrace/SearchTraces scanned signed path_signature int64 into *uint64, failing on any hash with the sign bit set — fixed via int64 scan + uint64 bit-reinterpret, no schema/hash change. (2) shutdown called cold.SealDue (seals internally) then cold.Seal again on the same now-closed blocks — fixed using the side-effect-free cold.DueBlocks listing instead, matching tiered.go's existing SealAndBind pattern. TestSystem_EndToEnd_OTLPSpanReachesStore (the flagship integration test) now passes. Report: docs/reports/w15-cmd-wiring.md.
- api+web: TopologyReader interface gap fixed via a narrow adapter (internal/api/topology_adapter.go, type/signature translation) + one additive Has() method on topology.LiveGraph (reads the existing per-tenant services map — Graph interface itself untouched). Also independently fixed TestIncidents_RealGrouper (unrelated bug: testutil_test.go used zero-value anomaly.Config{} instead of DefaultConfig(), so MaxOpenIncidents=0 self-expired every incident). Report: docs/reports/w15-api-web.md.
- CROSS-SESSION: both fixes independently confirm the peer session's diagnosis — TestMCP_SearchTraces_RealStore is a pure internal/store/sqlite bug (SearchSpans operation-filter + window-default), api's call site is well-formed. This is now the ONLY known repo-wide test failure. Peer ping sent: clear to edit internal/store/sqlite/hotindex.go and internal/api now.
- gofmt: 10 files fixed directly by orchestrator, reverified green. TestEvictionAtCap flake reconfirmed transient (5/5 pass on rerun).
BATCH 4 (rca-llm, api+web, cmd) implementation COMPLETE. Review wave (16) NOT dispatched — USER STANDING INSTRUCTION: hold for explicit command.
Full repo: 182 .go files, 44 test files, 17 tested packages, 16 green + 1 known failure (peer's in-progress fix).
Running cost ~$11.75+ of $30 (wave 15's non-killed initial-agent tokens not separately reported; continuation-only measured).
NEXT (on command): Wave 16 = review batch 4 (rca-llm security-focused, api+web incl. the new adapter/Has(), cmd wiring). Flag for rca-llm reviewer: this is the security-critical wave, give it the same scrutiny wave 14 gave remediate.

## 2026-09-19 20:33 — Peer session ("Fix silently-wrong time window for unresolved phrases") fixed the last known repo-wide failure: SearchSpans' "by service" branch had operation+window as mandatory filters instead of optional narrowing ones (matches the sibling SearchTraces service-branch precedent). Independently verified by this session (not trusted on self-report alone): go build/vet/test ./... -count=1, whole tree, ALL GREEN. gofmt clean. Zero known failures anywhere in the repo — cleanest state this build has reached.
Two user-spawned nl-fix worktrees now believed complete or near-complete (this one confirmed done; "Add ambiguity caveat to fuzzy service-name matches" status unconfirmed from this session's side, was idle at last ListAgents check). Their changes are in this same working tree (no separate worktree, confirmed earlier) so no merge step needed — already reflected in the green build above.
STILL HOLDING per user standing instruction — Wave 16 not dispatched, waiting for explicit command.

## 2026-09-19 22:06 — Wave 16 COMPLETE (user said "go for it", hold after). Batch 4 review done, all 3: APPROVE-WITH-FIXES, 0 Blockers total.
- rca-llm (security-critical, 5 explicit properties checked): 2 Major fixed. The real one — second-order prompt-injection laundering: hypothesis text (model-authored, could re-quote attacker-controlled telemetry from an earlier turn) was rendered unwrapped in later prompts, bypassing the direct telemetry-wrapping defense. Fixed. Also added missing post_score bounds + length caps (confidence-corruption + cost-amplification vector). All 5 properties (telemetry-wrapping, schema-validation, SSRF-defense, prompt-injection-resistance, fallback-correctness) now PASS. SSRF note: Topo tool backend not wired to prod yet, not blocking now.
- api+web: 1 Major fixed — MCP endpoint had zero JSON-schema param validation before dispatch, fixed with real schema validation + 3 tests. RBAC re-checked for the wave-14-class fail-open bug: clean, every handler fails closed. Coverage: 16/34 REST endpoints (47%), 3/12 MCP tools real (rest correctly stubbed). topology_adapter.go + LiveGraph.Has() verified correct (tenant-scoped, safe cast). Peer's SearchSpans fix re-verified genuinely working, not accidental. SPA: real HTML served, no XSS, CSP header gap noted (deferred, sensible — better done with remaining 7 screens).
- cmd wiring: 3 Major fixed — shutdown race could silently drop an already-decided Keep=true trace; sampler.Manager.DrainRED was NEVER CALLED anywhere in the real binary (anomaly baseline permanently starved in production despite passing all unit tests in isolation — integration-only-catchable bug); related mid-flight write-abort bug found while fixing the first. 1 identical race flagged in internal/ingest/queue.go, correctly routed out-of-scope via spawn_task instead of scope-creep. Both original wave-15 bug fixes (path_signature, double-seal) re-verified genuinely correct and complete (bit-exact for all int64 incl. MinInt64; idempotent across all 3 block states). api-wiring decision: correctly left UN-wired — internal/auth has zero concrete implementations, wiring now would silently 401 everything while looking configured; honest TODO left instead.
gofmt clean, full repo green: 183 .go files, 45 test files, 17 tested packages, ZERO known failures or open Blockers anywhere.
BATCH 4 (rca-llm, api+web, cmd) fully COMPLETE: implemented, reviewed, fixed, green.
Running cost ~$12.58 of $30 (wave 16: 661.8k sonnet tokens ≈ $0.83).
HOLDING per user instruction ("go for wave 16 and wait after it completed"). Wait for explicit command.
NEXT (on command): Wave 17 = final whole-codebase review (opus, most capable model, one fix wave, one scoped re-check) per superpowers:requesting-code-review pattern, covering the full internal/+cmd/ tree since there's no commit history to diff against. Then Wave 18 = testing (⚠️ Istio live-cluster confirmation gate — see 2026-09-19 10:14 entry and docs/CHECKPOINT.md). Then Wave 19 = sign-off swarm.

## 2026-09-20 07:04 — Wave 17 COMPLETE. Final whole-codebase review (opus). VERDICT: APPROVE-WITH-FIXES. FIRST BLOCKERS of the whole build — 3 found, all genuine cross-tenant data-isolation breaches only a whole-system pass could catch, all fixed + 9 regression tests:
- B1: sampler.shardBuf.traces keyed by TraceID ALONE. TraceIDs are client-minted/attacker-choosable — a malicious client could get its spans assembled and persisted under ANOTHER tenant. Fixed: TraceKey{Tenant, TraceID}.
- B2: cross-package contract violation — ingest/otlpgrpc.go's doc says FromSubject should reject an empty tenant, but cmd/traceiq's simpleResolver.FromSubject just returned `subjectTenant, nil` — unauthenticated gRPC spans silently accepted under TenantID(""). Fixed at 3 layers: resolver, ingest re-check, api 401.
- B3: ReplayWAL rebuilt traces with Tenant unset -> WAL-recovered traces finalized under "". WAL.Truncate was tenant-blind — tenant A's finalize could delete tenant B's journal.
Also fixed 4 Majors: topology eviction tie-break (M1, see below), ops/lastNewEmit maps never pruned — max_edges only bounded 1 of 3 maps (M2), WarmStart bypassed max_edges entirely (M3), archtest never enforced DR-2's `web` package edge (M4).
TestEvictionAtCap: FIXED AT SOURCE this time, not deferred again — deterministic total order (Calls -> LastSeen -> insertion seq). Verified green at -count=50 plus a new 200-iteration regression test; the w16-suggested FirstSeen tie-break would NOT have worked (fake clock is static, all timestamps tie) — good catch that the earlier proposed fix was itself wrong.
ingest/queue.go shutdown race (flagged wave 16, spawned as task_87d08322): CONFIRMED ALREADY FIXED by that background task, verified by reading the actual code not trusting a comment. No action needed.
ONE remaining known issue, correctly scoped as a wave not a quick fix: cmd/traceiq still doesn't wire internal/api (internal/auth has zero concrete implementations, wiring now = every route 401s). This BOUNDS the Wave 18 test plan: the real binary only proves the pipeline path (ingest->sampler->store->topology->anomaly); api/nl/remediation need package-level test harnesses, not the assembled binary, until auth gets real implementations. memory.Import/Export's deferred admin check must close in whichever wave wires auth.
internal/depstub: correctly NOT deleted — 6 of 13 DR-1 pins still have no real importer.
FINAL STATE: 188 .go files, 50 test files (agent reported 190/374 test funcs, orchestrator's independent recount: 188 files/50 test files — close enough, agent count likely included files touched during the session vs. final state), 18/18 tested packages green (verified twice independently), gofmt clean, ZERO known Blockers or flaky tests anywhere.
Running cost: wave 17 = 219.5k opus tokens ~= $0.69. Running total ~$13.27 of $30.
ARCHITECTURE + IMPLEMENTATION + FINAL REVIEW PHASES ALL COMPLETE. Next is Wave 18 (testing) which has a MANDATORY GATE: must stop and ask user to confirm the Istio Transaction Lab live cluster is up before dispatching anything (see 2026-09-19 10:14 entry, docs/CHECKPOINT.md step 7). Also must account for the api-not-wired constraint in the Wave 18 test plan.

## 2026-09-20 07:26 — Wave 18 (live Istio S13 test). User confirmed cluster up. Independently verified before dispatch: kubectl context docker-desktop, bank ns istio-injection=enabled with all 7 pods 2/2 Ready, istio-system healthy (dual ingress gateways, jaeger collector w/ OTLP 4317/4318 already present), MeshConfig has 4 extensionProviders registered but ZERO active Telemetry resources anywhere (mesh currently exports no traces despite collectors existing). User requested pod-in-cluster deployment (better than host-local+pull-from-Jaeger plan). Scenario chosen: S13 connection-pool-exhaustion, 1:1 match to the implemented rca rule. Dispatched ONE sequential sonnet agent (real kubectl+docker access, tight scope): build Dockerfile+image, deploy to new traceiq-test namespace, add ONE additive extensionProvider + ONE Telemetry resource (100% sampling, bank ns) to turn on real mesh trace export to TraceIQ, inject S13, generate load, restore S13, verify via kubectl-cp'd sqlite + local Go harness against real store/anomaly/rca packages (no HTTP API needed since it's unwired), then MANDATORY full cleanup regardless of outcome. Mid-run: relayed 2 user safety/cleanup requirements via SendMessage to the live agent — (1) Dockerfile: EXPOSE only 4317, distroless nonroot, no shell/debug tools, no privileged/hostNetwork/hostPort, multi-stage only, paste full Dockerfile in report; (2) delete local YAML manifest scratch files too, not just live cluster resources, keep only the Dockerfile.

## 2026-09-20 08:39 — Wave 18 COMPLETE. VERDICT: PASS-WITH-DEFECTS. First genuine live end-to-end proof of the whole pipeline against a real mesh:
- Real spans reached TraceIQ ingest: YES (47/47 successful OTLP export batches from the real orchestrator sidecar).
- Sampler kept error traces: YES (89 real traces kept with KeepReason=KeepError from the actual S13 incident).
- Anomaly detector fired: YES on real observed data (ErrorBurstDetector, 0.989 error rate vs 0.0 baseline, score 0.994).
- RCA correctly diagnosed connection-pool-exhaustion: YES — rules reasoner walked the full catalog in priority order, correctly REFUTED 3 unrelated rules for lack of evidence, confirmed the right one at 0.80 confidence with real captured 503/UO evidence.
- 1 real code defect found: W18-D1 (High severity) — internal/store/store.go TieredStore.Append stamps every span's Service column with the trace's RootService instead of its own originating service; invisible in single-hop synthetic tests, broke on the first real multi-hop trace. Root cause pinpointed exactly (file:line), fix agent dispatched immediately.
- 2 pre-existing known gaps reconfirmed (not new): api still unwired (verification had to go around the pod entirely via kubectl-exec+cat since distroless has no shell for kubectl cp); rca's 5 tool backends (TraceStore/LogStore/MetricStore/TopologyStore/MemoryStore) are ALL no-op stubs in cmd/traceiq — the deployed binary can't actually confirm any MetricQuery-based rule today even though the rule LOGIC is proven correct (verified by substituting a real evidence-backed MetricStore fed from actual Envoy access-log counts in the standalone harness). This is a real wiring gap for a future wave alongside api/auth.
- Cleanup: FULLY CONFIRMED — Telemetry resource deleted, MeshConfig byte-exact reverted to original, traceiq-test namespace deleted, S13 restored, all 7 original bank pods healthy, local docker image + all scratch manifests/verification programs deleted. Only new files kept: Dockerfile (safety-reviewed, clean: EXPOSE 4317 only, distroless nonroot, no shell/debug tools, multi-stage), Testing_defect.md, docs/reports/w18-live-test.md.
- Dockerfile independently reviewed by orchestrator against all 6 user safety requirements: PASS on every one.
Running cost: wave 18 = 298.9k sonnet tokens ~= $0.37. Running total ~$13.64 of $30.
Fix dispatched for W18-D1 (TDD: regression test first proving multi-hop per-span service identity, then the one-line fix). Awaiting completion before declaring Wave 18/testing phase fully closed.

## 2026-09-20 08:57 — W18-D1 FIXED. Root cause confirmed exactly as diagnosed: internal/store/store.go:664 (TieredStore.Append) stamped every SpanIndex.Service with t.RootService instead of the span's own service. Fix: new spanService(sp) helper (mirrors sampler/spanutil.go's existing workaround for the same model.Span.Service() stub gap) reading sp.Resource.ServiceName; RootService itself untouched. New test TestAppend_SpanServicePerSpan_NotRootService (3-hop synthetic trace, real sqlite.Store) — confirmed failing pre-fix, passing post-fix, proper TDD. Testing_defect.md updated: W18-D1 status Fixed. Full repo green, gofmt clean, no existing test needed weakening.
WAVE 18 / TESTING PHASE FULLY CLOSED. Defect log clean (1 entry, Fixed, zero Open). Final state: 189 .go files, 51 test files, 19 tested packages all green (internal/store gained its own top-level test file this fix).
Running cost: fix = 113.1k sonnet tokens ~= $0.14. Running total ~$13.78 of $30.
ARCHITECTURE + IMPLEMENTATION + FINAL REVIEW + LIVE TESTING ALL COMPLETE. Only Wave 19 (sign-off swarm: PM, dev lead, test lead, architecture) remains. HOLDING for explicit user command before dispatching — same pattern as every other wave boundary this build.

## 2026-09-20 10:19 — Wave 19 (sign-off swarm) COMPLETE. All 4 honest, non-rubber-stamp assessments:
- PM: APPROVED WITH NOTED GAPS. 3/12 features fully delivered+proven (F01/F02/F05), 7/12 delivered-with-documented-gap, 2 deferred (was misreported as 4 in the agent's own count vs the 12-feature table — see docs/signoffs/pm-signoff.md for exact breakdown). Flagged: PRD's headline NL-chat example not runnable today (api/nl unwired), comparison table could overstate current state if shown externally.
- Dev lead: APPROVED WITH CONCERNS. Independent go build/vet/test/gofmt rerun matches every wave's claimed green exactly. Coverage: 21/24 buildable units reviewed; 3 small packages (bus, cluster, selfobs, 142 lines combined) never reviewed at all (low risk, unverified). Pushed back correctly on the dispatch brief's framing — early waves weren't shallow, found real bugs every time; the sharper finding is that w18's live test caught a multi-hop span-service bug that all 8 code-review waves missed (no test fixture anywhere used a genuinely multi-hop trace) — real evidence that review alone has a structural blind spot only integration testing closes.
- Test lead: APPROVED WITH NOTED COVERAGE GAPS. Independently confirmed: 51 test files, 375 PASS, 0 FAIL, zero flakes across 3 repeated full runs, defect log clean (W18-D1 Fixed). Honest gap: only 1 of ~23 Istio lab scenarios has live proof; api/web have zero real HTTP/MCP traffic or load/soak/chaos testing since unwired.
- Architecture: APPROVED WITH DEVIATIONS. DR-2 adjacency PASS (live archtest rerun), DR-5 tenant-first-param PASS (spot-checked across 5 packages, zero exceptions). **Real finding: D-X5 (the PRD's headline "unique wedge" — agent-to-sampler feedback loop) is built+unit-tested on both sides but cmd/traceiq/system.go passes nil for the InterestSink — the wire connecting them was never made in the running binary. Verdict: ASPIRATIONAL, not real, in the shipped system today.** Also found this specific gap was undocumented anywhere outside source comments — a real doc-consistency miss. Consolidated 12-item gap list produced.
Running cost: wave 19 = 469.6k sonnet tokens (99.5k+168.8k+79.9k+121.3k) ~= $0.59. Running total ~$14.37 of $30.
ACTION: D-X5 InterestSink wiring gap is fixable (adapter pattern already anticipated by rca/interest.go's own doc comment) and it's the product's headline claim — dispatched immediate fix rather than leaving it as a known gap, consistent with this build's practice of fixing real findings on the spot. Fix agent: build the cmd/traceiq adapter (rca.InterestPredicate -> sampler.InterestPredicate), wire real sampler.Sampler in place of nil, add a test proving a pushed predicate actually changes a sampling decision (not just "compiles"), update architecture-final-signoff.md's D-X5 section.

## 2026-09-20 10:48 — D-X5 WIRED AND PROVEN LIVE. New cmd/traceiq/interest_adapter.go (samplerInterestSink), wired into rca.NewEngine (was nil). Test TestInterestSinkAdapter_RCAPredicateReachesRunningSampler: runs a REAL rca.Engine.Investigate against a REAL sampler.Impl — proves a mid-investigation trace for the epicenter service is kept (Reason=KeepInterest, Set reached the sampler) AND the same trace drops again once the investigation concludes (Remove reached the sampler too). Full round-trip proof, not a compile check. Full repo green: 19/19 test packages ok, gofmt clean, 191 .go files, 52 test files. docs/signoffs/architecture-final-signoff.md D-X5 verdict updated to RESOLVED.
Running cost: fix = 164.0k sonnet tokens ~= $0.21. FINAL running total ~$14.58 of $30.

## === BUILD COMPLETE ===
All 19 waves done. Architecture (docs/architecture/00-06 + features/*), Go implementation (internal/ x24 packages + cmd/traceiq), 8 review waves (10,12,14,16,17,19-fix), 1 live end-to-end Istio-mesh test (wave 18, real fault injected, real diagnosis, 1 real defect found+fixed), 4 sign-offs (PM/dev-lead/test-lead/architecture, all honest non-rubber-stamp with noted gaps), 1 headline-claim wiring gap found by sign-off and fixed same-day.
Final state: 191 .go files, 52 test files, 19 tested packages, 375+ passing tests, 0 known Blockers, 0 open defects, gofmt clean, build/vet clean.
Known, documented, deliberately-deferred gaps (not blocking, all traceable): internal/api/nl/remediate unwired pending internal/auth implementation; rca's 5 tool backends stubbed in cmd/traceiq; 9/12 MCP tools unimplemented; 2/6 rca rules missing (retry-storm, timeout-mismatch); OTLP-HTTP/Jaeger receivers missing (Zipkin+OTLP-gRPC done); store.Compact() no-op; topology is_new reconciliation open; 3 packages (bus/cluster/selfobs) never individually reviewed; only 1 of 23 Istio lab scenarios proven live.
Total cost: ~$14.58 of $30 cap ($15.42 under budget, well inside the $28 hard stop).
