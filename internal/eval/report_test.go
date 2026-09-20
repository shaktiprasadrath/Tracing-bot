package eval

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"traceiq/internal/model"
)

func sampleReport() Report {
	return buildReport("run-test", time.Now(), time.Now(), RunOptions{Reasoner: model.ReasonerRules}, []Result{
		{ScenarioID: "S01", Top1: true, Top3: true, PartialCredit: 1, TimeToRCAMillis: 120, EvidencePrecision: 1, EvidenceRecall: 1, ReasonerKind: model.ReasonerRules},
		{ScenarioID: "S02", Top1: false, Top3: false, PartialCredit: 0, TimeToRCAMillis: 340, ReasonerKind: model.ReasonerRules, Error: "no incident candidate"},
	})
}

func TestBuildReport_Summary(t *testing.T) {
	rep := sampleReport()
	if rep.Total != 2 {
		t.Errorf("Total = %d, want 2", rep.Total)
	}
	if rep.Passed != 1 {
		t.Errorf("Passed = %d, want 1", rep.Passed)
	}
	if rep.Top1Accuracy != 0.5 {
		t.Errorf("Top1Accuracy = %v, want 0.5", rep.Top1Accuracy)
	}
	if rep.Top3Accuracy != 0.5 {
		t.Errorf("Top3Accuracy = %v, want 0.5", rep.Top3Accuracy)
	}
	if rep.MeanTimeToRCAMillis != 230 {
		t.Errorf("MeanTimeToRCAMillis = %d, want 230", rep.MeanTimeToRCAMillis)
	}
}

func TestReport_ToJSON_Valid(t *testing.T) {
	rep := sampleReport()
	raw, err := rep.ToJSON()
	if err != nil {
		t.Fatalf("ToJSON: %v", err)
	}
	if !json.Valid(raw) {
		t.Fatal("ToJSON produced invalid JSON")
	}
	var decoded Report
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("round-trip unmarshal: %v", err)
	}
	if decoded.RunID != rep.RunID || decoded.Top1Accuracy != rep.Top1Accuracy || len(decoded.Results) != len(rep.Results) {
		t.Errorf("round-tripped report mismatch: %+v vs %+v", decoded, rep)
	}
}

func TestReport_ToMarkdown_WellFormed(t *testing.T) {
	rep := sampleReport()
	md := rep.ToMarkdown()
	if !strings.HasPrefix(md, "# TraceIQ Eval Report") {
		t.Error("missing top-level heading")
	}
	if !strings.Contains(md, "## Summary") || !strings.Contains(md, "## Scenarios") {
		t.Error("missing expected section headings")
	}
	if strings.Count(md, "\n| S0") != 2 {
		t.Errorf("expected exactly 2 scenario table rows, markdown:\n%s", md)
	}
	if !strings.Contains(md, "no incident candidate") {
		t.Error("expected the failing scenario's error message to appear in the report")
	}
}

// TestBuildReport_CostAggregation covers FR-F11-11's cost-gate inputs:
// MedianCostMicroUSD/MaxCostMicroUSD computed over Results[].CostMicroUSD.
func TestBuildReport_CostAggregation(t *testing.T) {
	rep := buildReport("run-cost", time.Now(), time.Now(), RunOptions{Reasoner: model.ReasonerRules}, []Result{
		{ScenarioID: "S01", CostMicroUSD: 10_000},
		{ScenarioID: "S02", CostMicroUSD: 90_000},
		{ScenarioID: "S03", CostMicroUSD: 50_000},
	})
	if rep.MedianCostMicroUSD != 50_000 {
		t.Errorf("MedianCostMicroUSD = %d, want 50000", rep.MedianCostMicroUSD)
	}
	if rep.MaxCostMicroUSD != 90_000 {
		t.Errorf("MaxCostMicroUSD = %d, want 90000", rep.MaxCostMicroUSD)
	}
}

// TestBuildReport_EvidenceScenariosCount covers the transparency fix for the
// empty-expected-evidence risk: scoreEvidence's vacuous 1.0/1.0 convention
// (empty Expect.ExpectedEvidence) must be distinguishable, at the Report
// level, from a genuinely measured evidence precision/recall — otherwise a
// reader of report.md/report.json has no way to tell "evidence scoring
// passed" from "evidence scoring was never exercised."
func TestBuildReport_EvidenceScenariosCount(t *testing.T) {
	t.Run("all vacuous (no scenario had expected_evidence)", func(t *testing.T) {
		rep := buildReport("run-ev", time.Now(), time.Now(), RunOptions{Reasoner: model.ReasonerRules}, []Result{
			{ScenarioID: "S01", EvidencePrecision: 1, EvidenceRecall: 1, EvidenceExpected: false},
			{ScenarioID: "S02", EvidencePrecision: 1, EvidenceRecall: 1, EvidenceExpected: false},
		})
		if rep.EvidenceScenariosCount != 0 {
			t.Errorf("EvidenceScenariosCount = %d, want 0", rep.EvidenceScenariosCount)
		}
		md := rep.ToMarkdown()
		if !strings.Contains(md, "vacuous 1.00/1.00 convention") {
			t.Errorf("expected the vacuous-evidence note in report.md, got:\n%s", md)
		}
	})

	t.Run("mixed: some scenarios genuinely exercised evidence scoring", func(t *testing.T) {
		rep := buildReport("run-ev2", time.Now(), time.Now(), RunOptions{Reasoner: model.ReasonerRules}, []Result{
			{ScenarioID: "S01", EvidencePrecision: 1, EvidenceRecall: 1, EvidenceExpected: false},
			{ScenarioID: "S02", EvidencePrecision: 0.5, EvidenceRecall: 0.5, EvidenceExpected: true},
		})
		if rep.EvidenceScenariosCount != 1 {
			t.Errorf("EvidenceScenariosCount = %d, want 1", rep.EvidenceScenariosCount)
		}
		md := rep.ToMarkdown()
		if strings.Contains(md, "vacuous 1.00/1.00 convention") {
			t.Errorf("did not expect the vacuous-evidence note when EvidenceScenariosCount > 0, got:\n%s", md)
		}
		if !strings.Contains(md, "reflect 1 of 2 scenarios") {
			t.Errorf("expected the partial-coverage note in report.md, got:\n%s", md)
		}
	})
}

func TestWriteReport_And_LoadBaseline_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	rep := sampleReport()
	if err := writeReport(dir, rep); err != nil {
		t.Fatalf("writeReport: %v", err)
	}
	loaded, err := loadBaselineReport(dir + "/report.json")
	if err != nil {
		t.Fatalf("loadBaselineReport: %v", err)
	}
	if loaded.Top1Accuracy != rep.Top1Accuracy {
		t.Errorf("loaded.Top1Accuracy = %v, want %v", loaded.Top1Accuracy, rep.Top1Accuracy)
	}
}

func TestLoadBaselineReport_EmptyPathIsNil(t *testing.T) {
	rep, err := loadBaselineReport("")
	if err != nil || rep != nil {
		t.Errorf("loadBaselineReport(\"\") = %+v, %v, want nil, nil", rep, err)
	}
}
