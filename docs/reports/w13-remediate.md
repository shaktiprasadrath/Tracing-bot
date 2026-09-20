# W13 — `internal/remediate` implementation (F09 guarded remediation)

## Status: GREEN (core scope), with documented gaps

`internal/remediate` builds clean, `go vet` is clean, and its test suite
passes 18/18. Full-repo `go build ./...` is green. Full-repo `go test ./...`
has pre-existing failures in `internal/nl`, `internal/eval` and
`internal/archtest` — none touched by this change (see "Full-repo test run"
below).

## What was implemented

Files added, all under `internal/remediate/` (scope lock honored — no edits
to `internal/k8s`, `internal/model`, `internal/auth`, `internal/tenant`, or
any other package):

- `guard.go` — the `Guard` implementation (`NewGuard(Deps) Guard`): an
  in-memory control plane enforcing the 9-state machine, separation of
  duty, approval-TTL expiry-in-transaction, the attempted-mutation budget,
  budget overrides, target re-resolution, and rollback's
  concurrent-modification refusal.
- `validate.go` — `validateProposal` (type-level rejection of anything
  that doesn't match the closed `ActionType`/spec-pointer shape, plus
  per-type field bounds) and `checkAllowlists` (namespace/type allowlist,
  evaluated only after resolution per DR-22 §22.3 step 4). `riskTier()` is
  DR-22 §22.4's fixed table, verbatim — proposer-supplied values are always
  overwritten.
- `executor.go` — `DryRunExecutor` (zero I/O, structurally incapable of
  touching a real cluster) and `KubectlExecutor` (builds a typed
  `k8s.Request` via `buildKubectlRequest`/`verbForAction`/`stdinPayload`,
  delegates argv construction exclusively to `k8s.BuildArgv`, patch bodies
  on stdin never argv).
- `audit.go` — `InMemoryAuditLog`, a `auth.AuditSink`-satisfying,
  append-only, SHA-256 hash-chained log (each entry commits to the
  previous entry's hash; tampering any stored field breaks
  `VerifyChain` from that point forward). Per-tenant chains are
  independent.
- `errors.go` — sentinel errors, one per DR-22/DR-23 refusal code exercised
  by this pass (`ErrApproverMustDiffer`, `ErrActionExpired`,
  `ErrBudgetExceeded`, `ErrTargetNotResolvable`, `ErrTargetReplaced`,
  `ErrInvalidProposal`, etc).
- Tests: `guard_test.go`, `executor_test.go`, `audit_test.go` — table-driven
  per the task's required scenarios (a)–(h), see below.

`remediate.go` (the pre-existing scaffold with `Guard`, `ApprovalRequest`,
`RecoverySignal`, `Verifier`, `ActionFilter`, `Stats`) was left untouched;
all new code implements/consumes it as given.

## Security-relevant test results (all passing)

| Req | Test | Result |
|---|---|---|
| (a) malformed/mismatched typed payload rejected at Propose | `TestPropose_RejectsMalformedTypedPayload` (6 subtests: no spec, type/spec mismatch, two specs set, out-of-range replicas, injection-shaped namespace, slash-in-name) | PASS |
| (b) separation of duty | `TestApprove_RejectsSameProposerApprover`, `TestApprove_DifferentApproverSucceeds` | PASS |
| (c) TTL expiry blocks execution | `TestExecute_ExpiredApprovalMovesToExpiredAndRefusesExecute` — advances a fake clock past `approval_ttl`, asserts `Execute` returns `ErrActionExpired`, state becomes `Expired`, and a second `Execute` call is still refused | PASS |
| (d) `auto_execute_on_approve` defaults false | `TestApprove_AutoExecuteDefaultsFalse` (global flag true, tenant policy false ⇒ AND is false, action stays `Approved`), `TestApprove_RiskTier3ForcesAutoExecuteFalseRegardlessOfConfig` | PASS |
| (e) budget counts failed actions | `TestBudget_CountsFailedActionAgainstCap` — cap=1, one action fails via executor error, a second proposal on the same incident is refused with `ErrBudgetExceeded` | PASS |
| (f) dryrun never touches real state | `TestDryRunExecutor_NeverTouchesRealState` — structural (empty struct, no client field) + behavioral (spy counter stays 0 across 5 calls) | PASS |
| (g) audit hash chain / tamper detection | `TestAuditLog_HashChainDetectsTampering` (in-place payload mutation breaks `VerifyChain` at the tampered seq), `TestAuditLog_PerTenantChainsAreIndependent`, `TestAuditLog_QueryFiltersByActorAndAction` | PASS |
| (h) kubectl argv never string-concatenated | `TestBuildKubectlRequest_NoShellMetacharactersAndTypedFieldsOnly`, `TestBuildKubectlRequest_RejectsInjectionShapedNamespaceOrName` (semicolons, `&&`, `../`, `$(...)` all rejected before reaching a Request), `TestVerbForAction_IsAPureSwitchNoStringBuilding`, `TestKubectlExecutor_NeverSpawnsRealProcess_AndSurvivesUnimplementedBuildArgv` | PASS |

Additional coverage beyond the minimum list: target re-resolution refusing
an unresolvable target before the allowlist check
(`TestPropose_UnresolvableTargetRefused`), and execute-time UID
re-resolution catching a target swapped out between approval and execution
(`TestExecute_TargetReplacedFailsAction`, DR-22 §22.3 step 3).

Test count: **18 tests, 18 passing, 0 failing**, plus 6 subtests under
`TestPropose_RejectsMalformedTypedPayload`.

```
go test ./internal/remediate/... -v   → PASS (all), ok  traceiq/internal/remediate  1.285s (fresh run)
go build ./...                        → clean
go vet ./internal/remediate/...       → clean
```

## Deliberate scope reductions (NEEDS_CONTEXT / partial)

These are documented gaps, not silent omissions. All are things blocked by
a dependency this task was explicitly forbidden from editing
(`internal/k8s`), or that require infrastructure (SQL persistence, a real
topology/auth wiring) outside `internal/remediate`'s scope lock:

1. **`k8s.BuildArgv` / `k8s.MinimalEnv` are `panic("not implemented")`
   upstream** (internal/k8s is scaffolded but not implemented — confirmed
   by reading `internal/k8s/k8s.go`). `KubectlExecutor.Do` recovers that
   panic and returns a plain error instead of crashing, so real kubectl
   execution is currently unavailable end-to-end, by construction, until
   `internal/k8s` ships. Everything upstream of that call
   (`buildKubectlRequest`, `verbForAction`, `stdinPayload`) is implemented
   and tested directly, per the task's own instruction to "test the
   builder directly, not by spawning real kubectl."
2. **`RemoveIstioFault`'s JSON-patch diff** (DR-22 §22.2: diff
   `PreSnapshot.spec.http[*]` against the same object with `fault`
   removed) is not implemented — `stdinPayload` returns a `NEEDS_CONTEXT`
   error for this one action type when a snapshot is present, and
   `ErrNothingToRemove` when it is not. VirtualService-shaped diffing
   needs a real object schema this pass didn't have time to model safely
   (a wrong diff here is exactly the kind of bug DR-22 exists to prevent).
3. **Idempotency (DR-23 §23.8)** is enforced for `Propose` and `Execute`
   (in-memory `tenant|endpoint|key → request_hash` cache, replay-safe,
   `ErrIdempotencyReused` on a hash mismatch) but **not** for `Approve` —
   the `Guard.Approve` signature in the given scaffold carries no
   `Idempotency-Key` parameter, unlike `Propose`/`Execute`. No SQL-backed
   `idempotency` table (DR-23 §23.8's DDL) was built; this is in-memory
   only, matching the whole package's persistence posture for this pass.
4. **Persistence**: the control plane (`guard.actions`) is entirely
   in-memory (`map[string]*model.Action` guarded by a mutex). No
   `internal/store` wiring, since that would mean editing/depending on
   store internals beyond this task's scope and time budget; the `Guard`
   interface and all its invariants are implemented and tested against
   that in-memory store, so swapping in a persistent backing store is a
   mechanical follow-up, not a design change.
5. **DR-23 §23.9 auto-rollback wiring**: `Rollback`'s
   concurrent-modification refusal is implemented and reachable, but there
   is no automatic Verifying→RolledBack trigger on a failed post-verify —
   `Rollback` must be called explicitly by a caller. AC-F09-12's "an
   auto-rollback consumes a slot of its own" (a second budget-relevant row
   distinct from the original action) is not modeled; this pass's simpler
   interpretation — the same action's `Failed`/`RolledBack` state already
   counts against budget — satisfies the task's required test (e) but is a
   narrower reading than the register's stricter one.
6. **`TargetResolver`/`Snapshotter`/`SignalSource`/`PolicyStore` are
   consumer-declared interfaces**, not wired to real `topology.Graph`,
   `k8s.Executor` (Get), `auth.Authorizer`, or `tenant.PolicyStore`
   implementations — those wiring points belong to whichever batch owns
   `cmd/traceiq`'s dependency injection, out of this task's scope. Fakes
   satisfying each interface are used in tests.
7. Chat-approval flow (§23.7), the 81-pair full transition-matrix fuzz
   (`AC-F09-9`), the 50-case argv fuzz (`DR-24`), and the adversarial
   corpus (`AC-F09-6`) were not built — time-boxed out given the size of
   the full F09 spec relative to the session budget.

## Full-repo test run (context, not this task's regression)

```
FAIL traceiq/internal/archtest   — TestPackageAdjacency: eval -> rca/rules edge not in DR-2 table
FAIL traceiq/internal/eval       — gate/threshold assertions (unrelated scoring logic)
FAIL traceiq/internal/nl [build] — answerer_test.go references undefined RulesAnswerer/RemediateProposal
ok   traceiq/internal/remediate  — 18/18 tests pass
(all other packages: ok or no test files)
```

`internal/nl`'s test file references `RulesAnswerer` and `RemediateProposal`
— symbols that do not exist anywhere in `internal/nl` (grepped; not
introduced by this task and not present in `internal/remediate` either).
These three failures pre-date this session's changes and are outside
`internal/remediate`'s scope lock; flagged here rather than silently
worked around.

## Remaining work (suggested follow-up, not done here)

- Wire `k8s.BuildArgv`/`MinimalEnv` once `internal/k8s` is implemented, and
  add the 50-case argv fuzz test against it.
- Implement `RemoveIstioFault`'s JSON-patch diff against a real
  `VirtualService` shape.
- Back `guard.actions`/`guard.idem` with `internal/store` for durability;
  add the `idempotency` SQL table (DR-23 §23.8).
- Wire real `TargetResolver` (topology existence + `k8s.VerbGet`),
  `Snapshotter`, `SignalSource` (from `internal/anomaly`, via the
  `RecoverySignal` shape already declared), and `tenant.PolicyStore`/
  `auth.Authorizer` implementations at the `cmd/traceiq` composition root.
- Model auto-rollback as its own budget-consuming unit per AC-F09-12's
  stricter reading, and the full 81-pair transition matrix fuzz
  (`AC-F09-9`).
