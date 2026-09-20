package remediate

import (
	"fmt"
	"path"
	"regexp"

	"traceiq/internal/model"
	"traceiq/internal/tenant"
)

// riskTier is DR-22 §22.4's fixed table, verbatim. A proposer-supplied
// RiskTier is always ignored — this table is the only source.
func riskTier(t model.ActionType) (uint8, bool) {
	switch t {
	case model.ActionRestartPod, model.ActionScaleReplicas:
		return 1, true
	case model.ActionRollbackDeployment, model.ActionToggleFeatureFlag:
		return 2, true
	case model.ActionRemoveIstioFault:
		return 3, true
	default:
		return 0, false
	}
}

// dns1123Label is a DNS-1123 label: internal/k8s.BuildArgv's validateName is
// the enforcement point for anything that reaches an argv; this is
// remediate's own early rejection at Propose time so a malformed target
// never advances to approval. Verbatim regex from DR-24 rule 2.
var dns1123 = regexp.MustCompile(`^[a-z0-9]([-a-z0-9.]{0,251}[a-z0-9])?$`)

// validateProposal is DR-22 §22.1's "exactly one spec pointer is non-nil and
// it MUST match Type" plus the per-type field bounds from §22.1's inline
// comments. It runs BEFORE target re-resolution (§22.3) and BEFORE the
// allowlist check, and is the type-level backstop that replaces the deleted
// `Params map[string]string`: there is no field on ActionProposal a
// free-form value could occupy, so "rejected at the type level" here means
// "the closed set of typed spec pointers is exhaustively checked and
// anything that doesn't match Type, or carries a second non-nil spec, is
// refused immediately."
func validateProposal(p model.ActionProposal, pol tenant.Policy) error {
	specs := map[model.ActionType]bool{}
	if p.Rollback != nil {
		specs[model.ActionRollbackDeployment] = true
	}
	if p.Scale != nil {
		specs[model.ActionScaleReplicas] = true
	}
	if p.Restart != nil {
		specs[model.ActionRestartPod] = true
	}
	if p.FeatureFlag != nil {
		specs[model.ActionToggleFeatureFlag] = true
	}
	if p.IstioFault != nil {
		specs[model.ActionRemoveIstioFault] = true
	}
	if len(specs) != 1 {
		return fmt.Errorf("%w: exactly one typed spec must be set, got %d", ErrInvalidProposal, len(specs))
	}
	if !specs[p.Type] {
		return fmt.Errorf("%w: Type %d does not match the non-nil spec pointer", ErrInvalidProposal, p.Type)
	}
	if _, ok := riskTier(p.Type); !ok {
		return fmt.Errorf("%w: unknown ActionType %d (closed 5-value enum)", ErrInvalidProposal, p.Type)
	}
	if len(p.Rationale) > 2000 {
		return fmt.Errorf("%w: rationale exceeds 2000 bytes", ErrInvalidProposal)
	}

	if err := validateTarget(p.Target); err != nil {
		return err
	}

	switch p.Type {
	case model.ActionRollbackDeployment:
		if p.Rollback.ToRevision < 0 {
			return fmt.Errorf("%w: ToRevision must be >= 0", ErrInvalidProposal)
		}
	case model.ActionScaleReplicas:
		max := pol.MaxReplicas
		if max <= 0 {
			max = 50
		}
		if p.Scale.Replicas < 1 || p.Scale.Replicas > max {
			return fmt.Errorf("%w: Replicas %d out of range [1,%d]", ErrInvalidProposal, p.Scale.Replicas, max)
		}
	case model.ActionRestartPod:
		if p.Restart.GracePeriodSeconds < 0 || p.Restart.GracePeriodSeconds > 300 {
			return fmt.Errorf("%w: GracePeriodSeconds out of range [0,300]", ErrInvalidProposal)
		}
	case model.ActionToggleFeatureFlag:
		allowed, ok := pol.FeatureFlags[p.FeatureFlag.Key]
		if !ok {
			return fmt.Errorf("%w: feature flag Key %q not in tenant.Policy.FeatureFlags", ErrInvalidProposal, p.FeatureFlag.Key)
		}
		want := "false"
		if p.FeatureFlag.Value {
			want = "true"
		}
		found := false
		for _, v := range allowed {
			if v == want {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("%w: value %q not an allowed value for flag %q", ErrInvalidProposal, want, p.FeatureFlag.Key)
		}
	case model.ActionRemoveIstioFault:
		// No fields (DR-22 §22.1): nothing further to validate here. The
		// Guard computes the patch at execute time (§22.2) and refuses
		// nothing_to_remove there if the pre-snapshot carries no fault.
	}
	return nil
}

func validateTarget(t model.ActionTarget) error {
	if t.Namespace == "" || !dns1123.MatchString(t.Namespace) {
		return fmt.Errorf("%w: Namespace %q is not a DNS-1123 label", ErrInvalidProposal, t.Namespace)
	}
	if t.Name == "" || !dns1123.MatchString(t.Name) {
		return fmt.Errorf("%w: Name %q is not a valid DNS-1123 subdomain (no \"/\")", ErrInvalidProposal, t.Name)
	}
	switch t.Kind {
	case model.KindDeployment, model.KindStatefulSet, model.KindPod, model.KindConfigMap, model.KindVirtualService:
	default:
		return fmt.Errorf("%w: unknown TargetKind %d", ErrInvalidProposal, t.Kind)
	}
	return nil
}

// checkAllowlists is DR-22 §22.3 step 4: evaluated ONLY after topology
// existence, live existence and (at execute) re-resolution have all passed.
//
// All three allowlists are FAIL-CLOSED: an empty/unconfigured allowlist
// denies every candidate, it does not permit everything. (An earlier
// version of this function treated an empty RemediationAllowlist as
// "allow all action types" — the opposite of FR-F09-2's "Propose() MUST
// reject any ActionType not present in ... RemediationAllowlist" and
// inconsistent with this same function's NamespaceAllowlist check just
// below it. That inversion meant a tenant with no explicit allowlist
// configured — the documented zero-value `remediate.allowlist: []` — had
// every one of the five action types permitted instead of none, defeating
// the blast-radius bound this check exists to enforce. Fixed per
// docs/reports/w14-review-remediate.md, finding B1.)
func checkAllowlists(p model.ActionProposal, pol tenant.Policy) error {
	okType := false
	for _, at := range pol.RemediationAllowlist {
		if at == p.Type {
			okType = true
			break
		}
	}
	if !okType {
		return fmt.Errorf("%w: ActionType %d not in tenant.Policy.RemediationAllowlist", ErrInvalidProposal, p.Type)
	}
	okNS := false
	for _, ns := range pol.NamespaceAllowlist {
		if ns == p.Target.Namespace {
			okNS = true
			break
		}
	}
	if !okNS {
		return fmt.Errorf("%w: namespace %q not in tenant.Policy.NamespaceAllowlist", ErrInvalidProposal, p.Target.Namespace)
	}
	// DR-22 §22.3 step 4 / F09 §2 drawback table D-X4(b): TargetAllowlist
	// glob rules ("prod/Deployment/checkout-*") gate the specific target in
	// addition to the type and namespace checks above. This field existed
	// on tenant.Policy but was never consulted anywhere in this package —
	// fixed per docs/reports/w14-review-remediate.md, finding M1.
	if err := checkTargetAllowlist(p.Target, pol.TargetAllowlist); err != nil {
		return err
	}
	return nil
}

// kindName renders a model.TargetKind as the string used in
// tenant.Policy.TargetAllowlist glob entries ("<namespace>/<Kind>/<name>",
// e.g. "prod/Deployment/checkout-*", per DR-22 §22.3 / DR-3's own example).
func kindName(k model.TargetKind) string {
	switch k {
	case model.KindDeployment:
		return "Deployment"
	case model.KindStatefulSet:
		return "StatefulSet"
	case model.KindPod:
		return "Pod"
	case model.KindConfigMap:
		return "ConfigMap"
	case model.KindVirtualService:
		return "VirtualService"
	default:
		return "Unknown"
	}
}

// checkTargetAllowlist matches "<namespace>/<Kind>/<name>" against each
// glob pattern in allowlist using path.Match, whose "*" does not cross "/"
// segment boundaries — exactly the semantics DR-22's example pattern
// ("prod/Deployment/checkout-*") needs. An empty allowlist denies every
// target (fail-closed, same posture as NamespaceAllowlist above).
func checkTargetAllowlist(t model.ActionTarget, allowlist []string) error {
	candidate := t.Namespace + "/" + kindName(t.Kind) + "/" + t.Name
	for _, pattern := range allowlist {
		if ok, err := path.Match(pattern, candidate); err == nil && ok {
			return nil
		}
	}
	return fmt.Errorf("%w: target %q not in tenant.Policy.TargetAllowlist", ErrInvalidProposal, candidate)
}
