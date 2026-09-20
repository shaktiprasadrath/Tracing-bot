package rca

import "errors"

// ErrNoMoreRules is the rules reasoner's "candidates empty" outcome (F06
// §4.4's NextStep_rules pseudocode: "if candidates empty: return _,
// ok=false, 'no_more_rules'"). DR-15's Reasoner.NextStep signature has no
// third return value for this, so it is surfaced as an error the Engine
// specifically recognizes (distinct from an LLM fallback-trigger error,
// which the rules reasoner never produces since it never swaps further)
// and treats as an ordinary, non-fatal end of the hypothesis loop.
var ErrNoMoreRules = errors.New("rca: no more rules to try")

// ErrEvidenceCorrupt is DR-18 §18.2's ReplayRecorded/ReplayLiveDiff integrity
// check: "A ToolResultHash mismatch aborts the replay and raises a Critical
// incident" (FR-F06-24).
var ErrEvidenceCorrupt = errors.New("rca: evidence hash mismatch")

// confidenceThreshold is DR-17 §17.5's rca.confidence_threshold (canonical
// 0.75; F06's 0.8 is deleted by the register).
const confidenceThreshold = 0.75

// --- w15: LLM-reasoner fallback-trigger sentinels (DR-34 §34.4) ---
//
// DR-34 §34.4 lists six triggers (transport, refusal, daily cap, saturation,
// schema, canary) for the llm -> rules mid-loop swap. This wave implements
// the ones LLMReasoner can detect locally from a single NextStep call:
// transport failure, a refusal, schema-validation failure surviving the one
// retry F06 §4.4 allows, a canary echo (DR-37 §37.1(3)), and — a w15
// extension beyond the register's six, justified below — the reasoner's own
// preflight read of Investigation.Spend against Investigation.Budget, so an
// LLM call that would certainly blow the per-investigation budget is never
// even attempted; the investigation instead continues under the deterministic
// rules reasoner within whatever Steps/ToolCalls budget remains, rather than
// dying with TermFailed or silently wasting a call it cannot afford. Daily
// cap and saturation (queue-depth) are engine/dispatcher-level concerns this
// wave's Engine does not yet implement (see engine.go's doc comment) and are
// out of scope here.
var (
	ErrLLMTransport       = errors.New("rca: llm transport failure")
	ErrLLMRefused         = errors.New("rca: llm refused (stop_reason=refusal)")
	ErrLLMSchemaInvalid   = errors.New("rca: llm output failed schema validation after retry")
	ErrLLMCanaryViolation = errors.New("rca: llm output echoed the untrusted-region canary")
	ErrLLMBudgetExceeded  = errors.New("rca: llm reasoner preflight budget check failed")
)

// isFallbackTrigger reports whether err is one of the DR-34 §34.4 (plus w15
// extension) triggers that swap the reasoner to rules mid-loop, rather than
// failing the investigation outright.
func isFallbackTrigger(err error) bool {
	return errors.Is(err, ErrLLMTransport) ||
		errors.Is(err, ErrLLMRefused) ||
		errors.Is(err, ErrLLMSchemaInvalid) ||
		errors.Is(err, ErrLLMCanaryViolation) ||
		errors.Is(err, ErrLLMBudgetExceeded)
}
