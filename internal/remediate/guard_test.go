package remediate

import (
	"context"
	"errors"
	"testing"
	"time"

	"traceiq/internal/auth"
	"traceiq/internal/k8s"
	"traceiq/internal/model"
	"traceiq/internal/tenant"
)

// fakeFailExecutor implements k8s.Executor and always fails — used to
// exercise the "a failed action still consumes its budget slot" path
// (requirement (e)) without depending on the real kubectl executor path.
type fakeFailExecutor struct{}

func (fakeFailExecutor) Kind() string { return "dryrun" }
func (fakeFailExecutor) Do(ctx context.Context, r k8s.Request) (k8s.Result, error) {
	return k8s.Result{ExitCode: 1}, errors.New("simulated executor failure")
}

// fakePolicyStore is a minimal tenant.PolicyStore.Get-shaped fake — no
// internal/tenant type is modified, only this package's test-local
// satisfier of the PolicyStore interface the Guard declares for itself.
type fakePolicyStore struct{ pol tenant.Policy }

func (f fakePolicyStore) Get(ctx context.Context, tid model.TenantID) (tenant.Policy, error) {
	return f.pol, nil
}

// fakeResolver stamps a deterministic UID/version for any target whose
// name doesn't start with "missing-", and errors (ErrTargetNotResolvable)
// otherwise — enough to exercise §22.3's existence and re-resolution
// checks without a real topology/k8s dependency.
type fakeResolver struct {
	uid     string
	version string
	replace bool // if true, Resolve returns a DIFFERENT UID on the 2nd+ call (simulates target_replaced)
	calls   int
}

func (f *fakeResolver) Resolve(ctx context.Context, tid model.TenantID, t model.ActionTarget) (model.ActionTarget, error) {
	f.calls++
	if len(t.Name) >= 8 && t.Name[:8] == "missing-" {
		return model.ActionTarget{}, ErrTargetNotResolvable
	}
	uid := f.uid
	if f.replace && f.calls > 1 {
		uid = f.uid + "-replaced"
	}
	t.ResolvedUID = uid
	t.ResolvedVersion = f.version
	return t, nil
}

// fakeAuthorizer implements the auth.Authorizer surface this package
// needs (only SeparationOfDuty is exercised by the Guard).
type fakeAuthorizer struct{}

func (fakeAuthorizer) Can(ctx context.Context, s auth.Subject, c auth.Capability, res auth.ResourceRef) error {
	return nil
}
func (fakeAuthorizer) SeparationOfDuty(proposer, approver string) error {
	if proposer == approver {
		return ErrApproverMustDiffer
	}
	return nil
}
func (fakeAuthorizer) Roles(s auth.Subject) []auth.Role { return s.Roles }

func testPolicy(tid model.TenantID) tenant.Policy {
	return tenant.Policy{
		TenantID:           tid,
		NamespaceAllowlist: []string{"checkout"},
		// All five action types and a wildcard covering every Kind/Name
		// under the "checkout" namespace: exercising the fail-closed
		// RemediationAllowlist/TargetAllowlist checks (finding B1/M1) is
		// what TestPropose_EmptyRemediationAllowlistDeniesAll and
		// TestPropose_TargetNotInTargetAllowlistDenied below do
		// specifically; every other test wants these open so it can focus
		// on the behavior it's actually testing.
		RemediationAllowlist: []model.ActionType{
			model.ActionRollbackDeployment,
			model.ActionScaleReplicas,
			model.ActionRestartPod,
			model.ActionToggleFeatureFlag,
			model.ActionRemoveIstioFault,
		},
		TargetAllowlist:         []string{"checkout/*/*"},
		ActionBudgetPerIncident: 2,
		MaxReplicas:             50,
		FeatureFlags:            map[string][]string{"new-checkout": {"true", "false"}},
	}
}

func newTestGuard(t *testing.T, cfg GuardConfig, clock *fakeClock, resolver TargetResolver) Guard {
	t.Helper()
	return NewGuard(Deps{
		Policy:   fakePolicyStore{pol: testPolicy("t1")},
		Resolver: resolver,
		Authz:    fakeAuthorizer{},
		Audit:    NewInMemoryAuditLog(),
		Now:      clock.Now,
		Config:   cfg,
	})
}

type fakeClock struct{ t time.Time }

func (c *fakeClock) Now() time.Time          { return c.t }
func (c *fakeClock) Advance(d time.Duration) { c.t = c.t.Add(d) }

func scaleProposal(tid model.TenantID, incident, name string) model.ActionProposal {
	return model.ActionProposal{
		Tenant:     tid,
		IncidentID: incident,
		Type:       model.ActionScaleReplicas,
		Target: model.ActionTarget{
			Namespace: "checkout",
			Kind:      model.KindDeployment,
			Name:      name,
		},
		Scale: &model.ScaleReplicasSpec{Replicas: 3},
	}
}

// --- (a) malformed / untyped-mismatch proposals are rejected immediately ---

func TestPropose_RejectsMalformedTypedPayload(t *testing.T) {
	clock := &fakeClock{t: time.Now()}
	g := newTestGuard(t, GuardConfig{}, clock, &fakeResolver{uid: "u1", version: "v1"})
	proposer := auth.Subject{ID: "user:alice"}

	cases := []struct {
		name string
		p    model.ActionProposal
	}{
		{"no spec set", model.ActionProposal{Tenant: "t1", IncidentID: "inc1", Type: model.ActionScaleReplicas, Target: model.ActionTarget{Namespace: "checkout", Kind: model.KindDeployment, Name: "checkout"}}},
		{"type/spec mismatch", model.ActionProposal{Tenant: "t1", IncidentID: "inc1", Type: model.ActionScaleReplicas, Target: model.ActionTarget{Namespace: "checkout", Kind: model.KindDeployment, Name: "checkout"}, Restart: &model.RestartPodSpec{}}},
		{"two specs set", model.ActionProposal{Tenant: "t1", IncidentID: "inc1", Type: model.ActionScaleReplicas, Target: model.ActionTarget{Namespace: "checkout", Kind: model.KindDeployment, Name: "checkout"}, Scale: &model.ScaleReplicasSpec{Replicas: 3}, Restart: &model.RestartPodSpec{}}},
		{"out of range replicas", model.ActionProposal{Tenant: "t1", IncidentID: "inc1", Type: model.ActionScaleReplicas, Target: model.ActionTarget{Namespace: "checkout", Kind: model.KindDeployment, Name: "checkout"}, Scale: &model.ScaleReplicasSpec{Replicas: 999}}},
		{"bad namespace (path injection shaped)", model.ActionProposal{Tenant: "t1", IncidentID: "inc1", Type: model.ActionScaleReplicas, Target: model.ActionTarget{Namespace: "checkout;rm -rf", Kind: model.KindDeployment, Name: "checkout"}, Scale: &model.ScaleReplicasSpec{Replicas: 3}}},
		{"slash in name (deleted deployment/checkout form)", model.ActionProposal{Tenant: "t1", IncidentID: "inc1", Type: model.ActionScaleReplicas, Target: model.ActionTarget{Namespace: "checkout", Kind: model.KindDeployment, Name: "deployment/checkout"}, Scale: &model.ScaleReplicasSpec{Replicas: 3}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := g.Propose(context.Background(), "t1", c.p, proposer, "idem-"+c.name)
			if err == nil {
				t.Fatalf("expected ErrInvalidProposal, got nil")
			}
		})
	}
}

func TestPropose_ValidTypedPayloadAccepted(t *testing.T) {
	clock := &fakeClock{t: time.Now()}
	g := newTestGuard(t, GuardConfig{}, clock, &fakeResolver{uid: "u1", version: "v1"})
	proposer := auth.Subject{ID: "user:alice"}
	a, err := g.Propose(context.Background(), "t1", scaleProposal("t1", "inc1", "checkout"), proposer, "idem-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a.State != model.ActionProposed {
		t.Fatalf("want Proposed, got %d", a.State)
	}
	if a.Proposal.RiskTier != 1 {
		t.Fatalf("want RiskTier 1 for ScaleReplicas, got %d", a.Proposal.RiskTier)
	}
}

// --- (b) separation of duty ---

func TestApprove_RejectsSameProposerApprover(t *testing.T) {
	clock := &fakeClock{t: time.Now()}
	g := newTestGuard(t, GuardConfig{}, clock, &fakeResolver{uid: "u1", version: "v1"})
	alice := auth.Subject{ID: "user:alice"}
	a, err := g.Propose(context.Background(), "t1", scaleProposal("t1", "inc1", "checkout"), alice, "idem-1")
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	_, err = g.Approve(context.Background(), "t1", a.ID, alice, ApprovalRequest{})
	if err != ErrApproverMustDiffer {
		t.Fatalf("want ErrApproverMustDiffer, got %v", err)
	}
}

func TestApprove_DifferentApproverSucceeds(t *testing.T) {
	clock := &fakeClock{t: time.Now()}
	g := newTestGuard(t, GuardConfig{}, clock, &fakeResolver{uid: "u1", version: "v1"})
	alice := auth.Subject{ID: "user:alice"}
	bob := auth.Subject{ID: "user:bob"}
	a, err := g.Propose(context.Background(), "t1", scaleProposal("t1", "inc1", "checkout"), alice, "idem-1")
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	approved, err := g.Approve(context.Background(), "t1", a.ID, bob, ApprovalRequest{})
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if approved.State != model.ActionApproved {
		t.Fatalf("want Approved, got %d", approved.State)
	}
}

// --- (c) TTL expiry moves to Expired, cannot then execute ---

func TestExecute_ExpiredApprovalMovesToExpiredAndRefusesExecute(t *testing.T) {
	clock := &fakeClock{t: time.Now()}
	g := newTestGuard(t, GuardConfig{ApprovalTTL: 15 * time.Minute}, clock, &fakeResolver{uid: "u1", version: "v1"})
	alice := auth.Subject{ID: "user:alice"}
	bob := auth.Subject{ID: "user:bob"}
	a, err := g.Propose(context.Background(), "t1", scaleProposal("t1", "inc1", "checkout"), alice, "idem-1")
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	if _, err := g.Approve(context.Background(), "t1", a.ID, bob, ApprovalRequest{}); err != nil {
		t.Fatalf("approve: %v", err)
	}

	clock.Advance(16 * time.Minute) // past the 15m approval_ttl

	got, err := g.Execute(context.Background(), "t1", a.ID, bob, "exec-1")
	if err != ErrActionExpired {
		t.Fatalf("want ErrActionExpired, got %v", err)
	}
	if got.State != model.ActionExpired {
		t.Fatalf("want Expired, got %d", got.State)
	}

	// A second Execute call must still refuse (illegal transition from Expired).
	if _, err := g.Execute(context.Background(), "t1", a.ID, bob, "exec-2"); err == nil {
		t.Fatalf("expected an error executing an Expired action")
	}
}

// --- (d) auto_execute defaults false: approved-but-not-executed action does not run itself ---

func TestApprove_AutoExecuteDefaultsFalse(t *testing.T) {
	clock := &fakeClock{t: time.Now()}
	// Even with the global flag true, tenant policy AutoExecuteAllowed is
	// false by zero value, so AutoExecute must still end up false.
	g := NewGuard(Deps{
		Policy:   fakePolicyStore{pol: testPolicy("t1")}, // AutoExecuteAllowed: false (zero value)
		Resolver: &fakeResolver{uid: "u1", version: "v1"},
		Authz:    fakeAuthorizer{},
		Audit:    NewInMemoryAuditLog(),
		Now:      clock.Now,
		Config:   GuardConfig{AutoExecuteOnApprove: true},
	})
	alice := auth.Subject{ID: "user:alice"}
	bob := auth.Subject{ID: "user:bob"}
	a, err := g.Propose(context.Background(), "t1", scaleProposal("t1", "inc1", "checkout"), alice, "idem-1")
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	approved, err := g.Approve(context.Background(), "t1", a.ID, bob, ApprovalRequest{})
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if approved.AutoExecute {
		t.Fatalf("AutoExecute must be false: global*tenant AND, tenant is false")
	}
	// The action must still be sitting in Approved, not Executing/Succeeded —
	// nothing about Approve() itself may run the action.
	if approved.State != model.ActionApproved {
		t.Fatalf("want Approved (no self-execution), got state %d", approved.State)
	}
	still, err := g.Get(context.Background(), "t1", a.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if still.State != model.ActionApproved {
		t.Fatalf("action must remain Approved until a separate Execute call; got %d", still.State)
	}
}

func TestApprove_RiskTier3ForcesAutoExecuteFalseRegardlessOfConfig(t *testing.T) {
	clock := &fakeClock{t: time.Now()}
	pol := testPolicy("t1")
	pol.AutoExecuteAllowed = true
	g := NewGuard(Deps{
		Policy:   fakePolicyStore{pol: pol},
		Resolver: &fakeResolver{uid: "u1", version: "v1"},
		Authz:    fakeAuthorizer{},
		Audit:    NewInMemoryAuditLog(),
		Now:      clock.Now,
		Config:   GuardConfig{AutoExecuteOnApprove: true},
	})
	alice := auth.Subject{ID: "user:alice"}
	bob := auth.Subject{ID: "user:bob"}
	p := model.ActionProposal{
		Tenant: "t1", IncidentID: "inc1", Type: model.ActionRemoveIstioFault,
		Target:     model.ActionTarget{Namespace: "checkout", Kind: model.KindVirtualService, Name: "checkout"},
		IstioFault: &model.RemoveIstioFaultSpec{},
	}
	a, err := g.Propose(context.Background(), "t1", p, alice, "idem-1")
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	approved, err := g.Approve(context.Background(), "t1", a.ID, bob, ApprovalRequest{})
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if approved.AutoExecute {
		t.Fatalf("RiskTier 3 must force AutoExecute=false regardless of config")
	}
}

// --- (e) budget counts a failed/rolled-back action against the cap ---

func TestBudget_CountsFailedActionAgainstCap(t *testing.T) {
	clock := &fakeClock{t: time.Now()}
	pol := testPolicy("t1")
	pol.ActionBudgetPerIncident = 1
	g := NewGuard(Deps{
		Policy:    fakePolicyStore{pol: pol},
		Resolver:  &fakeResolver{uid: "u1", version: "v1"},
		Authz:     fakeAuthorizer{},
		Audit:     NewInMemoryAuditLog(),
		Executors: map[string]k8s.Executor{"dryrun": fakeFailExecutor{}},
		Now:       clock.Now,
	})
	alice := auth.Subject{ID: "user:alice"}
	bob := auth.Subject{ID: "user:bob"}

	a1, err := g.Propose(context.Background(), "t1", scaleProposal("t1", "inc1", "checkout"), alice, "p1")
	if err != nil {
		t.Fatalf("propose1: %v", err)
	}
	if _, err := g.Approve(context.Background(), "t1", a1.ID, bob, ApprovalRequest{}); err != nil {
		t.Fatalf("approve1: %v", err)
	}
	got, err := g.Execute(context.Background(), "t1", a1.ID, bob, "e1")
	if err == nil {
		t.Fatalf("expected the fake executor to fail this action")
	}
	if got.State != model.ActionFailed {
		t.Fatalf("want Failed, got %d", got.State)
	}

	// Budget is now exhausted (cap=1, one Failed action already counts):
	// a second Propose on the same incident must be refused.
	_, err = g.Propose(context.Background(), "t1", scaleProposal("t1", "inc1", "checkout2"), alice, "p2")
	if err != ErrBudgetExceeded {
		t.Fatalf("want ErrBudgetExceeded (failed action still counts against budget), got %v", err)
	}
}

// --- target re-resolution: unresolvable target refused before allowlist ---

func TestPropose_UnresolvableTargetRefused(t *testing.T) {
	clock := &fakeClock{t: time.Now()}
	g := newTestGuard(t, GuardConfig{}, clock, &fakeResolver{uid: "u1", version: "v1"})
	alice := auth.Subject{ID: "user:alice"}
	_, err := g.Propose(context.Background(), "t1", scaleProposal("t1", "inc1", "missing-svc"), alice, "idem-1")
	if err == nil {
		t.Fatalf("expected ErrTargetNotResolvable")
	}
}

// --- execute re-resolution: changed UID -> target_replaced, Failed ---

func TestExecute_TargetReplacedFailsAction(t *testing.T) {
	clock := &fakeClock{t: time.Now()}
	resolver := &fakeResolver{uid: "u1", version: "v1", replace: true}
	g := newTestGuard(t, GuardConfig{}, clock, resolver)
	alice := auth.Subject{ID: "user:alice"}
	bob := auth.Subject{ID: "user:bob"}
	a, err := g.Propose(context.Background(), "t1", scaleProposal("t1", "inc1", "checkout"), alice, "idem-1")
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	if _, err := g.Approve(context.Background(), "t1", a.ID, bob, ApprovalRequest{}); err != nil {
		t.Fatalf("approve: %v", err)
	}
	got, err := g.Execute(context.Background(), "t1", a.ID, bob, "e1")
	if err != ErrTargetReplaced {
		t.Fatalf("want ErrTargetReplaced, got %v", err)
	}
	if got.State != model.ActionFailed {
		t.Fatalf("want Failed, got %d", got.State)
	}
}

// --- review findings (docs/reports/w14-review-remediate.md) regression tests ---

// mutablePolicyStore lets a test change the policy a Guard sees between two
// calls (e.g. simulate an admin editing the allowlist/budget between
// Propose and Execute, or between two Approve calls), which
// fakePolicyStore's plain by-value struct cannot do.
type mutablePolicyStore struct{ pol tenant.Policy }

func (m *mutablePolicyStore) Get(ctx context.Context, tid model.TenantID) (tenant.Policy, error) {
	return m.pol, nil
}

// fakeSnapshotter returns a canned Snapshot, or a configurable error.
type fakeSnapshotter struct {
	snap model.Snapshot
	err  error
}

func (f fakeSnapshotter) Snapshot(ctx context.Context, tid model.TenantID, target model.ActionTarget) (model.Snapshot, error) {
	return f.snap, f.err
}

// --- finding B1: an empty/unconfigured RemediationAllowlist must deny
// every action type (fail-closed), not allow every type. ---

func TestPropose_EmptyRemediationAllowlistDeniesAll(t *testing.T) {
	clock := &fakeClock{t: time.Now()}
	pol := testPolicy("t1")
	pol.RemediationAllowlist = nil // the documented zero-config value (`remediate.allowlist: []`)
	g := NewGuard(Deps{
		Policy:   fakePolicyStore{pol: pol},
		Resolver: &fakeResolver{uid: "u1", version: "v1"},
		Authz:    fakeAuthorizer{},
		Audit:    NewInMemoryAuditLog(),
		Now:      clock.Now,
	})
	alice := auth.Subject{ID: "user:alice"}
	_, err := g.Propose(context.Background(), "t1", scaleProposal("t1", "inc1", "checkout"), alice, "idem-1")
	if err == nil {
		t.Fatalf("expected Propose to be refused with an empty RemediationAllowlist, got nil error")
	}
	if !errors.Is(err, ErrInvalidProposal) {
		t.Fatalf("want ErrInvalidProposal, got %v", err)
	}
}

// --- finding M1: tenant.Policy.TargetAllowlist glob rules must actually
// gate the specific target, not just Type/Namespace. ---

func TestPropose_TargetNotInTargetAllowlistDenied(t *testing.T) {
	clock := &fakeClock{t: time.Now()}
	pol := testPolicy("t1")
	pol.TargetAllowlist = []string{"checkout/Deployment/checkout-svc-*"} // does not match "checkout" below
	g := NewGuard(Deps{
		Policy:   fakePolicyStore{pol: pol},
		Resolver: &fakeResolver{uid: "u1", version: "v1"},
		Authz:    fakeAuthorizer{},
		Audit:    NewInMemoryAuditLog(),
		Now:      clock.Now,
	})
	alice := auth.Subject{ID: "user:alice"}
	_, err := g.Propose(context.Background(), "t1", scaleProposal("t1", "inc1", "checkout"), alice, "idem-1")
	if !errors.Is(err, ErrInvalidProposal) {
		t.Fatalf("want ErrInvalidProposal (target not in TargetAllowlist), got %v", err)
	}

	// A matching glob must be accepted.
	pol.TargetAllowlist = []string{"checkout/Deployment/check*"}
	g2 := NewGuard(Deps{
		Policy:   fakePolicyStore{pol: pol},
		Resolver: &fakeResolver{uid: "u1", version: "v1"},
		Authz:    fakeAuthorizer{},
		Audit:    NewInMemoryAuditLog(),
		Now:      clock.Now,
	})
	if _, err := g2.Propose(context.Background(), "t1", scaleProposal("t1", "inc1", "checkout"), alice, "idem-2"); err != nil {
		t.Fatalf("expected a matching TargetAllowlist glob to be accepted, got %v", err)
	}
}

// --- finding B2: a snapshot-read error at Execute must abort BEFORE the
// mutating call, leave the action in Approved (not Executing), and audit
// snapshot_failed — never silently proceed without a confirmed snapshot. ---

func TestExecute_SnapshotErrorAbortsAndLeavesActionApproved(t *testing.T) {
	clock := &fakeClock{t: time.Now()}
	auditLog := NewInMemoryAuditLog()
	g := NewGuard(Deps{
		Policy:    fakePolicyStore{pol: testPolicy("t1")},
		Resolver:  &fakeResolver{uid: "u1", version: "v1"},
		Snapshot:  fakeSnapshotter{err: errors.New("simulated snapshot read failure")},
		Authz:     fakeAuthorizer{},
		Audit:     auditLog,
		Executors: map[string]k8s.Executor{"dryrun": fakeFailExecutor{}}, // must never be reached
		Now:       clock.Now,
	})
	alice := auth.Subject{ID: "user:alice"}
	bob := auth.Subject{ID: "user:bob"}
	a, err := g.Propose(context.Background(), "t1", scaleProposal("t1", "inc1", "checkout"), alice, "idem-1")
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	if _, err := g.Approve(context.Background(), "t1", a.ID, bob, ApprovalRequest{}); err != nil {
		t.Fatalf("approve: %v", err)
	}
	got, err := g.Execute(context.Background(), "t1", a.ID, bob, "e1")
	if err == nil {
		t.Fatalf("expected Execute to abort on a snapshot read error")
	}
	if got.State != model.ActionApproved {
		t.Fatalf("want the action to remain Approved (never Executing) after a snapshot failure, got state %d", got.State)
	}
	events, _, qerr := auditLog.Query(context.Background(), "t1", auth.Filter{Action: "snapshot_failed"})
	if qerr != nil {
		t.Fatalf("query: %v", qerr)
	}
	if len(events) != 1 {
		t.Fatalf("want exactly one snapshot_failed audit event, got %d", len(events))
	}
}

// --- finding M4: Execute must re-evaluate NamespaceAllowlist/
// TargetAllowlist AFTER re-resolution, so a policy change between
// Propose and Execute (e.g. the namespace revoked from the allowlist)
// actually blocks an already-approved action. ---

func TestExecute_RefusesWhenAllowlistRevokedAfterApproval(t *testing.T) {
	clock := &fakeClock{t: time.Now()}
	store := &mutablePolicyStore{pol: testPolicy("t1")}
	g := NewGuard(Deps{
		Policy:   store,
		Resolver: &fakeResolver{uid: "u1", version: "v1"},
		Authz:    fakeAuthorizer{},
		Audit:    NewInMemoryAuditLog(),
		Now:      clock.Now,
	})
	alice := auth.Subject{ID: "user:alice"}
	bob := auth.Subject{ID: "user:bob"}
	a, err := g.Propose(context.Background(), "t1", scaleProposal("t1", "inc1", "checkout"), alice, "idem-1")
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	if _, err := g.Approve(context.Background(), "t1", a.ID, bob, ApprovalRequest{}); err != nil {
		t.Fatalf("approve: %v", err)
	}

	// Simulate an admin revoking the namespace from the allowlist between
	// approval and execution.
	store.pol.NamespaceAllowlist = nil

	got, err := g.Execute(context.Background(), "t1", a.ID, bob, "e1")
	if err == nil {
		t.Fatalf("expected Execute to refuse once the namespace allowlist no longer covers the target")
	}
	if got.State != model.ActionFailed {
		t.Fatalf("want Failed, got %d", got.State)
	}
}

// --- finding M3: Approve's budget-exceeded refusal (no override
// requested) must transition the action and write an audit event, not
// silently return an error with no trace. ---

func TestApprove_BudgetExceededTransitionsToRejectedAndAudits(t *testing.T) {
	clock := &fakeClock{t: time.Now()}
	store := &mutablePolicyStore{pol: testPolicy("t1")}
	store.pol.ActionBudgetPerIncident = 2
	auditLog := NewInMemoryAuditLog()
	g := NewGuard(Deps{
		Policy:   store,
		Resolver: &fakeResolver{uid: "u1", version: "v1"},
		Authz:    fakeAuthorizer{},
		Audit:    auditLog,
		Now:      clock.Now,
	})
	alice := auth.Subject{ID: "user:alice"}
	bob := auth.Subject{ID: "user:bob"}

	a1, err := g.Propose(context.Background(), "t1", scaleProposal("t1", "inc1", "checkout"), alice, "p1")
	if err != nil {
		t.Fatalf("propose1: %v", err)
	}
	a2, err := g.Propose(context.Background(), "t1", scaleProposal("t1", "inc1", "checkout2"), alice, "p2")
	if err != nil {
		t.Fatalf("propose2: %v", err)
	}

	// Simulate the budget being tightened (e.g. by an admin) after both
	// actions were already proposed, so Approve's own guard — not
	// Propose's — is what has to catch it.
	store.pol.ActionBudgetPerIncident = 1

	got, err := g.Approve(context.Background(), "t1", a1.ID, bob, ApprovalRequest{})
	if !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("want ErrBudgetExceeded, got %v", err)
	}
	if got.State != model.ActionRejected {
		t.Fatalf("want Rejected, got %d", got.State)
	}
	events, _, qerr := auditLog.Query(context.Background(), "t1", auth.Filter{Action: "approve_refused_budget_exceeded"})
	if qerr != nil {
		t.Fatalf("query: %v", qerr)
	}
	if len(events) != 1 {
		t.Fatalf("want exactly one approve_refused_budget_exceeded audit event, got %d", len(events))
	}
	_ = a2
}
