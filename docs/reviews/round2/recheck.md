# TraceIQ — Round 2 Re-check (scoped verification gate)

**Round:** 2 — scoped re-check
**Date:** 2026-09-16
**Board:** Architecture Review Board
**Scope:** the 10 items the orchestrator submitted as resolved. This is **not** a full re-review;
findings outside these 10 are noted only where the round-2 fix wave touched them.
**Method:** targeted grep + bounded read per item against the finding text in
[`verification.md`](verification.md). No full-document reads.

---

## Verdict

> ## **NOT APPROVED — 1 of 10 remains open**
>
> Nine items are **CONFIRMED-FIXED** and the scaffold gate is green. **Item 7 (AC-ID renumbering)
> is overruled: the orchestrator's "false lead" ruling rests on a premise that is factually
> false in both spot-checked documents**, and `DR-38 §38.4(2)` still normatively mandates the
> renumbering that was not performed. Three new issues are recorded below, one of which
> (**NI-3**, a dangling `AC-XSEC-11` cited by the register itself) is the concrete harm the
> item-7 ruling assumed could not occur.

**Scaffold gate — GREEN.**

```
GOTOOLCHAIN=go1.27.1 CGO_ENABLED=0 go build ./...   # PASS (exit 0)
GOTOOLCHAIN=go1.27.1 CGO_ENABLED=0 go vet ./...     # PASS (exit 0)
GOTOOLCHAIN=go1.27.1 CGO_ENABLED=0 go test ./...    # PASS (exit 0) — ok traceiq/internal/archtest 0.924s
```

`go version` on the build box reports `go1.27.1 windows/amd64`, so the pin is the toolchain that
actually ran, not a requested-and-silently-substituted one.

---

## Item table

| Item | Verdict | Evidence |
|---|---|---|
| **1 — N2-1** `05` live CI text | **CONFIRMED-FIXED** | `05 §6 D-12` l.277 now reads `` `GOTOOLCHAIN=go1.27.1` \| — \| **Enforced in CI. Never `auto`, never `local`** (DR-1) ``. The `govulncheck` row (l.274) reads "**blocking on any reachable finding — no allowlist file of any kind (DR-1; FU-2 CLOSED — not needed)**"; the three analyser rows now read "pinned in `§ 8.2`" instead of Set-A versions. Every surviving `GOTOOLCHAIN=local` hit is historical and correctly scoped: l.148/157 (§5 verification-methodology record of the 2026-09-14 run on go1.23.1), l.380/428 (§8.1 Set A, headed WITHDRAWN). l.450 forbids both `auto` and `local`; l.500, l.558 restate `go1.27.1`. **Zero** `TBD` strings remain in the document. |
| **2 — N2-2** `F01` overflow ACs | **CONFIRMED-FIXED** | `F01 §7` l.366 `AC-F01-4` now tests `shed` (sheds immediately, `traceiq_ingest_spans_dropped_total{reason="shard_full"}` rises, zero unbounded growth) and `block_up_to_timeout` (blocks ≤ `enqueue_timeout` then sheds), citing **DR-28 §28.1**. l.371 `AC-F01-9` carries DR-28's text verbatim (2× ingest capacity ⇒ `429`s rise, RSS under `01 §10.2`, bounded goroutines, goroutine-dump shows zero handlers blocked > `enqueue_timeout`). Unit-test bullet l.343 rewritten to `shed` vs `block_up_to_timeout`; l.344 records that "`block` and `reject` no longer exist as policy values (DR-28 §28.1)". Integration bullet l.354–358 asserts the AC-F01-9 criteria. Residual `reject` hits are the unrelated `oversize_attr: truncate\|reject` limits key (l.217, l.323) — different config key, not the overflow policy. |
| **3 — N2-3** `05 §D-13` kubectl | **CONFIRMED-FIXED** | Version pinned: **`v1.32.3`**, with a re-verify instruction tied to the commit that lands the Dockerfile. Digest recorded as the honest CI pattern, not a fabricated hash: `sha256:<TO BE COMPUTED AT IMAGE BUILD — CI step must record and pin this>`, with the fail-closed rule ("the Dockerfile fails closed if a subsequent fetch of that version does not match the recorded digest") and an explicit statement that this is the reproducible-build pattern (FU-9), not an unpinned placeholder. Upstream URL and SBOM-entry rows populated. CVE-tracking owner named: **Platform/SRE team**, via the Kubernetes `security-announce` list, with each patch release reviewed before the pin is bumped. No `TBD` anywhere in `05`. |
| **4 — N2-4** `api_Deps` vs scaffold | **CONFIRMED-FIXED** | `02 §4` l.1430–1455 and `internal/api/api.go` l.108–164 match **field-for-field, in order, 25 fields**: `Config`, `SelfObs`, `Cluster`, `Auth`, `Authorizer`, `RateLimit`, `Audit`, `Identities`, `Tenants`, `Policies`, `Topology`, `Remediate`, `Interpreter`, `Answerer`, `Eval`, `Investigations`, `Memory`, `Correlate`, `Anomaly`, `Baselines`, `Deploys`, `Sampler`, `Store`, `Cold`, `K8s` — with the type on each side agreeing (`store_HotIndex`/`store.HotIndex`, `anomaly_Grouper`/`anomaly.Grouper`, etc.). The reconciliation direction is recorded at `02` l.1736 with the DR-0 rationale, and the genuine reverse gap is closed: `Interpreter nl.Interpreter` was **added to the scaffold** (api.go l.124–129) because `nl.Answerer.Answer` consumes the `Intent` only `nl.Interpreter.Interpret` produces (DR-35 §35.2). Orphan `anomaly_Engine` is **removed outright** from `02 §2` with a justification comment (l.716–720: names no DR, `internal/anomaly` declares no `Engine`); the only surviving mentions repo-wide are that comment, the l.1736 note, and the round-2 review docs. All `api_Deps o--` edges (l.1537–1548) point at extant classes. |
| **5 — N2-5** PRD ROI row | **CONFIRMED-FIXED** | `Tracing-Bot-PRD.md` l.251 now reads `~$7.3k (ceiling)`, replacing the stale `~$6–12k`. l.254–262 carry a visible formula footnote: the per-investigation cost formula over `rca.llm.pricing`, the DR-17 §17.1 token model (`uncached_in = 97,920`, `cached_read = 541,432`, `out = 64,000`), the note that `rca.llm.pricing` defaults to `0` in `01 §7` until an operator supplies live rates (DR-26 rule 8), and the derivation of the carried figure — the per-tenant daily cap `rca.budget.max_cost_micro_usd_per_tenant_per_day` = $20/day × 365 = **$7,300/yr** (DR-17 §17.4). **DR-34 §34.3 is cited**, and the prior figure is explicitly named as superseded. Arithmetic checks: total row `~$32.3–47.3k` = infra `$25–40k` + `$7.3k`; the stated 8-investigations/month band (median $0.08 → cap $0.50 per investigation, ≤ $48/yr) is consistent at 8 × 12 × $0.50 = $48. |
| **6 — Set-A** | **CONFIRMED-FIXED — orchestrator's judgment agreed, and it is now *more* correct than at filing** | `05 §8.1` is headed "**Set A — WITHDRAWN 2026-09-14**" with "**No document may pin from Set A**" and "retained below, unedited, purely as the historical record § 7.1 refers to". `01 §11` (l.3251) is a pointer only, stating no version of its own. Every repo-wide `Set A` hit is a withdrawal, supersession, or historical statement — **no document treats Set A as normative**. Note the orchestrator's premise has been *strengthened* since filing: N2-6 (out of my 10, checked because it bore on this item) is also resolved — `05 §6` `D-3`/`D-4`/`D-5`/`D-7`/`D-8`/`D-9` now all read "version pinned in `§ 8.2`" with the version numbers struck, and `D-7` records the GO-2026-6061 advisory as resolved under Set B. At filing time item 6 was correct *despite* `§6`; it is now correct *including* `§6`. |
| **7 — AC-ID "renumbering"** | **STILL-OPEN — orchestrator's ruling OVERRULED** | The stated reasoning is factually wrong on its first limb. **AC IDs do not mirror FR IDs.** `F09 §7` (l.708–722): `AC-F09-2`→`FR-F09-5`/`-20`; `-3`→`FR-F09-8`; `-4`→`FR-F09-9`; `-14`→`FR-F09-15`; `-15`→`FR-F09-16`; `-17`→`FR-F09-19`; `-19` **and** `-20`→both `FR-F09-20`; `-21` **and** `-22`→both `FR-F09-5`. Only 3 of 15 coincide (1→1, 7→7, 18→18) — chance, not design. `X-SEC §7` (l.592–601) is worse: `AC-XSEC-1`→`FR-XSEC-2`; `-7a–d`→`FR-XSEC-11…14`; `-14`/`-15`→both `FR-XSEC-10`; `-16…19`→`FR-XSEC-8`; `-20`→`FR-XSEC-2`; `-12`→**no FR at all**. Traceability is carried by the table's explicit FR column, not by the number — so the absent numbers (`AC-F09-5`,`-6`,`-8`…`-13`; `AC-XSEC-2`,`-4`,`-6`,`-8`,`-10`,`-11`,`-13`) are plain holes, exactly the "sampled rather than complete coverage" `DR-38 §38.4(2)` names. That clause is **still normative and unamended** (register l.3645), and DR-38's Docs-to-change line still reads "every `§7` (AC renumbering)". Nothing in `05 §9` FU-11 (l.533) or anywhere else records the renumbering as a tracked deferral — grep for `renumber` across `docs/architecture/` returns one unrelated hit (`F10` FR renumber note). **The second limb of the reasoning is sound**: a duplicate-definition sweep over all feature docs (`^\| AC-` rows, sorted, `uniq -c`) returns **zero** IDs defined twice, so repeated grep hits are indeed multi-site citations. That half is confirmed; the conclusion it was used to support is not. |
| **8 — N2-9 (DDL comment)** | **CONFIRMED-FIXED** *(see NI-1)* | `01 §5.1` l.1594 now reads `resolution INTEGER NOT NULL, -- 1=10s 2=5m 3=1h (model.Resolution, DR-39)`. The string `topology.Resolution` no longer appears in `01`. Item 8's stated scope is met exactly. |
| **9 — N2-10 (TODO + Appendix D)** | **CONFIRMED-FIXED** *(see NI-2)* | Both halves present. `internal/model/rca.go` l.281–298: `ToolResult` is declared in `model` (moved from the `rca`-local workaround) with a doc comment naming DR-15 `Tool.Invoke`/`ToolRegistry.Dispatch`, DR-16 §16.3's `Clamped`, DR-37's projection rule, and an explicit `// TODO(DR-15/DR-16): under-specified — the register never prints its field list; reconstructed minimally` over the four-field shape. `06-decision-register.md` l.3880–3886 adds "**Appendix D — Known gaps (round-2 verification, tracked not silently dropped)**" as a table, recording the gap, where it surfaces (F06 implementation), and the status "**Open — architecture board to resolve before F06 coding begins. Do not invent a field list; extend this appendix with a DR-40 when resolved.**" The register's closing line (l.3888) is updated to reference it. |
| **10 — N2-11 (`X-OPS §6` image)** | **CONFIRMED-FIXED** | `X-OPS-deployment.md` §6 l.417–422 adds "**Image contents (DR-1, DR-24).**" stating: base `gcr.io/distroless/static-debian12:nonroot` (citing `05 §8.2`), "exactly two binaries layered on top" — the statically-linked `traceiq` binary (`CGO_ENABLED=0`, `-trimpath`) and the pinned `kubectl` at "the version and build-time-computed digest recorded in `05 §D-13`", scoped to `remediate`'s `internal/k8s` executor path — and "No shell, package manager, or other tooling is present", with the pivot-resistance rationale. All four elements item 10 names are present. |

**Summary: 9 CONFIRMED-FIXED · 1 STILL-OPEN · 3 NEW-ISSUE (below).**

---

## New issues

Scan performed as directed: (a) does Appendix D conflict with DR-15/DR-16/DR-37's existing
`ToolResult` text; (b) does the `X-OPS §6` image sentence conflict with RBAC/ServiceAccount text
in the same document; plus a general sweep for facts the fix wave moved out of agreement.

**NI-1 (Minor, fix-wave-sharpened) — `01 §4.7` still declares `topology.Resolution`, which the
item-8 fix now contradicts inside the same document.**
*Where:* `01 §4.1` l.593–595 vs `01 §4.7` l.1332–1335 vs `01 §5.1` l.1594.
`§4.1` declares `type Resolution uint8` in the `model` block under DR-39 §39.1's "the ONLY RED
type" banner. `§4.7` opens `package topology` (l.1332) and declares `type Resolution uint8` a
second time, with `Edge.Resolution` typed against it (l.1344). `F04 §4.2` l.108–120 carries a
third copy. Before the fix, `§5.1`'s column comment agreed with `§4.7` (both `topology`); the
item-8 fix corrected the comment to `model.Resolution`, so the DDL comment and the type
declaration 250 lines above it in the *same file* now disagree. The code is unambiguous
(`internal/model/red.go` is the sole declaration; `topology.Edge.Resolution` and
`HotIndex.CascadeRED` both take `model.Resolution`), so this is a docs-only defect — but an
engineer reading `01 §4.7` or `F04 §4.2` still writes a type that does not exist. This is N2-9's
unfixed remainder; item 8 only ever scoped the comment.
*Fix:* delete the `Resolution` declaration from `01 §4.7` and `F04 §4.2`, leaving `01 §4.1`'s;
add the one-line tie-break note to DR-39 §39.1 that N2-9 asked for.

**NI-2 (Minor, introduced by the item-9 fix) — Appendix D's citation list is wrong in one
direction and short in the other.**
*Where:* `06-decision-register.md` l.3884.
Appendix D states the type is "referenced by name in DR-15, DR-16 §16.3, DR-37 **and Appendix
C**". DR-15 (l.1542/1549), DR-16 §16.3 (l.1710) and DR-37 (l.3556) check out. **Appendix C
contains zero occurrences of `ToolResult`** (verified: `sed -n '3855,3878p' | grep -c ToolResult`
→ `0`) — it is the FR/AC ID table and names no types. Conversely Appendix D **omits DR-18**
(l.1858–1860, l.1893, l.1900, l.1910, l.1913), which is the reference that makes the gap
load-bearing: `Step` persists `ToolResultHash`/`ToolResultRef`/`ToolResultBytes` "over the
canonical result bytes", and DR-18's replay drift-detection compares `sha256(newResult)` against
`ToolResultHash` — so replay correctness depends on bytes no document defines. N2-10 named DR-18
explicitly for this reason. No *substantive* conflict with DR-15/DR-16/DR-37 exists: DR-16 names
one field (`Clamped`), which is consistent with "no field *list* is printed", and the scaffold's
`Clamped` is register-mandated rather than invented, so "Do not invent a field list" is not
violated by the existing TODO'd struct.
*Fix:* swap `Appendix C` for `DR-18` in Appendix D's reference list, and note the replay-hash
dependency as the reason the gap blocks F06.

**NI-3 (Major, pre-existing but newly surfaced — the concrete harm item 7 assumed away) —
`AC-XSEC-11` is cited by three documents, including the register, and defined nowhere.**
*Where:* `01 §10` l.1419; `06-decision-register.md` l.479 (DR-6 §6.2's writer-budget table);
`F09 §4` l.156.
All three cite `AC-XSEC-11` as the acceptance criterion gating the control-writer budget
("≥ 200 tx/s, **p99 audit append ≤ 15 ms, independent of `store.hot.sqlite.batch_interval`**").
`X-SEC §7`'s AC table runs `1, 3, 5, 7a–d, 9, 12, 14, 15, 16…19, 20` — **there is no
`AC-XSEC-11` row**, and the register's own Appendix C (l.3873) mandates
"`AC-XSEC-7a–d, AC-XSEC-12 … AC-XSEC-20`", deliberately skipping 11. So a register-owned
performance budget (DR-6 §6.2, cited in SR-11/PD-17's evidence) is gated by an AC that does not
exist, and DR-38 §38.4(2)'s CI rule ("fail on any FR with zero ACs") would not catch it — it
checks FR→AC, not AC-citation→AC-definition. A full dangling-reference sweep found only this one
genuine case: `AC-XSEC-7`-bare (l.3079 in `01`, l.3574 in `06`) is a deliberate historical
reference to the pre-split corpus ID, and `7b`/`7c`/`7d`/`17`/`18`/`19` are covered by the
`7a–d` and `16…19` range rows.
*Fix:* define `AC-XSEC-11` in `X-SEC §7` (it has three inbound citations and a stated criterion,
so the text already exists), or retarget the three citations at `AC-F03-*`'s writer-budget AC.
Add an AC-citation-resolves check to FU-11's generated `traceability.md` so this class cannot
recur — which is also the cheapest way to close item 7.

---

## Checked and clean

- **Appendix D vs DR-15/DR-16/DR-37 — no substantive conflict.** See NI-2; the only defects are
  citation hygiene, not contradiction.
- **`X-OPS §6` image sentence vs RBAC/ServiceAccount text — no contradiction.** The RBAC bullet
  (l.407–410) grants write RBAC only to `api` (specifically `remediate`'s `internal/k8s` path)
  and states `gateway`/`sampler`/`brain` never touch the Kubernetes API; the image paragraph
  (l.417–422) ships `kubectl` in the runtime image. Since `server.mode` is one binary with five
  roles (`single | gateway | sampler | brain | api`, `X-OPS §3` l.11–12) deployed as four
  workloads, `kubectl` is present in pods that have no RBAC to use it. That is defense in depth,
  not a contradiction — RBAC is the binding control and an unauthenticated `kubectl` is inert.
  *Observation only:* the pivot-resistance argument is stated for `api` alone; one clause noting
  the binary is inert in the other three roles would close the reading gap.
- **`05 §5` methodology record.** The `GOTOOLCHAIN=local` / `go1.23.1` code block under
  "Verification: the decision was compiled, not asserted" is correctly a record of the
  2026-09-14 run, as the orchestrator states, and `§8.1`/`§7.1` frame it. *Observation only:* the
  block is introduced with "This is reproducible" and no date, so it reads as a live instruction
  on a skim — a dated header ("as run on 2026-09-14 under go1.23.1") would remove the ambiguity
  that N2-1 was filed about in the first place.
- **`X-OPS §6` pod-hardening set.** N2-11's required change also asked for the pod-hardening set
  (`runAsNonRoot`, `readOnlyRootFilesystem`, `capabilities.drop: [ALL]`, `seccompProfile`);
  `X-OPS` contains none of those strings. Not counted against item 10, whose stated scope is
  image contents only, and defensible under DR-0 since `05 §D-13` owns pod hardening and the
  `X-OPS` paragraph cites `05 §D-13`. Flagged so the next pass does not re-derive it.
- **Scaffold gate.** Build, vet and test all exit 0 under `GOTOOLCHAIN=go1.27.1 CGO_ENABLED=0`,
  on a box reporting `go1.27.1`. `internal/archtest` is the only package with tests and it passes.

---

## What remains before ARCHITECTURE-APPROVED

1. **Item 7** — either perform `DR-38 §38.4(2)`'s contiguous AC renumbering in `F09`/`F11`/`F12`/
   `X-SEC` with every inbound cross-reference updated, **or** amend `DR-38 §38.4(2)` to drop the
   renumbering mandate in favour of the FR-column + CI-gate mechanism and record that amendment.
   The board's preference is the second, folded into FU-11, plus the AC-citation-resolves check
   from NI-3 — renumbering stable requirement IDs churns every citation in the round-1 and
   round-2 review record for no traceability gain. What is not acceptable is the current state:
   a normative clause mandating work that was not done, with no decision recording why.
2. **NI-3** — define or retarget `AC-XSEC-11`.
3. **NI-1**, **NI-2** — Minor; may be bundled with the above.

*End of round-2 re-check. 10 items checked · 9 CONFIRMED-FIXED · 1 STILL-OPEN · 3 NEW-ISSUE ·
build/vet/test GREEN · verdict **NOT APPROVED**.*
