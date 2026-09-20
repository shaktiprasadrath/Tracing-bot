package model

import "time"

// ActionType is DR-22 §22.1's closed, five-value action enum, verbatim.
// "A sixth action type is an architecture change (a new DR), never a
// config value."
type ActionType uint8

const (
	ActionRollbackDeployment ActionType = 1
	ActionScaleReplicas      ActionType = 2
	ActionRestartPod         ActionType = 3
	ActionToggleFeatureFlag  ActionType = 4
	ActionRemoveIstioFault   ActionType = 5
)

// TargetKind is DR-22 §22.1, verbatim.
type TargetKind uint8

const (
	KindDeployment     TargetKind = 1
	KindStatefulSet    TargetKind = 2
	KindPod            TargetKind = 3
	KindConfigMap      TargetKind = 4
	KindVirtualService TargetKind = 5
)

// ActionTarget is DR-22 §22.1, verbatim.
type ActionTarget struct {
	Cluster         string // "" = the configured kubeconfig context; NEVER model-authored
	Namespace       string // DNS-1123 label; MUST be in tenant.Policy.NamespaceAllowlist
	Kind            TargetKind
	Name            string // DNS-1123 subdomain. NO "/" (the "deployment/checkout" form is DELETED)
	ResolvedUID     string // stamped by the Guard from the live cluster; never by the proposer
	ResolvedVersion string // resourceVersion at resolve time
}

// RollbackDeploymentSpec is DR-22 §22.1, verbatim.
type RollbackDeploymentSpec struct {
	ToRevision int64 // 0 = previous; must exist in rollout history
}

// ScaleReplicasSpec is DR-22 §22.1, verbatim.
type ScaleReplicasSpec struct {
	Replicas int32 // 1..tenant.Policy.MaxReplicas (default 50); and <= 3x the current replica count
}

// RestartPodSpec is DR-22 §22.1, verbatim.
type RestartPodSpec struct {
	GracePeriodSeconds int32 // 0..300
}

// ToggleFeatureFlagSpec is DR-22 §22.1, verbatim.
type ToggleFeatureFlagSpec struct {
	Key   string // MUST be in tenant.Policy.FeatureFlags
	Value bool   // MUST be one of that key's allowed values
}

// RemoveIstioFaultSpec is DR-22 §22.1, verbatim: "NO FIELDS. The Guard
// computes the patch."
type RemoveIstioFaultSpec struct{}

// ActionProposal is DR-22 §22.1, verbatim: the closed data model that
// replaces the free-form `Params map[string]string` model-authored payload.
// Exactly one spec pointer is non-nil and it MUST match Type.
type ActionProposal struct {
	ID              string
	Tenant          TenantID
	IncidentID      string
	InvestigationID string
	Type            ActionType
	Target          ActionTarget
	RiskTier        uint8  // 1..3, from the fixed table in DR-22 §22.4 — NOT proposer-supplied
	Rationale       string // <= 2000 bytes. DISPLAY ONLY. Never reaches a tool, an argv or the cluster.
	ProposedBy      string // auth.Subject.ID, or "rca:<investigationID>"
	ProposedAt      time.Time

	Rollback    *RollbackDeploymentSpec
	Scale       *ScaleReplicasSpec
	Restart     *RestartPodSpec
	FeatureFlag *ToggleFeatureFlagSpec
	IstioFault  *RemoveIstioFaultSpec
}

// ActionState is 01 §4.5's `remediate.State`, promoted to model per
// DR-23's explicit `model.Action` usage in remediate.Guard (e.g.
// `Get(...) (model.Action, error)`). NOTE: 01 §4.5 numbers this enum
// 1 Proposed, 2 Rejected, 3 Approved, ...; DR-23 §23.2 restates "01 §4.5's
// nine states are adopted verbatim" but then itself lists a DIFFERENT
// order — Proposed(1), Approved(2), Rejected(3), ... This is a register
// contradiction (flagged in docs/reports/scaffold-report.md). DR-0's
// precedence rule ("this register wins" over 00-05) makes DR-23 §23.2's
// own explicit numbering authoritative here.
type ActionState uint8

const (
	ActionProposed   ActionState = 1
	ActionApproved   ActionState = 2
	ActionRejected   ActionState = 3
	ActionExecuting  ActionState = 4
	ActionVerifying  ActionState = 5
	ActionSucceeded  ActionState = 6
	ActionFailed     ActionState = 7
	ActionRolledBack ActionState = 8
	ActionExpired    ActionState = 9
)

// Snapshot is DR-23 §23.9's "model.Snapshot gains ResourceVersion,
// Generation, UID, TakenAt, SHA256" — the register never prints the base
// struct it amends, only this additive sentence, so the fields below are a
// reconstruction (RawJSON is assumed to hold the pre-mutation object spec,
// mirroring 01 §4.5's Action.PreSnapshotJSON). See
// docs/reports/scaffold-report.md.
type Snapshot struct {
	ResourceVersion string
	Generation      int64
	UID             string
	TakenAt         time.Time
	SHA256          string
	RawJSON         []byte
}

// VerifyResult is remediate.Guard.Verify and remediate.Verifier.Verify's
// return type (DR-23 §23.1). The register never gives its field list;
// this is a minimal reconstruction that deliberately holds no
// remediate-package types (model is a leaf per DR-4 and cannot import
// remediate.RecoverySignal). See docs/reports/scaffold-report.md.
type VerifyResult struct {
	Verified   bool
	Message    string
	VerifiedAt time.Time
	Details    map[string]string
}

// Action is 01 §4.5's remediate.Action, promoted to model per DR-23's
// `model.Action` usage. Base fields are 01 §4.5 verbatim (Tenant retyped to
// TenantID, State retyped to ActionState, PreSnapshotJSON []byte retyped to
// the typed Snapshot above). DR-23 additions: AutoExecute (§23.5) and the
// budget-override audit fields (§23.6) — reconstructed, since the register
// states the override *rules* but not a struct shape.
type Action struct {
	ID              string
	Tenant          TenantID
	IncidentID      string
	InvestigationID string
	Proposal        ActionProposal
	State           ActionState
	StateReason     string
	ProposedBy      string // "rca:<investigationID>" | "user:<subject>"
	ApprovedBy      string // subject with role approver|admin
	ApprovedAt      time.Time
	ExpiresAt       time.Time // ApprovedAt + remediate.approval_ttl (DR-23 §23.3)

	ExecutorKind string // "dryrun" | "kubectl"
	PreSnapshot  Snapshot
	DryRunOutput string

	ExecutedAt     time.Time
	VerifyDeadline time.Time // ExecutedAt + remediate.verify_window
	PostVerify     VerifyResult
	Verified       bool

	AutoExecute bool // DR-23 §23.5; forbidden at RiskTier 3 regardless of config

	BudgetIndex           int // 1..tenant.Policy.ActionBudgetPerIncident (DR-23 §23.6)
	OverrideUsed          bool
	OverrideJustification string // >= 40 bytes when used (DR-23 §23.6)
	OverriddenBy          string
	OverriddenAt          time.Time

	AuditSeqs []int64 // audit_log.seq rows for this action
	Error     string
	CreatedAt time.Time
	UpdatedAt time.Time
}
