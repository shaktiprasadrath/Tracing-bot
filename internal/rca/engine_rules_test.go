package rca_test

// Full-loop integration tests driving engine.Investigate with the REAL
// rules.Reasoner (not a fake). This file must be package rca_test (an
// external test package), not package rca: internal/rca/rules imports
// internal/rca, so an *internal* rca test file importing rules would be a
// genuine import cycle (rca's test-augmented package -> rules -> rca). The
// external-test-package exemption Go provides for exactly this shape (a
// package's own tests depending on a downstream consumer of that package)
// is what makes this file possible; internal/rca/engine_test.go (package
// rca) covers everything that doesn't need a real Reasoner implementation.

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"traceiq/internal/model"
	"traceiq/internal/rca"
	"traceiq/internal/rca/rules"
)

// fakeRegistry (local to this package) lets each test control exactly what
// each of the four implemented rules' tool call returns.
type fakeRegistry struct {
	dispatch func(ctx context.Context, tid model.TenantID, a rca.ToolArgs) (model.ToolResult, error)
}

func (f *fakeRegistry) Get(model.ToolName) (rca.Tool, bool) { return nil, false }
func (f *fakeRegistry) Names() []model.ToolName {
	return []model.ToolName{rca.ToolTraceQuery, rca.ToolLogQuery, rca.ToolMetricQuery, rca.ToolTopologyQuery, rca.ToolMemoryQuery}
}
func (f *fakeRegistry) Dispatch(ctx context.Context, tid model.TenantID, a rca.ToolArgs) (model.ToolResult, error) {
	return f.dispatch(ctx, tid, a)
}

var _ rca.ToolRegistry = (*fakeRegistry)(nil)

func testTenant() model.TenantID { return model.TenantID("t1") }

func testIncident() model.Incident {
	return model.Incident{ID: "inc-1", EpicenterService: "checkout", Fingerprint: "fp1:abc"}
}

func rowsJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

// refutingDeployRows / refutingPoolRows / refutingTraceRows are evidence
// shaped so none of the four catalog rules confirm on it — used to let a
// later rule in priority order be reached without an earlier one firing
// first, and (on its own) to exercise the "whole catalog exhausted, no
// false positive" path.
func refutingDeployRows() []map[string]any { return []map[string]any{} }
func refutingPoolRows() []map[string]any {
	return []map[string]any{{"service": "checkout", "pool_wait_trend_up": false, "exhaustion_log_matches": 0}}
}
func refutingTraceRows() []map[string]any {
	// Shaped to refute BOTH downstream-latency-propagation (low child ratio)
	// and n-plus-one-span-pattern (sibling count under threshold) at once,
	// since both rules query the same tool with the same args shape and so
	// share this fixture within a single catalog pass.
	return []map[string]any{{
		"service": "checkout", "operation": "POST /pay", "child_service": "payments",
		"self_millis": 90.0, "child_millis": 10.0, "total_millis": 100.0,
		"parent_span_id": "sp1", "statement_template": "SELECT 1", "count": 2,
	}}
}

// newRulesRegistry dispatches deploy-correlation / pool / trace rows by
// tool + template so a test can refute the earlier rules in priority order
// while confirming exactly one target rule.
func newRulesRegistry(t *testing.T, deployRows, poolRows, traceRows []map[string]any) *fakeRegistry {
	return &fakeRegistry{dispatch: func(ctx context.Context, tid model.TenantID, a rca.ToolArgs) (model.ToolResult, error) {
		switch a.Tool {
		case rca.ToolMetricQuery:
			switch a.Metric.TemplateID {
			case "error_signature_deploy_correlation":
				return model.ToolResult{Rows: rowsJSON(t, deployRows), Tool: a.Tool, ObservedAt: time.Now()}, nil
			case "pool_wait_and_exhaustion_logs":
				return model.ToolResult{Rows: rowsJSON(t, poolRows), Tool: a.Tool, ObservedAt: time.Now()}, nil
			}
		case rca.ToolTraceQuery:
			return model.ToolResult{Rows: rowsJSON(t, traceRows), Tool: a.Tool, ObservedAt: time.Now()}, nil
		}
		return model.ToolResult{Rows: []byte(`[]`), Tool: a.Tool, ObservedAt: time.Now()}, nil
	}}
}

func newRealEngine(registry rca.ToolRegistry) rca.Engine {
	journal := rca.NewMemJournal()
	objects := rca.NewMemObjectStore()
	return rca.NewEngine(journal, registry, rules.New(), objects, nil, nil)
}

// --- (a) each implemented rule fires correctly given matching evidence ---

func TestFullLoop_ConnectionPoolExhaustionFires(t *testing.T) {
	registry := newRulesRegistry(t,
		refutingDeployRows(),
		[]map[string]any{{"service": "checkout", "pool_wait_trend_up": true, "exhaustion_log_matches": 5}},
		refutingTraceRows(),
	)
	e := newRealEngine(registry)

	inv, err := e.Investigate(context.Background(), testTenant(), testIncident())
	if err != nil {
		t.Fatalf("Investigate: %v", err)
	}
	if inv.Status != model.InvestigationConcluded {
		t.Fatalf("Status = %v, want Concluded (report:\n%s)", inv.Status, inv.ReportMarkdown)
	}
	if inv.Confidence < 0.75 {
		t.Errorf("Confidence = %v, want >= 0.75", inv.Confidence)
	}
	var supported *model.Hypothesis
	for i := range inv.Hypotheses {
		if inv.Hypotheses[i].Status == model.HypothesisSupported {
			supported = &inv.Hypotheses[i]
		}
	}
	if supported == nil {
		t.Fatal("expected exactly one Supported hypothesis")
	}
	if supported.ID != "connection-pool-exhaustion" {
		t.Errorf("supported hypothesis = %q, want connection-pool-exhaustion", supported.ID)
	}
	if supported.Category != model.CatResourceExhaustion {
		t.Errorf("Category = %v, want CatResourceExhaustion", supported.Category)
	}
	if supported.Component != "checkout" {
		t.Errorf("Component = %q, want checkout", supported.Component)
	}
}

func TestFullLoop_DownstreamLatencyPropagationFires(t *testing.T) {
	registry := newRulesRegistry(t,
		refutingDeployRows(),
		refutingPoolRows(),
		[]map[string]any{{
			"service": "checkout", "operation": "POST /pay", "child_service": "payments",
			"self_millis": 5.0, "child_millis": 95.0, "total_millis": 100.0,
			"parent_span_id": "sp1", "statement_template": "SELECT 1", "count": 1,
		}},
	)
	e := newRealEngine(registry)
	inv, err := e.Investigate(context.Background(), testTenant(), testIncident())
	if err != nil {
		t.Fatalf("Investigate: %v", err)
	}
	if inv.Status != model.InvestigationConcluded {
		t.Fatalf("Status = %v, want Concluded (report:\n%s)", inv.Status, inv.ReportMarkdown)
	}
	if inv.RootCause == "" {
		t.Fatal("expected a non-empty RootCause")
	}
	var supported *model.Hypothesis
	for i := range inv.Hypotheses {
		if inv.Hypotheses[i].Status == model.HypothesisSupported {
			supported = &inv.Hypotheses[i]
		}
	}
	if supported == nil || supported.ID != "downstream-latency-propagation" {
		t.Fatalf("expected downstream-latency-propagation to be the supported hypothesis, got %+v", inv.Hypotheses)
	}
}

// --- (a) no false positive: no rule fires without matching evidence ---

func TestFullLoop_NoMatchingEvidenceIsInconclusiveNotAFalsePositive(t *testing.T) {
	registry := newRulesRegistry(t, refutingDeployRows(), refutingPoolRows(), refutingTraceRows())
	e := newRealEngine(registry)

	inv, err := e.Investigate(context.Background(), testTenant(), testIncident())
	if err != nil {
		t.Fatalf("Investigate: %v", err)
	}
	if inv.Status != model.InvestigationInconclusive {
		t.Fatalf("Status = %v, want Inconclusive (report:\n%s)", inv.Status, inv.ReportMarkdown)
	}
	if inv.RootCause != "" {
		t.Errorf("RootCause = %q, want empty — no rule should have fired on non-matching evidence", inv.RootCause)
	}
	for _, h := range inv.Hypotheses {
		if h.Status == model.HypothesisSupported {
			t.Errorf("hypothesis %q is Supported despite refuting evidence: %+v", h.ID, h)
		}
	}
	// All 4 implemented rules should have been tried and refuted.
	if len(inv.Hypotheses) != 4 {
		t.Errorf("len(Hypotheses) = %d, want 4 (all catalog rules tried)", len(inv.Hypotheses))
	}
}

// --- (c) step persistence: real tool-call inputs + result hash + result
// ref + reasoner output per DR-18 §18.1 (AC-F06-18) ---

func TestFullLoop_StepPersistsInputsHashesAndReasonerOutput(t *testing.T) {
	registry := newRulesRegistry(t, refutingDeployRows(), refutingPoolRows(), refutingTraceRows())
	e := newRealEngine(registry)
	inv, err := e.Investigate(context.Background(), testTenant(), testIncident())
	if err != nil {
		t.Fatalf("Investigate: %v", err)
	}

	var toolSteps int
	for _, st := range inv.Steps {
		if st.Tool == "" {
			continue // pure-reasoning step; DR-18 doesn't require tool fields here
		}
		toolSteps++
		if st.ToolArgsJSON == "" {
			t.Errorf("step %d (%s): missing ToolArgsJSON", st.Seq, st.Tool)
		}
		if st.ToolArgsHash == "" {
			t.Errorf("step %d (%s): missing ToolArgsHash", st.Seq, st.Tool)
		}
		if st.Verdict == model.VerdictOK {
			if st.ToolResultHash == "" {
				t.Errorf("step %d (%s): ok verdict but missing ToolResultHash", st.Seq, st.Tool)
			}
			if st.ToolResultRef == "" {
				t.Errorf("step %d (%s): ok verdict but missing ToolResultRef", st.Seq, st.Tool)
			}
		}
		if st.ReasonerOutputRef == "" || st.ReasonerOutputHash == "" {
			t.Errorf("step %d (%s): missing ReasonerOutputRef/Hash", st.Seq, st.Tool)
		}
	}
	if toolSteps == 0 {
		t.Fatal("expected at least one tool-dispatching step across the 4-rule catalog")
	}
}

// --- (d) replay reproduces identical steps from recorded results (AC-F06-15) ---

func TestReplayRecorded_Deterministic(t *testing.T) {
	registry := newRulesRegistry(t,
		refutingDeployRows(), refutingPoolRows(),
		[]map[string]any{{
			"service": "checkout", "operation": "POST /pay", "child_service": "payments",
			"self_millis": 5.0, "child_millis": 95.0, "total_millis": 100.0,
			"parent_span_id": "sp1", "statement_template": "SELECT 1", "count": 1,
		}},
	)
	e := newRealEngine(registry)
	original, err := e.Investigate(context.Background(), testTenant(), testIncident())
	if err != nil {
		t.Fatalf("Investigate: %v", err)
	}

	replay1, err := e.Replay(context.Background(), testTenant(), original.ID, rca.ReplayRecorded)
	if err != nil {
		t.Fatalf("Replay #1: %v", err)
	}
	replay2, err := e.Replay(context.Background(), testTenant(), original.ID, rca.ReplayRecorded)
	if err != nil {
		t.Fatalf("Replay #2: %v", err)
	}

	if replay1.ReplayOf != original.ID || replay2.ReplayOf != original.ID {
		t.Errorf("ReplayOf not set to the original investigation ID")
	}
	if replay1.Spend != (model.Spend{}) {
		t.Errorf("ReplayRecorded must spend zero: got %+v", replay1.Spend)
	}
	if len(replay1.Steps) != len(original.Steps) || len(replay2.Steps) != len(original.Steps) {
		t.Fatalf("step count mismatch: original=%d replay1=%d replay2=%d", len(original.Steps), len(replay1.Steps), len(replay2.Steps))
	}
	for i := range original.Steps {
		a, b := replay1.Steps[i], replay2.Steps[i]
		if a.ToolArgsJSON != b.ToolArgsJSON || a.ToolArgsHash != b.ToolArgsHash ||
			a.ToolResultHash != b.ToolResultHash || a.ToolResultRef != b.ToolResultRef ||
			a.Tool != b.Tool || a.Verdict != b.Verdict || a.Phase != b.Phase || a.Seq != b.Seq {
			t.Errorf("step %d not byte-identical (modulo ID/timestamps) between the two replays:\n  a=%+v\n  b=%+v", i, a, b)
		}
		// And identical to the original recording too.
		orig := original.Steps[i]
		if a.ToolResultHash != orig.ToolResultHash || a.ToolArgsHash != orig.ToolArgsHash {
			t.Errorf("step %d hash diverged from the original recording", i)
		}
	}
}

// --- (d) a corrupted evidence blob aborts replay (AC-F06-17) ---

func TestReplayRecorded_CorruptEvidenceAborts(t *testing.T) {
	registry := newRulesRegistry(t,
		refutingDeployRows(), refutingPoolRows(),
		[]map[string]any{{
			"service": "checkout", "operation": "POST /pay", "child_service": "payments",
			"self_millis": 5.0, "child_millis": 95.0, "total_millis": 100.0,
			"parent_span_id": "sp1", "statement_template": "SELECT 1", "count": 1,
		}},
	)
	objects := rca.NewMemObjectStore()
	journal := rca.NewMemJournal()
	e := rca.NewEngine(journal, registry, rules.New(), objects, nil, nil)

	original, err := e.Investigate(context.Background(), testTenant(), testIncident())
	if err != nil {
		t.Fatalf("Investigate: %v", err)
	}
	steps, err := journal.Steps(context.Background(), testTenant(), original.ID)
	if err != nil {
		t.Fatalf("Steps: %v", err)
	}
	var corrupted bool
	for _, st := range steps {
		if st.ToolResultRef != "" {
			if err := objects.Put(context.Background(), testTenant(), st.ToolResultRef, []byte(`{"corrupted":true}`)); err != nil {
				t.Fatalf("Put: %v", err)
			}
			corrupted = true
			break
		}
	}
	if !corrupted {
		t.Fatal("test setup: no step with a ToolResultRef found to corrupt")
	}

	_, err = e.Replay(context.Background(), testTenant(), original.ID, rca.ReplayRecorded)
	if !errors.Is(err, rca.ErrEvidenceCorrupt) {
		t.Fatalf("err = %v, want ErrEvidenceCorrupt", err)
	}
}
