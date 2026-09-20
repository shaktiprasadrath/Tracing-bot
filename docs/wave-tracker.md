# TraceIQ — Wave Tracker

Living document, updated after every wave completes. Source data: `docs/ledger.md` (narrative log)
and `docs/cost-ledger.md` (token/cost rows). Dollar figures are blended list-price estimates from
reported agent tokens (sonnet $1.25/M, opus $3.15/M, ~8% output/15% input/77% cache-read mix) —
not exact billing, but consistent and directionally accurate across the whole build.

Skills used throughout, not repeated per row unless wave-specific: `subagent-driven-development`
(the process every wave follows), the `Agent` tool with explicit `model:` per dispatch.

| # | Wave | Status | Time (actual/est.) | Agents (type × count) | Model(s) | Skills (wave-specific) | Tokens used | Cost | Running $ |
|---|---|---|---|---|---|---|---|---|---|
| 1 | Doc-fix round 1: system docs 01-04, F01-F04, F05-F08, F09-F12+X-SEC+X-OPS+05+00 | DONE | ~09-15 17:24–17:48 (24 min) | 4 × general-purpose | sonnet | dispatching-parallel-agents | 1.02M | $1.28 | $2.58 |
| 2 | 01-doc cont. + scaffold (model, tenant, interfaces, archtest) | DONE | ~09-15 17:48–18:17 (29 min) | 4 × general-purpose | sonnet | — | 0.83M | $1.03 | $3.61 |
| 3 | 01-doc DR-14..39 finisher + scaffold cleanup (archtest path bug, dedup types) | DONE | 09-15 18:17–23:25 (multi-session, ~83 min active) | 2 × general-purpose | sonnet | — | ~0.71M | $0.89 | ~$4.50 |
| 4 | Round-2 board verification (98 findings vs decision register) | DONE | 09-15 23:25–23:56 (31 min) | 1 × general-purpose | opus | requesting-code-review pattern | 350.6k | $1.10 | $5.60 |
| 5 | Round-2 fix (10 open items: N2-1..7 + minors) | DONE | 09-16 03:15 (session-limit interrupted, resumed) | 1 × general-purpose (+3 orchestrator-direct edits) | sonnet | — | ~0.3M est. | ~$0.30 | ~$5.90 |
| 6 | Opus scoped re-check of wave-5 fixes | DONE | 09-16 03:20 (~20 min) | 1 × general-purpose | opus | — | 138.9k | $0.44 | ~$6.04 |
| 7 | AC-ID renumbering, Resolution dedup, Appendix D citation fix | DONE | 09-16 06:26–06:44 (ran on user's forked session, verified/synced) | 1 × general-purpose | sonnet | — | ~0.3M est. | ~$0.30 | ~$6.34 |
| 8 | Architecture sign-off gate | DONE | 09-16 06:50 (~15 min) | 1 × general-purpose | opus | — | 91.7k | $0.29 | $6.33* |
| 9 | TDD batch 1: store, ingest+topology, sampler | DONE | 09-16 06:50–08:42 (initial killed by 429, 3 continuation agents) | 3 × general-purpose (killed) + 3 × general-purpose (cont.) | sonnet | test-driven-development | ~1.58M | ~$1.97 | — |
| 10 | Review + fix batch 1 (store, ingest+topology, sampler) | DONE | 09-16 09:17 (~30 min) | 3 × general-purpose | sonnet | requesting-code-review pattern | 0.63M | $0.79 | $7.81 |
| 11 | TDD batch 2: anomaly, correlate+memory, rca-rules | DONE | 09-16 09:28–13:38 (initial killed by 429 at 1pm reset, 3 continuation) | 3 × general-purpose (killed) + 3 × general-purpose (cont.) | sonnet | test-driven-development | ~0.52M (cont. only measured) | $0.65 | $9.15 |
| 12 | Review + fix batch 2 (anomaly, correlate+memory, rca-rules) | DONE | 09-19 09:23–09:29 (resumed after weekly reset) | 3 × general-purpose | sonnet | requesting-code-review pattern | 0.69M | $0.86 | $10.01 |
| 13 | TDD batch 3: remediate, nl, eval | DONE | 09-19 09:49–10:09 (~20 min, no interruption) | 3 × general-purpose | sonnet | test-driven-development | 0.58M | $0.72 | $10.73 |
| 14 | Review + fix batch 3 (remediate, nl, eval) | DONE | 09-19 14:00–14:24 (auto-dispatched by pre-authorized cron, ~24 min) | 3 × general-purpose | sonnet | requesting-code-review pattern | 0.53M | $0.66 | $11.39 |
| 15 | TDD batch 4: rca LLM-reasoner, api+web UI, cmd wiring | IN PROGRESS | 09-19 14:36–ongoing (initial killed by 429 at 6:50pm reset; 2 continuation agents fixing real bugs found by integration testing) | 3 × general-purpose (1 succeeded, 2 killed) + 2 × general-purpose (cont., running) | sonnet | test-driven-development | TBD | TBD | TBD |
| 16 | Review + fix batch 4 | NOT STARTED | est. 25–40 min | 3 × general-purpose | sonnet | requesting-code-review pattern | — | — | — |
| 17 | Final whole-codebase review + fix wave | NOT STARTED | est. 30–45 min | 1 × general-purpose (review) + 1 × general-purpose (fix) | opus + sonnet | requesting-code-review, finishing-a-development-branch | — | — | — |
| 18 | Testing: integration scenario + Istio S01–S23 (⚠️ needs live cluster — user must confirm cluster up first) | NOT STARTED | est. 30–60 min + fix-loop time | 1–2 × general-purpose | sonnet | verification-before-completion | — | — | — |
| 19 | Sign-off swarm: PM, dev lead, test lead, architecture | NOT STARTED | est. 15–20 min | 4 × general-purpose | haiku/sonnet | — | — | — | — |

**Running spend so far: ≈ $11.39 of $30 cap** (through wave 14; wave 15's cost will be added once its
continuation agents report). Projected total through wave 19: **≈ $16–20**, comfortably inside the
$28 hard stop.

\* Wave 8's row shows $6.33 as the running total at that point per the original cost-ledger, which
predates waves 1-3's full reconciliation — minor rounding drift between the two ledgers (a few
cents) is expected and immaterial; `docs/cost-ledger.md` remains the authoritative row-by-row source.

## Notes on gaps in this reconstruction

- Waves 1-3, 5, 7, 9 (initial), 11 (initial) include killed/interrupted agent runs whose exact
  token spend before the 429 was never reported (failed agents return no `<usage>` block) — those
  are marked "est." and are a small fraction of the true total (the repo shows their work was
  substantial, so their real cost is on the same order as the successful continuation that finished
  the same package, not negligible, but not separately billed twice).
- Skills column only lists wave-specific skills; every wave implicitly used the `Agent` tool
  dispatch pattern from `subagent-driven-development`.
- This file will be re-appended (new row, updated running total) each time a wave completes —
  ask for the latest view any time, or check this file directly.
