package eval

import (
	"sort"
	"time"

	"traceiq/internal/model"
)

// matchRootCause is F11 §4.4 / DR-36 §36.5's matchRootCause, verbatim: a
// hypothesis matches only when BOTH Category and Component agree with the
// expectation — no shadow ServiceOrComponent/Narrative fields, no fuzzy
// keyword overlap.
func matchRootCause(h model.Hypothesis, exp Expectation) bool {
	return h.Category == exp.RootCauseCategory && h.Component == exp.RootCauseComponent
}

// scoreRootCause implements FR-F11-4: Top1 is true iff the highest-PostScore
// hypothesis matches; Top3 is true iff any of the top 3 by PostScore match;
// PartialCredit is 1.0 on a Top1 match, 0.5 for a category-only match
// (wrong Component) within the top 3, else 0.
func scoreRootCause(hyps []model.Hypothesis, exp Expectation) (top1, top3 bool, partial float64) {
	sorted := make([]model.Hypothesis, len(hyps))
	copy(sorted, hyps)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].PostScore > sorted[j].PostScore })

	if len(sorted) > 0 && matchRootCause(sorted[0], exp) {
		top1 = true
	}

	limit := 3
	if len(sorted) < limit {
		limit = len(sorted)
	}
	categoryOnly := false
	for i := 0; i < limit; i++ {
		if matchRootCause(sorted[i], exp) {
			top3 = true
		}
		if sorted[i].Category == exp.RootCauseCategory {
			categoryOnly = true
		}
	}

	switch {
	case top1:
		partial = 1.0
	case categoryOnly:
		partial = 0.5
	default:
		partial = 0.0
	}
	return top1, top3, partial
}

// scoreEvidence implements FR-F11-6: precision/recall of the evidence
// categories an investigation actually surfaced (Steps[].EvidenceIDs ->
// Evidence.Category, resolved via evidenceByID) against
// Expect.ExpectedEvidence, a CLOSED enum. An empty ExpectedEvidence means
// "nothing specific expected" and scores 1.0/1.0 by convention (there is
// nothing to be imprecise or incomplete about); a non-empty expectation with
// zero evidence surfaced scores 0/0 (nothing precise, nothing recalled).
func scoreEvidence(steps []model.Step, evidence []model.Evidence, expected []model.EvidenceCategory) (precision, recall float64) {
	if len(expected) == 0 {
		return 1, 1
	}
	evByID := make(map[string]model.Evidence, len(evidence))
	for _, e := range evidence {
		evByID[e.ID] = e
	}

	gotSet := make(map[model.EvidenceCategory]bool)
	seen := make(map[string]bool)
	for _, st := range steps {
		for _, id := range st.EvidenceIDs {
			if seen[id] {
				continue
			}
			seen[id] = true
			if ev, ok := evByID[id]; ok {
				gotSet[ev.Category] = true
			}
		}
	}
	if len(gotSet) == 0 {
		return 0, 0
	}

	expectedSet := make(map[model.EvidenceCategory]bool, len(expected))
	for _, e := range expected {
		expectedSet[e] = true
	}

	truePositives := 0
	for c := range gotSet {
		if expectedSet[c] {
			truePositives++
		}
	}
	precision = float64(truePositives) / float64(len(gotSet))

	matched := 0
	for e := range expectedSet {
		if gotSet[e] {
			matched++
		}
	}
	recall = float64(matched) / float64(len(expectedSet))
	return precision, recall
}

// absoluteFloor is DR-36 §36.6's Top-1 accuracy floor table: 70% for the
// llm reasoner, 40% for rules.
func absoluteFloor(kind model.ReasonerKind) float64 {
	if kind == model.ReasonerLLM {
		return 0.70
	}
	return 0.40
}

// meanTimeToRCACeiling is DR-36 §36.6's absolute ceiling, independent of any
// reasoner kind.
const meanTimeToRCACeiling = 180 * time.Second

// medianCostCeilingMicroUSD / maxCostCeilingMicroUSD are FR-F11-11 / DR-36
// §36.6's cost-per-investigation absolute ceilings ($0.08 median, $0.50 max),
// expressed in the same micro-USD unit as Result.CostMicroUSD /
// Report.MedianCostMicroUSD / Report.MaxCostMicroUSD.
const (
	medianCostCeilingMicroUSD = 80_000
	maxCostCeilingMicroUSD    = 500_000
)

// regressionTop1DropPP / regressionTimeToRCAFactor are FR-F11-9's relative
// gate: a Top1Accuracy drop of more than 5 percentage points, or a
// MeanTimeToRCA increase of more than 25%, vs the stored baseline.
const (
	regressionTop1DropPP      = 0.05
	regressionTimeToRCAFactor = 1.25
)

// checkGates applies F11 §4.4's Run() gate algorithm to an already-built
// Report: absolute floors first (FR-F11-11, independent of any baseline),
// then the relative regression gate (FR-F11-9) when a baseline is supplied
// and FailOnRegression is set. It mutates rep.GatesPassed/FailedGates and
// returns the first-encountered sentinel error, or nil when every gate
// passes — callers that need "all failing gates" read rep.FailedGates
// rather than relying on the single returned error.
func checkGates(rep *Report, opts RunOptions, baseline *Report) error {
	rep.FailedGates = nil
	var firstErr error
	fail := func(name string, err error) {
		rep.FailedGates = append(rep.FailedGates, name)
		if firstErr == nil {
			firstErr = err
		}
	}

	floor := absoluteFloor(opts.Reasoner)
	if rep.Top1Accuracy < floor {
		fail("absolute_floor_top1_accuracy", ErrBelowAbsoluteFloor)
	}
	meanTimeToRCA := time.Duration(rep.MeanTimeToRCAMillis) * time.Millisecond
	if meanTimeToRCA > meanTimeToRCACeiling {
		fail("absolute_ceiling_mean_time_to_rca", ErrBelowAbsoluteFloor)
	}
	if rep.MedianCostMicroUSD > medianCostCeilingMicroUSD {
		fail("absolute_ceiling_median_cost", ErrBelowAbsoluteFloor)
	}
	if rep.MaxCostMicroUSD > maxCostCeilingMicroUSD {
		fail("absolute_ceiling_max_cost", ErrBelowAbsoluteFloor)
	}

	if baseline != nil && opts.FailOnRegression {
		// epsilon absorbs float64 arithmetic noise (e.g. 0.90-0.85 landing on
		// 0.050000000000000044) so an exact boundary value ("exactly 5pp",
		// "exactly +25%") reliably passes, per AC-F11-4's boundary wording.
		const epsilon = 1e-9

		if rep.RegressionDiff == nil {
			rep.RegressionDiff = map[string]float64{}
		}
		top1Drop := baseline.Top1Accuracy - rep.Top1Accuracy
		rep.RegressionDiff["top1_accuracy_drop_pp"] = top1Drop
		if top1Drop > regressionTop1DropPP+epsilon {
			fail("regression_top1_accuracy", ErrRegressionTop1Accuracy)
		}

		if baseline.MeanTimeToRCAMillis > 0 {
			ratio := float64(rep.MeanTimeToRCAMillis) / float64(baseline.MeanTimeToRCAMillis)
			rep.RegressionDiff["mean_time_to_rca_ratio"] = ratio
			if ratio > regressionTimeToRCAFactor+epsilon {
				fail("regression_time_to_rca", ErrRegressionTimeToRCA)
			}
		}
	}

	rep.GatesPassed = len(rep.FailedGates) == 0
	return firstErr
}
