package eval

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// buildReport implements FR-F11-8's Report aggregate: per-scenario Results
// plus the summary accuracy/timing/evidence fields the JSON/Markdown
// renderers and checkGates both read.
func buildReport(runID string, started, finished time.Time, opts RunOptions, results []Result) Report {
	rep := Report{
		RunID:      runID,
		StartedAt:  started,
		FinishedAt: finished,
		Mode:       opts.Mode,
		Reasoner:   opts.Reasoner,
		Seed:       opts.Seed,
		Results:    results,
		Total:      len(results),
	}

	var top1Count, top3Count int
	var timeSum, evPrecisionSum, evRecallSum float64
	costs := make([]int64, 0, len(results))
	for _, r := range results {
		if r.Top1 {
			top1Count++
			rep.Passed++
		}
		if r.Top3 {
			top3Count++
		}
		if r.EvidenceExpected {
			rep.EvidenceScenariosCount++
		}
		timeSum += float64(r.TimeToRCAMillis)
		evPrecisionSum += r.EvidencePrecision
		evRecallSum += r.EvidenceRecall
		costs = append(costs, r.CostMicroUSD)
	}
	if rep.Total > 0 {
		rep.Top1Accuracy = float64(top1Count) / float64(rep.Total)
		rep.Top3Accuracy = float64(top3Count) / float64(rep.Total)
		rep.MeanTimeToRCAMillis = int64(timeSum / float64(rep.Total))
		rep.MeanEvidencePrecision = evPrecisionSum / float64(rep.Total)
		rep.MeanEvidenceRecall = evRecallSum / float64(rep.Total)
		rep.MedianCostMicroUSD, rep.MaxCostMicroUSD = medianAndMax(costs)
	}
	return rep
}

// medianAndMax computes FR-F11-11 / DR-36 §36.6's cost-gate inputs over a
// run's per-investigation CostMicroUSD values. The input slice is copied
// before sorting so callers keep result order untouched.
func medianAndMax(costs []int64) (median, max int64) {
	if len(costs) == 0 {
		return 0, 0
	}
	sorted := make([]int64, len(costs))
	copy(sorted, costs)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })

	n := len(sorted)
	if n%2 == 1 {
		median = sorted[n/2]
	} else {
		median = (sorted[n/2-1] + sorted[n/2]) / 2
	}
	max = sorted[n-1]
	return median, max
}

// ToJSON renders report.json (FR-F11-8): machine-consumable, and itself the
// format loadBaselineReport reads back in for the regression gate.
func (r Report) ToJSON() ([]byte, error) {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("eval: marshal report json: %w", err)
	}
	return b, nil
}

// ToMarkdown renders report.md (FR-F11-8): a human-readable summary table,
// generated from the same Report the JSON came from — never re-run.
func (r Report) ToMarkdown() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# TraceIQ Eval Report\n\n")
	fmt.Fprintf(&b, "- Run ID: `%s`\n", r.RunID)
	fmt.Fprintf(&b, "- Mode: %d, Reasoner: %s, Seed: %d\n", r.Mode, r.Reasoner, r.Seed)
	fmt.Fprintf(&b, "- Started: %s, Finished: %s\n", r.StartedAt.Format(time.RFC3339), r.FinishedAt.Format(time.RFC3339))
	fmt.Fprintf(&b, "- Gates passed: **%v**\n", r.GatesPassed)
	if len(r.FailedGates) > 0 {
		fmt.Fprintf(&b, "- Failed gates: %s\n", strings.Join(r.FailedGates, ", "))
	}
	fmt.Fprintf(&b, "\n## Summary\n\n")
	fmt.Fprintf(&b, "| Metric | Value |\n|---|---|\n")
	fmt.Fprintf(&b, "| Total scenarios | %d |\n", r.Total)
	fmt.Fprintf(&b, "| Passed (Top-1) | %d |\n", r.Passed)
	fmt.Fprintf(&b, "| Top-1 accuracy | %.1f%% |\n", r.Top1Accuracy*100)
	fmt.Fprintf(&b, "| Top-3 accuracy | %.1f%% |\n", r.Top3Accuracy*100)
	fmt.Fprintf(&b, "| Mean time-to-RCA | %d ms |\n", r.MeanTimeToRCAMillis)
	fmt.Fprintf(&b, "| Mean evidence precision | %.2f |\n", r.MeanEvidencePrecision)
	fmt.Fprintf(&b, "| Mean evidence recall | %.2f |\n", r.MeanEvidenceRecall)
	fmt.Fprintf(&b, "| Median cost/investigation (uUSD) | %d |\n", r.MedianCostMicroUSD)
	fmt.Fprintf(&b, "| Max cost/investigation (uUSD) | %d |\n", r.MaxCostMicroUSD)
	if r.EvidenceScenariosCount == 0 {
		fmt.Fprintf(&b, "\n> **Note:** 0 of %d scenarios had a non-empty `expected_evidence`, so the evidence "+
			"precision/recall above are the vacuous 1.00/1.00 convention (nothing was expected, so nothing "+
			"is imprecise or incomplete) — they do not reflect a measured evidence quality.\n", r.Total)
	} else {
		fmt.Fprintf(&b, "\n_Evidence precision/recall reflect %d of %d scenarios with a non-empty `expected_evidence`._\n",
			r.EvidenceScenariosCount, r.Total)
	}

	fmt.Fprintf(&b, "\n## Scenarios\n\n")
	fmt.Fprintf(&b, "| Scenario | Top1 | Top3 | Partial | TimeToRCA(ms) | EvP | EvR | Cost(uUSD) | Reasoner | Errors |\n")
	fmt.Fprintf(&b, "|---|---|---|---|---|---|---|---|---|---|\n")
	for _, res := range r.Results {
		fmt.Fprintf(&b, "| %s | %v | %v | %.2f | %d | %.2f | %.2f | %d | %s | %s |\n",
			res.ScenarioID, res.Top1, res.Top3, res.PartialCredit, res.TimeToRCAMillis,
			res.EvidencePrecision, res.EvidenceRecall, res.CostMicroUSD, res.ReasonerKind, res.Error)
	}
	return b.String()
}

// writeReport writes report.json and report.md under dir (FR-F11-8).
func writeReport(dir string, rep Report) error {
	if dir == "" {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("eval: create output dir %s: %w", dir, err)
	}
	jsonBytes, err := rep.ToJSON()
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "report.json"), jsonBytes, 0o644); err != nil {
		return fmt.Errorf("eval: write report.json: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "report.md"), []byte(rep.ToMarkdown()), 0o644); err != nil {
		return fmt.Errorf("eval: write report.md: %w", err)
	}
	return nil
}

// loadBaselineReport reads back a Report previously written by ToJSON, for
// RunOptions.RegressionBaseline (FR-F11-9).
func loadBaselineReport(path string) (*Report, error) {
	if path == "" {
		return nil, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("eval: read regression baseline %s: %w", path, err)
	}
	var rep Report
	if err := json.Unmarshal(raw, &rep); err != nil {
		return nil, fmt.Errorf("eval: parse regression baseline %s: %w", path, err)
	}
	return &rep, nil
}
