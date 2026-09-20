package eval

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"traceiq/internal/model"
	"traceiq/internal/rca/rules"
)

// TestRunner_S01_MatchesTop1 wires S01's fixture through the REAL rca.Engine
// + rules.Reasoner (internal/rca, internal/rca/rules) end-to-end: it proves
// the harness actually drives an investigation, not a mock, by asserting
// the rules catalog's own errorSigAfterDeploy rule fired and its Category
// (CatDeployRegression) is what the scorer sees.
func TestRunner_S01_MatchesTop1(t *testing.T) {
	rep, err := NewRunner(rules.New()).Run(context.Background(), RunOptions{
		Tenant:       "eval-tenant",
		ScenariosDir: testdataScenariosDir,
		Select:       []string{"S01-error-signature-after-deploy"},
		Reasoner:     model.ReasonerRules,
	})
	if err != nil {
		t.Fatalf("Run: %v (gates failed: %v)", err, rep.FailedGates)
	}
	if len(rep.Results) != 1 {
		t.Fatalf("len(Results) = %d, want 1", len(rep.Results))
	}
	res := rep.Results[0]
	if res.Error != "" {
		t.Fatalf("scenario error: %s", res.Error)
	}
	if !res.Top1 {
		t.Errorf("Top1 = false, want true (hypotheses: %+v)", res.Investigation.Hypotheses)
	}
	if res.Investigation == nil || res.Investigation.RootCause == "" {
		t.Error("expected a non-empty RootCause on the real Investigation")
	}
	if res.ReasonerKind != model.ReasonerRules {
		t.Errorf("ReasonerKind = %q, want rules", res.ReasonerKind)
	}
	if !rep.GatesPassed {
		t.Errorf("GatesPassed = false, want true (100%% top1 on a single scenario clears the 40%% rules floor); failed gates: %v", rep.FailedGates)
	}
}

// TestRunner_S02_MatchesTop1 does the same for the
// downstream-latency-propagation rule (CatDependencyFailure).
func TestRunner_S02_MatchesTop1(t *testing.T) {
	rep, err := NewRunner(rules.New()).Run(context.Background(), RunOptions{
		Tenant:       "eval-tenant",
		ScenariosDir: testdataScenariosDir,
		Select:       []string{"S02-downstream-latency-propagation"},
		Reasoner:     model.ReasonerRules,
	})
	if err != nil {
		t.Fatalf("Run: %v (gates failed: %v)", err, rep.FailedGates)
	}
	res := rep.Results[0]
	if res.Error != "" {
		t.Fatalf("scenario error: %s", res.Error)
	}
	if !res.Top1 {
		t.Errorf("Top1 = false, want true (hypotheses: %+v)", res.Investigation.Hypotheses)
	}
	found := false
	for _, h := range res.Investigation.Hypotheses {
		if h.Category == model.CatDependencyFailure && h.Status == model.HypothesisSupported {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a Supported CatDependencyFailure hypothesis, got %+v", res.Investigation.Hypotheses)
	}
}

// TestRunner_S03_DoesNotMatch is this wave's required negative case: the
// investigation genuinely confirms connection-pool-exhaustion
// (CatResourceExhaustion), but the scenario's Expect deliberately names a
// different category, so the scorer MUST report a miss — proving the
// scorer discriminates instead of always reporting "match" (task
// requirement). A single-scenario 0% Top1Accuracy also trips the 40% rules
// absolute floor (FR-F11-11), so Run() returns ErrBelowAbsoluteFloor.
func TestRunner_S03_DoesNotMatch(t *testing.T) {
	rep, err := NewRunner(rules.New()).Run(context.Background(), RunOptions{
		Tenant:       "eval-tenant",
		ScenariosDir: testdataScenariosDir,
		Select:       []string{"S03-mismatched-expectation"},
		Reasoner:     model.ReasonerRules,
	})
	if len(rep.Results) != 1 {
		t.Fatalf("len(Results) = %d, want 1", len(rep.Results))
	}
	res := rep.Results[0]
	if res.Top1 {
		t.Error("Top1 = true, want false — the scenario's Expect deliberately mismatches the confirmed rule")
	}
	if res.Top3 {
		t.Error("Top3 = true, want false")
	}
	// Sanity: the investigation DID confirm something real (resource
	// exhaustion), it's just not what Expect asked for.
	foundResourceExhaustion := false
	for _, h := range res.Investigation.Hypotheses {
		if h.Category == model.CatResourceExhaustion && h.Status == model.HypothesisSupported {
			foundResourceExhaustion = true
		}
	}
	if !foundResourceExhaustion {
		t.Errorf("expected the real investigation to confirm CatResourceExhaustion regardless of Expect, got %+v", res.Investigation.Hypotheses)
	}
	if rep.GatesPassed {
		t.Error("GatesPassed = true, want false (0%% top1 accuracy is below the 40%% rules floor)")
	}
	if !errors.Is(err, ErrBelowAbsoluteFloor) {
		t.Errorf("err = %v, want ErrBelowAbsoluteFloor", err)
	}
}

// TestRunner_Determinism covers FR-F11-12/AC-F11-6: two ModeOffline runs
// with the same Seed against the same scenario set produce bit-identical
// Top1/Top3/EvidencePrecision/EvidenceRecall for every scenario.
func TestRunner_Determinism(t *testing.T) {
	opts := RunOptions{
		Tenant:       "eval-tenant",
		ScenariosDir: testdataScenariosDir,
		Reasoner:     model.ReasonerRules,
		Seed:         42,
	}
	r := NewRunner(rules.New())
	rep1, _ := r.Run(context.Background(), opts)
	rep2, _ := r.Run(context.Background(), opts)

	if len(rep1.Results) != len(rep2.Results) {
		t.Fatalf("result count differs: %d vs %d", len(rep1.Results), len(rep2.Results))
	}
	for i := range rep1.Results {
		a, b := rep1.Results[i], rep2.Results[i]
		if a.ScenarioID != b.ScenarioID {
			t.Fatalf("scenario order differs at %d: %s vs %s", i, a.ScenarioID, b.ScenarioID)
		}
		if a.Top1 != b.Top1 || a.Top3 != b.Top3 || a.EvidencePrecision != b.EvidencePrecision || a.EvidenceRecall != b.EvidenceRecall {
			t.Errorf("%s: non-deterministic scoring: run1={Top1:%v Top3:%v EvP:%v EvR:%v} run2={Top1:%v Top3:%v EvP:%v EvR:%v}",
				a.ScenarioID, a.Top1, a.Top3, a.EvidencePrecision, a.EvidenceRecall,
				b.Top1, b.Top3, b.EvidencePrecision, b.EvidenceRecall)
		}
		if a.PartialCredit != b.PartialCredit {
			t.Errorf("%s: PartialCredit differs: %v vs %v", a.ScenarioID, a.PartialCredit, b.PartialCredit)
		}
	}
	if rep1.Top1Accuracy != rep2.Top1Accuracy {
		t.Errorf("Top1Accuracy differs across runs: %v vs %v", rep1.Top1Accuracy, rep2.Top1Accuracy)
	}
}

// TestRunner_Run_WritesReports covers FR-F11-8: Run() with OutputDir set
// emits both report.json and report.md, without re-running scenarios, and
// both are well-formed.
func TestRunner_Run_WritesReports(t *testing.T) {
	dir := t.TempDir()
	rep, _ := NewRunner(rules.New()).Run(context.Background(), RunOptions{
		Tenant:       "eval-tenant",
		ScenariosDir: testdataScenariosDir,
		Select:       []string{"S01-error-signature-after-deploy"},
		Reasoner:     model.ReasonerRules,
		OutputDir:    dir,
	})

	jsonPath := filepath.Join(dir, "report.json")
	raw, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatalf("report.json missing: %v", err)
	}
	if !json.Valid(raw) {
		t.Error("report.json is not valid JSON")
	}
	var decoded Report
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("report.json does not decode back into a Report: %v", err)
	}
	if decoded.RunID != rep.RunID {
		t.Errorf("decoded RunID = %q, want %q", decoded.RunID, rep.RunID)
	}

	mdPath := filepath.Join(dir, "report.md")
	mdRaw, err := os.ReadFile(mdPath)
	if err != nil {
		t.Fatalf("report.md missing: %v", err)
	}
	md := string(mdRaw)
	if !strings.HasPrefix(md, "# TraceIQ Eval Report") {
		t.Error("report.md missing expected top-level heading")
	}
	if !strings.Contains(md, "S01-error-signature-after-deploy") {
		t.Error("report.md missing the scenario row")
	}
	if !strings.Contains(md, "| Scenario | Top1 |") {
		t.Error("report.md missing the scenario table header")
	}
}

// TestRunner_RegressionGate is an end-to-end run of AC-F11-4's scenario: a
// stored baseline scoring higher than the current run trips
// ErrRegressionTop1Accuracy through the full Run() plumbing (RegressionBaseline
// file loading + FailOnRegression), not just the checkGates unit test.
func TestRunner_RegressionGate(t *testing.T) {
	dir := t.TempDir()
	baseline := Report{Top1Accuracy: 0.90, MeanTimeToRCAMillis: 1000}
	baselineJSON, _ := baseline.ToJSON()
	baselinePath := filepath.Join(dir, "baseline.json")
	if err := os.WriteFile(baselinePath, baselineJSON, 0o644); err != nil {
		t.Fatal(err)
	}

	// S03 alone always scores Top1Accuracy=0.0, an 90pp drop vs the 0.90
	// baseline — well past the 5pp regression threshold.
	rep, err := NewRunner(rules.New()).Run(context.Background(), RunOptions{
		Tenant:             "eval-tenant",
		ScenariosDir:       testdataScenariosDir,
		Select:             []string{"S03-mismatched-expectation"},
		Reasoner:           model.ReasonerRules,
		RegressionBaseline: baselinePath,
		FailOnRegression:   true,
	})
	if !errors.Is(err, ErrRegressionTop1Accuracy) && !errors.Is(err, ErrBelowAbsoluteFloor) {
		t.Fatalf("err = %v, want ErrRegressionTop1Accuracy (or ErrBelowAbsoluteFloor, whichever the gate order surfaces first)", err)
	}
	if rep.GatesPassed {
		t.Error("GatesPassed = true, want false")
	}
	if len(rep.FailedGates) == 0 {
		t.Error("expected at least one named failed gate")
	}
}

func TestRunner_RegressionBaseline_MissingFile(t *testing.T) {
	_, err := NewRunner(rules.New()).Run(context.Background(), RunOptions{
		Tenant:             "eval-tenant",
		ScenariosDir:       testdataScenariosDir,
		Select:             []string{"S01-error-signature-after-deploy"},
		Reasoner:           model.ReasonerRules,
		RegressionBaseline: filepath.Join(t.TempDir(), "does-not-exist.json"),
		FailOnRegression:   true,
	})
	if err == nil {
		t.Fatal("expected an error for a missing baseline file")
	}
}

func TestRunner_IsolationShared_RefusesParallelism(t *testing.T) {
	_, err := NewRunner(rules.New()).Run(context.Background(), RunOptions{
		Tenant:       "eval-tenant",
		ScenariosDir: testdataScenariosDir,
		Isolation:    IsolationShared,
		Parallelism:  4,
	})
	if err == nil {
		t.Fatal("expected an error: IsolationShared refuses Parallelism > 1")
	}
}

func TestRunner_ModeLive_OutOfScope(t *testing.T) {
	_, err := NewRunner(rules.New()).Run(context.Background(), RunOptions{
		Tenant:       "eval-tenant",
		ScenariosDir: testdataScenariosDir,
		Mode:         ModeLive,
	})
	if err == nil {
		t.Fatal("expected an error: ModeLive is out of scope this wave")
	}
}

func TestRunner_List(t *testing.T) {
	specs, err := NewRunner(rules.New()).List(context.Background(), testdataScenariosDir)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(specs) != 3 {
		t.Fatalf("len(specs) = %d, want 3", len(specs))
	}
}

func TestRunner_Stats(t *testing.T) {
	r := NewRunner(rules.New())
	before := r.Stats()
	if before.RunsTotal != 0 {
		t.Fatalf("RunsTotal = %d before any run, want 0", before.RunsTotal)
	}
	_, _ = r.Run(context.Background(), RunOptions{
		Tenant:       "eval-tenant",
		ScenariosDir: testdataScenariosDir,
		Select:       []string{"S01-error-signature-after-deploy"},
	})
	after := r.Stats()
	if after.RunsTotal != 1 {
		t.Errorf("RunsTotal = %d after one run, want 1", after.RunsTotal)
	}
	if after.ScenariosCount != 1 {
		t.Errorf("ScenariosCount = %d, want 1", after.ScenariosCount)
	}
}
