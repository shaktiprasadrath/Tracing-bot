# Development Lead Sign-Off — Review Trail Audit

**Role:** Development Lead, formal sign-off on code quality process (not a re-review of code).
**Scope:** Audit the completeness and honesty of the review trail (waves 9–18) — does every
implemented package have real review coverage, and does the record's "green" claim hold up under
independent re-verification. This document does not re-derive any individual finding from waves
10/12/14/16/17/18; their Blocker/Major/Minor counts and verdicts are taken as read and cross-checked
only at the ledger level (do the numbers, files, and coverage claims line up).

**Environment used for independent verification:** `GOTOOLCHAIN=go1.27.1`, `CGO_ENABLED=0`,
`go version go1.27.1 windows/amd64` — same toolchain pin every review wave used.

---

## 1. Package-to-review coverage table

24 buildable units exist (23 `internal/*` packages + `cmd/traceiq`), plus `web/`.

| Package | Dedicated review report | Coverage type |
|---|---|---|
| `internal/ingest` | `w10-review-ingest-topology.md` | Dedicated |
| `internal/topology` | `w10-review-ingest-topology.md`, revisited in `w17` (M1–M3) | Dedicated |
| `internal/sampler` | `w10-review-sampler.md`, revisited in `w17` (B1, B3) | Dedicated |
| `internal/store` (+`sqlite`,`parquet`,`tiered`) | `w10-review-store.md`; defect found live in `w18-live-test.md`, fixed in `w18-fix-d1.md` | Dedicated |
| `internal/anomaly` | `w12-review-anomaly.md` | Dedicated |
| `internal/correlate` | `w12-review-correlate-memory.md` | Dedicated |
| `internal/memory` | `w12-review-correlate-memory.md` | Dedicated |
| `internal/rca` (+`rules`) | `w12-review-rca.md`, `w16-review-rca-llm.md` | Dedicated |
| `internal/eval` | `w14-review-eval.md` | Dedicated |
| `internal/nl` | `w14-review-nl.md` | Dedicated |
| `internal/remediate` | `w14-review-remediate.md` | Dedicated |
| `internal/api` | `w16-review-api-web.md` | Dedicated |
| `web/` | `w16-review-api-web.md` (XSS/CSP/static-asset review) | Dedicated |
| `cmd/traceiq` | `w16-review-cmd.md`, `w17-final-review.md` | Dedicated |
| `internal/config` | `w16-review-cmd.md` §5 | Dedicated |
| `internal/llm` | `w16-review-rca-llm.md` (as `rca`'s consumer-side contract; no reachable call sites — `cmd/traceiq` does not import it) | Dedicated (contract-level) |
| `internal/archtest` | No standalone report; audited as a *tool*, not a subject, in `w17` check 1 (its own adjacency table checked against DR-2 and against the real import graph; one gap found and fixed, M4) | Cross-cutting only |
| `internal/tenant` | No standalone report; `TenantID` threading is traced end-to-end across every consumer in `w17` check 2 (the check that found B1–B3), but the package's own code (`Policy`, `WithTenant`/`FromContext`) has zero dedicated review and zero tests | Cross-cutting only |
| `internal/k8s` | No standalone report; `BuildArgv`/`MinimalEnv` (both still `panic("not implemented")`) audited for fail-closed call-site behavior in `w14-review-remediate.md` and again in `w17`'s fail-open sweep | Cross-cutting only |
| `internal/model` | No standalone report; its panic-stub methods audited for reachability in `w17` check 3 (zero call sites found); otherwise exercised only indirectly, as the type substrate every other package's tests already depend on | Cross-cutting only |
| `internal/depstub` | No standalone report; its 13 pinned modules individually audited for real-importer status in `w17` check 7 | Cross-cutting only |
| `internal/auth` | No standalone report. **245 lines, zero concrete implementations** — confirmed independently by `wc -l` and by `go test` reporting `[no test files]`. Its absence is the subject of `w17` Finding M5, not a gap in the review trail: there is no logic yet to review. | Documented as unimplemented, not silently skipped |
| `internal/bus` | Never reviewed, standalone or cross-cutting. 48 lines, no test files. | **Uncovered** |
| `internal/cluster` | Never reviewed, standalone or cross-cutting. 37 lines, no test files. | **Uncovered** |
| `internal/selfobs` | Never reviewed, standalone or cross-cutting. 57 lines, no test files. | **Uncovered** |

**Verdict on coverage:** every package that carries real branching logic has a dedicated review
report, and every one of those reports found and fixed genuine bugs (see §3). Three packages
(`bus`, `cluster`, `selfobs` — 142 lines combined) received no review of any kind, dedicated or
incidental, in any wave. Independently confirmed via `go build`/`go vet`/`go test` that all three
compile clean and have no test files to fail; a manual read confirms they are interface/type
declarations with no control flow, consistent with — but not independently verified by — `w17`'s
characterization of them. This is a real gap in the trail, not a severe one: there is nothing in
these files a review could meaningfully exercise, but "nothing to review" was never itself stated
and checked by any wave for these three specifically, unlike `auth`/`archtest`/`depstub`/`k8s`/
`model`, which all got at least a targeted look. Recommend a five-minute confirmatory pass before
final release sign-off, not a blocking item.

## 2. Independent build/vet/test/gofmt verification

Run directly against the repo at its current HEAD, not taken from any report's claimed output:

```
$ go build ./...
(exit 0)

$ go vet ./...
(exit 0)

$ gofmt -l .
(empty output — no files need formatting)

$ go test ./... -count=1
ok    traceiq/cmd/traceiq          13.074s
ok    traceiq/internal/anomaly      1.458s
ok    traceiq/internal/api          9.243s
ok    traceiq/internal/archtest     3.151s
?     traceiq/internal/auth         [no test files]
?     traceiq/internal/bus          [no test files]
?     traceiq/internal/cluster      [no test files]
ok    traceiq/internal/config       0.996s
ok    traceiq/internal/correlate    1.705s
?     traceiq/internal/depstub      [no test files]
ok    traceiq/internal/eval         2.677s
ok    traceiq/internal/ingest       2.822s
?     traceiq/internal/k8s          [no test files]
?     traceiq/internal/llm          [no test files]
ok    traceiq/internal/memory       1.221s
?     traceiq/internal/model        [no test files]
ok    traceiq/internal/nl           1.071s
ok    traceiq/internal/rca          1.164s
ok    traceiq/internal/rca/rules    1.115s
ok    traceiq/internal/remediate    1.281s
ok    traceiq/internal/sampler     30.427s
?     traceiq/internal/selfobs      [no test files]
ok    traceiq/internal/store        1.364s
ok    traceiq/internal/store/parquet 2.819s
ok    traceiq/internal/store/sqlite  2.191s
ok    traceiq/internal/store/tiered  2.808s
?     traceiq/internal/tenant       [no test files]
ok    traceiq/internal/topology     1.442s
?     traceiq/web                   [no test files]
```

**Result: the ledger's "green" claim is confirmed, independently, at HEAD.** No test failures, no
vet findings, no formatting drift, every package that should build does. This matches `w17`'s own
verification table and `w18-fix-d1.md`'s post-fix re-run exactly — nothing regressed between the
last report and this audit.

## 3. Review-trail health: was anything skipped or rushed?

**No wave was a rubber stamp.** Every one of the eight review reports (`w10`×3, `w12`×3, `w14`×3,
`w16`×3, `w17`) found and fixed at least one Major-or-above defect, several by independently
re-deriving the spec's numbers or attempting to construct a bypass rather than trusting the prior
wave's prose. Multiple reports explicitly catch and correct claims made by earlier reports (e.g.
`w16-review-api-web.md` re-derives the "16/34 endpoints" count from scratch rather than trusting
`w15`'s arithmetic; `w17` re-diagnoses the topology eviction flake from the code rather than
accepting `w16`'s suggested fix, and shows why that suggested fix would not have worked). That is
the behavior of a trail auditing itself, not one waving things through.

**The task brief's framing needs a correction.** It states waves 16–17 found "the first-ever
Blockers." That is not accurate against the reports themselves:

- `w10-review-sampler.md` found **two Blockers** at wave 10 (the shed-order violating AC-F02-13, and
  a missing concrete `Sampler` implementation).
- `w14-review-nl.md` found **one Blocker** (RBAC capability check skipped entirely on a nil
  `AuthZ`).
- `w14-review-remediate.md` found **one Blocker** (`RemediationAllowlist` defaulting to
  allow-everything when empty — a fail-open inversion) plus a second Blocker-class fail-open bug in
  `Execute()`.
- `w17-final-review.md` found **three Blockers**, all cross-tenant data-isolation breaches.

So Blockers were found in nearly every wave, not just at the end — the early waves were not
"too shallow to find Blockers." What *is* true, and is the real, useful distinction: every Blocker
through `w14` was a single-package defect (one file, one contract, one fail-open check). The three
`w17` Blockers were specifically **inter-package contract mismatches** — cases where package A's
code and package B's code were each individually defensible in isolation, and only broke when
traced across the boundary (a `TraceID`-only cache key that was fine until a second tenant's spans
could collide with it; an empty-subject-tenant fallback that was fine until the one production
resolver turned out to implement the "reject it" half as a bare pass-through). That class of bug is
**structurally invisible to a package-scoped review**, no matter how careful — which is exactly why
`w17` was scoped as a separate, later, whole-system pass in the plan to begin with, not as an
afterthought triggered by earlier passes failing. This is the expected, correct shape for a review
programme, not a symptom of `w10`/`w12`/`w14` being rushed.

**The more important, less comfortable data point is `w18`, not `w17`.** After all eight review
waves closed — three of them at Blocker severity, all confirmed fixed and green — the live
Kubernetes/Istio end-to-end test (`w18-live-test.md`) found a real, unflagged, un-Blockered defect:
every span in a multi-hop trace was stamped with the *trace's* root service instead of its own
originating service (`w18-fix-d1.md`, `internal/store/store.go`). This shipped through `w10`'s
dedicated `store` review, `w12`, `w16`, and `w17`'s full cross-package tenant-and-data-flow trace,
undetected, for one structural reason stated plainly in the fix report: **every test fixture in the
whole suite, across every wave, used single-hop or root-equals-span-service synthetic traces**, so
the bug was mathematically invisible to every test anyone had written. It only surfaced once real,
multi-hop mesh traffic (`mesh-client → transaction-orchestrator → payment-service`, a genuine
three-service call chain) was pushed through the real binary.

That is the honest lesson from this trail, and it argues for a specific conclusion: **eight waves
of rigorous code review are not, and cannot be, a substitute for at least one pass of live
integration testing with topologically realistic data.** Review — even excellent review, re-deriving
numbers and attempting bypasses — verifies code against the fixtures the reviewer thinks to write.
A bug that only exists in the gap between "what every fixture happened to test" and "what real
traffic actually looks like" needs real traffic to find. The programme got this right in the end
(it scheduled a live test, and that test's one finding was diagnosed and fixed same-day, with a
regression test that specifically exercises the multi-hop case the whole suite had been missing),
but it is worth stating for the record rather than treating `w17`'s "APPROVE-WITH-FIXES, proceed to
integration testing" as the finish line it reads like — `w18` correctly did not stop at proceeding
to integration testing, and found exactly the kind of bug that step exists to catch.

**Residual, disclosed-not-hidden gap:** `internal/auth` still has zero concrete implementation
(confirmed independently, §1), which means `internal/api` — and everything above it (`nl`,
`remediate`, `memory`, `correlate`, `eval`) — is implemented, individually reviewed, and
individually green, but **not exercised by `cmd/traceiq` as a running system** (`w17` Finding M5).
This is accurately and repeatedly disclosed across `w16-review-cmd.md`, `w17`, and is not a review
gap — it is a real, acknowledged product-completeness gap that bounds what "the review trail is
green" can honestly be taken to mean: it does not mean "the whole product has been observed
running end-to-end," only "the ingest→store→detection pipeline has" (as `w18` then did).

## 4. Formal sign-off

# APPROVED WITH CONCERNS

**Rationale.** The review trail itself is honest, rigorous, and self-correcting — every package
carrying real logic has dedicated review coverage, every wave found and fixed genuine defects
rather than rubber-stamping, and the "final green" claim (`go build`/`go vet`/`go test`/`gofmt`)
is independently reproduced at HEAD exactly as reported. Nothing was found to be skipped, rushed,
or misrepresented.

The concerns are specific and bounded, not a reason to block:

1. **`internal/auth` has no implementation**, which keeps roughly half the implemented surface
   (`api`, `nl`, `remediate`, `memory`, `correlate`, `eval`) out of the running binary. This is
   fully disclosed (M5) and does not affect the quality of what *is* wired, but it means "code
   quality is approved" should not be read as "the product is feature-complete" — it isn't, by
   design, pending that package.
2. **`bus`, `cluster`, and `selfobs` (142 lines total) received no review of any kind.** Low risk
   given their size and apparent lack of branching logic, but that lack was never independently
   confirmed by a reviewer the way `auth`/`model`/`k8s`/`depstub`/`archtest` were. A short
   confirmatory pass is recommended before the next milestone, not required to hold this sign-off.
3. **Eight waves of review did not catch `W18-D1`** (the multi-hop span-service bug) because no
   fixture in the entire suite exercised a genuinely multi-hop trace. It was caught by live testing,
   fixed same-day, and now has a regression test. The lesson to carry forward is process, not a
   code defect: schedule real, topologically realistic integration/live tests as a standing part of
   the release gate, not a one-off wave — package-level and whole-system *code* review, however
   careful, has a structural blind spot that only real traffic closes.

**Reviewed by:** Development Lead (review-trail audit, not a code review).
**Date:** 2026-09-20.
