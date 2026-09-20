package rca_test

// Full-loop tests driving engine.Investigate with the REAL rca.LLMReasoner
// (backed by llm.MockClient — no network calls) and, for the fallback
// tests, the REAL rules.Reasoner as the swap target (rca cannot construct
// rules.New() itself — see engine.go's SetFallbackReasoner doc comment —
// so only an external test package wired to both can exercise the real
// swap end to end). package rca_test per engine_rules_test.go's own doc
// comment: internal/rca/rules imports internal/rca, so this file importing
// both is only representable as an external test package.

import (
	"context"
	"errors"
	"testing"

	"traceiq/internal/llm"
	"traceiq/internal/model"
	"traceiq/internal/rca"
	"traceiq/internal/rca/rules"
)

// --- (f) a successful mocked round-trip produces a valid hypothesis/tool-
// call sequence the existing engine orchestration can consume end to end ---

func TestFullLoop_LLMReasoner_HappyPathConcludes(t *testing.T) {
	mock := llm.NewMockClient()
	calls := 0
	mock.InvokeFunc = func(_ context.Context, _ model.TenantID, req llm.Request) (llm.Response, error) {
		calls++
		if calls == 1 {
			return llm.Response{
				StopReason: llm.StopToolUse,
				Content: []byte(`{"phase":"test","done":false,"rationale":"testing pool exhaustion",` +
					`"hypotheses":[{"id":"h1","statement":"pool exhaustion suspected","category":"resource_exhaustion","component":"checkout","post_score":0.3,"status":"testing"}],` +
					`"tool_call":{"tool":"metric_query","metric":{"TemplateID":"pool_wait_and_exhaustion_logs","Params":{"service":"checkout"}}}}`),
				Usage: llm.Usage{InputTokens: 1000, OutputTokens: 100},
			}, nil
		}
		return llm.Response{
			StopReason: llm.StopEndTurn,
			Content: []byte(`{"phase":"validate","done":true,"rationale":"confirmed",` +
				`"hypotheses":[{"id":"h1","statement":"pool exhaustion confirmed","category":"resource_exhaustion","component":"checkout","post_score":0.9,"status":"supported"}],` +
				`"tool_call":null}`),
			Usage: llm.Usage{InputTokens: 500, OutputTokens: 50},
		}, nil
	}

	registry := &fakeRegistry{dispatch: func(ctx context.Context, tid model.TenantID, a rca.ToolArgs) (model.ToolResult, error) {
		return model.ToolResult{Rows: []byte(`[{"service":"checkout"}]`), Tool: a.Tool}, nil
	}}
	journal := rca.NewMemJournal()
	objects := rca.NewMemObjectStore()
	e := rca.NewEngine(journal, registry, &rca.LLMReasoner{Client: mock}, objects, nil, nil)

	inv, err := e.Investigate(context.Background(), testTenant(), testIncident())
	if err != nil {
		t.Fatalf("Investigate: %v", err)
	}
	if inv.ReasonerKind != model.ReasonerLLM {
		t.Fatalf("ReasonerKind = %v, want llm", inv.ReasonerKind)
	}
	if inv.Status != model.InvestigationConcluded {
		t.Fatalf("Status = %v, want Concluded (report:\n%s)", inv.Status, inv.ReportMarkdown)
	}
	if inv.Confidence != 0.9 {
		t.Errorf("Confidence = %v, want 0.9", inv.Confidence)
	}
	if inv.Spend.TokensIn != 1500 || inv.Spend.TokensOut != 150 {
		t.Errorf("Spend = %+v, want TokensIn=1500 TokensOut=150 (both calls' usage folded in)", inv.Spend)
	}
	var toolStep *model.Step
	for i := range inv.Steps {
		if inv.Steps[i].Tool == rca.ToolMetricQuery {
			toolStep = &inv.Steps[i]
		}
	}
	if toolStep == nil {
		t.Fatal("expected a metric_query step in the persisted sequence")
	}
	if toolStep.ToolArgsJSON == "" || toolStep.ToolArgsHash == "" || toolStep.ToolResultHash == "" {
		t.Errorf("tool step missing persisted args/result hashes (DR-18 §18.1): %+v", toolStep)
	}
}

// --- (e) LLM transport error mid-loop swaps to the REAL rules reasoner,
// preserves prior evidence, and still reaches a graceful conclusion ---

func TestFullLoop_LLMReasoner_TransportFailureFallsBackToRealRulesReasoner(t *testing.T) {
	mock := llm.NewMockClient()
	mock.Errors = []error{errors.New("connection refused")} // repeats every call (MockClient clamps to last)

	registry := newRulesRegistry(t,
		refutingDeployRows(),
		[]map[string]any{{"service": "checkout", "pool_wait_trend_up": true, "exhaustion_log_matches": 5}}, // confirms connection-pool-exhaustion
		refutingTraceRows(),
	)
	journal := rca.NewMemJournal()
	objects := rca.NewMemObjectStore()
	e := rca.NewEngine(journal, registry, &rca.LLMReasoner{Client: mock}, objects, nil, nil)
	e.SetFallbackReasoner(rules.New())

	inv, err := e.Investigate(context.Background(), testTenant(), testIncident())
	if err != nil {
		t.Fatalf("Investigate: %v", err)
	}

	if inv.ReasonerKind != model.ReasonerRules {
		t.Fatalf("ReasonerKind = %v, want rules after the transport-failure swap", inv.ReasonerKind)
	}
	if len(inv.ReasonerSwaps) != 1 {
		t.Fatalf("ReasonerSwaps = %+v, want exactly one swap", inv.ReasonerSwaps)
	}
	if inv.ReasonerSwaps[0].From != model.ReasonerLLM || inv.ReasonerSwaps[0].To != model.ReasonerRules {
		t.Errorf("swap = %+v, want From=llm To=rules", inv.ReasonerSwaps[0])
	}
	// The rules catalog's connection-pool-exhaustion rule confirms at 0.8,
	// but DR-34 §34.4 point 5 caps a post-swap conclusion below
	// rca.confidence_threshold (0.75) — so this must read Inconclusive, not
	// Concluded, despite the rule genuinely firing.
	wantCap := 0.75 - 0.01
	if inv.Confidence != wantCap {
		t.Errorf("Confidence = %v, want capped at %v", inv.Confidence, wantCap)
	}
	if inv.Status != model.InvestigationInconclusive {
		t.Errorf("Status = %v, want Inconclusive (confidence-capped despite the rule firing)", inv.Status)
	}
	if inv.TerminationReason != model.TermConcluded {
		t.Errorf("TerminationReason = %v, want TermConcluded (the rules reasoner itself still concluded cleanly)", inv.TerminationReason)
	}
	// The Contextualize step from before the swap must survive.
	if len(inv.Steps) == 0 {
		t.Fatal("expected at least the pre-swap Contextualize step to survive")
	}
	// No LLM tool step should exist (the very first NextStep call failed
	// before any tool call was ever proposed) but at least one rules-catalog
	// tool step must.
	foundToolStep := false
	for _, st := range inv.Steps {
		if st.Tool != "" {
			foundToolStep = true
		}
	}
	if !foundToolStep {
		t.Error("expected at least one tool-dispatching step from the post-swap rules catalog")
	}
}

// --- malformed output, retried then falling back, still reaches a graceful
// terminal state through the real rules reasoner ---

func TestFullLoop_LLMReasoner_SchemaInvalidFallsBackAndConcludesGracefully(t *testing.T) {
	mock := llm.NewMockClient()
	mock.Responses = []llm.Response{{StopReason: llm.StopEndTurn, Content: []byte(`not valid json, ever`)}} // repeats
	registry := newRulesRegistry(t, refutingDeployRows(), refutingPoolRows(), refutingTraceRows())
	journal := rca.NewMemJournal()
	objects := rca.NewMemObjectStore()
	e := rca.NewEngine(journal, registry, &rca.LLMReasoner{Client: mock, MaxRetries: 1}, objects, nil, nil)
	e.SetFallbackReasoner(rules.New())

	inv, err := e.Investigate(context.Background(), testTenant(), testIncident())
	if err != nil {
		t.Fatalf("Investigate: %v", err)
	}
	if inv.ReasonerKind != model.ReasonerRules {
		t.Fatalf("ReasonerKind = %v, want rules", inv.ReasonerKind)
	}
	// No matching evidence anywhere in this fixture: the rules catalog tries
	// all 4 implemented rules and refutes every one — a graceful, non-
	// panicking, non-hanging Inconclusive outcome.
	if inv.Status != model.InvestigationInconclusive {
		t.Fatalf("Status = %v, want Inconclusive (report:\n%s)", inv.Status, inv.ReportMarkdown)
	}
	if inv.RootCause != "" {
		t.Errorf("RootCause = %q, want empty — no rule should have fired on non-matching evidence", inv.RootCause)
	}
}
