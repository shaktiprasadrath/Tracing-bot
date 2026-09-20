# TraceIQ cost ledger (user cap: $30 remaining from 2026-09-15 12:40; hard stop at projected $28)

Blended $/M reported agent-tokens (list price, ~8% output / 15% input / 77% cache-read): sonnet 1.25 | opus 3.15 | haiku 0.65 | orchestrator ~0.06/turn

| When | Agent | Model | Tokens | $ | Running $ |
|---|---|---|---|---|---|
| 12:40 | (baseline — cap starts here) | – | – | 0.00 | 0.00 |
| 12:42 | fix wave x4 + scaffold (killed by user halt) | sonnet | ~1.0M est | 1.30 | 1.30 |
| 17:48 | W1 fix wave (7 of 8 done: F05-06 126k, F07-08 116k, 02-04 98k, F01-04 124k, F11-12 199k, F09-10-X 230k, scaffold 128k) | sonnet | 1.02M | 1.28 | 2.58 |
| 18:17 | W1 01-doc 194k + W2 (01 cont 119k, scaffold A 178k, B 182k, C 156k) | sonnet | 0.83M | 1.03 | 3.61 |
| 06:50 | Wave 8 (opus sign-off gate) | opus | 91.7k | 0.29 | 6.33 |
| 09:17 | W9 cont (store 178k, ingest 205k, sampler 171k) + W10 review (store 217k, ingest-topo 198k, sampler 214k) | sonnet | 1.18M | 1.48 | 7.81+ | 
| 13:38 | W11 cont (anomaly 207k, memory 133k, rca 181k) | sonnet | 0.52M | 0.65 | ~9.15 |
| 09:29 | W12 review (anomaly 236k, correlate-memory 205k, rca 249k) | sonnet | 0.69M | 0.86 | ~10.01 |
| 10:09 | W13 impl (remediate 152k, nl 182k, eval 245k) | sonnet | 0.58M | 0.72 | ~10.73 |
| 14:24 | W14 review (remediate 208k, nl 161k, eval 158k) | sonnet | 0.53M | 0.66 | ~11.39 |
| 20:20 | W15 impl (llm-reasoner ~est, api+web ~est, cmd ~est) + cont (cmd/store 133k, api 158k) | sonnet | ~0.29M measured cont | ~0.36 | ~11.75+ |
