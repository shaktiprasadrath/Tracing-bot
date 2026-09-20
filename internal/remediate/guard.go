package remediate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"traceiq/internal/auth"
	"traceiq/internal/k8s"
	"traceiq/internal/model"
	"traceiq/internal/tenant"
)

// TargetResolver is remediate's consumer-declared seam onto DR-22 §22.3's
// target re-resolution (topology existence + live existence + UID/version
// stamping), following this codebase's own pattern of small
// consumer-declared interfaces (e.g. topology.EdgeSink/EdgeSource) so this
// package never has to import a concrete topology or k8s client type to
// express what it needs. Resolve returns the target with ResolvedUID and
// ResolvedVersion stamped, or ErrTargetNotResolvable.
type TargetResolver interface {
	Resolve(ctx context.Context, tid model.TenantID, target model.ActionTarget) (model.ActionTarget, error)
}

// Snapshotter takes DR-23 §23.9's pre-mutation snapshot. Declared here for
// the same reason as TargetResolver.
type Snapshotter interface {
	Snapshot(ctx context.Context, tid model.TenantID, target model.ActionTarget) (model.Snapshot, error)
}

// PolicyStore narrows tenant.PolicyStore to the single method the Guard
// needs (Get), satisfied structurally by any tenant.PolicyStore.
type PolicyStore interface {
	Get(ctx context.Context, tid model.TenantID) (tenant.Policy, error)
}

// SignalSource produces the RecoverySignal Verify checks an action's
// post-execution outcome against. Declared here so remediate never imports
// anomaly (DR-2's cycle-break table, restated verbatim from
// remediate.go's existing doc comment).
type SignalSource interface {
	Signal(ctx context.Context, tid model.TenantID, a model.Action) (RecoverySignal, error)
}

// Deps wires the Guard's collaborators. Every field is an interface this
// package declares itself (or, for Authz/Audit, one internal/auth already
// declares) — remediate never imports a concrete implementation of any of
// them, per DR-2's adjacency table.
type Deps struct {
	Policy    PolicyStore
	Resolver  TargetResolver
	Snapshot  Snapshotter
	Verifier  Verifier
	Signals   SignalSource
	Authz     auth.Authorizer // SeparationOfDuty; nil => Guard's own proposer!=approver fallback
	Audit     auth.AuditSink
	Executors map[string]k8s.Executor // "dryrun" | "kubectl"
	Now       func() time.Time
	Config    GuardConfig
}

// GuardConfig is DR-23 §23.10's remediate.* config block, the subset the
// Guard consults directly (fields not yet wired, e.g. idempotency TTL
// beyond "required", are called out in docs/reports/w13-remediate.md).
type GuardConfig struct {
	Mode                 string // "dryrun" | "execute"
	DefaultExecutor      string // "dryrun" | "kubectl"
	ApprovalTTL          time.Duration
	ProposalTTL          time.Duration
	VerifyWindow         time.Duration
	AutoExecuteOnApprove bool // global default; DEFAULT FALSE (DR-23 §23.5)
	DefaultActionBudget  int  // fallback when tenant.Policy.ActionBudgetPerIncident is 0
	MaxBudgetOverrides   int
}

func (c GuardConfig) withDefaults() GuardConfig {
	if c.ApprovalTTL == 0 {
		c.ApprovalTTL = 15 * time.Minute
	}
	if c.ProposalTTL == 0 {
		c.ProposalTTL = 60 * time.Minute
	}
	if c.VerifyWindow == 0 {
		c.VerifyWindow = 10 * time.Minute
	}
	if c.DefaultActionBudget == 0 {
		c.DefaultActionBudget = 2
	}
	if c.MaxBudgetOverrides == 0 {
		c.MaxBudgetOverrides = 1
	}
	if c.DefaultExecutor == "" {
		c.DefaultExecutor = "dryrun"
	}
	// AutoExecuteOnApprove intentionally left as given: its zero value
	// (false) IS the DR-23 §23.5 default, so there is nothing to default.
	return c
}

type idemRecord struct {
	hash   string
	action model.Action
}

// guard is the Guard interface's implementation: an in-memory control
// plane (no store/sqlite dependency — out of this task's scope; see the
// report) that nonetheless enforces every state-machine, separation-of-
// duty, expiry, and budget rule DR-23 specifies.
type guard struct {
	mu      sync.Mutex
	deps    Deps
	cfg     GuardConfig
	actions map[string]*model.Action // keyed by Action.ID
	idem    map[string]idemRecord    // keyed by tenant|endpoint|key
	seq     int
}

func NewGuard(deps Deps) Guard {
	if deps.Now == nil {
		deps.Now = time.Now
	}
	if deps.Audit == nil {
		deps.Audit = NewInMemoryAuditLog()
	}
	if deps.Executors == nil {
		deps.Executors = map[string]k8s.Executor{}
	}
	if _, ok := deps.Executors["dryrun"]; !ok {
		deps.Executors["dryrun"] = DryRunExecutor{}
	}
	return &guard{
		deps:    deps,
		cfg:     deps.Config.withDefaults(),
		actions: map[string]*model.Action{},
		idem:    map[string]idemRecord{},
	}
}

func (g *guard) audit(ctx context.Context, tid model.TenantID, actor, action string, payload any) {
	b, _ := json.Marshal(payload)
	_, _ = g.deps.Audit.Append(ctx, auth.Event{
		Tenant: tid, Actor: actor, Action: action, Payload: b, Timestamp: g.deps.Now(),
	})
}

func nextID(seq int) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("action-%d-%d", seq, time.Now().UnixNano())))
	return "act_" + hex.EncodeToString(sum[:])[:16]
}

func requestHash(v any) string {
	b, _ := json.Marshal(v)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// checkIdempotency returns (cached action, true) on an identical replay,
// records a fresh key on first use, and returns ErrIdempotencyReused on a
// key reused with a different payload (DR-23 §23.8).
func (g *guard) checkIdempotency(tid model.TenantID, endpoint, key string, req any) (model.Action, bool, error) {
	if key == "" {
		return model.Action{}, false, ErrIdempotencyRequired
	}
	h := requestHash(req)
	k := string(tid) + "|" + endpoint + "|" + key
	if rec, ok := g.idem[k]; ok {
		if rec.hash != h {
			return model.Action{}, false, ErrIdempotencyReused
		}
		return rec.action, true, nil
	}
	return model.Action{}, false, nil
}

func (g *guard) storeIdempotency(tid model.TenantID, endpoint, key string, req any, a model.Action) {
	if key == "" {
		return
	}
	k := string(tid) + "|" + endpoint + "|" + key
	g.idem[k] = idemRecord{hash: requestHash(req), action: a}
}

// budgetUsed is DR-23 §23.6, verbatim: count(actions WHERE incident_id=?
// AND state NOT IN (Rejected, Expired)).
func (g *guard) budgetUsed(tid model.TenantID, incidentID string) int {
	n := 0
	for _, a := range g.actions {
		if a.Tenant != tid || a.IncidentID != incidentID {
			continue
		}
		if a.State == model.ActionRejected || a.State == model.ActionExpired {
			continue
		}
		n++
	}
	return n
}

func (g *guard) Propose(ctx context.Context, tid model.TenantID, p model.ActionProposal, by auth.Subject, idem string) (model.Action, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if cached, hit, err := g.checkIdempotency(tid, "propose", idem, p); err != nil {
		return model.Action{}, err
	} else if hit {
		return cached, nil
	}

	pol, err := g.policy(ctx, tid)
	if err != nil {
		return model.Action{}, err
	}

	if err := validateProposal(p, pol); err != nil {
		return model.Action{}, err
	}

	// DR-22 §22.3: re-resolution runs before the allowlist check.
	resolved := p.Target
	if g.deps.Resolver != nil {
		resolved, err = g.deps.Resolver.Resolve(ctx, tid, p.Target)
		if err != nil {
			g.audit(ctx, tid, by.ID, "propose_refused_target_not_resolvable", map[string]any{"target": p.Target})
			return model.Action{}, fmt.Errorf("%w: %v", ErrTargetNotResolvable, err)
		}
	}
	p.Target = resolved

	if err := checkAllowlists(p, pol); err != nil {
		return model.Action{}, err
	}

	budgetCap := pol.ActionBudgetPerIncident
	if budgetCap == 0 {
		budgetCap = g.cfg.DefaultActionBudget
	}
	if g.budgetUsed(tid, p.IncidentID) >= budgetCap {
		g.audit(ctx, tid, by.ID, "propose_refused_budget_exceeded", map[string]any{"incident_id": p.IncidentID})
		return model.Action{}, ErrBudgetExceeded
	}

	tier, _ := riskTier(p.Type)
	p.RiskTier = tier // proposer-supplied value is always ignored (§22.4)

	now := g.deps.Now()
	p.ProposedAt = now
	if p.ProposedBy == "" {
		p.ProposedBy = by.ID
	}
	p.ID = nextID(g.seq)
	g.seq++

	a := &model.Action{
		ID:              p.ID,
		Tenant:          tid,
		IncidentID:      p.IncidentID,
		InvestigationID: p.InvestigationID,
		Proposal:        p,
		State:           model.ActionProposed,
		ProposedBy:      p.ProposedBy,
		ExecutorKind:    g.cfg.DefaultExecutor,
		BudgetIndex:     g.budgetUsed(tid, p.IncidentID) + 1,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	g.actions[a.ID] = a
	g.audit(ctx, tid, by.ID, "action_proposed", map[string]any{"action_id": a.ID, "type": p.Type, "risk_tier": tier})

	out := *a
	g.storeIdempotency(tid, "propose", idem, p, out)
	return out, nil
}

func (g *guard) sepOfDuty(proposer, approver string) error {
	if g.deps.Authz != nil {
		return g.deps.Authz.SeparationOfDuty(proposer, approver)
	}
	if proposer == approver {
		return ErrApproverMustDiffer
	}
	return nil
}

func (g *guard) Approve(ctx context.Context, tid model.TenantID, id string, by auth.Subject, req ApprovalRequest) (model.Action, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	a, err := g.get(tid, id)
	if err != nil {
		return model.Action{}, err
	}

	// NOTE: Guard.Approve's signature carries no Idempotency-Key parameter
	// (unlike Propose/Execute), so DR-23 §23.8's idempotency table is not
	// enforced here in this pass; see docs/reports/w13-remediate.md.
	now := g.deps.Now()
	if a.State == model.ActionProposed && !a.CreatedAt.Add(g.cfg.ProposalTTL).After(now) {
		a.State = model.ActionExpired
		a.UpdatedAt = now
		g.audit(ctx, tid, by.ID, "action_expired_proposal_ttl", map[string]any{"action_id": id})
		return *a, ErrActionExpired
	}
	if a.State != model.ActionProposed {
		return model.Action{}, fmt.Errorf("%w: cannot Approve from state %d", ErrIllegalTransition, a.State)
	}

	// DR-23 §23.4: separation of duty. When the proposer is the engine
	// ("rca:<investigationID>"), any human approver satisfies the check —
	// that only holds structurally if by.ID != that literal string, which
	// is guaranteed since auth.Subject.ID for a human is never
	// "rca:"-prefixed.
	if err := g.sepOfDuty(a.ProposedBy, by.ID); err != nil {
		g.audit(ctx, tid, by.ID, "approve_refused_approver_must_differ", map[string]any{"action_id": id})
		return model.Action{}, ErrApproverMustDiffer
	}

	pol, err := g.policy(ctx, tid)
	if err != nil {
		return model.Action{}, err
	}

	if req.RequestBudgetOverride {
		if len(req.OverrideJustification) < 40 {
			return model.Action{}, ErrOverrideJustTooShort
		}
		used := 0
		for _, x := range g.actions {
			if x.Tenant == tid && x.IncidentID == a.IncidentID && x.OverrideUsed {
				used++
			}
		}
		max := g.cfg.MaxBudgetOverrides
		if used >= max {
			return model.Action{}, ErrOverrideLimit
		}
		a.OverrideUsed = true
		a.OverrideJustification = req.OverrideJustification
		a.OverriddenBy = by.ID
		a.OverriddenAt = now
		g.audit(ctx, tid, by.ID, "budget_override", map[string]any{"action_id": id, "incident_id": a.IncidentID})
	} else {
		budgetCap := pol.ActionBudgetPerIncident
		if budgetCap == 0 {
			budgetCap = g.cfg.DefaultActionBudget
		}
		if g.budgetUsed(tid, a.IncidentID) > budgetCap {
			// DR-23 §23.6 pseudocode: "a.State = Rejected;
			// Audit.Append(budget_exceeded, a)". The previous version of
			// this branch returned the sentinel error with NEITHER a state
			// transition NOR an audit event — a silent refusal with no
			// trace in the tamper-evident log this package's whole selling
			// point rests on. Fixed per
			// docs/reports/w14-review-remediate.md, finding M3.
			a.State = model.ActionRejected
			a.StateReason = "budget_exceeded"
			a.UpdatedAt = now
			g.audit(ctx, tid, by.ID, "approve_refused_budget_exceeded", map[string]any{"action_id": id, "incident_id": a.IncidentID})
			return *a, ErrBudgetExceeded
		}
	}

	// DR-23 §23.5: auto_execute_on_approve.
	auto := g.cfg.AutoExecuteOnApprove && pol.AutoExecuteAllowed
	if a.Proposal.RiskTier == 3 {
		auto = false // forbidden at RiskTier 3 regardless of config
	}

	a.State = model.ActionApproved
	a.ApprovedBy = by.ID
	a.ApprovedAt = now
	a.ExpiresAt = now.Add(g.cfg.ApprovalTTL)
	a.AutoExecute = auto
	a.UpdatedAt = now
	g.audit(ctx, tid, by.ID, "action_approved", map[string]any{"action_id": id, "expires_at": a.ExpiresAt})

	out := *a
	// NOTE (DR-23 §23.5): "Approve() no longer tail-calls Execute(id)."
	// auto_execute_on_approve is recorded on the Action for a caller (e.g.
	// the HTTP handler layer, out of this package's scope) to act on; the
	// Guard itself never self-invokes Execute here — this is what
	// requirement (d) tests.
	return out, nil
}

func (g *guard) Reject(ctx context.Context, tid model.TenantID, id string, by auth.Subject, reason string) (model.Action, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	a, err := g.get(tid, id)
	if err != nil {
		return model.Action{}, err
	}
	if a.State != model.ActionProposed && a.State != model.ActionApproved {
		return model.Action{}, fmt.Errorf("%w: cannot Reject from state %d", ErrIllegalTransition, a.State)
	}
	a.State = model.ActionRejected
	a.StateReason = reason
	a.UpdatedAt = g.deps.Now()
	g.audit(ctx, tid, by.ID, "action_rejected", map[string]any{"action_id": id, "reason": reason})
	return *a, nil
}

func (g *guard) Execute(ctx context.Context, tid model.TenantID, id string, by auth.Subject, idem string) (model.Action, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	a, err := g.get(tid, id)
	if err != nil {
		return model.Action{}, err
	}

	if cached, hit, err := g.checkIdempotency(tid, "execute", idem, id); err != nil {
		return model.Action{}, err
	} else if hit {
		return cached, nil
	}

	now := g.deps.Now()

	// DR-23 §23.3: expiry is checked in the SAME critical section as the
	// compare-and-set from Approved->Executing (the mutex around the whole
	// method IS that transaction boundary here). Zero executor calls occur
	// before this check.
	if a.State == model.ActionApproved && !a.ExpiresAt.After(now) {
		a.State = model.ActionExpired
		a.UpdatedAt = now
		g.audit(ctx, tid, by.ID, "action_expired_approval_ttl", map[string]any{"action_id": id})
		return *a, ErrActionExpired
	}
	if a.State != model.ActionApproved {
		return model.Action{}, fmt.Errorf("%w: cannot Execute from state %d", ErrIllegalTransition, a.State)
	}

	// Re-resolution at execute (§22.3 step 3): an unchanged ResolvedUID is
	// required.
	if g.deps.Resolver != nil {
		reresolved, err := g.deps.Resolver.Resolve(ctx, tid, a.Proposal.Target)
		if err != nil {
			a.State = model.ActionFailed
			a.StateReason = "target_not_resolvable"
			a.UpdatedAt = now
			g.audit(ctx, tid, by.ID, "execute_failed_target_not_resolvable", map[string]any{"action_id": id})
			return *a, fmt.Errorf("%w: %v", ErrTargetNotResolvable, err)
		}
		if a.Proposal.Target.ResolvedUID != "" && reresolved.ResolvedUID != a.Proposal.Target.ResolvedUID {
			a.State = model.ActionFailed
			a.StateReason = "target_replaced"
			a.UpdatedAt = now
			g.audit(ctx, tid, by.ID, "execute_failed_target_replaced", map[string]any{"action_id": id})
			return *a, ErrTargetReplaced
		}
		a.Proposal.Target = reresolved
	}

	// FR-F09-6 step 2 / DR-22 §22.3 step 4: allowlists are evaluated again
	// here, AFTER re-resolution, inside the same critical section as the
	// Approved->Executing transition below. Propose() checked them once
	// against the policy at proposal time; without this re-check here, a
	// tenant policy change between Propose and Execute (a namespace or
	// target removed from its allowlist, or the whole allowlist emptied)
	// would have zero effect on an already-approved action. Fixed per
	// docs/reports/w14-review-remediate.md, finding M4.
	if pol, perr := g.policy(ctx, tid); perr == nil {
		if err := checkAllowlists(a.Proposal, pol); err != nil {
			a.State = model.ActionFailed
			a.StateReason = "allowlist_revoked"
			a.UpdatedAt = now
			g.audit(ctx, tid, by.ID, "execute_failed_allowlist_revoked", map[string]any{"action_id": id})
			return *a, err
		}
	}

	// FR-F09-6 step 3: "call the snapshot read ... before any mutating
	// call. If the snapshot read errors, Execute() MUST abort, leave the
	// action in state Approved (not Executing), and emit audit event
	// snapshot_failed." The previous version of this block silently
	// swallowed a Snapshot error (`if err == nil { ... }`) and fell through
	// to Executing regardless — meaning an action could reach the mutating
	// executor call with no confirmed pre-snapshot at all, and Rollback's
	// concurrent-modification check (§23.9) is skipped whenever
	// PreSnapshot.ResourceVersion is empty, silently defeating that safety
	// net too. Fixed per docs/reports/w14-review-remediate.md, finding B2
	// (fail-open snapshot handling in a security-critical mutation path).
	if g.deps.Snapshot != nil {
		snap, err := g.deps.Snapshot.Snapshot(ctx, tid, a.Proposal.Target)
		if err != nil {
			a.UpdatedAt = now // state stays Approved, NOT Executing
			g.audit(ctx, tid, by.ID, "snapshot_failed", map[string]any{"action_id": id, "error": err.Error()})
			return *a, fmt.Errorf("remediate: snapshot_failed: %w", err)
		}
		a.PreSnapshot = snap
	}

	a.State = model.ActionExecuting
	a.UpdatedAt = now
	g.audit(ctx, tid, by.ID, "action_executing", map[string]any{"action_id": id, "executor": a.ExecutorKind})

	exec, ok := g.deps.Executors[a.ExecutorKind]
	if !ok {
		a.State = model.ActionFailed
		a.StateReason = "unknown_executor"
		a.UpdatedAt = g.deps.Now()
		return *a, ErrUnknownExecutor
	}

	verb, verr := verbForAction(a.Proposal.Type)
	if verr != nil {
		a.State = model.ActionFailed
		a.StateReason = verr.Error()
		a.UpdatedAt = g.deps.Now()
		return *a, verr
	}
	stdin, serr := stdinPayload(*a)
	if serr != nil {
		a.State = model.ActionFailed
		a.StateReason = serr.Error()
		a.UpdatedAt = g.deps.Now()
		g.audit(ctx, tid, by.ID, "action_failed", map[string]any{"action_id": id, "reason": a.StateReason})
		return *a, serr
	}

	// FR-F09-17 / DR-22 §22.2: refuse a computed payload that touches
	// anything outside the per-type allowed path set, before it ever
	// reaches the executor. Fixed per
	// docs/reports/w14-review-remediate.md, finding M2 (this check was
	// declared, via ErrPayloadOutOfScope, but never called anywhere).
	if scopeErr := checkPayloadScope(a.Proposal, stdin); scopeErr != nil {
		a.State = model.ActionFailed
		a.StateReason = scopeErr.Error()
		a.UpdatedAt = g.deps.Now()
		g.audit(ctx, tid, by.ID, "action_failed_payload_out_of_scope", map[string]any{"action_id": id})
		return *a, scopeErr
	}

	req := k8s.Request{Verb: verb, Namespace: a.Proposal.Target.Namespace, Kind: a.Proposal.Target.Kind, Name: a.Proposal.Target.Name, StdinJSON: stdin, DryRun: a.ExecutorKind == "dryrun"}
	res, execErr := exec.Do(ctx, req)
	a.ExecutedAt = g.deps.Now()
	a.VerifyDeadline = a.ExecutedAt.Add(g.cfg.VerifyWindow)
	if execErr != nil {
		a.State = model.ActionFailed
		a.StateReason = execErr.Error()
		a.UpdatedAt = g.deps.Now()
		g.audit(ctx, tid, by.ID, "action_failed", map[string]any{"action_id": id, "reason": a.StateReason})
		out := *a
		g.storeIdempotency(tid, "execute", idem, id, out)
		return out, execErr
	}

	a.State = model.ActionVerifying
	a.DryRunOutput = string(res.Stdout)
	a.UpdatedAt = g.deps.Now()
	g.audit(ctx, tid, by.ID, "action_verifying", map[string]any{"action_id": id})

	out := *a
	g.storeIdempotency(tid, "execute", idem, id, out)
	return out, nil
}

func (g *guard) Verify(ctx context.Context, tid model.TenantID, id string) (model.VerifyResult, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	a, err := g.get(tid, id)
	if err != nil {
		return model.VerifyResult{}, err
	}
	if a.State != model.ActionVerifying {
		return model.VerifyResult{}, fmt.Errorf("%w: cannot Verify from state %d", ErrIllegalTransition, a.State)
	}

	var res model.VerifyResult
	if g.deps.Signals != nil && g.deps.Verifier != nil {
		sig, serr := g.deps.Signals.Signal(ctx, tid, *a)
		if serr == nil {
			res, err = g.deps.Verifier.Verify(ctx, tid, *a, sig)
		}
	} else {
		res = model.VerifyResult{Verified: true, Message: "no verifier configured; default success", VerifiedAt: g.deps.Now()}
	}

	a.PostVerify = res
	a.Verified = res.Verified
	if res.Verified {
		a.State = model.ActionSucceeded
	} else {
		a.State = model.ActionFailed
		a.StateReason = "verify_failed"
	}
	a.UpdatedAt = g.deps.Now()
	g.audit(ctx, tid, "system", "action_verified", map[string]any{"action_id": id, "verified": res.Verified})
	return res, nil
}

func (g *guard) Rollback(ctx context.Context, tid model.TenantID, id string, by auth.Subject, reason string) (model.Action, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	a, err := g.get(tid, id)
	if err != nil {
		return model.Action{}, err
	}
	if a.State != model.ActionVerifying && a.State != model.ActionFailed {
		return model.Action{}, fmt.Errorf("%w: cannot Rollback from state %d", ErrIllegalTransition, a.State)
	}
	// §23.9: refuse when the live object's resourceVersion differs from
	// the snapshot's.
	if g.deps.Resolver != nil {
		live, rerr := g.deps.Resolver.Resolve(ctx, tid, a.Proposal.Target)
		if rerr == nil && a.PreSnapshot.ResourceVersion != "" && live.ResolvedVersion != a.PreSnapshot.ResourceVersion {
			a.State = model.ActionFailed
			a.StateReason = "concurrent_modification"
			a.UpdatedAt = g.deps.Now()
			g.audit(ctx, tid, by.ID, "rollback_refused_concurrent_modification", map[string]any{"action_id": id})
			return *a, fmt.Errorf("remediate: rollback_refused_concurrent_modification")
		}
	}
	a.State = model.ActionRolledBack
	a.StateReason = reason
	a.UpdatedAt = g.deps.Now()
	g.audit(ctx, tid, by.ID, "action_rolled_back", map[string]any{"action_id": id, "reason": reason})
	return *a, nil
}

func (g *guard) Get(ctx context.Context, tid model.TenantID, id string) (model.Action, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	a, err := g.get(tid, id)
	if err != nil {
		return model.Action{}, err
	}
	return *a, nil
}

func (g *guard) get(tid model.TenantID, id string) (*model.Action, error) {
	a, ok := g.actions[id]
	if !ok || a.Tenant != tid {
		return nil, ErrNotFound
	}
	return a, nil
}

func (g *guard) List(ctx context.Context, tid model.TenantID, f ActionFilter) ([]model.Action, string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	var out []model.Action
	for _, a := range g.actions {
		if a.Tenant != tid {
			continue
		}
		if f.IncidentID != "" && a.IncidentID != f.IncidentID {
			continue
		}
		if len(f.States) > 0 {
			match := false
			for _, s := range f.States {
				if a.State == s {
					match = true
					break
				}
			}
			if !match {
				continue
			}
		}
		out = append(out, *a)
	}
	return out, "", nil
}

func (g *guard) ExpireDue(ctx context.Context, now time.Time) (int, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	n := 0
	for _, a := range g.actions {
		switch a.State {
		case model.ActionProposed:
			if !a.CreatedAt.Add(g.cfg.ProposalTTL).After(now) {
				a.State = model.ActionExpired
				a.UpdatedAt = now
				n++
			}
		case model.ActionApproved:
			if !a.ExpiresAt.IsZero() && !a.ExpiresAt.After(now) {
				a.State = model.ActionExpired
				a.UpdatedAt = now
				n++
			}
		}
	}
	return n, nil
}

func (g *guard) Stats() Stats {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := Stats{CountByState: map[model.ActionState]int{}}
	for _, a := range g.actions {
		out.CountByState[a.State]++
	}
	return out
}

func (g *guard) policy(ctx context.Context, tid model.TenantID) (tenant.Policy, error) {
	if g.deps.Policy == nil {
		return tenant.Policy{TenantID: tid, ActionBudgetPerIncident: g.cfg.DefaultActionBudget, MaxReplicas: 50}, nil
	}
	return g.deps.Policy.Get(ctx, tid)
}
