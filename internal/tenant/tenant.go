package tenant

import (
	"context"
	"time"

	"traceiq/internal/model"
)

// ChatBinding is DR-3, verbatim.
type ChatBinding struct {
	Platform    string // "slack" | "teams"
	WorkspaceID string
	ChannelIDs  []string // approval-capable channels; empty = none
}

// Policy is DR-3's single per-tenant record, verbatim: consumed by F02
// (floor), F03 (retention, byte budget), F06 (LLM spend), F09 (remediation
// allowlist) and X-SEC (RBAC).
type Policy struct {
	TenantID    model.TenantID
	DisplayName string

	// F03 retention and cost
	RetentionAnomalousDays int   // default 30
	RetentionHealthyDays   int   // default 7
	RetentionHotSpanHours  int   // default 24 (dev) / 72 (prod)
	ByteBudgetBytes        int64 // 0 = inherit store.budget.max_disk_bytes share
	// F02 sampling
	SamplingFloor float64 // default 0.01
	MaxKeepRate   float64 // default 0.25
	// F06 cost
	LLMCostMicroUSDPerDay int64 // default 20_000_000 ($20)
	// F09 remediation
	RemediationAllowlist    []model.ActionType
	NamespaceAllowlist      []string
	TargetAllowlist         []string            // glob rules, e.g. "prod/Deployment/checkout-*"
	ActionBudgetPerIncident int                 // default 2
	AutoExecuteAllowed      bool                // default false
	FeatureFlags            map[string][]string // key -> allowed string values ("true"/"false" for bools)
	// X-SEC
	RBACBindings map[string][]string // subjectID -> role names
	ChatBindings []ChatBinding
	UpdatedAt    time.Time
	Version      int64 // optimistic concurrency

	// CorrelationTenantID maps this tenant onto a correlate backend's own
	// org/tenant id when it differs from TenantID (DR-20 §20.2). Defaults
	// to TenantID.
	CorrelationTenantID string

	// EvalNamespaceAllowlist is DR-36 §36.7: disjoint from NamespaceAllowlist;
	// a namespace in both is a startup error.
	EvalNamespaceAllowlist []string

	// MaxReplicas bounds ScaleReplicasSpec.Replicas (DR-22 §22.1's spec
	// comment: "1..tenant.Policy.MaxReplicas (default 50)"). The register
	// never adds this field to DR-3's Policy block explicitly; it is
	// reconstructed here because ScaleReplicasSpec's own doc comment
	// requires it to exist somewhere on Policy. See
	// docs/reports/scaffold-report.md.
	MaxReplicas int32
}

// Resolver is DR-3, verbatim: the ONLY tenant resolution path in the
// system.
type Resolver interface {
	// FromSubject is the sole production path: the tenant is a property of
	// the authenticated principal. No header, no body field, no query
	// parameter.
	FromSubject(ctx context.Context, subjectTenant model.TenantID) (model.TenantID, error)
	// FromDevDefault is legal ONLY when server.profile == "dev" AND the
	// listener is loopback AND auth.mode == "none". It returns
	// tenancy.default_tenant.
	FromDevDefault(ctx context.Context) (model.TenantID, error)
}

// PolicyStore is DR-3, verbatim.
type PolicyStore interface {
	Get(ctx context.Context, tid model.TenantID) (Policy, error)
	Set(ctx context.Context, p Policy, by string) (Policy, error) // admin only, audited
	List(ctx context.Context) ([]Policy, error)
	Watch(ctx context.Context, tid model.TenantID) (<-chan Policy, error)
}

// contextKey is unexported so WithTenant/FromContext are the only way to
// carry a tenant on a context.Context (DR-3's "convenience for logging and
// defence in depth" note: this is NEVER the enforcement path).
type contextKey struct{}

// WithTenant is DR-3, verbatim: a convenience for logging and defence in
// depth. It is NEVER the enforcement path: tenancy is enforced by the
// explicit model.TenantID parameter on every store/memory/correlate/
// topology/rca/remediate call.
func WithTenant(ctx context.Context, tid model.TenantID) context.Context {
	panic("not implemented")
}

// FromContext is DR-3, verbatim.
func FromContext(ctx context.Context) (model.TenantID, bool) {
	panic("not implemented")
}
