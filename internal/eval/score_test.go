package eval

import (
	"errors"
	"testing"

	"traceiq/internal/model"
)

func TestMatchRootCause(t *testing.T) {
	exp := Expectation{RootCauseCategory: model.CatDeployRegression, RootCauseComponent: "checkout"}
	cases := []struct {
		name string
		h    model.Hypothesis
		want bool
	}{
		{"exact match", model.Hypothesis{Category: model.CatDeployRegression, Component: "checkout"}, true},
		{"wrong component", model.Hypothesis{Category: model.CatDeployRegression, Component: "orders"}, false},
		{"wrong category", model.Hypothesis{Category: model.CatResourceExhaustion, Component: "checkout"}, false},
		{"both wrong", model.Hypothesis{Category: model.CatNetwork, Component: "orders"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := matchRootCause(tc.h, exp); got != tc.want {
				t.Errorf("matchRootCause() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestScoreRootCause is FR-F11-4's table-driven scoring test (hand-
// constructed rca.Hypothesis fixtures with known expected scores — no
// shadow types, per F11 §7).
func TestScoreRootCause(t *testing.T) {
	exp := Expectation{RootCauseCategory: model.CatDeployRegression, RootCauseComponent: "checkout"}

	t.Run("top1 match", func(t *testing.T) {
		hyps := []model.Hypothesis{
			{ID: "a", Category: model.CatDeployRegression, Component: "checkout", PostScore: 0.9},
			{ID: "b", Category: model.CatNetwork, Component: "orders", PostScore: 0.3},
		}
		top1, top3, partial := scoreRootCause(hyps, exp)
		if !top1 || !top3 || partial != 1.0 {
			t.Errorf("got top1=%v top3=%v partial=%v, want true/true/1.0", top1, top3, partial)
		}
	})

	t.Run("top3 only (correct answer ranked 3rd)", func(t *testing.T) {
		hyps := []model.Hypothesis{
			{ID: "a", Category: model.CatNetwork, Component: "orders", PostScore: 0.9},
			{ID: "b", Category: model.CatResourceExhaustion, Component: "orders", PostScore: 0.5},
			{ID: "c", Category: model.CatDeployRegression, Component: "checkout", PostScore: 0.4},
		}
		top1, top3, partial := scoreRootCause(hyps, exp)
		if top1 {
			t.Error("expected top1=false")
		}
		if !top3 {
			t.Error("expected top3=true")
		}
		// F11 §4.4's literal algorithm: partial = 1.0 iff top1, else 0.5 iff
		// any top-3 hypothesis's Category matches (which a full top-3-only
		// match trivially satisfies) — top3=true does not itself imply
		// partial=1.0 unless it's also top1.
		if partial != 0.5 {
			t.Errorf("partial = %v, want 0.5 (top3 match at rank>1, not top1)", partial)
		}
	})

	t.Run("category-only match within top3 (wrong component)", func(t *testing.T) {
		hyps := []model.Hypothesis{
			{ID: "a", Category: model.CatDeployRegression, Component: "orders", PostScore: 0.8},
		}
		top1, top3, partial := scoreRootCause(hyps, exp)
		if top1 || top3 {
			t.Errorf("got top1=%v top3=%v, want both false (component mismatch)", top1, top3)
		}
		if partial != 0.5 {
			t.Errorf("partial = %v, want 0.5 for a category-only match", partial)
		}
	})

	t.Run("4th place match outside top3 scores nothing", func(t *testing.T) {
		hyps := []model.Hypothesis{
			{ID: "a", Category: model.CatNetwork, Component: "x", PostScore: 0.9},
			{ID: "b", Category: model.CatResourceExhaustion, Component: "y", PostScore: 0.8},
			{ID: "c", Category: model.CatSaturation, Component: "z", PostScore: 0.7},
			{ID: "d", Category: model.CatDeployRegression, Component: "checkout", PostScore: 0.1},
		}
		top1, top3, partial := scoreRootCause(hyps, exp)
		if top1 || top3 || partial != 0 {
			t.Errorf("got top1=%v top3=%v partial=%v, want false/false/0", top1, top3, partial)
		}
	})

	t.Run("no hypotheses", func(t *testing.T) {
		top1, top3, partial := scoreRootCause(nil, exp)
		if top1 || top3 || partial != 0 {
			t.Errorf("got top1=%v top3=%v partial=%v, want false/false/0", top1, top3, partial)
		}
	})
}

// TestScoreEvidence is FR-F11-6's table-driven scoring test (hand-
// constructed model.Evidence fixtures against a closed EvidenceCategory
// expectation).
func TestScoreEvidence(t *testing.T) {
	steps := []model.Step{
		{ID: "st1", EvidenceIDs: []string{"ev1", "ev2"}},
		{ID: "st2", EvidenceIDs: []string{"ev3"}},
	}
	evidence := []model.Evidence{
		{ID: "ev1", Category: model.EvErrorSignature},
		{ID: "ev2", Category: model.EvDeployMarker},
		{ID: "ev3", Category: model.EvLogLine},
	}

	t.Run("empty expectation scores vacuously perfect", func(t *testing.T) {
		p, r := scoreEvidence(steps, evidence, nil)
		if p != 1 || r != 1 {
			t.Errorf("precision=%v recall=%v, want 1/1", p, r)
		}
	})

	t.Run("perfect precision and recall", func(t *testing.T) {
		expected := []model.EvidenceCategory{model.EvErrorSignature, model.EvDeployMarker, model.EvLogLine}
		p, r := scoreEvidence(steps, evidence, expected)
		if p != 1 || r != 1 {
			t.Errorf("precision=%v recall=%v, want 1/1", p, r)
		}
	})

	t.Run("partial recall: one expected category never surfaced", func(t *testing.T) {
		expected := []model.EvidenceCategory{model.EvErrorSignature, model.EvDeployMarker, model.EvMetricSeries}
		p, r := scoreEvidence(steps, evidence, expected)
		// got categories {ErrorSignature, DeployMarker, LogLine}; expected {ErrorSignature, DeployMarker, MetricSeries}
		// precision: 2/3 of got categories are expected; recall: 2/3 of expected categories were got.
		if p < 0.66 || p > 0.67 {
			t.Errorf("precision = %v, want ~0.667", p)
		}
		if r < 0.66 || r > 0.67 {
			t.Errorf("recall = %v, want ~0.667", r)
		}
	})

	t.Run("no evidence surfaced against a non-empty expectation scores 0/0", func(t *testing.T) {
		p, r := scoreEvidence(nil, nil, []model.EvidenceCategory{model.EvErrorSignature})
		if p != 0 || r != 0 {
			t.Errorf("precision=%v recall=%v, want 0/0", p, r)
		}
	})

	t.Run("dangling EvidenceID (no matching Evidence record) is ignored, not a panic", func(t *testing.T) {
		danglingSteps := []model.Step{{EvidenceIDs: []string{"does-not-exist"}}}
		p, r := scoreEvidence(danglingSteps, evidence, []model.EvidenceCategory{model.EvErrorSignature})
		if p != 0 || r != 0 {
			t.Errorf("precision=%v recall=%v, want 0/0 for an unresolvable evidence id", p, r)
		}
	})
}

func TestAbsoluteFloor(t *testing.T) {
	if got := absoluteFloor(model.ReasonerLLM); got != 0.70 {
		t.Errorf("llm floor = %v, want 0.70", got)
	}
	if got := absoluteFloor(model.ReasonerRules); got != 0.40 {
		t.Errorf("rules floor = %v, want 0.40", got)
	}
}

// TestCheckGates_AbsoluteFloor covers AC-F11-5's boundary cases: exactly at
// the floor passes, one point below fails.
func TestCheckGates_AbsoluteFloor(t *testing.T) {
	t.Run("exactly at the 40% rules floor passes", func(t *testing.T) {
		rep := Report{Top1Accuracy: 0.40, MeanTimeToRCAMillis: 1000}
		opts := RunOptions{Reasoner: model.ReasonerRules}
		err := checkGates(&rep, opts, nil)
		if err != nil || !rep.GatesPassed {
			t.Errorf("err=%v GatesPassed=%v, want nil/true", err, rep.GatesPassed)
		}
	})

	t.Run("one point below the 40% rules floor fails", func(t *testing.T) {
		rep := Report{Top1Accuracy: 0.35, MeanTimeToRCAMillis: 1000}
		opts := RunOptions{Reasoner: model.ReasonerRules}
		err := checkGates(&rep, opts, nil)
		if !errors.Is(err, ErrBelowAbsoluteFloor) {
			t.Errorf("err = %v, want ErrBelowAbsoluteFloor", err)
		}
		if rep.GatesPassed {
			t.Error("GatesPassed = true, want false")
		}
	})

	t.Run("mean time to RCA over the 180s ceiling fails independent of accuracy", func(t *testing.T) {
		rep := Report{Top1Accuracy: 1.0, MeanTimeToRCAMillis: 181_000}
		opts := RunOptions{Reasoner: model.ReasonerRules}
		err := checkGates(&rep, opts, nil)
		if !errors.Is(err, ErrBelowAbsoluteFloor) {
			t.Errorf("err = %v, want ErrBelowAbsoluteFloor", err)
		}
	})

	// FR-F11-11 / DR-36 §36.6: median cost <= $0.08/investigation, max cost
	// <= $0.50/investigation, independent of accuracy or baseline. Before
	// this gate existed, checkGates never inspected MedianCostMicroUSD/
	// MaxCostMicroUSD at all, so a run whose reasoner blew the published cost
	// budget still reported GatesPassed=true.
	t.Run("exactly at the $0.08 median cost ceiling passes", func(t *testing.T) {
		rep := Report{Top1Accuracy: 1.0, MedianCostMicroUSD: 80_000, MaxCostMicroUSD: 80_000}
		opts := RunOptions{Reasoner: model.ReasonerRules}
		if err := checkGates(&rep, opts, nil); err != nil || !rep.GatesPassed {
			t.Errorf("err=%v GatesPassed=%v, want nil/true", err, rep.GatesPassed)
		}
	})

	t.Run("one micro-USD over the $0.08 median cost ceiling fails", func(t *testing.T) {
		rep := Report{Top1Accuracy: 1.0, MedianCostMicroUSD: 80_001, MaxCostMicroUSD: 80_001}
		opts := RunOptions{Reasoner: model.ReasonerRules}
		err := checkGates(&rep, opts, nil)
		if !errors.Is(err, ErrBelowAbsoluteFloor) {
			t.Errorf("err = %v, want ErrBelowAbsoluteFloor", err)
		}
		if !contains(rep.FailedGates, "absolute_ceiling_median_cost") {
			t.Errorf("FailedGates = %v, want it to contain absolute_ceiling_median_cost", rep.FailedGates)
		}
	})

	t.Run("exactly at the $0.50 max cost ceiling passes", func(t *testing.T) {
		rep := Report{Top1Accuracy: 1.0, MedianCostMicroUSD: 10_000, MaxCostMicroUSD: 500_000}
		opts := RunOptions{Reasoner: model.ReasonerRules}
		if err := checkGates(&rep, opts, nil); err != nil || !rep.GatesPassed {
			t.Errorf("err=%v GatesPassed=%v, want nil/true", err, rep.GatesPassed)
		}
	})

	t.Run("one micro-USD over the $0.50 max cost ceiling fails even with cheap median", func(t *testing.T) {
		rep := Report{Top1Accuracy: 1.0, MedianCostMicroUSD: 10_000, MaxCostMicroUSD: 500_001}
		opts := RunOptions{Reasoner: model.ReasonerRules}
		err := checkGates(&rep, opts, nil)
		if !errors.Is(err, ErrBelowAbsoluteFloor) {
			t.Errorf("err = %v, want ErrBelowAbsoluteFloor", err)
		}
		if !contains(rep.FailedGates, "absolute_ceiling_max_cost") {
			t.Errorf("FailedGates = %v, want it to contain absolute_ceiling_max_cost", rep.FailedGates)
		}
	})
}

func contains(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}

// TestMedianAndMax covers report.go's medianAndMax helper (FR-F11-11's cost
// gate inputs) directly: odd/even counts and the empty-slice edge case.
func TestMedianAndMax(t *testing.T) {
	cases := []struct {
		name           string
		costs          []int64
		wantMed, wantM int64
	}{
		{"empty", nil, 0, 0},
		{"single", []int64{42}, 42, 42},
		{"odd count", []int64{30, 10, 20}, 20, 30},
		{"even count", []int64{40, 10, 20, 30}, 25, 40},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			med, max := medianAndMax(tc.costs)
			if med != tc.wantMed || max != tc.wantM {
				t.Errorf("medianAndMax(%v) = %d, %d, want %d, %d", tc.costs, med, max, tc.wantMed, tc.wantM)
			}
		})
	}
}

// TestCheckGates_Regression covers AC-F11-4's boundary cases: exactly a 5pp
// drop passes, 5.01pp fails.
func TestCheckGates_Regression(t *testing.T) {
	baseline := &Report{Top1Accuracy: 0.90, MeanTimeToRCAMillis: 10_000}
	opts := RunOptions{Reasoner: model.ReasonerRules, FailOnRegression: true}

	t.Run("exactly a 5pp drop passes", func(t *testing.T) {
		rep := Report{Top1Accuracy: 0.85, MeanTimeToRCAMillis: 10_000}
		if err := checkGates(&rep, opts, baseline); err != nil {
			t.Errorf("err = %v, want nil", err)
		}
	})

	t.Run("5.01pp drop fails", func(t *testing.T) {
		rep := Report{Top1Accuracy: 0.8499, MeanTimeToRCAMillis: 10_000}
		if err := checkGates(&rep, opts, baseline); !errors.Is(err, ErrRegressionTop1Accuracy) {
			t.Errorf("err = %v, want ErrRegressionTop1Accuracy", err)
		}
	})

	t.Run("AC-F11-4's literal example: 0.90 baseline, 0.83 current (7pp drop)", func(t *testing.T) {
		rep := Report{Top1Accuracy: 0.83, MeanTimeToRCAMillis: 10_000}
		if err := checkGates(&rep, opts, baseline); !errors.Is(err, ErrRegressionTop1Accuracy) {
			t.Errorf("err = %v, want ErrRegressionTop1Accuracy", err)
		}
	})

	t.Run("time-to-RCA +25% exactly passes, +25.1% fails", func(t *testing.T) {
		okRep := Report{Top1Accuracy: 0.90, MeanTimeToRCAMillis: 12_500}
		if err := checkGates(&okRep, opts, baseline); err != nil {
			t.Errorf("err = %v, want nil at exactly +25%%", err)
		}
		badRep := Report{Top1Accuracy: 0.90, MeanTimeToRCAMillis: 12_600}
		if err := checkGates(&badRep, opts, baseline); !errors.Is(err, ErrRegressionTimeToRCA) {
			t.Errorf("err = %v, want ErrRegressionTimeToRCA", err)
		}
	})

	t.Run("regression gate is skipped without FailOnRegression", func(t *testing.T) {
		rep := Report{Top1Accuracy: 0.50, MeanTimeToRCAMillis: 10_000}
		noFail := RunOptions{Reasoner: model.ReasonerRules, FailOnRegression: false}
		if err := checkGates(&rep, noFail, baseline); err != nil {
			t.Errorf("err = %v, want nil (regression gate off; 0.50 still clears the 40%% absolute floor)", err)
		}
	})
}
