package remediate

import (
	"context"
	"time"

	"traceiq/internal/auth"
	"traceiq/internal/model"
)

// The closed action-type enum (model.ActionType), the typed payload specs
// (model.RollbackDeploymentSpec etc.) and the 9-state machine
// (model.ActionState, incl. model.ActionExpired) are declared in
// internal/model per DR-22 §22.1 / DR-23 §23.2 and DR-4's "reference, never
// redefine" rule — see internal/model/remediate.go. This file holds only
// what DR-23 §23.1 declares directly in `package remediate`.

// Guard is DR-23 §23.1's full remediation control-plane interface,
// verbatim. `02 §3`'s `Approve(id, approver string)` is deleted; a CI grep
// gate (not part of this scaffold) fails the build if `Approve(` is
// declared with a non-auth.Subject approver.
type Guard interface {
	Propose(ctx context.Context, tid model.TenantID, p model.ActionProposal, by auth.Subject, idem string) (model.Action, error)
	Approve(ctx context.Context, tid model.TenantID, id string, by auth.Subject, req ApprovalRequest) (model.Action, error)
	Reject(ctx context.Context, tid model.TenantID, id string, by auth.Subject, reason string) (model.Action, error)
	Execute(ctx context.Context, tid model.TenantID, id string, by auth.Subject, idem string) (model.Action, error)
	Verify(ctx context.Context, tid model.TenantID, id string) (model.VerifyResult, error)
	Rollback(ctx context.Context, tid model.TenantID, id string, by auth.Subject, reason string) (model.Action, error)
	Get(ctx context.Context, tid model.TenantID, id string) (model.Action, error)
	List(ctx context.Context, tid model.TenantID, f ActionFilter) ([]model.Action, string, error)
	ExpireDue(ctx context.Context, now time.Time) (int, error) // CLEANUP ONLY — never the control (DR-23 §23.3)
	Stats() Stats
}

// ApprovalRequest is DR-23 §23.1, verbatim: OverrideJustification is a
// structurally distinct field from Comment so "20 characters in the
// comment" cannot satisfy a budget override (DR-23 §23.6, SR-5ii).
type ApprovalRequest struct {
	Comment               string // <= 1000 bytes, display only
	OverrideJustification string // >= 40 bytes when RequestBudgetOverride is true
	RequestBudgetOverride bool
}

// RecoverySignal is DR-23 §23.1, verbatim: declared HERE so remediate never
// imports anomaly (DR-2's cycle-break table).
type RecoverySignal struct {
	Service, Operation              string
	Window                          model.Window
	ErrorRateBefore, ErrorRateAfter float64
	P99BeforeNanos, P99AfterNanos   uint64
	OpenIncidents                   int
}

// Verifier is DR-23 §23.1, verbatim.
type Verifier interface {
	Verify(ctx context.Context, tid model.TenantID, a model.Action, s RecoverySignal) (model.VerifyResult, error)
}

// ActionFilter is Guard.List's filter parameter. The register never prints
// its field list; reconstructed minimally from the List signature and the
// endpoint table's implied query params (state/incident/pagination).
// TODO(DR-23): confirm field list against the eventual endpoint spec.
type ActionFilter struct {
	IncidentID string
	States     []model.ActionState
	Cursor     string
	Limit      int
}

// Stats is Guard.Stats' return type. Not given a shape in the register;
// reconstructed minimally as counts per state.
// TODO(DR-23): confirm against any published /v1/actions/stats response.
type Stats struct {
	CountByState map[model.ActionState]int
}
