# W14 — Security review of `internal/remediate` (F09 guarded remediation)

Reviewed against: `docs/architecture/features/F09-guarded-remediation.md` (full),
`docs/architecture/06-decision-register.md` DR-22 (L2253-2368), DR-23
(L2369-2536), DR-24 (L2537-2594). Prior work reviewed: `docs/reports/w13-remediate.md`.

## Verdict: **APPROVE-WITH-FIXES** (fixes applied in this pass)

One Blocker and four Major findings were found by re-deriving each of the 8
required security scenarios against the actual code (not just re-reading
the w13 report's claims) and by attempting to construct a bypass for each
gate the design relies on. All five were fixed in this pass, with a
regression test added per finding, and the full suite (27 tests, up from
18) plus `go build`/`go vet`/full-repo `go test ./...` are green.

## Fail-closed verdict (the task's top priority): **PASS, confirmed fail-closed**

`KubectlExecutor.Do` (`internal/remediate/executor.go`) wraps the call to
the still-unimplemented `k8s.BuildArgv`/`k8s.MinimalEnv`
(`internal/k8s/k8s.go` — both still `panic("not implemented")`, confirmed
by reading the file) in a `defer recover()` that converts any panic into a
plain `error`, and returns an error (never a synthesized success) when
`Runner` is `nil`. There is no path through `Do` that returns
`(Result{ExitCode:0}, nil)` without either (a) a real `argvRunner` actually
having been invoked, or (b) an explicit error. `TestKubectlExecutor_NeverSpawnsRealProcess_AndSurvivesUnimplementedBuildArgv`
exercises this directly and asserts the runner is never called. This is
correct, safe boundary behavior for a stubbed-upstream dependency in a
mutation path — it fails closed, not silently-succeeds-without-acting.

## Findings

| # | Sev | File:Line (pre-fix) | Issue | Fix | Status |
|---|---|---|---|---|---|
| B1 | **Blocker** | `validate.go` `checkAllowlists` (was line 137) | `okType := len(pol.RemediationAllowlist) == 0` — an empty/unconfigured `RemediationAllowlist` (the documented zero-config value, `remediate.allowlist: []`) **allowed every action type**, inverting FR-F09-2's fail-closed requirement and inconsistent with the very next check in the same function (`NamespaceAllowlist`, which correctly defaults to deny). No test exercised this path — `testPolicy()` in `guard_test.go` never set `RemediationAllowlist`, so every existing test unknowingly relied on the fail-open bug. | Changed to fail-closed (explicit membership required, same as `NamespaceAllowlist`). Updated `testPolicy()` to set an explicit allowlist. Added `TestPropose_EmptyRemediationAllowlistDeniesAll`. | **FIXED** |
| M1 | Major | `validate.go` `checkAllowlists` | `tenant.Policy.TargetAllowlist` (glob rules, e.g. `"prod/Deployment/checkout-*"` — declared in `internal/tenant/tenant.go:37`, named explicitly in F09 §2's drawback table and DR-22 §22.3 step 4) was **never referenced anywhere in `internal/remediate`** — confirmed by grep, zero hits. Only `Type` and `Namespace` were gated; the specific-target allowlist was a complete no-op. Not flagged in the w13 report's scope-reduction list. | Added `checkTargetAllowlist` (glob match via `path.Match` on `"<namespace>/<Kind>/<name>"`, fail-closed on empty). Wired into `checkAllowlists`. Added `TestPropose_TargetNotInTargetAllowlistDenied`. | **FIXED** |
| B2 | **Blocker**-class (fail-open in a mutation path) | `guard.go` `Execute()` (was ~line 447) | FR-F09-6: "If the snapshot read errors, `Execute()` MUST abort, leave the action in state `Approved` (not `Executing`), and emit audit event `snapshot_failed`." The code was `if err == nil { a.PreSnapshot = snap }` — **the error was silently discarded** and execution proceeded to `Executing` and on to the mutating call regardless. This also silently defeated `Rollback`'s concurrent-modification check (§23.9), which only runs when `PreSnapshot.ResourceVersion != ""`. | Now returns immediately on a `Snapshot` error, leaves `a.State` at `Approved`, audits `snapshot_failed`. Added `TestExecute_SnapshotErrorAbortsAndLeavesActionApproved` (also asserts the fake mutating executor is never reached and exactly one `snapshot_failed` audit row exists). | **FIXED** |
| M2 | Major | `errors.go` / `executor.go` | `ErrPayloadOutOfScope` (FR-F09-17 / DR-22 §22.2's payload-diff scope check) was **declared but never returned or checked anywhere** in the package — grepped, zero call sites. The Guard never diffed a computed patch against the allowed-path set before sending it to the executor. | Added `checkPayloadScope` (validates `ToggleFeatureFlag`'s merge-patch has exactly the one expected key, and `RemoveIstioFault`'s patch ops are `remove` on `/spec/http/*/fault` only — the latter currently unreachable since `stdinPayload` already errors for that type, but now meaningful the moment that gap closes). Wired into `Execute()` right after `stdinPayload`. Added 3 new unit tests in `executor_test.go`. | **FIXED** |
| M3 | Major | `guard.go` `Approve()` (was ~line 350) | The plain (non-override) budget-exceeded refusal returned `ErrBudgetExceeded` with **no audit event and no state transition** — silently leaving the action in `Proposed`. DR-23 §23.6's own pseudocode is explicit: `a.State = Rejected; Audit.Append("budget_exceeded", a)`. A refusal with zero audit trail is a real gap in a package whose stated value proposition is a tamper-evident record of every proposed/approved/executed/refused action. | Sets `a.State = Rejected`, `StateReason = "budget_exceeded"`, audits `approve_refused_budget_exceeded`, returns the updated action alongside the error (matching this file's existing pattern for TTL expiry). Added `TestApprove_BudgetExceededTransitionsToRejectedAndAudits`. | **FIXED** |
| M4 | Major | `guard.go` `Execute()` | FR-F09-6 step 2 and DR-23's pseudocode both require allowlists to be re-evaluated *after* re-resolution, inside `Execute`'s own critical section — "evaluate NamespaceAllowlist / TargetAllowlist (only AFTER re-resolution)". Only `Propose()` ever called `checkAllowlists`; a tenant policy change between approval and execution (namespace pulled from the allowlist, `RemediationAllowlist` tightened, etc.) had zero effect on an already-approved action. | Re-fetches policy and re-runs `checkAllowlists(a.Proposal, pol)` inside `Execute()`, before the snapshot read; a violation fails the action (`allowlist_revoked`) and audits. Added `TestExecute_RefusesWhenAllowlistRevokedAfterApproval`. | **FIXED** |
| C1 | Minor / coverage gap | `guard_test.go` | `Rollback()` (DR-23 §23.9 — concurrent-modification refusal, `scope_violation`, budget consumption, pre-authorization) has **zero tests** anywhere in the suite, despite being one of the safety-critical branches and despite the w13 report's phrasing ("implemented and reachable") reading as if it were exercised. Left as a follow-up — not fixed in this pass, flagged for a future session; the mechanism itself was read and looks structurally sound (refuses on `ResolvedVersion` mismatch, sets `Failed`/`concurrent_modification`, audits) but is unverified by any test. | — | Not fixed (scope: flagged, not required to block approval) |

## Re-verification of the 8 required security scenarios

All 8 re-checked directly against the code (not the report's prose), after fixes:

| Req | Verified how | Result |
|---|---|---|
| (a) no free-form params, type-level rejection | `validateProposal` exhaustively checks exactly-one-spec-set + Type match against the closed 5-value enum; `model.ActionProposal` has no `Params`/patch-string field at all (grepped `internal/model/remediate.go`) | Confirmed, rigorously tested (6 subtests) |
| (b) separation of duty | `sepOfDuty` calls `auth.Authorizer.SeparationOfDuty` (falls back to `proposer==approver` check when `Authz` is nil); tested both directions | Confirmed |
| (c) TTL expiry → Expired, blocks Execute | Checked inside the same mutex-protected critical section as the state read — no poller dependency; second `Execute` call after expiry re-confirmed refused | Confirmed |
| (d) `auto_execute` defaults false | `cfg.AutoExecuteOnApprove && pol.AutoExecuteAllowed`, forced false at RiskTier 3 regardless; `Approve` never tail-calls `Execute` | Confirmed |
| (e) budget counts failed/rejected actions | `budgetUsed` excludes only `Rejected`/`Expired`; a `Failed` action still occupies its slot (verified by tracing `TestBudget_CountsFailedActionAgainstCap`) — note: a **budget-exceeded refusal now correctly transitions the blocked action itself to `Rejected`** (fix M3), which per the same formula frees its own slot, matching DR-23's stated semantics | Confirmed |
| (f) dryrun never touches real state | `DryRunExecutor` is a zero-field struct with no client/socket/file handle — structurally incapable, not just behaviorally untested | Confirmed |
| (g) audit hash-chain tamper detection | `chainHash` folds `prevHash` into every entry; `VerifyChain` recomputes and reports the first broken seq; tamper test mutates payload in place without recomputing hash and detection is exact | Confirmed |
| (h) kubectl argv never string-interpolated | `buildKubectlRequest` only ever populates typed `k8s.Request` fields; `k8s.BuildArgv` is the sole argv constructor (currently unimplemented, and its absence is handled fail-closed per the verdict above); patch bodies travel on `StdinJSON`, never argv | Confirmed |

## Bypass attempts (none succeeded)

- **Malformed action reaching `Execute()` without validation?** No — `Execute()` only ever operates on an `Action` already created by `Propose()` (which ran `validateProposal` + `checkAllowlists` + resolution), and the in-memory store is unexported; there is no caller-reachable path that constructs an `Action` bypassing `Propose`.
- **Approval TTL race?** No — the entire `Approve`/`Execute` method bodies run under `guard.mu`, which is the "transaction" DR-23 §23.3 calls for; the expiry check and the state compare-and-set are in the same critical section.
- **Audit chain forged by an in-process caller?** `InMemoryAuditLog.tamperForTest` exists only to let this package's *own tests* prove tamper detection; it is unexported and unreachable from any other package. No forgery path exists for a real caller.

## Build/vet/test status after fixes

```
go build ./internal/remediate/...   → clean
go vet ./internal/remediate/...     → clean
go test ./internal/remediate/... -v → PASS, 27/27 (18 original + 9 new regression tests), ok  traceiq/internal/remediate  ~0.7-1.2s
go build ./...                      → clean
go test ./... -count=1              → ALL PACKAGES PASS (previously-reported pre-existing failures in
                                       internal/archtest, internal/eval, internal/nl from w13-remediate.md
                                       are no longer present — full repo is green as of this review)
gofmt -l internal/remediate/        → clean
```

## Files changed in this pass

- `internal/remediate/validate.go` — B1 fix (fail-closed `RemediationAllowlist`), M1 fix (`checkTargetAllowlist` + `kindName`)
- `internal/remediate/executor.go` — M2 fix (`checkPayloadScope`)
- `internal/remediate/guard.go` — B2 fix (abort on snapshot error), M3 fix (audit+Reject on budget_exceeded), M4 fix (re-check allowlists in `Execute`)
- `internal/remediate/guard_test.go` — updated `testPolicy()` fixture; added `TestPropose_EmptyRemediationAllowlistDeniesAll`, `TestPropose_TargetNotInTargetAllowlistDenied`, `TestExecute_SnapshotErrorAbortsAndLeavesActionApproved`, `TestExecute_RefusesWhenAllowlistRevokedAfterApproval`, `TestApprove_BudgetExceededTransitionsToRejectedAndAudits`
- `internal/remediate/executor_test.go` — added `TestCheckPayloadScope_AcceptsWellFormedToggleFeatureFlagPayload`, `TestCheckPayloadScope_RejectsExtraKeysOrWrongKey`, `TestCheckPayloadScope_RemoveIstioFaultRejectsPathsOutsideAllowedSet`

## Not in scope of this review (unchanged from w13-remediate.md)

`k8s.BuildArgv`/`MinimalEnv` remain unimplemented upstream (verified fail-closed, above); `RemoveIstioFault` JSON-patch diffing remains a `NEEDS_CONTEXT` stub; real `TargetResolver`/`Snapshotter`/`SignalSource`/`PolicyStore` wiring, SQL-backed persistence, `Approve`'s idempotency key, and the 81-pair transition-matrix fuzz all remain as documented in `docs/reports/w13-remediate.md`. `Rollback()` test coverage (finding C1) is flagged above as a follow-up.
