package rca

// w15: engine-loop tests specific to the LLM reasoner's budget dimensions
// (DR-17 §17.2's "if reasoner.Kind() == llm and ...ounces" loop-top checks)
// and the DR-34 §34.4 mid-loop fallback mechanics that only the Engine
// (never LLMReasoner alone) can prove: prior steps preserved, ReasonerSwaps
// recorded, no swap-back, graceful termination with no fallback wired.

import (
	"context"
	"errors"
	"testing"

	"traceiq/internal/model"
)

// --- (d) llm-only Tokens/CachedTokens budget dimensions terminate gracefully ---

func TestInvestigate_LLMTokensBudgetExhausted(t *testing.T) {
	registry := okRegistry()
	reasoner := &fakeReasoner{
		kind: model.ReasonerLLM,
		next: func(ctx context.Context, s State) (Proposal, error) {
			// Every call "spends" more than the whole uncached-token budget
			// in one shot, so the loop-top check trips before a second call.
			return Proposal{Phase: model.PhaseHypothesize, Usage: Charge{TokensIn: int64(DefaultBudget().MaxTokensIn) + 1}}, nil
		},
	}
	e := newTestEngine(reasoner, registry, nil, nil)
	inv, err := e.Investigate(context.Background(), testTenant(), testIncident())
	if err != nil {
		t.Fatalf("Investigate: %v", err)
	}
	if inv.TerminationReason != model.TermTokens {
		t.Fatalf("TerminationReason = %v, want TermTokens", inv.TerminationReason)
	}
	if inv.Status != model.InvestigationBudgetExhausted {
		t.Errorf("Status = %v, want InvestigationBudgetExhausted", inv.Status)
	}
	if inv.Spend.TokensIn == 0 {
		t.Error("expected the charged TokensIn to be folded into Investigation.Spend")
	}
	if inv.ReasonerKind != model.ReasonerLLM {
		t.Errorf("ReasonerKind = %v, want llm (no fallback was wired, so no swap should occur)", inv.ReasonerKind)
	}
}

func TestInvestigate_LLMCachedTokensBudgetExhausted(t *testing.T) {
	registry := okRegistry()
	reasoner := &fakeReasoner{
		kind: model.ReasonerLLM,
		next: func(ctx context.Context, s State) (Proposal, error) {
			return Proposal{Phase: model.PhaseHypothesize, Usage: Charge{CachedTokensIn: int64(DefaultBudget().MaxCachedTokensIn) + 1}}, nil
		},
	}
	e := newTestEngine(reasoner, registry, nil, nil)
	inv, err := e.Investigate(context.Background(), testTenant(), testIncident())
	if err != nil {
		t.Fatalf("Investigate: %v", err)
	}
	if inv.TerminationReason != model.TermCachedTokens {
		t.Fatalf("TerminationReason = %v, want TermCachedTokens", inv.TerminationReason)
	}
}

// A rules-kind reasoner must never trip the llm-only token checks, even with
// an identical Usage value on its Proposal (rules never actually sets Usage,
// but the loop-top gate is Kind()-based, not Usage-based, and this proves
// it).
func TestInvestigate_RulesKindNeverChecksTokenBudget(t *testing.T) {
	registry := okRegistry()
	calls := 0
	reasoner := &fakeReasoner{
		next: func(ctx context.Context, s State) (Proposal, error) {
			calls++
			if calls > 2 {
				return Proposal{}, ErrNoMoreRules
			}
			return Proposal{Phase: model.PhaseHypothesize, Usage: Charge{TokensIn: int64(DefaultBudget().MaxTokensIn) * 10}}, nil
		},
	}
	e := newTestEngine(reasoner, registry, nil, nil)
	inv, err := e.Investigate(context.Background(), testTenant(), testIncident())
	if err != nil {
		t.Fatalf("Investigate: %v", err)
	}
	if inv.TerminationReason == model.TermTokens || inv.TerminationReason == model.TermCachedTokens {
		t.Fatalf("TerminationReason = %v, a rules-kind reasoner must never trigger the llm-only token checks", inv.TerminationReason)
	}
}

// --- (d)/(e) mid-loop fallback: preserves prior steps, records the swap,
// never swaps back, caps confidence ---

func TestInvestigate_FallbackSwap_PreservesPriorStepsRecordsSwapCapsConfidence(t *testing.T) {
	registry := okRegistry()
	triggerErr := errors.New("boom: simulated llm failure")
	calls := 0
	reasoner := &fakeReasoner{
		kind: model.ReasonerLLM,
		next: func(ctx context.Context, s State) (Proposal, error) {
			calls++
			if calls == 1 {
				// One successful pure-reasoning step before the trigger, so
				// the test can prove it survives the swap.
				return Proposal{Phase: model.PhaseHypothesize, Rationale: "pre-swap step"}, nil
			}
			return Proposal{}, isFallbackTriggerErr(triggerErr)
		},
	}
	fallback := &fakeReasoner{
		next: func(ctx context.Context, s State) (Proposal, error) {
			return Proposal{}, ErrNoMoreRules // concludes inconclusively, cleanly
		},
		conclude: func(ctx context.Context, s State) (model.Conclusion, error) {
			// A confidence well above threshold, to prove the cap actually
			// suppresses it rather than merely not raising it further.
			return model.Conclusion{RootCause: "would-be high confidence", Confidence: 0.95}, nil
		},
	}
	e := newTestEngine(reasoner, registry, nil, nil)
	e.SetFallbackReasoner(fallback)

	inv, err := e.Investigate(context.Background(), testTenant(), testIncident())
	if err != nil {
		t.Fatalf("Investigate: %v", err)
	}

	if inv.ReasonerKind != model.ReasonerRules {
		t.Fatalf("ReasonerKind = %v, want rules after the swap", inv.ReasonerKind)
	}
	if len(inv.ReasonerSwaps) != 1 {
		t.Fatalf("ReasonerSwaps = %+v, want exactly one swap", inv.ReasonerSwaps)
	}
	swap := inv.ReasonerSwaps[0]
	if swap.From != model.ReasonerLLM || swap.To != model.ReasonerRules {
		t.Errorf("swap = %+v, want From=llm To=rules", swap)
	}
	// Contextualize (engine-internal) + the one pre-swap Hypothesize step
	// must both still be present.
	if len(inv.Steps) < 2 {
		t.Fatalf("Steps = %+v, want the pre-swap steps preserved (>= 2: Contextualize + pre-swap step)", inv.Steps)
	}
	foundPreSwap := false
	for _, st := range inv.Steps {
		if st.Phase == model.PhaseHypothesize {
			foundPreSwap = true
		}
	}
	if !foundPreSwap {
		t.Error("the pre-swap Hypothesize step was not preserved")
	}
	wantCap := confidenceThreshold - 0.01
	if inv.Confidence != wantCap {
		t.Errorf("Confidence = %v, want it capped at %v (DR-34 §34.4 point 5) despite Conclude returning 0.95", inv.Confidence, wantCap)
	}
	if inv.Status == model.InvestigationConcluded {
		t.Errorf("Status = %v, a confidence-capped conclusion below rca.confidence_threshold must never read as Concluded", inv.Status)
	}
}

// isFallbackTriggerErr wraps err as one of the recognized fallback triggers
// (ErrLLMTransport) so this test file doesn't need to depend on the exact
// wrapping LLMReasoner itself uses — it only needs SOME error
// isFallbackTrigger recognizes.
func isFallbackTriggerErr(err error) error {
	return errWrap(ErrLLMTransport, err)
}

func errWrap(sentinel, cause error) error {
	return &wrappedErr{sentinel: sentinel, cause: cause}
}

type wrappedErr struct {
	sentinel error
	cause    error
}

func (w *wrappedErr) Error() string { return w.sentinel.Error() + ": " + w.cause.Error() }
func (w *wrappedErr) Unwrap() error { return w.sentinel }

// --- no fallback wired: DR-34-trigger-shaped error still fails gracefully,
// never panics, never restarts ---

func TestInvestigate_FallbackTriggerWithNoFallbackWiredFailsGracefully(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Investigate panicked: %v", r)
		}
	}()
	registry := okRegistry()
	reasoner := &fakeReasoner{
		kind: model.ReasonerLLM,
		next: func(ctx context.Context, s State) (Proposal, error) {
			return Proposal{}, ErrLLMTransport
		},
	}
	e := newTestEngine(reasoner, registry, nil, nil) // no SetFallbackReasoner call
	inv, err := e.Investigate(context.Background(), testTenant(), testIncident())
	if err != nil {
		t.Fatalf("Investigate: %v", err)
	}
	if inv.TerminationReason != model.TermFailed {
		t.Fatalf("TerminationReason = %v, want TermFailed", inv.TerminationReason)
	}
	if inv.Status != model.InvestigationFailed {
		t.Fatalf("Status = %v, want InvestigationFailed", inv.Status)
	}
	if inv.Error == "" {
		t.Error("expected Investigation.Error to record the failure reason")
	}
	if len(inv.ReasonerSwaps) != 0 {
		t.Errorf("ReasonerSwaps = %+v, want none when no fallback is wired", inv.ReasonerSwaps)
	}
}
