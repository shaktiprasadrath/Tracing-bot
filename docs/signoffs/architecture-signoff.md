# TraceIQ — Architecture Sign-off

**Project:** TraceIQ — distributed-tracing RCA bot (Go 1.27, `GOTOOLCHAIN=go1.27.1`, `CGO_ENABLED=0`)
**Date:** 2026-09-16
**Issued by:** Architecture Review Board
**Document:** `docs/signoffs/architecture-signoff.md`

---

## Verdict

> ## **APPROVED FOR IMPLEMENTATION**

The TraceIQ architecture is approved for implementation. Feature teams may begin coding against
the architecture documents as they now stand, subject to the single tracked gap recorded in
§4 below.

---

## 1. Scope of this sign-off

- **All 40 binding decisions, `DR-0` … `DR-39`**, are applied across the architecture document
  set: `00-feature-catalog`, `01-system-architecture`, `02-class-diagram`,
  `03-sequence-diagrams`, `04-execution-flow`, `05-tool-selection-adr`, the twelve feature docs
  `F01` … `F12`, and the two cross-cutting docs `X-SEC-security` and `X-OPS-deployment` — twenty
  documents, governed by `06-decision-register.md`.
- **98 of 98 round-1 findings resolved.** Confirmed against the register's own closing line:
  *"DR-0 … DR-39, forty binding decisions, 98 of 98 round-1 findings resolved."*
- **Round-2 verification is clean.** The round-2 scoped re-check
  (`docs/reviews/round2/recheck.md`) closed 9 of 10 items and left three open defects. All three
  are now verified fixed in the live documents. No round-2 finding remains open.

---

## 2. What was verified this round

Verification method: targeted `grep` plus bounded reads of the cited line ranges in the current
files on disk. The fix-wave report (`docs/reports/w7-fix.md`) was used only to locate claims; every
claim below was confirmed against the document text itself, not the report's prose.

### 2.1 AC-ID renumbering — `DR-38 §38.4(2)` — **CLOSED**

`DR-38 §38.4(2)` mandates that *"AC ID sequences are renumbered contiguously in `F09`, `F11`,
`F12` and `X-SEC`."* All four `§7` tables are now contiguous from 1:

| Doc | AC table on disk | Result |
|---|---|---|
| `F09-guarded-remediation.md` §7 (l.708–722) | `AC-F09-1` … `AC-F09-15` | contiguous |
| `F11-eval-harness.md` §7 (l.487–494) | `AC-F11-1` … `AC-F11-8` | contiguous |
| `F12-ui-api-integrations.md` §7 (l.513–523) | `AC-F12-1` … `AC-F12-11` | contiguous |
| `X-SEC-security.md` §7 (l.592–602) | `AC-XSEC-1`, `-2`, `-3`, `-4a–d`, `-5` … `-8`, `-9…12`, `-13`, `-14` | contiguous (sub-letter and range rows preserved) |

The clause is therefore satisfied as written; no amendment to `DR-38` is required, and no
normative clause now mandates undone work.

**NI-3 (`AC-XSEC-11` dangling) is closed as a side-effect and verified independently.** The
criterion is now defined — `X-SEC §7` l.601, `AC-XSEC-13`: *"Control writer: ≥ 200 tx/s, p99
audit append ≤ 15ms, independent of `store.hot.sqlite.batch_interval`"* — and all three inbound
citations were confirmed retargeted to it with matching criterion text:
`01-system-architecture.md` l.1415 (control-writer budget row), `06-decision-register.md` l.479
(DR-6 §6.2 writer-budget table), `F09-guarded-remediation.md` l.156. A fourth site,
`F03-tiered-storage.md` l.74, carries the same corrected citation.

**Cross-reference sweep.** Every distinct `AC-F09-*`, `AC-F11-*`, `AC-F12-*` and `AC-XSEC-*` ID
cited anywhere under `docs/architecture/**` resolves to a currently-defined row in its feature
doc's own table. No dangling citation remains in the normative corpus.

**Spot-checks of the old → new map, by criterion content (not number alone):**

| Site | Cites | Criterion text matches target row |
|---|---|---|
| `06` l.3431 (DR-36 §36.2) | `AC-F11-7` | yes — receiver-entry test hook (was `AC-F11-13`) |
| `06` l.3522 (DR-36) | `AC-F11-8` | yes — teardown verified on scenario panic (was `AC-F11-14`) |
| `06` l.2993 (DR-29 §29.3) | `AC-XSEC-6` | yes — viewer-scoped MCP token, JSON-RPC `-32000` (was `-12`) |
| `06` l.2994 (DR-29 §29.3) | `AC-XSEC-14` | yes — no endpoint reachable on HMAC signature alone (was `-20`) |
| `06` l.2730 (DR-25 §25.4) | `AC-XSEC-7`, `-8` | yes — route enumeration; Slack `403`/`403`/`200` (were `-14`, `-15`) |
| `F09` l.100, l.696 | `AC-XSEC-7`, `-8` | yes — content-matched against the new table |

No citation was found missed or mis-mapped.

### 2.2 `Resolution` type triplication — **CLOSED**

A repo-wide search for `type Resolution uint8` returns exactly **two** declarations, both correct:

- `internal/model/red.go` l.16 — the sole Go declaration.
- `06-decision-register.md` l.3662 — inside `DR-39 §39.1`'s `package model` block, which is the
  canonical decision that *creates* `model.Resolution`.

The three former duplicate declarations are gone. `01 §4.1` (l.592–604), `01 §4.7` (l.1332–1350),
`F04 §4.2` (l.102–126) and `06` DR-13 (l.1123–1141) now all carry the same convention — a
`// model.Resolution (DR-39 §39.1) — the ONLY RED type, declared once in internal/model; cited
here, not redeclared (DR-0)` comment with the struct field typed `Resolution model.Resolution`.
The DDL column comment at `01 §5.1` l.1590 reads
`resolution INTEGER NOT NULL, -- 1=10s 2=5m 3=1h (model.Resolution, DR-39)` and no longer
contradicts the type declaration above it. `topology.Resolution` appears nowhere in
`docs/architecture/**`. NI-1 is closed.

### 2.3 Appendix D wrong citation — **CLOSED**

`06-decision-register.md` Appendix D (l.3879–3885) now reads *"referenced by name in DR-15,
DR-16 §16.3, DR-37 and DR-18"* — the false `Appendix C` reference is removed and the
load-bearing `DR-18` reference is added, together with the reason it is load-bearing: `Step`
persists `ToolResultHash`/`ToolResultRef`/`ToolResultBytes` over the canonical result bytes and
DR-18's replay drift-detection compares `sha256(newResult)` against `ToolResultHash`. NI-2 is
closed.

### 2.4 Scaffold gate

Independently confirmed green by the orchestrator on a box reporting `go1.27.1 windows/amd64`:

```
GOTOOLCHAIN=go1.27.1 CGO_ENABLED=0 go build ./...   # PASS (exit 0)
GOTOOLCHAIN=go1.27.1 CGO_ENABLED=0 go vet   ./...   # PASS (exit 0)
GOTOOLCHAIN=go1.27.1 CGO_ENABLED=0 go test  ./...   # PASS (exit 0) — ok traceiq/internal/archtest
```

---

## 3. Deliberately out of scope

Stale AC-ID citations remain inside `docs/reviews/round1/**`, `docs/reviews/round2/**`,
`docs/reports/w1-*.md` and `docs/ledger.md`. These are dated, point-in-time review records that
describe what reviewers saw at the time; rewriting them to post-fix numbering would falsify the
audit history. They are correctly left untouched and are not defects. `internal/**` Go source
contains zero AC-ID references, so no source change was implied by the renumbering.

---

## 4. The one open gap — tracked, not blocking

**`model.ToolResult`'s field list is never printed in the decision register.**

- **Recorded at:** `06-decision-register.md` Appendix D (l.3879–3885), and in code at
  `internal/model/rca.go` as an explicit `TODO(DR-15/DR-16)` over a minimally reconstructed
  four-field shape.
- **Why it matters:** DR-18's replay drift-detection compares `sha256(newResult)` against the
  persisted `ToolResultHash`, so replay correctness depends on canonical result bytes that no
  document defines.
- **Disposition:** **This must be resolved by the F06 implementer (the RCA engine —
  `rca.Tool.Invoke`, `ToolRegistry.Dispatch`), in consultation with the architecture board,
  before F06 coding begins. It is not blocking for any other package.** F01–F05 and F07–F12,
  `X-SEC` and `X-OPS` may proceed immediately.
- **Do not invent a field list.** Resolution is to be recorded as a new `DR-40` extending
  Appendix D.

---

## 5. Minor residue (non-blocking, recorded for the next editorial pass)

`01-system-architecture.md` l.3075 and `06-decision-register.md` l.3573 each contain a narrative
aside referring to *"the prior corpora (`AC-XSEC-7`, `AC-F06-3`)"* — a deliberate historical
reference to the pre-split corpus ID. Under the new contiguous numbering, `AC-XSEC-7` is a live
ID for an unrelated criterion (route enumeration), so a reader chasing the number lands on the
wrong row. Both sentences are immediately followed by a table that maps `FR-XSEC-11…14` to the
correct current IDs `AC-XSEC-4a–d`, so no requirement loses coverage and no implementer is
misdirected. Recommended wording on the next pass: *"the prior corpora (the pre-split
`AC-XSEC-7`, now `AC-XSEC-4a–d`; `AC-F06-3`)"*. **Editorial only — does not block
implementation.**

Separately, `FU-11`'s generated `traceability.md` (per `DR-38 §38.4(2)`) should add an
AC-citation-resolves check alongside the existing FR-has-at-least-one-AC check, so the dangling
`AC-XSEC-11` class of defect cannot recur. Recorded as a CI hardening item, not a gate.

---

## 6. Authorization

The architecture of TraceIQ is **APPROVED FOR IMPLEMENTATION** as of 2026-09-16, on the basis of:
forty binding decisions applied across twenty architecture documents; 98 of 98 round-1 findings
resolved; all ten round-2 scoped items now confirmed fixed against live document content; and a
green `build` / `vet` / `test` gate on the pinned `go1.27.1` toolchain.

*Architecture Review Board — round 2 closed. Next board action: `DR-40` resolving the Appendix D
`model.ToolResult` gap, required before F06 implementation starts.*
