# W7 — Doc-fix wave (AC-ID renumbering, Resolution dedup, Appendix D citation)

Progressive log. Written one section per completed task so a follow-on agent can see
what's already landed if this run stops partway.

---

## Task 2 — `Resolution` type triplication in docs — DONE

**Ground-truth check first.** The task brief named three sites: `01-system-architecture.md`
near line 594 and near line 1334, and `F04-topology.md` near line 108. On inspection, **two of
those three were already fixed** — `01-system-architecture.md` §4.1 (~l.592-601, the `REDSample`
struct) and §4.7 (~l.1330-1350, `topology.Edge`) both already carry the
`// model.Resolution (DR-39 §39.1) — the ONLY RED type, declared once in internal/model; cited
here, not redeclared (DR-0)` comment with no local `type Resolution uint8` block, and the struct
field is already `Resolution model.Resolution`. `F04-topology.md` §4.2 (~l.100-126) is likewise
already clean (prose at l.102-104 + field at l.118).

A repo-wide grep for `type Resolution uint8` found the **actual** remaining duplicate somewhere
the brief didn't point at: **`docs/architecture/06-decision-register.md`, DR-13 section
(~l.1115-1145)** — `package topology` code block re-declaring `type Resolution uint8` + the
`Res10s/Res5m/Res1h` const block, with `Edge.Resolution` typed as bare `Resolution`. This is DR-13
(topology's original decision), which predates DR-39 (the later decision that consolidated all RED
types into one `model.Resolution` and is what 01/F04 now correctly cite) — DR-13's text was never
updated when DR-39 landed and 01/F04 were fixed.

**Fix applied:** deleted the `type Resolution uint8` + `const (...)` block from DR-13's code
sample, added the same "cited here, not redeclared" comment convention already used in 01/F04, and
changed `Resolution  Resolution` → `Resolution  model.Resolution` (06-decision-register.md, in the
DR-13 `topology.Edge` code block, ~l.1123-1141).

**Left untouched, deliberately:**
- `06-decision-register.md` DR-39 §39.1 (~l.3654-3683): this block opens with `package model` and
  is the actual canonical declaration this whole convention points at (mirrors
  `internal/model/red.go`, per NI-1's own text: "internal/model/red.go is the sole declaration").
  Inside `package model`, a bare `Resolution Resolution` field is correct, same-package Go —
  not a bug, do not touch.
- `01-system-architecture.md:1275` `Resolution string` (inside `model.Record`, the memory record
  type at l.1260-1289): verified by context — this sits next to `RootCause`, `Services`,
  `ErrorSignatures` and is the free-text "how was this incident resolved" narrative field on a
  memory/investigation record, a completely different concept from the RED-bucket resolution enum
  (10s/5m/1h). Confirmed genuinely different; left as `string`, no fix needed.
- `F04-topology.md` `AC-F04-4` (~l.472) and FR-F04-4 (~l.44): both use "Resolution" only as a
  generic noun in prose ("at each `Resolution`"), not a type reference. AC-F04-4's number was not
  touched (it isn't in Task 1's renumbering scope either).

Files changed: `docs/architecture/06-decision-register.md` (one code block).

---

## Task 3 — Appendix D citation fix — DONE

Verified via grep: `docs/architecture/06-decision-register.md` lines 3855-3878 (Appendix C, the
FR/AC ID manifest) contain **zero** occurrences of `ToolResult` — the false claim confirmed false.
DR-15 (l.1541/1548), DR-16 §16.3 (l.1709) and DR-37 (l.3555) all do reference `ToolResult` and
check out. DR-18 (header at l.1834, "The replay contract (D-Y1)") persists `ToolResultHash` /
`ToolResultRef` / `ToolResultBytes` (l.1857-1859) and its drift-detection at l.1899 compares
`sha256(newResult)` against `ToolResultHash` — the load-bearing reference Appendix D was missing.

**Fix applied** (06-decision-register.md, Appendix D row, ~l.3884): replaced "Appendix C" with
"DR-18" in the citation list, and added a clause explaining why DR-18 makes the gap load-bearing
(replay drift-detection cannot be verified against an underspecified type).

Files changed: `docs/architecture/06-decision-register.md` (Appendix D row).

---

## Task 1 — AC-ID renumbering — DONE

**Ground-truth check first, per instructions.** `docs/reviews/round2/recheck.md`'s item-7 evidence
quotes F09 AC-ID mappings that go up to `AC-F09-22`/`-23`, but the F09 file's own §7 table on disk
was already contiguous `AC-F09-1`…`AC-F09-15` (l.708-722) — i.e. F09's *own table* had already been
renumbered at some earlier point, but the **cross-document citations to the old numbers were never
updated**, so `01-system-architecture.md`, `06-decision-register.md` (including its own Appendix C
manifest) still cited `AC-F09-14`…`-23` for criteria that F09 itself now calls `AC-F09-6`…`-15`.
F11 and F12's own tables genuinely still had gaps (F11: 1,4,7,9,11,12,13,14; F12:
1,5,6,8,10,12,13,14,15,16,17) and needed real renumbering. X-SEC's table also had real gaps
(1,3,5,7a-d,9,12,14,15,16-19,20) **and** the NI-3 dangling `AC-XSEC-11` (cited by 4 documents,
defined nowhere in X-SEC's own table).

**Approach chosen, per file:**
- **F09** — table already contiguous 1-15 starting at 1; zero AC-ID-column edits needed (this is
  the "whichever requires touching fewer numbers" case — touching zero). Work was 100%
  cross-reference repair (see mapping below).
- **F11** — renumbered contiguous from 1 (old numbering had only "1" as a stable prefix; starting
  at 1 changes 7 of 8 IDs, same cost as any other contiguous choice here).
- **F12** — renumbered contiguous from 1 (same reasoning; old "1" was the only stable anchor).
- **X-SEC** — inserted a new row for the previously-undefined `AC-XSEC-11` criterion (control
  writer: ≥200 tx/s, p99 audit append ≤15ms, independent of `store.hot.sqlite.batch_interval`),
  mapped to **FR-XSEC-8** (the audit-chain FR) and placed beside the other audit-chain criteria
  (old `AC-XSEC-16…19`), then renumbered the whole table contiguous from 1. The `7a–d` sub-letter
  convention was preserved (kept as one numeral with four letter variants) since each sub-item is
  independently cited elsewhere by its own FR; the `16…19` group (four independently-meaningless-
  standalone whole numbers, always cited as a range) was kept as a whole-number range too.
  `AC-XSEC-10` and `AC-XSEC-13` were confirmed via grep to have zero citations anywhere — pure
  gaps, folded away by the renumbering with no cross-ref work needed.

**Scope of cross-reference fixes:** `docs/architecture/**` (01, 06-decision-register, all feature
docs) — i.e. the normative, living documents. `docs/reviews/round1/**`, `docs/reviews/round2/**`,
`docs/reports/w1-*.md` and `docs/ledger.md` were **deliberately left untouched**: these are dated,
point-in-time review/audit records, and rewriting their citations to match post-fix numbering would
falsify history rather than reflect it (the recheck.md and verification.md citations describe what
reviewers saw *at the time*, not the current state). `internal/**` Go source was grepped — zero
AC-ID references exist there (test names don't embed them), so no source changes were needed.

**Bare `AC-XSEC-7` (no letter)** at `01-system-architecture.md:3075` and
`06-decision-register.md:3573` are deliberate historical references to the pre-7a–d-split corpus ID
(per NI-3) — left untouched. Two more bare-`AC-XSEC-7`/`-8`-shaped citations were found in
`F09-guarded-remediation.md` (l.100 `AC-XSEC-7`, signature-alone-never-reaches-handler; l.696
`AC-XSEC-8`, Slack approval round-trip) that were **already** using what turn out to be the new
contiguous numbers even before this pass (F09 was evidently authored assuming the renumbering had
already happened) — verified by content match against the new table; left as-is, no edit needed.

**Extra dangling reference found and fixed beyond the brief:** `F09-guarded-remediation.md:156`
cited the control-writer-durability criterion as `AC-XSEC-9` (wrong under both old and new
numbering — old/new 9 is the CVE-gate criterion) — corrected to `AC-XSEC-13`.
`06-decision-register.md` DR-36 §36.2 (~l.3431) cited the receiver-entry test hook as
`AC-F11-13` (stale old number) — corrected to `AC-F11-7`.

### Old → new AC-ID map

**F09-guarded-remediation.md** (table unchanged — already contiguous 1-15; map is old-citation → current-id, needed only for the cross-doc reference fixes):

| Old (as cited in 01/06 before this fix) | New (current, matches F09's own table) |
|---|---|
| AC-F09-1..4 | unchanged (1, 2, 3, 4) |
| AC-F09-14 | AC-F09-6 |
| AC-F09-15 | AC-F09-7 |
| AC-F09-16 | AC-F09-8 |
| AC-F09-17 | AC-F09-9 |
| AC-F09-18 | AC-F09-10 |
| AC-F09-19 | AC-F09-11 |
| AC-F09-20 | AC-F09-12 |
| AC-F09-21 | AC-F09-13 |
| AC-F09-22 | AC-F09-14 |
| AC-F09-23 | AC-F09-15 |

**F11-eval-harness.md:**

| Old | New |
|---|---|
| AC-F11-1 | AC-F11-1 |
| AC-F11-4 | AC-F11-2 |
| AC-F11-7 | AC-F11-3 |
| AC-F11-9 | AC-F11-4 |
| AC-F11-11 | AC-F11-5 |
| AC-F11-12 | AC-F11-6 |
| AC-F11-13 | AC-F11-7 |
| AC-F11-14 | AC-F11-8 |

**F12-ui-api-integrations.md:**

| Old | New |
|---|---|
| AC-F12-1 | AC-F12-1 |
| AC-F12-5 | AC-F12-2 |
| AC-F12-6 | AC-F12-3 |
| AC-F12-8 | AC-F12-4 |
| AC-F12-10 | AC-F12-5 |
| AC-F12-12 | AC-F12-6 |
| AC-F12-13 | AC-F12-7 |
| AC-F12-14 | AC-F12-8 |
| AC-F12-15 | AC-F12-9 |
| AC-F12-16 | AC-F12-10 |
| AC-F12-17 | AC-F12-11 |

**X-SEC-security.md:**

| Old | New |
|---|---|
| AC-XSEC-1 | AC-XSEC-1 |
| AC-XSEC-3 | AC-XSEC-2 |
| AC-XSEC-5 | AC-XSEC-3 |
| AC-XSEC-7a | AC-XSEC-4a |
| AC-XSEC-7b | AC-XSEC-4b |
| AC-XSEC-7c | AC-XSEC-4c |
| AC-XSEC-7d | AC-XSEC-4d |
| AC-XSEC-9 | AC-XSEC-5 |
| AC-XSEC-12 | AC-XSEC-6 |
| AC-XSEC-14 | AC-XSEC-7 |
| AC-XSEC-15 | AC-XSEC-8 |
| AC-XSEC-16 | AC-XSEC-9 |
| AC-XSEC-17 | AC-XSEC-10 |
| AC-XSEC-18 | AC-XSEC-11 |
| AC-XSEC-19 | AC-XSEC-12 |
| *(new — was dangling `AC-XSEC-11`)* | **AC-XSEC-13** |
| AC-XSEC-20 | AC-XSEC-14 |
| AC-XSEC-10, AC-XSEC-13 (old, unused gaps) | — (folded away, zero citations) |

### Files changed for Task 1

- `docs/architecture/features/F09-guarded-remediation.md` (in-file citation, l.156)
- `docs/architecture/features/F11-eval-harness.md` (table + prose)
- `docs/architecture/features/F12-ui-api-integrations.md` (table)
- `docs/architecture/features/X-SEC-security.md` (table — includes the new AC-XSEC-13 row — + prose)
- `docs/architecture/features/F03-tiered-storage.md` (one citation)
- `docs/architecture/features/F06-rca-engine.md` (one citation)
- `docs/architecture/01-system-architecture.md` (multiple citations, including the writer-budget
  table and the FR-XSEC-11..15 table)
- `docs/architecture/06-decision-register.md` (multiple citations, DR-13, DR-23, DR-25, DR-27,
  DR-29, DR-36, DR-37, and Appendix C's F09/F11/F12/X-SEC rows)

### Verification (step 6)

Re-ran the cross-reference grep across `docs/architecture/**` after all edits: every remaining
`AC-F09-*`/`AC-F11-*`/`AC-F12-*`/`AC-XSEC-*` occurrence resolves to a currently-defined row in its
feature doc's own table, with two intentional exceptions both confirmed non-dangling: the bare
historical `AC-XSEC-7` references (pre-split corpus ID) and the one explanatory "formerly cited as
the undefined `AC-XSEC-11`" note left in X-SEC's new row for auditability. `internal/**` has zero
AC-ID references. Stale references remain, as designed, only inside
`docs/reviews/round1/**`, `docs/reviews/round2/**`, `docs/reports/w1-*.md`, and `docs/ledger.md`
(historical records, intentionally not rewritten).

---

## Final verification (all three tasks)

```
GOTOOLCHAIN=go1.27.1 CGO_ENABLED=0 go build ./...   # PASS (exit 0)
GOTOOLCHAIN=go1.27.1 CGO_ENABLED=0 go vet ./...     # PASS (exit 0)
GOTOOLCHAIN=go1.27.1 CGO_ENABLED=0 go test ./...    # PASS (exit 0) — ok traceiq/internal/archtest
```

`go version` on this box: `go1.27.1 windows/amd64` — the pin is the toolchain that actually ran.
