package remediate

import "errors"

// Sentinel errors, one per DR-23/DR-22 refusal code named in the register
// (e.g. "409 approver_must_differ", "422 target_not_resolvable"). Callers
// that need the HTTP-ish code string can type-switch or use errors.Is.
var (
	ErrInvalidProposal      = errors.New("remediate: invalid_proposal")
	ErrTargetNotResolvable  = errors.New("remediate: target_not_resolvable")
	ErrTargetReplaced       = errors.New("remediate: target_replaced")
	ErrApproverMustDiffer   = errors.New("remediate: approver_must_differ")
	ErrActionExpired        = errors.New("remediate: action_expired")
	ErrBudgetExceeded       = errors.New("remediate: budget_exceeded")
	ErrOverrideLimit        = errors.New("remediate: override_limit")
	ErrOverrideJustTooShort = errors.New("remediate: override_justification_too_short")
	ErrIllegalTransition    = errors.New("remediate: illegal_transition")
	ErrNotFound             = errors.New("remediate: not_found")
	ErrPayloadOutOfScope    = errors.New("remediate: payload_out_of_scope")
	ErrNothingToRemove      = errors.New("remediate: nothing_to_remove")
	ErrForbiddenFlag        = errors.New("remediate: forbidden_flag")
	ErrIdempotencyReused    = errors.New("remediate: idempotency_key_reused")
	ErrIdempotencyRequired  = errors.New("remediate: idempotency_key_required")
	ErrAutoExecuteForbidden = errors.New("remediate: auto_execute_forbidden_risk_tier_3")
	ErrUnknownExecutor      = errors.New("remediate: unknown_executor")
)
