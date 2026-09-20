package rules

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"traceiq/internal/model"
	"traceiq/internal/rca"
)

// --- helpers ---

func mustRows(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	return b
}

func result(rows []byte) model.ToolResult {
	return model.ToolResult{Rows: rows}
}

// F06 §7: "One fixture-driven test per rule in the rules catalog (positive +
// negative cases), asserting the stamped Category/Component." Component is
// stamped by Reasoner.NextStep (see TestReasoner_StampsComponent below), not
// by Rule.Confirm itself, so Category is asserted here and Component in the
// Reasoner-level test.

func TestErrorSigAfterDeploy(t *testing.T) {
	rule := errorSigAfterDeploy{}
	if got := rule.Category(); got != model.CatDeployRegression {
		t.Fatalf("Category() = %v, want CatDeployRegression", got)
	}

	deployAt := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	t.Run("positive: first-seen shortly after deploy", func(t *testing.T) {
		rows := mustRows(t, []DeployCorrelationRow{{
			ErrorSigID: "es1",
			Service:    "checkout",
			FirstSeen:  deployAt.Add(5 * time.Minute),
			DeployAt:   deployAt,
		}})
		confirmed, conf, finding := rule.Confirm(result(rows))
		if !confirmed {
			t.Fatalf("expected confirmed, got not-confirmed (finding=%q)", finding)
		}
		if conf != 0.85 {
			t.Errorf("confidence = %v, want 0.85", conf)
		}
		if finding == "" {
			t.Error("expected a non-empty finding")
		}
	})

	t.Run("negative: first-seen before deploy", func(t *testing.T) {
		rows := mustRows(t, []DeployCorrelationRow{{
			ErrorSigID: "es1",
			Service:    "checkout",
			FirstSeen:  deployAt.Add(-5 * time.Minute),
			DeployAt:   deployAt,
		}})
		confirmed, conf, _ := rule.Confirm(result(rows))
		if confirmed {
			t.Fatalf("expected NOT confirmed for a pre-deploy first-seen, got confirmed conf=%v", conf)
		}
	})

	t.Run("negative: first-seen outside the post-deploy window", func(t *testing.T) {
		rows := mustRows(t, []DeployCorrelationRow{{
			ErrorSigID: "es1",
			Service:    "checkout",
			FirstSeen:  deployAt.Add(2 * time.Hour),
			DeployAt:   deployAt,
		}})
		confirmed, _, _ := rule.Confirm(result(rows))
		if confirmed {
			t.Fatal("expected NOT confirmed for a first-seen far outside the 30m post-deploy window")
		}
	})

	t.Run("negative: no rows", func(t *testing.T) {
		confirmed, conf, finding := rule.Confirm(result(mustRows(t, []DeployCorrelationRow{})))
		if confirmed {
			t.Fatal("expected NOT confirmed for empty rows")
		}
		if conf != 0 || finding == "" {
			t.Errorf("expected zero confidence and a finding, got conf=%v finding=%q", conf, finding)
		}
	})

	t.Run("negative: empty/unparseable result", func(t *testing.T) {
		confirmed, _, _ := rule.Confirm(result(nil))
		if confirmed {
			t.Fatal("expected NOT confirmed for an empty result body")
		}
	})

	t.Run("BuildCall shape", func(t *testing.T) {
		call := rule.BuildCall(model.Incident{EpicenterService: "checkout"})
		if call.Tool != rca.ToolMetricQuery || call.Metric == nil {
			t.Fatalf("expected a metric_query call, got %+v", call)
		}
		if call.Metric.TemplateID != "error_signature_deploy_correlation" {
			t.Errorf("TemplateID = %q", call.Metric.TemplateID)
		}
		if call.Metric.Params["service"] != "checkout" {
			t.Errorf("Params[service] = %q, want checkout", call.Metric.Params["service"])
		}
	})
}

func TestDownstreamLatencyPropagation(t *testing.T) {
	rule := downstreamLatencyPropagation{}
	if got := rule.Category(); got != model.CatDependencyFailure {
		t.Fatalf("Category() = %v, want CatDependencyFailure", got)
	}

	t.Run("positive: child dominates total duration", func(t *testing.T) {
		rows := mustRows(t, []SpanTimingRow{{
			Service: "checkout", Operation: "POST /pay", ChildService: "payments",
			SelfMillis: 10, ChildMillis: 90, TotalMillis: 100,
		}})
		confirmed, conf, finding := rule.Confirm(result(rows))
		if !confirmed {
			t.Fatalf("expected confirmed, finding=%q", finding)
		}
		if conf != 0.8 {
			t.Errorf("confidence = %v, want 0.8", conf)
		}
	})

	t.Run("negative: self-time dominates", func(t *testing.T) {
		rows := mustRows(t, []SpanTimingRow{{
			Service: "checkout", Operation: "POST /pay", ChildService: "payments",
			SelfMillis: 90, ChildMillis: 10, TotalMillis: 100,
		}})
		confirmed, _, _ := rule.Confirm(result(rows))
		if confirmed {
			t.Fatal("expected NOT confirmed when self-time dominates")
		}
	})

	t.Run("negative: zero total duration is skipped, not a div-by-zero panic", func(t *testing.T) {
		rows := mustRows(t, []SpanTimingRow{{
			Service: "checkout", TotalMillis: 0, ChildMillis: 5,
		}})
		confirmed, _, _ := rule.Confirm(result(rows))
		if confirmed {
			t.Fatal("expected NOT confirmed for a zero-duration row")
		}
	})

	t.Run("BuildCall shape", func(t *testing.T) {
		call := rule.BuildCall(model.Incident{EpicenterService: "checkout"})
		if call.Tool != rca.ToolTraceQuery || call.Trace == nil {
			t.Fatalf("expected a trace_query call, got %+v", call)
		}
		if call.Trace.Service != "checkout" {
			t.Errorf("Trace.Service = %q", call.Trace.Service)
		}
	})
}

func TestNPlusOneSpanPattern(t *testing.T) {
	rule := nPlusOneSpanPattern{threshold: 10}
	if got := rule.Category(); got != model.CatDataSkew {
		t.Fatalf("Category() = %v, want CatDataSkew", got)
	}

	t.Run("positive: sibling count at threshold", func(t *testing.T) {
		rows := mustRows(t, []SiblingSpanGroupRow{{
			ParentSpanID: "sp1", Service: "orders", Operation: "SELECT", StatementTemplate: "SELECT * FROM x WHERE id=?", Count: 10,
		}})
		confirmed, conf, finding := rule.Confirm(result(rows))
		if !confirmed {
			t.Fatalf("expected confirmed at threshold, finding=%q", finding)
		}
		if conf < 0.7 || conf > 0.95 {
			t.Errorf("confidence out of expected range: %v", conf)
		}
	})

	t.Run("positive: confidence clamps at 0.95 for very large counts", func(t *testing.T) {
		rows := mustRows(t, []SiblingSpanGroupRow{{Count: 1000}})
		confirmed, conf, _ := rule.Confirm(result(rows))
		if !confirmed {
			t.Fatal("expected confirmed for a large sibling count")
		}
		if conf != 0.95 {
			t.Errorf("confidence = %v, want clamped 0.95", conf)
		}
	})

	t.Run("negative: sibling count below threshold", func(t *testing.T) {
		rows := mustRows(t, []SiblingSpanGroupRow{{Count: 3}})
		confirmed, _, _ := rule.Confirm(result(rows))
		if confirmed {
			t.Fatal("expected NOT confirmed below the N+1 threshold")
		}
	})

	t.Run("zero-value threshold defaults to 10", func(t *testing.T) {
		r := nPlusOneSpanPattern{} // threshold left unset
		rows := mustRows(t, []SiblingSpanGroupRow{{Count: 9}})
		if confirmed, _, _ := r.Confirm(result(rows)); confirmed {
			t.Fatal("expected NOT confirmed for count 9 against the default threshold 10")
		}
		rows = mustRows(t, []SiblingSpanGroupRow{{Count: 10}})
		if confirmed, _, _ := r.Confirm(result(rows)); !confirmed {
			t.Fatal("expected confirmed for count 10 against the default threshold 10")
		}
	})
}

func TestConnectionPoolExhaustion(t *testing.T) {
	rule := connectionPoolExhaustion{}
	if got := rule.Category(); got != model.CatResourceExhaustion {
		t.Fatalf("Category() = %v, want CatResourceExhaustion", got)
	}

	t.Run("positive: rising wait time with exhaustion logs", func(t *testing.T) {
		rows := mustRows(t, []PoolMetricRow{{Service: "orders", PoolWaitTrendUp: true, ExhaustionLogMatches: 3}})
		confirmed, conf, finding := rule.Confirm(result(rows))
		if !confirmed {
			t.Fatalf("expected confirmed, finding=%q", finding)
		}
		if conf != 0.8 {
			t.Errorf("confidence = %v, want 0.8", conf)
		}
	})

	t.Run("negative: trend up but no log matches", func(t *testing.T) {
		rows := mustRows(t, []PoolMetricRow{{Service: "orders", PoolWaitTrendUp: true, ExhaustionLogMatches: 0}})
		confirmed, _, _ := rule.Confirm(result(rows))
		if confirmed {
			t.Fatal("expected NOT confirmed without exhaustion log matches")
		}
	})

	t.Run("negative: log matches but no trend", func(t *testing.T) {
		rows := mustRows(t, []PoolMetricRow{{Service: "orders", PoolWaitTrendUp: false, ExhaustionLogMatches: 5}})
		confirmed, _, _ := rule.Confirm(result(rows))
		if confirmed {
			t.Fatal("expected NOT confirmed without a rising wait-time trend")
		}
	})

	t.Run("BuildCall shape", func(t *testing.T) {
		call := rule.BuildCall(model.Incident{EpicenterService: "orders"})
		if call.Tool != rca.ToolMetricQuery || call.Metric == nil || call.Metric.TemplateID != "pool_wait_and_exhaustion_logs" {
			t.Fatalf("unexpected BuildCall shape: %+v", call)
		}
	})
}

// TestReasoner_StampsComponent covers the half of F06 §7's "stamped
// Category/Component" requirement that lives in the Reasoner, not the Rule:
// NextStep's proposed Hypothesis.Component always comes from
// Incident.EpicenterService.
func TestReasoner_StampsComponent(t *testing.T) {
	r := New()
	inc := model.Incident{ID: "inc1", EpicenterService: "checkout"}
	prop, err := r.NextStep(context.Background(), "t1", rca.State{Incident: inc})
	if err != nil {
		t.Fatalf("NextStep: %v", err)
	}
	if len(prop.Hypotheses) != 1 {
		t.Fatalf("expected exactly one proposed hypothesis, got %d", len(prop.Hypotheses))
	}
	if prop.Hypotheses[0].Component != "checkout" {
		t.Errorf("Component = %q, want %q", prop.Hypotheses[0].Component, "checkout")
	}
	if prop.Hypotheses[0].ID != catalog[0].ID() {
		t.Errorf("first proposed rule = %q, want catalog[0] = %q", prop.Hypotheses[0].ID, catalog[0].ID())
	}
	if prop.Call == nil || prop.Call.Tool != catalog[0].BuildCall(inc).Tool {
		t.Error("expected NextStep's first proposal to carry catalog[0]'s BuildCall")
	}
}

// TestReasoner_NoMoreRules confirms the catalog-exhausted path (F06 §4.4's
// NextStep_rules "candidates empty" case) once every catalog rule's ID is
// already present in Investigation.Hypotheses.
func TestReasoner_NoMoreRules(t *testing.T) {
	r := New()
	var hyps []model.Hypothesis
	for _, rule := range catalog {
		hyps = append(hyps, model.Hypothesis{ID: rule.ID(), Status: model.HypothesisRefuted})
	}
	inv := model.Investigation{Hypotheses: hyps}
	_, err := r.NextStep(context.Background(), "t1", rca.State{Investigation: inv})
	if err != rca.ErrNoMoreRules {
		t.Fatalf("err = %v, want rca.ErrNoMoreRules", err)
	}
}

// TestReasoner_EvaluatesFoldsInAndConcludes drives NextStep's LastResult fold-
// in path directly (the mechanism engine.go's State doc comment describes)
// and checks Conclude() picks the highest-PostScore supported hypothesis.
func TestReasoner_EvaluatesFoldsInAndConcludes(t *testing.T) {
	r := New()
	inc := model.Incident{ID: "inc1", EpicenterService: "checkout"}

	rows := mustRows(t, []DeployCorrelationRow{{
		ErrorSigID: "es1", Service: "checkout",
		FirstSeen: time.Unix(1000, 0), DeployAt: time.Unix(900, 0),
	}})
	state := rca.State{
		Incident:         inc,
		LastResult:       &model.ToolResult{Rows: rows},
		LastHypothesisID: catalog[0].ID(), // errorSigAfterDeploy
	}
	prop, err := r.NextStep(context.Background(), "t1", state)
	if err != nil {
		t.Fatalf("NextStep (fold-in): %v", err)
	}
	if !prop.Done {
		t.Fatalf("expected Done=true once a rule confirms above threshold, got %+v", prop)
	}
	if prop.Call != nil {
		t.Error("expected a pure fold-in proposal (Call == nil)")
	}
	if len(prop.Hypotheses) != 1 || prop.Hypotheses[0].Status != model.HypothesisSupported {
		t.Fatalf("expected exactly one Supported hypothesis update, got %+v", prop.Hypotheses)
	}

	concl, err := r.Conclude(context.Background(), "t1", rca.State{
		Investigation: model.Investigation{Hypotheses: prop.Hypotheses},
	})
	if err != nil {
		t.Fatalf("Conclude: %v", err)
	}
	if concl.Confidence != prop.Hypotheses[0].PostScore {
		t.Errorf("Confidence = %v, want %v", concl.Confidence, prop.Hypotheses[0].PostScore)
	}
	if concl.RootCause == "" {
		t.Error("expected a non-empty RootCause")
	}
}

// TestReasoner_ConcludeInconclusiveWithoutASupportedHypothesis is the
// important "no false positives" guarantee at the Conclude layer: with only
// refuted hypotheses on record, Conclude must not manufacture a root cause.
func TestReasoner_ConcludeInconclusiveWithoutASupportedHypothesis(t *testing.T) {
	r := New()
	inv := model.Investigation{Hypotheses: []model.Hypothesis{
		{ID: "a", Status: model.HypothesisRefuted, PostScore: 0},
		{ID: "b", Status: model.HypothesisTesting, PostScore: 0},
	}}
	concl, err := r.Conclude(context.Background(), "t1", rca.State{Investigation: inv})
	if err != nil {
		t.Fatalf("Conclude: %v", err)
	}
	if concl.RootCause != "" {
		t.Errorf("RootCause = %q, want empty when nothing is Supported", concl.RootCause)
	}
	if concl.Confidence != 0 {
		t.Errorf("Confidence = %v, want 0", concl.Confidence)
	}
}
